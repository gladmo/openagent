package runtime

// Ports of publishResponse classification behaviors.

import (
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func settleScope() *session.OperationState {
	return &session.OperationState{Control: session.Control{Status: "running"}, TriggerEntryID: "trig-1"}
}

func retryStateOf(attempt, max float64) struct {
	Attempt         float64
	MaxAttempts     float64
	RetryEnabled    bool
	MaxRetries      int
	BaseDelayMS     float64
	MaxAgentDelayMS *float64
} {
	return struct {
		Attempt         float64
		MaxAttempts     float64
		RetryEnabled    bool
		MaxRetries      int
		BaseDelayMS     float64
		MaxAgentDelayMS *float64
	}{Attempt: attempt, MaxAttempts: max, RetryEnabled: true, MaxRetries: 3, BaseDelayMS: 1000}
}

func emptyOptions() struct{ Recovery bool } { return struct{ Recovery bool }{} }

func neverOverflow(*jsonx.Obj) bool  { return false }
func neverRetryable(*jsonx.Obj) bool { return false }

func TestClassifyResponseAbortedInvariant(t *testing.T) {
	response := jsonx.ObjFrom("stopReason", "aborted")
	disposition := ClassifyResponse(session.AtAssistantEffectPending, response, settleScope(), "resp-1", false, false, emptyOptions(), retryStateOf(1, 4), neverRetryable, func() float64 { return 0 }, nil)
	if disposition.Invarianterror == nil {
		t.Fatal("aborted under running control accepted")
	}
}

func TestClassifyResponseOverflow(t *testing.T) {
	response := jsonx.ObjFrom("stopReason", "error", "errorMessage", "context length exceeded")
	prep := &ThresholdPreparation{TaskID: "task-9"}
	// Recovery already used -> provider failure.
	disposition := ClassifyResponse(session.AtAssistantEffectPending, response, settleScope(), "resp-1", true, true, emptyOptions(), retryStateOf(1, 4), neverRetryable, func() float64 { return 0 }, prep)
	if disposition.Failure == nil || disposition.Failure.Code != "assistant_error" {
		t.Fatalf("disposition = %+v", disposition)
	}
	// First overflow -> structural summary.deciding.
	disposition = ClassifyResponse(session.AtAssistantEffectPending, response, settleScope(), "resp-1", true, false, emptyOptions(), retryStateOf(1, 4), neverRetryable, func() float64 { return 0 }, prep)
	if disposition.Settled == nil || disposition.Settled.At != session.AtSummaryDeciding {
		t.Fatalf("disposition = %+v", disposition.Settled)
	}
	task := disposition.Settled.Task
	if task.MustGet("reason") != "overflow" {
		t.Fatalf("task = %v", task)
	}
	boundary := task.MustGet("boundary").(*jsonx.Obj)
	resume := boundary.MustGet("resumeAfter").(*jsonx.Obj)
	if resume.MustGet("continuation").(*jsonx.Obj).MustGet("overflowRecoveryUsed") != true {
		t.Fatal("overflowRecoveryUsed not set")
	}
}

func TestClassifyResponseDeferred(t *testing.T) {
	response := jsonx.ObjFrom("stopReason", "deferred", "deferred", jsonx.ObjFrom("id", "d1"))
	disposition := ClassifyResponse(session.AtAssistantEffectPending, response, settleScope(), "resp-1", false, false, emptyOptions(), retryStateOf(1, 4), neverRetryable, func() float64 { return 0 }, nil)
	if disposition.Settled == nil || disposition.Settled.At != session.AtDeferredSuspended {
		t.Fatalf("disposition = %+v", disposition.Settled)
	}
	if disposition.Settled.SourceEntryID != "resp-1" || disposition.Settled.Poll != 0 {
		t.Fatal("suspension fields")
	}
}

func TestClassifyResponseErrorRetry(t *testing.T) {
	response := jsonx.ObjFrom("stopReason", "error", "errorMessage", "rate limited")
	isRetryable := func(*jsonx.Obj) bool { return true }
	// Within attempts -> retry wait.
	disposition := ClassifyResponse(session.AtAssistantEffectPending, response, settleScope(), "resp-1", false, false, emptyOptions(), retryStateOf(1, 4), isRetryable, func() float64 { return 9999 }, nil)
	if disposition.Settled == nil || disposition.Settled.At != session.AtAssistantRetryWait {
		t.Fatalf("disposition = %+v", disposition.Settled)
	}
	if disposition.Settled.NextAttempt != 2 || disposition.Settled.NotBefore != 9999 {
		t.Fatal("retry fields")
	}
	if disposition.Settled.ErrorMessage != "rate limited" {
		t.Fatalf("message = %q", disposition.Settled.ErrorMessage)
	}
	// At max attempts -> failure.
	disposition = ClassifyResponse(session.AtAssistantEffectPending, response, settleScope(), "resp-1", false, false, emptyOptions(), retryStateOf(4, 4), isRetryable, func() float64 { return 0 }, nil)
	if disposition.Failure == nil {
		t.Fatal("exhausted retries not failing")
	}
	// Non-retryable -> immediate failure.
	disposition = ClassifyResponse(session.AtAssistantEffectPending, response, settleScope(), "resp-1", false, false, emptyOptions(), retryStateOf(1, 4), neverRetryable, func() float64 { return 0 }, nil)
	if disposition.Failure == nil {
		t.Fatal("non-retryable not failing")
	}
}

func TestClassifyResponseTools(t *testing.T) {
	response := jsonx.ObjFrom("stopReason", "toolUse", "content", []any{
		jsonx.ObjFrom("type", "text", "text", "hi"),
		jsonx.ObjFrom("type", "toolCall", "id", "c1"),
		jsonx.ObjFrom("type", "toolCall", "id", "c2"),
	})
	disposition := ClassifyResponse(session.AtAssistantEffectPending, response, settleScope(), "resp-1", false, false, emptyOptions(), retryStateOf(1, 4), neverRetryable, func() float64 { return 0 }, nil)
	if disposition.Settled == nil || disposition.Settled.At != session.AtTools {
		t.Fatalf("disposition = %+v", disposition.Settled)
	}
	calls := disposition.Settled.Batch.MustGet("calls").([]any)
	if len(calls) != 2 {
		t.Fatalf("calls = %d", len(calls))
	}
	first := calls[0].(*jsonx.Obj)
	if first.MustGet("status") != "planned" || first.MustGet("sourceIndex") != float64(1) {
		t.Fatalf("call = %v", first)
	}
	// toolUse without calls -> failure.
	empty := jsonx.ObjFrom("stopReason", "toolUse", "content", []any{})
	disposition = ClassifyResponse(session.AtAssistantEffectPending, empty, settleScope(), "resp-1", false, false, emptyOptions(), retryStateOf(1, 4), neverRetryable, func() float64 { return 0 }, nil)
	if disposition.Failure == nil || disposition.Committed.MustGet("errorMessage") != "Provider reported tool use without any tool calls" {
		t.Fatalf("disposition = %+v", disposition)
	}
}

func TestClassifyResponseFinish(t *testing.T) {
	response := jsonx.ObjFrom("stopReason", "stop", "content", []any{jsonx.ObjFrom("type", "text", "text", "answer")})
	disposition := ClassifyResponse(session.AtAssistantEffectPending, response, settleScope(), "resp-1", false, false, emptyOptions(), retryStateOf(1, 4), neverRetryable, func() float64 { return 0 }, nil)
	if disposition.Settled == nil || disposition.Settled.At != session.AtCheckpoint {
		t.Fatalf("disposition = %+v", disposition.Settled)
	}
	continuation := disposition.Settled.Continuation
	if continuation.MustGet("kind") != "may_finish" || continuation.MustGet("includeFinalAssistant") != true {
		t.Fatalf("continuation = %v", continuation)
	}
	if disposition.Settled.TriggerEntryID != "resp-1" {
		t.Fatal("trigger")
	}
}

func TestClassifyCancelledResponse(t *testing.T) {
	response := jsonx.ObjFrom("stopReason", "stop", "content", []any{})
	disposition := ClassifyCancelledResponse("assistant", response, settleScope(), "resp-1")
	if disposition.Settled == nil || disposition.Settled.At != session.AtCheckpoint {
		t.Fatalf("disposition = %+v", disposition.Settled)
	}
	if disposition.Committed.MustGet("stopReason") != "aborted" {
		t.Fatal("committed not aborted")
	}
	if disposition.Committed.MustGet("errorMessage") != "Assistant request was cancelled" {
		t.Fatalf("message = %v", disposition.Committed.MustGet("errorMessage"))
	}
}

func TestPlanResponseWrites(t *testing.T) {
	sess := session.NewStorageBackedSession(session.SessionMetadata{ID: "s1", StorageVersion: 1}, session.NewMemoryStorage())
	committed := jsonx.ObjFrom("role", "assistant", "content", []any{}, "usage", jsonx.ObjFrom("input", float64(5)))
	meta := &session.OperationMeta{OperationID: "op1", Lane: "main", StartedAt: 1}
	// Non-failing: entry + usage + tip + frames delete.
	plan, err := PlanResponseWrites("main", "resp-1", committed, "usage-1", "op1", settleScope(), sess, harnessBackground(), nil, meta, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Failed || len(plan.Writes) != 4 {
		t.Fatalf("plan = %+v writes = %d", plan, len(plan.Writes))
	}
	// Failing: cleanup writes instead of the frames delete + record.
	failure := &session.OperationError{Code: "assistant_error", Message: "boom"}
	plan, err = PlanResponseWrites("main", "resp-1", committed, "usage-1", "op1", settleScope(), sess, harnessBackground(), failure, meta, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Failed || plan.Record == nil || plan.Record.Status != session.StatusFailed {
		t.Fatalf("plan = %+v", plan)
	}
}
