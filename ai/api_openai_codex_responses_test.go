package ai

// api_openai_codex_responses_test.go ports the SSE-replay essence of the
// codex tests (identity headers, URL resolution, response.done terminal
// mapping with endTurn, usage-limit friendliness, retries).

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

// codexTestToken builds a JWT-ish token whose payload carries the ChatGPT
// account id under the auth claim path.
func codexTestToken(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`,
	))
	return header + "." + payload + ".signature"
}

func codexModel(baseURL string) *Model {
	return &Model{
		ID:               "gpt-5.3-codex",
		API:              "openai-codex-responses",
		Provider:         "openai-codex",
		BaseURL:          baseURL,
		Input:            []string{"text"},
		ContextWindow:    200000,
		MaxTokens:        65536,
		Reasoning:        true,
		ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: strPtrOf("none"), ThinkingHigh: strPtrOf("high")},
	}
}

func codexTerminalEvents() []sseFixture {
	return []sseFixture{
		{"response.created", `{"type":"response.created","response":{"id":"resp_c"}}`},
		{"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_c","role":"assistant","status":"in_progress","content":[]}}`},
		{"response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"delta":"code"}`},
		{"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_c","role":"assistant","status":"completed","content":[{"type":"output_text","text":"code","annotations":[]}]}}`},
		{"response.done", `{"type":"response.done","response":{"id":"resp_c","status":"completed","end_turn":true,"usage":{"input_tokens":50,"output_tokens":7,"total_tokens":57},"output":[]}}`},
	}
}

func TestCodexResponsesStream(t *testing.T) {
	var capture capturedRequest
	server := responsesServer(t, codexTerminalEvents(), &capture)
	model := codexModel(server.URL)
	token := codexTestToken("acct_1")

	events, result := collectStream(t, OpenAICodexResponsesApi().StreamCodex(model, userContext("code"), &OpenAICodexResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &token},
	}))

	var types []string
	for _, event := range events {
		types = append(types, event.EventType())
	}
	want := []string{"start", "text_start", "text_delta", "text_end", "done"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("event sequence: got %v want %v", types, want)
	}
	if result.ResponseID == nil || *result.ResponseID != "resp_c" {
		t.Fatalf("unexpected responseId: %v", result.ResponseID)
	}
	if result.EndTurn == nil || !*result.EndTurn {
		t.Fatalf("expected endTurn true, got %v", result.EndTurn)
	}
	if text, ok := result.Content[0].(TextContent); !ok || text.Text != "code" {
		t.Fatalf("unexpected content: %#v", result.Content[0])
	}
	if result.Usage.Input != 50 || result.Usage.Output != 7 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s", result.StopReason)
	}

	// Request shape.
	if capture.url != "/codex/responses" {
		t.Fatalf("unexpected url: %s", capture.url)
	}
	if got := capture.headers.Get("chatgpt-account-id"); got != "acct_1" {
		t.Fatalf("expected chatgpt-account-id header, got %q", got)
	}
	if got := capture.headers.Get("originator"); got != "pi" {
		t.Fatalf("expected originator header, got %q", got)
	}
	if got := capture.headers.Get("OpenAI-Beta"); got != "responses=experimental" {
		t.Fatalf("expected OpenAI-Beta header, got %q", got)
	}
	if got := capture.headers.Get("Authorization"); got != "Bearer "+token {
		t.Fatalf("expected bearer token, got %q", got)
	}
}

func TestCodexRequestBodyShape(t *testing.T) {
	var capture capturedRequest
	server := responsesServer(t, codexTerminalEvents(), &capture)
	model := codexModel(server.URL)
	token := codexTestToken("acct_2")

	context := NewTranscriptContext([]Message{
		&SystemMessage{Content: StringContent("You are Codex."), ToolsAdded: []Tool{}, TimestampMs: 1},
		&UserMessage{Content: StringContent("code"), TimestampMs: 2},
	})
	_, result := collectStream(t, OpenAICodexResponsesApi().StreamCodex(model, context, &OpenAICodexResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &token},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}

	params := readCapturedBody(t, &capture)
	if params["instructions"] != "You are Codex." {
		t.Fatalf("expected instructions from system message, got %v", params["instructions"])
	}
	if params["store"] != false || params["stream"] != true {
		t.Fatalf("unexpected base params: %v", params)
	}
	if params["tool_choice"] != "auto" || params["parallel_tool_calls"] != true {
		t.Fatalf("unexpected tool params: %v", params)
	}
	text, ok := params["text"].(map[string]any)
	if !ok || text["verbosity"] != "low" {
		t.Fatalf("expected low verbosity, got %v", params["text"])
	}
	include, ok := params["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("expected include, got %v", params["include"])
	}
	// No reasoning effort requested -> off mapping ("none").
	reasoning, ok := params["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "none" {
		t.Fatalf("expected reasoning effort none, got %v", params["reasoning"])
	}
	input := params["input"].([]any)
	// includeSystemPrompt=false: only the user item.
	if len(input) != 1 {
		t.Fatalf("expected 1 input item, got %d: %s", len(input), mustJSON(input))
	}
}

func TestCodexResponsesMissingAPIKey(t *testing.T) {
	server := responsesServer(t, codexTerminalEvents(), nil)
	model := codexModel(server.URL)

	_, result := collectStream(t, OpenAICodexResponsesApi().StreamCodex(model, userContext("x"), &OpenAICodexResponsesOptions{}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	if result.ErrorMessage == nil || *result.ErrorMessage != "No API key for provider: openai-codex" {
		t.Fatalf("unexpected error: %v", result.ErrorMessage)
	}
}

func TestCodexResponsesBadToken(t *testing.T) {
	server := responsesServer(t, codexTerminalEvents(), nil)
	model := codexModel(server.URL)
	token := "not-a-jwt"

	_, result := collectStream(t, OpenAICodexResponsesApi().StreamCodex(model, userContext("x"), &OpenAICodexResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &token},
	}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	if result.ErrorMessage == nil || *result.ErrorMessage != "Failed to extract accountId from token" {
		t.Fatalf("unexpected error: %v", result.ErrorMessage)
	}
}

func TestCodexResponsesUsageLimitFriendlyError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"usage_limit_reached","message":"You've hit your limit","plan_type":"Pro","resets_at":9999999999}}`))
	}))
	t.Cleanup(server.Close)

	model := codexModel(server.URL)
	token := codexTestToken("acct_3")

	_, result := collectStream(t, OpenAICodexResponsesApi().StreamCodex(model, userContext("x"), &OpenAICodexResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &token},
	}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	if result.ErrorMessage == nil {
		t.Fatal("expected error message")
	}
	message := *result.ErrorMessage
	if !strings.Contains(message, "You have hit your ChatGPT usage limit (pro plan).") {
		t.Fatalf("expected friendly usage limit message, got %q", message)
	}
	if !strings.Contains(message, "Try again in ~") {
		t.Fatalf("expected reset hint, got %q", message)
	}
}

func TestCodexResponsesRetryOn503(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("retry-after-ms", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("upstream overloaded"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, fixture := range codexTerminalEvents() {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", fixture.event, fixture.data)
		}
	}))
	t.Cleanup(server.Close)

	model := codexModel(server.URL)
	token := codexTestToken("acct_4")
	retries := 1.0
	_, result := collectStream(t, OpenAICodexResponsesApi().StreamCodex(model, userContext("x"), &OpenAICodexResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &token, MaxRetries: &retries},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop after retry, got %s (%v)", result.StopReason, result.ErrorMessage)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestCodexResponsesErrorEvent(t *testing.T) {
	events := []sseFixture{
		{"error", `{"type":"error","code":"invalid_request","message":"model not found"}`},
	}
	server := responsesServer(t, events, nil)
	model := codexModel(server.URL)
	token := codexTestToken("acct_5")

	_, result := collectStream(t, OpenAICodexResponsesApi().StreamCodex(model, userContext("x"), &OpenAICodexResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &token},
	}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "Codex error: model not found") {
		t.Fatalf("unexpected error: %v", result.ErrorMessage)
	}
}

func TestResolveCodexURL(t *testing.T) {
	cases := map[string]string{
		"":                                      "https://chatgpt.com/backend-api/codex/responses",
		"https://chatgpt.com/backend-api":       "https://chatgpt.com/backend-api/codex/responses",
		"https://chatgpt.com/backend-api/":      "https://chatgpt.com/backend-api/codex/responses",
		"https://chatgpt.com/backend-api/codex": "https://chatgpt.com/backend-api/codex/responses",
		"https://chatgpt.com/backend-api/codex/responses":  "https://chatgpt.com/backend-api/codex/responses",
		"https://chatgpt.com/backend-api/codex/responses/": "https://chatgpt.com/backend-api/codex/responses",
	}
	for input, want := range cases {
		if got := resolveCodexURL(input); got != want {
			t.Errorf("resolveCodexURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCodexSessionCacheHeaders(t *testing.T) {
	var capture capturedRequest
	server := responsesServer(t, codexTerminalEvents(), &capture)
	model := codexModel(server.URL)
	token := codexTestToken("acct_6")
	sessionID := "codex-session-1"

	_, result := collectStream(t, OpenAICodexResponsesApi().StreamCodex(model, userContext("x"), &OpenAICodexResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &token, SessionID: &sessionID},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("expected stop, got %s (%v)", result.StopReason, result.ErrorMessage)
	}
	if got := capture.headers.Get("session-id"); got != "codex-session-1" {
		t.Fatalf("expected session-id header, got %q", got)
	}
	if got := capture.headers.Get("x-client-request-id"); got != "codex-session-1" {
		t.Fatalf("expected x-client-request-id header, got %q", got)
	}
	params := readCapturedBody(t, &capture)
	if params["prompt_cache_key"] != "codex-session-1" {
		t.Fatalf("expected prompt_cache_key, got %v", params["prompt_cache_key"])
	}
	_ = jsonx.NewObj()
}
