package pico3

// view.go ports harness/pico3/view.ts: the envelope/audit surface of the
// ViewManager. The full ConversationView builder (config routing, turn/
// compaction/tasks/plugins projections) rides on the kinds/system ports;
// here the watch lifecycle, envelope buffering with WATCH_CAPACITY, the
// ordered delivery queue, and applyEnvelope land with their session
// integration points.

import (
	"fmt"

	chorddelta "github.com/gladmo/openagent/chord/delta"
	"github.com/gladmo/openagent/jsonx"
)

// WatchCapacity mirrors WATCH_CAPACITY.
const WatchCapacity = 256

// ViewEvent is one conversation-scoped event payload.
type ViewEvent = *jsonx.Obj

// Envelope is one revision's delta: ops + events, frozen at issue.
type Envelope struct {
	Revision int64
	Ops      []chorddelta.Op
	Events   []ViewEvent
}

// WatchListener receives envelopes synchronously and ordered.
type WatchListener func(envelope *Envelope)

// Watch mirrors the TS interface.
type Watch interface {
	View() *jsonx.Obj
	Revision() int64
	Closed() bool
	Start(listener WatchListener)
	Stop()
}

// watchImpl ports WatchImpl: buffers envelopes until start(), capacity
// bounded, listener errors fail the watch.
type watchImpl struct {
	view     *jsonx.Obj
	revision int64
	onReport func(err error)
	onStop   func()
	listener WatchListener
	buffer   []*Envelope
	stopped  bool
}

// View returns the snapshot the watcher was created with.
func (w *watchImpl) View() *jsonx.Obj { return w.view }

// Revision returns the revision at creation.
func (w *watchImpl) Revision() int64 { return w.revision }

// Closed reports whether the watch stopped.
func (w *watchImpl) Closed() bool { return w.stopped }

// Start installs the listener and drains the buffer in order.
func (w *watchImpl) Start(listener WatchListener) {
	if w.listener != nil || w.stopped {
		return
	}
	w.listener = listener
	for _, envelope := range w.buffer {
		if w.stopped {
			break
		}
		w.deliver(envelope)
	}
	w.buffer = nil
}

// Stop closes the watch once.
func (w *watchImpl) Stop() {
	if w.stopped {
		return
	}
	w.stopped = true
	w.buffer = nil
	w.onStop()
}

// Accept buffers or delivers one envelope.
func (w *watchImpl) Accept(envelope *Envelope) {
	if w.stopped {
		return
	}
	if w.listener == nil {
		if len(w.buffer) >= WatchCapacity {
			w.Stop()
			w.report(fmt.Errorf("watch capacity %d exceeded before start()", WatchCapacity))
			return
		}
		w.buffer = append(w.buffer, envelope)
		return
	}
	w.deliver(envelope)
}

func (w *watchImpl) deliver(envelope *Envelope) {
	defer func() {
		if r := recover(); r != nil {
			w.Fail(fmt.Errorf("%v", r))
		}
	}()
	w.listener(envelope)
}

// Fail stops the watch and reports the error.
func (w *watchImpl) Fail(err error) {
	w.Stop()
	w.report(err)
}

func (w *watchImpl) report(err error) {
	if w.onReport != nil {
		w.onReport(err)
	}
}

// ApplyEnvelope applies an envelope's ops immutably.
func ApplyEnvelope(view *jsonx.Obj, envelope *Envelope) *jsonx.Obj {
	applied, err := chorddelta.ApplyImmutable(view, envelope.Ops)
	if err != nil {
		return cloneJSONObj(view)
	}
	if obj, ok := applied.(*jsonx.Obj); ok {
		return obj
	}
	return cloneJSONObj(view)
}

// viewRecord is one watched conversation.
type viewRecord struct {
	conversationID Id
	tracker        *Tracker
	watchers       []*watchImpl
	revision       int64
}

// delivery pairs one envelope with its watcher snapshot.
type delivery struct {
	envelope *Envelope
	watchers []*watchImpl
}

// ViewManager coordinates watches and post-commit envelope delivery.
type ViewManager struct {
	records    map[Id]*viewRecord
	order      []Id
	deliveries []delivery
	session    *Session
	onReport   func(err error)
}

// NewViewManager builds a manager over a session.
func NewViewManager(session *Session, onReport func(err error)) *ViewManager {
	return &ViewManager{
		records:  map[Id]*viewRecord{},
		session:  session,
		onReport: onReport,
	}
}

// Watch registers a watcher over a conversation snapshot.
func (m *ViewManager) Watch(conversation *Conversation, view *jsonx.Obj) Watch {
	record, ok := m.records[conversation.ID]
	if !ok {
		tracker := Track(view)
		tracker.Flush()
		record = &viewRecord{conversationID: conversation.ID, tracker: tracker}
		m.records[conversation.ID] = record
		m.order = append(m.order, conversation.ID)
	}
	var watcher *watchImpl
	watcher = &watchImpl{
		view:     cloneJSONObj(record.tracker.Target()),
		revision: record.revision,
		onReport: m.onReport,
		onStop: func() {
			for i, candidate := range record.watchers {
				if candidate == watcher {
					record.watchers = append(record.watchers[:i], record.watchers[i+1:]...)
					break
				}
			}
			if len(record.watchers) == 0 {
				if current, exists := m.records[conversation.ID]; exists && current == record {
					delete(m.records, conversation.ID)
					for i, id := range m.order {
						if id == conversation.ID {
							m.order = append(m.order[:i], m.order[i+1:]...)
							break
						}
					}
				}
			}
		},
	}
	record.watchers = append(record.watchers, watcher)
	return watcher
}

// EmitFlushed wraps the tracker's flushed ops (plus events) as one
// envelope for the conversation's watchers. Called by update() after the
// session line persists.
func (m *ViewManager) EmitFlushed(conversationID Id, events []ViewEvent) {
	record, ok := m.records[conversationID]
	if !ok {
		return
	}
	ops := record.tracker.Flush()
	if len(ops) == 0 && len(events) == 0 {
		return
	}
	record.revision++
	envelope := &Envelope{Revision: record.revision, Ops: ops, Events: events}
	watchers := append([]*watchImpl{}, record.watchers...)
	m.deliveries = append(m.deliveries, delivery{envelope: envelope, watchers: watchers})
}

// FailRecord drops a conversation's record and fails its watchers.
func (m *ViewManager) FailRecord(conversationID Id, err error) {
	record, ok := m.records[conversationID]
	if !ok {
		return
	}
	delete(m.records, conversationID)
	for i, id := range m.order {
		if id == conversationID {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	for _, watcher := range append([]*watchImpl{}, record.watchers...) {
		watcher.Fail(err)
	}
}

// Deliver runs after the session line; listeners are synchronous and
// ordered.
func (m *ViewManager) Deliver() {
	pending := m.deliveries
	m.deliveries = nil
	for _, d := range pending {
		for _, watcher := range d.watchers {
			watcher.Accept(d.envelope)
		}
	}
}

// Close stops every watcher and drops pending deliveries.
func (m *ViewManager) Close() {
	records := append([]*viewRecord{}, m.recordsSnapshot()...)
	m.records = map[Id]*viewRecord{}
	m.order = nil
	m.deliveries = nil
	for _, record := range records {
		for _, watcher := range append([]*watchImpl{}, record.watchers...) {
			watcher.Stop()
		}
	}
}

func (m *ViewManager) recordsSnapshot() []*viewRecord {
	out := make([]*viewRecord, 0, len(m.records))
	for _, id := range m.order {
		if record, ok := m.records[id]; ok {
			out = append(out, record)
		}
	}
	return out
}

// UpdateState mutates the conversation's tracked view state (the analog of
// the TS update() body writing record.tracker.state before flushing).
func (m *ViewManager) UpdateState(conversationID Id, mutate func(state *jsonx.Obj)) {
	if record, ok := m.records[conversationID]; ok {
		mutate(record.tracker.State())
	}
}
