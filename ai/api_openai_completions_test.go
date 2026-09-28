package ai

// api_openai_completions_test.go ports the SSE-replay essence of the
// openai-completions tests (raw stop reasons, tool-call assembly, reasoning
// replay, usage parsing, deepseek/zai/openrouter compat params) against an
// httptest server.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func completionsServer(t *testing.T, chunks []string, capture *capturedRequest) *httptest.Server {
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
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(server.Close)
	return server
}

func readCapturedBody(t *testing.T, capture *capturedRequest) map[string]any {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal(capture.body, &params); err != nil {
		t.Fatal(err)
	}
	return params
}

func openaiCompletionsModel(baseURL string) *Model {
	return &Model{
		ID:            "gpt-5.1",
		API:           "openai-completions",
		Provider:      "openai",
		BaseURL:       baseURL,
		Input:         []string{"text"},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
}

func TestOpenAICompletionsTextStream(t *testing.T) {
	chunks := []string{
		`{"id":"chatcmpl-1","model":"gpt-5.1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}`,
		`{"id":"chatcmpl-1","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":" there"}}]}`,
		`{"id":"chatcmpl-1","model":"gpt-5.1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":4}}}`,
	}
	server := completionsServer(t, chunks, nil)
	model := openaiCompletionsModel(server.URL)
	apiKey := "sk-oai"

	events, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, userContext("Hello"), &OpenAICompletionsOptions{
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
	if result.ResponseID == nil || *result.ResponseID != "chatcmpl-1" {
		t.Fatalf("unexpected responseId: %v", result.ResponseID)
	}
	if text, ok := result.Content[0].(TextContent); !ok || text.Text != "Hi there" {
		t.Fatalf("unexpected content: %#v", result.Content[0])
	}
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s", result.StopReason)
	}
	// input = 10 - 4 cached = 6; cacheRead = 4.
	if result.Usage.Input != 6 || result.Usage.CacheRead != 4 || result.Usage.Output != 2 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if result.Usage.TotalTokens != 12 {
		t.Fatalf("expected totalTokens 12, got %v", result.Usage.TotalTokens)
	}
}

func TestOpenAICompletionsToolCalls(t *testing.T) {
	chunks := []string{
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]}}]}`,
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"get_time","arguments":"{}"}}]}}]}`,
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	server := completionsServer(t, chunks, nil)
	model := openaiCompletionsModel(server.URL)
	apiKey := "sk-oai"

	events, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, userContext("weather"), &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))

	if result.StopReason != StopToolUse {
		t.Fatalf("expected toolUse, got %s", result.StopReason)
	}
	if len(result.Content) != 2 {
		t.Fatalf("expected 2 tool calls, got %d blocks", len(result.Content))
	}
	first, ok := result.Content[0].(*ToolCall)
	if !ok || first.ID != "call_a" || first.Name != "get_weather" {
		t.Fatalf("unexpected first call: %+v", result.Content[0])
	}
	if city, _ := first.Arguments.Get("city"); city != "Paris" {
		t.Fatalf("expected city Paris, got %v (%s)", city, jsonx.Stringify(first.Arguments))
	}
	second, ok := result.Content[1].(*ToolCall)
	if !ok || second.ID != "call_b" || second.Name != "get_time" {
		t.Fatalf("unexpected second call: %+v", result.Content[1])
	}

	toolCallEnds := 0
	for _, event := range events {
		if _, isEnd := event.(*EventToolCallEnd); isEnd {
			toolCallEnds++
		}
	}
	if toolCallEnds != 2 {
		t.Fatalf("expected 2 toolcall_end events, got %d", toolCallEnds)
	}
}

func TestOpenAICompletionsReasoningContent(t *testing.T) {
	chunks := []string{
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"reasoning_content":"thinking hard"}}]}`,
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":"answer"}}]}`,
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	server := completionsServer(t, chunks, nil)
	model := openaiCompletionsModel(server.URL)
	apiKey := "sk-oai"

	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))

	if len(result.Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(result.Content))
	}
	thinking, ok := result.Content[0].(ThinkingContent)
	if !ok || thinking.Thinking != "thinking hard" {
		t.Fatalf("unexpected thinking block: %#v", result.Content[0])
	}
	if thinking.ThinkingSignature == nil || *thinking.ThinkingSignature != "reasoning_content" {
		t.Fatalf("expected reasoning_content signature marker, got %v", thinking.ThinkingSignature)
	}
	if text, ok := result.Content[1].(TextContent); !ok || text.Text != "answer" {
		t.Fatalf("unexpected text block: %#v", result.Content[1])
	}
}

func TestOpenAICompletionsReasoningDetailsMerge(t *testing.T) {
	chunks := []string{
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"part1"}]}}]}`,
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"part2"}]}}]}`,
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":"done"}}]}`,
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	server := completionsServer(t, chunks, nil)
	model := openaiCompletionsModel(server.URL)
	apiKey := "sk-oai"

	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))

	thinking, ok := result.Content[0].(ThinkingContent)
	if !ok {
		t.Fatalf("expected thinking block, got %#v", result.Content[0])
	}
	if thinking.ThinkingSignature == nil {
		t.Fatal("expected merged reasoning details signature")
	}
	details, err := ParseJSONWithRepair(*thinking.ThinkingSignature)
	if err != nil {
		t.Fatal(err)
	}
	list, ok := details.([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("expected 1 merged detail, got %s", jsonx.Stringify(details))
	}
	first, _ := JxObj(list[0])
	text, _ := JxString(first, "text")
	if text != "part1part2" {
		t.Fatalf("expected merged text, got %q", text)
	}
}

func TestOpenAICompletionsMissingFinishReason(t *testing.T) {
	chunks := []string{
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":"x"}}]}`,
	}
	server := completionsServer(t, chunks, nil)
	model := openaiCompletionsModel(server.URL)
	apiKey := "sk-oai"

	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	if result.ErrorMessage == nil || *result.ErrorMessage != "Stream ended without finish_reason" {
		t.Fatalf("unexpected error: %v", result.ErrorMessage)
	}
}

func TestOpenAICompletionsErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`))
	}))
	t.Cleanup(server.Close)

	model := openaiCompletionsModel(server.URL)
	apiKey := "sk-oai"
	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	want := `429 {"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`
	if result.ErrorMessage == nil || *result.ErrorMessage != want {
		t.Fatalf("expected %q, got %v", want, *result.ErrorMessage)
	}
}

func TestOpenAICompletionsRequestShape(t *testing.T) {
	var capture capturedRequest
	chunks := []string{
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
	}
	server := completionsServer(t, chunks, &capture)
	model := openaiCompletionsModel(server.URL)
	apiKey := "sk-oai"

	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	if capture.url != "/chat/completions" {
		t.Fatalf("unexpected url: %s", capture.url)
	}
	if got := capture.headers.Get("Authorization"); got != "Bearer sk-oai" {
		t.Fatalf("expected bearer auth, got %q", got)
	}

	params := readCapturedBody(t, &capture)
	if params["model"] != "gpt-5.1" || params["stream"] != true {
		t.Fatalf("unexpected base params: %v", params)
	}
	if _, has := params["store"]; !has {
		t.Fatal("expected store:false for openai provider")
	}
	streamOptions, ok := params["stream_options"].(map[string]any)
	if !ok || streamOptions["include_usage"] != true {
		t.Fatalf("expected stream_options.include_usage, got %v", params["stream_options"])
	}
	// Non-reasoning model -> system role.
	messages := params["messages"].([]any)
	first := messages[0].(map[string]any)
	if first["role"] != "user" {
		t.Fatalf("expected user role, got %v", first["role"])
	}
}

func TestOpenAICompletionsDeveloperRoleForReasoning(t *testing.T) {
	var capture capturedRequest
	chunks := []string{
		`{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
	}
	server := completionsServer(t, chunks, &capture)
	model := openaiCompletionsModel(server.URL)
	model.Reasoning = true
	apiKey := "sk-oai"

	context := NewTranscriptContext([]Message{
		&SystemMessage{Content: StringContent("Be brief."), ToolsAdded: []Tool{}, TimestampMs: 1},
		&UserMessage{Content: StringContent("Hello"), TimestampMs: 2},
	})
	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, context, &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	params := readCapturedBody(t, &capture)
	messages := params["messages"].([]any)
	first := messages[0].(map[string]any)
	if first["role"] != "developer" {
		t.Fatalf("expected developer role for reasoning model, got %v", first["role"])
	}
}

func TestOpenAICompletionsDeepseekThinkingFormat(t *testing.T) {
	var capture capturedRequest
	chunks := []string{
		`{"id":"c1","model":"deepseek-chat","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
	}
	server := completionsServer(t, chunks, &capture)
	model := &Model{
		ID:        "deepseek-chat",
		API:       "openai-completions",
		Provider:  "deepseek",
		BaseURL:   server.URL,
		Input:     []string{"text"},
		Reasoning: true,
		MaxTokens: 4096,
		ThinkingLevelMap: ThinkingLevelMap{
			ThinkingOff: strPtr("off"), ThinkingHigh: strPtr("high"),
		},
	}
	apiKey := "sk-ds"

	reasoning := ThinkingHigh
	maxTokens := 1024.0
	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions:   StreamOptions{APIKey: &apiKey, MaxTokens: &maxTokens},
		ReasoningEffort: &reasoning,
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	params := readCapturedBody(t, &capture)
	thinking, ok := params["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("expected thinking enabled, got %v", params["thinking"])
	}
	if params["reasoning_effort"] != "high" {
		t.Fatalf("expected reasoning_effort high, got %v", params["reasoning_effort"])
	}
	if _, has := params["store"]; has {
		t.Fatal("deepseek is non-standard: no store field")
	}
	if _, has := params["max_tokens"]; !has {
		t.Fatal("deepseek uses max_tokens field")
	}
	if params["max_tokens"] != float64(1024) {
		t.Fatalf("unexpected max_tokens: %v", params["max_tokens"])
	}
}

func TestOpenAICompletionsDeepseekReasoningContentReplay(t *testing.T) {
	var capture capturedRequest
	chunks := []string{
		`{"id":"c1","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
	}
	server := completionsServer(t, chunks, &capture)
	model := &Model{
		ID:        "deepseek-reasoner",
		API:       "openai-completions",
		Provider:  "deepseek",
		BaseURL:   server.URL,
		Input:     []string{"text"},
		Reasoning: true,
		MaxTokens: 4096,
	}
	apiKey := "sk-ds"

	signature := "reasoning_content"
	context := NewTranscriptContext([]Message{
		&UserMessage{Content: StringContent("Hello"), TimestampMs: 1},
		&AssistantMessage{
			Content: []ContentBlock{
				ThinkingContent{Thinking: "prior thoughts", ThinkingSignature: &signature},
				TextContent{Text: "prior answer"},
			},
			API: "openai-completions", Provider: "deepseek", Model: "deepseek-reasoner",
			StopReason: StopStop, TimestampMs: 2,
		},
		&UserMessage{Content: StringContent("Again"), TimestampMs: 3},
	})
	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, context, &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	params := readCapturedBody(t, &capture)
	messages := params["messages"].([]any)
	assistant := messages[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("expected assistant, got %v", assistant["role"])
	}
	if assistant["reasoning_content"] != "prior thoughts" {
		t.Fatalf("expected reasoning_content replay, got %v", assistant["reasoning_content"])
	}
	if assistant["content"] != "prior answer" {
		t.Fatalf("expected content replay, got %v", assistant["content"])
	}
}

func TestOpenAICompletionsZaiThinkingFormat(t *testing.T) {
	var capture capturedRequest
	chunks := []string{
		`{"id":"c1","model":"glm-5.2","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
	}
	server := completionsServer(t, chunks, &capture)
	model := &Model{
		ID:        "glm-5.2",
		API:       "openai-completions",
		Provider:  "zai",
		BaseURL:   server.URL,
		Input:     []string{"text"},
		Reasoning: true,
		MaxTokens: 4096,
		ThinkingLevelMap: ThinkingLevelMap{
			ThinkingOff: strPtr("none"), ThinkingHigh: strPtr("high"),
		},
		Compat: jsonx.ObjFrom("thinkingFormat", "zai", "supportsReasoningEffort", true, "zaiToolStream", true, "maxTokensField", "max_tokens"),
	}
	apiKey := "sk-zai"

	reasoning := ThinkingHigh
	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions:   StreamOptions{APIKey: &apiKey},
		ReasoningEffort: &reasoning,
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	params := readCapturedBody(t, &capture)
	thinking, ok := params["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" || thinking["clear_thinking"] != false {
		t.Fatalf("expected zai thinking enabled, got %v", params["thinking"])
	}
	if params["reasoning_effort"] != "high" {
		t.Fatalf("expected mapped effort high, got %v", params["reasoning_effort"])
	}
}

func TestOpenAICompletionsOpenRouterCompat(t *testing.T) {
	var capture capturedRequest
	chunks := []string{
		`{"id":"gen-1","model":"anthropic/claude-sonnet-5","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
	}
	server := completionsServer(t, chunks, &capture)
	model := &Model{
		ID:               "anthropic/claude-sonnet-5",
		API:              "openai-completions",
		Provider:         "openrouter",
		BaseURL:          server.URL,
		Input:            []string{"text"},
		Reasoning:        true,
		MaxTokens:        4096,
		ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: strPtr("none"), ThinkingHigh: strPtr("high")},
		Compat:           jsonx.ObjFrom("openRouterRouting", jsonx.ObjFrom("sort", "price"), "supportsDeveloperRole", true),
	}
	apiKey := "sk-or"
	sessionID := "session-1"

	reasoning := ThinkingHigh
	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions:   StreamOptions{APIKey: &apiKey, SessionID: &sessionID},
		ReasoningEffort: &reasoning,
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	if got := capture.headers.Get("x-session-id"); got != "session-1" {
		t.Fatalf("expected openrouter session affinity header, got %q", got)
	}

	params := readCapturedBody(t, &capture)
	reasoningParam, ok := params["reasoning"].(map[string]any)
	if !ok || reasoningParam["effort"] != "high" {
		t.Fatalf("expected nested openrouter reasoning effort, got %v", params["reasoning"])
	}
	provider, ok := params["provider"].(map[string]any)
	if !ok || provider["sort"] != "price" {
		t.Fatalf("expected openrouter provider routing, got %v", params["provider"])
	}
	// openrouter anthropic/* models use developer role via compat.
	messages := params["messages"].([]any)
	if messages[0].(map[string]any)["role"] != "user" {
		t.Fatal("no system message expected")
	}
}

func TestOpenAICompletionsToolResultAndHistory(t *testing.T) {
	var capture capturedRequest
	chunks := []string{
		`{"id":"c1","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
	}
	server := completionsServer(t, chunks, &capture)
	model := openaiCompletionsModel(server.URL)
	apiKey := "sk-oai"

	context := NewTranscriptContext([]Message{
		&UserMessage{Content: StringContent("weather?"), TimestampMs: 1},
		&AssistantMessage{
			Content: []ContentBlock{&ToolCall{ID: "call_a", Name: "get_weather", Arguments: jsonx.ObjFrom("city", "Paris")}},
			API:     "openai-completions", Provider: "openai", Model: "gpt-5.1",
			StopReason: StopToolUse, TimestampMs: 2,
		},
		&ToolResultMessage{ToolCallID: "call_a", ToolName: "get_weather", Content: []ContentBlock{TextContent{Text: "sunny"}}, TimestampMs: 3},
		&UserMessage{Content: StringContent("thanks"), TimestampMs: 4},
	})
	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, context, &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	params := readCapturedBody(t, &capture)
	messages := params["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("expected 4 messages, got %d: %s", len(messages), jsonx.Stringify(params["messages"]))
	}
	assistant := messages[1].(map[string]any)
	toolCalls := assistant["tool_calls"].([]any)
	firstCall := toolCalls[0].(map[string]any)
	function := firstCall["function"].(map[string]any)
	if function["name"] != "get_weather" {
		t.Fatalf("unexpected tool call: %v", firstCall)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(function["arguments"].(string)), &args); err != nil {
		t.Fatal(err)
	}
	if args["city"] != "Paris" {
		t.Fatalf("unexpected arguments: %v", args)
	}
	tool := messages[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_a" || tool["content"] != "sunny" {
		t.Fatalf("unexpected tool result: %v", tool)
	}
}

func TestOpenAICompletionsRetryOn429(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("retry-after-ms", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	model := openaiCompletionsModel(server.URL)
	apiKey := "sk-oai"
	retries := 1.0
	_, result := collectStream(t, OpenAICompletionsApi().StreamCompletions(model, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey, MaxRetries: &retries},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop after retry, got %s (%v)", result.StopReason, result.ErrorMessage)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}
