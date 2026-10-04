package tui

// Ported from pi/packages/tui/test/editor.test.ts (kill ring + undo
// batches).

import "testing"

func TestEditorKillRingBasics(t *testing.T) {
	// Ctrl+W deletes; Ctrl+Y yanks.
	editor := newTestEditor(80, 24)
	editor.SetText("foo bar baz")
	editor.HandleInput("\x17") // Ctrl+W deletes "baz"
	if got := editor.GetText(); got != "foo bar " {
		t.Fatalf("after W: %q", got)
	}
	editor.HandleInput("\x01") // Ctrl+A
	editor.HandleInput("\x19") // Ctrl+Y
	if got := editor.GetText(); got != "bazfoo bar " {
		t.Fatalf("after Y: %q", got)
	}

	// Ctrl+U deletes to start.
	editor = newTestEditor(80, 24)
	editor.SetText("hello world")
	editor.HandleInput("\x01")
	for i := 0; i < 6; i++ {
		editor.HandleInput("\x1b[C")
	}
	editor.HandleInput("\x15") // Ctrl+U deletes "hello "
	if got := editor.GetText(); got != "world" {
		t.Fatalf("after U: %q", got)
	}
	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "hello world" {
		t.Fatalf("after yank: %q", got)
	}

	// Ctrl+K deletes to end.
	editor = newTestEditor(80, 24)
	editor.SetText("hello world")
	editor.HandleInput("\x01")
	editor.HandleInput("\x0b") // Ctrl+K
	if got := editor.GetText(); got != "" {
		t.Fatalf("after K: %q", got)
	}
	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "hello world" {
		t.Fatalf("after yank: %q", got)
	}

	// Ctrl+Y on empty ring: no-op.
	editor = newTestEditor(80, 24)
	editor.SetText("test")
	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "test" {
		t.Fatalf("empty ring: %q", got)
	}
}

func TestEditorKillRingYankPopCycles(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("first")
	editor.HandleInput("\x17")
	editor.SetText("second")
	editor.HandleInput("\x17")
	editor.SetText("third")
	editor.HandleInput("\x17")

	if got := editor.GetText(); got != "" {
		t.Fatalf("after kills: %q", got)
	}

	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "third" {
		t.Fatalf("yank: %q", got)
	}
	editor.HandleInput("\x1by")
	if got := editor.GetText(); got != "second" {
		t.Fatalf("pop 1: %q", got)
	}
	editor.HandleInput("\x1by")
	if got := editor.GetText(); got != "first" {
		t.Fatalf("pop 2: %q", got)
	}
	editor.HandleInput("\x1by")
	if got := editor.GetText(); got != "third" {
		t.Fatalf("pop 3 wraps: %q", got)
	}
}

func TestEditorKillRingGuards(t *testing.T) {
	// Not preceded by yank.
	editor := newTestEditor(80, 24)
	editor.SetText("test")
	editor.HandleInput("\x17")
	editor.SetText("other")
	editor.HandleInput("x")
	if got := editor.GetText(); got != "otherx" {
		t.Fatalf("typed: %q", got)
	}
	editor.HandleInput("\x1by")
	if got := editor.GetText(); got != "otherx" {
		t.Fatalf("pop without yank: %q", got)
	}

	// Single-entry ring.
	editor = newTestEditor(80, 24)
	editor.SetText("only")
	editor.HandleInput("\x17")
	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "only" {
		t.Fatalf("yank: %q", got)
	}
	editor.HandleInput("\x1by")
	if got := editor.GetText(); got != "only" {
		t.Fatalf("pop single: %q", got)
	}
}

func TestEditorKillRingAccumulation(t *testing.T) {
	// Consecutive Ctrl+W accumulates.
	editor := newTestEditor(80, 24)
	editor.SetText("one two three")
	editor.HandleInput("\x17")
	editor.HandleInput("\x17")
	editor.HandleInput("\x17")
	if got := editor.GetText(); got != "" {
		t.Fatalf("after kills: %q", got)
	}
	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "one two three" {
		t.Fatalf("accumulated yank: %q", got)
	}

	// Multi-line Ctrl+U accumulation including newlines.
	editor = newTestEditor(80, 24)
	editor.SetText("line1\nline2\nline3")

	editor.HandleInput("\x15")
	if got := editor.GetText(); got != "line1\nline2\n" {
		t.Fatalf("1st U: %q", got)
	}
	editor.HandleInput("\x15")
	if got := editor.GetText(); got != "line1\nline2" {
		t.Fatalf("2nd U: %q", got)
	}
	editor.HandleInput("\x15")
	if got := editor.GetText(); got != "line1\n" {
		t.Fatalf("3rd U: %q", got)
	}
	editor.HandleInput("\x15")
	if got := editor.GetText(); got != "line1" {
		t.Fatalf("4th U: %q", got)
	}
	editor.HandleInput("\x15")
	if got := editor.GetText(); got != "" {
		t.Fatalf("5th U: %q", got)
	}
	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "line1\nline2\nline3" {
		t.Fatalf("accumulated multi-line: %q", got)
	}
}

func TestEditorKillRingForwardBackwardAccumulation(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("prefix|suffix")
	editor.HandleInput("\x01")
	for i := 0; i < 6; i++ {
		editor.HandleInput("\x1b[C")
	}

	editor.HandleInput("\x0b") // Ctrl+K deletes "suffix" (forward)
	editor.HandleInput("\x0b") // deletes "|" (forward, appended)
	if got := editor.GetText(); got != "prefix" {
		t.Fatalf("after K: %q", got)
	}

	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "prefix|suffix" {
		t.Fatalf("yank: %q", got)
	}
}

func TestEditorKillRingNonDeleteBreaksAccumulation(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("foo bar baz")
	editor.HandleInput("\x17")
	if got := editor.GetText(); got != "foo bar " {
		t.Fatalf("after W: %q", got)
	}

	editor.HandleInput("x")
	if got := editor.GetText(); got != "foo bar x" {
		t.Fatalf("typed: %q", got)
	}

	editor.HandleInput("\x17")
	if got := editor.GetText(); got != "foo bar " {
		t.Fatalf("second W: %q", got)
	}

	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "foo bar x" {
		t.Fatalf("yank most recent: %q", got)
	}

	editor.HandleInput("\x1by")
	if got := editor.GetText(); got != "foo bar baz" {
		t.Fatalf("pop to baz: %q", got)
	}
}

func TestEditorKillRingNonYankBreaksChain(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("first")
	editor.HandleInput("\x17")
	editor.SetText("second")
	editor.HandleInput("\x17")
	editor.SetText("")

	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "second" {
		t.Fatalf("yank: %q", got)
	}

	editor.HandleInput("x")
	if got := editor.GetText(); got != "secondx" {
		t.Fatalf("typed: %q", got)
	}

	editor.HandleInput("\x1by")
	if got := editor.GetText(); got != "secondx" {
		t.Fatalf("pop after break: %q", got)
	}
}

func TestEditorKillRingRotationPersists(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("first")
	editor.HandleInput("\x17")
	editor.SetText("second")
	editor.HandleInput("\x17")
	editor.SetText("")

	editor.HandleInput("\x19")  // second
	editor.HandleInput("\x1by") // first
	editor.HandleInput("\x1by") // second (wraps)

	// Ring order persists across further cycles.
	editor.HandleInput("\x1by")
	if got := editor.GetText(); got != "first" {
		t.Fatalf("rotation: %q", got)
	}
}

func TestEditorKillRingCrossLineCoalesce(t *testing.T) {
	// Reference scenario: delete everything with Ctrl+W from the end.
	editor := newTestEditor(80, 24)
	editor.SetText("1\n2\n3")
	editor.HandleInput("\x17") // deletes "3"
	if got := editor.GetText(); got != "1\n2\n" {
		t.Fatalf("1st W: %q", got)
	}
	editor.HandleInput("\x17") // deletes newline (merge)
	if got := editor.GetText(); got != "1\n2" {
		t.Fatalf("2nd W: %q", got)
	}
	editor.HandleInput("\x17") // deletes "2"
	if got := editor.GetText(); got != "1\n" {
		t.Fatalf("3rd W: %q", got)
	}
	editor.HandleInput("\x17") // deletes newline
	if got := editor.GetText(); got != "1" {
		t.Fatalf("4th W: %q", got)
	}
	editor.HandleInput("\x17") // deletes "1"
	if got := editor.GetText(); got != "" {
		t.Fatalf("5th W: %q", got)
	}
	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "1\n2\n3" {
		t.Fatalf("coalesced: %q", got)
	}
}

func TestEditorCtrlKAtLineEndDeletesNewline(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("line1\nline2")
	// Move to end of line1.
	editor.HandleInput("\x1b[A")
	editor.HandleInput("\x0b") // Ctrl+K at end deletes the newline
	if got := editor.GetText(); got != "line1line2" {
		t.Fatalf("after K: %q", got)
	}
}

func TestEditorYankInMiddle(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("hello world")
	editor.HandleInput("\x17") // deletes "world"
	editor.SetText("aaabbb")   // cursor at end

	// Move to middle (col 3).
	editor.HandleInput("\x01")
	editor.HandleInput("\x1b[C")
	editor.HandleInput("\x1b[C")
	editor.HandleInput("\x1b[C")
	editor.HandleInput("\x19") // yank "world" in middle
	if got := editor.GetText(); got != "aaaworldbbb" {
		t.Fatalf("middle yank: %q", got)
	}
}

func TestEditorYankPopInMiddle(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("first")
	editor.HandleInput("\x17")
	editor.SetText("second")
	editor.HandleInput("\x17")
	editor.SetText("aaabbb")

	editor.HandleInput("\x01")
	editor.HandleInput("\x1b[C")
	editor.HandleInput("\x1b[C")
	editor.HandleInput("\x1b[C")
	editor.HandleInput("\x19") // yank "second" -> "aaasecondbbb"
	if got := editor.GetText(); got != "aaasecondbbb" {
		t.Fatalf("yank: %q", got)
	}
	editor.HandleInput("\x1by") // pop to "first"
	if got := editor.GetText(); got != "aaafirstbbb" {
		t.Fatalf("pop: %q", got)
	}
}

func TestEditorMultilineYankAndPop(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("l1\nl2\nl3")
	editor.HandleInput("\x17") // deletes "l3"
	editor.SetText("l4")
	editor.HandleInput("\x17") // deletes "l4"
	editor.SetText("xx")

	editor.HandleInput("\x01")
	editor.HandleInput("\x19") // yank "l4"
	if got := editor.GetText(); got != "l4xx" {
		t.Fatalf("yank: %q", got)
	}
	editor.HandleInput("\x1by") // pop to "l3"
	if got := editor.GetText(); got != "l3xx" {
		t.Fatalf("pop: %q", got)
	}
}

func TestEditorAltDDeletesForward(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("foo bar baz")
	editor.HandleInput("\x01")
	editor.HandleInput("\x1bd") // Alt+D deletes "foo"
	if got := editor.GetText(); got != " bar baz" {
		t.Fatalf("after Alt+D: %q", got)
	}
	editor.HandleInput("\x19")
	if got := editor.GetText(); got != "foo bar baz" {
		t.Fatalf("yank: %q", got)
	}
}

func TestEditorAltDAtEndDeletesNewline(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("line1\nline2")
	editor.HandleInput("\x1b[A") // end of line1
	editor.HandleInput("\x1bd")  // Alt+D at end merges lines
	if got := editor.GetText(); got != "line1line2" {
		t.Fatalf("after Alt+D: %q", got)
	}
}

func TestEditorUndoBasics(t *testing.T) {
	editor := newTestEditor(80, 24)

	// Empty stack: no-op on a fresh editor.
	editor.HandleInput("\x1b[45;5u") // Ctrl+- (undo)
	if got := editor.GetText(); got != "" {
		t.Fatalf("empty undo: %q", got)
	}

	// Coalescing: word chars one unit.
	editor = newTestEditor(80, 24)
	editor.HandleInput("h")
	editor.HandleInput("e")
	editor.HandleInput("l")
	editor.HandleInput("l")
	editor.HandleInput("o")
	editor.HandleInput(" ")
	editor.HandleInput("w")
	editor.HandleInput("o")
	editor.HandleInput("r")
	editor.HandleInput("l")
	editor.HandleInput("d")
	if got := editor.GetText(); got != "hello world" {
		t.Fatalf("typed: %q", got)
	}
	editor.HandleInput("\x1b[45;5u")
	if got := editor.GetText(); got != "hello" {
		t.Fatalf("undo word group: %q", got)
	}
	editor.HandleInput("\x1b[45;5u")
	if got := editor.GetText(); got != "" {
		t.Fatalf("undo coalesced: %q", got)
	}
}

func TestEditorUndoBackspace(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.HandleInput("h")
	editor.HandleInput("e")
	editor.HandleInput("l")
	editor.HandleInput("l")
	editor.HandleInput("o")
	editor.HandleInput("\x7f")
	if got := editor.GetText(); got != "hell" {
		t.Fatalf("after backspace: %q", got)
	}
	editor.HandleInput("\x1b[45;5u")
	if got := editor.GetText(); got != "hello" {
		t.Fatalf("undo: %q", got)
	}
}

func TestEditorUndoForwardDelete(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.HandleInput("h")
	editor.HandleInput("e")
	editor.HandleInput("l")
	editor.HandleInput("l")
	editor.HandleInput("o")
	editor.HandleInput("\x01")
	editor.HandleInput("\x1b[C")
	editor.HandleInput("\x1b[3~") // Delete key
	if got := editor.GetText(); got != "hllo" {
		t.Fatalf("after delete: %q", got)
	}
	editor.HandleInput("\x1b[45;5u")
	if got := editor.GetText(); got != "hello" {
		t.Fatalf("undo: %q", got)
	}
}

func TestEditorUndoCtrlW(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.HandleInput("h")
	editor.HandleInput("e")
	editor.HandleInput("l")
	editor.HandleInput("l")
	editor.HandleInput("o")
	editor.HandleInput(" ")
	editor.HandleInput("w")
	editor.HandleInput("o")
	editor.HandleInput("r")
	editor.HandleInput("l")
	editor.HandleInput("d")
	editor.HandleInput("\x17") // Ctrl+W deletes "world"
	if got := editor.GetText(); got != "hello " {
		t.Fatalf("after W: %q", got)
	}
	editor.HandleInput("\x1b[45;5u")
	if got := editor.GetText(); got != "hello world" {
		t.Fatalf("undo: %q", got)
	}
}

func TestEditorUndoNewline(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.HandleInput("l")
	editor.HandleInput("1")
	editor.HandleInput("\x1b[13;2u") // shift+enter
	editor.HandleInput("l")
	editor.HandleInput("2")
	if got := editor.GetText(); got != "l1\nl2" {
		t.Fatalf("typed: %q", got)
	}
	editor.HandleInput("\x1b[45;5u")
	if got := editor.GetText(); got != "l1\n" {
		t.Fatalf("undo l2: %q", got)
	}
	editor.HandleInput("\x1b[45;5u")
	if got := editor.GetText(); got != "l1" {
		t.Fatalf("undo newline: %q", got)
	}
	editor.HandleInput("\x1b[45;5u")
	if got := editor.GetText(); got != "" {
		t.Fatalf("undo l1: %q", got)
	}
}

func TestEditorUndoPaste(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.HandleInput("a")
	editor.HandleInput("\x1b[200~pasted content\x1b[201~")
	if got := editor.GetText(); got != "apasted content" {
		t.Fatalf("pasted: %q", got)
	}
	editor.HandleInput("\x1b[45;5u")
	if got := editor.GetText(); got != "a" {
		t.Fatalf("undo paste: %q", got)
	}
}

func TestEditorUndoSubmitClears(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.HandleInput("h")
	editor.HandleInput("i")
	editor.HandleInput("\r")
	if got := editor.GetText(); got != "" {
		t.Fatalf("after submit: %q", got)
	}
	// Undo stack cleared by submit.
	editor.HandleInput("\x1b[45;5u")
	if got := editor.GetText(); got != "" {
		t.Fatalf("undo after submit: %q", got)
	}
}
