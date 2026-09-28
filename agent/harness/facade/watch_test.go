package harnessfacade

// Ports of the facade watch path.

import (
	"testing"

	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func TestWatchCapturesSnapshot(t *testing.T) {
	lane := newFacadeLane(t)
	ctx := harness.BackgroundContext
	// Append one message so the snapshot has content.
	if _, err := lane.AppendMessage(jsonx.ObjFrom("role", "user", "content", "hi", "timestamp", float64(1)), ctx); err != nil {
		t.Fatal(err)
	}
	handle, err := lane.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := handle.Snapshot()
	if snapshot.MustGet("lane") != "main" {
		t.Fatalf("snapshot = %v", snapshot)
	}
	tip := snapshot.MustGet("tipId")
	if tip == nil || tip == "" {
		t.Fatal("tip missing")
	}
}

func TestWatchResnapshot(t *testing.T) {
	lane := newFacadeLane(t)
	ctx := harness.BackgroundContext
	if _, err := lane.AppendMessage(jsonx.ObjFrom("role", "user", "content", "one", "timestamp", float64(1)), ctx); err != nil {
		t.Fatal(err)
	}
	handle, err := lane.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstTip := handle.Snapshot().MustGet("tipId")

	// Append another message; resnapshot sees the new tip.
	if _, err := lane.AppendMessage(jsonx.ObjFrom("role", "user", "content", "two", "timestamp", float64(2)), ctx); err != nil {
		t.Fatal(err)
	}
	fresh, err := handle.Resnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondTip := fresh.MustGet("tipId")
	if secondTip == firstTip {
		t.Fatalf("tip unchanged: %v", secondTip)
	}
	// The handle's owned snapshot advanced.
	if handle.Snapshot().MustGet("tipId") != secondTip {
		t.Fatal("owned snapshot stale")
	}
}

func TestWatchListenerLifecycle(t *testing.T) {
	lane := newFacadeLane(t)
	handle, err := lane.Watch(harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	handle.Start(func(*jsonx.Obj) { called++ })
	handle.listener(jsonx.ObjFrom("type", "message_start"))
	if called != 1 {
		t.Fatalf("called = %d", called)
	}
	// Unsubscribe drops the listener.
	handle.Unsubscribe()
	if handle.listener != nil {
		t.Fatal("listener survived unsubscribe")
	}
	if !handle.unsubscribed {
		t.Fatal("flag not set")
	}
}

func TestWatchEmptyLane(t *testing.T) {
	lane := newFacadeLane(t)
	handle, err := lane.Watch(harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh lane's tip is null and the transcript empty.
	if handle.Snapshot().MustGet("tipId") != nil {
		t.Fatal("fresh tip not null")
	}
}

// Session-level watch covers every lane.
func TestWatchSessionSnapshots(t *testing.T) {
	h := newFacadeHarness(t)
	ctx := harness.BackgroundContext
	for _, name := range []string{"main", "work"} {
		if _, err := h.Lane(name, ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Each lane watches independently.
	for _, name := range []string{"main", "work"} {
		lane, err := h.ForLane(name, ctx)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := lane.Watch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if handle.Snapshot().MustGet("lane") != name {
			t.Fatalf("lane = %v", handle.Snapshot().MustGet("lane"))
		}
	}
	_ = session.SessionMetadata{}
}
