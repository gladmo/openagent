package runtime

// Ports of drive/tool-placement.ts completion routing.

import (
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func TestPlanCompletionComplete(t *testing.T) {
	sess := session.NewStorageBackedSession(session.SessionMetadata{ID: "s1", StorageVersion: 1}, session.NewMemoryStorage())
	ctx := harnessBackground()
	// Seed tool args under the operation prefix.
	if err := sess.SetValue(session.OperationToolArgs("op1", "t1", 0), jsonx.MustParseString(`{"a":1}`), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.OperationToolArgs("op1", "t1", 1), jsonx.MustParseString(`{"b":2}`), ctx); err != nil {
		t.Fatal(err)
	}

	// All calls completed via the plan.
	batch := jsonx.ObjFrom("turnId", "t1", "calls", []any{
		jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "outcome_ready"),
	})
	items := []PlacementItem{
		{Call: jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "outcome_ready"), Message: jsonx.NewObj()},
	}
	completedBatch := markCallsCompleted(batch, items)
	scope := &session.OperationState{Control: session.Control{Status: "running"}}
	plan, err := PlanCompletion("main", "op1", batch, completedBatch, items, scope, sess, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Complete || plan.NextBatch != nil {
		t.Fatalf("plan = %+v", plan)
	}
	// need_assistant continuation (not all terminate).
	if plan.NextCheckpoint.At != session.AtCheckpoint {
		t.Fatalf("at = %s", plan.NextCheckpoint.At)
	}
	continuation := plan.NextCheckpoint.Continuation
	if continuation.MustGet("kind") != "need_assistant" {
		t.Fatalf("continuation = %v", continuation)
	}
	if plan.NextCheckpoint.TriggerEntryID != "r1" {
		t.Fatalf("trigger = %s", plan.NextCheckpoint.TriggerEntryID)
	}
	// Tool-args cleanup covers both prefix rows.
	if len(plan.CleanupArgs) != 2 {
		t.Fatalf("cleanup = %d", len(plan.CleanupArgs))
	}
	// Tip write targets the last placed entry.
	if plan.TipWrite.Value != "r1" {
		t.Fatalf("tip = %v", plan.TipWrite.Value)
	}
}

func TestPlanCompletionAllTerminateMayFinish(t *testing.T) {
	sess := session.NewMemoryStorage()
	batch := jsonx.ObjFrom("turnId", "t1", "calls", []any{
		jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "outcome_ready", "terminate", true),
		jsonx.ObjFrom("sourceIndex", float64(3), "resultEntryId", "r2", "status", "completed", "terminate", true),
	})
	items := []PlacementItem{
		{Call: jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "outcome_ready", "terminate", true), Message: jsonx.NewObj()},
	}
	completedBatch := markCallsCompleted(batch, items)
	scope := &session.OperationState{Control: session.Control{Status: "running"}}
	plan, err := PlanCompletion("main", "op1", batch, completedBatch, items, scope, sess, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Complete {
		t.Fatal("not complete")
	}
	if plan.NextCheckpoint.Continuation.MustGet("kind") != "may_finish" {
		t.Fatalf("continuation = %v", plan.NextCheckpoint.Continuation)
	}
}

func TestPlanCompletionIncompleteSwapsBatch(t *testing.T) {
	sess := session.NewMemoryStorage()
	batch := placementBatch() // one ready + one planned
	items := []PlacementItem{
		{Call: jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "outcome_ready"), Message: jsonx.NewObj()},
	}
	completedBatch := markCallsCompleted(batch, items)
	scope := &session.OperationState{Control: session.Control{Status: "running"}}
	plan, err := PlanCompletion("main", "op1", batch, completedBatch, items, scope, sess, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Complete || plan.NextCheckpoint != nil {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.NextBatch.At != session.AtTools || plan.NextBatch.Batch == nil {
		t.Fatal("batch swap missing")
	}
	calls := plan.NextBatch.Batch.MustGet("calls").([]any)
	if calls[0].(*jsonx.Obj).MustGet("status") != "completed" || calls[1].(*jsonx.Obj).MustGet("status") != "planned" {
		t.Fatal("batch states wrong")
	}
}

func TestRenderPlacementEvents(t *testing.T) {
	tip := "tip-1"
	message := jsonx.ObjFrom("role", "toolResult", "toolCallId", "c0", "toolName", "read", "content", []any{}, "usage", jsonx.ObjFrom("input", float64(10)))
	items := []PlacementItem{
		{Call: jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "outcome_ready"), Message: message},
	}
	plan := PlanPlacementWrites(placementBatch(), items, &tip, func() string { return "u1" })
	commit := session.CommitResult{
		FirstSeq:  1,
		Seqs:      []int64{1, 2, 3},
		Timestamp: 500,
		Stats:     session.SessionStats{Usage: aiUsageRuntimeOf(10, 5)},
	}
	events := RenderPlacementEvents("main", plan, commit)
	// entry_added + usage.
	if len(events) != 2 {
		t.Fatalf("events = %d", len(events))
	}
	if EventType(events[0]) != "entry_added" {
		t.Fatal("first event not entry_added")
	}
	entry := events[0].MustGet("entry").(*jsonx.Obj)
	if entry.MustGet("id") != "r1" || entry.MustGet("seq") != float64(1) || entry.MustGet("timestamp") != float64(500) {
		t.Fatalf("entry = %v", entry)
	}
	if EventType(events[1]) != "usage" {
		t.Fatal("second event not usage")
	}
	row := events[1].MustGet("row").(*jsonx.Obj)
	if row.MustGet("id") != "u1" || row.MustGet("seq") != float64(3) {
		t.Fatalf("row = %v", row)
	}
	totals := events[1].MustGet("totals").(*jsonx.Obj)
	if totals.MustGet("input") != float64(10) || totals.MustGet("output") != float64(5) {
		t.Fatalf("totals = %v", totals)
	}
}

func aiUsageRuntimeOf(in, out float64) aiUsageRuntime {
	return aiUsageRuntime{Input: in, Output: out, TotalTokens: in + out}
}
