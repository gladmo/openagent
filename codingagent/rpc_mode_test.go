package codingagent

// Tests for the RPC dispatcher and JSONL framing (ported from rpc-mode
// and jsonl test surfaces).

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

type stubRpcHost struct {
	session *AgentSession
}

func (h *stubRpcHost) Session() *AgentSession { return h.session }
func (h *stubRpcHost) Dispose() error         { return nil }

func newRpcDispatcher(t *testing.T) (*RpcDispatcher, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(EnvAgentDirName(), dir)
	manager, _ := SessionManagerCreate(dir, "", nil)
	testAgent := agent.NewAgent(agent.AgentOptions{})
	// sendPrompt drives a real run; stub the stream so prompts complete
	// deterministically without a provider.
	testAgent.StreamFunction = func(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		go func() {
			message := &ai.AssistantMessage{StopReason: "stop"}
			stream.Push(&ai.EventStart{Partial: message})
			stream.Push(&ai.EventDone{Message: message, Reason: "stop"})
		}()
		return stream
	}
	session := NewAgentSession(AgentSessionRuntimeOptions{
		Config: AgentSessionConfig{
			Agent:           testAgent,
			SessionManager:  manager,
			SettingsManager: SettingsManagerInMemory(Settings{}, nil),
			Cwd:             dir,
		},
	})
	runtime, err := NewModelRuntime(&CreateModelRuntimeOptions{
		AuthPath:      dir + "/auth.json",
		ModelsPath:    dir + "/absent-models.json",
		HasModelsPath: true,
	})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	session.ModelRuntime = runtime

	stdout := &bytes.Buffer{}
	return NewRpcDispatcher(&stubRpcHost{session: session}, NewOutputGuard(stdout, nil)), stdout
}

func dispatchJSON(t *testing.T, d *RpcDispatcher, command string) map[string]any {
	t.Helper()
	response := d.Dispatch([]byte(command))
	if response == nil {
		t.Fatalf("no response for %q", command)
	}
	encoded, _ := json.Marshal(response)
	var parsed map[string]any
	if err := json.Unmarshal(encoded, &parsed); err != nil {
		t.Fatalf("parse %q: %v", encoded, err)
	}
	return parsed
}

func TestRpcDispatcherPrompting(t *testing.T) {
	dispatcher, _ := newRpcDispatcher(t)

	response := dispatchJSON(t, dispatcher, `{"id":"1","type":"prompt","message":"hello"}`)
	if response["command"] != "prompt" || response["success"] != true {
		t.Fatalf("prompt = %v", response)
	}
	data := response["data"].(map[string]any)
	if data["disposition"] != "started" {
		t.Fatalf("disposition = %v", data)
	}

	response = dispatchJSON(t, dispatcher, `{"id":"2","type":"steer","message":"mid"}`)
	if response["command"] != "steer" {
		t.Fatalf("steer = %v", response)
	}
	if got := dispatcher.host.Session().GetSteeringMessages(); len(got) != 1 || got[0] != "mid" {
		t.Fatalf("steered = %v", got)
	}

	response = dispatchJSON(t, dispatcher, `{"id":"3","type":"follow_up","message":"later"}`)
	if response["command"] != "follow_up" {
		t.Fatalf("follow_up = %v", response)
	}

	// clear_queue empties both.
	response = dispatchJSON(t, dispatcher, `{"id":"4","type":"clear_queue"}`)
	if response["success"] != true {
		t.Fatalf("clear = %v", response)
	}
	if len(dispatcher.host.Session().GetSteeringMessages()) != 0 || len(dispatcher.host.Session().GetFollowUpMessages()) != 0 {
		t.Fatal("queues not cleared")
	}
}

func TestRpcDispatcherGetState(t *testing.T) {
	dispatcher, _ := newRpcDispatcher(t)

	response := dispatchJSON(t, dispatcher, `{"id":"1","type":"get_state"}`)
	data := response["data"].(map[string]any)
	state := data["state"].(map[string]any)
	if state["thinkingLevel"] == "" {
		t.Fatal("thinking level missing")
	}
	if state["sessionId"] == "" {
		t.Fatal("session id missing")
	}
	if state["steeringMode"] != "one-at-a-time" {
		t.Fatalf("steering = %v", state["steeringMode"])
	}
}

func TestRpcDispatcherSetModel(t *testing.T) {
	dispatcher, _ := newRpcDispatcher(t)

	// Unknown model errors.
	response := dispatchJSON(t, dispatcher, `{"id":"1","type":"set_model","provider":"x","modelId":"y"}`)
	if response["success"] != false || !strings.Contains(response["error"].(string), "Model not found") {
		t.Fatalf("missing model = %v", response)
	}
}

func TestRpcDispatcherThinking(t *testing.T) {
	dispatcher, _ := newRpcDispatcher(t)

	response := dispatchJSON(t, dispatcher, `{"id":"1","type":"set_thinking_level","level":"high"}`)
	if response["success"] != true {
		t.Fatalf("set = %v", response)
	}
	if dispatcher.host.Session().ThinkingLevel() != "high" {
		t.Fatal("level not applied")
	}

	response = dispatchJSON(t, dispatcher, `{"id":"2","type":"get_available_thinking_levels"}`)
	data := response["data"].(map[string]any)
	levels := data["levels"].([]any)
	if len(levels) != 7 {
		t.Fatalf("levels = %v", levels)
	}
}

func TestRpcDispatcherMessages(t *testing.T) {
	dispatcher, _ := newRpcDispatcher(t)

	// Prompt recorded a user message.
	dispatchJSON(t, dispatcher, `{"type":"prompt","message":"recorded"}`)
	response := dispatchJSON(t, dispatcher, `{"id":"1","type":"get_messages"}`)
	data := response["data"].(map[string]any)
	messages := data["messages"].([]any)
	if len(messages) < 1 {
		t.Fatalf("messages = %d", len(messages))
	}
	first := messages[0].(map[string]any)
	if first["role"] != "user" {
		t.Fatalf("role = %v", first["role"])
	}

	// get_entries carries session entries.
	response = dispatchJSON(t, dispatcher, `{"id":"2","type":"get_entries"}`)
	data = response["data"].(map[string]any)
	if len(data["entries"].([]any)) == 0 {
		t.Fatal("no entries")
	}
}

func TestRpcDispatcherLastAssistantText(t *testing.T) {
	dispatcher, _ := newRpcDispatcher(t)
	session := dispatcher.host.Session()

	session.Agent.SetMessages([]agent.AgentMessage{
		&ai.AssistantMessage{Content: []ai.ContentBlock{ai.TextContent{Text: "final answer"}}},
	})
	response := dispatchJSON(t, dispatcher, `{"id":"1","type":"get_last_assistant_text"}`)
	data := response["data"].(map[string]any)
	if data["text"] != "final answer" {
		t.Fatalf("text = %v", data["text"])
	}
}

func TestRpcDispatcherSessionName(t *testing.T) {
	dispatcher, _ := newRpcDispatcher(t)

	response := dispatchJSON(t, dispatcher, `{"id":"1","type":"set_session_name","name":"my session"}`)
	if response["success"] != true {
		t.Fatalf("set = %v", response)
	}
	if got := dispatcher.host.Session().SessionManager.GetSessionName(); got != "my session" {
		t.Fatalf("name = %q", got)
	}
}

func TestRpcDispatcherInvalid(t *testing.T) {
	dispatcher, _ := newRpcDispatcher(t)

	response := dispatchJSON(t, dispatcher, `{not json`)
	if response["success"] != false {
		t.Fatalf("invalid = %v", response)
	}

	response = dispatchJSON(t, dispatcher, `{"id":"1","type":"definitely_not_a_command"}`)
	if response["success"] != false || !strings.Contains(response["error"].(string), "Unknown command") {
		t.Fatalf("unknown = %v", response)
	}
}

func TestSerializeJsonLine(t *testing.T) {
	if got := SerializeJsonLine(map[string]any{"a": 1}); got != "{\"a\":1}\n" {
		t.Fatalf("serialize = %q", got)
	}
	// U+2028 stays inline (LF-only framing).
	if got := SerializeJsonLine(map[string]any{"s": "a b"}); !strings.HasSuffix(got, "\n") || strings.Contains(got, "\n\"") {
		t.Fatalf("framing = %q", got)
	}
}

func TestAttachJsonlLineReader(t *testing.T) {
	input := "{\"a\":1}\n{\"a\":2}\r\n{\"a\":3}"
	var lines []string
	done := make(chan struct{})
	detach := AttachJsonlLineReader(strings.NewReader(input), func(line string) {
		lines = append(lines, line)
		if len(lines) == 3 {
			close(done)
		}
	})
	defer detach()
	<-done

	if len(lines) != 3 {
		t.Fatalf("lines = %v", lines)
	}
	if lines[0] != "{\"a\":1}" || lines[1] != "{\"a\":2}" || lines[2] != "{\"a\":3}" {
		t.Fatalf("lines = %q", lines)
	}
}

func TestRpcDispatcherOutput(t *testing.T) {
	dispatcher, stdout := newRpcDispatcher(t)
	dispatcher.Output(map[string]any{"type": "response", "success": true})
	if !strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("output = %q", stdout.String())
	}
	var parsed map[string]any
	json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &parsed)
	if parsed["success"] != true {
		t.Fatalf("parsed = %v", parsed)
	}
}
