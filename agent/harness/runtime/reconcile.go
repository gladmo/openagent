package runtime

// reconcile.go ports harness/runtime/drive/reconcile.ts: publishing the
// aborted terminal for a cancelled operation and the per-leaf dispatch.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
)

// PublishAbortedTerminal settles a cancelled operation: aborted record +
// cleanup writes + kind-specific end events. Requires cancelled durable
// control.
func PublishAbortedTerminal(lane *Lane, drive *Drive, capability *session.OperationState) (*ProcedureResult, error) {
	result, err := lane.SettleOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		if current.Control.Status != "cancel_requested" {
			return &OperationCommand{Kind: OperationCommandReject, Error: &session.SessionInvariantError{Message: "Cancellation reconciliation requires cancelled durable control"}}
		}
		record, err := OperationResultRecord(meta, session.StatusAborted, state.TipID, nil)
		if err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}
		cleanup, err := OperationCleanupWrites(reader, drive.OperationID, current, drive.Context)
		if err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}
		kind := intentKind(meta)
		var events []HarnessEvent
		switch kind {
		case "run":
			// A cancelled summary leaf with an invalid boundary is an
			// invariant; otherwise the run_end event closes the run.
			if isSummaryLeaf(current.At) {
				if taskBoundaryKind(current.Task) != "resume_checkpoint" {
					return &OperationCommand{Kind: OperationCommandReject, Error: &session.SessionInvariantError{Message: "Cancelled run summary has an invalid result boundary"}}
				}
				if reason, ok := taskReason(current.Task); ok {
					compactionEnd := eventLane(lane.Name, "compaction_end")
					compactionEnd.Set("runId", drive.OperationID)
					compactionEnd.Set("reason", reason)
					compactionEnd.Set("status", session.StatusAborted)
					compactionEnd.Set("endedAt", record.EndedAt)
					events = append(events, compactionEnd)
				} else {
					return &OperationCommand{Kind: OperationCommandReject, Error: &session.SessionInvariantError{Message: "Cancelled run summary has an invalid result boundary"}}
				}
			}
			runEnd := eventLane(lane.Name, "run_end")
			runEnd.Set("runId", drive.OperationID)
			runEnd.Set("status", session.StatusAborted)
			if meta.SourceTipID != nil {
				runEnd.Set("fromTipId", *meta.SourceTipID)
			} else {
				runEnd.Set("fromTipId", nil)
			}
			if state.TipID != nil {
				runEnd.Set("tipId", *state.TipID)
			} else {
				runEnd.Set("tipId", nil)
			}
			runEnd.Set("endedAt", record.EndedAt)
			events = append(events, runEnd)
		case "compaction":
			compactionEnd := eventLane(lane.Name, "compaction_end")
			compactionEnd.Set("runId", drive.OperationID)
			compactionEnd.Set("reason", "manual")
			compactionEnd.Set("status", session.StatusAborted)
			compactionEnd.Set("endedAt", record.EndedAt)
			events = append(events, compactionEnd)
		default: // navigation
			navEnd := eventLane(lane.Name, "navigation_end")
			navEnd.Set("runId", drive.OperationID)
			navEnd.Set("status", session.StatusAborted)
			if meta.SourceTipID != nil {
				navEnd.Set("fromTipId", *meta.SourceTipID)
			} else {
				navEnd.Set("fromTipId", nil)
			}
			if state.TipID != nil {
				navEnd.Set("tipId", *state.TipID)
			} else {
				navEnd.Set("tipId", nil)
			}
			navEnd.Set("endedAt", record.EndedAt)
			events = append(events, navEnd)
		}
		return &OperationCommand{
			Kind:   OperationCommandFinish,
			Writes: cleanup,
			Record: record,
			Events: events,
			Result: &ProcedureResult{Kind: "settled", Outcome: record},
		}
	}, drive.Context)
	if err != nil {
		return nil, err
	}
	return result.(*ProcedureResult), nil
}

func isSummaryLeaf(at string) bool {
	return len(at) >= 8 && at[:8] == "summary."
}

func taskReason(task *TaskObj) (string, bool) {
	if task == nil {
		return "", false
	}
	if reason, ok := task.Get("reason"); ok {
		if s, ok := reason.(string); ok {
			return s, true
		}
	}
	return "", false
}

// TaskObj aliases the task payload object.
type TaskObj = taskObjAlias

// ReconcileOperation advances one cancelled durable leaf without starting
// new ordinary work. Effect-pending leaves route to recovery (injectable
// until the generation port); every other leaf publishes the aborted
// terminal.
func ReconcileOperation(lane *Lane, drive *Drive) (*ProcedureResult, error) {
	operation := lane.State().Operation
	if operation == nil || operation.Meta.OperationID != drive.OperationID {
		return nil, &session.SessionInvariantError{Message: "Drive " + drive.OperationID + " has no matching operation to reconcile"}
	}
	if operation.State.Control.Status != "cancel_requested" {
		return nil, &session.SessionInvariantError{Message: "Operation " + drive.OperationID + " is not cancelled"}
	}
	switch operation.State.At {
	case "assistant.effect_pending", "tools", "deferred.suspended", "deferred.effect_pending":
		// Effect-bearing leaves reconcile through their recovery
		// procedures; until the generation/tools ports land they publish
		// the aborted terminal (the local-effect side is empty in tests).
		if ReconcileEffect != nil {
			return ReconcileEffect(lane, drive, &operation.State)
		}
		return PublishAbortedTerminal(lane, drive, &operation.State)
	default:
		return PublishAbortedTerminal(lane, drive, &operation.State)
	}
}

// ReconcileEffect reconciles effect-bearing cancelled leaves (generation/
// tools/deferred recovery); injectable.
var ReconcileEffect DriveProcedure
