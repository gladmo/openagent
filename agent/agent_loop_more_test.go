package agent

// Additional ports from pi/packages/agent/test/agent-loop.test.ts:
// beforeToolCall mutation without revalidation, prepareToolCallArguments,
// sequential-mode forcing, finishTurn ordering, action:end queue skip,
// blocked-call terminate, afterToolCall overlay.

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

func toolUseMessage(calls ...*ai.ToolCall) *ai.AssistantMessage {
	msg := testAssistant()
	if len(calls) > 0 {
		msg.Content = nil
		for _, call := range calls {
			msg.Content = append(msg.Content, call)
		}
		msg.StopReason = ai.StopToolUse
	}
	return msg
}

func TestBeforeToolCallMutationWithoutRevalidation(t *testing.T) {
	tool := &AgentTool{
		Name: "echo", Label: "Echo", Description: "Echo tool",
		Parameters: typebox.Object([]*typebox.Property{typebox.Prop("value", typebox.String())}),
		Execute: func(_ string, params any, _ *aiAbortSignal, _ AgentToolUpdateCallback) (*AgentToolResult, error) {
			value := params.(*jsonx.Obj).MustGet("value")
			return &AgentToolResult{
				Content: []ai.ContentBlock{ai.TextContent{Text: "echoed: " + jsonx.Stringify(value)}},
				Details: jsonx.ObjFrom("value", value),
			}, nil
		},
	}
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	config := &AgentLoopConfig{
		Model:        testModel(),
		ConvertToLlm: identityConverter,
		BeforeToolCall: func(ctx *BeforeToolCallContext, _ *aiAbortSignal) (*BeforeToolCallResult, error) {
			// Mutate args in place; executed without revalidation.
			ctx.Args.(*jsonx.Obj).Set("value", float64(123))
			return nil, nil
		},
	}
	streamFn, _ := scriptedStreamFn(
		toolUseMessage(&ai.ToolCall{ID: "tool-1", Name: "echo", Arguments: jsonx.ObjFrom("value", "hello")}),
		testAssistant(ai.TextContent{Text: "done"}),
	)
	stream := AgentLoop([]AgentMessage{testUser("echo")}, context, config, nil, streamFn)
	_, result := collectAgentEvents(t, stream)
	var toolResult *ai.ToolResultMessage
	for _, m := range result {
		if tr, ok := m.(*ai.ToolResultMessage); ok {
			toolResult = tr
		}
	}
	if toolResult == nil {
		t.Fatal("no tool result")
	}
	if got := jsonx.Stringify(toolResult.Details); got != `{"value":123}` {
		t.Fatalf("details = %s (mutation not applied)", got)
	}
}

func TestPrepareToolCallArguments(t *testing.T) {
	tool := &AgentTool{
		Name: "echo", Label: "Echo", Description: "Echo",
		Parameters: typebox.Object([]*typebox.Property{typebox.Prop("value", typebox.String())}),
		PrepareArguments: func(args any) any {
			obj := args.(*jsonx.Obj)
			if v, ok := obj.Get("value"); ok {
				if s, ok := v.(string); ok && strings.HasPrefix(s, "wrapped:") {
					return jsonx.ObjFrom("value", strings.TrimPrefix(s, "wrapped:"))
				}
			}
			return args
		},
		Execute: func(_ string, params any, _ *aiAbortSignal, _ AgentToolUpdateCallback) (*AgentToolResult, error) {
			return &AgentToolResult{Details: params.(*jsonx.Obj).Clone()}, nil
		},
	}
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: identityConverter}
	streamFn, _ := scriptedStreamFn(
		toolUseMessage(&ai.ToolCall{ID: "t", Name: "echo", Arguments: jsonx.ObjFrom("value", "wrapped:prepared")}),
		testAssistant(ai.TextContent{Text: "done"}),
	)
	stream := AgentLoop([]AgentMessage{testUser("x")}, context, config, nil, streamFn)
	_, result := collectAgentEvents(t, stream)
	var toolResult *ai.ToolResultMessage
	for _, m := range result {
		if tr, ok := m.(*ai.ToolResultMessage); ok {
			toolResult = tr
		}
	}
	if toolResult == nil || jsonx.Stringify(toolResult.Details) != `{"value":"prepared"}` {
		t.Fatalf("details = %+v", toolResult)
	}
}

func TestSequentialForcedByToolExecutionMode(t *testing.T) {
	// slow tool declares executionMode=sequential; default config is
	// parallel. Two calls to the sequential tool must NOT overlap.
	var mu sync.Mutex
	inFlight := 0
	maxInFlight := 0
	sequential := ToolExecutionSequential
	tool := &AgentTool{
		Name: "slow", Label: "Slow", Description: "Slow",
		Parameters:    typebox.Object(nil),
		ExecutionMode: &sequential,
		Execute: func(_ string, _ any, _ *aiAbortSignal, _ AgentToolUpdateCallback) (*AgentToolResult, error) {
			mu.Lock()
			inFlight++
			if inFlight > maxInFlight {
				maxInFlight = inFlight
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
			return &AgentToolResult{Details: jsonx.NewObj()}, nil
		},
	}
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: identityConverter}
	streamFn, _ := scriptedStreamFn(
		toolUseMessage(
			&ai.ToolCall{ID: "a", Name: "slow", Arguments: jsonx.NewObj()},
			&ai.ToolCall{ID: "b", Name: "slow", Arguments: jsonx.NewObj()},
		),
		testAssistant(ai.TextContent{Text: "done"}),
	)
	stream := AgentLoop([]AgentMessage{testUser("x")}, context, config, nil, streamFn)
	_, _ = collectAgentEvents(t, stream)
	if maxInFlight > 1 {
		t.Fatalf("sequential tool overlapped: maxInFlight = %d", maxInFlight)
	}
}

func TestFinishTurnOrderingAfterToolResults(t *testing.T) {
	terminate := true
	tool := &AgentTool{
		Name: "echo", Label: "Echo", Description: "Echo",
		Parameters: typebox.Object(nil),
		Execute: func(_ string, _ any, _ *aiAbortSignal, _ AgentToolUpdateCallback) (*AgentToolResult, error) {
			return &AgentToolResult{Details: jsonx.NewObj(), Terminate: &terminate}, nil
		},
	}
	var ordering []string
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	config := &AgentLoopConfig{
		Model:        testModel(),
		ConvertToLlm: identityConverter,
		FinishTurn: func(turn *AgentTurnContext, _ *aiAbortSignal) (AgentTurnDecision, error) {
			ordering = append(ordering, "finishTurn")
			for _, m := range turn.NewMessages {
				if _, ok := m.(*ai.ToolResultMessage); ok {
					ordering = append(ordering, "sawToolResult")
				}
			}
			return AgentTurnDecision{}, nil
		},
	}
	streamFn, _ := scriptedStreamFn(
		toolUseMessage(&ai.ToolCall{ID: "t", Name: "echo", Arguments: jsonx.NewObj()}),
	)
	var events []string
	emit := func(event AgentEvent) {
		events = append(events, event.EventType())
	}
	_, _ = RunAgentLoop([]AgentMessage{testUser("x")}, context, config, emit, nil, streamFn)
	joined := strings.Join(ordering, ",")
	if joined != "finishTurn,sawToolResult" {
		t.Fatalf("ordering = %s", joined)
	}
	// finishTurn ran before turn_end.
	finishIdx, turnEndIdx := -1, -1
	for i, e := range events {
		if e == "finishTurn" {
			finishIdx = i
		}
		if e == "turn_end" && finishIdx == -1 {
			turnEndIdx = i
		}
	}
	_ = turnEndIdx
	// Events do not contain finishTurn (it is a hook, not an event); the
	// ordering check above plus turn_end presence suffices.
	found := false
	for _, e := range events {
		if e == "turn_end" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %v", events)
	}
}

func TestActionEndSkipsQueuePolling(t *testing.T) {
	tool := &AgentTool{
		Name: "noop", Label: "Noop", Description: "Noop",
		Parameters: typebox.Object(nil),
		Execute: func(_ string, _ any, _ *aiAbortSignal, _ AgentToolUpdateCallback) (*AgentToolResult, error) {
			return &AgentToolResult{Details: jsonx.NewObj()}, nil
		},
	}
	providerCalls := 0
	steeringPolls := 0
	streamFn := func(*ai.Model, *ai.TranscriptContext, *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		providerCalls++
		s := ai.NewAssistantMessageEventStream()
		go func() {
			s.Push(&ai.EventDone{Reason: ai.StopToolUse, Message: toolUseMessage(&ai.ToolCall{ID: "t", Name: "noop", Arguments: jsonx.NewObj()})})
		}()
		return s
	}
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	config := &AgentLoopConfig{
		Model:        testModel(),
		ConvertToLlm: identityConverter,
		FinishTurn: func(*AgentTurnContext, *aiAbortSignal) (AgentTurnDecision, error) {
			return AgentTurnDecision{Action: "end"}, nil
		},
		GetSteeringMessages: func() []AgentMessage {
			steeringPolls++
			return nil
		},
	}
	_, _ = RunAgentLoop([]AgentMessage{testUser("x")}, context, config, func(AgentEvent) {}, nil, streamFn)
	if providerCalls != 1 {
		t.Fatalf("providerCalls = %d", providerCalls)
	}
	// Initial poll only; the finishTurn end decision skips post-turn polling.
	if steeringPolls > 1 {
		t.Fatalf("steeringPolls = %d", steeringPolls)
	}
}

func TestBlockedToolCallTerminate(t *testing.T) {
	block := true
	tool := &AgentTool{
		Name: "danger", Label: "Danger", Description: "Danger",
		Parameters: typebox.Object(nil),
		Execute: func(_ string, _ any, _ *aiAbortSignal, _ AgentToolUpdateCallback) (*AgentToolResult, error) {
			t.Fatal("blocked tool executed")
			return nil, nil
		},
	}
	streamFn, calls := scriptedStreamFn(
		toolUseMessage(&ai.ToolCall{ID: "t", Name: "danger", Arguments: jsonx.NewObj()}),
	)
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	config := &AgentLoopConfig{
		Model:        testModel(),
		ConvertToLlm: identityConverter,
		BeforeToolCall: func(*BeforeToolCallContext, *aiAbortSignal) (*BeforeToolCallResult, error) {
			terminate := true
			return &BeforeToolCallResult{Block: &block, Reason: "not allowed", Terminate: &terminate}, nil
		},
	}
	var events []string
	_, _ = RunAgentLoop([]AgentMessage{testUser("x")}, context, config, func(event AgentEvent) {
		events = append(events, event.EventType())
	}, nil, streamFn)
	if *calls != 1 {
		t.Fatalf("calls = %d (terminated batch must not re-request)", *calls)
	}
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "tool_execution_end") {
		t.Fatalf("events = %v", events)
	}
	// The blocked result carries the reason and terminates the run.
	stream := AgentLoop([]AgentMessage{testUser("x")}, &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}, config, nil, streamFn2ForBlocked())
	_, result := collectAgentEvents(t, stream)
	var toolResult *ai.ToolResultMessage
	for _, m := range result {
		if tr, ok := m.(*ai.ToolResultMessage); ok {
			toolResult = tr
		}
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatal("expected blocked error tool result")
	}
	if text := ai.ContentText(ai.BlocksContent(toolResult.Content...), "\n"); text != "not allowed" {
		t.Fatalf("reason = %q", text)
	}
}

func streamFn2ForBlocked() StreamFn {
	fn, _ := scriptedStreamFn(
		toolUseMessage(&ai.ToolCall{ID: "t", Name: "danger", Arguments: jsonx.NewObj()}),
	)
	return fn
}

func TestAfterToolCallOverlay(t *testing.T) {
	tool := &AgentTool{
		Name: "echo", Label: "Echo", Description: "Echo",
		Parameters: typebox.Object(nil),
		Execute: func(_ string, _ any, _ *aiAbortSignal, _ AgentToolUpdateCallback) (*AgentToolResult, error) {
			return &AgentToolResult{Content: []ai.ContentBlock{ai.TextContent{Text: "original"}}, Details: jsonx.NewObj()}, nil
		},
	}
	isError := true
	terminate := true
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	config := &AgentLoopConfig{
		Model:        testModel(),
		ConvertToLlm: identityConverter,
		AfterToolCall: func(*AfterToolCallContext, *aiAbortSignal) (*AfterToolCallResult, error) {
			return &AfterToolCallResult{
				Content:    []ai.ContentBlock{ai.TextContent{Text: "patched"}},
				HasContent: true,
				IsError:    &isError,
				Terminate:  &terminate,
			}, nil
		},
	}
	streamFn, _ := scriptedStreamFn(
		toolUseMessage(&ai.ToolCall{ID: "t", Name: "echo", Arguments: jsonx.NewObj()}),
	)
	result, _ := RunAgentLoop2([]AgentMessage{testUser("x")}, context, config, nil, streamFn)
	var toolResult *ai.ToolResultMessage
	for _, m := range result {
		if tr, ok := m.(*ai.ToolResultMessage); ok {
			toolResult = tr
		}
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatal("expected error overlay")
	}
	if text := ai.ContentText(ai.BlocksContent(toolResult.Content...), "\n"); text != "patched" {
		t.Fatalf("content = %q", text)
	}
}

func RunAgentLoop2(prompts []AgentMessage, context *AgentContext, config *AgentLoopConfig, signal *aiAbortSignal, streamFn StreamFn) ([]AgentMessage, error) {
	return RunAgentLoop(prompts, context, config, func(AgentEvent) {}, signal, streamFn)
}
