package tui

// Inline markdown lexer: escapes, codespans, latex (inline), emphasis,
// strong, strikethrough, links (inline + autolink + bare GFM), breaks.

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	mdEscapePattern   = regexp.MustCompile(`^\\([!"#$%&'()*+,\-./:;<=>?@\[\\\]^_` + "`" + `{|}~])`)
	mdAutolinkPattern = regexp.MustCompile(`^<(?:[a-zA-Z][a-zA-Z0-9+.-]{1,31}:[^<>\x00-\x20]*|([^<>@\s]+@[^<>@\s]+\.[^<>@\s]+))>`)
	mdEmailPattern    = regexp.MustCompile(`[a-zA-Z0-9._+-]+@[a-zA-Z0-9-]+(?:\.[a-zA-Z0-9-]+)*\.[a-zA-Z]{2,}`)
	mdBareUrlPattern  = regexp.MustCompile(`\b(?:https?|ftp):\/\/[^\s<>\x00-\x20]+[^\s<>\x00-\x20.,:;'"!)\]}?]`)
	mdBrPattern       = regexp.MustCompile(`^ {2,}\n`)
)

// mdCodespanMatch matches a backtick-delimited codespan manually (Go RE2
// has no backreferences): returns the matched raw text.
func mdCodespanMatch(src string) (string, bool) {
	if len(src) == 0 || src[0] != '`' {
		return "", false
	}
	opens := 0
	for opens < len(src) && src[opens] == '`' {
		opens++
	}
	// Find a closing run of exactly `opens` backticks (not longer).
	search := opens
	for search < len(src) {
		if src[search] == '`' {
			closes := 0
			for search+closes < len(src) && src[search+closes] == '`' {
				closes++
			}
			if closes == opens {
				return src[:search+closes], true
			}
			if closes > opens {
				// Longer run: skip past it (marked treats as literal text
				// but the loose form still matches; use the full span).
				search += closes
				continue
			}
			search += closes
			continue
		}
		search++
	}
	// Loose form: no closing run; unmatched backticks render literally.
	return "", false
}

// mdLexInline tokenizes inline markdown content.
func mdLexInline(src string) []MdToken {
	var tokens []MdToken
	src = strings.ReplaceAll(src, "\r\n", "\n")
	rest := src

	for len(rest) > 0 {
		// Line break (two+ spaces before newline).
		if match := mdBrPattern.FindString(rest); match != "" {
			tokens = append(tokens, MdBr{})
			rest = rest[len(match):]
			continue
		}

		// Escaped punctuation.
		if match := mdEscapePattern.FindStringSubmatch(rest); match != nil {
			tokens = append(tokens, MdEscape{Text: match[1], Raw: match[0]})
			rest = rest[len(match[0]):]
			continue
		}

		// Codespan.
		if strings.HasPrefix(rest, "`") {
			if match, ok := mdCodespanMatch(rest); ok {
				content := mdCodespanContent(match)
				tokens = append(tokens, MdCodespan{Text: content})
				rest = rest[len(match):]
				continue
			}
		}

		// Inline latex ($$..$$, \(..\), \[..\], $..$).
		if latex := mdLexInlineLatex(rest); latex != nil {
			tokens = append(tokens, *latex)
			rest = rest[len(latex.Raw):]
			continue
		}

		// Autolink <scheme:...> or <user@host>.
		if strings.HasPrefix(rest, "<") {
			if match := mdAutolinkPattern.FindStringSubmatch(rest); match != nil {
				if match[1] != "" {
					tokens = append(tokens, mdPlainLinkTokens("mailto:"+match[1], match[1]))
				} else {
					target := match[0][1 : len(match[0])-1]
					tokens = append(tokens, mdPlainLinkTokens(target, target))
				}
				rest = rest[len(match[0]):]
				continue
			}
		}

		// Image or link [text](href "title").
		if strings.HasPrefix(rest, "[") || strings.HasPrefix(rest, "![") {
			if linkText, href, width, ok := mdMatchLink(rest); ok {
				isImage := strings.HasPrefix(rest, "!")
				if isImage {
					// Images render as alt text (terminal).
					tokens = append(tokens, MdText{Text: linkText})
				} else {
					tokens = append(tokens, MdLink{
						Href:   href,
						Text:   linkText,
						Tokens: mdLexInline(linkText),
					})
				}
				rest = rest[width:]
				continue
			}
		}

		// Strikethrough (strict): ~~text~~.
		if strings.HasPrefix(rest, "~~") {
			if content, width := mdMatchStrictDel(rest); width > 0 {
				tokens = append(tokens, MdDel{Tokens: mdLexInline(content)})
				rest = rest[width:]
				continue
			}
		}

		// Strong **text** / __text__.
		if content, width, ok := mdMatchStrong(rest); ok {
			tokens = append(tokens, MdStrong{Tokens: mdLexInline(content)})
			rest = rest[width:]
			continue
		} else if literal, width := mdLiteralRejectedStrong(rest); width > 0 {
			// A ** run that failed flanking renders literally (marked
			// consumes the whole delimiter run, so em cannot re-match a
			// subset of it).
			tokens = append(tokens, MdText{Text: literal})
			rest = rest[width:]
			continue
		}

		// Emphasis *text* / _text_.
		if content, width, ok := mdMatchEm(rest); ok {
			tokens = append(tokens, MdEm{Tokens: mdLexInline(content)})
			rest = rest[width:]
			continue
		}

		// Plain text run: up to the next special character or end.
		special := strings.IndexAny(rest, "*_~`\\[<!$\n")
		if special < 0 {
			special = len(rest)
		}
		if special == 0 {
			special = 1
		}
		// Bare URLs / emails link when they start the run.
		if linkToken, consumed := mdMatchBareLink(rest); consumed > 0 {
			if linkToken != nil {
				tokens = append(tokens, linkToken)
			}
			rest = rest[consumed:]
			continue
		}
		tokens = append(tokens, MdText{Text: rest[:special]})
		rest = rest[special:]
	}

	return mdMergeTextTokens(tokens)
}

func mdMergeTextTokens(tokens []MdToken) []MdToken {
	var merged []MdToken
	for _, token := range tokens {
		if text, isText := token.(MdText); isText && len(merged) > 0 {
			if prevText, ok := merged[len(merged)-1].(MdText); ok {
				prevText.Text += text.Text
				merged[len(merged)-1] = prevText
				continue
			}
		}
		merged = append(merged, token)
	}
	return merged
}

func mdCodespanContent(match string) string {
	// Strip the opening/closing backtick runs and one leading+trailing
	// space each when both are present (marked semantics).
	opens := 0
	for opens < len(match) && match[opens] == '`' {
		opens++
	}
	content := match[opens : len(match)-opens]
	if strings.HasPrefix(content, " ") && strings.HasSuffix(content, " ") && strings.TrimSpace(content) != "" {
		content = content[1 : len(content)-1]
	}
	if strings.Contains(content, "\n") {
		content = strings.ReplaceAll(content, "\n", " ")
	}
	return content
}

// mdMatchStrictDel matches ~~text~~ with the strict rules (Go RE2 has no
// lookahead): opening not followed by whitespace/~, closing not preceded by
// backslash, content non-empty, and the char after the closing ~~ is not ~.
func mdMatchStrictDel(src string) (string, int) {
	if !strings.HasPrefix(src, "~~") {
		return "", 0
	}
	rest := src[2:]
	if rest == "" || rest[0] == '~' || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' {
		return "", 0
	}
	runes := []rune(rest)
	var content []rune
	i := 0
	for i < len(runes) {
		if runes[i] == '\\' && i+1 < len(runes) {
			content = append(content, runes[i], runes[i+1])
			i += 2
			continue
		}
		if runes[i] == '~' && i+1 < len(runes) && runes[i+1] == '~' {
			// Closing delimiter: content must not end with whitespace/~.
			if len(content) == 0 || content[len(content)-1] == ' ' || content[len(content)-1] == '\t' ||
				content[len(content)-1] == '\n' || content[len(content)-1] == '~' {
				return "", 0
			}
			// The character after must not be ~.
			if i+2 < len(runes) && runes[i+2] == '~' {
				return "", 0
			}
			return string(content), 2 + i + 2
		}
		content = append(content, runes[i])
		i++
	}
	return "", 0
}

func mdMatchStrong(src string) (string, int, bool) {
	for _, marker := range []string{"**", "__"} {
		if !strings.HasPrefix(src, marker) {
			continue
		}
		content, _, ok := mdMatchDelimited(src, marker)
		if ok {
			return content, len(marker) + len(content) + len(marker), true
		}
	}
	return "", 0, false
}

func mdMatchEm(src string) (string, int, bool) {
	for _, marker := range []string{"*", "_"} {
		if !strings.HasPrefix(src, marker) {
			continue
		}
		content, _, ok := mdMatchDelimited(src, marker)
		if ok {
			return content, len(marker) + len(content) + len(marker), true
		}
	}
	return "", 0, false
}

// mdMatchDelimited matches a delimiter run with marked-ish emphasis rules:
// content is non-empty, opening not followed by whitespace, closing not
// preceded by whitespace; '_' does not intraword-emphasize.
func mdMatchDelimited(src, marker string) (string, int, bool) {
	if len(src) < len(marker)*2+1 {
		return "", 0, false
	}
	runes := []rune(src)
	markerRunes := []rune(marker)
	mLen := len(markerRunes)
	// Reject longer runs (*** = strong + em handled by strong-first).
	for i := 0; i < mLen; i++ {
		if runes[i] != markerRunes[i] {
			return "", 0, false
		}
	}
	if len(runes) > mLen && runes[mLen] == markerRunes[0] {
		// Run longer than the marker: let the caller's stronger marker
		// handle it; a bare run renders literally.
		return "", 0, false
	}
	depth := 1
	i := mLen
	var content []rune
	for i < len(runes) {
		if runes[i] == '\\' {
			content = append(content, runes[i])
			i++
			if i < len(runes) {
				content = append(content, runes[i])
				i++
			}
			continue
		}
		if runes[i] == markerRunes[0] && i+mLen <= len(runes) {
			matches := true
			for j := 0; j < mLen; j++ {
				if runes[i+j] != markerRunes[j] {
					matches = false
					break
				}
			}
			if matches {
				depth--
				if depth == 0 {
					// Closing checks: not preceded by whitespace.
					if len(content) == 0 || content[len(content)-1] == ' ' || content[len(content)-1] == '\n' {
						return "", 0, false
					}
					// Intraword restriction for _.
					if marker == "_" {
						leftWord := len(content) > 0 && mdIsWordRune(content[len(content)-1])
						rightWord := i+mLen < len(runes) && mdIsWordRune(runes[i+mLen])
						if leftWord && rightWord {
							return "", 0, false
						}
					}
					// CJK-friendly flanking (#10730): a closer preceded by
					// non-CJK punctuation and followed by a word rune is
					// intraword and stays literal, while CJK punctuation
					// counts like whitespace and stays a valid closer.
					if i+mLen < len(runes) && mdIsWordRune(runes[i+mLen]) && len(content) > 0 {
						before := content[len(content)-1]
						if mdIsPunctRune(before) && !CjkPunctuationRune(before) {
							return "", 0, false
						}
					}
					nextIdx := i + mLen
					_ = nextIdx
					return string(content), i + mLen, true
				}
				content = append(content, runes[i:i+mLen]...)
				i += mLen
				continue
			}
		}
		content = append(content, runes[i])
		i++
	}
	return "", 0, false
}

func mdIsWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// mdIsPunctRune reports ASCII/CommonMark punctuation.
func mdIsPunctRune(r rune) bool {
	if r < 127 {
		return strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", r)
	}
	return unicode.IsPunct(r)
}

func mdLinkTokens(href, text string) MdToken {
	return MdLink{Href: href, Text: text, Tokens: mdLexInline(text)}
}

// mdMatchBareLink recognizes a GFM bare URL or email at the START of src.
// The link text is a plain text token: re-lexing the URL would match the
// bare-link pattern again (infinite recursion).
func mdMatchBareLink(src string) (MdToken, int) {
	if match := mdEmailPattern.FindStringIndex(src); match != nil && match[0] == 0 {
		email := src[:match[1]]
		return mdPlainLinkTokens("mailto:"+email, email), match[1]
	}
	if match := mdBareUrlPattern.FindStringIndex(src); match != nil && match[0] == 0 {
		url := src[:match[1]]
		return mdPlainLinkTokens(url, url), match[1]
	}
	return nil, 0
}

func mdPlainLinkTokens(href, text string) MdToken {
	return MdLink{Href: href, Text: text, Tokens: []MdToken{MdText{Text: text}}}
}

var (
	mdInlineLatexDollarSpace = regexp.MustCompile(`^\$\s`)
	mdInlineTrailingSpace    = regexp.MustCompile(`\s$`)
	mdInlineLeadingDigit     = regexp.MustCompile(`^\d`)
	mdInlineConstantLike     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*(?:[^A-Za-z0-9_\s])?$`)
	mdInlineIdentNext        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
)

// mdLexInlineLatex tokenizes inline latex at the start of src.
func mdLexInlineLatex(src string) *MdLatex {
	var opening, closing string
	switch {
	case strings.HasPrefix(src, "$$"):
		opening, closing = "$$", "$$"
	case strings.HasPrefix(src, "\\("):
		opening, closing = "\\(", "\\)"
	case strings.HasPrefix(src, "\\["):
		opening, closing = "\\[", "\\]"
	case strings.HasPrefix(src, "$") && !mdInlineLatexDollarSpace.MatchString(src):
		opening, closing = "$", "$"
	default:
		return nil
	}

	closingIndex := mdFindClosingDelimiter(src, closing, len(opening))
	if closingIndex >= 0 && opening == "$" {
		inner := src[len(opening):closingIndex]
		after := src[closingIndex+len(closing):]
		if mdInlineTrailingSpace.MatchString(inner) ||
			mdInlineLeadingDigit.MatchString(after) ||
			(mdInlineConstantLike.MatchString(inner) && mdInlineIdentNext.MatchString(after)) ||
			strings.Contains(inner, "`") {
			return nil
		}
	}

	if closingIndex < 0 {
		pendingSource := src[len(opening):]
		if strings.HasPrefix(opening, "\\") || mdLooksLikePendingDollarMath(pendingSource) {
			return &MdLatex{Text: pendingSource, Raw: src, Pending: true}
		}
		return nil
	}

	text := src[len(opening):closingIndex]
	if text == "" || strings.Contains(text, "\n") {
		return nil
	}

	raw := src[:closingIndex+len(closing)]
	return &MdLatex{Text: text, Raw: raw}
}

// mdMatchLink matches [text](href "title") / ![alt](src) manually: the JS
// regex's capture semantics do not translate cleanly to RE2.
func mdMatchLink(src string) (linkText, href string, width int, ok bool) {
	if !strings.HasPrefix(src, "[") && !strings.HasPrefix(src, "![") {
		return "", "", 0, false
	}
	isImage := strings.HasPrefix(src, "!")
	body := src
	if isImage {
		body = src[1:]
	}
	if !strings.HasPrefix(body, "[") {
		return "", "", 0, false
	}
	// Find the closing ] of the link text (bracket-aware).
	depth := 0
	i := 1
	textEnd := -1
	runes := []rune(body)
	for i < len(runes) {
		switch runes[i] {
		case '\\':
			i++
		case '[':
			depth++
		case ']':
			if depth == 0 {
				textEnd = i
			} else {
				depth--
			}
		}
		if textEnd >= 0 {
			break
		}
		i++
	}
	if textEnd < 0 || textEnd+1 >= len(runes) || runes[textEnd+1] != '(' {
		return "", "", 0, false
	}
	linkText = string(runes[1:textEnd])
	// Parse the (...) destination.
	j := textEnd + 2
	if j < len(runes) && runes[j] == '<' {
		j++
		end := strings.Index(string(runes[j:]), ">")
		if end < 0 {
			return "", "", 0, false
		}
		href = string(runes[j : j+end])
		j += end + 1
	} else {
		start := j
		paren := 0
		for j < len(runes) {
			if runes[j] == '\\' {
				j++
			} else if runes[j] == '(' {
				paren++
			} else if runes[j] == ')' {
				if paren == 0 {
					break
				}
				paren--
			} else if runes[j] == ' ' || runes[j] == '\t' || runes[j] == '\n' {
				break
			}
			j++
		}
		href = string(runes[start:j])
	}
	// Skip optional title + whitespace up to the closing ).
	for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t' || runes[j] == '\n') {
		j++
	}
	if j < len(runes) && (runes[j] == '"' || runes[j] == '\'' || runes[j] == '(') {
		open := runes[j]
		closing := rune(')')
		if open == '"' || open == '\'' {
			closing = open
		}
		j++
		for j < len(runes) && runes[j] != rune(closing) {
			j++
		}
		if j < len(runes) {
			j++
		}
	}
	for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t' || runes[j] == '\n') {
		j++
	}
	if j >= len(runes) || runes[j] != ')' {
		return "", "", 0, false
	}
	consumedRunes := j + 1
	width = len(string(runes[:consumedRunes]))
	if isImage {
		width++
	}
	return linkText, href, width, true
}

// mdLiteralRejectedStrong reports a **...** run whose closer failed
// flanking: marked keeps the entire run literal instead of letting em
// rematch a subset of the delimiters.
func mdLiteralRejectedStrong(src string) (string, int) {
	if !strings.HasPrefix(src, "**") {
		return "", 0
	}
	closing := strings.Index(src[2:], "**")
	if closing < 0 {
		return "", 0
	}
	content := src[2 : 2+closing]
	after := src[2+closing+2:]
	if len(content) == 0 || len(after) == 0 {
		return "", 0
	}
	before := []rune(content)
	last := before[len(before)-1]
	if !mdIsWordRune([]rune(after)[0]) || !mdIsPunctRune(last) || CjkPunctuationRune(last) {
		return "", 0
	}
	width := 2 + closing + 2
	return src[:width], width
}
