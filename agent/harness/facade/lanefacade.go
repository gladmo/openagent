// Package harnessfacade: lanefacade.go forwards the facade's lane entry
// points to the ported runtime surfaces — steer/followUp/nextRun queueing,
// cancel, abort request, config get/set, and result reads.
package harnessfacade

import (
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/runtime"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// FacadeLane couples a lane with its owning harness for forwarding.
type FacadeLane struct {
	harness *AgentHarness
	lane    *runtime.Lane
}

// ForLane wraps a runtime lane with the facade forwarding surface.
func (h *AgentHarness) ForLane(name string, ctx harness.Context) (*FacadeLane, error) {
	lane, err := h.Lane(name, ctx)
	if err != nil {
		return nil, err
	}
	return &FacadeLane{harness: h, lane: lane}, nil
}

// Name returns the lane name.
func (f *FacadeLane) Name() string { return f.lane.Name }

// GetTipID reads the branch tip.
func (f *FacadeLane) GetTipID(ctx harness.Context) (*string, error) {
	backed, ok := f.harness.Session().(*session.StorageBackedSession)
	if !ok {
		return nil, nil
	}
	return backed.GetBranchTip(f.lane.Name, ctx)
}

// Steer queues a steer message.
func (f *FacadeLane) Steer(text string, ctx harness.Context) (*runtime.QueueResult, error) {
	return runtime.QueueMessage(f.lane, "steer", text, nil, ctx)
}

// FollowUp queues a follow-up message.
func (f *FacadeLane) FollowUp(text string, ctx harness.Context) (*runtime.QueueResult, error) {
	return runtime.QueueMessage(f.lane, "followUp", text, nil, ctx)
}

// NextRun queues a next-run message.
func (f *FacadeLane) NextRun(text string, ctx harness.Context) (*runtime.QueueResult, error) {
	return runtime.QueueMessage(f.lane, "nextRun", text, nil, ctx)
}

// CancelQueued cancels one queued entry.
func (f *FacadeLane) CancelQueued(entryID string, ctx harness.Context) (*runtime.CancelQueuedResult, error) {
	return runtime.CancelQueued(f.lane, entryID, ctx)
}

// RequestAbort requests an operation abort.
func (f *FacadeLane) RequestAbort(operationID string, ctx harness.Context) (*runtime.AbortRequestOutcome, error) {
	return runtime.RequestAbort(f.lane, operationID, ctx)
}

// GetResult reads one operation's terminal record.
func (f *FacadeLane) GetResult(operationID string, ctx harness.Context) (*session.OperationResultRecord, error) {
	stored, err := f.harness.Session().GetValue(session.OperationResult(operationID), ctx)
	if err != nil || stored == nil {
		return nil, err
	}
	obj, ok := stored.Value.(*jsonx.Obj)
	if !ok {
		return nil, nil
	}
	record := &session.OperationResultRecord{
		OperationID: strGet(obj, "operationId"),
		Kind:        strGet(obj, "kind"),
		Status:      strGet(obj, "status"),
		StartedAt:   numGet(obj, "startedAt"),
		EndedAt:     numGet(obj, "endedAt"),
	}
	return record, nil
}

// GetThinkingLevel reads the lane's configured thinking level.
func (f *FacadeLane) GetThinkingLevel(ctx harness.Context) (string, error) {
	stored, err := f.harness.Session().GetValue(session.LaneConfig(f.lane.Name), ctx)
	if err != nil || stored == nil {
		return "off", err
	}
	obj, ok := stored.Value.(*jsonx.Obj)
	if !ok {
		return "off", nil
	}
	if level := strGet(obj, "thinkingLevel"); level != "" {
		return level, nil
	}
	return "off", nil
}

// SetThinkingLevel forwards to the harness setter.
func (f *FacadeLane) SetThinkingLevel(level string, ctx harness.Context) error {
	return f.harness.SetThinkingLevel(f.lane.Name, level, ctx)
}

// GetActiveTools reads the configured active tool names.
func (f *FacadeLane) GetActiveTools(ctx harness.Context) ([]string, error) {
	stored, err := f.harness.Session().GetValue(session.LaneConfig(f.lane.Name), ctx)
	if err != nil || stored == nil {
		return nil, err
	}
	obj, ok := stored.Value.(*jsonx.Obj)
	if !ok {
		return nil, nil
	}
	namesValue, _ := obj.Get("activeToolNames")
	names, _ := namesValue.([]any)
	out := make([]string, 0, len(names))
	for _, name := range names {
		if s, ok := name.(string); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// SetActiveTools forwards to the harness setter.
func (f *FacadeLane) SetActiveTools(names []string, ctx harness.Context) error {
	return f.harness.SetActiveTools(f.lane.Name, names, ctx)
}

// AppendMessage appends a message entry at the branch tip.
func (f *FacadeLane) AppendMessage(message *jsonx.Obj, ctx harness.Context) (string, error) {
	backed, ok := f.harness.Session().(*session.StorageBackedSession)
	if !ok {
		return "", nil
	}
	payload := session.AgentMessagePayload{
		Role:    strGet(message, "role"),
		Message: message,
	}
	return backed.AppendToBranch(f.lane.Name, &session.Entry{
		EntryBase: session.EntryBase{Type: session.EntryTypeMessage},
		Message:   payload,
	}, ctx)
}

func strGet(obj *jsonx.Obj, key string) string {
	if v, ok := obj.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func numGet(obj *jsonx.Obj, key string) float64 {
	if v, ok := obj.Get(key); ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}
