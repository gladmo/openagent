package tui

// Ported from pi/packages/tui/test/markdown.test.ts (first batch: transforms,
// lists, tables, spacing, blockquotes, headings, inline styling, links,
// strikethrough, latex interactions). The defaultMarkdownTheme uses chalk
// level-3 ANSI codes mirrored literally.

import (
	"regexp"
	"strings"
	"testing"
)

var ansiSgrPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripAnsi(line string) string {
	return ansiSgrPattern.ReplaceAllString(line, "")
}

// chalk level-3 mirrors of test-themes.ts
var defaultMarkdownTheme = MarkdownTheme{
	Heading:         func(text string) string { return "\x1b[1m\x1b[36m" + text + "\x1b[39m\x1b[22m" },
	Link:            func(text string) string { return "\x1b[34m" + text + "\x1b[39m" },
	LinkUrl:         func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
	Code:            func(text string) string { return "\x1b[33m" + text + "\x1b[39m" },
	CodeBlock:       func(text string) string { return "\x1b[32m" + text + "\x1b[39m" },
	CodeBlockBorder: func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
	Quote:           func(text string) string { return "\x1b[3m" + text + "\x1b[23m" },
	QuoteBorder:     func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
	Hr:              func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
	ListBullet:      func(text string) string { return "\x1b[36m" + text + "\x1b[39m" },
	Bold:            func(text string) string { return "\x1b[1m" + text + "\x1b[22m" },
	Italic:          func(text string) string { return "\x1b[3m" + text + "\x1b[23m" },
	Strikethrough:   func(text string) string { return "\x1b[9m" + text + "\x1b[29m" },
	Underline:       func(text string) string { return "\x1b[4m" + text + "\x1b[24m" },
}

func plainLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = stripAnsi(line)
	}
	return out
}

func trimmedPlain(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(stripAnsi(line), " ")
	}
	return out
}

func anyLineContains(lines []string, text string) bool {
	for _, line := range lines {
		if strings.Contains(line, text) {
			return true
		}
	}
	return false
}

func TestMarkdownTransformCaching(t *testing.T) {
	var calls []struct {
		source         string
		availableWidth int
	}
	renderLatex := true
	markdown := NewMarkdown("source", 2, 0, defaultMarkdownTheme, nil, &MarkdownOptions{
		Transform: func(source string, availableWidth int) string {
			calls = append(calls, struct {
				source         string
				availableWidth int
			}{source, availableWidth})
			return source + " " + itoa(availableWidth)
		},
		RenderLatexOption: &renderLatex,
	})

	got := trimmedPlain(markdown.Render(80))
	if len(got) != 1 || strings.TrimSpace(got[0]) != "source 76" {
		t.Fatalf("render 80 = %q", got)
	}
	markdown.Render(80)
	got = trimmedPlain(markdown.Render(60))
	if strings.TrimSpace(got[0]) != "source 56" {
		t.Fatalf("render 60 = %q", got)
	}
	if len(calls) != 2 || calls[0].availableWidth != 76 || calls[1].availableWidth != 56 {
		t.Fatalf("calls = %+v", calls)
	}

	markdown.SetText("updated")
	got = trimmedPlain(markdown.Render(60))
	if strings.TrimSpace(got[0]) != "updated 56" {
		t.Fatalf("updated = %q", got)
	}
	if calls[len(calls)-1].source != "updated" || calls[len(calls)-1].availableWidth != 56 {
		t.Fatalf("last call = %+v", calls[len(calls)-1])
	}

	markdown.Invalidate()
	markdown.Render(60)
	if len(calls) != 4 {
		t.Fatalf("calls = %d", len(calls))
	}
}

func TestMarkdownSimpleNestedList(t *testing.T) {
	markdown := NewMarkdown("- Item 1\n  - Nested 1.1\n  - Nested 1.2\n- Item 2", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := plainLines(markdown.Render(80))
	for _, want := range []string{"- Item 1", "    - Nested 1.1", "    - Nested 1.2", "- Item 2"} {
		if !anyLineContains(lines, want) {
			t.Errorf("missing %q in %q", want, lines)
		}
	}
}

func TestMarkdownDeeplyNestedList(t *testing.T) {
	markdown := NewMarkdown("- Level 1\n  - Level 2\n    - Level 3\n      - Level 4", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := plainLines(markdown.Render(80))
	for _, want := range []string{"- Level 1", "    - Level 2", "        - Level 3", "            - Level 4"} {
		if !anyLineContains(lines, want) {
			t.Errorf("missing %q in %q", want, lines)
		}
	}
}

func TestMarkdownOrderedNestedList(t *testing.T) {
	markdown := NewMarkdown("1. First\n   1. Nested first\n   2. Nested second\n2. Second", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := plainLines(markdown.Render(80))
	for _, want := range []string{"1. First", "    1. Nested first", "    2. Nested second", "2. Second"} {
		if !anyLineContains(lines, want) {
			t.Errorf("missing %q in %q", want, lines)
		}
	}
}

func TestMarkdownNormalizeOrderedMarkers(t *testing.T) {
	markdown := NewMarkdown("1. alpha\n1. beta\n1. gamma", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := trimmedPlain(markdown.Render(80))
	want := []string{"1. alpha", "2. beta", "3. gamma"}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

func TestMarkdownPreserveOrderedMarkers(t *testing.T) {
	markdown := NewMarkdown("1. alpha\n1. beta", 0, 0, defaultMarkdownTheme, nil, &MarkdownOptions{PreserveOrderedListMarkers: true})
	lines := trimmedPlain(markdown.Render(80))
	if lines[0] != "1. alpha" || lines[1] != "1. beta" {
		t.Fatalf("lines = %q", lines)
	}
}

func TestMarkdownTaskList(t *testing.T) {
	markdown := NewMarkdown("- [ ] todo\n- [x] done", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := plainLines(markdown.Render(80))
	if !anyLineContains(lines, "[ ] todo") {
		t.Fatalf("unchecked = %q", lines)
	}
	if !anyLineContains(lines, "[x] done") {
		t.Fatalf("checked = %q", lines)
	}
}

func TestMarkdownLooseListBlankLines(t *testing.T) {
	markdown := NewMarkdown("- list\n\n- items", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := trimmedPlain(markdown.Render(80))
	// Loose list: blank line between items.
	foundBlank := false
	for _, line := range lines {
		if line == "" {
			foundBlank = true
			break
		}
	}
	if !foundBlank {
		t.Fatalf("no blank line in loose list: %q", lines)
	}
}

func TestMarkdownHeading(t *testing.T) {
	markdown := NewMarkdown("# Title\n\nParagraph after.", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(80)
	if !strings.Contains(lines[0], "\x1b[1m") {
		t.Fatalf("heading missing bold: %q", lines[0])
	}
	if strings.TrimRight(stripAnsi(lines[0]), " ") != "Title" {
		t.Fatalf("heading = %q", stripAnsi(lines[0]))
	}
	// One blank line between heading and paragraph (padded to width).
	if strings.TrimSpace(stripAnsi(lines[1])) != "" {
		t.Fatalf("expected blank after heading, got %q", lines[1])
	}
	if strings.TrimRight(stripAnsi(lines[2]), " ") != "Paragraph after." {
		t.Fatalf("paragraph = %q", stripAnsi(lines[2]))
	}
	// No trailing blank.
	if lines[len(lines)-1] == "" {
		t.Fatalf("trailing blank: %q", lines)
	}
}

func TestMarkdownCodeBlock(t *testing.T) {
	markdown := NewMarkdown("```\nlet x = 1\n```", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := trimmedPlain(markdown.Render(80))
	want := []string{"```", "  let x = 1", "```"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %q", lines)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

func TestMarkdownCodeBlockLang(t *testing.T) {
	markdown := NewMarkdown("```go\nfmt.Println()\n```", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := trimmedPlain(markdown.Render(80))
	if lines[0] != "```go" {
		t.Fatalf("fence = %q", lines[0])
	}
}

func TestMarkdownHr(t *testing.T) {
	markdown := NewMarkdown("---\n\nAfter", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := trimmedPlain(markdown.Render(40))
	if !strings.HasPrefix(lines[0], "────") {
		t.Fatalf("hr = %q", lines[0])
	}
	if plain := stripAnsi(lines[0]); VisibleWidth(plain) != 40 {
		t.Fatalf("hr width = %d (%q)", VisibleWidth(plain), plain)
	}
	if strings.TrimSpace(stripAnsi(lines[1])) != "" {
		t.Fatalf("no blank after hr: %q", lines[1])
	}
	if lines[2] != "After" {
		t.Fatalf("after = %q", lines[2])
	}
	if lines[len(lines)-1] == "" {
		t.Fatalf("trailing blank: %q", lines)
	}
}

func TestMarkdownBlockquote(t *testing.T) {
	markdown := NewMarkdown("> Quoted text\n> More here", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(80)
	if !strings.HasPrefix(lines[0], "\x1b[2m│") {
		t.Fatalf("quote border missing: %q", lines[0])
	}
	if !strings.Contains(stripAnsi(lines[0]), "Quoted text") {
		t.Fatalf("quote text: %q", lines[0])
	}
	if !strings.Contains(stripAnsi(lines[1]), "More here") {
		t.Fatalf("quote continuation: %q", lines[1])
	}
}

func TestMarkdownInlineStyles(t *testing.T) {
	markdown := NewMarkdown("*em* and **strong** and `code`", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(80)
	line := lines[0]
	if !strings.Contains(line, "\x1b[3mem\x1b[23m") {
		t.Fatalf("em missing: %q", line)
	}
	if !strings.Contains(line, "\x1b[1mstrong\x1b[22m") {
		t.Fatalf("strong missing: %q", line)
	}
	if !strings.Contains(line, "\x1b[33mcode\x1b[39m") {
		t.Fatalf("code missing: %q", line)
	}
}

func TestMarkdownStrikethrough(t *testing.T) {
	markdown := NewMarkdown("~~text~~", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(80)
	if !strings.Contains(lines[0], "\x1b[9mtext\x1b[29m") {
		t.Fatalf("del missing: %q", lines[0])
	}
}

func TestMarkdownKeepsTildeAsPlain(t *testing.T) {
	markdown := NewMarkdown("~text~", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(80)
	if strings.TrimRight(stripAnsi(lines[0]), " ") != "~text~" {
		t.Fatalf("single tilde: %q", lines[0])
	}
	if strings.Contains(lines[0], "\x1b[9m") {
		t.Fatalf("unexpected strikethrough: %q", lines[0])
	}
}

func TestMarkdownEscapedPunctuation(t *testing.T) {
	markdown := NewMarkdown(`Escaped \*not em\*`, 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(80)
	if strings.TrimRight(stripAnsi(lines[0]), " ") != `Escaped *not em*` {
		t.Fatalf("escaped = %q", stripAnsi(lines[0]))
	}
}

func TestMarkdownPreserveEscapes(t *testing.T) {
	markdown := NewMarkdown(`Escaped \*not em\*`, 0, 0, defaultMarkdownTheme, nil, &MarkdownOptions{PreserveBackslashEscapes: true})
	lines := markdown.Render(80)
	if strings.TrimRight(stripAnsi(lines[0]), " ") != `Escaped \*not em\*` {
		t.Fatalf("preserved = %q", stripAnsi(lines[0]))
	}
}

func TestMarkdownTableSimple(t *testing.T) {
	markdown := NewMarkdown("| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := plainLines(markdown.Render(40))
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"┌", "│ A │ B │", "│ 1 │ 2 │", "│ 3 │ 4 │", "└"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	// Row dividers between data rows.
	if strings.Count(joined, "├") < 1 || strings.Count(joined, "┼") < 1 {
		t.Errorf("missing row divider:\n%s", joined)
	}
}

func TestMarkdownTableLastBlockNoTrailingBlank(t *testing.T) {
	markdown := NewMarkdown("| A |\n|---|\n| 1 |", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(40)
	if lines[len(lines)-1] == "" {
		t.Fatalf("trailing blank: %q", lines)
	}
}

func TestMarkdownCodeBlockLastBlockNoTrailingBlank(t *testing.T) {
	markdown := NewMarkdown("```\ncode\n```", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(40)
	if lines[len(lines)-1] == "" {
		t.Fatalf("trailing blank: %q", lines)
	}
}

func TestMarkdownParagraphSpacing(t *testing.T) {
	markdown := NewMarkdown("First para.\n\nSecond para.", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := trimmedPlain(markdown.Render(80))
	want := []string{"First para.", "", "Second para."}
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

func TestMarkdownPadding(t *testing.T) {
	markdown := NewMarkdown("hi", 2, 1, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(20)
	if len(lines) != 3 {
		t.Fatalf("lines = %d (%q)", len(lines), lines)
	}
	if stripAnsi(lines[0]) != strings.Repeat(" ", 20) {
		t.Fatalf("top pad = %q", lines[0])
	}
	if !strings.HasPrefix(stripAnsi(lines[1]), "  hi") {
		t.Fatalf("content = %q", lines[1])
	}
}

func TestMarkdownLatexInline(t *testing.T) {
	markdown := NewMarkdown("$x^2$", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(80)
	if strings.TrimRight(stripAnsi(lines[0]), " ") != "x²" {
		t.Fatalf("latex = %q", stripAnsi(lines[0]))
	}
}

func TestMarkdownLatexDisabled(t *testing.T) {
	renderLatex := false
	markdown := NewMarkdown("$x^2$", 0, 0, defaultMarkdownTheme, nil, &MarkdownOptions{RenderLatexOption: &renderLatex})
	lines := markdown.Render(80)
	if strings.TrimRight(stripAnsi(lines[0]), " ") != "$x^2$" {
		t.Fatalf("raw = %q", stripAnsi(lines[0]))
	}
}

func TestMarkdownLatexBlock(t *testing.T) {
	markdown := NewMarkdown("$$x = y$$", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(80)
	if strings.TrimRight(stripAnsi(lines[0]), " ") != "x = y" {
		t.Fatalf("latex block = %q", stripAnsi(lines[0]))
	}
}

func TestMarkdownLatexPendingStaysRaw(t *testing.T) {
	markdown := NewMarkdown("$$\\frac{1}{", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(80)
	if !strings.Contains(stripAnsi(lines[0]), "$$") {
		t.Fatalf("pending raw = %q", lines[0])
	}
}

func TestMarkdownCurrencyNotMath(t *testing.T) {
	markdown := NewMarkdown("Costs $5 and $10 total", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(80)
	if !strings.Contains(stripAnsi(lines[0]), "$5 and $10") {
		t.Fatalf("currency = %q", lines[0])
	}
}

func TestMarkdownLinkFallbacks(t *testing.T) {
	savedCaps := GetCapabilities()
	defer SetCapabilities(savedCaps)

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolNone, TrueColor: false, Hyperlinks: false})

	// Autolinked email: no mailto: prefix shown.
	markdown := NewMarkdown("Contact user@example.com for help", 0, 0, defaultMarkdownTheme, nil, nil)
	joined := strings.Join(plainLines(markdown.Render(80)), " ")
	if !strings.Contains(joined, "user@example.com") || strings.Contains(joined, "mailto:") {
		t.Fatalf("email = %q", joined)
	}

	// Bare URL appears once.
	markdown = NewMarkdown("Visit https://example.com for more", 0, 0, defaultMarkdownTheme, nil, nil)
	joined = strings.Join(plainLines(markdown.Render(80)), " ")
	if got := strings.Count(joined, "https://example.com"); got != 1 {
		t.Fatalf("url count = %d in %q", got, joined)
	}

	// URL in parentheses when text differs from href.
	markdown = NewMarkdown("[click here](https://example.com)", 0, 0, defaultMarkdownTheme, nil, nil)
	joined = strings.Join(plainLines(markdown.Render(80)), " ")
	if !strings.Contains(joined, "click here") || !strings.Contains(joined, "(https://example.com)") {
		t.Fatalf("fallback = %q", joined)
	}

	// mailto URL in parentheses.
	markdown = NewMarkdown("[Email me](mailto:test@example.com)", 0, 0, defaultMarkdownTheme, nil, nil)
	joined = strings.Join(plainLines(markdown.Render(80)), " ")
	if !strings.Contains(joined, "Email me") || !strings.Contains(joined, "(mailto:test@example.com)") {
		t.Fatalf("mailto fallback = %q", joined)
	}
}

func stripOsc8(line string) string {
	runes := []rune(line)
	var out []rune
	i := 0
	for i < len(runes) {
		if strings.HasPrefix(string(runes[i:]), "\x1b]8;;") {
			end := strings.Index(string(runes[i:]), "\x1b\\")
			if end >= 0 {
				i += end + 2
				continue
			}
		}
		out = append(out, runes[i])
		i++
	}
	return string(out)
}

func TestMarkdownHyperlinkOsc8(t *testing.T) {
	savedCaps := GetCapabilities()
	defer SetCapabilities(savedCaps)

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolNone, TrueColor: false, Hyperlinks: true})

	markdown := NewMarkdown("[click here](https://example.com)", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(80)
	joined := strings.Join(lines, "")
	if !strings.Contains(joined, "\x1b]8;;https://example.com\x1b\\") {
		t.Fatalf("OSC 8 open missing: %q", joined)
	}
	if !strings.Contains(joined, "\x1b]8;;\x1b\\") {
		t.Fatalf("OSC 8 close missing: %q", joined)
	}
	var rawPlain []string
	for _, line := range lines {
		rawPlain = append(rawPlain, stripAnsi(stripOsc8(line)))
	}
	if !strings.Contains(strings.Join(rawPlain, ""), "click here") {
		t.Fatalf("link text missing: %q", rawPlain)
	}
	if strings.Contains(strings.Join(rawPlain, ""), "(https://example.com)") {
		t.Fatalf("URL inline in parentheses: %q", rawPlain)
	}

	// mailto via OSC 8.
	markdown = NewMarkdown("[Email me](mailto:test@example.com)", 0, 0, defaultMarkdownTheme, nil, nil)
	joined = strings.Join(markdown.Render(80), "")
	if !strings.Contains(joined, "\x1b]8;;mailto:test@example.com\x1b\\") {
		t.Fatalf("mailto OSC 8 missing: %q", joined)
	}
}

func TestMarkdownEscapedQuote(t *testing.T) {
	markdown := NewMarkdown(`"\"`, 0, 0, defaultMarkdownTheme, nil, nil)
	lines := trimmedPlain(markdown.Render(80))
	if lines[0] != `""` {
		t.Fatalf("normalized = %q", lines[0])
	}

	markdown = NewMarkdown(`"\"`, 0, 0, defaultMarkdownTheme, nil, &MarkdownOptions{PreserveBackslashEscapes: true})
	lines = trimmedPlain(markdown.Render(80))
	if lines[0] != `"\"` {
		t.Fatalf("preserved = %q", lines[0])
	}
}

func TestMarkdownThinkingStylePrefix(t *testing.T) {
	// Thinking traces use a gray-italic default style; inline code resets
	// must restore it.
	grayItalic := &DefaultTextStyle{
		Color:  func(text string) string { return "\x1b[90m" + text + "\x1b[39m" },
		Italic: true,
	}
	markdown := NewMarkdown("This is thinking with `inline code` and more text after", 1, 0, defaultMarkdownTheme, grayItalic, nil)
	lines := markdown.Render(80)
	plain := stripAnsi(lines[0])
	if !strings.Contains(plain, "This is thinking with") {
		t.Fatalf("prefix text missing: %q", plain)
	}
	if !strings.Contains(plain, "and more text after") {
		t.Fatalf("suffix text missing: %q", plain)
	}
	// The reference asserts code presence + gray(90) + italic(3) codes in
	// the joined output (the exact reset ordering is chalk's).
	joinedOutput := strings.Join(lines, "\n")
	if !strings.Contains(joinedOutput, "inline code") {
		t.Fatalf("code text missing: %q", joinedOutput)
	}
	if !strings.Contains(joinedOutput, "\x1b[90m") {
		t.Fatalf("gray code missing: %q", joinedOutput)
	}
	if !strings.Contains(joinedOutput, "\x1b[3m") {
		t.Fatalf("italic code missing: %q", joinedOutput)
	}
	if !strings.Contains(joinedOutput, "\x1b[33m") {
		t.Fatalf("code color missing: %q", joinedOutput)
	}
}

func TestMarkdownBoldStylePrefix(t *testing.T) {
	gray := &DefaultTextStyle{Color: func(text string) string { return "\x1b[90m" + text + "\x1b[39m" }}
	markdown := NewMarkdown("before **bold** after", 1, 0, defaultMarkdownTheme, gray, nil)
	lines := markdown.Render(80)
	joinedOutput := strings.Join(lines, "\n")
	if !strings.Contains(joinedOutput, "\x1b[1m") {
		t.Fatalf("bold missing: %q", joinedOutput)
	}
	if !strings.Contains(joinedOutput, "\x1b[90m") {
		t.Fatalf("gray after bold missing: %q", joinedOutput)
	}
	if !strings.Contains(stripAnsi(lines[0]), "after") {
		t.Fatalf("suffix text missing: %q", lines[0])
	}
}

func TestMarkdownTableAlignment(t *testing.T) {
	markdown := NewMarkdown("| Left | Center | Right |\n|:-----|:------:|------:|\n| 1 | 2 | 3 |", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := plainLines(markdown.Render(60))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Left") || !strings.Contains(joined, "Center") || !strings.Contains(joined, "Right") {
		t.Fatalf("headers = %q", joined)
	}
	if !strings.Contains(joined, "1") || !strings.Contains(joined, "2") || !strings.Contains(joined, "3") {
		t.Fatalf("cells = %q", joined)
	}
}

func TestMarkdownTableWraps(t *testing.T) {
	long := strings.Repeat("word ", 30)
	markdown := NewMarkdown("| A |\n|---|\n| "+long+" |", 0, 0, defaultMarkdownTheme, nil, nil)
	lines := markdown.Render(30)
	for _, line := range lines {
		if IsImageLine(line) {
			continue
		}
		if w := VisibleWidth(line); w > 30 {
			t.Fatalf("overflow %d: %q", w, stripAnsi(line))
		}
	}
	// Borders appear on each wrapped row.
	borderCount := 0
	for _, line := range lines {
		if strings.Contains(line, "│") {
			borderCount++
		}
	}
	if borderCount < 2 {
		t.Fatalf("borders = %d in %q", borderCount, lines)
	}
}

// TestMarkdownZeroThemeRendersAllBlockForms pins the zero-value theme
// contract promised by NewMarkdown: every block form must render without a
// nil style function. The list case crashed a live TUI (nil ListBullet).
func TestMarkdownZeroThemeRendersAllBlockForms(t *testing.T) {
	source := strings.Join([]string{
		"# Heading",
		"",
		"- item one",
		"- item two",
		"  - nested item",
		"",
		"1. first",
		"2. second",
		"",
		"> quoted text",
		"",
		"```go",
		"fmt.Println(\"hi\")",
		"```",
		"",
		"---",
		"",
		"[link](https://example.com) with `inline code` and **bold**",
		"",
		"| a | b |",
		"| - | - |",
		"| 1 | 2 |",
	}, "\n")

	markdown := NewMarkdown(source, 0, 0, MarkdownTheme{}, nil, nil)
	lines := markdown.Render(60)
	if len(lines) == 0 {
		t.Fatal("empty render")
	}

	joined := stripAnsi(strings.Join(lines, "\n"))
	for _, want := range []string{
		"Heading", "item one", "nested item", "first", "quoted text",
		"Println", "link", "inline code", "bold", "1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("zero-theme render missing %q:\n%s", want, joined)
		}
	}
}

// Port of the unclosed-display-math table cases (#11159).
func TestMarkdownUnclosedDisplayMathBeforeTables(t *testing.T) {
	table := []string{"| A | B |", "|---|---|", "| 1 | 2 |"}
	renderedTable := []string{"┌───┬───┐", "│ A │ B │", "├───┼───┤", "│ 1 │ 2 │", "└───┴───┘"}
	render := func(source string) []string {
		markdown := NewMarkdown(source, 0, 0, defaultMarkdownTheme, nil, nil)
		lines := markdown.Render(80)
		out := make([]string, len(lines))
		for i, line := range lines {
			out[i] = strings.TrimRight(stripAnsi(line), " ")
		}
		return out
	}

	got := render("Intro\n\n$$\n\n" + strings.Join(table, "\n") + "\n\nAfter")
	want := []string{"Intro", "", "$$", ""}
	want = append(want, renderedTable...)
	want = append(want, "", "After")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("orphan $$:\n%q\nwant\n%q", got, want)
	}

	got = render("$$\nx = 1\n\n" + strings.Join(table, "\n"))
	want = append([]string{"$$", "x = 1", ""}, renderedTable...)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("math-like $$:\n%q\nwant\n%q", got, want)
	}

	got = render("Intro\n\n$$\nx = 1\n\n" + strings.Join(table, "\n") + "\n\n$$\ny^2\n$$")
	want = []string{"Intro", "", "$$", "x = 1", ""}
	want = append(want, renderedTable...)
	want = append(want, "", "y²")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("no pairing across a table:\n%q\nwant\n%q", got, want)
	}

	// Pipe-free single-column delimiter row still gates.
	if !mdContainsTableStart("| A |\n:---:\n| 1 |") {
		t.Fatal("pipe-free delimiter not detected")
	}
	if !mdContainsTableStart("Value\n| --- |\n1") {
		t.Fatal("pipe-free header not detected")
	}
	if mdContainsTableStart("plain text\nno table here") {
		t.Fatal("table falsely detected")
	}
}

// Port of the CJK emphasis block (b2d0da6fe / #10730): emphasis delimited
// next to CJK punctuation renders, ASCII punctuation keeps CommonMark
// flanking.
func TestMarkdownCjkEmphasis(t *testing.T) {
	// The renderer measures styled output by visible columns, so theme
	// functions must not change the visible width; assert on the default
	// SGR markers instead of a tag theme.
	render := func(text string) string {
		markdown := NewMarkdown(text, 0, 0, defaultMarkdownTheme, nil, nil)
		lines := markdown.Render(80)
		return strings.TrimRight(strings.Join(lines, ""), " ")
	}
	bold := func(text string) string { return "\x1b[1m" + text + "\x1b[22m" }
	italic := func(text string) string { return "\x1b[3m" + text + "\x1b[23m" }
	cases := []struct{ in, want string }{
		{"**这是测试。**后面", bold("这是测试。") + "后面"},
		{"**一句话说清：**一台机器", bold("一句话说清：") + "一台机器"},
		{"**要点到此为止。**20 小时", bold("要点到此为止。") + "20 小时"},
		{"字**「引用」**字", "字" + bold("「引用」") + "字"},
		{"*斜体。*后面", italic("斜体。") + "后面"},
		{"**end.**Then next", "**end.**Then next"},
	}
	for _, tc := range cases {
		if got := render(tc.in); got != tc.want {
			t.Fatalf("render(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Strikethrough over multibyte content: the width fix (review of d684400)
// keeps the source slicing on byte boundaries.
func TestMarkdownStrictDelMultibyte(t *testing.T) {
	theme := defaultMarkdownTheme
	theme.Strikethrough = func(text string) string { return "<s>" + text + "</s>" }
	markdown := NewMarkdown("~~删除~~x", 0, 0, theme, nil, nil)
	lines := markdown.Render(40)
	got := strings.Join(lines, "")
	if !strings.Contains(got, "<s>删除</s>") || !strings.Contains(got, "x") || strings.Contains(got, "~~") {
		t.Fatalf("strike = %q", got)
	}
	// A rejected __ run renders literally as a whole (no em re-match).
	markdown = NewMarkdown("__end.__Then next", 0, 0, defaultMarkdownTheme, nil, nil)
	got = strings.Join(markdown.Render(40), "")
	if strings.Contains(got, "\x1b[3m") || !strings.Contains(got, "__end.__Then") {
		t.Fatalf("underscore run = %q", got)
	}
}
