package runtime

// state_machine_defects_test.go pins review fixes: captured inbox items
// materialize at accept (StartRun resolves them), the durable branch tip
// advances with them, lastOperationId survives queue/accept writes, the
// queue_update events carry the post-commit queues, and operation state
// round trips through every field.

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"

	session "github.com/gladmo/openagent/agent/harness/session"
)

func TestAcceptCapturedSteerMaterializesAndRuns(t *testing.T) {
	lane, sess := newAcceptLane(t)
	ctx := harnessBackground()

	queued, err := QueueMessage(lane, "steer", "change direction", nil, ctx)
	if err != nil {
		t.Fatal(err)
	}

	result, err := AcceptRun(lane, nil, "all", "all", acceptNextID(), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Admitted {
		t.Fatalf("accept result = %+v", result)
	}

	// The captured item is a materialized message entry, not an orphaned
	// pending payload.
	entries, err := sess.GetEntries([]string{queued.EntryID}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := entries[queued.EntryID]
	if !ok || entry.Type != session.EntryTypeMessage {
		t.Fatalf("captured entry = %+v", entry)
	}
	stored, err := sess.GetValue(session.PendingEntryValue(queued.EntryID), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored != nil {
		t.Fatal("pending payload not consumed")
	}

	// The durable tip advanced to the captured entry.
	tip, err := sess.GetValue(session.BranchTip("main"), ctx)
	if err != nil || tip == nil || tip.Value != queued.EntryID {
		t.Fatalf("durable tip = %+v err = %v", tip, err)
	}
	if id := lane.State().TipID; id == nil || *id != queued.EntryID {
		t.Fatalf("lane tip = %v", id)
	}

	// StartRun resolves the intent without the "missing its message"
	// invariant error.
	runResult, err := StartRun(lane, &Drive{OperationID: result.OperationID, Context: ctx}, &lane.State().Operation.State)
	if err != nil {
		t.Fatalf("StartRun failed: %v", err)
	}
	if runResult.Kind != "continue" {
		t.Fatalf("run result = %+v", runResult)
	}
}

func TestAcceptPreservesLastOperationId(t *testing.T) {
	lane, sess := newAcceptLane(t)
	ctx := harnessBackground()
	previous := "op-earlier"
	lane.State().LastOperationID = &previous

	prompt := AcceptPrompt{ID: "p-1", Message: jsonx.ObjFrom("role", "user", "content", "hi", "timestamp", float64(1))}
	if _, err := AcceptRun(lane, []AcceptPrompt{prompt}, "all", "all", acceptNextID(), ctx); err != nil {
		t.Fatal(err)
	}
	state, err := sess.GetValue(session.LaneStateValue("main"), ctx)
	if err != nil || state == nil {
		t.Fatalf("lane state = %v err = %v", state, err)
	}
	laneState := state.Value.(*jsonx.Obj)
	if last := laneState.MustGet("lastOperationId"); last != previous {
		t.Fatalf("lastOperationId = %v, want %s", last, previous)
	}
}

func TestQueueWritesPreserveLastOperationIdAndEventQueues(t *testing.T) {
	lane, sess := newAcceptLane(t)
	ctx := harnessBackground()
	previous := "op-earlier"
	lane.State().LastOperationID = &previous

	queued, err := QueueMessage(lane, "steer", "turn left", nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	state, err := sess.GetValue(session.LaneStateValue("main"), ctx)
	if err != nil || state == nil {
		t.Fatalf("lane state = %v err = %v", state, err)
	}
	laneState := state.Value.(*jsonx.Obj)
	if last := laneState.MustGet("lastOperationId"); last != previous {
		t.Fatalf("lastOperationId = %v, want %s", last, previous)
	}

	// The queue_update event shows the message just queued.
	events := lane.DrainEvents()
	if len(events) == 0 {
		t.Fatal("no queue_update event")
	}
	queues := events[0].MustGet("queues").(*jsonx.Obj)
	steering := queues.MustGet("steering").([]any)
	if len(steering) != 1 {
		t.Fatalf("steering queue = %v", steering)
	}
	item := steering[0].(*jsonx.Obj)
	if item.MustGet("entryId") != queued.EntryID {
		t.Fatalf("queued item = %v", item)
	}
}

func TestCancelQueuedEventCarriesQueues(t *testing.T) {
	lane, _ := newAcceptLane(t)
	ctx := harnessBackground()
	first, err := QueueMessage(lane, "steer", "one", nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := QueueMessage(lane, "steer", "two", nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	lane.DrainEvents()

	if _, err := CancelQueued(lane, first.EntryID, ctx); err != nil {
		t.Fatal(err)
	}
	events := lane.DrainEvents()
	if len(events) == 0 {
		t.Fatal("no queue_update event on cancel")
	}
	queues, ok := events[0].Get("queues")
	if !ok {
		t.Fatal("queue_update event missing its queues payload")
	}
	steering := queues.(*jsonx.Obj).MustGet("steering").([]any)
	if len(steering) != 1 || steering[0].(*jsonx.Obj).MustGet("entryId") != second.EntryID {
		t.Fatalf("steering after cancel = %v", steering)
	}
}

func TestOperationStateRoundTripsEveryField(t *testing.T) {
	pointer := func(s string) *string { return &s }
	state := &session.OperationState{
		At:                     session.AtAssistantEffectPending,
		Control:                session.Control{Status: "running"},
		Settings:               jsonx.ObjFrom("steeringMode", "all"),
		LatestAssistantEntryID: pointer("entry-latest"),
		Continuation:           jsonx.ObjFrom("kind", "need_assistant"),
		TriggerEntryID:         "entry-trigger",
		GenerationContext:      jsonx.ObjFrom("stepId", "s-1", "triggerEntryId", "entry-trigger"),
		Attempt:                2,
		NextAttempt:            3,
		NotBefore:              12345.6,
		ErrorMessage:           "boom",
		ResponseEntryID:        "entry-response",
		UsageID:                "usage-9",
		IntendedOutputLimit:    4096,
		ContextWindow:          200000,
		Batch:                  jsonx.ObjFrom("calls", []any{}),
		StepID:                 "step-7",
		SourceEntryID:          "entry-source",
		Poll:                   4,
		Configuration:          jsonx.ObjFrom("model", "m1"),
		StreamOptions:          jsonx.ObjFrom("transport", "sse"),
		Task:                   jsonx.ObjFrom("kind", "gen"),
		SummaryContext:         jsonx.ObjFrom("entries", []any{}),
		TargetID:               pointer("entry-target"),
		Label:                  pointer("the-label"),
	}
	restored, err := operationStateFromValue(operationStateToJSON(state))
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case restored.At != state.At:
		t.Fatalf("At = %s", restored.At)
	case restored.Control.Status != state.Control.Status:
		t.Fatalf("Control = %+v", restored.Control)
	case restored.Settings == nil || restored.Settings.MustGet("steeringMode") != "all":
		t.Fatalf("Settings = %v", restored.Settings)
	case restored.LatestAssistantEntryID == nil || *restored.LatestAssistantEntryID != "entry-latest":
		t.Fatalf("LatestAssistantEntryID = %v", restored.LatestAssistantEntryID)
	case restored.Continuation == nil || restored.Continuation.MustGet("kind") != "need_assistant":
		t.Fatalf("Continuation = %v", restored.Continuation)
	case restored.TriggerEntryID != "entry-trigger":
		t.Fatalf("TriggerEntryID = %s", restored.TriggerEntryID)
	case restored.GenerationContext == nil || restored.GenerationContext.MustGet("stepId") != "s-1":
		t.Fatalf("GenerationContext = %v", restored.GenerationContext)
	case restored.Attempt != 2 || restored.NextAttempt != 3:
		t.Fatalf("attempts = %d/%d", restored.Attempt, restored.NextAttempt)
	case restored.NotBefore != 12345.6:
		t.Fatalf("NotBefore = %v", restored.NotBefore)
	case restored.ErrorMessage != "boom":
		t.Fatalf("ErrorMessage = %s", restored.ErrorMessage)
	case restored.ResponseEntryID != "entry-response" || restored.UsageID != "usage-9":
		t.Fatalf("response/usage = %s/%s", restored.ResponseEntryID, restored.UsageID)
	case restored.IntendedOutputLimit != 4096 || restored.ContextWindow != 200000:
		t.Fatalf("limits = %v/%v", restored.IntendedOutputLimit, restored.ContextWindow)
	case restored.Batch == nil || restored.StepID != "step-7" || restored.SourceEntryID != "entry-source" || restored.Poll != 4:
		t.Fatalf("batch/step/source/poll = %v/%s/%s/%d", restored.Batch, restored.StepID, restored.SourceEntryID, restored.Poll)
	case restored.Configuration == nil || restored.StreamOptions == nil:
		t.Fatalf("configuration/streamOptions = %v/%v", restored.Configuration, restored.StreamOptions)
	case restored.Task == nil || restored.SummaryContext == nil:
		t.Fatalf("task/summary = %v/%v", restored.Task, restored.SummaryContext)
	case restored.TargetID == nil || *restored.TargetID != "entry-target":
		t.Fatalf("TargetID = %v", restored.TargetID)
	case restored.Label == nil || *restored.Label != "the-label":
		t.Fatalf("Label = %v", restored.Label)
	}
}
