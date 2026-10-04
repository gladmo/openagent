package tui

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TuiBase core tests, ported from the TUI-level cases in
// terminal-colors.test.ts plus the overlay/focus/input paths of tui.ts. The
// screen stub stands in for TuiMainScreen (ported with the screens batch).

type testTerminal struct {
	mu            sync.Mutex
	inputHandler  func(data string)
	resizeHandler func()
	columnCount   int
	rowCount      int
	Writes        []string
}

func newTestTerminal(columns, rows int) *testTerminal {
	return &testTerminal{columnCount: columns, rowCount: rows}
}

func (t *testTerminal) Start(onInput func(string), onResize func()) {
	t.mu.Lock()
	t.inputHandler = onInput
	t.resizeHandler = onResize
	t.mu.Unlock()
}

func (t *testTerminal) Stop() {
	t.mu.Lock()
	t.inputHandler = nil
	t.resizeHandler = nil
	t.mu.Unlock()
}

func (t *testTerminal) DrainInput(maxMs, idleMs time.Duration) {}

func (t *testTerminal) Write(data string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Writes = append(t.Writes, data)
}

func (t *testTerminal) Columns() int              { return t.columnCount }
func (t *testTerminal) Rows() int                 { return t.rowCount }
func (t *testTerminal) KittyProtocolActive() bool { return false }
func (t *testTerminal) MoveBy(lines int)          {}
func (t *testTerminal) HideCursor()               {}
func (t *testTerminal) ShowCursor()               {}
func (t *testTerminal) ClearLine()                {}
func (t *testTerminal) ClearFromCursor()          {}
func (t *testTerminal) ClearScreen()              {}
func (t *testTerminal) SetTitle(title string)     {}
func (t *testTerminal) SetProgress(active bool)   {}

func (t *testTerminal) SendInput(data string) {
	t.mu.Lock()
	handler := t.inputHandler
	t.mu.Unlock()
	if handler != nil {
		handler(data)
	}
}

func (t *testTerminal) Written() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.Writes, "")
}

// minimalScreen is a TuiBase-only TUI: doRender composites overlays.
type minimalScreen struct {
	TuiBase
	lastRender *[]string
}

func newMinimalScreen(terminal Terminal) *minimalScreen {
	screen := &minimalScreen{}
	screen.InitTuiBase(terminal, nil)
	screen.ModeField = TuiModeRegular
	lastRender := []string{}
	screen.DoRender = func() {
		screen.mu.Lock()
		defer screen.mu.Unlock()
		lines := screen.Render(screen.terminalRef.Columns())
		lines = screen.CompositeOverlays(lines, screen.terminalRef.Columns(), screen.terminalRef.Rows())
		lines = screen.ApplyLineResets(lines)
		lastRender = lines
	}
	screen.lastRender = &lastRender
	return screen
}

// inputRecorder records received input.
type inputRecorder struct {
	Component
	Inputs []string
}

func (r *inputRecorder) HandleInput(data string) { r.Inputs = append(r.Inputs, data) }

func (r *inputRecorder) Render(width int) []string { return nil }
func (r *inputRecorder) Invalidate()               {}

func TestTuiBaseQueryTerminalBackgroundColor(t *testing.T) {
	terminal := newTestTerminal(80, 24)
	screen := newMinimalScreen(terminal)
	screen.Start()
	defer screen.Stop(nil)

	query := screen.QueryTerminalBackgroundColor(1000)
	if !strings.Contains(terminal.Written(), "\x1b]11;?\x07") {
		t.Fatal("OSC 11 query not written")
	}

	terminal.SendInput("\x1b]11;#ffffff\x07")

	select {
	case rgb := <-query:
		if rgb == nil || *rgb != (RgbColor{R: 255, G: 255, B: 255}) {
			t.Fatalf("query result = %+v", rgb)
		}
	case <-time.After(time.Second):
		t.Fatal("query timed out")
	}
}

func TestTuiBaseQueryConsumesRepliesBeforeListeners(t *testing.T) {
	terminal := newTestTerminal(80, 24)
	screen := newMinimalScreen(terminal)
	component := &inputRecorder{}
	screen.AddChild(component)
	screen.SetFocus(component)
	listenerInputs := []string{}
	screen.AddInputListener(func(data string) *TuiInputListenerResult {
		listenerInputs = append(listenerInputs, data)
		return nil
	})
	screen.Start()
	defer screen.Stop(nil)

	query := screen.QueryTerminalBackgroundColor(1000)

	terminal.SendInput("\x1b]11;#000000\x07")

	select {
	case rgb := <-query:
		if rgb == nil || *rgb != (RgbColor{R: 0, G: 0, B: 0}) {
			t.Fatalf("query result = %+v", rgb)
		}
	case <-time.After(time.Second):
		t.Fatal("query timed out")
	}
	if len(listenerInputs) != 0 {
		t.Fatalf("listener saw OSC reply: %q", listenerInputs)
	}
	if len(component.Inputs) != 0 {
		t.Fatalf("component saw OSC reply: %q", component.Inputs)
	}
}

func TestTuiBaseQueryUnparseableReplyResolvesNil(t *testing.T) {
	terminal := newTestTerminal(80, 24)
	screen := newMinimalScreen(terminal)
	component := &inputRecorder{}
	screen.AddChild(component)
	screen.SetFocus(component)
	screen.Start()
	defer screen.Stop(nil)

	query := screen.QueryTerminalBackgroundColor(1000)

	terminal.SendInput("\x1b]11;not-a-color\x07")

	select {
	case rgb := <-query:
		if rgb != nil {
			t.Fatalf("expected nil, got %+v", rgb)
		}
	case <-time.After(time.Second):
		t.Fatal("query timed out")
	}
}

func TestTuiBaseQueryDispatchesNonMatchingInput(t *testing.T) {
	terminal := newTestTerminal(80, 24)
	screen := newMinimalScreen(terminal)
	component := &inputRecorder{}
	screen.AddChild(component)
	screen.SetFocus(component)
	listenerInputs := []string{}
	screen.AddInputListener(func(data string) *TuiInputListenerResult {
		listenerInputs = append(listenerInputs, data)
		return nil
	})
	screen.Start()
	defer screen.Stop(nil)

	query := screen.QueryTerminalBackgroundColor(1000)

	terminal.SendInput("x")
	time.Sleep(20 * time.Millisecond)
	if len(listenerInputs) != 1 || listenerInputs[0] != "x" {
		t.Fatalf("listener inputs = %q", listenerInputs)
	}
	if len(component.Inputs) != 1 || component.Inputs[0] != "x" {
		t.Fatalf("component inputs = %q", component.Inputs)
	}

	terminal.SendInput("\x1b]11;#ffffff\x07")
	select {
	case rgb := <-query:
		if rgb == nil || *rgb != (RgbColor{R: 255, G: 255, B: 255}) {
			t.Fatalf("query result = %+v", rgb)
		}
	case <-time.After(time.Second):
		t.Fatal("query timed out")
	}
}

func TestTuiBaseQueryForegroundAndBackgroundDoNotCross(t *testing.T) {
	terminal := newTestTerminal(80, 24)
	screen := newMinimalScreen(terminal)
	component := &inputRecorder{}
	screen.AddChild(component)
	screen.SetFocus(component)
	screen.Start()
	defer screen.Stop(nil)

	foreground := screen.QueryTerminalForegroundColor(1000)
	background := screen.QueryTerminalBackgroundColor(1000)
	if !strings.Contains(terminal.Written(), "\x1b]10;?\x07") {
		t.Fatal("OSC 10 query not written")
	}

	terminal.SendInput("\x1b]11;rgb:0000/0000/0000\x07")
	terminal.SendInput("\x1b]10;rgb:ffff/ffff/ffff\x07")

	select {
	case rgb := <-foreground:
		if rgb == nil || *rgb != (RgbColor{R: 255, G: 255, B: 255}) {
			t.Fatalf("foreground = %+v", rgb)
		}
	case <-time.After(time.Second):
		t.Fatal("foreground timed out")
	}
	select {
	case rgb := <-background:
		if rgb == nil || *rgb != (RgbColor{R: 0, G: 0, B: 0}) {
			t.Fatalf("background = %+v", rgb)
		}
	case <-time.After(time.Second):
		t.Fatal("background timed out")
	}
	if len(component.Inputs) != 0 {
		t.Fatalf("component saw replies: %q", component.Inputs)
	}
}

func TestTuiBaseInputListenerConsumeAndRewrite(t *testing.T) {
	terminal := newTestTerminal(80, 24)
	screen := newMinimalScreen(terminal)
	component := &inputRecorder{}
	screen.AddChild(component)
	screen.SetFocus(component)
	screen.Start()
	defer screen.Stop(nil)

	screen.AddInputListener(func(data string) *TuiInputListenerResult {
		if data == "drop" {
			return &TuiInputListenerResult{Consume: true}
		}
		if data == "rewrite-me" {
			replacement := "rewritten"
			return &TuiInputListenerResult{Data: &replacement}
		}
		return nil
	})

	terminal.SendInput("a")
	terminal.SendInput("drop")
	terminal.SendInput("rewrite-me")
	time.Sleep(30 * time.Millisecond)

	if len(component.Inputs) != 2 || component.Inputs[0] != "a" || component.Inputs[1] != "rewritten" {
		t.Fatalf("inputs = %q", component.Inputs)
	}
}

func TestTuiBaseKeyReleaseFiltered(t *testing.T) {
	terminal := newTestTerminal(80, 24)
	screen := newMinimalScreen(terminal)
	component := &inputRecorder{}
	screen.AddChild(component)
	screen.SetFocus(component)
	screen.Start()
	defer screen.Stop(nil)

	terminal.SendInput("\x1b[97;1:3u") // release 'a'
	terminal.SendInput("b")
	time.Sleep(30 * time.Millisecond)

	if len(component.Inputs) != 1 || component.Inputs[0] != "b" {
		t.Fatalf("inputs = %q", component.Inputs)
	}
}

type wantsReleaseRecorder struct {
	inputRecorder
	released []string
}

func (r *wantsReleaseRecorder) WantsKeyRelease() bool { return true }

func TestTuiBaseKeyReleaseOptIn(t *testing.T) {
	terminal := newTestTerminal(80, 24)
	screen := newMinimalScreen(terminal)
	component := &wantsReleaseRecorder{}
	screen.AddChild(component)
	screen.SetFocus(component)
	screen.Start()
	defer screen.Stop(nil)

	terminal.SendInput("\x1b[97;1:3u")
	time.Sleep(30 * time.Millisecond)

	if len(component.Inputs) != 1 {
		t.Fatalf("release not delivered: %q", component.Inputs)
	}
}

type overlayComponent struct {
	lines []string
}

func (c *overlayComponent) Render(width int) []string {
	result := append([]string(nil), c.lines...)
	for i := range result {
		result[i] = TruncateToWidth(result[i], width, "...", false)
	}
	return result
}

func (c *overlayComponent) Invalidate() {}

func TestTuiBaseOverlayFocusLifecycle(t *testing.T) {
	terminal := newTestTerminal(80, 24)
	screen := newMinimalScreen(terminal)
	base := &inputRecorder{}
	screen.AddChild(base)
	screen.SetFocus(base)
	screen.Start()
	defer screen.Stop(nil)

	if screen.GetFocusedComponent() != Component(base) {
		t.Fatal("base not focused initially")
	}

	overlay := &inputRecorder{Component: &overlayComponent{lines: []string{"overlay"}}}
	handle := screen.ShowOverlay(overlay, nil)
	time.Sleep(30 * time.Millisecond)

	if screen.GetFocusedComponent() != Component(overlay) {
		t.Fatal("overlay did not take focus")
	}
	if !handle.IsFocused() {
		t.Fatal("handle reports not focused")
	}
	if !screen.HasOverlay() {
		t.Fatal("HasOverlay false")
	}

	handle.Hide()
	time.Sleep(30 * time.Millisecond)

	if screen.GetFocusedComponent() != Component(base) {
		t.Fatal("focus not restored after hide")
	}
	if screen.HasOverlay() {
		t.Fatal("HasOverlay true after hide")
	}
	if handle.GetBounds() != nil {
		t.Fatal("bounds after hide")
	}
}

func TestTuiBaseOverlayBoundsAndCompositing(t *testing.T) {
	terminal := newTestTerminal(20, 5)
	screen := newMinimalScreen(terminal)
	base := &overlayComponent{lines: []string{"base 0", "base 1", "base 2"}}
	screen.AddChild(base)
	screen.Start()
	defer screen.Stop(nil)

	overlay := &overlayComponent{lines: []string{"X"}}
	handle := screen.ShowOverlay(overlay, &OverlayOptions{Row: 1, Col: 4, Width: 4})

	screen.RenderNow(true)

	bounds := handle.GetBounds()
	if bounds == nil {
		t.Fatal("no bounds")
	}
	if bounds.Row != 1 || bounds.Col != 4 {
		t.Fatalf("bounds = %+v", bounds)
	}

	rendered := *screen.lastRender
	if len(rendered) < 3 {
		t.Fatalf("rendered = %q", rendered)
	}
	if !strings.Contains(rendered[1], "X") {
		t.Fatalf("overlay not composited: %q", rendered[1])
	}
	if !strings.HasPrefix(rendered[1], "base") {
		t.Fatalf("base line clobbered: %q", rendered[1])
	}
}

func TestTuiBaseNonCapturingOverlayKeepsFocus(t *testing.T) {
	terminal := newTestTerminal(80, 24)
	screen := newMinimalScreen(terminal)
	base := &inputRecorder{}
	screen.AddChild(base)
	screen.SetFocus(base)
	screen.Start()
	defer screen.Stop(nil)

	overlay := &inputRecorder{Component: &overlayComponent{lines: []string{"hint"}}}
	handle := screen.ShowOverlay(overlay, &OverlayOptions{NonCapturing: true})
	defer handle.Hide()

	if screen.GetFocusedComponent() != Component(base) {
		t.Fatal("non-capturing overlay stole focus")
	}
}

func TestCompositeTuiLineBasics(t *testing.T) {
	// Overlay at col 6 width 4 over "base 0" padded line.
	result := CompositeTuiLine("base 0             ", "over", 6, 4, 20)
	if !strings.Contains(result, "over") {
		t.Fatalf("overlay missing: %q", result)
	}
	if VisibleWidth(result) > 20 {
		t.Fatalf("width overflow: %d (%q)", VisibleWidth(result), result)
	}
	if !strings.Contains(result, segmentReset) {
		t.Fatalf("segment resets missing: %q", result)
	}
}

func TestExtractCursorPosition(t *testing.T) {
	lines := []string{"first", "second " + CURSOR_MARKER + "tail", "third"}
	row, col, ok := (&TuiBase{}).ExtractCursorPosition(lines, 3)
	if !ok || row != 1 || col != 7 {
		t.Fatalf("row=%d col=%d ok=%v", row, col, ok)
	}
	if strings.Contains(lines[1], CURSOR_MARKER) {
		t.Fatal("marker not stripped")
	}
	if lines[1] != "second tail" {
		t.Fatalf("line = %q", lines[1])
	}

	// Outside the bottom viewport window: not found.
	lines = []string{"first", "second " + CURSOR_MARKER, "third", "fourth"}
	if _, _, ok := (&TuiBase{}).ExtractCursorPosition(lines, 2); ok {
		t.Fatal("marker found outside viewport")
	}
}

// TestRenderPanicLeavesMutexBalanced pins the render-callback hygiene: a
// panicking DoRender must surface its own panic instead of the runtime
// fatal "sync: unlock of unlocked mutex" from the outer deferred Unlock.
func TestRenderPanicLeavesMutexBalanced(t *testing.T) {
	terminal := newTestTerminal(20, 5)
	screen := NewTuiMainScreen(terminal, nil, "")

	// Collect scheduled ticks instead of running them on a timer goroutine,
	// so the test can invoke one after RequestRender released the mutex
	// (which is the production ordering).
	var pending []func()
	screen.After = func(_ time.Duration, fn func()) *time.Timer {
		pending = append(pending, fn)
		timer := time.NewTimer(time.Hour)
		timer.Stop()
		return timer
	}
	marker := errors.New("render exploded")
	screen.DoRender = func() { panic(marker) }

	// Start clears stoppedFlag and queues its first tick; drop that tick so
	// only the forced render below runs.
	screen.Start()
	pending = nil
	screen.RequestRender(true)
	if len(pending) == 0 {
		t.Fatal("no render tick scheduled")
	}

	func() {
		defer func() {
			if recovered := recover(); recovered != marker {
				t.Fatalf("recovered %v, want the render panic", recovered)
			}
		}()
		pending[0]()
	}()

	// The mutex must still be balanced and usable.
	screen.SetShowHardwareCursor(true)
	if !screen.GetShowHardwareCursor() {
		t.Fatal("screen unusable after a render panic")
	}
	screen.Stop(nil)
}

// TestViewportTUIBrandMatchesReference pins the VIEWPORT_TUI brand: only the
// alt screen is a viewport TUI, so mount code can follow the reference's
// isViewportTUI branch (the main screen renders its children directly).
func TestViewportTUIBrandMatchesReference(t *testing.T) {
	terminal := newTestTerminal(20, 5)
	main := NewTuiMainScreen(terminal, nil, "")
	if IsViewportTUI(main) {
		t.Fatal("main screen must not be a viewport TUI")
	}
	alt := NewTuiAltScreen(terminal, nil, "", TuiAltScreenOptions{})
	if !IsViewportTUI(alt) {
		t.Fatal("alt screen must be a viewport TUI")
	}
}
