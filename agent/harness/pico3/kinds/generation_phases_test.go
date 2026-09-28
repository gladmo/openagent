package kinds

// Ports of kinds/generation.ts phase model behaviors.

import (
	"testing"

	"github.com/gladmo/openagent/agent/harness/pico3"
	"github.com/gladmo/openagent/jsonx"
)

func TestParseGenerationCheckpoint(t *testing.T) {
	system := float64(5)
	maxDelay := float64(30000)
	checkpoint := jsonx.ObjFrom(
		"phase", "retrying",
		"cutoff", float64(9),
		"system", system,
		"model", jsonx.ObjFrom("provider", "p", "modelId", "m"),
		"thinkingLevel", "high",
		"tools", []any{"bash", "read"},
		"retry", jsonx.ObjFrom("enabled", true, "maxRetries", float64(3), "baseDelayMs", float64(2000), "maxAgentDelayMs", maxDelay),
		"attempt", float64(2),
		"untilMs", float64(88),
		"lastError", "rate limited",
	)
	fields := ParseGenerationCheckpoint(checkpoint)
	if fields.Phase != GenPhaseRetrying || fields.UntilMS != 88 || fields.LastError != "rate limited" {
		t.Fatalf("fields = %+v", fields)
	}
	prep := fields.Prep
	if prep.Cutoff != 9 || prep.System == nil || *prep.System != 5 {
		t.Fatalf("prep = %+v", prep)
	}
	if prep.ThinkingLevel != "high" || len(prep.Tools) != 2 || prep.Tools[0] != "bash" {
		t.Fatalf("prep = %+v", prep)
	}
	if !prep.RetryEnabled || prep.MaxRetries != 3 || prep.BaseDelayMS != 2000 {
		t.Fatalf("retry = %+v", prep)
	}
	if prep.MaxAgentDelayMS == nil || *prep.MaxAgentDelayMS != 30000 {
		t.Fatal("maxAgentDelay")
	}
	if prep.Attempt != 2 || prep.Model == nil {
		t.Fatalf("prep = %+v", prep)
	}

	// Deferred phase carries handle + pollAt.
	handle := jsonx.ObjFrom("provider", "p", "id", "d1")
	deferred := jsonx.ObjFrom("phase", GenPhaseDeferred, "cutoff", float64(1), "handle", handle, "pollAt", float64(42))
	fields = ParseGenerationCheckpoint(deferred)
	if fields.Phase != GenPhaseDeferred || fields.Handle == nil || fields.PollAt != 42 {
		t.Fatalf("fields = %+v", fields)
	}
}

func TestGenerationFailedCompletion(t *testing.T) {
	completion := GenerationFailedCompletion("provider", "boom", nil)
	failure := completion.MustGet("failure").(*jsonx.Obj)
	if failure.MustGet("reason") != "provider" || failure.MustGet("detail") != "boom" {
		t.Fatalf("failure = %v", failure)
	}
	if _, has := failure.Get("assistant"); has {
		t.Fatal("assistant key present without value")
	}
	assistant := int64(7)
	completion = GenerationFailedCompletion("overflow", "too long", &assistant)
	failure = completion.MustGet("failure").(*jsonx.Obj)
	if failure.MustGet("assistant") != float64(7) {
		t.Fatal("assistant missing")
	}
}

func TestDisplayAssistantData(t *testing.T) {
	display := jsonx.ObjFrom("role", "assistant", "content", []any{})
	data := DisplayAssistantData(2, display, "error")
	if data.MustGet("attempt") != float64(2) || data.MustGet("reason") != "error" {
		t.Fatalf("data = %v", data)
	}
	if data.MustGet("display").(*jsonx.Obj) == nil {
		t.Fatal("display missing")
	}
}

func TestIsDisplayOnly(t *testing.T) {
	// Display-only: pi.assistant with data.display and no model.
	entry := &pico3.Entry{
		Kind: "pi.assistant",
		Data: DisplayAssistantData(1, jsonx.NewObj(), "aborted"),
	}
	if !IsDisplayOnly(entry) {
		t.Fatal("display-only not detected")
	}
	// With model: not display-only.
	entry.Model = []any{jsonx.ObjFrom("role", "assistant")}
	if IsDisplayOnly(entry) {
		t.Fatal("model entry counted display-only")
	}
	// Wrong kind.
	entry2 := &pico3.Entry{Kind: "pi.user", Data: jsonx.NewObj()}
	if IsDisplayOnly(entry2) {
		t.Fatal("wrong kind")
	}
	// No data.
	if IsDisplayOnly(&pico3.Entry{Kind: "pi.assistant"}) {
		t.Fatal("no data counted")
	}
}

func TestGenerationConfigDefaults(t *testing.T) {
	defaults := GenerationConfigDefaults()
	if defaults["thinkingLevel"] != "off" || defaults["profile"] != "default" {
		t.Fatalf("defaults = %v", defaults)
	}
	retry := defaults["retry"].(*jsonx.Obj)
	if retry.MustGet("enabled") != true || retry.MustGet("maxRetries") != float64(3) ||
		retry.MustGet("baseDelayMs") != float64(2000) || retry.MustGet("maxAgentDelayMs") != float64(60000) {
		t.Fatalf("retry = %v", retry)
	}
	// model has no default: absent from the map.
	if _, has := defaults["model"]; has {
		t.Fatal("model default leaked")
	}
}
