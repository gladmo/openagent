package runtime

// Ports of commitNavigation + publishConfigurationFailure.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
)

func navTarget(id string) *string { return &id }

func TestValidateNavigation(t *testing.T) {
	// Valid: existing target differing from the source tip.
	if err := ValidateNavigation(navTarget("e5"), nil, true, navTarget("e1")); err != nil {
		t.Fatal(err)
	}
	// Missing target.
	if err := ValidateNavigation(navTarget("ghost"), nil, false, navTarget("e1")); err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("err = %v", err)
	}
	// Target equals source tip.
	if err := ValidateNavigation(navTarget("e1"), nil, true, navTarget("e1")); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("err = %v", err)
	}
	// Root with a label.
	if err := ValidateNavigation(nil, navTarget("home"), true, navTarget("e1")); err == nil || !strings.Contains(err.Error(), "cannot set a label") {
		t.Fatalf("err = %v", err)
	}
	// Root without a label is fine; nil source tip is fine.
	if err := ValidateNavigation(nil, nil, false, nil); err != nil {
		t.Fatal(err)
	}
}

func TestNavigationWrites(t *testing.T) {
	// Target + label.
	writes := NavigationWrites("main", navTarget("e5"), navTarget("home"))
	if len(writes) != 2 {
		t.Fatalf("writes = %d", len(writes))
	}
	if writes[0].Namespace != "pi.branch.tip" || writes[0].Key != "main" || writes[0].Value != "e5" {
		t.Fatalf("tip write = %+v", writes[0])
	}
	if writes[1].Namespace != "pi.entry.label" || writes[1].Key != "e5" || writes[1].Value != "home" {
		t.Fatalf("label write = %+v", writes[1])
	}
	// Root without label: single tip write with nil.
	writes = NavigationWrites("main", nil, nil)
	if len(writes) != 1 || writes[0].Value != nil {
		t.Fatalf("writes = %+v", writes)
	}
	// Target without label: no label write.
	writes = NavigationWrites("main", navTarget("e9"), nil)
	if len(writes) != 1 {
		t.Fatalf("writes = %d", len(writes))
	}
}

func TestNavigationEndEvent(t *testing.T) {
	event := NavigationEndEvent("main", "run-1", "completed", navTarget("e5"), 1234)
	if EventType(event) != "navigation_end" || event.MustGet("status") != "completed" {
		t.Fatalf("event = %v", event)
	}
	if event.MustGet("tipId") != "e5" || event.MustGet("endedAt") != float64(1234) {
		t.Fatal("event fields")
	}
	// Root target carries null tipId.
	event = NavigationEndEvent("main", "run-1", "completed", nil, 1234)
	if event.MustGet("tipId") != nil {
		t.Fatal("root tipId not null")
	}
}

func TestCommitNavigationEndToEnd(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtNavigationReadyToCommit, "navigation")
	lane.State().Operation.State.Control.Status = "running"
	target := "tip-root" // the fixture's committed root entry
	lane.State().Operation.State.TargetID = &target
	// The fixture's source tip is also tip-root; the invariant demands a
	// different source, so retarget the lane copy's meta.
	otherSource := "elsewhere"
	lane.State().Operation.Meta.SourceTipID = &otherSource
	ctx := harnessBackground()
	if err := lane.session.SetValue(session.OperationStateValue("op-1"), operationStateToJSON(&lane.State().Operation.State), ctx); err != nil {
		t.Fatal(err)
	}
	result, err := CommitNavigation(lane, drive, &lane.State().Operation.State)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "settled" {
		t.Fatalf("result = %+v", result)
	}
	// Operation cleared; tip moved.
	if lane.State().Operation != nil {
		t.Fatal("operation not cleared")
	}
	tip, _ := lane.session.GetValue(session.BranchTip("main"), ctx)
	if tip == nil || tip.Value != target {
		t.Fatalf("tip = %v", tip.Value)
	}
	// navigation_end completed event.
	events := lane.DrainEvents()
	found := false
	for _, event := range events {
		if EventType(event) == "navigation_end" && event.MustGet("status") == "completed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %v", events)
	}
}

func TestCommitNavigationMissingTarget(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtNavigationReadyToCommit, "navigation")
	lane.State().Operation.State.Control.Status = "running"
	ghost := "ghost"
	lane.State().Operation.State.TargetID = &ghost
	ctx := harnessBackground()
	if err := lane.session.SetValue(session.OperationStateValue("op-1"), operationStateToJSON(&lane.State().Operation.State), ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitNavigation(lane, drive, &lane.State().Operation.State); err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublishConfigurationFailure(t *testing.T) {
	lane, _, drive := newReconcileEnv(t, session.AtAssistantReady, "run")
	lane.State().Operation.State.Control.Status = "running"
	failure := &session.OperationError{Code: "model_unavailable", Message: "The configured model is unavailable in this process"}
	result, err := PublishConfigurationFailure(lane, drive, &lane.State().Operation.State, failure)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "settled" {
		t.Fatalf("result = %+v", result)
	}
	record := result.Outcome.(*session.OperationResultRecord)
	if record.Status != session.StatusFailed || record.Error == nil || record.Error.Code != "model_unavailable" {
		t.Fatalf("record = %+v", record)
	}
	// Operation cleared + run_end failed event.
	if lane.State().Operation != nil {
		t.Fatal("operation not cleared")
	}
	events := lane.DrainEvents()
	found := false
	for _, event := range events {
		if EventType(event) == "run_end" && event.MustGet("status") == "failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %v", events)
	}
}
