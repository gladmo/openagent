package tui

// The LaTeX command parser (LatexParser class continuation of latex.ts).

import (
	"regexp"
	"strings"
	"unicode"
)

var latexLimitsModifier = regexp.MustCompile(`^\\(limits|nolimits)`)
var latexAlignatPrefix = regexp.MustCompile(`^\s*\{[^}]*\}`)

func isASCIILetterRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

var latexRowSplit = regexp.MustCompile(`\\\\(?:\[[^\]\n]*\])?`)

func (p *latexParser) render() (string, bool) {
	rendered := p.parseSequence(0)
	if !p.supported || p.position != len(p.source) {
		return "", false
	}
	return latexNormalizeOutput(rendered), true
}

func (p *latexParser) at(offset int) rune {
	if p.position+offset < len(p.source) {
		return p.source[p.position+offset]
	}
	return 0
}

func (p *latexParser) sliceFrom(start int) string {
	if start < 0 || start > len(p.source) {
		return ""
	}
	return string(p.source[start:])
}

// sliceRange returns source[start:end] (end exclusive).
func (p *latexParser) sliceRange(start, end int) string {
	if start < 0 || end > len(p.source) || start > end {
		return ""
	}
	return string(p.source[start:end])
}

func (p *latexParser) parseSequence(endCharacter rune) string {
	var result strings.Builder
	for p.position < len(p.source) {
		character := p.source[p.position]
		if endCharacter != 0 && character == endCharacter {
			p.position++
			return result.String()
		}

		switch {
		case character == '}':
			p.supported = false
			return result.String()
		case character == '{':
			p.position++
			result.WriteString(p.parseSequence('}'))
		case character == '\\':
			command := p.parseCommand()
			if command == latexNegativeSpace {
				trimmed := strings.TrimRight(result.String(), " \t\n\v\f\r")
				result.Reset()
				result.WriteString(trimmed)
				if strings.HasSuffix(result.String(), latexNamedOperatorEnd) {
					result.Reset()
					result.WriteString(trimmed[:len(trimmed)-len(latexNamedOperatorEnd)])
				}
			} else {
				result.WriteString(command)
			}
		case character == '^' || character == '_':
			p.position++
			trimmed := strings.TrimRight(result.String(), " \t\n\v\f\r")
			result.Reset()
			result.WriteString(trimmed)
			script := p.parseScripts(character)
			current := result.String()
			if strings.HasSuffix(current, latexNamedOperatorEnd) {
				result.Reset()
				result.WriteString(current[:len(current)-len(latexNamedOperatorEnd)] + script + latexNamedOperatorEnd)
			} else {
				result.WriteString(script)
			}
		case unicode.IsSpace(character):
			result.WriteString(p.parseWhitespace())
		case character == '=' || character == '<' || character == '>':
			trimmed := strings.TrimRight(result.String(), " \t\n\v\f\r")
			result.Reset()
			result.WriteString(trimmed)
			result.WriteString(" " + string(character) + " ")
			p.position++
		case character == '&':
			p.position++
		case character == '~':
			p.position++
			result.WriteString(" ")
		case character == '.':
			nodeIndex, ok := latexTrailingLayoutMarker(result.String())
			if ok && nodeIndex < len(*p.nodes) && (*p.nodes)[nodeIndex].kind == latexMatrix {
				node := &(*p.nodes)[nodeIndex]
				lastLine := len(node.lines) - 1
				node.lines[lastLine] += string(character)
				p.position++
				continue
			}
			result.WriteRune(character)
			p.position++
		default:
			result.WriteRune(character)
			p.position++
		}
	}

	if endCharacter != 0 {
		p.supported = false
	}
	return result.String()
}

func (p *latexParser) parseScripts(initialMarker rune) string {
	var sub, sup string
	hasSub, hasSup := false, false
	var order []rune
	parse := func(marker rune) {
		kind := marker == '_'
		p.scriptDepth++
		value := p.parseRequiredArgument(false)
		p.scriptDepth--
		if kind {
			sub = value
			hasSub = true
		} else {
			sup = value
			hasSup = true
		}
		order = append(order, marker)
	}

	parse(initialMarker)
	nextPosition := p.position
	for nextPosition < len(p.source) && unicode.IsSpace(p.source[nextPosition]) {
		nextPosition++
	}
	var nextMarker rune
	if nextPosition < len(p.source) {
		nextMarker = p.source[nextPosition]
	}
	if (nextMarker == '^' || nextMarker == '_') && nextMarker != initialMarker {
		p.position = nextPosition + 1
		parse(nextMarker)
	}

	subUnicode, subOK := "", false
	if hasSub {
		subUnicode, subOK = latexFormatUnicodeScript(sub, true)
	}
	supUnicode, supOK := "", false
	if hasSup {
		supUnicode, supOK = latexFormatUnicodeScript(sup, false)
	}
	blocksLayout := func(value string, has bool, unicodeOK bool) bool {
		return has && (strings.Contains(value, "/") ||
			(!strings.Contains(value, latexLayoutMarkerStart) && len([]rune(value)) > 1 && !latexHasUppercaseOrStar(value)))
	}
	canUseLayout := !blocksLayout(sub, hasSub, subOK) && !blocksLayout(sup, hasSup, supOK)
	needsLayout := p.display && canUseLayout &&
		(p.scriptDepth > 0 ||
			(hasSub && !subOK) ||
			(hasSup && !supOK))
	if !needsLayout {
		var out strings.Builder
		for _, marker := range order {
			if marker == '_' {
				if subOK {
					out.WriteString(subUnicode)
				} else {
					out.WriteString(latexFormatScript(sub, true))
				}
			} else {
				if supOK {
					out.WriteString(supUnicode)
				} else {
					out.WriteString(latexFormatScript(sup, false))
				}
			}
		}
		return out.String()
	}

	node := latexLayoutNode{kind: latexScript}
	if hasSub {
		node.lower = latexNormalizeOutput(sub)
		node.hasLower = true
	}
	if hasSup {
		node.upper = latexNormalizeOutput(sup)
		node.hasUpper = true
	}
	*p.nodes = append(*p.nodes, node)
	index := len(*p.nodes) - 1
	return latexLayoutMarkerStart + itoa(index) + latexLayoutMarkerEnd
}

var latexUppercaseOrStar = regexp.MustCompile("[A-Z*∗]")

func latexHasUppercaseOrStar(value string) bool {
	return latexUppercaseOrStar.MatchString(value)
}

func (p *latexParser) parseWhitespace() string {
	for p.position < len(p.source) && unicode.IsSpace(p.source[p.position]) {
		p.position++
	}
	return " "
}

func (p *latexParser) parseCommand() string {
	p.position++
	if p.position >= len(p.source) {
		p.supported = false
		return ""
	}

	command := ""
	first := p.source[p.position]
	if first == '\n' || first == '\r' {
		p.position++
		if first == '\r' && p.position < len(p.source) && p.source[p.position] == '\n' {
			p.position++
		}
		return " "
	}
	if unicode.IsLetter(first) && first < 128 {
		start := p.position
		for p.position < len(p.source) && unicode.IsLetter(p.source[p.position]) && p.source[p.position] < 128 {
			p.position++
		}
		command = p.sliceRange(start, p.position)
	} else {
		command = string(first)
		p.position++
	}

	switch {
	case command == "\\":
		return "\n"
	case latexSpacingCommands[command]:
		return " "
	case latexNegativeSpacingCommands[command]:
		return latexNegativeSpace
	case latexFontSwitchCommands[command]:
		for p.position < len(p.source) && unicode.IsSpace(p.source[p.position]) {
			p.position++
		}
		return ""
	case latexIgnoredCommands[command]:
		return ""
	case command == "{" || command == "}" || command == "$" || command == "%" ||
		command == "#" || command == "_" || command == "&":
		return command
	case command == "|":
		return "‖"
	case command == "not":
		value := strings.TrimSpace(p.parseRequiredArgument(true))
		if negated, ok := latexNegatedSymbols[value]; ok {
			return " " + negated + " "
		}
		characters := []rune(value)
		if len(characters) == 0 {
			p.supported = false
			return ""
		}
		return " " + string(characters[0]) + "\u0338" + string(characters[1:]) + " "
	case latexLimitOperators[command]:
		return p.parseOperator(command, "bracket", true, true)
	}

	if symbol, ok := latexSymbols[command]; ok {
		if latexDisplayLimitSymbols[command] {
			return p.parseOperator(symbol, "script", true, false)
		}
		if command == "cdot" || command == "times" || latexRelationCommands[command] {
			return " " + symbol + " "
		}
		return symbol
	}
	if latexNamedOperators[command] {
		return latexNamedOperatorStart + command + latexNamedOperatorEnd
	}
	if latexSizeCommands[command] {
		return ""
	}
	if command == "left" || command == "middle" || command == "right" {
		if p.at(0) == '.' {
			p.position++
		}
		return ""
	}
	switch command {
	case "frac", "dfrac", "tfrac":
		shouldStack := p.display && p.stackFractions && command != "tfrac"
		numerator := p.parseRequiredArgument(!shouldStack)
		denominator := p.parseRequiredArgument(!shouldStack)
		if shouldStack {
			*p.nodes = append(*p.nodes, latexLayoutNode{
				kind:        latexFraction,
				numerator:   latexNormalizeOutput(numerator),
				denominator: latexNormalizeOutput(denominator),
			})
			index := len(*p.nodes) - 1
			return latexLayoutMarkerStart + itoa(index) + latexLayoutMarkerEnd
		}
		return latexFormatFraction(numerator, denominator)
	case "sqrt":
		degree := ""
		hasDegree := false
		if optional, ok := p.parseOptionalArgument(); ok {
			degree = strings.TrimSpace(optional)
			hasDegree = true
		}
		value := p.parseRequiredArgument(true)
		if !hasDegree || degree == "2" {
			return latexFormatRoot(value, "√")
		}
		if degree == "3" {
			return latexFormatRoot(value, "∛")
		}
		if degree == "4" {
			return latexFormatRoot(value, "∜")
		}
		return latexFormatScript(degree, false) + latexFormatRoot(value, "√")
	case "boxed", "fbox":
		return "[" + strings.TrimSpace(p.parseRequiredArgument(true)) + "]"
	case "binom", "dbinom", "tbinom":
		return "(" + p.parseRequiredArgument(true) + " choose " + p.parseRequiredArgument(true) + ")"
	}
	if accent, ok := latexAccents[command]; ok {
		value := p.parseRequiredArgument(true)
		if len([]rune(value)) == 1 {
			return value + accent
		}
		return command + "(" + value + ")"
	}
	if command == "mathbb" {
		value := p.parseRequiredArgument(true)
		var b strings.Builder
		for _, character := range value {
			if mapped, ok := latexBlackboard[string(character)]; ok {
				b.WriteString(mapped)
			} else {
				b.WriteRune(character)
			}
		}
		return b.String()
	}
	if command == "operatorname" {
		starred := p.at(0) == '*'
		if starred {
			p.position++
		}
		operator := strings.TrimSpace(latexNormalizeOutput(p.parseRequiredArgument(true)))
		return p.parseOperator(operator, "bracket", starred, true)
	}
	if command == "mod" || command == "bmod" {
		return " mod "
	}
	if command == "pmod" || command == "pod" {
		value := strings.TrimSpace(p.parseRequiredArgument(true))
		if command == "pmod" {
			return " (mod " + value + ")"
		}
		return " (" + value + ")"
	}
	if command == "overset" || command == "stackrel" {
		upper := p.parseRequiredArgument(true)
		value := strings.TrimSpace(p.parseRequiredArgument(true))
		return value + latexFormatScript(upper, false)
	}
	if command == "underset" {
		lower := p.parseRequiredArgument(true)
		value := strings.TrimSpace(p.parseRequiredArgument(true))
		return value + latexFormatScript(lower, true)
	}
	if latexPlainWrappers[command] {
		value := p.parseRequiredArgument(true)
		if strings.HasPrefix(command, "text") || command == "mbox" {
			return value
		}
		return strings.TrimSpace(value)
	}
	if command == "begin" {
		return p.parseEnvironment()
	}
	if command == "end" {
		p.supported = false
		return ""
	}

	p.supported = false
	return "\\" + command
}

func (p *latexParser) parseOperator(operator, inlineLowerStyle string, displayLimits, spaced bool) string {
	useDisplayLimits := displayLimits
	modifierPosition := p.position
	for modifierPosition < len(p.source) && (p.source[modifierPosition] == ' ' || p.source[modifierPosition] == '\t') {
		modifierPosition++
	}
	if modifier := latexLimitsModifier.FindStringSubmatch(p.sliceFrom(modifierPosition)); modifier != nil {
		// Go regexp lacks negative lookahead: reject when the command name
		// continues with a letter.
		rest := p.sliceFrom(modifierPosition)[len(modifier[0]):]
		if rest == "" || !isASCIILetterRune([]rune(rest)[0]) {
			useDisplayLimits = modifier[1] == "limits"
			p.position = modifierPosition + len(modifier[0])
		}
	}

	lower, upper := "", ""
	hasLower, hasUpper := false, false
	for {
		scriptPosition := p.position
		for scriptPosition < len(p.source) && (p.source[scriptPosition] == ' ' || p.source[scriptPosition] == '\t') {
			scriptPosition++
		}
		if scriptPosition >= len(p.source) {
			break
		}
		kind := p.source[scriptPosition]
		if kind != '_' && kind != '^' {
			break
		}
		p.position = scriptPosition + 1
		value := strings.ReplaceAll(latexNormalizeOutput(p.parseRequiredArgument(false)), " ", "")
		if kind == '_' {
			if hasLower {
				p.supported = false
			}
			lower = value
			hasLower = true
		} else {
			if hasUpper {
				p.supported = false
			}
			upper = value
			hasUpper = true
		}
	}

	if p.display && useDisplayLimits && (hasLower || hasUpper) {
		node := latexLayoutNode{kind: latexOperator, operator: operator}
		if hasLower {
			node.lower, node.hasLower = lower, true
		}
		if hasUpper {
			node.upper, node.hasUpper = upper, true
		}
		*p.nodes = append(*p.nodes, node)
		index := len(*p.nodes) - 1
		return latexLayoutMarkerStart + itoa(index) + latexLayoutMarkerEnd
	}

	rendered := operator
	if hasLower {
		if inlineLowerStyle == "bracket" {
			rendered += "[" + lower + "]"
		} else {
			rendered += latexFormatScript(lower, true)
		}
	}
	if hasUpper {
		rendered += latexFormatScript(upper, false)
	}
	if spaced {
		return " " + rendered + " "
	}
	return rendered
}

func (p *latexParser) parseRequiredArgument(stackFractions bool) string {
	previousStackFractions := p.stackFractions
	p.stackFractions = previousStackFractions && stackFractions
	value := p.parseRequiredArgumentValue()
	p.stackFractions = previousStackFractions
	return value
}

func (p *latexParser) parseRequiredArgumentValue() string {
	for p.position < len(p.source) && unicode.IsSpace(p.source[p.position]) {
		p.position++
	}
	if p.position >= len(p.source) {
		p.supported = false
		return ""
	}
	if p.source[p.position] == '{' {
		p.position++
		return p.parseSequence('}')
	}
	if p.source[p.position] == '\\' {
		return p.parseCommand()
	}
	value := string(p.source[p.position])
	p.position++
	return value
}

func (p *latexParser) parseOptionalArgument() (string, bool) {
	for p.position < len(p.source) && (p.source[p.position] == ' ' || p.source[p.position] == '\t') {
		p.position++
	}
	if p.position >= len(p.source) || p.source[p.position] != '[' {
		return "", false
	}
	source := p.sliceFrom(0)
	end := strings.Index(source[p.position+1:], "]")
	if end < 0 {
		p.supported = false
		return "", false
	}
	end += p.position + 1
	value := source[p.position+1 : end]
	p.position = end + 1
	return p.renderNested(value, true), true
}

func (p *latexParser) readRawGroup() (string, bool) {
	for p.position < len(p.source) && (p.source[p.position] == ' ' || p.source[p.position] == '\t') {
		p.position++
	}
	if p.position >= len(p.source) || p.source[p.position] != '{' {
		p.supported = false
		return "", false
	}

	p.position++
	start := p.position
	depth := 1
	for p.position < len(p.source) {
		character := p.source[p.position]
		if character == '\\' {
			p.position += 2
			continue
		}
		if character == '{' {
			depth++
		}
		if character == '}' {
			depth--
		}
		if depth == 0 {
			value := p.sliceRange(start, p.position)
			p.position++
			return value, true
		}
		p.position++
	}
	p.supported = false
	return "", false
}

func (p *latexParser) splitEnvironmentRows(body string) []string {
	return latexRowSplit.Split(body, -1)
}

func (p *latexParser) parseEnvironment() string {
	environment, ok := p.readRawGroup()
	if !ok {
		return ""
	}
	source := p.sliceFrom(0)
	endMarker := "\\end{" + environment + "}"
	end := strings.Index(source[p.position:], endMarker)
	if end < 0 {
		p.supported = false
		return ""
	}
	end += p.position
	body := source[p.position:end]
	p.position = end + len(endMarker)

	switch environment {
	case "equation", "equation*", "displaymath":
		return strings.TrimSpace(p.renderNested(body, true))
	}

	switch environment {
	case "aligned", "align", "align*", "alignedat", "alignat", "alignat*",
		"gather", "gathered", "multline", "multline*", "split":
		alignedAt := environment == "alignedat" || environment == "alignat" || environment == "alignat*"
		alignedBody := body
		if alignedAt {
			alignedBody = latexAlignatPrefix.ReplaceAllString(body, "")
		}
		var rows []string
		for _, row := range p.splitEnvironmentRows(alignedBody) {
			cells := strings.Split(row, "&")
			sourceRow := row
			if alignedAt {
				var pairs []string
				for index := 0; index < len(cells); index += 2 {
					end := index + 2
					if end > len(cells) {
						end = len(cells)
					}
					pairs = append(pairs, strings.Join(cells[index:end], ""))
				}
				sourceRow = strings.Join(pairs, " ")
			} else {
				sourceRow = strings.Join(cells, "")
			}
			rendered := strings.TrimSpace(p.renderNested(sourceRow, true))
			if rendered != "" {
				rows = append(rows, rendered)
			}
		}
		return strings.Join(rows, "\n")
	case "cases", "cases*":
		return p.renderCases(body)
	case "array", "matrix", "smallmatrix", "pmatrix", "bmatrix", "Bmatrix", "vmatrix", "Vmatrix":
		matrixBody := body
		if environment == "array" {
			matrixBody = latexAlignatPrefix.ReplaceAllString(body, "")
		}
		return p.renderMatrix(environment, matrixBody)
	}

	p.supported = false
	return body
}

func (p *latexParser) renderCases(body string) string {
	var rows [][]string
	for _, row := range p.splitEnvironmentRows(body) {
		var cells []string
		for _, cell := range strings.Split(row, "&") {
			cells = append(cells, strings.TrimSpace(p.renderNested(cell, false)))
		}
		hasContent := false
		for _, cell := range cells {
			if cell != "" {
				hasContent = true
				break
			}
		}
		if hasContent {
			rows = append(rows, cells)
		}
	}
	valueWidth := 0
	for _, row := range rows {
		value := strings.TrimSuffix(strings.TrimRight(row[0], " "), ",")
		if w := VisibleWidth(value); w > valueWidth {
			valueWidth = w
		}
	}
	var contents []string
	for _, row := range rows {
		value := strings.TrimSuffix(strings.TrimRight(row[0], " "), ",")
		condition := ""
		if len(row) > 1 {
			condition = row[1]
		}
		if condition == "" {
			contents = append(contents, value)
			continue
		}
		conditionPrefix := " if "
		if latexConditionPrefix.MatchString(condition) {
			conditionPrefix = " "
		}
		contents = append(contents, value+stringRepeat(latexProtectedSpace, valueWidth-VisibleWidth(value))+conditionPrefix+condition)
	}
	if len(contents) == 0 {
		return ""
	}
	if len(contents) == 1 {
		return "⎧ " + contents[0]
	}

	middle := len(contents) / 2
	visualRows := make([]*string, 0, len(contents)+1)
	for i := range contents {
		visualRows = append(visualRows, &contents[i])
	}
	if len(contents)%2 == 0 {
		visualRows = append(visualRows[:middle], append([]*string{nil}, visualRows[middle:]...)...)
	}
	var lines []string
	for index, content := range visualRows {
		delimiter := "⎨"
		if index == 0 {
			delimiter = "⎧"
		} else if index == len(visualRows)-1 {
			delimiter = "⎩"
		}
		if content == nil {
			lines = append(lines, delimiter)
		} else {
			lines = append(lines, delimiter+" "+*content)
		}
	}
	*p.nodes = append(*p.nodes, latexLayoutNode{kind: latexMatrix, lines: lines, baseline: middle})
	index := len(*p.nodes) - 1
	return latexLayoutMarkerStart + itoa(index) + latexLayoutMarkerEnd
}

func (p *latexParser) renderMatrix(environment, body string) string {
	var matrix [][]string
	for _, row := range p.splitEnvironmentRows(body) {
		var cells []string
		for _, cell := range strings.Split(row, "&") {
			cells = append(cells, strings.TrimSpace(p.renderNested(cell, false)))
		}
		hasContent := false
		for _, cell := range cells {
			if cell != "" {
				hasContent = true
				break
			}
		}
		if hasContent {
			matrix = append(matrix, cells)
		}
	}
	columnCount := 0
	for _, row := range matrix {
		if len(row) > columnCount {
			columnCount = len(row)
		}
	}
	columnWidths := make([]int, columnCount)
	for column := 0; column < columnCount; column++ {
		for _, row := range matrix {
			cell := ""
			if column < len(row) {
				cell = row[column]
			}
			if w := VisibleWidth(cell); w > columnWidths[column] {
				columnWidths[column] = w
			}
		}
	}
	var rows []string
	for _, row := range matrix {
		var cells []string
		for column := 0; column < columnCount; column++ {
			cell := ""
			if column < len(row) {
				cell = row[column]
			}
			cells = append(cells, cell+stringRepeat(latexProtectedSpace, maxInt(0, columnWidths[column]-VisibleWidth(cell))))
		}
		rows = append(rows, strings.Join(cells, " │ "))
	}

	var lines []string
	switch environment {
	case "array", "matrix", "smallmatrix":
		lines = rows
	case "pmatrix", "bmatrix", "Bmatrix", "vmatrix", "Vmatrix":
		delimiters := map[string][6]string{
			"pmatrix": {"⎛", "⎞", "⎜", "⎟", "⎝", "⎠"},
			"bmatrix": {"⎡", "⎤", "⎢", "⎥", "⎣", "⎦"},
			"Bmatrix": {"⎧", "⎫", "⎨", "⎬", "⎩", "⎭"},
			"vmatrix": {"│", "│", "│", "│", "│", "│"},
			"Vmatrix": {"║", "║", "║", "║", "║", "║"},
		}
		delimiter := delimiters[environment]
		for index, row := range rows {
			left := delimiter[2]
			right := delimiter[3]
			if index == 0 {
				left, right = delimiter[0], delimiter[1]
			} else if index == len(rows)-1 {
				left, right = delimiter[4], delimiter[5]
			}
			lines = append(lines, left+" "+row+" "+right)
		}
	default:
		p.supported = false
		return strings.Join(rows, "\n")
	}

	if len(lines) <= 1 {
		if len(lines) == 0 {
			return ""
		}
		return lines[0]
	}
	*p.nodes = append(*p.nodes, latexLayoutNode{kind: latexMatrix, lines: lines})
	index := len(*p.nodes) - 1
	return latexLayoutMarkerStart + itoa(index) + latexLayoutMarkerEnd
}

func (p *latexParser) renderNested(source string, stackFractions bool) string {
	nested := &latexParser{
		source:         []rune(source),
		nodes:          p.nodes,
		display:        p.display && stackFractions,
		supported:      true,
		stackFractions: true,
	}
	rendered, ok := nested.render()
	if !ok {
		p.supported = false
		return source
	}
	return rendered
}
