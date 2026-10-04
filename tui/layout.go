package tui

// Port of layout.ts: the box-tree layout engine (stacks, scroll views),
// painting with clipping, scrollbar painting, and hit testing.

import (
	"regexp"
	"strconv"
)

var osc133ZonePrefix = regexp.MustCompile(`^(?:\x1b\]133;[ABC](?:\x07|\x1b\\))+`)

// LayoutRect is an axis-aligned rectangle.
type LayoutRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// LayoutBox is one laid-out component box.
type LayoutBox struct {
	Component          Component
	Rect               LayoutRect
	Clip               LayoutRect
	Children           []*LayoutBox
	Parent             *LayoutBox
	Lines              []string
	LineOffset         int
	ScrollView         *ScrollView
	ScrollContentLines []string
	Layer              int
}

// LayoutFrame is a rendered frame.
type LayoutFrame struct {
	Root              *LayoutBox
	Width             int
	Height            int
	Lines             []string
	PrimaryScrollView *ScrollView
}

// ScrollbarGeometry describes the painted scrollbar.
type ScrollbarGeometry struct {
	Column       int
	TrackTop     int
	TrackHeight  int
	ThumbTop     int
	ThumbHeight  int
	MaxScrollTop int
}

type layoutContext struct {
	viewport          LayoutViewport
	renderCache       map[Component]map[int][]string
	requestRender     func()
	primaryScrollView *ScrollView
}

func intersectRect(a, b LayoutRect) LayoutRect {
	x := maxInt(a.X, b.X)
	y := maxInt(a.Y, b.Y)
	right := minInt(a.X+a.Width, b.X+b.Width)
	bottom := minInt(a.Y+a.Height, b.Y+b.Height)
	return LayoutRect{X: x, Y: y, Width: maxInt(0, right-x), Height: maxInt(0, bottom-y)}
}

func renderCached(context *layoutContext, component Component, width int) []string {
	safeWidth := maxInt(1, width)
	widths, ok := context.renderCache[component]
	if !ok {
		widths = map[int][]string{}
		context.renderCache[component] = widths
	}
	lines, ok := widths[safeWidth]
	if !ok {
		lines = component.Render(safeWidth)
		widths[safeWidth] = lines
	}
	return lines
}

func measureHeight(context *layoutContext, component Component, width int) int {
	return len(renderCached(context, component, width))
}

func measureWidth(context *layoutContext, component Component, width int) int {
	max := 0
	for _, line := range renderCached(context, component, width) {
		if w := VisibleWidth(line); w > max {
			max = w
		}
	}
	return max
}

func translateBox(box *LayoutBox, deltaY int) {
	box.Rect.Y += deltaY
	for _, child := range box.Children {
		translateBox(child, deltaY)
	}
}

func updateClips(box *LayoutBox, parentClip LayoutRect) {
	box.Clip = intersectRect(parentClip, box.Rect)
	for _, child := range box.Children {
		updateClips(child, box.Clip)
	}
}

func layoutComponent(context *layoutContext, component Component, x, y, width int, height *int, clip LayoutRect) *LayoutBox {
	safeWidth := maxInt(1, width)
	node := GetLayoutNode(component)
	if node == nil {
		lines := renderCached(context, component, safeWidth)
		allocatedHeight := len(lines)
		if height != nil {
			allocatedHeight = maxInt(0, *height)
		}
		lineOffset := 0
		if len(lines) > allocatedHeight && allocatedHeight > 0 {
			cursorLine := -1
			for i, line := range lines {
				if stringsContains(line, CURSOR_MARKER) {
					cursorLine = i
					break
				}
			}
			if cursorLine >= allocatedHeight {
				lineOffset = cursorLine - allocatedHeight + 1
			}
		}
		return &LayoutBox{
			Component:  component,
			Rect:       LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight},
			Clip:       intersectRect(clip, LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}),
			Lines:      lines,
			LineOffset: lineOffset,
		}
	}

	if scroll, ok := node.(*ScrollLayoutNode); ok {
		previousScrollTop := scroll.State.ScrollTop()
		contentWidth := scroll.State.GetContentWidth(safeWidth)
		childBox := layoutComponent(context, scroll.Component, x, y-previousScrollTop, contentWidth, nil, clip)
		contentHeight := childBox.Rect.Height
		viewportHeight := contentHeight
		if height != nil {
			viewportHeight = maxInt(0, *height)
		}
		scroll.State.UpdateLayout(contentHeight, viewportHeight, context.requestRender)
		translateBox(childBox, previousScrollTop-scroll.State.ScrollTop())
		scrollView := scroll.State.(*ScrollView)
		if scroll.State.Primary() || context.primaryScrollView == nil {
			context.primaryScrollView = scrollView
		}
		rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: viewportHeight}
		childClip := intersectRect(clip, rect)
		box := &LayoutBox{
			Component:          component,
			Rect:               rect,
			Clip:               childClip,
			Children:           []*LayoutBox{childBox},
			ScrollView:         scrollView,
			ScrollContentLines: renderCached(context, scroll.Component, contentWidth),
		}
		childBox.Parent = box
		updateClips(childBox, childClip)
		return box
	}

	stack, ok := node.(StackLayoutNode)
	if !ok {
		// Unknown node type: fall back to plain rendering.
		lines := renderCached(context, component, safeWidth)
		return &LayoutBox{
			Component: component,
			Rect:      LayoutRect{X: x, Y: y, Width: safeWidth, Height: len(lines)},
			Clip:      clip,
			Lines:     lines,
		}
	}

	entries := VisibleStackEntries(stack.Entries, context.viewport)
	gapTotal := maxInt(0, len(entries)-1) * stack.Gap
	if stack.Type == "vstack" {
		intrinsicHeights := make([]int, len(entries))
		for i, entry := range entries {
			if entry.Basis != nil && !entry.Basis.Auto {
				intrinsicHeights[i] = entry.Basis.Value
			} else {
				intrinsicHeights[i] = measureHeight(context, entry.Component, safeWidth)
			}
		}
		sizes := AllocateStackSizes(entries, intrinsicHeights, height, stack.Gap)
		naturalHeight := gapTotal
		for _, size := range sizes {
			naturalHeight += size
		}
		allocatedHeight := naturalHeight
		if height != nil {
			allocatedHeight = maxInt(0, *height)
		}
		rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
		box := &LayoutBox{Component: component, Rect: rect, Clip: intersectRect(clip, rect)}
		childY := y
		for index := range entries {
			child := layoutComponent(context, entries[index].Component, x, childY, safeWidth, &sizes[index], box.Clip)
			child.Parent = box
			box.Children = append(box.Children, child)
			childY += sizes[index] + stack.Gap
		}
		return box
	}

	intrinsicWidths := make([]int, len(entries))
	for i, entry := range entries {
		if entry.Basis != nil && !entry.Basis.Auto {
			intrinsicWidths[i] = entry.Basis.Value
		} else {
			intrinsicWidths[i] = measureWidth(context, entry.Component, safeWidth)
		}
	}
	widthValue := safeWidth
	widths := AllocateStackSizes(entries, intrinsicWidths, &widthValue, stack.Gap)
	intrinsicHeights := make([]int, len(entries))
	for i, entry := range entries {
		intrinsicHeights[i] = measureHeight(context, entry.Component, maxInt(1, widths[i]))
	}
	allocatedHeight := 0
	if height == nil {
		for _, childHeight := range intrinsicHeights {
			if childHeight > allocatedHeight {
				allocatedHeight = childHeight
			}
		}
	} else {
		allocatedHeight = maxInt(0, *height)
	}
	rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
	box := &LayoutBox{Component: component, Rect: rect, Clip: intersectRect(clip, rect)}
	childX := x
	for index := range entries {
		naturalChildHeight := intrinsicHeights[index]
		childHeight := naturalChildHeight
		if stack.Align == StackAlignStretch {
			childHeight = allocatedHeight
		} else if childHeight > allocatedHeight {
			childHeight = allocatedHeight
		}
		childY := y
		if stack.Align == StackAlignCenter {
			childY += (allocatedHeight - childHeight) / 2
		} else if stack.Align == StackAlignEnd {
			childY += allocatedHeight - childHeight
		}
		childWidth := widths[index]
		if childWidth == 0 {
			box.Children = append(box.Children, &LayoutBox{
				Component: entries[index].Component,
				Rect:      LayoutRect{X: childX, Y: childY, Width: 0, Height: childHeight},
				Clip:      LayoutRect{X: childX, Y: childY, Width: 0, Height: 0},
				Parent:    box,
			})
		} else {
			child := layoutComponent(context, entries[index].Component, childX, childY, childWidth, &childHeight, box.Clip)
			child.Parent = box
			box.Children = append(box.Children, child)
		}
		childX += childWidth + stack.Gap
	}
	return box
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && indexString(s, substr) != -1
}

func replaceScrollbarCell(line string, column, totalWidth int, replacement string, preserveTargetBackground bool) string {
	if IsImageLine(line) {
		return line
	}

	graphemeRange := GetGraphemeCellRange(line, column)
	start := column
	end := column + 1
	if graphemeRange != nil {
		start = graphemeRange.Start
		end = graphemeRange.End
	}
	before := SliceByColumn(line, 0, start, true)
	target := SliceByColumn(line, start, end-start, true)
	after := SliceByColumn(line, end, maxInt(0, totalWidth-end), true)

	targetPrefix := ""
	targetRunes := []rune(target)
	targetIndex := 0
	for targetIndex < len(targetRunes) {
		code, n := extractAnsiCodeAt(targetRunes, targetIndex)
		if n == 0 {
			break
		}
		targetPrefix += code
		targetIndex += n
	}
	beforePadding := stringRepeat(" ", maxInt(0, start-VisibleWidth(before)))
	cellPaddingBefore := stringRepeat(" ", maxInt(0, column-start))
	cellPaddingAfter := stringRepeat(" ", maxInt(0, end-column-1))
	targetStyle := "\x1b[0m\x1b]8;;\x07"
	if preserveTargetBackground {
		targetStyle += GetActiveBackgroundAnsi(targetPrefix)
	}
	return before + beforePadding + targetStyle + cellPaddingBefore + replacement + cellPaddingAfter + after
}

// GetScrollbarGeometry returns the scrollbar geometry for a scroll box;
// includeHiddenAuto reveals an auto scrollbar that would show on activity.
func GetScrollbarGeometry(box *LayoutBox, includeHiddenAuto bool) *ScrollbarGeometry {
	if box.ScrollView == nil || box.Rect.Width <= 0 || box.Rect.Height <= 0 {
		return nil
	}

	contentHeight := 0
	if len(box.Children) > 0 {
		contentHeight = box.Children[0].Rect.Height
	} else if box.ScrollContentLines != nil {
		contentHeight = len(box.ScrollContentLines)
	}
	trackHeight := box.Rect.Height
	canRevealHiddenAuto := includeHiddenAuto && box.ScrollView.Scrollbar() == ScrollbarAuto && contentHeight > trackHeight
	if !box.ScrollView.IsScrollbarVisible() && !canRevealHiddenAuto {
		return nil
	}

	minThumbHeight := minInt(2, trackHeight)
	thumbHeight := maxInt(minThumbHeight, minInt(trackHeight, jsRoundInt((trackHeight*trackHeight)/maxInt(1, contentHeight))))
	maxScrollTop := maxInt(0, contentHeight-trackHeight)
	maxThumbTop := trackHeight - thumbHeight
	thumbOffset := 0
	if maxScrollTop != 0 {
		thumbOffset = jsRoundInt((box.ScrollView.ScrollTop() * maxThumbTop) / maxScrollTop)
	}
	column := box.Rect.X + box.Rect.Width - 1
	if column < box.Clip.X || column >= box.Clip.X+box.Clip.Width {
		return nil
	}

	return &ScrollbarGeometry{
		Column:       column,
		TrackTop:     box.Rect.Y,
		TrackHeight:  trackHeight,
		ThumbTop:     box.Rect.Y + thumbOffset,
		ThumbHeight:  thumbHeight,
		MaxScrollTop: maxScrollTop,
	}
}

func jsRoundInt(v int) int { return int(jsRound(float64(v))) }

func paintScrollbar(box *LayoutBox, screen []string, totalWidth int) {
	geometry := GetScrollbarGeometry(box, false)
	if geometry == nil || box.ScrollView == nil {
		return
	}

	for offset := 0; offset < geometry.TrackHeight; offset++ {
		row := geometry.TrackTop + offset
		if row < box.Clip.Y || row >= box.Clip.Y+box.Clip.Height || row < 0 || row >= len(screen) {
			continue
		}
		isThumb := row >= geometry.ThumbTop && row < geometry.ThumbTop+geometry.ThumbHeight
		var replacement string
		if isThumb {
			thumb := "┃"
			if box.ScrollView.IsScrollbarActive() {
				thumb = "█"
			}
			replacement = box.ScrollView.ScrollbarThumbStyle(thumb)
		} else {
			replacement = box.ScrollView.ScrollbarTrackStyle("│")
		}
		existing := ""
		if row < len(screen) {
			existing = screen[row]
		}
		screen[row] = replaceScrollbarCell(existing, geometry.Column, totalWidth, replacement, box.ScrollView.Scrollbar() != ScrollbarAlways)
	}
}

func paintBox(box *LayoutBox, screen []string, totalWidth int) {
	if box.Lines != nil {
		offset := box.LineOffset
		firstRow := maxInt(box.Rect.Y, maxInt(box.Clip.Y, 0))
		lastRow := minInt(box.Rect.Y+box.Rect.Height, minInt(box.Clip.Y+box.Clip.Height, len(screen)))
		for row := firstRow; row < lastRow; row++ {
			sourceIndex := offset + row - box.Rect.Y
			if sourceIndex < 0 || sourceIndex >= len(box.Lines) {
				continue
			}
			line := osc133ZonePrefix.ReplaceAllString(box.Lines[sourceIndex], "")
			if metadata := GetKittyImageMetadata(line); metadata != nil {
				clipBottom := minInt(len(screen), box.Clip.Y+box.Clip.Height)
				visibleRows := minInt(metadata.Rows, clipBottom-row)
				if visibleRows < metadata.Rows {
					line = CropKittyImageLine(line, 0, visibleRows)
				}
			}
			// Fast path: a full-width box painting onto an untouched row uses
			// the source line directly; compositing would rebuild the row
			// through ANSI/grapheme segmentation every frame.
			existing := ""
			if row < len(screen) {
				existing = screen[row]
			}
			if box.Rect.X == 0 && box.Rect.Width >= totalWidth && (IsImageLine(line) || existing == "") {
				screen[row] = line
			} else {
				screen[row] = CompositeTuiLine(existing, line, box.Rect.X, box.Rect.Width, totalWidth)
			}
		}
	}
	for _, child := range box.Children {
		paintBox(child, screen, totalWidth)
	}

	// Re-anchor a partially scrolled-off kitty image at the viewport top.
	if box.ScrollView != nil && box.ScrollContentLines != nil && box.ScrollView.ScrollTop() > 0 && box.Rect.Height > 0 {
		for imageRow := box.ScrollView.ScrollTop() - 1; imageRow >= 0; imageRow-- {
			imageLine := ""
			if imageRow >= 0 && imageRow < len(box.ScrollContentLines) {
				imageLine = box.ScrollContentLines[imageRow]
			}
			if metadata := GetKittyImageMetadata(imageLine); metadata != nil {
				hiddenRows := box.ScrollView.ScrollTop() - imageRow
				if hiddenRows < metadata.Rows {
					visibleRows := minInt(box.Rect.Height, metadata.Rows-hiddenRows)
					cropped := CropKittyImageLine(imageLine, hiddenRows, visibleRows)
					if box.Rect.X == 0 && box.Rect.Width >= totalWidth && box.Rect.Y < len(screen) {
						screen[box.Rect.Y] = cropped
					}
				}
				break
			}
			if imageLine != "" {
				break
			}
		}
	}

	paintScrollbar(box, screen, totalWidth)
}

// RenderLayoutFrame lays out and paints the root into a frame.
func RenderLayoutFrame(root Component, width, height int, requestRender func()) LayoutFrame {
	safeWidth := maxInt(1, width)
	safeHeight := maxInt(1, height)
	context := &layoutContext{
		viewport:      LayoutViewport{Width: safeWidth, Height: safeHeight},
		renderCache:   map[Component]map[int][]string{},
		requestRender: requestRender,
	}
	heightValue := safeHeight
	rootBox := layoutComponent(context, root, 0, 0, safeWidth, &heightValue, LayoutRect{X: 0, Y: 0, Width: safeWidth, Height: safeHeight})
	lines := make([]string, safeHeight)
	paintBox(rootBox, lines, safeWidth)
	return LayoutFrame{
		Root:              rootBox,
		Width:             safeWidth,
		Height:            safeHeight,
		Lines:             lines,
		PrimaryScrollView: context.primaryScrollView,
	}
}

func containsPoint(rect LayoutRect, x, y int) bool {
	return x >= rect.X && x < rect.X+rect.Width && y >= rect.Y && y < rect.Y+rect.Height
}

// GetLayoutBoxesAt returns the visual hit path from the deepest component to
// the layout root (layer first, then depth).
func GetLayoutBoxesAt(frame LayoutFrame, x, y int) []*LayoutBox {
	type hit struct {
		box   *LayoutBox
		depth int
	}
	var result []hit
	var visit func(box *LayoutBox, depth int)
	visit = func(box *LayoutBox, depth int) {
		if !containsPoint(box.Clip, x, y) {
			return
		}
		result = append(result, hit{box: box, depth: depth})
		for _, child := range box.Children {
			visit(child, depth+1)
		}
	}
	visit(frame.Root, 0)
	// Stable sort by layer desc then depth desc.
	for i := 1; i < len(result); i++ {
		for j := i; j > 0; j-- {
			a, b := result[j], result[j-1]
			if a.box.Layer > b.box.Layer || (a.box.Layer == b.box.Layer && a.depth > b.depth) {
				result[j], result[j-1] = result[j-1], result[j]
			} else {
				break
			}
		}
	}
	boxes := make([]*LayoutBox, len(result))
	for i, entry := range result {
		boxes[i] = entry.box
	}
	return boxes
}

// GetScrollViewBox finds the layout box for a scroll view.
func GetScrollViewBox(frame LayoutFrame, scrollView *ScrollView) *LayoutBox {
	var visit func(box *LayoutBox) *LayoutBox
	visit = func(box *LayoutBox) *LayoutBox {
		if box.ScrollView == scrollView {
			return box
		}
		for _, child := range box.Children {
			if match := visit(child); match != nil {
				return match
			}
		}
		return nil
	}
	return visit(frame.Root)
}

// GetScrollViewsAt returns scroll views under a point, deepest first.
func GetScrollViewsAt(frame LayoutFrame, x, y int) []*ScrollView {
	type hit struct {
		scrollView *ScrollView
		depth      int
	}
	var result []hit
	var visit func(box *LayoutBox, depth int)
	visit = func(box *LayoutBox, depth int) {
		if !containsPoint(box.Clip, x, y) {
			return
		}
		if box.ScrollView != nil && containsPoint(box.Rect, x, y) {
			result = append(result, hit{scrollView: box.ScrollView, depth: depth})
		}
		for _, child := range box.Children {
			visit(child, depth+1)
		}
	}
	visit(frame.Root, 0)
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j].depth > result[j-1].depth; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	views := make([]*ScrollView, len(result))
	for i, entry := range result {
		views[i] = entry.scrollView
	}
	return views
}

var _ = strconv.Itoa
