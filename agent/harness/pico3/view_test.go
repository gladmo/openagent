package pico3

// Ports of view.ts watch lifecycle behaviors.

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"

	chorddelta "github.com/gladmo/openagent/chord/delta"
)

func newViewManagerForTest(t *testing.T) (*Session, *ViewManager) {
	t.Helper()
	sess, err := NewSession(NewMemoryStorage())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	var reported []error
	manager := NewViewManager(sess, func(err error) { reported = append(reported, err) })
	t.Cleanup(manager.Close)
	return sess, manager
}

func envelopeWith(revision int64, ops ...chorddelta.Op) *Envelope {
	return &Envelope{Revision: revision, Ops: ops}
}

func TestWatchBufferUntilStart(t *testing.T) {
	_, manager := newViewManagerForTest(t)
	conversation := &Conversation{ID: 1}
	watch := manager.Watch(conversation, objFromPairs("a", float64(1)))

	// Envelopes delivered before start() buffer inside the watch.
	manager.UpdateState(1, func(state *jsonx.Obj) { state.Set("b", float64(2)) })
	manager.EmitFlushed(1, nil)
	manager.Deliver()
	var received []*Envelope
	watch.Start(func(envelope *Envelope) {
		received = append(received, envelope)
	})
	if len(received) != 1 || received[0].Revision != 1 {
		t.Fatalf("received = %d", len(received))
	}
	// After start, deliveries flow directly.
	manager.UpdateState(1, func(state *jsonx.Obj) { state.Set("c", float64(3)) })
	manager.EmitFlushed(1, nil)
	manager.Deliver()
	if len(received) != 2 || received[1].Revision != 2 {
		t.Fatalf("received = %d", len(received))
	}
}

func TestWatchCapacityExceededBeforeStart(t *testing.T) {
	_, manager := newViewManagerForTest(t)
	conversation := &Conversation{ID: 1}
	watch := manager.Watch(conversation, objFromPairs("a", float64(1)))
	for i := 0; i <= WatchCapacity; i++ {
		manager.UpdateState(1, func(state *jsonx.Obj) { state.Set("k", float64(i)) })
		manager.EmitFlushed(1, nil)
	}
	manager.Deliver()
	manager.Deliver()
	if !watch.Closed() {
		t.Fatal("watch survived capacity overflow")
	}
	// The buffer was cleared; starting is a no-op.
	started := false
	watch.Start(func(*Envelope) { started = true })
	if started {
		t.Fatal("started after overflow stop")
	}
}

func TestWatchStopUnregistersRecord(t *testing.T) {
	_, manager := newViewManagerForTest(t)
	conversation := &Conversation{ID: 1}
	watch := manager.Watch(conversation, objFromPairs("a", float64(1)))
	if _, exists := manager.records[1]; !exists {
		t.Fatal("record missing")
	}
	watch.Stop()
	if _, exists := manager.records[1]; exists {
		t.Fatal("record survived last stop")
	}
	if len(manager.order) != 0 {
		t.Fatal("order survived")
	}
	// Double stop is safe.
	watch.Stop()
}

func TestWatchListenerErrorFails(t *testing.T) {
	_, manager := newViewManagerForTest(t)
	conversation := &Conversation{ID: 1}
	watch := manager.Watch(conversation, objFromPairs("a", float64(1)))
	watch.Start(func(*Envelope) { panic("boom") })
	manager.UpdateState(1, func(state *jsonx.Obj) { state.Set("b", float64(9)) })
	manager.EmitFlushed(1, nil)
	manager.Deliver()
	if !watch.Closed() {
		t.Fatal("watch survived listener error")
	}
}

func TestDeliverOrderedSnapshot(t *testing.T) {
	_, manager := newViewManagerForTest(t)
	conversation := &Conversation{ID: 1}
	watchA := manager.Watch(conversation, objFromPairs("a", float64(1)))
	watchB := manager.Watch(conversation, objFromPairs("a", float64(1)))

	var order []int64
	watchA.Start(func(e *Envelope) { order = append(order, e.Revision) })
	watchB.Start(func(e *Envelope) { order = append(order, e.Revision) })
	manager.UpdateState(1, func(state *jsonx.Obj) { state.Set("b", float64(1)) })
	manager.EmitFlushed(1, nil)
	manager.UpdateState(1, func(state *jsonx.Obj) { state.Set("c", float64(2)) })
	manager.EmitFlushed(1, nil)
	manager.Deliver()
	if len(order) != 4 || order[0] != 1 || order[1] != 1 || order[2] != 2 || order[3] != 2 {
		t.Fatalf("order = %v", order)
	}
}

func TestApplyEnvelope(t *testing.T) {
	view := objFromPairs("a", float64(1))
	envelope := envelopeWith(1, &chorddelta.Set{Path: chorddelta.Path{"b"}, Value: float64(2)})
	next := ApplyEnvelope(view, envelope)
	if next.MustGet("a") != float64(1) || next.MustGet("b") != float64(2) {
		t.Fatalf("view = %v", next)
	}
	// Source untouched (immutable apply).
	if _, existed := view.Get("b"); existed {
		t.Fatal("source mutated")
	}
}

func TestFailRecordFailsWatchers(t *testing.T) {
	_, manager := newViewManagerForTest(t)
	conversation := &Conversation{ID: 1}
	watch := manager.Watch(conversation, objFromPairs("a", float64(1)))
	manager.FailRecord(1, errViewTest("record fault"))
	if !watch.Closed() {
		t.Fatal("watcher survived record failure")
	}
	if _, exists := manager.records[1]; exists {
		t.Fatal("record survived failure")
	}
}

func TestCloseStopsAll(t *testing.T) {
	_, manager := newViewManagerForTest(t)
	conversation := &Conversation{ID: 1}
	watch := manager.Watch(conversation, objFromPairs("a", float64(1)))
	manager.UpdateState(1, func(state *jsonx.Obj) { state.Set("b", float64(1)) })
	manager.EmitFlushed(1, nil)
	manager.Close()
	if !watch.Closed() {
		t.Fatal("watch survived close")
	}
	// Pending deliveries dropped.
	manager.Deliver()
	// After close the manager is empty but reusable.
	if len(manager.records) != 0 || len(manager.deliveries) != 0 {
		t.Fatal("state survived close")
	}
}

func errViewTest(msg string) error { return &viewTestError{msg} }

type viewTestError struct{ msg string }

func (e *viewTestError) Error() string { return e.msg }
