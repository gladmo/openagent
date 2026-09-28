package runtime

// Ports of captureLaneSnapshot behaviors.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func watchLane(t *testing.T) (*Lane, session.Session) {
	t.Helper()
	lane, sess := newAcceptLane(t)
	ctx := harnessBackground()
	// Chain: root -> e1 -> e2.
	for _, entry := range []*session.Entry{
		{EntryBase: session.EntryBase{ID: "e1", ParentID: strptrW("root"), Type: session.EntryTypeMessage}, Message: session.AgentMessagePayload{Role: "user"}},
		{EntryBase: session.EntryBase{ID: "e2", ParentID: strptrW("e1"), Type: session.EntryTypeMessage}, Message: session.AgentMessagePayload{Role: "assistant"}},
	} {
		if err := sess.Mutate(func(mutator session.SessionMutator, ctx sessionCtxAlias) error {
			_, err := mutator.Commit([]session.Write{session.InsertEntry(entry)}, ctx)
			return err
		}, ctx); err != nil {
			t.Fatal(err)
		}
	}
	lane.State().TipID = strptrW("e2")
	if err := sess.SetValue(session.BranchTip("main"), "e2", ctx); err != nil {
		t.Fatal(err)
	}
	return lane, sess
}

func strptrW(s string) *string { return &s }

func TestCaptureLaneSnapshotTranscript(t *testing.T) {
	lane, sess := watchLane(t)
	snapshot, err := CaptureLaneSnapshot(lane, sess, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	// Oldest-first bounded transcript (root through tip).
	if len(snapshot.Transcript) != 3 || snapshot.Transcript[0].ID != "root" || snapshot.Transcript[2].ID != "e2" {
		ids := []string{}
		for _, e := range snapshot.Transcript {
			ids = append(ids, e.ID)
		}
		t.Fatalf("transcript = %v", ids)
	}
	if snapshot.TipID == nil || *snapshot.TipID != "e2" {
		t.Fatal("tip")
	}
	// Stats carry the message count.
	if snapshot.Stats.MessageCount != 3 { // root + e1 + e2
		t.Fatalf("stats = %+v", snapshot.Stats)
	}
}

func TestCaptureLaneSnapshotEmptyTip(t *testing.T) {
	lane, sess := watchLane(t)
	lane.State().TipID = nil
	// Clear the durable tip too: the capture prefers pi.branch.tip.
	if err := sess.SetValue(session.BranchTip("main"), nil, harnessBackground()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CaptureLaneSnapshot(lane, sess, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Transcript) != 0 {
		t.Fatalf("transcript = %d", len(snapshot.Transcript))
	}
}

func TestCaptureLaneSnapshotLastResult(t *testing.T) {
	lane, sess := watchLane(t)
	ctx := harnessBackground()
	// Seed a result + point the lane at it.
	if err := sess.SetValue(session.OperationResult("op-done"), jsonx.ObjFrom(
		"operationId", "op-done", "kind", "run", "status", "completed",
		"fromTipId", "root", "tipId", "e2", "startedAt", float64(1), "endedAt", float64(2),
	), ctx); err != nil {
		t.Fatal(err)
	}
	lane.State().LastOperationID = strptrW("op-done")
	snapshot, err := CaptureLaneSnapshot(lane, sess, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastResult == nil || snapshot.LastResult.Status != "completed" {
		t.Fatalf("lastResult = %+v", snapshot.LastResult)
	}
	if snapshot.LastResult.TipID == nil || *snapshot.LastResult.TipID != "e2" {
		t.Fatal("result tip")
	}

	// A missing result is an invariant.
	lane.State().LastOperationID = strptrW("ghost")
	if _, err := CaptureLaneSnapshot(lane, sess, ctx); err == nil || !strings.Contains(err.Error(), "missing result") {
		t.Fatalf("err = %v", err)
	}
}

func TestCaptureLaneSnapshotOperation(t *testing.T) {
	lane, sess := watchLane(t)
	lane.State().Operation = &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-1", StartedAt: 5, Intent: jsonx.ObjFrom("kind", "run")},
		State: session.OperationState{At: session.AtStarting, Control: session.Control{Status: "running"}},
	}
	snapshot, err := CaptureLaneSnapshot(lane, sess, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	op := snapshot.Operation
	if op == nil || op.Kind != "run" || op.Status != "open" || op.StartedAt != 5 {
		t.Fatalf("op = %+v", op)
	}
	// Cancelled control -> aborting.
	lane.State().Operation.State.Control.Status = "cancel_requested"
	snapshot, _ = CaptureLaneSnapshot(lane, sess, harnessBackground())
	if snapshot.Operation.Status != "aborting" {
		t.Fatalf("status = %s", snapshot.Operation.Status)
	}
}

func TestLaneSnapshotJSON(t *testing.T) {
	lane, sess := watchLane(t)
	snapshot, err := CaptureLaneSnapshot(lane, sess, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	obj := LaneSnapshotJSON(snapshot)
	if obj.MustGet("lane") != "main" || obj.MustGet("tipId") != "e2" {
		t.Fatalf("obj = %v", obj)
	}
	transcript := obj.MustGet("transcript").([]any)
	if len(transcript) != 3 || transcript[0] != "root" {
		t.Fatalf("transcript = %v", transcript)
	}
}
