package runtime

// queue.go ports harness/runtime/lane.ts's steer/followUp/nextRun queue
// and cancelQueued: staging a pending message payload + inbox append +
// lane.state update + queue_update event, and the cancel trichotomy.

import (
	"fmt"
	"time"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// QueueResult mirrors the TS shape.
type QueueResult struct {
	EntryID string
}

// CancelKind enumerates cancelQueued's outcomes.
type CancelKind string

const (
	CancelCancelled       CancelKind = "cancelled"
	CancelNotFound        CancelKind = "not_found"
	CancelAlreadyConsumed CancelKind = "already_consumed"
)

// CancelQueuedResult carries the cancel outcome.
type CancelQueuedResult struct {
	Kind CancelKind
}

// QueueMessage stages one queued message of the given kind.
func QueueMessage(lane *Lane, kind, text string, images []any, ctx sessionCtxAlias) (*QueueResult, error) {
	at := float64(time.Now().UnixMilli())
	entryID := newRuntimeID()
	message := jsonx.NewObj()
	message.Set("role", "user")
	if len(images) > 0 {
		content := []any{jsonx.ObjFrom("type", "text", "text", text)}
		content = append(content, images...)
		message.Set("content", content)
	} else {
		message.Set("content", text)
	}
	message.Set("timestamp", at)

	result, err := lane.Command(func(state *RuntimeLaneState, reader session.SessionReader) *LaneCommand {
		inbox := append(append([]session.InboxItem{}, state.Inbox...), session.InboxItem{EntryID: entryID, Kind: kind})
		queues, err := ReadLaneQueues(reader, state.Inbox, ctx)
		if err != nil {
			return &LaneCommand{Kind: LaneCommandReject, Error: err}
		}
		queueEvent := eventLane(lane.Name, "queue_update")
		queuesJSON := jsonx.NewObj()
		steering := make([]any, 0, len(queues))
		followUp := make([]any, 0, len(queues))
		for _, item := range queues {
			obj := jsonx.ObjFrom("entryId", item.EntryID, "kind", item.Kind)
			switch item.Kind {
			case "steer":
				steering = append(steering, obj)
			case "followUp":
				followUp = append(followUp, obj)
			}
		}
		queuesJSON.Set("steering", steering)
		queuesJSON.Set("followUp", followUp)
		queuesJSON.Set("nextRun", nil)
		queueEvent.Set("queues", queuesJSON)

		currentOp := ""
		if state.Operation != nil {
			currentOp = state.Operation.Meta.OperationID
		}
		writes := []session.Write{
			session.WriteFromValue(session.SetValue(session.PendingEntryValue(entryID), jsonx.ObjFrom("type", "message", "payload", message))),
			session.WriteFromValue(session.SetValue(session.LaneStateValue(lane.Name), durableStateJSON(currentOp, inbox))),
		}
		next := *state
		next.Inbox = inbox
		return &LaneCommand{
			Kind:   LaneCommandCommit,
			Writes: writes,
			Next:   &next,
			Events: []HarnessEvent{queueEvent},
			Result: &QueueResult{EntryID: entryID},
		}
	}, ctx)
	if err != nil {
		return nil, err
	}
	return result.(*QueueResult), nil
}

func durableStateJSON(currentOp string, inbox []session.InboxItem) *jsonx.Obj {
	var current *string
	if currentOp != "" {
		current = &currentOp
	}
	return DurableLaneStateJSON(current, nil, inbox)
}

// CancelQueued removes one queued entry: queued -> cancel; already an
// entry -> already_consumed; else not_found.
func CancelQueued(lane *Lane, entryID string, ctx sessionCtxAlias) (*CancelQueuedResult, error) {
	result, err := lane.Command(func(state *RuntimeLaneState, reader session.SessionReader) *LaneCommand {
		var queued *session.InboxItem
		for i := range state.Inbox {
			if state.Inbox[i].EntryID == entryID {
				queued = &state.Inbox[i]
				break
			}
		}
		if queued == nil {
			entries, err := reader.GetEntries([]string{entryID}, ctx)
			if err != nil {
				return &LaneCommand{Kind: LaneCommandReject, Error: err}
			}
			if _, consumed := entries[entryID]; consumed {
				return &LaneCommand{Kind: LaneCommandReturn, Result: &CancelQueuedResult{Kind: CancelAlreadyConsumed}}
			}
			return &LaneCommand{Kind: LaneCommandReturn, Result: &CancelQueuedResult{Kind: CancelNotFound}}
		}
		stored, err := reader.GetValue(session.PendingEntryValue(entryID), ctx)
		if err != nil {
			return &LaneCommand{Kind: LaneCommandReject, Error: err}
		}
		if stored == nil {
			return &LaneCommand{Kind: LaneCommandReject, Error: &session.SessionInvariantError{Message: fmt.Sprintf("Queued %s entry %s is missing its payload", queued.Kind, entryID)}}
		}
		inbox := make([]session.InboxItem, 0, len(state.Inbox))
		for _, item := range state.Inbox {
			if item.EntryID != entryID {
				inbox = append(inbox, item)
			}
		}
		queues, err := ReadLaneQueues(reader, inbox, ctx)
		if err != nil {
			return &LaneCommand{Kind: LaneCommandReject, Error: err}
		}
		queueEvent := eventLane(lane.Name, "queue_update")
		_ = queues
		currentOp := ""
		if state.Operation != nil {
			currentOp = state.Operation.Meta.OperationID
		}
		writes := []session.Write{
			session.WriteFromValue(session.DeleteValue(session.PendingEntryValue(entryID))),
			session.WriteFromValue(session.SetValue(session.LaneStateValue(lane.Name), durableStateJSON(currentOp, inbox))),
		}
		next := *state
		next.Inbox = inbox
		return &LaneCommand{
			Kind:   LaneCommandCommit,
			Writes: writes,
			Next:   &next,
			Events: []HarnessEvent{queueEvent},
			Result: &CancelQueuedResult{Kind: CancelCancelled},
		}
	}, ctx)
	if err != nil {
		return nil, err
	}
	return result.(*CancelQueuedResult), nil
}
