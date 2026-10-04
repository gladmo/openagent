package tui

import (
	"strings"
	"testing"
)

// Smoke: alt screen enters/leaves the alternate screen buffer, renders
// viewport rows, and scrolls with input.
func TestAltScreenSmoke(t *testing.T) {
	terminal := newVirtualTerminal(20, 5)
	screen := NewTuiAltScreen(terminal, nil, "", TuiAltScreenOptions{})
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "line " + itoa(i)
	}
	screen.AddChild(&linesComponent{lines: lines})
	screen.Start()
	defer screen.Stop(nil)
	waitForRender()

	written := terminal.Written()
	if !strings.Contains(written, "\x1b[?1049h") {
		t.Fatal("did not enter alternate screen")
	}
	viewport := terminal.GetViewport()
	joined := strings.Join(viewport, "\n")
	if !strings.Contains(joined, "line 11") {
		t.Fatalf("follow-end content not visible: %q", viewport)
	}
}

func TestAltScreenScrollInput(t *testing.T) {
	// Height 10: one page = viewportHeight - PAGE_SCROLL_OVERLAP = 6 lines,
	// so a single pageUp from scrollTop 2 clamps to the top.
	terminal := newVirtualTerminal(20, 10)
	screen := NewTuiAltScreen(terminal, nil, "", TuiAltScreenOptions{})
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "line " + itoa(i)
	}
	screen.AddChild(&linesComponent{lines: lines})
	screen.Start()
	defer screen.Stop(nil)
	waitForRender()

	// pageUp keybinding scrolls the primary viewport without deadlock.
	terminal.SendInput("\x1b[5~")
	waitForRender()
	joined := strings.Join(terminal.GetViewport(), "\n")
	if !strings.Contains(joined, "line 0") {
		t.Fatalf("pageUp did not scroll to top: %q", terminal.GetViewport())
	}

	// pageDown returns to the end.
	terminal.SendInput("\x1b[6~")
	waitForRender()
	joined = strings.Join(terminal.GetViewport(), "\n")
	if !strings.Contains(joined, "line 11") {
		t.Fatalf("pageDown did not follow end: %q", terminal.GetViewport())
	}
}

func TestAltScreenExitLeavesDocumentInMainScreen(t *testing.T) {
	terminal := newVirtualTerminal(20, 5)
	screen := NewTuiAltScreen(terminal, nil, "", TuiAltScreenOptions{})
	screen.AddChild(&linesComponent{lines: []string{"hello", "world"}})
	screen.Start()
	waitForRender()
	screen.Stop(nil)

	written := terminal.Written()
	if !strings.Contains(written, "\x1b[?1049l") {
		t.Fatal("did not leave alternate screen")
	}
	// The document replays into the main screen.
	scroll := terminal.GetScrollBuffer()
	joined := strings.Join(scroll, "\n")
	if !strings.Contains(joined, "hello") || !strings.Contains(joined, "world") {
		t.Fatalf("document not replayed: %q", scroll)
	}
}
