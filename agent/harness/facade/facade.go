// Package harnessfacade ports harness/agent-harness.ts's interface
// surface: the AgentHarness facade aggregating lanes over one open
// session. This file lands the facade skeleton — lane acquisition,
// listing, name/label passthroughs, and config get/set backed by the
// session — with the drive admission surfaces wired to the ported
// runtime pieces.
package harnessfacade

import (
	"sync"

	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/runtime"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// AgentHarnessOptions mirrors the TS interface.
type AgentHarnessOptions struct {
	Session         session.Session
	Models          ai.Models
	Model           *ai.Model
	ThinkingLevel   string
	ActiveToolNames []string
	SystemPrompt    string
	SteeringMode    string
	FollowUpMode    string
	ToolExecution   string
}

// LaneInfo mirrors the TS interface.
type LaneInfo struct {
	Name      string
	TipID     *string
	Operation *runtime.RuntimeLaneState
}

// AgentHarness is the facade.
type AgentHarness struct {
	mu      sync.RWMutex
	session session.Session
	options AgentHarnessOptions
	lanes   map[string]*runtime.Lane
	closed  bool
}

// CreateAgentHarness builds a harness over an open session.
func CreateAgentHarness(options AgentHarnessOptions) (*AgentHarness, map[string]*runtime.RuntimeLaneState, error) {
	restored, err := runtime.RestoreSession(options.Session, harness.BackgroundContext)
	if err != nil {
		return nil, nil, err
	}
	h := &AgentHarness{
		session: options.Session,
		options: options,
		lanes:   map[string]*runtime.Lane{},
	}
	for name, state := range restored {
		h.lanes[name] = runtime.NewLane(runtime.LaneOptions{
			Name:    name,
			Session: options.Session,
			State:   state,
		})
	}
	return h, restored, nil
}

// Lane acquires (or creates) one lane by name.
func (h *AgentHarness) Lane(name string, ctx harness.Context) (*runtime.Lane, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errClosed()
	}
	if lane, ok := h.lanes[name]; ok {
		return lane, nil
	}
	// Create the branch + lane config/state in one mutation.
	if _, err := h.session.CreateBranch(name, nil, ctx); err != nil {
		return nil, err
	}
	backed, ok := h.session.(*session.StorageBackedSession)
	if !ok {
		return nil, errClosed()
	}
	tip, err := backed.GetBranchTip(name, ctx)
	if err != nil {
		return nil, err
	}
	configuration := session.LaneConfiguration{
		Model:           session.LaneModel{Provider: h.options.Model.Provider, ModelID: h.options.Model.ID},
		ThinkingLevel:   h.options.ThinkingLevel,
		ActiveToolNames: h.options.ActiveToolNames,
	}
	if configuration.ThinkingLevel == "" {
		configuration.ThinkingLevel = "off"
	}
	if err := h.session.SetValue(session.LaneConfig(name), laneConfigToJSON(configuration), ctx); err != nil {
		return nil, err
	}
	if err := h.session.SetValue(session.LaneStateValue(name), runtime.DurableLaneStateJSON(nil, nil, nil), ctx); err != nil {
		return nil, err
	}
	state := &runtime.RuntimeLaneState{TipID: tip, Configuration: configuration}
	lane := runtime.NewLane(runtime.LaneOptions{
		Name:    name,
		Session: h.session,
		State:   state,
	})
	h.lanes[name] = lane
	return lane, nil
}

// Lanes lists the open lanes.
func (h *AgentHarness) Lanes() []LaneInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]LaneInfo, 0, len(h.lanes))
	for name, lane := range h.lanes {
		state := lane.State()
		info := LaneInfo{Name: name, TipID: state.TipID, Operation: state}
		out = append(out, info)
	}
	return out
}

// GetName reads the session name.
func (h *AgentHarness) GetName(ctx harness.Context) (*string, error) {
	return h.session.GetName(ctx)
}

// SetName writes the session name.
func (h *AgentHarness) SetName(name *string, ctx harness.Context) error {
	return h.session.SetName(name, ctx)
}

// GetLabel reads an entry label.
func (h *AgentHarness) GetLabel(targetID string, ctx harness.Context) (*string, error) {
	return h.session.GetLabel(targetID, ctx)
}

// SetLabel writes an entry label.
func (h *AgentHarness) SetLabel(targetID string, label *string, ctx harness.Context) error {
	return h.session.SetLabel(targetID, label, ctx)
}

// SetThinkingLevel updates a lane's durable config.
func (h *AgentHarness) SetThinkingLevel(laneName, level string, ctx harness.Context) error {
	stored, err := h.session.GetValue(session.LaneConfig(laneName), ctx)
	if err != nil || stored == nil {
		return err
	}
	return h.session.SetValue(session.LaneConfig(laneName), jsonWithField(stored.Value, "thinkingLevel", level), ctx)
}

// SetActiveTools updates a lane's active tool names.
func (h *AgentHarness) SetActiveTools(laneName string, names []string, ctx harness.Context) error {
	stored, err := h.session.GetValue(session.LaneConfig(laneName), ctx)
	if err != nil || stored == nil {
		return err
	}
	arr := make([]any, 0, len(names))
	for _, name := range names {
		arr = append(arr, name)
	}
	return h.session.SetValue(session.LaneConfig(laneName), jsonWithField(stored.Value, "activeToolNames", arr), ctx)
}

// Close closes the session.
func (h *AgentHarness) Close(ctx harness.Context) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.mu.Unlock()
	return h.session.Close(ctx)
}

// Closed reports the close state.
func (h *AgentHarness) Closed() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.closed
}

func laneConfigToJSON(config session.LaneConfiguration) session.JsonValue {
	obj := jsonx.NewObj()
	obj.Set("model", jsonx.ObjFrom("provider", config.Model.Provider, "modelId", config.Model.ModelID))
	obj.Set("thinkingLevel", config.ThinkingLevel)
	tools := make([]any, 0, len(config.ActiveToolNames))
	for _, name := range config.ActiveToolNames {
		tools = append(tools, name)
	}
	obj.Set("activeToolNames", tools)
	return obj
}

func jsonWithField(base any, key string, value any) session.JsonValue {
	obj := jsonx.NewObj()
	if baseObj, ok := base.(*jsonx.Obj); ok {
		for _, k := range baseObj.Keys() {
			v, _ := baseObj.Get(k)
			obj.Set(k, v)
		}
	}
	obj.Set(key, value)
	return obj
}

type harnessClosedError struct{}

func (e *harnessClosedError) Error() string { return "harness is closed" }

func errClosed() error { return &harnessClosedError{} }

// Session exposes the backing session.
func (h *AgentHarness) Session() session.Session { return h.session }
