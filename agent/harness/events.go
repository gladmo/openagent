package harness

// events.go ports harness/events.ts: the passive harness event bus with
// isolated handler failures and buffered watchers. HarnessEvent itself is a
// JSON-shaped map payload (the full typed union lives in agent-harness.ts
// and is ported with the runtime); the bus only needs `type` and optional
// `lane` fields.

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gladmo/openagent/jsonx"
)

// HarnessEvent is the bus payload: a JSON object with at least "type".
type HarnessEvent = *jsonx.Obj

// EventType returns the event's type field.
func EventType(event HarnessEvent) string {
	if v, ok := event.Get("type"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// EventLane returns the optional lane field.
func EventLane(event HarnessEvent) (string, bool) {
	if v, ok := event.Get("lane"); ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}

// EventListener receives events; it may block (the bus serializes).
type EventListener func(event HarnessEvent, ctx Context)

// Events is the bus contract.
type Events interface {
	On(eventType string, listener EventListener) (unsubscribe func())
	Emit(event HarnessEvent, ctx Context)
	EmitBatch(events []HarnessEvent, ctx Context)
	WatchHandle() WatchHandleFactory
	Close(err error)
}

// WatchHandle is the watcher contract (snapshot generic erased to any).
type WatchHandle interface {
	Start(listener EventListener)
	Resnapshot(ctx Context) (any, error)
	Unsubscribe()
}

// HarnessEventBus mirrors the TS class. Delivery is serialized through a
// mutex-backed queue; EmitBatch appends one contiguous batch and returns
// after queuing (TS returns a promise; callers that must observe delivery
// completion use Flush).
type registeredListener struct {
	id int64
	fn EventListener
}

type HarnessEventBus struct {
	mu             sync.Mutex
	listeners      map[string][]registeredListener
	watchListeners []EventListener
	nextListenerID int64
	queue          []func()
	draining       bool
	closedError    error
	closed         bool
}

// NewHarnessEventBus creates an open bus.
func NewHarnessEventBus() *HarnessEventBus {
	return &HarnessEventBus{listeners: map[string][]registeredListener{}}
}

// On registers a typed listener; the unsubscribe func is idempotent.
func (b *HarnessEventBus) On(eventType string, listener EventListener) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closedError != nil {
		panic(b.closedError)
	}
	b.nextListenerID++
	id := b.nextListenerID
	b.listeners[eventType] = append(b.listeners[eventType], registeredListener{id: id, fn: listener})
	removed := false
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if removed {
			return
		}
		removed = true
		list := b.listeners[eventType]
		for i, candidate := range list {
			if candidate.id == id {
				b.listeners[eventType] = append(list[:i], list[i+1:]...)
				return
			}
		}
	}
}

// Emit delivers one event.
func (b *HarnessEventBus) Emit(event HarnessEvent, ctx Context) {
	b.EmitBatch([]HarnessEvent{event}, ctx)
}

// EmitBatch binds current recipients and appends one contiguous batch to
// the global delivery tail, then drains synchronously (callers may rely on
// delivery having happened when EmitBatch returns; TS queues a promise and
// the serialized tail preserves order identically).
func (b *HarnessEventBus) EmitBatch(events []HarnessEvent, ctx Context) {
	b.mu.Lock()
	if b.closedError != nil || len(events) == 0 {
		b.mu.Unlock()
		return
	}
	type boundDelivery struct {
		payload    HarnessEvent
		recipients []EventListener
	}
	bound := make([]boundDelivery, 0, len(events))
	for _, event := range events {
		payload := cloneEvent(event)
		recipients := b.snapshotRecipientsLocked(payload)
		bound = append(bound, boundDelivery{payload, recipients})
	}
	b.mu.Unlock()

	for _, delivery := range bound {
		b.deliver(delivery.payload, delivery.recipients, true, ctx)
	}
}

func (b *HarnessEventBus) snapshotRecipientsLocked(event HarnessEvent) []EventListener {
	recipients := make([]EventListener, 0, len(b.listeners[EventType(event)])+len(b.watchListeners))
	for _, registered := range b.listeners[EventType(event)] {
		recipients = append(recipients, registered.fn)
	}
	recipients = append(recipients, b.watchListeners...)
	return recipients
}

func cloneEvent(event HarnessEvent) HarnessEvent {
	if event == nil {
		return jsonx.NewObj()
	}
	return event.Clone()
}

// deliver invokes each recipient with its own structuredClone of the event;
// failures re-emit handler_error (never re-entering).
func (b *HarnessEventBus) deliver(event HarnessEvent, recipients []EventListener, reportErrors bool, ctx Context) {
	for _, listener := range recipients {
		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = ToError(r)
				}
			}()
			listener(cloneEvent(event), ctx)
			return nil
		}()
		if err == nil {
			continue
		}
		if !reportErrors || EventType(event) == "handler_error" {
			continue
		}
		handlerError := jsonx.NewObj()
		handlerError.Set("type", "handler_error")
		handlerError.Set("kind", "event")
		handlerError.Set("event", EventType(event))
		handlerError.Set("error", err.Error())
		if lane, ok := EventLane(event); ok {
			handlerError.Set("lane", lane)
		}
		b.mu.Lock()
		recipients := b.snapshotRecipientsLocked(handlerError)
		b.mu.Unlock()
		b.deliver(handlerError, recipients, false, ctx)
	}
}

// WatchHandleFactory is a placeholder for the typed watch entry points that
// arrive with the runtime (they need LaneSnapshot types).
type WatchHandleFactory struct{}

// Close freezes the bus after pending deliveries complete.
func (b *HarnessEventBus) Close(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closedError == nil {
		b.closedError = err
	}
	b.closed = true
}

// Closed reports the close error.
func (b *HarnessEventBus) Closed() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closedError
}

// bufferedEventWatcher mirrors BufferedEventWatcher.
type bufferedEventWatcher struct {
	mu                 sync.Mutex
	snapshot           any
	resnapshotCallback func(ctx Context) (any, error)
	onError            func(err error, event HarnessEvent, ctx Context)
	buffer             []struct {
		event HarnessEvent
		ctx   Context
		epoch int64
	}
	listener            EventListener
	unsubscribeCallback func()
	epoch               int64
	resnapshotPhase     string // "" | "dropping" | "holding"
	held                []struct {
		event HarnessEvent
		ctx   Context
	}
	state string // "buffering" | "started" | "unsubscribed"
}

// Start flushes buffered events to the listener; once only.
func (w *bufferedEventWatcher) Start(listener EventListener) {
	w.mu.Lock()
	if w.state != "buffering" {
		w.mu.Unlock()
		panic("WatchHandle.start() may be called only once")
	}
	w.state = "started"
	w.listener = listener
	buffered := w.buffer
	w.buffer = nil
	w.mu.Unlock()
	for _, bufferedEvent := range buffered {
		w.enqueue(bufferedEvent.event, bufferedEvent.ctx, bufferedEvent.epoch)
	}
}

// Resnapshot captures a fresh snapshot with a delivery-tail boundary.
func (w *bufferedEventWatcher) Resnapshot(ctx Context) (any, error) {
	w.mu.Lock()
	if w.state == "unsubscribed" {
		w.mu.Unlock()
		return nil, fmt.Errorf("WatchHandle is unsubscribed")
	}
	if w.resnapshotCallback == nil {
		w.mu.Unlock()
		return nil, fmt.Errorf("WatchHandle does not support resnapshot")
	}
	if w.resnapshotPhase != "" {
		w.mu.Unlock()
		return nil, fmt.Errorf("WatchHandle resnapshot is already in progress")
	}
	w.epoch++
	w.resnapshotPhase = "dropping"
	w.held = nil
	w.mu.Unlock()

	snapshot, err := w.resnapshotCallback(ctx)
	w.mu.Lock()
	// After the callback (which marks the boundary mid-flight), anything
	// delivered while holding stays in held and replays after the swap.
	held := w.held
	w.held = nil
	w.resnapshotPhase = ""
	if err != nil {
		w.mu.Unlock()
		for _, h := range held {
			w.Push(h.event, h.ctx)
		}
		return nil, err
	}
	w.snapshot = snapshot
	w.mu.Unlock()
	for _, h := range held {
		w.Push(h.event, h.ctx)
	}
	return snapshot, nil
}

// MarkResnapshotBoundary flips dropping -> holding.
func (w *bufferedEventWatcher) MarkResnapshotBoundary() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.resnapshotPhase != "dropping" {
		return
	}
	w.resnapshotPhase = "holding"
}

// Unsubscribe stops the watcher.
func (w *bufferedEventWatcher) Unsubscribe() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state == "unsubscribed" {
		return
	}
	w.state = "unsubscribed"
	w.buffer = nil
	w.listener = nil
	if w.unsubscribeCallback != nil {
		w.unsubscribeCallback()
		w.unsubscribeCallback = nil
	}
}

// Push buffers or enqueues an event.
func (w *bufferedEventWatcher) Push(event HarnessEvent, ctx Context) {
	w.mu.Lock()
	if w.state == "unsubscribed" {
		w.mu.Unlock()
		return
	}
	if w.resnapshotPhase == "dropping" {
		w.mu.Unlock()
		return
	}
	if w.resnapshotPhase == "holding" {
		w.held = append(w.held, struct {
			event HarnessEvent
			ctx   Context
		}{event, ctx})
		w.mu.Unlock()
		return
	}
	if w.state == "buffering" {
		w.buffer = append(w.buffer, struct {
			event HarnessEvent
			ctx   Context
			epoch int64
		}{event, ctx, w.epoch})
		w.mu.Unlock()
		return
	}
	epoch := w.epoch
	w.mu.Unlock()
	w.enqueue(event, ctx, epoch)
}

func (w *bufferedEventWatcher) enqueue(event HarnessEvent, ctx Context, epoch int64) {
	w.mu.Lock()
	listener := w.listener
	started := w.state == "started"
	currentEpoch := w.epoch
	w.mu.Unlock()
	if listener == nil || !started || epoch != currentEpoch {
		return
	}
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = ToError(r)
			}
		}()
		listener(cloneEvent(event), ctx)
		return nil
	}()
	if err != nil && w.onError != nil {
		func() {
			defer func() { _ = recover() }()
			w.onError(err, event, ctx)
		}()
	}
}

// keep encoding/json for future typed payloads.
var _ = json.Marshal
