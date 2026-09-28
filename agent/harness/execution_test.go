package harness

// Ports of execution-primitives.test.ts, execution-tools.test.ts and
// execution-assistant.test.ts (representative cases).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

func echoHarnessTool() *AgentHarnessTool {
	return &AgentHarnessTool{
		AgentTool: agent.AgentTool{
			Name:        "echo",
			Label:       "Echo",
			Description: "Echo tool",
			Parameters:  typebox.Object([]*typebox.Property{typebox.Prop("value", typebox.String())}),
		},
		Execute: func(_ string, params any, _ AgentHarnessToolUpdateCallback, _ any, _ AgentHarnessToolInvocation, _ Context) (*agent.AgentToolResult, error) {
			value := params.(*jsonx.Obj).MustGet("value")
			return &agent.AgentToolResult{
				Content: []ai.ContentBlock{ai.TextContent{Text: "echo: " + jsonx.Stringify(value)}},
				Details: jsonx.ObjFrom("value", value),
			}, nil
		},
	}
}

func TestGateLifecycle(t *testing.T) {
	gate, control := CreateGate()
	ran := false
	gate.Admit(func() { ran = true })
	if !ran {
		t.Fatal("open gate rejected admission")
	}

	// beginAbort: admit throws AbortRequested with the cancellation handle.
	cancellation := make(chan struct{})
	control.BeginAbort(cancellation)
	aborted := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if ar, ok := r.(*AbortRequested); ok && ar.Cancellation == cancellation {
					aborted = true
				}
			}
		}()
		gate.Admit(func() {})
	}()
	if !aborted {
		t.Fatal("aborting gate admitted effect")
	}

	// signalAbort fires the gate signal with AbortRequested reason.
	control.SignalAbort()
	if !gate.Signal().Aborted() {
		t.Fatal("signal not aborted")
	}

	// close: admit throws the close error.
	closeErr := ToError("closed")
	control.Close(closeErr)
	func() {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok && err == closeErr {
					return
				}
				t.Fatalf("close panic = %v", r)
			}
			t.Fatal("closed gate admitted effect")
		}()
		gate.Admit(func() {})
	}()
	// Idempotent close.
	control.Close(nil)
}

func TestPrepareToolCall(t *testing.T) {
	tool := echoHarnessTool()
	call := &ai.ToolCall{ID: "c1", Name: "echo", Arguments: jsonx.ObjFrom("value", "hi")}
	prepared, immediate := PrepareToolCall(call, []*AgentHarnessTool{tool})
	if immediate != nil {
		t.Fatalf("immediate = %+v", immediate)
	}
	if prepared.Tool != tool || prepared.Args.MustGet("value") != "hi" {
		t.Fatalf("prepared = %+v", prepared.Args)
	}

	// Unknown tool.
	_, immediate = PrepareToolCall(&ai.ToolCall{ID: "c2", Name: "missing", Arguments: jsonx.NewObj()}, []*AgentHarnessTool{tool})
	if immediate == nil || !strings.Contains(ai.ContentText(ai.BlocksContent(immediate.Result.Content...), "\n"), "is unavailable") {
		t.Fatalf("immediate = %+v", immediate)
	}

	// Invalid arguments: required value missing (TS uses call({})).
	_, immediate = PrepareToolCall(&ai.ToolCall{ID: "c3", Name: "echo", Arguments: jsonx.NewObj()}, []*AgentHarnessTool{tool})
	if immediate == nil {
		t.Fatal("invalid args accepted")
	}
	// Numbers coerce to strings via the always-on coercion pass.
	coerced, coercedImmediate := PrepareToolCall(&ai.ToolCall{ID: "c3b", Name: "echo", Arguments: jsonx.ObjFrom("value", float64(42))}, []*AgentHarnessTool{tool})
	if coercedImmediate != nil || coerced.Args.MustGet("value") != "42" {
		t.Fatalf("coerced = %+v immediate = %+v", coerced, coercedImmediate)
	}

	// prepareArguments shim.
	shimmed := echoHarnessTool()
	shimmed.PrepareArguments = func(args any) any {
		return jsonx.ObjFrom("value", "shimmed")
	}
	prepared, _ = PrepareToolCall(&ai.ToolCall{ID: "c4", Name: "echo", Arguments: jsonx.NewObj()}, []*AgentHarnessTool{shimmed})
	if prepared.Args.MustGet("value") != "shimmed" {
		t.Fatalf("args = %s", jsonx.Stringify(prepared.Args))
	}
}

func TestApplyBeforeToolDecision(t *testing.T) {
	tool := echoHarnessTool()
	call := &ai.ToolCall{ID: "c1", Name: "echo", Arguments: jsonx.ObjFrom("value", "x")}
	prepared, _ := PrepareToolCall(call, []*AgentHarnessTool{tool})

	// No decision: passes through.
	cleared, immediate := ApplyBeforeToolDecision(prepared, nil)
	if immediate != nil || cleared.Args.MustGet("value") != "x" {
		t.Fatalf("cleared = %+v immediate = %+v", cleared, immediate)
	}

	// Block with terminate.
	block := &BeforeToolDecision{Block: &struct {
		Reason    string
		Terminate bool
	}{Reason: "not allowed", Terminate: true}}
	_, immediate = ApplyBeforeToolDecision(prepared, block)
	if immediate == nil || !immediate.Terminate {
		t.Fatalf("immediate = %+v", immediate)
	}
	if text := ai.ContentText(ai.BlocksContent(immediate.Result.Content...), "\n"); text != "not allowed" {
		t.Fatalf("reason = %q", text)
	}

	// Replacement args revalidated.
	cleared, immediate = ApplyBeforeToolDecision(prepared, &BeforeToolDecision{Args: jsonx.ObjFrom("value", "replaced")})
	if immediate != nil || cleared.Args.MustGet("value") != "replaced" {
		t.Fatalf("cleared = %+v", cleared)
	}
	// Invalid replacement args (required missing) -> immediate error.
	_, immediate = ApplyBeforeToolDecision(prepared, &BeforeToolDecision{Args: jsonx.NewObj()})
	if immediate == nil {
		t.Fatal("invalid replacement accepted")
	}
}

func TestExecuteAndFinalizeToolCall(t *testing.T) {
	tool := echoHarnessTool()
	call := &ai.ToolCall{ID: "c1", Name: "echo", Arguments: jsonx.ObjFrom("value", "v")}
	prepared, _ := PrepareToolCall(call, []*AgentHarnessTool{tool})
	cleared, _ := ApplyBeforeToolDecision(prepared, nil)

	gate, _ := CreateGate()
	updates := 0
	executed, admitted := ExecuteToolCall(cleared, gate, func(*agent.AgentToolResult, *AgentHarnessToolUpdateOptions) {
		updates++
	}, nil, nil, BackgroundContext)
	if !admitted {
		t.Fatal("not admitted")
	}
	if executed.IsError {
		t.Fatal("echo failed")
	}
	_ = updates

	// Finalize without patch keeps the executed values.
	final := FinalizeToolCall(cleared, executed, nil)
	if final.IsError || final.Terminate {
		t.Fatalf("final = %+v", final)
	}

	// Patch overlays fields.
	isError, terminate := true, true
	patched := FinalizeToolCall(cleared, executed, &AfterToolPatch{
		Content:    []ai.ContentBlock{ai.TextContent{Text: "patched"}},
		HasContent: true,
		IsError:    &isError,
		Terminate:  &terminate,
	})
	if !patched.IsError || !patched.Terminate {
		t.Fatalf("patched = %+v", patched)
	}
	if text := ai.ContentText(ai.BlocksContent(patched.Result.Content...), "\n"); text != "patched" {
		t.Fatalf("content = %q", text)
	}

	// Tool throws convert to error output.
	throwing := echoHarnessTool()
	throwing.Execute = func(string, any, AgentHarnessToolUpdateCallback, any, AgentHarnessToolInvocation, Context) (*agent.AgentToolResult, error) {
		return nil, ToError("tool exploded")
	}
	preparedThrow, _ := PrepareToolCall(call, []*AgentHarnessTool{throwing})
	clearedThrow, _ := ApplyBeforeToolDecision(preparedThrow, nil)
	executedThrow, _ := ExecuteToolCall(clearedThrow, gate, nil, nil, nil, BackgroundContext)
	if !executedThrow.IsError {
		t.Fatal("throw not converted to error")
	}
	if text := ai.ContentText(ai.BlocksContent(executedThrow.Result.Content...), "\n"); text != "tool exploded" {
		t.Fatalf("error text = %q", text)
	}
}

func TestToolResultMessageRoundTrip(t *testing.T) {
	call := &ai.ToolCall{ID: "c1", Name: "echo", Arguments: jsonx.NewObj()}
	final := &FinalizedToolCall{
		ToolCall: call,
		Result: &agent.AgentToolResult{
			Content: []ai.ContentBlock{ai.TextContent{Text: "out"}},
			Details: jsonx.ObjFrom("k", float64(1)),
		},
	}
	message := CreateToolResultMessage(final)
	if message.ToolCallID != "c1" || message.ToolName != "echo" || message.IsError {
		t.Fatalf("message = %+v", message)
	}
	reconstructed := ToolResultFromMessage(message, false)
	if ai.ContentText(ai.BlocksContent(reconstructed.Content...), "\n") != "out" {
		t.Fatal("round trip content")
	}
	if jsonx.Stringify(reconstructed.Details) != `{"k":1}` {
		t.Fatalf("details = %s", jsonx.Stringify(reconstructed.Details))
	}
}

// recordingObserver captures the stream lifecycle.
type recordingObserver struct {
	starts    int
	updates   int
	ends      int
	lastEnd   *ai.AssistantMessage
	failStart func()
}

func (o *recordingObserver) Start(_ *ai.AssistantMessage, _ *ai.EventStart, _ Context) {
	o.starts++
	if o.failStart != nil {
		o.failStart()
	}
}
func (o *recordingObserver) Update(_ *ai.AssistantMessage, _ ai.AssistantMessageEvent, _ Context) {
	o.updates++
}
func (o *recordingObserver) End(message *ai.AssistantMessage, _ Context) {
	o.ends++
	o.lastEnd = message
}

func TestConsumeAssistantStreamLifecycle(t *testing.T) {
	stream := ai.NewAssistantMessageEventStream()
	partial := &ai.AssistantMessage{Content: []ai.ContentBlock{}, StopReason: ai.StopPending}
	final := &ai.AssistantMessage{Content: []ai.ContentBlock{ai.TextContent{Text: "done"}}, StopReason: ai.StopStop}
	go func() {
		stream.Push(&ai.EventStart{Partial: partial})
		stream.Push(&ai.EventTextStart{ContentIndex: 0, Partial: partial})
		stream.Push(&ai.EventTextDelta{ContentIndex: 0, Delta: "do", Partial: partial})
		stream.Push(&ai.EventTextDelta{ContentIndex: 0, Delta: "ne", Partial: partial})
		stream.Push(&ai.EventTextEnd{ContentIndex: 0, Content: "done", Partial: partial})
		stream.Push(&ai.EventDone{Reason: ai.StopStop, Message: final})
	}()
	observer := &recordingObserver{}
	settled, err := ConsumeAssistantStream(stream, observer, nil, BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if settled != final {
		t.Fatal("settled message mismatch")
	}
	if observer.starts != 1 || observer.updates != 4 || observer.ends != 1 {
		t.Fatalf("observer = %+v", observer)
	}
	if observer.lastEnd != final {
		t.Fatal("end message mismatch")
	}
}

func TestConsumeAssistantStreamDoubleStartRejected(t *testing.T) {
	stream := ai.NewAssistantMessageEventStream()
	partial := &ai.AssistantMessage{Content: []ai.ContentBlock{}, StopReason: ai.StopPending}
	go func() {
		stream.Push(&ai.EventStart{Partial: partial})
		stream.Push(&ai.EventStart{Partial: partial})
		stream.Push(&ai.EventError{Reason: ai.StopError, Error: partial})
	}()
	_, err := ConsumeAssistantStream(stream, &recordingObserver{}, nil, BackgroundContext)
	if err == nil || !strings.Contains(err.Error(), "more than one start") {
		t.Fatalf("err = %v", err)
	}
}

func TestConsumeAssistantStreamAbortRequestedWaitsCancellation(t *testing.T) {
	stream := ai.NewAssistantMessageEventStream()
	partial := &ai.AssistantMessage{Content: []ai.ContentBlock{}, StopReason: ai.StopPending}
	go func() {
		stream.Push(&ai.EventStart{Partial: partial})
		stream.Push(&ai.EventDone{Reason: ai.StopStop, Message: partial})
	}()
	cancellation := make(chan struct{})
	close(cancellation)
	observer := &recordingObserver{}
	settled, err := ConsumeAssistantStream(stream, observer, func(message *ai.AssistantMessage, _ Context) (*ai.AssistantMessage, error) {
		return nil, &AbortRequested{Cancellation: cancellation}
	}, BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	// AbortRequested keeps the settled message and still ends the observer.
	if settled != partial {
		t.Fatal("settled mismatch")
	}
	if observer.ends != 1 {
		t.Fatalf("ends = %d", observer.ends)
	}
	// Other errors propagate.
	stream2 := ai.NewAssistantMessageEventStream()
	go func() {
		stream2.Push(&ai.EventStart{Partial: partial})
		stream2.Push(&ai.EventDone{Reason: ai.StopStop, Message: partial})
	}()
	if _, err := ConsumeAssistantStream(stream2, &recordingObserver{}, func(*ai.AssistantMessage, Context) (*ai.AssistantMessage, error) {
		return nil, ToError("boom")
	}, BackgroundContext); err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamHarnessAssistantWiresOptions(t *testing.T) {
	tool := echoHarnessTool()
	var capturedOptions *ai.SimpleStreamOptions
	var capturedContext ai.Context
	model := &ai.Model{ID: "m", Provider: "p", API: "a"}
	sawPayloadHook := false
	config := &HarnessAssistantStreamConfig{
		Model:         model,
		SystemPrompt:  "be brief",
		Tools:         []ai.Tool{tool.AgentTool.ToTool()},
		ThinkingLevel: "high",
		StreamOptions: AgentHarnessStreamOptions{TimeoutMs: floatPtr(5000)},
		ToProviderMessages: func(messages []agent.AgentMessage, _ Context) []ai.Message {
			out := []ai.Message{}
			for _, m := range messages {
				if aiMsg, ok := m.(ai.Message); ok {
					out = append(out, aiMsg)
				}
			}
			return out
		},
		BeforePayload: func(any, *ai.Model, Context) any {
			sawPayloadHook = true
			return nil
		},
		Request: func(aiContext ai.Context, options ai.SimpleStreamOptions, _ Context) *ai.AssistantMessageEventStream {
			capturedOptions = &options
			capturedContext = aiContext
			stream := ai.NewAssistantMessageEventStream()
			go func() {
				final := &ai.AssistantMessage{Content: []ai.ContentBlock{}, StopReason: ai.StopStop}
				stream.Push(&ai.EventStart{Partial: final})
				stream.Push(&ai.EventDone{Reason: ai.StopStop, Message: final})
			}()
			return stream
		},
		Observer: &recordingObserver{},
	}
	settled, err := StreamHarnessAssistant([]agent.AgentMessage{}, config, BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if settled.StopReason != ai.StopStop {
		t.Fatalf("settled = %+v", settled)
	}
	if capturedContext.SystemPrompt == nil || *capturedContext.SystemPrompt != "be brief" {
		t.Fatalf("systemPrompt = %v", capturedContext.SystemPrompt)
	}
	if len(capturedContext.Tools) != 1 {
		t.Fatalf("tools = %d", len(capturedContext.Tools))
	}
	if capturedOptions.Reasoning == nil || *capturedOptions.Reasoning != "high" {
		t.Fatal("thinking level not forwarded")
	}
	if capturedOptions.TimeoutMs == nil || *capturedOptions.TimeoutMs != 5000 {
		t.Fatal("timeout not forwarded")
	}
	// The payload hook must be wired through OnPayload.
	if capturedOptions.OnPayload == nil {
		t.Fatal("onPayload not wired")
	}
	capturedOptions.OnPayload(nil, model)
	if !sawPayloadHook {
		t.Fatal("beforePayload not invoked")
	}
	// "off" thinking level forwards no reasoning.
	offConfig := *config
	offConfig.ThinkingLevel = "off"
	offConfig.Request = func(ai.Context, ai.SimpleStreamOptions, Context) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		go func() {
			final := &ai.AssistantMessage{Content: []ai.ContentBlock{}, StopReason: ai.StopStop}
			stream.Push(&ai.EventStart{Partial: final})
			stream.Push(&ai.EventDone{Reason: ai.StopStop, Message: final})
		}()
		return stream
	}
	if _, err := StreamHarnessAssistant(nil, &offConfig, BackgroundContext); err != nil {
		t.Fatal(err)
	}
	_ = abort.NewController
}

func floatPtr(f float64) *float64 { return &f }
