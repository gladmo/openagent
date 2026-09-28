package kinds

// generation.go ports harness/pico3/kinds/generation.ts's retryDecision:
// the shared retry-vs-fail policy over an assistant message and the
// captured retry policy.

import (
	"math"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// RetryDecision mirrors the TS union.
type RetryDecision struct {
	Kind    string // "retry" | "fail"
	Reason  string // provider | retries_exhausted
	UntilMS float64
}

// RetryDecisionCP carries the checkpoint's retry fields.
type RetryDecisionCP struct {
	Retry   ai.RetryPolicy
	Attempt float64
}

// RetryDecision mirrors retryDecision: a non-retryable assistant error
// fails as provider; disabled retry fails as provider; attempts beyond
// maxRetries fail as retries_exhausted; otherwise retry at now + min(base
// * 2^(attempt-1), maxAgentDelayMs default 60s), clamped to
// MAX_SAFE_INTEGER.
func RetryDecisionFor(cp RetryDecisionCP, message *ai.AssistantMessage, now float64) RetryDecision {
	if message != nil && !ai.IsRetryableAssistantError(message) {
		return RetryDecision{Kind: "fail", Reason: "provider"}
	}
	if !cp.Retry.Enabled {
		return RetryDecision{Kind: "fail", Reason: "provider"}
	}
	if cp.Attempt > float64(cp.Retry.MaxRetries) {
		return RetryDecision{Kind: "fail", Reason: "retries_exhausted"}
	}
	exp := cp.Attempt - 1
	if exp < 0 {
		exp = 0
	}
	delay := cp.Retry.BaseDelayMs * math.Pow(2, exp)
	if delay > 9007199254740991 {
		delay = 9007199254740991
	}
	maxDelay := float64(60000)
	if cp.Retry.MaxAgentDelayMs != nil {
		maxDelay = *cp.Retry.MaxAgentDelayMs
	}
	effective := delay
	if maxDelay < delay {
		effective = maxDelay
	}
	return RetryDecision{Kind: "retry", UntilMS: now + effective}
}

// ThresholdCollapseThrough mirrors thresholdCollapseThrough's read-side
// decision: no collapse when one is already active or the threshold is
// off; compute used tokens as max(usage input+output, estimate incl. the
// new message); below threshold -> none; else the chooseThrough cut.
func ThresholdCollapseThrough(
	activeCollapse bool,
	threshold, keepRecent float64,
	usageInput, usageOutput float64,
	estimateAll float64,
	choose func() (int64, bool),
) (int64, bool) {
	if activeCollapse {
		return 0, false
	}
	if threshold <= 0 {
		return 0, false
	}
	usageUsed := usageInput + usageOutput
	used := usageUsed
	if estimateAll > used {
		used = estimateAll
	}
	if used <= threshold {
		return 0, false
	}
	return choose()
}

// UsageOfMessage reads the usage block off a stored assistant message.
func UsageOfMessage(message *jsonx.Obj) (input, output float64) {
	if message == nil {
		return 0, 0
	}
	usageValue, ok := message.Get("usage")
	if !ok {
		return 0, 0
	}
	usage, ok := usageValue.(*jsonx.Obj)
	if !ok {
		return 0, 0
	}
	if v, ok := usage.Get("input"); ok {
		if f, ok := v.(float64); ok {
			input = f
		}
	}
	if v, ok := usage.Get("output"); ok {
		if f, ok := v.(float64); ok {
			output = f
		}
	}
	return input, output
}
