package tui

// Port of components/scroll-view.ts: the vertical scroll viewport with
// follow-end semantics and transient scrollbar visibility.

import (
	"math"
	"sync"
	"time"
)

// ScrollViewScrollbar selects scrollbar visibility.
type ScrollViewScrollbar string

const (
	ScrollbarHidden ScrollViewScrollbar = "hidden"
	ScrollbarAuto   ScrollViewScrollbar = "auto"
	ScrollbarAlways ScrollViewScrollbar = "always"
)

// ScrollViewOptions configure the scroll view.
type ScrollViewOptions struct {
	Follow               string // "none" | "end"
	Primary              bool
	Overscroll           string // "chain" | "contain"
	Scrollbar            ScrollViewScrollbar
	ScrollbarTrackStyle  func(text string) string
	ScrollbarThumbStyle  func(text string) string
	ScrollbarHideDelayMs int
}

// ScrollViewScrollToOptions tweaks scrollTo.
type ScrollViewScrollToOptions struct {
	// DisableFollow keeps follow-end disabled even at the content end.
	DisableFollow bool
}

// ScrollView wraps one component in a vertical scroll viewport.
type ScrollView struct {
	Container
	Child               Component
	FollowEnd           bool
	PrimaryFlag         bool
	OverscrollMode      string
	ScrollbarTrackStyle func(text string) string
	ScrollbarThumbStyle func(text string) string

	currentScrollbar      ScrollViewScrollbar
	scrollbarHideDelayMs  int
	currentScrollTop      int
	contentHeight         int
	currentViewportHeight int
	followingEnd          bool
	followSuppressedAtEnd bool
	requestRenderCallback func()

	// stateMu guards the scroll/layout/scrollbar fields: layout and
	// input handling run on separate goroutines from the hide timer.
	stateMu                   sync.Mutex
	transientScrollbarVisible bool
	scrollbarActive           bool
	scrollbarHideTimer        *time.Timer

	// After is the timer scheduler (injectable for tests).
	After func(d time.Duration, fn func()) *time.Timer
}

// NewScrollView builds a scroll view over one child.
func NewScrollView(component Component, options ScrollViewOptions) *ScrollView {
	if options.Overscroll == "" {
		options.Overscroll = "chain"
	}
	if options.Scrollbar == "" {
		options.Scrollbar = ScrollbarHidden
	}
	followEnd := options.Follow == "end"
	scrollbarTrackStyle := options.ScrollbarTrackStyle
	if scrollbarTrackStyle == nil {
		scrollbarTrackStyle = func(text string) string { return "\x1b[90m" + text + "\x1b[39m" }
	}
	scrollbarThumbStyle := options.ScrollbarThumbStyle
	if scrollbarThumbStyle == nil {
		scrollbarThumbStyle = func(text string) string { return "\x1b[37m" + text + "\x1b[39m" }
	}
	hideDelay := options.ScrollbarHideDelayMs
	if hideDelay == 0 {
		hideDelay = 1000
	}
	view := &ScrollView{
		Child:                component,
		FollowEnd:            followEnd,
		followingEnd:         followEnd,
		PrimaryFlag:          options.Primary,
		OverscrollMode:       options.Overscroll,
		currentScrollbar:     options.Scrollbar,
		ScrollbarTrackStyle:  scrollbarTrackStyle,
		ScrollbarThumbStyle:  scrollbarThumbStyle,
		scrollbarHideDelayMs: maxInt(0, hideDelay),
		After:                time.AfterFunc,
	}
	view.Children = []Component{component}
	return view
}

// ScrollTop returns the current scroll offset.
func (s *ScrollView) ScrollTop() int { return s.currentScrollTop }

// IsFollowingEnd reports follow-end state.
func (s *ScrollView) IsFollowingEnd() bool { return s.followingEnd }

// ViewportHeight returns the current viewport height.
func (s *ScrollView) ViewportHeight() int { return s.currentViewportHeight }

// Scrollbar returns the scrollbar mode.
func (s *ScrollView) Scrollbar() ScrollViewScrollbar { return s.currentScrollbar }

// IsScrollbarVisible reports whether the scrollbar renders now.
func (s *ScrollView) IsScrollbarVisible() bool {
	if s.currentScrollbar == ScrollbarAlways {
		return s.currentViewportHeight > 0
	}
	s.stateMu.Lock()
	visible := s.transientScrollbarVisible
	s.stateMu.Unlock()
	return s.currentScrollbar == ScrollbarAuto && s.contentHeight > s.currentViewportHeight && visible
}

// IsScrollbarActive reports scrollbar interaction state.
func (s *ScrollView) IsScrollbarActive() bool { return s.scrollbarActive }

// SetScrollbar switches the scrollbar mode.
func (s *ScrollView) SetScrollbar(scrollbar ScrollViewScrollbar) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if scrollbar == s.currentScrollbar {
		return
	}
	s.currentScrollbar = scrollbar
	if scrollbar != ScrollbarAuto {
		s.hideTransientScrollbarLocked()
	} else if s.scrollbarActive {
		s.markScrollbarActivityLocked()
	}
	if s.requestRenderCallback != nil {
		s.requestRenderCallback()
	}
}

// GetContentWidth reserves the scrollbar column when always-on.
func (s *ScrollView) GetContentWidth(width int) int {
	if s.currentScrollbar == ScrollbarAlways && width > 1 {
		return width - 1
	}
	return width
}

// markScrollbarActivityLocked requires stateMu.
func (s *ScrollView) markScrollbarActivityLocked() {
	if s.currentScrollbar != ScrollbarAuto || s.contentHeight <= s.currentViewportHeight {
		return
	}
	s.transientScrollbarVisible = true
	if s.scrollbarHideTimer != nil {
		s.scrollbarHideTimer.Stop()
		s.scrollbarHideTimer = nil
	}
	if s.scrollbarActive {
		return
	}
	s.scrollbarHideTimer = s.After(time.Duration(s.scrollbarHideDelayMs)*time.Millisecond, func() {
		s.stateMu.Lock()
		s.scrollbarHideTimer = nil
		s.transientScrollbarVisible = false
		callback := s.requestRenderCallback
		s.stateMu.Unlock()
		if callback != nil {
			callback()
		}
	})
}

func (s *ScrollView) markScrollbarActivity() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.markScrollbarActivityLocked()
}

// hideTransientScrollbarLocked requires stateMu (called from locked
// contexts; the public wrapper acquires).
func (s *ScrollView) hideTransientScrollbarLocked() {
	s.transientScrollbarVisible = false
	if s.scrollbarHideTimer == nil {
		return
	}
	s.scrollbarHideTimer.Stop()
	s.scrollbarHideTimer = nil
}

func (s *ScrollView) hideTransientScrollbar() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.hideTransientScrollbarLocked()
}

// SetScrollbarActive toggles scrollbar interaction.
func (s *ScrollView) SetScrollbarActive(active bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if active == s.scrollbarActive {
		return
	}
	s.scrollbarActive = active
	s.markScrollbarActivityLocked()
	if s.requestRenderCallback != nil {
		s.requestRenderCallback()
	}
}

// ScrollTo scrolls to an offset, clamped to the content.
func (s *ScrollView) ScrollTo(scrollTop int, options ScrollViewScrollToOptions) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	requested := scrollTop
	if math.IsNaN(float64(requested)) {
		requested = s.currentScrollTop
	}
	maxScrollTop := maxInt(0, s.contentHeight-s.currentViewportHeight)
	next := maxInt(0, minInt(maxScrollTop, requested))
	nextFollowSuppressedAtEnd := options.DisableFollow && next == maxScrollTop
	nextFollowingEnd := !nextFollowSuppressedAtEnd && s.FollowEnd && next == maxScrollTop
	if next == s.currentScrollTop && nextFollowingEnd == s.followingEnd && nextFollowSuppressedAtEnd == s.followSuppressedAtEnd {
		return
	}
	moved := next != s.currentScrollTop
	s.currentScrollTop = next
	s.followingEnd = nextFollowingEnd
	s.followSuppressedAtEnd = nextFollowSuppressedAtEnd
	if moved {
		s.markScrollbarActivityLocked()
	}
	if s.requestRenderCallback != nil {
		s.requestRenderCallback()
	}
}

// ScrollBy scrolls by lines, returning the unconsumed remainder (overscroll).
func (s *ScrollView) ScrollBy(lines int) int {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	requested := lines
	if requested == 0 {
		return 0
	}
	maxScrollTop := maxInt(0, s.contentHeight-s.currentViewportHeight)
	start := s.currentScrollTop
	if s.followingEnd {
		start = maxScrollTop
	}
	next := maxInt(0, minInt(maxScrollTop, start+requested))
	moved := next - start
	wasFollowingEnd := s.followingEnd
	s.currentScrollTop = next
	s.followingEnd = s.FollowEnd && next == maxScrollTop
	s.followSuppressedAtEnd = false
	if moved != 0 {
		s.markScrollbarActivityLocked()
	}
	if moved != 0 || s.followingEnd != wasFollowingEnd {
		if s.requestRenderCallback != nil {
			s.requestRenderCallback()
		}
	}
	return requested - moved
}

// ScrollToStart scrolls to the top.
func (s *ScrollView) ScrollToStart() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	targetFollow := s.FollowEnd && s.contentHeight <= s.currentViewportHeight
	changed := s.currentScrollTop != 0 || s.followingEnd != targetFollow
	s.currentScrollTop = 0
	s.followingEnd = targetFollow
	s.followSuppressedAtEnd = false
	if changed {
		s.markScrollbarActivityLocked()
		if s.requestRenderCallback != nil {
			s.requestRenderCallback()
		}
	}
}

// ScrollToEnd scrolls to the bottom.
func (s *ScrollView) ScrollToEnd() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	next := maxInt(0, s.contentHeight-s.currentViewportHeight)
	changed := s.currentScrollTop != next || s.followingEnd != s.FollowEnd
	s.currentScrollTop = next
	s.followingEnd = s.FollowEnd
	s.followSuppressedAtEnd = false
	if changed {
		s.markScrollbarActivityLocked()
		if s.requestRenderCallback != nil {
			s.requestRenderCallback()
		}
	}
}

// UpdateLayout implements ScrollLayoutState.
func (s *ScrollView) UpdateLayout(contentHeight, viewportHeight int, requestRender func()) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.contentHeight = maxInt(0, contentHeight)
	s.currentViewportHeight = maxInt(0, viewportHeight)
	s.requestRenderCallback = requestRender
	maxScrollTop := maxInt(0, s.contentHeight-s.currentViewportHeight)
	if s.followingEnd {
		s.currentScrollTop = maxScrollTop
	} else {
		s.currentScrollTop = maxInt(0, minInt(s.currentScrollTop, maxScrollTop))
	}
	if s.currentScrollTop < maxScrollTop {
		s.followSuppressedAtEnd = false
	}
	if s.FollowEnd && s.currentScrollTop == maxScrollTop && !s.followSuppressedAtEnd {
		s.followingEnd = true
	}
	if s.contentHeight <= s.currentViewportHeight {
		s.hideTransientScrollbarLocked()
	}
}

// Primary implements ScrollLayoutState.
func (s *ScrollView) Primary() bool { return s.PrimaryFlag }

// Overscroll implements ScrollLayoutState.
func (s *ScrollView) Overscroll() string { return s.OverscrollMode }

// AddChild panics: a ScrollView has exactly one child.
func (s *ScrollView) AddChild(component Component) { panic("ScrollView has exactly one child") }

// RemoveChild panics: the child cannot be removed.
func (s *ScrollView) RemoveChild(component Component) { panic("ScrollView child cannot be removed") }

// Clear panics: the child cannot be cleared.
func (s *ScrollView) Clear() { panic("ScrollView child cannot be cleared") }

// Render renders the child at the content width, padding the scrollbar
// column when reserved.
func (s *ScrollView) Render(width int) []string {
	contentWidth := s.GetContentWidth(width)
	lines := s.Child.Render(contentWidth)
	if contentWidth == width {
		return lines
	}
	padded := make([]string, len(lines))
	for i, line := range lines {
		padded[i] = line + " "
	}
	return padded
}

// LayoutNode exposes the scroll layout node.
func (s *ScrollView) LayoutNode() LayoutNode {
	return &ScrollLayoutNode{Component: s.Child, State: s}
}
