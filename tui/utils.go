package tui

import (
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// Port of pi/packages/tui/src/utils.ts. Indices into strings are rune offsets
// (the TS reference uses UTF-16 code-unit offsets; all callers are internal
// and consistent, so behavior is identical).

// couldBeEmoji is a fast heuristic to avoid the expensive RGI emoji test. The
// tested Unicode blocks are deliberately broad to account for future Unicode
// additions.
func couldBeEmoji(segment string) bool {
	runes := []rune(segment)
	cp := runes[0]
	return (cp >= 0x1f000 && cp <= 0x1fbff) || // Emoji and Pictograph
		(cp >= 0x2300 && cp <= 0x23ff) || // Misc technical
		(cp >= 0x2600 && cp <= 0x27bf) || // Misc symbols, dingbats
		(cp >= 0x2b50 && cp <= 0x2b55) || // Specific stars/circles
		strings.ContainsRune(segment, '\ufe0f') || // Contains VS16
		jsUtf16Length(runes) > 2 // Multi-codepoint sequences (ZWJ, skin tones, etc.)
}

func jsUtf16Length(runes []rune) int {
	n := 0
	for _, r := range runes {
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// Character classification (same as the string-width library).
func isMarkRune(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

// isZeroWidthCluster: every rune is Default_Ignorable, Control (Cc), Mark, or
// a surrogate (impossible in valid Go strings).
func isZeroWidthCluster(segment string) bool {
	if segment == "" {
		return false
	}
	for _, r := range segment {
		if !(isDefaultIgnorable(r) || unicode.Is(unicode.Cc, r) || isMarkRune(r)) {
			return false
		}
	}
	return true
}

// nonPrintingRune: Default_Ignorable, Control, Format (Cf), Mark, or surrogate.
func nonPrintingRune(r rune) bool {
	return isDefaultIgnorable(r) || unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || isMarkRune(r)
}

func stripLeadingNonPrinting(segment string) string {
	runes := []rune(segment)
	i := 0
	for i < len(runes) && nonPrintingRune(runes[i]) {
		i++
	}
	return string(runes[i:])
}

// terminalSpacingMarkRune matches marks that terminals allocate cells for when
// attached to a base character: Spacing_Mark (gc=Mc) minus U+1734, U+302E,
// U+302F, plus the legacy wcwidth exceptions.
func terminalSpacingMarkRune(r rune) bool {
	if r == 0x1734 || r == 0x302e || r == 0x302f {
		return false
	}
	if unicode.Is(unicode.Mc, r) {
		return true
	}
	switch {
	case r == 0x065f, r == 0x0f7f, r == 0x102b, r == 0x102c, r == 0x1031, r == 0x1038:
		return true
	case r >= 0x1033 && r <= 0x1035, r >= 0x103a && r <= 0x103e:
		return true
	}
	return false
}

func isTerminalSpacingMark(segment string) bool {
	if segment == "" {
		return false
	}
	for _, r := range segment {
		if !terminalSpacingMarkRune(r) {
			return false
		}
	}
	return true
}

const widthCacheSize = 512

var widthCacheMu sync.Mutex
var widthCache = map[string]int{}
var widthCacheOrder []string

func cacheWidth(s string, width int) {
	widthCacheMu.Lock()
	defer widthCacheMu.Unlock()
	if len(widthCacheOrder) >= widthCacheSize {
		oldest := widthCacheOrder[0]
		widthCacheOrder = widthCacheOrder[1:]
		delete(widthCache, oldest)
	}
	widthCache[s] = width
	widthCacheOrder = append(widthCacheOrder, s)
}

func cachedWidth(s string) (int, bool) {
	widthCacheMu.Lock()
	defer widthCacheMu.Unlock()
	width, ok := widthCache[s]
	return width, ok
}

// CjkBreakRune reports CJK ideographs and kana (script extensions of Han,
// Hiragana, Katakana, Hangul, Bopomofo): these remain part of words and paths.
func CjkBreakRune(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Bopomofo, r)
}

// cjkPunctuationExtra covers Script_Extensions-CJK punctuation the Go
// script tables place in Common (。、《》「」 and friends) — the reference's
// \\p{Punctuation} + CJK-break lookahead branch.
var cjkPunctuationExtra = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x3001, Hi: 0x3003, Stride: 1},
		{Lo: 0x3008, Hi: 0x3011, Stride: 1},
		{Lo: 0x3014, Hi: 0x301f, Stride: 1},
		{Lo: 0xff0c, Hi: 0xff0c, Stride: 1},
		{Lo: 0xff0e, Hi: 0xff0f, Stride: 1},
		{Lo: 0xff1a, Hi: 0xff1b, Stride: 1},
		{Lo: 0xff1f, Hi: 0xff20, Stride: 1},
		{Lo: 0xff3b, Hi: 0xff3d, Stride: 1},
		{Lo: 0xff3f, Hi: 0xff3f, Stride: 1},
		{Lo: 0xff5b, Hi: 0xff5d, Stride: 1},
		{Lo: 0xff5f, Hi: 0xff65, Stride: 1},
		{Lo: 0xffe3, Hi: 0xffe3, Stride: 1},
	},
}

// CjkPunctuationRune reports CJK letters' punctuation neighbors: only
// punctuation can separate prose from completions.
func CjkPunctuationRune(r rune) bool {
	if unicode.IsPunct(r) && CjkBreakRune(r) {
		return true
	}
	if unicode.Is(cjkPunctuationExtra, r) {
		return true
	}
	switch r {
	case '，', '．', '：', '；', '！', '？', '（', '）', '［', '］', '｛', '｝',
		'“', '”', '‘', '’', '…', '—':
		return true
	}
	return false
}

// AutocompleteSeparatorRune separates autocomplete tokens.
func AutocompleteSeparatorRune(r rune) bool {
	return unicode.IsSpace(r) || CjkPunctuationRune(r)
}

func isPrintableAscii(str string) bool {
	for i := 0; i < len(str); i++ {
		code := str[i]
		if code < 0x20 || code > 0x7e {
			return false
		}
	}
	return true
}

type truncatedFragment struct {
	text  string
	width int
}

func truncateFragmentToWidth(text string, maxWidth int) truncatedFragment {
	if maxWidth <= 0 || len(text) == 0 {
		return truncatedFragment{}
	}

	if isPrintableAscii(text) {
		runes := []rune(text)
		if maxWidth < len(runes) {
			runes = runes[:maxWidth]
		}
		return truncatedFragment{text: string(runes), width: len(runes)}
	}

	hasAnsi := strings.ContainsRune(text, '\x1b')
	hasTabs := strings.ContainsRune(text, '\t')
	if !hasAnsi && !hasTabs {
		var result []rune
		width := 0
		for _, segment := range segmentGraphemes(text) {
			w := graphemeWidth(segment)
			if width+w > maxWidth {
				break
			}
			result = append(result, []rune(segment)...)
			width += w
		}
		return truncatedFragment{text: string(result), width: width}
	}

	var result []rune
	width := 0
	runes := []rune(text)
	i := 0
	pendingAnsi := ""

	for i < len(runes) {
		if ansi, n := extractAnsiCodeAt(runes, i); n > 0 {
			pendingAnsi += ansi
			i += n
			continue
		}

		if runes[i] == '\t' {
			if width+3 > maxWidth {
				break
			}
			if pendingAnsi != "" {
				result = append(result, []rune(pendingAnsi)...)
				pendingAnsi = ""
			}
			result = append(result, '\t')
			width += 3
			i++
			continue
		}

		end := i
		for end < len(runes) && runes[end] != '\t' {
			if _, n := extractAnsiCodeAt(runes, end); n > 0 {
				break
			}
			end++
		}

		for _, segment := range segmentGraphemes(string(runes[i:end])) {
			w := graphemeWidth(segment)
			if width+w > maxWidth {
				return truncatedFragment{text: string(result), width: width}
			}
			if pendingAnsi != "" {
				result = append(result, []rune(pendingAnsi)...)
				pendingAnsi = ""
			}
			result = append(result, []rune(segment)...)
			width += w
		}
		i = end
	}

	return truncatedFragment{text: string(result), width: width}
}

func finalizeTruncatedResult(prefix string, prefixWidth int, ellipsis string, ellipsisWidth, maxWidth int, pad bool) string {
	reset := "\x1b[0m"
	hyperlinkClose := getActiveOsc8Close(prefix)
	visibleWidth := prefixWidth + ellipsisWidth
	var result string

	if len(ellipsis) > 0 {
		result = prefix + hyperlinkClose + reset + ellipsis + reset
	} else {
		result = prefix + hyperlinkClose + reset
	}

	if pad {
		space := maxWidth - visibleWidth
		if space < 0 {
			space = 0
		}
		result += strings.Repeat(" ", space)
	}
	return result
}

// graphemeWidth calculates the terminal width of a single grapheme cluster,
// based on the string-width library, with a pre-filter to avoid the RGI emoji
// test where it cannot match.
func graphemeWidth(segment string) int {
	if segment == "\t" {
		return 3
	}

	// Some marks occupy cells even without a base character.
	if isTerminalSpacingMark(segment) {
		return len([]rune(segment))
	}

	// Zero-width clusters
	if isZeroWidthCluster(segment) {
		return 0
	}

	// Emoji check with pre-filter
	if couldBeEmoji(segment) && isRGIEmoji(segment) {
		return 2
	}

	// Get base visible codepoint
	base := []rune(stripLeadingNonPrinting(segment))
	if len(base) == 0 {
		return 0
	}
	cp := base[0]

	// Regional indicator symbols (U+1F1E6..U+1F1FF) are often rendered as
	// full-width emoji in terminals, even when isolated during streaming.
	// Keep width conservative (2) to avoid terminal auto-wrap drift artifacts.
	if cp >= 0x1f1e6 && cp <= 0x1f1ff {
		return 2
	}

	width := eastAsianWidth(cp)

	// A grapheme can group multiple terminal-spacing code points. Count
	// trailing visible code points that terminals may allocate cells for:
	// Indic consonants after marks, halfwidth/fullwidth forms, and Thai/Lao
	// AM vowels.
	followsMark := false
	for _, char := range base[1:] {
		switch {
		case terminalSpacingMarkRune(char):
			width++
			followsMark = false
		case isMarkRune(char):
			followsMark = true
		case !nonPrintingRune(char):
			if followsMark || (char >= 0xff00 && char <= 0xffef) {
				// halfwidth + fullwidth forms
				width += eastAsianWidth(char)
			} else if char == 0x0e33 || char == 0x0eb3 {
				width++
			}
			followsMark = false
		}
	}

	return width
}

// eastAsianWidth mirrors npm get-east-asian-width 1.6.0: 2 for Fullwidth and
// Wide code points, 1 otherwise.
func eastAsianWidth(codePoint rune) int {
	if isFullWidth(codePoint) || isWideWidth(codePoint) {
		return 2
	}
	return 1
}

// VisibleWidth calculates the visible width of a string in terminal columns,
// counting tabs as 3 and stripping ANSI/OSC/APC escape sequences.
func VisibleWidth(str string) int {
	if len(str) == 0 {
		return 0
	}

	// Fast path: pure ASCII printable
	if isPrintableAscii(str) {
		return len(str)
	}

	if cached, ok := cachedWidth(str); ok {
		return cached
	}

	// Normalize: tabs to 3 spaces, strip ANSI escape codes
	clean := str
	if strings.ContainsRune(clean, '\t') {
		clean = strings.ReplaceAll(clean, "\t", "   ")
	}
	if strings.ContainsRune(clean, '\x1b') {
		runes := []rune(clean)
		var stripped []rune
		i := 0
		for i < len(runes) {
			if _, n := extractAnsiCodeAt(runes, i); n > 0 {
				i += n
				continue
			}
			stripped = append(stripped, runes[i])
			i++
		}
		clean = string(stripped)
	}

	width := 0
	for _, segment := range segmentGraphemes(clean) {
		width += graphemeWidth(segment)
	}

	cacheWidth(str, width)
	return width
}

// StripTerminalSequences removes ANSI, OSC, and APC control sequences while
// preserving visible text.
func StripTerminalSequences(str string) string {
	if !strings.ContainsRune(str, '\x1b') {
		return str
	}
	runes := []rune(str)
	var result []rune
	i := 0
	for i < len(runes) {
		if _, n := extractAnsiCodeAt(runes, i); n > 0 {
			i += n
			continue
		}
		result = append(result, runes[i])
		i++
	}
	return string(result)
}

// GraphemeCellRange is the terminal-cell range occupied by a grapheme at a
// visible column.
type GraphemeCellRange struct {
	Start int
	End   int
}

// GetGraphemeCellRange returns the cell range of the grapheme covering a
// visible column, or nil when the column has no covering grapheme.
func GetGraphemeCellRange(line string, column int) *GraphemeCellRange {
	runes := []rune(line)
	currentCol := 0
	i := 0
	for i < len(runes) {
		if _, n := extractAnsiCodeAt(runes, i); n > 0 {
			i += n
			continue
		}
		textEnd := i
		for textEnd < len(runes) {
			if _, n := extractAnsiCodeAt(runes, textEnd); n > 0 {
				break
			}
			textEnd++
		}
		for _, segment := range segmentGraphemes(string(runes[i:textEnd])) {
			width := graphemeWidth(segment)
			if width > 0 && column >= currentCol && column < currentCol+width {
				return &GraphemeCellRange{Start: currentCol, End: currentCol + width}
			}
			currentCol += width
		}
		i = textEnd
	}
	return nil
}

var osc8HyperlinkPattern = regexp.MustCompile(`^\x1b\]8;[^;]*;([^\x07\x1b]*)(?:\x07|\x1b\\)$`)

// GetOsc8LinkAtColumn returns the OSC 8 hyperlink covering a visible terminal
// column, or "".
func GetOsc8LinkAtColumn(line string, column int) string {
	activeUrl := ""
	runes := []rune(line)
	currentCol := 0
	i := 0
	for i < len(runes) {
		if code, n := extractAnsiCodeAt(runes, i); n > 0 {
			if hyperlink := osc8HyperlinkPattern.FindStringSubmatch(code); hyperlink != nil {
				activeUrl = hyperlink[1]
			}
			i += n
			continue
		}
		textEnd := i
		for textEnd < len(runes) {
			if _, n := extractAnsiCodeAt(runes, textEnd); n > 0 {
				break
			}
			textEnd++
		}
		for _, segment := range segmentGraphemes(string(runes[i:textEnd])) {
			width := 3
			if segment != "\t" {
				width = graphemeWidth(segment)
			}
			if column >= currentCol && column < currentCol+width {
				return activeUrl
			}
			currentCol += width
		}
		i = textEnd
	}
	return ""
}

// NormalizeTerminalOutput normalizes text for terminal output without
// changing logical editor content: precomposed Thai/Lao AM vowels decompose
// (same cell width, no stale-cell artifacts), visible tabs expand to the
// fixed layout width, while tabs inside escape sequences stay untouched.
func NormalizeTerminalOutput(str string) string {
	normalized := str
	if strings.ContainsAny(normalized, "\u0e33\u0eb3") {
		normalized = strings.ReplaceAll(normalized, "\u0e33", "\u0e4d\u0e32")
		normalized = strings.ReplaceAll(normalized, "\u0eb3", "\u0ecd\u0eb2")
	}
	if !strings.ContainsRune(normalized, '\t') {
		return normalized
	}

	runes := []rune(normalized)
	var result []rune
	i := 0
	for i < len(runes) {
		if code, n := extractAnsiCodeAt(runes, i); n > 0 {
			result = append(result, []rune(code)...)
			i += n
			continue
		}
		if runes[i] == '\t' {
			result = append(result, ' ', ' ', ' ')
		} else {
			result = append(result, runes[i])
		}
		i++
	}
	return string(result)
}

// extractAnsiCodeAt extracts an ANSI/OSC/APC escape sequence at rune offset
// pos; returns the code and its rune length, or n==0 when none starts there.
func extractAnsiCodeAt(runes []rune, pos int) (string, int) {
	if pos >= len(runes) || runes[pos] != '\x1b' {
		return "", 0
	}
	if pos+1 >= len(runes) {
		return "", 0
	}
	next := runes[pos+1]

	// CSI sequence: ESC [ ... m/G/K/H/J
	if next == '[' {
		j := pos + 2
		for j < len(runes) && !isCsiTerminator(runes[j]) {
			j++
		}
		if j < len(runes) {
			return string(runes[pos : j+1]), j + 1 - pos
		}
		return "", 0
	}

	// OSC sequence: ESC ] ... BEL or ESC ] ... ST (ESC \)
	if next == ']' {
		j := pos + 2
		for j < len(runes) {
			if runes[j] == '\x07' {
				return string(runes[pos : j+1]), j + 1 - pos
			}
			if runes[j] == '\x1b' && j+1 < len(runes) && runes[j+1] == '\\' {
				return string(runes[pos : j+2]), j + 2 - pos
			}
			j++
		}
		return "", 0
	}

	// APC sequence: ESC _ ... BEL or ESC _ ... ST (ESC \)
	if next == '_' {
		j := pos + 2
		for j < len(runes) {
			if runes[j] == '\x07' {
				return string(runes[pos : j+1]), j + 1 - pos
			}
			if runes[j] == '\x1b' && j+1 < len(runes) && runes[j+1] == '\\' {
				return string(runes[pos : j+2]), j + 2 - pos
			}
			j++
		}
		return "", 0
	}

	return "", 0
}

// ExtractAnsiCode extracts ANSI escape sequences from a string at the given
// rune position; returns (code, length) with length 0 when none starts there.
func ExtractAnsiCode(str string, pos int) (string, int) {
	return extractAnsiCodeAt([]rune(str), pos)
}

func isCsiTerminator(r rune) bool {
	return r == 'm' || r == 'G' || r == 'K' || r == 'H' || r == 'J'
}

type osc8Terminator string

const (
	osc8Bel osc8Terminator = "\x07"
	osc8St  osc8Terminator = "\x1b\\"
)

type activeHyperlink struct {
	params     string
	url        string
	terminator osc8Terminator
}

// parseOsc8Hyperlink returns (&hyperlink, true) for an OSC 8 open code,
// (nil, true) for an OSC 8 close, and (nil, false) for other codes.
func parseOsc8Hyperlink(ansiCode string) (*activeHyperlink, bool) {
	if !strings.HasPrefix(ansiCode, "\x1b]8;") {
		return nil, false
	}

	terminator := osc8St
	if strings.HasSuffix(ansiCode, "\x07") {
		terminator = osc8Bel
	}
	body := ansiCode[len("\x1b]8;"):]
	if term := string(terminator); term == "\x07" {
		body = body[:len(body)-1]
	} else {
		body = body[:len(body)-2]
	}
	separatorIndex := strings.IndexRune(body, ';')
	if separatorIndex == -1 {
		return nil, false
	}

	params := body[:separatorIndex]
	url := body[separatorIndex+1:]
	if url == "" {
		return nil, true
	}
	return &activeHyperlink{params: params, url: url, terminator: terminator}, true
}

func formatOsc8Hyperlink(hyperlink *activeHyperlink) string {
	return "\x1b]8;" + hyperlink.params + ";" + hyperlink.url + string(hyperlink.terminator)
}

func formatOsc8Close(terminator osc8Terminator) string {
	return "\x1b]8;;" + string(terminator)
}

func getActiveOsc8Close(prefix string) string {
	if !strings.Contains(prefix, "\x1b]8;") {
		return ""
	}

	var active *activeHyperlink
	runes := []rune(prefix)
	i := 0
	for i < len(runes) {
		if code, n := extractAnsiCodeAt(runes, i); n > 0 {
			if hyperlink, isOsc8 := parseOsc8Hyperlink(code); isOsc8 {
				active = hyperlink
			}
			i += n
		} else {
			i++
		}
	}
	if active != nil {
		return formatOsc8Close(active.terminator)
	}
	return ""
}

// ansiCodeTracker tracks active ANSI SGR codes to preserve styling across
// line breaks.
type ansiCodeTracker struct {
	bold          bool
	dim           bool
	italic        bool
	underline     bool
	blink         bool
	inverse       bool
	hidden        bool
	strikethrough bool
	fgColor       string // full code like "31" or "38;5;240"
	bgColor       string
	activeHyper   *activeHyperlink
}

func (t *ansiCodeTracker) process(ansiCode string) {
	// OSC 8 hyperlink open/close. Preserve the original terminator because
	// some terminals only make BEL-terminated links clickable; reopening
	// wrapped lines with ST made only the first physical line clickable there.
	if hyperlink, isOsc8 := parseOsc8Hyperlink(ansiCode); isOsc8 {
		t.activeHyper = hyperlink
		return
	}

	if !strings.HasSuffix(ansiCode, "m") {
		return
	}

	match := sgrParamsPattern.FindStringSubmatch(ansiCode)
	if match == nil {
		return
	}

	params := match[1]
	if params == "" || params == "0" {
		t.reset()
		return
	}

	parts := strings.Split(params, ";")
	i := 0
	for i < len(parts) {
		code := parseIntRadix10(parts[i])

		// Handle 256-color and RGB codes which consume multiple parameters
		if code == 38 || code == 48 {
			if i+1 < len(parts) && parts[i+1] == "5" && i+2 < len(parts) {
				colorCode := parts[i] + ";" + parts[i+1] + ";" + parts[i+2]
				if code == 38 {
					t.fgColor = colorCode
				} else {
					t.bgColor = colorCode
				}
				i += 3
				continue
			}
			if i+1 < len(parts) && parts[i+1] == "2" && i+4 < len(parts) {
				colorCode := parts[i] + ";" + parts[i+1] + ";" + parts[i+2] + ";" + parts[i+3] + ";" + parts[i+4]
				if code == 38 {
					t.fgColor = colorCode
				} else {
					t.bgColor = colorCode
				}
				i += 5
				continue
			}
		}

		switch {
		case code == 0:
			t.reset()
		case code == 1:
			t.bold = true
		case code == 2:
			t.dim = true
		case code == 3:
			t.italic = true
		case code == 4:
			t.underline = true
		case code == 5:
			t.blink = true
		case code == 7:
			t.inverse = true
		case code == 8:
			t.hidden = true
		case code == 9:
			t.strikethrough = true
		case code == 21: // Some terminals
			t.bold = false
		case code == 22:
			t.bold = false
			t.dim = false
		case code == 23:
			t.italic = false
		case code == 24:
			t.underline = false
		case code == 25:
			t.blink = false
		case code == 27:
			t.inverse = false
		case code == 28:
			t.hidden = false
		case code == 29:
			t.strikethrough = false
		case code == 39:
			t.fgColor = ""
		case code == 49:
			t.bgColor = ""
		default:
			// Standard foreground colors 30-37, 90-97
			if (code >= 30 && code <= 37) || (code >= 90 && code <= 97) {
				t.fgColor = formatJSNumber(float64(code))
			} else if (code >= 40 && code <= 47) || (code >= 100 && code <= 107) {
				t.bgColor = formatJSNumber(float64(code))
			}
		}
		i++
	}
}

func parseIntRadix10(s string) int {
	value := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return value // JS parseInt stops at the first non-digit
		}
		value = value*10 + int(c-'0')
	}
	return value
}

var sgrParamsPattern = regexp.MustCompile(`\x1b\[([\d;]*)m`)

func (t *ansiCodeTracker) reset() {
	t.bold = false
	t.dim = false
	t.italic = false
	t.underline = false
	t.blink = false
	t.inverse = false
	t.hidden = false
	t.strikethrough = false
	t.fgColor = ""
	t.bgColor = ""
	// SGR reset does not affect OSC 8 hyperlink state
}

// clear resets all state for reuse.
func (t *ansiCodeTracker) clear() {
	t.reset()
	t.activeHyper = nil
}

func (t *ansiCodeTracker) getActiveCodes() string {
	var codes []string
	if t.bold {
		codes = append(codes, "1")
	}
	if t.dim {
		codes = append(codes, "2")
	}
	if t.italic {
		codes = append(codes, "3")
	}
	if t.underline {
		codes = append(codes, "4")
	}
	if t.blink {
		codes = append(codes, "5")
	}
	if t.inverse {
		codes = append(codes, "7")
	}
	if t.hidden {
		codes = append(codes, "8")
	}
	if t.strikethrough {
		codes = append(codes, "9")
	}
	if t.fgColor != "" {
		codes = append(codes, t.fgColor)
	}
	if t.bgColor != "" {
		codes = append(codes, t.bgColor)
	}

	result := ""
	if len(codes) > 0 {
		result = "\x1b[" + strings.Join(codes, ";") + "m"
	}
	if t.activeHyper != nil {
		result += formatOsc8Hyperlink(t.activeHyper)
	}
	return result
}

func (t *ansiCodeTracker) getActiveBackgroundCode() string {
	if t.bgColor != "" {
		return "\x1b[" + t.bgColor + "m"
	}
	return ""
}

func (t *ansiCodeTracker) hasActiveCodes() bool {
	return t.bold || t.dim || t.italic || t.underline || t.blink ||
		t.inverse || t.hidden || t.strikethrough ||
		t.fgColor != "" || t.bgColor != "" || t.activeHyper != nil
}

// getLineEndReset returns reset codes for attributes that must be turned off
// at line end: underline must be closed to prevent bleeding into padding, and
// active OSC 8 hyperlinks must be closed (they re-open on the next line via
// getActiveCodes).
func (t *ansiCodeTracker) getLineEndReset() string {
	result := ""
	if t.underline {
		result += "\x1b[24m" // Underline off only
	}
	if t.activeHyper != nil {
		result += formatOsc8Close(t.activeHyper.terminator)
	}
	return result
}

func updateTrackerFromText(text string, tracker *ansiCodeTracker) {
	runes := []rune(text)
	i := 0
	for i < len(runes) {
		if code, n := extractAnsiCodeAt(runes, i); n > 0 {
			tracker.process(code)
			i += n
		} else {
			i++
		}
	}
}

// GetActiveBackgroundAnsi returns only the background color active at the end
// of an ANSI-styled string.
func GetActiveBackgroundAnsi(text string) string {
	var tracker ansiCodeTracker
	updateTrackerFromText(text, &tracker)
	return tracker.getActiveBackgroundCode()
}

// splitIntoTokensWithAnsi splits text into words while keeping ANSI codes
// attached.
func splitIntoTokensWithAnsi(text string) []string {
	var tokens []string
	current := ""
	pendingAnsi := "" // ANSI codes waiting to be attached to next visible content
	currentKind := "" // "" | "space" | "word"
	runes := []rune(text)
	i := 0

	flushCurrent := func() {
		if current == "" {
			return
		}
		tokens = append(tokens, current)
		current = ""
		currentKind = ""
	}

	for i < len(runes) {
		if code, n := extractAnsiCodeAt(runes, i); n > 0 {
			pendingAnsi += code
			i += n
			continue
		}

		end := i
		for end < len(runes) {
			if _, n := extractAnsiCodeAt(runes, end); n > 0 {
				break
			}
			end++
		}

		for _, segment := range segmentGraphemes(string(runes[i:end])) {
			segmentRunes := []rune(segment)
			segmentIsSpace := segment == " "
			if !segmentIsSpace && len(segmentRunes) == 1 && CjkBreakRune(segmentRunes[0]) {
				flushCurrent()
				tokens = append(tokens, pendingAnsi+segment)
				pendingAnsi = ""
				continue
			}

			segmentKind := "word"
			if segmentIsSpace {
				segmentKind = "space"
			}
			if current != "" && currentKind != segmentKind {
				flushCurrent()
			}

			if pendingAnsi != "" {
				current += pendingAnsi
				pendingAnsi = ""
			}

			currentKind = segmentKind
			current += segment
		}

		i = end
	}

	// Handle any remaining pending ANSI codes (attach to last token)
	if pendingAnsi != "" {
		if current != "" {
			current += pendingAnsi
		} else if len(tokens) > 0 {
			tokens[len(tokens)-1] += pendingAnsi
		} else {
			current = pendingAnsi
		}
	}

	if current != "" {
		tokens = append(tokens, current)
	}

	return tokens
}

// trimEnd mirrors JS String.prototype.trimEnd (Unicode whitespace).
func trimEnd(s string) string {
	return strings.TrimRightFunc(s, unicode.IsSpace)
}

// WrapTextWithAnsi word-wraps text with ANSI codes preserved: no padding, no
// background colors; each line is <= width visible columns; active ANSI codes
// carry across line breaks.
func WrapTextWithAnsi(text string, width int) []string {
	if text == "" {
		return []string{""}
	}

	// Handle newlines by processing each line separately, tracking ANSI state
	// across lines so styles carry over after literal newlines.
	inputLines := splitKeepEmpty(text)
	var result []string
	var tracker ansiCodeTracker

	for _, inputLine := range inputLines {
		// Prepend active ANSI codes from previous lines (except for first line)
		prefix := ""
		if len(result) > 0 {
			prefix = tracker.getActiveCodes()
		}
		result = append(result, wrapSingleLine(prefix+inputLine, width)...)
		updateTrackerFromText(inputLine, &tracker)
	}

	if len(result) == 0 {
		return []string{""}
	}
	return result
}

// splitKeepEmpty mirrors JS text.split(/\r\n|\r|\n/): separators don't produce
// empty artifacts beyond their literal splits.
func splitKeepEmpty(text string) []string {
	return regexp.MustCompile(`\r\n|\r|\n`).Split(text, -1)
}

func wrapSingleLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}

	visibleLength := VisibleWidth(line)
	if visibleLength <= width {
		return []string{line}
	}

	var wrapped []string
	var tracker ansiCodeTracker
	tokens := splitIntoTokensWithAnsi(line)

	currentLine := ""
	currentVisibleLength := 0

	for _, token := range tokens {
		tokenVisibleLength := VisibleWidth(token)
		isWhitespace := strings.TrimSpace(token) == ""

		// Token itself is too long - break it character by character
		if tokenVisibleLength > width && !isWhitespace {
			if currentLine != "" {
				lineEndReset := tracker.getLineEndReset()
				currentLine += lineEndReset
				wrapped = append(wrapped, currentLine)
				currentLine = ""
				currentVisibleLength = 0
			}

			// breakLongWord handles its own resets
			broken := breakLongWord(token, width, &tracker)
			for i := 0; i < len(broken)-1; i++ {
				wrapped = append(wrapped, broken[i])
			}
			currentLine = broken[len(broken)-1]
			currentVisibleLength = VisibleWidth(currentLine)
			continue
		}

		// Check if adding this token would exceed width
		totalNeeded := currentVisibleLength + tokenVisibleLength

		if totalNeeded > width && currentVisibleLength > 0 {
			// Trim trailing whitespace, then add underline reset (not full
			// reset, to preserve background)
			lineToWrap := trimEnd(currentLine)
			lineEndReset := tracker.getLineEndReset()
			lineToWrap += lineEndReset
			wrapped = append(wrapped, lineToWrap)
			if isWhitespace {
				// Don't start new line with whitespace
				currentLine = tracker.getActiveCodes()
				currentVisibleLength = 0
			} else {
				currentLine = tracker.getActiveCodes() + token
				currentVisibleLength = tokenVisibleLength
			}
		} else {
			currentLine += token
			currentVisibleLength += tokenVisibleLength
		}

		updateTrackerFromText(token, &tracker)
	}

	if currentLine != "" {
		// No reset at end of final line - let caller handle it
		wrapped = append(wrapped, currentLine)
	}

	// Trailing whitespace can cause lines to exceed the requested width
	if len(wrapped) == 0 {
		return []string{""}
	}
	for i := range wrapped {
		wrapped[i] = trimEnd(wrapped[i])
	}
	return wrapped
}

// IsPunctuationRune reports ASCII operator/pairing punctuation.
func IsPunctuationRune(r rune) bool {
	switch r {
	case '(', ')', '{', '}', '[', ']', '<', '>', '.', ',', ';', ':', '\'', '"',
		'!', '?', '+', '-', '=', '*', '/', '\\', '|', '&', '%', '^', '$', '#',
		'@', '~', '`':
		return true
	}
	return false
}

// IsWhitespaceRune reports JS /\s/ whitespace.
func IsWhitespaceRune(r rune) bool {
	return unicode.IsSpace(r)
}

func breakLongWord(word string, width int, tracker *ansiCodeTracker) []string {
	var lines []string
	currentLine := tracker.getActiveCodes()
	currentWidth := 0

	// Separate ANSI codes from visible content (they are not graphemes).
	runes := []rune(word)
	i := 0
	type segmentPart struct {
		isAnsi bool
		value  string
	}
	var segments []segmentPart

	for i < len(runes) {
		if code, n := extractAnsiCodeAt(runes, i); n > 0 {
			segments = append(segments, segmentPart{isAnsi: true, value: code})
			i += n
		} else {
			end := i
			for end < len(runes) {
				if _, n := extractAnsiCodeAt(runes, end); n > 0 {
					break
				}
				end++
			}
			for _, seg := range segmentGraphemes(string(runes[i:end])) {
				segments = append(segments, segmentPart{value: seg})
			}
			i = end
		}
	}

	for _, seg := range segments {
		if seg.isAnsi {
			currentLine += seg.value
			tracker.process(seg.value)
			continue
		}

		grapheme := seg.value
		if grapheme == "" {
			continue
		}

		gWidth := VisibleWidth(grapheme)

		if currentWidth+gWidth > width {
			lineEndReset := tracker.getLineEndReset()
			currentLine += lineEndReset
			lines = append(lines, currentLine)
			currentLine = tracker.getActiveCodes()
			currentWidth = 0
		}

		currentLine += grapheme
		currentWidth += gWidth
	}

	if currentLine != "" {
		// No reset at end of final segment - caller handles continuation
		lines = append(lines, currentLine)
	}

	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// ApplyBackgroundToLine applies a background color to a line, padding to full
// width.
func ApplyBackgroundToLine(line string, width int, bgFn func(string) string) string {
	visibleLen := VisibleWidth(line)
	paddingNeeded := width - visibleLen
	if paddingNeeded < 0 {
		paddingNeeded = 0
	}
	withPadding := line + strings.Repeat(" ", paddingNeeded)
	return bgFn(withPadding)
}

// TruncateToWidth truncates text to a maximum visible width, adding ellipsis
// if needed, optionally padding with spaces to exactly maxWidth. ANSI escape
// codes do not count toward width.
func TruncateToWidth(text string, maxWidth int, ellipsis string, pad bool) string {
	if maxWidth <= 0 {
		return ""
	}

	if len(text) == 0 {
		if pad {
			return strings.Repeat(" ", maxWidth)
		}
		return ""
	}

	ellipsisWidth := VisibleWidth(ellipsis)
	if ellipsisWidth >= maxWidth {
		textWidth := VisibleWidth(text)
		if textWidth <= maxWidth {
			if pad {
				return text + strings.Repeat(" ", maxWidth-textWidth)
			}
			return text
		}

		clippedEllipsis := truncateFragmentToWidth(ellipsis, maxWidth)
		if clippedEllipsis.width == 0 {
			if pad {
				return strings.Repeat(" ", maxWidth)
			}
			return ""
		}
		return finalizeTruncatedResult("", 0, clippedEllipsis.text, clippedEllipsis.width, maxWidth, pad)
	}

	if isPrintableAscii(text) {
		if len(text) <= maxWidth {
			if pad {
				return text + strings.Repeat(" ", maxWidth-len(text))
			}
			return text
		}
		targetWidth := maxWidth - ellipsisWidth
		return finalizeTruncatedResult(text[:targetWidth], targetWidth, ellipsis, ellipsisWidth, maxWidth, pad)
	}

	targetWidth := maxWidth - ellipsisWidth
	var result []rune
	pendingAnsi := ""
	visibleSoFar := 0
	keptWidth := 0
	keepContiguousPrefix := true
	overflowed := false
	exhaustedInput := false
	hasAnsi := strings.ContainsRune(text, '\x1b')
	hasTabs := strings.ContainsRune(text, '\t')

	runes := []rune(text)
	if !hasAnsi && !hasTabs {
		for _, segment := range segmentGraphemes(text) {
			width := graphemeWidth(segment)
			if keepContiguousPrefix && keptWidth+width <= targetWidth {
				result = append(result, []rune(segment)...)
				keptWidth += width
			} else {
				keepContiguousPrefix = false
			}
			visibleSoFar += width
			if visibleSoFar > maxWidth {
				overflowed = true
				break
			}
		}
		exhaustedInput = !overflowed
	} else {
		i := 0
		for i < len(runes) {
			if code, n := extractAnsiCodeAt(runes, i); n > 0 {
				pendingAnsi += code
				i += n
				continue
			}

			if runes[i] == '\t' {
				if keepContiguousPrefix && keptWidth+3 <= targetWidth {
					if pendingAnsi != "" {
						result = append(result, []rune(pendingAnsi)...)
						pendingAnsi = ""
					}
					result = append(result, '\t')
					keptWidth += 3
				} else {
					keepContiguousPrefix = false
					pendingAnsi = ""
				}
				visibleSoFar += 3
				if visibleSoFar > maxWidth {
					overflowed = true
					break
				}
				i++
				continue
			}

			end := i
			for end < len(runes) && runes[end] != '\t' {
				if _, n := extractAnsiCodeAt(runes, end); n > 0 {
					break
				}
				end++
			}

			for _, segment := range segmentGraphemes(string(runes[i:end])) {
				width := graphemeWidth(segment)
				if keepContiguousPrefix && keptWidth+width <= targetWidth {
					if pendingAnsi != "" {
						result = append(result, []rune(pendingAnsi)...)
						pendingAnsi = ""
					}
					result = append(result, []rune(segment)...)
					keptWidth += width
				} else {
					keepContiguousPrefix = false
					pendingAnsi = ""
				}

				visibleSoFar += width
				if visibleSoFar > maxWidth {
					overflowed = true
					break
				}
			}
			if overflowed {
				break
			}
			i = end
		}
		exhaustedInput = i >= len(runes)
	}

	if !overflowed && exhaustedInput {
		if pad {
			pad_ := maxWidth - visibleSoFar
			if pad_ < 0 {
				pad_ = 0
			}
			return text + strings.Repeat(" ", pad_)
		}
		return text
	}

	return finalizeTruncatedResult(string(result), keptWidth, ellipsis, ellipsisWidth, maxWidth, pad)
}

// SliceByColumn extracts a range of visible columns from a line, handling
// ANSI codes and wide chars. With strict=true, wide chars at the boundary
// that would extend past the range are excluded.
func SliceByColumn(line string, startCol, length int, strict bool) string {
	return SliceWithWidth(line, startCol, length, strict).text
}

// SliceWithWidthResult is SliceByColumn's result plus the actual visible
// width of the extracted text.
type SliceWithWidthResult struct {
	text  string
	width int
}

// SliceWithWidth is like SliceByColumn but also returns the actual visible
// width of the result.
func SliceWithWidth(line string, startCol, length int, strict bool) SliceWithWidthResult {
	if length <= 0 {
		return SliceWithWidthResult{}
	}
	endCol := startCol + length
	result := ""
	resultWidth := 0
	currentCol := 0
	pendingAnsi := ""
	runes := []rune(line)
	i := 0

	for i < len(runes) {
		if code, n := extractAnsiCodeAt(runes, i); n > 0 {
			if currentCol >= startCol && currentCol < endCol {
				result += code
			} else if currentCol < startCol {
				pendingAnsi += code
			}
			i += n
			continue
		}

		textEnd := i
		for textEnd < len(runes) {
			if _, n := extractAnsiCodeAt(runes, textEnd); n > 0 {
				break
			}
			textEnd++
		}

		for _, segment := range segmentGraphemes(string(runes[i:textEnd])) {
			w := graphemeWidth(segment)
			inRange := currentCol >= startCol && currentCol < endCol
			fits := !strict || currentCol+w <= endCol
			if inRange && fits {
				if pendingAnsi != "" {
					result += pendingAnsi
					pendingAnsi = ""
				}
				result += segment
				resultWidth += w
			}
			currentCol += w
			if currentCol >= endCol {
				break
			}
		}
		i = textEnd
		if currentCol >= endCol {
			break
		}
	}
	return SliceWithWidthResult{text: result, width: resultWidth}
}

// ExtractSegmentsResult carries the "before" and "after" segments around an
// overlay region, extracted in a single pass.
type ExtractSegmentsResult struct {
	Before      string
	BeforeWidth int
	After       string
	AfterWidth  int
}

// ExtractSegments extracts "before" and "after" segments from a line in a
// single pass, for overlay compositing: styling from before the overlay
// carries into the "after" content.
func ExtractSegments(line string, beforeEnd, afterStart, afterLen int, strictAfter bool) ExtractSegmentsResult {
	before := ""
	beforeWidth := 0
	after := ""
	afterWidth := 0
	currentCol := 0
	pendingAnsiBefore := ""
	afterStarted := false
	afterEnd := afterStart + afterLen

	var tracker ansiCodeTracker
	runes := []rune(line)
	i := 0

	for i < len(runes) {
		if code, n := extractAnsiCodeAt(runes, i); n > 0 {
			tracker.process(code)
			if currentCol < beforeEnd {
				pendingAnsiBefore += code
			} else if currentCol >= afterStart && currentCol < afterEnd && afterStarted {
				after += code
			}
			i += n
			continue
		}

		textEnd := i
		for textEnd < len(runes) {
			if _, n := extractAnsiCodeAt(runes, textEnd); n > 0 {
				break
			}
			textEnd++
		}

		for _, segment := range segmentGraphemes(string(runes[i:textEnd])) {
			w := graphemeWidth(segment)

			if currentCol < beforeEnd && currentCol+w <= beforeEnd {
				if pendingAnsiBefore != "" {
					before += pendingAnsiBefore
					pendingAnsiBefore = ""
				}
				before += segment
				beforeWidth += w
			} else if currentCol >= afterStart && currentCol < afterEnd {
				fits := !strictAfter || currentCol+w <= afterEnd
				if fits {
					if !afterStarted {
						after += tracker.getActiveCodes()
						afterStarted = true
					}
					after += segment
					afterWidth += w
				}
			}

			currentCol += w
			// Early exit: done with "before" only, or done with both segments
			done := currentCol >= afterEnd
			if afterLen <= 0 {
				done = currentCol >= beforeEnd
			}
			if done {
				break
			}
		}
		i = textEnd
		done := currentCol >= afterEnd
		if afterLen <= 0 {
			done = currentCol >= beforeEnd
		}
		if done {
			break
		}
	}

	return ExtractSegmentsResult{Before: before, BeforeWidth: beforeWidth, After: after, AfterWidth: afterWidth}
}
