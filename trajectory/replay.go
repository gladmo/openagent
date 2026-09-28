// trajectory/replay.go: regression replay. A recorded trajectory's
// assistant messages become a faux-provider script; a fresh agent over that
// script reproduces the run without network or keys. An override sidecar
// (JSON) patches or appends entries the log alone cannot express — thrown
// calls, hangs, error variants. Tools marked Replay "never" are refused
// during replay instead of re-executed.
package trajectory

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// ReplayProvider and ReplayModelID identify the faux provider replay mounts.
const (
	ReplayProvider = "trajectory-replay"
	ReplayAPI      = "replay"
)

// DeriveScript extracts one faux response step per assistant/message record,
// in commit order. assistant/attempt records are skipped by default — they
// are retry material — and an override can inject them where a regression
// needs a failure first.
func DeriveScript(t *Trajectory) ([]ai.FauxResponseStep, error) {
	return DeriveScriptFromRecords(t.Snapshot())
}

// DeriveScriptFromRecords is DeriveScript over a record slice (for stored
// trajectories).
func DeriveScriptFromRecords(records []Record) ([]ai.FauxResponseStep, error) {
	var script []ai.FauxResponseStep
	for i := range records {
		rec := &records[i]
		if rec.Type != KindAssistantMessage {
			continue
		}
		obj, ok := rec.Data.(*jsonx.Obj)
		if !ok {
			continue
		}
		messageValue, ok := obj.Get("message")
		if !ok {
			continue
		}
		message, err := ai.MessageFromJSON(messageValue)
		if err != nil {
			return nil, fmt.Errorf("trajectory: replay step %d: %w", len(script)+1, err)
		}
		assistant, ok := message.(*ai.AssistantMessage)
		if !ok || assistant == nil {
			return nil, fmt.Errorf("trajectory: replay step %d: message is not an assistant message", len(script)+1)
		}
		script = append(script, ai.FauxStep(assistant))
	}
	if len(script) == 0 {
		return nil, fmt.Errorf("trajectory: no assistant messages to replay")
	}
	return script, nil
}

// ReplayPatch replaces or appends one script entry. At == len(script)
// appends; At beyond that is refused.
type ReplayPatch struct {
	At      int    `json:"at"`
	Kind    string `json:"kind"` // "throw" | "hang" | "message"
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
}

// LoadReplayOverride reads the override sidecar (a JSON array of patches).
func LoadReplayOverride(path string) ([]ReplayPatch, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("trajectory: read override: %w", err)
	}
	var patches []ReplayPatch
	if err := json.Unmarshal(raw, &patches); err != nil {
		return nil, fmt.Errorf("trajectory: parse override: %w", err)
	}
	return patches, nil
}

// ApplyOverride patches a derived script in place order.
func ApplyOverride(script []ai.FauxResponseStep, patches []ReplayPatch) ([]ai.FauxResponseStep, error) {
	out := append([]ai.FauxResponseStep{}, script...)
	for _, patch := range patches {
		if patch.At > len(out) {
			return nil, fmt.Errorf("trajectory: override at %d exceeds script length %d", patch.At, len(out))
		}
		step, err := patch.step()
		if err != nil {
			return nil, err
		}
		if patch.At == len(out) {
			out = append(out, step)
			continue
		}
		out[patch.At] = step
	}
	return out, nil
}

func (p ReplayPatch) step() (ai.FauxResponseStep, error) {
	switch p.Kind {
	case "throw":
		message := p.Message
		if message == "" {
			message = "replay-injected failure"
		}
		stop := ai.StopError
		return ai.FauxStepFn(func(_ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *ai.FauxProviderState, model *ai.Model) *ai.AssistantMessage {
			return ai.FauxAssistantMessage("", ai.FauxAssistantMessageOptions{
				StopReason: &stop, ErrorMessage: &message,
			})
		}), nil
	case "hang":
		aborted := ai.StopAborted
		return ai.FauxStepFn(func(_ *ai.TranscriptContext, options *ai.SimpleStreamOptions, _ *ai.FauxProviderState, _ *ai.Model) *ai.AssistantMessage {
			// Wait for the abort signal: a hang models cancellation.
			if options != nil && options.Signal != nil {
				<-options.Signal.Done()
			}
			return ai.FauxAssistantMessage("", ai.FauxAssistantMessageOptions{StopReason: &aborted})
		}), nil
	default:
		return ai.FauxResponseStep{}, fmt.Errorf("trajectory: override kind %q is not one of throw|hang", p.Kind)
	}
}

// ReplayOptions configure the replay agent.
type ReplayOptions struct {
	// Tools is the toolset to expose. Tools with Replay "never" are
	// wrapped to refuse execution during replay; others execute for real —
	// a replay is a regression harness, not a sandbox.
	Tools []*agent.AgentTool
	// SystemPrompt seeds the fresh agent (usually the recorded session's
	// prompt).
	SystemPrompt string
	// ID is the replay trajectory/session id.
	ID string
}

// BuildReplayAgent assembles a fresh agent over the recorded script. The
// returned faux handle lets tests assert full consumption
// (GetPendingResponseCount).
func BuildReplayAgent(script []ai.FauxResponseStep, options ReplayOptions) (*agent.Agent, *ai.FauxProviderHandle, error) {
	faux := ai.FauxProvider(ai.RegisterFauxProviderOptions{
		API:      ReplayAPI,
		Provider: ReplayProvider,
		Models:   []ai.FauxModelDefinition{{ID: "replay", Name: "Replay"}},
	})
	faux.SetResponses(script)
	models := ai.CreateModels()
	models.SetProvider(faux.Provider)
	model := faux.GetModel()

	tools := make([]*agent.AgentTool, 0, len(options.Tools))
	for _, tool := range options.Tools {
		tools = append(tools, refuseNonReplayable(tool))
	}
	thinking := agent.ThinkingOff
	initial := &agent.AgentInitialState{
		SystemPrompt:  &options.SystemPrompt,
		Model:         model,
		ThinkingLevel: &thinking,
		Tools:         tools,
	}
	sessionID := options.ID
	if sessionID == "" {
		sessionID = "replay"
	}
	a := agent.NewAgent(agent.AgentOptions{
		InitialState: initial,
		SessionID:    &sessionID,
		StreamFn: func(m *ai.Model, ctx *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			return models.StreamSimple(m, ai.Context{Messages: ctx.Messages}, opts)
		},
	})
	return a, faux, nil
}

// refuseNonReplayable wraps a tool whose Replay marker is "never": the
// wrapped Execute fails instead of re-executing side effects during replay.
func refuseNonReplayable(tool *agent.AgentTool) *agent.AgentTool {
	if tool == nil || tool.Replay == nil || *tool.Replay != "never" {
		return tool
	}
	name := tool.Name
	return &agent.AgentTool{
		Name:             tool.Name,
		Description:      tool.Description,
		Parameters:       tool.Parameters,
		Label:            tool.Label,
		PrepareArguments: tool.PrepareArguments,
		ExecutionMode:    tool.ExecutionMode,
		Replay:           tool.Replay,
		Execute: func(string, any, *abort.Signal, agent.AgentToolUpdateCallback) (*agent.AgentToolResult, error) {
			return nil, fmt.Errorf("replay refused: tool %q is marked replay:never", name)
		},
	}
}

// CompareRecords asserts two record sequences describe the same run: types,
// turns, steps, and data must match; seq and time are environment noise and
// are ignored. Retries, attempts, and errors all participate — a replay
// that diverges fails here.
func CompareRecords(golden, live []Record) error {
	if len(golden) != len(live) {
		var typesA, typesB []string
		for i := range golden {
			typesA = append(typesA, golden[i].Type)
		}
		for i := range live {
			typesB = append(typesB, live[i].Type)
		}
		return fmt.Errorf("trajectory: record count differs: golden %d (%v), live %d (%v)",
			len(golden), typesA, len(live), typesB)
	}
	for i := range golden {
		a, b := &golden[i], &live[i]
		if a.Type != b.Type {
			return fmt.Errorf("trajectory: record %d: type %q != %q", i+1, a.Type, b.Type)
		}
		if a.Turn != b.Turn || a.Step != b.Step {
			return fmt.Errorf("trajectory: record %d (%s): turn/step %d/%d != %d/%d",
				i+1, a.Type, a.Turn, a.Step, b.Turn, b.Step)
		}
		if !jsonx.Equal(normalizeForCompare(a.Data), normalizeForCompare(b.Data)) {
			return fmt.Errorf("trajectory: record %d (%s): data differs:\n  golden: %s\n  live:   %s",
				i+1, a.Type, jsonx.Stringify(a.Data), jsonx.Stringify(b.Data))
		}
	}
	return nil
}

// normalizeForCompare strips fields that legitimately differ between two
// runs of the same script: usage (faux estimation varies), stream timings,
// wall durations, timestamps, and provider identity stamps. Content —
// text, thinking, tool arguments, results — is compared verbatim.
func normalizeForCompare(v any) any {
	switch t := v.(type) {
	case *jsonx.Obj:
		out := jsonx.NewObj()
		for _, entry := range t.Entries() {
			key := entry[0].(string)
			switch key {
			case "usage", "stream", "durationMs", "ttftMs", "timestamp", "createdAt",
				"provider", "model", "api", "responseModel", "responseId", "sessionId":
				continue
			default:
				out.Set(key, normalizeForCompare(entry[1]))
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, item := range t {
			out = append(out, normalizeForCompare(item))
		}
		return out
	default:
		return v
	}
}
