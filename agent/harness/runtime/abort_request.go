package runtime

// abort_request.go ports harness/runtime/lane.ts's requestAbort: the
// operation mismatch rejection, the idempotent already-requested path,
// the steer/followUp payload extraction with inbox pruning, and the
// cancel_requested state write.

import (
	"fmt"
	"time"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// OperationMismatch mirrors the TS error.
type OperationMismatch struct {
	Requested, Active, Last string
}

func (e *OperationMismatch) Error() string {
	return fmt.Sprintf("Operation %s is not active (active: %s, last: %s)", e.Requested, e.Active, e.Last)
}

// AbortRequestOutcome carries the request result.
type AbortRequestOutcome struct {
	// Mismatch rejects when the operation id does not match.
	Mismatch *OperationMismatch
	// NewlyRequested is true when this request flipped the control.
	NewlyRequested bool
	// Steer/FollowUp are the returned queued messages.
	Steer    []*jsonx.Obj
	FollowUp []*jsonx.Obj
}

// RequestAbort mirrors requestAbort's decision.
func RequestAbort(lane *Lane, operationID string, ctx sessionCtxAlias) (*AbortRequestOutcome, error) {
	result, err := lane.Command(func(state *RuntimeLaneState, reader session.SessionReader) *LaneCommand {
		operation := state.Operation
		if operation == nil || operation.Meta.OperationID != operationID {
			active := ""
			last := ""
			if operation != nil {
				active = operation.Meta.OperationID
			}
			if state.LastOperationID != nil {
				last = *state.LastOperationID
			}
			return &LaneCommand{Kind: LaneCommandReturn, Result: &AbortRequestOutcome{
				Mismatch: &OperationMismatch{Requested: operationID, Active: active, Last: last},
			}}
		}
		// Already requested: idempotent no-op.
		if operation.State.Control.Status == "cancel_requested" {
			return &LaneCommand{Kind: LaneCommandReturn, Result: &AbortRequestOutcome{
				NewlyRequested: false,
			}}
		}
		// Extract steer + followUp payloads and prune them from the inbox.
		var removed []session.InboxItem
		var inbox []session.InboxItem
		for _, item := range state.Inbox {
			if item.Kind == "steer" || item.Kind == "followUp" {
				removed = append(removed, item)
				continue
			}
			inbox = append(inbox, item)
		}
		var steer, followUp []*jsonx.Obj
		for _, item := range removed {
			stored, err := reader.GetValue(session.PendingEntryValue(item.EntryID), ctx)
			if err != nil {
				return &LaneCommand{Kind: LaneCommandReject, Error: err}
			}
			if stored == nil {
				return &LaneCommand{Kind: LaneCommandReject, Error: &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is missing its message", item.Kind, item.EntryID)}}
			}
			payload, _ := stored.Value.(*jsonx.Obj)
			if payload == nil {
				return &LaneCommand{Kind: LaneCommandReject, Error: &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is missing its message", item.Kind, item.EntryID)}}
			}
			payloadType, _ := payload.Get("type")
			if payloadType != "message" {
				return &LaneCommand{Kind: LaneCommandReject, Error: &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is missing its message", item.Kind, item.EntryID)}}
			}
			messageValue, _ := payload.Get("payload")
			message, _ := messageValue.(*jsonx.Obj)
			if message == nil {
				return &LaneCommand{Kind: LaneCommandReject, Error: &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is missing its message", item.Kind, item.EntryID)}}
			}
			switch item.Kind {
			case "steer":
				steer = append(steer, message)
			case "followUp":
				followUp = append(followUp, message)
			}
		}

		// cancel_requested state write.
		requestedAt := float64(time.Now().UnixMilli())
		operationState := operationStateToJSON(&operation.State)
		control := jsonx.ObjFrom("status", "cancel_requested", "requestedAt", requestedAt)
		operationState.Set("control", control)

		var writes []session.Write
		for _, item := range removed {
			writes = append(writes, session.WriteFromValue(session.DeleteValue(session.PendingEntryValue(item.EntryID))))
		}
		writes = append(writes,
			session.WriteFromValue(session.SetValue(session.OperationStateValue(operationID), operationState)),
			session.WriteFromValue(session.SetValue(session.LaneStateValue(lane.Name), DurableLaneStateJSON(&operationID, state.LastOperationID, inbox))),
		)

		next := *state
		next.Inbox = inbox
		nextOp := *operation
		nextOp.State.Control.Status = "cancel_requested"
		next.Operation = &nextOp
		return &LaneCommand{
			Kind:   LaneCommandCommit,
			Writes: writes,
			Next:   &next,
			Result: &AbortRequestOutcome{NewlyRequested: true, Steer: steer, FollowUp: followUp},
		}
	}, ctx)
	if err != nil {
		return nil, err
	}
	return result.(*AbortRequestOutcome), nil
}
