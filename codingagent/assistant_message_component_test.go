package codingagent

// Tests for the assistant-message component (adapted from
// assistant-message.test.ts).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
)

func textBlock(text string) *ai.TextContent {
	return &ai.TextContent{Text: text}
}

func thinkingBlock(text string) *ai.ThinkingContent {
	return &ai.ThinkingContent{Thinking: text}
}

func TestAssistantMessageTextOnly(t *testing.T) {
	message := &ai.AssistantMessage{Content: []ai.ContentBlock{textBlock("Hello world")}}
	component := NewAssistantMessageComponent(message, false, "Thinking...", 1)

	lines := component.Render(60)
	if len(lines) == 0 {
		t.Fatal("empty render")
	}
	// OSC 133 zones wrap text-only output.
	if !strings.HasPrefix(lines[0], "\x1b]133;A\x07") {
		t.Fatalf("start missing: %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "\x1b]133;B\x07\x1b]133;C\x07") {
		t.Fatalf("end missing: %q", lines[len(lines)-1])
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Hello world") {
		t.Fatalf("content = %q", joined)
	}
	if component.HasToolCalls() {
		t.Fatal("phantom tool calls")
	}
}

func TestAssistantMessageToolCallsNoZones(t *testing.T) {
	message := &ai.AssistantMessage{Content: []ai.ContentBlock{
		&ai.ToolCall{ID: "t1", Name: "bash"},
	}}
	component := NewAssistantMessageComponent(message, false, "Thinking...", 1)

	lines := component.Render(60)
	if component.HasToolCalls() != true {
		t.Fatal("tool calls not detected")
	}
	// Tool-call renders skip the zones.
	for _, line := range lines {
		if strings.Contains(line, "]133;") {
			t.Fatalf("zone leaked: %q", line)
		}
	}
}

func TestAssistantMessageThinkingGrouping(t *testing.T) {
	message := &ai.AssistantMessage{Content: []ai.ContentBlock{
		thinkingBlock("first thought"),
		thinkingBlock("second thought"),
		textBlock("answer"),
	}}
	component := NewAssistantMessageComponent(message, false, "Thinking...", 1)

	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "first thought") || !strings.Contains(joined, "second thought") {
		t.Fatalf("thinking = %q", joined)
	}
	if !strings.Contains(joined, "answer") {
		t.Fatalf("answer = %q", joined)
	}
}

func TestAssistantMessageHiddenThinking(t *testing.T) {
	message := &ai.AssistantMessage{Content: []ai.ContentBlock{
		thinkingBlock("deep reasoning"),
		textBlock("result"),
	}}
	component := NewAssistantMessageComponent(message, true, "Thinking...", 1)

	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Thinking...") {
		t.Fatalf("label missing: %q", joined)
	}
	if strings.Contains(joined, "deep reasoning") {
		t.Fatalf("thinking leaked: %q", joined)
	}

	// Reveal: per-run override.
	component.ToggleThinkingVisibility(0)
	lines = component.Render(60)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "deep reasoning") {
		t.Fatalf("reveal = %q", joined)
	}

	// Global toggle clears overrides.
	component.SetHideThinkingBlock(true)
	lines = component.Render(60)
	joined = strings.Join(lines, "\n")
	if strings.Contains(joined, "deep reasoning") {
		t.Fatalf("re-hide = %q", joined)
	}
}

func TestAssistantMessageSetHiddenLabel(t *testing.T) {
	message := &ai.AssistantMessage{Content: []ai.ContentBlock{thinkingBlock("x"), textBlock("y")}}
	component := NewAssistantMessageComponent(message, true, "Thinking...", 1)
	component.SetHiddenThinkingLabel("Reasoned")
	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "Reasoned") {
		t.Fatalf("label = %v", lines)
	}
}

func TestAssistantMessageEmptyContent(t *testing.T) {
	message := &ai.AssistantMessage{Content: []ai.ContentBlock{}}
	component := NewAssistantMessageComponent(message, false, "Thinking...", 1)
	if lines := component.Render(60); len(lines) != 0 {
		// Empty renders may still produce spacer-free zero lines.
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				t.Fatalf("non-empty = %q", line)
			}
		}
	}
}

func TestAssistantMessageUpdateContent(t *testing.T) {
	component := NewAssistantMessageComponent(nil, false, "Thinking...", 1)
	message := &ai.AssistantMessage{Content: []ai.ContentBlock{textBlock("v1")}}
	component.UpdateContent(message, false)
	if component.LastMessage() == nil {
		t.Fatal("message not retained")
	}
	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "v1") {
		t.Fatalf("v1 = %v", lines)
	}

	component.UpdateContent(&ai.AssistantMessage{Content: []ai.ContentBlock{textBlock("v2 replaced")}}, true)
	lines = component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "v2 replaced") {
		t.Fatalf("v2 = %v", lines)
	}
}

// TestAssistantMessageValueBlocksRender pins the provider form: text and
// thinking blocks arrive as values (ai.TextContent, not pointers), and
// dropping them left every real reply blank in the TUI.
func TestAssistantMessageValueBlocksRender(t *testing.T) {
	message := &ai.AssistantMessage{Content: []ai.ContentBlock{
		ai.TextContent{Text: "value text"},
		ai.ThinkingContent{Thinking: "value thinking"},
		ai.TextContent{Text: "more value text"},
	}}
	component := NewAssistantMessageComponent(message, false, "Thinking...", 1)

	joined := strings.Join(component.Render(80), "\n")
	if !strings.Contains(joined, "value text") {
		t.Fatalf("value text block not rendered: %q", joined)
	}
	if !strings.Contains(joined, "more value text") {
		t.Fatalf("second value text block not rendered: %q", joined)
	}
	if !strings.Contains(joined, "value thinking") {
		t.Fatalf("value thinking block not rendered: %q", joined)
	}
}
