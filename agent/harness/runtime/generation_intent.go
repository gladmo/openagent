package runtime

// generation_intent.go ports harness/runtime/drive/generation.ts's
// publishGenerationIntent: reserving the response + usage entry ids and
// transitioning to assistant.effect_pending with the model's output-limit
// and context-window, emitting turn_start on the first attempt.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// GenerationIntent captures the effect-pending fields reserved at intent
// publication.
type GenerationIntent struct {
	ResponseEntryID     string
	UsageID             string
	IntendedOutputLimit float64
	ContextWindow       float64
	Attempt             int64
	GenerationContext   *jsonx.Obj
}

// ReserveGenerationIntent mirrors the pending fields: two uuidv7 ids
// minted at the same timestamp plus the model's limits.
func ReserveGenerationIntent(ready *session.OperationState, model *ai.Model, responseID, usageID func() string) *GenerationIntent {
	intent := &GenerationIntent{
		ResponseEntryID:   responseID(),
		UsageID:           usageID(),
		Attempt:           ready.NextAttempt,
		GenerationContext: ready.GenerationContext,
	}
	if model != nil {
		intent.IntendedOutputLimit = model.MaxTokens
		intent.ContextWindow = model.ContextWindow
	}
	return intent
}

// EffectPendingState mirrors publishGenerationIntent's next state.
func EffectPendingState(scope *session.OperationState, intent *GenerationIntent) *session.OperationState {
	next := OperationScopeCopy(scope)
	next.At = session.AtAssistantEffectPending
	next.GenerationContext = intent.GenerationContext
	next.Attempt = intent.Attempt
	next.ResponseEntryID = intent.ResponseEntryID
	next.UsageID = intent.UsageID
	next.IntendedOutputLimit = intent.IntendedOutputLimit
	next.ContextWindow = intent.ContextWindow
	return &next
}

// IntentEvents mirrors the turn_start emission: only on the first attempt.
func IntentEvents(lane, runID, stepID string, nextAttempt int64) []HarnessEvent {
	if nextAttempt != 1 {
		return nil
	}
	event := eventLane(lane, "turn_start")
	event.Set("runId", runID)
	event.Set("turnId", stepID)
	return []HarnessEvent{event}
}

// PublishGenerationIntent runs the intent commit through the lane.
func PublishGenerationIntent(lane *Lane, drive *Drive, ready *session.OperationState, model *ai.Model, nextIDs func() string) (*GenerationIntent, error) {
	intent := ReserveGenerationIntent(ready, model, nextIDs, nextIDs)
	stepID := ""
	if ready.GenerationContext != nil {
		if v, ok := ready.GenerationContext.Get("stepId"); ok {
			if s, ok := v.(string); ok {
				stepID = s
			}
		}
	}
	// Keep the durable op.state aligned for the settle write.
	if err := lane.session.SetValue(session.OperationStateValue(drive.OperationID), operationStateToJSON(ready), drive.Context); err != nil {
		return nil, err
	}
	result, err := lane.ContinueOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		next := EffectPendingState(current, intent)
		return &OperationCommand{
			Kind:           OperationCommandCommit,
			OperationState: next,
			Events:         IntentEvents(lane.Name, drive.OperationID, stepID, ready.NextAttempt),
			Result:         intent,
		}
	}, drive.Context)
	if err != nil {
		return nil, err
	}
	if result.Kind == "cancel_requested" {
		return nil, nil
	}
	return result.Value.(*GenerationIntent), nil
}
