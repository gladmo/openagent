package runtime

// Ports of runTools dispatch decisions.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func TestIsRecoveryBatch(t *testing.T) {
	plannedOnly := jsonx.ObjFrom("calls", []any{
		jsonx.ObjFrom("status", "planned"),
	})
	if IsRecoveryBatch(plannedOnly) {
		t.Fatal("planned-only counted as recovery")
	}
	withPending := jsonx.ObjFrom("calls", []any{
		jsonx.ObjFrom("status", "planned"),
		jsonx.ObjFrom("status", "effect_pending"),
	})
	if !IsRecoveryBatch(withPending) {
		t.Fatal("effect_pending not recovery")
	}
	withReady := jsonx.ObjFrom("calls", []any{
		jsonx.ObjFrom("status", "outcome_ready"),
	})
	if !IsRecoveryBatch(withReady) {
		t.Fatal("outcome_ready not recovery")
	}
}

func TestRecoveryTurnStartEvent(t *testing.T) {
	event := RecoveryTurnStartEvent("main", "run-1", "turn-9")
	if EventType(event) != "turn_start" || event.MustGet("recovery") != true {
		t.Fatalf("event = %v", event)
	}
	if event.MustGet("runId") != "run-1" || event.MustGet("turnId") != "turn-9" || event.MustGet("lane") != "main" {
		t.Fatal("event fields")
	}
}

func TestFilterActiveTools(t *testing.T) {
	type tool struct{ name string }
	tools := []tool{{"bash"}, {"ghost"}, {"read"}}
	active := BatchActiveNames(jsonx.ObjFrom("configuration", jsonx.ObjFrom("activeToolNames", []any{"bash", "read"})))
	filtered := FilterActiveTools(tools, active, func(t tool) string { return t.name })
	if len(filtered) != 2 || filtered[0].name != "bash" || filtered[1].name != "read" {
		t.Fatalf("filtered = %v", filtered)
	}
}

func TestBatchActiveNames(t *testing.T) {
	batch := jsonx.ObjFrom("configuration", jsonx.ObjFrom("activeToolNames", []any{"a", "b"}))
	names := BatchActiveNames(batch)
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("names = %v", names)
	}
	// Missing configuration.
	if names := BatchActiveNames(jsonx.NewObj()); names != nil {
		t.Fatalf("names = %v", names)
	}
}

func TestResolveExecutionMode(t *testing.T) {
	if mode := ResolveExecutionMode(jsonx.ObjFrom("toolExecution", "sequential")); mode != ExecutionSequential {
		t.Fatalf("mode = %s", mode)
	}
	if mode := ResolveExecutionMode(jsonx.ObjFrom("toolExecution", "parallel")); mode != ExecutionParallel {
		t.Fatalf("mode = %s", mode)
	}
	// Absent setting defaults to parallel.
	if mode := ResolveExecutionMode(nil); mode != ExecutionParallel {
		t.Fatalf("mode = %s", mode)
	}
	if mode := ResolveExecutionMode(jsonx.NewObj()); mode != ExecutionParallel {
		t.Fatalf("mode = %s", mode)
	}
}

func TestPlanSequential(t *testing.T) {
	batch := jsonx.ObjFrom("calls", []any{
		jsonx.ObjFrom("status", "completed", "resultEntryId", "r0"),
		jsonx.ObjFrom("status", "planned", "resultEntryId", "r1"),
		jsonx.ObjFrom("status", "outcome_ready", "resultEntryId", "r2"),
		jsonx.ObjFrom("status", "effect_pending", "resultEntryId", "r3"),
	})
	plan, err := PlanSequential(batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.CallsToRun) != 2 || plan.Transitions != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.CallsToRun[0].MustGet("resultEntryId") != "r1" || plan.CallsToRun[1].MustGet("resultEntryId") != "r3" {
		t.Fatal("call selection wrong")
	}
}

func TestPlanSequentialBudgetExceeded(t *testing.T) {
	calls := make([]any, 0, MaxSequentialTransitions+1)
	for i := 0; i <= MaxSequentialTransitions; i++ {
		calls = append(calls, jsonx.ObjFrom("status", "planned"))
	}
	batch := jsonx.ObjFrom("calls", calls)
	_, err := PlanSequential(batch)
	if err == nil || !strings.Contains(err.Error(), "bounded transition count") {
		t.Fatalf("err = %v", err)
	}
}
