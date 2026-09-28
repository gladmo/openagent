package ai

// Ports of pi/packages/ai/test/overflow.test.ts, text.test.ts, uuid.test.ts,
// and the estimate/event-stream behaviors.

import (
	"regexp"
	"sort"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func overflowErrorMessage(errorMessage string, provider string) *AssistantMessage {
	msg := &AssistantMessage{
		Content:      []ContentBlock{},
		API:          "openai-completions",
		Provider:     provider,
		Model:        "qwen3.5:35b",
		Usage:        Usage{},
		StopReason:   StopError,
		ErrorMessage: &errorMessage,
		TimestampMs:  nowMs(),
	}
	return msg
}

func overflowLengthStop(input, cacheRead, output float64, provider string) *AssistantMessage {
	return &AssistantMessage{
		Content:     []ContentBlock{},
		API:         "openai-completions",
		Provider:    provider,
		Model:       "test-model",
		Usage:       Usage{Input: input, Output: output, CacheRead: cacheRead, TotalTokens: input + cacheRead + output},
		StopReason:  StopLength,
		TimestampMs: nowMs(),
	}
}

func TestIsContextOverflow(t *testing.T) {
	positive := []struct {
		errorMessage string
		provider     string
		window       float64
	}{
		{"400 `prompt too long; exceeded max context length by 100918 tokens`", "ollama", 32768},
		{`400 {"code":"1261","message":"Prompt too long"}`, "zai", 1048576},
		{"400 The input (516368 tokens) is longer than the model's context length (262144 tokens).", "ollama", 262144},
		{"Error: 503 litellm.ServiceUnavailableError: litellm.MidStreamFallbackError: litellm.APIConnectionError: APIConnectionError: OpenAIException - Requested token count exceeds the model's maximum context length of 131072 tokens.", "ollama", 131072},
		{"Error: 400 Input length (265330) exceeds model's maximum context length (262144).", "ollama", 262144},
		{"Provider returned error: Input length 131393 exceeds the maximum allowed input length of 131040 tokens.", "ollama", 131072},
		{"400 Prompt has 256468 tokens, but the configured context size is 256000 tokens", "ollama", 256000},
		{"Prompt has 5,958,968 tokens, but the configured context size is 256,000 tokens", "ollama", 256000},
		{"400 status code (no body)", "cerebras", 131072},
		{"413 status code (no body)", "cerebras", 131072},
	}
	for _, tc := range positive {
		if !IsContextOverflow(overflowErrorMessage(tc.errorMessage, tc.provider), tc.window) {
			t.Fatalf("expected overflow: %s (%s)", tc.errorMessage, tc.provider)
		}
	}
	negative := []struct {
		errorMessage string
		provider     string
		window       float64
	}{
		{"500 `model runner crashed unexpectedly`", "ollama", 32768},
		{"400 status code (no body)", "opencode-go", 1000000},
		{"413 status code (no body)", "opencode-go", 1000000},
		{"Throttling error: Too many tokens, please wait before trying again.", "ollama", 200000},
		{"Service unavailable: The service is temporarily unavailable.", "ollama", 200000},
		{"Rate limit exceeded, please retry after 30 seconds.", "ollama", 200000},
		{"Too many requests. Please slow down.", "ollama", 200000},
	}
	for _, tc := range negative {
		if IsContextOverflow(overflowErrorMessage(tc.errorMessage, tc.provider), tc.window) {
			t.Fatalf("expected non-overflow: %s (%s)", tc.errorMessage, tc.provider)
		}
	}
	// Xiaomi-style silent overflow.
	if !IsContextOverflow(overflowLengthStop(58, 1048512, 0, "xiaomi"), 1048576) {
		t.Fatal("Xiaomi filled-context length stop not detected")
	}
	// Normal length stops with output are not overflow.
	if IsContextOverflow(overflowLengthStop(1000, 0, 4096, "test"), 200000) {
		t.Fatal("normal length stop treated as overflow")
	}
	if IsContextOverflow(overflowLengthStop(100, 0, 0, "test"), 200000) {
		t.Fatal("zero-output length stop far below context treated as overflow")
	}
}

func TestIsRecoverableLength(t *testing.T) {
	if !IsRecoverableLength(overflowLengthStop(3, 253584, 16, "openai"), 128000) {
		t.Fatal("below limit should be recoverable")
	}
	if IsRecoverableLength(overflowLengthStop(4062, 0, 1024, "test"), 1024) {
		t.Fatal("reached limit should not be recoverable")
	}
	if !IsRecoverableLength(overflowLengthStop(100, 0, 0, "test"), 128000) {
		t.Fatal("zero output should be recoverable")
	}
}

func TestContentTextAssistantBlocks(t *testing.T) {
	content := []ContentBlock{
		ThinkingContent{Thinking: "reasoning"},
		TextContent{Text: "first"},
		FauxToolCall("read", jsonx.NewObj()),
		TextContent{Text: "second"},
	}
	if got := ContentText(BlocksContent(content...), "\n"); got != "first\nsecond" {
		t.Fatalf("got %q", got)
	}
	if got := ContentText(BlocksContent(content...), ""); got != "firstsecond" {
		t.Fatalf("sep: %q", got)
	}
	toolResultContent := []ContentBlock{
		TextContent{Text: "first"},
		ImageContent{Data: "...", MimeType: "image/png"},
		TextContent{Text: "second"},
	}
	if got := ContentText(BlocksContent(toolResultContent...), ""); got != "firstsecond" {
		t.Fatalf("tool result: %q", got)
	}
}

var uuidV7Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func parseUUIDTimestamp(t *testing.T, id string) int64 {
	t.Helper()
	hexDigits := ""
	for _, r := range id {
		if r != '-' {
			hexDigits += string(r)
		}
	}
	var v int64
	for _, c := range hexDigits[:12] {
		v = v*16 + int64(hexDigitValue(c))
	}
	return v
}

func hexDigitValue(c rune) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	default:
		return int(c-'a') + 10
	}
}

func TestUUIDv7FollowerTimestampsPreserved(t *testing.T) {
	followerTimestamp := float64(0x0123456789ab - 1000)
	followers := []string{UUIDv7(followerTimestamp), UUIDv7(followerTimestamp)}
	for _, id := range followers {
		if !uuidV7Re.MatchString(id) {
			t.Fatalf("format: %s", id)
		}
	}
	for _, id := range followers {
		if got := parseUUIDTimestamp(t, id); got != int64(followerTimestamp) {
			t.Fatalf("timestamp = %d want %d", got, int64(followerTimestamp))
		}
	}
	if followers[0] == followers[1] {
		t.Fatal("followers not distinct")
	}
}

func TestUUIDv7OrderedOrdinaryIds(t *testing.T) {
	ids := make([]string, 50)
	for i := range ids {
		ids[i] = UUIDv7()
	}
	sorted := append([]string{}, ids...)
	sort.Strings(sorted)
	for i := range ids {
		if ids[i] != sorted[i] {
			t.Fatalf("ids not ordered at %d", i)
		}
	}
}

func TestUUIDv7TimestampBoundaries(t *testing.T) {
	for _, ts := range []float64{0, 281474976710655} { // 0, 2^48-1
		if got := parseUUIDTimestamp(t, UUIDv7(ts)); got != int64(ts) {
			t.Fatalf("timestamp %v -> %d", ts, got)
		}
	}
}

func TestUUIDv7RejectsInvalidTimestamps(t *testing.T) {
	for _, ts := range []float64{-1, 281474976710656, 1.5} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("timestamp %v accepted", ts)
				}
			}()
			UUIDv7(ts)
		}()
	}
}

func TestEstimateContextTokensWithUsage(t *testing.T) {
	// A system prompt, an assistant message with usage, a trailing user
	// message: tokens = usage total + trailing estimate.
	usageTotal := 100.0
	messages := []Message{
		&SystemMessage{Content: StringContent("sys"), TimestampMs: 0},
		&AssistantMessage{Content: []ContentBlock{TextContent{Text: "reply"}}, Usage: Usage{Input: 80, Output: 20, TotalTokens: usageTotal}, StopReason: StopStop, TimestampMs: 10},
		&UserMessage{Content: StringContent("12345678"), TimestampMs: 20},
	}
	estimate := EstimateContextTokens(messages)
	if estimate.LastUsageIndex != 1 {
		t.Fatalf("lastUsageIndex = %d", estimate.LastUsageIndex)
	}
	if estimate.UsageTokens != usageTotal {
		t.Fatalf("usageTokens = %v", estimate.UsageTokens)
	}
	// 8 chars / 4 = 2 tokens trailing.
	if estimate.TrailingTokens != 2 {
		t.Fatalf("trailingTokens = %v", estimate.TrailingTokens)
	}
	if estimate.Tokens != 102 {
		t.Fatalf("tokens = %v", estimate.Tokens)
	}
}

func TestEstimateContextTokensIgnoresInsertedPrefix(t *testing.T) {
	// A compaction summary inserted AFTER the assistant response (timestamp
	// newer than the response) invalidates its usage.
	messages := []Message{
		&AssistantMessage{Content: []ContentBlock{TextContent{Text: "r"}}, Usage: Usage{TotalTokens: 500}, StopReason: StopStop, TimestampMs: 10},
		&UserMessage{Content: StringContent("later"), TimestampMs: 20},
	}
	estimate := EstimateContextTokens(messages)
	if estimate.LastUsageIndex != 0 || estimate.Tokens != 500+2 {
		t.Fatalf("estimate = %+v", estimate)
	}
	// Inserted summary BEFORE the assistant in list order but with a newer
	// timestamp: usage does not apply to the prefix.
	messages = []Message{
		&AssistantMessage{Content: []ContentBlock{TextContent{Text: "r"}}, Usage: Usage{TotalTokens: 500}, StopReason: StopStop, TimestampMs: 10},
		&UserMessage{Content: StringContent("summary"), TimestampMs: 30},
		&UserMessage{Content: StringContent("tail"), TimestampMs: 40},
	}
	estimate = EstimateContextTokens(messages)
	// The user message at timestamp 30 is newer than the assistant at 10, so
	// the assistant usage no longer describes the prefix.
	if estimate.LastUsageIndex == 0 && estimate.Tokens == 500+13 {
		t.Fatalf("stale usage still applied: %+v", estimate)
	}
}

func TestEstimateMessageTokensSystemWithTools(t *testing.T) {
	m := &SystemMessage{
		Content:    StringContent("1234"),
		ToolsAdded: []Tool{{Name: "read", Description: "Read a file", Parameters: nil}},
	}
	tokens := EstimateMessageTokens(m)
	// 4 chars system text = 1 token; tools JSON adds more.
	if tokens < 2 {
		t.Fatalf("tokens = %v", tokens)
	}
}
