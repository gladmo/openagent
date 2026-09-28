package runtime

// tool_placement_finish.go ports harness/runtime/drive/tool-placement.ts's
// placement completion: the completion decision (all-completed -> the
// checkpoint leaf with the terminate-aware continuation; else the batch
// swap), the tool-args cleanup on completion, and the event rendering for
// committed entries + usage rows.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// CompletionPlan is the placement's terminal routing.
type CompletionPlan struct {
	// Complete reports whether every batch call completed.
	Complete bool
	// NextCheckpoint is set when complete.
	NextCheckpoint *session.OperationState
	// NextBatch is set when incomplete.
	NextBatch *session.OperationState
	// TipWrite appends the branch tip advance.
	TipWrite session.Write
	// CleanupArgs deletes the operation's tool-args prefix on completion.
	CleanupArgs []session.Write
}

// PlanCompletion mirrors the completion routing: the completed-call view
// (placed calls carry their terminate), the all-complete checkpoint with
// may_finish iff EVERY call terminates, else the swapped batch; the tip
// advances to the last placed entry; completion deletes the batch's
// tool-args prefix.
func PlanCompletion(
	lane string,
	operationID string,
	batch *jsonx.Obj,
	completedBatch *jsonx.Obj,
	items []PlacementItem,
	scope *session.OperationState,
	reader session.SessionReader,
	ctx contextContextAlias,
) (*CompletionPlan, error) {
	calls := batchCalls(completedBatch)
	complete := true
	allTerminate := true
	for _, call := range calls {
		status, _ := call.Get("status")
		if status != "completed" {
			complete = false
			allTerminate = false
			continue
		}
		if terminate, ok := call.Get("terminate"); !ok || terminate != true {
			allTerminate = false
		}
	}
	plan := &CompletionPlan{Complete: complete}
	// Tip advances to the last placed entry.
	if len(items) > 0 {
		tip := stringOfObj(items[len(items)-1].Call, "resultEntryId")
		plan.TipWrite = session.WriteFromValue(session.SetValue(session.BranchTip(lane), tip))
	} else {
		plan.TipWrite = session.WriteFromValue(session.SetValue(session.BranchTip(lane), nil))
	}
	if complete {
		next := OperationScopeCopy(scope)
		next.At = session.AtCheckpoint
		continuation := jsonx.ObjFrom("kind", "need_assistant", "overflowRecoveryUsed", false)
		if allTerminate {
			continuation = jsonx.ObjFrom("kind", "may_finish", "includeFinalAssistant", false)
		}
		next.Continuation = continuation
		if len(items) > 0 {
			trigger := stringOfObj(items[len(items)-1].Call, "resultEntryId")
			next.TriggerEntryID = trigger
		}
		plan.NextCheckpoint = &next
		turnID := stringOfObj(batch, "turnId")
		args, err := reader.ScanValues(session.OperationToolArgsPrefix(operationID, turnID), ctx)
		if err != nil {
			return nil, err
		}
		for _, stored := range args {
			plan.CleanupArgs = append(plan.CleanupArgs, session.WriteFromValue(session.DeleteValue(stored.Address)))
		}
		return plan, nil
	}
	next := OperationScopeCopy(scope)
	next.At = session.AtTools
	next.Batch = completedBatch
	plan.NextBatch = &next
	return plan, nil
}

// RenderPlacementEvents mirrors the events closure: entry_added per placed
// entry (materialized with the committed seq/timestamp) plus a usage event
// for entries carrying a usage row, with the commit totals.
func RenderPlacementEvents(
	lane string,
	plan *PlacementWritePlan,
	commit session.CommitResult,
) []HarnessEvent {
	var events []HarnessEvent
	for i, entry := range plan.Entries {
		added := eventLane(lane, "entry_added")
		entryObj := jsonx.NewObj()
		entryObj.Set("id", entry.ID)
		if entry.ParentID != nil {
			entryObj.Set("parentId", *entry.ParentID)
		} else {
			entryObj.Set("parentId", nil)
		}
		entryObj.Set("type", entry.Type)
		if entry.Message.Message != nil {
			entryObj.Set("message", entry.Message.Message)
		}
		entryObj.Set("seq", float64(commit.Seqs[plan.EntryWriteIndexes[i]]))
		entryObj.Set("timestamp", commit.Timestamp)
		added.Set("entry", entryObj)
		events = append(events, added)

		for j, row := range plan.UsageRows {
			if row.EntryID != nil && *row.EntryID == entry.ID {
				usageEvent := eventLane(lane, "usage")
				rowObj := jsonx.NewObj()
				rowObj.Set("id", row.ID)
				rowObj.Set("entryId", *row.EntryID)
				rowObj.Set("seq", float64(commit.Seqs[plan.UsageWriteIndexes[j]]))
				usageEvent.Set("row", rowObj)
				usageEvent.Set("totals", usageToJSONForTotals(commit.Stats.Usage))
				events = append(events, usageEvent)
			}
		}
	}
	return events
}

func usageToJSONForTotals(usage aiUsageRuntime) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("input", usage.Input)
	obj.Set("output", usage.Output)
	obj.Set("cacheRead", usage.CacheRead)
	obj.Set("cacheWrite", usage.CacheWrite)
	obj.Set("totalTokens", usage.TotalTokens)
	return obj
}
