package codingagent

// Tests for the interactive transcript event bridge (adapted from the
// interactive-mode streaming surfaces).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/tui"
)

func TestTranscriptStreamingAssistant(t *testing.T) {
	transcript := NewInteractiveTranscript()

	// message_start opens a streaming block.
	transcript.HandleEvent(&agent.EventMessageStart{Message: &ai.AssistantMessage{
		Content: []ai.ContentBlock{&ai.TextContent{Text: "streaming"}},
	}}, nil)
	if len(transcript.Blocks()) != 1 {
		t.Fatalf("blocks = %v", transcript.Blocks())
	}
	joined := strings.Join(transcript.Component().Render(60), "\n")
	if !strings.Contains(joined, "streaming") {
		t.Fatalf("render = %q", joined)
	}

	// message_update re-renders with the new content.
	transcript.HandleEvent(&agent.EventMessageUpdate{
		Message: &ai.AssistantMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: "streaming more text"}}},
	}, nil)
	joined = strings.Join(transcript.Component().Render(60), "\n")
	if !strings.Contains(joined, "streaming more text") {
		t.Fatalf("update = %q", joined)
	}

	// message_end finalizes (block keeps its position).
	transcript.HandleEvent(&agent.EventMessageEnd{Message: &ai.AssistantMessage{
		Content: []ai.ContentBlock{&ai.TextContent{Text: "final answer"}},
	}}, nil)
	joined = strings.Join(transcript.Component().Render(60), "\n")
	if !strings.Contains(joined, "final answer") {
		t.Fatalf("final = %q", joined)
	}
	if len(transcript.Blocks()) != 1 {
		t.Fatalf("blocks = %v", transcript.Blocks())
	}
}

func TestTranscriptMultipleAssistantTurns(t *testing.T) {
	transcript := NewInteractiveTranscript()

	transcript.HandleEvent(&agent.EventMessageStart{Message: &ai.AssistantMessage{
		Content: []ai.ContentBlock{&ai.TextContent{Text: "first turn"}},
	}}, nil)
	transcript.HandleEvent(&agent.EventMessageEnd{Message: &ai.AssistantMessage{
		Content: []ai.ContentBlock{&ai.TextContent{Text: "first turn"}},
	}}, nil)
	transcript.HandleEvent(&agent.EventMessageStart{Message: &ai.AssistantMessage{
		Content: []ai.ContentBlock{&ai.TextContent{Text: "second turn"}},
	}}, nil)
	transcript.HandleEvent(&agent.EventMessageEnd{Message: &ai.AssistantMessage{
		Content: []ai.ContentBlock{&ai.TextContent{Text: "second turn"}},
	}}, nil)

	joined := strings.Join(transcript.Component().Render(60), "\n")
	if !strings.Contains(joined, "first turn") || !strings.Contains(joined, "second turn") {
		t.Fatalf("turns = %q", joined)
	}
	if len(transcript.Blocks()) != 2 {
		t.Fatalf("blocks = %v", transcript.Blocks())
	}
}

func TestTranscriptToolExecution(t *testing.T) {
	transcript := NewInteractiveTranscript()

	transcript.HandleEvent(&agent.EventToolExecutionStart{
		ToolCallID: "call-1",
		ToolName:   "read",
		Args:       map[string]any{"path": "/tmp/f"},
	}, nil)

	component := transcript.ToolComponent("call-1")
	if component == nil {
		t.Fatal("tool component missing")
	}
	joined := strings.Join(transcript.Component().Render(60), "\n")
	if !strings.Contains(joined, "read") {
		t.Fatalf("tool render = %q", joined)
	}

	// End with a text result.
	transcript.HandleEvent(&agent.EventToolExecutionEnd{
		ToolCallID: "call-1",
		ToolName:   "read",
		Result: &agent.AgentToolResult{
			Content: []ai.ContentBlock{&ai.TextContent{Text: "file contents here"}},
		},
	}, nil)
	joined = strings.Join(transcript.Component().Render(60), "\n")
	if !strings.Contains(joined, "file contents here") {
		t.Fatalf("result = %q", joined)
	}
}

func TestTranscriptToolExecutionError(t *testing.T) {
	transcript := NewInteractiveTranscript()
	transcript.HandleEvent(&agent.EventToolExecutionStart{ToolCallID: "c", ToolName: "bash", Args: nil}, nil)
	transcript.HandleEvent(&agent.EventToolExecutionEnd{
		ToolCallID: "c",
		ToolName:   "bash",
		Result:     &agent.AgentToolResult{Content: []ai.ContentBlock{&ai.TextContent{Text: "boom"}}},
		IsError:    true,
	}, nil)

	component := transcript.ToolComponent("c")
	if component == nil || !component.IsError() {
		t.Fatalf("component = %+v", component)
	}
}

func TestTranscriptToolRenderers(t *testing.T) {
	transcript := NewInteractiveTranscript()
	renderers := &stubToolRenderers{shell: "default", callText: "CUSTOM CALL", resultText: "CUSTOM RESULT"}
	transcript.HandleEvent(&agent.EventToolExecutionStart{
		ToolCallID: "custom-1",
		ToolName:   "custom",
		Args:       nil,
	}, func(string) ToolRenderers { return renderers })

	joined := strings.Join(transcript.Component().Render(60), "\n")
	if !strings.Contains(joined, "CUSTOM CALL") {
		t.Fatalf("custom call = %q", joined)
	}
	transcript.HandleEvent(&agent.EventToolExecutionEnd{
		ToolCallID: "custom-1",
		ToolName:   "custom",
		Result:     &agent.AgentToolResult{Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}},
	}, nil)
	joined = strings.Join(transcript.Component().Render(60), "\n")
	if !strings.Contains(joined, "CUSTOM RESULT") {
		t.Fatalf("custom result = %q", joined)
	}
}

func TestTranscriptBashBlocks(t *testing.T) {
	transcript := NewInteractiveTranscript()
	transcript.AppendBashBlock("bash-1", "echo hi", false, nil)
	transcript.HandleBashUpdate("bash-1", "hi\n")

	joined := strings.Join(transcript.Component().Render(60), "\n")
	if !strings.Contains(joined, "$ echo hi") {
		t.Fatalf("command = %q", joined)
	}
	if !strings.Contains(joined, "hi") {
		t.Fatalf("output = %q", joined)
	}

	// Unknown ids are ignored.
	transcript.HandleBashUpdate("missing", "x")
}

func TestTranscriptRenderOrder(t *testing.T) {
	transcript := NewInteractiveTranscript()
	transcript.HandleEvent(&agent.EventMessageStart{Message: &ai.AssistantMessage{
		Content: []ai.ContentBlock{&ai.TextContent{Text: "before tool"}},
	}}, nil)
	transcript.HandleEvent(&agent.EventMessageEnd{Message: &ai.AssistantMessage{
		Content: []ai.ContentBlock{&ai.TextContent{Text: "before tool"}},
	}}, nil)
	transcript.HandleEvent(&agent.EventToolExecutionStart{ToolCallID: "t", ToolName: "bash", Args: nil}, nil)

	joined := strings.Join(transcript.Component().Render(200), "\n")
	textIndex := strings.Index(joined, "before tool")
	toolIndex := strings.Index(joined, " bash")
	if textIndex == -1 || toolIndex == -1 {
		t.Fatalf("missing blocks: %q", joined)
	}
	if textIndex > toolIndex {
		t.Fatalf("order inverted: text=%d tool=%d", textIndex, toolIndex)
	}
}

func TestTranscriptClear(t *testing.T) {
	transcript := NewInteractiveTranscript()
	transcript.HandleEvent(&agent.EventToolExecutionStart{ToolCallID: "t", ToolName: "bash", Args: nil}, nil)
	transcript.Clear()
	if len(transcript.Blocks()) != 0 {
		t.Fatalf("blocks = %v", transcript.Blocks())
	}
	if transcript.ToolComponent("t") != nil {
		t.Fatal("tool component survived")
	}
}

func TestToolRenderersAdapter(t *testing.T) {
	definition := &ToolDefinition{
		Name:        "custom",
		RenderShell: "self",
		RenderCall: func(args any, context *ToolRenderContext) tui.Component {
			if !context.ExecutionStarted || !context.ArgsComplete {
				t.Error("call context not marked started/complete")
			}
			return tui.NewText("adapter call", 0, 0, nil)
		},
		RenderResult: func(result *ToolExecutionResult, options ToolRenderResultOptions, context *ToolRenderContext) tui.Component {
			if !options.Expanded {
				t.Error("expanded flag lost")
			}
			return tui.NewText("adapter result", 0, 0, nil)
		},
	}

	adapter := &toolRenderersAdapter{definition: definition}
	if adapter.RenderShell() != "self" {
		t.Fatalf("shell = %q", adapter.RenderShell())
	}
	if call := adapter.RenderCall(nil); call == nil {
		t.Fatal("call nil")
	}
	result := adapter.RenderResult(&ToolExecutionResult{}, ToolRenderResultOptions{Expanded: true})
	if result == nil {
		t.Fatal("result nil")
	}

	// Definitions without hooks yield nil (fallback rendering).
	empty := &toolRenderersAdapter{definition: &ToolDefinition{Name: "x"}}
	if empty.RenderCall(nil) != nil || empty.RenderResult(nil, ToolRenderResultOptions{}) != nil {
		t.Fatal("hookless definition rendered")
	}
	if empty.RenderShell() != "" {
		t.Fatalf("shell = %q", empty.RenderShell())
	}
}

func TestTranscriptConcurrentRenderDuringEvents(t *testing.T) {
	// Regression: HandleEvent mutates the tree from the agent goroutine
	// while the scheduler renders — the locked Render boundary must keep
	// every read serialized (run under -race).
	transcript := NewInteractiveTranscript()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			message := &ai.AssistantMessage{StopReason: "stop"}
			transcript.HandleEvent(&agent.EventMessageStart{Message: message}, nil)
			message.Content = []ai.ContentBlock{&ai.TextContent{Text: "concurrent"}}
			transcript.HandleEvent(&agent.EventMessageUpdate{Message: message}, nil)
			transcript.HandleEvent(&agent.EventMessageEnd{Message: message}, nil)
		}
	}()
	for i := 0; i < 200; i++ {
		_ = transcript.Render(60)
	}
	<-done
}

func TestInteractiveStatusConcurrentRender(t *testing.T) {
	// Regression: status writes from event handlers race renderer reads
	// unless the slot is locked (run under -race).
	mode, _ := newInteractiveForTest(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			mode.showStatus("tick")
			mode.showWarning("warn")
			mode.showError("err")
		}
	}()
	for i := 0; i < 200; i++ {
		_ = mode.statusStack.Render(80)
	}
	<-done
}
