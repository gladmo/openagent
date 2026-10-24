package tui

// Port of tui-alt-screen.ts: the alternate-screen TUI with an
// application-owned scrollable viewport, mouse selection, scrollbar
// interaction, transcript search, flash messages, and Kitty image
// management. doRender paints viewport rows absolutely (CUP + EL) instead of
// scrolling like the main screen.

import (
	"encoding/base64"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	enterAltScreen                         = "\x1b[?1049h"
	exitAltScreen                          = "\x1b[?1049l"
	disableAutowrap                        = "\x1b[?7l"
	enableAutowrap                         = "\x1b[?7h"
	enableButtonMotion                     = "\x1b[?1000h\x1b[?1002h\x1b[?1004h\x1b[?1006h"
	enableAllMotion                        = "\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1004h\x1b[?1006h"
	disableMouseSeq                        = "\x1b[?1006l\x1b[?1004l\x1b[?1003l\x1b[?1002l\x1b[?1000l"
	focusIn                                = "\x1b[I"
	focusOut                               = "\x1b[O"
	beginSyncOutput                        = "\x1b[?2026h"
	endSyncOutput                          = "\x1b[?2026l"
	pageScrollOverlap                      = 4
	altWheelMultiplier                     = 5
	maxCachedOffscreenKittyImages          = 16
	maxCachedOffscreenKittyTransmissionBts = 32 * 1024 * 1024
	maxCachedOffscreenKittyDecodedBts      = 64 * 1024 * 1024
	doubleClickIntervalMs                  = 500
	copyErrorFlashDurationMs               = 5000
)

// Regular mode delegates double-click selection to the terminal emulator.
// Fullscreen owns mouse selection, so mirror common terminal word-selection
// behavior by keeping paths and kebab-case tokens whole.
var terminalWordSelectionJoiners = map[string]bool{"/": true, "-": true}

type cachedKittyImage struct {
	transmissionGeneration int
	transmissionBytes      int
	estimatedDecodedBytes  int
}

type selectionPoint struct {
	row        int
	col        int
	scrollView *ScrollView
	// boundary marks a point between cells rather than on a cell.
	boundary bool
}

type selectionRange struct {
	start selectionPoint
	end   selectionPoint
}

type selectionGranularity string

const (
	selectionCharacter selectionGranularity = "character"
	selectionWord      selectionGranularity = "word"
	selectionLine      selectionGranularity = "line"
)

type clickTarget struct {
	timestamp  timeNano
	count      int
	row        int
	scrollView *ScrollView
	wordStart  int
	wordEnd    int
}

type sgrMouseEvent struct {
	button  int
	x       int
	y       int
	release bool
}

type wheelEvent struct {
	direction int // -1 | 1
	x         int
	y         int
	button    int
}

type scrollbarDrag struct {
	scrollView *ScrollView
	grabOffset int
}

type scrollbarTarget struct {
	scrollView *ScrollView
	geometry   *ScrollbarGeometry
}

type scrollToEndIndicatorRect struct {
	row    int
	column int
	width  int
}

type searchSelectionMode string

const (
	searchModeQuery    searchSelectionMode = "query"
	searchModeRetain   searchSelectionMode = "retain"
	searchModeNext     searchSelectionMode = "next"
	searchModePrevious searchSelectionMode = "previous"
)

// ActiveSearch is the live transcript search session.
type ActiveSearch struct {
	component      *AltScreenSearchComponent
	index          AltScreenSearchIndex
	overlay        OverlayHandle
	query          string
	matches        []AltScreenSearchMatch
	selectedIndex  int
	selectedKey    string
	hasSelectedKey bool
	anchorRow      int
	selectionMode  searchSelectionMode
}

type searchHighlightRange struct {
	startCol int
	endCol   int
	current  bool
}

// TuiAltScreenOptions configures the alternate screen.
type TuiAltScreenOptions struct {
	// WheelScrollLines is the logical lines moved per wheel event.
	WheelScrollLines int
	// Mouse captures events for viewport scrolling and app-owned selection.
	Mouse *bool
	// SearchMatchStyle styles a non-current search match.
	SearchMatchStyle func(text string) string
	// SearchCurrentMatchStyle styles the current match.
	SearchCurrentMatchStyle func(text string) string
	// SearchNavigationButtonStyle styles a navigation button.
	SearchNavigationButtonStyle func(text string, hovered bool) string
	// ScrollToEndIndicator renders a clickable jump-to-end label.
	ScrollToEndIndicator func() string
	// OpenUrl opens an OSC 8 hyperlink activated by a primary click.
	OpenUrl func(url string)
	// OnRightClickPaste handles unmodified secondary press (Windows only).
	OnRightClickPaste func()
	// CopyOnSelect copies selected text on release (default true).
	CopyOnSelect *bool
	// CopySelection copies to the system clipboard: true on success, an
	// error message string on failure, nil for generic error. When nil,
	// selection copies via an OSC 52 write.
	CopySelection func(text string) func() any
}

// TuiAltScreen renders into the alternate screen with an owned viewport.
type TuiAltScreen struct {
	TuiBase

	previousScreen               []string
	lastDocument                 []string
	previousScreenWidth          int
	previousScreenHeight         int
	layoutRoot                   Component
	currentLayout                *LayoutFrame
	implicitDocument             *implicitDocumentComponent
	implicitScrollView           *ScrollView
	flashes                      *AltScreenFlashContainer
	altScreenActive              bool
	imageProtocol                ImageProtocol
	savedCapabilities            *TerminalCapabilities
	uploadedKittyImages          map[int]*cachedKittyImage
	kittyUploadOrder             []int
	selectionAnchor              *selectionPoint
	selectionFocus               *selectionPoint
	selectionGranularity         selectionGranularity
	selectionInitialRange        *selectionRange
	lastClick                    *clickTarget
	selectionDragPointer         *struct{ x, y int }
	selectionAutoScrollDirection int
	selectionAutoScrollTimer     *time.Ticker
	selectionPressActive         bool
	scrollbarDrag                *scrollbarDrag
	scrollbarHover               *ScrollView
	scrollToEndIndicatorRect     *scrollToEndIndicatorRect
	activeSearch                 *ActiveSearch
	pressedUrl                   string
	selectionDragged             bool
	mouseCapture                 *TuiMouseDispatchTarget
	mousePressTarget             *TuiMouseDispatchTarget
	mousePressPoint              *struct{ x, y int }
	mousePressMoved              bool
	lastComponentClick           *struct {
		timestamp timeNano
		count     int
		component Component
		x, y      int
	}

	wheelScrollLines            int
	mouseEnabled                bool
	searchMatchStyle            func(string) string
	searchCurrentMatchStyle     func(string) string
	searchNavigationButtonStyle func(text string, hovered bool) string
	scrollToEndIndicator        func() string
	openUrl                     func(string)
	onRightClickPaste           func()
	copyOnSelect                bool
	copySelection               func(text string) func() any
}

// implicitDocumentComponent adapts TuiBase's children into a Component for
// the implicit scroll view.
type implicitDocumentComponent struct {
	base *TuiAltScreen
}

func (d *implicitDocumentComponent) Render(width int) []string {
	return d.base.TuiBase.Render(width)
}

func (d *implicitDocumentComponent) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	return d.base.TuiBase.Container.HandleMouse(event)
}

func (d *implicitDocumentComponent) Invalidate() {
	for _, child := range d.base.Children {
		child.Invalidate()
	}
}

// NewTuiAltScreen builds the alternate-screen TUI.
func NewTuiAltScreen(terminal Terminal, showHardwareCursor *bool, logDirectory string, options TuiAltScreenOptions) *TuiAltScreen {
	screen := &TuiAltScreen{}
	screen.InitTuiBase(terminal, showHardwareCursor)
	screen.LogDirectory = logDirectory
	screen.ModeField = TuiModeFullscreen
	screen.uploadedKittyImages = map[int]*cachedKittyImage{}
	// The fatal-signal restore path may hold TuiBase.mu (the same mutex as
	// screen.mu); the override reads the alt-screen state the restore path
	// already serialized. The constructor runs before Start, so no lock is
	// needed here.
	screen.SetExitRestoreOverride(func() (string, string) {
		return screen.exitRestorePrefixLocked(), screen.exitRestoreSuffixLocked()
	})

	screen.implicitDocument = &implicitDocumentComponent{base: screen}
	screen.implicitScrollView = NewScrollView(screen.implicitDocument, ScrollViewOptions{Follow: "end", Primary: true})
	screen.flashes = NewAltScreenFlashContainer(func() { screen.RequestRender(false) })

	screen.wheelScrollLines = maxInt(1, options.WheelScrollLines)
	if screen.wheelScrollLines < 1 {
		screen.wheelScrollLines = 1
	}
	screen.mouseEnabled = options.Mouse == nil || *options.Mouse
	screen.searchMatchStyle = options.SearchMatchStyle
	if screen.searchMatchStyle == nil {
		screen.searchMatchStyle = func(text string) string { return "\x1b[4m" + text + "\x1b[24m" }
	}
	screen.searchCurrentMatchStyle = options.SearchCurrentMatchStyle
	if screen.searchCurrentMatchStyle == nil {
		screen.searchCurrentMatchStyle = func(text string) string { return "\x1b[1;7m" + text + "\x1b[22;27m" }
	}
	screen.searchNavigationButtonStyle = options.SearchNavigationButtonStyle
	if screen.searchNavigationButtonStyle == nil {
		screen.searchNavigationButtonStyle = func(text string, hovered bool) string { return text }
	}
	screen.scrollToEndIndicator = options.ScrollToEndIndicator
	screen.openUrl = options.OpenUrl
	screen.onRightClickPaste = options.OnRightClickPaste
	screen.copyOnSelect = options.CopyOnSelect == nil || *options.CopyOnSelect
	screen.copySelection = options.CopySelection

	screen.DoRender = screen.doRenderAlt
	screen.ResetRenderState = screen.resetRenderState
	screen.BeforeTerminalStart = screen.beforeTerminalStart
	screen.BeforeTerminalStop = screen.beforeTerminalStop
	screen.AfterTerminalStop = screen.afterTerminalStop

	screen.AddInputListener(func(data string) *TuiInputListenerResult {
		result := screen.handleViewportInput(data)
		if result == nil {
			return nil
		}
		return &TuiInputListenerResult{Consume: result.consume}
	})
	return screen
}

func (s *TuiAltScreen) isViewportTUI() {}

// SetLayoutRoot replaces the layout root; nil restores implicit children.
func (s *TuiAltScreen) SetLayoutRoot(component Component) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.layoutRoot == component {
		return
	}
	s.layoutRoot = component
	s.currentLayout = nil
	s.requestRenderLocked(false)
}

// Render renders through the layout root when set.
func (s *TuiAltScreen) Render(width int) []string {
	s.mu.Lock()
	root := s.layoutRoot
	s.mu.Unlock()
	if root != nil {
		return root.Render(width)
	}
	return s.Container.Render(width)
}

// getMountedRootsLocked overrides the focus-mount roots.
func (s *TuiAltScreen) getMountedRootsLocked() []Component {
	if s.layoutRoot != nil {
		return []Component{s.layoutRoot}
	}
	return s.Children
}

// ViewportTop returns the primary scroll offset.
func (s *TuiAltScreen) ViewportTop() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getPrimaryScrollViewLocked().ScrollTop()
}

// IsFollowingOutput reports follow-end state.
func (s *TuiAltScreen) IsFollowingOutput() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getPrimaryScrollViewLocked().IsFollowingEnd()
}

// GetCopyOnSelect reports copy-on-select.
func (s *TuiAltScreen) GetCopyOnSelect() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copyOnSelect
}

// SetCopyOnSelect toggles copy-on-select.
func (s *TuiAltScreen) SetCopyOnSelect(enabled bool) {
	s.mu.Lock()
	s.copyOnSelect = enabled
	s.mu.Unlock()
}

// HasActiveSelection reports a non-empty text selection.
func (s *TuiAltScreen) HasActiveSelection() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getActiveSelectionTextLocked() != ""
}

// CopyActiveSelectionToClipboard copies the selection (async result via
// flash); returns whether a selection existed.
func (s *TuiAltScreen) CopyActiveSelectionToClipboard() bool {
	s.mu.Lock()
	text := s.getActiveSelectionTextLocked()
	s.mu.Unlock()
	if text == "" {
		return false
	}
	s.copyTextToClipboard(text)
	return true
}

func (s *TuiAltScreen) getPrimaryScrollViewLocked() *ScrollView {
	if s.currentLayout != nil && s.currentLayout.PrimaryScrollView != nil {
		return s.currentLayout.PrimaryScrollView
	}
	return s.implicitScrollView
}

func (s *TuiAltScreen) beforeTerminalStart() {
	s.stopSelectionAutoScroll()
	s.selectionPressActive = false
	s.stopScrollbarHover()
	s.stopScrollbarDrag()
	s.flashes.Dispose()
	s.altScreenActive = true
	capabilities := GetCapabilities()
	s.imageProtocol = capabilities.Images
	s.uploadedKittyImages = map[int]*cachedKittyImage{}
	s.kittyUploadOrder = nil
	if capabilities.Images == ImageProtocolITerm2 {
		saved := capabilities
		s.savedCapabilities = &saved
		SetCapabilities(TerminalCapabilities{Images: ImageProtocolNone, TrueColor: capabilities.TrueColor, Hyperlinks: capabilities.Hyperlinks})
		s.invalidateLocked()
	}
	s.lastDocument = nil
	s.selectionAnchor = nil
	s.selectionFocus = nil
	s.selectionGranularity = selectionCharacter
	s.selectionInitialRange = nil
	s.lastClick = nil
	s.pressedUrl = ""
	s.selectionDragged = false
	s.clearComponentMouseGesture()
	s.lastComponentClick = nil
	s.resetRenderState()
	term := strings.ToLower(os.Getenv("TERM"))
	// Multiplexers lag when every pointer movement is forwarded; button-
	// motion tracking preserves clicks, wheel, selections, scrollbar drags.
	mouseSequence := enableAllMotion
	if os.Getenv("TMUX") != "" || os.Getenv("ZELLIJ") != "" || os.Getenv("STY") != "" ||
		strings.HasPrefix(term, "tmux") || strings.HasPrefix(term, "screen") {
		mouseSequence = enableButtonMotion
	}
	mousePart := ""
	if s.mouseEnabled {
		mousePart = mouseSequence
	}
	s.terminalRef.Write(enterAltScreen + disableAutowrap + mousePart + "\x1b[2J\x1b[H\x1b[?25l")
}

func (s *TuiAltScreen) beforeTerminalStop(options *TuiStopOptions) {
	s.closeSearchLocked()
	s.stopSelectionAutoScroll()
	s.selectionPressActive = false
	s.stopScrollbarHover()
	s.stopScrollbarDrag()
	s.clearComponentMouseGesture()
	s.flashes.Dispose()
	if !s.altScreenActive {
		return
	}
	mousePart := ""
	if s.mouseEnabled {
		mousePart = disableMouseSeq
	}
	s.terminalRef.Write(beginSyncOutput + s.deleteKittyImagesAll() + mousePart + enableAutowrap + endSyncOutput)
	s.uploadedKittyImages = map[int]*cachedKittyImage{}
	s.kittyUploadOrder = nil
}

func (s *TuiAltScreen) afterTerminalStop(options *TuiStopOptions) {
	if !s.altScreenActive {
		return
	}
	s.altScreenActive = false
	if options != nil && options.PreserveScreen {
		s.terminalRef.Write(beginSyncOutput + exitAltScreen + "\x1b[?25h" + endSyncOutput)
	} else {
		width := maxInt(1, s.terminalRef.Columns())
		// renderUnlocked: Stop holds the base mutex across this hook.
		var documentLines []string
		if s.layoutRoot != nil {
			documentLines = s.layoutRoot.Render(width)
		} else {
			documentLines = s.Container.Render(width)
		}
		documentLines = s.resolveFakeCursors(documentLines)
		stripped := make([]string, len(documentLines))
		for i, line := range documentLines {
			stripped[i] = osc133ZonePrefix.ReplaceAllString(strings.ReplaceAll(line, CURSOR_MARKER, ""), "")
		}
		s.lastDocument = s.ApplyLineResets(stripped)
		for i, line := range s.lastDocument {
			if !IsImageLine(line) && VisibleWidth(line) > width {
				s.lastDocument[i] = SliceByColumn(line, 0, width, true)
			}
		}
		var buffer strings.Builder
		buffer.WriteString(beginSyncOutput + exitAltScreen + disableAutowrap)
		for row := 0; row < len(s.lastDocument); row++ {
			if row > 0 {
				buffer.WriteString("\r\n")
			}
			line := ""
			if row < len(s.lastDocument) {
				line = s.lastDocument[row]
			}
			buffer.WriteString("\r\x1b[2K" + line)
		}
		buffer.WriteString("\x1b[0m" + enableAutowrap + "\r\n\x1b[?25h" + endSyncOutput)
		s.terminalRef.Write(buffer.String())
	}
	if s.savedCapabilities != nil {
		SetCapabilities(*s.savedCapabilities)
		s.savedCapabilities = nil
	}
}

func (s *TuiAltScreen) deleteKittyImagesAll() string {
	if s.imageProtocol == ImageProtocolKitty {
		return DeleteAllKittyImages()
	}
	return ""
}

type preparedKittyScreen struct {
	lines                []string
	evictedImageDeletion string
}

func (s *TuiAltScreen) prepareKittyScreen(screen []string) preparedKittyScreen {
	visibleImageIDs := map[int]bool{}
	lines := make([]string, len(screen))
	for i, line := range screen {
		placement := GetKittyImagePlacement(line)
		if placement == nil {
			lines[i] = line
			continue
		}
		visibleImageIDs[placement.ImageID] = true

		cachedImage := s.uploadedKittyImages[placement.ImageID]
		nextCachedImage := &cachedKittyImage{
			transmissionGeneration: placement.TransmissionGeneration,
			transmissionBytes:      placement.TransmissionBytes,
			estimatedDecodedBytes:  placement.EstimatedDecodedBytes,
		}
		if cachedImage != nil {
			delete(s.uploadedKittyImages, placement.ImageID)
			s.removeFromUploadOrder(placement.ImageID)
		}
		s.uploadedKittyImages[placement.ImageID] = nextCachedImage
		s.kittyUploadOrder = append(s.kittyUploadOrder, placement.ImageID)

		if cachedImage != nil && cachedImage.transmissionGeneration == placement.TransmissionGeneration {
			lines[i] = placement.ReplacementLine
		} else {
			lines[i] = line
		}
	}

	cachedOffscreenImageCount := 0
	cachedOffscreenTransmissionBytes := 0
	cachedOffscreenDecodedBytes := 0
	for _, imageID := range s.kittyUploadOrder {
		cachedImage := s.uploadedKittyImages[imageID]
		if cachedImage == nil || visibleImageIDs[imageID] {
			continue
		}
		cachedOffscreenImageCount++
		cachedOffscreenTransmissionBytes += cachedImage.transmissionBytes
		cachedOffscreenDecodedBytes += cachedImage.estimatedDecodedBytes
	}

	var evicted strings.Builder
	for _, imageID := range append([]int(nil), s.kittyUploadOrder...) {
		if cachedOffscreenImageCount <= maxCachedOffscreenKittyImages &&
			cachedOffscreenTransmissionBytes <= maxCachedOffscreenKittyTransmissionBts &&
			cachedOffscreenDecodedBytes <= maxCachedOffscreenKittyDecodedBts {
			break
		}
		if visibleImageIDs[imageID] {
			continue
		}
		cachedImage := s.uploadedKittyImages[imageID]
		if cachedImage == nil {
			continue
		}
		evicted.WriteString(DeleteKittyImage(imageID))
		delete(s.uploadedKittyImages, imageID)
		s.removeFromUploadOrder(imageID)
		cachedOffscreenImageCount--
		cachedOffscreenTransmissionBytes -= cachedImage.transmissionBytes
		cachedOffscreenDecodedBytes -= cachedImage.estimatedDecodedBytes
	}
	return preparedKittyScreen{lines: lines, evictedImageDeletion: evicted.String()}
}

func (s *TuiAltScreen) removeFromUploadOrder(imageID int) {
	for i, id := range s.kittyUploadOrder {
		if id == imageID {
			s.kittyUploadOrder = append(s.kittyUploadOrder[:i], s.kittyUploadOrder[i+1:]...)
			return
		}
	}
}

func (s *TuiAltScreen) resetRenderState() {
	s.previousScreen = nil
	s.previousScreenWidth = 0
	s.previousScreenHeight = 0
	s.currentLayout = nil
}

func (s *TuiAltScreen) invalidateLocked() {
	for _, root := range s.getMountedRootsLocked() {
		root.Invalidate()
	}
	for _, overlay := range s.overlayStack {
		overlay.component.Invalidate()
	}
}

// ScrollBy scrolls the primary viewport.
func (s *TuiAltScreen) ScrollBy(lines int) {
	s.mu.Lock()
	s.getPrimaryScrollViewLocked().ScrollBy(lines)
	s.mu.Unlock()
	s.RequestRender(false)
}

// ScrollToTop scrolls to the viewport top.
func (s *TuiAltScreen) ScrollToTop() {
	s.mu.Lock()
	s.getPrimaryScrollViewLocked().ScrollToStart()
	s.mu.Unlock()
	s.RequestRender(false)
}

// ScrollToBottom scrolls to the viewport bottom.
func (s *TuiAltScreen) ScrollToBottom() {
	s.mu.Lock()
	s.getPrimaryScrollViewLocked().ScrollToEnd()
	s.mu.Unlock()
	s.RequestRender(false)
}

var osc133PromptStart = regexp.MustCompile(`^\x1b\]133;A(?:\x07|\x1b\\)`)

func (s *TuiAltScreen) scrollToPrompt(direction int) {
	if s.currentLayout == nil {
		return
	}
	scrollView := s.getPrimaryScrollViewLocked()
	var lines []string
	if box := GetScrollViewBox(*s.currentLayout, scrollView); box != nil {
		lines = box.ScrollContentLines
	}
	if lines == nil {
		return
	}

	for row := scrollView.ScrollTop() + direction; row >= 0 && row < len(lines); row += direction {
		line := ""
		if row >= 0 && row < len(lines) {
			line = lines[row]
		}
		if !osc133PromptStart.MatchString(line) {
			continue
		}
		scrollView.ScrollTo(row, ScrollViewScrollToOptions{})
		s.requestRenderLocked(false)
		return
	}
}

// Flash shows a transient message.
func (s *TuiAltScreen) Flash(message string, durationMs int) {
	s.flashes.Flash(message, durationMs)
}

func (s *TuiAltScreen) shouldDeferViewportInputToOverlayLocked() bool {
	if !s.isOverlayFocusedLocked() {
		return false
	}
	if s.activeSearch != nil && s.activeSearch.overlay != nil && s.activeSearch.overlay.IsFocused() {
		return false
	}
	return true
}

func (s *TuiAltScreen) clearComponentMouseGesture() {
	s.mouseCapture = nil
	s.mousePressTarget = nil
	s.mousePressPoint = nil
	s.mousePressMoved = false
}

type viewportInputResult struct {
	consume bool
}

var altSgrMousePattern = regexp.MustCompile(`^\x1b\[<(\d+);(\d+);(\d+)([Mm])$`)
var altLegacyMousePattern = regexp.MustCompile(`^\x1b\[<\d+;\d+;\d+[Mm]$`)

func (s *TuiAltScreen) handleViewportInput(data string) *viewportInputResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if data == focusOut {
		hadActiveSelection := s.selectionPressActive
		hadNonEmpty := hadActiveSelection && s.getSelectionBoundsLocked() != nil
		s.selectionPressActive = false
		s.stopSelectionAutoScroll()
		s.stopScrollbarHover()
		if s.activeSearch != nil && s.activeSearch.component.SetHoveredNavigationDirection(0) {
			s.requestRenderLocked(false)
		}
		s.stopScrollbarDrag()
		s.pressedUrl = ""
		s.selectionDragged = false
		s.clearComponentMouseGesture()
		s.lastComponentClick = nil
		if hadActiveSelection {
			s.selectionAnchor = nil
			s.selectionFocus = nil
			s.selectionGranularity = selectionCharacter
			s.selectionInitialRange = nil
			if hadNonEmpty {
				s.requestRenderLocked(false)
			}
		}
		s.lastClick = nil
		return &viewportInputResult{consume: true}
	}
	if data == focusIn {
		return &viewportInputResult{consume: true}
	}

	if wheel := s.parseWheelEvent(data); wheel != nil {
		delta := wheel.direction * s.getWheelScrollLines(wheel.button)
		event := s.createMouseEventLocked(MouseWheel, wheel.button, wheel.x, wheel.y, &delta, nil)
		hit, result := s.dispatchMouseToOverlayLocked(event)
		if result == nil && !hit {
			result = s.dispatchMouseToLayoutLocked(event)
		}
		if result != nil {
			if s.applyMouseDispatchResultLocked(event, result) {
				s.requestRenderLocked(false)
			}
			return &viewportInputResult{consume: true}
		}
		// Unhandled wheel input scrolls the transcript even under a
		// focused overlay; overlays wanting wheel input implement mouse
		// handling.
		s.routeWheelLocked(wheel)
		return &viewportInputResult{consume: true}
	}
	if mouse := s.parseSgrMouseEvent(data); mouse != nil {
		s.handleMouseEventLocked(mouse)
		return &viewportInputResult{consume: true}
	}
	if s.isMouseSequence(data) {
		return &viewportInputResult{consume: true}
	}

	keybindings := GetKeybindings()
	isRelease := IsKeyRelease(data)
	if keybindings.Matches(data, "tui.altScreen.search") {
		if !isRelease {
			s.toggleSearchLocked()
		}
		return &viewportInputResult{consume: true}
	}
	if s.activeSearch != nil && s.activeSearch.overlay != nil && s.activeSearch.overlay.IsFocused() {
		if keybindings.Matches(data, "tui.altScreen.searchNext") {
			if !isRelease {
				s.navigateSearchLocked(1)
			}
			return &viewportInputResult{consume: true}
		}
		if keybindings.Matches(data, "tui.altScreen.searchPrevious") {
			if !isRelease {
				s.navigateSearchLocked(-1)
			}
			return &viewportInputResult{consume: true}
		}
		if keybindings.Matches(data, "tui.altScreen.searchClose") {
			if !isRelease {
				s.closeSearchLocked()
			}
			return &viewportInputResult{consume: true}
		}
	}
	if s.shouldDeferViewportInputToOverlayLocked() {
		return nil
	}
	primary := s.getPrimaryScrollViewLocked()
	if keybindings.Matches(data, "tui.altScreen.pageUp") {
		if !isRelease {
			s.getPrimaryScrollViewLocked().ScrollBy(-maxInt(1, primary.ViewportHeight()-pageScrollOverlap))
			s.requestRenderLocked(false)
		}
		return &viewportInputResult{consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.pageDown") {
		if !isRelease {
			s.getPrimaryScrollViewLocked().ScrollBy(maxInt(1, primary.ViewportHeight()-pageScrollOverlap))
			s.requestRenderLocked(false)
		}
		return &viewportInputResult{consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.halfPageUp") {
		if !isRelease {
			s.getPrimaryScrollViewLocked().ScrollBy(-maxInt(1, primary.ViewportHeight()/2))
			s.requestRenderLocked(false)
		}
		return &viewportInputResult{consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.halfPageDown") {
		if !isRelease {
			s.getPrimaryScrollViewLocked().ScrollBy(maxInt(1, primary.ViewportHeight()/2))
			s.requestRenderLocked(false)
		}
		return &viewportInputResult{consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.lineUp") {
		if !isRelease {
			s.getPrimaryScrollViewLocked().ScrollBy(-1)
			s.requestRenderLocked(false)
		}
		return &viewportInputResult{consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.lineDown") {
		if !isRelease {
			s.getPrimaryScrollViewLocked().ScrollBy(1)
			s.requestRenderLocked(false)
		}
		return &viewportInputResult{consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.previousPrompt") {
		if !isRelease {
			s.scrollToPrompt(-1)
		}
		return &viewportInputResult{consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.nextPrompt") {
		if !isRelease {
			s.scrollToPrompt(1)
		}
		return &viewportInputResult{consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.top") {
		if !isRelease {
			s.getPrimaryScrollViewLocked().ScrollToStart()
			s.requestRenderLocked(false)
		}
		return &viewportInputResult{consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.bottom") {
		if !isRelease {
			s.getPrimaryScrollViewLocked().ScrollToEnd()
			s.requestRenderLocked(false)
		}
		return &viewportInputResult{consume: true}
	}
	return nil
}

func (s *TuiAltScreen) parseWheelEvent(data string) *wheelEvent {
	if sgr := altSgrMousePattern.FindStringSubmatch(data); sgr != nil {
		button, _ := strconv.Atoi(sgr[1])
		if button&64 == 0 {
			return nil
		}
		direction := button & 3
		if direction != 0 && direction != 1 {
			return nil
		}
		dir := -1
		if direction == 1 {
			dir = 1
		}
		x, _ := strconv.Atoi(sgr[2])
		y, _ := strconv.Atoi(sgr[3])
		return &wheelEvent{direction: dir, x: x - 1, y: y - 1, button: button}
	}
	if len(data) == 6 && strings.HasPrefix(data, "\x1b[M") {
		button := int(data[3]) - 32
		if button&64 == 0 {
			return nil
		}
		direction := button & 3
		if direction != 0 && direction != 1 {
			return nil
		}
		dir := -1
		if direction == 1 {
			dir = 1
		}
		return &wheelEvent{direction: dir, x: int(data[4]) - 33, y: int(data[5]) - 33, button: button}
	}
	return nil
}

func (s *TuiAltScreen) getWheelScrollLines(button int) int {
	// SGR mouse button codes use bit 3 (value 8) for the Alt modifier.
	if button&8 != 0 {
		return s.wheelScrollLines * altWheelMultiplier
	}
	return s.wheelScrollLines
}

func (s *TuiAltScreen) routeWheelLocked(event *wheelEvent) {
	remaining := event.direction * s.getWheelScrollLines(event.button)
	seen := map[*ScrollView]bool{}
	if s.currentLayout != nil {
		for _, scrollView := range GetScrollViewsAt(*s.currentLayout, event.x, event.y) {
			seen[scrollView] = true
			remaining = scrollView.ScrollBy(remaining)
			if remaining == 0 || scrollView.OverscrollMode == "contain" {
				break
			}
		}
	}
	primary := s.getPrimaryScrollViewLocked()
	if remaining != 0 && !seen[primary] {
		primary.ScrollBy(remaining)
	}
	s.updateScrollbarHoverLocked(event.x, event.y)
	s.requestRenderLocked(false)
}

func (s *TuiAltScreen) parseSgrMouseEvent(data string) *sgrMouseEvent {
	match := altSgrMousePattern.FindStringSubmatch(data)
	if match == nil {
		return nil
	}
	button, _ := strconv.Atoi(match[1])
	x, _ := strconv.Atoi(match[2])
	y, _ := strconv.Atoi(match[3])
	return &sgrMouseEvent{button: button, x: x - 1, y: y - 1, release: match[4] == "m"}
}

func (s *TuiAltScreen) isMouseSequence(data string) bool {
	return altLegacyMousePattern.MatchString(data) || (len(data) == 6 && strings.HasPrefix(data, "\x1b[M"))
}

func (s *TuiAltScreen) decodeMouseButton(button int) TuiMouseButton {
	switch button & 3 {
	case 0:
		return MouseLeft
	case 1:
		return MouseMiddle
	case 2:
		return MouseRight
	default:
		return MouseNone
	}
}

func (s *TuiAltScreen) createMouseEventLocked(eventType TuiMouseEventType, button, x, y int, wheelDelta *int, clickCount *int) TuiMouseEvent {
	mouseButton := MouseNone
	if eventType != MouseWheel {
		mouseButton = s.decodeMouseButton(button)
	}
	return TuiMouseEvent{
		Type:       eventType,
		Button:     mouseButton,
		X:          x,
		Y:          y,
		ScreenX:    x,
		ScreenY:    y,
		Width:      maxInt(1, s.terminalRef.Columns()),
		Height:     maxInt(1, s.terminalRef.Rows()),
		Shift:      button&4 != 0,
		Alt:        button&8 != 0,
		Ctrl:       button&16 != 0,
		WheelDelta: wheelDelta,
		ClickCount: clickCount,
	}
}

func (s *TuiAltScreen) dispatchMouseToLayoutLocked(event TuiMouseEvent) *TuiMouseDispatchResult {
	if s.currentLayout == nil {
		return nil
	}
	visited := map[Component]bool{}
	boxes := GetLayoutBoxesAt(*s.currentLayout, event.ScreenX, event.ScreenY)
	for _, box := range boxes {
		if visited[box.Component] {
			continue
		}
		// Skip layout nodes that only forward through the default container
		// handler; the alt screen dispatches directly to leaf components.
		if GetLayoutNode(box.Component) != nil {
			if _, isPlainContainer := box.Component.(*Container); isPlainContainer {
				continue
			}
			if _, isStack := box.Component.(*Stack); isStack {
				continue
			}
		}
		visited[box.Component] = true
		local := event
		local.X = event.ScreenX - box.Rect.X
		local.Y = event.ScreenY - box.Rect.Y
		local.Width = box.Rect.Width
		local.Height = box.Rect.Height
		if result := DispatchMouseEvent(box.Component, local); result != nil {
			return result
		}
	}
	return nil
}

func (s *TuiAltScreen) applyMouseDispatchResultLocked(event TuiMouseEvent, result *TuiMouseDispatchResult) bool {
	var focusTarget Component
	if result.FocusTarget != nil {
		focusTarget = result.FocusTarget
	} else {
		focusTarget = result.Target.Component
	}
	focusTarget = s.resolveMouseFocusTargetLocked(focusTarget)
	focusChanged := result.Focus && s.focusedComponent != focusTarget
	if result.Focus {
		s.setFocusInternal(focusTarget, false)
	}
	if result.Capture {
		s.mouseCapture = &result.Target
	}
	if result.Render != nil {
		return *result.Render
	}
	return focusChanged || event.Type == MousePress || event.Type == MouseClick ||
		event.Type == MouseDrag || event.Type == MouseWheel
}

func (s *TuiAltScreen) dispatchMouseToTargetLocked(event TuiMouseEvent, target TuiMouseDispatchTarget) *TuiMouseDispatchResult {
	return DispatchMouseEvent(target.Component, RetargetMouseEvent(event, target))
}

func (s *TuiAltScreen) getComponentClickCount(target TuiMouseDispatchTarget, x, y int) int {
	now := s.Now()
	previous := s.lastComponentClick
	count := 1
	if previous != nil &&
		now-previous.timestamp <= doubleClickIntervalMs &&
		previous.component == target.Component &&
		previous.x == x && previous.y == y {
		count = previous.count%3 + 1
	}
	s.lastComponentClick = &struct {
		timestamp timeNano
		count     int
		component Component
		x, y      int
	}{timestamp: now, count: count, component: target.Component, x: x, y: y}
	return count
}

func (s *TuiAltScreen) clearTextSelection() {
	s.stopSelectionAutoScroll()
	s.selectionPressActive = false
	s.selectionAnchor = nil
	s.selectionFocus = nil
	s.selectionGranularity = selectionCharacter
	s.selectionInitialRange = nil
	s.pressedUrl = ""
	s.selectionDragged = false
}

// ExitRestorePrefix prepends the alt-screen mode resets: synchronized
// output ends, mouse tracking stops, autowrap re-enables, then the base
// prefix (kitty flags were popped by the terminal's own restore).
func (s *TuiAltScreen) ExitRestorePrefix() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitRestorePrefixLocked()
}

// exitRestorePrefixLocked requires s.mu.
func (s *TuiAltScreen) exitRestorePrefixLocked() string {
	prefix := s.TuiBase.ExitRestorePrefix()
	if !s.altScreenActive {
		return prefix
	}
	restored := endSyncOutput
	if s.mouseEnabled {
		restored += disableMouseSeq
	}
	restored += enableAutowrap
	return restored + prefix
}

// ExitRestoreSuffix leaves the alternate screen and shows the cursor.
func (s *TuiAltScreen) ExitRestoreSuffix() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitRestoreSuffixLocked()
}

// exitRestoreSuffixLocked requires s.mu.
func (s *TuiAltScreen) exitRestoreSuffixLocked() string {
	suffix := s.TuiBase.ExitRestoreSuffix()
	if !s.altScreenActive {
		return suffix
	}
	return suffix + exitAltScreen + "\x1b[?25h"
}

// ResetTextSelection clears the selection so a transcript rebuild starts
// from a clean selection state; a click is no longer pending.
func (s *TuiAltScreen) ResetTextSelection() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearTextSelection()
	s.lastClick = nil
}

func (s *TuiAltScreen) handleMouseEventLocked(raw *sgrMouseEvent) {
	isMotion := raw.button&32 != 0
	var eventType TuiMouseEventType
	switch {
	case raw.release:
		eventType = MouseRelease
	case isMotion && s.decodeMouseButton(raw.button) == MouseNone:
		eventType = MouseMove
	case isMotion:
		eventType = MouseDrag
	default:
		eventType = MousePress
	}
	event := s.createMouseEventLocked(eventType, raw.button, raw.x, raw.y, nil, nil)

	if s.mouseCapture != nil || s.mousePressTarget != nil {
		var target TuiMouseDispatchTarget
		if s.mouseCapture != nil {
			target = *s.mouseCapture
		} else {
			target = *s.mousePressTarget
		}
		if s.mousePressPoint != nil && (raw.x != s.mousePressPoint.x || raw.y != s.mousePressPoint.y) {
			s.mousePressMoved = true
			s.lastComponentClick = nil
		}
		render := false
		if targetResult := s.dispatchMouseToTargetLocked(event, target); targetResult != nil {
			render = s.applyMouseDispatchResultLocked(event, targetResult)
		}
		if raw.release {
			if !s.mousePressMoved && s.mousePressPoint != nil &&
				s.mousePressPoint.x == raw.x && s.mousePressPoint.y == raw.y {
				clickCount := s.getComponentClickCount(target, raw.x, raw.y)
				clickEvent := s.createMouseEventLocked(MouseClick, raw.button, raw.x, raw.y, nil, &clickCount)
				if clickResult := s.dispatchMouseToTargetLocked(clickEvent, target); clickResult != nil {
					if s.applyMouseDispatchResultLocked(clickEvent, clickResult) {
						render = true
					}
				}
			}
			s.clearComponentMouseGesture()
		}
		if render {
			s.requestRenderLocked(false)
		}
		return
	}

	if s.handleSearchMouseEventLocked(raw) {
		return
	}

	hit, overlayResult := s.dispatchMouseToOverlayLocked(event)
	if !hit {
		if s.handleScrollToEndIndicatorMouseEventLocked(raw) {
			return
		}
		scrollbarHandled := s.handleScrollbarMouseEventLocked(raw)
		if s.scrollbarDrag == nil {
			s.updateScrollbarHoverLocked(raw.x, raw.y)
		}
		if scrollbarHandled {
			return
		}
	} else {
		s.stopScrollbarHover()
	}

	result := overlayResult
	if result == nil && !hit {
		result = s.dispatchMouseToLayoutLocked(event)
	}
	if result != nil {
		render := s.applyMouseDispatchResultLocked(event, result)
		if event.Type == MousePress {
			s.clearTextSelection()
			s.mousePressTarget = &result.Target
			s.mousePressPoint = &struct{ x, y int }{x: raw.x, y: raw.y}
			s.mousePressMoved = false
		}
		if render {
			s.requestRenderLocked(false)
		}
		return
	}

	if s.handleRightClickPasteLocked(raw) {
		return
	}
	s.handleSelectionMouseEventLocked(raw)
}

func (s *TuiAltScreen) handleRightClickPasteLocked(event *sgrMouseEvent) bool {
	if s.onRightClickPaste == nil ||
		runtimeGOOS() != "windows" ||
		strings.ToLower(os.Getenv("TERM_PROGRAM")) == "vscode" ||
		event.release ||
		event.button != 2 {
		return false
	}
	// Clipboard paste is best-effort.
	s.onRightClickPaste()
	return true
}

func (s *TuiAltScreen) handleScrollToEndIndicatorMouseEventLocked(event *sgrMouseEvent) bool {
	rect := s.scrollToEndIndicatorRect
	if rect == nil || event.release || event.button&32 != 0 || event.button&3 != 0 {
		return false
	}
	if event.y != rect.row || event.x < rect.column || event.x >= rect.column+rect.width {
		return false
	}
	s.getPrimaryScrollViewLocked().ScrollToEnd()
	s.requestRenderLocked(false)
	return true
}

func (s *TuiAltScreen) getScrollbarTargetAt(x, y int, includeHiddenAuto bool) *scrollbarTarget {
	if s.HasOverlay() || s.currentLayout == nil {
		return nil
	}
	for _, scrollView := range GetScrollViewsAt(*s.currentLayout, x, y) {
		var box *LayoutBox
		if b := GetScrollViewBox(*s.currentLayout, scrollView); b != nil {
			boxCopy := *b
			box = &boxCopy
		}
		var geometry *ScrollbarGeometry
		if box != nil {
			geometry = GetScrollbarGeometry(box, includeHiddenAuto)
		}
		if geometry != nil &&
			x == geometry.Column &&
			y >= geometry.TrackTop &&
			y < geometry.TrackTop+geometry.TrackHeight {
			return &scrollbarTarget{scrollView: scrollView, geometry: geometry}
		}
	}
	return nil
}

func (s *TuiAltScreen) setScrollbarHover(scrollView *ScrollView) {
	if scrollView == s.scrollbarHover {
		return
	}
	if s.scrollbarHover != nil {
		s.scrollbarHover.SetScrollbarActive(false)
	}
	s.scrollbarHover = scrollView
	if s.scrollbarHover != nil {
		s.scrollbarHover.SetScrollbarActive(true)
	}
}

func (s *TuiAltScreen) updateScrollbarHoverLocked(x, y int) {
	var target *ScrollView
	if t := s.getScrollbarTargetAt(x, y, true); t != nil {
		target = t.scrollView
	}
	s.setScrollbarHover(target)
}

func (s *TuiAltScreen) stopScrollbarHover() {
	s.setScrollbarHover(nil)
}

func (s *TuiAltScreen) scrollScrollbarToPointer(scrollView *ScrollView, geometry *ScrollbarGeometry, pointerY, grabOffset int) {
	maxThumbOffset := geometry.TrackHeight - geometry.ThumbHeight
	thumbOffset := maxInt(0, minInt(maxThumbOffset, pointerY-geometry.TrackTop-grabOffset))
	scrollTop := 0
	if maxThumbOffset != 0 {
		scrollTop = jsRoundInt((thumbOffset * geometry.MaxScrollTop) / maxThumbOffset)
	}
	scrollView.ScrollTo(scrollTop, ScrollViewScrollToOptions{})
}

func (s *TuiAltScreen) handleScrollbarMouseEventLocked(event *sgrMouseEvent) bool {
	if s.scrollbarDrag != nil {
		if event.release {
			s.stopScrollbarDrag()
			return true
		}
		var geometry *ScrollbarGeometry
		if s.currentLayout != nil {
			if box := GetScrollViewBox(*s.currentLayout, s.scrollbarDrag.scrollView); box != nil {
				geometry = GetScrollbarGeometry(box, false)
			}
		}
		if geometry != nil {
			s.scrollScrollbarToPointer(s.scrollbarDrag.scrollView, geometry, event.y, s.scrollbarDrag.grabOffset)
		}
		return true
	}

	if event.release || event.button&32 != 0 || event.button&3 != 0 {
		return false
	}
	target := s.getScrollbarTargetAt(event.x, event.y, false)
	if target == nil {
		return false
	}
	s.stopSelectionAutoScroll()
	s.selectionPressActive = false
	s.selectionAnchor = nil
	s.selectionFocus = nil
	s.selectionGranularity = selectionCharacter
	s.selectionInitialRange = nil
	s.lastClick = nil
	s.pressedUrl = ""
	s.selectionDragged = false
	s.setScrollbarHover(target.scrollView)
	onThumb := event.y >= target.geometry.ThumbTop && event.y < target.geometry.ThumbTop+target.geometry.ThumbHeight
	grabOffset := event.y - target.geometry.ThumbTop
	if !onThumb {
		grabOffset = target.geometry.ThumbHeight / 2
		s.scrollScrollbarToPointer(target.scrollView, target.geometry, event.y, grabOffset)
	}
	s.scrollbarDrag = &scrollbarDrag{scrollView: target.scrollView, grabOffset: grabOffset}
	return true
}

func (s *TuiAltScreen) stopScrollbarDrag() {
	s.scrollbarDrag = nil
}

var _ = rand.Intn
var _ = base64.StdEncoding
