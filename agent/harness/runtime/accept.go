package runtime

// accept.go ports harness/runtime/lane.ts's acceptRun core: the
// busy-lane rejection, the inbox selection with pending-payload
// validation, and the run operation installation writes.

import (
	"fmt"
	"time"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// LaneBusy mirrors the TS error.
type LaneBusy struct {
	Lane          string
	OperationID   string
	OperationKind string
}

func (e *LaneBusy) Error() string {
	return fmt.Sprintf("Lane %q already has an active operation", e.Lane)
}

// InvalidMessage mirrors the TS error.
type InvalidMessage struct{ Lane, Reason string }

func (e *InvalidMessage) Error() string { return e.Reason }

// AcceptPrompt is one admitted prompt message.
type AcceptPrompt struct {
	ID      string
	Message *jsonx.Obj
}

// AcceptResult carries the admission outcome.
type AcceptResult struct {
	// Busy rejects with the active operation.
	Busy *LaneBusy
	// Invalid rejects the request.
	Invalid *InvalidMessage
	// Admitted carries the installed operation id.
	Admitted    bool
	OperationID string
}

// AcceptRun mirrors acceptRun's decision: prompts chain from the tip as
// pending entries; the selected inbox joins the prompt entry ids; the
// run operation installs with op.meta + op.state + lane.state updates.
func AcceptRun(
	lane *Lane,
	prompts []AcceptPrompt,
	steeringMode, followUpMode string,
	nextID func(timestamp float64) string,
	ctx sessionCtxAlias,
) (*AcceptResult, error) {
	startedAt := float64(time.Now().UnixMilli())
	operationID := nextID(startedAt)
	result, err := lane.Command(func(state *RuntimeLaneState, reader session.SessionReader) *LaneCommand {
		if state.Operation != nil {
			return &LaneCommand{Kind: LaneCommandReturn, Result: &AcceptResult{
				Busy: &LaneBusy{
					Lane:          lane.Name,
					OperationID:   state.Operation.Meta.OperationID,
					OperationKind: intentKind(&state.Operation.Meta),
				},
			}}
		}
		// Inbox selection.
		selected, remainder := SelectAcceptedInbox(state.Inbox, steeringMode, followUpMode)
		promptEntryIDs := make([]any, 0, len(prompts))
		for _, prompt := range prompts {
			promptEntryIDs = append(promptEntryIDs, prompt.ID)
		}
		hasCapturedConversation := false
		for _, item := range selected {
			if item.Kind != "write" {
				hasCapturedConversation = true
			}
			promptEntryIDs = append(promptEntryIDs, item.EntryID)
		}
		if len(prompts) == 0 && !hasCapturedConversation {
			return &LaneCommand{Kind: LaneCommandReturn, Result: &AcceptResult{
				Invalid: &InvalidMessage{Lane: lane.Name, Reason: "no prompt messages and no queued conversation"},
			}}
		}

		// Validate each selected pending payload; keep the payloads for
		// materialization below.
		payloads := map[string]*jsonx.Obj{}
		for _, item := range selected {
			stored, err := reader.GetValue(session.PendingEntryValue(item.EntryID), ctx)
			if err != nil {
				return &LaneCommand{Kind: LaneCommandReject, Error: err}
			}
			if stored == nil {
				return &LaneCommand{Kind: LaneCommandReject, Error: &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is missing its payload", item.Kind, item.EntryID)}}
			}
			payload, _ := stored.Value.(*jsonx.Obj)
			if payload == nil {
				return &LaneCommand{Kind: LaneCommandReject, Error: &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is missing its payload", item.Kind, item.EntryID)}}
			}
			payloadType, _ := payload.Get("type")
			if item.Kind != "write" && payloadType != "message" {
				return &LaneCommand{Kind: LaneCommandReject, Error: &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is not a message", item.Kind, item.EntryID)}}
			}
			if payloadType == "message" {
				messageValue, _ := payload.Get("payload")
				if message, ok := messageValue.(*jsonx.Obj); ok {
					if stringOfObjKey(message, "role") == "assistant" && stringOfObjKey(message, "stopReason") == "pending" {
						return &LaneCommand{Kind: LaneCommandReject, Error: &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s contains a pending assistant", item.Kind, item.EntryID)}}
					}
				}
			}
			payloads[item.EntryID] = payload
		}

		// Installation writes: prompt entries + op.meta + op.state + lane.state.
		intentObj := jsonx.ObjFrom("kind", "run", "promptEntryIds", promptEntryIDs)
		meta := jsonx.ObjFrom(
			"operationId", operationID,
			"lane", lane.Name,
			"sourceTipId", nil,
			"startedAt", startedAt,
			"intent", intentObj,
		)
		stateObj := jsonx.ObjFrom(
			"at", session.AtStarting,
			"control", jsonx.ObjFrom("status", "running"),
		)
		settings := jsonx.ObjFrom(
			"steeringMode", steeringMode,
			"followUpMode", followUpMode,
		)
		stateObj.Set("settings", settings)

		// Prompts and captured inbox items all materialize as entries
		// chained from the tip (the boundary planner does the same when it
		// consumes inbox items); the pending payloads are consumed.
		var writes []session.Write
		parentID := state.TipID
		for _, prompt := range prompts {
			entry := &session.Entry{
				EntryBase: session.EntryBase{ID: prompt.ID, ParentID: parentID, Type: session.EntryTypeMessage},
				Message:   session.AgentMessagePayload{Role: stringOfObjKey(prompt.Message, "role"), Message: prompt.Message},
			}
			writes = append(writes, session.InsertEntry(entry))
			id := prompt.ID
			parentID = &id
		}
		for _, item := range selected {
			entry := PendingEntryWrite(item.EntryID, payloads[item.EntryID])
			entry.ParentID = parentID
			parentID = &item.EntryID
			writes = append(writes, session.InsertEntry(entry))
		}
		for _, item := range selected {
			writes = append(writes, session.WriteFromValue(session.DeleteValue(session.PendingEntryValue(item.EntryID))))
		}
		writes = append(writes,
			session.WriteFromValue(session.SetValue(session.OperationMetaValue(operationID), meta)),
			session.WriteFromValue(session.SetValue(session.OperationStateValue(operationID), stateObj)),
			session.WriteFromValue(session.SetValue(session.LaneStateValue(lane.Name), DurableLaneStateJSON(&operationID, state.LastOperationID, remainder))),
		)
		// The durable tip advances with the materialized entries so
		// snapshots and crash restore see the run's opening transcript.
		tipAdvanced := parentID != nil && (state.TipID == nil || *parentID != *state.TipID)
		if tipAdvanced {
			writes = append(writes, session.WriteFromValue(session.SetValue(session.BranchTip(lane.Name), *parentID)))
		}

		nextState := *state
		nextState.Inbox = remainder
		// The tip advances to the last materialized entry.
		nextState.TipID = parentID
		nextState.Operation = &session.Operation{
			Meta: session.OperationMeta{
				OperationID: operationID,
				Lane:        lane.Name,
				StartedAt:   startedAt,
				Intent:      intentObj,
			},
			State: session.OperationState{
				At:       session.AtStarting,
				Control:  session.Control{Status: "running"},
				Settings: settings,
			},
		}
		return &LaneCommand{
			Kind:   LaneCommandCommit,
			Writes: writes,
			Next:   &nextState,
			Result: &AcceptResult{Admitted: true, OperationID: operationID},
		}
	}, ctx)
	if err != nil {
		return nil, err
	}
	return result.(*AcceptResult), nil
}

func stringOfObjKey(obj *jsonx.Obj, key string) string {
	if v, ok := obj.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
