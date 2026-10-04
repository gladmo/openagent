package codingagent

// Port of modes/rpc/jsonl.ts and modes/rpc/rpc-types.ts plus the core of
// modes/rpc/rpc-mode.ts: strict LF-only JSONL framing, the RPC command/
// response vocabulary, and the command dispatcher over AgentSession.
// Extension-UI request/response bridging rides the seam (extensions
// phase).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/gladmo/openagent/ai"
)

// ============================================================================
// jsonl.ts
// ============================================================================

// SerializeJsonLine renders one strict JSONL record (LF-only framing;
// payload strings may contain U+2028/U+2029).
func SerializeJsonLine(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}\n"
	}
	return string(encoded) + "\n"
}

// AttachJsonlLineReader reads LF-only JSONL lines (CR trimmed, final line
// without newline emitted); the returned detach stops delivery.
func AttachJsonlLineReader(reader io.Reader, onLine func(line string)) func() {
	scanner := bufio.NewReader(reader)
	var buffer []byte
	for {
		chunk, err := scanner.ReadBytes('\n')
		if len(chunk) > 0 {
			if chunk[len(chunk)-1] == '\n' {
				buffer = append(buffer, chunk[:len(chunk)-1]...)
				emitJsonlLine(buffer, onLine)
				buffer = nil
			} else {
				// Final chunk without newline (EOF).
				buffer = append(buffer, chunk...)
				if err == io.EOF && len(buffer) > 0 {
					emitJsonlLine(buffer, onLine)
				}
				return func() {}
			}
		}
		if err != nil {
			return func() {}
		}
	}
}

// ============================================================================
// rpc-types.ts
// ============================================================================

// RpcCommand is one stdin command.
type RpcCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`

	// prompt / steer / follow_up
	Message           string `json:"message,omitempty"`
	StreamingBehavior string `json:"streamingBehavior,omitempty"`

	// set_model
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"modelId,omitempty"`

	// set_thinking_level
	Level string `json:"level,omitempty"`

	// set_steering_mode / set_follow_up_mode
	Mode string `json:"mode,omitempty"`

	// set_auto_compaction / set_auto_retry
	Enabled *bool `json:"enabled,omitempty"`

	// bash
	Command            string `json:"command,omitempty"`
	ExcludeFromContext *bool  `json:"excludeFromContext,omitempty"`

	// switch_session / fork / set_session_name
	SessionPath string `json:"sessionPath,omitempty"`
	EntryID     string `json:"entryId,omitempty"`
	Name        string `json:"name,omitempty"`

	// new_session
	ParentSession string `json:"parentSession,omitempty"`

	// get_entries
	Since string `json:"since,omitempty"`

	// compact
	CustomInstructions string `json:"customInstructions,omitempty"`
}

// RpcSessionState is the get_state payload.
type RpcSessionState struct {
	Model                 *modelForWire `json:"model,omitempty"`
	ThinkingLevel         string        `json:"thinkingLevel"`
	IsStreaming           bool          `json:"isStreaming"`
	IsCompacting          bool          `json:"isCompacting"`
	SteeringMode          string        `json:"steeringMode"`
	FollowUpMode          string        `json:"followUpMode"`
	SessionFile           string        `json:"sessionFile,omitempty"`
	SessionID             string        `json:"sessionId"`
	SessionName           string        `json:"sessionName,omitempty"`
	AutoCompactionEnabled bool          `json:"autoCompactionEnabled"`
	MessageCount          int           `json:"messageCount"`
	PendingMessageCount   int           `json:"pendingMessageCount"`
}

type modelForWire = struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// RpcResponse is one stdout response.
type RpcResponse struct {
	ID      string         `json:"id,omitempty"`
	Type    string         `json:"type"` // "response"
	Command string         `json:"command"`
	Success bool           `json:"success"`
	Data    map[string]any `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// ============================================================================
// rpc-mode.ts dispatcher
// ============================================================================

// RpcHost abstracts the runtime surface the dispatcher needs.
type RpcHost interface {
	Session() *AgentSession
	Dispose() error
}

// RpcDispatcher turns commands into responses over one session.
type RpcDispatcher struct {
	host  RpcHost
	guard *OutputGuard
}

// NewRpcDispatcher builds the dispatcher.
func NewRpcDispatcher(host RpcHost, guard *OutputGuard) *RpcDispatcher {
	if guard == nil {
		guard = DefaultOutputGuard()
	}
	return &RpcDispatcher{host: host, guard: guard}
}

// Output writes one JSON line.
func (d *RpcDispatcher) Output(value any) {
	_ = d.guard.WriteRawStdout(SerializeJsonLine(value))
}

func rpcSuccess(id, command string, data map[string]any) *RpcResponse {
	return &RpcResponse{ID: id, Type: "response", Command: command, Success: true, Data: data}
}

func rpcError(id, command, message string) *RpcResponse {
	return &RpcResponse{ID: id, Type: "response", Command: command, Success: false, Error: message}
}

// Dispatch executes one command and returns its response (nil when the
// command emitted its own response).
func (d *RpcDispatcher) Dispatch(raw []byte) *RpcResponse {
	var command RpcCommand
	if err := json.Unmarshal(raw, &command); err != nil {
		return rpcError("", "unknown", "Invalid JSON command: "+err.Error())
	}
	session := d.host.Session()

	switch command.Type {
	case "prompt":
		sendPrompt(session, command.Message)
		return rpcSuccess(command.ID, "prompt", map[string]any{"disposition": "started"})

	case "steer":
		session.Steer(command.Message)
		return rpcSuccess(command.ID, "steer", map[string]any{"disposition": "queued"})

	case "follow_up":
		session.FollowUp(command.Message)
		return rpcSuccess(command.ID, "follow_up", map[string]any{"disposition": "queued"})

	case "abort":
		session.Abort()
		return rpcSuccess(command.ID, "abort", nil)

	case "clear_queue":
		steering := session.ClearSteering()
		followUp := session.ClearFollowUp()
		return rpcSuccess(command.ID, "clear_queue", map[string]any{
			"steering": len(steering), "followUp": len(followUp),
		})

	case "get_state":
		header := session.SessionManager.GetHeader()
		state := &RpcSessionState{
			ThinkingLevel: session.ThinkingLevel(),
			IsStreaming:   session.IsStreaming(),
			SteeringMode:  session.SettingsManager.GetSteeringMode(),
			FollowUpMode:  session.SettingsManager.GetFollowUpMode(),
			SessionFile:   session.SessionManager.GetSessionFile(),
		}
		if header != nil {
			state.SessionID = header.ID
		}
		state.SessionName = session.SessionManager.GetSessionName()
		state.MessageCount = len(session.Agent.Messages())
		return rpcSuccess(command.ID, "get_state", map[string]any{"state": state})

	case "set_model":
		model := session.ModelRuntime.GetModel(command.Provider, command.ModelID)
		if model == nil {
			return rpcError(command.ID, "set_model",
				fmt.Sprintf("Model not found: %s/%s", command.Provider, command.ModelID))
		}
		if err := session.SetModel(model, false); err != nil {
			return rpcError(command.ID, "set_model", err.Error())
		}
		return rpcSuccess(command.ID, "set_model", map[string]any{
			"model": &modelForWire{Provider: model.Provider, ID: model.ID},
		})

	case "cycle_model":
		result := session.CycleModel("forward", false)
		if result == nil {
			return rpcSuccess(command.ID, "cycle_model", nil)
		}
		return rpcSuccess(command.ID, "cycle_model", map[string]any{
			"model":         &modelForWire{Provider: result.Model.Provider, ID: result.Model.ID},
			"thinkingLevel": result.ThinkingLevel,
		})

	case "get_available_models":
		models := session.ModelRuntime.GetAvailableSnapshot()
		wire := make([]*modelForWire, 0, len(models))
		for _, model := range models {
			wire = append(wire, &modelForWire{Provider: model.Provider, ID: model.ID})
		}
		return rpcSuccess(command.ID, "get_available_models", map[string]any{"models": wire})

	case "set_thinking_level":
		session.SetThinkingLevel(command.Level)
		return rpcSuccess(command.ID, "set_thinking_level", nil)

	case "get_available_thinking_levels":
		return rpcSuccess(command.ID, "get_available_thinking_levels", map[string]any{
			"levels": ThinkingLevelOptions,
		})

	case "set_steering_mode":
		session.SettingsManager.SetSteeringMode(command.Mode)
		return rpcSuccess(command.ID, "set_steering_mode", nil)

	case "set_follow_up_mode":
		session.SettingsManager.SetFollowUpMode(command.Mode)
		return rpcSuccess(command.ID, "set_follow_up_mode", nil)

	case "get_messages":
		messages := session.Agent.Messages()
		encoded := make([]json.RawMessage, 0, len(messages))
		for _, message := range messages {
			raw := marshalAgentMessageWithRole(message)
			encoded = append(encoded, json.RawMessage(raw))
		}
		return rpcSuccess(command.ID, "get_messages", map[string]any{"messages": encoded})

	case "get_entries":
		entries := session.SessionManager.GetEntries()
		encoded := make([]json.RawMessage, 0, len(entries))
		for _, entry := range entries {
			raw, _ := json.Marshal(entry)
			encoded = append(encoded, raw)
		}
		return rpcSuccess(command.ID, "get_entries", map[string]any{"entries": encoded})

	case "get_last_assistant_text":
		messages := session.Agent.Messages()
		for i := len(messages) - 1; i >= 0; i-- {
			if assistant, ok := messages[i].(interface {
				Role() string
			}); ok && assistant.Role() == "assistant" {
				// Text extraction over the last assistant message.
				return rpcSuccess(command.ID, "get_last_assistant_text", map[string]any{
					"text": extractAssistantText(messages[i]),
				})
			}
		}
		return rpcSuccess(command.ID, "get_last_assistant_text", map[string]any{"text": ""})

	case "set_session_name":
		session.SessionManager.AppendSessionInfo(command.Name)
		return rpcSuccess(command.ID, "set_session_name", nil)

	case "get_tree":
		tree := session.SessionManager.GetTree()
		encoded := make([]json.RawMessage, 0, len(tree))
		for _, node := range tree {
			raw, _ := json.Marshal(node.Entry)
			encoded = append(encoded, raw)
		}
		return rpcSuccess(command.ID, "get_tree", map[string]any{"tree": encoded})

	default:
		return rpcError(command.ID, command.Type, "Unknown command type: "+command.Type)
	}
}

// extractAssistantText pulls the text blocks from a typed assistant
// message.
func extractAssistantText(message interface{}) string {
	assistant, ok := message.(*ai.AssistantMessage)
	if !ok {
		return ""
	}
	var parts []string
	for _, block := range assistant.Content {
		if text, ok := block.(ai.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func emitJsonlLine(line []byte, onLine func(string)) {
	text := string(line)
	if strings.HasSuffix(text, "\r") {
		text = text[:len(text)-1]
	}
	onLine(text)
}
