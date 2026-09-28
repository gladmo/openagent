package runtime

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// operationStateToJSON serializes the FULL OperationState: crash recovery
// re-reads the operation's position from these fields, so every json-tagged
// field round trips (write and restore stay symmetric; jsonx values stay in
// the jsonx model).
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
	if state.Continuation != nil {
		obj.Set("continuation", state.Continuation)
	}
	if state.TriggerEntryID != "" {
		obj.Set("triggerEntryId", state.TriggerEntryID)
	}
	if state.GenerationContext != nil {
		obj.Set("generationContext", state.GenerationContext)
	}
	if state.Attempt != 0 {
		obj.Set("attempt", float64(state.Attempt))
	}
	if state.NextAttempt != 0 {
		obj.Set("nextAttempt", float64(state.NextAttempt))
	}
	if state.NotBefore != 0 {
		obj.Set("notBefore", state.NotBefore)
	}
	if state.ErrorMessage != "" {
		obj.Set("errorMessage", state.ErrorMessage)
	}
	if state.ResponseEntryID != "" {
		obj.Set("responseEntryId", state.ResponseEntryID)
	}
	if state.UsageID != "" {
		obj.Set("usageId", state.UsageID)
	}
	if state.IntendedOutputLimit != 0 {
		obj.Set("intendedOutputLimit", state.IntendedOutputLimit)
	}
	if state.ContextWindow != 0 {
		obj.Set("contextWindow", state.ContextWindow)
	}
	if state.Batch != nil {
		obj.Set("batch", state.Batch)
	}
	if state.StepID != "" {
		obj.Set("stepId", state.StepID)
	}
	if state.SourceEntryID != "" {
		obj.Set("sourceEntryId", state.SourceEntryID)
	}
	if state.Poll != 0 {
		obj.Set("poll", float64(state.Poll))
	}
	if state.Configuration != nil {
		obj.Set("configuration", state.Configuration)
	}
	if state.StreamOptions != nil {
		obj.Set("streamOptions", state.StreamOptions)
	}
	if state.Task != nil {
		obj.Set("task", state.Task)
	}
	if state.SummaryContext != nil {
		obj.Set("summaryContext", state.SummaryContext)
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
