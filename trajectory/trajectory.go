// trajectory/trajectory.go: the trajectory object — an in-memory append-only
// log with commit-then-publish semantics, a repeatable live subscription,
// snapshot/query reads, and instance-scoped kind registration.
package trajectory

import (
	"fmt"
	"sync"

	"github.com/gladmo/openagent/chord"
	"github.com/gladmo/openagent/jsonx"
)

// Options configures a Trajectory.
type Options struct {
	// ID identifies the recorded session; generated when empty.
	ID string
	// ParentTrajectoryID links a nested agent's trajectory to its parent
	// (sub-agent scheduling records the linkage, not the child's content).
	ParentTrajectoryID string
	// Now stamps commit times (Unix ms). Defaults to the wall clock.
	Now func() float64
	// OnListenerError observes a subscriber panic or post-Close delivery
	// failure. Listener failures are always contained: they never abort the
	// publisher, starve later subscribers, or fail Append. Defaults to a
	// no-op; capture side effects here instead of panicking.
	OnListenerError func(rec *Record, err error)
}

// Trajectory is the trajectory log. Append validates, commits (seq/time
// assignment), then publishes to subscribers; subscribers never see an
// uncommitted record.
type Trajectory struct {
	mu sync.Mutex

	id     string
	parent string
	now    func() float64
	onErr  func(rec *Record, err error)

	records []Record
	kinds   map[string]kindRule

	listeners     map[*listenerSlot]bool
	listenerOrder []*listenerSlot
	nextListener  int64

	publishing bool
	closedErr  error
	closed     bool
}

type listenerSlot struct {
	id  int64
	fn  func(*Record)
	mu  sync.Mutex
	off bool
}

// New creates an open trajectory.
func New(options Options) *Trajectory {
	now := options.Now
	if now == nil {
		now = func() float64 { return float64(unixMilliNow()) }
	}
	onErr := options.OnListenerError
	if onErr == nil {
		onErr = func(*Record, error) {}
	}
	id := options.ID
	if id == "" {
		id = generateID()
	}
	kinds := make(map[string]kindRule, len(coreKinds)+4)
	for kind, rule := range coreKinds {
		kinds[kind] = rule
	}
	return &Trajectory{
		id:        id,
		parent:    options.ParentTrajectoryID,
		now:       now,
		onErr:     onErr,
		kinds:     kinds,
		listeners: map[*listenerSlot]bool{},
	}
}

// ID returns the trajectory identity.
func (t *Trajectory) ID() string { return t.id }

// ParentID returns the parent trajectory identity ("" when none).
func (t *Trajectory) ParentID() string { return t.parent }

// RegisterKind adds an extension kind. Extension kinds are written with
// ignorable=true so readers that do not know the kind skip the record
// instead of refusing the log; register with required=true only when the
// record's loss breaks reconstruction for every consumer of this instance.
func (t *Trajectory) RegisterKind(kind string, required bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return fmt.Errorf("trajectory: cannot register kind %q on a closed trajectory", kind)
	}
	if kind == "" {
		return fmt.Errorf("trajectory: kind must be non-empty")
	}
	if _, exists := t.kinds[kind]; exists {
		return fmt.Errorf("trajectory: kind %q is already registered", kind)
	}
	t.kinds[kind] = kindRule{scope: ScopeAny, core: false, required: required}
	return nil
}

// KindKnown reports whether a kind is registered on this instance.
func (t *Trajectory) KindKnown(kind string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.kinds[kind]
	return ok
}

// Append validates the record, assigns seq and time, commits it, then
// publishes to subscribers. The returned record is the committed value.
// Data must be a JSON value (jsonx semantics); the payload is defensively
// cloned so later mutation by the caller cannot rewrite history.
//
// Reentrancy: calling Append from inside a subscriber fails with
// ErrReentrantAppend — a listener that appends in reaction to a record
// would otherwise publish out of order.
func (t *Trajectory) Append(rec Record) (*Record, error) {
	t.mu.Lock()
	if t.closed {
		err := fmt.Errorf("trajectory: closed: %w", t.closedErr)
		t.mu.Unlock()
		return nil, err
	}
	if t.publishing {
		t.mu.Unlock()
		return nil, fmt.Errorf("trajectory: Append from inside a subscriber: %w", ErrReentrantAppend)
	}
	rule, ok := t.kinds[rec.Type]
	if !ok {
		t.mu.Unlock()
		return nil, fmt.Errorf("trajectory: unknown record type %q (register it first): %w", rec.Type, ErrUnknownKind)
	}
	if err := (&rec).validate(rule); err != nil {
		t.mu.Unlock()
		return nil, err
	}
	if !chord.IsJsonValue(rec.Data) {
		t.mu.Unlock()
		return nil, fmt.Errorf("trajectory: record %q data is not a JSON value", rec.Type)
	}
	rec.Data = jsonx.Clone(rec.Data)
	if !rule.core && !rule.required {
		rec.Ignorable = true
	}
	rec.Seq = int64(len(t.records)) + 1
	rec.TimeMs = t.now()
	t.records = append(t.records, rec)
	committed := t.records[len(t.records)-1]

	listeners := make([]*listenerSlot, len(t.listenerOrder))
	copy(listeners, t.listenerOrder)
	t.publishing = true
	t.mu.Unlock()

	t.publish(&committed, listeners)

	t.mu.Lock()
	t.publishing = false
	t.mu.Unlock()
	return &committed, nil
}

// publish delivers the committed record to each listener sequentially in
// subscription order. Listener failures are contained by deliverTo.
func (t *Trajectory) publish(rec *Record, listeners []*listenerSlot) {
	for _, slot := range listeners {
		t.deliverTo(slot, rec)
	}
}

// Subscribe registers a live listener. Subscribe is repeatable: any number
// of independent subscriptions may coexist, each invocation returns its own
// unsubscribe function, and disposal is idempotent. Listeners are invoked
// synchronously in subscription order, after the record committed. A new
// subscriber sees only future records — history is read through Snapshot or
// Query, never replayed on subscribe. Listener failures are contained (see
// Options.OnListenerError).
func (t *Trajectory) Subscribe(listener func(*Record)) (unsubscribe func()) {
	// afterSeq < 0 means live-only: no backlog delivery.
	return t.subscribeAfter(-1, listener)
}

// SubscribeAfter registers a live listener whose delivery starts with the
// first committed record after afterSeq (0 replays the whole log). The
// backlog is delivered under the commit lock before registration returns,
// so no record is missed, duplicated, or reordered — the hand-off between
// backlog and live delivery is atomic. Storage sinks use this to attach to
// a trajectory that already has records.
//
// Like Append from a listener, calling SubscribeAfter from inside a
// subscriber deadlocks the trajectory; wire sinks from the outside.
func (t *Trajectory) SubscribeAfter(afterSeq int64, listener func(*Record)) (unsubscribe func()) {
	return t.subscribeAfter(afterSeq, listener)
}

func (t *Trajectory) subscribeAfter(afterSeq int64, listener func(*Record)) (unsubscribe func()) {
	t.mu.Lock()
	slot := &listenerSlot{id: t.nextListener, fn: listener}
	t.nextListener++
	t.listeners[slot] = true
	t.listenerOrder = append(t.listenerOrder, slot)
	// Deliver the backlog under the commit lock: any later Append blocks on
	// t.mu until the backlog is done, so live delivery strictly follows it.
	if afterSeq >= 0 {
		for i := range t.records {
			if t.records[i].Seq > afterSeq {
				rec := t.records[i]
				t.deliverTo(slot, &rec)
			}
		}
	}
	t.mu.Unlock()
	return t.disposerFor(slot)
}

func (t *Trajectory) deliverTo(slot *listenerSlot, rec *Record) {
	slot.mu.Lock()
	off := slot.off
	fn := slot.fn
	slot.mu.Unlock()
	if off {
		return
	}
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("listener panicked: %v", r)
			}
		}()
		fn(rec)
		return nil
	}()
	if err != nil {
		t.onErr(rec, err)
	}
}

func (t *Trajectory) disposerFor(slot *listenerSlot) func() {
	return func() {
		slot.mu.Lock()
		wasOff := slot.off
		slot.off = true
		slot.mu.Unlock()
		if wasOff {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.listeners[slot] {
			delete(t.listeners, slot)
			for i, candidate := range t.listenerOrder {
				if candidate == slot {
					t.listenerOrder = append(t.listenerOrder[:i], t.listenerOrder[i+1:]...)
					break
				}
			}
		}
	}
}

// Snapshot returns a stable, detached copy of every committed record:
// mutation of a snapshot (payloads included) never rewrites history.
func (t *Trajectory) Snapshot() []Record {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Record, len(t.records))
	for i := range t.records {
		out[i] = t.records[i]
		out[i].Data = jsonx.Clone(t.records[i].Data)
	}
	return out
}

// Len returns the number of committed records.
func (t *Trajectory) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.records)
}

// Query filters the committed records (see Query).
func (t *Trajectory) Query(q Query) []Record {
	return FilterRecords(t.Snapshot(), q)
}

// Close freezes the trajectory. Later Append calls fail; already-registered
// subscribers stop receiving records (their disposers stay valid no-ops).
// The cause is surfaced by Append errors and Closed.
func (t *Trajectory) Close(cause error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	if cause == nil {
		cause = fmt.Errorf("trajectory closed")
	}
	t.closedErr = cause
	t.closed = true
}

// Closed reports the close cause (nil while open).
func (t *Trajectory) Closed() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closedErr
}

// Sentinel errors.
var (
	// ErrReentrantAppend marks an Append issued from inside a subscriber.
	ErrReentrantAppend = errSentinel("reentrant append")
	// ErrUnknownKind marks an Append with an unregistered record type.
	ErrUnknownKind = errSentinel("unknown record kind")
)

type errSentinel string

func (e errSentinel) Error() string { return string(e) }
