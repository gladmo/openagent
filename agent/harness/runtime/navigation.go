package runtime

// navigation.go ports harness/runtime/drive/structural.ts's
// commitNavigation: target validation, the branch-tip move with an
// optional label, and the terminal record + navigation_end event.

import (
	"fmt"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// ValidateNavigation mirrors the three invariants: the target must exist,
// must differ from the source tip, and a root target cannot carry a label.
func ValidateNavigation(targetID *string, label *string, targetExists bool, sourceTipID *string) error {
	if targetID != nil && !targetExists {
		return &session.SessionInvariantError{Message: fmt.Sprintf("Navigation target %s is missing", *targetID)}
	}
	if targetID != nil && sourceTipID != nil && *targetID == *sourceTipID {
		return &session.SessionInvariantError{Message: "Navigation target must differ from its source tip"}
	}
	if targetID == nil && label != nil {
		return &session.SessionInvariantError{Message: "Root navigation cannot set a label"}
	}
	return nil
}

// NavigationWrites builds the tip move + optional label write.
func NavigationWrites(lane string, targetID *string, label *string) []session.Write {
	var writes []session.Write
	if targetID != nil {
		writes = append(writes, session.WriteFromValue(session.SetValue(session.BranchTip(lane), *targetID)))
	} else {
		writes = append(writes, session.WriteFromValue(session.SetValue(session.BranchTip(lane), nil)))
	}
	if label != nil && targetID != nil {
		writes = append(writes, session.WriteFromValue(session.SetValue(session.EntryLabel(*targetID), *label)))
	}
	return writes
}

// NavigationEndEvent builds the navigation_end event.
func NavigationEndEvent(lane, runID string, status string, targetID *string, endedAt float64) HarnessEvent {
	event := eventLane(lane, "navigation_end")
	event.Set("runId", runID)
	event.Set("status", status)
	if targetID != nil {
		event.Set("tipId", *targetID)
	} else {
		event.Set("tipId", nil)
	}
	event.Set("endedAt", endedAt)
	return event
}

// CommitNavigation executes the navigation commit end-to-end.
func CommitNavigation(lane *Lane, drive *Drive, navigation *session.OperationState) (*ProcedureResult, error) {
	// Keep the durable op.state aligned for the settle write.
	if err := lane.session.SetValue(session.OperationStateValue(drive.OperationID), operationStateToJSON(navigation), drive.Context); err != nil {
		return nil, err
	}
	var events []HarnessEvent
	result, err := lane.ContinueOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		targetExists := false
		if current.TargetID != nil {
			entries, err := reader.GetEntries([]string{*current.TargetID}, drive.Context)
			if err != nil {
				return &OperationCommand{Kind: OperationCommandReject, Error: err}
			}
			_, targetExists = entries[*current.TargetID]
		}
		if err := ValidateNavigation(current.TargetID, current.Label, targetExists, meta.SourceTipID); err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}
		record, err := OperationResultRecord(meta, session.StatusCompleted, current.TargetID, nil)
		if err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}
		cleanup, err := OperationCleanupWrites(reader, drive.OperationID, current, drive.Context)
		if err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}
		writes := append(NavigationWrites(lane.Name, current.TargetID, current.Label), cleanup...)
		events = []HarnessEvent{NavigationEndEvent(lane.Name, drive.OperationID, session.StatusCompleted, current.TargetID, record.EndedAt)}
		return &OperationCommand{
			Kind:     OperationCommandFinish,
			Writes:   writes,
			Record:   record,
			HasInbox: false,
			Events:   events,
			Result:   &ProcedureResult{Kind: "settled", Outcome: record},
		}
	}, drive.Context)
	if err != nil {
		return nil, err
	}
	if result.Kind == "cancel_requested" {
		return &ProcedureResult{Kind: "continue"}, nil
	}
	return result.Value.(*ProcedureResult), nil
}

// PublishConfigurationFailure mirrors publishConfigurationFailure: the
// failed record + cleanup writes + run_end failed event; requires a
// branch tip.
func PublishConfigurationFailure(lane *Lane, drive *Drive, capability *session.OperationState, failure *session.OperationError) (*ProcedureResult, error) {
	if err := lane.session.SetValue(session.OperationStateValue(drive.OperationID), operationStateToJSON(capability), drive.Context); err != nil {
		return nil, err
	}
	result, err := lane.SettleOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		if state.TipID == nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: &session.SessionInvariantError{Message: "Failed run has no Branch tip"}}
		}
		record, err := OperationResultRecord(meta, session.StatusFailed, state.TipID, failure)
		if err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}
		cleanup, err := OperationCleanupWrites(reader, drive.OperationID, current, drive.Context)
		if err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}
		runEnd := eventLane(lane.Name, "run_end")
		runEnd.Set("runId", drive.OperationID)
		runEnd.Set("status", session.StatusFailed)
		errorObj := jsonx.NewObj()
		errorObj.Set("code", failure.Code)
		errorObj.Set("message", failure.Message)
		runEnd.Set("error", errorObj)
		if meta.SourceTipID != nil {
			runEnd.Set("fromTipId", *meta.SourceTipID)
		} else {
			runEnd.Set("fromTipId", nil)
		}
		runEnd.Set("tipId", *state.TipID)
		runEnd.Set("endedAt", record.EndedAt)
		return &OperationCommand{
			Kind:   OperationCommandFinish,
			Writes: cleanup,
			Record: record,
			Events: []HarnessEvent{runEnd},
			Result: &ProcedureResult{Kind: "settled", Outcome: record},
		}
	}, drive.Context)
	if err != nil {
		return nil, err
	}
	return result.(*ProcedureResult), nil
}
