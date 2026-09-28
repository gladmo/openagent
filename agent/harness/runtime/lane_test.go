package runtime

// Ports of lane.test.ts behaviors: inbox selection fairness, command
// commit/return/reject, settleOperation finish, continueOperation
// cancel_requested short-circuit.

import (
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func inboxOf(kinds ...string) []session.InboxItem {
	out := make([]session.InboxItem, 0, len(kinds))
	for i, kind := range kinds {
		out = append(out, session.InboxItem{EntryID: "e" + string(rune('0'+i)), Kind: kind})
	}
	return out
}

func TestSelectAcceptedInboxWritesAndNextRunAlwaysTaken(t *testing.T) {
	selected, remainder := SelectAcceptedInbox(
		inboxOf("write", "nextRun", "steer", "followUp"), "one-at-a-time", "one-at-a-time")
	if len(remainder) != 0 {
		t.Fatalf("remainder = %v", remainder)
	}
	if len(selected) != 4 {
		t.Fatalf("selected = %v", selected)
	}
}

func TestSelectAcceptedInboxOneAtATimeFairness(t *testing.T) {
	// Two steers + two followUps under one-at-a-time: first of each taken.
	selected, remainder := SelectAcceptedInbox(
		inboxOf("steer", "steer", "followUp", "followUp"), "one-at-a-time", "one-at-a-time")
	if len(selected) != 2 || selected[0].Kind != "steer" || selected[1].Kind != "followUp" {
		t.Fatalf("selected = %v", selected)
	}
	if len(remainder) != 2 || remainder[0].Kind != "steer" || remainder[1].Kind != "followUp" {
		t.Fatalf("remainder = %v", remainder)
	}
}

func TestSelectAcceptedInboxAllMode(t *testing.T) {
	selected, remainder := SelectAcceptedInbox(
		inboxOf("steer", "steer", "steer"), "all", "all")
	if len(selected) != 3 || len(remainder) != 0 {
		t.Fatalf("selected = %v remainder = %v", selected, remainder)
	}
}

func newLaneForTest(t *testing.T) (*Lane, session.Session) {
	t.Helper()
	storage := session.NewMemoryStorage()
	metadata := session.SessionMetadata{ID: "s1", StorageVersion: 1}
	sess := session.NewStorageBackedSession(metadata, storage)
	tip := "tip-1"
	lane := NewLane(LaneOptions{
		Name:    "main",
		Session: sess,
		State: &RuntimeLaneState{
			TipID:         &tip,
			Configuration: session.LaneConfiguration{ThinkingLevel: "off"},
		},
	})
	return lane, sess
}

func TestLaneCommandCommitWritesAndSwapsState(t *testing.T) {
	lane, sess := newLaneForTest(t)
	ctx := harnessBackground()
	if err := sess.SetValue(session.LaneStateValue("main"), jsonx.MustParseString(`{"currentOperationId":null,"lastOperationId":null,"inbox":[]}`), ctx); err != nil {
		t.Fatal(err)
	}
	result, err := lane.Command(func(state *RuntimeLaneState, reader session.SessionReader) *LaneCommand {
		newTip := "tip-2"
		next := *state
		next.TipID = &newTip
		return &LaneCommand{
			Kind:   LaneCommandCommit,
			Writes: []session.Write{session.WriteFromValue(session.SetValue(session.LaneStateValue("main"), jsonx.MustParseString(`{"currentOperationId":null,"lastOperationId":null,"inbox":[]}`)))},
			Next:   &next,
			Result: nil,
			Materialize: func(commit session.CommitResult) {
				if commit.FirstSeq < 1 {
					t.Error("materialize saw no commit")
				}
			},
		}
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = result
	if lane.State().TipID == nil || *lane.State().TipID != "tip-2" {
		t.Fatalf("tip = %v", lane.State().TipID)
	}
}

func TestLaneCommandReturnAndReject(t *testing.T) {
	lane, _ := newLaneForTest(t)
	ctx := harnessBackground()
	result, err := lane.Command(func(*RuntimeLaneState, session.SessionReader) *LaneCommand {
		return &LaneCommand{Kind: LaneCommandReturn, Result: "ok"}
	}, ctx)
	if err != nil || result != "ok" {
		t.Fatalf("result = %v err = %v", result, err)
	}
	if _, err := lane.Command(func(*RuntimeLaneState, session.SessionReader) *LaneCommand {
		return &LaneCommand{Kind: LaneCommandReject, Error: errTestLane("rejected")}
	}, ctx); err == nil || err.Error() != "rejected" {
		t.Fatalf("err = %v", err)
	}
}

func errTestLane(msg string) error { return &laneTestError{msg} }

type laneTestError struct{ msg string }

func (e *laneTestError) Error() string { return e.msg }

func TestLaneSettleFinishClearsOperation(t *testing.T) {
	lane, sess := newLaneForTest(t)
	ctx := harnessBackground()
	// Establish an active operation in durable + memory state.
	operation := &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-1", Lane: "main", StartedAt: 1},
		State: session.OperationState{At: session.AtStarting, Control: session.Control{Status: "running"}},
	}
	state := lane.State()
	state.Operation = operation
	if err := sess.SetValue(session.OperationMetaValue("op-1"), jsonx.MustParseString(`{"operationId":"op-1","lane":"main","sourceTipId":null,"startedAt":1,"intent":{"kind":"run","promptEntryIds":[]}}`), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.OperationStateValue("op-1"), jsonx.MustParseString(`{"at":"starting","control":{"status":"running"}}`), ctx); err != nil {
		t.Fatal(err)
	}

	_, err := lane.SettleOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		return &OperationCommand{
			Kind:   OperationCommandFinish,
			Record: &session.OperationResultRecord{OperationID: "op-1", Kind: "run", Status: "completed", StartedAt: 1, EndedAt: 2},
		}
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lane.State().Operation != nil {
		t.Fatal("operation not cleared")
	}
	if lane.State().LastOperationID == nil || *lane.State().LastOperationID != "op-1" {
		t.Fatalf("lastOperationId = %v", lane.State().LastOperationID)
	}
	// The result record landed in durable storage.
	stored, err := sess.GetValue(session.OperationResult("op-1"), ctx)
	if err != nil || stored == nil {
		t.Fatalf("result stored = %v err = %v", stored, err)
	}
	if obj, ok := stored.Value.(*jsonx.Obj); !ok || stringOf(obj, "status") != "completed" {
		t.Fatalf("record = %v", stored.Value)
	}
}

func TestLaneContinueOperationCancelRequestedShortCircuits(t *testing.T) {
	lane, sess := newLaneForTest(t)
	ctx := harnessBackground()
	operation := &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-2", Lane: "main", StartedAt: 1},
		State: session.OperationState{At: session.AtStarting, Control: session.Control{Status: "cancel_requested"}},
	}
	lane.State().Operation = operation
	if err := sess.SetValue(session.OperationMetaValue("op-2"), jsonx.MustParseString(`{"operationId":"op-2","lane":"main","sourceTipId":null,"startedAt":1,"intent":{"kind":"run","promptEntryIds":[]}}`), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.OperationStateValue("op-2"), jsonx.MustParseString(`{"at":"starting","control":{"status":"cancel_requested"}}`), ctx); err != nil {
		t.Fatal(err)
	}

	plannerRan := false
	result, err := lane.ContinueOperation(func(*RuntimeLaneState, *session.OperationState, *session.OperationMeta, session.SessionReader) *OperationCommand {
		plannerRan = true
		return &OperationCommand{Kind: OperationCommandReturn, Result: "unused"}
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "cancel_requested" {
		t.Fatalf("result = %+v", result)
	}
	if plannerRan {
		t.Fatal("planner ran after cancel_requested")
	}
}

func TestLaneContinueOperationResultPassesThrough(t *testing.T) {
	lane, sess := newLaneForTest(t)
	ctx := harnessBackground()
	operation := &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-3", Lane: "main", StartedAt: 1},
		State: session.OperationState{At: session.AtStarting, Control: session.Control{Status: "running"}},
	}
	lane.State().Operation = operation
	if err := sess.SetValue(session.OperationMetaValue("op-3"), jsonx.MustParseString(`{"operationId":"op-3","lane":"main","sourceTipId":null,"startedAt":1,"intent":{"kind":"run","promptEntryIds":[]}}`), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.OperationStateValue("op-3"), jsonx.MustParseString(`{"at":"starting","control":{"status":"running"}}`), ctx); err != nil {
		t.Fatal(err)
	}

	result, err := lane.ContinueOperation(func(*RuntimeLaneState, *session.OperationState, *session.OperationMeta, session.SessionReader) *OperationCommand {
		return &OperationCommand{Kind: OperationCommandReturn, Result: 42}
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "result" || result.Value != 42 {
		t.Fatalf("result = %+v", result)
	}
}
