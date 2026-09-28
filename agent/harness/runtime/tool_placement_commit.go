package runtime

// tool_placement_commit.go ports harness/runtime/drive/tool-placement.ts's
// staged-result validation and the placement write planning: staged
// pending entries must be toolResult messages matching the source call's
// id+name; the write plan chains entries from the tip, deletes the staged
// payloads, and inserts usage rows for messages carrying usage.

import (
	"fmt"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// PlacementItem pairs one outcome_ready call with its staged message.
type PlacementItem struct {
	Call    *jsonx.Obj
	Message *jsonx.Obj
}

// ValidateStagedResults mirrors readPlacement's staged-result checks:
// each ready call's pending entry must be a toolResult message whose
// toolCallId/toolName match the source block.
func ValidateStagedResults(
	reader session.SessionReader,
	source *ToolBatchSource,
	ready []*jsonx.Obj,
	ctx contextContextAlias,
) ([]PlacementItem, error) {
	items := make([]PlacementItem, 0, len(ready))
	for _, call := range ready {
		resultEntryID := stringOfObj(call, "resultEntryId")
		stored, err := reader.GetValue(session.PendingEntryValue(resultEntryID), ctx)
		if err != nil {
			return nil, err
		}
		if stored == nil {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call %s is missing its staged result", resultEntryID)}
		}
		payload, _ := stored.Value.(*jsonx.Obj)
		if payload == nil {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call %s is missing its staged result", resultEntryID)}
		}
		if payloadType, _ := payload.Get("type"); payloadType != "message" {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call %s is missing its staged result", resultEntryID)}
		}
		messageValue, _ := payload.Get("payload")
		message, ok := messageValue.(*jsonx.Obj)
		if !ok || stringOfObj(message, "role") != "toolResult" {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call %s is missing its staged result", resultEntryID)}
		}
		sourceBlock, err := ToolCallFor(source, call)
		if err != nil {
			return nil, err
		}
		if stringOfObj(message, "toolCallId") != stringOfObj(sourceBlock, "id") ||
			stringOfObj(message, "toolName") != stringOfObj(sourceBlock, "name") {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call %s has a mismatched staged result", resultEntryID)}
		}
		items = append(items, PlacementItem{Call: call, Message: message})
	}
	return items, nil
}

// PlacementWritePlan is the commit plan for one placement.
type PlacementWritePlan struct {
	Writes []session.Write
	// Entries in chain order with their write index (for event seqs).
	Entries           []*session.Entry
	EntryWriteIndexes []int
	// UsageRows carry ids only when the message has usage.
	UsageRows         []session.UsageRow
	UsageWriteIndexes []int
	// CompletedBatch is the batch with every placed call marked completed.
	CompletedBatch *jsonx.Obj
}

// PlanPlacementWrites mirrors commitPlacement's write planning: entries
// chain from the tip, staged payloads delete, usage rows ride along when
// the staged message carries usage, and the batch marks placed calls
// completed.
func PlanPlacementWrites(
	batch *jsonx.Obj,
	items []PlacementItem,
	tipID *string,
	usageID func() string,
) *PlacementWritePlan {
	plan := &PlacementWritePlan{}
	parentID := tipID
	for _, item := range items {
		entry := &session.Entry{
			EntryBase: session.EntryBase{
				ID:       stringOfObj(item.Call, "resultEntryId"),
				ParentID: parentID,
				Type:     session.EntryTypeMessage,
			},
			Message: session.AgentMessagePayload{Role: "toolResult", Message: item.Message},
		}
		if terminate, ok := item.Call.Get("terminate"); ok && terminate == true {
			entry.Terminate = true
		}
		plan.Entries = append(plan.Entries, entry)
		plan.EntryWriteIndexes = append(plan.EntryWriteIndexes, len(plan.Writes))
		plan.Writes = append(plan.Writes,
			session.InsertEntry(entry),
			session.WriteFromValue(session.DeleteValue(session.PendingEntryValue(entry.ID))),
		)
		if usage, ok := item.Message.Get("usage"); ok && usage != nil {
			row := session.UsageRow{
				ID:      usageID(),
				Usage:   usageFromObj(usage),
				EntryID: &entry.ID,
			}
			plan.UsageRows = append(plan.UsageRows, row)
			plan.UsageWriteIndexes = append(plan.UsageWriteIndexes, len(plan.Writes))
			plan.Writes = append(plan.Writes, session.InsertUsage(row))
		}
		parent := entry.ID
		parentID = &parent
	}
	plan.CompletedBatch = markCallsCompleted(batch, items)
	return plan
}

func markCallsCompleted(batch *jsonx.Obj, items []PlacementItem) *jsonx.Obj {
	placed := map[string]bool{}
	for _, item := range items {
		placed[stringOfObj(item.Call, "resultEntryId")] = true
	}
	out := jsonx.NewObj()
	for _, key := range batch.Keys() {
		if key == "calls" {
			continue
		}
		value, _ := batch.Get(key)
		out.Set(key, value)
	}
	callsValue, _ := batch.Get("calls")
	calls, _ := callsValue.([]any)
	updated := make([]any, 0, len(calls))
	for _, item := range calls {
		if call, ok := item.(*jsonx.Obj); ok && placed[stringOfObj(call, "resultEntryId")] {
			completed := jsonx.NewObj()
			for _, key := range call.Keys() {
				value, _ := call.Get(key)
				completed.Set(key, value)
			}
			completed.Set("status", "completed")
			updated = append(updated, completed)
			continue
		}
		updated = append(updated, item)
	}
	out.Set("calls", updated)
	return out
}

func usageFromObj(usage any) (u aiUsageRuntime) {
	obj, ok := usage.(*jsonx.Obj)
	if !ok {
		return u
	}
	u.Input = floatOfKey(obj, "input")
	u.Output = floatOfKey(obj, "output")
	u.CacheRead = floatOfKey(obj, "cacheRead")
	u.CacheWrite = floatOfKey(obj, "cacheWrite")
	u.TotalTokens = floatOfKey(obj, "totalTokens")
	return u
}

func floatOfKey(obj *jsonx.Obj, key string) float64 {
	if v, ok := obj.Get(key); ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}
