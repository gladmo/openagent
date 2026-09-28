package runtime

// Ports of deferred poll intent behaviors.

import (
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
)

func TestReserveDeferredIntentSuspended(t *testing.T) {
	suspended := &session.OperationState{At: session.AtDeferredSuspended, Poll: 2}
	ids := []string{"resp-1", "usage-1"}
	next := 0
	intent := ReserveDeferredIntent(suspended.At, suspended, func() string {
		id := ids[next]
		next++
		return id
	})
	// Suspended increments the poll.
	if intent.Poll != 3 {
		t.Fatalf("poll = %d", intent.Poll)
	}
	if intent.Repoll || intent.PrevResponseEntryID != "" {
		t.Fatal("suspended marked as repoll")
	}
	if intent.ResponseEntryID != "resp-1" || intent.UsageID != "usage-1" {
		t.Fatalf("ids = %s %s", intent.ResponseEntryID, intent.UsageID)
	}
}

func TestReserveDeferredIntentRepoll(t *testing.T) {
	effect := &session.OperationState{
		At:              session.AtDeferredEffectPending,
		Poll:            4,
		ResponseEntryID: "old-resp",
	}
	intent := ReserveDeferredIntent(effect.At, effect, func() string { return "x" })
	// Effect-pending keeps the poll but flags the repoll cleanup.
	if intent.Poll != 4 {
		t.Fatalf("poll = %d", intent.Poll)
	}
	if !intent.Repoll || intent.PrevResponseEntryID != "old-resp" {
		t.Fatalf("intent = %+v", intent)
	}
}

func TestDeferredEffectPendingState(t *testing.T) {
	scope := &session.OperationState{Control: session.Control{Status: "running"}}
	intent := &DeferredIntent{Poll: 5, ResponseEntryID: "r", UsageID: "u"}
	next := DeferredEffectPendingState(scope, intent)
	if next.At != session.AtDeferredEffectPending || next.Poll != 5 {
		t.Fatalf("next = %+v", next)
	}
	if next.ResponseEntryID != "r" || next.UsageID != "u" {
		t.Fatal("ids not carried")
	}
	if next.Control.Status != "running" {
		t.Fatal("scope lost")
	}
}

func TestDeferredIntentEvents(t *testing.T) {
	events := DeferredIntentEvents("main", "run-1", "step-2", 3, true)
	if len(events) != 2 {
		t.Fatalf("events = %d", len(events))
	}
	if EventType(events[0]) != "run_resume" || events[0].MustGet("recovery") != true {
		t.Fatalf("resume = %v", events[0])
	}
	if EventType(events[1]) != "turn_start" || events[1].MustGet("turnId") != "step-2:poll:3" {
		t.Fatalf("turn = %v", events[1])
	}
	if events[1].MustGet("recovery") != true {
		t.Fatal("turn recovery flag")
	}
	// Non-recovery omits the flags.
	events = DeferredIntentEvents("main", "run-1", "step-2", 3, false)
	if _, has := events[0].Get("recovery"); has {
		t.Fatal("recovery leaked")
	}
}

func TestPublishDeferredIntent(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtDeferredSuspended, "run")
	lane.State().Operation.State.Control.Status = "running"
	lane.State().Operation.State.Poll = 1
	lane.State().Operation.State.StepID = "step-9"
	ctx := harnessBackground()
	if err := lane.session.SetValue(session.OperationStateValue("op-1"), operationStateToJSON(&lane.State().Operation.State), ctx); err != nil {
		t.Fatal(err)
	}
	drive.PollDeferred = 1

	ids := []string{"resp-4", "usage-4"}
	next := 0
	intent, err := PublishDeferredIntent(lane, drive, &lane.State().Operation.State, "step-9", false, func() string {
		id := ids[next]
		next++
		return id
	})
	if err != nil || intent == nil {
		t.Fatalf("intent = %v err = %v", intent, err)
	}
	if intent.Poll != 2 {
		t.Fatalf("poll = %d", intent.Poll)
	}
	// State moved to effect_pending.
	if lane.State().Operation.State.At != session.AtDeferredEffectPending {
		t.Fatalf("at = %s", lane.State().Operation.State.At)
	}
	// Permit consumed.
	if drive.PollDeferred != 0 {
		t.Fatalf("permits = %d", drive.PollDeferred)
	}
	// run_resume + turn_start events.
	events := lane.DrainEvents()
	if len(events) != 2 || EventType(events[0]) != "run_resume" || EventType(events[1]) != "turn_start" {
		t.Fatalf("events = %v", events)
	}
	if events[1].MustGet("turnId") != "step-9:poll:2" {
		t.Fatalf("turnId = %v", events[1].MustGet("turnId"))
	}
}
