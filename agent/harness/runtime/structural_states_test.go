package runtime

// Ports of structural.ts state constructors.

import (
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func summaryScope() *session.OperationState {
	return &session.OperationState{
		Control: session.Control{Status: "running"},
		Task:    jsonx.ObjFrom("taskId", "task-1"),
	}
}

func TestEffectPendingFromReady(t *testing.T) {
	ready := summaryScope()
	ready.At = session.AtSummaryReady
	ready.NextAttempt = 2
	effect := EffectPendingFromReady(ready)
	if effect.At != session.AtSummaryEffectPending {
		t.Fatalf("at = %s", effect.At)
	}
	if effect.Attempt != 2 {
		t.Fatalf("attempt = %d", effect.Attempt)
	}
	if effect.Task == nil || effect.Task.MustGet("taskId") != "task-1" {
		t.Fatal("task lost")
	}
}

func TestRetryWaitFromEffect(t *testing.T) {
	effect := summaryScope()
	effect.At = session.AtSummaryEffectPending
	effect.Attempt = 2
	policy := ai.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 1000}
	retry := RetryWaitFromEffect(effect, "summarizer exploded", policy, 1000)
	if retry.At != session.AtSummaryRetryWait {
		t.Fatalf("at = %s", retry.At)
	}
	if retry.NextAttempt != 3 {
		t.Fatalf("nextAttempt = %d", retry.NextAttempt)
	}
	if retry.NotBefore != 3000 { // 1000 + 1000*2^1
		t.Fatalf("notBefore = %v", retry.NotBefore)
	}
	if retry.ErrorMessage != "summarizer exploded" {
		t.Fatalf("message = %q", retry.ErrorMessage)
	}
}

func TestReadyFromRetryWait(t *testing.T) {
	retry := summaryScope()
	retry.At = session.AtSummaryRetryWait
	retry.NextAttempt = 4
	ready := ReadyFromRetryWait(retry)
	if ready.At != session.AtSummaryReady || ready.NextAttempt != 4 {
		t.Fatalf("ready = %+v", ready)
	}
	if ready.Task == nil {
		t.Fatal("task lost")
	}
}

func TestCompactionHookOutcome(t *testing.T) {
	if outcome := CompactionHookOutcome(false, nil); outcome != "ready" {
		t.Fatalf("outcome = %s", outcome)
	}
	if outcome := CompactionHookOutcome(true, nil); outcome != "declined" {
		t.Fatalf("outcome = %s", outcome)
	}
	if outcome := CompactionHookOutcome(false, jsonx.ObjFrom("kind", "compaction")); outcome != "compaction" {
		t.Fatalf("outcome = %s", outcome)
	}
}

func TestStructuralUsageEvent(t *testing.T) {
	entryID := "e-9"
	row := session.UsageRow{ID: "u1", EntryID: &entryID}
	event := StructuralUsageEvent("main", row, ai.Usage{Input: 3, Output: 4, TotalTokens: 7})
	if EventType(event) != "usage" {
		t.Fatalf("type = %s", EventType(event))
	}
	if event.MustGet("row").(*jsonx.Obj).MustGet("entryId") != "e-9" {
		t.Fatal("row entry linkage")
	}
	totals := event.MustGet("totals").(*jsonx.Obj)
	if totals.MustGet("input") != float64(3) || totals.MustGet("totalTokens") != float64(7) {
		t.Fatalf("totals = %v", totals)
	}
}
