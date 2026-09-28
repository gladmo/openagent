package ai

// transport_defects_test.go pins review fixes: Copilot dynamic headers on
// the anthropic transport, mismatched-delta tolerance, malformed-JSON decode
// without panics, byte-consistent frame deltas, transport-error retry
// classification, and per-attempt abort-derivation disposal in codex.

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"unicode/utf8"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

func TestAnthropicCopilotDynamicHeaders(t *testing.T) {
	events := []sseFixture{
		{"message_start", `{"type":"message_start","message":{"id":"m1","usage":{"input_tokens":1,"output_tokens":1}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":1}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}
	var capture capturedRequest
	server := sseServer(t, events, &capture)
	model := testAnthropicModel(server.URL)
	model.Provider = "github-copilot"
	apiKey := "copilot-token"

	_, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("hi"), &AnthropicOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("stream failed: %s (%v)", result.StopReason, result.ErrorMessage)
	}
	if got := capture.headers.Get("Openai-Intent"); got == "" {
		t.Fatal("Copilot Openai-Intent header missing on anthropic transport")
	}
	if got := capture.headers.Get("Authorization"); got != "Bearer copilot-token" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestAnthropicMismatchedDeltaTolerated(t *testing.T) {
	// A thinking_delta indexed at a text block and a text_delta indexed at
	// a thinking block: both must be skipped, not crash the stream.
	events := []sseFixture{
		{"message_start", `{"type":"message_start","message":{"id":"m1","usage":{"input_tokens":1,"output_tokens":1}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"nope"}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"also-nope"}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"real"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"content_block_stop", `{"type":"content_block_stop","index":1}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":1}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}
	server := sseServer(t, events, nil)
	model := testAnthropicModel(server.URL)
	apiKey := "sk-test"

	_, result := collectStream(t, AnthropicMessagesApi().StreamAnthropic(model, userContext("hi"), &AnthropicOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey},
	}))
	if result.StopReason != StopStop {
		t.Fatalf("stream failed on mismatched delta: %s (%v)", result.StopReason, result.ErrorMessage)
	}
	if text, ok := result.Content[0].(TextContent); !ok || text.Text != "" {
		t.Fatalf("text block corrupted: %#v", result.Content[0])
	}
	if thinking, ok := result.Content[1].(ThinkingContent); !ok || thinking.Thinking != "real" {
		t.Fatalf("thinking block corrupted: %#v", result.Content[1])
	}
}

func TestMessageFromJSONMalformedNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("decode panicked: %v", r)
		}
	}()
	cases := []string{
		`{"role":"assistant","content":[{"type":"text"}]}`,
		`{"role":"assistant","content":[{"type":"thinking"}]}`,
		`{"role":"assistant","content":[{"type":"image"}]}`,
		`{"role":"assistant","content":[{"type":"toolCall"}]}`,
		`{"role":"assistant","content":[{"type":"toolCall","id":1,"name":true,"arguments":"nope","thoughtSignature":2}]}`,
	}
	for _, line := range cases {
		parsed, parseErr := jsonx.Parse(line)
		if parseErr != nil {
			t.Fatalf("%s: parse: %v", line, parseErr)
		}
		if _, err := MessageFromJSON(parsed); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
	}
	// Round trip preserves the decodable fields it does carry.
	parsed, parseErr := jsonx.Parse(`{"role":"assistant","content":[{"type":"text","text":"ok","textSignature":"sig"}]}`)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	message, err := MessageFromJSON(parsed)
	if err != nil {
		t.Fatal(err)
	}
	assistant := message.(*AssistantMessage)
	block := assistant.Content[0].(TextContent)
	if block.Text != "ok" || block.TextSignature == nil || *block.TextSignature != "sig" {
		t.Fatalf("block = %#v", block)
	}
}

func TestFrameEncoderMultibyteCoveredBoundary(t *testing.T) {
	// A block that starts with non-empty text ("é") and a delta repeating
	// it: the covered boundary must slice a valid UTF-8 suffix (skip
	// exactly the initial é, emit the rest).
	partial := seedMessage()
	encoder := NewAssistantMessageFrameEncoder()
	frames := []AssistantMessageFrame{mustFrame(t, encoder, &EventStart{Partial: partial})}
	partial.Content = append(partial.Content, TextContent{Text: "é"})
	frames = append(frames, mustFrame(t, encoder, &EventTextStart{ContentIndex: 0, Partial: partial}))
	partial.Content[0] = TextContent{Text: "ééé"}
	frames = append(frames, mustFrame(t, encoder, &EventTextDelta{ContentIndex: 0, Delta: "éé", Partial: partial}))

	delta, ok := frames[len(frames)-1].(*FrameTextDelta)
	if !ok {
		t.Fatalf("last frame = %#v", frames[len(frames)-1])
	}
	if !utf8.ValidString(delta.Delta) {
		t.Fatalf("invalid UTF-8 delta: %q", delta.Delta)
	}
	if delta.Delta != "é" {
		t.Fatalf("delta = %q, want just the uncovered é", delta.Delta)
	}
}

func TestTransportErrorClassifiedRetryable(t *testing.T) {
	// Connection-refused from the fetch layer becomes a status-0
	// ProviderHTTPError, which the SDK-parity policy retries.
	err := transportError(errors.New("dial tcp 127.0.0.1:1: connect: connection refused"))
	if !IsRetryableProviderError(err) {
		t.Fatalf("transport error not retryable: %v", err)
	}
	delay, delayErr := GetRetryDelayMs(err, 0, DefaultMaxRetryDelayMs)
	if delayErr != nil || delay <= 0 {
		t.Fatalf("delay = %v err = %v", delay, delayErr)
	}
	// Abort errors pass through and stay non-retryable.
	abortErr := transportError(abort.NewAbortError("aborted"))
	if _, isAbort := abortErr.(*abort.Error); !isAbort {
		t.Fatalf("abort error rewrapped: %T", abortErr)
	}
	if IsRetryableProviderError(abortErr) {
		t.Fatal("abort became retryable")
	}
	// Classified provider errors are not rewrapped.
	kept := transportError(&ProviderHTTPError{Status: 400})
	if IsRetryableProviderError(kept) {
		t.Fatal("400 became retryable")
	}
	if _, isHTTP := kept.(*ProviderHTTPError); !isHTTP || kept.(*ProviderHTTPError).Status != 400 {
		t.Fatal("classified provider error was rewrapped")
	}
}

func TestCodexRetriesTransportErrors(t *testing.T) {
	attempts := 0
	failing := func(FetchRequest) (FetchResponse, error) {
		attempts++
		return FetchResponse{}, fmt.Errorf("connection reset by peer")
	}
	model := codexModel("http://unused.invalid")
	token := codexTestToken("acct_7")
	retries := 2.0
	_, result := collectStream(t, OpenAICodexResponsesApi().StreamCodex(model, userContext("x"), &OpenAICodexResponsesOptions{
		StreamOptions: StreamOptions{APIKey: &token, MaxRetries: &retries, Fetch: failing},
	}))
	if result == nil || result.StopReason != StopError {
		t.Fatalf("expected error stop, got %+v", result)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts (1 + 2 retries), got %d", attempts)
	}
}

var _ = jsonx.Stringify
var _ = http.MethodPost
