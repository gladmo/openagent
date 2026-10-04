package codingagent

// Tests for ANSI-to-HTML conversion and export-color derivation (ported
// from ansi-to-html.test.ts and export surfaces).

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestAnsiToHtmlPlain(t *testing.T) {
	if got := AnsiToHtml("hello world"); got != "hello world" {
		t.Fatalf("plain = %q", got)
	}
}

func TestAnsiToHtmlColors(t *testing.T) {
	// Red foreground (31).
	got := AnsiToHtml("\x1b[31mred\x1b[0m")
	if !strings.Contains(got, `color:#800000`) {
		t.Fatalf("red = %q", got)
	}
	if !strings.Contains(got, ">red</span>") {
		t.Fatalf("text = %q", got)
	}

	// Bright green (92).
	got = AnsiToHtml("\x1b[92mgreen\x1b[0m")
	if !strings.Contains(got, "color:#00ff00") {
		t.Fatalf("bright green = %q", got)
	}

	// Background blue (44).
	got = AnsiToHtml("\x1b[44mbg\x1b[0m")
	if !strings.Contains(got, "background-color:#000080") {
		t.Fatalf("bg = %q", got)
	}
}

func TestAnsiToHtml256Color(t *testing.T) {
	got := AnsiToHtml("\x1b[38;5;196mcolor\x1b[0m")
	if !strings.Contains(got, "color:#ff") {
		t.Fatalf("256 = %q", got)
	}

	// Grayscale index.
	got = AnsiToHtml("\x1b[38;5;240mgray\x1b[0m")
	if !strings.Contains(got, "color:#") {
		t.Fatalf("gray = %q", got)
	}
}

func TestAnsiToHtmlRgb(t *testing.T) {
	got := AnsiToHtml("\x1b[38;2;10;20;30mtext\x1b[0m")
	if !strings.Contains(got, "color:rgb(10,20,30)") {
		t.Fatalf("rgb = %q", got)
	}
}

func TestAnsiToHtmlStyles(t *testing.T) {
	// Bold + italic + underline.
	got := AnsiToHtml("\x1b[1;3;4mstyled\x1b[0m")
	for _, expected := range []string{"font-weight:bold", "font-style:italic", "text-decoration:underline"} {
		if !strings.Contains(got, expected) {
			t.Errorf("missing %q in %q", expected, got)
		}
	}

	// Dim.
	got = AnsiToHtml("\x1b[2mdim\x1b[0m")
	if !strings.Contains(got, "opacity:0.6") {
		t.Fatalf("dim = %q", got)
	}
}

func TestAnsiToHtmlReset(t *testing.T) {
	// Reset clears styles: subsequent plain text has no span.
	got := AnsiToHtml("\x1b[31mred\x1b[0m plain")
	if !strings.Contains(got, ">red</span>") {
		t.Fatalf("red = %q", got)
	}
	if !strings.HasSuffix(got, " plain") {
		t.Fatalf("suffix = %q", got)
	}
	// Exactly one span (reset closed the first).
	if strings.Count(got, "<span") != 1 {
		t.Fatalf("spans = %q", got)
	}
}

func TestAnsiToHtmlEscape(t *testing.T) {
	got := AnsiToHtml("\x1b[31m<a href=\"x\">&'\x1b[0m")
	if !strings.Contains(got, "&lt;a href=&quot;x&quot;&gt;&amp;&#039;") {
		t.Fatalf("escaped = %q", got)
	}
}

func TestAnsiLinesToHtml(t *testing.T) {
	got := AnsiLinesToHtml([]string{"one", "", "\x1b[31mtwo\x1b[0m"})
	if !strings.Contains(got, `<div class="ansi-line">one</div>`) {
		t.Fatalf("one = %q", got)
	}
	if !strings.Contains(got, "&nbsp;") {
		t.Fatalf("empty = %q", got)
	}
	if !strings.Contains(got, "color:#800000") {
		t.Fatalf("colored = %q", got)
	}
}

func TestColor256ToHex(t *testing.T) {
	if got := color256ToHex(0); got != "#000000" {
		t.Fatalf("0 = %q", got)
	}
	if got := color256ToHex(15); got != "#ffffff" {
		t.Fatalf("15 = %q", got)
	}
	// Cube: index 16 = #000000, index 21 = #ffff... check a mid value.
	if got := color256ToHex(196); !strings.HasPrefix(got, "#ff") {
		t.Fatalf("196 = %q", got)
	}
	// Grayscale: 232 = #080808.
	if got := color256ToHex(232); got != "#080808" {
		t.Fatalf("232 = %q", got)
	}
}

func TestDeriveExportColorsDark(t *testing.T) {
	colors := deriveExportColors("#343541")
	if colors.PageBg == "" || colors.CardBg == "" || colors.InfoBg == "" {
		t.Fatalf("colors = %+v", colors)
	}
	// Dark base: page darker than card (factor 0.7 vs 0.85).
	if !strings.Contains(colors.PageBg, "rgb(") {
		t.Fatalf("page = %q", colors.PageBg)
	}
}

func TestDeriveExportColorsLight(t *testing.T) {
	colors := deriveExportColors("#ffffff")
	// Light base: card is the base itself.
	if colors.CardBg != "#ffffff" {
		t.Fatalf("card = %q", colors.CardBg)
	}
}

func TestDeriveExportColorsInvalid(t *testing.T) {
	colors := deriveExportColors("not-a-color")
	if colors.PageBg != "rgb(24, 24, 30)" {
		t.Fatalf("fallback = %+v", colors)
	}
}

func TestGenerateThemeVars(t *testing.T) {
	colors := map[string]string{
		"accent": "#ff0000",
		"text":   "#ffffff",
	}
	vars := GenerateThemeVars(colors, "#343541")
	if !strings.Contains(vars, "--accent: #ff0000;") {
		t.Fatalf("accent = %q", vars)
	}
	if !strings.Contains(vars, "--exportPageBg:") {
		t.Fatalf("export vars = %q", vars)
	}
}

func TestBuildSessionData(t *testing.T) {
	manager := newTestSessionManager(t, true)
	manager.AppendMessage(map[string]any{"role": "user", "content": "hello"})

	data, err := BuildSessionData(manager, []ToolExport{{Name: "read", Description: "Read files"}}, "system prompt")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if data.Header == nil || data.Header.ID == "" {
		t.Fatal("header missing")
	}
	if len(data.Entries) != 1 {
		t.Fatalf("entries = %d", len(data.Entries))
	}
	if data.LeafID == "" {
		t.Fatal("leaf empty")
	}
	if data.SystemPrompt != "system prompt" {
		t.Fatalf("prompt = %q", data.SystemPrompt)
	}
	if len(data.Tools) != 1 || data.Tools[0].Name != "read" {
		t.Fatalf("tools = %+v", data.Tools)
	}

	// Base64 round trip.
	encoded, err := EncodeSessionDataBase64(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var reparsed SessionData
	if err := json.Unmarshal(decoded, &reparsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if reparsed.Header.ID != data.Header.ID {
		t.Fatal("round trip mismatch")
	}
}
