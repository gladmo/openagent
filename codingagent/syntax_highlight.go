package codingagent

// Port of utils/syntax-highlight.ts: the hljs span-scope rendering
// machinery plus a regex-based tokenizer for the eager language subset
// (documented deviation: highlight.js -> hand-rolled token classifiers for
// the top languages; the span/scope/theme contract is preserved).

import (
	"regexp"
	"strings"
)

// HighlightFormatter styles one text run.
type HighlightFormatter func(text string) string

// HighlightTheme maps hljs scopes to formatters.
type HighlightTheme map[string]HighlightFormatter

// HighlightOptions configure one highlight call.
type HighlightOptions struct {
	Language       string
	IgnoreIllegals bool
	LanguageSubset []string
	Theme          HighlightTheme
}

const spanClose = "</span>"
const highlightClassPrefix = "hljs-"

var spanClassPattern = regexp.MustCompile(`\sclass\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// getScopeFromSpanTag extracts the hljs-* class from a span tag.
func getScopeFromSpanTag(tag string) string {
	match := spanClassPattern.FindStringSubmatch(tag)
	if match == nil {
		return ""
	}
	classValue := match[1]
	if classValue == "" {
		classValue = match[2]
	}
	if classValue == "" {
		return ""
	}
	for _, className := range strings.Fields(classValue) {
		if strings.HasPrefix(className, highlightClassPrefix) {
			return className[len(highlightClassPrefix):]
		}
	}
	return ""
}

// getScopeFormatter resolves a scope to its formatter (exact, then dot
// prefix, then dash prefix).
func getScopeFormatter(scope string, theme HighlightTheme) HighlightFormatter {
	if exact, ok := theme[scope]; ok {
		return exact
	}
	if dotIndex := strings.IndexByte(scope, '.'); dotIndex != -1 {
		if formatter, ok := theme[scope[:dotIndex]]; ok {
			return formatter
		}
	}
	if dashIndex := strings.IndexByte(scope, '-'); dashIndex != -1 {
		if formatter, ok := theme[scope[:dashIndex]]; ok {
			return formatter
		}
	}
	return nil
}

// getActiveFormatter walks scopes innermost-out; default is the fallback.
func getActiveFormatter(scopes []string, theme HighlightTheme) HighlightFormatter {
	for i := len(scopes) - 1; i >= 0; i-- {
		if scopes[i] == "" {
			continue
		}
		if formatter := getScopeFormatter(scopes[i], theme); formatter != nil {
			return formatter
		}
	}
	return theme["default"]
}

// isSpanOpenTagStart checks for "<span" followed by a tag delimiter.
func isSpanOpenTagStart(html string, index int) bool {
	if !strings.HasPrefix(html[index:], "<span") {
		return false
	}
	rest := html[index+len("<span"):]
	if rest == "" {
		return false
	}
	switch rest[0] {
	case '>', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// RenderHighlightedHtml renders hljs span HTML through the theme's
// formatters with entity decoding.
func RenderHighlightedHtml(html string, theme HighlightTheme) string {
	var output strings.Builder
	var textBuffer strings.Builder
	var scopes []string

	flushText := func() {
		if textBuffer.Len() == 0 {
			return
		}
		formatter := getActiveFormatter(scopes, theme)
		if formatter != nil {
			output.WriteString(formatter(textBuffer.String()))
		} else {
			output.WriteString(textBuffer.String())
		}
		textBuffer.Reset()
	}

	index := 0
	for index < len(html) {
		if isSpanOpenTagStart(html, index) {
			if tagEndIndex := strings.IndexByte(html[index+5:], '>'); tagEndIndex != -1 {
				flushText()
				tag := html[index : index+5+tagEndIndex+1]
				scopes = append(scopes, getScopeFromSpanTag(tag))
				index += 5 + tagEndIndex + 1
				continue
			}
		}

		if strings.HasPrefix(html[index:], spanClose) {
			flushText()
			if len(scopes) > 0 {
				scopes = scopes[:len(scopes)-1]
			}
			index += len(spanClose)
			continue
		}

		if html[index] == '&' {
			if decoded, length := decodeHtmlEntityAt(html, index); decoded != "" {
				textBuffer.WriteString(decoded)
				index += length
				continue
			}
		}

		textBuffer.WriteByte(html[index])
		index++
	}

	flushText()
	return output.String()
}

// decodeHtmlEntityAt decodes one entity at index; "" when none.
func decodeHtmlEntityAt(html string, index int) (string, int) {
	rest := html[index:]
	for entity, replacement := range map[string]string{
		"&amp;": "&", "&lt;": "<", "&gt;": ">",
		"&quot;": `"`, "&#x27;": "'", "&#39;": "'", "&apos;": "'",
	} {
		if strings.HasPrefix(rest, entity) {
			return replacement, len(entity)
		}
	}
	// Numeric entities.
	if strings.HasPrefix(rest, "&#") {
		if endIndex := strings.IndexByte(rest, ';'); endIndex != -1 {
			numeric := rest[2:endIndex]
			if strings.HasPrefix(numeric, "x") || strings.HasPrefix(numeric, "X") {
				var code int
				if _, err := fmtSscanfHex(numeric[1:], &code); err == nil && code > 0 {
					return string(rune(code)), endIndex + 1
				}
			} else {
				var code int
				if _, err := fmtSscanfInt(numeric, &code); err == nil && code > 0 {
					return string(rune(code)), endIndex + 1
				}
			}
		}
	}
	return "", 0
}

// ============================================================================
// Regex-based tokenizer (the highlight.js deviation)
// ============================================================================

// tokenRule classifies one lexeme to an hljs scope.
type tokenRule struct {
	pattern *regexp.Regexp
	scope   string
}

// languageRules holds the per-language rule sets (ordered).
var languageRules = map[string][]tokenRule{
	"go": {
		{regexp.MustCompile(`//[^\n]*`), "comment"},
		{regexp.MustCompile(`/\*[\s\S]*?\*/`), "comment"},
		{regexp.MustCompile("`" + "`" + "[^`" + "`" + "]*`"), "string"},
		{regexp.MustCompile(`"[^"\n]*"`), "string"},
		{regexp.MustCompile(`'(?:[^'\\\n]|\\.)*'`), "string"},
		{regexp.MustCompile(`\b(?:break|case|chan|const|continue|default|defer|else|fallthrough|for|func|go|goto|if|import|interface|map|package|range|return|select|struct|switch|type|var)\b`), "keyword"},
		{regexp.MustCompile(`\b(?:bool|byte|complex64|complex128|error|float32|float64|int|int8|int16|int32|int64|rune|string|uint|uint8|uint16|uint32|uint64|uintptr|any)\b`), "type"},
		{regexp.MustCompile(`\b(?:true|false|nil|iota)\b`), "literal"},
		{regexp.MustCompile(`\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b`), "number"},
		{regexp.MustCompile(`\b[A-Z][A-Za-z0-9_]*\b`), "title.function"},
	},
	"python": {
		{regexp.MustCompile(`#[^\n]*`), "comment"},
		{regexp.MustCompile(`"""[\s\S]*?"""`), "string"},
		{regexp.MustCompile(`'''[\s\S]*?'''`), "string"},
		{regexp.MustCompile(`"[^"\n]*"`), "string"},
		{regexp.MustCompile(`'[^'\n]*'`), "string"},
		{regexp.MustCompile(`\b(?:and|as|assert|async|await|break|class|continue|def|del|elif|else|except|finally|for|from|global|if|import|in|is|lambda|nonlocal|not|or|pass|raise|return|try|while|with|yield)\b`), "keyword"},
		{regexp.MustCompile(`\b(?:True|False|None)\b`), "literal"},
		{regexp.MustCompile(`\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b`), "number"},
		{regexp.MustCompile(`\b(?:def|class)\s+\w+`), "title.function"},
	},
	"bash": {
		{regexp.MustCompile(`#[^\n]*`), "comment"},
		{regexp.MustCompile(`"[^"\n]*"`), "string"},
		{regexp.MustCompile(`'[^'\n]*'`), "string"},
		{regexp.MustCompile(`\b(?:if|then|else|elif|fi|for|while|do|done|case|esac|function|in|select|until|time|coproc|return|break|continue|local|export|readonly|declare|typeset|unset)\b`), "keyword"},
		{regexp.MustCompile(`\$\{[^}]*\}`), "variable"},
		{regexp.MustCompile(`\$\w+`), "variable"},
		{regexp.MustCompile(`\b\d+\b`), "number"},
	},
	"javascript": {
		{regexp.MustCompile(`//[^\n]*`), "comment"},
		{regexp.MustCompile(`/\*[\s\S]*?\*/`), "comment"},
		{regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"`), "string"},
		{regexp.MustCompile(`'(?:[^'\\\n]|\\.)*'`), "string"},
		{regexp.MustCompile("`[^`]*`"), "string"},
		{regexp.MustCompile(`\b(?:async|await|break|case|catch|class|const|continue|debugger|default|delete|do|else|export|extends|finally|for|function|if|import|in|instanceof|let|new|of|return|static|super|switch|this|throw|try|typeof|var|void|while|with|yield)\b`), "keyword"},
		{regexp.MustCompile(`\b(?:true|false|null|undefined|NaN|Infinity)\b`), "literal"},
		{regexp.MustCompile(`\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b`), "number"},
	},
	"typescript": {
		{regexp.MustCompile(`//[^\n]*`), "comment"},
		{regexp.MustCompile(`/\*[\s\S]*?\*/`), "comment"},
		{regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"`), "string"},
		{regexp.MustCompile(`'(?:[^'\\\n]|\\.)*'`), "string"},
		{regexp.MustCompile("`[^`]*`"), "string"},
		{regexp.MustCompile(`\b(?:abstract|any|as|async|await|boolean|break|case|catch|class|const|continue|declare|default|delete|do|else|enum|export|extends|finally|for|from|function|if|implements|import|in|infer|instanceof|interface|is|keyof|let|module|namespace|never|new|null|number|object|of|private|protected|public|readonly|return|satisfies|static|string|super|switch|symbol|this|throw|try|type|typeof|undefined|unknown|var|void|while|with|yield)\b`), "keyword"},
		{regexp.MustCompile(`\b(?:true|false)\b`), "literal"},
		{regexp.MustCompile(`\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b`), "number"},
	},
	"rust": {
		{regexp.MustCompile(`//[^\n]*`), "comment"},
		{regexp.MustCompile(`/\*[\s\S]*?\*/`), "comment"},
		{regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"`), "string"},
		{regexp.MustCompile(`\b(?:as|async|await|break|const|continue|crate|dyn|else|enum|extern|false|fn|for|if|impl|in|let|loop|match|mod|move|mut|pub|ref|return|self|Self|static|struct|super|trait|true|type|unsafe|use|where|while)\b`), "keyword"},
		{regexp.MustCompile(`\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b`), "number"},
	},
	"json": {
		{regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"`), "string"},
		{regexp.MustCompile(`\b(?:true|false|null)\b`), "literal"},
		{regexp.MustCompile(`-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b`), "number"},
	},
}

// languageAliases map common aliases to the rule sets.
var languageAliases = map[string]string{
	"py": "python", "golang": "go", "sh": "bash", "shell": "bash", "zsh": "bash",
	"js": "javascript", "jsx": "javascript", "ts": "typescript", "tsx": "typescript",
	"rs": "rust",
}

// highlightToken is one classified lexeme.
type highlightToken struct {
	text  string
	scope string
}

// tokenize classifies code with the language's rules (longest-match wins
// at each position; unclassified runs carry no scope). Plain runs between
// matches are tracked separately from the scan cursor.
func tokenize(code string, rules []tokenRule) []highlightToken {
	var tokens []highlightToken
	runes := []rune(code)
	plainStart := 0
	scan := 0

	pushPlainTo := func(end int) {
		if end > plainStart {
			tokens = append(tokens, highlightToken{text: string(runes[plainStart:end])})
		}
		plainStart = end
	}

	for scan < len(runes) {
		bestLen := 0
		bestScope := ""
		rest := string(runes[scan:])
		for _, rule := range rules {
			if loc := rule.pattern.FindStringIndex(rest); loc != nil && loc[0] == 0 && loc[1] > bestLen {
				bestLen = loc[1]
				bestScope = rule.scope
			}
		}
		if bestLen > 0 {
			pushPlainTo(scan)
			tokens = append(tokens, highlightToken{text: string(runes[scan : scan+bestLen]), scope: bestScope})
			scan += bestLen
			plainStart = scan
			continue
		}
		scan++
	}
	pushPlainTo(scan)
	return tokens
}

// escapeHighlightHtml escapes text for span HTML.
func escapeHighlightHtml(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(text)
}

// Highlight tokenizes code and renders through the theme. The deviation
// from highlight.js: regex token classifiers instead of the full grammar
// engine (span/scope contract preserved).
func Highlight(code string, options *HighlightOptions) string {
	if options == nil {
		options = &HighlightOptions{}
	}
	language := options.Language
	if alias, ok := languageAliases[language]; ok {
		language = alias
	}
	rules, supported := languageRules[language]
	if !supported {
		// Auto-detect: try each language, keep the one with the most
		// classified characters.
		best := []tokenRule(nil)
		bestCoverage := 0
		for _, candidate := range detectLanguageOrder(options.LanguageSubset) {
			candidateRules := languageRules[candidate]
			coverage := 0
			for _, token := range tokenize(code, candidateRules) {
				if token.scope != "" {
					coverage += len(token.text)
				}
			}
			if coverage > bestCoverage {
				bestCoverage = coverage
				best = candidateRules
			}
		}
		rules = best
	}

	var html strings.Builder
	for _, token := range tokenize(code, rules) {
		escaped := escapeHighlightHtml(token.text)
		if token.scope == "" {
			html.WriteString(escaped)
			continue
		}
		html.WriteString(`<span class="` + highlightClassPrefix + token.scope + `">` + escaped + spanClose)
	}
	return RenderHighlightedHtml(html.String(), options.Theme)
}

// detectLanguageOrder orders detection candidates (subset first).
func detectLanguageOrder(subset []string) []string {
	order := append([]string(nil), subset...)
	for _, language := range []string{"go", "python", "javascript", "typescript", "bash", "rust", "json"} {
		found := false
		for _, existing := range order {
			if existing == language {
				found = true
				break
			}
		}
		if !found {
			order = append(order, language)
		}
	}
	return order
}

// SupportedLanguages lists the tokenizer's languages.
func SupportedLanguages() []string {
	names := make([]string, 0, len(languageRules))
	for name := range languageRules {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// SupportsLanguage reports tokenizer coverage for a name.
func SupportsLanguage(name string) bool {
	if alias, ok := languageAliases[name]; ok {
		name = alias
	}
	_, ok := languageRules[name]
	return ok
}

// fmtSscanfHex parses a hex integer.
func fmtSscanfHex(s string, out *int) (int, error) {
	value := 0
	for _, c := range s {
		digit := -1
		switch {
		case c >= '0' && c <= '9':
			digit = int(c - '0')
		case c >= 'a' && c <= 'f':
			digit = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			digit = int(c-'A') + 10
		}
		if digit < 0 {
			return 0, errInvalidEntity
		}
		value = value*16 + digit
	}
	*out = value
	return 1, nil
}

// fmtSscanfInt parses a decimal integer.
func fmtSscanfInt(s string, out *int) (int, error) {
	if s == "" {
		return 0, errInvalidEntity
	}
	value := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errInvalidEntity
		}
		value = value*10 + int(c-'0')
	}
	*out = value
	return 1, nil
}

type entityError string

func (e entityError) Error() string { return string(e) }

var errInvalidEntity = entityError("invalid entity")
