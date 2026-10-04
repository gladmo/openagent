package mcp

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func blocksFrom(t *testing.T, text string) *CallToolResult {
	t.Helper()
	result, err := ParseCallToolResult(mustParse(t, text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return result
}

func TestToLlmContentPassesTextAndImages(t *testing.T) {
	result := blocksFrom(t, `{
		"content": [
			{ "type": "text", "text": "hello", "annotations": { "priority": 1 } },
			{ "type": "image", "data": "aW1n", "mimeType": "image/png", "_meta": { "x": 1 } },
			{ "type": "audio", "data": "YXVk", "mimeType": "audio/wav" },
			{ "type": "resource_link", "uri": "file:///a.txt", "name": "a.txt" },
			{ "type": "resource", "resource": { "uri": "file:///b.txt", "text": "inline" } },
			{ "type": "resource", "resource": { "uri": "file:///c.png", "mimeType": "image/png", "blob": "Yw==" } },
			{ "type": "resource", "resource": { "uri": "file:///d.bin", "blob": "ZA==" } }
		]
	}`)
	content := ToLlmContent(result)
	expected := []LlmContent{
		{Type: "text", Text: "hello"},
		{Type: "image", Data: "aW1n", MimeType: "image/png"},
		{Type: "text", Text: "[audio audio/wav omitted]"},
		{Type: "text", Text: "a.txt: file:///a.txt"},
		{Type: "text", Text: "inline"},
		{Type: "image", Data: "Yw==", MimeType: "image/png"},
		{Type: "text", Text: "[binary resource file:///d.bin (unknown type) omitted]"},
	}
	if len(content) != len(expected) {
		t.Fatalf("content = %+v", content)
	}
	for index, wanted := range expected {
		if content[index] != wanted {
			t.Fatalf("content[%d] = %+v, want %+v", index, content[index], wanted)
		}
	}
}

func TestToLlmContentFallsBackToStructured(t *testing.T) {
	structured, _ := jsonx.Parse(`{"n":1}`)
	result := &CallToolResult{StructuredContent: structured.(*jsonx.Obj)}
	content := ToLlmContent(result)
	if len(content) != 1 || content[0].Text != "{\n  \"n\": 1\n}" {
		t.Fatalf("structured fallback: %+v", content)
	}
	withText := blocksFrom(t, `{ "content": [{ "type": "text", "text": "n=1" }], "structuredContent": { "n": 1 } }`)
	content = ToLlmContent(withText)
	if len(content) != 1 || content[0].Text != "n=1" {
		t.Fatalf("text wins: %+v", content)
	}
}

func TestParseCallToolResultDefaultsContent(t *testing.T) {
	result := blocksFrom(t, `{ "structuredContent": { "ok": true } }`)
	if len(result.Content) != 0 || result.StructuredContent.MustGet("ok") != true {
		t.Fatalf("result = %+v", result)
	}
	if _, err := ParseCallToolResult(mustParse(t, `{ "content": "not a list" }`)); err == nil ||
		!strings.Contains(err.Error(), "Invalid MCP tools/call result") {
		t.Fatalf("non-list content: %v", err)
	}
}

func TestParseToolValidation(t *testing.T) {
	if _, ok := ParseTool(mustParse(t, `{"name":"search","inputSchema":{"type":"object"}}`)); !ok {
		t.Fatal("valid tool rejected")
	}
	if _, ok := ParseTool(mustParse(t, `{"name":"search"}`)); ok {
		t.Fatal("tool without schema accepted")
	}
	resource, ok := parseResource(mustParse(t, `{"uri":"file:///b"}`))
	if !ok || resource.Name != "" {
		t.Fatalf("resource = %+v", resource)
	}
	if name := ToResource(resource).Name; name != "file:///b" {
		t.Fatalf("fallback name = %q", name)
	}
	if _, ok := parseResource(mustParse(t, `{"name":"no uri"}`)); ok {
		t.Fatal("resource without uri accepted")
	}
}
