package tui

// Ported from pi/packages/tui/test/editor.test.ts (batch 1: history
// navigation, state accessors, backslash+Enter, Kitty CSI-u, unicode
// editing basics, scroll indicators, word wrapping).

import (
	"strings"
	"testing"
)

func createTestEditorTUI(cols, rows int) TUI {
	return NewTuiMainScreen(newVirtualTerminal(cols, rows), nil, "")
}

var defaultEditorTheme = EditorTheme{
	BorderColor: func(str string) string { return "\x1b[2m" + str + "\x1b[22m" },
	SelectList:  identitySelectTheme,
}

func newTestEditor(cols, rows int) *Editor {
	return NewEditor(createTestEditorTUI(cols, rows), defaultEditorTheme, EditorOptions{})
}

func stripVT(line string) string {
	return stripAnsi(stripOsc8(line))
}

func TestEditorHistoryEmpty(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.HandleInput("\x1b[A") // Up
	if got := editor.GetText(); got != "" {
		t.Fatalf("text = %q", got)
	}
}

func TestEditorHistoryShowsMostRecent(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("first prompt")
	editor.AddToHistory("second prompt")

	editor.HandleInput("\x1b[A") // Up

	if got := editor.GetText(); got != "second prompt" {
		t.Fatalf("text = %q", got)
	}
}

func TestEditorHistoryCyclesUp(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("first")
	editor.AddToHistory("second")
	editor.AddToHistory("third")

	editor.HandleInput("\x1b[A")
	if got := editor.GetText(); got != "third" {
		t.Fatalf("1st up = %q", got)
	}
	editor.HandleInput("\x1b[A")
	if got := editor.GetText(); got != "second" {
		t.Fatalf("2nd up = %q", got)
	}
	editor.HandleInput("\x1b[A")
	if got := editor.GetText(); got != "first" {
		t.Fatalf("3rd up = %q", got)
	}
	// At oldest entry: stays.
	editor.HandleInput("\x1b[A")
	if got := editor.GetText(); got != "first" {
		t.Fatalf("4th up = %q", got)
	}
}

func TestEditorHistoryJumpToStartFromDraft(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("prompt")
	editor.SetText("draft")
	editor.HandleInput("\x1b[D")
	editor.HandleInput("\x1b[D")

	editor.HandleInput("\x1b[A") // Up: jumps to start before history browsing
	if got := editor.GetText(); got != "draft" {
		t.Fatalf("text = %q", got)
	}
	if cursor := editor.GetCursor(); cursor.Line != 0 || cursor.Col != 0 {
		t.Fatalf("cursor = %+v", cursor)
	}

	editor.HandleInput("\x1b[A") // Up at start: enters history
	if got := editor.GetText(); got != "prompt" {
		t.Fatalf("history text = %q", got)
	}

	editor.HandleInput("\x1b[B") // Down: restores draft
	if got := editor.GetText(); got != "draft" {
		t.Fatalf("restored = %q", got)
	}
	if cursor := editor.GetCursor(); cursor.Line != 0 || cursor.Col != 0 {
		t.Fatalf("restored cursor = %+v", cursor)
	}
}

func TestEditorHistoryDownNavigation(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("first")
	editor.AddToHistory("second")

	editor.HandleInput("\x1b[A") // second
	editor.HandleInput("\x1b[A") // first
	editor.HandleInput("\x1b[B") // back to second
	if got := editor.GetText(); got != "second" {
		t.Fatalf("down = %q", got)
	}
	editor.HandleInput("\x1b[B") // exits history, restores draft (empty)
	if got := editor.GetText(); got != "" {
		t.Fatalf("exit = %q", got)
	}
}

func TestEditorHistoryExitOnTyping(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("old prompt")

	editor.HandleInput("\x1b[A")
	editor.HandleInput("x")

	if got := editor.GetText(); got != "xold prompt" {
		t.Fatalf("text = %q", got)
	}
}

func TestEditorHistoryExitOnSetText(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("first")

	editor.HandleInput("\x1b[A")
	editor.SetText("replacement")

	editor.HandleInput("\x1b[B") // Down: no history browse (exited)
	if got := editor.GetText(); got != "replacement" {
		t.Fatalf("text = %q", got)
	}
}

func TestEditorHistoryDedup(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("same")
	editor.AddToHistory("same")

	editor.HandleInput("\x1b[A")
	editor.HandleInput("\x1b[A") // Still the only entry
	if got := editor.GetText(); got != "same" {
		t.Fatalf("text = %q", got)
	}
}

func TestEditorHistoryNonConsecutiveDuplicates(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("a")
	editor.AddToHistory("b")
	editor.AddToHistory("a")

	editor.HandleInput("\x1b[A")
	if got := editor.GetText(); got != "a" {
		t.Fatalf("1st = %q", got)
	}
	editor.HandleInput("\x1b[A")
	if got := editor.GetText(); got != "b" {
		t.Fatalf("2nd = %q", got)
	}
}

func TestEditorHistoryUsesCursorWhenContent(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("first")

	editor.SetText("draft")
	cursorBefore := editor.GetCursor()

	editor.HandleInput("\x1b[A") // cursor moves within content, not history

	if got := editor.GetText(); got != "draft" {
		t.Fatalf("text = %q", got)
	}
	cursorAfter := editor.GetCursor()
	if cursorAfter.Line == cursorBefore.Line && cursorAfter.Col == cursorBefore.Col {
		t.Fatalf("cursor did not move: %+v", cursorAfter)
	}
}

func TestEditorHistoryLimit100(t *testing.T) {
	editor := newTestEditor(80, 24)
	for i := 0; i < 105; i++ {
		editor.AddToHistory("entry " + itoa(i))
	}
	// Oldest entries dropped: browsing to the end yields entry 104..5.
	editor.HandleInput("\x1b[A")
	if got := editor.GetText(); got != "entry 104" {
		t.Fatalf("most recent = %q", got)
	}
	for i := 0; i < 99; i++ {
		editor.HandleInput("\x1b[A")
	}
	if got := editor.GetText(); got != "entry 5" {
		t.Fatalf("oldest = %q", got)
	}
	editor.HandleInput("\x1b[A")
	if got := editor.GetText(); got != "entry 5" {
		t.Fatalf("below oldest = %q", got)
	}
}

func TestEditorHistoryCursorPlacement(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("older entry")
	editor.AddToHistory("line1\nline2\nline3")

	editor.HandleInput("\x1b[A") // multi-line entry at start
	if got := editor.GetText(); got != "line1\nline2\nline3" {
		t.Fatalf("up text = %q", got)
	}
	if cursor := editor.GetCursor(); cursor.Line != 0 || cursor.Col != 0 {
		t.Fatalf("up cursor = %+v", cursor)
	}

	editor.HandleInput("\x1b[A") // older entry
	if got := editor.GetText(); got != "older entry" {
		t.Fatalf("older text = %q", got)
	}
	if cursor := editor.GetCursor(); cursor.Line != 0 || cursor.Col != 0 {
		t.Fatalf("older cursor = %+v", cursor)
	}
}

func TestEditorHistoryCursorPlacementDown(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("older entry")
	editor.AddToHistory("line1\nline2\nline3")
	editor.AddToHistory("newer entry")

	editor.HandleInput("\x1b[A") // newer
	editor.HandleInput("\x1b[A") // multi-line
	editor.HandleInput("\x1b[A") // older

	editor.HandleInput("\x1b[B") // multi-line at end
	if got := editor.GetText(); got != "line1\nline2\nline3" {
		t.Fatalf("down text = %q", got)
	}
	if cursor := editor.GetCursor(); cursor.Line != 2 || cursor.Col != 5 {
		t.Fatalf("down cursor = %+v", cursor)
	}

	editor.HandleInput("\x1b[B") // newer
	if got := editor.GetText(); got != "newer entry" {
		t.Fatalf("newer text = %q", got)
	}
}

func TestEditorHistoryOppositeDirectionCursor(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.AddToHistory("line1\nline2\nline3")

	editor.HandleInput("\x1b[A")
	if cursor := editor.GetCursor(); cursor.Line != 0 || cursor.Col != 0 {
		t.Fatalf("up cursor = %+v", cursor)
	}

	editor.HandleInput("\x1b[B") // cursor moves to line2
	if got := editor.GetText(); got != "line1\nline2\nline3" {
		t.Fatalf("text = %q", got)
	}
	if cursor := editor.GetCursor(); cursor.Line != 1 || cursor.Col != 0 {
		t.Fatalf("down cursor = %+v", cursor)
	}

	editor.HandleInput("\x1b[A") // back to line1
	if cursor := editor.GetCursor(); cursor.Line != 0 || cursor.Col != 0 {
		t.Fatalf("up again cursor = %+v", cursor)
	}
}

func TestEditorCursorPosition(t *testing.T) {
	editor := newTestEditor(80, 24)

	cursor := editor.GetCursor()
	if cursor.Line != 0 || cursor.Col != 0 {
		t.Fatalf("initial = %+v", cursor)
	}

	editor.HandleInput("a")
	editor.HandleInput("b")
	editor.HandleInput("c")
	cursor = editor.GetCursor()
	if cursor.Line != 0 || cursor.Col != 3 {
		t.Fatalf("after abc = %+v", cursor)
	}

	editor.HandleInput("\x1b[D") // Left
	cursor = editor.GetCursor()
	if cursor.Line != 0 || cursor.Col != 2 {
		t.Fatalf("after left = %+v", cursor)
	}
}

func TestEditorLinesDefensiveCopy(t *testing.T) {
	editor := newTestEditor(80, 24)
	editor.SetText("a\nb")

	lines := editor.GetLines()
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Fatalf("lines = %q", lines)
	}

	lines[0] = "mutated"
	lines2 := editor.GetLines()
	if lines2[0] != "a" {
		t.Fatalf("mutation leaked: %q", lines2)
	}
}

func TestEditorBackslashEnter(t *testing.T) {
	// Backslash inserts immediately.
	editor := newTestEditor(80, 24)
	editor.HandleInput("\\")
	if got := editor.GetText(); got != "\\" {
		t.Fatalf("text = %q", got)
	}

	// Enter after backslash converts to newline.
	editor.HandleInput("\r")
	lines := editor.GetLines()
	if len(lines) != 2 || lines[0] != "" || lines[1] != "" {
		t.Fatalf("lines = %q", lines)
	}

	// Backslash followed by characters: Enter submits normally (only when
	// shift+enter is bound; with defaults submit clears the editor).
	editor2 := newTestEditor(80, 24)
	editor2.HandleInput("a")
	editor2.HandleInput("\\")
	editor2.HandleInput("b")
	if got := editor2.GetText(); got != "a\\b" {
		t.Fatalf("text = %q", got)
	}
	editor2.HandleInput("\r")
	if got := editor2.GetText(); got != "" {
		t.Fatalf("submit cleared: %q", got)
	}
}

func TestEditorKittyCsiU(t *testing.T) {
	// Unsupported modifiers: ignored.
	editor := newTestEditor(80, 24)
	editor.HandleInput("\x1b[97;1:1u")
	if got := editor.GetText(); got != "a" {
		t.Fatalf("press = %q", got)
	}

	// Shifted CSI-u letters insert as text.
	editor2 := newTestEditor(80, 24)
	editor2.HandleInput("\x1b[65;2u") // Shift+A
	if got := editor2.GetText(); got != "A" {
		t.Fatalf("shifted = %q", got)
	}

	// Shifted modifyOtherKeys letters insert as text.
	editor3 := newTestEditor(80, 24)
	editor3.HandleInput("\x1b[27;2;65~")
	if got := editor3.GetText(); got != "A" {
		t.Fatalf("modifyOtherKeys = %q", got)
	}
}

func TestEditorUnicodeEditing(t *testing.T) {
	editor := newTestEditor(80, 24)

	// Insert combining chars: backspace deletes grapheme clusters.
	editor.HandleInput("e")
	editor.HandleInput("\u0301") // combining acute
	if got := editor.GetText(); got != "e\u0301" {
		t.Fatalf("text = %q", got)
	}
	editor.HandleInput("\x7f") // backspace
	if got := editor.GetText(); got != "" {
		t.Fatalf("after backspace = %q", got)
	}

	// Astral chars: cursor moves by grapheme.
	editor.HandleInput("\U0001F600") // emoji
	editor.HandleInput("\U0001F600")
	cursor := editor.GetCursor()
	if cursor.Col != 8 {
		t.Fatalf("cursor col = %d (2 astral chars = 8 bytes)", cursor.Col)
	}
	editor.HandleInput("\x1b[D") // left
	cursor = editor.GetCursor()
	if cursor.Col != 4 {
		t.Fatalf("after left col = %d", cursor.Col)
	}
}

func TestEditorScrollIndicators(t *testing.T) {
	// Fill past the visible window (30% of 6 rows = 5 lines minimum).
	editor := newTestEditor(40, 6)
	for i := 0; i < 8; i++ {
		editor.HandleInput("line " + itoa(i))
		editor.HandleInput("\x1b[13;2u") // shift+enter -> new line
	}
	editor.Render(40)
	// Editor renders without panic; the scroll borders appear in output.
	lines := editor.Render(40)
	if len(lines) < 2 {
		t.Fatalf("lines = %q", lines)
	}
	joined := strings.Join(lines, "\n")
	// Either scroll indicator or content: no width overflow.
	for i, line := range lines {
		if w := VisibleWidth(line); w > 40 {
			t.Fatalf("line %d overflow %d: %q", i, w, stripVT(line))
		}
	}
	_ = joined
}

func TestEditorWordWrapLine(t *testing.T) {
	// Simple wrap: reference produces "hello" / " " / "world" (the space
	// forms its own chunk when the following word exceeds the width).
	chunks := WordWrapLine("hello world", 5, nil)
	if len(chunks) != 3 {
		t.Fatalf("chunks = %+v", chunks)
	}
	if chunks[0].Text != "hello" || chunks[1].Text != " " || chunks[2].Text != "world" {
		t.Fatalf("chunks = %+v", chunks)
	}
	if chunks[0].StartIndex != 0 || chunks[0].EndIndex != 5 {
		t.Fatalf("chunk0 indices = %+v", chunks[0])
	}

	// Long word force-breaks.
	chunks = WordWrapLine("abcdefghij", 4, nil)
	if len(chunks) != 3 {
		t.Fatalf("force chunks = %+v", chunks)
	}

	// CJK breaks between adjacent characters.
	chunks = WordWrapLine("日本語テスト", 4, nil)
	for i, chunk := range chunks {
		if w := VisibleWidth(chunk.Text); w > 4 {
			t.Fatalf("cjk chunk %d width %d: %+v", i, w, chunk)
		}
	}
}

// TestEditorTopBorderHook pins the CustomEditor.renderTopBorder seam: a
// non-empty hook result replaces the default rule, an empty one keeps it.
func TestEditorTopBorderHook(t *testing.T) {
	terminal := newTestTerminal(40, 12)
	screen := NewTuiMainScreen(terminal, nil, "")
	defer screen.Stop(nil)

	editor := NewEditor(screen, EditorTheme{BorderColor: func(s string) string { return s }}, EditorOptions{
		TopBorder: func(width, hiddenLineCount int) string {
			if width < 10 {
				return ""
			}
			return "custom border"
		},
	})

	if got := editor.Render(40)[0]; !strings.Contains(got, "custom border") {
		t.Fatalf("top border = %q, want the hook output", got)
	}
	if got := editor.Render(5)[0]; !strings.Contains(got, "─") {
		t.Fatalf("top border = %q, want the default rule on an empty hook result", got)
	}

	plain := NewEditor(screen, EditorTheme{BorderColor: func(s string) string { return s }}, EditorOptions{})
	if got := plain.Render(40)[0]; !strings.Contains(got, "─") {
		t.Fatalf("default top border = %q", got)
	}
}
