package runtime

// checkpoint.go ports harness/runtime/drive/checkpoint.ts: startRun (the
// initial checkpoint) and the runCheckpoint boundary advance. Hook effects
// are injectable so the facade wires the real hook registry.

import (
	"fmt"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// BeforeRunHook mirrors the before_run hook contract: returns injected
// messages (nil = none).
var BeforeRunHook func(lane *Lane, drive *Drive, prompt []*jsonx.Obj) ([]*jsonx.Obj, error)

// StartRun consumes before_run and commits the initial checkpoint:
// reserved injected entries chain from the tip, the trigger is the last
// entry (or the existing tip), the state moves to checkpoint with a fresh
// need_assistant continuation.
func StartRun(lane *Lane, drive *Drive, run *session.OperationState) (*ProcedureResult, error) {
	ctx := drive.Context

	// Resolve the prompt messages from the intent.
	promptResult, err := lane.ContinueOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		if intentKind(meta) != "run" {
			return &OperationCommand{Kind: OperationCommandReject, Error: &session.SessionInvariantError{Message: "Run operation has non-run intent"}}
		}
		entries, err := reader.GetEntries(promptEntryIDs(meta), ctx)
		if err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}
		messages := make([]*jsonx.Obj, 0, len(promptEntryIDs(meta)))
		for _, id := range promptEntryIDs(meta) {
			entry, ok := entries[id]
			if !ok || entry.Type != session.EntryTypeMessage {
				return &OperationCommand{Kind: OperationCommandReject, Error: &session.SessionInvariantError{Message: fmt.Sprintf("Run prompt entry %s is missing its message", id)}}
			}
			messages = append(messages, entry.Message.Message)
		}
		return &OperationCommand{Kind: OperationCommandReturn, Result: messages}
	}, ctx)
	if err != nil {
		return nil, err
	}
	if promptResult.Kind == "cancel_requested" {
		return &ProcedureResult{Kind: "continue"}, nil
	}
	prompt := promptResult.Value.([]*jsonx.Obj)

	var injected []*jsonx.Obj
	if BeforeRunHook != nil {
		messages, err := BeforeRunHook(lane, drive, prompt)
		if err != nil {
			return nil, err
		}
		injected = messages
	}
	// Pending assistant injections are invariant errors.
	for _, message := range injected {
		if stringOf(message, "role") == "assistant" && stringOf(message, "stopReason") == "pending" {
			return nil, &session.SessionInvariantError{Message: "before_run returned a pending assistant message"}
		}
	}

	reserved := make([]reservedEntry, 0, len(injected))
	for _, message := range injected {
		reserved = append(reserved, reservedEntry{id: newRuntimeID(), message: message})
	}

	// Commit the checkpoint.
	result, err := lane.ContinueOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		newEntries := make([]*session.Entry, 0, len(reserved))
		for _, item := range reserved {
			newEntries = append(newEntries, &session.Entry{
				EntryBase: session.EntryBase{ID: item.id, Type: session.EntryTypeMessage},
				Message:   session.AgentMessagePayload{Role: stringOf(item.message, "role"), Message: item.message},
			})
		}
		chained := ChainEntries(state.TipID, newEntries)

		triggerEntryID := ""
		if len(chained) > 0 {
			triggerEntryID = chained[len(chained)-1].ID
		} else if state.TipID != nil {
			triggerEntryID = *state.TipID
		} else {
			return &OperationCommand{Kind: OperationCommandReject, Error: &session.SessionInvariantError{Message: "Run start has no trigger entry"}}
		}

		nextState := OperationScopeCopy(current)
		nextState.At = session.AtCheckpoint
		continuation := jsonx.ObjFrom("kind", "need_assistant", "overflowRecoveryUsed", false)
		nextState.Continuation = continuation
		nextState.TriggerEntryID = triggerEntryID

		writes := []session.Write{}
		for _, entry := range chained {
			writes = append(writes, session.InsertEntry(entry))
		}
		if len(chained) > 0 {
			writes = append(writes, session.WriteFromValue(session.SetValue(
				session.BranchTip(lane.Name), triggerEntryID,
			)))
		}
		return &OperationCommand{
			Kind:           OperationCommandCommit,
			Writes:         writes,
			OperationState: &nextState,
			HasInbox:       false,
			Result:         &ProcedureResult{Kind: "continue"},
			Materialize:    func(session.CommitResult) {},
			Events:         nil,
		}
	}, ctx)
	if err != nil {
		return nil, err
	}
	if result.Kind == "cancel_requested" {
		return &ProcedureResult{Kind: "continue"}, nil
	}
	return result.Value.(*ProcedureResult), nil
}

type reservedEntry struct {
	id      string
	message *jsonx.Obj
}

func promptEntryIDs(meta *session.OperationMeta) []string {
	if meta.Intent == nil {
		return nil
	}
	if ids, ok := meta.Intent.Get("promptEntryIds"); ok {
		if arr, ok := ids.([]any); ok {
			out := make([]string, 0, len(arr))
			for _, id := range arr {
				if s, ok := id.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

// newRuntimeID mints entry ids (uuidv7 shim until the session generator is
// threaded through).
func newRuntimeID() string { return runtimeIDGenerator() }

var runtimeIDGenerator = defaultRuntimeID

// ThresholdPreparation mirrors prepareCompactionThreshold's value: when a
// threshold fires the run diverts into summary.deciding.
type ThresholdPreparation struct {
	TaskID      string
	Preparation *jsonx.Obj
}

// PrepareCompactionThreshold is injectable (structural.ts port provides
// the real policy; nil means never compact). DefaultThreshold wires the
// compaction.go decision when the caller does not override.
var PrepareCompactionThreshold func(lane *Lane, drive *Drive, state *session.OperationState) (*ThresholdPreparation, bool, error)

// DefaultThreshold evaluates the threshold using TriggerCompaction's
// guards with the model availability and window supplied by the caller.
func DefaultThreshold(lane *Lane, drive *Drive, state *session.OperationState, settings CompactionSettings, modelAvailable bool, contextWindow float64, tokensBefore float64) (*ThresholdPreparation, bool, error) {
	bounded, err := ReadBoundedEntries(lane.sessionReader(), lane.State().TipID, drive.Context)
	if err != nil {
		return nil, false, err
	}
	decision, err := TriggerCompaction(settings, modelAvailable, contextWindow, bounded, state.TriggerEntryID, tokensBefore)
	if err != nil {
		return nil, false, err
	}
	if !decision.Compact {
		return nil, false, nil
	}
	return &ThresholdPreparation{TaskID: newRuntimeID(), Preparation: nil}, true, nil
}

// RunCheckpoint advances one durable run boundary with at most one commit.
func RunCheckpoint(lane *Lane, drive *Drive, run *session.OperationState) (*ProcedureResult, error) {
	ctx := drive.Context
	SetLaneName(lane.Name)

	var threshold *ThresholdPreparation
	if PrepareCompactionThreshold != nil {
		preparation, fired, err := PrepareCompactionThreshold(lane, drive, run)
		if err != nil {
			return nil, err
		}
		if fired {
			threshold = preparation
		}
	}

	continuation := run.Continuation
	continuationKind := "need_assistant"
	if continuation != nil {
		if kind, ok := continuation.Get("kind"); ok {
			if s, ok := kind.(string); ok {
				continuationKind = s
			}
		}
	}

	planned, err := lane.ContinueOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		placement, err := PlanBoundaryInbox(
			state.Inbox, steeringModeOf(current), followUpModeOf(current),
			reader, state.TipID,
			threshold == nil && continuationKind == "may_finish",
			projectorSetOf(lane), ctx,
		)
		if err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}

		if placement.HasTrigger {
			nextState := AssistantReadyAtBoundary(state, current, placement.TriggerEntryID, false, newRuntimeID())
			return &OperationCommand{
				Kind:           OperationCommandCommit,
				Writes:         placement.Writes,
				OperationState: nextState,
				HasInbox:       true,
				Inbox:          placement.Inbox,
				Result:         &ProcedureResult{Kind: "continue"},
				Materialize:    func(session.CommitResult) {},
			}
		}

		if threshold != nil {
			// Divert into summary.deciding with a resume_checkpoint
			// boundary.
			nextState := OperationScopeCopy(current)
			nextState.At = session.AtSummaryDeciding
			resumeAfter := jsonx.NewObj()
			resumeAfter.Set("continuation", continuation)
			resumeAfter.Set("triggerEntryId", current.TriggerEntryID)
			boundary := jsonx.ObjFrom("kind", "resume_checkpoint", "resumeAfter", resumeAfter)
			task := jsonx.ObjFrom("taskId", threshold.TaskID, "reason", "threshold", "boundary", boundary)
			nextState.Task = task

			writes := append([]session.Write{}, placement.Writes...)
			writes = append(writes, session.WriteFromValue(session.SetValue(
				session.OperationPreparation(drive.OperationID, threshold.TaskID), threshold.Preparation,
			)))
			return &OperationCommand{
				Kind:           OperationCommandCommit,
				Writes:         writes,
				OperationState: &nextState,
				HasInbox:       true,
				Inbox:          placement.Inbox,
				Result:         &ProcedureResult{Kind: "continue"},
				Materialize:    func(session.CommitResult) {},
			}
		}

		if continuationKind == "need_assistant" {
			// Stay at checkpoint but advance to assistant.ready using the
			// current trigger.
			nextState := AssistantReadyAtBoundary(state, current, current.TriggerEntryID, overflowRecoveryUsedOf(continuation), newRuntimeID())
			return &OperationCommand{
				Kind:           OperationCommandCommit,
				Writes:         placement.Writes,
				OperationState: nextState,
				HasInbox:       true,
				Inbox:          placement.Inbox,
				Result:         &ProcedureResult{Kind: "continue"},
				Materialize:    func(session.CommitResult) {},
			}
		}

		// may_finish with no trigger: finish pending.
		entryIDs := make([]string, 0, len(placement.Entries))
		for _, entry := range placement.Entries {
			entryIDs = append(entryIDs, entry.ID)
		}
		return &OperationCommand{Kind: OperationCommandReturn, Result: &boundaryFinishPending{EntryIDs: entryIDs}}
	}, ctx)
	if err != nil {
		return nil, err
	}
	if planned.Kind == "cancel_requested" {
		return &ProcedureResult{Kind: "continue"}, nil
	}
	if pending, ok := planned.Value.(*boundaryFinishPending); ok {
		if continuationKind != "may_finish" {
			return nil, &session.SessionInvariantError{Message: "Checkpoint finish mediation requires a finish continuation"}
		}
		return FinishRunBoundary(lane, drive, run, continuation, pending.EntryIDs, nil)
	}
	return planned.Value.(*ProcedureResult), nil
}

type boundaryFinishPending struct {
	EntryIDs []string
}

// steeringModeOf/followUpModeOf read the captured settings.
func steeringModeOf(state *session.OperationState) string {
	if state.Settings != nil {
		if v, ok := state.Settings.Get("steeringMode"); ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return "one-at-a-time"
}

func followUpModeOf(state *session.OperationState) string {
	if state.Settings != nil {
		if v, ok := state.Settings.Get("followUpMode"); ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return "one-at-a-time"
}

func projectorSetOf(*Lane) map[string]bool { return nil }

func overflowRecoveryUsedOf(continuation *jsonx.Obj) bool {
	if continuation == nil {
		return false
	}
	if v, ok := continuation.Get("overflowRecoveryUsed"); ok {
		return v == true
	}
	return false
}

// FinishRunBoundary mirrors finishRunBoundary: replan after before_run_end
// and either renew work (trigger or followUp) or commit the terminal
// completed record.
func FinishRunBoundary(lane *Lane, drive *Drive, capability *session.OperationState, continuation *jsonx.Obj, plannedEntryIDs []string, pendingEvents []HarnessEvent) (*ProcedureResult, error) {
	ctx := drive.Context
	SetLaneName(lane.Name)

	includeFinalAssistant := false
	if continuation != nil {
		if v, ok := continuation.Get("includeFinalAssistant"); ok {
			includeFinalAssistant = v == true
		}
	}

	result, err := lane.ContinueOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		placement, err := PlanBoundaryInbox(
			state.Inbox, steeringModeOf(current), followUpModeOf(current),
			reader, state.TipID, true, projectorSetOf(lane), ctx,
		)
		if err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}

		if placement.HasTrigger {
			nextState := AssistantReadyAtBoundary(state, current, placement.TriggerEntryID, false, newRuntimeID())
			return &OperationCommand{
				Kind:           OperationCommandCommit,
				Writes:         placement.Writes,
				OperationState: nextState,
				HasInbox:       true,
				Inbox:          placement.Inbox,
				Result:         &ProcedureResult{Kind: "continue"},
			}
		}

		// Terminal completion.
		if placement.TipID == nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: &session.SessionInvariantError{Message: "Completed run has no tip"}}
		}
		if includeFinalAssistant && current.LatestAssistantEntryID == nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: &session.SessionInvariantError{Message: "Completed run is missing its final assistant"}}
		}
		record, err := OperationResultRecord(meta, session.StatusCompleted, placement.TipID, nil)
		if err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}
		cleanup, err := OperationCleanupWrites(reader, drive.OperationID, current, ctx)
		if err != nil {
			return &OperationCommand{Kind: OperationCommandReject, Error: err}
		}
		writes := append(append([]session.Write{}, placement.Writes...), cleanup...)
		return &OperationCommand{
			Kind:     OperationCommandFinish,
			Writes:   writes,
			Record:   record,
			HasInbox: true,
			Inbox:    placement.Inbox,
			Result:   &ProcedureResult{Kind: "settled", Outcome: record},
		}
	}, ctx)
	if err != nil {
		return nil, err
	}
	if result.Kind == "cancel_requested" {
		return &ProcedureResult{Kind: "continue"}, nil
	}
	return result.Value.(*ProcedureResult), nil
}

// keep harness imported for the gate context type.
var _ harness.Context = nil

// sessionReader exposes the lane's storage as a reader.
func (l *Lane) sessionReader() session.SessionReader { return l.session }
