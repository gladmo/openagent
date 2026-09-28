package kinds

// generation_recovery.go ports harness/pico3/kinds/generation.ts's
// recovery decisions: the requesting crash path (count the attempt,
// retry per policy, emit usage + retrying events), the retrying backoff
// reset, the deferred poll loop, and the abort partial display entry.

import (
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// RequestingRecoveryDecision captures the requesting-phase crash path.
type RequestingRecoveryDecision struct {
	Fail     bool
	Reason   string
	Detail   string
	UntilMS  float64
	HasRetry bool
	Attempt  float64
}

// RecoverRequesting mirrors the requesting phase: the call may have
// happened with no result lookup, so count it as a failed attempt and
// retry per policy with detail "interrupted".
func RecoverRequesting(cp GenerationCheckpointFields, now float64) *RequestingRecoveryDecision {
	policy := ai.RetryPolicy{
		Enabled:         cp.Prep.RetryEnabled,
		MaxRetries:      cp.Prep.MaxRetries,
		BaseDelayMs:     cp.Prep.BaseDelayMS,
		MaxAgentDelayMs: cp.Prep.MaxAgentDelayMS,
	}
	decision := RetryDecisionFor(RetryDecisionCP{Retry: policy, Attempt: cp.Prep.Attempt}, nil, now)
	out := &RequestingRecoveryDecision{Attempt: cp.Prep.Attempt}
	if decision.Kind == "fail" {
		out.Fail = true
		out.Reason = decision.Reason
		out.Detail = "interrupted"
		return out
	}
	out.UntilMS = decision.UntilMS
	out.HasRetry = true
	return out
}

// UsageEntryData builds the pi.usage display entry data for the
// interrupted attempt.
func UsageEntryData(attempt float64) *jsonx.Obj {
	return jsonx.ObjFrom("attempt", attempt, "error", "interrupted")
}

// RetryingReset mirrors the retrying phase's next checkpoint: strip
// untilMs/lastError back to prepared.
func RetryingReset(cp GenerationCheckpointFields) *jsonx.Obj {
	next := jsonx.NewObj()
	next.Set("phase", GenPhasePrepared)
	next.Set("cutoff", float64(cp.Prep.Cutoff))
	if cp.Prep.System != nil {
		next.Set("system", float64(*cp.Prep.System))
	} else {
		next.Set("system", nil)
	}
	if cp.Prep.Model != nil {
		next.Set("model", cp.Prep.Model)
	}
	next.Set("thinkingLevel", cp.Prep.ThinkingLevel)
	tools := make([]any, 0, len(cp.Prep.Tools))
	for _, tool := range cp.Prep.Tools {
		tools = append(tools, tool)
	}
	next.Set("tools", tools)
	return next
}

// DeferredPollDecision captures the deferred poll outcome.
type DeferredPollDecision struct {
	// StillDeferred carries the new handle + pollAt.
	StillDeferred bool
	Handle        *jsonx.Obj
	PollAt        float64
	// Terminal carries the settled message.
	Terminal any
}

// DecideDeferredPoll mirrors the deferred phase's poll branch: still
// deferred -> reschedule with the new handle; else the message settles
// (recorded into the turn view by the caller) and prep (minus handle/
// pollAt) classifies.
func DecideDeferredPoll(newHandle *jsonx.Obj, pollAfterMS, now float64, terminal any) *DeferredPollDecision {
	if newHandle != nil {
		return &DeferredPollDecision{
			StillDeferred: true,
			Handle:        newHandle,
			PollAt:        DeferredPollAt(now, pollAfterMS),
		}
	}
	return &DeferredPollDecision{Terminal: terminal}
}

// AbortPartialDecision captures the abort closure's partial handling.
type AbortPartialDecision struct {
	// AppendPartial is true when the partial has content and becomes a
	// display-only assistant entry.
	AppendPartial bool
	Attempt       float64
	Partial       *jsonx.Obj
}

// DecideAbortPartial mirrors the abort partial rule: a partial with
// content goes into `data` (never `model`) with stopReason aborted; the
// turn clears and inputs resolve unanswered/aborted.
func DecideAbortPartial(partial *jsonx.Obj, hasContent bool, attempt float64) *AbortPartialDecision {
	if partial == nil || !hasContent {
		return &AbortPartialDecision{Attempt: attempt}
	}
	return &AbortPartialDecision{AppendPartial: true, Attempt: attempt, Partial: partial}
}

// DisplayAbortedData builds the abort display entry data.
func DisplayAbortedData(partial *jsonx.Obj, attempt float64) *jsonx.Obj {
	display := cloneJSON(partial)
	if obj, ok := display.(*jsonx.Obj); ok {
		obj.Set("stopReason", "aborted")
	}
	return jsonx.ObjFrom("attempt", attempt, "display", display, "reason", "aborted")
}

// PrepForClassify strips the deferred extras (handle/pollAt) off a
// checkpoint for classify.
func PrepForClassify(cp GenerationCheckpointFields) *jsonx.Obj {
	next := RetryingReset(cp)
	return next
}
