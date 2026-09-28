package runtime

// Ports of acceptRun decision behaviors.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func newAcceptLane(t *testing.T) (*Lane, session.Session) {
	t.Helper()
	sess := session.NewStorageBackedSession(session.SessionMetadata{ID: "s1", StorageVersion: 1}, session.NewMemoryStorage())
	tip := "root"
	if err := sess.Mutate(func(mutator session.SessionMutator, ctx sessionCtxAlias) error {
		_, err := mutator.Commit([]session.Write{session.InsertEntry(&session.Entry{
			EntryBase: session.EntryBase{ID: tip, Type: session.EntryTypeMessage},
			Message:   session.AgentMessagePayload{Role: "user"},
		})}, ctx)
		return err
	}, harnessBackground()); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.LaneStateValue("main"), DurableLaneStateJSON(nil, nil, nil), harnessBackground()); err != nil {
		t.Fatal(err)
	}
	lane := NewLane(LaneOptions{
		Name:    "main",
		Session: sess,
		State:   &RuntimeLaneState{TipID: &tip},
	})
	return lane, sess
}

func acceptNextID(ts ...float64) func(float64) string {
	counter := 0
	return func(float64) string {
		counter++
		return ai.UUIDv7()
	}
}

func TestAcceptRunAdmits(t *testing.T) {
	lane, sess := newAcceptLane(t)
	prompt := AcceptPrompt{ID: "p-1", Message: jsonx.ObjFrom("role", "user", "content", "hi", "timestamp", float64(1))}
	result, err := AcceptRun(lane, []AcceptPrompt{prompt}, "all", "all", acceptNextID(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Admitted || result.OperationID == "" {
		t.Fatalf("result = %+v", result)
	}
	// The lane carries the installed starting operation.
	op := lane.State().Operation
	if op == nil || op.State.At != session.AtStarting {
		t.Fatalf("op = %+v", op)
	}
	if op.Meta.OperationID != result.OperationID {
		t.Fatal("operation id mismatch")
	}
	// Durable values landed.
	ctx := harnessBackground()
	meta, _ := sess.GetValue(session.OperationMetaValue(result.OperationID), ctx)
	if meta == nil {
		t.Fatal("op.meta missing")
	}
	intent := meta.Value.(*jsonx.Obj).MustGet("intent").(*jsonx.Obj)
	ids := intent.MustGet("promptEntryIds").([]any)
	if len(ids) != 1 || ids[0] != "p-1" {
		t.Fatalf("prompt ids = %v", ids)
	}
	laneState, _ := sess.GetValue(session.LaneStateValue("main"), ctx)
	if laneState == nil {
		t.Fatal("lane.state missing")
	}
	// The prompt entry landed with the tip as parent.
	entry, _ := sess.GetEntry("p-1", ctx)
	if entry == nil || entry.ParentID == nil || *entry.ParentID != "root" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestAcceptRunBusy(t *testing.T) {
	lane, _ := newAcceptLane(t)
	// Install an operation directly on the state.
	lane.State().Operation = &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-old", Intent: jsonx.ObjFrom("kind", "run")},
		State: session.OperationState{At: session.AtStarting},
	}
	result, err := AcceptRun(lane, []AcceptPrompt{{ID: "p-2", Message: jsonx.ObjFrom("role", "user")}}, "all", "all", acceptNextID(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if result.Busy == nil || result.Busy.OperationID != "op-old" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Busy.Error(), "already has an active operation") {
		t.Fatalf("busy = %v", result.Busy)
	}
}

func TestAcceptRunEmptyRequest(t *testing.T) {
	lane, _ := newAcceptLane(t)
	result, err := AcceptRun(lane, nil, "all", "all", acceptNextID(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if result.Invalid == nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestAcceptRunCapturesInbox(t *testing.T) {
	lane, sess := newAcceptLane(t)
	ctx := harnessBackground()
	// Seed a steer in the pending entries + inbox.
	if err := sess.SetValue(session.PendingEntryValue("q-1"), jsonx.ObjFrom(
		"type", "message",
		"payload", jsonx.ObjFrom("role", "user", "content", "steer", "timestamp", float64(2)),
	), ctx); err != nil {
		t.Fatal(err)
	}
	lane.State().Inbox = []session.InboxItem{{EntryID: "q-1", Kind: "steer"}}
	if err := sess.SetValue(session.LaneStateValue("main"), DurableLaneStateJSON(nil, nil, lane.State().Inbox), ctx); err != nil {
		t.Fatal(err)
	}

	result, err := AcceptRun(lane, []AcceptPrompt{{ID: "p-3", Message: jsonx.ObjFrom("role", "user")}}, "all", "all", acceptNextID(), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Admitted {
		t.Fatalf("result = %+v", result)
	}
	// The intent carries prompt + captured entry ids.
	meta, _ := sess.GetValue(session.OperationMetaValue(result.OperationID), ctx)
	ids := meta.Value.(*jsonx.Obj).MustGet("intent").(*jsonx.Obj).MustGet("promptEntryIds").([]any)
	if len(ids) != 2 || ids[0] != "p-3" || ids[1] != "q-1" {
		t.Fatalf("ids = %v", ids)
	}
	// The inbox drained.
	if len(lane.State().Inbox) != 0 {
		t.Fatalf("inbox = %+v", lane.State().Inbox)
	}
}

func TestAcceptRunPendingAssistantRejected(t *testing.T) {
	lane, sess := newAcceptLane(t)
	ctx := harnessBackground()
	if err := sess.SetValue(session.PendingEntryValue("bad-1"), jsonx.ObjFrom(
		"type", "message",
		"payload", jsonx.ObjFrom("role", "assistant", "stopReason", "pending", "content", []any{}),
	), ctx); err != nil {
		t.Fatal(err)
	}
	lane.State().Inbox = []session.InboxItem{{EntryID: "bad-1", Kind: "steer"}}
	if err := sess.SetValue(session.LaneStateValue("main"), DurableLaneStateJSON(nil, nil, lane.State().Inbox), ctx); err != nil {
		t.Fatal(err)
	}
	_, err := AcceptRun(lane, []AcceptPrompt{{ID: "p-4", Message: jsonx.ObjFrom("role", "user")}}, "all", "all", acceptNextID(), ctx)
	if err == nil || !strings.Contains(err.Error(), "contains a pending assistant") {
		t.Fatalf("err = %v", err)
	}
}

func TestAcceptRunMissingPayload(t *testing.T) {
	lane, sess := newAcceptLane(t)
	ctx := harnessBackground()
	lane.State().Inbox = []session.InboxItem{{EntryID: "ghost", Kind: "steer"}}
	if err := sess.SetValue(session.LaneStateValue("main"), DurableLaneStateJSON(nil, nil, lane.State().Inbox), ctx); err != nil {
		t.Fatal(err)
	}
	_, err := AcceptRun(lane, []AcceptPrompt{{ID: "p-5", Message: jsonx.ObjFrom("role", "user")}}, "all", "all", acceptNextID(), ctx)
	if err == nil || !strings.Contains(err.Error(), "missing its payload") {
		t.Fatalf("err = %v", err)
	}
}
