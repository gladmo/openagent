package codingagent

// Ports of the MCP tools cases from packages/coding-agent
// test/mcp-extension.test.ts: tool naming and result conversion.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/mcp"
)

func TestCreateMcpToolName(t *testing.T) {
	if got := CreateMcpToolName("docs", "search", nil); got != "mcp__docs__search" {
		t.Fatalf("name = %q", got)
	}
	if got := CreateMcpToolName("my-server", "get.item/v2", nil); got != "mcp__my_server__get_item_v2" {
		t.Fatalf("name = %q", got)
	}
	long := CreateMcpToolName("server", strings.Repeat("x", 100), nil)
	if len(long) != 64 {
		t.Fatalf("length = %d", len(long))
	}
	if !regexp.MustCompile(`^mcp__server__x+_[0-9a-f]{8}$`).MatchString(long) {
		t.Fatalf("long = %q", long)
	}
	if other := CreateMcpToolName("server", strings.Repeat("x", 100)+"y", nil); other == long {
		t.Fatal("distinct tools hashed to one name")
	}
	// Names that sanitize to one already taken by another tool get a hash
	// suffix.
	taken := CreateMcpToolName("s", "a_b", nil)
	second := CreateMcpToolName("s", "a-b", func(name string) bool { return name == taken })
	if !regexp.MustCompile(`^mcp__s__a_b_[0-9a-f]{8}$`).MatchString(second) {
		t.Fatalf("second = %q", second)
	}
}

func mcpTextBlock(t *testing.T, block ai.ContentBlock) string {
	t.Helper()
	text, ok := block.(ai.TextContent)
	if !ok {
		t.Fatalf("block = %#v", block)
	}
	return text.Text
}

func callResult(t *testing.T, body string) *mcp.CallToolResult {
	t.Helper()
	value, err := jsonx.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	result, err := mcp.ParseCallToolResult(value)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return result
}

func TestConvertMcpResultPassesCallToolResultToScripts(t *testing.T) {
	result := callResult(t, `{
		"content": [
			{ "type": "resource_link", "uri": "file:///a", "name": "a" },
			{ "type": "resource", "resource": { "uri": "file:///b", "text": "b text" } },
			{ "type": "audio", "data": "", "mimeType": "audio/wav" }
		],
		"structuredContent": { "ok": true },
		"_meta": { "trace": "x" }
	}`)
	converted, err := ConvertMcpResult("docs", "t", result, ConvertMcpResultOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(converted.Content) != 3 {
		t.Fatalf("content = %+v", converted.Content)
	}
	if text := mcpTextBlock(t, converted.Content[0]); text != `[Resource file:///a "a"]` {
		t.Fatalf("link = %q", text)
	}
	if text := mcpTextBlock(t, converted.Content[1]); text != "b text" {
		t.Fatalf("resource = %q", text)
	}
	if text := mcpTextBlock(t, converted.Content[2]); text != "[audio audio/wav omitted]" {
		t.Fatalf("audio = %q", text)
	}
	details, ok := converted.Details.(McpToolDetails)
	if !ok || details.Server != "docs" || details.Tool != "t" {
		t.Fatalf("details = %+v", converted.Details)
	}
	// Scripts get the server's blocks as sent, without _meta.
	structured := converted.StructuredContent
	if structured == nil {
		t.Fatal("no structured content")
	}
	if _, has := structured.Get("_meta"); has {
		t.Fatalf("structured = %s", jsonx.Stringify(structured))
	}
	if structured.MustGet("isError") != false {
		t.Fatalf("structured = %s", jsonx.Stringify(structured))
	}
	scriptContent, _ := structured.MustGet("content").([]any)
	if len(scriptContent) != 3 {
		t.Fatalf("script content = %v", scriptContent)
	}

	// An empty content falls back to the structured content as JSON.
	converted, err = ConvertMcpResult("docs", "t", callResult(t, `{"content": [], "structuredContent": {"n": 1}}`), ConvertMcpResultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if text := mcpTextBlock(t, converted.Content[0]); text != "{\n  \"n\": 1\n}" {
		t.Fatalf("fallback = %q", text)
	}
}

func TestConvertMcpResultFlagsErrors(t *testing.T) {
	converted, err := ConvertMcpResult("docs", "t", callResult(t, `{"content": [{"type": "text", "text": "nope"}], "isError": true}`), ConvertMcpResultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !converted.IsError || mcpTextBlock(t, converted.Content[0]) != "nope" {
		t.Fatalf("converted = %+v", converted)
	}
	if converted.StructuredContent.MustGet("isError") != true {
		t.Fatal("structured content lost the error flag")
	}
	converted, err = ConvertMcpResult("docs", "t", callResult(t, `{"content": [], "isError": true}`), ConvertMcpResultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if text := mcpTextBlock(t, converted.Content[0]); text != "MCP tool docs/t returned an error" {
		t.Fatalf("error text = %q", text)
	}
}

func TestConvertMcpResultLinksAndBinaryResources(t *testing.T) {
	var saved []struct {
		data      []byte
		extension string
	}
	saveOutput := func(data []byte, extension string) (string, error) {
		saved = append(saved, struct {
			data      []byte
			extension string
		}{data, extension})
		return "/tmp/saved" + extension, nil
	}
	converted, err := ConvertMcpResult("docs", "t", callResult(t, `{
		"content": [
			{
				"type": "resource_link",
				"uri": "docs://guide",
				"name": "guide",
				"title": "The Guide",
				"mimeType": "text/markdown",
				"size": 2048,
				"description": "How to use it"
			},
			{ "type": "resource", "resource": { "uri": "file:///r/report.pdf", "mimeType": "application/pdf", "blob": "JVBERg==" } },
			{ "type": "resource", "resource": { "uri": "docs://logo", "mimeType": "image/png", "blob": "AAAA" } }
		]
	}`), ConvertMcpResultOptions{SaveOutput: saveOutput, ReadableResources: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Content) != 3 {
		t.Fatalf("content = %+v", converted.Content)
	}
	if text := mcpTextBlock(t, converted.Content[0]); text != `[Resource docs://guide "The Guide" (text/markdown, 2.0KB): How to use it. Read it with read_mcp_resource (server "docs")]` {
		t.Fatalf("link = %q", text)
	}
	if text := mcpTextBlock(t, converted.Content[1]); text != "[Binary resource file:///r/report.pdf (application/pdf, 4B) saved to /tmp/saved.pdf]" {
		t.Fatalf("binary = %q", text)
	}
	image, ok := converted.Content[2].(ai.ImageContent)
	if !ok || image.Data != "AAAA" || image.MimeType != "image/png" {
		t.Fatalf("image = %+v", converted.Content[2])
	}
	if len(saved) != 1 || string(saved[0].data) != "%PDF" || saved[0].extension != ".pdf" {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestConvertMcpResultCutsMiddleAndKeepsFullForScripts(t *testing.T) {
	var saved []string
	saveOutput := func(data []byte, extension string) (string, error) {
		saved = append(saved, string(data))
		return "/tmp/full.txt", nil
	}
	lines := make([]string, 3000)
	for index := range lines {
		lines[index] = fmt.Sprintf("line %d", index+1)
	}
	full := strings.Join(lines, "\n")
	result := callResult(t, `{
		"content": [
			{ "type": "text", "text": `+jsonx.Stringify(full)+` },
			{ "type": "image", "data": "AAAA", "mimeType": "image/png" }
		]
	}`)
	converted, err := ConvertMcpResult("docs", "snapshot", result, ConvertMcpResultOptions{SaveOutput: saveOutput})
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Content) != 2 {
		t.Fatalf("content = %+v", converted.Content)
	}
	text := mcpTextBlock(t, converted.Content[0])
	// Codex's format: a header, the start and end of the text, then the
	// file with the full text.
	header := fmt.Sprintf("Warning: truncated output (original token count: %d)\nTotal output lines: 3000\n\nline 1\nline 2\n", (len(full)+3)/4)
	if !strings.HasPrefix(text, header) {
		t.Fatalf("header = %q", text[:min(len(text), 160)])
	}
	if !regexp.MustCompile(`…\d+ chars truncated…`).MatchString(text) {
		t.Fatal("no truncation marker")
	}
	if !strings.HasSuffix(text, "line 3000\n\n[Full output: /tmp/full.txt (read it with offset/limit)]") {
		t.Fatalf("tail = %q", text[len(text)-80:])
	}
	if len(text) >= 21*1024 {
		t.Fatalf("text is %d bytes", len(text))
	}
	image, ok := converted.Content[1].(ai.ImageContent)
	if !ok || image.Data != "AAAA" {
		t.Fatalf("image = %+v", converted.Content[1])
	}
	details := converted.Details.(McpToolDetails)
	if details.FullOutputPath != "/tmp/full.txt" {
		t.Fatalf("details = %+v", details)
	}
	if len(saved) != 1 || saved[0] != full {
		t.Fatalf("saved = %d entries", len(saved))
	}
	// The script result keeps the whole blocks.
	if _, ok := converted.StructuredContent.MustGet("content").([]any); !ok {
		t.Fatalf("structured = %s", jsonx.Stringify(converted.StructuredContent))
	}

	// Text within the limit is not saved.
	if _, err := ConvertMcpResult("docs", "small", callResult(t, `{"content": [{"type": "text", "text": "ok"}]}`), ConvertMcpResultOptions{SaveOutput: saveOutput}); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 {
		t.Fatalf("saved = %d entries", len(saved))
	}
}

func TestTruncateMiddleCutsAtCharacterBoundaries(t *testing.T) {
	text := strings.Repeat("é", 20_000) + "end"
	result := TruncateMiddle(text, 1001)
	if !result.Truncated {
		t.Fatal("not truncated")
	}
	if strings.ContainsRune(result.Content, '�') {
		t.Fatal("replacement rune in output")
	}
	if !strings.HasSuffix(result.Content, "end") {
		t.Fatal("tail lost")
	}
	parts := regexp.MustCompile(`…\d+ chars truncated…`).Split(result.Content, 2)
	if len(parts) != 2 {
		t.Fatalf("content = %q", result.Content[:80])
	}
	if len(parts[0]) > 500 {
		t.Fatalf("head = %d bytes", len(parts[0]))
	}
	if len(parts[1]) > 501 {
		t.Fatalf("tail = %d bytes", len(parts[1]))
	}
	headRunes := len([]rune(parts[0]))
	tailRunes := len([]rune(parts[1]))
	if headRunes+tailRunes+result.RemovedChars != len([]rune(text)) {
		t.Fatalf("runes do not add up: %d+%d+%d != %d", headRunes, tailRunes, result.RemovedChars, len([]rune(text)))
	}
}
