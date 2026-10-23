package tui

// TuiAltScreen continued: selection model, search session, and the render
// pipeline. Continuation of the tui-alt-screen.ts port.

import (
	"encoding/base64"
	"os"
	"strings"
	"time"
)

func (s *TuiAltScreen) toggleSearchLocked() {
	if s.activeSearch != nil {
		s.closeSearchLocked()
		return
	}
	component := NewAltScreenSearchComponent(
		func(query string) { s.updateSearchQueryLocked(query) },
		s.searchNavigationButtonStyle,
	)
	minWidth := 32
	search := &ActiveSearch{
		component:     component,
		query:         "",
		selectedIndex: -1,
		anchorRow:     s.getPrimaryScrollViewLocked().ScrollTop(),
		selectionMode: searchModeQuery,
	}
	s.activeSearch = search
	search.overlay = s.ShowOverlay(component, &OverlayOptions{
		Anchor:   AnchorTopRight,
		Width:    "40%",
		MinWidth: &minWidth,
		Margin:   1,
	})
}

func (s *TuiAltScreen) closeSearchLocked() {
	search := s.activeSearch
	if search == nil {
		return
	}
	s.activeSearch = nil
	if search.overlay != nil {
		search.overlay.Hide()
	}
	s.requestRenderLocked(false)
}

func (s *TuiAltScreen) updateSearchQueryLocked(query string) {
	search := s.activeSearch
	if search == nil || query == search.query {
		return
	}
	anchor := s.getPrimaryScrollViewLocked().ScrollTop()
	if search.selectedIndex >= 0 && search.selectedIndex < len(search.matches) {
		if segments := search.matches[search.selectedIndex].Segments; len(segments) > 0 {
			anchor = segments[0].Row
		}
	}
	search.anchorRow = anchor
	search.query = query
	search.selectionMode = searchModeQuery
	search.component.SetResult(-1, 0)
	s.requestRenderLocked(false)
}

func (s *TuiAltScreen) navigateSearchLocked(direction int) {
	search := s.activeSearch
	if search == nil || search.query == "" {
		return
	}
	if direction < 0 {
		search.selectionMode = searchModePrevious
	} else {
		search.selectionMode = searchModeNext
	}
	s.requestRenderLocked(false)
}

func (s *TuiAltScreen) getSearchNavigationDirectionAtLocked(x, y int) int {
	search := s.activeSearch
	if search == nil || search.overlay == nil {
		return 0
	}
	bounds := search.overlay.GetBounds()
	if bounds == nil {
		return 0
	}
	if x < bounds.Col || x >= bounds.Col+bounds.Width || y < bounds.Row || y >= bounds.Row+bounds.Height {
		return 0
	}
	return search.component.GetNavigationDirectionAt(y-bounds.Row, x-bounds.Col)
}

func (s *TuiAltScreen) handleSearchMouseEventLocked(event *sgrMouseEvent) bool {
	search := s.activeSearch
	if search == nil {
		return false
	}
	direction := s.getSearchNavigationDirectionAtLocked(event.x, event.y)
	if search.component.SetHoveredNavigationDirection(direction) {
		s.requestRenderLocked(false)
	}
	if direction == 0 || event.release || event.button&32 != 0 || event.button&3 != 0 {
		return false
	}
	s.navigateSearchLocked(direction)
	return true
}

func (s *TuiAltScreen) refreshSearch(layout LayoutFrame) bool {
	search := s.activeSearch
	if search == nil {
		return false
	}
	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = s.implicitScrollView
	}
	var lines []string
	if box := GetScrollViewBox(layout, scrollView); box != nil {
		lines = box.ScrollContentLines
	}
	if lines == nil || strings.TrimSpace(search.query) == "" {
		search.matches = nil
		search.selectedIndex = -1
		search.selectedKey = ""
		search.hasSelectedKey = false
		search.selectionMode = searchModeRetain
		search.component.SetResult(-1, 0)
		return false
	}

	shouldRevealSelection := search.selectionMode != searchModeRetain
	result := search.index.Search(lines, search.query)
	matches := result.Matches
	search.matches = matches
	if !result.Changed && search.selectionMode == searchModeRetain {
		return false
	}

	exactIndex := search.selectedIndex
	if result.Changed {
		exactIndex = -1
		if search.hasSelectedKey {
			for i, match := range matches {
				if GetAltScreenSearchMatchKey(match) == search.selectedKey {
					exactIndex = i
					break
				}
			}
		}
	}
	selectedIndex := -1
	if len(matches) > 0 {
		switch search.selectionMode {
		case searchModeQuery:
			low, high := 0, len(matches)
			for low < high {
				middle := low + (high-low)/2
				row := 0
				if segments := matches[middle].Segments; len(segments) > 0 {
					row = segments[0].Row
				}
				if row < search.anchorRow {
					low = middle + 1
				} else {
					high = middle
				}
			}
			selectedIndex = low
			if low >= len(matches) {
				selectedIndex = 0
			}
		case searchModeNext:
			baseIndex := exactIndex
			if baseIndex < 0 {
				baseIndex = minInt(search.selectedIndex, len(matches)-1)
			}
			if baseIndex < 0 {
				selectedIndex = 0
			} else {
				selectedIndex = (baseIndex + 1) % len(matches)
			}
		case searchModePrevious:
			baseIndex := exactIndex
			if baseIndex < 0 {
				baseIndex = minInt(search.selectedIndex, len(matches)-1)
			}
			if baseIndex < 0 {
				selectedIndex = len(matches) - 1
			} else {
				selectedIndex = (baseIndex - 1 + len(matches)) % len(matches)
			}
		default:
			if exactIndex >= 0 {
				selectedIndex = exactIndex
			} else {
				selectedIndex = minInt(maxInt(0, search.selectedIndex), len(matches)-1)
			}
		}
	}

	search.selectedIndex = selectedIndex
	if selectedIndex >= 0 {
		search.selectedKey = GetAltScreenSearchMatchKey(matches[selectedIndex])
		search.hasSelectedKey = true
	} else {
		search.selectedKey = ""
		search.hasSelectedKey = false
	}
	search.selectionMode = searchModeRetain
	search.component.SetResult(selectedIndex, len(matches))
	if !shouldRevealSelection {
		return false
	}

	if selectedIndex < 0 || selectedIndex >= len(matches) {
		return false
	}
	selected := matches[selectedIndex]
	if len(selected.Segments) == 0 || scrollView.ViewportHeight() <= 0 {
		return false
	}
	firstSegment := selected.Segments[0]
	lastSegment := selected.Segments[len(selected.Segments)-1]
	box := GetScrollViewBox(layout, scrollView)
	if box == nil {
		return false
	}
	before := scrollView.ScrollTop()
	visibleBottom := before + scrollView.ViewportHeight() - 1
	target := before
	if firstSegment.Row < before || lastSegment.Row > visibleBottom {
		target = firstSegment.Row - scrollView.ViewportHeight()/3
	}
	scrollView.ScrollTo(target, ScrollViewScrollToOptions{DisableFollow: true})
	return scrollView.ScrollTop() != before
}

func (s *TuiAltScreen) getScrollSelectionPoint(scrollView *ScrollView, x, y int) *selectionPoint {
	if s.currentLayout == nil {
		return nil
	}
	box := GetScrollViewBox(*s.currentLayout, scrollView)
	if box == nil || box.Rect.Height <= 0 || box.Clip.Height <= 0 {
		return nil
	}
	visibleTop := maxInt(0, maxInt(box.Rect.Y, box.Clip.Y))
	visibleBottom := minInt(s.terminalRef.Rows()-1, minInt(box.Rect.Y+box.Rect.Height-1, box.Clip.Y+box.Clip.Height-1))
	if visibleBottom < visibleTop {
		return nil
	}
	pointerRow := maxInt(visibleTop, minInt(visibleBottom, y))
	contentLines := 1
	if box.ScrollContentLines != nil {
		contentLines = len(box.ScrollContentLines)
	}
	maxContentRow := maxInt(0, contentLines-1)
	return &selectionPoint{
		row:        maxInt(0, minInt(maxContentRow, scrollView.ScrollTop()+pointerRow-box.Rect.Y)),
		col:        maxInt(0, minInt(box.Rect.Width-1, x-box.Rect.X)),
		scrollView: scrollView,
	}
}

func (s *TuiAltScreen) getSelectionPoint(event *sgrMouseEvent, scrollView *ScrollView) selectionPoint {
	if scrollView != nil {
		if point := s.getScrollSelectionPoint(scrollView, event.x, event.y); point != nil {
			return *point
		}
	}
	return selectionPoint{
		row: maxInt(0, minInt(s.terminalRef.Rows()-1, event.y)),
		col: maxInt(0, minInt(s.terminalRef.Columns()-1, event.x)),
	}
}

func (s *TuiAltScreen) getSelectionSourceLine(point selectionPoint) string {
	if point.scrollView != nil && s.currentLayout != nil {
		if box := GetScrollViewBox(*s.currentLayout, point.scrollView); box != nil && box.ScrollContentLines != nil {
			if point.row >= 0 && point.row < len(box.ScrollContentLines) {
				return box.ScrollContentLines[point.row]
			}
			return ""
		}
	}
	if point.row >= 0 && point.row < len(s.previousScreen) {
		return s.previousScreen[point.row]
	}
	return ""
}

type wordSegmentInfo struct {
	start      int
	end        int
	selectable bool
	joiner     bool
}

func (s *TuiAltScreen) getWordSelection(point selectionPoint) *selectionRange {
	line := StripTerminalSequences(s.getSelectionSourceLine(point))
	var segments []wordSegmentInfo
	start := 0
	for _, segment := range segmentWords(line) {
		end := start + VisibleWidth(segment.Segment)
		joiner := terminalWordSelectionJoiners[segment.Segment]
		segments = append(segments, wordSegmentInfo{start: start, end: end, selectable: segment.IsWordLike || joiner, joiner: joiner})
		start = end
	}
	clickedSegmentIndex := -1
	for i, segment := range segments {
		if point.col >= segment.start && point.col < segment.end {
			clickedSegmentIndex = i
			break
		}
	}
	if clickedSegmentIndex < 0 {
		return nil
	}

	canJoin := func(left, right wordSegmentInfo) bool {
		return left.selectable && right.selectable && (left.joiner || right.joiner)
	}
	selectionStart := segments[clickedSegmentIndex].start
	selectionEnd := segments[clickedSegmentIndex].end
	for index := clickedSegmentIndex; index > 0 && canJoin(segments[index-1], segments[index]); index-- {
		selectionStart = segments[index-1].start
	}
	for index := clickedSegmentIndex; index < len(segments)-1 && canJoin(segments[index], segments[index+1]); index++ {
		selectionEnd = segments[index+1].end
	}
	startPoint := point
	startPoint.col = selectionStart
	endPoint := point
	endPoint.col = selectionEnd
	endPoint.boundary = true
	return &selectionRange{start: startPoint, end: endPoint}
}

func (s *TuiAltScreen) getLineSelection(point selectionPoint) selectionRange {
	start := point
	start.col = 0
	end := point
	end.col = VisibleWidth(s.getSelectionSourceLine(point))
	end.boundary = true
	return selectionRange{start: start, end: end}
}

func (s *TuiAltScreen) updateSelectionFocus(point selectionPoint) {
	if s.selectionGranularity == selectionCharacter || s.selectionInitialRange == nil {
		s.selectionFocus = &point
		return
	}
	var range_ *selectionRange
	if s.selectionGranularity == selectionWord {
		range_ = s.getWordSelection(point)
	} else {
		line := s.getLineSelection(point)
		range_ = &line
	}
	if range_ == nil {
		return
	}
	initial := s.selectionInitialRange
	targetBeforeInitial := range_.start.row < initial.start.row ||
		(range_.start.row == initial.start.row && range_.start.col < initial.start.col)
	if targetBeforeInitial {
		s.selectionAnchor = &initial.end
		s.selectionFocus = &range_.start
	} else {
		s.selectionAnchor = &initial.start
		s.selectionFocus = &range_.end
	}
}

func (s *TuiAltScreen) getClickCount(point selectionPoint, word *selectionRange) int {
	now := s.Now()
	previous := s.lastClick
	count := 1
	if word != nil && previous != nil &&
		now-previous.timestamp <= doubleClickIntervalMs &&
		previous.row == point.row &&
		previous.scrollView == point.scrollView &&
		previous.wordStart == word.start.col &&
		previous.wordEnd == word.end.col {
		count = previous.count%3 + 1
	}
	if word != nil {
		s.lastClick = &clickTarget{
			timestamp:  now,
			count:      count,
			row:        point.row,
			scrollView: point.scrollView,
			wordStart:  word.start.col,
			wordEnd:    word.end.col,
		}
	} else {
		s.lastClick = nil
	}
	return count
}

func (s *TuiAltScreen) updateSelectionAutoScroll(event *sgrMouseEvent) {
	var scrollView *ScrollView
	if s.selectionAnchor != nil {
		scrollView = s.selectionAnchor.scrollView
	}
	if scrollView == nil || s.currentLayout == nil {
		s.stopSelectionAutoScroll()
		return
	}
	box := GetScrollViewBox(*s.currentLayout, scrollView)
	if box == nil || box.Rect.Height <= 0 || box.Clip.Height <= 0 {
		s.stopSelectionAutoScroll()
		return
	}
	visibleTop := maxInt(0, maxInt(box.Rect.Y, box.Clip.Y))
	visibleBottom := minInt(s.terminalRef.Rows()-1, minInt(box.Rect.Y+box.Rect.Height-1, box.Clip.Y+box.Clip.Height-1))
	s.selectionDragPointer = &struct{ x, y int }{x: event.x, y: event.y}
	direction := 0
	if event.y <= visibleTop {
		direction = -1
	} else if event.y >= visibleBottom {
		direction = 1
	}
	s.selectionAutoScrollDirection = direction
	if direction == 0 {
		s.stopSelectionAutoScroll()
		return
	}
	if s.selectionAutoScrollTimer != nil {
		return
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	s.selectionAutoScrollTimer = ticker
	go func() {
		for range ticker.C {
			s.mu.Lock()
			if s.selectionAutoScrollTimer != ticker {
				ticker.Stop()
				s.mu.Unlock()
				return
			}
			s.autoScrollSelectionLocked()
			s.mu.Unlock()
		}
	}()
}

func (s *TuiAltScreen) autoScrollSelectionLocked() {
	var scrollView *ScrollView
	if s.selectionAnchor != nil {
		scrollView = s.selectionAnchor.scrollView
	}
	pointer := s.selectionDragPointer
	direction := s.selectionAutoScrollDirection
	if scrollView == nil || pointer == nil || direction == 0 {
		s.stopSelectionAutoScroll()
		return
	}
	remaining := scrollView.ScrollBy(direction)
	if remaining == direction {
		s.stopSelectionAutoScroll()
		return
	}
	if point := s.getScrollSelectionPoint(scrollView, pointer.x, pointer.y); point != nil {
		s.updateSelectionFocus(*point)
	}
	s.requestRenderLocked(false)
}

func (s *TuiAltScreen) stopSelectionAutoScroll() {
	if s.selectionAutoScrollTimer != nil {
		s.selectionAutoScrollTimer.Stop()
		s.selectionAutoScrollTimer = nil
	}
	s.selectionAutoScrollDirection = 0
	s.selectionDragPointer = nil
}

func (s *TuiAltScreen) handleSelectionMouseEventLocked(event *sgrMouseEvent) {
	button := event.button & 3
	if button != 0 && !(event.release && button == 3) {
		return
	}
	var anchorScrollView *ScrollView
	if s.selectionAnchor != nil {
		anchorScrollView = s.selectionAnchor.scrollView
	}
	point := s.getSelectionPoint(event, anchorScrollView)
	if event.release {
		if !s.selectionPressActive {
			return
		}
		s.selectionPressActive = false
		s.stopSelectionAutoScroll()
		if s.selectionAnchor == nil {
			return
		}
		s.updateSelectionFocus(point)
		isClick := !s.selectionDragged &&
			s.selectionAnchor.scrollView == point.scrollView &&
			s.selectionAnchor.row == point.row &&
			s.selectionAnchor.col == point.col
		clickedUrl := ""
		if isClick {
			clickedUrl = s.pressedUrl
		}
		s.pressedUrl = ""
		if clickedUrl != "" && s.openUrl != nil {
			s.selectionAnchor = nil
			s.selectionFocus = nil
			// URL activation is best-effort.
			s.openUrl(clickedUrl)
			s.requestRenderLocked(false)
			return
		}
		if isClick {
			clickCount := 1
			if s.lastClick != nil {
				clickCount = s.lastClick.count
			}
			clickEvent := s.createMouseEventLocked(MouseClick, event.button, event.x, event.y, nil, &clickCount)
			hit, overlayResult := s.dispatchMouseToOverlayLocked(clickEvent)
			result := overlayResult
			if result == nil && !hit {
				result = s.dispatchMouseToLayoutLocked(clickEvent)
			}
			if result != nil {
				render := s.applyMouseDispatchResultLocked(clickEvent, result)
				s.clearTextSelection()
				if render {
					s.requestRenderLocked(false)
				}
				return
			}
		}
		if s.copyOnSelect {
			go s.copySelectionToClipboard()
		}
		s.requestRenderLocked(false)
		return
	}
	if event.button&32 != 0 {
		if !s.selectionPressActive || s.selectionAnchor == nil {
			return
		}
		s.selectionDragged = true
		s.lastClick = nil
		s.pressedUrl = ""
		s.updateSelectionFocus(point)
		s.updateSelectionAutoScroll(event)
		s.requestRenderLocked(false)
		return
	}
	s.stopSelectionAutoScroll()
	s.selectionPressActive = true
	var scrollView *ScrollView
	if !s.HasOverlay() && s.currentLayout != nil {
		if views := GetScrollViewsAt(*s.currentLayout, event.x, event.y); len(views) > 0 {
			scrollView = views[0]
		}
	}
	anchor := s.getSelectionPoint(event, scrollView)
	word := s.getWordSelection(anchor)
	clickCount := s.getClickCount(anchor, word)
	var range_ *selectionRange
	granularity := selectionCharacter
	if clickCount == 2 && word != nil {
		range_ = word
		granularity = selectionWord
	} else if clickCount == 3 {
		line := s.getLineSelection(anchor)
		range_ = &line
		granularity = selectionLine
	}
	s.selectionGranularity = granularity
	s.selectionInitialRange = range_
	if range_ != nil {
		anchorCopy := range_.start
		focusCopy := range_.end
		s.selectionAnchor = &anchorCopy
		s.selectionFocus = &focusCopy
	} else {
		anchorCopy := anchor
		s.selectionAnchor = &anchorCopy
		focusCopy := anchor
		s.selectionFocus = &focusCopy
	}
	s.selectionDragged = false
	if range_ != nil {
		s.pressedUrl = ""
	} else {
		row := maxInt(0, minInt(s.terminalRef.Rows()-1, event.y))
		line := ""
		if row >= 0 && row < len(s.previousScreen) {
			line = s.previousScreen[row]
		}
		s.pressedUrl = GetOsc8LinkAtColumn(line, maxInt(0, minInt(s.terminalRef.Columns()-1, event.x)))
	}
	s.requestRenderLocked(false)
}

type selectionBounds struct {
	start selectionPoint
	end   selectionPoint
}

func (s *TuiAltScreen) getSelectionBounds() *selectionBounds {
	if s.selectionAnchor == nil || s.selectionFocus == nil {
		return nil
	}
	if s.selectionAnchor.scrollView != s.selectionFocus.scrollView {
		return nil
	}
	anchorBeforeFocus := s.selectionAnchor.row < s.selectionFocus.row ||
		(s.selectionAnchor.row == s.selectionFocus.row && s.selectionAnchor.col < s.selectionFocus.col)
	if s.selectionAnchor.row == s.selectionFocus.row && s.selectionAnchor.col == s.selectionFocus.col {
		return nil
	}
	if anchorBeforeFocus {
		return &selectionBounds{start: *s.selectionAnchor, end: *s.selectionFocus}
	}
	return &selectionBounds{start: *s.selectionFocus, end: *s.selectionAnchor}
}

// getSelectionBoundsLocked mirrors getSelectionBounds for locked callers.
func (s *TuiAltScreen) getSelectionBoundsLocked() *selectionBounds {
	return s.getSelectionBounds()
}

type selectionColumns struct {
	start int
	end   int
}

func (s *TuiAltScreen) getSelectionColumns(line string, row int, selection selectionBounds, minColumn, maxColumn int) selectionColumns {
	lineWidth := VisibleWidth(line)
	start := maxInt(0, minColumn)
	end := minInt(lineWidth, maxColumn)
	if row == selection.start.row {
		if r := GetGraphemeCellRange(line, selection.start.col); r != nil {
			start = r.Start
		} else {
			start = minInt(selection.start.col, lineWidth)
		}
	}
	if row == selection.end.row {
		if selection.end.boundary {
			end = minInt(selection.end.col, lineWidth)
		} else if r := GetGraphemeCellRange(line, selection.end.col); r != nil {
			end = r.End
		} else {
			end = minInt(selection.end.col+1, lineWidth)
		}
	}
	return selectionColumns{start: maxInt(minColumn, start), end: minInt(maxColumn, end)}
}

func (s *TuiAltScreen) getActiveSelectionTextLocked() string {
	selection := s.getSelectionBounds()
	if selection == nil {
		return ""
	}
	sourceLines := s.previousScreen
	if selection.start.scrollView != nil {
		if s.currentLayout == nil {
			return ""
		}
		box := GetScrollViewBox(*s.currentLayout, selection.start.scrollView)
		if box == nil || box.ScrollContentLines == nil {
			return ""
		}
		sourceLines = box.ScrollContentLines
	}
	var lines []string
	for row := selection.start.row; row <= selection.end.row; row++ {
		line := ""
		if row >= 0 && row < len(sourceLines) {
			line = sourceLines[row]
		}
		columns := s.getSelectionColumns(line, row, *selection, 0, VisibleWidth(line))
		sliced := SliceByColumn(line, columns.start, maxInt(0, columns.end-columns.start), true)
		lines = append(lines, strings.TrimRight(StripTerminalSequences(sliced), " "))
	}
	return strings.Join(lines, "\n")
}

func (s *TuiAltScreen) copySelectionToClipboard() {
	s.mu.Lock()
	text := s.getActiveSelectionTextLocked()
	s.mu.Unlock()
	if text == "" {
		return
	}
	s.copyTextToClipboard(text)
}

func (s *TuiAltScreen) copyTextToClipboard(text string) {
	s.mu.Lock()
	copySelection := s.copySelection
	s.mu.Unlock()
	if copySelection != nil {
		// Injected implementation reports success/failure synchronously.
		result := copySelection(text)()
		s.mu.Lock()
		ok := result == true
		if ok {
			s.flashes.Flash("Copied!", 0)
		} else if message, isString := result.(string); isString {
			s.flashes.Flash(message, copyErrorFlashDurationMs)
		} else {
			s.flashes.Flash("Copy failed", copyErrorFlashDurationMs)
		}
		s.mu.Unlock()
		return
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	s.mu.Lock()
	s.terminalRef.Write("\x1b]52;c;" + encoded + "\x07")
	s.flashes.Flash("Copied!", 0)
	s.mu.Unlock()
}

func (s *TuiAltScreen) applySearchTextHighlight(text string, current bool) string {
	style := s.searchMatchStyle
	if current {
		style = s.searchCurrentMatchStyle
	}
	var result strings.Builder
	plainStart := 0
	runes := []rune(text)
	index := 0
	for index < len(runes) {
		code, n := extractAnsiCodeAt(runes, index)
		if n == 0 {
			index++
			continue
		}
		if index > plainStart {
			result.WriteString(style(string(runes[plainStart:index])))
		}
		result.WriteString(code)
		index += n
		plainStart = index
	}
	if plainStart < len(runes) {
		result.WriteString(style(string(runes[plainStart:])))
	}
	return result.String()
}

func (s *TuiAltScreen) applySearchHighlights(screen []string, layout LayoutFrame) []string {
	search := s.activeSearch
	if search == nil || search.selectedIndex < 0 || len(search.matches) == 0 {
		return screen
	}
	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = s.implicitScrollView
	}
	box := GetScrollViewBox(layout, scrollView)
	if box == nil {
		return screen
	}

	rangesByRow := map[int][]searchHighlightRange{}
	scrollbarColumn := -1
	if geometry := GetScrollbarGeometry(box, false); geometry != nil {
		scrollbarColumn = geometry.Column
	}
	minRow := maxInt(0, maxInt(box.Rect.Y, box.Clip.Y))
	maxRow := minInt(len(screen), minInt(box.Rect.Y+box.Rect.Height, box.Clip.Y+box.Clip.Height))
	minColumn := maxInt(0, maxInt(box.Rect.X, box.Clip.X))
	maxColumn := minInt(s.terminalRef.Columns(), minInt(box.Rect.X+box.Rect.Width, box.Clip.X+box.Clip.Width))
	if scrollbarColumn >= 0 && scrollbarColumn < maxColumn {
		maxColumn = scrollbarColumn
	}
	minContentRow := scrollView.ScrollTop() + minRow - box.Rect.Y
	maxContentRow := scrollView.ScrollTop() + maxRow - box.Rect.Y - 1
	low, high := 0, len(search.matches)
	for low < high {
		middle := low + (high-low)/2
		match := search.matches[middle]
		lastRow := -1
		if len(match.Segments) > 0 {
			lastRow = match.Segments[len(match.Segments)-1].Row
		}
		if lastRow < minContentRow {
			low = middle + 1
		} else {
			high = middle
		}
	}
	for matchIndex := low; matchIndex < len(search.matches); matchIndex++ {
		match := search.matches[matchIndex]
		firstRow := 0
		if len(match.Segments) > 0 {
			firstRow = match.Segments[0].Row
		}
		if firstRow > maxContentRow {
			break
		}
		for _, segment := range match.Segments {
			row := box.Rect.Y + segment.Row - scrollView.ScrollTop()
			if row < minRow || row >= maxRow {
				continue
			}
			startCol := maxInt(minColumn, box.Rect.X+segment.StartCol)
			endCol := minInt(maxColumn, box.Rect.X+segment.EndCol)
			if endCol <= startCol {
				continue
			}
			rangesByRow[row] = append(rangesByRow[row], searchHighlightRange{startCol: startCol, endCol: endCol, current: matchIndex == search.selectedIndex})
		}
	}

	result := append([]string(nil), screen...)
	for row, ranges := range rangesByRow {
		line := result[row]
		if IsImageLine(line) {
			continue
		}
		lineWidth := VisibleWidth(line)
		// Apply right-to-left so earlier replacements keep column meaning.
		for i := len(ranges) - 1; i >= 0; i-- {
			// Sort a copy descending by startCol (stable).
			for j := i; j > 0; j-- {
				if ranges[j].startCol > ranges[j-1].startCol {
					ranges[j], ranges[j-1] = ranges[j-1], ranges[j]
				}
			}
		}
		for _, r := range ranges {
			startCol := minInt(r.startCol, lineWidth)
			endCol := minInt(r.endCol, lineWidth)
			if endCol <= startCol {
				continue
			}
			before := SliceByColumn(line, 0, startCol, true)
			highlighted := SliceByColumn(line, startCol, endCol-startCol, true)
			after := SliceByColumn(line, endCol, maxInt(0, lineWidth-endCol), true)
			line = before + s.applySearchTextHighlight(highlighted, r.current) + after
		}
		result[row] = line
	}
	return result
}

func (s *TuiAltScreen) applySelectionHighlight(text string) string {
	var result strings.Builder
	result.WriteString("\x1b[7m")
	runes := []rune(text)
	index := 0
	for index < len(runes) {
		code, n := extractAnsiCodeAt(runes, index)
		if n == 0 {
			result.WriteRune(runes[index])
			index++
			continue
		}
		result.WriteString(code)
		if strings.HasSuffix(code, "m") {
			result.WriteString("\x1b[7m")
		}
		index += n
	}
	result.WriteString("\x1b[27m")
	return result.String()
}

func (s *TuiAltScreen) applySelection(screen []string, layout *LayoutFrame) []string {
	selection := s.getSelectionBounds()
	if selection == nil {
		return screen
	}
	screenSelection := *selection
	minRow := 0
	maxRow := len(screen) - 1
	minColumn := 0
	maxColumn := s.terminalRef.Columns()
	if selection.start.scrollView != nil {
		if layout == nil {
			return screen
		}
		box := GetScrollViewBox(*layout, selection.start.scrollView)
		if box == nil {
			return screen
		}
		minRow = maxInt(0, maxInt(box.Rect.Y, box.Clip.Y))
		maxRow = minInt(len(screen)-1, minInt(box.Rect.Y+box.Rect.Height-1, box.Clip.Y+box.Clip.Height-1))
		minColumn = maxInt(0, maxInt(box.Rect.X, box.Clip.X))
		maxColumn = minInt(s.terminalRef.Columns(), minInt(box.Rect.X+box.Rect.Width, box.Clip.X+box.Clip.Width))
		scrollTop := selection.start.scrollView.ScrollTop()
		start := selection.start
		start.row = box.Rect.Y + selection.start.row - scrollTop
		start.col = box.Rect.X + selection.start.col
		end := selection.end
		end.row = box.Rect.Y + selection.end.row - scrollTop
		end.col = box.Rect.X + selection.end.col
		screenSelection = selectionBounds{start: start, end: end}
	}
	result := make([]string, len(screen))
	for row, line := range screen {
		if row < minRow || row > maxRow ||
			row < screenSelection.start.row || row > screenSelection.end.row ||
			IsImageLine(line) {
			result[row] = line
			continue
		}
		lineWidth := VisibleWidth(line)
		columns := s.getSelectionColumns(line, row, screenSelection, minColumn, maxColumn)
		if columns.end <= columns.start {
			result[row] = line
			continue
		}
		before := SliceByColumn(line, 0, columns.start, true)
		selected := SliceByColumn(line, columns.start, columns.end-columns.start, true)
		after := SliceByColumn(line, columns.end, maxInt(0, lineWidth-columns.end), true)
		result[row] = before + s.applySelectionHighlight(selected) + after
	}
	return result
}

func (s *TuiAltScreen) compositeScrollToEndIndicator(screen []string, layout LayoutFrame, width int) []string {
	s.scrollToEndIndicatorRect = nil
	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = s.implicitScrollView
	}
	if s.scrollToEndIndicator == nil || !scrollView.FollowEnd || scrollView.IsFollowingEnd() {
		return screen
	}
	box := GetScrollViewBox(layout, scrollView)
	if box == nil || box.Clip.Width <= 0 || box.Clip.Height <= 0 {
		return screen
	}
	row := box.Clip.Y + box.Clip.Height - 1
	if row >= len(screen) {
		return screen
	}
	if row >= 0 && row < len(screen) && IsImageLine(screen[row]) {
		return screen
	}
	scrollbarColumn := -1
	if geometry := GetScrollbarGeometry(box, false); geometry != nil {
		scrollbarColumn = geometry.Column
	}
	label := TruncateToWidth(s.scrollToEndIndicator(), box.Clip.Width, "", false)
	labelWidth := VisibleWidth(label)
	column := box.Clip.X + (box.Clip.Width-labelWidth)/2
	rightEdge := box.Clip.X + box.Clip.Width
	if scrollbarColumn >= 0 {
		rightEdge = scrollbarColumn
	}
	availableWidth := maxInt(0, rightEdge-column)
	text := TruncateToWidth(label, availableWidth, "", false)
	textWidth := VisibleWidth(text)
	if textWidth == 0 {
		return screen
	}
	result := append([]string(nil), screen...)
	base := ""
	if row >= 0 && row < len(result) {
		base = result[row]
	}
	result[row] = CompositeTuiLine(base, text, column, textWidth, width)
	s.scrollToEndIndicatorRect = &scrollToEndIndicatorRect{row: row, column: column, width: textWidth}
	return result
}

func (s *TuiAltScreen) compositeFlashes(screen []string, width, height int) []string {
	flashLines := s.flashes.Render(width)
	if len(flashLines) > height {
		flashLines = flashLines[len(flashLines)-height:]
	}
	if len(flashLines) == 0 {
		return screen
	}
	result := append([]string(nil), screen...)
	for len(result) < height {
		result = append(result, "")
	}
	for row := 0; row < len(flashLines); row++ {
		line := flashLines[row]
		flashWidth := VisibleWidth(line)
		if flashWidth == 0 {
			continue
		}
		base := result[row]
		result[row] = CompositeTuiLine(base, line, width-flashWidth, flashWidth, width)
	}
	return result
}

func (s *TuiAltScreen) doRenderAlt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stoppedFlag || !s.altScreenActive {
		return
	}
	width := maxInt(1, s.terminalRef.Columns())
	height := maxInt(1, s.terminalRef.Rows())
	root := Component(s.implicitScrollView)
	if s.layoutRoot != nil {
		root = s.layoutRoot
	}
	// requestRenderFromCallback: ScrollView invokes it synchronously both
	// from the input path (base lock held) and during layout (lock released),
	// so the schedule runs asynchronously like the reference's nextTick.
	requestRenderFromCallback := func() {
		go s.RequestRender(false)
	}
	renderFrame := func() LayoutFrame {
		s.mu.Unlock()
		frame := RenderLayoutFrame(root, width, height, requestRenderFromCallback)
		s.mu.Lock()
		return frame
	}
	nextLayout := renderFrame()
	if s.refreshSearch(nextLayout) {
		nextLayout = renderFrame()
	}
	screen := make([]string, len(nextLayout.Lines))
	for i, line := range nextLayout.Lines {
		screen[i] = osc133ZonePrefix.ReplaceAllString(line, "")
	}
	screen = s.resolveFakeCursors(screen)
	screen = s.applySearchHighlights(screen, nextLayout)
	screen = s.compositeScrollToEndIndicator(screen, nextLayout, width)
	screen = s.CompositeOverlays(screen, width, height)
	if len(screen) > height {
		screen = screen[len(screen)-height:]
	}
	screen = s.applySelection(screen, &nextLayout)
	screen = s.compositeFlashes(screen, width, height)

	cursorRow, cursorCol, hasCursor := s.ExtractCursorPosition(screen, height)
	screen = s.ApplyLineResets(screen)
	for i, line := range screen {
		if !IsImageLine(line) && VisibleWidth(line) > width {
			screen[i] = SliceByColumn(line, 0, width, true)
		}
	}

	fullRedraw := len(s.previousScreen) == 0 || s.previousScreenWidth != width || s.previousScreenHeight != height
	imagesNeedRedraw := false
	for row, line := range screen {
		previous := ""
		if row >= 0 && row < len(s.previousScreen) {
			previous = s.previousScreen[row]
		}
		if line != previous && (IsImageLine(line) || IsImageLine(previous)) {
			imagesNeedRedraw = true
			break
		}
	}
	redrawImages := fullRedraw || imagesNeedRedraw
	hadUploadedKittyImages := len(s.uploadedKittyImages) > 0
	prepared := preparedKittyScreen{lines: screen}
	if redrawImages && s.imageProtocol == ImageProtocolKitty {
		prepared = s.prepareKittyScreen(screen)
	}

	var buffer strings.Builder
	buffer.WriteString(beginSyncOutput)
	if fullRedraw {
		s.FullRedrawCount++
		clearImages := s.deleteKittyImagesAll()
		if s.imageProtocol == ImageProtocolKitty && hadUploadedKittyImages {
			clearImages = DeleteAllKittyPlacements()
		}
		buffer.WriteString(clearImages + "\x1b[2J")
	} else if imagesNeedRedraw {
		if s.imageProtocol == ImageProtocolITerm2 {
			buffer.WriteString("\x1b[2J")
		} else if s.imageProtocol == ImageProtocolKitty {
			buffer.WriteString(DeleteAllKittyPlacements())
		}
	}
	buffer.WriteString(prepared.evictedImageDeletion)

	// WezTerm erases intersecting Kitty image cells when a later EL clears a
	// covered row; only separate clearing from drawing for WezTerm frames
	// that place images.
	clearRowsBeforeKittyImages := redrawImages &&
		s.imageProtocol == ImageProtocolKitty &&
		func() bool {
			for _, line := range screen {
				if IsImageLine(line) {
					return true
				}
			}
			return false
		}() &&
		(os.Getenv("WEZTERM_PANE") != "" || strings.ToLower(os.Getenv("TERM_PROGRAM")) == "wezterm")
	if clearRowsBeforeKittyImages {
		for row := 0; row < height; row++ {
			previous := ""
			if row >= 0 && row < len(s.previousScreen) {
				previous = s.previousScreen[row]
			}
			if !fullRedraw && !imagesNeedRedraw && screen[row] == previous {
				continue
			}
			buffer.WriteString("\x1b[" + itoa(row+1) + ";1H\x1b[2K")
		}
	}

	for row := 0; row < height; row++ {
		previous := ""
		if row >= 0 && row < len(s.previousScreen) {
			previous = s.previousScreen[row]
		}
		if !fullRedraw && !imagesNeedRedraw && screen[row] == previous {
			continue
		}
		line := ""
		if row >= 0 && row < len(prepared.lines) {
			line = prepared.lines[row]
		}
		el := "\x1b[2K"
		if clearRowsBeforeKittyImages {
			el = ""
		}
		buffer.WriteString("\x1b[" + itoa(row+1) + ";1H" + el + line)
	}

	if hasCursor {
		buffer.WriteString("\x1b[" + itoa(cursorRow+1) + ";" + itoa(minInt(width, cursorCol)+1) + "H")
		if s.showHardwareCursor {
			buffer.WriteString("\x1b[?25h")
		} else {
			buffer.WriteString("\x1b[?25l")
		}
	} else {
		buffer.WriteString("\x1b[?25l")
	}
	buffer.WriteString(endSyncOutput)
	s.terminalRef.Write(buffer.String())

	s.previousScreen = screen
	s.previousScreenWidth = width
	s.previousScreenHeight = height
	s.currentLayout = &nextLayout
}
