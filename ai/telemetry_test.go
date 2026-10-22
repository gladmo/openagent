package ai

// Port of pi-ai test/telemetry.test.ts: the request observer wraps Models
// streams without changing delivered events or results.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/telemetry"
)

type telemetryTestProvider struct {
	model   *Model
	respond func() *AssistantMessageEventStream
}

func (p *telemetryTestProvider) ID() string   { return p.model.Provider }
func (p *telemetryTestProvider) Name() string { return p.model.Provider }
func (p *telemetryTestProvider) Auth() ProviderAuth {
	return NewAPIKeyAuth("Test", "test-key")
}
func (p *telemetryTestProvider) GetModels() []*Model { return []*Model{p.model} }

func (p *telemetryTestProvider) Stream(model *Model, _ *TranscriptContext, _ *StreamOptions) *AssistantMessageEventStream {
	return p.respond()
}

func (p *telemetryTestProvider) StreamSimple(model *Model, _ *TranscriptContext, _ *SimpleStreamOptions) *AssistantMessageEventStream {
	return p.respond()
}

func (p *telemetryTestProvider) FetchDeferred(model *Model, _ *DeferredHandle, _ *DeferredFetchOptions) *AssistantMessageEventStream {
	return p.respond()
}

func (p *telemetryTestProvider) CancelDeferred(*Model, *DeferredHandle, *DeferredCancelOptions) error {
	return nil
}

var telemetryTestModel = &Model{
	ID:            "model",
	Name:          "Model",
	API:           "telemetry-test",
	Provider:      "test",
	BaseURL:       "https://example.test",
	Reasoning:     true,
	Input:         []string{"text"},
	ContextWindow: 1000,
	MaxTokens:     100,
}

func telemetryTestMessage(stopReason string) *AssistantMessage {
	message := FauxAssistantMessage("")
	message.StopReason = stopReason
	message.Content = []ContentBlock{
		&TextContent{Text: "text-secret"},
		&ThinkingContent{Thinking: "thinking-secret"},
		&ToolCall{ID: "call-secret", Name: "lookup", Arguments: jsonxObjFromPairs("value", "argument-secret")},
	}
	message.Usage = Usage{
		Input: 11, Output: 22, CacheRead: 33, CacheWrite: 44, TotalTokens: 110,
		Cost: UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
	}
	responseID := "response"
	message.ResponseID = &responseID
	return message
}

func telemetryTestEvents(result *AssistantMessage) []AssistantMessageEvent {
	toolCall := result.Content[2].(*ToolCall)
	events := []AssistantMessageEvent{
		&EventStart{Partial: result},
		&EventTextDelta{ContentIndex: 0, Delta: "text-secret", Partial: result},
		&EventTextEnd{ContentIndex: 0, Content: "text-secret", Partial: result},
		&EventThinkingEnd{ContentIndex: 1, Content: "thinking-secret", Partial: result},
		&EventToolCallDelta{ContentIndex: 2, Delta: `{"value":`, Partial: result},
		&EventToolCallDelta{ContentIndex: 2, Delta: `"argument-secret"}`, Partial: result},
		&EventToolCallEnd{ContentIndex: 2, ToolCall: toolCall, Partial: result},
	}
	if result.StopReason == StopError || result.StopReason == StopAborted {
		events = append(events, &EventError{Reason: result.StopReason, Error: result})
	} else {
		reason := result.StopReason
		if reason == StopPending {
			reason = StopDeferred
		}
		events = append(events, &EventDone{Reason: reason, Message: result})
	}
	return events
}

func telemetryTestSetup(result *AssistantMessage) (MutableModels, []AssistantMessageEvent) {
	events := telemetryTestEvents(result)
	respond := func() *AssistantMessageEventStream {
		stream := NewAssistantMessageEventStream()
		for _, event := range events {
			stream.Push(event)
		}
		stream.End(result)
		return stream
	}
	models := CreateModels()
	models.SetProvider(&telemetryTestProvider{model: telemetryTestModel, respond: respond})
	return models, events
}

func waitSettled(t *testing.T, recorder *telemetry.InMemoryTelemetryContext) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		settled := true
		observed := false
		for _, span := range recorder.GetSpans() {
			if !strings.HasPrefix(span.Name, "pi.ai.") {
				continue
			}
			observed = true
			if !span.Settled {
				settled = false
			}
		}
		if observed && settled {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("spans did not settle: %+v", recorder.GetSpans())
}

func findRecordedSpan(t *testing.T, recorder *telemetry.InMemoryTelemetryContext, name string) telemetry.RecordedTelemetrySpan {
	t.Helper()
	for _, span := range recorder.GetSpans() {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("missing span %s in %+v", name, recorder.GetSpans())
	return telemetry.RecordedTelemetrySpan{}
}

func TestAITelemetryObservesStream(t *testing.T) {
	for _, method := range []string{"stream", "streamSimple", "fetchDeferred"} {
		t.Run(method, func(t *testing.T) {
			recorder := telemetry.NewInMemoryTelemetryContext()
			result := telemetryTestMessage(StopStop)
			models, streamEvents := telemetryTestSetup(result)
			handle := &DeferredHandle{Provider: telemetryTestModel.Provider, ModelID: telemetryTestModel.ID, API: telemetryTestModel.API, ID: "deferred-secret"}
			reasoning := "high"
			_, err := recorder.StartSpan(telemetry.SpanOptions{Name: "caller"}, func(parent telemetry.TelemetrySpan) (any, error) {
				var stream *AssistantMessageEventStream
				switch method {
				case "stream":
					stream = models.Stream(telemetryTestModel, Context{Messages: nil}, &StreamOptions{Telemetry: parent, Reasoning: &reasoning})
				case "streamSimple":
					stream = models.StreamSimple(telemetryTestModel, Context{Messages: nil}, &SimpleStreamOptions{StreamOptions: StreamOptions{Telemetry: parent, Reasoning: &reasoning}})
				default:
					stream = models.FetchDeferred(telemetryTestModel, handle, &DeferredFetchOptions{StreamOptions: StreamOptions{Telemetry: parent}})
				}
				received := []AssistantMessageEvent{}
				for {
					event, ok := stream.Next()
					if !ok {
						break
					}
					received = append(received, event)
				}
				if len(received) != len(streamEvents) {
					t.Fatalf("received %d events, want %d", len(received), len(streamEvents))
				}
				for i := range received {
					if received[i] != streamEvents[i] {
						t.Fatalf("event[%d] identity changed", i)
					}
				}
				if stream.Result() != result {
					t.Fatal("result identity changed")
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			waitSettled(t, recorder)
			spanName := "pi.ai.request"
			if method == "fetchDeferred" {
				spanName = "pi.ai.poll"
			}
			span := findRecordedSpan(t, recorder, spanName)
			want := telemetry.SpanAttributes{
				"provider":          "test",
				"api":               "telemetry-test",
				"model":             "model",
				"stop":              StopStop,
				"usage.input":       11.0,
				"usage.output":      22.0,
				"usage.cache_read":  33.0,
				"usage.cache_write": 44.0,
				"cost.total":        1.0,
				"response.id":       "response",
			}
			if method != "fetchDeferred" {
				want["reasoning"] = "high"
			}
			if len(span.Attributes) != len(want) {
				t.Fatalf("attributes = %+v, want %+v", span.Attributes, want)
			}
			for key, value := range want {
				if span.Attributes[key] != value {
					t.Fatalf("attribute %s = %v, want %v", key, span.Attributes[key], value)
				}
			}
			if len(span.Events) != 4 {
				t.Fatalf("events = %+v", span.Events)
			}
			if span.Events[0].Name != "pi.ai.first_chunk" {
				t.Fatalf("events = %+v", span.Events)
			}
			blocks := span.Events[1:]
			expectedBlocks := []telemetry.SpanAttributes{
				{"type": "text", "index": 0.0, "chars": 11.0},
				{"type": "thinking", "index": 1.0, "chars": 15.0},
				{"type": "tool_call", "index": 2.0, "chars": 27.0, "tool": "lookup"},
			}
			for i, block := range expectedBlocks {
				event := blocks[i]
				if event.Name != "pi.ai.block" || len(event.Attributes) != len(block) {
					t.Fatalf("block[%d] = %+v, want %+v", i, event, block)
				}
				for key, value := range block {
					if event.Attributes[key] != value {
						t.Fatalf("block[%d].%s = %v, want %v", i, key, event.Attributes[key], value)
					}
				}
			}
			serialized, _ := json.Marshal(span)
			for _, secret := range []string{"text-secret", "thinking-secret", "argument-secret", "call-secret", "deferred-secret"} {
				if strings.Contains(string(serialized), secret) {
					t.Fatalf("span leaked %s", secret)
				}
			}
			measurements := recorder.GetMetrics()
			if len(measurements) != 7 {
				t.Fatalf("measurements = %+v", measurements)
			}
			counterValues := []float64{}
			for _, measurement := range measurements {
				if measurement.Kind == "counter" {
					counterValues = append(counterValues, measurement.Value)
				}
			}
			wantCounters := []float64{11, 22, 33, 44, 1}
			if len(counterValues) != len(wantCounters) {
				t.Fatalf("counters = %v", counterValues)
			}
			for i := range wantCounters {
				if counterValues[i] != wantCounters[i] {
					t.Fatalf("counters = %v", counterValues)
				}
			}
			for _, measurement := range measurements {
				keys := make([]string, 0, len(measurement.Attributes))
				for key := range measurement.Attributes {
					keys = append(keys, key)
				}
				sortStrings(keys)
				var wantKeys []string
				switch measurement.Name {
				case "pi.ai.tokens":
					wantKeys = []string{"kind", "model", "provider"}
				case "pi.ai.request.duration":
					wantKeys = []string{"model", "provider", "stop"}
				default:
					wantKeys = []string{"model", "provider"}
				}
				if strings.Join(keys, ",") != strings.Join(wantKeys, ",") {
					t.Fatalf("measurement %s keys = %v, want %v", measurement.Name, keys, wantKeys)
				}
			}
		})
	}
}

func TestAITelemetryReportsErrorAndAbortedStatus(t *testing.T) {
	for _, reason := range []string{StopError, StopAborted} {
		t.Run(reason, func(t *testing.T) {
			recorder := telemetry.NewInMemoryTelemetryContext()
			result := telemetryTestMessage(reason)
			errorMessage := "error-secret"
			result.ErrorMessage = &errorMessage
			models, _ := telemetryTestSetup(result)
			var message *AssistantMessage
			_, err := telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "caller"}, func(parent telemetry.TelemetrySpan) (any, error) {
				var completeErr error
				message, completeErr = models.CompleteSimple(telemetryTestModel, Context{Messages: nil}, &SimpleStreamOptions{StreamOptions: StreamOptions{Telemetry: parent}})
				return nil, completeErr
			})
			if err != nil {
				t.Fatal(err)
			}
			if message != result {
				t.Fatal("result identity changed")
			}
			waitSettled(t, recorder)
			span := findRecordedSpan(t, recorder, "pi.ai.request")
			if reason == StopError {
				want := telemetry.StatusError("provider_error", "error-secret")
				if !span.Status.Equal(want) {
					t.Fatalf("status = %+v", span.Status)
				}
			} else if !span.Status.Equal(telemetry.StatusOK()) {
				t.Fatalf("status = %+v", span.Status)
			}
			if span.Attributes["stop"] != reason {
				t.Fatalf("stop = %v", span.Attributes["stop"])
			}
			serialized, _ := json.Marshal(span)
			if strings.Contains(string(serialized), "error-secret") && reason == StopAborted {
				t.Fatal("aborted span leaked the error message")
			}
		})
	}
}

func TestAITelemetryBoundsPollErrorMessages(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "present", true: "missing"}[missing], func(t *testing.T) {
			recorder := telemetry.NewInMemoryTelemetryContext()
			result := telemetryTestMessage(StopError)
			if !missing {
				long := strings.Repeat("x", 500) + "discarded-tail"
				result.ErrorMessage = &long
			}
			models, _ := telemetryTestSetup(result)
			handle := &DeferredHandle{Provider: telemetryTestModel.Provider, ModelID: telemetryTestModel.ID, API: telemetryTestModel.API, ID: "d"}
			_, err := telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "caller"}, func(parent telemetry.TelemetrySpan) (any, error) {
				return models.FetchDeferred(telemetryTestModel, handle, &DeferredFetchOptions{StreamOptions: StreamOptions{Telemetry: parent}}).Result(), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			waitSettled(t, recorder)
			span := findRecordedSpan(t, recorder, "pi.ai.poll")
			wantMessage := ""
			if !missing {
				wantMessage = strings.Repeat("x", 500)
			}
			if !span.Status.Equal(telemetry.StatusError("provider_error", wantMessage)) {
				t.Fatalf("status = %+v", span.Status)
			}
			serialized, _ := json.Marshal(span)
			if strings.Contains(string(serialized), "discarded-tail") {
				t.Fatal("span leaked the unbounded message")
			}
			attributesJSON, _ := json.Marshal(span.Attributes)
			eventsJSON, _ := json.Marshal(span.Events)
			if !missing && (strings.Contains(string(attributesJSON), "xxx") || strings.Contains(string(eventsJSON), "xxx")) {
				t.Fatal("attributes or events leaked the unbounded message")
			}
		})
	}
}

func TestAITelemetryRecordsSetupFailures(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	models := CreateModels()
	unknown := &Model{ID: "missing", Name: "Missing", API: "telemetry-test", Provider: "nowhere"}
	_ = models.StreamSimple(unknown, Context{Messages: nil}, &SimpleStreamOptions{StreamOptions: StreamOptions{Telemetry: spanOf(t, recorder)}})
	waitSettled(t, recorder)
	span := findRecordedSpan(t, recorder, "pi.ai.request")
	if span.Status.Status != "error" {
		t.Fatalf("status = %+v", span.Status)
	}
}

func spanOf(t *testing.T, recorder *telemetry.InMemoryTelemetryContext) telemetry.TelemetrySpan {
	t.Helper()
	spanBox := make(chan telemetry.TelemetrySpan, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	go func() {
		_, _ = recorder.StartSpan(telemetry.SpanOptions{Name: "caller"}, func(span telemetry.TelemetrySpan) (any, error) {
			spanBox <- span
			<-release
			return nil, nil
		})
	}()
	return <-spanBox
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func jsonxObjFromPairs(key, value string) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set(key, value)
	return obj
}
