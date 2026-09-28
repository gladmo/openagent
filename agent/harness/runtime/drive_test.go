package runtime

// Ports of drive dispatch behaviors: settled/waiting returns, no-progress
// invariant, cancel_requested routing to reconcile, AbortRequested wait.

import (
	"testing"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
)

type driveFixture struct {
	lane     *Lane
	sess     session.Session
	registry *DriveRegistry
	drive    *Drive
	gate     harness.Gate
	control  harness.GateControl
}

func newDriveFixture(t *testing.T, at string) *driveFixture {
	t.Helper()
	storage := session.NewMemoryStorage()
	metadata := session.SessionMetadata{ID: "s1", StorageVersion: 1}
	sess := session.NewStorageBackedSession(metadata, storage)
	tip := "tip-1"
	operation := &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-1", Lane: "main", StartedAt: 1},
		State: session.OperationState{At: at, Control: session.Control{Status: "running"}},
	}
	lane := NewLane(LaneOptions{
		Name:    "main",
		Session: sess,
		State: &RuntimeLaneState{
			TipID:     &tip,
			Operation: operation,
		},
	})
	gate, control := harness.CreateGate()
	return &driveFixture{
		lane:     lane,
		sess:     sess,
		registry: NewDriveRegistry(),
		drive:    &Drive{OperationID: "op-1", Gate: gate, Context: harness.BackgroundContext},
		gate:     gate,
		control:  control,
	}
}

func (f *driveFixture) advance(at string) {
	f.lane.State().Operation.State.At = at
}

func TestDriveDispatchSettled(t *testing.T) {
	f := newDriveFixture(t, session.AtStarting)
	f.registry.Register(session.AtStarting, func(lane *Lane, drive *Drive, state *session.OperationState) (*ProcedureResult, error) {
		return &ProcedureResult{Kind: "settled", Outcome: &session.OperationResultRecord{
			OperationID: "op-1", Kind: "run", Status: "completed", StartedAt: 1, EndedAt: 2,
		}}, nil
	})
	outcome, err := DriveOperation(f.lane, f.drive, f.registry)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != DriveOutcomeSettled || outcome.Outcome.Status != "completed" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestDriveDispatchWaiting(t *testing.T) {
	f := newDriveFixture(t, session.AtAssistantRetryWait)
	f.registry.Register(session.AtAssistantRetryWait, func(*Lane, *Drive, *session.OperationState) (*ProcedureResult, error) {
		return &ProcedureResult{Kind: "waiting", Outcome: &DriveOutcome{Kind: DriveOutcomeWaitingRetry}}, nil
	})
	outcome, err := DriveOperation(f.lane, f.drive, f.registry)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != DriveOutcomeWaitingRetry {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestDriveChainsContinueUntilTerminal(t *testing.T) {
	f := newDriveFixture(t, session.AtStarting)
	calls := []string{}
	f.registry.Register(session.AtStarting, func(lane *Lane, drive *Drive, state *session.OperationState) (*ProcedureResult, error) {
		calls = append(calls, "starting")
		lane.State().Operation.State.At = session.AtCheckpoint
		return &ProcedureResult{Kind: "continue"}, nil
	})
	f.registry.Register(session.AtCheckpoint, func(lane *Lane, drive *Drive, state *session.OperationState) (*ProcedureResult, error) {
		calls = append(calls, "checkpoint")
		return &ProcedureResult{Kind: "settled", Outcome: &session.OperationResultRecord{
			OperationID: "op-1", Kind: "run", Status: "completed",
		}}, nil
	})
	outcome, err := DriveOperation(f.lane, f.drive, f.registry)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != DriveOutcomeSettled {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(calls) != 2 || calls[0] != "starting" || calls[1] != "checkpoint" {
		t.Fatalf("calls = %v", calls)
	}
}

func TestDriveNoProgressInvariant(t *testing.T) {
	f := newDriveFixture(t, session.AtStarting)
	f.registry.Register(session.AtStarting, func(*Lane, *Drive, *session.OperationState) (*ProcedureResult, error) {
		// Continue without changing the state.
		return &ProcedureResult{Kind: "continue"}, nil
	})
	_, err := DriveOperation(f.lane, f.drive, f.registry)
	if err == nil {
		t.Fatal("no progress accepted")
	}
}

func TestDriveCancelRequestedRoutesToReconcile(t *testing.T) {
	f := newDriveFixture(t, session.AtStarting)
	f.lane.State().Operation.State.Control.Status = "cancel_requested"
	reconcileRan := false
	original := Reconcile
	Reconcile = func(lane *Lane, drive *Drive, state *session.OperationState) (*ProcedureResult, error) {
		reconcileRan = true
		return &ProcedureResult{Kind: "settled", Outcome: &session.OperationResultRecord{
			OperationID: "op-1", Kind: "run", Status: "aborted",
		}}, nil
	}
	defer func() { Reconcile = original }()

	startingRan := false
	f.registry.Register(session.AtStarting, func(*Lane, *Drive, *session.OperationState) (*ProcedureResult, error) {
		startingRan = true
		return &ProcedureResult{Kind: "continue"}, nil
	})
	outcome, err := DriveOperation(f.lane, f.drive, f.registry)
	if err != nil {
		t.Fatal(err)
	}
	if !reconcileRan || startingRan {
		t.Fatalf("reconcile = %v starting = %v", reconcileRan, startingRan)
	}
	if outcome.Outcome.Status != "aborted" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestDriveUnknownOperationMismatch(t *testing.T) {
	f := newDriveFixture(t, session.AtStarting)
	f.drive.OperationID = "other"
	f.registry.Register(session.AtStarting, func(*Lane, *Drive, *session.OperationState) (*ProcedureResult, error) {
		return &ProcedureResult{Kind: "continue"}, nil
	})
	if _, err := DriveOperation(f.lane, f.drive, f.registry); err == nil {
		t.Fatal("mismatched operation accepted")
	}
}

func TestDriveAbortRequestedWaitsCancellationThenContinues(t *testing.T) {
	f := newDriveFixture(t, session.AtStarting)
	cancellation := make(chan struct{})
	abortedOnce := false
	f.registry.Register(session.AtStarting, func(lane *Lane, drive *Drive, state *session.OperationState) (*ProcedureResult, error) {
		if !abortedOnce {
			abortedOnce = true
			// Release the cancellation before returning so the waiter is
			// not blocked forever.
			close(cancellation)
			return nil, &harness.AbortRequested{Cancellation: cancellation}
		}
		return &ProcedureResult{Kind: "settled", Outcome: &session.OperationResultRecord{
			OperationID: "op-1", Kind: "run", Status: "completed",
		}}, nil
	})
	outcome, err := DriveOperation(f.lane, f.drive, f.registry)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != DriveOutcomeSettled {
		t.Fatalf("outcome = %+v", outcome)
	}
}
