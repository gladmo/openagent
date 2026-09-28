package kinds

// Ports of kinds/generation.ts request derivation decisions.

import (
	"testing"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func userMessageOf(text string) *ai.UserMessage {
	return &ai.UserMessage{Content: ai.StringContent(text), TimestampMs: 1}
}

func errorAssistantOf(stop string) *ai.AssistantMessage {
	return &ai.AssistantMessage{StopReason: stop}
}

func TestEstimateGenerationFilters(t *testing.T) {
	messages := []ai.Message{
		userMessageOf("hello world"),
		errorAssistantOf("error"),
		errorAssistantOf("aborted"),
		userMessageOf("second message"),
	}
	// System messages are dropped too.
	estimate := EstimateGeneration(messages)
	expected := ai.EstimateContextTokens([]ai.Message{messages[0], messages[3]}).Tokens
	if estimate != expected {
		t.Fatalf("estimate = %v expected = %v", estimate, expected)
	}
}

func TestCheckOverflow(t *testing.T) {
	// window 10000, maxTokens 2000 -> budget 8000.
	if CheckOverflow(8000, 10000, 2000) {
		t.Fatal("at budget overflows")
	}
	if !CheckOverflow(8001, 10000, 2000) {
		t.Fatal("over budget passes")
	}
}

func TestBuildRequest(t *testing.T) {
	messages := []any{jsonx.ObjFrom("role", "user", "content", "hi")}

	// No effective tools: messages pass through unchanged.
	build, err := BuildRequest(messages, func([]any) []string { return nil }, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(build.Messages) != 1 || build.RemovedTools != nil {
		t.Fatalf("build = %+v", build)
	}

	// With tools: the removal system message is appended.
	build, err = BuildRequest(messages, func([]any) []string { return []string{"bash", "read"} }, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(build.Messages) != 2 {
		t.Fatalf("messages = %d", len(build.Messages))
	}
	removal := build.Messages[1].(*jsonx.Obj)
	if removal.MustGet("role") != "system" {
		t.Fatal("removal not system")
	}
	removed := removal.MustGet("toolsRemoved").([]any)
	if len(removed) != 2 || removed[0].(*jsonx.Obj).MustGet("name") != "bash" {
		t.Fatalf("removed = %v", removed)
	}
	if removal.MustGet("timestamp") != float64(1000) {
		t.Fatal("timestamp")
	}
}

func TestBuildRequestBeforeRequestHook(t *testing.T) {
	messages := []any{jsonx.ObjFrom("role", "user", "content", "original")}
	rewritten := []any{jsonx.ObjFrom("role", "user", "content", "rewritten")}
	build, err := BuildRequest(
		messages,
		func([]any) []string { return nil },
		func([]any) ([]any, error) { return rewritten, nil },
		1000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if build.Messages[0].(*jsonx.Obj).MustGet("content") != "rewritten" {
		t.Fatal("hook rewrite lost")
	}
	// Source untouched.
	if messages[0].(*jsonx.Obj).MustGet("content") != "original" {
		t.Fatal("source mutated")
	}
}

func TestDeferredPollAt(t *testing.T) {
	if at := DeferredPollAt(1000, 3000); at != 4000 {
		t.Fatalf("at = %v", at)
	}
	// Default 5s when unset or non-positive.
	if at := DeferredPollAt(1000, 0); at != 6000 {
		t.Fatalf("at = %v", at)
	}
	if at := DeferredPollAt(1000, -1); at != 6000 {
		t.Fatalf("at = %v", at)
	}
}

func TestClassifyTerminal(t *testing.T) {
	// Error stop reason routes to retry.
	decision := ClassifyTerminal("error", "provider exploded")
	if !decision.RetryAfterError || decision.OK {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.FailDetail != "provider exploded" {
		t.Fatalf("detail = %s", decision.FailDetail)
	}
	// Normal stop reason classifies OK.
	decision = ClassifyTerminal("stop", "")
	if !decision.OK || decision.RetryAfterError {
		t.Fatalf("decision = %+v", decision)
	}
}
