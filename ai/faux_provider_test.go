package ai

// Ports of pi/packages/ai/test/faux-provider.test.ts (core subset: usage
// estimation, block helpers, queue semantics, factories, error/aborted
// streaming, event order, tool calls, caching).

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func collectEvents(t *testing.T, stream *AssistantMessageEventStream) ([]string, *AssistantMessage) {
	t.Helper()
	var types []string
	var final *AssistantMessage
	for {
		event, ok := stream.Next()
		if !ok {
			break
		}
		types = append(types, event.EventType())
		switch e := event.(type) {
		case *EventDone:
			final = e.Message
		case *EventError:
			final = e.Error
		}
	}
	return types, final
}

func TestFauxRegistersCustomProviderAndEstimatesUsage(t *testing.T) {
	faux := FauxProvider(RegisterFauxProviderOptions{API: "custom", Provider: "custom-prov"})
	models := CreateModels()
	models.SetProvider(faux.Provider)
	faux.SetResponses([]FauxResponseStep{FauxStep(FauxAssistantMessage("hi"))})

	model := faux.GetModel()
	result, err := CompleteSimple(models, model, Context{Messages: []Message{
		&UserMessage{Content: StringContent("hello"), TimestampMs: 1},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "custom-prov" || result.API != "custom" {
		t.Fatalf("message = %s/%s", result.Provider, result.API)
	}
	// 12-char serialized prompt ("user:hello") -> 3 tokens input; "hi" -> 1 output.
	if result.Usage.Input < 1 || result.Usage.Output < 1 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if faux.State.CallCount() != 1 {
		t.Fatalf("callCount = %d", faux.State.CallCount())
	}
}

func CompleteSimple(models Models, model *Model, context Context, options *SimpleStreamOptions) (*AssistantMessage, error) {
	return models.CompleteSimple(model, context, options)
}

func TestFauxHelperBlocks(t *testing.T) {
	faux := FauxProvider(RegisterFauxProviderOptions{})
	models := CreateModels()
	models.SetProvider(faux.Provider)
	faux.SetResponses([]FauxResponseStep{FauxStep(FauxAssistantMessage([]ContentBlock{
		FauxThinking("reasoning"),
		FauxText("answer"),
		FauxToolCall("read", jsonx.ObjFrom("path", "x")),
	}))})
	result, err := models.CompleteSimple(faux.GetModel(), Context{Messages: []Message{
		&UserMessage{Content: StringContent("q"), TimestampMs: 1},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 3 {
		t.Fatalf("content = %d", len(result.Content))
	}
	if thinking, ok := result.Content[0].(ThinkingContent); !ok || thinking.Thinking != "reasoning" {
		t.Fatalf("block 0 = %+v", result.Content[0])
	}
	if text, ok := result.Content[1].(TextContent); !ok || text.Text != "answer" {
		t.Fatalf("block 1 = %+v", result.Content[1])
	}
	call, ok := result.Content[2].(*ToolCall)
	if !ok || call.Name != "read" || jsonx.Stringify(call.Arguments) != `{"path":"x"}` {
		t.Fatalf("block 2 = %+v", result.Content[2])
	}
	if result.StopReason != StopStop {
		t.Fatalf("stopReason = %s", result.StopReason)
	}
}

func TestFauxQueueSemantics(t *testing.T) {
	faux := FauxProvider(RegisterFauxProviderOptions{})
	models := CreateModels()
	models.SetProvider(faux.Provider)
	model := faux.GetModel()
	one := FauxAssistantMessage("one")
	two := FauxAssistantMessage("two")
	faux.SetResponses([]FauxResponseStep{FauxStep(one), FauxStep(two)})

	first, err := models.CompleteSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("a"), TimestampMs: 1}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := models.CompleteSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("b"), TimestampMs: 2}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Content[0].(TextContent).Text != "one" || second.Content[0].(TextContent).Text != "two" {
		t.Fatal("queue order wrong")
	}
	// Exhausted queue errors.
	third, err := models.CompleteSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("c"), TimestampMs: 3}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if third.StopReason != StopError || third.ErrorMessage == nil || *third.ErrorMessage != "No more faux responses queued" {
		t.Fatalf("third = %+v", third)
	}

	// Replace and append.
	faux.SetResponses([]FauxResponseStep{FauxStep(FauxAssistantMessage("replaced"))})
	faux.AppendResponses([]FauxResponseStep{FauxStep(FauxAssistantMessage("appended"))})
	if faux.GetPendingResponseCount() != 2 {
		t.Fatalf("pending = %d", faux.GetPendingResponseCount())
	}
}

func TestFauxAsyncFactoriesAndErrors(t *testing.T) {
	faux := FauxProvider(RegisterFauxProviderOptions{})
	models := CreateModels()
	models.SetProvider(faux.Provider)
	model := faux.GetModel()

	faux.SetResponses([]FauxResponseStep{FauxStepFn(func(_ *TranscriptContext, _ *SimpleStreamOptions, state *FauxProviderState, m *Model) *AssistantMessage {
		return FauxAssistantMessage("factory:" + m.ID)
	})})
	result, err := models.CompleteSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("x"), TimestampMs: 1}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := result.Content[0].(TextContent).Text; text != "factory:"+model.ID {
		t.Fatalf("text = %s", text)
	}

	// Throwing factory surfaces as an error message.
	faux.SetResponses([]FauxResponseStep{FauxStepFn(func(*TranscriptContext, *SimpleStreamOptions, *FauxProviderState, *Model) *AssistantMessage {
		panic(errorFromString("factory boom"))
	})})
	result, err = models.CompleteSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("y"), TimestampMs: 2}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopError || result.ErrorMessage == nil || *result.ErrorMessage != "factory boom" {
		t.Fatalf("result = %+v", result)
	}

	// Pending stop reason is rejected.
	pending := FauxAssistantMessage("stuck")
	pending.StopReason = StopPending
	faux.SetResponses([]FauxResponseStep{FauxStep(pending)})
	result, err = models.CompleteSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("z"), TimestampMs: 3}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopError || result.ErrorMessage == nil || *result.ErrorMessage != "Faux response ended without a stop reason" {
		t.Fatalf("pending result = %+v", result)
	}
}

func TestFauxPromptCacheSimulation(t *testing.T) {
	faux := FauxProvider(RegisterFauxProviderOptions{})
	models := CreateModels()
	models.SetProvider(faux.Provider)
	model := faux.GetModel()
	session := "s1"
	sessionOptions := &SimpleStreamOptions{StreamOptions: StreamOptions{SessionID: &session}}

	first, err := models.CompleteSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("aaaa"), TimestampMs: 1}}}, sessionOptions)
	if err != nil {
		t.Fatal(err)
	}
	if first.Usage.CacheWrite == 0 || first.Usage.CacheRead != 0 {
		t.Fatalf("first usage = %+v", first.Usage)
	}

	// Same session, extended prompt: prefix read from cache, remainder written.
	second, err := models.CompleteSimple(model, Context{Messages: []Message{
		&UserMessage{Content: StringContent("aaaa"), TimestampMs: 1},
		&AssistantMessage{Content: []ContentBlock{TextContent{Text: "r"}}, StopReason: StopStop, TimestampMs: 2},
		&UserMessage{Content: StringContent("bbbb"), TimestampMs: 3},
	}}, sessionOptions)
	if err != nil {
		t.Fatal(err)
	}
	if second.Usage.CacheRead == 0 {
		t.Fatalf("second usage = %+v", second.Usage)
	}

	// No sessionId: no caching.
	none := "none"
	_ = none
	third, err := models.CompleteSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("cccc"), TimestampMs: 4}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if third.Usage.CacheRead != 0 || third.Usage.CacheWrite != 0 {
		t.Fatalf("uncached usage = %+v", third.Usage)
	}

	// cacheRetention none disables caching.
	noRetention := CacheRetentionNone
	fourth, err := models.CompleteSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("dddd"), TimestampMs: 5}}},
		&SimpleStreamOptions{StreamOptions: StreamOptions{SessionID: &session, CacheRetention: &noRetention}})
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Usage.CacheRead != 0 || fourth.Usage.CacheWrite != 0 {
		t.Fatalf("retention-none usage = %+v", fourth.Usage)
	}
}

func TestFauxStreamsEventOrder(t *testing.T) {
	faux := FauxProvider(RegisterFauxProviderOptions{TokenSizeMin: 4, TokenSizeMax: 4})
	models := CreateModels()
	models.SetProvider(faux.Provider)
	faux.SetResponses([]FauxResponseStep{FauxStep(FauxAssistantMessage([]ContentBlock{
		FauxText("0123456789"),
	}))})
	stream := models.StreamSimple(faux.GetModel(), Context{Messages: []Message{&UserMessage{Content: StringContent("q"), TimestampMs: 1}}}, nil)
	types, final := collectEvents(t, stream)
	// Chunks of 16 chars: one chunk for 10 chars.
	want := []string{"start", "text_start", "text_delta", "text_end", "done"}
	if len(types) != len(want) {
		t.Fatalf("types = %v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("types = %v", types)
		}
	}
	if final.Content[0].(TextContent).Text != "0123456789" {
		t.Fatalf("final = %+v", final.Content)
	}
}

func TestFauxStreamsToolCalls(t *testing.T) {
	faux := FauxProvider(RegisterFauxProviderOptions{TokenSizeMin: 4, TokenSizeMax: 4})
	models := CreateModels()
	models.SetProvider(faux.Provider)
	faux.SetResponses([]FauxResponseStep{FauxStep(FauxAssistantMessage([]ContentBlock{
		FauxToolCall("a", jsonx.NewObj()),
		FauxToolCall("b", jsonx.ObjFrom("x", float64(1))),
	}))})
	stream := models.StreamSimple(faux.GetModel(), Context{Messages: []Message{&UserMessage{Content: StringContent("q"), TimestampMs: 1}}}, nil)
	types, final := collectEvents(t, stream)
	want := []string{"start", "toolcall_start", "toolcall_delta", "toolcall_end", "toolcall_start", "toolcall_delta", "toolcall_end", "done"}
	if len(types) != len(want) {
		t.Fatalf("types = %v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("types = %v", types)
		}
	}
	if final.StopReason != StopStop || len(final.Content) != 2 {
		t.Fatalf("final = %s %d", final.StopReason, len(final.Content))
	}
}

func TestFauxStreamsExplicitErrorAndAborted(t *testing.T) {
	faux := FauxProvider(RegisterFauxProviderOptions{})
	models := CreateModels()
	models.SetProvider(faux.Provider)
	model := faux.GetModel()

	errMsg := "provider exploded"
	errMsgMsg := FauxAssistantMessage("")
	errMsgMsg.StopReason = StopError
	errMsgMsg.ErrorMessage = &errMsg
	faux.SetResponses([]FauxResponseStep{FauxStep(errMsgMsg)})
	result, err := models.CompleteSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("a"), TimestampMs: 1}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopError || result.ErrorMessage == nil || *result.ErrorMessage != "provider exploded" {
		t.Fatalf("error result = %+v", result)
	}

	abortedMsg := FauxAssistantMessage("")
	abortedMsg.StopReason = StopAborted
	faux.SetResponses([]FauxResponseStep{FauxStep(abortedMsg)})
	result, err = models.CompleteSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("b"), TimestampMs: 2}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopAborted {
		t.Fatalf("aborted result = %+v", result)
	}
}
