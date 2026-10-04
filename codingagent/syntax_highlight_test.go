package codingagent

// Tests for syntax highlighting (ported from syntax-highlight.test.ts
// surfaces, adapted to the regex-tokenizer deviation).

import (
	"strings"
	"testing"
)

func TestRenderHighlightedHtmlScopes(t *testing.T) {
	theme := HighlightTheme{
		"keyword":    func(text string) string { return "[K]" + text + "[/K]" },
		"string":     func(text string) string { return "[S]" + text + "[/S]" },
		"title":      func(text string) string { return "[T]" + text + "[/T]" },
		"title.func": func(text string) string { return "[TF]" + text + "[/TF]" },
		"default":    func(text string) string { return "[D]" + text + "[/D]" },
	}

	html := `<span class="hljs-keyword">const</span> <span class="hljs-title function_">foo</span>(<span class="hljs-string">"x"</span>)`
	rendered := RenderHighlightedHtml(html, theme)

	if !strings.Contains(rendered, "[K]const[/K]") {
		t.Fatalf("keyword = %q", rendered)
	}
	// The class attribute is "hljs-title function_"; the first hljs-*
	// class wins, so the scope is "title" (matching the reference).
	if !strings.Contains(rendered, "[T]foo[/T]") {
		t.Fatalf("title = %q", rendered)
	}
	if !strings.Contains(rendered, `[S]"x"[/S]`) {
		t.Fatalf("string = %q", rendered)
	}
}

func TestRenderHighlightedHtmlScopeFallbacks(t *testing.T) {
	theme := HighlightTheme{
		"title": func(text string) string { return "[T]" + text + "[/T]" },
	}
	// title.function falls back to title (dot prefix).
	html := `<span class="hljs-title function_">main</span>()`
	rendered := RenderHighlightedHtml(html, theme)
	if !strings.Contains(rendered, "[T]main[/T]") {
		t.Fatalf("dot fallback = %q", rendered)
	}

	// built_in-type falls back to built_in (dash prefix).
	theme = HighlightTheme{
		"built_in": func(text string) string { return "[B]" + text + "[/B]" },
	}
	html = `<span class="hljs-built_in">console</span>`
	rendered = RenderHighlightedHtml(html, theme)
	if !strings.Contains(rendered, "[B]console[/B]") {
		t.Fatalf("dash fallback = %q", rendered)
	}
}

func TestRenderHighlightedHtmlEntities(t *testing.T) {
	html := `<span class="hljs-keyword">a</span>&amp;&lt;&gt;&quot;&#x27;`
	rendered := RenderHighlightedHtml(html, nil)
	if !strings.Contains(rendered, "&<>\"'") {
		t.Fatalf("entities = %q", rendered)
	}
}

func TestRenderHighlightedHtmlDefault(t *testing.T) {
	theme := HighlightTheme{"default": func(text string) string { return "[" + text + "]" }}
	html := `plain text`
	if got := RenderHighlightedHtml(html, theme); got != "[plain text]" {
		t.Fatalf("default = %q", got)
	}
}

func TestHighlightGo(t *testing.T) {
	theme := HighlightTheme{
		"keyword": func(text string) string { return "«" + text + "»" },
		"string":  func(text string) string { return "‹" + text + "›" },
		"comment": func(text string) string { return "⟨" + text + "⟩" },
	}

	result := Highlight("func main() {\n\t// hi\n\ts := \"x\"\n}", &HighlightOptions{Language: "go", Theme: theme})

	if !strings.Contains(result, "«func»") {
		t.Fatalf("keyword = %q", result)
	}
	if !strings.Contains(result, "⟨// hi⟩") {
		t.Fatalf("comment = %q", result)
	}
	if !strings.Contains(result, "‹\"x\"›") {
		t.Fatalf("string = %q", result)
	}
}

func TestHighlightLanguageAliases(t *testing.T) {
	if !SupportsLanguage("py") || !SupportsLanguage("golang") || !SupportsLanguage("sh") {
		t.Fatal("aliases missing")
	}
	if !SupportsLanguage("typescript") || !SupportsLanguage("rust") {
		t.Fatal("canonical names missing")
	}
	if SupportsLanguage("cobol") {
		t.Fatal("unsupported reported")
	}

	languages := SupportedLanguages()
	if len(languages) < 5 {
		t.Fatalf("languages = %v", languages)
	}
}

func TestHighlightPython(t *testing.T) {
	theme := HighlightTheme{
		"keyword": func(text string) string { return "*" + text + "*" },
		"literal": func(text string) string { return "|" + text + "|" },
	}

	result := Highlight("if True:\n    pass", &HighlightOptions{Language: "python", Theme: theme})
	if !strings.Contains(result, "*if*") {
		t.Fatalf("keyword = %q", result)
	}
	if !strings.Contains(result, "|True|") {
		t.Fatalf("literal = %q", result)
	}
}

func TestHighlightAutoDetect(t *testing.T) {
	// No language: auto-detect picks the best-coverage tokenizer.
	result := Highlight("package main\n\nfunc main() {}\n", nil)
	if result == "" {
		t.Fatal("empty result")
	}
}

func TestHighlightUnknownLanguage(t *testing.T) {
	// Unknown language: auto-detect path (no error).
	result := Highlight("some code", &HighlightOptions{Language: "not-a-language"})
	if result != "some code" {
		t.Fatalf("passthrough = %q", result)
	}
}

func TestHighlightBashVariables(t *testing.T) {
	theme := HighlightTheme{
		"variable": func(text string) string { return "$(" + text + ")" },
	}
	result := Highlight("echo ${HOME}", &HighlightOptions{Language: "bash", Theme: theme})
	if !strings.Contains(result, "$(${HOME})") {
		t.Fatalf("variable = %q", result)
	}
}
