package codingagent

// Tests for the interactive-mode driver: construction, dispatch order,
// slash-command routing, status plumbing, and the shutdown handoff.

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/tui"
)

func newInteractiveForTest(t *testing.T) (*InteractiveMode, *[]string) {
	t.Helper()
	session := newTestSession(t)
	mode := NewInteractiveMode(InteractiveModeOptions{
		Session: session,
		TuiMode: "regular",
		Version: "test",
	})
	lines := &[]string{}
	mode.StatusSink = func(line string) {
		*lines = append(*lines, line)
	}
	return mode, lines
}

func TestInteractiveModeConstruction(t *testing.T) {
	mode, _ := newInteractiveForTest(t)
	if mode.tui == nil {
		t.Fatal("no TUI")
	}
	if mode.editor == nil {
		t.Fatal("no editor")
	}
	if mode.keybindings == nil {
		t.Fatal("no keybindings")
	}
	if mode.ShutdownRequested() {
		t.Fatal("shutdown at construction")
	}
}

func TestInteractiveDispatchQuit(t *testing.T) {
	mode, _ := newInteractiveForTest(t)
	mode.handleEditorSubmit("/quit")
	if !mode.ShutdownRequested() {
		t.Fatal("quit did not request shutdown")
	}

	// Idempotent.
	mode.handleEditorSubmit("/quit")
}

func TestInteractiveDispatchUnknownSlash(t *testing.T) {
	mode, lines := newInteractiveForTest(t)
	mode.handleEditorSubmit("/not-a-command")
	found := false
	for _, line := range *lines {
		if strings.Contains(line, "Unknown command") {
			found = true
		}
	}
	if !found {
		t.Fatalf("lines = %v", *lines)
	}
}

func TestInteractiveDispatchBuiltinSeam(t *testing.T) {
	mode, lines := newInteractiveForTest(t)
	// A catalog command without a wired handler lands on the seam
	// (/model now reports the current model instead).
	mode.handleEditorSubmit("/tree")
	found := false
	for _, line := range *lines {
		if strings.Contains(line, "selector widget") {
			found = true
		}
	}
	if !found {
		t.Fatalf("lines = %v", *lines)
	}
	if mode.ShutdownRequested() {
		t.Fatal("seam command shut down")
	}
}

func TestInteractiveDispatchResumeSeam(t *testing.T) {
	mode, lines := newInteractiveForTest(t)
	mode.handleEditorSubmit("/resume")
	found := false
	for _, line := range *lines {
		if strings.Contains(line, "session selector") {
			found = true
		}
	}
	if !found {
		t.Fatalf("lines = %v", *lines)
	}
}

func TestInteractiveDispatchBash(t *testing.T) {
	mode, lines := newInteractiveForTest(t)
	var mu sync.Mutex
	mode.StatusSink = func(line string) { mu.Lock(); *lines = append(*lines, line); mu.Unlock() }
	mode.handleEditorSubmit("!echo hello-bash")
	if !waitForStatus(t, lines, &mu, "hello-bash") {
		mu.Lock()
		joined := strings.Join(*lines, "|")
		mu.Unlock()
		t.Fatalf("lines = %q", joined)
	}
}

func TestInteractiveDispatchPrompt(t *testing.T) {
	mode, _ := newInteractiveForTest(t)
	before := len(mode.session.SessionManager.GetEntries())
	mode.handleEditorSubmit("hello agent")
	after := len(mode.session.SessionManager.GetEntries())
	if after != before+1 {
		t.Fatalf("entries %d -> %d", before, after)
	}
	entries := mode.session.SessionManager.GetEntries()
	last := entries[len(entries)-1]
	message, ok := last.(*SessionMessageEntry)
	if !ok {
		t.Fatalf("last entry type = %T", last)
	}
	if !strings.Contains(string(message.Message), "hello agent") {
		t.Fatalf("message = %s", message.Message)
	}
}

func TestInteractiveSessionCommand(t *testing.T) {
	mode, lines := newInteractiveForTest(t)
	mode.session.SessionManager.AppendMessage(map[string]any{"role": "user", "content": "x"})
	mode.handleEditorSubmit("/session")
	found := false
	for _, line := range *lines {
		if strings.Contains(line, "Messages: 1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("lines = %v", *lines)
	}
}

func TestInteractiveNameCommand(t *testing.T) {
	mode, lines := newInteractiveForTest(t)
	mode.handleEditorSubmit("/name my-session")
	if got := mode.session.SessionManager.GetSessionName(); got != "my-session" {
		t.Fatalf("name = %q", got)
	}
	found := false
	for _, line := range *lines {
		if strings.Contains(line, "Session name set: my-session") {
			found = true
		}
	}
	if !found {
		t.Fatalf("lines = %v", *lines)
	}

	// Bare /name reports the current name.
	mode.handleEditorSubmit("/name")
	reported := false
	for _, line := range *lines {
		if strings.Contains(line, "Session name: my-session") {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("lines = %v", *lines)
	}
}

func TestInteractiveStartupDiagnostics(t *testing.T) {
	session := newTestSession(t)
	mode := NewInteractiveMode(InteractiveModeOptions{
		Session: session,
		TuiMode: "regular",
		Version: "test",
		StartupDiagnostics: []CliDiagnostic{
			{Type: "error", Message: "boom"},
			{Type: "warning", Message: "careful"},
			{Type: "info", Message: "fyi"},
		},
		ModelFallbackMessage: "fell back",
	})
	lines := &[]string{}
	mode.StatusSink = func(line string) { *lines = append(*lines, line) }

	mode.showStartupDiagnostics()
	mode.showStartupNotices()

	joined := strings.Join(*lines, "\n")
	for _, expected := range []string{"error: boom", "warning: careful", "status: fyi", "warning: fell back"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("missing %q in %q", expected, joined)
		}
	}
}

// --- Core run wiring -------------------------------------------------------

// stubStreamFn drives one agent run without a provider: it emits a text
// delta and completes the assistant message.
func stubStreamFn() agent.StreamFn {
	return func(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		go func() {
			message := &ai.AssistantMessage{StopReason: "stop"}
			stream.Push(&ai.EventStart{Partial: message})
			message.Content = []ai.ContentBlock{&ai.TextContent{Text: "stub reply"}}
			stream.Push(&ai.EventTextDelta{ContentIndex: 0, Delta: "stub reply", Partial: message})
			stream.Push(&ai.EventDone{Message: message, Reason: "stop"})
		}()
		return stream
	}
}

func TestInteractiveSendPromptRunsAgent(t *testing.T) {
	session := newTestSession(t)
	session.Agent.StreamFunction = stubStreamFn()

	mode := NewInteractiveMode(InteractiveModeOptions{Session: session, TuiMode: "regular"})
	lines := &[]string{}
	var mu sync.Mutex
	mode.StatusSink = func(line string) { mu.Lock(); *lines = append(*lines, line); mu.Unlock() }
	mode.subscribeAgentEvents() // Run() performs this subscription

	mode.handleEditorSubmit("hello agent")

	// The run is dispatched off the input path.
	if !mode.IsRunActive() {
		t.Fatal("run not active after submit")
	}
	// The working indication lives in the editor's top border
	// (CustomEditor.renderTopBorder), not on a status line.
	if border := mode.editor.Render(60)[0]; !strings.Contains(border, "Working") {
		t.Fatalf("editor top border = %q, want the working indicator", border)
	}
	mode.WaitForRun()

	// The user message persisted and the transcript rendered the reply.
	entries := session.SessionManager.GetEntries()
	foundUser := false
	for _, entry := range entries {
		if message, ok := entry.(*SessionMessageEntry); ok {
			if strings.Contains(string(message.Message), "hello agent") {
				foundUser = true
			}
		}
	}
	if !foundUser {
		t.Fatal("user message not persisted")
	}
	joined := strings.Join(mode.Transcript().Component().Render(80), "\n")
	if !strings.Contains(joined, "stub reply") {
		t.Fatalf("transcript = %q", joined)
	}

	mu.Lock()
	statuses := strings.Join(*lines, "|")
	mu.Unlock()
	if strings.Contains(statuses, "Working") {
		t.Fatalf("working indicator must not render as a status line: %q", statuses)
	}
	// The indicator clears at settle: the editor's default rule returns.
	if border := mode.editor.Render(60)[0]; !strings.Contains(border, "─") {
		t.Fatalf("editor top border after settle = %q", border)
	}
}

func TestInteractiveRunQueuesWhileActive(t *testing.T) {
	session := newTestSession(t)
	release := make(chan struct{})
	session.Agent.StreamFunction = func(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		go func() {
			<-release
			message := &ai.AssistantMessage{StopReason: "stop"}
			stream.Push(&ai.EventStart{Partial: message})
			stream.Push(&ai.EventDone{Message: message, Reason: "stop"})
		}()
		return stream
	}

	mode := NewInteractiveMode(InteractiveModeOptions{Session: session, TuiMode: "regular"})
	lines := &[]string{}
	var mu sync.Mutex
	mode.StatusSink = func(line string) { mu.Lock(); *lines = append(*lines, line); mu.Unlock() }
	mode.subscribeAgentEvents()

	mode.handleEditorSubmit("first")
	mode.handleEditorSubmit("second") // in-flight: queued, not a second run

	mu.Lock()
	joined := strings.Join(*lines, "|")
	mu.Unlock()
	if !strings.Contains(joined, "Queued") {
		t.Fatalf("statuses = %q", joined)
	}
	close(release)
	mode.WaitForRun()
	if mode.IsRunActive() {
		t.Fatal("run still active after settle")
	}
}

func TestInteractiveRunReportsErrors(t *testing.T) {
	session := newTestSession(t)
	session.Agent.StreamFunction = func(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		go func() {
			text := "provider exploded"
			message := &ai.AssistantMessage{StopReason: "error", ErrorMessage: &text}
			stream.Push(&ai.EventError{Reason: "error", Error: message})
		}()
		return stream
	}

	mode := NewInteractiveMode(InteractiveModeOptions{Session: session, TuiMode: "regular"})
	lines := &[]string{}
	var mu sync.Mutex
	mode.StatusSink = func(line string) { mu.Lock(); *lines = append(*lines, line); mu.Unlock() }

	mode.subscribeAgentEvents()
	mode.handleEditorSubmit("fail please")
	mode.WaitForRun()

	mu.Lock()
	joined := strings.Join(*lines, "|")
	mu.Unlock()
	if !strings.Contains(joined, "error:") {
		t.Fatalf("statuses = %q", joined)
	}
}

func TestInteractiveSubscribeDisposer(t *testing.T) {
	session := newTestSession(t)
	session.Agent.StreamFunction = stubStreamFn()
	mode := NewInteractiveMode(InteractiveModeOptions{Session: session, TuiMode: "regular"})
	mode.subscribeAgentEvents()
	if mode.unsubscribe == nil {
		t.Fatal("no disposer registered")
	}

	// Events flow through the transcript while subscribed.
	mode.handleEditorSubmit("bridge me")
	mode.WaitForRun()
	if joined := strings.Join(mode.Transcript().Component().Render(60), "\n"); !strings.Contains(joined, "stub reply") {
		t.Fatalf("bridge = %q", joined)
	}

	// After disposal the transcript stops receiving events.
	mode.unsubscribe()
	mode.unsubscribe = nil
	before := len(mode.Transcript().Blocks())
	mode.handleEditorSubmit("after dispose")
	mode.WaitForRun()
	if len(mode.Transcript().Blocks()) != before {
		t.Fatalf("transcript grew after dispose: %d -> %d", before, len(mode.Transcript().Blocks()))
	}
}

func TestInteractiveStatusRendering(t *testing.T) {
	mode, _ := newInteractiveForTest(t)
	mode.showStatus("plain status")
	lines := mode.statusStack.Render(80)
	if len(lines) == 0 || !strings.Contains(lines[0], "plain status") {
		t.Fatalf("status = %v", lines)
	}
	if strings.Contains(lines[0], "\x1b[31m") {
		t.Fatalf("styled = %q", lines[0])
	}

	// Warnings carry the yellow code; errors red.
	mode.showWarning("careful")
	lines = mode.statusStack.Render(80)
	if !strings.Contains(lines[0], "\x1b[33m") {
		t.Fatalf("warning = %q", lines[0])
	}
	mode.showError("boom")
	lines = mode.statusStack.Render(80)
	if !strings.Contains(lines[0], "\x1b[31m") {
		t.Fatalf("error = %q", lines[0])
	}

	// The newest status replaces the previous line (single slot).
	mode.showStatus("replaced")
	lines = mode.statusStack.Render(80)
	if len(lines) != 1 || !strings.Contains(lines[0], "replaced") {
		t.Fatalf("replaced = %v", lines)
	}
}

// waitForStatus polls until the status sink receives a substring (bash
// runs off the input goroutine now).
func waitForStatus(t *testing.T, lines *[]string, mu *sync.Mutex, want string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		joined := strings.Join(*lines, "|")
		mu.Unlock()
		if strings.Contains(joined, want) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestInteractiveFooterRendering(t *testing.T) {
	mode, _ := newInteractiveForTest(t)
	lines := mode.footerStack.Render(120)
	if len(lines) < 2 {
		t.Fatalf("footer = %v", lines)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "?/") && !strings.Contains(joined, "/") {
		t.Fatalf("footer pwd = %q", joined)
	}
}

func TestInteractiveNewCommand(t *testing.T) {
	mode, lines := newInteractiveForTest(t)
	mode.session.SessionManager.AppendMessage(map[string]any{"role": "user", "content": "old"})
	mode.handleEditorSubmit("/new")
	joined := strings.Join(*lines, "|")
	if !strings.Contains(joined, "Started a new session") {
		t.Fatalf("statuses = %q", joined)
	}
	if got := len(mode.Transcript().Blocks()); got != 0 {
		t.Fatalf("transcript = %d blocks", got)
	}
}

func TestInteractiveThinkingCommand(t *testing.T) {
	mode, lines := newInteractiveForTest(t)

	// Report.
	mode.handleEditorSubmit("/thinking")
	joined := strings.Join(*lines, "|")
	if !strings.Contains(joined, "Thinking level:") {
		t.Fatalf("report = %q", joined)
	}

	// Set.
	mode.handleEditorSubmit("/thinking high")
	joined = strings.Join(*lines, "|")
	if !strings.Contains(joined, "Thinking level set to high") {
		t.Fatalf("set = %q", joined)
	}
	if got := mode.session.Agent.ThinkingLevel(); got != "high" {
		t.Fatalf("level = %q", got)
	}

	// Invalid.
	mode.handleEditorSubmit("/thinking bogus")
	joined = strings.Join(*lines, "|")
	if !strings.Contains(joined, "Unknown thinking level") {
		t.Fatalf("invalid = %q", joined)
	}
}

func TestInteractiveModelCommand(t *testing.T) {
	mode, lines := newInteractiveForTest(t)
	mode.handleEditorSubmit("/model")
	joined := strings.Join(*lines, "|")
	if !strings.Contains(joined, "Current model:") {
		t.Fatalf("report = %q", joined)
	}
}

func TestInteractiveExportCommand(t *testing.T) {
	mode, lines := newInteractiveForTest(t)
	mode.session.SessionManager.AppendMessage(map[string]any{"role": "user", "content": "export me"})

	dir := t.TempDir()
	path := dir + "/out.jsonl"
	mode.handleEditorSubmit("/export " + path)

	joined := strings.Join(*lines, "|")
	if !strings.Contains(joined, "Exported session to") {
		t.Fatalf("statuses = %q", joined)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "export me") {
		t.Fatalf("exported = %q", data)
	}
}

// TestInteractiveModeMountsEditorAndDockOrder pins the ported mount
// protocol (interactive-mode.ts mountInteractiveTui + editorContainer): the
// editor is mounted in the editor container, and the main screen children
// follow the reference dock order.
func TestInteractiveModeMountsEditorAndDockOrder(t *testing.T) {
	mode, _ := newInteractiveForTest(t)

	screen, ok := mode.TUI().(*tui.TuiMainScreen)
	if !ok {
		t.Fatalf("renderer = %T, want main screen", mode.TUI())
	}
	if len(screen.Children) != 4 {
		t.Fatalf("screen children = %d, want document/status/editor/footer", len(screen.Children))
	}
	want := []tui.Component{mode.documentStack, mode.statusStack, mode.editorStack, mode.footerStack}
	for i, component := range want {
		if screen.Children[i] != component {
			t.Errorf("child %d = %T, want %T", i, screen.Children[i], component)
		}
	}

	mounted := false
	for _, child := range mode.editorStack.Children {
		if child == tui.Component(mode.editor) {
			mounted = true
		}
	}
	if !mounted {
		t.Fatal("editor is not mounted in the editor container")
	}
}

// TestInteractiveModeFullscreenMountsViewportRoot pins the fullscreen leg:
// the alt screen renders through a layout root whose transcript is the
// primary follow-end scroll view.
func TestInteractiveModeFullscreenMountsViewportRoot(t *testing.T) {
	session := newTestSession(t)
	mode := NewInteractiveMode(InteractiveModeOptions{
		Session: session,
		TuiMode: "fullscreen",
		Version: "test",
	})

	alt, ok := mode.TUI().(*tui.TuiAltScreen)
	if !ok {
		t.Fatalf("renderer = %T, want alt screen", mode.TUI())
	}
	if !tui.IsViewportTUI(alt) {
		t.Fatal("alt screen must be a viewport TUI")
	}
	if mode.transcriptScroll == nil {
		t.Fatal("no transcript scroll view")
	}
	if !mode.transcriptScroll.Primary() {
		t.Fatal("transcript must be the primary scroll view")
	}
	if !mode.transcriptScroll.IsFollowingEnd() {
		t.Fatal("transcript must follow the end")
	}
}
