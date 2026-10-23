package mcp

// Port of packages/mcp/src/transports/stdio.ts: a server as a child process
// speaking newline-delimited JSON-RPC over stdin/stdout.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gladmo/openagent/jsonx"
)

const (
	defaultMaxStderrBytes = 64 * 1024
	defaultCloseTimeout   = 2 * time.Second
	// stdinCloseGraceMs is how long a server gets to exit on its own after
	// stdin closes, before it is sent SIGTERM.
	stdinCloseGrace = 500 * time.Millisecond
)

// StdioTransportOptions configures a stdio server process.
type StdioTransportOptions struct {
	Command         string
	Args            []string
	Cwd             string
	Env             map[string]string
	InheritEnv      bool // set false to start from an empty environment
	StderrInherit   bool
	OnStderr        func(chunk string)
	MaxMessageBytes int
	MaxStderrBytes  int
	// CloseTimeout is the time to wait for the server to exit after SIGTERM
	// before sending SIGKILL. Default 2s.
	CloseTimeout time.Duration
}

// StdioTransport runs one server process.
type StdioTransport struct {
	TransportEvents
	options      StdioTransportOptions
	mu           sync.Mutex
	child        *exec.Cmd
	stdin        io.WriteCloser
	stdoutBuffer []byte
	stderrBuffer []byte
	started      bool
	closed       bool
	exited       bool
	waitDone     chan struct{}
}

// NewStdioTransport builds a stdio transport.
func NewStdioTransport(options StdioTransportOptions) *StdioTransport {
	if options.MaxMessageBytes == 0 {
		options.MaxMessageBytes = DefaultMaxMessageBytes
	}
	if options.MaxStderrBytes == 0 {
		options.MaxStderrBytes = defaultMaxStderrBytes
	}
	if options.CloseTimeout == 0 {
		options.CloseTimeout = defaultCloseTimeout
	}
	arguments := make([]string, len(options.Args))
	copy(arguments, options.Args)
	options.Args = arguments
	return &StdioTransport{options: options}
}

// Pid returns the server process id, or 0 before start.
func (t *StdioTransport) Pid() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.child != nil && t.child.Process != nil {
		return t.child.Process.Pid
	}
	return 0
}

// Stderr returns the buffered stderr text.
func (t *StdioTransport) Stderr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.stderrBuffer)
}

// Start implements McpTransport.
func (t *StdioTransport) Start() error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return errors.New("MCP stdio transport already started")
	}
	if t.closed {
		t.mu.Unlock()
		return NewMcpConnectionClosedError("")
	}
	t.started = true
	t.mu.Unlock()

	env := buildChildEnv(t.options.InheritEnv, t.options.Env)
	child := exec.Command(t.options.Command, t.options.Args...)
	child.Dir = t.options.Cwd
	child.Env = env
	var stderrWriter io.Writer
	if t.options.StderrInherit {
		child.Stderr = os.Stderr
	} else {
		stderrWriter = &lockedBuffer{transport: t}
		child.Stderr = stderrWriter
	}
	setChildProcessGroup(child)
	stdin, err := child.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		return err
	}
	if err := child.Start(); err != nil {
		return err
	}
	t.mu.Lock()
	t.child = child
	t.stdin = stdin
	t.waitDone = make(chan struct{})
	t.mu.Unlock()

	go t.readStdout(stdout)
	go t.waitChild(child)
	return nil
}

func (t *StdioTransport) waitChild(child *exec.Cmd) {
	err := child.Wait()
	t.mu.Lock()
	t.exited = true
	incomplete := len(bytes.TrimSpace(t.stdoutBuffer)) > 0
	t.stdoutBuffer = nil
	t.child = nil
	t.stdin = nil
	t.mu.Unlock()
	if err != nil && !t.isClosed() {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.EmitError(err)
		}
	}
	if incomplete {
		t.EmitError(errors.New("MCP stdio server closed with an incomplete JSON-RPC message"))
	}
	t.EmitClose()
	close(t.waitDone)
}

func (t *StdioTransport) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// Send implements McpTransport.
func (t *StdioTransport) Send(message *JsonRpcMessage, _ *McpTransportSendOptions) error {
	t.mu.Lock()
	started, closed := t.started, t.closed
	stdin := t.stdin
	t.mu.Unlock()
	if !started || closed || stdin == nil {
		return NewMcpConnectionClosedError("")
	}
	payload := jsonx.Stringify(message.Raw) + "\n"
	_, err := io.WriteString(stdin, payload)
	return err
}

// SetProtocolVersion implements McpTransport (stdio ignores it).
func (t *StdioTransport) SetProtocolVersion(string) {}

// Close implements McpTransport. Shutdown per the spec: close stdin and let
// the server exit, then SIGTERM, then SIGKILL.
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	child, stdin, exited, waitDone := t.child, t.stdin, t.exited, t.waitDone
	t.mu.Unlock()
	if child == nil {
		t.EmitClose()
		return nil
	}
	if stdin != nil {
		_ = stdin.Close()
	}
	if exited {
		return nil
	}
	closeTimeout := t.options.CloseTimeout
	grace := stdinCloseGrace
	if closeTimeout < grace {
		grace = closeTimeout
	}
	termTimer := time.AfterFunc(grace, func() { killProcessTree(child, syscall.SIGTERM) })
	killTimer := time.AfterFunc(grace+closeTimeout, func() { killProcessTree(child, syscall.SIGKILL) })
	defer termTimer.Stop()
	defer killTimer.Stop()
	<-waitDone
	// Children of the server that ignored stdin closing would otherwise
	// outlive it.
	killProcessTree(child, syscall.SIGTERM)
	return nil
}

func (t *StdioTransport) readStdout(stdout io.Reader) {
	buffer := make([]byte, 32*1024)
	for {
		read, err := stdout.Read(buffer)
		if read > 0 {
			t.handleStdout(buffer[:read])
		}
		if err != nil {
			return
		}
	}
}

func (t *StdioTransport) handleStdout(chunk []byte) {
	t.mu.Lock()
	t.stdoutBuffer = append(t.stdoutBuffer, chunk...)
	maxMessageBytes := t.options.MaxMessageBytes
	t.mu.Unlock()
	for {
		t.mu.Lock()
		newline := bytes.IndexByte(t.stdoutBuffer, 0x0a)
		var line []byte
		if newline >= 0 {
			line = t.stdoutBuffer[:newline]
			t.stdoutBuffer = t.stdoutBuffer[newline+1:]
		} else if len(t.stdoutBuffer) > maxMessageBytes {
			t.stdoutBuffer = nil
			t.mu.Unlock()
			t.EmitError(errors.New("MCP stdio message exceeds " + itoa(maxMessageBytes) + " bytes"))
			return
		} else {
			t.mu.Unlock()
			return
		}
		oversized := len(line) > maxMessageBytes
		t.mu.Unlock()
		if oversized {
			t.EmitError(errors.New("MCP stdio message exceeds " + itoa(maxMessageBytes) + " bytes"))
			continue
		}
		text := strings.TrimSuffix(string(line), "\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		value, err := jsonx.Parse(text)
		if err != nil {
			t.EmitError(toError(err))
			continue
		}
		message, err := ParseJsonRpcMessage(value)
		if err != nil {
			t.EmitError(err)
			continue
		}
		t.EmitMessage(message)
	}
}

// lockedBuffer forwards the child's stderr into the ring buffer.
type lockedBuffer struct {
	transport *StdioTransport
}

func (b *lockedBuffer) Write(chunk []byte) (int, error) {
	t := b.transport
	t.mu.Lock()
	maxStderrBytes := t.options.MaxStderrBytes
	t.stderrBuffer = append(t.stderrBuffer, chunk...)
	if len(t.stderrBuffer) > maxStderrBytes {
		t.stderrBuffer = t.stderrBuffer[len(t.stderrBuffer)-maxStderrBytes:]
	}
	onStderr := t.options.OnStderr
	t.mu.Unlock()
	if onStderr != nil {
		onStderr(string(chunk))
	}
	return len(chunk), nil
}

// buildChildEnv merges the inherited environment with the overrides; a
// later entry with the same name replaces the earlier one.
func buildChildEnv(inheritEnv bool, overrides map[string]string) []string {
	positions := map[string]int{}
	env := make([]string, 0, len(overrides)+16)
	add := func(entry string) {
		name := entry
		if equals := strings.IndexByte(entry, '='); equals >= 0 {
			name = entry[:equals]
		}
		if at, ok := positions[name]; ok {
			env[at] = entry
			return
		}
		positions[name] = len(env)
		env = append(env, entry)
	}
	if inheritEnv {
		for _, entry := range os.Environ() {
			add(entry)
		}
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		add(name + "=" + overrides[name])
	}
	return env
}

func itoa(value int) string { return strconv.Itoa(value) }
