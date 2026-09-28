package kinds

// Ports of kinds/generation.ts retryDecision + threshold read-side.

import (
	"testing"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func retryPolicy(enabled bool, maxRetries int, base float64, maxAgent *float64) ai.RetryPolicy {
	return ai.RetryPolicy{Enabled: enabled, MaxRetries: maxRetries, BaseDelayMs: base, MaxAgentDelayMs: maxAgent}
}

func TestRetryDecisionNonRetryableFails(t *testing.T) {
	// A non-error message is not retryable -> provider failure.
	cp := RetryDecisionCP{Retry: retryPolicy(true, 5, 100, nil), Attempt: 1}
	message := &ai.AssistantMessage{}
	decision := RetryDecisionFor(cp, message, 1000)
	if decision.Kind != "fail" || decision.Reason != "provider" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestRetryDecisionDisabledFails(t *testing.T) {
	cp := RetryDecisionCP{Retry: retryPolicy(false, 5, 100, nil), Attempt: 1}
	decision := RetryDecisionFor(cp, nil, 1000)
	if decision.Kind != "fail" || decision.Reason != "provider" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestRetryDecisionExhausted(t *testing.T) {
	// attempt > maxRetries fails exhausted (nil message counts retryable).
	cp := RetryDecisionCP{Retry: retryPolicy(true, 3, 100, nil), Attempt: 4}
	decision := RetryDecisionFor(cp, nil, 1000)
	if decision.Kind != "fail" || decision.Reason != "retries_exhausted" {
		t.Fatalf("decision = %+v", decision)
	}
	// attempt == maxRetries still retries.
	cp.Attempt = 3
	decision = RetryDecisionFor(cp, nil, 1000)
	if decision.Kind != "retry" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestRetryDecisionBackoffAndCap(t *testing.T) {
	// base * 2^(attempt-1): 100, 200, 400.
	now := float64(1000)
	cp := RetryDecisionCP{Retry: retryPolicy(true, 5, 100, nil), Attempt: 1}
	if decision := RetryDecisionFor(cp, nil, now); decision.UntilMS != 1100 {
		t.Fatalf("until = %v", decision.UntilMS)
	}
	cp.Attempt = 2
	if decision := RetryDecisionFor(cp, nil, now); decision.UntilMS != 1200 {
		t.Fatalf("until = %v", decision.UntilMS)
	}
	cp.Attempt = 3
	if decision := RetryDecisionFor(cp, nil, now); decision.UntilMS != 1400 {
		t.Fatalf("until = %v", decision.UntilMS)
	}
	// maxAgentDelayMs caps.
	maxDelay := float64(300)
	cp.Retry.MaxAgentDelayMs = &maxDelay
	cp.Attempt = 3
	if decision := RetryDecisionFor(cp, nil, now); decision.UntilMS != 1300 {
		t.Fatalf("until = %v", decision.UntilMS)
	}
	// Default cap 60s.
	cp.Retry.MaxAgentDelayMs = nil
	cp.Retry.BaseDelayMs = 1 << 40
	cp.Attempt = 1
	if decision := RetryDecisionFor(cp, nil, now); decision.UntilMS != 61000 {
		t.Fatalf("until = %v", decision.UntilMS)
	}
}

func TestThresholdCollapseThrough(t *testing.T) {
	choose := func() (int64, bool) { return 42, true }
	// Active collapse suppresses.
	if through, ok := ThresholdCollapseThrough(true, 1000, 500, 0, 0, 0, choose); ok {
		t.Fatalf("through = %d", through)
	}
	// Threshold off.
	if through, ok := ThresholdCollapseThrough(false, 0, 500, 0, 0, 0, choose); ok {
		t.Fatalf("through = %d", through)
	}
	// Usage path above threshold.
	if through, ok := ThresholdCollapseThrough(false, 1000, 500, 600, 500, 0, choose); !ok || through != 42 {
		t.Fatalf("through = %d ok = %v", through, ok)
	}
	// Estimate path wins when larger.
	if through, ok := ThresholdCollapseThrough(false, 1000, 500, 100, 100, 5000, choose); !ok || through != 42 {
		t.Fatalf("through = %d ok = %v", through, ok)
	}
	// Below threshold: no collapse.
	if through, ok := ThresholdCollapseThrough(false, 1000, 500, 100, 200, 0, choose); ok {
		t.Fatalf("through = %d", through)
	}
	// Exactly at threshold: no collapse (<=).
	if through, ok := ThresholdCollapseThrough(false, 1000, 500, 500, 500, 0, choose); ok {
		t.Fatalf("through = %d", through)
	}
}

func TestUsageOfMessage(t *testing.T) {
	message := jsonx.ObjFrom("usage", jsonx.ObjFrom("input", float64(10), "output", float64(5)))
	input, output := UsageOfMessage(message)
	if input != 10 || output != 5 {
		t.Fatalf("usage = %v %v", input, output)
	}
	// Missing usage.
	if input, output := UsageOfMessage(jsonx.NewObj()); input != 0 || output != 0 {
		t.Fatal("default usage")
	}
	// Nil message.
	if input, output := UsageOfMessage(nil); input != 0 || output != 0 {
		t.Fatal("nil usage")
	}
}
