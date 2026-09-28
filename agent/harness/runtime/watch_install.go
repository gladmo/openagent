package runtime

// watch_install.go ports harness/runtime/lane.ts's installWatch: the
// push-mode watch registry — watchers subscribe with a filter and a
// resnapshot boundary; emitted events flow to matching watchers with
// boundary-marked resnapshots.

import (
	"sync"

	"github.com/gladmo/openagent/jsonx"
)

// WatchEvent is the strict-JSON harness event form pushed to watchers.
type WatchEvent = *jsonx.Obj

// Watcher wraps one installed watch.
type Watcher struct {
	mu           sync.Mutex
	snapshot     any
	filter       func(event HarnessEvent) bool
	resnapshot   func(markBoundary func()) (any, error)
	listener     func(event WatchEvent)
	events       []WatchEvent
	buffered     int
	unsubscribed bool
}

// Snapshot returns the owned snapshot.
func (w *Watcher) Snapshot() any { return w.snapshot }

// Start installs the listener and drains buffered events in order.
func (w *Watcher) Start(listener func(event WatchEvent)) {
	w.mu.Lock()
	w.listener = listener
	buffered := w.events
	w.events = nil
	w.mu.Unlock()
	for _, event := range buffered {
		if w.unsubscribed {
			break
		}
		w.deliver(event)
	}
}

// Unsubscribe removes the watcher.
func (w *Watcher) Unsubscribe() {
	w.mu.Lock()
	w.unsubscribed = true
	w.listener = nil
	w.mu.Unlock()
}

// Accept offers one event: buffered without a listener, delivered with.
func (w *Watcher) Accept(event HarnessEvent) {
	if !w.filter(event) {
		return
	}
	rendered := renderWatchEvent(event)
	w.mu.Lock()
	if w.unsubscribed {
		w.mu.Unlock()
		return
	}
	if w.listener == nil {
		const watchCapacity = 256
		if len(w.events) >= watchCapacity {
			w.mu.Unlock()
			w.Unsubscribe()
			return
		}
		w.events = append(w.events, rendered)
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	w.deliver(rendered)
}

func (w *Watcher) deliver(event WatchEvent) {
	defer func() { _ = recover() }()
	if w.listener != nil {
		w.listener(event)
	}
}

// Resnapshot recaptures through the boundary-marked resnapshot.
func (w *Watcher) Resnapshot() (any, error) {
	if w.resnapshot == nil {
		return w.snapshot, nil
	}
	return w.resnapshot(func() {})
}

// renderWatchEvent renders one harness event through the jsonx model.
func renderWatchEvent(event HarnessEvent) WatchEvent {
	obj := jsonx.NewObj()
	for _, key := range event.Keys() {
		value, _ := event.Get(key)
		obj.Set(key, value)
	}
	return obj
}

// WatchRegistry is the lane's push-mode event bus.
type WatchRegistry struct {
	mu       sync.Mutex
	watchers []*Watcher
}

// NewWatchRegistry builds an empty registry.
func NewWatchRegistry() *WatchRegistry {
	return &WatchRegistry{}
}

// Install registers one watcher with its filter and resnapshot.
func (r *WatchRegistry) Install(snapshot any, filter func(event HarnessEvent) bool, resnapshot func(markBoundary func()) (any, error)) *Watcher {
	watcher := &Watcher{
		snapshot:   snapshot,
		filter:     filter,
		resnapshot: resnapshot,
	}
	r.mu.Lock()
	r.watchers = append(r.watchers, watcher)
	r.mu.Unlock()
	return watcher
}

// Publish fans one event out to matching watchers.
func (r *WatchRegistry) Publish(event HarnessEvent) {
	r.mu.Lock()
	watchers := append([]*Watcher{}, r.watchers...)
	r.mu.Unlock()
	for _, watcher := range watchers {
		watcher.Accept(event)
	}
}

// Drop removes unsubscribed watchers.
func (r *WatchRegistry) Prune() {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.watchers[:0]
	for _, watcher := range r.watchers {
		if !watcher.unsubscribed {
			kept = append(kept, watcher)
		}
	}
	r.watchers = kept
}

// Count reports the installed watcher count.
func (r *WatchRegistry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.watchers)
}
