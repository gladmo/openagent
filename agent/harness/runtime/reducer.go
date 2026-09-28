// Package runtime ports harness/runtime/*: the durable operation state
// machine, lane command planning, and the drive pipeline.
package runtime

import (
	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// Config is the current process-local harness configuration.
type Config struct {
	Tools              []*harness.AgentHarnessTool
	Resources          harness.AgentHarnessResources
	StreamOptions      harness.AgentHarnessStreamOptions
	RetryPolicy        retryPolicyAlias
	Compaction         CompactionSettings
	SteeringMode       string
	FollowUpMode       string
	ToolExecution      string
	ToolContext        any
	SystemPrompt       any
	ToProviderMessages func(messages []jsonxMessageAlias) []messageAlias
	EntryProjectors    []session.EntryProjector
}

// CompactionSettings mirrors the compaction settings.
type CompactionSettings struct {
	Enabled          bool
	ReserveTokens    float64
	KeepRecentTokens float64
}

// LaneSnapshot mirrors the TS interface (mutable under the reducer).
type LaneSnapshot struct {
	Lane          string
	TipID         *string
	Transcript    []*session.Entry
	Queues        *QueuesSnapshot
	LastResult    *session.OperationResultRecord
	Stats         session.SessionStats
	Operation     *LaneOperationSnapshot
	Configuration session.LaneConfiguration
	Faulted       bool
}

// QueuesSnapshot mirrors the queues view.
type QueuesSnapshot struct {
	Steering []queueItemSnapshot
	FollowUp []queueItemSnapshot
	NextRun  *queueItemSnapshot
}

type queueItemSnapshot struct {
	EntryID string
	Kind    string
}

// LaneOperationSnapshot mirrors the operation view on a snapshot.
type LaneOperationSnapshot struct {
	ID               string
	Kind             string // run | compaction | navigation
	StartedAt        float64
	FromTipID        *string
	Status           string // open | aborting
	RunningTools     []runningToolSnapshot
	StreamingMessage *jsonx.Obj
	Retry            *retrySnapshot
	Deferred         *deferredSnapshot
}

type runningToolSnapshot struct {
	Status     string // running | settled
	ToolCallID string
	ToolName   string
	Args       any
	Result     any
	IsError    bool
}

type retrySnapshot struct {
	Attempt       int64
	MaxAttempts   int64
	NextAttemptAt float64
}

type deferredSnapshot struct {
	Handle *jsonx.Obj
	Poll   int64
}

// HarnessEvent is the JSON-shaped harness event (type + lane + payload).
type HarnessEvent = *jsonx.Obj

// EventType returns the event type field.
func EventType(event HarnessEvent) string {
	if v, ok := event.Get("type"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// EventLane returns the optional lane field.
func EventLane(event HarnessEvent) (string, bool) {
	if v, ok := event.Get("lane"); ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}

func eventString(event HarnessEvent, key string) string {
	if v, ok := event.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func eventFloat(event HarnessEvent, key string) float64 {
	if v, ok := event.Get(key); ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

// LaneSnapshotReduction reports whether a fresh snapshot is required.
type LaneSnapshotReduction string

// ReduceLaneSnapshot applies one harness event to a mutable lane snapshot.
// Returns "rebase" when navigation completed (fresh snapshot required).
func ReduceLaneSnapshot(snapshot *LaneSnapshot, event HarnessEvent) LaneSnapshotReduction {
	if lane, hasLane := EventLane(event); hasLane && lane != snapshot.Lane && EventType(event) != "usage" {
		return ""
	}
	switch EventType(event) {
	case "run_start", "compaction_start", "navigation_start":
		kind := "run"
		switch EventType(event) {
		case "compaction_start":
			if snapshot.Operation != nil {
				return ""
			}
			kind = "compaction"
		case "navigation_start":
			kind = "navigation"
		}
		snapshot.Operation = &LaneOperationSnapshot{
			ID:        eventString(event, "runId"),
			Kind:      kind,
			StartedAt: eventFloat(event, "startedAt"),
			FromTipID: snapshot.TipID,
			Status:    "open",
		}
	case "operation_abort":
		if operation := matchingOperation(snapshot, eventString(event, "operationId")); operation != nil {
			operation.Status = "aborting"
		}
	case "run_resume":
		if operation := matchingOperation(snapshot, eventString(event, "runId")); operation != nil {
			operation.Deferred = nil
		}
	case "run_suspend":
		operation := matchingOperation(snapshot, eventString(event, "runId"))
		if operation == nil {
			return ""
		}
		operation.StreamingMessage = nil
		poll, _ := jsonx.ToFloat(mustGet(event, "poll"))
		operation.Deferred = &deferredSnapshot{
			Handle: objOf(event, "deferred"),
			Poll:   int64(poll),
		}
	case "retry_scheduled":
		if operation := matchingOperation(snapshot, eventString(event, "runId")); operation != nil {
			operation.Retry = &retrySnapshot{
				Attempt:       int64(eventFloat(event, "attempt")),
				MaxAttempts:   int64(eventFloat(event, "maxAttempts")),
				NextAttemptAt: eventFloat(event, "notBefore"),
			}
		}
	case "retry_start", "retry_end":
		if operation := matchingOperation(snapshot, eventString(event, "runId")); operation != nil {
			operation.Retry = nil
		}
	case "message_start":
		runID, hasRun := event.Get("runId")
		_ = runID
		message := objOf(event, "message")
		if !hasRun || message == nil {
			return ""
		}
		if stringFromObj(message, "role") != "assistant" || stringFromObj(message, "stopReason") != "pending" {
			return ""
		}
		if operation := matchingOperation(snapshot, eventString(event, "runId")); operation != nil {
			operation.StreamingMessage = message
		}
	case "message_update":
		message := objOf(event, "message")
		if message == nil || stringFromObj(message, "role") != "assistant" {
			return ""
		}
		if operation := matchingOperation(snapshot, eventString(event, "runId")); operation != nil {
			operation.StreamingMessage = message
		}
	case "message_end":
		if v, ok := event.Get("runId"); !ok || v == nil {
			return ""
		}
		if operation := matchingOperation(snapshot, eventString(event, "runId")); operation != nil {
			operation.StreamingMessage = nil
		}
	case "tool_start":
		operation := matchingOperation(snapshot, eventString(event, "runId"))
		if operation == nil {
			return ""
		}
		upsertTool(operation, runningToolSnapshot{
			Status:     "running",
			ToolCallID: eventString(event, "toolCallId"),
			ToolName:   eventString(event, "toolName"),
			Args:       mustGet(event, "args"),
		})
	case "tool_update":
		operation := matchingOperation(snapshot, eventString(event, "runId"))
		if operation == nil {
			return ""
		}
		for i := range operation.RunningTools {
			tool := &operation.RunningTools[i]
			if tool.ToolCallID == eventString(event, "toolCallId") && tool.Status == "running" {
				tool.Result = mustGet(event, "partialResult")
			}
		}
	case "tool_end":
		operation := matchingOperation(snapshot, eventString(event, "runId"))
		if operation == nil {
			return ""
		}
		for i := range operation.RunningTools {
			if operation.RunningTools[i].ToolCallID == eventString(event, "toolCallId") {
				current := operation.RunningTools[i]
				operation.RunningTools[i] = runningToolSnapshot{
					Status:     "settled",
					ToolCallID: current.ToolCallID,
					ToolName:   eventString(event, "toolName"),
					Args:       current.Args,
					Result:     mustGet(event, "result"),
					IsError:    event.MustGet("isError") == true,
				}
				return ""
			}
		}
	case "entry_added":
		entry := entryOf(event)
		if entry == nil {
			return ""
		}
		if entry.Type == session.EntryTypeMessage && entry.Message.Role == "toolResult" {
			if operation := snapshot.Operation; operation != nil {
				toolCallID := ""
				if entry.Message.Message != nil {
					toolCallID = stringFromObj(entry.Message.Message, "toolCallId")
				}
				for i := range operation.RunningTools {
					if operation.RunningTools[i].ToolCallID == toolCallID {
						operation.RunningTools = append(operation.RunningTools[:i], operation.RunningTools[i+1:]...)
						break
					}
				}
			}
		}
		if entry.Type == session.EntryTypeCompaction {
			snapshot.Transcript = []*session.Entry{entry}
		} else {
			snapshot.Transcript = append(snapshot.Transcript, entry)
		}
		entryID := entry.ID
		snapshot.TipID = &entryID
		if entry.Type == session.EntryTypeMessage {
			snapshot.Stats.MessageCount++
		}
	case "queue_update":
		queues := objOf(event, "queues")
		if queues != nil {
			snapshot.Queues = queuesFromJSON(queues)
		}
	case "usage":
		if totals := objOf(event, "totals"); totals != nil {
			snapshot.Stats.Usage = usageFromJSONObject(totals)
		}
	case "config_update":
		if lane, hasLane := EventLane(event); !hasLane || lane != snapshot.Lane {
			return ""
		}
		switch eventString(event, "property") {
		case "model":
			if value := objOf(event, "value"); value != nil {
				snapshot.Configuration.Model.Provider = stringFromObj(value, "provider")
				snapshot.Configuration.Model.ModelID = stringFromObj(value, "modelId")
			}
		case "thinkingLevel":
			snapshot.Configuration.ThinkingLevel = eventString(event, "value")
		case "activeTools":
			if arr, ok := mustGet(event, "value").([]any); ok {
				names := make([]string, 0, len(arr))
				for _, item := range arr {
					if s, ok := item.(string); ok {
						names = append(names, s)
					}
				}
				snapshot.Configuration.ActiveToolNames = names
			}
		}
	case "run_end":
		operation := matchingOperation(snapshot, eventString(event, "runId"))
		if operation == nil || operation.Kind != "run" {
			return ""
		}
		record := &session.OperationResultRecord{
			OperationID: eventString(event, "runId"),
			Kind:        "run",
			Status:      eventString(event, "status"),
			FromTipID:   stringPtrOf(event, "fromTipId"),
			TipID:       stringPtrOf(event, "tipId"),
			StartedAt:   operation.StartedAt,
			EndedAt:     eventFloat(event, "endedAt"),
		}
		if record.Status == "failed" {
			record.Error = errorOf(event)
		}
		snapshot.LastResult = record
		snapshot.Operation = nil
		tip := eventString(event, "tipId")
		snapshot.TipID = &tip
	case "compaction_end":
		operation := matchingOperation(snapshot, eventString(event, "runId"))
		if operation == nil || operation.Kind != "compaction" {
			return ""
		}
		record := &session.OperationResultRecord{
			OperationID: eventString(event, "runId"),
			Kind:        "compaction",
			Status:      eventString(event, "status"),
			FromTipID:   operation.FromTipID,
			TipID:       snapshot.TipID,
			StartedAt:   operation.StartedAt,
			EndedAt:     eventFloat(event, "endedAt"),
		}
		if record.Status == "failed" {
			record.Error = errorOf(event)
		}
		snapshot.LastResult = record
		snapshot.Operation = nil
	case "navigation_end":
		return "rebase"
	case "fault":
		snapshot.Faulted = true
	}
	return ""
}

func matchingOperation(snapshot *LaneSnapshot, operationID string) *LaneOperationSnapshot {
	if snapshot.Operation != nil && snapshot.Operation.ID == operationID {
		return snapshot.Operation
	}
	return nil
}

func upsertTool(operation *LaneOperationSnapshot, tool runningToolSnapshot) {
	for i := range operation.RunningTools {
		if operation.RunningTools[i].ToolCallID == tool.ToolCallID {
			operation.RunningTools[i] = tool
			return
		}
	}
	operation.RunningTools = append(operation.RunningTools, tool)
}

func mustGet(event HarnessEvent, key string) any {
	if v, ok := event.Get(key); ok {
		return v
	}
	return nil
}

func objOf(event HarnessEvent, key string) *jsonx.Obj {
	if v, ok := event.Get(key); ok {
		if obj, ok := v.(*jsonx.Obj); ok {
			return obj
		}
	}
	return nil
}

func stringFromObj(obj *jsonx.Obj, key string) string {
	if v, ok := obj.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func stringPtrOf(event HarnessEvent, key string) *string {
	if v, ok := event.Get(key); ok {
		if s, ok := v.(string); ok {
			return &s
		}
	}
	return nil
}

func errorOf(event HarnessEvent) *session.OperationError {
	if errObj := objOf(event, "error"); errObj != nil {
		return &session.OperationError{
			Code:    stringFromObj(errObj, "code"),
			Message: stringFromObj(errObj, "message"),
		}
	}
	return nil
}

func entryOf(event HarnessEvent) *session.Entry {
	entryObj := objOf(event, "entry")
	if entryObj == nil {
		return nil
	}
	entry := &session.Entry{}
	entry.ID = stringFromObj(entryObj, "id")
	if parent, ok := entryObj.Get("parentId"); ok && parent != nil {
		if s, ok := parent.(string); ok {
			entry.ParentID = &s
		}
	}
	entry.Seq = int64FromObj(entryObj, "seq")
	entry.Timestamp = floatFromObj(entryObj, "timestamp")
	entry.Type = stringFromObj(entryObj, "type")
	if message := objOf2(entryObj, "message"); message != nil {
		entry.Message = session.AgentMessagePayload{
			Role:    stringFromObj(message, "role"),
			Message: message,
		}
	}
	return entry
}

func objOf2(obj *jsonx.Obj, key string) *jsonx.Obj {
	if v, ok := obj.Get(key); ok {
		if o, ok := v.(*jsonx.Obj); ok {
			return o
		}
	}
	return nil
}

func int64FromObj(obj *jsonx.Obj, key string) int64 {
	if v, ok := obj.Get(key); ok {
		if f, ok := v.(float64); ok {
			return int64(f)
		}
	}
	return 0
}

func floatFromObj(obj *jsonx.Obj, key string) float64 {
	if v, ok := obj.Get(key); ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

func queuesFromJSON(queues *jsonx.Obj) *QueuesSnapshot {
	out := &QueuesSnapshot{}
	read := func(key string) []queueItemSnapshot {
		arr, ok := queues.MustGet(key).([]any)
		if !ok {
			return nil
		}
		items := make([]queueItemSnapshot, 0, len(arr))
		for _, item := range arr {
			if obj, ok := item.(*jsonx.Obj); ok {
				items = append(items, queueItemSnapshot{
					EntryID: stringFromObj(obj, "entryId"),
					Kind:    stringFromObj(obj, "kind"),
				})
			}
		}
		return items
	}
	out.Steering = read("steering")
	out.FollowUp = read("followUp")
	if next := objOf2(queues, "nextRun"); next != nil {
		out.NextRun = &queueItemSnapshot{
			EntryID: stringFromObj(next, "entryId"),
			Kind:    stringFromObj(next, "kind"),
		}
	}
	return out
}

func usageFromJSONObject(totals *jsonx.Obj) (usage aiUsageRuntime) {
	usage.Input = floatFromObj(totals, "input")
	usage.Output = floatFromObj(totals, "output")
	usage.CacheRead = floatFromObj(totals, "cacheRead")
	usage.CacheWrite = floatFromObj(totals, "cacheWrite")
	usage.TotalTokens = floatFromObj(totals, "totalTokens")
	return usage
}
