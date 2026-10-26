package tasks

// events.go: the scheduler's structured event stream (NFR-2). Listeners
// are invoked inline on the scheduler goroutine or the CRUD caller;
// they must not block, and panics are contained.

import (
	"fmt"
	"sync"
	"time"
)

// TaskEventType values.
type TaskEventType string

const (
	EventTaskRegistered TaskEventType = "task_registered"
	EventTaskUpdated    TaskEventType = "task_updated"
	EventTaskDeleted    TaskEventType = "task_deleted"
	EventTaskPaused     TaskEventType = "task_paused"
	EventTaskResumed    TaskEventType = "task_resumed"
	EventFired          TaskEventType = "fired"
	EventRunStarted     TaskEventType = "run_started"
	EventRunSkipped     TaskEventType = "run_skipped"
	EventRunFinished    TaskEventType = "run_finished"
	EventStoreDegraded  TaskEventType = "store_degraded"
)

// TaskEvent is one scheduler observation. Run is set for fire/run
// events; Issue for store degradation.
type TaskEvent struct {
	Type   TaskEventType
	TaskID string
	RunID  string
	Run    *TaskRun
	Issue  *StoreIssue
	At     time.Time
}

func (e TaskEvent) String() string {
	if e.TaskID != "" {
		return fmt.Sprintf("tasks %s task=%s run=%s", e.Type, e.TaskID, e.RunID)
	}
	return fmt.Sprintf("tasks %s", e.Type)
}

// TaskEventListener receives events; it must not block.
type TaskEventListener func(event TaskEvent)

type eventBus struct {
	mu        sync.Mutex
	emitMu    sync.Mutex
	listeners map[*TaskEventListener]struct{}
	order     []*TaskEventListener
}

func newEventBus() *eventBus {
	return &eventBus{listeners: map[*TaskEventListener]struct{}{}}
}

// subscribe registers a listener; the returned function unsubscribes.
func (b *eventBus) subscribe(listener TaskEventListener) func() {
	key := &listener
	b.mu.Lock()
	b.listeners[key] = struct{}{}
	b.order = append(b.order, key)
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.listeners[key]; !ok {
			return
		}
		delete(b.listeners, key)
		for i, candidate := range b.order {
			if candidate == key {
				b.order = append(b.order[:i], b.order[i+1:]...)
				break
			}
		}
	}
}

// emit delivers to the current listener list, serialized; a panicking
// listener is dropped for that event without affecting others.
func (b *eventBus) emit(event TaskEvent) {
	b.mu.Lock()
	listeners := append([]*TaskEventListener(nil), b.order...)
	b.mu.Unlock()
	b.emitMu.Lock()
	defer b.emitMu.Unlock()
	for _, listener := range listeners {
		func() {
			defer func() { _ = recover() }()
			(*listener)(event)
		}()
	}
}
