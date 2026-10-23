package mcp

// Port of packages/mcp/src/transports/in-memory.ts: a transport pair wired
// directly together, for tests.

import "sync"

// InMemoryTransport delivers messages to its peer.
type InMemoryTransport struct {
	TransportEvents
	mu      sync.Mutex
	peer    *InMemoryTransport
	started bool
	closed  bool
}

// ConnectPeer wires this transport to its peer; each transport accepts one
// peer only.
func (t *InMemoryTransport) ConnectPeer(peer *InMemoryTransport) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.peer != nil {
		panic("In-memory MCP transport already has a peer")
	}
	t.peer = peer
}

// Start implements McpTransport.
func (t *InMemoryTransport) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return NewMcpConnectionClosedError("")
	}
	t.started = true
	return nil
}

// Send implements McpTransport.
func (t *InMemoryTransport) Send(message *JsonRpcMessage, _ *McpTransportSendOptions) error {
	t.mu.Lock()
	started, closed, peer := t.started, t.closed, t.peer
	t.mu.Unlock()
	if !started || closed {
		return NewMcpConnectionClosedError("")
	}
	if peer == nil || !peer.isStarted() || peer.isClosed() {
		return NewMcpConnectionClosedError("In-memory MCP peer is not connected")
	}
	copy := CloneMessage(message)
	go peer.deliver(copy)
	return nil
}

// Close implements McpTransport.
func (t *InMemoryTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	peer := t.peer
	t.mu.Unlock()
	t.EmitClose()
	if peer != nil {
		return peer.Close()
	}
	return nil
}

// SetProtocolVersion implements McpTransport (in-memory ignores it).
func (t *InMemoryTransport) SetProtocolVersion(string) {}

// EmitErrorError exposes EmitError so tests can simulate transport-level
// failures.
func (t *InMemoryTransport) EmitErrorError(err error) { t.EmitError(err) }

func (t *InMemoryTransport) isStarted() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.started
}

func (t *InMemoryTransport) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *InMemoryTransport) deliver(message *JsonRpcMessage) {
	if t.isClosed() {
		return
	}
	t.EmitMessage(message)
}

// CreateInMemoryTransportPair builds a connected transport pair.
func CreateInMemoryTransportPair() (client *InMemoryTransport, server *InMemoryTransport) {
	client = &InMemoryTransport{}
	server = &InMemoryTransport{}
	client.ConnectPeer(server)
	server.ConnectPeer(client)
	return client, server
}
