// Package pico3 ports harness/pico3: harness-v3 records, documents,
// storage, kinds, transactions, runtime, tools, hooks.
package pico3

import (
	"fmt"

	chordjson "github.com/gladmo/openagent/chord"
	chorddelta "github.com/gladmo/openagent/chord/delta"
	jsonx "github.com/gladmo/openagent/jsonx"
)

// JsonValue is the strict durable JSON model (chord).
type JsonValue = chordjson.JsonValue

// JsonObject is a strict durable JSON object (insertion-ordered).
type JsonObject = *jsonx.Obj

// Id and Seq mirror the TS numeric identifiers (float64 per porting rules).
type Id = int64
type Seq = int64

// Op is the chord delta op.
type Op = chorddelta.Op

// Conversation mirrors the TS record.
type Conversation struct {
	ID       int64            `json:"id"`
	Parent   *ConversationRef `json:"parent,omitempty"`
	Owner    *Id              `json:"owner,omitempty"`
	Sections []SectionSeedRef `json:"sections,omitempty"`
}

// ConversationRef locates a fork point.
type ConversationRef struct {
	ConversationId Id `json:"conversationId"`
	At             Id `json:"at"`
}

// SectionSeedRef is the §8.1 section seed marker (opaque here).
type SectionSeedRef struct {
	Kind string    `json:"kind"`
	Data JsonValue `json:"data,omitempty"`
}

// ContextEdit mirrors the TS record.
type ContextEdit struct {
	Target   Id          `json:"target"`
	Action   string      `json:"action"` // omit | replace
	Messages []StoredMsg `json:"messages,omitempty"`
}

// StoredMsg is a stored (JSON-shaped) message payload.
type StoredMsg = JsonValue

// Entry mirrors the TS record.
type Entry struct {
	ID             int64         `json:"id"`
	ConversationID Id            `json:"conversationId"`
	Kind           string        `json:"kind"`
	Model          []StoredMsg   `json:"model,omitempty"`
	Data           JsonObject    `json:"data,omitempty"`
	Head           *Id           `json:"head,omitempty"`
	Edits          []ContextEdit `json:"edits,omitempty"`
	ByTaskID       *Id           `json:"byTaskId,omitempty"`
}

// Checkpoint mirrors the TS type.
type Checkpoint = JsonObject

// Outcome mirrors the TS union (JSON-shaped).
type Outcome = JsonObject

// Task mirrors the TS record.
type Task struct {
	ID             int64      `json:"id"`
	ConversationID Id         `json:"conversationId"`
	Kind           string     `json:"kind"`
	Input          JsonValue  `json:"input"`
	Status         string     `json:"status"` // pending | running | terminal
	Checkpoint     Checkpoint `json:"checkpoint,omitempty"`
	Abort          bool       `json:"abort,omitempty"`
	Outcome        Outcome    `json:"outcome,omitempty"`
	After          []Id       `json:"after"`
	Owns           []Id       `json:"owns"`
	Background     bool       `json:"background,omitempty"`
}

// TaskPatch mirrors the TS type.
type TaskPatch struct {
	ID                Id
	Status            *string
	Checkpoint        Checkpoint // nil = untouched; HasCheckpointNull = clear
	HasCheckpointNull bool
	Abort             *bool
	Outcome           Outcome
	HasOutcome        bool
	Owns              []Id
	HasOwns           bool
}

// Input mirrors the TS record.
type Input struct {
	ID             int64   `json:"id"`
	ConversationID Id      `json:"conversationId"`
	RequestID      *string `json:"requestId,omitempty"`
	Status         string  `json:"status"` // queued | placed | done | unanswered
	Entry          *Id     `json:"entry,omitempty"`
	Answer         *Id     `json:"answer,omitempty"`
	Reason         *string `json:"reason,omitempty"`
	Detail         *string `json:"detail,omitempty"`
}

// DocRef mirrors the TS union.
type DocRef struct {
	Doc string // "session" | "rewindable" | "sticky"
	// ConversationID is 0 for the session doc.
	ConversationID Id
}

// Write mirrors the TS union (Type discriminates).
type Write struct {
	Type         string // conversation | entry | task | task.patch | input | doc
	Conversation *Conversation
	Entry        *Entry
	Task         *Task
	Patch        *TaskPatch
	Input        *Input
	Ref          *DocRef
	Ops          []Op
}

// W constructors mirror the TS literal factories.
func NewConversationWrite(c *Conversation) Write { return Write{Type: "conversation", Conversation: c} }
func NewEntryWrite(e *Entry) Write               { return Write{Type: "entry", Entry: e} }
func NewTaskWrite(t *Task) Write                 { return Write{Type: "task", Task: t} }
func NewTaskPatchWrite(p *TaskPatch) Write       { return Write{Type: "task.patch", Patch: p} }
func NewInputWrite(i *Input) Write               { return Write{Type: "input", Input: i} }
func NewDocWrite(ref *DocRef, ops []Op) Write    { return Write{Type: "doc", Ref: ref, Ops: ops} }

// EntryScan mirrors the TS interface.
type EntryScan struct {
	ConversationID Id
	Before         *Id
	Kind           string
	WithHead       bool
	Limit          int
}

// TaskScan mirrors the TS interface.
type TaskScan struct {
	ConversationID *Id
	Status         []string
	Kind           string
}

// Storage mirrors the TS interface (read side + commit).
type Storage interface {
	MintID() Id
	Commit(writes []Write) (Seq, error)
	Conversation(id Id) (*Conversation, error)
	Conversations() ([]*Conversation, error)
	Entries(ids []Id) (map[Id]*Entry, error)
	ScanEntries(scan EntryScan) ([]*Entry, error)
	Task(id Id) (*Task, error)
	ScanTasks(scan TaskScan) ([]*Task, error)
	Input(id Id) (*Input, error)
	InputByRequest(conversationID Id, requestID string) (*Input, error)
	Doc(ref DocRef) (JsonObject, error)
	DocAsOf(conversationID, at Id) (JsonObject, error)
	Truncate(ref DocRef) error
	Close() error
}

// Clone deep-copies any JSON value.
func Clone(value JsonValue) JsonValue {
	copied, err := chordjson.CopyJson(value, nil)
	if err != nil {
		return nil
	}
	return copied
}

// NewObject builds a durable object.
func NewObject() JsonObject { return jsonx.NewObj() }

// errPico is the storage error constructor.
func errPico(format string, args ...any) error { return fmt.Errorf(format, args...) }
