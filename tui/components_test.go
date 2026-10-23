package tui

import (
	"strings"
	"testing"
)

// Ported from select-list.test.ts, settings-list.test.ts, and
// truncated-text.test.ts.

var identitySelectTheme = SelectListTheme{
	SelectedPrefix: func(text string) string { return text },
	SelectedText:   func(text string) string { return text },
	Description:    func(text string) string { return text },
	ScrollInfo:     func(text string) string { return text },
	NoMatch:        func(text string) string { return text },
}

func visibleIndexOf(t *testing.T, line, text string) int {
	t.Helper()
	index := strings.Index(line, text)
	if index == -1 {
		t.Fatalf("text %q not found in %q", text, line)
	}
	return VisibleWidth(line[:index])
}

func TestSelectListMultilineDescriptions(t *testing.T) {
	items := []SelectItem{
		{Value: "test", Label: "test", Description: "Line one\nLine two\nLine three"},
	}
	list := NewSelectList(items, 5, identitySelectTheme, SelectListLayoutOptions{})
	rendered := list.Render(100)

	if len(rendered) == 0 {
		t.Fatal("no output")
	}
	if strings.Contains(rendered[0], "\n") {
		t.Fatalf("newline survived: %q", rendered[0])
	}
	if !strings.Contains(rendered[0], "Line one Line two Line three") {
		t.Fatalf("description = %q", rendered[0])
	}
}

func TestSelectListDescriptionAlignment(t *testing.T) {
	items := []SelectItem{
		{Value: "short", Label: "short", Description: "short description"},
		{Value: "very-long-command-name-that-needs-truncation", Label: "very-long-command-name-that-needs-truncation", Description: "long description"},
	}
	list := NewSelectList(items, 5, identitySelectTheme, SelectListLayoutOptions{})
	rendered := list.Render(80)

	if visibleIndexOf(t, rendered[0], "short description") != visibleIndexOf(t, rendered[1], "long description") {
		t.Fatalf("misaligned: %q vs %q", rendered[0], rendered[1])
	}
}

func TestSelectListColumnBounds(t *testing.T) {
	// Minimum primary column width.
	items := []SelectItem{
		{Value: "a", Label: "a", Description: "first"},
		{Value: "bb", Label: "bb", Description: "second"},
	}
	minWidth, maxWidth := 12, 20
	list := NewSelectList(items, 5, identitySelectTheme, SelectListLayoutOptions{
		MinPrimaryColumnWidth: &minWidth,
		MaxPrimaryColumnWidth: &maxWidth,
	})
	rendered := list.Render(80)
	if got := visibleIndexOf(t, rendered[0], "first"); got != 14 {
		t.Fatalf("min: first at %d, want 14", got)
	}
	if got := visibleIndexOf(t, rendered[1], "second"); got != 14 {
		t.Fatalf("min: second at %d, want 14", got)
	}

	// Maximum primary column width.
	items = []SelectItem{
		{Value: "very-long-command-name-that-needs-truncation", Label: "very-long-command-name-that-needs-truncation", Description: "first"},
		{Value: "short", Label: "short", Description: "second"},
	}
	list = NewSelectList(items, 5, identitySelectTheme, SelectListLayoutOptions{
		MinPrimaryColumnWidth: &minWidth,
		MaxPrimaryColumnWidth: &maxWidth,
	})
	rendered = list.Render(80)
	if got := visibleIndexOf(t, rendered[0], "first"); got != 22 {
		t.Fatalf("max: first at %d, want 22", got)
	}
	if got := visibleIndexOf(t, rendered[1], "second"); got != 22 {
		t.Fatalf("max: second at %d, want 22", got)
	}
}

func TestSelectListCustomTruncation(t *testing.T) {
	items := []SelectItem{
		{Value: "very-long-command-name-that-needs-truncation", Label: "very-long-command-name-that-needs-truncation", Description: "first"},
		{Value: "short", Label: "short", Description: "second"},
	}
	minWidth, maxWidth := 12, 12
	list := NewSelectList(items, 5, identitySelectTheme, SelectListLayoutOptions{
		MinPrimaryColumnWidth: &minWidth,
		MaxPrimaryColumnWidth: &maxWidth,
		TruncatePrimary: func(context SelectListTruncatePrimaryContext) string {
			if len(context.Text) <= context.MaxWidth {
				return context.Text
			}
			return context.Text[:maxInt(0, context.MaxWidth-1)] + "…"
		},
	})
	rendered := list.Render(80)
	if visibleIndexOf(t, rendered[0], "first") != visibleIndexOf(t, rendered[1], "second") {
		t.Fatalf("misaligned with custom truncation: %q vs %q", rendered[0], rendered[1])
	}
	if !strings.Contains(rendered[0], "…") {
		t.Fatalf("custom ellipsis missing: %q", rendered[0])
	}
}

var identitySettingsTheme = SettingsListTheme{
	Label:       func(text string, selected bool) string { return text },
	Value:       func(text string, selected bool) string { return text },
	Description: func(text string) string { return text },
	Cursor:      "> ",
	Hint:        func(text string) string { return text },
}

func testSettingItems() []SettingItem {
	return []SettingItem{{
		ID:           "tui-mode",
		Label:        "TUI mode",
		CurrentValue: "regular",
		Values:       []string{"regular", "fullscreen"},
	}}
}

func TestSettingsListSearchVsSpace(t *testing.T) {
	var changes []struct{ id, value string }
	list := NewSettingsList(testSettingItems(), 10, identitySettingsTheme,
		func(id, value string) { changes = append(changes, struct{ id, value string }{id, value}) },
		func() {}, SettingsListOptions{EnableSearch: true})

	for _, character := range "TUI mode" {
		list.HandleInput(string(character))
	}

	if len(changes) != 0 {
		t.Fatalf("search typed spaces changed settings: %v", changes)
	}
	rendered := list.Render(80)
	if !strings.Contains(rendered[0], "TUI mode") {
		t.Fatalf("search line = %q", rendered[0])
	}

	list.HandleInput("\r")
	if len(changes) != 1 || changes[0].id != "tui-mode" || changes[0].value != "fullscreen" {
		t.Fatalf("enter changes = %v", changes)
	}
}

func TestSettingsListSpaceShortcutBeforeQuery(t *testing.T) {
	var changes []struct{ id, value string }
	list := NewSettingsList(testSettingItems(), 10, identitySettingsTheme,
		func(id, value string) { changes = append(changes, struct{ id, value string }{id, value}) },
		func() {}, SettingsListOptions{EnableSearch: true})

	list.HandleInput(" ")

	if len(changes) != 1 || changes[0].id != "tui-mode" || changes[0].value != "fullscreen" {
		t.Fatalf("space changes = %v", changes)
	}
}

func TestTruncatedTextPadsToWidth(t *testing.T) {
	component := NewTruncatedText("hello", 0, 0)
	lines := component.Render(20)
	if len(lines) != 1 {
		t.Fatalf("lines = %d", len(lines))
	}
	if w := VisibleWidth(lines[0]); w != 20 {
		t.Fatalf("width = %d, want 20 (%q)", w, lines[0])
	}
}

func TestTruncatedTextVerticalPadding(t *testing.T) {
	component := NewTruncatedText("hi", 1, 2)
	lines := component.Render(10)
	if len(lines) != 5 {
		t.Fatalf("lines = %d", len(lines))
	}
	for i, line := range lines {
		if w := VisibleWidth(line); w != 10 {
			t.Fatalf("line %d width = %d (%q)", i, w, line)
		}
	}
}

func TestTruncatedTextTruncates(t *testing.T) {
	component := NewTruncatedText("a very long line of text", 0, 0)
	lines := component.Render(10)
	if w := VisibleWidth(lines[0]); w != 10 {
		t.Fatalf("width = %d (%q)", w, lines[0])
	}
	if !strings.Contains(lines[0], "...") {
		t.Fatalf("ellipsis missing: %q", lines[0])
	}
}

func TestTruncatedTextAnsiPreserved(t *testing.T) {
	component := NewTruncatedText("\x1b[31mred\x1b[0m", 0, 0)
	lines := component.Render(10)
	if !strings.Contains(lines[0], "\x1b[31m") {
		t.Fatalf("ANSI lost: %q", lines[0])
	}
	if w := VisibleWidth(lines[0]); w != 10 {
		t.Fatalf("width = %d (%q)", w, lines[0])
	}
}

func TestTruncatedTextStopsAtNewline(t *testing.T) {
	component := NewTruncatedText("first\nsecond", 0, 0)
	lines := component.Render(20)
	if strings.Contains(lines[0], "second") {
		t.Fatalf("second line leaked: %q", lines[0])
	}
	if strings.TrimRight(StripTerminalSequences(lines[0]), " ") != "first" {
		t.Fatalf("line = %q", lines[0])
	}
}

func TestTruncatedTextEmpty(t *testing.T) {
	component := NewTruncatedText("", 0, 0)
	lines := component.Render(10)
	if len(lines) != 1 || VisibleWidth(lines[0]) != 10 {
		t.Fatalf("lines = %q", lines)
	}
}

// Port of the fake-cursor resolution (1cedd3272): reverse video when
// shown, dropped when the hardware cursor sits on it.
func TestResolveFakeCursors(t *testing.T) {
	base := &TuiBase{}
	base.showHardwareCursor = false
	lines := []string{
		"plain line",
		CURSOR_MARKER + RenderFakeCursor("x") + " rest",
		RenderFakeCursor("y"),
	}
	resolved := base.resolveFakeCursors(lines)
	if resolved[0] != "plain line" {
		t.Fatalf("plain = %q", resolved[0])
	}
	if resolved[1] != CURSOR_MARKER+"\x1b[7mx\x1b[27m rest" {
		t.Fatalf("focused = %q", resolved[1])
	}
	if resolved[2] != "\x1b[7my\x1b[27m" {
		t.Fatalf("unfocused = %q", resolved[2])
	}
	base.showHardwareCursor = true
	resolved = base.resolveFakeCursors(lines)
	if resolved[1] != CURSOR_MARKER+"x rest" {
		t.Fatalf("hardware focused = %q", resolved[1])
	}
	if resolved[2] != "\x1b[7my\x1b[27m" {
		t.Fatalf("hardware unfocused = %q", resolved[2])
	}
}

// Port of the exit-restore behavior (9df819de3, adapted to Go signal
// handling): a TUI that never stops restores terminal modes on a fatal
// signal, and Stop disarms the restore.
type restoreCaptureTerminal struct {
	testTerminal
	restoreWrites []string
}

func (t *restoreCaptureTerminal) RestoreOnExit(before, after string) {
	t.restoreWrites = append(t.restoreWrites, before+"\x1b[?2004l"+after)
}

func TestExitRestoreArmedAndDisarmed(t *testing.T) {
	terminal := &restoreCaptureTerminal{}
	base := &TuiBase{terminalRef: terminal}
	base.Start()
	base.Stop(nil)
	// After Stop the restore is disarmed: a late signal writes nothing.
	base.restoreTerminalForExit()
	if len(terminal.restoreWrites) != 0 {
		t.Fatalf("writes after stop = %q", terminal.restoreWrites)
	}

	terminal2 := &restoreCaptureTerminal{}
	base2 := &TuiBase{terminalRef: terminal2}
	base2.Start()
	base2.restoreTerminalForExit()
	base2.Stop(nil)
	if len(terminal2.restoreWrites) != 1 {
		t.Fatalf("writes = %q", terminal2.restoreWrites)
	}
	if write := terminal2.restoreWrites[0]; write != "\x1b[?25h\x1b[?2004l" {
		t.Fatalf("restore = %q", write)
	}
}

func TestAltScreenExitRestoreSequences(t *testing.T) {
	terminal := &testTerminal{}
	screen := NewTuiAltScreen(terminal, nil, "", TuiAltScreenOptions{})
	screen.mu.Lock()
	screen.altScreenActive = true
	screen.mouseEnabled = true
	screen.mu.Unlock()
	prefix := screen.ExitRestorePrefix()
	wantPrefix := endSyncOutput + disableMouseSeq + enableAutowrap + "\x1b[?25h"
	if prefix != wantPrefix {
		t.Fatalf("prefix = %q, want %q", prefix, wantPrefix)
	}
	if suffix := screen.ExitRestoreSuffix(); suffix != exitAltScreen+"\x1b[?25h" {
		t.Fatalf("suffix = %q", suffix)
	}
	screen.mu.Lock()
	screen.altScreenActive = false
	screen.mu.Unlock()
	if prefix := screen.ExitRestorePrefix(); prefix != "\x1b[?25h" {
		t.Fatalf("inactive prefix = %q", prefix)
	}
	if suffix := screen.ExitRestoreSuffix(); suffix != "" {
		t.Fatalf("inactive suffix = %q", suffix)
	}
}
