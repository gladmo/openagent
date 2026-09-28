package runtime

// boundary.go ports harness/runtime/drive/boundary.ts: the boundary inbox
// planner, assistant-ready construction, placement events, and the
// finish-run boundary replan.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// NormalizedRetryPolicy mirrors the TS interface.
type NormalizedRetryPolicy struct {
	MaxAttempts     int64
	BaseDelayMS     float64
	MaxAgentDelayMS float64
}

// NormalizeRetryPolicy derives the runtime policy from config.
func NormalizeRetryPolicy(enabled bool, maxRetries int64, baseDelayMS, maxAgentDelayMS float64) NormalizedRetryPolicy {
	attempts := int64(1)
	if enabled {
		attempts = maxRetries + 1
	}
	delay := maxAgentDelayMS
	if delay == 0 {
		delay = 60000 // DEFAULT_MAX_AGENT_RETRY_DELAY_MS
	}
	return NormalizedRetryPolicy{MaxAttempts: attempts, BaseDelayMS: baseDelayMS, MaxAgentDelayMS: delay}
}

// BoundaryPlacement mirrors the TS interface.
type BoundaryPlacement struct {
	Entries        []*session.Entry
	Writes         []session.Write
	TipID          *string
	Inbox          []session.InboxItem
	TriggerEntryID string
	HasTrigger     bool
	Queues         *QueuesSnapshot
}

// AssistantReadyAtBoundary mirrors assistantReadyAtBoundary: builds the
// assistant.ready leaf with a fresh generation context.
func AssistantReadyAtBoundary(state *RuntimeLaneState, scope *session.OperationState, triggerEntryID string, overflowRecoveryUsed bool, stepID string) *session.OperationState {
	next := OperationScopeCopy(scope)
	next.At = session.AtAssistantReady
	generation := jsonx.NewObj()
	generation.Set("stepId", stepID)
	generation.Set("triggerEntryId", triggerEntryID)
	next.GenerationContext = generation
	// nextAttempt
	next.NextAttempt = 1
	_ = overflowRecoveryUsed
	_ = state
	return &next
}

// OperationScopeCopy copies the uniform scope into a fresh state value.
func OperationScopeCopy(scope *session.OperationState) session.OperationState {
	return session.OperationState{
		Control:                scope.Control,
		Settings:               scope.Settings,
		LatestAssistantEntryID: scope.LatestAssistantEntryID,
	}
}

// PlanBoundaryInbox mirrors planBoundaryInbox: select steer (mode-aware),
// optionally one followUp when nothing projects, chain entries from the
// tip, delete consumed pending payloads, advance the tip.
// projectorSet decides which custom entries project as model context.
// laneName namespaces the durable branch-tip write (the planner is pure;
// the lane name arrives per call, never via shared state).
func PlanBoundaryInbox(
	inbox []session.InboxItem,
	steeringMode, followUpMode string,
	reader session.SessionReader,
	tipID *string,
	followUpWhenNoTrigger bool,
	projectorSet map[string]bool,
	laneName string,
	ctx contextContextAlias,
) (*BoundaryPlacement, error) {
	steer := filterKind(inbox, "steer")
	selectedSteer := steer
	if steeringMode != "all" && len(steer) > 1 {
		selectedSteer = steer[:1]
	}
	selectedSet := map[string]bool{}
	for _, item := range selectedSteer {
		selectedSet[item.EntryID] = true
	}
	selected := []session.InboxItem{}
	for _, item := range inbox {
		if item.Kind == "write" || selectedSet[item.EntryID] {
			selected = append(selected, item)
		}
	}

	load := func(items []session.InboxItem) ([]pendingPayload, error) {
		out := make([]pendingPayload, 0, len(items))
		for _, item := range items {
			stored, err := reader.GetValue(session.PendingEntryValue(item.EntryID), ctx)
			if err != nil {
				return nil, err
			}
			if stored == nil {
				return nil, &session.SessionInvariantError{Message: "Pending " + item.Kind + " entry " + item.EntryID + " is missing its payload"}
			}
			payload, _ := stored.Value.(*jsonx.Obj)
			if payload == nil {
				return nil, &session.SessionInvariantError{Message: "Pending " + item.Kind + " entry " + item.EntryID + " is missing its payload"}
			}
			payloadType, _ := payload.Get("type")
			if item.Kind != "write" && payloadType != "message" {
				return nil, &session.SessionInvariantError{Message: "Queued " + item.Kind + " entry " + item.EntryID + " is not a message"}
			}
			out = append(out, pendingPayload{item: item, pending: payload})
		}
		return out, nil
	}

	pending, err := load(selected)
	if err != nil {
		return nil, err
	}
	projects := func(payload *jsonx.Obj) bool {
		payloadType, _ := payload.Get("type")
		if payloadType == "message" {
			return true
		}
		customType, _ := payload.Get("customType")
		if s, ok := customType.(string); ok {
			return projectorSet[s]
		}
		return false
	}
	if followUpWhenNoTrigger {
		anyProjects := false
		for _, p := range pending {
			if projects(p.pending) {
				anyProjects = true
				break
			}
		}
		if !anyProjects {
			followUp := filterKind(inbox, "followUp")
			selectedFollowUp := followUp
			if followUpMode != "all" && len(followUp) > 1 {
				selectedFollowUp = followUp[:1]
			}
			for _, item := range selectedFollowUp {
				selected = append(selected, item)
			}
			// Restore inbox order.
			selected = sortByInbox(selected, inbox)
			pending, err = load(selected)
			if err != nil {
				return nil, err
			}
		}
	}

	parentID := tipID
	triggerEntryID := ""
	hasTrigger := false
	entries := []*session.Entry{}
	for _, p := range pending {
		entry := PendingEntryWrite(p.item.EntryID, p.pending)
		entry.ParentID = parentID
		parentID = &p.item.EntryID
		if projects(p.pending) {
			triggerEntryID = p.item.EntryID
			hasTrigger = true
		}
		entries = append(entries, entry)
	}
	selectedIDs := map[string]bool{}
	for _, item := range selected {
		selectedIDs[item.EntryID] = true
	}
	remainder := []session.InboxItem{}
	for _, item := range inbox {
		if !selectedIDs[item.EntryID] {
			remainder = append(remainder, item)
		}
	}

	writes := []session.Write{}
	for _, entry := range entries {
		writes = append(writes, session.InsertEntry(entry))
	}
	for _, item := range selected {
		writes = append(writes, session.WriteFromValue(session.DeleteValue(session.PendingEntryValue(item.EntryID))))
	}
	if len(entries) > 0 && parentID != nil {
		writes = append(writes, session.WriteFromValue(session.SetValue(
			session.BranchTip(laneName), *parentID,
		)))
	}

	var queues *QueuesSnapshot
	if len(selected) > 0 {
		items, err := ReadLaneQueues(reader, remainder, ctx)
		if err != nil {
			return nil, err
		}
		snapshot := queuesFromItems(items)
		queues = snapshot
	}
	return &BoundaryPlacement{
		Entries:        entries,
		Writes:         writes,
		TipID:          parentID,
		Inbox:          remainder,
		TriggerEntryID: triggerEntryID,
		HasTrigger:     hasTrigger,
		Queues:         queues,
	}, nil
}

// laneNameHolder carried the branch-tip write namespace as package state;
// it raced cross-lane commits and was replaced by the laneName parameter.

type pendingPayload struct {
	item    session.InboxItem
	pending *jsonx.Obj
}

func filterKind(inbox []session.InboxItem, kind string) []session.InboxItem {
	out := []session.InboxItem{}
	for _, item := range inbox {
		if item.Kind == kind {
			out = append(out, item)
		}
	}
	return out
}

func sortByInbox(selected, inbox []session.InboxItem) []session.InboxItem {
	position := map[string]int{}
	for i, item := range inbox {
		position[item.EntryID] = i
	}
	out := append([]session.InboxItem{}, selected...)
	// stable insertion by inbox position
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			if position[out[j].EntryID] < position[out[j-1].EntryID] {
				out[j], out[j-1] = out[j-1], out[j]
			} else {
				break
			}
		}
	}
	return out
}

func queuesFromItems(items []LaneQueuedItem) *QueuesSnapshot {
	out := &QueuesSnapshot{}
	for _, item := range items {
		switch item.Kind {
		case "steer":
			out.Steering = append(out.Steering, queueItemSnapshot{EntryID: item.EntryID, Kind: item.Kind})
		case "followUp":
			out.FollowUp = append(out.FollowUp, queueItemSnapshot{EntryID: item.EntryID, Kind: item.Kind})
		case "nextRun":
			snapshot := queueItemSnapshot{EntryID: item.EntryID, Kind: item.Kind}
			out.NextRun = &snapshot
		}
	}
	return out
}

// BoundaryPlacementEvents mirrors boundaryPlacementEvents.
func BoundaryPlacementEvents(placement *BoundaryPlacement, commit session.CommitResult, firstWriteIndex int, lane, runID string) []HarnessEvent {
	events := CommittedEntryEvents(placement.Entries, commit, lane, runID, firstWriteIndex)
	if placement.Queues != nil {
		queueEvent := eventLane(lane, "queue_update")
		queueEvent.Set("queues", queuesToJSON(placement.Queues))
		events = append(events, queueEvent)
	}
	return events
}

func queuesToJSON(queues *QueuesSnapshot) *jsonx.Obj {
	obj := jsonx.NewObj()
	items := func(list []queueItemSnapshot) []any {
		out := make([]any, 0, len(list))
		for _, item := range list {
			out = append(out, jsonx.ObjFrom("entryId", item.EntryID, "kind", item.Kind))
		}
		return out
	}
	obj.Set("steering", items(queues.Steering))
	obj.Set("followUp", items(queues.FollowUp))
	if queues.NextRun != nil {
		obj.Set("nextRun", jsonx.ObjFrom("entryId", queues.NextRun.EntryID, "kind", queues.NextRun.Kind))
	} else {
		obj.Set("nextRun", nil)
	}
	return obj
}
