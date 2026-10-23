package tui

// Port of components/text.ts: multi-line text with word wrapping, optional
// background, and padding; plus the single-line TruncatedText and Spacer
// components from their own files.

import "strings"

// Text displays multi-line text with word wrapping.
type Text struct {
	text       string
	paddingX   int
	paddingY   int
	customBgFn func(text string) string

	cachedText  *string
	cachedWidth *int
	cachedLines []string
}

// NewText builds a Text component with horizontal and vertical padding.
func NewText(text string, paddingX, paddingY int, customBgFn func(string) string) *Text {
	return &Text{text: text, paddingX: paddingX, paddingY: paddingY, customBgFn: customBgFn}
}

func (t *Text) invalidateCache() {
	t.cachedText = nil
	t.cachedWidth = nil
	t.cachedLines = nil
}

// SetText replaces the text.
func (t *Text) SetText(text string) {
	t.text = text
	t.invalidateCache()
}

// SetCustomBgFn replaces the background function.
func (t *Text) SetCustomBgFn(customBgFn func(string) string) {
	t.customBgFn = customBgFn
	t.invalidateCache()
}

// Invalidate drops the render cache.
func (t *Text) Invalidate() { t.invalidateCache() }

// Render wraps and pads the text to width.
func (t *Text) Render(width int) []string {
	if t.cachedLines != nil && t.cachedText != nil && *t.cachedText == t.text && t.cachedWidth != nil && *t.cachedWidth == width {
		return t.cachedLines
	}

	// Nothing to render for blank text.
	if t.text == "" || strings.TrimSpace(t.text) == "" {
		t.cachedText = &t.text
		t.cachedWidth = &width
		t.cachedLines = []string{}
		return t.cachedLines
	}

	// Tabs render as three spaces.
	normalizedText := strings.ReplaceAll(t.text, "\t", "   ")

	// Reduce margins when necessary so content and padding fit.
	paddingX := minInt(t.paddingX, maxInt(0, (width-1)/2))
	contentWidth := maxInt(1, width-paddingX*2)

	// Wrap text (preserves ANSI codes, does NOT pad).
	wrappedLines := WrapTextWithAnsi(normalizedText, contentWidth)

	leftMargin := stringRepeat(" ", paddingX)
	rightMargin := stringRepeat(" ", paddingX)
	var contentLines []string

	for _, line := range wrappedLines {
		lineWithMargins := leftMargin + line + rightMargin

		// Background application also pads to full width.
		if t.customBgFn != nil {
			contentLines = append(contentLines, ApplyBackgroundToLine(lineWithMargins, width, t.customBgFn))
		} else {
			paddingNeeded := maxInt(0, width-VisibleWidth(lineWithMargins))
			contentLines = append(contentLines, lineWithMargins+stringRepeat(" ", paddingNeeded))
		}
	}

	// Vertical padding lines.
	emptyLine := stringRepeat(" ", width)
	var emptyLines []string
	for i := 0; i < t.paddingY; i++ {
		if t.customBgFn != nil {
			emptyLines = append(emptyLines, ApplyBackgroundToLine(emptyLine, width, t.customBgFn))
		} else {
			emptyLines = append(emptyLines, emptyLine)
		}
	}

	result := append(append(append([]string{}, emptyLines...), contentLines...), emptyLines...)

	t.cachedText = &t.text
	t.cachedWidth = &width
	t.cachedLines = result
	if len(result) == 0 {
		return []string{""}
	}
	return result
}

// TruncatedText renders its first line truncated to the viewport width.
type TruncatedText struct {
	text     string
	paddingX int
	paddingY int
}

// NewTruncatedText builds a single-line truncating Text.
func NewTruncatedText(text string, paddingX, paddingY int) *TruncatedText {
	return &TruncatedText{text: text, paddingX: paddingX, paddingY: paddingY}
}

// Invalidate is a no-op (no cached state).
func (t *TruncatedText) Invalidate() {}

// Render truncates the first line and pads to width.
func (t *TruncatedText) Render(width int) []string {
	var result []string

	emptyLine := stringRepeat(" ", width)
	for i := 0; i < t.paddingY; i++ {
		result = append(result, emptyLine)
	}

	availableWidth := maxInt(1, width-t.paddingX*2)

	singleLineText := t.text
	if newlineIndex := strings.IndexByte(t.text, '\n'); newlineIndex != -1 {
		singleLineText = t.text[:newlineIndex]
	}

	displayText := TruncateToWidth(singleLineText, availableWidth, "...", false)

	leftPadding := stringRepeat(" ", t.paddingX)
	rightPadding := stringRepeat(" ", t.paddingX)
	lineWithPadding := leftPadding + displayText + rightPadding

	paddingNeeded := maxInt(0, width-VisibleWidth(lineWithPadding))
	result = append(result, lineWithPadding+stringRepeat(" ", paddingNeeded))

	for i := 0; i < t.paddingY; i++ {
		result = append(result, emptyLine)
	}

	return result
}

// Spacer renders empty lines.
type Spacer struct {
	lines int
}

// NewSpacer builds a spacer of n empty lines.
func NewSpacer(lines int) *Spacer { return &Spacer{lines: lines} }

// SetLines changes the spacer height.
func (s *Spacer) SetLines(lines int) { s.lines = lines }

// Invalidate is a no-op.
func (s *Spacer) Invalidate() {}

// Render returns empty lines.
func (s *Spacer) Render(width int) []string {
	result := make([]string, s.lines)
	return result
}

// SetPaddingX changes the horizontal padding and invalidates the cache
// (setPaddingX).
func (t *Text) SetPaddingX(padding int) {
	if t.paddingX == padding {
		return
	}
	t.paddingX = padding
	t.Invalidate()
}
