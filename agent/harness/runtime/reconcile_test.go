package runtime

// Ports of drive/reconcile.ts behaviors.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func newReconcileEnv(t *testing.T, at string, intentKindOf string) (*Lane, session.Session, *Drive) {
	t.Helper()
	storage := session.NewMemoryStorage()
	metadata := session.SessionMetadata{ID: "s1", StorageVersion: 1}
	sess := session.NewStorageBackedSession(metadata, storage)
	tip := "tip-root"
	// Root entry for parent validation.
	rootEntry := &session.Entry{
		EntryBase: session.EntryBase{ID: tip, Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "user"},
	}
	if err := sess.Mutate(func(mutator session.SessionMutator, ctx contextContextAlias) error {
		_, err := mutator.Commit([]session.Write{session.InsertEntry(rootEntry)}, ctx)
		return err
	}, harnessBackground()); err != nil {
		t.Fatal(err)
	}
	intent := jsonx.ObjFrom("kind", intentKindOf, "promptEntryIds", []any{})
	operation := &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-1", Lane: "main", StartedAt: 1, Intent: intent},
		State: session.OperationState{At: at, Control: session.Control{Status: "cancel_requested"}},
	}
	sourceTip := "tip-root"
	operation.Meta.SourceTipID = &sourceTip
	lane := NewLane(LaneOptions{
		Name:    "main",
		Session: sess,
		State: &RuntimeLaneState{
			TipID:     &tip,
			Operation: operation,
		},
	})
	ctx := harnessBackground()
	if err := sess.SetValue(session.OperationMetaValue("op-1"), jsonx.ObjFrom(
		"operationId", "op-1", "lane", "main", "sourceTipId", sourceTip,
		"startedAt", float64(1), "intent", intent,
	), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.OperationStateValue("op-1"), operationStateToJSON(&operation.State), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.BranchTip("main"), tip, ctx); err != nil {
		t.Fatal(err)
	}
	drive := &Drive{OperationID: "op-1", Context: ctx}
	return lane, sess, drive
}

func TestReconcileRunPublishesAborted(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtStarting, "run")
	result, err := ReconcileOperation(lane, drive)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "settled" {
		t.Fatalf("result = %+v", result)
	}
	record := result.Outcome.(*session.OperationResultRecord)
	if record.Status != session.StatusAborted || record.Kind != "run" {
		t.Fatalf("record = %+v", record)
	}
	if lane.State().Operation != nil {
		t.Fatal("operation not cleared")
	}
	// run_end aborted event collected.
	events := lane.DrainEvents()
	found := false
	for _, event := range events {
		if EventType(event) == "run_end" && event.MustGet("status") == "aborted" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %v", events)
	}
}

func TestReconcileCompactionAndNavigation(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtSummaryDeciding, "compaction")
	if _, err := ReconcileOperation(lane, drive); err != nil {
		t.Fatal(err)
	}
	events := lane.DrainEvents()
	found := false
	for _, event := range events {
		if EventType(event) == "compaction_end" && event.MustGet("reason") == "manual" {
			found = true
		}
	}
	if !found {
		t.Fatal("compaction_end manual missing")
	}

	lane, _, drive = newReconcileEnv(t, session.AtNavigationReadyToCommit, "navigation")
	if _, err := ReconcileOperation(lane, drive); err != nil {
		t.Fatal(err)
	}
	events = lane.DrainEvents()
	found = false
	for _, event := range events {
		if EventType(event) == "navigation_end" && event.MustGet("status") == "aborted" {
			found = true
		}
	}
	if !found {
		t.Fatal("navigation_end missing")
	}
}

func TestReconcileCancelledSummaryBoundaryInvariant(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtSummaryDeciding, "run")
	// Invalid boundary (finish, not resume_checkpoint).
	lane.State().Operation.State.Task = jsonx.ObjFrom("boundary", jsonx.ObjFrom("kind", "finish"))
	if _, err := ReconcileOperation(lane, drive); err == nil || !strings.Contains(err.Error(), "invalid result boundary") {
		t.Fatalf("err = %v", err)
	}
	// Valid boundary with a reason reconciles.
	lane.State().Operation.State.Task = jsonx.ObjFrom(
		"reason", "threshold",
		"boundary", jsonx.ObjFrom("kind", "resume_checkpoint"),
	)
	if _, err := ReconcileOperation(lane, drive); err != nil {
		t.Fatal(err)
	}
	events := lane.DrainEvents()
	found := false
	for _, event := range events {
		if EventType(event) == "compaction_end" && event.MustGet("reason") == "threshold" {
			found = true
		}
	}
	if !found {
		t.Fatal("summary compaction_end missing")
	}
}

func TestReconcileRequiresCancelRequested(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtStarting, "run")
	lane.State().Operation.State.Control.Status = "running"
	if _, err := ReconcileOperation(lane, drive); err == nil || !strings.Contains(err.Error(), "not cancelled") {
		t.Fatalf("err = %v", err)
	}
}

func TestReconcileOperationMismatch(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtStarting, "run")
	drive.OperationID = "other"
	if _, err := ReconcileOperation(lane, drive); err == nil || !strings.Contains(err.Error(), "no matching operation") {
		t.Fatalf("err = %v", err)
	}
}

func TestReconcileEffectLeavesDefaultToAborted(t *testing.T) {
	for _, at := range []string{session.AtAssistantEffectPending, session.AtTools, session.AtDeferredSuspended, session.AtDeferredEffectPending} {
		lane, _, drive := newReconcileEnv(t, at, "run")
		result, err := ReconcileOperation(lane, drive)
		if err != nil {
			t.Fatal(err)
		}
		if result.Kind != "settled" {
			t.Fatalf("[%s] result = %+v", at, result)
		}
		if lane.State().Operation != nil {
			t.Fatalf("[%s] operation not cleared", at)
		}
	}
}
