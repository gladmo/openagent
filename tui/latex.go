package tui

// Port of latex.ts: renders basic LaTeX math as terminal-friendly Unicode
// text with a stacked-layout engine for display fractions, operator limits,
// and matrices. The parser operates on runes (TS indexes UTF-16 code units;
// identical for the BMP-heavy math input this renderer accepts).

import (
	"regexp"
	"strings"
	"unicode"
)

var latexSymbols = map[string]string{
	"alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "epsilon": "ϵ", "varepsilon": "ε",
	"zeta": "ζ", "eta": "η", "theta": "θ", "vartheta": "ϑ", "iota": "ι", "kappa": "κ",
	"varkappa": "ϰ", "lambda": "λ", "mu": "μ", "nu": "ν", "xi": "ξ", "pi": "π", "varpi": "ϖ",
	"rho": "ρ", "varrho": "ϱ", "sigma": "σ", "varsigma": "ς", "tau": "τ", "upsilon": "υ",
	"phi": "ϕ", "varphi": "φ", "chi": "χ", "psi": "ψ", "omega": "ω",
	"Gamma": "Γ", "Delta": "Δ", "Theta": "Θ", "Lambda": "Λ", "Xi": "Ξ", "Pi": "Π",
	"Sigma": "Σ", "Upsilon": "Υ", "Phi": "Φ", "Psi": "Ψ", "Omega": "Ω",
	"pm": "±", "mp": "∓", "times": "×", "div": "÷", "cdot": "·", "ast": "∗", "star": "⋆",
	"circ": "∘", "bullet": "•", "oplus": "⊕", "ominus": "⊖", "otimes": "⊗", "oslash": "⊘",
	"odot": "⊙", "bigcirc": "○", "dagger": "†", "ddagger": "‡", "amalg": "⨿", "uplus": "⊎",
	"sqcap": "⊓", "sqcup": "⊔", "bowtie": "⋈", "Join": "⋈", "ltimes": "⋉", "rtimes": "⋊",
	"leftouterjoin": "⟕", "rightouterjoin": "⟖", "fullouterjoin": "⟗",
	"triangleleft": "◁", "triangleright": "▷", "wr": "≀", "cap": "∩", "cup": "∪",
	"bigcap": "⋂", "bigcup": "⋃", "bigwedge": "⋀", "bigvee": "⋁", "bigsqcup": "⨆",
	"biguplus": "⨄", "bigoplus": "⨁", "bigotimes": "⨂", "bigodot": "⨀",
	"setminus": "∖", "in": "∈", "notin": "∉", "ni": "∋", "subset": "⊂", "supset": "⊃",
	"subseteq": "⊆", "supseteq": "⊇", "sqsubset": "⊏", "sqsupset": "⊐", "sqsubseteq": "⊑",
	"sqsupseteq": "⊒", "prec": "≺", "preceq": "≼", "succ": "≻", "succeq": "≽",
	"ll": "≪", "gg": "≫", "le": "≤", "leq": "≤", "leqslant": "≤", "ge": "≥", "geq": "≥",
	"geqslant": "≥", "ne": "≠", "neq": "≠", "equiv": "≡", "approx": "≈", "sim": "∼",
	"simeq": "≃", "cong": "≅", "asymp": "≍", "doteq": "≐", "propto": "∝", "parallel": "∥",
	"perp": "⊥", "mid": "∣", "vdash": "⊢", "dashv": "⊣", "models": "⊨", "Vdash": "⊩",
	"Vvdash": "⊪", "nvdash": "⊬", "nvDash": "⊭", "forall": "∀", "exists": "∃",
	"nexists": "∄", "neg": "¬", "land": "∧", "wedge": "∧", "lor": "∨", "vee": "∨",
	"to": "→", "rightarrow": "→", "longrightarrow": "→", "leftarrow": "←",
	"longleftarrow": "←", "gets": "←", "leftrightarrow": "↔", "longleftrightarrow": "↔",
	"hookleftarrow": "↩", "hookrightarrow": "↪", "twoheadleftarrow": "↞",
	"twoheadrightarrow": "↠", "leftharpoonup": "↼", "leftharpoondown": "↽",
	"rightharpoonup": "⇀", "rightharpoondown": "⇁", "rightleftharpoons": "⇌",
	"leftrightharpoons": "⇋", "nearrow": "↗", "searrow": "↘", "swarrow": "↙",
	"nwarrow": "↖", "rightsquigarrow": "⇝", "leadsto": "⇝", "Rightarrow": "⇒",
	"Longrightarrow": "⇒", "Leftarrow": "⇐", "Longleftarrow": "⇐", "Leftrightarrow": "⇔",
	"Longleftrightarrow": "⇔", "implies": "⇒", "iff": "⇔", "mapsto": "↦", "longmapsto": "↦",
	"uparrow": "↑", "downarrow": "↓", "partial": "∂", "nabla": "∇",
	"int": "∫", "iint": "∬", "iiint": "∭", "oint": "∮", "sum": "∑", "prod": "∏",
	"coprod": "∐", "infty": "∞", "emptyset": "∅", "varnothing": "∅", "angle": "∠",
	"therefore": "∴", "because": "∵", "aleph": "ℵ", "beth": "ℶ", "gimel": "ℷ",
	"daleth": "ℸ", "top": "⊤", "bot": "⊥", "triangle": "△", "square": "□", "lozenge": "◊",
	"checkmark": "✓", "complement": "∁", "wp": "℘", "prime": "′", "ldots": "…", "dots": "…",
	"cdots": "⋯", "vdots": "⋮", "ddots": "⋱", "ell": "ℓ", "hbar": "ℏ", "Im": "ℑ", "Re": "ℜ",
	"langle": "⟨", "rangle": "⟩", "vert": "|", "lvert": "|", "rvert": "|", "Vert": "‖",
	"lVert": "‖", "rVert": "‖", "lbrace": "{", "rbrace": "}", "backslash": "\\",
	"lfloor": "⌊", "rfloor": "⌋", "lceil": "⌈", "rceil": "⌉", "colon": ":",
}

func stringSet(items ...string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

var latexNamedOperators = stringSet(
	"arccos", "arcsin", "arctan", "arg", "cos", "cosh", "cot", "coth", "csc", "deg",
	"det", "dim", "exp", "gcd", "hom", "inf", "ker", "lg", "lim", "liminf", "limsup",
	"ln", "log", "max", "min", "Pr", "sec", "sin", "sinh", "sup", "tan", "tanh",
)

var latexLimitOperators = stringSet(
	"argmax", "argmin", "inf", "injlim", "lim", "liminf", "limsup", "max", "min",
	"projlim", "sup",
)

var latexDisplayLimitSymbols = stringSet(
	"bigcap", "bigcup", "bigodot", "bigoplus", "bigotimes", "bigsqcup", "biguplus",
	"bigvee", "bigwedge", "coprod", "int", "iint", "iiint", "oint", "prod", "sum",
)

var latexRelationCommands = stringSet(
	"Leftarrow", "Leftrightarrow", "Longleftarrow", "Longleftrightarrow",
	"Longrightarrow", "Rightarrow", "Join", "Vdash", "Vvdash", "approx", "asymp",
	"bowtie", "cong", "dashv", "fullouterjoin", "doteq", "downarrow", "equiv",
	"ge", "geq", "geqslant", "gets", "gg", "hookleftarrow", "hookrightarrow", "iff",
	"implies", "in", "leadsto", "le", "leftarrow", "leftharpoondown", "leftharpoonup",
	"leftrightarrow", "leftrightharpoons", "leftouterjoin", "leq", "leqslant", "ll",
	"longleftarrow", "longleftrightarrow", "longmapsto", "longrightarrow", "ltimes",
	"mapsto", "mid", "models", "ne", "nearrow", "neq", "ni", "notin", "nvdash",
	"nvDash", "nwarrow", "parallel", "perp", "prec", "preceq", "propto",
	"rightharpoondown", "rightharpoonup", "rightleftharpoons", "rightouterjoin",
	"rightarrow", "rightsquigarrow", "rtimes", "searrow", "sim", "simeq", "sqsubset",
	"sqsubseteq", "sqsupset", "sqsupseteq", "subset", "subseteq", "succ", "succeq",
	"supset", "supseteq", "swarrow", "to", "triangleleft", "triangleright",
	"twoheadleftarrow", "twoheadrightarrow", "uparrow", "vdash",
)

var latexNegatedSymbols = map[string]string{
	"<": "≮", ">": "≯", "=": "≠", "∈": "∉", "∋": "∌", "∣": "∤", "∥": "∦", "∼": "≁",
	"≃": "≄", "≅": "≇", "≈": "≉", "≡": "≢", "≤": "≰", "≥": "≱", "≺": "⊀", "≻": "⊁",
	"⊂": "⊄", "⊃": "⊅", "⊆": "⊈", "⊇": "⊉", "⊢": "⊬", "⊨": "⊭", "↔": "↮", "←": "↚",
	"→": "↛", "⇒": "⇏", "⇐": "⇍", "⇔": "⇎", "≼": "⋠", "≽": "⋡",
}

var latexBlackboard = map[string]string{
	"C": "ℂ", "H": "ℍ", "N": "ℕ", "P": "ℙ", "Q": "ℚ", "R": "ℝ", "Z": "ℤ",
}

var latexSuperscripts = map[rune]rune{
	'0': '⁰', '1': '¹', '2': '²', '3': '³', '4': '⁴', '5': '⁵', '6': '⁶', '7': '⁷',
	'8': '⁸', '9': '⁹', '+': '⁺', '-': '⁻', '=': '⁼', '(': '⁽', ')': '⁾',
	'a': 'ᵃ', 'b': 'ᵇ', 'c': 'ᶜ', 'd': 'ᵈ', 'e': 'ᵉ', 'f': 'ᶠ', 'g': 'ᵍ', 'h': 'ʰ',
	'i': 'ⁱ', 'j': 'ʲ', 'k': 'ᵏ', 'l': 'ˡ', 'm': 'ᵐ', 'n': 'ⁿ', 'o': 'ᵒ', 'p': 'ᵖ',
	'r': 'ʳ', 's': 'ˢ', 't': 'ᵗ', 'u': 'ᵘ', 'v': 'ᵛ', 'w': 'ʷ', 'x': 'ˣ', 'y': 'ʸ',
	'z': 'ᶻ',
}

var latexSubscripts = map[rune]rune{
	'0': '₀', '1': '₁', '2': '₂', '3': '₃', '4': '₄', '5': '₅', '6': '₆', '7': '₇',
	'8': '₈', '9': '₉', '+': '₊', '-': '₋', '=': '₌', '(': '₍', ')': '₎',
	'a': 'ₐ', 'e': 'ₑ', 'h': 'ₕ', 'i': 'ᵢ', 'j': 'ⱼ', 'k': 'ₖ', 'l': 'ₗ', 'm': 'ₘ',
	'n': 'ₙ', 'o': 'ₒ', 'p': 'ₚ', 'r': 'ᵣ', 's': 'ₛ', 't': 'ₜ', 'u': 'ᵤ', 'v': 'ᵥ',
	'x': 'ₓ',
}

var latexSpacingCommands = stringSet(",", ":", ";", " ", ">", "enspace", "enskip", "medspace", "quad", "qquad", "thickspace", "thinspace")
var latexNegativeSpacingCommands = stringSet("!", "negmedspace", "negthickspace", "negthinspace")

const latexNegativeSpace = "\u0000"

var latexFontSwitchCommands = stringSet("bf", "cal", "it", "rm", "sf", "sl", "tt")
var latexIgnoredCommands = stringSet("displaystyle", "limits", "nolimits", "scriptstyle", "scriptscriptstyle", "textstyle")
var latexSizeCommands = stringSet("big", "Big", "bigg", "Bigg", "bigl", "Bigl", "biggl", "Biggl", "bigr", "Bigr", "biggr", "Biggr")
var latexPlainWrappers = stringSet(
	"emph", "mathcal", "mathbf", "mathfrak", "mathit", "mathrm", "mathnormal",
	"mathscr", "mathsf", "mathtt", "mathup", "mbox", "overbrace", "pmb", "smash",
	"substack", "text", "textbf", "textit", "textmd", "textnormal", "textrm",
	"textsc", "textsf", "textsl", "texttt", "textup", "underbrace", "bm", "boldsymbol",
)

var latexAccents = map[string]string{
	"acute": "\u0301", "bar": "\u0305", "breve": "\u0306", "check": "\u030c",
	"ddot": "\u0308", "dot": "\u0307", "grave": "\u0300", "hat": "\u0302",
	"mathring": "\u030a", "overleftarrow": "\u20d6", "overleftrightarrow": "\u20e1",
	"overline": "\u0305", "overrightarrow": "\u20d7", "tilde": "\u0303",
	"underline": "\u0332", "vec": "\u20d7", "widehat": "\u0302", "widetilde": "\u0303",
}

var latexLetterOrNumberPattern = regexp.MustCompile(`^[\p{L}\p{N}.]+$`)
var latexNumberPattern = regexp.MustCompile(`^[\p{N}.]+$`)
var latexScriptSpacingPattern = regexp.MustCompile(`\s*([=+-])\s*`)
var latexLooksLikePendingMath = regexp.MustCompile(`\\[A-Za-z]+|[_^=+*/<>()[\]|±≤≥≠≈∈→⇒∞∫∑√-]`)
var latexConditionPrefix = regexp.MustCompile(`^(?:if|when|for|otherwise)\b`)
var latexSpaceCollapse = regexp.MustCompile(`[ \t]+`)

func latexReplaceCharacters(value string, replacements map[rune]rune) (string, bool) {
	var result strings.Builder
	for _, character := range value {
		replacement, ok := replacements[character]
		if !ok {
			return "", false
		}
		result.WriteRune(replacement)
	}
	return result.String(), true
}

func latexNormalizeScriptValue(value string) string {
	return strings.TrimSpace(latexScriptSpacingPattern.ReplaceAllString(value, "$1"))
}

func latexFormatUnicodeScript(value string, sub bool) (string, bool) {
	table := latexSuperscripts
	if sub {
		table = latexSubscripts
	}
	return latexReplaceCharacters(latexNormalizeScriptValue(value), table)
}

func latexFormatScript(value string, sub bool) string {
	value = latexNormalizeScriptValue(value)
	if unicode_, ok := latexFormatUnicodeScript(value, sub); ok {
		return unicode_
	}

	prefix := "^"
	if sub {
		prefix = "_"
	}
	runes := []rune(value)
	if len(runes) == 1 || (sub && latexASCIILetters(value)) {
		return prefix + value
	}
	return prefix + "(" + value + ")"
}

// latexASCIILetters mirrors the TS /^[A-Za-z]+$/ check: ASCII letters only
// (Greek subscripts fall back to parenthesized form).
func latexASCIILetters(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !isASCIILetterRune(r) {
			return false
		}
	}
	return true
}

func latexFormatFraction(numerator, denominator string) string {
	numerator = strings.TrimSpace(numerator)
	denominator = strings.TrimSpace(denominator)
	simpleNumerator := latexLetterOrNumberPattern.MatchString(numerator)
	simpleDenominator := latexNumberPattern.MatchString(denominator) || len([]rune(denominator)) == 1
	num := numerator
	if !simpleNumerator {
		num = "(" + numerator + ")"
	}
	den := denominator
	if !simpleDenominator {
		den = "(" + denominator + ")"
	}
	return num + "/" + den
}

func latexFormatRoot(value, symbol string) string {
	value = strings.TrimSpace(value)
	if latexLetterOrNumberPattern.MatchString(value) {
		return symbol + value
	}
	return symbol + "(" + value + ")"
}

const (
	latexNamedOperatorStart = "\U000f0004"
	latexNamedOperatorEnd   = "\U000f0005"
	latexLayoutMarkerStart  = "\U000f0000"
	latexLayoutMarkerEnd    = "\U000f0001"
	latexProtectedSpace     = "\U000f0002"
)

// latexNormalizeOutput applies named-operator spacing (Go regexp has no
// lookbehind: manual scan), collapses runs, and drops blank edge lines.
func latexNormalizeOutput(value string) string {
	// Left spacing: marker-start preceded by letter/number/)]}/marker-end.
	runes := []rune(value)
	var b strings.Builder
	for i, r := range runes {
		if string(r) == latexNamedOperatorStart && i > 0 {
			prev := runes[i-1]
			if unicode.IsLetter(prev) || unicode.IsNumber(prev) ||
				prev == ')' || prev == ']' || prev == '}' || string(prev) == latexLayoutMarkerEnd {
				b.WriteString(" ")
				continue
			}
		}
		b.WriteRune(r)
	}
	value = b.String()
	value = strings.ReplaceAll(value, latexNamedOperatorStart, "")

	// Right spacing: marker-end followed by letter/number/√/marker-start.
	runes = []rune(value)
	b.Reset()
	for i, r := range runes {
		b.WriteRune(r)
		if string(r) == latexNamedOperatorEnd && i+1 < len(runes) {
			next := runes[i+1]
			if unicode.IsLetter(next) || unicode.IsNumber(next) ||
				next == '√' || string(next) == latexLayoutMarkerStart {
				b.WriteString(" ")
			}
		}
	}
	value = b.String()
	value = strings.ReplaceAll(value, latexNamedOperatorEnd, "")

	lines := strings.Split(value, "\n")
	var kept []string
	for index, line := range lines {
		normalized := strings.TrimSpace(latexSpaceCollapse.ReplaceAllString(line, " "))
		if len(normalized) > 0 || (index > 0 && index < len(lines)-1) {
			kept = append(kept, normalized)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

type latexLayoutNodeKind int

const (
	latexFraction latexLayoutNodeKind = iota
	latexOperator
	latexScript
	latexMatrix
)

type latexLayoutNode struct {
	kind        latexLayoutNodeKind
	numerator   string // fraction
	denominator string
	operator    string // operator
	lower       string
	hasLower    bool
	upper       string
	hasUpper    bool
	lines       []string // matrix
	baseline    int
}

type latexLayout struct {
	lines    []string
	width    int
	baseline int
}

func latexPadLayoutLine(line string, width int, centered bool) string {
	padding := maxInt(0, width-VisibleWidth(line))
	left := 0
	if centered {
		left = padding / 2
	}
	return stringRepeat(" ", left) + line + stringRepeat(" ", padding-left)
}

func latexJoinLayouts(layouts []latexLayout) latexLayout {
	if len(layouts) == 0 {
		return latexLayout{lines: []string{""}}
	}
	baseline := 0
	for _, layout := range layouts {
		if layout.baseline > baseline {
			baseline = layout.baseline
		}
	}
	below := 0
	for _, layout := range layouts {
		if d := len(layout.lines) - layout.baseline - 1; d > below {
			below = d
		}
	}
	var lines []string
	for row := 0; row <= baseline+below; row++ {
		line := ""
		for _, layout := range layouts {
			sourceRow := row - baseline + layout.baseline
			if sourceRow >= 0 && sourceRow < len(layout.lines) {
				line += latexPadLayoutLine(layout.lines[sourceRow], layout.width, false)
			} else {
				line += stringRepeat(" ", layout.width)
			}
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	width := 0
	for _, layout := range layouts {
		width += layout.width
	}
	return latexLayout{lines: lines, width: width, baseline: baseline}
}

// latexFindMarkers locates layout marker occurrences (manual: fixed strings).
func latexFindMarkers(line string) []struct{ index, node, end int } {
	var found []struct{ index, node, end int }
	searchFrom := 0
	for {
		index := strings.Index(line[searchFrom:], latexLayoutMarkerStart)
		if index == -1 {
			break
		}
		index += searchFrom
		digitsStart := index + len(latexLayoutMarkerStart)
		digitsEnd := digitsStart
		for digitsEnd < len(line) && line[digitsEnd] >= '0' && line[digitsEnd] <= '9' {
			digitsEnd++
		}
		if end := strings.Index(line[digitsStart:digitsEnd], latexLayoutMarkerEnd); end == -1 && digitsEnd > digitsStart {
			if rest := line[digitsEnd:]; strings.HasPrefix(rest, latexLayoutMarkerEnd) {
				node := 0
				for _, c := range line[digitsStart:digitsEnd] {
					node = node*10 + int(c-'0')
				}
				found = append(found, struct{ index, node, end int }{index, node, digitsEnd + len(latexLayoutMarkerEnd)})
				searchFrom = digitsEnd + len(latexLayoutMarkerEnd)
				continue
			}
		}
		searchFrom = digitsStart
	}
	return found
}

func latexTrailingLayoutMarker(value string) (int, bool) {
	if !strings.HasSuffix(value, latexLayoutMarkerEnd) {
		return -1, false
	}
	index := strings.LastIndex(value, latexLayoutMarkerStart)
	if index == -1 {
		return -1, false
	}
	digits := value[index+len(latexLayoutMarkerStart) : len(value)-len(latexLayoutMarkerEnd)]
	if digits == "" {
		return -1, false
	}
	node := 0
	for _, c := range digits {
		if c < '0' || c > '9' {
			return -1, false
		}
		node = node*10 + int(c-'0')
	}
	return node, true
}

func latexRenderLayout(source string, nodes []latexLayoutNode) latexLayout {
	var renderedLines []string
	firstBaseline := 0
	for _, sourceLine := range strings.Split(source, "\n") {
		var layouts []latexLayout
		position := 0
		var previousNode *latexLayoutNode
		for _, match := range latexFindMarkers(sourceLine) {
			if match.node >= len(nodes) {
				continue
			}
			node := &nodes[match.node]
			if match.index > position {
				sliced := sourceLine[position:match.index]
				trimmed := sliced
				if previousNode != nil {
					trimmed = strings.TrimLeft(sliced, " \t\n\v\f\r")
				}
				trimmed = strings.TrimRight(trimmed, " \t\n\v\f\r")
				preserveLeadingSpace := previousNode != nil && previousNode.kind == latexMatrix &&
					strings.HasPrefix(sliced, " ") || strings.HasPrefix(sliced, "\t")
				preserveTrailingSpace := node.kind == latexMatrix &&
					strings.HasSuffix(sliced, " ") || strings.HasSuffix(sliced, "\t")
				text := ""
				if trimmed != "" {
					if preserveLeadingSpace {
						text += " "
					}
					text += trimmed
					if preserveTrailingSpace {
						text += " "
					}
				} else if preserveLeadingSpace || preserveTrailingSpace {
					text = " "
				}
				layouts = append(layouts, latexLayout{lines: []string{text}, width: VisibleWidth(text)})
			}
			switch node.kind {
			case latexFraction:
				numerator := latexRenderLayout(node.numerator, nodes)
				denominator := latexRenderLayout(node.denominator, nodes)
				contentWidth := maxInt(numerator.width, maxInt(denominator.width, 1))
				width := contentWidth + 2
				var lines []string
				for _, line := range numerator.lines {
					lines = append(lines, latexPadLayoutLine(line, width, true))
				}
				lines = append(lines, " "+stringRepeat("─", contentWidth)+" ")
				for _, line := range denominator.lines {
					lines = append(lines, latexPadLayoutLine(line, width, true))
				}
				layouts = append(layouts, latexLayout{lines: lines, width: width, baseline: len(numerator.lines)})
			case latexOperator:
				contentWidth := VisibleWidth(node.operator)
				if node.hasLower {
					contentWidth = maxInt(contentWidth, VisibleWidth(node.lower))
				}
				if node.hasUpper {
					contentWidth = maxInt(contentWidth, VisibleWidth(node.upper))
				}
				var lines []string
				if node.hasUpper {
					lines = append(lines, latexPadLayoutLine(node.upper, contentWidth, true)+" ")
				}
				lines = append(lines, latexPadLayoutLine(node.operator, contentWidth, true)+" ")
				if node.hasLower {
					lines = append(lines, latexPadLayoutLine(node.lower, contentWidth, true)+" ")
				}
				baseline := 0
				if node.hasUpper {
					baseline = 1
				}
				layouts = append(layouts, latexLayout{lines: lines, width: contentWidth + 1, baseline: baseline})
			case latexScript:
				var upper, lower *latexLayout
				if node.hasUpper {
					rendered := latexRenderLayout(node.upper, nodes)
					upper = &rendered
				}
				if node.hasLower {
					rendered := latexRenderLayout(node.lower, nodes)
					lower = &rendered
				}
				width := 0
				if upper != nil && upper.width > width {
					width = upper.width
				}
				if lower != nil && lower.width > width {
					width = lower.width
				}
				var lines []string
				if upper != nil {
					for _, line := range upper.lines {
						lines = append(lines, latexPadLayoutLine(line, width, false))
					}
				}
				lines = append(lines, stringRepeat(" ", width))
				if lower != nil {
					for _, line := range lower.lines {
						lines = append(lines, latexPadLayoutLine(line, width, false))
					}
				}
				baseline := 0
				if upper != nil {
					baseline = len(upper.lines)
				}
				layouts = append(layouts, latexLayout{lines: lines, width: width, baseline: baseline})
			default: // matrix
				width := 0
				for _, line := range node.lines {
					if w := VisibleWidth(line); w > width {
						width = w
					}
				}
				var lines []string
				for _, line := range node.lines {
					lines = append(lines, latexPadLayoutLine(line, width, false))
				}
				layouts = append(layouts, latexLayout{lines: lines, width: width, baseline: node.baseline})
			}
			position = match.end
			previousNode = node
		}
		if position < len(sourceLine) {
			sliced := sourceLine[position:]
			trimmed := sliced
			if previousNode != nil {
				trimmed = strings.TrimLeft(sliced, " \t\n\v\f\r")
			}
			text := trimmed
			if previousNode != nil && previousNode.kind == latexMatrix && strings.HasPrefix(sliced, " ") {
				text = " " + trimmed
			}
			layouts = append(layouts, latexLayout{lines: []string{text}, width: VisibleWidth(text)})
		}
		lineLayout := latexJoinLayouts(layouts)
		if len(renderedLines) == 0 {
			firstBaseline = lineLayout.baseline
		}
		renderedLines = append(renderedLines, lineLayout.lines...)
	}
	width := 0
	for _, line := range renderedLines {
		if w := VisibleWidth(line); w > width {
			width = w
		}
	}
	return latexLayout{lines: renderedLines, width: width, baseline: firstBaseline}
}

// latexParser renders one LaTeX source string.
type latexParser struct {
	source         []rune
	nodes          *[]latexLayoutNode
	display        bool
	position       int
	supported      bool
	stackFractions bool
	scriptDepth    int
}

// RenderLatexOptions configures display-math layouts.
type RenderLatexOptions struct {
	// Display stacks fractions and operator limits vertically.
	Display bool
}

// RenderLatex renders a basic LaTeX math expression as terminal-friendly
// Unicode text; ok=false for unsupported or malformed syntax.
func RenderLatex(source string, options RenderLatexOptions) (string, bool) {
	layoutNodes := []latexLayoutNode{}
	parser := &latexParser{
		source:         []rune(source),
		nodes:          &layoutNodes,
		display:        options.Display,
		supported:      true,
		stackFractions: true,
	}
	rendered, ok := parser.render()
	if !ok {
		return "", false
	}
	if len(layoutNodes) == 0 {
		return strings.ReplaceAll(rendered, latexProtectedSpace, " "), true
	}
	lines := latexRenderLayout(rendered, layoutNodes).lines
	indentation := -1
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indentation == -1 || indent < indentation {
			indentation = indent
		}
	}
	if indentation < 0 {
		indentation = 0
	}
	var out []string
	for _, line := range lines {
		if indentation <= len(line) {
			out = append(out, strings.TrimRight(line[indentation:], " \t"))
		} else {
			out = append(out, strings.TrimRight(line, " \t"))
		}
	}
	return strings.TrimRight(strings.ReplaceAll(strings.Join(out, "\n"), latexProtectedSpace, " "), " \t\n\r"), true
}
