package agent

// Ports of representative cases from pi/packages/agent/test/agent-loop.test.ts.

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func testModel() *ai.Model {
	return &ai.Model{
		ID: "mock", Name: "mock", API: "openai-responses", Provider: "openai",
		BaseURL: "https://example.invalid", Input: []string{"text"},
		ContextWindow: 8192, MaxTokens: 2048,
	}
}

func testAssistant(blocks ...ai.ContentBlock) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Content: blocks, API: "openai-responses", Provider: "openai", Model: "mock",
		StopReason: ai.StopStop, TimestampMs: float64(time.Now().UnixMilli()),
	}
}

func testUser(text string) *ai.UserMessage {
	return &ai.UserMessage{Content: ai.StringContent(text), TimestampMs: float64(time.Now().UnixMilli())}
}

// scriptedStreamFn returns a StreamFn serving the given messages in order.
func scriptedStreamFn(messages ...*ai.AssistantMessage) (StreamFn, *int) {
	calls := 0
	index := 0
	fn := func(_ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		calls++
		message := messages[index]
		index++
		go func(message *ai.AssistantMessage) {
			partial := &ai.AssistantMessage{Content: []ai.ContentBlock{}, API: message.API, Provider: message.Provider, Model: message.Model, StopReason: ai.StopPending}
			stream.Push(&ai.EventStart{Partial: partial})
			stream.Push(&ai.EventDone{Reason: message.StopReason, Message: message})
		}(message)
		return stream
	}
	return fn, &calls
}

func identityConverter(messages []AgentMessage) []ai.Message {
	out := []ai.Message{}
	for _, message := range messages {
		switch m := message.(type) {
		case *ai.SystemMessage, *ai.UserMessage, *ai.AssistantMessage, *ai.ToolResultMessage:
			out = append(out, m.(ai.Message))
		}
	}
	return out
}

func collectAgentEvents(t *testing.T, stream *ai.EventStream[AgentEvent, []AgentMessage]) ([]string, []AgentMessage) {
	t.Helper()
	var types []string
	for {
		event, ok := stream.Next()
		if !ok {
			break
		}
		types = append(types, event.EventType())
	}
	return types, stream.Result()
}

func TestAgentLoopEventSequence(t *testing.T) {
	streamFn, _ := scriptedStreamFn(testAssistant(ai.TextContent{Text: "Hello"}))
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{}}
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: identityConverter}

	stream := AgentLoop([]AgentMessage{testUser("Hi")}, context, config, nil, streamFn)
	types, result := collectAgentEvents(t, stream)
	want := "agent_start,turn_start,message_start,message_end,message_start,message_end,turn_end,agent_end"
	if strings.Join(types, ",") != want {
		t.Fatalf("types = %v", types)
	}
	if len(result) != 2 {
		t.Fatalf("result = %d messages", len(result))
	}
	// The user prompt and the assistant reply are the run's new messages.
	if _, ok := result[0].(*ai.UserMessage); !ok {
		t.Fatalf("result[0] = %T", result[0])
	}
	if _, ok := result[1].(*ai.AssistantMessage); !ok {
		t.Fatalf("result[1] = %T", result[1])
	}
	// The caller's context object is not mutated (TS copies the array).
	if len(context.Messages) != 0 {
		t.Fatalf("context mutated: %d", len(context.Messages))
	}
}

func TestAgentLoopToolCallsAndResults(t *testing.T) {
	streamFn, _ := scriptedStreamFn(
		testAssistant(&ai.ToolCall{ID: "call_1", Name: "echo", Arguments: jsonx.ObjFrom("x", float64(1))}),
		testAssistant(ai.TextContent{Text: "after tool"}),
	)
	var executed [][]any
	tool := &AgentTool{
		Name: "echo", Description: "Echo", Label: "Echo",
		Parameters: typeboxObjectX(),
		Execute: func(toolCallID string, params any, _ *aiAbortSignal, _ AgentToolUpdateCallback) (*AgentToolResult, error) {
			executed = append(executed, []any{toolCallID, params})
			return &AgentToolResult{
				Content: []ai.ContentBlock{ai.TextContent{Text: "echoed"}},
				Details: jsonx.NewObj(),
			}, nil
		},
	}
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: identityConverter}

	stream := AgentLoop([]AgentMessage{testUser("run tool")}, context, config, nil, streamFn)
	types, result := collectAgentEvents(t, stream)
	joined := strings.Join(types, ",")
	for _, want := range []string{"tool_execution_start", "tool_execution_end", "turn_end"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %v", want, types)
		}
	}
	if len(executed) != 1 {
		t.Fatalf("executed = %d", len(executed))
	}
	if executed[0][0] != "call_1" {
		t.Fatalf("id = %v", executed[0][0])
	}
	// Tool result message persisted in the run's new messages.
	var toolResult *ai.ToolResultMessage
	for _, m := range result {
		if tr, ok := m.(*ai.ToolResultMessage); ok {
			toolResult = tr
		}
	}
	if toolResult == nil || toolResult.ToolCallID != "call_1" || toolResult.IsError {
		t.Fatalf("toolResult = %+v", toolResult)
	}
}

func TestAgentLoopLengthTruncatedToolCallsFail(t *testing.T) {
	truncated := testAssistant(&ai.ToolCall{ID: "call_1", Name: "echo", Arguments: jsonx.ObjFrom("x", float64(1))})
	truncated.StopReason = ai.StopLength
	streamFn, _ := scriptedStreamFn(truncated, testAssistant(ai.TextContent{Text: "recovered"}))
	executed := 0
	tool := &AgentTool{
		Name: "echo", Description: "Echo", Label: "Echo", Parameters: typeboxObjectX(),
		Execute: func(string, any, *aiAbortSignal, AgentToolUpdateCallback) (*AgentToolResult, error) {
			executed++
			return &AgentToolResult{Content: nil, Details: jsonx.NewObj()}, nil
		},
	}
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: identityConverter}

	stream := AgentLoop([]AgentMessage{testUser("run")}, context, config, nil, streamFn)
	_, result := collectAgentEvents(t, stream)
	if executed != 0 {
		t.Fatalf("tool executed %d times", executed)
	}
	var toolResult *ai.ToolResultMessage
	for _, m := range result {
		if tr, ok := m.(*ai.ToolResultMessage); ok {
			toolResult = tr
		}
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatal("expected error tool result")
	}
	if text := ai.ContentText(ai.BlocksContent(toolResult.Content...), "\n"); !strings.Contains(text, "was not executed") {
		t.Fatalf("text = %s", text)
	}
}

func TestAgentLoopParallelCompletionOrderSourcePersistence(t *testing.T) {
	// Tool A blocks until tool B finishes; tool_execution_end for B must be
	// able to arrive before A's, but tool-result messages persist in source
	// order.
	releaseA := make(chan struct{})
	streamFn, _ := scriptedStreamFn(
		testAssistant(
			&ai.ToolCall{ID: "call_a", Name: "toolA", Arguments: jsonx.ObjFrom("x", float64(1))},
			&ai.ToolCall{ID: "call_b", Name: "toolB", Arguments: jsonx.ObjFrom("x", float64(2))},
		),
		testAssistant(ai.TextContent{Text: "done"}),
	)
	mu := sync.Mutex{}
	var endOrder []string
	toolA := &AgentTool{
		Name: "toolA", Description: "A", Label: "A", Parameters: typeboxObjectX(),
		Execute: func(string, any, *aiAbortSignal, AgentToolUpdateCallback) (*AgentToolResult, error) {
			<-releaseA
			return &AgentToolResult{Content: []ai.ContentBlock{ai.TextContent{Text: "a"}}, Details: jsonx.NewObj()}, nil
		},
	}
	toolB := &AgentTool{
		Name: "toolB", Description: "B", Label: "B", Parameters: typeboxObjectX(),
		Execute: func(string, any, *aiAbortSignal, AgentToolUpdateCallback) (*AgentToolResult, error) {
			return &AgentToolResult{Content: []ai.ContentBlock{ai.TextContent{Text: "b"}}, Details: jsonx.NewObj()}, nil
		},
	}
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{toolA, toolB}}
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: identityConverter}
	events := []string{}
	emit := func(event AgentEvent) {
		mu.Lock()
		if e, ok := event.(*EventToolExecutionEnd); ok {
			endOrder = append(endOrder, e.ToolCallID)
		}
		events = append(events, event.EventType())
		mu.Unlock()
	}

	done := make(chan struct{})
	var runResult []AgentMessage
	go func() {
		defer close(done)
		var runErr error
		runResult, runErr = RunAgentLoop([]AgentMessage{testUser("go")}, context, config, emit, nil, streamFn)
		if runErr != nil {
			t.Errorf("run error: %v", runErr)
		}
	}()
	// Wait for B to complete, then release A.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		hasB := false
		for _, id := range endOrder {
			if id == "call_b" {
				hasB = true
			}
		}
		mu.Unlock()
		if hasB || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseA)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(endOrder) != 2 || endOrder[0] != "call_b" || endOrder[1] != "call_a" {
		t.Fatalf("end order = %v", endOrder)
	}
	// Tool-result messages in source order.
	var order []string
	for _, m := range runResult {
		if tr, ok := m.(*ai.ToolResultMessage); ok {
			order = append(order, tr.ToolCallID)
		}
	}
	if len(order) != 2 || order[0] != "call_a" || order[1] != "call_b" {
		t.Fatalf("message order = %v", order)
	}
}

func TestAgentLoopTerminateBatch(t *testing.T) {
	terminate := true
	streamFn, _ := scriptedStreamFn(testAssistant(
		&ai.ToolCall{ID: "call_1", Name: "stopper", Arguments: jsonx.ObjFrom("x", float64(1))},
	))
	tool := &AgentTool{
		Name: "stopper", Description: "Stops", Label: "Stops", Parameters: typeboxObjectX(),
		Execute: func(string, any, *aiAbortSignal, AgentToolUpdateCallback) (*AgentToolResult, error) {
			return &AgentToolResult{
				Content: []ai.ContentBlock{ai.TextContent{Text: "done"}}, Details: jsonx.NewObj(),
				Terminate: &terminate,
			}, nil
		},
	}
	// A second stream response would be requested if the loop continued.
	streamFn2, calls2 := scriptedStreamFn(testAssistant(ai.TextContent{Text: "second"}))
	_ = streamFn2
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: identityConverter}
	_, _ = RunAgentLoop([]AgentMessage{testUser("x")}, context, config, func(AgentEvent) {}, nil, streamFn)
	if *calls2 != 0 {
		t.Fatal("unexpected extra calls")
	}
	// With terminate, the loop stops after the tool batch: only one stream
	// call total.
	streamFn3, calls3 := scriptedStreamFn(testAssistant(
		&ai.ToolCall{ID: "call_1", Name: "stopper", Arguments: jsonx.ObjFrom("x", float64(1))},
	))
	context2 := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	_, _ = RunAgentLoop([]AgentMessage{testUser("x")}, context2, config, func(AgentEvent) {}, nil, streamFn3)
	if *calls3 != 1 {
		t.Fatalf("calls = %d, want 1 (terminated after batch)", *calls3)
	}
}

func TestAgentLoopCustomMessagesViaConvertToLm(t *testing.T) {
	streamFn, calls := scriptedStreamFn(testAssistant(ai.TextContent{Text: "ok"}))
	sawCustom := false
	convert := func(messages []AgentMessage) []ai.Message {
		out := []ai.Message{}
		for _, message := range messages {
			if custom, ok := message.(*CustomAgentMessage); ok {
				sawCustom = true
				out = append(out, &ai.UserMessage{Content: ai.StringContent("[" + custom.Role_ + "]"), TimestampMs: custom.TimestampMs})
				continue
			}
			switch m := message.(type) {
			case *ai.SystemMessage, *ai.UserMessage, *ai.AssistantMessage, *ai.ToolResultMessage:
				out = append(out, m.(ai.Message))
			}
		}
		return out
	}
	custom := &CustomAgentMessage{Role_: "bashExecution", TimestampMs: 1, Fields: jsonx.ObjFrom("command", "ls")}
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{}}
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: convert}
	stream := AgentLoop([]AgentMessage{testUser("hi"), custom}, context, config, nil, streamFn)
	_, _ = collectAgentEvents(t, stream)
	if !sawCustom {
		t.Fatal("custom message not passed to convertToLlm")
	}
	if *calls != 1 {
		t.Fatalf("calls = %d", *calls)
	}
}

func TestAgentLoopContinueValidation(t *testing.T) {
	if _, err := AgentLoopContinue(&AgentContext{Messages: []AgentMessage{}}, &AgentLoopConfig{}, nil, nil); err == nil {
		t.Fatal("empty context accepted")
	}
	assistant := testAssistant(ai.TextContent{Text: "hi"})
	if _, err := AgentLoopContinue(&AgentContext{Messages: []AgentMessage{assistant}}, &AgentLoopConfig{}, nil, nil); err == nil {
		t.Fatal("assistant-last accepted")
	}
}

func TestAgentLoopContinueFromToolResult(t *testing.T) {
	streamFn, calls := scriptedStreamFn(testAssistant(ai.TextContent{Text: "after tools"}))
	toolResult := &ai.ToolResultMessage{
		ToolCallID: "call_1", ToolName: "echo",
		Content: []ai.ContentBlock{ai.TextContent{Text: "r"}}, Details: jsonx.NewObj(),
		TimestampMs: 2,
	}
	context := &AgentContext{Messages: []AgentMessage{toolResult}, Tools: []*AgentTool{}}
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: identityConverter}
	stream, err := AgentLoopContinue(context, config, nil, streamFn)
	if err != nil {
		t.Fatal(err)
	}
	types, _ := collectAgentEvents(t, stream)
	// No message events for pre-existing context.
	want := "agent_start,turn_start,message_start,message_end,turn_end,agent_end"
	if strings.Join(types, ",") != want {
		t.Fatalf("types = %v", types)
	}
	if *calls != 1 {
		t.Fatalf("calls = %d", *calls)
	}
}
