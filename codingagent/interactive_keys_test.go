package codingagent

// Tests for the app-level key handling (interactive-mode.ts
// setupKeyHandlers) and for the rule that the startup header only
// advertises keys with a handler.

import (
	"strings"
	"sync"
	"testing"

	"github.com/gladmo/openagent/agent"
)

func keyModeForTest(t *testing.T) (*InteractiveMode, *[]string) {
	t.Helper()
	mode, lines := newInteractiveForTest(t)
	return mode, lines
}

func TestEscapeInterruptsStreamingRun(t *testing.T) {
	session := newTestSession(t)
	mode := NewInteractiveMode(InteractiveModeOptions{Session: session, TuiMode: "regular"})
	lines := &[]string{}
	var mu sync.Mutex
	mode.StatusSink = func(line string) { mu.Lock(); *lines = append(*lines, line); mu.Unlock() }

	// Idle and empty editor: escape is consumed as the double-escape wait.
	if !mode.dispatchAppKey("\x1b") {
		t.Fatal("escape not consumed on an empty editor")
	}

	// Streaming: escape aborts the run.
	session.SetAgentRunActive(true)
	if !mode.dispatchAppKey("\x1b") {
		t.Fatal("escape not consumed while streaming")
	}
	session.mu.Lock()
	aborted := session.agentRunAbortRequested
	session.mu.Unlock()
	if !aborted {
		t.Fatal("run abort not requested")
	}
	mu.Lock()
	joined := strings.Join(*lines, "|")
	mu.Unlock()
	if !strings.Contains(joined, "Interrupted") {
		t.Fatalf("statuses = %q", joined)
	}
}

func TestCtrlCClearsThenExits(t *testing.T) {
	mode, _ := keyModeForTest(t)
	mode.editor.SetText("half typed")

	if !mode.dispatchAppKey("\x03") {
		t.Fatal("ctrl+c not consumed")
	}
	if got := mode.editor.GetText(); got != "" {
		t.Fatalf("editor text = %q, want cleared", got)
	}
	if mode.ShutdownRequested() {
		t.Fatal("single ctrl+c must not exit")
	}

	if !mode.dispatchAppKey("\x03") {
		t.Fatal("second ctrl+c not consumed")
	}
	if !mode.ShutdownRequested() {
		t.Fatal("double ctrl+c must exit")
	}
}

func TestCtrlDExitsOnlyWithEmptyEditor(t *testing.T) {
	mode, _ := keyModeForTest(t)
	mode.editor.SetText("text")

	if mode.dispatchAppKey("\x04") {
		t.Fatal("ctrl+d consumed with a non-empty editor")
	}
	if mode.ShutdownRequested() {
		t.Fatal("ctrl+d must not exit with a non-empty editor")
	}

	mode.editor.SetText("")
	if !mode.dispatchAppKey("\x04") {
		t.Fatal("ctrl+d not consumed on an empty editor")
	}
	if !mode.ShutdownRequested() {
		t.Fatal("ctrl+d must exit on an empty editor")
	}
}

func TestCtrlOTogglesHeaderAndToolExpansion(t *testing.T) {
	mode, _ := keyModeForTest(t)
	mode.transcript.HandleEvent(&agent.EventToolExecutionStart{
		ToolCallID: "call-1",
		ToolName:   "read",
		Args:       nil,
	}, func(string) ToolRenderers { return nil })

	collapsed := strings.Join(mode.header.Render(120), "\n")
	if strings.Contains(collapsed, "to interrupt") {
		t.Fatalf("header already expanded: %q", collapsed)
	}

	if !mode.dispatchAppKey("\x0f") {
		t.Fatal("ctrl+o not consumed")
	}
	expanded := strings.Join(mode.header.Render(120), "\n")
	if !strings.Contains(expanded, "to interrupt") {
		t.Fatalf("header not expanded: %q", expanded)
	}
	component := mode.transcript.ToolComponent("call-1")
	if component == nil {
		t.Fatal("tool component missing")
	}
	if !component.Expanded() {
		t.Fatal("tool block not expanded")
	}

	mode.dispatchAppKey("\x0f")
	if component.Expanded() {
		t.Fatal("tool block not collapsed")
	}
	if h := strings.Join(mode.header.Render(120), "\n"); strings.Contains(h, "to interrupt") {
		t.Fatalf("header not collapsed: %q", h)
	}
}

func TestThinkingCycleKey(t *testing.T) {
	session := newTestSession(t)
	mode := NewInteractiveMode(InteractiveModeOptions{Session: session, TuiMode: "regular"})
	session.SetThinkingLevel("off")

	if !mode.dispatchAppKey("\x1b[Z") { // shift+tab
		t.Fatal("thinking cycle key not consumed")
	}
	if got := session.ThinkingLevel(); got != "minimal" {
		t.Fatalf("thinking level = %q, want the next level", got)
	}
}

func TestModelCycleKeyIsHandled(t *testing.T) {
	session := newTestSession(t)
	mode := NewInteractiveMode(InteractiveModeOptions{Session: session, TuiMode: "regular"})
	lines := &[]string{}
	var mu sync.Mutex
	mode.StatusSink = func(line string) { mu.Lock(); *lines = append(*lines, line); mu.Unlock() }

	if !mode.dispatchAppKey("\x10") { // ctrl+p
		t.Fatal("model cycle key not consumed")
	}
	mu.Lock()
	joined := strings.Join(*lines, "|")
	mu.Unlock()
	if !strings.Contains(joined, "model") && !strings.Contains(joined, "Switched") {
		t.Fatalf("statuses = %q", joined)
	}
}

// TestStartupHeaderOnlyAdvertisesImplementedKeys pins the anti-drift rule:
// every advertised key either has a handler or is editor-level, and hints
// for unported features do not appear.
func TestStartupHeaderOnlyAdvertisesImplementedKeys(t *testing.T) {
	mode, _ := keyModeForTest(t)

	for _, action := range mode.keyActions() {
		if action.binding == "" {
			continue
		}
		if (action.long != "" || action.short != "") && action.handle == nil && !action.editorLevel {
			t.Errorf("advertised binding without a handler: %s", action.binding)
		}
	}

	compact, expanded := mode.startupHintLines()
	advertised := compact + "\n" + expanded
	for _, unported := range []string{
		"to suspend",
		"external editor",
		"queue follow-up",
		"queued messages",
		"paste image",
		"drop files",
		"expand thinking",
		"to select model",
	} {
		if strings.Contains(advertised, unported) {
			t.Errorf("header advertises an unported feature: %q", unported)
		}
	}
	for _, implemented := range []string{"interrupt", "clear/exit", "commands", "bash", "more"} {
		if !strings.Contains(compact, implemented) {
			t.Errorf("compact header missing %q: %q", implemented, compact)
		}
	}
}
