package codingagent

// Port of the app-level key handling (interactive-mode.ts setupKeyHandlers):
// the editor owns editing keys, the driver owns the app actions. The
// startup header advertises exactly the actions registered here, so hints
// and behavior cannot drift apart.

import (
	"strings"
	"time"

	"github.com/gladmo/openagent/tui"
)

// appKeyAction is one app-level keybinding. handle == nil marks a
// display-only hint for a key the editor or submit path implements.
type appKeyAction struct {
	binding string
	short   string
	long    string
	// editorLevel marks a hint implemented outside the app-level listener.
	editorLevel bool
	handle      func() bool // reports whether the key was consumed
}

// keyActions is the single source of truth for app-level keys: both the
// input listener and the startup header derive from it.
func (m *InteractiveMode) keyActions() []appKeyAction {
	return []appKeyAction{
		{binding: "app.interrupt", short: "interrupt", long: "to interrupt", handle: m.handleInterrupt},
		{binding: "app.clear", short: "", long: "to clear", handle: m.handleClear},
		{binding: "app.exit", short: "", long: "to exit (empty)", handle: m.handleExit},
		{binding: "app.thinking.cycle", short: "", long: "to cycle thinking level", handle: m.handleCycleThinking},
		{binding: "app.model.cycleForward", short: "", long: "to cycle models", handle: func() bool { return m.handleCycleModel("forward") }},
		{binding: "app.model.cycleBackward", short: "", long: "", handle: func() bool { return m.handleCycleModel("backward") }},
		{binding: "app.tools.expand", short: "more", long: "to expand tools", handle: m.handleToggleExpand},
		// The editor owns this key; the hint documents it.
		{binding: "tui.editor.deleteToLineEnd", long: "to delete to end", editorLevel: true},
	}
}

// setupKeyHandlers registers the app-level listener (before the focused
// editor sees the key) and returns its disposer.
func (m *InteractiveMode) setupKeyHandlers() func() {
	return m.tui.AddInputListener(func(data string) *tui.TuiInputListenerResult {
		if m.dispatchAppKey(data) {
			return &tui.TuiInputListenerResult{Consume: true}
		}
		return nil
	})
}

// dispatchAppKey routes one input chunk to the matching app action; it
// reports whether the key was consumed. A matched action may decline (for
// example Escape with a non-empty editor), which passes the key through to
// the editor.
func (m *InteractiveMode) dispatchAppKey(data string) bool {
	if m.keybindings == nil {
		return false
	}
	for _, action := range m.keyActions() {
		if action.binding == "" || action.handle == nil {
			continue
		}
		if !m.keybindings.Matches(data, tui.Keybinding(action.binding)) {
			continue
		}
		return action.handle()
	}
	return false
}

// handleInterrupt ports the reference's onEscape: abort the run or a bash
// command, and treat a double escape on an empty editor as the tree/fork
// action.
func (m *InteractiveMode) handleInterrupt() bool {
	if m.session == nil {
		return false
	}
	if m.session.IsStreaming() {
		m.session.ClearFollowUp()
		m.session.ClearSteering()
		m.session.RequestAbort()
		m.showStatus("Interrupted")
		return true
	}
	m.mu.Lock()
	bashRunning := m.bashRunning
	m.mu.Unlock()
	if bashRunning {
		// AbortBash is still a stub in this port; say so instead of
		// pretending the command was cancelled.
		m.showWarning("A bash command is running; aborting it is not ported yet (seam).")
		return true
	}
	if strings.TrimSpace(m.editor.GetText()) == "" {
		now := time.Now()
		if now.Sub(m.lastEscapeAt) < 500*time.Millisecond {
			m.lastEscapeAt = time.Time{}
			m.showWarning("Session tree and fork selectors are not ported (seam).")
			return true
		}
		m.lastEscapeAt = now
		return true
	}
	return false
}

// handleClear ports handleCtrlC: the first press clears the editor, a second
// press within 500ms shuts down.
func (m *InteractiveMode) handleClear() bool {
	now := time.Now()
	if now.Sub(m.lastClearAt) < 500*time.Millisecond {
		m.lastClearAt = time.Time{}
		m.requestShutdown()
		return true
	}
	m.lastClearAt = now
	if m.editor.GetText() != "" {
		m.editor.SetText("")
		m.editor.Invalidate()
		m.tui.RequestRender(false)
	}
	return true
}

// handleExit ports handleCtrlD: the reference's CustomEditor only fires it
// with an empty editor.
func (m *InteractiveMode) handleExit() bool {
	if strings.TrimSpace(m.editor.GetText()) != "" {
		return false
	}
	m.requestShutdown()
	return true
}

// handleToggleExpand ports toggleToolOutputExpansion: it expands the
// transcript's tool blocks and the startup help together.
func (m *InteractiveMode) handleToggleExpand() bool {
	m.mu.Lock()
	m.toolOutputExpanded = !m.toolOutputExpanded
	expanded := m.toolOutputExpanded
	m.mu.Unlock()

	m.transcript.SetExpanded(expanded)
	if m.header != nil {
		m.header.SetExpanded(expanded || m.options.Verbose)
	}
	m.tui.RequestRender(false)
	return true
}

// handleCycleThinking ports cycleThinkingLevel.
func (m *InteractiveMode) handleCycleThinking() bool {
	if m.session == nil {
		return true
	}
	current := m.session.ThinkingLevel()
	next := nextThinkingLevel(current)
	if next == "" || next == current {
		m.showStatus("Current model does not support thinking")
		return true
	}
	m.session.SetThinkingLevel(next)
	if m.footer != nil {
		m.footer.Invalidate()
	}
	m.showStatus("Thinking level: " + next)
	m.tui.RequestRender(false)
	return true
}

// nextThinkingLevel returns the following level in the reference order.
func nextThinkingLevel(current string) string {
	for i, level := range ThinkingLevelOptions {
		if level == current {
			return ThinkingLevelOptions[(i+1)%len(ThinkingLevelOptions)]
		}
	}
	return ""
}

// handleCycleModel ports cycleModel.
func (m *InteractiveMode) handleCycleModel(direction string) bool {
	if m.session == nil {
		return true
	}
	result := m.session.CycleModel(direction, false)
	if result == nil || result.Model == nil {
		if len(m.session.scopedModels) > 0 {
			m.showStatus("Only one model in scope")
		} else {
			m.showStatus("Only one model available")
		}
		return true
	}
	if m.footer != nil {
		m.footer.Invalidate()
	}
	name := result.Model.Name
	if name == "" {
		name = result.Model.ID
	}
	thinking := ""
	if result.Model.Reasoning && result.ThinkingLevel != "" && result.ThinkingLevel != "off" {
		thinking = " (thinking: " + result.ThinkingLevel + ")"
	}
	m.showStatus("Switched to " + name + thinking)
	m.tui.RequestRender(false)
	return true
}

// registeredBindings reports which app-level bindings have handlers.
func (m *InteractiveMode) registeredBindings() map[string]bool {
	registered := map[string]bool{}
	for _, action := range m.keyActions() {
		if action.binding != "" && action.handle != nil {
			registered[action.binding] = true
		}
	}
	return registered
}

// startupHintLines renders the compact and expanded keybinding hints from
// the registered actions, so the header never advertises a key without a
// handler.
func (m *InteractiveMode) startupHintLines() (compact string, expanded string) {
	registered := m.registeredBindings()

	var compactParts []string
	if registered["app.interrupt"] {
		compactParts = append(compactParts, KeyHint("app.interrupt", "interrupt"))
	}
	if registered["app.clear"] && registered["app.exit"] {
		compactParts = append(compactParts,
			RawKeyHint(KeyTextOf("app.clear")+"/"+KeyTextOf("app.exit"), "clear/exit"))
	}
	// `/` commands and `!` bash are submit-level keys implemented by the
	// driver's editor-submit dispatch.
	compactParts = append(compactParts, RawKeyHint("/", "commands"), RawKeyHint("!", "bash"))
	if registered["app.tools.expand"] {
		compactParts = append(compactParts, KeyHint("app.tools.expand", "more"))
	}
	compact = strings.Join(compactParts, " · ")

	lines := []string{}
	seenCycleModel := false
	for _, action := range m.keyActions() {
		if action.long == "" {
			continue
		}
		switch action.binding {
		case "app.clear":
			lines = append(lines, RawKeyHint(KeyTextOf("app.clear")+" twice", "to exit"))
			lines = append(lines, KeyHint("app.clear", action.long))
		case "app.model.cycleBackward":
			// Fold both cycle directions into one hint.
			if !seenCycleModel {
				lines = append(lines, RawKeyHint(
					KeyTextOf("app.model.cycleForward")+"/"+KeyTextOf("app.model.cycleBackward"),
					"to cycle models"))
				seenCycleModel = true
			}
		default:
			if action.binding == "" {
				lines = append(lines, action.long)
				continue
			}
			lines = append(lines, KeyHint(action.binding, action.long))
		}
	}
	// `!` / `!!` and `/` are editor-submit level.
	lines = append(lines,
		RawKeyHint("/", "for commands"),
		RawKeyHint("!", "to run bash"),
		RawKeyHint("!!", "to run bash (no context)"),
	)
	expanded = strings.Join(lines, "\n")
	return compact, expanded
}
