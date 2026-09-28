package pico3

// chord_bridge.go ports harness/pico3/chord.ts's service definitions and
// the chord view bridge core: one publication per commit with a bounded
// envelope queue and failure teardown, over the services registry.

import (
	"fmt"

	"github.com/gladmo/openagent/chord/services"
	"github.com/gladmo/openagent/jsonx"
)

// PicoHarnessService mirrors the local-only harness service definition.
var PicoHarnessService = services.DefineService("pi.harness", struct{ Local bool }{Local: true})

// PicoConversationService mirrors the keyed conversation service.
var PicoConversationService = services.DefineService("pi.conversation", struct{ Local bool }{})

// ChordViewBridge mirrors the TS interface.
type ChordViewBridge struct {
	view      *jsonx.Obj
	closed    bool
	queue     []*Envelope
	capacity  int
	onFailure func(error)
	publish   func(view *jsonx.Obj, envelope *Envelope) (*jsonx.Obj, error)
}

// ChordViewBridgeOptions mirrors the TS interface.
type ChordViewBridgeOptions struct {
	Capacity  int
	OnFailure func(error)
}

// AttachChordView mirrors attachChordView: watch the conversation and
// bridge each commit envelope into one publication.
func AttachChordView(
	initial *jsonx.Obj,
	createState func(initial *jsonx.Obj) *jsonx.Obj,
	publish func(view *jsonx.Obj, envelope *Envelope) (*jsonx.Obj, error),
	options ChordViewBridgeOptions,
) (*ChordViewBridge, error) {
	capacity := options.Capacity
	if capacity == 0 {
		capacity = WatchCapacity
	}
	if capacity < 1 {
		return nil, fmt.Errorf("Chord view queue capacity must be positive")
	}
	base := jsonx.NewObj()
	for _, key := range initial.Keys() {
		value, _ := initial.Get(key)
		base.Set(key, value)
	}
	base.Set("commit", jsonx.ObjFrom("events", []any{}))
	view := createState(base)
	return &ChordViewBridge{
		view:      view,
		capacity:  capacity,
		onFailure: options.OnFailure,
		publish:   publish,
	}, nil
}

// View exposes the replicated state.
func (b *ChordViewBridge) View() *jsonx.Obj { return b.view }

// Closed reports the teardown state.
func (b *ChordViewBridge) Closed() bool { return b.closed }

// Enqueue offers one envelope: bounded queue, overflow fails the bridge.
func (b *ChordViewBridge) Enqueue(envelope *Envelope) error {
	if b.closed {
		return nil
	}
	if len(b.queue) >= b.capacity {
		b.fail(fmt.Errorf("chord view queue capacity %d exceeded", b.capacity))
		return nil
	}
	b.queue = append(b.queue, envelope)
	return nil
}

// Apply drains the queue: each envelope publishes atomically against the
// view with no interleaving.
func (b *ChordViewBridge) Apply() error {
	if b.closed {
		return nil
	}
	for _, envelope := range b.queue {
		next, err := b.publish(b.view, envelope)
		if err != nil {
			b.fail(err)
			return err
		}
		b.view = next
	}
	b.queue = nil
	return nil
}

// Close tears the bridge down.
func (b *ChordViewBridge) Close() {
	b.fail(nil)
}

func (b *ChordViewBridge) fail(cause error) {
	if b.closed {
		return
	}
	b.closed = true
	b.queue = nil
	if b.onFailure != nil && cause != nil {
		b.onFailure(cause)
	}
}
