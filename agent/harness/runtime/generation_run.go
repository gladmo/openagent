package runtime

// generation_run.go ports harness/runtime/drive/generation.ts's
// runGeneration dispatch and runRetryWait: the retry-wait gating (waiting
// outcome vs in-drive wait) and the retry-start transition back to
// assistant.ready.

import (
	"time"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// RetryWaitDecision captures runRetryWait's gating.
type RetryWaitDecision struct {
	// Waiting is true when the drive should return the waiting outcome.
	Waiting bool
	// Outcome carries the waiting retry outcome for the drive result.
	Outcome *DriveOutcome
}

// DecideRetryWait mirrors the retry-wait gate: before notBefore, a drive
// without waitForRetry returns the waiting outcome; a waiting drive waits
// (abort-aware) then proceeds.
func DecideRetryWait(notBefore float64, waitForRetry bool, now float64) *RetryWaitDecision {
	if now >= notBefore {
		return &RetryWaitDecision{}
	}
	if !waitForRetry {
		return &RetryWaitDecision{Waiting: true}
	}
	return &RetryWaitDecision{}
}

// WaitUntilNotBefore waits until notBefore (used by waiting drives).
func WaitUntilNotBefore(notBefore float64, signal *SignalAlias) error {
	return WaitUntil(notBefore, signal)
}

// RetryStartTransition mirrors the retry-wait -> ready transition.
type RetryStartTransition struct {
	NextState *session.OperationState
	Events    []HarnessEvent
}

// BuildRetryStart mirrors runRetryWait's commit: assistant.ready at
// nextAttempt carrying the generation context, plus the retry_start event.
func BuildRetryStart(lane, runID, stepID string, scope *session.OperationState, generationContext *jsonx.Obj, nextAttempt int64) *RetryStartTransition {
	next := OperationScopeCopy(scope)
	next.At = session.AtAssistantReady
	next.GenerationContext = generationContext
	next.NextAttempt = nextAttempt
	event := eventLane(lane, "retry_start")
	event.Set("runId", runID)
	event.Set("step", stepID)
	event.Set("attempt", float64(nextAttempt))
	return &RetryStartTransition{NextState: &next, Events: []HarnessEvent{event}}
}

// RunGenerationDispatch mirrors runGeneration's procedure routing:
// retry_wait -> the retry gate; ready -> the preparation pipeline
// (injectable) then the response publication.
type GenerationProcedure func(lane *Lane, drive *Drive, generation *session.OperationState) (*ProcedureResult, error)

// ReadyProcedure is the assistant.ready/retry pipeline (prepare ->
// intent -> perform -> publish); injectable per seam.
var ReadyProcedure GenerationProcedure

// RunGeneration routes one generation leaf.
func RunGeneration(lane *Lane, drive *Drive, generation *session.OperationState) (*ProcedureResult, error) {
	if generation.At == session.AtAssistantRetryWait {
		return RunRetryWait(lane, drive, generation)
	}
	if ReadyProcedure != nil {
		return ReadyProcedure(lane, drive, generation)
	}
	return nil, &session.SessionInvariantError{Message: "No ready procedure registered"}
}

// RunRetryWait executes the retry-wait leaf end-to-end: gate, wait (when
// the drive waits), then the ready transition through the lane.
func RunRetryWait(lane *Lane, drive *Drive, generation *session.OperationState) (*ProcedureResult, error) {
	now := float64(time.Now().UnixMilli())
	decision := DecideRetryWait(generation.NotBefore, drive.WaitForRetry, now)
	if decision.Waiting {
		return &ProcedureResult{Kind: "waiting", Outcome: &DriveOutcome{
			Kind: DriveOutcomeWaitingRetry,
		}}, nil
	}
	if decision := DecideRetryWait(generation.NotBefore, true, now); !drive.WaitForRetry {
		// Non-waiting drives returned above; waiting drives wait here.
		_ = decision
	}
	if now < generation.NotBefore && drive.WaitForRetry {
		if err := WaitUntilNotBefore(generation.NotBefore, nil); err != nil {
			return nil, err
		}
	}
	// Keep the durable op.state aligned so the settle write validates.
	if err := lane.session.SetValue(session.OperationStateValue(drive.OperationID), operationStateToJSON(generation), drive.Context); err != nil {
		return nil, err
	}

	stepID := ""
	if generation.GenerationContext != nil {
		if v, ok := generation.GenerationContext.Get("stepId"); ok {
			if s, ok := v.(string); ok {
				stepID = s
			}
		}
	}
	scope := OperationScopeCopy(generation)
	scope.At = generation.At
	result, err := lane.ContinueOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		transition := BuildRetryStart(lane.Name, drive.OperationID, stepID, current, generation.GenerationContext, generation.NextAttempt)
		return &OperationCommand{
			Kind:           OperationCommandCommit,
			Writes:         nil,
			OperationState: transition.NextState,
			Events:         transition.Events,
			Result:         &ProcedureResult{Kind: "continue"},
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
