package runtime

// deferred_intent.go ports harness/runtime/drive/deferred.ts's poll
// intent publication: reserving the poll's response + usage ids,
// transitioning to deferred.effect_pending, decrementing the drive's
// deferred permits, and emitting the run_resume + turn_start events.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
)

// DeferredIntent captures the effect-pending fields reserved for one poll.
type DeferredIntent struct {
	ResponseEntryID string
	UsageID         string
	Poll            int64
	// PrevResponseEntryID is the prior poll's response entry (whose frame
	// list is deleted when re-polling from effect_pending).
	PrevResponseEntryID string
	Repoll              bool
}

// ReserveDeferredIntent mirrors the pending fields: poll + 1 from
// suspended (kept from effect_pending) and two fresh ids.
func ReserveDeferredIntent(at string, current *session.OperationState, responseID func() string) *DeferredIntent {
	poll := current.Poll
	if at == session.AtDeferredSuspended {
		poll = current.Poll + 1
	}
	intent := &DeferredIntent{
		Poll:            poll,
		ResponseEntryID: responseID(),
		UsageID:         responseID(),
	}
	if at == session.AtDeferredEffectPending {
		intent.Repoll = true
		intent.PrevResponseEntryID = current.ResponseEntryID
	}
	return intent
}

// DeferredEffectPendingState builds the next state.
func DeferredEffectPendingState(scope *session.OperationState, intent *DeferredIntent) *session.OperationState {
	next := OperationScopeCopy(scope)
	next.At = session.AtDeferredEffectPending
	next.Poll = intent.Poll
	next.ResponseEntryID = intent.ResponseEntryID
	next.UsageID = intent.UsageID
	return &next
}

// DeferredIntentEvents mirrors the run_resume + turn_start emission, both
// flagged on recovery.
func DeferredIntentEvents(lane, runID, stepID string, poll int64, recovery bool) []HarnessEvent {
	resume := eventLane(lane, "run_resume")
	resume.Set("runId", runID)
	if recovery {
		resume.Set("recovery", true)
	}
	turn := eventLane(lane, "turn_start")
	turn.Set("runId", runID)
	turn.Set("turnId", TurnIDOf(session.AtDeferredEffectPending, stepID, poll))
	if recovery {
		turn.Set("recovery", true)
	}
	return []HarnessEvent{resume, turn}
}

// PublishDeferredIntent runs the poll intent commit through the lane and
// decrements the drive's deferred permits on materialize.
func PublishDeferredIntent(lane *Lane, drive *Drive, deferred *session.OperationState, stepID string, recovery bool, nextID func() string) (*DeferredIntent, error) {
	intent := ReserveDeferredIntent(deferred.At, deferred, nextID)
	// Keep the durable op.state aligned for the settle write.
	if err := lane.session.SetValue(session.OperationStateValue(drive.OperationID), operationStateToJSON(deferred), drive.Context); err != nil {
		return nil, err
	}
	result, err := lane.ContinueOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		next := DeferredEffectPendingState(current, intent)
		var writes []session.Write
		if intent.Repoll {
			writes = append(writes, DeferredFramesCleanup(drive.OperationID, intent.PrevResponseEntryID))
		}
		return &OperationCommand{
			Kind:           OperationCommandCommit,
			Writes:         writes,
			OperationState: next,
			Events:         DeferredIntentEvents(lane.Name, drive.OperationID, stepID, intent.Poll, recovery),
			Materialize: func(session.CommitResult) {
				drive.PollDeferred--
			},
			Result: intent,
		}
	}, drive.Context)
	if err != nil {
		return nil, err
	}
	if result.Kind == "cancel_requested" {
		return nil, nil
	}
	return result.Value.(*DeferredIntent), nil
}
