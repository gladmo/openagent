package tui

// Port of components/image.ts and components/select-list.ts.

import (
	"regexp"
	"strings"
)

// ImageTheme styles the text fallback.
type ImageTheme struct {
	FallbackColor func(str string) string
}

// ImageOptions configures inline image rendering.
type ImageOptions struct {
	MaxWidthCells  *int
	MaxHeightCells *int
	Filename       string
	// ImageID reuses a Kitty image ID (animations/updates).
	ImageID *int
}

// Image renders inline images with a text fallback.
type Image struct {
	base64Data string
	mimeType   string
	dimensions ImageDimensions
	theme      ImageTheme
	options    ImageOptions
	imageID    *int

	cachedLines []string
	cachedWidth *int
}

// NewImage builds an Image; dimensions default to sniffing then 800x600.
func NewImage(base64Data, mimeType string, theme ImageTheme, options ImageOptions, dimensions *ImageDimensions) *Image {
	dims := dimensions
	if dims == nil {
		if sniffed := GetImageDimensions(base64Data, mimeType); sniffed != nil {
			dims = sniffed
		} else {
			dims = &ImageDimensions{WidthPx: 800, HeightPx: 600}
		}
	}
	image := &Image{
		base64Data: base64Data,
		mimeType:   mimeType,
		dimensions: *dims,
		theme:      theme,
		options:    options,
	}
	if options.ImageID != nil {
		id := *options.ImageID
		image.imageID = &id
	}
	return image
}

// GetImageID returns the allocated Kitty image ID, or nil.
func (i *Image) GetImageID() *int { return i.imageID }

// Invalidate drops the render cache.
func (i *Image) Invalidate() {
	i.cachedLines = nil
	i.cachedWidth = nil
}

// Render draws the image or its fallback, reserving rows for its height.
func (i *Image) Render(width int) []string {
	if i.cachedLines != nil && i.cachedWidth != nil && *i.cachedWidth == width {
		return i.cachedLines
	}

	maxWidth := minInt(width-2, 60)
	if i.options.MaxWidthCells != nil {
		maxWidth = minInt(maxWidth, *i.options.MaxWidthCells)
	}
	maxWidth = maxInt(1, maxWidth)
	cellDimensions := GetCellDimensions()
	defaultMaxHeight := maxInt(1, ceilDiv(maxWidth*cellDimensions.WidthPx, cellDimensions.HeightPx))
	maxHeight := defaultMaxHeight
	if i.options.MaxHeightCells != nil {
		maxHeight = *i.options.MaxHeightCells
	}

	caps := GetCapabilities()
	var lines []string

	if caps.Images != ImageProtocolNone {
		if caps.Images == ImageProtocolKitty && i.imageID == nil {
			id := AllocateImageID()
			i.imageID = &id
		}
		moveCursor := false
		result := RenderImage(i.base64Data, i.dimensions, ImageRenderOptions{
			MaxWidthCells:  &maxWidth,
			MaxHeightCells: &maxHeight,
			ImageID:        i.imageID,
			MoveCursor:     &moveCursor,
		})

		if result != nil {
			if result.ImageID != nil {
				id := *result.ImageID
				i.imageID = &id
			}

			if caps.Images == ImageProtocolKitty {
				// C=1 prevents cursor movement; reserve rows for height.
				lines = []string{result.Sequence}
				for row := 1; row < result.Rows; row++ {
					lines = append(lines, "")
				}
			} else {
				// iTerm2: empty rows above, then move up, draw, move back
				// down so cursor accounting stays inside the scroll area.
				lines = nil
				for row := 0; row < result.Rows-1; row++ {
					lines = append(lines, "")
				}
				rowOffset := result.Rows - 1
				moveUp := ""
				if rowOffset > 0 {
					moveUp = "\x1b[" + itoa(rowOffset) + "A"
				}
				lines = append(lines, moveUp+result.Sequence)
			}
		} else {
			lines = i.fallbackLines(width)
		}
	} else {
		lines = i.fallbackLines(width)
	}

	i.cachedLines = lines
	cachedWidth := width
	i.cachedWidth = &cachedWidth
	return lines
}

func (i *Image) fallbackLines(width int) []string {
	fallback := ImageFallback(i.mimeType, &i.dimensions, i.options.Filename)
	return []string{TruncateToWidth(i.theme.FallbackColor(fallback), width, "...", false)}
}

// SelectItem is one selectable row.
type SelectItem struct {
	Value       string
	Label       string
	Description string
}

// SelectListTheme styles selection output.
type SelectListTheme struct {
	SelectedPrefix func(text string) string
	SelectedText   func(text string) string
	Description    func(text string) string
	ScrollInfo     func(text string) string
	NoMatch        func(text string) string
}

// SelectListTruncatePrimaryContext carries truncation inputs.
type SelectListTruncatePrimaryContext struct {
	Text        string
	MaxWidth    int
	ColumnWidth int
	Item        SelectItem
	IsSelected  bool
}

// SelectListLayoutOptions customize the primary column.
type SelectListLayoutOptions struct {
	MinPrimaryColumnWidth *int
	MaxPrimaryColumnWidth *int
	TruncatePrimary       func(context SelectListTruncatePrimaryContext) string
}

const (
	selectDefaultPrimaryColumnWidth = 32
	selectPrimaryColumnGap          = 2
	selectMinDescriptionWidth       = 10
)

var selectNewlinesPattern = regexp.MustCompile(`[\r\n]+`)

func normalizeToSingleLine(text string) string {
	return strings.TrimSpace(selectNewlinesPattern.ReplaceAllString(text, " "))
}

func clampInt(value, minValue, maxValue int) int {
	return maxInt(minValue, minInt(value, maxValue))
}

// SelectList renders a filterable, keyboard-navigable list.
type SelectList struct {
	items             []SelectItem
	filtered          []SelectItem
	selectedIndex     int
	mousePressedIndex *int
	maxVisible        int
	theme             SelectListTheme
	layout            SelectListLayoutOptions

	OnSelect          func(item SelectItem)
	OnCancel          func()
	OnSelectionChange func(item SelectItem)
}

// NewSelectList builds the list.
func NewSelectList(items []SelectItem, maxVisible int, theme SelectListTheme, layout SelectListLayoutOptions) *SelectList {
	return &SelectList{
		items:      items,
		filtered:   items,
		maxVisible: maxVisible,
		theme:      theme,
		layout:     layout,
	}
}

// SetFilter filters items by value prefix (case-insensitive) and resets
// selection.
func (s *SelectList) SetFilter(filter string) {
	filterLower := strings.ToLower(filter)
	var filtered []SelectItem
	for _, item := range s.items {
		if strings.HasPrefix(strings.ToLower(item.Value), filterLower) {
			filtered = append(filtered, item)
		}
	}
	s.filtered = filtered
	s.selectedIndex = 0
}

// SetSelectedIndex clamps and sets the selection.
func (s *SelectList) SetSelectedIndex(index int) {
	s.selectedIndex = maxInt(0, minInt(index, len(s.filtered)-1))
}

// Invalidate is a no-op (no cached state).
func (s *SelectList) Invalidate() {}

// Render draws visible items plus a scroll indicator.
func (s *SelectList) Render(width int) []string {
	var lines []string

	if len(s.filtered) == 0 {
		lines = append(lines, s.theme.NoMatch("  No matching commands"))
		return lines
	}

	primaryColumnWidth := s.getPrimaryColumnWidth()
	startIndex, endIndex := s.getVisibleRange()

	for i := startIndex; i < endIndex; i++ {
		item := s.filtered[i]
		isSelected := i == s.selectedIndex
		descriptionSingleLine := ""
		if item.Description != "" {
			descriptionSingleLine = normalizeToSingleLine(item.Description)
		}
		lines = append(lines, s.renderItem(item, isSelected, width, descriptionSingleLine, primaryColumnWidth))
	}

	if startIndex > 0 || endIndex < len(s.filtered) {
		scrollText := "  (" + itoa(s.selectedIndex+1) + "/" + itoa(len(s.filtered)) + ")"
		lines = append(lines, s.theme.ScrollInfo(TruncateToWidth(scrollText, width-2, "", false)))
	}

	return lines
}

// HandleMouse wheels the selection and selects on left click.
func (s *SelectList) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	if len(s.filtered) == 0 {
		return nil
	}
	if event.Type == MouseWheel && event.WheelDelta != nil {
		delta := 1
		if *event.WheelDelta < 0 {
			delta = -1
		}
		previousIndex := s.selectedIndex
		s.selectedIndex = maxInt(0, minInt(len(s.filtered)-1, s.selectedIndex+delta))
		if s.selectedIndex != previousIndex {
			s.notifySelectionChange()
		}
		render := s.selectedIndex != previousIndex
		return &TuiMouseEventResult{Handled: true, Render: &render}
	}
	// Hover must not change selection: the visible range centers on it.
	if event.Button != MouseLeft || (event.Type != MousePress && event.Type != MouseClick) {
		return nil
	}
	startIndex, endIndex := s.getVisibleRange()
	itemIndex := startIndex + event.Y
	if itemIndex < startIndex || itemIndex >= endIndex {
		return nil
	}

	if event.Type == MousePress {
		s.mousePressedIndex = &itemIndex
		if s.selectedIndex != itemIndex {
			s.selectedIndex = itemIndex
			s.notifySelectionChange()
		}
		return &TuiMouseEventResult{Handled: true, Focus: true}
	}
	// Click
	clickedIndex := itemIndex
	if s.mousePressedIndex != nil {
		clickedIndex = *s.mousePressedIndex
		s.mousePressedIndex = nil
	}
	changed := s.selectedIndex != clickedIndex
	s.selectedIndex = clickedIndex
	if changed {
		s.notifySelectionChange()
	}
	if s.selectedIndex >= 0 && s.selectedIndex < len(s.filtered) {
		if s.OnSelect != nil {
			s.OnSelect(s.filtered[s.selectedIndex])
		}
	}
	return &TuiMouseEventResult{Handled: true}
}

// HandleInput navigates with the select keybindings.
func (s *SelectList) HandleInput(keyData string) {
	kb := GetKeybindings()
	switch {
	case kb.Matches(keyData, "tui.select.up"):
		// Wrap to bottom when at top.
		if s.selectedIndex == 0 {
			s.selectedIndex = len(s.filtered) - 1
		} else {
			s.selectedIndex--
		}
		s.notifySelectionChange()
	case kb.Matches(keyData, "tui.select.down"):
		// Wrap to top when at bottom.
		if s.selectedIndex == len(s.filtered)-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
		s.notifySelectionChange()
	case kb.Matches(keyData, "tui.select.confirm"):
		if s.selectedIndex >= 0 && s.selectedIndex < len(s.filtered) && s.OnSelect != nil {
			s.OnSelect(s.filtered[s.selectedIndex])
		}
	case kb.Matches(keyData, "tui.select.cancel"):
		if s.OnCancel != nil {
			s.OnCancel()
		}
	}
}

func (s *SelectList) getVisibleRange() (int, int) {
	startIndex := maxInt(0,
		minInt(s.selectedIndex-s.maxVisible/2, len(s.filtered)-s.maxVisible))
	endIndex := minInt(startIndex+s.maxVisible, len(s.filtered))
	return startIndex, endIndex
}

func (s *SelectList) renderItem(item SelectItem, isSelected bool, width int, descriptionSingleLine string, primaryColumnWidth int) string {
	prefix := "  "
	if isSelected {
		prefix = "→ "
	}
	prefixWidth := VisibleWidth(prefix)

	if descriptionSingleLine != "" && width > 40 {
		effectivePrimaryColumnWidth := maxInt(1, minInt(primaryColumnWidth, width-prefixWidth-4))
		maxPrimaryWidth := maxInt(1, effectivePrimaryColumnWidth-selectPrimaryColumnGap)
		truncatedValue := s.truncatePrimary(item, isSelected, maxPrimaryWidth, effectivePrimaryColumnWidth)
		truncatedValueWidth := VisibleWidth(truncatedValue)
		spacing := stringRepeat(" ", maxInt(1, effectivePrimaryColumnWidth-truncatedValueWidth))
		descriptionStart := prefixWidth + truncatedValueWidth + len(spacing)
		remainingWidth := width - descriptionStart - 2 // -2 for safety

		if remainingWidth > selectMinDescriptionWidth {
			truncatedDesc := TruncateToWidth(descriptionSingleLine, remainingWidth, "", false)
			if isSelected {
				return s.theme.SelectedText(prefix + truncatedValue + spacing + truncatedDesc)
			}
			descText := s.theme.Description(spacing + truncatedDesc)
			return prefix + truncatedValue + descText
		}
	}

	maxWidth := width - prefixWidth - 2
	truncatedValue := s.truncatePrimary(item, isSelected, maxWidth, maxWidth)
	if isSelected {
		return s.theme.SelectedText(prefix + truncatedValue)
	}
	return prefix + truncatedValue
}

func (s *SelectList) getPrimaryColumnWidth() int {
	minWidth, maxWidth := s.getPrimaryColumnBounds()
	widestPrimary := 0
	for _, item := range s.filtered {
		if w := VisibleWidth(s.getDisplayValue(item)) + selectPrimaryColumnGap; w > widestPrimary {
			widestPrimary = w
		}
	}
	return clampInt(widestPrimary, minWidth, maxWidth)
}

func (s *SelectList) getPrimaryColumnBounds() (int, int) {
	rawMin := selectDefaultPrimaryColumnWidth
	if s.layout.MinPrimaryColumnWidth != nil {
		rawMin = *s.layout.MinPrimaryColumnWidth
	} else if s.layout.MaxPrimaryColumnWidth != nil {
		rawMin = *s.layout.MaxPrimaryColumnWidth
	}
	rawMax := selectDefaultPrimaryColumnWidth
	if s.layout.MaxPrimaryColumnWidth != nil {
		rawMax = *s.layout.MaxPrimaryColumnWidth
	} else if s.layout.MinPrimaryColumnWidth != nil {
		rawMax = *s.layout.MinPrimaryColumnWidth
	}
	return maxInt(1, minInt(rawMin, rawMax)), maxInt(1, maxInt(rawMin, rawMax))
}

func (s *SelectList) truncatePrimary(item SelectItem, isSelected bool, maxWidth, columnWidth int) string {
	displayValue := s.getDisplayValue(item)
	truncatedValue := TruncateToWidth(displayValue, maxWidth, "", false)
	if s.layout.TruncatePrimary != nil {
		truncatedValue = s.layout.TruncatePrimary(SelectListTruncatePrimaryContext{
			Text:        displayValue,
			MaxWidth:    maxWidth,
			ColumnWidth: columnWidth,
			Item:        item,
			IsSelected:  isSelected,
		})
	}
	return TruncateToWidth(truncatedValue, maxWidth, "", false)
}

func (s *SelectList) getDisplayValue(item SelectItem) string {
	if item.Label != "" {
		return item.Label
	}
	return item.Value
}

func (s *SelectList) notifySelectionChange() {
	if s.selectedIndex >= 0 && s.selectedIndex < len(s.filtered) && s.OnSelectionChange != nil {
		s.OnSelectionChange(s.filtered[s.selectedIndex])
	}
}

// GetSelectedItem returns the selection, or ok=false when empty.
func (s *SelectList) GetSelectedItem() (SelectItem, bool) {
	if s.selectedIndex < 0 || s.selectedIndex >= len(s.filtered) {
		return SelectItem{}, false
	}
	return s.filtered[s.selectedIndex], true
}
