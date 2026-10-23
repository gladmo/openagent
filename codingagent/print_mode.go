package codingagent

// Port of modes/json-event.ts and modes/print-mode.ts plus core/output-
// guard.ts: the JSON wire-event projection and the single-shot print mode
// with raw-stdout writes. The runtime-host surface rides a seam (full
// runtime phase pending); AgentSession prompt/state access uses the ported
// shapes.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gladmo/openagent/ai"
)

// ============================================================================
// json-event.ts
// ============================================================================

// JsonMessageUpdateEvent is the message_update wire shape (cumulative
// assistant snapshots stripped).
type JsonMessageUpdateEvent struct {
	Type                  string          `json:"type"`
	Usage                 json.RawMessage `json:"usage"`
	AssistantMessageEvent json.RawMessage `json:"assistantMessageEvent"`
}

// ToJsonEvent projects one session event to the wire shape: message_update
// events lose their cumulative assistant snapshot (usage, tool-call ids,
// and names stay).
func ToJsonEvent(event *AgentSessionEvent) (any, error) {
	if event.Type != "message_update" {
		return event, nil
	}
	// The session's message-update projection is JSON-shaped; strip the
	// partial snapshot fields.
	return map[string]any{
		"type":                  "message_update",
		"assistantMessageEvent": stripPartial(event.AssistantMessageEvent),
	}, nil
}

// stripPartial removes "partial" keys recursively (toolcall_start carries
// a cumulative snapshot).
func stripPartial(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return stripPartialValue(value)
}

func stripPartialValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, entry := range typed {
			if key == "partial" {
				continue
			}
			out[key] = stripPartialValue(entry)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, entry := range typed {
			out[i] = stripPartialValue(entry)
		}
		return out
	}
	return value
}

// ============================================================================
// output-guard.ts
// ============================================================================

// OutputGuard serializes raw writes and retries on transient would-block
// errors. stdout/stderr takeover maps to separate writers (Go has no
// monkey-patched process.stdout).
type OutputGuard struct {
	mu     sync.Mutex
	Stdout io.Writer
	Stderr io.Writer
}

// DefaultOutputGuard writes to the process streams.
func DefaultOutputGuard() *OutputGuard {
	return &OutputGuard{Stdout: os.Stdout, Stderr: os.Stderr}
}

// NewOutputGuard builds a guard over explicit writers (tests).
func NewOutputGuard(stdout, stderr io.Writer) *OutputGuard {
	return &OutputGuard{Stdout: stdout, Stderr: stderr}
}

// WriteRawStdout writes one chunk with retry semantics.
func (g *OutputGuard) WriteRawStdout(text string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, err := io.WriteString(g.Stdout, text)
	return err
}

// WriteRawStderr writes one chunk to the error stream.
func (g *OutputGuard) WriteRawStderr(text string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, err := io.WriteString(g.Stderr, text)
	return err
}

// ============================================================================
// print-mode.ts
// ============================================================================

// PrintModeOptions configure the single-shot run.
type PrintModeOptions struct {
	// Mode is "text" (final response) or "json" (event stream).
	Mode string
	// Messages follow the initial message.
	Messages []string
	// InitialMessage is the first prompt.
	InitialMessage string
}

// PrintModeHost is the runtime-host seam print mode needs.
type PrintModeHost interface {
	// Session returns the bound session.
	Session() *AgentSession
	// Dispose tears the runtime down.
	Dispose() error
}

// RunPrintMode sends the prompts and writes the output; returns the exit
// code.
func RunPrintMode(host PrintModeHost, options PrintModeOptions, guard *OutputGuard) int {
	if guard == nil {
		guard = DefaultOutputGuard()
	}
	exitCode := 0
	session := host.Session()

	var unsubscribe func()
	jsonMode := options.Mode == "json"
	if jsonMode {
		unsubscribe = session.Subscribe(func(event *AgentSessionEvent) {
			projected, err := ToJsonEvent(event)
			if err != nil {
				return
			}
			encoded, err := json.Marshal(projected)
			if err != nil {
				return
			}
			_ = guard.WriteRawStdout(string(encoded) + "\n")
		})
	}

	defer func() {
		if unsubscribe != nil {
			unsubscribe()
		}
		_ = host.Dispose()
	}()

	if jsonMode {
		if header := session.SessionManager.GetHeader(); header != nil {
			if encoded, err := json.Marshal(header); err == nil {
				_ = guard.WriteRawStdout(string(encoded) + "\n")
			}
		}
	}

	// Send the prompts. The session's prompt pipeline runs through the
	// agent; print mode awaits idle after each message.
	if options.InitialMessage != "" {
		sendPrompt(session, options.InitialMessage)
	}
	for _, message := range options.Messages {
		sendPrompt(session, message)
	}

	if !jsonMode {
		// Text mode: print the last assistant message's text blocks.
		messages := session.Agent.Messages()
		if len(messages) > 0 {
			if assistant, ok := messages[len(messages)-1].(*assistantMessageAlias); ok {
				if assistant.StopReason == "error" || assistant.StopReason == "aborted" {
					message := "Request failed"
					if assistant.ErrorMessage != nil {
						message = *assistant.ErrorMessage
					}
					_ = guard.WriteRawStderr(message + "\n")
					exitCode = 1
				} else {
					for _, block := range assistant.Content {
						if text, ok := block.(textContentAlias); ok {
							_ = guard.WriteRawStdout(text.Text + "\n")
						}
					}
					if assistant.StopReason == "length" {
						_ = guard.WriteRawStderr("Response was truncated: the model reached its output token limit.\n")
						exitCode = 1
					}
				}
			}
		}
	}

	return exitCode
}

// sendPrompt records the user message and drives one agent run through
// the ported agent loop, blocking until the run settles (print mode is
// single-shot; the interactive driver owns the async variant).
func sendPrompt(session *AgentSession, text string) {
	message := &ai.UserMessage{Content: ai.StringContent(text)}
	session.SessionManager.AppendMessage(message)
	session.Agent.SetMessages(append(session.Agent.Messages(), message))
	session.SetAgentRunActive(true)
	if err := session.Agent.ContinueFrom(); err != nil {
		// Surface run failures as an error-stop assistant message so the
		// text/json projection paths report them uniformly.
		text := err.Error()
		session.Agent.SetMessages(append(session.Agent.Messages(), &ai.AssistantMessage{
			StopReason:   "error",
			ErrorMessage: &text,
		}))
	}
	session.SetAgentRunActive(false)
}

// aliases keep the print-mode file free of extra imports.
type assistantMessageAlias = ai.AssistantMessage
type textContentAlias = ai.TextContent

var _ = fmt.Sprintf
var _ = time.Now
var _ = strings.TrimSpace

// AssistantMessageEvent carries the raw streaming event for JSON
// projection (set by the runtime when forwarding message updates).
func (e *AgentSessionEvent) WithAssistantMessageEvent(raw json.RawMessage) *AgentSessionEvent {
	e.AssistantMessageEvent = raw
	return e
}
