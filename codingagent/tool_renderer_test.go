package codingagent

// Tests for the tool HTML renderer (adapted from tool-renderer.test.ts).

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTrimRenderedResultLines(t *testing.T) {
	lines := []string{"", "\x1b[31m\x1b[39m", "real", "content", "  ", "\x1b[2m\x1b[22m"}
	trimmed := trimRenderedResultLines(lines)
	if len(trimmed) != 2 || trimmed[0] != "real" || trimmed[1] != "content" {
		t.Fatalf("trimmed = %v", trimmed)
	}

	// All blank: empty.
	if got := trimRenderedResultLines([]string{"", ""}); len(got) != 0 {
		t.Fatalf("all blank = %v", got)
	}
}

func TestToolHtmlRendererRenderCall(t *testing.T) {
	renderer := &ToolHtmlRenderer{
		GetToolDefinition: func(name string) *ToolDefinition {
			if name == "custom" {
				return &ToolDefinition{Name: "custom"}
			}
			return nil
		},
		RenderCallLines: func(toolCallID string, definition *ToolDefinition, args any) []string {
			return []string{"\x1b[31mcolored call\x1b[0m"}
		},
	}

	if got := renderer.RenderCall("t1", "custom", nil); !strings.Contains(got, "colored call") || !strings.Contains(got, "ansi-line") {
		t.Fatalf("call = %q", got)
	}

	// Unknown tool: empty.
	if got := renderer.RenderCall("t1", "unknown", nil); got != "" {
		t.Fatalf("unknown = %q", got)
	}

	// Panicking render: recovered to empty.
	renderer.RenderCallLines = func(toolCallID string, definition *ToolDefinition, args any) []string {
		panic("boom")
	}
	if got := renderer.RenderCall("t1", "custom", nil); got != "" {
		t.Fatalf("panic = %q", got)
	}
}

func TestToolHtmlRendererRenderResult(t *testing.T) {
	renderer := &ToolHtmlRenderer{
		GetToolDefinition: func(name string) *ToolDefinition {
			return &ToolDefinition{Name: name}
		},
		RenderResultLines: func(toolCallID string, definition *ToolDefinition, result []map[string]any, details any, isError, expanded bool) []string {
			if expanded {
				return []string{"", "expanded view", ""}
			}
			return []string{"collapsed view"}
		},
	}

	out := renderer.RenderResult("t1", "custom", nil, nil, false)
	if out == nil {
		t.Fatal("nil result")
	}
	if !strings.Contains(out.ResultHtmlExpanded, "expanded view") {
		t.Fatalf("expanded = %q", out.ResultHtmlExpanded)
	}
	if !strings.Contains(out.ResultHtmlCollapsed, "collapsed view") {
		t.Fatalf("collapsed = %q", out.ResultHtmlCollapsed)
	}

	// Identical views: collapsed omitted.
	renderer.RenderResultLines = func(toolCallID string, definition *ToolDefinition, result []map[string]any, details any, isError, expanded bool) []string {
		return []string{"same"}
	}
	out = renderer.RenderResult("t2", "custom", nil, nil, false)
	if out.ResultHtmlCollapsed != "" {
		t.Fatalf("identical collapsed = %q", out.ResultHtmlCollapsed)
	}
	if !strings.Contains(out.ResultHtmlExpanded, "same") {
		t.Fatalf("expanded = %q", out.ResultHtmlExpanded)
	}
}

func TestPreRenderCustomTools(t *testing.T) {
	renderer := &ToolHtmlRenderer{
		GetToolDefinition: func(name string) *ToolDefinition {
			return &ToolDefinition{Name: name}
		},
		RenderCallLines: func(toolCallID string, definition *ToolDefinition, args any) []string {
			return []string{"call for " + definition.Name}
		},
		RenderResultLines: func(toolCallID string, definition *ToolDefinition, result []map[string]any, details any, isError, expanded bool) []string {
			return []string{"result for " + definition.Name}
		},
	}

	manager := newTestSessionManager(t, false)
	// Assistant message with a custom tool call + a template-rendered one.
	assistant := map[string]any{
		"role": "assistant",
		"Content": []map[string]any{
			{"type": "toolCall", "ID": "call-1", "Name": "custom-tool"},
			{"type": "toolCall", "ID": "call-2", "Name": "bash"},
		},
	}
	rawAssistant, _ := json.Marshal(assistant)
	manager.AppendMessage(json.RawMessage(rawAssistant))

	// Tool result for the custom call.
	toolResult := map[string]any{
		"role":       "toolResult",
		"toolCallId": "call-1",
		"toolName":   "custom-tool",
		"Content":    []map[string]any{{"type": "text", "text": "ok"}},
	}
	rawResult, _ := json.Marshal(toolResult)
	manager.AppendMessage(json.RawMessage(rawResult))

	rendered := PreRenderCustomTools(manager.GetEntries(), renderer)
	if len(rendered) != 1 {
		t.Fatalf("rendered = %v (ids)", rendered)
	}
	entry, ok := rendered["call-1"]
	if !ok {
		t.Fatal("call-1 missing")
	}
	if !strings.Contains(entry.CallHtml, "call for custom-tool") {
		t.Fatalf("call = %q", entry.CallHtml)
	}
	if !strings.Contains(entry.ResultHtmlExpanded, "result for custom-tool") {
		t.Fatalf("result = %q", entry.ResultHtmlExpanded)
	}
}

func TestPreRenderCustomToolsTemplateOnlySkipped(t *testing.T) {
	renderer := &ToolHtmlRenderer{
		GetToolDefinition: func(name string) *ToolDefinition { return &ToolDefinition{Name: name} },
		RenderCallLines: func(toolCallID string, definition *ToolDefinition, args any) []string {
			return []string{"should not render"}
		},
	}

	manager := newTestSessionManager(t, false)
	assistant := map[string]any{
		"role":    "assistant",
		"Content": []map[string]any{{"type": "toolCall", "ID": "b1", "Name": "bash"}},
	}
	rawAssistant, _ := json.Marshal(assistant)
	manager.AppendMessage(json.RawMessage(rawAssistant))

	rendered := PreRenderCustomTools(manager.GetEntries(), renderer)
	if len(rendered) != 0 {
		t.Fatalf("template tool rendered = %v", rendered)
	}
}

func TestPreRenderCustomToolsNilRenderer(t *testing.T) {
	manager := newTestSessionManager(t, false)
	if rendered := PreRenderCustomTools(manager.GetEntries(), nil); rendered != nil {
		t.Fatalf("nil renderer = %v", rendered)
	}
}
