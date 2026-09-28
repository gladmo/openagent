package runtime

// Ports of drive/tool-placement.ts commit planning behaviors.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func placementEnvWithStaged(t *testing.T, toolCallID, toolName string, stage bool) (session.Session, *ToolBatchSource, []*jsonx.Obj) {
	t.Helper()
	env := newPlacementEnv(t)
	ctx := harnessBackground()
	batch := placementBatch()
	source, err := ReadToolBatchSource(env.sess, batch, ctx)
	if err != nil {
		t.Fatal(err)
	}
	ready := []*jsonx.Obj{jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "outcome_ready")}
	if stage {
		message := jsonx.ObjFrom("role", "toolResult", "toolCallId", toolCallID, "toolName", toolName, "content", []any{})
		if err := env.sess.SetValue(session.PendingEntryValue("r1"), jsonx.ObjFrom("type", "message", "payload", message), ctx); err != nil {
			t.Fatal(err)
		}
	}
	return env.sess, source, ready
}

func TestValidateStagedResults(t *testing.T) {
	// Matching staged result validates.
	sess, source, ready := placementEnvWithStaged(t, "c0", "read", true)
	items, err := ValidateStagedResults(sess, source, ready, harnessBackground())
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %d err = %v", len(items), err)
	}
	if stringOfObj(items[0].Message, "toolCallId") != "c0" {
		t.Fatal("message lost")
	}
}

func TestValidateStagedResultsMissing(t *testing.T) {
	sess, source, ready := placementEnvWithStaged(t, "c0", "read", false)
	_, err := ValidateStagedResults(sess, source, ready, harnessBackground())
	if err == nil || !strings.Contains(err.Error(), "missing its staged result") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateStagedResultsMismatch(t *testing.T) {
	// Wrong toolCallId in the staged payload.
	sess, source, ready := placementEnvWithStaged(t, "WRONG", "read", true)
	_, err := ValidateStagedResults(sess, source, ready, harnessBackground())
	if err == nil || !strings.Contains(err.Error(), "mismatched staged result") {
		t.Fatalf("err = %v", err)
	}
	// Wrong toolName.
	sess, source, ready = placementEnvWithStaged(t, "c0", "wrong-name", true)
	_, err = ValidateStagedResults(sess, source, ready, harnessBackground())
	if err == nil || !strings.Contains(err.Error(), "mismatched staged result") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanPlacementWrites(t *testing.T) {
	batch := placementBatch()
	tip := "tip-1"
	ids := []string{"u1"}
	nextID := 0
	message := jsonx.ObjFrom("role", "toolResult", "toolCallId", "c0", "toolName", "read", "content", []any{}, "usage", jsonx.ObjFrom("input", float64(10), "output", float64(5), "totalTokens", float64(15)))
	items := []PlacementItem{
		{Call: jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "outcome_ready", "terminate", true), Message: message},
	}
	plan := PlanPlacementWrites(batch, items, &tip, func() string {
		id := ids[nextID]
		nextID++
		return id
	})
	// Entry chains from the tip with terminate.
	if len(plan.Entries) != 1 {
		t.Fatalf("entries = %d", len(plan.Entries))
	}
	entry := plan.Entries[0]
	if entry.ID != "r1" || entry.ParentID == nil || *entry.ParentID != "tip-1" || !entry.Terminate {
		t.Fatalf("entry = %+v", entry)
	}
	// Writes: entry + pending delete + usage row.
	if len(plan.Writes) != 3 {
		t.Fatalf("writes = %d", len(plan.Writes))
	}
	// Usage row with entry linkage.
	if len(plan.UsageRows) != 1 || plan.UsageRows[0].ID != "u1" || plan.UsageRows[0].Usage.Input != 10 {
		t.Fatalf("usage = %+v", plan.UsageRows)
	}
	if plan.UsageRows[0].EntryID == nil || *plan.UsageRows[0].EntryID != "r1" {
		t.Fatal("usage entry linkage")
	}
	// Batch marks the placed call completed; the other stays planned.
	calls := plan.CompletedBatch.MustGet("calls").([]any)
	if calls[0].(*jsonx.Obj).MustGet("status") != "completed" {
		t.Fatal("placed call not completed")
	}
	if calls[1].(*jsonx.Obj).MustGet("status") != "planned" {
		t.Fatal("unplaced call mutated")
	}
}

func TestPlanPlacementWritesChainsAndNoUsage(t *testing.T) {
	batch := placementBatch()
	tip := "tip-1"
	// Two items without usage: entries chain r1 -> r2.
	message := jsonx.ObjFrom("role", "toolResult", "toolCallId", "c0", "toolName", "read", "content", []any{})
	items := []PlacementItem{
		{Call: jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "outcome_ready"), Message: message},
		{Call: jsonx.ObjFrom("sourceIndex", float64(3), "resultEntryId", "r2", "status", "outcome_ready"), Message: message},
	}
	plan := PlanPlacementWrites(batch, items, &tip, func() string { return "unused" })
	if len(plan.Entries) != 2 {
		t.Fatalf("entries = %d", len(plan.Entries))
	}
	if plan.Entries[1].ParentID == nil || *plan.Entries[1].ParentID != "r1" {
		t.Fatal("chain broken")
	}
	if len(plan.UsageRows) != 0 || len(plan.Writes) != 4 {
		t.Fatalf("usage = %d writes = %d", len(plan.UsageRows), len(plan.Writes))
	}
}
