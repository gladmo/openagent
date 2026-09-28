package session

// types.go ports harness/session/types.ts: the entry tree, the flat
// 13-leaf durable operation state machine, and the storage/session
// interfaces. JSON-shaped payloads use the jsonx value model.

import (
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// EntryType values.
const (
	EntryTypeMessage       = "message"
	EntryTypeCompaction    = "compaction"
	EntryTypeBranchSummary = "branch_summary"
	EntryTypeCustom        = "custom"
)

// EntryBase mirrors the TS base.
type EntryBase struct {
	ID         string  `json:"id"`
	ParentID   *string `json:"parentId"`
	Seq        int64   `json:"seq"`
	Timestamp  float64 `json:"timestamp"`
	Type       string  `json:"type"`
	CustomType *string `json:"customType,omitempty"`
}

// Entry is the union; Message carries any AgentMessage payload (ai messages
// or harness custom messages via their JSON form).
type Entry struct {
	EntryBase
	// message entries
	Message   AgentMessagePayload `json:"message,omitempty"`
	Terminate bool                `json:"terminate,omitempty"`
	// compaction entries
	Summary      string                `json:"summary,omitempty"`
	RetainedTail []AgentMessagePayload `json:"retainedTail,omitempty"`
	TokensBefore float64               `json:"tokensBefore,omitempty"`
	Details      JsonValue             `json:"details,omitempty"`
	Usage        *ai.Usage             `json:"usage,omitempty"`
	FromHook     bool                  `json:"fromHook,omitempty"`
	// branch_summary entries
	FromID *string `json:"fromId,omitempty"`
	// custom entries
	Data JsonValue `json:"data,omitempty"`
}

// AgentMessagePayload carries a serialized AgentMessage (ai message or
// custom role) plus its role discriminator.
type AgentMessagePayload struct {
	Role    string     `json:"role"`
	Message *jsonx.Obj `json:"message"`
}

// EntryProjector converts a custom entry into model context.
type EntryProjector func(entry *Entry, ctx Context) []AgentMessagePayload

// LaneConfiguration mirrors the TS interface.
type LaneConfiguration struct {
	Model           LaneModel `json:"model"`
	ThinkingLevel   string    `json:"thinkingLevel"`
	ActiveToolNames []string  `json:"activeToolNames"`
}

// LaneModel is the {provider, modelId} pair.
type LaneModel struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// OperationMeta mirrors the TS interface; Intent is the JSON form.
type OperationMeta struct {
	OperationID string     `json:"operationId"`
	Lane        string     `json:"lane"`
	SourceTipID *string    `json:"sourceTipId"`
	StartedAt   float64    `json:"startedAt"`
	Intent      *jsonx.Obj `json:"intent"`
}

// Control mirrors the TS union.
type Control struct {
	Status      string   `json:"status"` // "running" | "cancel_requested"
	RequestedAt *float64 `json:"requestedAt,omitempty"`
}

// OperationError mirrors the TS interface.
type OperationError struct {
	Code    string    `json:"code"`
	Message string    `json:"message"`
	Details JsonValue `json:"details,omitempty"`
}

// TerminalStatus values.
const (
	StatusCompleted = "completed"
	StatusDeclined  = "declined"
	StatusAborted   = "aborted"
	StatusFailed    = "failed"
)

// OperationResultRecord mirrors the TS interface.
type OperationResultRecord struct {
	OperationID string          `json:"operationId"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	Error       *OperationError `json:"error,omitempty"`
	FromTipID   *string         `json:"fromTipId"`
	TipID       *string         `json:"tipId"`
	StartedAt   float64         `json:"startedAt"`
	EndedAt     float64         `json:"endedAt"`
}

// OperationState is the flat union; At is the discriminator.
type OperationState struct {
	At string `json:"at"`

	// Uniform scope.
	Control                Control    `json:"control"`
	Settings               *jsonx.Obj `json:"settings,omitempty"`
	LatestAssistantEntryID *string    `json:"latestAssistantEntryId"`

	// Checkpoint leaf.
	Continuation   *jsonx.Obj `json:"continuation,omitempty"`
	TriggerEntryID string     `json:"triggerEntryId,omitempty"`

	// Assistant generation scope.
	GenerationContext *jsonx.Obj `json:"generationContext,omitempty"`

	// assistant.effect_pending / retry_wait extras.
	Attempt             int64   `json:"attempt,omitempty"`
	NextAttempt         int64   `json:"nextAttempt,omitempty"`
	NotBefore           float64 `json:"notBefore,omitempty"`
	ErrorMessage        string  `json:"errorMessage,omitempty"`
	ResponseEntryID     string  `json:"responseEntryId,omitempty"`
	UsageID             string  `json:"usageId,omitempty"`
	IntendedOutputLimit float64 `json:"intendedOutputLimit,omitempty"`
	ContextWindow       float64 `json:"contextWindow,omitempty"`

	// tools leaf.
	Batch *jsonx.Obj `json:"batch,omitempty"`

	// deferred scope.
	StepID        string     `json:"stepId,omitempty"`
	SourceEntryID string     `json:"sourceEntryId,omitempty"`
	Poll          int64      `json:"poll,omitempty"`
	Configuration *jsonx.Obj `json:"configuration,omitempty"`
	StreamOptions *jsonx.Obj `json:"streamOptions,omitempty"`

	// summary leaves.
	Task           *jsonx.Obj `json:"task,omitempty"`
	SummaryContext *jsonx.Obj `json:"summaryContext,omitempty"`

	// navigation leaf.
	TargetID *string `json:"targetId,omitempty"`
	Label    *string `json:"label,omitempty"`
}

// The 13 dispatcher leaves.
const (
	AtStarting                = "starting"
	AtCheckpoint              = "checkpoint"
	AtAssistantReady          = "assistant.ready"
	AtAssistantEffectPending  = "assistant.effect_pending"
	AtAssistantRetryWait      = "assistant.retry_wait"
	AtTools                   = "tools"
	AtDeferredSuspended       = "deferred.suspended"
	AtDeferredEffectPending   = "deferred.effect_pending"
	AtSummaryDeciding         = "summary.deciding"
	AtSummaryReady            = "summary.ready"
	AtSummaryEffectPending    = "summary.effect_pending"
	AtSummaryRetryWait        = "summary.retry_wait"
	AtNavigationReadyToCommit = "navigation.ready_to_commit"
)

// OperationScopeOf copies only the uniform scope.
func OperationScopeOf(state *OperationState) OperationState {
	return OperationState{
		At:                     state.At,
		Control:                state.Control,
		Settings:               state.Settings,
		LatestAssistantEntryID: state.LatestAssistantEntryID,
	}
}

// Operation mirrors the TS type.
type Operation struct {
	Meta  OperationMeta  `json:"meta"`
	State OperationState `json:"state"`
}

// LaneState mirrors the TS interface.
type LaneState struct {
	CurrentOperationID *string     `json:"currentOperationId"`
	LastOperationID    *string     `json:"lastOperationId"`
	Inbox              []InboxItem `json:"inbox"`
}

// InboxItem mirrors the TS interface.
type InboxItem struct {
	EntryID string `json:"entryId"`
	Kind    string `json:"kind"` // steer | followUp | nextRun | write
}

// PendingEntry mirrors the TS union.
type PendingEntry struct {
	Type       string    `json:"type"` // message | custom
	CustomType string    `json:"customType,omitempty"`
	Payload    JsonValue `json:"payload,omitempty"`
}

// DurableFileOperations mirrors the TS interface.
type DurableFileOperations struct {
	Read    []string `json:"read"`
	Written []string `json:"written"`
	Edited  []string `json:"edited"`
}

// UsageRow mirrors the TS interface.
type UsageRow struct {
	ID         string    `json:"id"`
	Seq        int64     `json:"seq"`
	Usage      ai.Usage  `json:"usage"`
	EntryID    *string   `json:"entryId,omitempty"`
	Adjustment bool      `json:"adjustment"`
	Details    JsonValue `json:"details,omitempty"`
}

// EntryWrite / UsageWrite mirror the TS unions.
type EntryWrite struct {
	Kind  string `json:"kind"` // "entry"
	Entry *Entry `json:"entry"`
}

type UsageWrite struct {
	Kind string   `json:"kind"` // "usage"
	Row  UsageRow `json:"row"`
}

// Write is the transactional write union (kind discriminates; value/list
// writes carry their own op discriminators).
type Write struct {
	Kind      string    `json:"kind"` // "entry" | "usage" | "value" | "list"
	Entry     *Entry    `json:"entry,omitempty"`
	Row       *UsageRow `json:"row,omitempty"`
	Op        string    `json:"op,omitempty"` // value: set|delete; list: append|delete
	Namespace string    `json:"namespace,omitempty"`
	Key       string    `json:"key,omitempty"`
	Value     JsonValue `json:"value,omitempty"`
}

// CommitResult mirrors the TS interface.
type CommitResult struct {
	FirstSeq  int64        `json:"firstSeq"`
	Seqs      []int64      `json:"seqs"`
	Timestamp float64      `json:"timestamp"`
	Stats     SessionStats `json:"stats"`
}

// EntryStructure mirrors the TS interface.
type EntryStructure = EntryBase

// EntryCursor mirrors the TS interface.
type EntryCursor struct{ Seq int64 }

// BranchScan mirrors the TS interface.
type BranchScan struct {
	Start      *string      `json:"start,omitempty"`
	StopAtType string       `json:"stopAtType,omitempty"`
	StopAtID   string       `json:"stopAtId,omitempty"`
	Type       string       `json:"type,omitempty"`
	CustomType string       `json:"customType,omitempty"`
	Order      string       `json:"order,omitempty"` // newestFirst | oldestFirst
	Limit      *int64       `json:"limit,omitempty"`
	Cursor     *EntryCursor `json:"cursor,omitempty"`
}

// StorageBranchScan is a BranchScan with a required start.
type StorageBranchScan struct {
	BranchScan
	StartID string
}

// EntryScan mirrors the TS interface.
type EntryScan struct {
	Type       string `json:"type,omitempty"`
	CustomType string `json:"customType,omitempty"`
	FromSeq    *int64 `json:"fromSeq,omitempty"`
	ToSeq      *int64 `json:"toSeq,omitempty"`
	Order      string `json:"order,omitempty"` // asc | desc
	Limit      *int64 `json:"limit,omitempty"`
}

// UsageScan mirrors the TS interface.
type UsageScan struct {
	FromSeq *int64 `json:"fromSeq,omitempty"`
	ToSeq   *int64 `json:"toSeq,omitempty"`
	Order   string `json:"order,omitempty"`
	Limit   *int64 `json:"limit,omitempty"`
}

// SessionStats mirrors the TS interface.
type SessionStats struct {
	MessageCount int64    `json:"messageCount"`
	Usage        ai.Usage `json:"usage"`
}

// Storage mirrors the TS interface.
type Storage interface {
	Commit(writes []Write, ctx Context) (CommitResult, error)
	GetEntries(ids []string, ctx Context) (map[string]*Entry, error)
	GetValue(address Value, ctx Context) (*StoredValue, error)
	ScanValues(prefix Value, ctx Context) ([]StoredValue, error)
	ReadList(address ValueList, options *ListReadOptions, ctx Context) ([]ListElement, error)
	ScanBranch(query StorageBranchScan, ctx Context) ([]*Entry, error)
	ScanBranchStructure(query StorageBranchScan, ctx Context) ([]EntryStructure, error)
	ScanEntries(query EntryScan, ctx Context) ([]*Entry, error)
	ScanUsage(query UsageScan, ctx Context) ([]UsageRow, error)
	GetStats(ctx Context) (SessionStats, error)
	Close(ctx Context) error
}

// SessionMetadata mirrors the TS interface.
type SessionMetadata struct {
	ID                      string  `json:"id"`
	CreatedAt               float64 `json:"createdAt"`
	StorageVersion          int64   `json:"storageVersion"`
	Cwd                     *string `json:"cwd,omitempty"`
	ParentSessionID         *string `json:"parentSessionId,omitempty"`
	LegacyParentSessionPath *string `json:"legacyParentSessionPath,omitempty"`
}

// IdGenerator mirrors the TS interface.
type IdGenerator interface {
	Next(timestampMs ...float64) string
}

// EntryQuery mirrors the TS interface.
type EntryQuery struct {
	Type       string       `json:"type,omitempty"`
	CustomType string       `json:"customType,omitempty"`
	Order      string       `json:"order,omitempty"`
	Limit      *int64       `json:"limit,omitempty"`
	Cursor     *EntryCursor `json:"cursor,omitempty"`
}

// SessionReader mirrors the TS interface.
type SessionReader interface {
	GetEntries(ids []string, ctx Context) (map[string]*Entry, error)
	GetStats(ctx Context) (SessionStats, error)
	GetValue(address Value, ctx Context) (*StoredValue, error)
	ScanValues(prefix Value, ctx Context) ([]StoredValue, error)
	ReadList(address ValueList, options *ListReadOptions, ctx Context) ([]ListElement, error)
	ScanBranch(query StorageBranchScan, ctx Context) ([]*Entry, error)
}

// Session mirrors the TS interface.
type Session interface {
	SessionReader
	Metadata() SessionMetadata
	IDGenerator() IdGenerator
	GetEntry(id string, ctx Context) (*Entry, error)
	GetName(ctx Context) (*string, error)
	GetLabel(targetID string, ctx Context) (*string, error)
	FindEntries(query *EntryQuery, ctx Context) ([]*Entry, error)
	FindEntry(query *EntryQuery, ctx Context) (*Entry, error)
	Branch(name string, ctx Context) (Branch, error)
	CreateBranch(name string, at *string, ctx Context) (Branch, error)
	BeginMutation(ctx Context) (SessionMutation, error)
	Mutate(mutation func(mutator SessionMutator, ctx Context) error, ctx Context) error
	SetValue(address Value, next JsonValue, ctx Context) error
	DeleteValue(address Value, ctx Context) error
	AppendList(address ValueList, element JsonValue, ctx Context) error
	DeleteList(address ValueList, ctx Context) error
	SetName(name *string, ctx Context) error
	SetLabel(targetID string, label *string, ctx Context) error
	Close(ctx Context) error
}

// SessionMutation mirrors the TS interface.
type SessionMutation interface {
	SessionReader
	Commit(writes []Write, ctx Context) (CommitResult, error)
	End(ctx Context) error
}

// SessionMutator is the callback-scoped capability.
type SessionMutator interface {
	SessionReader
	Commit(writes []Write, ctx Context) (CommitResult, error)
}

// Branch mirrors the TS interface.
type Branch interface {
	Name() string
	GetTipID(ctx Context) (*string, error)
	FindEntries(query *BranchScan, ctx Context) ([]*Entry, error)
	FindEntry(query *BranchScan, ctx Context) (*Entry, error)
	AppendMessage(message AgentMessagePayload, ctx Context) (string, error)
	AppendCustomEntry(customType string, data JsonValue, ctx Context) (string, error)
}

// ForkOptions mirrors the TS union (Scope discriminates).
type ForkOptions struct {
	Scope    string  `json:"scope"` // "branch" | "tree"
	Branch   string  `json:"branch,omitempty"`
	EntryID  *string `json:"entryId,omitempty"`
	Position string  `json:"position,omitempty"` // "at" | "before"
	ID       *string `json:"id,omitempty"`
}

// SessionRepo mirrors the TS interface.
type SessionRepo interface {
	Create(options map[string]any, ctx Context) (Session, error)
	Open(metadata SessionMetadata, ctx Context) (Session, error)
	List(options any, ctx Context) ([]SessionMetadata, error)
	Delete(metadata SessionMetadata, ctx Context) error
	Fork(source SessionMetadata, options ForkOptions, ctx Context) (Session, error)
}
