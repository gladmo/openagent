package runtime

// Ports of publishGenerationIntent behaviors.

import (
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func TestReserveGenerationIntent(t *testing.T) {
	ready := &session.OperationState{NextAttempt: 2, GenerationContext: jsonx.ObjFrom("stepId", "s1")}
	model := &ai.Model{MaxTokens: 4096, ContextWindow: 128000}
	ids := []string{"resp-9", "usage-9"}
	next := 0
	intent := ReserveGenerationIntent(ready, model, func() string {
		id := ids[next]
		next++
		return id
	}, func() string {
		id := ids[next]
		next++
		return id
	})
	if intent.ResponseEntryID != "resp-9" || intent.UsageID != "usage-9" {
		t.Fatalf("ids = %s %s", intent.ResponseEntryID, intent.UsageID)
	}
	if intent.IntendedOutputLimit != 4096 || intent.ContextWindow != 128000 {
		t.Fatalf("limits = %v %v", intent.IntendedOutputLimit, intent.ContextWindow)
	}
	if intent.Attempt != 2 {
		t.Fatalf("attempt = %d", intent.Attempt)
	}
	// Nil model leaves the limits at zero.
	intent = ReserveGenerationIntent(ready, nil, func() string { return "x" }, func() string { return "y" })
	if intent.IntendedOutputLimit != 0 || intent.ContextWindow != 0 {
		t.Fatal("nil model limits")
	}
}

func TestEffectPendingState(t *testing.T) {
	scope := &session.OperationState{Control: session.Control{Status: "running"}}
	intent := &GenerationIntent{
		ResponseEntryID:     "resp-1",
		UsageID:             "usage-1",
		IntendedOutputLimit: 100,
		ContextWindow:       200,
		Attempt:             1,
		GenerationContext:   jsonx.ObjFrom("stepId", "s1"),
	}
	next := EffectPendingState(scope, intent)
	if next.At != session.AtAssistantEffectPending {
		t.Fatalf("at = %s", next.At)
	}
	if next.ResponseEntryID != "resp-1" || next.UsageID != "usage-1" {
		t.Fatal("ids not carried")
	}
	if next.IntendedOutputLimit != 100 || next.ContextWindow != 200 {
		t.Fatal("limits not carried")
	}
	if next.Control.Status != "running" {
		t.Fatal("scope lost")
	}
}

func TestIntentEvents(t *testing.T) {
	// First attempt emits turn_start.
	events := IntentEvents("main", "run-1", "step-3", 1)
	if len(events) != 1 || EventType(events[0]) != "turn_start" {
		t.Fatalf("events = %v", events)
	}
	if events[0].MustGet("turnId") != "step-3" || events[0].MustGet("runId") != "run-1" {
		t.Fatal("event fields")
	}
	// Later attempts emit nothing.
	if events := IntentEvents("main", "run-1", "step-3", 2); events != nil {
		t.Fatalf("events = %v", events)
	}
}

func TestPublishGenerationIntent(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtAssistantReady, "run")
	lane.State().Operation.State.Control.Status = "running"
	lane.State().Operation.State.NextAttempt = 1
	lane.State().Operation.State.GenerationContext = jsonx.ObjFrom("stepId", "s1")
	ctx := harnessBackground()
	if err := lane.session.SetValue(session.OperationStateValue("op-1"), operationStateToJSON(&lane.State().Operation.State), ctx); err != nil {
		t.Fatal(err)
	}

	ids := []string{"resp-7", "usage-7"}
	next := 0
	intent, err := PublishGenerationIntent(lane, drive, &lane.State().Operation.State, &ai.Model{MaxTokens: 500, ContextWindow: 1000}, func() string {
		id := ids[next]
		next++
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	if intent == nil {
		t.Fatal("intent nil")
	}
	if intent.ResponseEntryID != "resp-7" || intent.UsageID != "usage-7" {
		t.Fatalf("ids = %s %s", intent.ResponseEntryID, intent.UsageID)
	}
	// The lane state moved to effect_pending.
	if lane.State().Operation.State.At != session.AtAssistantEffectPending {
		t.Fatalf("at = %s", lane.State().Operation.State.At)
	}
	if lane.State().Operation.State.ResponseEntryID != "resp-7" {
		t.Fatal("response entry id not in state")
	}
	// First attempt emitted turn_start.
	events := lane.DrainEvents()
	if len(events) != 1 || EventType(events[0]) != "turn_start" {
		t.Fatalf("events = %v", events)
	}
}
