package ai

// api_anthropic_messages_test.go ports the SSE-replay essence of
// anthropic-sse-parsing.test.ts and neighbors against an httptest server
// (the TS tests inject a fake SDK client; the Go client speaks the wire
// protocol directly, so the server replays the same fixtures).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

type sseFixture struct {
	event string
	data  string
}

type capturedRequest struct {
	url     string
	headers http.Header
	body    []byte
}

func sseServer(t *testing.T, events []sseFixture, capture *capturedRequest) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			body, _ := io.ReadAll(r.Body)
			capture.url = r.URL.String()
			capture.headers = r.Header.Clone()
			capture.body = body
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, fixture := range events {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", fixture.event, fixture.data)
			flusher.Flush()
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func testAnthropicModel(baseURL string) *Model {
	return &Model{
		ID:            "claude-haiku-4-5",
		Name:          "Claude Haiku 4.5",
		API:           "anthropic-messages",
		Provider:      "anthropic",
		BaseURL:       baseURL,
		Input:         []string{"text"},
		ContextWindow: 200000,
		MaxTokens:     8192,
		Cost:          ModelCost{ModelCostRates: ModelCostRates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 6.25}},
	}
}

func userContext(text string) *TranscriptContext {
	return NewTranscriptContext([]Message{&UserMessage{Content: StringContent(text), TimestampMs: 1}})
}

func collectStream(t *testing.T, stream *AssistantMessageEventStream) ([]AssistantMessageEvent, *AssistantMessage) {
	t.Helper()
	done := make(chan struct{})
	var events []AssistantMessageEvent
	var result *AssistantMessage
	go func() {
		defer close(done)
		for {
			event, ok := stream.Next()
			if !ok {
				break
			}
			events = append(events, event)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stream did not terminate")
	}
	result = stream.Result()
	return events, result
}

func minimalAnthropicEvents() []sseFixture {
	return []sseFixture{
		{"message_start", `{"type":"message_start","message":{"id":"msg_test","usage":{"input_tokens":12,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":12,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}
}

func TestAnthropicStreamParsesSSE(t *testing.T) {
	server := sseServer(t, minimalAnthropicEvents(), nil)
	model := testAnthropicModel(server.URL)
	apiKey := "sk-test"

	providerEvents := []any{}
	options := &AnthropicOptions{
		StreamOptions: StreamOptions{
			APIKey:                &apiKey,
			OnProviderStreamEvent: func(data any, _ *Model) { providerEvents = append(providerEvents, data) },
		},
	}

	events, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("Hello"), options))

	var types []string
	for _, event := range events {
		types = append(types, event.EventType())
	}
	want := []string{"start", "text_start", "text_delta", "text_end", "done"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("event sequence: got %v want %v", types, want)
	}
	if len(providerEvents) != 6 {
		t.Fatalf("expected 6 provider events, got %d", len(providerEvents))
	}
	if result.ResponseID == nil || *result.ResponseID != "msg_test" {
		t.Fatalf("expected responseId msg_test, got %v", result.ResponseID)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if text, ok := result.Content[0].(TextContent); !ok || text.Text != "Hello" {
		t.Fatalf("unexpected content: %#v", result.Content[0])
	}
	if result.Usage.Input != 12 || result.Usage.Output != 5 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if result.Usage.TotalTokens != 17 {
		t.Fatalf("expected totalTokens 17, got %v", result.Usage.TotalTokens)
	}
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s", result.StopReason)
	}
	expectedInputCost := 3.0 / 1000000 * 12
	if diff := result.Usage.Cost.Input - expectedInputCost; diff < -1e-12 || diff > 1e-12 {
		t.Fatalf("unexpected input cost: %v", result.Usage.Cost.Input)
	}
}

func TestAnthropicStreamRequestShape(t *testing.T) {
	var capture capturedRequest
	server := sseServer(t, minimalAnthropicEvents(), &capture)
	model := testAnthropicModel(server.URL)
	apiKey := "sk-test"

	_, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("Hello"), &AnthropicOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s: %v", result.StopReason, result.ErrorMessage)
	}

	if capture.url != "/v1/messages?beta=true" {
		t.Fatalf("unexpected url: %s", capture.url)
	}
	if got := capture.headers.Get("x-api-key"); got != "sk-test" {
		t.Fatalf("expected x-api-key header, got %q", got)
	}
	if got := capture.headers.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("expected anthropic-version header, got %q", got)
	}

	var params map[string]any
	if err := json.Unmarshal(capture.body, &params); err != nil {
		t.Fatal(err)
	}
	if params["model"] != "claude-haiku-4-5" {
		t.Fatalf("unexpected model: %v", params["model"])
	}
	if params["stream"] != true {
		t.Fatalf("expected stream=true")
	}
	if params["max_tokens"] != float64(8192) {
		t.Fatalf("expected max_tokens 8192, got %v", params["max_tokens"])
	}
	messages, ok := params["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("unexpected messages: %v", params["messages"])
	}
	first, ok := messages[0].(map[string]any)
	if !ok || first["role"] != "user" {
		t.Fatalf("unexpected first message: %v", messages[0])
	}
	// Default cache retention ("short") wraps the trailing user string
	// content into a block array carrying cache_control.
	content, ok := first["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("unexpected content: %v", first["content"])
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "text" || block["text"] != "Hello" {
		t.Fatalf("unexpected content block: %v", content[0])
	}
	cacheControl, ok := block["cache_control"].(map[string]any)
	if !ok || cacheControl["type"] != "ephemeral" {
		t.Fatalf("expected ephemeral cache_control, got %v", block["cache_control"])
	}
	if _, has := params["betas"]; has {
		t.Fatalf("expected no betas for non-reasoning model, got %v", params["betas"])
	}
	if _, has := params["thinking"]; has {
		t.Fatalf("expected no thinking param when not enabled, got %v", params["thinking"])
	}
}

func TestAnthropicStreamToolCall(t *testing.T) {
	events := []sseFixture{
		{"message_start", `{"type":"message_start","message":{"id":"msg_tool","usage":{"input_tokens":10,"output_tokens":0}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"Paris\"}"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":10,"output_tokens":7}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}
	server := sseServer(t, events, nil)
	model := testAnthropicModel(server.URL)
	apiKey := "sk-test"

	streamEvents, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("weather?"), &AnthropicOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))

	if result.StopReason != StopToolUse {
		t.Fatalf("expected toolUse, got %s", result.StopReason)
	}
	call, ok := result.Content[0].(*ToolCall)
	if !ok {
		t.Fatalf("expected ToolCall, got %#v", result.Content[0])
	}
	if call.ID != "toolu_1" || call.Name != "get_weather" {
		t.Fatalf("unexpected call: %+v", call)
	}
	if city, _ := call.Arguments.Get("city"); city != "Paris" {
		t.Fatalf("expected city=Paris, got %v (%s)", city, jsonx.Stringify(call.Arguments))
	}

	var kinds []string
	var deltas []string
	for _, event := range streamEvents {
		switch e := event.(type) {
		case *EventToolCallDelta:
			deltas = append(deltas, e.Delta)
			kinds = append(kinds, "toolcall_delta")
		case *EventToolCallEnd:
			kinds = append(kinds, "toolcall_end")
		}
	}
	if strings.Join(kinds, ",") != "toolcall_delta,toolcall_delta,toolcall_end" {
		t.Fatalf("unexpected toolcall events: %v", kinds)
	}
	if strings.Join(deltas, "|") != `{"city":|"Paris"}` {
		t.Fatalf("unexpected deltas: %v", deltas)
	}
}

func TestAnthropicStreamThinkingAndSignature(t *testing.T) {
	events := []sseFixture{
		{"message_start", `{"type":"message_start","message":{"id":"msg_think","usage":{"input_tokens":10,"output_tokens":0}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"pondering"}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig1"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":1}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":10,"output_tokens":9}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}
	server := sseServer(t, events, nil)
	model := testAnthropicModel(server.URL)
	apiKey := "sk-test"

	_, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("think"), &AnthropicOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))

	if len(result.Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(result.Content))
	}
	thinking, ok := result.Content[0].(ThinkingContent)
	if !ok {
		t.Fatalf("expected ThinkingContent, got %#v", result.Content[0])
	}
	if thinking.Thinking != "pondering" {
		t.Fatalf("unexpected thinking: %q", thinking.Thinking)
	}
	if thinking.ThinkingSignature == nil || *thinking.ThinkingSignature != "sig1" {
		t.Fatalf("unexpected signature: %v", thinking.ThinkingSignature)
	}
	redacted, ok := result.Content[1].(ThinkingContent)
	if !ok || redacted.Redacted == nil || !*redacted.Redacted {
		t.Fatalf("expected redacted thinking, got %#v", result.Content[1])
	}
	if redacted.Thinking != "[Reasoning redacted]" || *redacted.ThinkingSignature != "opaque" {
		t.Fatalf("unexpected redacted block: %+v", redacted)
	}
}

func TestAnthropicStreamErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"Number of requests has exceeded your per-minute rate limit"}}`))
	}))
	t.Cleanup(server.Close)

	model := testAnthropicModel(server.URL)
	apiKey := "sk-test"
	_, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("Hello"), &AnthropicOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))

	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	// The SDK message embeds the stringified whole body.
	want := `429 {"type":"error","error":{"type":"rate_limit_error","message":"Number of requests has exceeded your per-minute rate limit"}}`
	if result.ErrorMessage == nil || *result.ErrorMessage != want {
		t.Fatalf("expected %q, got %v", want, *result.ErrorMessage)
	}
}

func TestAnthropicStreamSSEErrorEvent(t *testing.T) {
	events := []sseFixture{
		{"message_start", `{"type":"message_start","message":{"id":"msg_x","usage":{"input_tokens":1,"output_tokens":0}}}`},
		{"error", `{"type":"error","error":{"message":"overloaded"}}`},
	}
	server := sseServer(t, events, nil)
	model := testAnthropicModel(server.URL)
	apiKey := "sk-test"

	_, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("Hello"), &AnthropicOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "overloaded") {
		t.Fatalf("expected overloaded message, got %v", result.ErrorMessage)
	}
}

func TestAnthropicStreamMissingStopReason(t *testing.T) {
	events := []sseFixture{
		{"message_start", `{"type":"message_start","message":{"id":"msg_x","usage":{"input_tokens":1,"output_tokens":0}}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}
	server := sseServer(t, events, nil)
	model := testAnthropicModel(server.URL)
	apiKey := "sk-test"

	_, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("Hello"), &AnthropicOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	if result.ErrorMessage == nil || *result.ErrorMessage != "Anthropic stream ended without a stop reason" {
		t.Fatalf("unexpected error: %v", result.ErrorMessage)
	}
}

func TestAnthropicStreamMissingAPIKey(t *testing.T) {
	server := sseServer(t, minimalAnthropicEvents(), nil)
	model := testAnthropicModel(server.URL)

	_, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("Hello"), &AnthropicOptions{}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	if result.ErrorMessage == nil || *result.ErrorMessage != "No API key for provider: anthropic" {
		t.Fatalf("unexpected error: %v", result.ErrorMessage)
	}
}

func TestAnthropicStreamMidStreamAbort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_a\",\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hel\"}}\n\n")
		flusher.Flush()
		// Hold the stream open until the client disconnects.
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	controller := abort.NewController()
	model := testAnthropicModel(server.URL)
	apiKey := "sk-test"
	stream := AnthropicMessagesApi().StreamAnthropic(model, userContext("Hello"), &AnthropicOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey, Signal: controller.Signal()},
	})
	time.AfterFunc(100*time.Millisecond, controller.Abort)
	_, result := collectStream(t, stream)

	if result.StopReason != StopAborted {
		t.Fatalf("expected aborted, got %s (%v)", result.StopReason, result.ErrorMessage)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected partial content preserved, got %d blocks", len(result.Content))
	}
	if text, ok := result.Content[0].(TextContent); !ok || text.Text != "Hel" {
		t.Fatalf("unexpected partial content: %#v", result.Content[0])
	}
}

func TestAnthropicStreamCacheWrite1hCost(t *testing.T) {
	events := []sseFixture{
		{"message_start", `{"type":"message_start","message":{"id":"msg_c","usage":{"input_tokens":100,"output_tokens":0,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_1h_input_tokens":10}}}}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":100,"output_tokens":5}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}
	server := sseServer(t, events, nil)
	model := testAnthropicModel(server.URL)
	apiKey := "sk-test"

	_, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("Hello"), &AnthropicOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))

	// cacheWrite rate 6.25 $/M; 10 short + 10 long (2x input rate 3) =>
	// (6.25*10 + 3*2*10) / 1e6.
	expected := (6.25*10 + 3*2*10) / 1000000
	if diff := result.Usage.Cost.CacheWrite - expected; diff < -1e-12 || diff > 1e-12 {
		t.Fatalf("expected cacheWrite cost %v, got %v", expected, result.Usage.Cost.CacheWrite)
	}
	if result.Usage.CacheWrite1h == nil || *result.Usage.CacheWrite1h != 10 {
		t.Fatalf("expected cacheWrite1h 10, got %v", result.Usage.CacheWrite1h)
	}
}

func TestAnthropicStreamSimpleThinkingBudget(t *testing.T) {
	var capture capturedRequest
	server := sseServer(t, minimalAnthropicEvents(), &capture)
	model := testAnthropicModel(server.URL)
	model.Reasoning = true
	apiKey := "sk-test"

	reasoning := ThinkingHigh
	_, result := collectStream(t, AnthropicMessagesApi().StreamSimple(model, userContext("Hello"), &SimpleStreamOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
		Reasoning:     &reasoning,
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	var params map[string]any
	if err := json.Unmarshal(capture.body, &params); err != nil {
		t.Fatal(err)
	}
	thinking, ok := params["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("expected thinking param, got %v", params["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("expected enabled thinking, got %v", thinking["type"])
	}
	// high = 16384 budget, capped to leave 1024 answer tokens under 8192.
	if thinking["budget_tokens"] != float64(8192-1024) {
		t.Fatalf("unexpected budget: %v", thinking["budget_tokens"])
	}
	if params["max_tokens"] != float64(8192) {
		t.Fatalf("unexpected max_tokens: %v", params["max_tokens"])
	}
}

func TestAnthropicStreamSimpleAdaptiveEffort(t *testing.T) {
	var capture capturedRequest
	server := sseServer(t, minimalAnthropicEvents(), &capture)
	model := testAnthropicModel(server.URL)
	model.Reasoning = true
	model.Compat = jsonx.ObjFrom("forceAdaptiveThinking", true)
	apiKey := "sk-test"

	reasoning := ThinkingMedium
	_, result := collectStream(t, AnthropicMessagesApi().StreamSimple(model, userContext("Hello"), &SimpleStreamOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
		Reasoning:     &reasoning,
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	var params map[string]any
	if err := json.Unmarshal(capture.body, &params); err != nil {
		t.Fatal(err)
	}
	thinking, ok := params["thinking"].(map[string]any)
	if !ok || thinking["type"] != "adaptive" {
		t.Fatalf("expected adaptive thinking, got %v", params["thinking"])
	}
	outputConfig, ok := params["output_config"].(map[string]any)
	if !ok || outputConfig["effort"] != "medium" {
		t.Fatalf("expected effort medium, got %v", params["output_config"])
	}
}

func TestAnthropicStreamRetryOn429(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("retry-after-ms", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"message":"rate limited"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_r\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(server.Close)

	model := testAnthropicModel(server.URL)
	apiKey := "sk-test"
	retries := 1.0
	_, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("Hello"), &AnthropicOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey, MaxRetries: &retries},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop after retry, got %s (%v)", result.StopReason, result.ErrorMessage)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}
