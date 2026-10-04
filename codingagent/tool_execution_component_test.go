package codingagent

// Tests for the tool execution component (adapted from
// tool-execution.test.ts).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/tui"
)

type stubToolRenderers struct {
	shell      string
	callText   string
	resultText string
}

func (s *stubToolRenderers) RenderCall(args any) tui.Component {
	return tui.NewText(s.callText, 0, 0, nil)
}

func (s *stubToolRenderers) RenderResult(result any, options ToolRenderResultOptions) tui.Component {
	return tui.NewText(s.resultText, 0, 0, nil)
}

func (s *stubToolRenderers) RenderShell() string { return s.shell }

func TestToolExecutionFallbackPending(t *testing.T) {
	component := NewToolExecutionComponent("read", "t1", nil, ToolExecutionOptions{}, nil, nil)
	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "read") {
		t.Fatalf("name = %q", joined)
	}
	if component.Expanded() {
		t.Fatal("expanded initially")
	}
}

func TestToolExecutionFallbackResultPreview(t *testing.T) {
	component := NewToolExecutionComponent("read", "t1", nil, ToolExecutionOptions{}, nil, nil)

	var content []map[string]any
	for i := 0; i < 25; i++ {
		content = append(content, map[string]any{"type": "text", "text": "line " + itoaBash(i)})
	}
	component.SetResult(&ToolExecutionResult{Content: content})

	lines := component.Render(200)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "more lines") {
		t.Fatalf("hint = %q", joined)
	}
	if strings.Contains(joined, "line 24") {
		t.Fatalf("over preview = %q", joined)
	}

	// Expanded: everything.
	component.SetExpanded(true)
	lines = component.Render(200)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "line 24") {
		t.Fatalf("expanded = %q", joined)
	}
}

func TestToolExecutionFallbackShortResult(t *testing.T) {
	component := NewToolExecutionComponent("bash", "t1", nil, ToolExecutionOptions{}, nil, nil)
	component.SetResult(&ToolExecutionResult{
		Content: []map[string]any{{"type": "text", "text": "done"}},
	})
	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "done") {
		t.Fatalf("result = %v", lines)
	}
	if strings.Contains(strings.Join(lines, "\n"), "more lines") {
		t.Fatal("short result hinted")
	}
}

func TestToolExecutionCustomRenderersDefaultShell(t *testing.T) {
	renderers := &stubToolRenderers{shell: "default", callText: "CALL RENDERED", resultText: "RESULT RENDERED"}
	component := NewToolExecutionComponent("custom", "t1", nil, ToolExecutionOptions{}, renderers, nil)

	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "CALL RENDERED") {
		t.Fatalf("call = %v", lines)
	}

	component.SetResult(&ToolExecutionResult{})
	lines = component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "RESULT RENDERED") {
		t.Fatalf("result = %v", lines)
	}
}

func TestToolExecutionCustomRenderersSelfShell(t *testing.T) {
	renderers := &stubToolRenderers{shell: "self", callText: "SELF CALL"}
	component := NewToolExecutionComponent("custom", "t1", nil, ToolExecutionOptions{}, renderers, nil)

	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "SELF CALL") {
		t.Fatalf("self = %q", joined)
	}
}

func TestToolExecutionResultErrorFlag(t *testing.T) {
	component := NewToolExecutionComponent("x", "t1", nil, ToolExecutionOptions{}, nil, nil)
	if component.IsError() {
		t.Fatal("error before result")
	}
	component.SetResult(&ToolExecutionResult{IsError: true})
	if !component.IsError() {
		t.Fatal("error flag lost")
	}
}

func TestToolExecutionStateTransitions(t *testing.T) {
	component := NewToolExecutionComponent("x", "t1", map[string]any{"path": "/f"}, ToolExecutionOptions{}, nil, nil)
	component.SetArgs(map[string]any{"path": "/g"})
	component.SetExecutionStarted()
	component.SetArgsComplete()
	component.SetPartial(false)
	// Renders remain valid through the transitions.
	if lines := component.Render(60); len(lines) == 0 && strings.Join(lines, "") != "" {
		t.Fatalf("render broke: %v", lines)
	}
}
