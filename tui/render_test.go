package tui

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Ported from pi/packages/tui/test/tui-render.test.ts (render scheduling,
// resize, shrinkage, differential updates, style resets; the Kitty image
// cleanup batch lands with the Image component port).

// linesComponent is mutated by tests while the renderer reads it; the
// mutex keeps Render race-clean.
type linesComponent struct {
	mu    sync.Mutex
	lines []string
}

func (c *linesComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lines
}
func (c *linesComponent) setLines(lines []string) {
	c.mu.Lock()
	c.lines = lines
	c.mu.Unlock()
}
func (c *linesComponent) Invalidate() {}

type inputLinesComponent struct {
	linesComponent
	renderMu    sync.Mutex
	renderCount int
}

func (c *inputLinesComponent) renderCountValue() int {
	c.renderMu.Lock()
	defer c.renderMu.Unlock()
	return c.renderCount
}

func (c *inputLinesComponent) Render(width int) []string {
	c.renderMu.Lock()
	c.renderCount++
	c.renderMu.Unlock()
	return c.lines
}

func (c *inputLinesComponent) HandleInput(data string) {
	c.setLines([]string{data})
}

func waitForRender() {
	time.Sleep(25 * time.Millisecond)
}

func TestRenderSchedulingInputPreemptsThrottledFrame(t *testing.T) {
	terminal := newVirtualTerminal(40, 10)
	tui := NewTuiMainScreen(terminal, nil, "")
	component := &inputLinesComponent{}
	component.setLines([]string{"initial"})
	tui.AddChild(component)
	tui.SetFocus(component)
	tui.Start()
	defer tui.Stop(nil)
	tui.RenderNow(false)
	renderCountBeforeInput := component.renderCountValue()

	// Queue a throttled render first; keyboard input must preempt it.
	component.setLines([]string{"pending"})
	tui.RequestRender(false)
	terminal.SendInput("first")
	terminal.SendInput("second")
	terminal.SendInput("typed")
	time.Sleep(30 * time.Millisecond)

	got := component.renderCountValue()
	if got < renderCountBeforeInput+1 {
		t.Fatalf("renderCount = %d, want at least %d", got, renderCountBeforeInput+1)
	}
	if len(component.lines) != 1 || component.lines[0] != "typed" {
		t.Fatalf("lines = %q", component.lines)
	}
}

func TestRenderHeightChangeTriggersFullRedraw(t *testing.T) {
	withEnvVars(t, map[string]string{"TERMUX_VERSION": ""}, func() {
		terminal := newVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, "")
		component := &linesComponent{}
		tui.AddChild(component)

		component.setLines([]string{"Line 0", "Line 1", "Line 2"})
		tui.Start()
		defer tui.Stop(nil)
		waitForRender()

		initialRedraws := tui.FullRedraws()
		terminal.Resize(40, 15)
		waitForRender()

		if tui.FullRedraws() <= initialRedraws {
			t.Fatal("height change should trigger full redraw")
		}
		viewport := terminal.GetViewport()
		if !strings.Contains(viewport[0], "Line 0") {
			t.Fatalf("content lost after height change: %q", viewport)
		}
	})
}

func TestRenderHeightChangeSkipsFullRedrawInTermux(t *testing.T) {
	withEnvVars(t, map[string]string{"TERMUX_VERSION": "1"}, func() {
		terminal := newVirtualTerminal(40, 10)
		tui := NewTuiMainScreen(terminal, nil, "")
		component := &linesComponent{}
		tui.AddChild(component)

		lines := make([]string, 20)
		for i := range lines {
			lines[i] = "Line " + itoa(i)
		}
		component.setLines(lines)
		tui.Start()
		defer tui.Stop(nil)
		waitForRender()

		initialRedraws := tui.FullRedraws()
		for _, height := range []int{15, 8, 14, 11} {
			terminal.Resize(40, height)
			waitForRender()
		}

		if tui.FullRedraws() != initialRedraws {
			t.Fatal("height change triggered full redraw in Termux")
		}
		written := terminal.Written()
		if strings.Contains(written, "\x1b[2J") || strings.Contains(written, "\x1b[3J") {
			t.Fatal("height change cleared the screen in Termux")
		}
		viewport := terminal.GetViewport()
		if !strings.Contains(strings.Join(viewport, "\n"), "Line 19") {
			t.Fatalf("latest content not visible after resize: %q", viewport)
		}
	})
}

func TestRenderWidthChangeTriggersFullRedraw(t *testing.T) {
	terminal := newVirtualTerminal(40, 10)
	tui := NewTuiMainScreen(terminal, nil, "")
	component := &linesComponent{}
	tui.AddChild(component)

	component.setLines([]string{"Line 0", "Line 1", "Line 2"})
	tui.Start()
	defer tui.Stop(nil)
	waitForRender()

	initialRedraws := tui.FullRedraws()
	terminal.Resize(60, 10)
	waitForRender()

	if tui.FullRedraws() <= initialRedraws {
		t.Fatal("width change should trigger full redraw")
	}
}

func TestRenderShrinkClearsEmptyRows(t *testing.T) {
	terminal := newVirtualTerminal(40, 10)
	tui := NewTuiMainScreen(terminal, nil, "")
	tui.SetClearOnShrink(true)
	component := &linesComponent{}
	tui.AddChild(component)

	component.setLines([]string{"Line 0", "Line 1", "Line 2", "Line 3", "Line 4", "Line 5"})
	tui.Start()
	defer tui.Stop(nil)
	waitForRender()

	initialRedraws := tui.FullRedraws()

	component.setLines([]string{"Line 0", "Line 1"})
	tui.RequestRender(false)
	waitForRender()

	if tui.FullRedraws() <= initialRedraws {
		t.Fatal("shrink should trigger full redraw")
	}
	viewport := terminal.GetViewport()
	if !strings.Contains(viewport[0], "Line 0") || !strings.Contains(viewport[1], "Line 1") {
		t.Fatalf("preserved lines lost: %q", viewport)
	}
	if strings.TrimSpace(viewport[2]) != "" || strings.TrimSpace(viewport[3]) != "" {
		t.Fatalf("rows below not cleared: %q", viewport)
	}
}

func TestRenderShrinkToSingleLine(t *testing.T) {
	terminal := newVirtualTerminal(40, 10)
	tui := NewTuiMainScreen(terminal, nil, "")
	tui.SetClearOnShrink(true)
	component := &linesComponent{}
	tui.AddChild(component)

	component.setLines([]string{"Line 0", "Line 1", "Line 2", "Line 3"})
	tui.Start()
	defer tui.Stop(nil)
	waitForRender()

	component.setLines([]string{"Only line"})
	tui.RequestRender(false)
	waitForRender()

	viewport := terminal.GetViewport()
	if !strings.Contains(viewport[0], "Only line") {
		t.Fatalf("single line lost: %q", viewport)
	}
	for i := 1; i < len(viewport); i++ {
		if strings.TrimSpace(viewport[i]) != "" {
			t.Fatalf("row %d not cleared: %q", i, viewport[i])
		}
	}
}

func TestRenderShrinkToEmpty(t *testing.T) {
	terminal := newVirtualTerminal(40, 10)
	tui := NewTuiMainScreen(terminal, nil, "")
	tui.SetClearOnShrink(true)
	component := &linesComponent{}
	tui.AddChild(component)

	component.setLines([]string{"Line 0", "Line 1", "Line 2"})
	tui.Start()
	defer tui.Stop(nil)
	waitForRender()

	component.setLines(nil)
	tui.RequestRender(false)
	waitForRender()

	viewport := terminal.GetViewport()
	for i := range viewport {
		if strings.TrimSpace(viewport[i]) != "" {
			t.Fatalf("row %d not cleared: %q", i, viewport[i])
		}
	}
}

func TestRenderMiddleLineChange(t *testing.T) {
	terminal := newVirtualTerminal(40, 10)
	tui := NewTuiMainScreen(terminal, nil, "")
	component := &linesComponent{}
	tui.AddChild(component)

	component.setLines([]string{"alpha", "beta", "gamma", "delta"})
	tui.Start()
	defer tui.Stop(nil)
	waitForRender()

	// Spinner case: only one middle line changes.
	writesBefore := terminal.writesCount()
	component.setLines([]string{"alpha", "beta", "GAMMA", "delta"})
	tui.RequestRender(false)
	waitForRender()

	if terminal.writesCount() <= writesBefore {
		t.Fatal("no update written")
	}
	viewport := terminal.GetViewport()
	want := []string{"alpha", "beta", "GAMMA", "delta"}
	for i, w := range want {
		if !strings.Contains(viewport[i], w) {
			t.Fatalf("viewport[%d] = %q, want %q", i, viewport[i], w)
		}
	}
}

func TestRenderFirstLineChangesRestSame(t *testing.T) {
	terminal := newVirtualTerminal(40, 10)
	tui := NewTuiMainScreen(terminal, nil, "")
	component := &linesComponent{}
	tui.AddChild(component)

	component.setLines([]string{"first", "second", "third"})
	tui.Start()
	defer tui.Stop(nil)
	waitForRender()

	component.setLines([]string{"FIRST", "second", "third"})
	tui.RequestRender(false)
	waitForRender()

	viewport := terminal.GetViewport()
	want := []string{"FIRST", "second", "third"}
	for i, w := range want {
		if !strings.Contains(viewport[i], w) {
			t.Fatalf("viewport[%d] = %q, want %q", i, viewport[i], w)
		}
	}
}

func TestRenderAppendedLinesScrollIntoView(t *testing.T) {
	terminal := newVirtualTerminal(20, 5)
	tui := NewTuiMainScreen(terminal, nil, "")
	component := &linesComponent{}
	tui.AddChild(component)

	component.setLines([]string{"one", "two", "three"})
	tui.Start()
	defer tui.Stop(nil)
	waitForRender()

	for i := 0; i < 8; i++ {
		component.appendLine("line " + itoa(i))
		tui.RequestRender(false)
		waitForRender()
	}

	viewport := terminal.GetViewport()
	joined := strings.Join(viewport, "\n")
	if !strings.Contains(joined, "line 7") {
		t.Fatalf("appended content not visible: %q", viewport)
	}
}

func TestRenderResetsStylesAfterEachLine(t *testing.T) {
	terminal := newVirtualTerminal(40, 5)
	tui := NewTuiMainScreen(terminal, nil, "")
	component := &linesComponent{lines: []string{"\x1b[31mred\x1b[0m", "plain"}}
	tui.AddChild(component)

	tui.Start()
	defer tui.Stop(nil)
	waitForRender()

	written := terminal.Written()
	lines := strings.Split(written, "\r\n")
	if len(lines) < 2 {
		t.Fatalf("expected multiple lines: %q", written)
	}
	for _, line := range lines[:2] {
		if strings.Contains(line, "\x1b[31m") && !strings.Contains(line, "\x1b[0m") {
			t.Fatalf("style leak without reset: %q", line)
		}
	}
}

var _ = os.Getenv

func (c *linesComponent) appendLine(line string) {
	c.mu.Lock()
	c.lines = append(c.lines, line)
	c.mu.Unlock()
}
