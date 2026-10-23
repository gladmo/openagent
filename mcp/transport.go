package mcp

// Port of packages/mcp/src/transports/transport.ts: the transport interface
// and the shared listener bookkeeping. emitClose fires at most once per
// transport.

import (
	"sync"

	"github.com/gladmo/openagent/jsonx"

	"github.com/gladmo/openagent/abort"
)

// DefaultMaxMessageBytes bounds one decoded message.
const DefaultMaxMessageBytes = 16 * 1024 * 1024

// McpTransportSendOptions carries the per-send abort signal: aborted once
// the sender no longer waits for a reply (the request was cancelled, timed
// out, answered, or the client closed). A transport must not dispatch the
// message after that, for example when retrying it after an authorization
// refresh. It does not undo a message already dispatched.
type McpTransportSendOptions struct {
	Signal *abort.Signal
}

// McpTransportMessageListener receives decoded messages.
type McpTransportMessageListener func(message *JsonRpcMessage)

// McpTransportErrorListener receives transport errors.
type McpTransportErrorListener func(err error)

// McpTransportCloseListener runs when the transport closed.
type McpTransportCloseListener func()

// McpTransport carries JSON-RPC messages to a server.
type McpTransport interface {
	// Start prepares the transport; it fails when already started.
	Start() error
	// Send delivers one message.
	Send(message *JsonRpcMessage, options *McpTransportSendOptions) error
	// Close releases the transport; it is a no-op when already closed.
	Close() error
	// OnMessage registers a listener and returns its disposer.
	OnMessage(listener McpTransportMessageListener) (dispose func())
	// OnError registers a listener and returns its disposer.
	OnError(listener McpTransportErrorListener) (dispose func())
	// OnClose registers a listener and returns its disposer.
	OnClose(listener McpTransportCloseListener) (dispose func())
	// SetProtocolVersion stores the negotiated version; optional.
	SetProtocolVersion(version string)
}

// TransportEvents is the listener bookkeeping shared by transports.
// emitClose fires at most once per transport.
type TransportEvents struct {
	mu               sync.Mutex
	messageListeners []*messageListener
	errorListeners   []*errorListener
	closeListeners   []*closeListener
	closeEmitted     bool
}

type messageListener struct{ fn McpTransportMessageListener }
type errorListener struct{ fn McpTransportErrorListener }
type closeListener struct{ fn McpTransportCloseListener }

// OnMessage registers a listener and returns its disposer.
func (t *TransportEvents) OnMessage(listener McpTransportMessageListener) func() {
	entry := &messageListener{fn: listener}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.messageListeners = append(t.messageListeners, entry)
	return func() { t.removeEntry(&t.messageListeners, entry) }
}

// OnError registers a listener and returns its disposer.
func (t *TransportEvents) OnError(listener McpTransportErrorListener) func() {
	entry := &errorListener{fn: listener}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errorListeners = append(t.errorListeners, entry)
	return func() { t.removeEntry(&t.errorListeners, entry) }
}

// OnClose registers a listener and returns its disposer.
func (t *TransportEvents) OnClose(listener McpTransportCloseListener) func() {
	entry := &closeListener{fn: listener}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closeListeners = append(t.closeListeners, entry)
	return func() { t.removeEntry(&t.closeListeners, entry) }
}

func (t *TransportEvents) removeEntry[T any](listeners *[]*T, entry *T) {
	t.mu.Lock()
	defer t.mu.Unlock()
	updated := (*listeners)[:0]
	for _, candidate := range *listeners {
		if candidate != entry {
			updated = append(updated, candidate)
		}
	}
	*listeners = updated
}

// EmitMessage delivers a message to the registered listeners.
func (t *TransportEvents) EmitMessage(message *JsonRpcMessage) {
	t.mu.Lock()
	listeners := append([]*messageListener(nil), t.messageListeners...)
	t.mu.Unlock()
	for _, listener := range listeners {
		listener.fn(message)
	}
}

// EmitError delivers an error to the registered listeners.
func (t *TransportEvents) EmitError(err error) {
	if err == nil {
		err = toError(nil)
	}
	t.mu.Lock()
	listeners := append([]*errorListener(nil), t.errorListeners...)
	t.mu.Unlock()
	for _, listener := range listeners {
		listener.fn(err)
	}
}

// EmitClose fires the close listeners once.
func (t *TransportEvents) EmitClose() {
	t.mu.Lock()
	if t.closeEmitted {
		t.mu.Unlock()
		return
	}
	t.closeEmitted = true
	listeners := append([]*closeListener(nil), t.closeListeners...)
	t.mu.Unlock()
	for _, listener := range listeners {
		listener.fn()
	}
}

// CloneMessage deep-copies a message like structuredClone in the reference.
func CloneMessage(message *JsonRpcMessage) *JsonRpcMessage {
	return &JsonRpcMessage{Kind: message.Kind, Raw: jsonx.Clone(message.Raw).(*jsonx.Obj)}
}
