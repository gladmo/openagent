// Package kinds ports harness/pico3/kinds/*: the pi.* entry kinds, task
// kinds, and the task-facing API surface.
package kinds

import (
	"fmt"

	"github.com/gladmo/openagent/agent/harness/pico3"
)

// Entry kind names.
const (
	KindUser       = "pi.user"
	KindAssistant  = "pi.assistant"
	KindToolResult = "pi.tool_result"
	KindSystem     = "pi.system"
	KindNotice     = "pi.notice"
	KindUsage      = "pi.usage"
	KindSummary    = "pi.summary"
	KindHandoff    = "pi.handoff"
	KindReset      = "pi.reset"
)

// DefineEntry mirrors defineEntry: a typed witness for one entry kind.
// Kind names beginning with "pi." are reserved for the core set.
func DefineEntry(kind string) (pico3.EntryKindWitness, error) {
	if len(kind) >= 3 && kind[:3] == "pi." {
		return pico3.EntryKindWitness{}, fmt.Errorf("entry kind names beginning with %q are reserved: %s", "pi.", kind)
	}
	return pico3.EntryKindWitness{KindName: kind}, nil
}

// CoreEntry builds a witness for one core kind (package-internal use).
func CoreEntry(kind string) pico3.EntryKindWitness {
	return pico3.EntryKindWitness{KindName: kind}
}

// Entries mirrors the TS `entries` registry.
var Entries = struct {
	User       pico3.EntryKindWitness
	Assistant  pico3.EntryKindWitness
	ToolResult pico3.EntryKindWitness
	System     pico3.EntryKindWitness
	Notice     pico3.EntryKindWitness
	Usage      pico3.EntryKindWitness
	Summary    pico3.EntryKindWitness
	Handoff    pico3.EntryKindWitness
	Reset      pico3.EntryKindWitness
}{
	User:       CoreEntry(KindUser),
	Assistant:  CoreEntry(KindAssistant),
	ToolResult: CoreEntry(KindToolResult),
	System:     CoreEntry(KindSystem),
	Notice:     CoreEntry(KindNotice),
	Usage:      CoreEntry(KindUsage),
	Summary:    CoreEntry(KindSummary),
	Handoff:    CoreEntry(KindHandoff),
	Reset:      CoreEntry(KindReset),
}

// TaskApiResult carries the task-API responses used by kinds.
type TaskApiResult struct {
	ID  int64
	Err error
}

// TaskApiSurface is the task-facing API (taskApi port): conversation /
// task creation, waits, and slot reads. The streaming/progress/memo
// members are tool-only and error here exactly like the TS.
type TaskApiSurface struct {
	TaskID         int64
	ConversationID int64
	Runtime        pico3.RuntimeAPI
}

// ConversationHandle mirrors the TS return of conversation().
type ConversationHandle struct {
	ID int64
}

// Stream errors like the TS: only tools get streaming.
func (a *TaskApiSurface) Stream() error {
	return fmt.Errorf("stream is only available to tools")
}

// Progress errors like the TS.
func (a *TaskApiSurface) Progress() error {
	return fmt.Errorf("progress is only available to tools")
}

// Memo errors like the TS.
func (a *TaskApiSurface) Memo() error {
	return fmt.Errorf("memo is only available to tools")
}

// Conversation creates an owned conversation through the runtime.
func (a *TaskApiSurface) Conversation(spec pico3.ConversationSpec, ctx pico3.APIContext) (int64, error) {
	if a.Runtime == nil {
		return 0, fmt.Errorf("runtime unavailable")
	}
	return a.Runtime.CreateOwnedConversation(spec, ctx)
}

// SendInput sends one input to an owned conversation and returns handles.
func (a *TaskApiSurface) SendInput(conversationID int64, input pico3.InputPayload, ctx pico3.APIContext) (*InputHandle, error) {
	if a.Runtime == nil {
		return nil, fmt.Errorf("runtime unavailable")
	}
	inputID, err := a.Runtime.SendOwned(conversationID, input, ctx)
	if err != nil {
		return nil, err
	}
	return &InputHandle{ID: inputID, api: a}, nil
}

// AbortConversation aborts an owned conversation.
func (a *TaskApiSurface) AbortConversation(conversationID int64, ctx pico3.APIContext) error {
	if a.Runtime == nil {
		return fmt.Errorf("runtime unavailable")
	}
	return a.Runtime.AbortConversation(conversationID, ctx)
}

// CreateTask creates a task of the given kind token.
func (a *TaskApiSurface) CreateTask(kind pico3.KindToken, input any, opts pico3.CreateTaskOptions, ctx pico3.APIContext) (*pico3.TaskRef, error) {
	if kind.Name == "" {
		return nil, fmt.Errorf("task(): pass a kind token")
	}
	if a.Runtime == nil {
		return nil, fmt.Errorf("runtime unavailable")
	}
	return a.Runtime.CreateTask(kind, input, opts, ctx)
}

// GetTask reads one task by reference.
func (a *TaskApiSurface) GetTask(ref pico3.TaskRef, ctx pico3.APIContext) (*pico3.Task, error) {
	if a.Runtime == nil {
		return nil, fmt.Errorf("runtime unavailable")
	}
	return a.Runtime.GetTask(ref.ID, ctx)
}

// WaitForTask waits for a task's terminal state.
func (a *TaskApiSurface) WaitForTask(ref pico3.TaskRef, ctx pico3.APIContext) (*pico3.Task, error) {
	if a.Runtime == nil {
		return nil, fmt.Errorf("runtime unavailable")
	}
	return a.Runtime.WaitForTask(ref.ID, ctx)
}

// InputHandle pairs one sent input with wait/result.
type InputHandle struct {
	ID  int64
	api *TaskApiSurface
}

// Wait waits for the input to be answered.
func (h *InputHandle) Wait(ctx pico3.APIContext) (*pico3.Input, error) {
	if h.api.Runtime == nil {
		return nil, fmt.Errorf("runtime unavailable")
	}
	return h.api.Runtime.WaitForInput(h.ID, ctx)
}

// Result reads the input record.
func (h *InputHandle) Result(ctx pico3.APIContext) (*pico3.Input, error) {
	if h.api.Runtime == nil {
		return nil, fmt.Errorf("runtime unavailable")
	}
	return h.api.Runtime.GetInput(h.ID, ctx)
}
