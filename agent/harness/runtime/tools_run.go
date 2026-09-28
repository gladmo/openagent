package runtime

// tools_run.go ports harness/runtime/drive/tools.ts's runTools dispatch
// decisions: recovery detection, the sequential bounded-transition rule,
// the active-tool filter, and the execution-mode routing. Invocation
// start/recover bodies ride on the execution/tools port.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// MaxSequentialTransitions mirrors the bounded transition count for a
// sequential batch.
const MaxSequentialTransitions = 10000

// IsRecoveryBatch mirrors the recovery detection: any effect_pending or
// outcome_ready call means recovery.
func IsRecoveryBatch(batch *jsonx.Obj) bool {
	for _, call := range batchCalls(batch) {
		status, _ := call.Get("status")
		if status == "effect_pending" || status == "outcome_ready" {
			return true
		}
	}
	return false
}

// RecoveryTurnStartEvent builds the recovery turn_start event.
func RecoveryTurnStartEvent(lane, runID, turnID string) HarnessEvent {
	event := eventLane(lane, "turn_start")
	event.Set("runId", runID)
	event.Set("turnId", turnID)
	event.Set("recovery", true)
	return event
}

// ActiveToolsFilter mirrors the active-tool filter: only tools named in
// the batch configuration's activeToolNames survive.
type ToolWithName interface {
	NameOf() string
}

// FilterActiveTools filters tool descriptors by the batch's active names.
func FilterActiveTools[T any](tools []T, active []string, nameOf func(T) string) []T {
	activeSet := map[string]bool{}
	for _, name := range active {
		activeSet[name] = true
	}
	out := make([]T, 0, len(tools))
	for _, tool := range tools {
		if activeSet[nameOf(tool)] {
			out = append(out, tool)
		}
	}
	return out
}

// BatchActiveNames reads the batch configuration's activeToolNames.
func BatchActiveNames(batch *jsonx.Obj) []string {
	configurationValue, ok := batch.Get("configuration")
	if !ok {
		return nil
	}
	configuration, ok := configurationValue.(*jsonx.Obj)
	if !ok {
		return nil
	}
	namesValue, ok := configuration.Get("activeToolNames")
	if !ok {
		return nil
	}
	names, _ := namesValue.([]any)
	out := make([]string, 0, len(names))
	for _, name := range names {
		if s, ok := name.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ExecutionMode mirrors the routing decision.
type ExecutionMode string

const (
	ExecutionSequential ExecutionMode = "sequential"
	ExecutionParallel   ExecutionMode = "parallel"
)

// ResolveExecutionMode mirrors the settings routing: sequential or
// parallel from the captured run settings.
func ResolveExecutionMode(settings *jsonx.Obj) ExecutionMode {
	if settings != nil {
		if v, ok := settings.Get("toolExecution"); ok {
			if s, ok := v.(string); ok && s == "sequential" {
				return ExecutionSequential
			}
		}
	}
	return ExecutionParallel
}

// SequentialPlan captures the sequential loop's per-call disposition.
type SequentialPlan struct {
	// Transitions counts the bounded loop iterations.
	Transitions int
	// CallsToRun are the calls needing start or recovery, in order.
	CallsToRun []*jsonx.Obj
}

// PlanSequential mirrors runSequential's selection: completed and
// outcome_ready calls skip; planned calls start; effect_pending calls
// recover; the transition budget bounds the loop.
func PlanSequential(batch *jsonx.Obj) (*SequentialPlan, error) {
	plan := &SequentialPlan{}
	for _, call := range batchCalls(batch) {
		status, _ := call.Get("status")
		switch status {
		case "completed", "outcome_ready":
			continue
		case "planned", "effect_pending":
			plan.CallsToRun = append(plan.CallsToRun, call)
			plan.Transitions++
			if plan.Transitions > MaxSequentialTransitions {
				return nil, &session.SessionInvariantError{Message: "Sequential tool batch exceeded its bounded transition count"}
			}
		}
	}
	return plan, nil
}
