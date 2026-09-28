package runtime

// Ports of requestAbort behaviors.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func abortLane(t *testing.T) (*Lane, session.Session) {
	t.Helper()
	lane, sess := newAcceptLane(t)
	ctx := harnessBackground()
	// Install a running operation durably + in memory.
	lane.State().Operation = &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-1", Lane: "main", Intent: jsonx.ObjFrom("kind", "run", "promptEntryIds", []any{})},
		State: session.OperationState{At: session.AtStarting, Control: session.Control{Status: "running"}},
	}
	if err := sess.SetValue(session.OperationMetaValue("op-1"), jsonx.ObjFrom(
		"operationId", "op-1", "lane", "main", "sourceTipId", nil, "startedAt", float64(1),
		"intent", jsonx.ObjFrom("kind", "run", "promptEntryIds", []any{}),
	), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.OperationStateValue("op-1"), jsonx.ObjFrom(
		"at", session.AtStarting, "control", jsonx.ObjFrom("status", "running"),
	), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.LaneStateValue("main"), DurableLaneStateJSON(strptrAR("op-1"), nil, nil), ctx); err != nil {
		t.Fatal(err)
	}
	return lane, sess
}

func strptrAR(s string) *string { return &s }

func TestRequestAbortMismatch(t *testing.T) {
	lane, _ := abortLane(t)
	outcome, err := RequestAbort(lane, "wrong-op", harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Mismatch == nil || outcome.Mismatch.Active != "op-1" {
		t.Fatalf("outcome = %+v", outcome)
	}
	// No operation at all reports the last id.
	lane.State().Operation = nil
	last := "op-old"
	lane.State().LastOperationID = &last
	outcome, err = RequestAbort(lane, "wrong-op", harnessBackground())
	if err != nil || outcome.Mismatch == nil || outcome.Mismatch.Last != "op-old" {
		t.Fatalf("outcome = %+v err = %v", outcome, err)
	}
}

func TestRequestAbortIdempotent(t *testing.T) {
	lane, _ := abortLane(t)
	lane.State().Operation.State.Control.Status = "cancel_requested"
	outcome, err := RequestAbort(lane, "op-1", harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if outcome.NewlyRequested || outcome.Mismatch != nil {
		t.Fatalf("outcome = %+v", outcome)
	}
	// No state change, no events.
	if len(lane.DrainEvents()) != 0 {
		t.Fatal("events emitted on idempotent abort")
	}
}

func TestRequestAbortReturnsQueued(t *testing.T) {
	lane, sess := abortLane(t)
	ctx := harnessBackground()
	// Queue a steer and a followUp.
	if err := sess.SetValue(session.PendingEntryValue("s-1"), jsonx.ObjFrom(
		"type", "message", "payload", jsonx.ObjFrom("role", "user", "content", "steer me"),
	), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.PendingEntryValue("f-1"), jsonx.ObjFrom(
		"type", "message", "payload", jsonx.ObjFrom("role", "user", "content", "follow up"),
	), ctx); err != nil {
		t.Fatal(err)
	}
	// A write stays.
	if err := sess.SetValue(session.PendingEntryValue("w-1"), jsonx.ObjFrom(
		"type", "custom", "customType", "file.write",
	), ctx); err != nil {
		t.Fatal(err)
	}
	lane.State().Inbox = []session.InboxItem{
		{EntryID: "s-1", Kind: "steer"},
		{EntryID: "w-1", Kind: "write"},
		{EntryID: "f-1", Kind: "followUp"},
	}
	if err := sess.SetValue(session.LaneStateValue("main"), DurableLaneStateJSON(strptrAR("op-1"), nil, lane.State().Inbox), ctx); err != nil {
		t.Fatal(err)
	}

	outcome, err := RequestAbort(lane, "op-1", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.NewlyRequested {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(outcome.Steer) != 1 || outcome.Steer[0].MustGet("content") != "steer me" {
		t.Fatalf("steer = %v", outcome.Steer)
	}
	if len(outcome.FollowUp) != 1 || outcome.FollowUp[0].MustGet("content") != "follow up" {
		t.Fatalf("followUp = %v", outcome.FollowUp)
	}
	// The write stays in the inbox; steer/followUp pruned.
	if len(lane.State().Inbox) != 1 || lane.State().Inbox[0].EntryID != "w-1" {
		t.Fatalf("inbox = %+v", lane.State().Inbox)
	}
	// The control flipped.
	if lane.State().Operation.State.Control.Status != "cancel_requested" {
		t.Fatal("control not flipped")
	}
	// Pruned payloads deleted; the write payload stays.
	if stored, _ := sess.GetValue(session.PendingEntryValue("s-1"), ctx); stored != nil {
		t.Fatal("steer payload survived")
	}
	if stored, _ := sess.GetValue(session.PendingEntryValue("f-1"), ctx); stored != nil {
		t.Fatal("followUp payload survived")
	}
	if stored, _ := sess.GetValue(session.PendingEntryValue("w-1"), ctx); stored == nil {
		t.Fatal("write payload deleted")
	}
	// Durable op.state carries cancel_requested.
	opState, _ := sess.GetValue(session.OperationStateValue("op-1"), ctx)
	control := opState.Value.(*jsonx.Obj).MustGet("control").(*jsonx.Obj)
	if control.MustGet("status") != "cancel_requested" {
		t.Fatalf("control = %v", control)
	}
}

func TestRequestAbortMissingMessageInvariant(t *testing.T) {
	lane, _ := abortLane(t)
	lane.State().Inbox = []session.InboxItem{{EntryID: "dangling", Kind: "steer"}}
	_, err := RequestAbort(lane, "op-1", harnessBackground())
	if err == nil || !strings.Contains(err.Error(), "missing its message") {
		t.Fatalf("err = %v", err)
	}
}
