package tui

// Block-level markdown lexer: produces the marked-compatible block token
// stream (heading/paragraph/code/list/table/blockquote/hr/space/html/
// latexBlock) over which inline tokens are lexed lazily by the renderer.

import (
	"regexp"
	"strings"
)

var (
	mdHeadingPattern   = regexp.MustCompile(`^ {0,3}(#{1,6})(?:\s+(.*))?$`)
	mdFencePattern     = regexp.MustCompile("^ {0,3}(```|~~~)")
	mdHrPattern        = regexp.MustCompile(`^ {0,3}(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})[ \t]*$`)
	mdListItemPattern  = regexp.MustCompile(`^( {0,3})([-+*]|\d{1,9}[.)])(?:[ \t]+(.*))?$`)
	mdOrderedListStart = regexp.MustCompile(`^ {0,3}(\d{1,9})[.)](?:[ \t]+|$)`)
	mdTableDivider     = regexp.MustCompile(`^ {0,3}\|?(.*)\|?[ \t]*$`)
	mdTableCellWidth   = regexp.MustCompile(`^:?-+:?$`)
	mdBlockQuotePrefix = regexp.MustCompile(`^ {0,3}> ?`)
	mdTabPattern       = regexp.MustCompile(`\t`)
)

// mdLexBlock tokenizes markdown source into block tokens.
func mdLexBlock(src string) []MdToken {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = mdTabPattern.ReplaceAllString(src, "   ")

	var tokens []MdToken
	lines := strings.Split(src, "\n")
	pos := 0
	previousPos := -1

	for pos < len(lines) {
		line := lines[pos]

		// Blank line: space token (marked collapses runs).
		if strings.TrimSpace(line) == "" {
			pos++
			// Count if more blanks follow; a single space token regardless.
			for pos < len(lines) && strings.TrimSpace(lines[pos]) == "" {
				pos++
			}
			if len(tokens) > 0 && pos < len(lines) {
				tokens = append(tokens, MdSpace{})
			} else if len(tokens) > 0 {
				tokens = append(tokens, MdSpace{})
			}
			continue
		}

		// Latex block ($$...$$ / \[...\], including pending streaming forms).
		if latex := mdLexBlockLatex(lines, pos); latex != nil {
			tokens = append(tokens, latex.token)
			pos = latex.next
			// A pending token consumed through the end of the source: the
			// remainder joins the token (marked's pending tokenizer consumes
			// the whole line), so drop it.
			if latex.token.Pending && latex.remainder != "" {
				_ = latex.remainder
			}
			if pos <= previousPos {
				pos = previousPos + 1
			}
			previousPos = pos
			continue
		}

		// ATX heading.
		if match := mdHeadingPattern.FindStringSubmatch(line); match != nil {
			text := strings.TrimRight(match[2], " #")
			tokens = append(tokens, MdHeading{Depth: len(match[1]), Tokens: mdLexInline(text)})
			pos++
			continue
		}

		// Fenced code block.
		if match := mdFencePattern.FindStringSubmatch(line); match != nil {
			fence := strings.TrimLeft(match[1], " ")
			lang := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(line, " ")), fence))
			pos++
			var body []string
			for pos < len(lines) {
				closeMatch := mdFencePattern.FindStringSubmatch(lines[pos])
				if closeMatch != nil && strings.TrimLeft(closeMatch[1], " ") == fence {
					pos++
					break
				}
				body = append(body, mdStripFenceIndent(lines[pos], fence))
				pos++
			}
			raw := line + "\n" + strings.Join(body, "\n")
			tokens = append(tokens, MdCode{Lang: lang, Text: strings.Join(body, "\n"), Raw: raw})
			continue
		}

		// Horizontal rule.
		if mdHrPattern.MatchString(line) {
			tokens = append(tokens, MdHr{})
			pos++
			continue
		}

		// Blockquote.
		if mdBlockQuotePrefix.MatchString(line) {
			var inner []string
			for pos < len(lines) {
				current := lines[pos]
				if mdBlockQuotePrefix.MatchString(current) {
					inner = append(inner, mdBlockQuotePrefix.ReplaceAllString(current, ""))
					pos++
				} else if strings.TrimSpace(current) != "" && len(inner) > 0 {
					// Lazy continuation: non-blank line extends the quote.
					inner = append(inner, current)
					pos++
				} else {
					break
				}
			}
			tokens = append(tokens, MdBlockquote{Tokens: mdLexBlock(strings.Join(inner, "\n"))})
			continue
		}

		// List.
		if mdListItemPattern.MatchString(line) {
			list, next := mdLexList(lines, pos)
			tokens = append(tokens, list)
			pos = next
			continue
		}

		// Table: current line contains | and the next line is a divider.
		if pos+1 < len(lines) && strings.Contains(line, "|") && mdIsTableDivider(lines[pos+1]) {
			table, next := mdLexTable(lines, pos)
			tokens = append(tokens, table)
			pos = next
			continue
		}

		// Paragraph: gather until blank line or block start.
		var para []string
		para = append(para, line)
		pos++
		for pos < len(lines) {
			next := lines[pos]
			if strings.TrimSpace(next) == "" ||
				mdHeadingPattern.MatchString(next) ||
				mdFencePattern.MatchString(next) ||
				mdHrPattern.MatchString(next) ||
				mdBlockQuotePrefix.MatchString(next) ||
				mdListItemPattern.MatchString(next) ||
				mdBlockLatexStartsAt(lines, pos) ||
				(strings.Contains(next, "|") && pos+1 < len(lines) && mdIsTableDivider(lines[pos+1])) {
				break
			}
			para = append(para, next)
			pos++
		}
		paraText := strings.Join(para, "\n")
		tokens = append(tokens, MdParagraph{Tokens: mdLexInline(paraText), Text: paraText})
	}

	// Trim trailing space tokens (marked keeps them only between blocks).
	var result []MdToken
	for i, token := range tokens {
		if _, isSpace := token.(MdSpace); isSpace && i == len(tokens)-1 {
			continue
		}
		result = append(result, token)
	}
	return result
}

func mdStripFenceIndent(line, fence string) string {
	// Content inside fences may be indented up to the fence marker width.
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 0 && len(line)-len(trimmed) <= len(fence)+3 {
		return trimmed
	}
	return line
}

type mdLatexResult struct {
	token     MdLatexBlock
	next      int
	remainder string
}

var (
	mdBlockLatexDollar  = regexp.MustCompile(`^ {0,3}\$\$[ \t]*(?:\n)?([\s\S]*?)\$\$[ \t]*(?:\n|$)`)
	mdBlockLatexBracket = regexp.MustCompile(`^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*?)\\\][ \t]*(?:\n|$)`)
	mdBlockLatexPendBrk = regexp.MustCompile(`^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*)$`)
	mdBlockLatexPendDol = regexp.MustCompile(`^ {0,3}\$\$[ \t]*(?:\n)?([\s\S]*)$`)
	mdLooksLikePending  = regexp.MustCompile(`\\[A-Za-z]+|[_^=+*/<>()[\]|±≤≥≠≈∈→⇒∞∫∑√-]`)
)

func mdIsEscapedAt(source string, index int) bool {
	backslashes := 0
	runes := []rune(source)
	for position := index - 1; position >= 0 && runes[position] == '\\'; position-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func mdFindClosingDelimiter(source, closing string, start int) int {
	index := strings.Index(source[start:], closing)
	for index >= 0 {
		absolute := start + index
		if !mdIsEscapedAt(source, absolute) {
			return absolute
		}
		next := strings.Index(source[absolute+len(closing):], closing)
		if next < 0 {
			return -1
		}
		index = absolute + len(closing) + next
	}
	return -1
}

func mdLooksLikePendingDollarMath(source string) bool {
	return mdLooksLikePending.MatchString(source)
}

// mdLexBlockLatex recognizes latex blocks starting at lines[pos]. When the
// match ends mid-line, the remainder of that line is returned so the caller
// reprocesses it.
// mdLatexMatch is the shared display-math match used by both the lexer and
// the paragraph-boundary check; the two must never drift apart.
type mdLatexMatch struct {
	consumed string
	content  string
	pending  bool
}

// mdMatchBlockLatex reports the first display-math form in source, or nil.
// Display math never spans a table: without that check, an unclosed "$$" or
// "\[" pairs with a later opener or stays pending, swallowing the table
// (#11159).
func mdMatchBlockLatex(source string) *mdLatexMatch {
	if match := mdBlockLatexDollar.FindStringSubmatch(source); match != nil && strings.TrimSpace(match[1]) != "" && mdBlockLatexEndsLine(source, match[0]) && !mdContainsTableStart(match[1]) {
		return &mdLatexMatch{consumed: match[0], content: strings.TrimSpace(match[1])}
	}
	if match := mdBlockLatexBracket.FindStringSubmatch(source); match != nil && strings.TrimSpace(match[1]) != "" && !mdContainsTableStart(match[1]) {
		return &mdLatexMatch{consumed: match[0], content: strings.TrimSpace(match[1])}
	}
	if match := mdBlockLatexPendBrk.FindStringSubmatch(source); match != nil && !mdContainsTableStart(match[1]) {
		return &mdLatexMatch{consumed: match[0], content: match[1], pending: true}
	}
	if match := mdBlockLatexPendDol.FindStringSubmatch(source); match != nil && mdLooksLikePendingDollarMath(match[1]) && !mdContainsTableStart(match[1]) {
		return &mdLatexMatch{consumed: match[0], content: match[1], pending: true}
	}
	return nil
}

func mdLexBlockLatex(lines []string, pos int) *mdLatexResult {
	match := mdMatchBlockLatex(strings.Join(lines[pos:], "\n"))
	if match == nil {
		return nil
	}
	next, remainder := mdAdvanceLines(lines, pos, match.consumed, match.pending)
	return &mdLatexResult{
		token:     MdLatexBlock{Text: match.content, Raw: mdTrimLastNewline(match.consumed), Pending: match.pending},
		next:      next,
		remainder: remainder,
	}
}

// mdBlockLatexStartsAt reports whether a latex block token starts on the
// line (paragraph boundary check; no consumption).
func mdBlockLatexStartsAt(lines []string, pos int) bool {
	return mdMatchBlockLatex(strings.Join(lines[pos:], "\n")) != nil
}

func mdTrimLastNewline(s string) string {
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
}

// mdAdvanceLines maps a consumed raw string to the next line index and the
// unconsumed remainder of the final line. Pending latex forms consume the
// whole remaining input, so they always advance to the end.
func mdAdvanceLines(lines []string, pos int, consumed string, pending bool) (int, string) {
	if pending {
		return len(lines), ""
	}
	consumedLines := strings.Count(consumed, "\n")
	if consumedLines >= len(lines)-pos {
		// Consumed through the end of the source.
		return len(lines), ""
	}
	if strings.HasSuffix(consumed, "\n") {
		return pos + consumedLines + 1, ""
	}
	// Consumed part of line pos+consumedLines: the rest of that line
	// remains to be reprocessed.
	target := lines[pos+consumedLines]
	if len(consumed) <= len(target) && strings.HasPrefix(target, consumed) {
		return pos + consumedLines, target[len(consumed):]
	}
	return pos + consumedLines + 1, ""
}

// mdIsTableDivider reports whether the line is a |---|---| divider row.
func mdIsTableDivider(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "-") {
		return false
	}
	if !strings.Contains(trimmed, "|") && !strings.HasPrefix(trimmed, "|") {
		// Single-column tables still need pipes in marked's GFM tables.
		return false
	}
	for _, cell := range mdSplitTableRow(trimmed) {
		cellText := strings.TrimSpace(cell)
		if cellText == "" || !mdTableCellWidth.MatchString(cellText) || !strings.Contains(cellText, "-") {
			return false
		}
	}
	return true
}

func mdSplitTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	// Cells may contain escaped pipes.
	var cells []string
	var current strings.Builder
	runes := []rune(trimmed)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) && runes[i+1] == '|' {
			current.WriteRune('|')
			i++
			continue
		}
		if runes[i] == '|' {
			cells = append(cells, current.String())
			current.Reset()
			continue
		}
		current.WriteRune(runes[i])
	}
	cells = append(cells, current.String())
	return cells
}

// mdLexTable parses a GFM table starting at lines[pos] (header row).
func mdLexTable(lines []string, pos int) (MdTable, int) {
	headerLine := lines[pos]
	dividerLine := lines[pos+1]
	headerCells := mdSplitTableRow(headerLine)

	align := make([]string, len(headerCells))
	for i, cell := range mdSplitTableRow(dividerLine) {
		if i >= len(align) {
			break
		}
		cellText := strings.TrimSpace(cell)
		left := strings.HasPrefix(cellText, ":")
		right := strings.HasSuffix(cellText, ":")
		switch {
		case left && right:
			align[i] = "center"
		case right:
			align[i] = "right"
		case left:
			align[i] = "left"
		}
	}

	header := make([]*MdTableCell, len(headerCells))
	for i, cell := range headerCells {
		cellText := strings.TrimSpace(cell)
		header[i] = &MdTableCell{Tokens: mdLexInline(cellText), Text: cellText}
	}

	pos += 2
	var rows [][]*MdTableCell
	for pos < len(lines) {
		line := lines[pos]
		if strings.TrimSpace(line) == "" || !strings.Contains(line, "|") {
			break
		}
		rowCells := mdSplitTableRow(line)
		row := make([]*MdTableCell, len(header))
		for i := range row {
			cellText := ""
			if i < len(rowCells) {
				cellText = strings.TrimSpace(rowCells[i])
			}
			row[i] = &MdTableCell{Tokens: mdLexInline(cellText), Text: cellText}
		}
		rows = append(rows, row)
		pos++
	}

	rawLines := []string{headerLine, dividerLine}
	raw := strings.Join(rawLines, "\n")
	return MdTable{Header: header, Align: align, Rows: rows, Raw: raw}, pos
}

// mdLexList parses a (possibly nested, possibly loose) list.
func mdLexList(lines []string, pos int) (*MdList, int) {
	match := mdListItemPattern.FindStringSubmatch(lines[pos])
	ordered := false
	start := 0
	if orderMatch := mdOrderedListStart.FindStringSubmatch(lines[pos]); orderMatch != nil {
		ordered = true
		start, _ = parseIntSafe(orderMatch[1])
	}

	var items []*MdListItem
	loose := false
	baseIndent := len(match[1])

	for pos < len(lines) {
		line := lines[pos]
		if strings.TrimSpace(line) == "" {
			// Blank line: look ahead — content belonging to the list means
			// loose; otherwise the list ends.
			nextPos := pos + 1
			for nextPos < len(lines) && strings.TrimSpace(lines[nextPos]) == "" {
				nextPos++
			}
			if nextPos < len(lines) && mdIsListContinuation(lines[nextPos], baseIndent) {
				loose = true
				pos = nextPos
				continue
			}
			break
		}

		itemMatch := mdListItemPattern.FindStringSubmatch(line)
		indent := 0
		if itemMatch != nil {
			indent = len(itemMatch[1])
		} else {
			indent = mdLeadingSpaces(line)
		}
		if itemMatch == nil && indent <= baseIndent {
			// Non-list line at or below the marker indent that isn't a
			// continuation breaks the list (lazy paragraph lines have
			// indent 0 and continue the last item's paragraph).
			if indent == 0 && len(items) > 0 && !mdIsBlockStart(line) {
				// Lazy continuation of the last item.
				last := items[len(items)-1]
				last.Raw += "\n" + line
				pos++
				continue
			}
			break
		}
		if itemMatch != nil && indent < baseIndent {
			break
		}

		// Gather this item's lines: the marker line plus following lines
		// indented beyond the content column, or lazy continuations.
		contentCol := indent + len(itemMatch[2]) + 1
		var itemLines []string
		itemLines = append(itemLines, itemMatch[3])
		pos++
		for pos < len(lines) {
			next := lines[pos]
			if strings.TrimSpace(next) == "" {
				// Include blank only when further item content follows.
				peek := pos + 1
				for peek < len(lines) && strings.TrimSpace(lines[peek]) == "" {
					peek++
				}
				if peek < len(lines) && mdLeadingSpaces(lines[peek]) >= contentCol {
					itemLines = append(itemLines, "")
					pos++
					continue
				}
				if peek < len(lines) && mdIsListContinuation(lines[peek], baseIndent) {
					loose = true
				}
				break
			}
			nextIndent := mdLeadingSpaces(next)
			if nextIndent >= contentCol {
				itemLines = append(itemLines, next[contentCol:])
				pos++
				continue
			}
			if mdListItemPattern.MatchString(next) || mdIsBlockStart(next) {
				break
			}
			// Lazy continuation line.
			itemLines = append(itemLines, next)
			pos++
		}

		raw := line
		item := &MdListItem{Raw: raw}
		trimmed := strings.Join(itemLines, "\n")
		if taskMatch := mdTaskPattern.FindStringSubmatch(trimmed); taskMatch != nil {
			item.Task = true
			item.Checked = taskMatch[1] == "x"
			trimmed = strings.TrimSpace(mdTaskPattern.ReplaceAllString(trimmed, ""))
		}
		item.Tokens = mdLexBlock(trimmed)
		if item.Task {
			// Task items carry a leading checkbox token.
			item.Tokens = append([]MdToken{MdCheckbox{Checked: item.Checked}}, item.Tokens...)
		}
		if !loose {
			// Tight items hold text blocks (marked uses paragraph only for
			// loose items).
			for i, token := range item.Tokens {
				if paragraph, ok := token.(MdParagraph); ok {
					item.Tokens[i] = MdTextBlock{Text: mdTokensText(paragraph.Tokens), Tokens: paragraph.Tokens}
				}
			}
		}
		items = append(items, item)
	}

	return &MdList{Ordered: ordered, Start: start, Loose: loose, Items: items}, pos
}

var mdTaskPattern = regexp.MustCompile(`^\[([ xX])\] `)

func parseIntSafe(s string) (int, bool) {
	value := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		value = value*10 + int(c-'0')
	}
	return value, true
}

func mdLeadingSpaces(line string) int {
	count := 0
	for count < len(line) && line[count] == ' ' {
		count++
	}
	return count
}

func mdIsListContinuation(line string, baseIndent int) bool {
	if mdListItemPattern.MatchString(line) {
		return true
	}
	return mdLeadingSpaces(line) > baseIndent
}

func mdIsBlockStart(line string) bool {
	return mdHeadingPattern.MatchString(line) ||
		mdFencePattern.MatchString(line) ||
		mdHrPattern.MatchString(line) ||
		mdBlockQuotePrefix.MatchString(line)
}

func mdTokensText(tokens []MdToken) string {
	var b strings.Builder
	for _, token := range tokens {
		switch t := token.(type) {
		case MdText:
			b.WriteString(t.Text)
		case MdCodespan:
			b.WriteString(t.Text)
		case MdStrong:
			b.WriteString(mdTokensText(t.Tokens))
		case MdEm:
			b.WriteString(mdTokensText(t.Tokens))
		case MdDel:
			b.WriteString(mdTokensText(t.Tokens))
		case MdLink:
			b.WriteString(mdTokensText(t.Tokens))
		case MdEscape:
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// mdBlockLatexEndsLine reports whether the matched raw ends at a line
// boundary (the closing delimiter must be followed by end-of-line for the
// non-pending form).
func mdBlockLatexEndsLine(source, consumed string) bool {
	end := len(consumed)
	if end >= len(source) {
		return true
	}
	return source[end] == '\n'
}

// mdTableDelimiterRow matches a GFM delimiter row such as "|---|---|".
var mdTableDelimiterRow = regexp.MustCompile(`^ {0,3}\|?[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$`)

// mdContainsTableStart reports whether source starts a GFM table: a header
// row followed by a delimiter row. The header needs a "|" only when the
// delimiter has none.
func mdContainsTableStart(source string) bool {
	lines := strings.Split(source, "\n")
	for index := 0; index+1 < len(lines); index++ {
		delimiterRow := lines[index+1]
		if (strings.Contains(delimiterRow, "|") || strings.Contains(lines[index], "|")) && mdTableDelimiterRow.MatchString(delimiterRow) {
			return true
		}
	}
	return false
}
