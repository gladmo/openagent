package runtime

// tools_batch.go ports harness/runtime/drive/tools.ts's pure batch
// helpers: call lookup/replacement, memo-name validation, the invocation
// ownership error, and the interruption marker surface.

import (
	"fmt"
	"strings"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// InterruptionMarker mirrors INTERRUPTION_MARKER.
const InterruptionMarker = "[Tool execution was interrupted. The preceding output is the latest durable progress snapshot; newer live output may be missing, and the external outcome is unknown.]"

// ToolInvocationEnded mirrors the TS error: the invocation no longer owns
// its durable effect.
type ToolInvocationEnded struct{}

func (e *ToolInvocationEnded) Error() string {
	return "Tool invocation no longer owns its durable effect"
}

// CurrentBatch returns the tools-leaf batch when the operation is in it.
func CurrentBatch(state *RuntimeLaneState) (*session.OperationState, *jsonx.Obj) {
	if state.Operation == nil || state.Operation.State.At != session.AtTools {
		return nil, nil
	}
	return &state.Operation.State, state.Operation.State.Batch
}

// FindCall locates one call in the batch by sourceIndex + resultEntryId.
func FindCall(batch *jsonx.Obj, sourceIndex int64, resultEntryID string) *jsonx.Obj {
	if batch == nil {
		return nil
	}
	callsValue, ok := batch.Get("calls")
	if !ok {
		return nil
	}
	calls, ok := callsValue.([]any)
	if !ok {
		return nil
	}
	for _, item := range calls {
		call, ok := item.(*jsonx.Obj)
		if !ok {
			continue
		}
		callIndex, _ := jsonx.ToFloat(call.MustGet("sourceIndex"))
		callEntry, _ := call.Get("resultEntryId")
		if int64(callIndex) == sourceIndex && callEntry == resultEntryID {
			return call
		}
	}
	return nil
}

// ReplaceCall swaps one call in the batch (by sourceIndex + resultEntryId)
// and returns the updated batch.
func ReplaceCall(batch *jsonx.Obj, replacement *jsonx.Obj) *jsonx.Obj {
	if batch == nil {
		return nil
	}
	callsValue, _ := batch.Get("calls")
	calls, _ := callsValue.([]any)
	replIndex, _ := jsonx.ToFloat(replacement.MustGet("sourceIndex"))
	replEntry, _ := replacement.Get("resultEntryId")
	out := jsonx.NewObj()
	// Preserve the other batch fields.
	for _, key := range batch.Keys() {
		if key == "calls" {
			continue
		}
		value, _ := batch.Get(key)
		out.Set(key, value)
	}
	updated := make([]any, 0, len(calls))
	replaced := false
	for _, item := range calls {
		if call, ok := item.(*jsonx.Obj); ok {
			callIndex, _ := jsonx.ToFloat(call.MustGet("sourceIndex"))
			callEntry, _ := call.Get("resultEntryId")
			if int64(callIndex) == int64(replIndex) && callEntry == replEntry {
				updated = append(updated, replacement)
				replaced = true
				continue
			}
		}
		updated = append(updated, item)
	}
	if !replaced {
		updated = append(updated, replacement)
	}
	out.Set("calls", updated)
	return out
}

// ValidateMemoName mirrors validateMemoName: non-empty, no ':'.
func ValidateMemoName(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("Tool invocation memo name must not be empty")
	}
	if strings.Contains(name, ":") {
		return fmt.Errorf("Tool invocation memo name must not contain ':'")
	}
	return nil
}

// MemoAddress builds the pi.op.tool_memo key for one invocation.
func MemoAddress(operationID, invocationID, name string) session.Value {
	return session.OperationToolMemo(operationID, invocationID, name)
}

// ArgsAddress builds the pi.op.tool_args key for one call.
func ArgsAddress(operationID, stepID string, sourceIndex int64) session.Value {
	return session.OperationToolArgs(operationID, stepID, sourceIndex)
}
