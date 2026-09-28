package pico3

import "github.com/gladmo/openagent/jsonx"

// EntryKindWitness is a typed witness for one entry kind.
type EntryKindWitness struct {
	KindName string
}

// Is narrows by kind name.
func (k EntryKindWitness) Is(entry *Entry) bool {
	return entry != nil && entry.Kind == k.KindName
}

// KindNameOf exposes the kind string.
func (k EntryKindWitness) Name() string { return k.KindName }

// APIContext is the context surface handed to task API calls.
type APIContext = contextAPIAlias

// ConversationSpec describes a child conversation.
type ConversationSpec struct {
	Parent   *ConversationRef
	Owner    *Id
	Sections []SectionSeedRef
}

// InputPayload is one conversation input.
type InputPayload struct {
	RequestID string
	Content   *jsonx.Obj
}

// KindToken identifies a task kind at the task() call site.
type KindToken struct {
	Name string
}

// CreateTaskOptions mirrors the TS options.
type CreateTaskOptions struct {
	Background bool
	After      []Id
}

// TaskRef is the caller-side handle to a created task.
type TaskRef struct {
	ID int64
}

// RuntimeAPI is the runtime surface the task API rides on.
type RuntimeAPI interface {
	CreateOwnedConversation(spec ConversationSpec, ctx APIContext) (int64, error)
	SendOwned(conversationID int64, input InputPayload, ctx APIContext) (int64, error)
	AbortConversation(conversationID int64, ctx APIContext) error
	CreateTask(kind KindToken, input any, opts CreateTaskOptions, ctx APIContext) (*TaskRef, error)
	GetTask(id Id, ctx APIContext) (*Task, error)
	WaitForTask(id Id, ctx APIContext) (*Task, error)
	WaitForInput(id Id, ctx APIContext) (*Input, error)
	GetInput(id Id, ctx APIContext) (*Input, error)
}
