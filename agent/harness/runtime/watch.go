package runtime

// watch.go ports harness/runtime/lane.ts's captureLaneSnapshot and the
// watch handle: cloning the lane state, scanning the bounded transcript,
// resolving queues and the last result, and folding the operation's
// streaming/retry/deferred views.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// CaptureLaneSnapshot mirrors captureLaneSnapshot: a cloned state with
// the bounded transcript (oldest-first), the queues view, the last
// result, session stats, and the operation view.
func CaptureLaneSnapshot(lane *Lane, reader session.SessionReader, ctx sessionCtxAlias) (*LaneSnapshot, error) {
	state := lane.State()
	// Prefer the durable branch tip: appends through the session update
	// pi.branch.tip, and the lane's in-memory copy may lag.
	tip := state.TipID
	if stored, err := reader.GetValue(session.BranchTip(lane.Name), ctx); err == nil && stored != nil {
		if s, ok := stored.Value.(string); ok {
			tip = &s
		} else if stored.Value == nil {
			tip = nil
		}
	}
	snapshot := &LaneSnapshot{
		Lane:          lane.Name,
		TipID:         tip,
		Stats:         session.SessionStats{},
		Configuration: state.Configuration,
	}
	// Bounded transcript oldest-first.
	if tip != nil {
		entries, err := reader.ScanBranch(session.StorageBranchScan{
			BranchScan: session.BranchScan{StopAtType: session.EntryTypeCompaction, Order: "newestFirst"},
			StartID:    *tip,
		}, ctx)
		if err != nil {
			return nil, err
		}
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
		snapshot.Transcript = entries
	}
	// Queues.
	queues, err := ReadLaneQueues(reader, state.Inbox, ctx)
	if err != nil {
		return nil, err
	}
	snapshot.Queues = queuesFromItems(queues)
	// Last result.
	if state.LastOperationID != nil {
		stored, err := reader.GetValue(session.OperationResult(*state.LastOperationID), ctx)
		if err != nil {
			return nil, err
		}
		if stored == nil {
			return nil, &session.SessionInvariantError{Message: "Lane " + lane.Name + " is missing result " + *state.LastOperationID}
		}
		if obj, ok := stored.Value.(*jsonx.Obj); ok {
			snapshot.LastResult = recordFromObj(obj)
		}
	}
	// Stats.
	stats, err := reader.GetStats(ctx)
	if err != nil {
		return nil, err
	}
	snapshot.Stats = stats
	// Operation view.
	if state.Operation != nil {
		snapshot.Operation = operationSnapshotOf(state.Operation)
	}
	return snapshot, nil
}

// recordFromObj decodes one pi.result record.
func recordFromObj(obj *jsonx.Obj) *session.OperationResultRecord {
	record := &session.OperationResultRecord{
		OperationID: strOf(obj, "operationId"),
		Kind:        strOf(obj, "kind"),
		Status:      strOf(obj, "status"),
		StartedAt:   numOf(obj, "startedAt"),
		EndedAt:     numOf(obj, "endedAt"),
	}
	if tip := strOf(obj, "tipId"); tip != "" {
		record.TipID = &tip
	}
	if from := strOf(obj, "fromTipId"); from != "" {
		record.FromTipID = &from
	}
	if errorValue, ok := obj.Get("error"); ok && errorValue != nil {
		if errorObj, ok := errorValue.(*jsonx.Obj); ok {
			record.Error = &session.OperationError{
				Code:    strOf(errorObj, "code"),
				Message: strOf(errorObj, "message"),
			}
		}
	}
	return record
}

// operationSnapshotOf projects the operation into the snapshot view.
func operationSnapshotOf(operation *session.Operation) *LaneOperationSnapshot {
	snapshot := &LaneOperationSnapshot{
		ID:        operation.Meta.OperationID,
		StartedAt: operation.Meta.StartedAt,
		FromTipID: operation.Meta.SourceTipID,
		Status:    "open",
	}
	switch intentKind(&operation.Meta) {
	case "compaction":
		snapshot.Kind = "compaction"
	case "navigation":
		snapshot.Kind = "navigation"
	default:
		snapshot.Kind = "run"
	}
	if operation.State.Control.Status == "cancel_requested" {
		snapshot.Status = "aborting"
	}
	state := &operation.State
	if state.ResponseEntryID != "" {
		snapshot.RunningTools = []runningToolSnapshot{}
	}
	if state.NextAttempt > 0 && state.At == session.AtAssistantRetryWait {
		snapshot.Retry = &retrySnapshot{NextAttemptAt: state.NotBefore}
	}
	return snapshot
}

func strOf(obj *jsonx.Obj, key string) string {
	if v, ok := obj.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func numOf(obj *jsonx.Obj, key string) float64 {
	if v, ok := obj.Get(key); ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

// LaneSnapshotJSON renders the snapshot through the jsonx model.
func LaneSnapshotJSON(snapshot *LaneSnapshot) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("lane", snapshot.Lane)
	obj.Set("tipId", idOrNil(snapshot.TipID))
	transcript := make([]any, 0, len(snapshot.Transcript))
	for _, entry := range snapshot.Transcript {
		transcript = append(transcript, entry.ID)
	}
	obj.Set("transcript", transcript)
	obj.Set("stats", jsonx.ObjFrom("messageCount", float64(snapshot.Stats.MessageCount)))
	if snapshot.LastResult != nil {
		result := jsonx.ObjFrom(
			"operationId", snapshot.LastResult.OperationID,
			"status", snapshot.LastResult.Status,
		)
		obj.Set("lastResult", result)
	}
	return obj
}

func idOrNil(id *string) any {
	if id == nil {
		return nil
	}
	return *id
}
