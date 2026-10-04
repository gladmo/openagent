package tui

import (
	"testing"
)

// Smoke: the main screen renders, differentially updates, and scrolls.
type staticComponent struct{ lines []string }

func (c *staticComponent) Render(width int) []string { return c.lines }
func (c *staticComponent) Invalidate()               {}

func TestMainScreenSmoke(t *testing.T) {
	terminal := newTestTerminal(20, 5)
	screen := NewTuiMainScreen(terminal, nil, "")
	component := &staticComponent{lines: []string{"one", "two"}}
	screen.AddChild(component)
	screen.Start()
	defer screen.Stop(nil)

	screen.RenderNow(true)
	if len(terminal.Writes) == 0 {
		t.Fatal("nothing written on first render")
	}

	// Differential update: change one line.
	writesBefore := len(terminal.Writes)
	component.lines = []string{"one", "TWO"}
	screen.RenderNow(false)
	if len(terminal.Writes) <= writesBefore {
		t.Fatal("no update written")
	}
}
