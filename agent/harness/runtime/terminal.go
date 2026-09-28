package runtime

// terminal.go ports harness/runtime/drive/terminal.ts.

import (
	"time"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// OperationCleanupWrites builds the mechanical operation-owned suffix for
// an owning procedure's terminal transaction: deletes op.meta/op.state, all
// tool args/memos/preparations/pending outputs under the operation prefix,
// the assistant frame list for effect-pending states, and pending entries
// of outcome-ready tool calls.
func OperationCleanupWrites(reader session.SessionReader, operationID string, state *session.OperationState, ctx contextContextAlias) ([]session.Write, error) {
	toolArguments, err := reader.ScanValues(session.OperationToolArgsPrefix(operationID), ctx)
	if err != nil {
		return nil, err
	}
	toolMemos, err := reader.ScanValues(session.OperationToolMemoPrefix(operationID), ctx)
	if err != nil {
		return nil, err
	}
	preparations, err := reader.ScanValues(session.OperationPreparationPrefix(operationID), ctx)
	if err != nil {
		return nil, err
	}
	toolOutputs, err := reader.ScanValues(session.PendingToolOutputPrefix(operationID), ctx)
	if err != nil {
		return nil, err
	}

	pendingIDs := []string{}
	// Batch carries ToolCall children; outcome_ready calls own pending
	// result entries.
	if state.At == session.AtTools && state.Batch != nil {
		if callsValue, ok := state.Batch.Get("calls"); ok {
			if calls, ok := callsValue.([]any); ok {
				for _, call := range calls {
					callObj, ok := call.(*jsonx.Obj)
					if !ok {
						continue
					}
					if status, _ := callObj.Get("status"); status == "outcome_ready" {
						if resultEntryID, ok := callObj.Get("resultEntryId"); ok {
							if s, ok := resultEntryID.(string); ok {
								pendingIDs = append(pendingIDs, s)
							}
						}
					}
				}
			}
		}
	}

	writes := []session.Write{
		session.WriteFromValue(session.DeleteValue(session.OperationMetaValue(operationID))),
		session.WriteFromValue(session.DeleteValue(session.OperationStateValue(operationID))),
	}
	appendDeletes := func(values []session.StoredValue) {
		for _, stored := range values {
			writes = append(writes, session.WriteFromValue(session.DeleteValue(stored.Address)))
		}
	}
	appendDeletes(toolArguments)
	appendDeletes(toolMemos)
	appendDeletes(preparations)
	appendDeletes(toolOutputs)

	if state.At == session.AtAssistantEffectPending || state.At == session.AtDeferredEffectPending {
		address := session.PendingAssistantFrames(operationID, state.ResponseEntryID)
		writes = append(writes, session.WriteFromList(session.DeleteList(address)))
	}
	for _, id := range pendingIDs {
		writes = append(writes, session.WriteFromValue(session.DeleteValue(session.PendingEntryValue(id))))
	}
	return writes, nil
}

// OperationResultRecord builds the immutable observation record for one
// terminal decision; only failed results may carry an error.
func OperationResultRecord(meta *session.OperationMeta, status string, tipID *string, operationError *session.OperationError) (*session.OperationResultRecord, error) {
	if (status == session.StatusFailed) != (operationError != nil) {
		return nil, &session.SessionInvariantError{Message: "Only a failed operation result may carry an error"}
	}
	kind := intentKind(meta)
	record := &session.OperationResultRecord{
		OperationID: meta.OperationID,
		Kind:        kind,
		Status:      status,
		FromTipID:   meta.SourceTipID,
		TipID:       tipID,
		StartedAt:   meta.StartedAt,
		EndedAt:     float64(time.Now().UnixMilli()),
	}
	if operationError != nil {
		record.Error = operationError
	}
	return record, nil
}
