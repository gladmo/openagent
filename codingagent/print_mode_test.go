package codingagent

// Tests for the print mode, JSON event projection, and output guard.

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"

	"os"
	"time"
)

type stubPrintHost struct {
	session *AgentSession
}

func (h *stubPrintHost) Session() *AgentSession { return h.session }
func (h *stubPrintHost) Dispose() error         { return nil }

func newPrintHarness(t *testing.T) (*stubPrintHost, *OutputGuard, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(EnvAgentDirName(), dir)
	manager, _ := SessionManagerCreate(dir, "", nil)
	session := NewAgentSession(AgentSessionRuntimeOptions{
		Config: AgentSessionConfig{
			Agent:           agent.NewAgent(agent.AgentOptions{}),
			SessionManager:  manager,
			SettingsManager: SettingsManagerInMemory(Settings{}, nil),
			Cwd:             dir,
		},
	})
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	guard := NewOutputGuard(stdout, stderr)
	return &stubPrintHost{session: session}, guard, stdout, stderr
}

func TestRunPrintModeText(t *testing.T) {
	host, guard, stdout, stderr := newPrintHarness(t)

	// Simulate the agent producing an assistant reply.
	setAgentMessages(host.session.Agent, &ai.AssistantMessage{Content: []ai.ContentBlock{ai.TextContent{Text: "hello world"}}})
	_ = guard

	code := RunPrintMode(host, PrintModeOptions{Mode: "text"}, guard)
	if code != 0 {
		t.Fatalf("code = %d (stderr = %q)", code, stderr.String())
	}
	if stdout.String() != "hello world\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunPrintModeTextError(t *testing.T) {
	host, guard, stdout, stderr := newPrintHarness(t)

	errorMessage := "boom"
	setAgentMessages(host.session.Agent, &ai.AssistantMessage{StopReason: "error", ErrorMessage: &errorMessage})

	code := RunPrintMode(host, PrintModeOptions{Mode: "text"}, guard)
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunPrintModeJSON(t *testing.T) {
	host, guard, stdout, _ := newPrintHarness(t)

	code := RunPrintMode(host, PrintModeOptions{
		Mode:           "json",
		InitialMessage: "hi",
	}, guard)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("lines = %q", stdout.String())
	}

	// First line: the session header.
	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("header: %v", err)
	}
	if header["type"] != "session" {
		t.Fatalf("header = %v", header)
	}

	// Subsequent lines: JSON events (agent_settled from the prompt cycle).
	var settledSeen bool
	for _, line := range lines[1:] {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("event %q: %v", line, err)
		}
		if event["type"] == "agent_settled" {
			settledSeen = true
		}
	}
	if !settledSeen {
		t.Fatalf("no agent_settled in %q", stdout.String())
	}
}

func TestToJsonEventStripsPartial(t *testing.T) {
	event := &AgentSessionEvent{Type: "message_update"}
	event.WithAssistantMessageEvent(json.RawMessage(`{
		"type": "toolcall_start",
		"partial": {"content": [{"type": "toolCall", "id": "t1", "name": "read"}]},
		"contentIndex": 0
	}`))

	projected, err := ToJsonEvent(event)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	encoded, _ := json.Marshal(projected)
	var parsed map[string]any
	json.Unmarshal(encoded, &parsed)
	inner, _ := parsed["assistantMessageEvent"].(map[string]any)
	if inner == nil {
		t.Fatalf("projected = %v", parsed)
	}
	if _, has := inner["partial"]; has {
		t.Fatal("partial survived")
	}
	if inner["type"] != "toolcall_start" {
		t.Fatalf("inner = %v", inner)
	}
}

func TestToJsonEventPassthrough(t *testing.T) {
	event := &AgentSessionEvent{Type: EventAgentSettled}
	projected, err := ToJsonEvent(event)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if projected != any(event) {
		t.Fatal("non-message_update should pass through")
	}
}

func TestOutputGuard(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	guard := NewOutputGuard(stdout, stderr)

	if err := guard.WriteRawStdout("out"); err != nil {
		t.Fatalf("stdout: %v", err)
	}
	if err := guard.WriteRawStderr("err"); err != nil {
		t.Fatalf("stderr: %v", err)
	}
	if stdout.String() != "out" || stderr.String() != "err" {
		t.Fatalf("buffers = %q / %q", stdout.String(), stderr.String())
	}
}

func TestOutputGuardConcurrent(t *testing.T) {
	stdout := &syncBuffer{}
	guard := NewOutputGuard(stdout, nil)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = guard.WriteRawStdout("x")
		}(i)
	}
	wg.Wait()
	if stdout.Len() != 10 {
		t.Fatalf("len = %d", stdout.Len())
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// setAgentMessages replaces the agent transcript for assertions.
func setAgentMessages(a *agent.Agent, messages ...agent.AgentMessage) {
	a.SetMessages(messages)
}

// Port of 441f5767c: a length-truncated response warns and exits 1.
func TestPrintModeWarnsOnTruncatedResponse(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvAgentDirName(), dir)
	host, err := SetupPrintSession(PrintSessionOptions{Cwd: dir, Mode: string(ModeText)})
	if err != nil {
		t.Fatal(err)
	}
	session := host.Session()
	session.Agent.StreamFunction = func(*ai.Model, *ai.TranscriptContext, *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		message := &ai.AssistantMessage{
			Content: []ai.ContentBlock{ai.TextContent{Text: "partial"}}, StopReason: ai.StopLength,
			TimestampMs: float64(time.Now().UnixMilli()),
		}
		go func() {
			stream.Push(&ai.EventDone{Reason: ai.StopLength, Message: message})
		}()
		return stream
	}
	session.Agent.Prompt(&ai.UserMessage{Content: ai.StringContent("go"), TimestampMs: float64(time.Now().UnixMilli())})
	var stderr bytes.Buffer
	code := RunPrintMode(host, PrintModeOptions{}, NewOutputGuard(os.Stdout, &stderr))
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "Response was truncated") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
