package runtime

// Ports of installWatch push-mode behaviors.

import (
	"sync"
	"testing"
	"time"

	"github.com/gladmo/openagent/jsonx"
)

func laneEventOf(eventType, lane string) HarnessEvent {
	event := jsonx.NewObj()
	event.Set("type", eventType)
	if lane != "" {
		event.Set("lane", lane)
	}
	return event
}

func TestWatcherFilterAndDelivery(t *testing.T) {
	registry := NewWatchRegistry()
	var received []WatchEvent
	watcher := registry.Install(nil,
		func(event HarnessEvent) bool { return EventType(event) == "usage" || event.MustGet("lane") == "main" },
		nil,
	)
	watcher.Start(func(event WatchEvent) { received = append(received, event) })

	// Matching events flow.
	registry.Publish(laneEventOf("message_start", "main"))
	registry.Publish(laneEventOf("usage", "other"))
	if len(received) != 2 {
		t.Fatalf("received = %d", len(received))
	}
	// Non-matching events do not.
	registry.Publish(laneEventOf("message_start", "other"))
	if len(received) != 2 {
		t.Fatal("filter leaked")
	}
	watcher.Unsubscribe()
}

func TestWatcherBuffersBeforeStart(t *testing.T) {
	registry := NewWatchRegistry()
	watcher := registry.Install(nil, func(HarnessEvent) bool { return true }, nil)
	// Publish without a listener: buffered.
	registry.Publish(laneEventOf("entry_added", "main"))
	registry.Publish(laneEventOf("usage", "main"))
	var received []WatchEvent
	watcher.Start(func(event WatchEvent) { received = append(received, event) })
	if len(received) != 2 || received[0].MustGet("type") != "entry_added" {
		t.Fatalf("received = %v", received)
	}
}

func TestWatcherCapacityOverflow(t *testing.T) {
	registry := NewWatchRegistry()
	watcher := registry.Install(nil, func(HarnessEvent) bool { return true }, nil)
	// Exceed the 256 buffer: the watcher unsubscribes.
	for i := 0; i < 300; i++ {
		registry.Publish(laneEventOf("entry_added", "main"))
	}
	if !watcher.unsubscribed {
		t.Fatal("watcher survived overflow")
	}
	// Prune removes it.
	registry.Prune()
	if registry.Count() != 0 {
		t.Fatalf("count = %d", registry.Count())
	}
}

func TestWatcherResnapshotBoundary(t *testing.T) {
	resnapshots := 0
	boundaries := 0
	snapshot := "initial"
	watcher := NewWatchRegistry().Install(snapshot, nil, func(markBoundary func()) (any, error) {
		resnapshots++
		boundaries++
		markBoundary()
		return "fresh", nil
	})
	if watcher.Snapshot() != "initial" {
		t.Fatal("initial snapshot lost")
	}
	if boundaries != 0 {
		t.Fatal("boundary before resnapshot")
	}
	value, err := watcher.Resnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if value != "fresh" || resnapshots != 1 {
		t.Fatalf("value = %v resnapshots = %d", value, resnapshots)
	}
}

func TestWatchRegistryConcurrentPublish(t *testing.T) {
	registry := NewWatchRegistry()
	var mu sync.Mutex
	total := 0
	watcher := registry.Install(nil, func(HarnessEvent) bool { return true }, nil)
	watcher.Start(func(WatchEvent) {
		mu.Lock()
		total++
		mu.Unlock()
	})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			registry.Publish(laneEventOf("usage", "main"))
		}()
	}
	wg.Wait()
	// All events delivered exactly once.
	mu.Lock()
	defer mu.Unlock()
	if total != 100 {
		t.Fatalf("total = %d", total)
	}
}

func TestWatcherListenerPanicIsolated(t *testing.T) {
	registry := NewWatchRegistry()
	watcher := registry.Install(nil, func(HarnessEvent) bool { return true }, nil)
	called := 0
	watcher.Start(func(WatchEvent) {
		called++
		if called == 1 {
			panic("listener exploded")
		}
	})
	// A panicking listener does not take down the publisher.
	registry.Publish(laneEventOf("usage", "main"))
	registry.Publish(laneEventOf("usage", "main"))
	if called != 2 {
		t.Fatalf("called = %d", called)
	}
}

func TestWatcherUnsubscribeStopsDelivery(t *testing.T) {
	registry := NewWatchRegistry()
	received := 0
	watcher := registry.Install(nil, func(HarnessEvent) bool { return true }, nil)
	watcher.Start(func(WatchEvent) { received++ })
	registry.Publish(laneEventOf("usage", "main"))
	watcher.Unsubscribe()
	registry.Publish(laneEventOf("usage", "main"))
	if received != 1 {
		t.Fatalf("received = %d", received)
	}
	_ = time.Millisecond
}
