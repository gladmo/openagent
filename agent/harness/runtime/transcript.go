package runtime

// transcript.go ports harness/runtime/transcript.ts.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// ChainEntries chains items from a parent: each item's ParentID becomes the
// previous item's ID.
func ChainEntries(parentID *string, entries []*session.Entry) []*session.Entry {
	out := make([]*session.Entry, 0, len(entries))
	current := parentID
	for _, entry := range entries {
		next := *entry
		next.ParentID = current
		id := next.ID
		current = &id
		out = append(out, &next)
	}
	return out
}

// EntryLifecycleEvents emits message_start/message_end/entry_added for
// message entries, entry_added otherwise.
func EntryLifecycleEvents(entry *session.Entry, lane string, runID ...string) []HarnessEvent {
	withRun := func(event HarnessEvent) HarnessEvent {
		if len(runID) > 0 && runID[0] != "" {
			event.Set("runId", runID[0])
		}
		return event
	}
	if entry.Type == session.EntryTypeMessage {
		start := eventLane(lane, "message_start")
		start.Set("message", entry.Message.Message)
		withRun(start)
		end := eventLane(lane, "message_end")
		end.Set("message", entry.Message.Message)
		end.Set("entryId", entry.ID)
		withRun(end)
		added := eventLane(lane, "entry_added")
		added.Set("entry", entryToEventJSON(entry))
		return []HarnessEvent{start, end, added}
	}
	added := eventLane(lane, "entry_added")
	added.Set("entry", entryToEventJSON(entry))
	return []HarnessEvent{added}
}

func eventLane(lane, typ string) HarnessEvent {
	obj := jsonx.NewObj()
	obj.Set("type", typ)
	obj.Set("lane", lane)
	return obj
}

func entryToEventJSON(entry *session.Entry) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("id", entry.ID)
	if entry.ParentID != nil {
		obj.Set("parentId", *entry.ParentID)
	} else {
		obj.Set("parentId", nil)
	}
	obj.Set("type", entry.Type)
	if entry.CustomType != nil {
		obj.Set("customType", *entry.CustomType)
	}
	if entry.Message.Message != nil {
		obj.Set("message", entry.Message.Message)
	}
	if entry.Summary != "" {
		obj.Set("summary", entry.Summary)
	}
	return obj
}

// CommittedEntryEvents materializes entries with committed seqs/timestamps
// and emits their lifecycle events.
func CommittedEntryEvents(entries []*session.Entry, commit session.CommitResult, lane string, runID string, firstWriteIndex int) []HarnessEvent {
	out := []HarnessEvent{}
	for index, entry := range entries {
		materialized := *entry
		seqIndex := firstWriteIndex + index
		if seqIndex < len(commit.Seqs) {
			materialized.Seq = commit.Seqs[seqIndex]
		}
		materialized.Timestamp = commit.Timestamp
		if runID != "" {
			out = append(out, EntryLifecycleEvents(&materialized, lane, runID)...)
		} else {
			out = append(out, EntryLifecycleEvents(&materialized, lane)...)
		}
	}
	return out
}

// ReadBoundedEntries scans a branch tip back to the newest compaction,
// returning oldest-first.
func ReadBoundedEntries(reader session.SessionReader, tipID *string, ctx contextContextAlias) ([]*session.Entry, error) {
	if tipID == nil {
		return nil, &sessionInvariantErrorAlias{Message: "Run operation has no Branch tip"}
	}
	entries, err := reader.ScanBranch(session.StorageBranchScan{
		BranchScan: session.BranchScan{StopAtType: session.EntryTypeCompaction, Order: "newestFirst"},
		StartID:    *tipID,
	}, ctx)
	if err != nil {
		return nil, err
	}
	// Reverse to oldest-first.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries, nil
}

// LaneQueuedItem mirrors the TS view of one inbox item.
type LaneQueuedItem struct {
	EntryID    string
	Kind       string
	Type       string // message | custom
	Message    *jsonx.Obj
	CustomType string
	Data       any
}

// ReadLaneQueues resolves inbox pending-entry payloads.
func ReadLaneQueues(reader session.SessionReader, inbox []session.InboxItem, ctx contextContextAlias) ([]LaneQueuedItem, error) {
	out := make([]LaneQueuedItem, 0, len(inbox))
	for _, item := range inbox {
		stored, err := reader.GetValue(session.PendingEntryValue(item.EntryID), ctx)
		if err != nil {
			return nil, err
		}
		if stored == nil {
			return nil, &sessionInvariantErrorAlias{Message: "Pending " + item.Kind + " entry " + item.EntryID + " is missing its payload"}
		}
		payload, _ := stored.Value.(*jsonx.Obj)
		if payload == nil {
			return nil, &sessionInvariantErrorAlias{Message: "Pending " + item.Kind + " entry " + item.EntryID + " is missing its payload"}
		}
		payloadType, _ := payload.Get("type")
		if payloadType == "message" {
			message, _ := payload.Get("payload")
			out = append(out, LaneQueuedItem{
				EntryID: item.EntryID,
				Kind:    item.Kind,
				Type:    "message",
				Message: objOrNil(message),
			})
			continue
		}
		if item.Kind != "write" {
			return nil, &sessionInvariantErrorAlias{Message: "Pending " + item.Kind + " entry " + item.EntryID + " is not a message"}
		}
		customType, _ := payload.Get("customType")
		queued := LaneQueuedItem{
			EntryID:    item.EntryID,
			Kind:       item.Kind,
			Type:       "custom",
			CustomType: stringOrEmpty(customType),
		}
		if data, ok := payload.Get("payload"); ok && data != nil {
			queued.Data = data
		}
		out = append(out, queued)
	}
	return out, nil
}

// PendingMessage pairs an entry id with its resolved message.
type PendingMessage struct {
	EntryID string
	Message *jsonx.Obj
}

// ReadPendingMessages resolves message payloads by entry id.
func ReadPendingMessages(reader session.SessionReader, ids []string, description string, ctx contextContextAlias) ([]PendingMessage, error) {
	out := make([]PendingMessage, 0, len(ids))
	for _, entryID := range ids {
		stored, err := reader.GetValue(session.PendingEntryValue(entryID), ctx)
		if err != nil {
			return nil, err
		}
		if stored == nil {
			return nil, &sessionInvariantErrorAlias{Message: description + " " + entryID + " is missing its message payload"}
		}
		payload, _ := stored.Value.(*jsonx.Obj)
		if payload == nil {
			return nil, &sessionInvariantErrorAlias{Message: description + " " + entryID + " is missing its message payload"}
		}
		if payloadType, _ := payload.Get("type"); payloadType != "message" {
			return nil, &sessionInvariantErrorAlias{Message: description + " " + entryID + " is missing its message payload"}
		}
		message, _ := payload.Get("payload")
		out = append(out, PendingMessage{EntryID: entryID, Message: objOrNil(message)})
	}
	return out, nil
}

func objOrNil(v any) *jsonx.Obj {
	if obj, ok := v.(*jsonx.Obj); ok {
		return obj
	}
	return nil
}

func stringOrEmpty(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
