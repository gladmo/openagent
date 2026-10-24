package tui

// Port of tui.ts: the minimal TUI core with differential rendering —
// Component/Container, the overlay stack with focus restore, input listener
// chains, OSC color/scheme queries, and the render scheduler. TuiBase is an
// embeddable struct whose screen implementations supply the render hooks.

import (
	"regexp"
	"strconv"
	"sync"
	"time"

	"os"
)

// TuiMouseEventType is the normalized mouse event kind.
type TuiMouseEventType string

const (
	MousePress   TuiMouseEventType = "press"
	MouseRelease TuiMouseEventType = "release"
	MouseMove    TuiMouseEventType = "move"
	MouseDrag    TuiMouseEventType = "drag"
	MouseClick   TuiMouseEventType = "click"
	MouseWheel   TuiMouseEventType = "wheel"
)

// TuiMouseButton is the reported button.
type TuiMouseButton string

const (
	MouseLeft   TuiMouseButton = "left"
	MouseMiddle TuiMouseButton = "middle"
	MouseRight  TuiMouseButton = "right"
	MouseNone   TuiMouseButton = "none"
)

// TuiMouseEvent is a normalized cell-based mouse event; coordinates are
// zero-based.
type TuiMouseEvent struct {
	Type   TuiMouseEventType
	Button TuiMouseButton
	// X/Y are local to the receiving component.
	X int
	Y int
	// ScreenX/ScreenY are absolute terminal coordinates.
	ScreenX int
	ScreenY int
	// Width/Height are the current component bounds.
	Width  int
	Height int
	Shift  bool
	Alt    bool
	Ctrl   bool
	// WheelDelta is logical lines; negative scrolls up.
	WheelDelta *int
	// ClickCount is the consecutive click count when Type is click.
	ClickCount *int
}

// TuiMouseEventResult controls propagation after handling.
type TuiMouseEventResult struct {
	// Handled stops propagation and suppresses renderer-level fallback.
	Handled bool
	// Capture routes subsequent drag/release events to the component.
	Capture bool
	// Focus gives keyboard focus to the component.
	Focus bool
	// Render requests or suppresses a render. Move and release default to
	// false; press, click, drag, and wheel default to true.
	Render *bool
}

// TuiMouseDispatchTarget is internal target metadata for containers and
// alternate-screen dispatch.
type TuiMouseDispatchTarget struct {
	Component Component
	OriginX   int
	OriginY   int
	Width     int
	Height    int
}

// TuiMouseDispatchResult is the result of dispatching to a concrete component.
type TuiMouseDispatchResult struct {
	TuiMouseEventResult
	Handled bool
	Target  TuiMouseDispatchTarget
	// FocusTarget is the keyboard focus target, which may be a delegating
	// parent container.
	FocusTarget Component
}

// DispatchMouseEvent dispatches an event to a component and retains the exact
// target and coordinate transform; nil when the component did not handle it.
func DispatchMouseEvent(component Component, event TuiMouseEvent) *TuiMouseDispatchResult {
	handler, ok := component.(MouseHandler)
	if !ok {
		return nil
	}
	result := handler.HandleMouse(event)
	if result == nil {
		return nil
	}
	if !result.Handled && !result.Capture && !result.Focus {
		return nil
	}
	dispatch := &TuiMouseDispatchResult{
		TuiMouseEventResult: *result,
		Handled:             true,
		Target: TuiMouseDispatchTarget{
			Component: component,
			OriginX:   event.ScreenX - event.X,
			OriginY:   event.ScreenY - event.Y,
			Width:     event.Width,
			Height:    event.Height,
		},
	}
	if result.Focus {
		dispatch.FocusTarget = component
	}
	return dispatch
}

// RetargetMouseEvent recreates local coordinates for a previously dispatched
// mouse target.
func RetargetMouseEvent(event TuiMouseEvent, target TuiMouseDispatchTarget) TuiMouseEvent {
	event.X = event.ScreenX - target.OriginX
	event.Y = event.ScreenY - target.OriginY
	event.Width = target.Width
	event.Height = target.Height
	return event
}

// Component is the base interface all components implement.
type Component interface {
	// Render returns the component's lines for the given viewport width.
	Render(width int) []string
	// Invalidate drops cached rendering state (theme change, re-render).
	Invalidate()
}

// InputHandler is the optional keyboard input interface for focused
// components.
type InputHandler interface {
	HandleInput(data string)
}

// MouseHandler is the optional normalized mouse interface.
type MouseHandler interface {
	HandleMouse(event TuiMouseEvent) *TuiMouseEventResult
}

// WantsKeyReleaseComponent opts into Kitty key release events (default:
// filtered out).
type WantsKeyReleaseComponent interface {
	WantsKeyRelease() bool
}

// Focusable components receive focus and display a hardware cursor: when
// focused, Render emits CURSOR_MARKER at the cursor position.
type Focusable interface {
	SetFocused(focused bool)
	IsFocused() bool
}

// IsFocusable reports whether the component implements Focusable.
func IsFocusable(component Component) bool {
	if component == nil {
		return false
	}
	_, ok := component.(Focusable)
	return ok
}

// CURSOR_MARKER is a zero-width APC sequence terminals ignore; components
// emit it at the cursor position when focused and TUI positions the hardware
// cursor there.
const CURSOR_MARKER = "\x1b_pi:c\x07"

// Fake cursor markers: zero-width APC sequences around the text drawn as
// the cursor. See resolveFakeCursors.
const FAKE_CURSOR_START = "\x1b_pi:fc\x07"
const FAKE_CURSOR_END = "\x1b_pi:/fc\x07"

// focusedFakeCursorPattern matches CURSOR_MARKER directly followed by a
// fake cursor; truncating a line right after the cursor cell drops the end
// marker, so the end is optional.
var focusedFakeCursorPattern = regexp.MustCompile(`(\x1b_pi:c\x07)\x1b_pi:fc\x07([\s\S]*?)(?:\x1b_pi:/fc\x07|$)`)

// RenderFakeCursor wraps text in fake cursor markers.
func RenderFakeCursor(text string) string {
	return FAKE_CURSOR_START + text + FAKE_CURSOR_END
}

// OverlayAnchor is an overlay positioning anchor.
type OverlayAnchor string

const (
	AnchorCenter       OverlayAnchor = "center"
	AnchorTopLeft      OverlayAnchor = "top-left"
	AnchorTopRight     OverlayAnchor = "top-right"
	AnchorBottomLeft   OverlayAnchor = "bottom-left"
	AnchorBottomRight  OverlayAnchor = "bottom-right"
	AnchorTopCenter    OverlayAnchor = "top-center"
	AnchorBottomCenter OverlayAnchor = "bottom-center"
	AnchorLeftCenter   OverlayAnchor = "left-center"
	AnchorRightCenter  OverlayAnchor = "right-center"
)

// OverlayMargin configures overlay margins from terminal edges.
type OverlayMargin struct {
	Top    *int
	Right  *int
	Bottom *int
	Left   *int
}

// SizeValue is absolute (int) or a percentage string like "50%".
type SizeValue any

// parseSizeValue parses a SizeValue into an absolute value.
func parseSizeValue(value SizeValue, referenceSize int) (int, bool) {
	switch v := value.(type) {
	case nil:
		return 0, false
	case int:
		return v, true
	case float64:
		return int(jsFloor(v)), true
	case string:
		if match := sizeValuePattern.FindStringSubmatch(v); match != nil {
			percent := parseFloatJS(match[1])
			return int(jsFloor((float64(referenceSize) * percent) / 100)), true
		}
	}
	return 0, false
}

var sizeValuePattern = regexp.MustCompile(`^(\d+(?:\.\d+)?)%$`)

func parseFloatJS(s string) float64 {
	value, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return jsNaN()
	}
	return value
}

// OverlayOptions configures overlay positioning and sizing.
type OverlayOptions struct {
	// Sizing
	Width     SizeValue
	MinWidth  *int
	MaxHeight SizeValue

	// Anchor positioning (default center)
	Anchor  OverlayAnchor
	OffsetX *int
	OffsetY *int

	// Percentage or absolute positioning
	Row SizeValue
	Col SizeValue

	// Margin from terminal edges; an int applies to all sides.
	Margin OverlayMarginOrInt

	// Visibility: when set, the overlay renders only while true.
	Visible func(termWidth, termHeight int) bool
	// NonCapturing overlays do not take keyboard focus when shown.
	NonCapturing bool
}

// OverlayMarginOrInt is OverlayMargin or a uniform int margin.
type OverlayMarginOrInt any

// OverlayUnfocusOptions names the focus target after releasing an overlay.
type OverlayUnfocusOptions struct {
	Target Component // nil allowed
}

// OverlayBounds is the last rendered terminal-relative overlay rectangle.
type OverlayBounds struct {
	Row    int
	Col    int
	Width  int
	Height int
}

// OverlayHandle controls a shown overlay.
type OverlayHandle interface {
	// Hide permanently removes the overlay (cannot be shown again).
	Hide()
	// SetHidden temporarily hides or shows the overlay.
	SetHidden(hidden bool)
	// IsHidden reports the temporary hidden state.
	IsHidden() bool
	// Focus brings the overlay to the visual front and focuses it.
	Focus()
	// Unfocus releases focus to the next visible capturing overlay, the
	// previous target, or an explicit target.
	Unfocus(options *OverlayUnfocusOptions)
	// IsFocused reports whether this overlay currently has focus.
	IsFocused() bool
	// GetBounds returns the most recent rendered bounds when visible.
	GetBounds() *OverlayBounds
}

type overlayStackEntry struct {
	component  Component
	options    *OverlayOptions
	preFocus   Component
	hidden     bool
	focusOrder int
	bounds     *OverlayBounds
}

type renderedOverlayLayout struct {
	entry  *overlayStackEntry
	row    int
	col    int
	width  int
	height int
}

type overlayFocusRestoreState struct {
	status    string // "inactive" | "eligible" | "blocked"
	overlay   *overlayStackEntry
	blockedBy Component
	resume    *overlayBlockedFocusResume
}

type overlayBlockedFocusResume struct {
	status string // "restore-overlay" | "focus-target"
	target Component
}

// Container is a component that stacks children vertically.
type Container struct {
	Children []Component

	// InputHandlerOverride: when non-nil and a child requests focus, this
	// container is the focus target (TS subclasses defining handleInput).
	InputHandlerOverride func(data string)

	mouseMu     sync.Mutex
	mouseLayout *containerMouseLayout
}

func (c *Container) setMouseLayout(layout *containerMouseLayout) {
	c.mouseMu.Lock()
	c.mouseLayout = layout
	c.mouseMu.Unlock()
}

func (c *Container) getMouseLayout() *containerMouseLayout {
	c.mouseMu.Lock()
	defer c.mouseMu.Unlock()
	return c.mouseLayout
}

type containerMouseLayout struct {
	width    int
	children []containerMouseChild
}

type containerMouseChild struct {
	component Component
	height    int
}

// AddChild appends a child component.
func (c *Container) AddChild(component Component) {
	c.Children = append(c.Children, component)
}

// RemoveChild removes a child component if present.
func (c *Container) RemoveChild(component Component) {
	for i, child := range c.Children {
		if child == component {
			c.Children = append(c.Children[:i], c.Children[i+1:]...)
			return
		}
	}
}

// Clear removes all children.
func (c *Container) Clear() {
	c.Children = nil
}

// Invalidate invalidates all children.
func (c *Container) Invalidate() {
	for _, child := range c.Children {
		child.Invalidate()
	}
}

// HandleMouse dispatches to the child under the event using the layout from
// the last render at this width.
func (c *Container) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if event.Y < 0 || event.Y >= event.Height {
		return nil
	}
	mouseChildren := c.getMouseLayout()
	if mouseChildren == nil || mouseChildren.width != event.Width {
		children := make([]containerMouseChild, 0, len(c.Children))
		for _, component := range c.Children {
			children = append(children, containerMouseChild{component: component, height: len(component.Render(event.Width))})
		}
		mouseChildren = &containerMouseLayout{width: event.Width, children: children}
		c.setMouseLayout(mouseChildren)
	}
	childY := 0
	for _, child := range mouseChildren.children {
		if event.Y >= childY && event.Y < childY+child.height {
			local := event
			local.Y = event.Y - childY
			local.Height = child.height
			result := DispatchMouseEvent(child.component, local)
			if result != nil && result.Focus && c.InputHandlerOverride != nil {
				result.FocusTarget = c
			}
			return result
		}
		childY += child.height
	}
	return nil
}

// Render concatenates all children's rendered lines.
func (c *Container) Render(width int) []string {
	var lines []string
	mouseChildren := make([]containerMouseChild, 0, len(c.Children))
	for _, child := range c.Children {
		childLines := child.Render(width)
		mouseChildren = append(mouseChildren, containerMouseChild{component: child, height: len(childLines)})
		lines = append(lines, childLines...)
	}
	c.setMouseLayout(&containerMouseLayout{width: width, children: mouseChildren})
	return lines
}

const segmentReset = "\x1b[0m\x1b]8;;\x07"

// CompositeTuiLine composites overlay content into a terminal line at a
// fixed column, resetting style segments around the overlay span.
func CompositeTuiLine(baseLine, overlayLine string, startCol, overlayWidth, totalWidth int) string {
	if IsImageLine(baseLine) {
		return baseLine
	}

	afterStart := startCol + overlayWidth
	base := ExtractSegments(baseLine, startCol, afterStart, totalWidth-afterStart, true)
	overlay := SliceWithWidth(overlayLine, 0, overlayWidth, true)
	beforePad := startCol - base.BeforeWidth
	if beforePad < 0 {
		beforePad = 0
	}
	overlayPad := overlayWidth - overlay.width
	if overlayPad < 0 {
		overlayPad = 0
	}
	actualBeforeWidth := startCol
	if base.BeforeWidth > actualBeforeWidth {
		actualBeforeWidth = base.BeforeWidth
	}
	actualOverlayWidth := overlayWidth
	if overlay.width > actualOverlayWidth {
		actualOverlayWidth = overlay.width
	}
	afterTarget := totalWidth - actualBeforeWidth - actualOverlayWidth
	if afterTarget < 0 {
		afterTarget = 0
	}
	afterPad := afterTarget - base.AfterWidth
	if afterPad < 0 {
		afterPad = 0
	}
	result := base.Before +
		stringRepeat(" ", beforePad) +
		segmentReset +
		overlay.text +
		stringRepeat(" ", overlayPad) +
		segmentReset +
		base.After +
		stringRepeat(" ", afterPad)

	if VisibleWidth(result) <= totalWidth {
		return result
	}
	return SliceByColumn(result, 0, totalWidth, true)
}

func stringRepeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

// TuiMode selects the renderer's screen mode.
type TuiMode string

const (
	TuiModeRegular    TuiMode = "regular"
	TuiModeFullscreen TuiMode = "fullscreen"
)

// TuiStopOptions leaves renderer output in place for another TUI taking over
// the same terminal.
type TuiStopOptions struct {
	PreserveScreen bool
}

// TuiInputListenerResult can consume the input or rewrite it for later
// listeners.
type TuiInputListenerResult struct {
	Consume bool
	Data    *string
}

// TuiInputListener observes raw input before focused components.
type TuiInputListener func(data string) *TuiInputListenerResult

// TUI is the full TUI surface implemented by the screen renderers.
type TUI interface {
	Component
	Mode() TuiMode
	Terminal() Terminal
	SetOnDebug(fn func())
	FullRedraws() int
	AddChild(component Component)
	RemoveChild(component Component)
	Clear()
	GetShowHardwareCursor() bool
	SetShowHardwareCursor(enabled bool)
	GetClearOnShrink() bool
	SetClearOnShrink(enabled bool)
	SetFocus(component Component)
	ShowOverlay(component Component, options *OverlayOptions) OverlayHandle
	HideOverlay()
	HasOverlay() bool
	Start()
	Stop(options *TuiStopOptions)
	RenderNow(force bool)
	RequestRender(force bool)
	AddInputListener(listener TuiInputListener) func()
	OnTerminalColorSchemeChange(listener func(TerminalColorScheme)) func()
	SetTerminalColorSchemeNotifications(enabled bool)
	QueryTerminalBackgroundColor(timeoutMs int) chan *RgbColor
	QueryTerminalForegroundColor(timeoutMs int) chan *RgbColor
	QueryTerminalColorScheme(timeoutMs int) chan TerminalColorScheme
}

// ViewportTUI is the marker interface for viewport-driven renderers. Only
// the alt screen implements it: isViewportTUI is the port of the reference's
// VIEWPORT_TUI symbol brand, so the main screen (which renders its children
// directly) must not satisfy it.
type ViewportTUI interface {
	TUI
	isViewportTUI()
	SetLayoutRoot(component Component)
}

// IsViewportTUI reports whether the TUI implements the viewport protocol.
func IsViewportTUI(tui TUI) bool {
	_, ok := tui.(ViewportTUI)
	return ok
}

// TuiBase carries the shared renderer state; screen implementations embed it
// and install their hooks before Start.
type TuiBase struct {
	Container
	terminalRef Terminal
	// ModeField is the embedding screen's mode.
	ModeField TuiMode

	focusedComponent Component
	inputListeners   []TuiInputListener

	// OnDebug is the global debug key callback (Shift+Ctrl+D), invoked
	// before input reaches the focused component.
	OnDebug func()

	renderRequested          bool
	immediateRenderScheduled bool
	renderTimer              *time.Timer
	lastRenderAt             timeNano
	showHardwareCursor       bool
	clearOnShrink            bool
	FullRedrawCount          int
	stoppedFlag              bool
	// exitRestoreSignals carries fatal signals to the last-resort
	// terminal restore (see TuiBase.installExitRestore).
	exitRestoreSignals chan os.Signal
	// exitRestoreOverride is the embedding screen's exit-restore sequences,
	// set under mu and read by the restore path (which may hold mu).
	exitRestoreOverride func() (string, string)

	pendingOscColorReplies       map[int]int
	pendingOscColorQueries       map[int][]*pendingOscColorQuery
	terminalColorSchemeListeners []schemeListenerEntry
	colorSchemeNotifications     bool

	focusOrderCounter      int
	overlayStack           []*overlayStackEntry
	renderedOverlayLayouts []renderedOverlayLayout
	overlayFocusRestore    overlayFocusRestoreState

	// Hooks the embedding screen installs.
	DoRender            func()
	ResetRenderState    func()
	BeforeTerminalStart func()
	AfterTerminalStart  func()
	BeforeTerminalStop  func(options *TuiStopOptions)
	AfterTerminalStop   func(options *TuiStopOptions)

	// LogDirectory holds debug/crash logs; empty disables debug logging and
	// crash dumps fall back to the OS temp directory.
	LogDirectory string

	// Now is the monotonic clock (injectable for tests).
	Now func() timeNano
	// After schedules a callback (injectable for tests).
	After func(d time.Duration, fn func()) *time.Timer

	mu sync.Mutex
}

type timeNano = int64

type pendingOscColorQuery struct {
	settled bool
	resolve func(rgb *RgbColor)
	timer   *time.Timer
}

const minRenderIntervalMs = 16

func nowNano() timeNano { return time.Now().UnixNano() }

// InitTuiBase initializes the embedded base (Go has no constructors for
// embeds; screens call this in their constructors).
func (t *TuiBase) InitTuiBase(terminal Terminal, showHardwareCursor *bool) {
	t.terminalRef = terminal
	t.pendingOscColorReplies = map[int]int{10: 0, 11: 0}
	t.pendingOscColorQueries = map[int][]*pendingOscColorQuery{10: {}, 11: {}}
	t.overlayFocusRestore = overlayFocusRestoreState{status: "inactive"}
	t.stoppedFlag = true
	t.Now = nowNano
	t.After = time.AfterFunc
	if showHardwareCursor != nil {
		t.showHardwareCursor = *showHardwareCursor
	}
}

// Terminal returns the terminal.
func (t *TuiBase) Terminal() Terminal { return t.terminalRef }

// Mode returns the embedding screen's mode.
func (t *TuiBase) Mode() TuiMode { return t.ModeField }

// FullRedraws returns the full-redraw counter.
func (t *TuiBase) FullRedraws() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.FullRedrawCount
}

// SetOnDebug installs the debug key callback.
func (t *TuiBase) SetOnDebug(fn func()) { t.OnDebug = fn }
