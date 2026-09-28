package runtime

// Ports of steer/followUp queue + cancelQueued.

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"

	session "github.com/gladmo/openagent/agent/harness/session"
)

func queueLane(t *testing.T) *Lane {
	t.Helper()
	lane, sess := newAcceptLane(t)
	if err := sess.SetValue(session.LaneStateValue("main"), DurableLaneStateJSON(nil, nil, nil), harnessBackground()); err != nil {
		t.Fatal(err)
	}
	return lane
}

func TestQueueMessageStages(t *testing.T) {
	lane := queueLane(t)
	result, err := QueueMessage(lane, "steer", "change direction", nil, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if result.EntryID == "" {
		t.Fatal("no entry id")
	}
	// The inbox carries the queued item.
	if len(lane.State().Inbox) != 1 || lane.State().Inbox[0].Kind != "steer" {
		t.Fatalf("inbox = %+v", lane.State().Inbox)
	}
	// The pending payload landed.
	stored, _ := laneSessionValue(lane, session.PendingEntryValue(result.EntryID))
	if stored == nil {
		t.Fatal("payload missing")
	}
	// The durable lane state carries the inbox.
	state, _ := laneSessionValue(lane, session.LaneStateValue("main"))
	if state == nil {
		t.Fatal("lane state missing")
	}
	// queue_update event emitted.
	events := lane.DrainEvents()
	if len(events) != 1 || EventType(events[0]) != "queue_update" {
		t.Fatalf("events = %v", events)
	}
}

func laneSessionValue(lane *Lane, address session.Value) (*session.StoredValue, error) {
	return lane.session.GetValue(address, harnessBackground())
}

func TestQueueMessageActiveOperationKept(t *testing.T) {
	lane := queueLane(t)
	lane.State().Operation = &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-live"},
		State: session.OperationState{At: session.AtStarting},
	}
	if _, err := QueueMessage(lane, "followUp", "later", nil, harnessBackground()); err != nil {
		t.Fatal(err)
	}
	// The durable lane state keeps the current operation id.
	state, _ := laneSessionValue(lane, session.LaneStateValue("main"))
	if state == nil {
		t.Fatal("lane state missing")
	}
	obj := state.Value.(*jsonx.Obj)
	if obj.MustGet("currentOperationId") != "op-live" {
		t.Fatalf("currentOperationId = %v", obj.MustGet("currentOperationId"))
	}
}

func TestQueueMessageWithImages(t *testing.T) {
	lane := queueLane(t)
	image := map[string]any{"type": "image", "data": "x"}
	result, err := QueueMessage(lane, "steer", "look", []any{image}, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := laneSessionValue(lane, session.PendingEntryValue(result.EntryID))
	if stored == nil {
		t.Fatal("payload missing")
	}
}

func TestCancelQueuedTrichotomy(t *testing.T) {
	lane := queueLane(t)
	// Unknown id -> not_found.
	result, err := CancelQueued(lane, "ghost", harnessBackground())
	if err != nil || result.Kind != CancelNotFound {
		t.Fatalf("result = %+v err = %v", result, err)
	}
	// Queue then cancel -> cancelled.
	queued, err := QueueMessage(lane, "steer", "hi", nil, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	result, err = CancelQueued(lane, queued.EntryID, harnessBackground())
	if err != nil || result.Kind != CancelCancelled {
		t.Fatalf("result = %+v err = %v", result, err)
	}
	// The inbox drained and the payload deleted.
	if len(lane.State().Inbox) != 0 {
		t.Fatalf("inbox = %+v", lane.State().Inbox)
	}
	stored, _ := laneSessionValue(lane, session.PendingEntryValue(queued.EntryID))
	if stored != nil {
		t.Fatal("payload survived")
	}
	// Cancelling an id that became an entry -> already_consumed.
	entry := &session.Entry{
		EntryBase: session.EntryBase{ID: "consumed-1", Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "user"},
	}
	if err := lane.session.Mutate(func(mutator session.SessionMutator, ctx sessionCtxAlias) error {
		_, err := mutator.Commit([]session.Write{session.InsertEntry(entry)}, ctx)
		return err
	}, harnessBackground()); err != nil {
		t.Fatal(err)
	}
	result, err = CancelQueued(lane, "consumed-1", harnessBackground())
	if err != nil || result.Kind != CancelAlreadyConsumed {
		t.Fatalf("result = %+v err = %v", result, err)
	}
}

func TestCancelQueuedMissingPayloadInvariant(t *testing.T) {
	lane := queueLane(t)
	// Hand-install an inbox item with no payload.
	lane.State().Inbox = []session.InboxItem{{EntryID: "dangling", Kind: "steer"}}
	if err := lane.session.SetValue(session.LaneStateValue("main"), DurableLaneStateJSON(nil, nil, lane.State().Inbox), harnessBackground()); err != nil {
		t.Fatal(err)
	}
	_, err := CancelQueued(lane, "dangling", harnessBackground())
	if err == nil {
		t.Fatal("dangling payload accepted")
	}
}
