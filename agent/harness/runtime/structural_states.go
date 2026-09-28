package runtime

// structural_states.go ports harness/runtime/drive/structural.ts's state
// constructors between the summary leaves: ready -> effect_pending ->
// retry_wait -> ready, plus the compaction-hook outcome routing.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// EffectPendingFromReady mirrors effectPendingFromReady: the summary leaf
// carrying the task + summary context at the reserved attempt with empty
// usage ids.
func EffectPendingFromReady(ready *session.OperationState) *session.OperationState {
	next := OperationScopeCopy(ready)
	next.At = session.AtSummaryEffectPending
	next.Task = ready.Task
	next.Attempt = ready.NextAttempt
	return &next
}

// RetryWaitFromEffect mirrors retryWaitFromEffect: nextAttempt + 1,
// notBefore from the retry policy, errorMessage carried.
func RetryWaitFromEffect(effect *session.OperationState, errorMessage string, policy ai.RetryPolicy, now float64) *session.OperationState {
	next := OperationScopeCopy(effect)
	next.At = session.AtSummaryRetryWait
	next.Task = effect.Task
	next.NextAttempt = effect.Attempt + 1
	next.NotBefore = RetryNotBefore(policy, int64(effect.Attempt), now)
	next.ErrorMessage = errorMessage
	return &next
}

// ReadyFromRetryWait mirrors readyFromRetryWait: back to ready at the
// reserved next attempt.
func ReadyFromRetryWait(retry *session.OperationState) *session.OperationState {
	next := OperationScopeCopy(retry)
	next.At = session.AtSummaryReady
	next.Task = retry.Task
	next.NextAttempt = retry.NextAttempt
	return &next
}

// CompactionHookOutcome captures the before_compaction hook routing:
// decline -> declined outcome; compaction -> a hook-provided compaction
// entry; otherwise proceed ready.
func CompactionHookOutcome(declined bool, compaction *jsonx.Obj) string {
	if declined {
		return "declined"
	}
	if compaction != nil {
		return "compaction"
	}
	return "ready"
}

// StructuralUsageEvent renders the structural usage event payload.
func StructuralUsageEvent(lane string, row session.UsageRow, totals ai.Usage) HarnessEvent {
	event := eventLane(lane, "usage")
	rowObj := jsonx.NewObj()
	rowObj.Set("id", row.ID)
	if row.EntryID != nil {
		rowObj.Set("entryId", *row.EntryID)
	} else {
		rowObj.Set("entryId", nil)
	}
	event.Set("row", rowObj)
	totalsObj := jsonx.NewObj()
	totalsObj.Set("input", totals.Input)
	totalsObj.Set("output", totals.Output)
	totalsObj.Set("cacheRead", totals.CacheRead)
	totalsObj.Set("cacheWrite", totals.CacheWrite)
	totalsObj.Set("totalTokens", totals.TotalTokens)
	event.Set("totals", totalsObj)
	return event
}
