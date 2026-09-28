package runtime

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func operationStateToJSON(state *session.OperationState) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("at", state.At)
	control := jsonx.NewObj()
	control.Set("status", state.Control.Status)
	obj.Set("control", control)
	if state.Settings != nil {
		obj.Set("settings", state.Settings)
	}
	if state.LatestAssistantEntryID != nil {
		obj.Set("latestAssistantEntryId", *state.LatestAssistantEntryID)
	}
	if state.Task != nil {
		obj.Set("task", state.Task)
	}
	if state.Batch != nil {
		obj.Set("batch", state.Batch)
	}
	if state.TargetID != nil {
		obj.Set("targetId", *state.TargetID)
	}
	if state.Label != nil {
		obj.Set("label", *state.Label)
	}
	return obj
}

func operationResultToJSON(record *session.OperationResultRecord) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("operationId", record.OperationID)
	obj.Set("kind", record.Kind)
	obj.Set("status", record.Status)
	if record.Error != nil {
		errObj := jsonx.NewObj()
		errObj.Set("code", record.Error.Code)
		errObj.Set("message", record.Error.Message)
		obj.Set("error", errObj)
	}
	if record.FromTipID != nil {
		obj.Set("fromTipId", *record.FromTipID)
	} else {
		obj.Set("fromTipId", nil)
	}
	if record.TipID != nil {
		obj.Set("tipId", *record.TipID)
	} else {
		obj.Set("tipId", nil)
	}
	obj.Set("startedAt", record.StartedAt)
	obj.Set("endedAt", record.EndedAt)
	return obj
}
