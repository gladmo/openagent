package codingagent

// Port of modes/interactive/interactive-mode.ts (driver core): the
// lifecycle (options → TUI construction → run → shutdown), the editor
// submit dispatch order (quit/resume → bash !/!! with running-guard →
// compaction queueing → streaming steer → session prompt), slash-command
// routing over the built-in catalog, and status/warning/error display.
// Selector-grade commands (model/tree/settings...) land on the selector
// seam; their TUI widgets arrive with the components phase.

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/tui"
)

// InteractiveModeOptions configure the driver.
type InteractiveModeOptions struct {
	Session            *AgentSession
	TuiMode            string // "regular" | "fullscreen"
	ShowHardwareCursor bool
	LogDirectory       string
	Version            string
	// Verbose forces the expanded startup header.
	Verbose bool
	// StartupDiagnostics render before the first prompt.
	StartupDiagnostics []CliDiagnostic
	// ModelFallbackMessage warns when the session model could not be
	// restored.
	ModelFallbackMessage string
	// Terminal overrides the process terminal (tests).
	Terminal tui.Terminal
}

// SlashCommandHandler runs one command (nil args = no arguments).
type SlashCommandHandler func(mode *InteractiveMode, args string)

// slashCommandHandlers routes the catalog; selector-grade commands ride
// the seam until their widgets land.
var slashCommandHandlers = map[string]SlashCommandHandler{
	"quit": func(mode *InteractiveMode, args string) {
		mode.requestShutdown()
	},
	"new":      func(mode *InteractiveMode, args string) { mode.handleNewCommand() },
	"thinking": func(mode *InteractiveMode, args string) { mode.handleThinkingCommand(args) },
	"model":    func(mode *InteractiveMode, args string) { mode.handleModelCommand(args) },
	"export":   func(mode *InteractiveMode, args string) { mode.handleExportCommand(args) },
	"session": func(mode *InteractiveMode, args string) {
		mode.showSessionInfo()
	},
	"compact": func(mode *InteractiveMode, args string) {
		mode.handleCompactCommand(args)
	},
	"name": func(mode *InteractiveMode, args string) {
		mode.handleNameCommand(args)
	},
}

// InteractiveMode drives the interactive TUI session.
type InteractiveMode struct {
	options InteractiveModeOptions
	session *AgentSession

	tui    tui.TUI
	editor *tui.Editor

	transcript  *InteractiveTranscript
	footer      *Footer
	footerStack *tui.VStack
	unsubscribe func()
	// disposeKeyHandlers unregisters the app-level input listener.
	disposeKeyHandlers func()

	// Component tree mirrors the reference: a document container (header,
	// loaded resources, chat) that scrolls in fullscreen, plus the fixed
	// status/editor/footer dock.
	documentStack    *tui.VStack
	headerStack      *tui.VStack
	header           *ExpandableText
	resourcesStack   *tui.VStack
	chatStack        *tui.VStack
	statusSlot       *statusLineComponent
	statusStack      *tui.VStack
	editorStack      *tui.VStack
	transcriptScroll *tui.ScrollView

	keybindings *tui.KeybindingsManager

	mu                 sync.Mutex
	runActive          bool
	workingIndicator   *WorkingStatusIndicator
	toolOutputExpanded bool
	lastEscapeAt       time.Time
	lastClearAt        time.Time
	shutdownPending    bool
	shutdownDone       chan struct{}
	statusText         string
	bashRunning        bool

	// StatusSink receives status/warning/error lines (tests assert here;
	// the TUI renders them into statusStack).
	StatusSink func(line string)
}

// NewInteractiveMode wires the TUI, editor, and dispatch.
func NewInteractiveMode(options InteractiveModeOptions) *InteractiveMode {
	mode := &InteractiveMode{
		options:      options,
		session:      options.Session,
		shutdownDone: make(chan struct{}),
	}

	terminal := options.Terminal
	if terminal == nil {
		terminal = tui.NewProcessTerminal()
	}

	var screen tui.TUI
	if options.TuiMode == "fullscreen" {
		screen = tui.NewTuiAltScreen(terminal, nil, options.LogDirectory, tui.TuiAltScreenOptions{})
	} else {
		showCursor := options.ShowHardwareCursor
		screen = tui.NewTuiMainScreen(terminal, &showCursor, options.LogDirectory)
	}
	mode.tui = screen

	mode.keybindings = tui.NewKeybindingsManager(AppKeybindings(), LoadUserKeybindings(""))

	// Component tree per the reference: header + loaded resources + chat in
	// the document container; status + editor + footer in the fixed dock.
	mode.headerStack = tui.NewVStack(0)
	mode.resourcesStack = tui.NewVStack(0)
	mode.chatStack = tui.NewVStack(0)
	mode.documentStack = tui.NewVStack(0)
	mode.documentStack.AddChild(mode.headerStack, nil)
	mode.documentStack.AddChild(mode.resourcesStack, nil)
	mode.documentStack.AddChild(mode.chatStack, nil)

	// Startup header: expanded when verbose or the tool output is expanded,
	// silenced by the quietStartup setting (interactive-mode.ts).
	quietStartup := false
	if options.Session != nil && options.Session.SettingsManager != nil {
		quietStartup = options.Session.SettingsManager.GetQuietStartup()
	}
	if options.Verbose || !quietStartup {
		compactHints, expandedHints := mode.startupHintLines()
		mode.header = NewStartupHeader(options.Version, options.Verbose, false, compactHints, expandedHints)
		mode.headerStack.AddChild(tui.NewSpacer(1), nil)
		mode.headerStack.AddChild(mode.header, nil)
		mode.headerStack.AddChild(tui.NewSpacer(1), nil)
	} else {
		mode.header = NewStartupHeader(options.Version, false, true, "", "")
		mode.headerStack.AddChild(mode.header, nil)
	}

	mode.statusSlot = &statusLineComponent{}
	mode.statusStack = tui.NewVStack(0)
	mode.statusStack.AddChild(mode.statusSlot, nil)

	one, three := 1, 3
	mode.editorStack = tui.NewVStack(0)
	editor := tui.NewEditor(screen, tui.EditorTheme{
		BorderColor: func(str string) string { return str },
	}, tui.EditorOptions{TopBorder: mode.renderEditorTopBorder})
	editor.OnSubmit = mode.handleEditorSubmit
	mode.editor = editor
	// The editor renders its own borders; the reference mounts it (as the
	// CustomEditor) into the editor container with a three-line minimum.
	mode.editorStack.AddChild(editor, &tui.StackEntryOptions{Shrink: &one, MinSize: &three})

	mode.transcript = NewInteractiveTranscript()
	mode.chatStack.AddChild(mode.transcript, nil)

	mode.footer = NewFooter(options.Session, &sessionFooterData{session: options.Session})
	mode.footerStack = tui.NewVStack(0)
	mode.footerStack.AddChild(mode.footer, nil)

	// One component tree, mounted per renderer (chat-viewport.ts +
	// mountInteractiveTui): the main screen renders the dock children
	// directly, the alt screen renders through the fullscreen root.
	viewport := CreateChatViewport(ChatViewportOptions{
		Document:  mode.documentStack,
		Status:    mode.statusStack,
		Editor:    mode.editorStack,
		Footer:    mode.footerStack,
		Scrollbar: tui.ScrollbarAuto,
	})
	mode.transcriptScroll = viewport.Transcript
	mountInteractiveTUI(screen, []tui.Component{
		mode.documentStack,
		mode.statusStack,
		mode.editorStack,
		mode.footerStack,
	}, viewport.Root)
	// App-level keys run before the focused editor sees the key
	// (setupKeyHandlers); the disposer is released in Run.
	mode.disposeKeyHandlers = mode.setupKeyHandlers()
	screen.SetFocus(editor)
	return mode
}

// mountInteractiveTUI ports mountInteractiveTui: every container becomes a
// screen child, and a viewport renderer (the alt screen) additionally
// renders through the fullscreen layout root.
func mountInteractiveTUI(screen tui.TUI, components []tui.Component, root tui.Component) {
	for _, component := range components {
		screen.AddChild(component)
	}
	if viewport, ok := screen.(tui.ViewportTUI); ok && root != nil {
		viewport.SetLayoutRoot(root)
	}
}

// Run initializes and drives the TUI until shutdown.
func (m *InteractiveMode) Run() error {
	m.showStartupDiagnostics()
	m.showStartupNotices()
	m.subscribeAgentEvents()

	m.tui.Start()
	defer func() {
		if m.unsubscribe != nil {
			m.unsubscribe()
			m.unsubscribe = nil
		}
		if m.disposeKeyHandlers != nil {
			m.disposeKeyHandlers()
			m.disposeKeyHandlers = nil
		}
		m.tui.Stop(&tui.TuiStopOptions{})
	}()

	select {
	case <-m.shutdownDone:
	}
	return nil
}

// subscribeAgentEvents bridges agent lifecycle events into the
// transcript; the disposer runs on shutdown (registrations are effects).
func (m *InteractiveMode) subscribeAgentEvents() {
	if m.session == nil || m.session.Agent == nil {
		return
	}
	m.unsubscribe = m.session.Agent.Subscribe(func(event agent.AgentEvent, signal *abort.Signal) {
		m.transcript.HandleEvent(event, func(toolName string) ToolRenderers {
			return m.toolRenderersFor(toolName)
		})
		m.tui.RequestRender(false)
	})
}

// toolRenderersFor exposes a registered tool's render hooks to the
// transcript (nil when the tool has none).
func (m *InteractiveMode) toolRenderersFor(toolName string) ToolRenderers {
	definition := m.session.GetToolDefinitionRef(toolName)
	if definition == nil || (definition.RenderCall == nil && definition.RenderResult == nil) {
		return nil
	}
	return &toolRenderersAdapter{definition: definition}
}

// Transcript exposes the live transcript (tests).
func (m *InteractiveMode) Transcript() *InteractiveTranscript { return m.transcript }

// TUI exposes the renderer (tests assert the mount protocol and fullscreen
// scroll state).
func (m *InteractiveMode) TUI() tui.TUI { return m.tui }

// showStartupDiagnostics renders options.StartupDiagnostics by severity.
func (m *InteractiveMode) showStartupDiagnostics() {
	for _, diagnostic := range m.options.StartupDiagnostics {
		switch diagnostic.Type {
		case "error":
			m.showError(diagnostic.Message)
		case "warning":
			m.showWarning(diagnostic.Message)
		default:
			m.showStatus(diagnostic.Message)
		}
	}
}

// showStartupNotices renders the model fallback and unnotified crash
// notices.
func (m *InteractiveMode) showStartupNotices() {
	if m.options.ModelFallbackMessage != "" {
		m.showWarning(m.options.ModelFallbackMessage)
	}
	if crash := TakeUnnotifiedCrash("", time.Now().UnixMilli()); crash != nil {
		parsed := ""
		if when, err := time.Parse(time.RFC3339, crash.Timestamp); err == nil {
			parsed = when.Local().Format("2006-01-02 15:04:05")
		}
		m.showWarning(fmt.Sprintf("%s crashed on %s (%s). Run /bug to report it; the crash details are attached automatically.", AppName(), parsed, crash.Message))
	}
}

// handleEditorSubmit implements the reference dispatch order.
func (m *InteractiveMode) handleEditorSubmit(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}

	// Exact-match commands first.
	if trimmed == "/quit" {
		m.requestShutdown()
		return
	}
	if trimmed == "/resume" {
		m.showStatus("Session resume requires the session selector (seam).")
		return
	}

	// Bash commands: ! normal, !! excluded from context.
	if strings.HasPrefix(trimmed, "!") {
		isExcluded := strings.HasPrefix(trimmed, "!!")
		command := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "!!"), "!"))
		if command != "" {
			m.mu.Lock()
			running := m.bashRunning
			m.mu.Unlock()
			if running {
				m.showWarning("A bash command is already running. Press Esc to cancel it first.")
				return
			}
			// Execute off the input goroutine: the terminal read path must
			// keep accepting Esc-to-cancel while the command runs.
			go m.handleBashCommand(command, isExcluded)
			return
		}
	}

	// Slash commands.
	if strings.HasPrefix(trimmed, "/") {
		m.dispatchSlashCommand(trimmed)
		return
	}

	// Queue input during compaction.
	if m.session != nil && m.sessionCompacting() {
		m.showStatus("Compaction in progress; message queued.")
		return
	}

	// Steer when streaming.
	if m.session != nil && m.session.IsStreaming() {
		m.session.Steer(trimmed)
		m.showStatus("Steered the running request.")
		return
	}

	m.sendPrompt(trimmed)
}

// dispatchSlashCommand routes one /command with optional args.
func (m *InteractiveMode) dispatchSlashCommand(text string) {
	body := strings.TrimSpace(strings.TrimPrefix(text, "/"))
	name := body
	args := ""
	if space := strings.IndexAny(body, " \t"); space != -1 {
		name = body[:space]
		args = strings.TrimSpace(body[space+1:])
	}

	if handler, ok := slashCommandHandlers[name]; ok {
		handler(m, args)
		return
	}
	for _, builtin := range BuiltinSlashCommands {
		if builtin.Name == name {
			m.showStatus(fmt.Sprintf("/%s requires its selector widget (seam).", name))
			return
		}
	}
	m.showWarning(fmt.Sprintf("Unknown command: /%s. Type /hotkeys to see available commands.", name))
}

// sendPrompt appends the user message, persists it, and drives one
// agent run. The run executes on its own goroutine: the driver keeps
// dispatching input (steer/abort) while the transcript streams events.
func (m *InteractiveMode) sendPrompt(text string) {
	if m.session == nil {
		return
	}
	message := &ai.UserMessage{Content: ai.StringContent(text)}
	m.session.SessionManager.AppendMessage(message)
	m.session.Agent.SetMessages(append(m.session.Agent.Messages(), message))
	m.showWorkingStatusIndicator()
	m.runAgent(func() error { return m.session.Agent.ContinueFrom() })
}

// renderEditorTopBorder embeds the working indicator in the editor's top
// border while a run is active (CustomEditor.renderTopBorder); an empty
// result keeps the editor's default rule.
func (m *InteractiveMode) renderEditorTopBorder(width, hiddenLineCount int) string {
	m.mu.Lock()
	indicator := m.workingIndicator
	m.mu.Unlock()
	if indicator == nil {
		return ""
	}
	return indicator.RenderInBorder(width)
}

// showWorkingStatusIndicator starts the in-border run indicator.
func (m *InteractiveMode) showWorkingStatusIndicator() {
	m.mu.Lock()
	if m.workingIndicator == nil {
		m.workingIndicator = NewWorkingStatusIndicator(m.tui, "Working")
	}
	indicator := m.workingIndicator
	m.mu.Unlock()

	indicator.Start()
	m.editor.Invalidate()
	m.tui.RequestRender(false)
}

// clearWorkingStatusIndicator stops and drops the indicator at settle.
func (m *InteractiveMode) clearWorkingStatusIndicator() {
	m.mu.Lock()
	indicator := m.workingIndicator
	m.workingIndicator = nil
	m.mu.Unlock()
	if indicator == nil {
		return
	}
	indicator.Dispose()
	m.editor.Invalidate()
	m.tui.RequestRender(false)
}

// runAgent executes one agent run off the input path; errors surface as
// driver errors and the working indicator clears at settle.
func (m *InteractiveMode) runAgent(run func() error) {
	if run == nil {
		return
	}
	m.mu.Lock()
	if m.runActive {
		// A run is in flight: the caller queues instead (steer/compaction).
		m.mu.Unlock()
		m.showStatus("Queued")
		return
	}
	m.runActive = true
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			m.runActive = false
			m.mu.Unlock()
			m.clearWorkingStatusIndicator()
			m.tui.RequestRender(false)
		}()
		if err := run(); err != nil {
			m.showError(err.Error())
			return
		}
		// Provider failures arrive as an error-stop message rather than a
		// returned error; surface them like the reference's error banner.
		if message, hasError := m.session.Agent.ErrorMessage(); hasError && message != "" {
			m.showError(message)
		}
	}()
}

// IsRunActive reports an in-flight agent run (tests).
func (m *InteractiveMode) IsRunActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runActive
}

// WaitForRun blocks until the current run settles (tests).
func (m *InteractiveMode) WaitForRun() {
	m.session.Agent.WaitForIdle()
	for {
		m.mu.Lock()
		active := m.runActive
		m.mu.Unlock()
		if !active {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// handleBashCommand runs one interactive bash command.
func (m *InteractiveMode) handleBashCommand(command string, excludedFromContext bool) {
	m.mu.Lock()
	m.bashRunning = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.bashRunning = false
		m.mu.Unlock()
	}()

	result, err := runInteractiveBash(command)
	if err != nil {
		m.showError(fmt.Sprintf("bash failed: %v", err))
		return
	}
	m.showStatus(fmt.Sprintf("$ %s\n%s", command, result))
	if !excludedFromContext && m.session != nil {
		m.session.SessionManager.AppendMessage(map[string]any{
			"role":    "user",
			"content": fmt.Sprintf("Ran bash command `%s`:\n%s", command, result),
		})
	}
}

func runInteractiveBash(command string) (string, error) {
	cwd, _ := osGetwd()
	if cwd == "" {
		cwd = "."
	}
	ops := CreateLocalBashOperations("")
	result, err := ExecuteBashWithOperations(command, cwd, ops, &BashExecutorOptions{})
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

func (m *InteractiveMode) sessionCompacting() bool {
	return false
}

// handleNameCommand sets the session display name.
func (m *InteractiveMode) handleNameCommand(args string) {
	if m.session == nil || m.session.SessionManager == nil {
		return
	}
	name := strings.TrimSpace(args)
	if name == "" {
		if current := m.session.SessionManager.GetSessionName(); current != "" {
			m.showStatus(fmt.Sprintf("Session name: %s", current))
		} else {
			m.showStatus("Session is unnamed. Use /name <title>.")
		}
		return
	}
	m.session.SessionManager.AppendSessionInfo(name)
	m.showStatus(fmt.Sprintf("Session name set: %s", name))
}

// handleNewCommand starts a fresh session (the reference's /new
// rebuilds the driver against a clean session; the minimal core resets
// the transcript and session name).
func (m *InteractiveMode) handleNewCommand() {
	if m.session == nil || m.session.SessionManager == nil {
		return
	}
	m.session.SessionManager.AppendSessionInfo("New session")
	m.transcript.Clear()
	m.showStatus("Started a new session.")
}

// handleThinkingCommand sets or reports the thinking level.
func (m *InteractiveMode) handleThinkingCommand(args string) {
	if m.session == nil || m.session.Agent == nil {
		return
	}
	level := strings.TrimSpace(args)
	if level == "" {
		m.showStatus(fmt.Sprintf("Thinking level: %s (levels: %s)", m.session.Agent.ThinkingLevel(), strings.Join(ThinkingLevelOptions, ", ")))
		return
	}
	if !IsValidThinkingLevel(level) {
		m.showWarning(fmt.Sprintf("Unknown thinking level %q. Valid: %s", level, strings.Join(ThinkingLevelOptions, ", ")))
		return
	}
	m.session.SetThinkingLevel(level)
	m.showStatus(fmt.Sprintf("Thinking level set to %s.", level))
}

// handleModelCommand reports the current model (the interactive
// selector rides the seam; patterns are not resolved here).
func (m *InteractiveMode) handleModelCommand(args string) {
	model := m.session.Model()
	if model == nil {
		m.showStatus("No model selected. Use --model <provider/id> at startup.")
		return
	}
	m.showStatus(fmt.Sprintf("Current model: %s/%s", model.Provider, model.ID))
}

// handleExportCommand writes the session JSONL to a path (or the
// default timestamped name).
func (m *InteractiveMode) handleExportCommand(args string) {
	if m.session == nil || m.session.SessionManager == nil {
		return
	}
	path := strings.TrimSpace(args)
	returned, err := ExportSessionToJsonl(m.session.SessionManager, path, nil)
	if err != nil {
		m.showError("Export failed: " + err.Error())
		return
	}
	m.showStatus("Exported session to " + returned)
}

// handleCompactCommand runs one manual compaction and reports the
// checkpoint outcome (nothing compressible stays a no-op).
func (m *InteractiveMode) handleCompactCommand(args string) {
	if m.session == nil {
		return
	}
	if m.session.defaultSummarizationStream == nil {
		m.showError("Compaction is unavailable: no summarization model is configured.")
		return
	}
	m.showStatus("Compacting...")
	result, err := m.session.CompactSession(&SessionCompactionOptions{CustomInstructions: args})
	if err != nil {
		m.showError("Compaction failed: " + err.Error())
		return
	}
	if result == nil || !result.Executed {
		m.showStatus("Nothing to compact yet.")
		return
	}
	m.showStatus(formatCompactionStatus(result))
	m.tui.RequestRender(false)
}

// formatCompactionStatus renders the post-compaction summary line.
func formatCompactionStatus(result *CompactSessionResult) string {
	return fmt.Sprintf("Compacted %d tokens into a checkpoint keeping entry %s",
		result.TokensBefore, result.FirstKeptEntryID)
}

// showSessionInfo renders /session stats.
func (m *InteractiveMode) showSessionInfo() {
	if m.session == nil || m.session.SessionManager == nil {
		return
	}
	manager := m.session.SessionManager
	entries := manager.GetEntries()
	messages := 0
	for _, entry := range entries {
		if _, isMessage := entry.(*SessionMessageEntry); isMessage {
			messages++
		}
	}
	m.showStatus(fmt.Sprintf(
		"Session %s\nMessages: %d\nModel: %s\nCwd: %s",
		manager.GetSessionID(), messages, m.session.Model().ID, manager.GetCwd(),
	))
}

// showStatus appends one status line.
func (m *InteractiveMode) showStatus(text string) {
	m.emitStatusLine("status", text)
}

// showWarning appends one warning line.
func (m *InteractiveMode) showWarning(text string) {
	m.emitStatusLine("warning", text)
}

// showError appends one error line.
func (m *InteractiveMode) showError(text string) {
	m.emitStatusLine("error", text)
}

// statusStyle maps a severity to its ANSI color (error red, warning
// yellow, status default).
func statusStyle(kind string) string {
	switch kind {
	case "error":
		return "\x1b[31m"
	case "warning":
		return "\x1b[33m"
	}
	return ""
}

// statusReset closes the style span.
const statusReset = "\x1b[0m"

func (m *InteractiveMode) emitStatusLine(kind, text string) {
	m.mu.Lock()
	m.statusText = text
	sink := m.StatusSink
	m.mu.Unlock()
	if sink != nil {
		sink(kind + ": " + text)
	}
	// The status slot is one locked component mounted at construction;
	// setting its text replaces the previous line (the reference keeps a
	// transient single-line slot) without touching the layout tree.
	if m.statusSlot != nil {
		m.statusSlot.set(statusStyle(kind) + text + statusReset)
	}
}

// statusLineComponent is the transient single-line status slot. Reads
// happen on the render goroutine; writes from event handlers.
type statusLineComponent struct {
	mu   sync.Mutex
	text string
}

func (s *statusLineComponent) set(text string) {
	s.mu.Lock()
	s.text = text
	s.mu.Unlock()
}

// Render implements tui.Component.
func (s *statusLineComponent) Render(width int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.text == "" {
		return nil
	}
	return []string{s.text}
}

// Invalidate implements tui.Component.
func (s *statusLineComponent) Invalidate() {}

// requestShutdown begins termination (idempotent).
func (m *InteractiveMode) requestShutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shutdownPending {
		return
	}
	m.shutdownPending = true
	close(m.shutdownDone)
}

// ShutdownRequested reports termination state (tests).
func (m *InteractiveMode) ShutdownRequested() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shutdownPending
}

// IsStdinATTY reports whether stdin is an interactive terminal. The
// character-device bit alone misclassifies /dev/null, so probe termios
// (available on unix; the windows build falls back to the device bit
// plus a /dev/null exclusion).
func IsStdinATTY() bool {
	return stdinIsATTYImpl()
}

// IsStdoutATTY reports whether stdout is an interactive terminal, using
// the same probe as IsStdinATTY.
func IsStdoutATTY() bool {
	return stdoutIsATTYImpl()
}
