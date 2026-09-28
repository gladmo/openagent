package ai

// providers_smoke_test.go is the optional live smoke test: it performs a real
// 1-token stream against a provider when PI_SMOKE_TESTS=1 AND the provider's
// API key is set. It always skips otherwise (no network in normal test runs).

import (
	"os"
	"testing"
	"time"
)

func runProviderSmoke(t *testing.T, providerID, envVar string, prompt string) {
	t.Helper()
	if os.Getenv("PI_SMOKE_TESTS") != "1" {
		t.Skip("live smoke tests disabled (set PI_SMOKE_TESTS=1)")
	}
	apiKey := os.Getenv(envVar)
	if apiKey == "" {
		t.Skipf("%s not set", envVar)
	}

	models := BuiltinModels()
	model := models.GetModel(providerID, prompt)
	if model == nil {
		t.Fatalf("model %s/%s not found", providerID, prompt)
	}
	maxTokens := 16.0
	stream := models.StreamSimple(model, Context{Messages: []Message{&UserMessage{Content: StringContent("Reply with exactly: ok"), TimestampMs: 1}}}, &SimpleStreamOptions{
		StreamOptions: StreamOptions{MaxTokens: &maxTokens},
	})
	result := collectStreamResult(t, stream)
	if result.StopReason == StopError {
		t.Fatalf("stream failed: %s", errorMessageOf(result))
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}
}

func collectStreamResult(t *testing.T, stream *AssistantMessageEventStream) *AssistantMessage {
	t.Helper()
	done := make(chan *AssistantMessage, 1)
	go func() {
		for {
			if _, ok := stream.Next(); !ok {
				break
			}
		}
		done <- stream.Result()
	}()
	select {
	case result := <-done:
		return result
	case <-timeoutAfterSeconds(20):
		t.Fatal("smoke stream did not terminate")
		return nil
	}
}

func errorMessageOf(message *AssistantMessage) string {
	if message.ErrorMessage != nil {
		return *message.ErrorMessage
	}
	return "<nil>"
}

func TestSmokeAnthropic(t *testing.T) {
	runProviderSmoke(t, "anthropic", "ANTHROPIC_API_KEY", "claude-haiku-4-5")
}

func TestSmokeDeepSeek(t *testing.T) {
	runProviderSmoke(t, "deepseek", "DEEPSEEK_API_KEY", "deepseek-chat")
}

func timeoutAfterSeconds(seconds int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		timer := time.NewTimer(time.Duration(seconds) * time.Second)
		<-timer.C
		close(ch)
	}()
	return ch
}
