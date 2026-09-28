package runtime

// Ports of drive/boundary.test.ts behaviors (representative cases).

import (
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

type boundaryEnv struct {
	sess session.Session
}

func newBoundaryEnv(t *testing.T) *boundaryEnv {
	t.Helper()
	storage := session.NewMemoryStorage()
	metadata := session.SessionMetadata{ID: "s1", StorageVersion: 1}
	sess := session.NewStorageBackedSession(metadata, storage)
	return &boundaryEnv{sess: sess}
}

// seedPending stores one pending payload under its entry id.
func (e *boundaryEnv) seedPending(t *testing.T, entryID string, payload string) session.InboxItem {
	t.Helper()
	ctx := harnessBackground()
	if err := e.sess.SetValue(session.PendingEntryValue(entryID), jsonx.MustParseString(payload), ctx); err != nil {
		t.Fatal(err)
	}
	// Determine kind from the payload shape for inbox construction.
	kind := "steer"
	item := session.InboxItem{EntryID: entryID, Kind: kind}
	return item
}

func TestPlanBoundaryInboxSteerSelection(t *testing.T) {
	env := newBoundaryEnv(t)
	ctx := harnessBackground()

	// Two steers + one write in the inbox.
	first := env.seedPending(t, "s1", `{"type":"message","payload":{"role":"user","content":"one"}}`)
	second := env.seedPending(t, "s2", `{"type":"message","payload":{"role":"user","content":"two"}}`)
	write := session.InboxItem{EntryID: "w1", Kind: "write"}
	env.seedPending(t, "w1", `{"type":"custom","customType":"file.write","payload":{"path":"x"}}`)
	first.EntryID, second.EntryID = "s1", "s2"
	first.Kind, second.Kind = "steer", "steer"
	inbox := []session.InboxItem{first, second, write}
	tip := "tip-1"

	// one-at-a-time: first steer + write.
	placement, err := PlanBoundaryInbox(inbox, "one-at-a-time", "one-at-a-time", env.sess, &tip, false, map[string]bool{"file.write": false}, "main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(placement.Entries) != 2 || placement.Entries[0].ID != "s1" || placement.Entries[1].ID != "w1" {
		ids := []string{}
		for _, entry := range placement.Entries {
			ids = append(ids, entry.ID)
		}
		t.Fatalf("entries = %v", ids)
	}
	// Trigger is the steer (a message projects).
	if !placement.HasTrigger || placement.TriggerEntryID != "s1" {
		t.Fatalf("trigger = %v", placement.TriggerEntryID)
	}
	// Remainder keeps the unselected steer.
	if len(placement.Inbox) != 1 || placement.Inbox[0].EntryID != "s2" {
		t.Fatalf("inbox = %+v", placement.Inbox)
	}
	// Tip advanced to the last entry; chained parents.
	if placement.TipID == nil || *placement.TipID != "w1" {
		t.Fatalf("tip = %v", placement.TipID)
	}
	if *placement.Entries[0].ParentID != "tip-1" || *placement.Entries[1].ParentID != "s1" {
		t.Fatal("chain broken")
	}

	// all-mode: both steers selected.
	placement, err = PlanBoundaryInbox(inbox, "all", "all", env.sess, &tip, false, map[string]bool{"file.write": false}, "main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(placement.Entries) != 3 {
		t.Fatalf("entries = %d", len(placement.Entries))
	}
	if len(placement.Inbox) != 0 {
		t.Fatalf("inbox = %+v", placement.Inbox)
	}
}

func TestPlanBoundaryInboxFollowUpWhenNoTrigger(t *testing.T) {
	env := newBoundaryEnv(t)
	ctx := harnessBackground()

	// Only a write (does not project) at the boundary.
	write := session.InboxItem{EntryID: "w1", Kind: "write"}
	env.seedPending(t, "w1", `{"type":"custom","customType":"file.write","payload":{"path":"x"}}`)
	followUp := session.InboxItem{EntryID: "f1", Kind: "followUp"}
	env.seedPending(t, "f1", `{"type":"message","payload":{"role":"user","content":"later"}}`)
	inbox := []session.InboxItem{write, followUp}
	tip := "tip-1"

	// followUpWhenNoTrigger=false: only the write, no trigger.
	placement, err := PlanBoundaryInbox(inbox, "one-at-a-time", "one-at-a-time", env.sess, &tip, false, map[string]bool{}, "main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(placement.Entries) != 1 || placement.HasTrigger {
		t.Fatalf("placement = %+v", placement)
	}

	// followUpWhenNoTrigger=true: the followUp joins and triggers.
	placement, err = PlanBoundaryInbox(inbox, "one-at-a-time", "one-at-a-time", env.sess, &tip, true, map[string]bool{}, "main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(placement.Entries) != 2 || !placement.HasTrigger || placement.TriggerEntryID != "f1" {
		ids := []string{}
		for _, entry := range placement.Entries {
			ids = append(ids, entry.ID)
		}
		t.Fatalf("entries = %v trigger = %v", ids, placement.TriggerEntryID)
	}
	// Original inbox order preserved: write before followUp.
	if placement.Entries[0].ID != "w1" {
		t.Fatalf("order = %v", placement.Entries)
	}
}

func TestPlanBoundaryInboxProjectorCustomTypes(t *testing.T) {
	env := newBoundaryEnv(t)
	ctx := harnessBackground()
	write := session.InboxItem{EntryID: "w1", Kind: "write"}
	env.seedPending(t, "w1", `{"type":"custom","customType":"app.note","payload":{"note":"x"}}`)
	tip := "tip-1"

	// Unknown custom type does not project.
	placement, err := PlanBoundaryInbox([]session.InboxItem{write}, "all", "all", env.sess, &tip, true, map[string]bool{}, "main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if placement.HasTrigger {
		t.Fatal("unknown custom type projected")
	}
	// Registered projector projects and triggers.
	placement, err = PlanBoundaryInbox([]session.InboxItem{write}, "all", "all", env.sess, &tip, true, map[string]bool{"app.note": true}, "main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !placement.HasTrigger || placement.TriggerEntryID != "w1" {
		t.Fatalf("trigger = %v", placement.TriggerEntryID)
	}
}

func TestPlanBoundaryInboxMissingPayloadInvariant(t *testing.T) {
	env := newBoundaryEnv(t)
	ctx := harnessBackground()
	ghost := session.InboxItem{EntryID: "ghost", Kind: "steer"}
	tip := "tip-1"
	if _, err := PlanBoundaryInbox([]session.InboxItem{ghost}, "all", "all", env.sess, &tip, false, nil, "main", ctx); err == nil {
		t.Fatal("missing payload accepted")
	}
}

func TestPlanBoundaryInboxNonMessageSteerInvariant(t *testing.T) {
	env := newBoundaryEnv(t)
	ctx := harnessBackground()
	// A steer whose payload is custom: rejected for non-write kinds.
	item := session.InboxItem{EntryID: "bad", Kind: "steer"}
	env.seedPending(t, "bad", `{"type":"custom","customType":"x"}`)
	tip := "tip-1"
	if _, err := PlanBoundaryInbox([]session.InboxItem{item}, "all", "all", env.sess, &tip, false, nil, "main", ctx); err == nil {
		t.Fatal("custom steer accepted")
	}
}

func TestPlanBoundaryInboxEmpty(t *testing.T) {
	env := newBoundaryEnv(t)
	ctx := harnessBackground()
	tip := "tip-1"
	placement, err := PlanBoundaryInbox(nil, "all", "all", env.sess, &tip, true, nil, "main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(placement.Entries) != 0 || placement.TipID == nil || *placement.TipID != "tip-1" {
		t.Fatalf("placement = %+v", placement)
	}
	if placement.HasTrigger || placement.Queues != nil {
		t.Fatalf("trigger/queues leaked: %+v", placement)
	}
}

func TestAssistantReadyAtBoundary(t *testing.T) {
	scope := &session.OperationState{
		Control:                session.Control{Status: "running"},
		LatestAssistantEntryID: strptrRT("e-prev"),
	}
	next := AssistantReadyAtBoundary(nil, scope, "trigger-1", false, "step-9")
	if next.At != session.AtAssistantReady {
		t.Fatalf("at = %s", next.At)
	}
	if next.Control.Status != "running" || next.LatestAssistantEntryID == nil || *next.LatestAssistantEntryID != "e-prev" {
		t.Fatalf("scope lost: %+v", next)
	}
	if next.NextAttempt != 1 {
		t.Fatalf("nextAttempt = %d", next.NextAttempt)
	}
	if next.GenerationContext == nil || next.GenerationContext.MustGet("triggerEntryId") != "trigger-1" {
		t.Fatalf("generation = %v", next.GenerationContext)
	}
	// Scope source untouched.
	if scope.At != "" {
		t.Fatal("scope mutated")
	}
}

func strptrRT(s string) *string { return &s }

func TestBoundaryPlacementEventsQueueUpdate(t *testing.T) {
	placement := &BoundaryPlacement{
		Queues: &QueuesSnapshot{
			Steering: []queueItemSnapshot{{EntryID: "s1", Kind: "steer"}},
		},
	}
	events := BoundaryPlacementEvents(placement, session.CommitResult{Timestamp: 1, Seqs: []int64{}}, 0, "main", "run-1")
	if len(events) != 1 || EventType(events[0]) != "queue_update" {
		t.Fatalf("events = %v", events)
	}
	queues := events[0].MustGet("queues").(*jsonx.Obj)
	if steering, ok := queues.Get("steering"); !ok || steering == nil {
		t.Fatal("steering missing")
	}
	// No queues -> no queue_update.
	placement.Queues = nil
	events = BoundaryPlacementEvents(placement, session.CommitResult{}, 0, "main", "run-1")
	if len(events) != 0 {
		t.Fatalf("events = %v", events)
	}
}
