package agent

// severe_defects_test.go pins regressions for review findings: the
// default-StreamFn fallback, nil tool results, caller-owned slice privacy
// for RunAgentLoopContinue, serialized listener dispatch under parallel
// tool execution, and bounded proxy contentIndex handling.

import (
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func TestNilStreamFnFailsLoudNotPanic(t *testing.T) {
	// No default configured and no explicit streamFn: the loop must return
	// GetDefaultStreamFn's error, not nil-deref on the first model call.
	SetDefaultStreamFn(nil)
	defer SetDefaultStreamFn(nil)
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: identityConverter}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RunAgentLoop panicked on nil streamFn: %v", r)
		}
	}()
	_, err := RunAgentLoop([]AgentMessage{testUser("hi")}, &AgentContext{Messages: []AgentMessage{}}, config, func(AgentEvent) {}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "default stream function") {
		t.Fatalf("err = %v, want default stream function error", err)
	}
	// The continue entry point fails the same way.
	continueContext := &AgentContext{Messages: []AgentMessage{testUser("continue")}}
	_, err = RunAgentLoopContinue(continueContext, config, func(AgentEvent) {}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "default stream function") {
		t.Fatalf("continue err = %v", err)
	}
	// With a default configured, an omitted streamFn resolves to it.
	calls := 0
	SetDefaultStreamFn(func(_ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		calls++
		fn, _ := scriptedStreamFn(testAssistant(ai.TextContent{Text: "ok"}))
		return fn(nil, nil, nil)
	})
	defer SetDefaultStreamFn(nil)
	if _, err := RunAgentLoop([]AgentMessage{testUser("hi")}, &AgentContext{Messages: []AgentMessage{}}, config, func(AgentEvent) {}, nil, nil); err != nil {
		t.Fatalf("run with default: %v", err)
	}
	if calls != 1 {
		t.Fatalf("default stream fn calls = %d", calls)
	}
}

func TestToolReturningNilResultConvertsToError(t *testing.T) {
	tool := &AgentTool{
		Name: "nilish", Description: "nil", Label: "nil", Parameters: typeboxObjectX(),
		Execute: func(string, any, *aiAbortSignal, AgentToolUpdateCallback) (*AgentToolResult, error) {
			return nil, nil
		},
	}
	context := &AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{tool}}
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: identityConverter}
	streamFn, _ := scriptedStreamFn(
		testAssistant(&ai.ToolCall{ID: "c1", Name: "nilish", Arguments: jsonx.ObjFrom("x", float64(1))}),
		testAssistant(ai.TextContent{Text: "done"}),
	)
	messages, err := RunAgentLoop([]AgentMessage{testUser("go")}, context, config, func(AgentEvent) {}, nil, streamFn)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	sawErrorResult := false
	for _, message := range messages {
		if result, ok := message.(*ai.ToolResultMessage); ok && result.IsError {
			if !strings.Contains(ai.ContentText(ai.BlocksContent(result.Content...), "\n"), "returned no result") {
				t.Fatalf("tool result = %q", ai.ContentText(ai.BlocksContent(result.Content...), "\n"))
			}
			sawErrorResult = true
		}
	}
	if !sawErrorResult {
		t.Fatal("nil tool result not converted to an error result")
	}
}

func TestRunAgentLoopContinueDoesNotClobberCallerSlice(t *testing.T) {
	backing := make([]AgentMessage, 4, 8)
	backing[0] = testAssistant(ai.TextContent{Text: "prior-assistant"})
	backing[1] = testUser("sentinel-1")
	backing[2] = testUser("sentinel-2")
	backing[3] = testUser("latest")
	sentinel1, sentinel2 := backing[1], backing[2]
	context := &AgentContext{Messages: backing[:4], Tools: []*AgentTool{}}
	config := &AgentLoopConfig{Model: testModel(), ConvertToLlm: identityConverter}
	streamFn, _ := scriptedStreamFn(testAssistant(ai.TextContent{Text: "next"}))
	_, err := RunAgentLoopContinue(context, config, func(AgentEvent) {}, nil, streamFn)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if backing[1] != sentinel1 || backing[2] != sentinel2 {
		t.Fatal("loop wrote into the caller's backing array")
	}
}

func TestParallelToolListenersStaySerialized(t *testing.T) {
	// Run under -race: with parallel tool execution (the default), tool
	// goroutines emit concurrently; listener dispatch must stay serialized
	// (subscription-order contract), so an unsynchronized listener is safe.
	release := make(chan struct{})
	mkTool := func(name string) *AgentTool {
		return &AgentTool{
			Name: name, Description: name, Label: name, Parameters: typeboxObjectX(),
			Execute: func(string, any, *aiAbortSignal, AgentToolUpdateCallback) (*AgentToolResult, error) {
				if name == "toolA" {
					<-release
				}
				return &AgentToolResult{Content: []ai.ContentBlock{ai.TextContent{Text: name}}, Details: jsonx.NewObj()}, nil
			},
		}
	}
	withToolCalls := &ai.AssistantMessage{
		Content: []ai.ContentBlock{
			&ai.ToolCall{ID: "call_a", Name: "toolA", Arguments: jsonx.ObjFrom("x", float64(1))},
			&ai.ToolCall{ID: "call_b", Name: "toolB", Arguments: jsonx.ObjFrom("x", float64(2))},
		},
		API: "openai-responses", Provider: "openai", Model: "mock",
		StopReason: ai.StopToolUse, TimestampMs: float64(time.Now().UnixMilli()),
	}
	go func() {
		// Stagger: toolB settles while toolA is still blocked, so their
		// emit windows provably overlap without the serialization.
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	agent := NewAgent(AgentOptions{
		StreamFn: agentTestStreamFn(withToolCalls, agentText("done")),
		InitialState: &AgentInitialState{
			Tools: []*AgentTool{mkTool("toolA"), mkTool("toolB")},
		},
	})
	// Deliberately unsynchronized appends: serialized dispatch makes this
	// race-free; overlapping dispatch fails under -race.
	seen := 0
	agent.Subscribe(func(event AgentEvent, _ *aiAbortSignal) {
		seen++
	})
	defer func() {
		if seen == 0 {
			t.Fatal("no listener dispatch observed")
		}
	}()
	if err := agent.PromptText("go"); err != nil {
		t.Fatalf("prompt error: %v", err)
	}
}
