package tui

// Port of components/markdown.ts (the renderer): styled terminal output for
// the marked-compatible token trees produced by markdown_block.go /
// markdown_inline.go.

import (
	"regexp"
	"strings"
)

// DefaultTextStyle is the base styling applied to all markdown text.
type DefaultTextStyle struct {
	Color         func(text string) string
	BgColor       func(text string) string
	Bold          bool
	Italic        bool
	Strikethrough bool
	Underline     bool
}

// MarkdownTheme styles markdown elements with ANSI codes.
type MarkdownTheme struct {
	Heading         func(text string) string
	Link            func(text string) string
	LinkUrl         func(text string) string
	Code            func(text string) string
	CodeBlock       func(text string) string
	CodeBlockBorder func(text string) string
	Quote           func(text string) string
	QuoteBorder     func(text string) string
	Hr              func(text string) string
	ListBullet      func(text string) string
	Bold            func(text string) string
	Italic          func(text string) string
	Strikethrough   func(text string) string
	Underline       func(text string) string
	HighlightCode   func(code, lang string) []string
	// CodeBlockIndent prefixes each code line (default "  ").
	CodeBlockIndent string
}

// MarkdownOptions tweak rendering behavior.
type MarkdownOptions struct {
	// PreserveOrderedListMarkers keeps source markers instead of normalizing.
	PreserveOrderedListMarkers bool
	// PreserveBackslashEscapes keeps source escapes.
	PreserveBackslashEscapes bool
	// Transform rewrites the source before parsing.
	Transform func(markdown string, availableWidth int) string
	// RenderLatex renders LaTeX as Unicode text (default true).
	RenderLatexOption *bool
}

type mdInlineStyleContext struct {
	applyText   func(text string) string
	stylePrefix string
}

// Markdown renders markdown text with theme styling.
type Markdown struct {
	text               string
	paddingX           int
	paddingY           int
	defaultTextStyle   *DefaultTextStyle
	theme              MarkdownTheme
	options            MarkdownOptions
	defaultStylePrefix *string

	cachedText  *string
	cachedWidth *int
	cachedLines []string
}

// NewMarkdown builds the component.
func NewMarkdown(text string, paddingX, paddingY int, theme MarkdownTheme, defaultTextStyle *DefaultTextStyle, options *MarkdownOptions) *Markdown {
	component := &Markdown{
		text:             text,
		paddingX:         paddingX,
		paddingY:         paddingY,
		defaultTextStyle: defaultTextStyle,
		theme:            theme,
	}
	if options != nil {
		component.options = *options
	}
	// Zero-value themes carry nil style functions; identity defaults keep
	// renders safe when callers pass MarkdownTheme{}.
	identity := func(s string) string { return s }
	for _, style := range []*func(string) string{
		&component.theme.Heading,
		&component.theme.Link,
		&component.theme.LinkUrl,
		&component.theme.Code,
		&component.theme.CodeBlock,
		&component.theme.CodeBlockBorder,
		&component.theme.Quote,
		&component.theme.QuoteBorder,
		&component.theme.Hr,
		&component.theme.ListBullet,
		&component.theme.Bold,
		&component.theme.Italic,
		&component.theme.Strikethrough,
		&component.theme.Underline,
	} {
		if *style == nil {
			*style = identity
		}
	}
	return component
}

// SetText replaces the text.
func (m *Markdown) SetText(text string) {
	m.text = text
	m.Invalidate()
}

// Invalidate drops the cache.
func (m *Markdown) Invalidate() {
	m.cachedText = nil
	m.cachedWidth = nil
	m.cachedLines = nil
	m.defaultStylePrefix = nil
}

// Render renders the markdown to styled lines at width.
func (m *Markdown) Render(width int) []string {
	if m.cachedLines != nil && m.cachedText != nil && *m.cachedText == m.text && m.cachedWidth != nil && *m.cachedWidth == width {
		return m.cachedLines
	}

	contentWidth := maxInt(1, width-m.paddingX*2)
	text := m.text
	if m.options.Transform != nil {
		text = m.options.Transform(m.text, contentWidth)
	}

	if text == "" || strings.TrimSpace(text) == "" {
		m.cachedText = &m.text
		cachedWidth := width
		m.cachedWidth = &cachedWidth
		m.cachedLines = []string{}
		return m.cachedLines
	}

	// Tabs render as three spaces.
	normalizedText := strings.ReplaceAll(text, "\t", "   ")

	tokens := mdLexBlock(normalizedText)
	trimPartialClosingFences(tokens)

	var renderedLines []string
	for i, token := range tokens {
		var nextType string
		if i+1 < len(tokens) {
			nextType = tokens[i+1].mdTokenKind()
		}
		renderedLines = append(renderedLines, m.renderToken(token, contentWidth, nextType, nil)...)
	}

	// Wrap lines (no padding or background yet).
	var wrappedLines []string
	for _, line := range renderedLines {
		if IsImageLine(line) {
			wrappedLines = append(wrappedLines, line)
		} else {
			wrappedLines = append(wrappedLines, WrapTextWithAnsi(line, contentWidth)...)
		}
	}

	// Margins and background.
	leftMargin := stringRepeat(" ", m.paddingX)
	rightMargin := stringRepeat(" ", m.paddingX)
	var bgFn func(string) string
	if m.defaultTextStyle != nil {
		bgFn = m.defaultTextStyle.BgColor
	}
	var contentLines []string
	for _, line := range wrappedLines {
		if IsImageLine(line) {
			contentLines = append(contentLines, line)
			continue
		}
		lineWithMargins := leftMargin + line + rightMargin
		if bgFn != nil {
			contentLines = append(contentLines, ApplyBackgroundToLine(lineWithMargins, width, bgFn))
		} else {
			contentLines = append(contentLines, lineWithMargins+stringRepeat(" ", maxInt(0, width-VisibleWidth(lineWithMargins))))
		}
	}

	// Vertical padding.
	emptyLine := stringRepeat(" ", width)
	var emptyLines []string
	for i := 0; i < m.paddingY; i++ {
		if bgFn != nil {
			emptyLines = append(emptyLines, ApplyBackgroundToLine(emptyLine, width, bgFn))
		} else {
			emptyLines = append(emptyLines, emptyLine)
		}
	}

	result := append(append(append([]string{}, emptyLines...), contentLines...), emptyLines...)
	m.cachedText = &m.text
	cachedWidth := width
	m.cachedWidth = &cachedWidth
	m.cachedLines = result
	if len(result) == 0 {
		return []string{""}
	}
	return result
}

var mdFenceStartPattern = regexp.MustCompile("^(`{3,}|~{3,})")

// trimPartialClosingFences drops streamed partial closing fences so code
// blocks do not shrink/flicker when the final fence character arrives
// (pi issue 5825).
func trimPartialClosingFences(tokens []MdToken) {
	if len(tokens) == 0 {
		return
	}
	token := tokens[len(tokens)-1]
	switch t := token.(type) {
	case *MdList:
		if len(t.Items) > 0 {
			last := t.Items[len(t.Items)-1]
			trimPartialClosingFences(last.Tokens)
		}
	case MdBlockquote:
		trimPartialClosingFences(t.Tokens)
	case MdCode:
		marker := mdFenceStartPattern.FindStringSubmatch(t.Raw)
		lines := strings.Split(t.Raw, "\n")
		lastLine := lines[len(lines)-1]
		if marker == nil || lastLine == "" || len(lastLine) >= len(marker[1]) || lastLine != strings.Repeat(string(marker[1][0]), len(lastLine)) {
			return
		}
		t.Text = strings.TrimSuffix(strings.TrimSuffix(t.Text, lastLine), "\n")
	}
}

func (m *Markdown) applyDefaultStyle(text string) string {
	if m.defaultTextStyle == nil {
		return text
	}
	styled := text
	if m.defaultTextStyle.Color != nil {
		styled = m.defaultTextStyle.Color(styled)
	}
	if m.defaultTextStyle.Bold {
		styled = m.theme.Bold(styled)
	}
	if m.defaultTextStyle.Italic {
		styled = m.theme.Italic(styled)
	}
	if m.defaultTextStyle.Strikethrough {
		styled = m.theme.Strikethrough(styled)
	}
	if m.defaultTextStyle.Underline {
		styled = m.theme.Underline(styled)
	}
	return styled
}

const mdStyleSentinel = "\x00"

func (m *Markdown) getDefaultStylePrefix() string {
	if m.defaultTextStyle == nil {
		return ""
	}
	if m.defaultStylePrefix != nil {
		return *m.defaultStylePrefix
	}

	styled := mdStyleSentinel
	if m.defaultTextStyle.Color != nil {
		styled = m.defaultTextStyle.Color(styled)
	}
	if m.defaultTextStyle.Bold {
		styled = m.theme.Bold(styled)
	}
	if m.defaultTextStyle.Italic {
		styled = m.theme.Italic(styled)
	}
	if m.defaultTextStyle.Strikethrough {
		styled = m.theme.Strikethrough(styled)
	}
	if m.defaultTextStyle.Underline {
		styled = m.theme.Underline(styled)
	}
	prefix := prefixBeforeSentinel(styled)
	m.defaultStylePrefix = &prefix
	return prefix
}

func prefixBeforeSentinel(styled string) string {
	index := strings.Index(styled, mdStyleSentinel)
	if index >= 0 {
		return styled[:index]
	}
	return ""
}

func mdStylePrefixOf(styleFn func(string) string) string {
	return prefixBeforeSentinel(styleFn(mdStyleSentinel))
}

func (m *Markdown) getDefaultInlineStyleContext() mdInlineStyleContext {
	return mdInlineStyleContext{
		applyText:   m.applyDefaultStyle,
		stylePrefix: m.getDefaultStylePrefix(),
	}
}

func (m *Markdown) renderLatexEnabled() bool {
	return m.options.RenderLatexOption == nil || *m.options.RenderLatexOption
}

func (m *Markdown) renderToken(token MdToken, width int, nextTokenType string, styleContext *mdInlineStyleContext) []string {
	var lines []string

	switch t := token.(type) {
	case MdHeading:
		headingPrefix := strings.Repeat("#", t.Depth) + " "
		var headingStyleFn func(string) string
		if t.Depth == 1 {
			headingStyleFn = func(text string) string { return m.theme.Heading(m.theme.Bold(m.theme.Underline(text))) }
		} else {
			headingStyleFn = func(text string) string { return m.theme.Heading(m.theme.Bold(text)) }
		}
		headingStyleContext := mdInlineStyleContext{
			applyText:   headingStyleFn,
			stylePrefix: mdStylePrefixOf(headingStyleFn),
		}
		headingText := m.renderInlineTokens(t.Tokens, &headingStyleContext)
		styledHeading := headingText
		if t.Depth >= 3 {
			styledHeading = headingStyleFn(headingPrefix) + headingText
		}
		lines = append(lines, styledHeading)
		if nextTokenType != "" && nextTokenType != "space" {
			lines = append(lines, "")
		}

	case MdParagraph:
		lines = append(lines, m.renderInlineTokens(t.Tokens, styleContext))
		if nextTokenType != "" && nextTokenType != "list" && nextTokenType != "space" {
			lines = append(lines, "")
		}

	case MdTextBlock:
		lines = append(lines, m.renderInlineTokens(t.Tokens, styleContext))

	case MdLatexBlock:
		rendered := strings.TrimSpace(t.Raw)
		if !t.Pending && m.renderLatexEnabled() {
			if out, ok := RenderLatex(t.Text, RenderLatexOptions{Display: true}); ok {
				rendered = out
			}
		}
		for _, line := range strings.Split(rendered, "\n") {
			lines = append(lines, m.applyDefaultStyle(line))
		}
		if nextTokenType != "" && nextTokenType != "space" {
			lines = append(lines, "")
		}

	case MdCode:
		indent := m.theme.CodeBlockIndent
		if indent == "" {
			indent = "  "
		}
		lines = append(lines, m.theme.CodeBlockBorder("```"+t.Lang))
		if m.theme.HighlightCode != nil {
			for _, hlLine := range m.theme.HighlightCode(t.Text, t.Lang) {
				lines = append(lines, indent+hlLine)
			}
		} else {
			for _, codeLine := range strings.Split(t.Text, "\n") {
				lines = append(lines, indent+m.theme.CodeBlock(codeLine))
			}
		}
		lines = append(lines, m.theme.CodeBlockBorder("```"))
		if nextTokenType != "" && nextTokenType != "space" {
			lines = append(lines, "")
		}

	case *MdList:
		lines = append(lines, m.renderList(t, 0, width, styleContext)...)

	case MdTable:
		lines = append(lines, m.renderTable(&t, width, nextTokenType, styleContext)...)

	case MdBlockquote:
		quoteStyle := func(text string) string { return m.theme.Quote(m.theme.Italic(text)) }
		quoteStylePrefix := mdStylePrefixOf(quoteStyle)
		applyQuoteStyle := func(line string) string {
			if quoteStylePrefix == "" {
				return quoteStyle(line)
			}
			lineWithReappliedStyle := strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+quoteStylePrefix)
			return quoteStyle(lineWithReappliedStyle)
		}

		quoteContentWidth := maxInt(1, width-2)
		quoteInlineStyleContext := mdInlineStyleContext{
			applyText:   func(text string) string { return text },
			stylePrefix: quoteStylePrefix,
		}
		var renderedQuoteLines []string
		for i, quoteToken := range t.Tokens {
			var nextQuoteType string
			if i+1 < len(t.Tokens) {
				nextQuoteType = t.Tokens[i+1].mdTokenKind()
			}
			renderedQuoteLines = append(renderedQuoteLines, m.renderToken(quoteToken, quoteContentWidth, nextQuoteType, &quoteInlineStyleContext)...)
		}
		for len(renderedQuoteLines) > 0 && renderedQuoteLines[len(renderedQuoteLines)-1] == "" {
			renderedQuoteLines = renderedQuoteLines[:len(renderedQuoteLines)-1]
		}
		for _, quoteLine := range renderedQuoteLines {
			styledLine := applyQuoteStyle(quoteLine)
			for _, wrappedLine := range WrapTextWithAnsi(styledLine, quoteContentWidth) {
				lines = append(lines, m.theme.QuoteBorder("│ ")+wrappedLine)
			}
		}
		if nextTokenType != "" && nextTokenType != "space" {
			lines = append(lines, "")
		}

	case MdHr:
		lines = append(lines, m.theme.Hr(stringRepeat("─", minInt(width, 80))))
		if nextTokenType != "" && nextTokenType != "space" {
			lines = append(lines, "")
		}

	case MdHtml:
		lines = append(lines, m.applyDefaultStyle(strings.TrimSpace(t.Raw)))

	case MdSpace:
		lines = append(lines, "")
	}

	return lines
}

func (m *Markdown) renderInlineTokens(tokens []MdToken, styleContext *mdInlineStyleContext) string {
	result := ""
	resolved := styleContext
	if resolved == nil {
		defaultContext := m.getDefaultInlineStyleContext()
		resolved = &defaultContext
	}
	applyText, stylePrefix := resolved.applyText, resolved.stylePrefix
	applyTextWithNewlines := func(text string) string {
		var segments []string
		for _, segment := range strings.Split(text, "\n") {
			segments = append(segments, applyText(segment))
		}
		return strings.Join(segments, "\n")
	}

	for _, token := range tokens {
		switch t := token.(type) {
		case MdLatex:
			rendered := t.Raw
			if !t.Pending && m.renderLatexEnabled() {
				if out, ok := RenderLatex(t.Text, RenderLatexOptions{}); ok {
					rendered = out
				}
			}
			result += applyTextWithNewlines(rendered)

		case MdEscape:
			if m.options.PreserveBackslashEscapes {
				result += applyTextWithNewlines(t.Raw)
			} else {
				result += applyTextWithNewlines(t.Text)
			}

		case MdText:
			if len(t.Tokens) > 0 {
				result += m.renderInlineTokens(t.Tokens, resolved)
			} else {
				result += applyTextWithNewlines(t.Text)
			}

		case MdParagraph:
			result += m.renderInlineTokens(t.Tokens, resolved)

		case MdStrong:
			result += m.theme.Bold(m.renderInlineTokens(t.Tokens, resolved)) + stylePrefix

		case MdEm:
			result += m.theme.Italic(m.renderInlineTokens(t.Tokens, resolved)) + stylePrefix

		case MdCodespan:
			result += m.theme.Code(t.Text) + stylePrefix

		case MdLink:
			linkText := m.renderInlineTokens(t.Tokens, resolved)
			styledLink := m.theme.Link(m.theme.Underline(linkText))
			if GetCapabilities().Hyperlinks {
				// OSC 8: the URL is not printed inline.
				result += Hyperlink(styledLink, t.Href) + stylePrefix
			} else {
				hrefForComparison := t.Href
				if strings.HasPrefix(hrefForComparison, "mailto:") {
					hrefForComparison = hrefForComparison[len("mailto:"):]
				}
				if t.Text == t.Href || t.Text == hrefForComparison {
					result += styledLink + stylePrefix
				} else {
					result += styledLink + m.theme.LinkUrl(" ("+t.Href+")") + stylePrefix
				}
			}

		case MdBr:
			result += "\n"

		case MdDel:
			result += m.theme.Strikethrough(m.renderInlineTokens(t.Tokens, resolved)) + stylePrefix

		case MdHtml:
			result += applyTextWithNewlines(t.Raw)

		case MdCheckbox:
			// Checkboxes render in the list bullet, not inline.
		}
	}

	for stylePrefix != "" && strings.HasSuffix(result, stylePrefix) {
		result = result[:len(result)-len(stylePrefix)]
	}
	return result
}

var mdOrderedMarkerPattern = regexp.MustCompile(`^(?: {0,3})(\d{1,9}[.)])[ \t]+`)

func (m *Markdown) getOrderedListMarker(item *MdListItem) string {
	match := mdOrderedMarkerPattern.FindStringSubmatch(item.Raw)
	if match == nil {
		return ""
	}
	return match[1] + " "
}

func (m *Markdown) getUnorderedListMarker(item *MdListItem) string {
	// Go RE2 has no lookahead: match the marker then verify the rest.
	trimmed := strings.TrimLeft(item.Raw, " ")
	if trimmed == "" {
		return ""
	}
	switch trimmed[0] {
	case '-', '+', '*':
		rest := trimmed[1:]
		if rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' {
			return string(trimmed[0]) + " "
		}
	}
	return ""
}

func (m *Markdown) renderList(token *MdList, depth int, width int, styleContext *mdInlineStyleContext) []string {
	var lines []string
	indent := stringRepeat("    ", depth)
	startNumber := token.Start
	if !token.Ordered {
		startNumber = 1
	}

	for i, item := range token.Items {
		isLastItem := i == len(token.Items)-1
		bullet := "- "
		if token.Ordered {
			bullet = itoa(startNumber+i) + ". "
			if m.options.PreserveOrderedListMarkers {
				if marker := m.getOrderedListMarker(item); marker != "" {
					bullet = marker
				}
			}
		} else if m.options.PreserveOrderedListMarkers {
			if marker := m.getUnorderedListMarker(item); marker != "" {
				bullet = marker
			}
		}
		taskMarker := ""
		if item.Task {
			checked := " "
			if item.Checked {
				checked = "x"
			}
			taskMarker = "[" + checked + "] "
		}
		marker := bullet + taskMarker
		firstPrefix := indent + m.theme.ListBullet(marker)
		continuationPrefix := indent + stringRepeat(" ", VisibleWidth(marker))
		itemWidth := maxInt(1, width-VisibleWidth(firstPrefix))
		renderedAnyLine := false

		for _, itemToken := range item.Tokens {
			if nested, isList := itemToken.(*MdList); isList {
				lines = append(lines, m.renderList(nested, depth+1, width, styleContext)...)
				renderedAnyLine = true
				continue
			}
			if _, isCheckbox := itemToken.(MdCheckbox); isCheckbox {
				continue
			}
			itemLines := m.renderToken(itemToken, itemWidth, "", styleContext)
			for _, line := range itemLines {
				for _, wrappedLine := range WrapTextWithAnsi(line, itemWidth) {
					linePrefix := continuationPrefix
					if !renderedAnyLine {
						linePrefix = firstPrefix
					}
					lines = append(lines, linePrefix+wrappedLine)
					renderedAnyLine = true
				}
			}
		}

		if !renderedAnyLine {
			lines = append(lines, firstPrefix)
		}

		if token.Loose && !isLastItem {
			lines = append(lines, "")
		}
	}

	return lines
}

var mdWordSplit = regexp.MustCompile(`\s+`)

func (m *Markdown) getLongestWordWidth(text string, maxWidth int) int {
	longest := 0
	for _, word := range mdWordSplit.Split(text, -1) {
		if word == "" {
			continue
		}
		if w := VisibleWidth(word); w > longest {
			longest = w
		}
	}
	if maxWidth >= 0 && longest > maxWidth {
		return maxWidth
	}
	return longest
}

func (m *Markdown) wrapCellText(text string, maxWidth int, stylePrefix string) []string {
	lines := WrapTextWithAnsi(text, maxInt(1, maxWidth))
	result := make([]string, len(lines))
	for index, line := range lines {
		styleReset := ""
		if index < len(lines)-1 {
			styleReset = "\x1b[22;23;24;25;27;28;29;39m"
		}
		result[index] = line + styleReset + stylePrefix
	}
	return result
}

func (m *Markdown) renderTable(token *MdTable, availableWidth int, nextTokenType string, styleContext *mdInlineStyleContext) []string {
	var lines []string
	numCols := len(token.Header)
	if numCols == 0 {
		return lines
	}

	borderOverhead := 3*numCols + 1
	availableForCells := availableWidth - borderOverhead
	if availableForCells < numCols {
		// Too narrow: fall back to raw markdown.
		fallbackLines := WrapTextWithAnsi(token.Raw, availableWidth)
		if nextTokenType != "" && nextTokenType != "space" {
			fallbackLines = append(fallbackLines, "")
		}
		return fallbackLines
	}

	maxUnbrokenWordWidth := 30
	stylePrefix := ""
	if styleContext != nil {
		stylePrefix = styleContext.stylePrefix
	}

	naturalWidths := make([]int, numCols)
	minWordWidths := make([]int, numCols)
	for i, cell := range token.Header {
		headerText := m.renderInlineTokens(cell.Tokens, styleContext)
		naturalWidths[i] = VisibleWidth(headerText)
		minWordWidths[i] = maxInt(1, m.getLongestWordWidth(headerText, maxUnbrokenWordWidth))
	}
	for _, row := range token.Rows {
		for i, cell := range row {
			cellText := m.renderInlineTokens(cell.Tokens, styleContext)
			if w := VisibleWidth(cellText); w > naturalWidths[i] {
				naturalWidths[i] = w
			}
			if w := m.getLongestWordWidth(cellText, maxUnbrokenWordWidth); w > minWordWidths[i] {
				minWordWidths[i] = w
			}
		}
	}

	minColumnWidths := append([]int(nil), minWordWidths...)
	minCellsWidth := 0
	for _, w := range minColumnWidths {
		minCellsWidth += w
	}

	if minCellsWidth > availableForCells {
		for i := range minColumnWidths {
			minColumnWidths[i] = 1
		}
		remaining := availableForCells - numCols
		if remaining > 0 {
			totalWeight := 0
			for _, w := range minWordWidths {
				totalWeight += maxInt(0, w-1)
			}
			growth := make([]int, numCols)
			for i, w := range minWordWidths {
				if totalWeight > 0 {
					growth[i] = int(float64(maxInt(0, w-1)) / float64(totalWeight) * float64(remaining))
				}
			}
			for i := range minColumnWidths {
				minColumnWidths[i] += growth[i]
			}
			allocated := 0
			for _, g := range growth {
				allocated += g
			}
			leftover := remaining - allocated
			for i := 0; leftover > 0 && i < numCols; i++ {
				minColumnWidths[i]++
				leftover--
			}
		}
		minCellsWidth = 0
		for _, w := range minColumnWidths {
			minCellsWidth += w
		}
	}

	totalNaturalWidth := borderOverhead
	for _, w := range naturalWidths {
		totalNaturalWidth += w
	}
	var columnWidths []int
	if totalNaturalWidth <= availableWidth {
		columnWidths = make([]int, numCols)
		for i, w := range naturalWidths {
			columnWidths[i] = maxInt(w, minColumnWidths[i])
		}
	} else {
		totalGrowPotential := 0
		for i, w := range naturalWidths {
			totalGrowPotential += maxInt(0, w-minColumnWidths[i])
		}
		extraWidth := maxInt(0, availableForCells-minCellsWidth)
		columnWidths = make([]int, numCols)
		for i, minWidth := range minColumnWidths {
			grow := 0
			if totalGrowPotential > 0 {
				grow = int(float64(maxInt(0, naturalWidths[i]-minWidth)) / float64(totalGrowPotential) * float64(extraWidth))
			}
			columnWidths[i] = minWidth + grow
		}
		allocated := 0
		for _, w := range columnWidths {
			allocated += w
		}
		remaining := availableForCells - allocated
		for remaining > 0 {
			grew := false
			for i := 0; i < numCols && remaining > 0; i++ {
				if columnWidths[i] < naturalWidths[i] {
					columnWidths[i]++
					remaining--
					grew = true
				}
			}
			if !grew {
				break
			}
		}
	}

	// Top border.
	topBorderCells := make([]string, numCols)
	for i, w := range columnWidths {
		topBorderCells[i] = stringRepeat("─", w)
	}
	lines = append(lines, "┌─"+strings.Join(topBorderCells, "─┬─")+"─┐")

	// Header.
	headerCellLines := make([][]string, numCols)
	for i, cell := range token.Header {
		text := m.renderInlineTokens(cell.Tokens, styleContext)
		headerCellLines[i] = m.wrapCellText(text, columnWidths[i], stylePrefix)
	}
	headerLineCount := 0
	for _, cellLines := range headerCellLines {
		if len(cellLines) > headerLineCount {
			headerLineCount = len(cellLines)
		}
	}
	for lineIdx := 0; lineIdx < headerLineCount; lineIdx++ {
		rowParts := make([]string, numCols)
		for colIdx, cellLines := range headerCellLines {
			text := ""
			if lineIdx < len(cellLines) {
				text = cellLines[lineIdx]
			}
			padded := text + stringRepeat(" ", maxInt(0, columnWidths[colIdx]-VisibleWidth(text)))
			rowParts[colIdx] = m.theme.Bold(padded)
		}
		lines = append(lines, "│ "+strings.Join(rowParts, " │ ")+" │")
	}

	// Separator.
	separatorCells := make([]string, numCols)
	for i, w := range columnWidths {
		separatorCells[i] = stringRepeat("─", w)
	}
	separatorLine := "├─" + strings.Join(separatorCells, "─┼─") + "─┤"
	lines = append(lines, separatorLine)

	// Rows.
	for rowIndex, row := range token.Rows {
		rowCellLines := make([][]string, numCols)
		for i, cell := range row {
			text := m.renderInlineTokens(cell.Tokens, styleContext)
			rowCellLines[i] = m.wrapCellText(text, columnWidths[i], stylePrefix)
		}
		rowLineCount := 0
		for _, cellLines := range rowCellLines {
			if len(cellLines) > rowLineCount {
				rowLineCount = len(cellLines)
			}
		}
		for lineIdx := 0; lineIdx < rowLineCount; lineIdx++ {
			rowParts := make([]string, numCols)
			for colIdx, cellLines := range rowCellLines {
				text := ""
				if lineIdx < len(cellLines) {
					text = cellLines[lineIdx]
				}
				rowParts[colIdx] = text + stringRepeat(" ", maxInt(0, columnWidths[colIdx]-VisibleWidth(text)))
			}
			lines = append(lines, "│ "+strings.Join(rowParts, " │ ")+" │")
		}
		if rowIndex < len(token.Rows)-1 {
			lines = append(lines, separatorLine)
		}
	}

	// Bottom border.
	bottomBorderCells := make([]string, numCols)
	for i, w := range columnWidths {
		bottomBorderCells[i] = stringRepeat("─", w)
	}
	lines = append(lines, "└─"+strings.Join(bottomBorderCells, "─┴─")+"─┘")

	if nextTokenType != "" && nextTokenType != "space" {
		lines = append(lines, "")
	}
	return lines
}
