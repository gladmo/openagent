package kinds

// Ports of kinds/generation.ts recovery decisions.

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func recoveryCheckpoint(attempt float64, enabled bool, maxRetries int) *jsonx.Obj {
	max := float64(60000)
	return jsonx.ObjFrom(
		"phase", GenPhaseRequesting,
		"cutoff", float64(4),
		"model", jsonx.ObjFrom("provider", "p", "modelId", "m"),
		"thinkingLevel", "off",
		"tools", []any{},
		"retry", jsonx.ObjFrom("enabled", enabled, "maxRetries", float64(maxRetries), "baseDelayMs", float64(1000), "maxAgentDelayMs", max),
		"attempt", attempt,
	)
}

func TestRecoverRequestingRetries(t *testing.T) {
	cp := ParseGenerationCheckpoint(recoveryCheckpoint(1, true, 3))
	decision := RecoverRequesting(cp, 1000)
	if decision.Fail || !decision.HasRetry || decision.Attempt != 1 {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.UntilMS != 2000 { // 1000 + 1000*2^0
		t.Fatalf("until = %v", decision.UntilMS)
	}
}

func TestRecoverRequestingExhausted(t *testing.T) {
	cp := ParseGenerationCheckpoint(recoveryCheckpoint(4, true, 3))
	decision := RecoverRequesting(cp, 1000)
	if !decision.Fail || decision.Reason != "retries_exhausted" || decision.Detail != "interrupted" {
		t.Fatalf("decision = %+v", decision)
	}
	// Disabled retry fails provider.
	cp = ParseGenerationCheckpoint(recoveryCheckpoint(1, false, 3))
	decision = RecoverRequesting(cp, 1000)
	if !decision.Fail || decision.Reason != "provider" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestUsageEntryData(t *testing.T) {
	data := UsageEntryData(2)
	if data.MustGet("attempt") != float64(2) || data.MustGet("error") != "interrupted" {
		t.Fatalf("data = %v", data)
	}
}

func TestRetryingReset(t *testing.T) {
	system := float64(9)
	checkpoint := jsonx.ObjFrom(
		"phase", GenPhaseRetrying,
		"cutoff", float64(4),
		"system", system,
		"model", jsonx.ObjFrom("provider", "p", "modelId", "m"),
		"thinkingLevel", "low",
		"tools", []any{"bash"},
		"retry", jsonx.ObjFrom("enabled", true, "maxRetries", float64(3), "baseDelayMs", float64(100)),
		"attempt", float64(2),
		"untilMs", float64(99),
		"lastError", "boom",
	)
	reset := RetryingReset(ParseGenerationCheckpoint(checkpoint))
	if reset.MustGet("phase") != GenPhasePrepared {
		t.Fatalf("phase = %v", reset.MustGet("phase"))
	}
	if _, has := reset.Get("untilMs"); has {
		t.Fatal("untilMs survived")
	}
	if _, has := reset.Get("lastError"); has {
		t.Fatal("lastError survived")
	}
	if reset.MustGet("cutoff") != float64(4) || reset.MustGet("system") != float64(9) {
		t.Fatalf("reset = %v", reset)
	}
	if reset.MustGet("thinkingLevel") != "low" {
		t.Fatal("thinkingLevel lost")
	}
}

func TestDecideDeferredPoll(t *testing.T) {
	// Still deferred: new handle + rescheduled poll.
	newHandle := jsonx.ObjFrom("provider", "p", "id", "d2")
	decision := DecideDeferredPoll(newHandle, 3000, 1000, nil)
	if !decision.StillDeferred || decision.Handle == nil || decision.PollAt != 4000 {
		t.Fatalf("decision = %+v", decision)
	}
	// Terminal: the message settles.
	terminal := jsonx.ObjFrom("role", "assistant")
	decision = DecideDeferredPoll(nil, 0, 1000, terminal)
	if decision.StillDeferred || decision.Terminal == nil {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecideAbortPartial(t *testing.T) {
	partial := jsonx.ObjFrom("role", "assistant", "content", []any{jsonx.ObjFrom("type", "text", "text", "partial")})
	// With content: appended as display.
	decision := DecideAbortPartial(partial, true, 2)
	if !decision.AppendPartial || decision.Partial == nil {
		t.Fatalf("decision = %+v", decision)
	}
	// Without content: not appended.
	decision = DecideAbortPartial(partial, false, 2)
	if decision.AppendPartial {
		t.Fatal("empty partial appended")
	}
	// Nil partial: not appended.
	decision = DecideAbortPartial(nil, true, 2)
	if decision.AppendPartial {
		t.Fatal("nil partial appended")
	}
}

func TestDisplayAbortedData(t *testing.T) {
	partial := jsonx.ObjFrom("role", "assistant", "content", []any{})
	data := DisplayAbortedData(partial, 3)
	if data.MustGet("attempt") != float64(3) || data.MustGet("reason") != "aborted" {
		t.Fatalf("data = %v", data)
	}
	display := data.MustGet("display").(*jsonx.Obj)
	if display.MustGet("stopReason") != "aborted" {
		t.Fatal("stopReason not set")
	}
	// Source untouched.
	if _, has := partial.Get("stopReason"); has {
		t.Fatal("partial mutated")
	}
}
