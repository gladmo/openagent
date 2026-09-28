package ai

// api_openai_responses_test.go ports the SSE-replay essence of the
// openai-responses tests (terminal event, tool-call ids, reasoning replay,
// usage parsing, request shape) against an httptest server.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func responsesServer(t *testing.T, events []sseFixture, capture *capturedRequest) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
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

func openaiResponsesModel(baseURL string) *Model {
	return &Model{
		ID:            "gpt-5.1",
		API:           "openai-responses",
		Provider:      "openai",
		BaseURL:       baseURL,
		Input:         []string{"text"},
		ContextWindow: 128000,
		MaxTokens:     4096,
		Cost:          ModelCost{ModelCostRates: ModelCostRates{Input: 2, Output: 10}},
	}
}

func minimalResponsesEvents() []sseFixture {
	return []sseFixture{
		{"response.created", `{"type":"response.created","response":{"id":"resp_1"}}`},
		{"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`},
		{"response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"delta":"Hel"}`},
		{"response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"delta":"lo"}`},
		{"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello","annotations":[]}]}}`},
		{"response.completed", `{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":100,"output_tokens":5,"total_tokens":105,"input_tokens_details":{"cached_tokens":20}},"output":[]}}`},
	}
}

func TestOpenAIResponsesTextStream(t *testing.T) {
	server := responsesServer(t, minimalResponsesEvents(), nil)
	model := openaiResponsesModel(server.URL)
	apiKey := "sk-oai"

	events, result := collectStream(t, OpenAIResponsesApi().StreamResponses(model, userContext("Hello"), &OpenAIResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))

	var types []string
	for _, event := range events {
		types = append(types, event.EventType())
	}
	want := []string{"start", "text_start", "text_delta", "text_delta", "text_end", "done"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("event sequence: got %v want %v", types, want)
	}
	if result.ResponseID == nil || *result.ResponseID != "resp_1" {
		t.Fatalf("unexpected responseId: %v", result.ResponseID)
	}
	text, ok := result.Content[0].(TextContent)
	if !ok || text.Text != "Hello" {
		t.Fatalf("unexpected content: %#v", result.Content[0])
	}
	if text.TextSignature == nil {
		t.Fatal("expected text signature")
	}
	parsed := parseTextSignature(*text.TextSignature)
	if parsed == nil || parsed.ID != "msg_1" {
		t.Fatalf("unexpected signature: %v", text.TextSignature)
	}
	// input = 100 - 20 cached = 80.
	if result.Usage.Input != 80 || result.Usage.CacheRead != 20 || result.Usage.Output != 5 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if result.Usage.TotalTokens != 105 {
		t.Fatalf("expected provider totalTokens 105, got %v", result.Usage.TotalTokens)
	}
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s", result.StopReason)
	}
}

func TestOpenAIResponsesReasoning(t *testing.T) {
	events := []sseFixture{
		{"response.created", `{"type":"response.created","response":{"id":"resp_r"}}`},
		{"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1"}}`},
		{"response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"thinking"}`},
		{"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thinking"}],"encrypted_content":"enc"}}`},
		{"response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`},
		{"response.output_text.delta", `{"type":"response.output_text.delta","output_index":1,"delta":"answer"}`},
		{"response.output_item.done", `{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer","annotations":[]}]}}`},
		{"response.completed", `{"type":"response.completed","response":{"id":"resp_r","status":"completed","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12},"output":[]}}`},
	}
	server := responsesServer(t, events, nil)
	model := openaiResponsesModel(server.URL)
	apiKey := "sk-oai"

	_, result := collectStream(t, OpenAIResponsesApi().StreamResponses(model, userContext("Hello"), &OpenAIResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))

	if len(result.Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(result.Content))
	}
	thinking, ok := result.Content[0].(ThinkingContent)
	if !ok || thinking.Thinking != "thinking" {
		t.Fatalf("unexpected thinking: %#v", result.Content[0])
	}
	if thinking.ThinkingSignature == nil {
		t.Fatal("expected reasoning item signature")
	}
	signature, err := ParseJSONWithRepair(*thinking.ThinkingSignature)
	if err != nil {
		t.Fatal(err)
	}
	sigObj, _ := JxObj(signature)
	if id, _ := JxString(sigObj, "id"); id != "rs_1" {
		t.Fatalf("expected rs_1 signature id, got %s", jsonx.Stringify(signature))
	}
	if encrypted, _ := JxString(sigObj, "encrypted_content"); encrypted != "enc" {
		t.Fatalf("expected encrypted_content, got %v", encrypted)
	}
}

func TestOpenAIResponsesFunctionCall(t *testing.T) {
	events := []sseFixture{
		{"response.created", `{"type":"response.created","response":{"id":"resp_t"}}`},
		{"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":""}}`},
		{"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"city\":"}`},
		{"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"Paris\"}"}`},
		{"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}","status":"completed"}}`},
		{"response.completed", `{"type":"response.completed","response":{"id":"resp_t","status":"completed","usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13},"output":[]}}`},
	}
	server := responsesServer(t, events, nil)
	model := openaiResponsesModel(server.URL)
	apiKey := "sk-oai"

	_, result := collectStream(t, OpenAIResponsesApi().StreamResponses(model, userContext("weather"), &OpenAIResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))

	if result.StopReason != StopToolUse {
		t.Fatalf("expected toolUse, got %s", result.StopReason)
	}
	call, ok := result.Content[0].(*ToolCall)
	if !ok {
		t.Fatalf("expected ToolCall, got %#v", result.Content[0])
	}
	if call.ID != "call_1|fc_1" || call.Name != "get_weather" {
		t.Fatalf("unexpected call: %+v", call)
	}
	if city, _ := call.Arguments.Get("city"); city != "Paris" {
		t.Fatalf("expected city Paris, got %v (%s)", city, jsonx.Stringify(call.Arguments))
	}
}

func TestOpenAIResponsesMissingTerminalEvent(t *testing.T) {
	events := []sseFixture{
		{"response.created", `{"type":"response.created","response":{"id":"resp_x"}}`},
		{"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`},
	}
	server := responsesServer(t, events, nil)
	model := openaiResponsesModel(server.URL)
	apiKey := "sk-oai"

	_, result := collectStream(t, OpenAIResponsesApi().StreamResponses(model, userContext("Hello"), &OpenAIResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "ended before a terminal response event") {
		t.Fatalf("unexpected error: %v", result.ErrorMessage)
	}
}

func TestOpenAIResponsesFailedEvent(t *testing.T) {
	events := []sseFixture{
		{"response.failed", `{"type":"response.failed","response":{"id":"resp_f","status":"failed","error":{"code":"server_error","message":"upstream exploded"}}}`},
	}
	server := responsesServer(t, events, nil)
	model := openaiResponsesModel(server.URL)
	apiKey := "sk-oai"

	_, result := collectStream(t, OpenAIResponsesApi().StreamResponses(model, userContext("Hello"), &OpenAIResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "server_error: upstream exploded") {
		t.Fatalf("unexpected error: %v", result.ErrorMessage)
	}
}

func TestOpenAIResponsesIncompleteMaxOutputTokens(t *testing.T) {
	events := []sseFixture{
		{"response.completed", `{"type":"response.completed","response":{"id":"resp_i","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":5,"output_tokens":9,"total_tokens":14},"output":[]}}`},
	}
	server := responsesServer(t, events, nil)
	model := openaiResponsesModel(server.URL)
	apiKey := "sk-oai"

	_, result := collectStream(t, OpenAIResponsesApi().StreamResponses(model, userContext("Hello"), &OpenAIResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopLength {
		t.Fatalf("expected length, got %s", result.StopReason)
	}
	if result.RawStopReason == nil || *result.RawStopReason != "incomplete.max_output_tokens" {
		t.Fatalf("unexpected raw stop reason: %v", result.RawStopReason)
	}
}

func TestOpenAIResponsesRequestShape(t *testing.T) {
	var capture capturedRequest
	server := responsesServer(t, minimalResponsesEvents(), &capture)
	model := openaiResponsesModel(server.URL)
	model.Reasoning = true
	model.ThinkingLevelMap = ThinkingLevelMap{ThinkingOff: strPtrOf("none"), ThinkingHigh: strPtrOf("high")}
	apiKey := "sk-oai"
	sessionID := "sess-1"

	reasoning := ThinkingHigh
	context := NewTranscriptContext([]Message{
		&SystemMessage{Content: StringContent("Be brief."), ToolsAdded: []Tool{}, TimestampMs: 1},
		&UserMessage{Content: StringContent("Hello"), TimestampMs: 2},
	})
	_, result := collectStream(t, OpenAIResponsesApi().StreamResponses(model, context, &OpenAIResponsesOptions{
		StreamOptions:   StreamOptions{APIKey: &apiKey, SessionID: &sessionID},
		ReasoningEffort: &reasoning,
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	if capture.url != "/responses" {
		t.Fatalf("unexpected url: %s", capture.url)
	}
	if got := capture.headers.Get("Authorization"); got != "Bearer sk-oai" {
		t.Fatalf("expected bearer auth, got %q", got)
	}
	if got := capture.headers.Get("session_id"); got != "sess-1" {
		t.Fatalf("expected session affinity header, got %q", got)
	}

	params := readCapturedBody(t, &capture)
	if params["model"] != "gpt-5.1" || params["store"] != false {
		t.Fatalf("unexpected base params: %v", params)
	}
	if params["prompt_cache_key"] != "sess-1" {
		t.Fatalf("expected prompt_cache_key, got %v", params["prompt_cache_key"])
	}
	reasoningParam, ok := params["reasoning"].(map[string]any)
	if !ok || reasoningParam["effort"] != "high" {
		t.Fatalf("expected reasoning effort high, got %v", params["reasoning"])
	}
	include, ok := params["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("expected encrypted content include, got %v", params["include"])
	}
	input := params["input"].([]any)
	first := input[0].(map[string]any)
	if first["role"] != "developer" {
		t.Fatalf("expected developer instruction role, got %v", first["role"])
	}
}

func TestOpenAIResponsesErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"model overloaded"}}`))
	}))
	t.Cleanup(server.Close)

	model := openaiResponsesModel(server.URL)
	apiKey := "sk-oai"
	_, result := collectStream(t, OpenAIResponsesApi().StreamResponses(model, userContext("Hello"), &OpenAIResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	// The SDK message embeds the stringified body ("500 {...}") and
	// messageCarriesBody is true, so the prefix+status form keeps it.
	want := `OpenAI API error (500): 500 {"error":{"message":"model overloaded"}}`
	if result.ErrorMessage == nil || *result.ErrorMessage != want {
		t.Fatalf("expected %q, got %s", want, *result.ErrorMessage)
	}
}

func TestOpenAIResponsesAssistantReplay(t *testing.T) {
	var capture capturedRequest
	server := responsesServer(t, minimalResponsesEvents(), &capture)
	model := openaiResponsesModel(server.URL)
	apiKey := "sk-oai"

	toolSignature := "reasoning-item-json"
	_ = toolSignature
	context := NewTranscriptContext([]Message{
		&UserMessage{Content: StringContent("Hello"), TimestampMs: 1},
		&AssistantMessage{
			Content: []ContentBlock{
				ThinkingContent{Thinking: "thoughts", ThinkingSignature: strPtrOf(`{"type":"reasoning","id":"rs_9","summary":[]}`)},
				TextContent{Text: "prior answer", TextSignature: strPtrOf(`{"v":1,"id":"msg_9"}`)},
				&ToolCall{ID: "call_9|fc_9", Name: "get_weather", Arguments: jsonx.ObjFrom("city", "Paris")},
			},
			API: "openai-responses", Provider: "openai", Model: "gpt-5.1",
			StopReason: StopToolUse, TimestampMs: 2,
		},
		&ToolResultMessage{ToolCallID: "call_9|fc_9", ToolName: "get_weather", Content: []ContentBlock{TextContent{Text: "sunny"}}, TimestampMs: 3},
		&UserMessage{Content: StringContent("thanks"), TimestampMs: 4},
	})
	_, result := collectStream(t, OpenAIResponsesApi().StreamResponses(model, context, &OpenAIResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	params := readCapturedBody(t, &capture)
	input := params["input"].([]any)
	// user, reasoning item, message item, function_call item, function_call_output, user
	if len(input) != 6 {
		t.Fatalf("expected 6 input items, got %d: %s", len(input), mustJSON(params["input"]))
	}
	reasoningItem := input[1].(map[string]any)
	if reasoningItem["type"] != "reasoning" || reasoningItem["id"] != "rs_9" {
		t.Fatalf("unexpected reasoning item: %v", reasoningItem)
	}
	messageItem := input[2].(map[string]any)
	if messageItem["type"] != "message" || messageItem["id"] != "msg_9" {
		t.Fatalf("unexpected message item: %v", messageItem)
	}
	functionCall := input[3].(map[string]any)
	if functionCall["type"] != "function_call" || functionCall["call_id"] != "call_9" || functionCall["id"] != "fc_9" {
		t.Fatalf("unexpected function call item: %v", functionCall)
	}
	output := input[4].(map[string]any)
	if output["type"] != "function_call_output" || output["call_id"] != "call_9" || output["output"] != "sunny" {
		t.Fatalf("unexpected function call output: %v", output)
	}
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "<error>"
	}
	return string(encoded)
}
