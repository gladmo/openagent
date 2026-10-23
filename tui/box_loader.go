package tui

// Port of components/box.ts, components/mouse-region.ts,
// components/loader.ts, components/cancellable-loader.ts, and
// editor-component.ts (interface only).

import (
	"sync"

	"time"

	"github.com/gladmo/openagent/abort"
)

// boxRenderCache caches rendered output keyed by child lines + bg sample.
type boxRenderCache struct {
	childLines []string
	width      int
	bgSample   string
	hasSample  bool
	lines      []string
}

// Box applies padding and an optional background to stacked children.
type Box struct {
	Children []Component
	PaddingX int
	PaddingY int
	BgFn     func(text string) string

	cache       *boxRenderCache
	mouseMu     sync.Mutex
	mouseLayout *containerMouseLayout
}

// NewBox builds a padded box container.
func NewBox(paddingX, paddingY int, bgFn func(string) string) *Box {
	return &Box{PaddingX: paddingX, PaddingY: paddingY, BgFn: bgFn}
}

// AddChild appends a child.
func (b *Box) AddChild(component Component) {
	b.Children = append(b.Children, component)
	b.invalidateCache()
}

// RemoveChild removes a child.
func (b *Box) RemoveChild(component Component) {
	for i, child := range b.Children {
		if child == component {
			b.Children = append(b.Children[:i], b.Children[i+1:]...)
			b.invalidateCache()
			return
		}
	}
}

// Clear removes all children.
func (b *Box) Clear() {
	b.Children = nil
	b.invalidateCache()
}

// SetBgFn replaces the background function. The render cache detects output
// changes by sampling, so no explicit invalidation happens here.
func (b *Box) SetBgFn(bgFn func(string) string) {
	b.BgFn = bgFn
}

func (b *Box) invalidateCache() {
	b.cache = nil
}

func (b *Box) matchCache(width int, childLines []string, bgSample string, hasSample bool) bool {
	cache := b.cache
	if cache == nil || cache.width != width || cache.hasSample != hasSample || cache.bgSample != bgSample {
		return false
	}
	if len(cache.childLines) != len(childLines) {
		return false
	}
	for i, line := range childLines {
		if cache.childLines[i] != line {
			return false
		}
	}
	return true
}

// Invalidate drops the cache and forwards to children.
func (b *Box) Invalidate() {
	b.invalidateCache()
	for _, child := range b.Children {
		child.Invalidate()
	}
}

// HandleMouse dispatches into children at padding-adjusted coordinates.
func (b *Box) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	contentWidth := maxInt(1, event.Width-b.PaddingX*2)
	contentY := event.Y - b.PaddingY
	contentX := event.X - b.PaddingX
	if contentY < 0 || contentX < 0 || contentX >= contentWidth {
		return nil
	}

	mouseChildren := b.getMouseLayout()
	if mouseChildren == nil || mouseChildren.width != contentWidth {
		children := make([]containerMouseChild, 0, len(b.Children))
		for _, component := range b.Children {
			children = append(children, containerMouseChild{component: component, height: len(component.Render(contentWidth))})
		}
		mouseChildren = &containerMouseLayout{width: contentWidth, children: children}
		b.setMouseLayout(mouseChildren)
	}
	childY := 0
	for _, child := range mouseChildren.children {
		if contentY >= childY && contentY < childY+child.height {
			local := event
			local.X = contentX
			local.Y = contentY - childY
			local.Width = contentWidth
			local.Height = child.height
			return DispatchMouseEvent(child.component, local)
		}
		childY += child.height
	}
	return nil
}

// Render pads children and applies the background with caching.
func (b *Box) Render(width int) []string {
	if len(b.Children) == 0 {
		return nil
	}

	contentWidth := maxInt(1, width-b.PaddingX*2)
	leftPad := stringRepeat(" ", b.PaddingX)

	var childLines []string
	mouseChildren := make([]containerMouseChild, 0, len(b.Children))
	for _, child := range b.Children {
		lines := child.Render(contentWidth)
		mouseChildren = append(mouseChildren, containerMouseChild{component: child, height: len(lines)})
		for _, line := range lines {
			childLines = append(childLines, leftPad+line)
		}
	}
	b.setMouseLayout(&containerMouseLayout{width: contentWidth, children: mouseChildren})

	if len(childLines) == 0 {
		return nil
	}

	// Sample bgFn output to detect theme changes.
	bgSample := ""
	hasSample := false
	if b.BgFn != nil {
		bgSample = b.BgFn("test")
		hasSample = true
	}

	if b.matchCache(width, childLines, bgSample, hasSample) {
		return b.cache.lines
	}

	var result []string
	for i := 0; i < b.PaddingY; i++ {
		result = append(result, b.applyBg("", width))
	}
	for _, line := range childLines {
		result = append(result, b.applyBg(line, width))
	}
	for i := 0; i < b.PaddingY; i++ {
		result = append(result, b.applyBg("", width))
	}

	b.cache = &boxRenderCache{childLines: childLines, width: width, bgSample: bgSample, hasSample: hasSample, lines: result}
	return result
}

func (b *Box) applyBg(line string, width int) string {
	padNeeded := maxInt(0, width-VisibleWidth(line))
	padded := line + stringRepeat(" ", padNeeded)
	if b.BgFn != nil {
		return ApplyBackgroundToLine(padded, width, b.BgFn)
	}
	return padded
}

// MouseRegionHandler receives mouse events for the region.
type MouseRegionHandler func(event TuiMouseEvent) *TuiMouseEventResult

// MouseRegion adds mouse handling to an existing component without changing
// its rendering.
type MouseRegion struct {
	Child   Component
	OnMouse MouseRegionHandler
}

// NewMouseRegion wraps a child with a mouse handler.
func NewMouseRegion(child Component, onMouse MouseRegionHandler) *MouseRegion {
	return &MouseRegion{Child: child, OnMouse: onMouse}
}

// Render forwards to the child.
func (m *MouseRegion) Render(width int) []string { return m.Child.Render(width) }

// HandleMouse dispatches to the child first, then the region handler.
func (m *MouseRegion) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if childResult := DispatchMouseEvent(m.Child, event); childResult != nil {
		return childResult
	}
	if result := m.OnMouse(event); result != nil {
		return DispatchMouseEvent(m, event)
	}
	return nil
}

// Invalidate forwards to the child.
func (m *MouseRegion) Invalidate() { m.Child.Invalidate() }

var loaderDefaultFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const loaderDefaultIntervalMs = 80

// LoaderIndicatorOptions configures the loader animation. Empty frames hide
// the indicator.
type LoaderIndicatorOptions struct {
	Frames     []string
	IntervalMs int
}

// Loader displays a message with an optional spinning animation, built on
// Text.
type Loader struct {
	*Text

	frames                  []string
	intervalMs              int
	currentFrame            int
	intervalID              *time.Ticker
	ui                      TUI
	renderIndicatorVerbatim bool
	spinnerColorFn          func(string) string
	messageColorFn          func(string) string
	message                 string
}

// NewLoader builds an animated loader.
func NewLoader(ui TUI, spinnerColorFn, messageColorFn func(string) string, message string, indicator *LoaderIndicatorOptions) *Loader {
	// Nil color functions normalize to identity so renders never
	// dereference a missing style hook.
	if spinnerColorFn == nil {
		spinnerColorFn = func(s string) string { return s }
	}
	if messageColorFn == nil {
		messageColorFn = func(s string) string { return s }
	}
	loader := &Loader{
		Text:           NewText("", 1, 0, nil),
		ui:             ui,
		spinnerColorFn: spinnerColorFn,
		messageColorFn: messageColorFn,
		message:        message,
	}
	loader.SetIndicator(indicator)
	return loader
}

// Render prefixes a blank line above the text.
func (l *Loader) Render(width int) []string {
	return append([]string{""}, l.Text.Render(width)...)
}

// Start updates the display and restarts the animation.
func (l *Loader) Start() {
	l.updateDisplay()
	l.restartAnimation()
}

// Stop stops the animation.
func (l *Loader) Stop() {
	if l.intervalID != nil {
		l.intervalID.Stop()
		l.intervalID = nil
	}
}

// SetMessage replaces the message.
func (l *Loader) SetMessage(message string) {
	l.message = message
	l.updateDisplay()
}

// Invalidate drops the cache and refreshes the display.
func (l *Loader) Invalidate() {
	l.Text.Invalidate()
	l.updateDisplay()
}

// SetIndicator replaces the animation frames and interval.
func (l *Loader) SetIndicator(indicator *LoaderIndicatorOptions) {
	l.renderIndicatorVerbatim = indicator != nil
	l.frames = append([]string(nil), loaderDefaultFrames...)
	l.intervalMs = loaderDefaultIntervalMs
	if indicator != nil {
		if indicator.Frames != nil {
			l.frames = append([]string(nil), indicator.Frames...)
		}
		if indicator.IntervalMs > 0 {
			l.intervalMs = indicator.IntervalMs
		}
	}
	l.currentFrame = 0
	l.Start()
}

func (l *Loader) restartAnimation() {
	l.Stop()
	if len(l.frames) <= 1 {
		return
	}
	ticker := time.NewTicker(time.Duration(l.intervalMs) * time.Millisecond)
	l.intervalID = ticker
	go func() {
		for range ticker.C {
			l.currentFrame = (l.currentFrame + 1) % len(l.frames)
			l.updateDisplay()
		}
	}()
}

// GetRenderedIndicator returns the current frame, colored unless the
// indicator was set explicitly.
func (l *Loader) GetRenderedIndicator() string {
	frame := ""
	if l.currentFrame < len(l.frames) {
		frame = l.frames[l.currentFrame]
	}
	if l.renderIndicatorVerbatim {
		return frame
	}
	return l.spinnerColorFn(frame)
}

func (l *Loader) updateDisplay() {
	renderedFrame := l.GetRenderedIndicator()
	indicator := ""
	if len(renderedFrame) > 0 {
		indicator = renderedFrame + " "
	}
	l.SetText(indicator + l.messageColorFn(l.message))
	if l.ui != nil {
		l.ui.RequestRender(false)
	}
}

// CancellableLoader is a Loader abortable with the cancel keybinding.
type CancellableLoader struct {
	*Loader

	abortController *abort.Controller

	// OnAbort is called when the user presses the cancel key.
	OnAbort func()
}

// NewCancellableLoader builds a cancellable loader.
func NewCancellableLoader(ui TUI, spinnerColorFn, messageColorFn func(string) string, message string) *CancellableLoader {
	return &CancellableLoader{
		Loader:          NewLoader(ui, spinnerColorFn, messageColorFn, message, nil),
		abortController: abort.NewController(),
	}
}

// Signal returns the abort signal.
func (c *CancellableLoader) Signal() *abort.Signal {
	return c.abortController.Signal()
}

// Aborted reports whether the loader was cancelled.
func (c *CancellableLoader) Aborted() bool {
	return c.abortController.Signal().Aborted()
}

// HandleInput aborts on the cancel keybinding.
func (c *CancellableLoader) HandleInput(data string) {
	if GetKeybindings().Matches(data, "tui.select.cancel") {
		c.abortController.Abort()
		if c.OnAbort != nil {
			c.OnAbort()
		}
	}
}

// Dispose stops the animation.
func (c *CancellableLoader) Dispose() {
	c.Stop()
}

// EditorComponent is the interface for custom editor implementations:
// extensions can provide their own editor (vim mode, emacs mode, custom
// keybindings) while staying compatible with the core application.
type EditorComponent interface {
	Component
	InputHandler

	// Core text access
	GetText() string
	SetText(text string)

	// Callbacks
	SubmitHandler() func(text string)
	SetOnSubmit(handler func(text string))
	ChangeHandler() func(text string)
	SetOnChange(handler func(text string))

	// History support (optional capability)
	HistoryAdder
	// Text manipulation (optional capability)
	TextInserter
	ExpandedTextGetter
	// Autocomplete (optional capability)
	AutocompleteProviderSetter
	// Appearance (optional capability)
	BorderColorSetter
	PaddingXSetter
	AutocompleteMaxVisibleSetter
}

// HistoryAdder adds text to history for up/down navigation.
type HistoryAdder interface {
	AddToHistory(text string)
}

// TextInserter inserts text at the current cursor position.
type TextInserter interface {
	InsertTextAtCursor(text string)
}

// ExpandedTextGetter returns text with markers expanded (e.g. paste
// markers).
type ExpandedTextGetter interface {
	GetExpandedText() string
}

// AutocompleteProviderSetter installs an autocomplete provider.
type AutocompleteProviderSetter interface {
	SetAutocompleteProvider(provider AutocompleteProvider)
}

// BorderColorSetter styles the editor border.
type BorderColorSetter interface {
	SetBorderColor(color func(str string) string)
}

// PaddingXSetter sets horizontal padding.
type PaddingXSetter interface {
	SetPaddingX(padding int)
}

// AutocompleteMaxVisibleSetter caps the autocomplete dropdown height.
type AutocompleteMaxVisibleSetter interface {
	SetAutocompleteMaxVisible(maxVisible int)
}

func (b *Box) setMouseLayout(layout *containerMouseLayout) {
	b.mouseMu.Lock()
	b.mouseLayout = layout
	b.mouseMu.Unlock()
}

func (b *Box) getMouseLayout() *containerMouseLayout {
	b.mouseMu.Lock()
	defer b.mouseMu.Unlock()
	return b.mouseLayout
}

// SetPaddingX changes the horizontal padding and invalidates the cache
// (setPaddingX).
func (b *Box) SetPaddingX(padding int) {
	if b.PaddingX == padding {
		return
	}
	b.PaddingX = padding
	b.invalidateCache()
}
