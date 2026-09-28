package runtime

// Ports of runGeneration dispatch + runRetryWait.

import (
	"testing"
	"time"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func TestDecideRetryWait(t *testing.T) {
	now := float64(1000)
	// Due: proceed regardless.
	decision := DecideRetryWait(1000, false, now)
	if decision.Waiting {
		t.Fatal("due wait")
	}
	// Future + non-waiting drive: waiting outcome.
	decision = DecideRetryWait(5000, false, now)
	if !decision.Waiting {
		t.Fatal("future not waiting")
	}
	// Future + waiting drive: proceed (it waits inline).
	decision = DecideRetryWait(5000, true, now)
	if decision.Waiting {
		t.Fatal("waiting drive flagged waiting")
	}
}

func TestBuildRetryStart(t *testing.T) {
	scope := &session.OperationState{Control: session.Control{Status: "running"}}
	generationContext := jsonx.ObjFrom("stepId", "step-7")
	transition := BuildRetryStart("main", "run-1", "step-7", scope, generationContext, 3)
	next := transition.NextState
	if next.At != session.AtAssistantReady || next.NextAttempt != 3 {
		t.Fatalf("next = %+v", next)
	}
	if next.GenerationContext != generationContext {
		t.Fatal("generation context lost")
	}
	event := transition.Events[0]
	if EventType(event) != "retry_start" || event.MustGet("attempt") != float64(3) {
		t.Fatalf("event = %v", event)
	}
	if event.MustGet("step") != "step-7" || event.MustGet("runId") != "run-1" {
		t.Fatal("event fields")
	}
}

func TestRunRetryWaitWaitingDrive(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtAssistantRetryWait, "run")
	// The fixture arms cancel_requested; the retry path needs running
	// control for the planner to run.
	lane.State().Operation.State.Control.Status = "running"
	drive.WaitForRetry = true
	// notBefore in the future but close, so the inline wait is brief.
	lane.State().Operation.State.NotBefore = float64(time.Now().UnixMilli() + 30)
	lane.State().Operation.State.NextAttempt = 2
	lane.State().Operation.State.GenerationContext = jsonx.ObjFrom("stepId", "s1")
	// Refresh durable op.state to match.
	ctx := harnessBackground()
	if err := lane.session.SetValue(session.OperationStateValue("op-1"), operationStateToJSON(&lane.State().Operation.State), ctx); err != nil {
		t.Fatal(err)
	}
	result, err := RunRetryWait(lane, drive, &lane.State().Operation.State)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "continue" {
		t.Fatalf("result = %+v", result)
	}
	if lane.State().Operation.State.At != session.AtAssistantReady {
		t.Fatalf("at = %s", lane.State().Operation.State.At)
	}
	events := lane.DrainEvents()
	if len(events) != 1 || EventType(events[0]) != "retry_start" {
		t.Fatalf("events = %v", events)
	}
}

func TestRunRetryWaitNonWaitingDrive(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtAssistantRetryWait, "run")
	drive.WaitForRetry = false
	lane.State().Operation.State.NotBefore = float64(time.Now().UnixMilli() + 60000)
	result, err := RunRetryWait(lane, drive, &lane.State().Operation.State)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "waiting" {
		t.Fatalf("result = %+v", result)
	}
	outcome := result.Outcome.(*DriveOutcome)
	if outcome.Kind != DriveOutcomeWaitingRetry {
		t.Fatalf("outcome = %+v", outcome)
	}
	// No state change, no events.
	if lane.State().Operation.State.At != session.AtAssistantRetryWait {
		t.Fatal("state moved")
	}
	if len(lane.DrainEvents()) != 0 {
		t.Fatal("events emitted")
	}
}

func TestRunGenerationRouting(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtAssistantRetryWait, "run")
	// No ready procedure registered: the ready leaf errors.
	original := ReadyProcedure
	ReadyProcedure = nil
	defer func() { ReadyProcedure = original }()
	if _, err := RunGeneration(lane, drive, &session.OperationState{At: session.AtAssistantReady}); err == nil {
		t.Fatal("ready without procedure accepted")
	}
	// The retry-wait leaf routes to the retry path (non-waiting -> waiting).
	drive.WaitForRetry = false
	lane.State().Operation.State.NotBefore = float64(time.Now().UnixMilli() + 60000)
	result, err := RunGeneration(lane, drive, &lane.State().Operation.State)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "waiting" {
		t.Fatalf("result = %+v", result)
	}
	// Registered procedure runs.
	called := false
	ReadyProcedure = func(*Lane, *Drive, *session.OperationState) (*ProcedureResult, error) {
		called = true
		return &ProcedureResult{Kind: "continue"}, nil
	}
	lane.State().Operation.State.At = session.AtAssistantReady
	if _, err := RunGeneration(lane, drive, &lane.State().Operation.State); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("procedure not called")
	}
}
