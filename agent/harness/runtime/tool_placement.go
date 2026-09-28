package runtime

// tool_placement.go ports harness/runtime/drive/tool-placement.ts's pure
// helpers: the batch source read (assistant entry + call blocks by source
// index), call resolution, the with-batch state construction, and the
// outcome_ready prefix run extraction.

import (
	"fmt"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// ToolBatchSource mirrors the TS type: the assistant message plus the
// tool-call blocks keyed by source index.
type ToolBatchSource struct {
	Assistant *jsonx.Obj
	Calls     map[int64]*jsonx.Obj
}

// ReadToolBatchSource mirrors readToolBatchSource: the assistant entry
// must be a message entry with assistant role; each batch call's
// sourceIndex must name a toolCall block.
func ReadToolBatchSource(reader session.SessionReader, batch *jsonx.Obj, ctx contextContextAlias) (*ToolBatchSource, error) {
	if batch == nil {
		return nil, &session.SessionInvariantError{Message: "Tool batch assistant entry is invalid"}
	}
	assistantID := stringOfObj(batch, "assistantEntryId")
	entries, err := reader.GetEntries([]string{assistantID}, ctx)
	if err != nil {
		return nil, err
	}
	entry, ok := entries[assistantID]
	if !ok || entry.Type != session.EntryTypeMessage || entry.Message.Role != "assistant" {
		return nil, &session.SessionInvariantError{Message: "Tool batch assistant entry is invalid"}
	}
	source := &ToolBatchSource{
		Assistant: entry.Message.Message,
		Calls:     map[int64]*jsonx.Obj{},
	}
	content := assistantContent(source.Assistant)
	for _, call := range batchCalls(batch) {
		sourceIndex, _ := jsonx.ToFloat(call.MustGet("sourceIndex"))
		idx := int64(sourceIndex)
		if idx < 0 || int(idx) >= len(content) {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call source index %d does not name a tool-call block", idx)}
		}
		block, ok := content[idx].(*jsonx.Obj)
		if !ok {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call source index %d does not name a tool-call block", idx)}
		}
		if t, _ := block.Get("type"); t != "toolCall" {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call source index %d does not name a tool-call block", idx)}
		}
		source.Calls[idx] = block
	}
	return source, nil
}

func assistantContent(assistant *jsonx.Obj) []any {
	if assistant == nil {
		return nil
	}
	contentValue, ok := assistant.Get("content")
	if !ok {
		return nil
	}
	content, _ := contentValue.([]any)
	return content
}

func batchCalls(batch *jsonx.Obj) []*jsonx.Obj {
	if batch == nil {
		return nil
	}
	callsValue, _ := batch.Get("calls")
	calls, _ := callsValue.([]any)
	out := make([]*jsonx.Obj, 0, len(calls))
	for _, item := range calls {
		if call, ok := item.(*jsonx.Obj); ok {
			out = append(out, call)
		}
	}
	return out
}

func stringOfObj(obj *jsonx.Obj, key string) string {
	if v, ok := obj.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ToolCallFor mirrors toolCallFor: resolve the source block or fail.
func ToolCallFor(source *ToolBatchSource, call *jsonx.Obj) (*jsonx.Obj, error) {
	sourceIndex, _ := jsonx.ToFloat(call.MustGet("sourceIndex"))
	block, ok := source.Calls[int64(sourceIndex)]
	if !ok {
		return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call source index %d is invalid", int64(sourceIndex))}
	}
	return block, nil
}

// WithToolBatch mirrors withToolBatch: the tools leaf carrying the batch.
func WithToolBatch(scope *session.OperationState, batch *jsonx.Obj) session.OperationState {
	next := OperationScopeCopy(scope)
	next.At = session.AtTools
	next.Batch = batch
	return next
}

// PlacementRun extracts the leading run of outcome_ready calls starting at
// the first non-completed call: the placement prefix.
type PlacementRun struct {
	Ready []*jsonx.Obj
	// HasResults reports whether turn results were staged.
	TurnResults []*jsonx.Obj
}

// ExtractPlacementRun mirrors readPlacement's prefix logic: find the first
// non-completed call; collect the consecutive outcome_ready run from
// there; anything else stops the run.
func ExtractPlacementRun(batch *jsonx.Obj) *PlacementRun {
	calls := batchCalls(batch)
	first := -1
	for i, call := range calls {
		status, _ := call.Get("status")
		if status != "completed" {
			first = i
			break
		}
	}
	if first == -1 {
		return nil
	}
	run := &PlacementRun{}
	for i := first; i < len(calls); i++ {
		status, _ := calls[i].Get("status")
		if status != "outcome_ready" {
			break
		}
		run.Ready = append(run.Ready, calls[i])
	}
	return run
}
