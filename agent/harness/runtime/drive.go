package runtime

// drive.go ports harness/runtime/drive.ts: the dispatcher that drives one
// installed pass through direct durable procedures until settlement or a
// durable wait. The per-state procedures are injectable so the pipeline
// files can land incrementally.

import (
	"fmt"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
)

type jsonxStringifyFn = func(any) string

// ProcedureResult mirrors the TS union.
type ProcedureResult struct {
	Kind    string // "continue" | "waiting" | "settled"
	Outcome any    // DriveOutcome for waiting; OperationResultRecord for settled
}

// DriveOutcomeKind values.
const (
	DriveOutcomeSettled         = "settled"
	DriveOutcomeWaitingRetry    = "waiting_retry"
	DriveOutcomeWaitingDeferred = "waiting_deferred"
)

// DriveOutcome mirrors the TS union.
type DriveOutcome struct {
	Kind    string // settled | waiting_retry | waiting_deferred
	Outcome *session.OperationResultRecord
}

// Drive mirrors the TS interface: one installed drive pass.
type Drive struct {
	OperationID string
	Gate        harness.Gate
	Context     harness.Context
	// WaitForRetry holds the drive in retry backoff when true.
	WaitForRetry bool
	// PollDeferred permits deferred polling when set.
	PollDeferred int
}

// DriveProcedure executes one state's durable procedure.
type DriveProcedure func(lane *Lane, drive *Drive, state *session.OperationState) (*ProcedureResult, error)

// DriveRegistry maps state.at -> procedure.
type DriveRegistry struct {
	procedures map[string]DriveProcedure
}

// NewDriveRegistry builds an empty registry.
func NewDriveRegistry() *DriveRegistry {
	return &DriveRegistry{procedures: map[string]DriveProcedure{}}
}

// Register installs a procedure for a state leaf.
func (r *DriveRegistry) Register(at string, procedure DriveProcedure) {
	r.procedures[at] = procedure
}

// Reconcile handles cancel_requested dispatch.
var Reconcile DriveProcedure

// BeforeDriveHook runs before the first pass while control is running.
// Returning an AbortRequested error waits cancellation then continues.
var BeforeDriveHook func(lane *Lane, drive *Drive) error

func currentOperation(lane *Lane, drive *Drive) (*session.Operation, error) {
	operation := lane.State().Operation
	if operation == nil || operation.Meta.OperationID != drive.OperationID {
		return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Drive %s has no matching current operation", drive.OperationID)}
	}
	return operation, nil
}

// DriveOperation drives one installed pass through procedures until
// settlement or a durable wait.
func DriveOperation(lane *Lane, drive *Drive, registry *DriveRegistry) (*DriveOutcome, error) {
	operation, err := currentOperation(lane, drive)
	if err != nil {
		return nil, err
	}
	if operation.State.Control.Status == "running" && BeforeDriveHook != nil {
		if err := BeforeDriveHook(lane, drive); err != nil {
			if _, ok := err.(*harness.AbortRequested); !ok {
				return nil, err
			}
			<-err.(*harness.AbortRequested).Cancellation
		}
	}

	for {
		operation, err = currentOperation(lane, drive)
		if err != nil {
			return nil, err
		}
		// Snapshot by value: procedures mutate the lane's operation in
		// place, so a pointer view cannot detect progress.
		stateCopy := operation.State
		state := &stateCopy
		var result *ProcedureResult
		if state.Control.Status == "cancel_requested" && Reconcile != nil {
			result, err = Reconcile(lane, drive, state)
		} else {
			procedure, found := registry.procedures[state.At]
			if !found {
				return nil, &session.SessionInvariantError{Message: fmt.Sprintf("No drive procedure registered for %s", state.At)}
			}
			result, err = procedure(lane, drive, state)
		}
		fromAbort := false
		if err != nil {
			if abort, ok := err.(*harness.AbortRequested); ok {
				<-abort.Cancellation
				result = &ProcedureResult{Kind: "continue"}
				fromAbort = true
			} else {
				return nil, err
			}
		}

		switch result.Kind {
		case "settled":
			return &DriveOutcome{Kind: DriveOutcomeSettled, Outcome: result.Outcome.(*session.OperationResultRecord)}, nil
		case "waiting":
			return result.Outcome.(*DriveOutcome), nil
		}
		// continue: verify progress.
		nextOp, err := currentOperation(lane, drive)
		if err != nil {
			return nil, err
		}
		if fromAbort {
			// The cancellation wait is itself progress; re-dispatch.
			continue
		}
		next := &nextOp.State
		// Compare the serialized forms to detect no-progress (struct
		// pointers never compare equal by value).
		if jsonxStringifyLocal(operationStateToJSON(next)) == jsonxStringifyLocal(operationStateToJSON(state)) && next.Control.Status != "cancel_requested" {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Drive procedure made no progress from %s", state.At)}
		}
	}
}

func jsonxStringifyLocal(v any) string { return jsonxStringifyAlias(v) }
