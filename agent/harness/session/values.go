// Package session ports harness/session/*: the durable session substrate
// (values, mutation line, entries, storage, repositories) plus the JSONL
// persistence backends under the jsonl subpackage.
package session

import (
	"fmt"
	"sync"

	chordjson "github.com/gladmo/openagent/chord"
	"github.com/gladmo/openagent/jsonx"
)

// JsonValue aliases the chord JsonValue model.
type JsonValue = chordjson.JsonValue

// Context aliases the harness context (avoiding an import cycle; the
// harness package re-exports chord context).
type Context = contextAlias

// ---------------------------------------------------------------------------
// values.ts
// ---------------------------------------------------------------------------

// StoredAddressBase mirrors the TS base.
type StoredAddressBase struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Kind      string `json:"kind"` // "value" | "list"
}

// Value is a scalar address.
type Value struct{ StoredAddressBase }

// ValueList is a list address.
type ValueList struct{ StoredAddressBase }

// StoredValue mirrors the TS interface.
type StoredValue struct {
	Address Value
	Value   JsonValue
	Seq     int64
}

// ListElement mirrors the TS interface.
type ListElement struct {
	Seq   int64
	Value JsonValue
}

// ListCursor mirrors the TS interface.
type ListCursor struct{ Seq int64 }

// ListReadOptions mirrors the TS interface.
type ListReadOptions struct {
	Cursor *ListCursor
	Order  string // "asc" | "desc"
	Limit  *int64
}

// ResolvedListReadOptions mirrors the TS interface.
type ResolvedListReadOptions struct {
	Cursor *ListCursor
	Order  string
	Limit  int64
}

// ValueWrite / ListWrite unions (op string discriminates).
type ValueWrite struct {
	Kind      string // "value"
	Op        string // "set" | "delete"
	Namespace string
	Key       string
	Value     JsonValue
}

type ListWrite struct {
	Kind      string // "list"
	Op        string // "append" | "delete"
	Namespace string
	Key       string
	Value     JsonValue
}

func validateAddress(namespace, key string) {
	if len(namespace) == 0 {
		panic("Value namespace must not be empty")
	}
	if containsNUL(namespace) {
		panic("Value namespace must not contain \\u0000")
	}
	if containsNUL(key) {
		panic("Value key must not contain \\u0000")
	}
}

func containsNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

// NewValue builds a scalar address (mirrors value()).
func NewValue(namespace string, key ...string) Value {
	k := ""
	if len(key) > 0 {
		k = key[0]
	}
	validateAddress(namespace, k)
	return Value{StoredAddressBase{Namespace: namespace, Key: k, Kind: "value"}}
}

// NewList builds a list address (mirrors list()).
func NewList(namespace string, key ...string) ValueList {
	k := ""
	if len(key) > 0 {
		k = key[0]
	}
	validateAddress(namespace, k)
	return ValueList{StoredAddressBase{Namespace: namespace, Key: k, Kind: "list"}}
}

// SetValue mirrors setValue().
func SetValue(address Value, next JsonValue) ValueWrite {
	return ValueWrite{Kind: "value", Op: "set", Namespace: address.Namespace, Key: address.Key, Value: next}
}

// DeleteValue mirrors deleteValue().
func DeleteValue(address Value) ValueWrite {
	return ValueWrite{Kind: "value", Op: "delete", Namespace: address.Namespace, Key: address.Key}
}

// AppendList mirrors appendList().
func AppendList(address ValueList, element JsonValue) ListWrite {
	return ListWrite{Kind: "list", Op: "append", Namespace: address.Namespace, Key: address.Key, Value: element}
}

// DeleteList mirrors deleteList().
func DeleteList(address ValueList) ListWrite {
	return ListWrite{Kind: "list", Op: "delete", Namespace: address.Namespace, Key: address.Key}
}

// ResolveListReadOptions mirrors resolveListReadOptions (default limit
// 1000, hard cap 10000).
func ResolveListReadOptions(options ListReadOptions) ResolvedListReadOptions {
	requested := int64(1000)
	if options.Limit != nil {
		requested = *options.Limit
	}
	if requested <= 0 {
		panic("List read limit must be a positive safe integer")
	}
	limit := requested
	if limit > 10000 {
		limit = 10000
	}
	order := options.Order
	if order == "" {
		order = "asc"
	}
	return ResolvedListReadOptions{Cursor: options.Cursor, Order: order, Limit: limit}
}

// Reserved namespace accessors (pi.*).

// BranchTip mirrors branchTip().
func BranchTip(branch string) Value { return NewValue("pi.branch.tip", branch) }

// LaneConfig mirrors laneConfig().
func LaneConfig(lane string) Value { return NewValue("pi.lane.config", lane) }

// LaneStateValue mirrors laneState() (the LaneState type lives in types.go).
func LaneStateValue(lane string) Value { return NewValue("pi.lane.state", lane) }

// OperationResult mirrors operationResult().
func OperationResult(operationID string) Value { return NewValue("pi.result", operationID) }

// OperationMetaValue mirrors operationMeta() (the OperationMeta type lives
// in types.go).
func OperationMetaValue(operationID string) Value { return NewValue("pi.op.meta", operationID) }

// OperationStateValue mirrors operationState() (the OperationState type
// lives in types.go).
func OperationStateValue(operationID string) Value { return NewValue("pi.op.state", operationID) }

// OperationToolArgs mirrors operationToolArgs().
func OperationToolArgs(operationID, stepID string, sourceIndex int64) Value {
	return NewValue("pi.op.tool_args", fmt.Sprintf("%s:%s:%d", operationID, stepID, sourceIndex))
}

// OperationToolMemo mirrors operationToolMemo().
func OperationToolMemo(operationID, invocationID, name string) Value {
	return NewValue("pi.op.tool_memo", fmt.Sprintf("%s:%s:%s", operationID, invocationID, name))
}

// OperationPreparation mirrors operationPreparation().
func OperationPreparation(operationID, taskID string) Value {
	return NewValue("pi.op.preparation", fmt.Sprintf("%s:%s", operationID, taskID))
}

// OperationToolArgsPrefix mirrors operationToolArgsPrefix().
func OperationToolArgsPrefix(operationID string, stepID ...string) Value {
	if len(stepID) == 0 {
		return NewValue("pi.op.tool_args", operationID+":")
	}
	return NewValue("pi.op.tool_args", fmt.Sprintf("%s:%s:", operationID, stepID[0]))
}

// OperationToolMemoPrefix mirrors operationToolMemoPrefix().
func OperationToolMemoPrefix(operationID string, invocationID ...string) Value {
	if len(invocationID) == 0 {
		return NewValue("pi.op.tool_memo", operationID+":")
	}
	return NewValue("pi.op.tool_memo", fmt.Sprintf("%s:%s:", operationID, invocationID[0]))
}

// OperationPreparationPrefix mirrors operationPreparationPrefix().
func OperationPreparationPrefix(operationID string) Value {
	return NewValue("pi.op.preparation", operationID+":")
}

// PendingEntryValue mirrors pendingEntry() (the PendingEntry type lives in
// types.go).
func PendingEntryValue(entryID string) Value { return NewValue("pi.pending.entry", entryID) }

// PendingToolOutput mirrors pendingToolOutput().
func PendingToolOutput(operationID, invocationID string) Value {
	return NewValue("pi.pending.tool_output", fmt.Sprintf("%s:%s", operationID, invocationID))
}

// PendingAssistantFrames mirrors pendingAssistantFrames().
func PendingAssistantFrames(operationID, responseEntryID string) ValueList {
	return NewList("pi.pending.assistant_frame", fmt.Sprintf("%s:%s", operationID, responseEntryID))
}

// PendingToolOutputPrefix mirrors pendingToolOutputPrefix().
func PendingToolOutputPrefix(operationID string) Value {
	return NewValue("pi.pending.tool_output", operationID+":")
}

// SessionName mirrors sessionName.
var SessionName = NewValue("pi.session.name")

// EntryLabel mirrors entryLabel().
func EntryLabel(entryID string) Value { return NewValue("pi.entry.label", entryID) }

// ---------------------------------------------------------------------------
// mutation-line.ts
// ---------------------------------------------------------------------------

// MutationLine serializes complete read-modify-write jobs for one Session.
type MutationLine struct {
	mu          sync.Mutex
	jobs        chan func()
	started     bool
	sealedError error
	wg          sync.WaitGroup
}

// Run enqueues one job on the line; it executes after all earlier jobs.
// The returned channel receives the job's outcome exactly once.
func (m *MutationLine) Run(operation func() error) <-chan error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealedError != nil {
		out := make(chan error, 1)
		out <- m.sealedError
		return out
	}
	if !m.started {
		m.jobs = make(chan func(), 1024)
		m.started = true
		go func(jobs chan func()) {
			for job := range jobs {
				job()
			}
		}(m.jobs)
	}
	out := make(chan error, 1)
	m.wg.Add(1)
	m.jobs <- func() {
		defer m.wg.Done()
		if m.sealedError != nil {
			out <- m.sealedError
			return
		}
		out <- operation()
	}
	return out
}

// Seal makes later and queued jobs fail with the error; the returned
// channel closes once queued jobs settle.
func (m *MutationLine) Seal(err error) <-chan struct{} {
	m.mu.Lock()
	if m.sealedError == nil {
		m.sealedError = err
	}
	done := make(chan struct{})
	m.wg.Add(1)
	if m.started {
		m.jobs <- func() { defer m.wg.Done() }
	} else {
		m.wg.Done()
	}
	go func() {
		m.wg.Wait()
		close(done)
	}()
	m.mu.Unlock()
	return done
}

// keep jsonx referenced for value payloads.
var _ = jsonx.NewObj

// WriteFromValue converts a ValueWrite into the transactional Write union.
func WriteFromValue(w ValueWrite) Write {
	return Write{Kind: w.Kind, Op: w.Op, Namespace: w.Namespace, Key: w.Key, Value: w.Value}
}

// WriteFromList converts a ListWrite into the transactional Write union.
func WriteFromList(w ListWrite) Write {
	return Write{Kind: w.Kind, Op: w.Op, Namespace: w.Namespace, Key: w.Key, Value: w.Value}
}
