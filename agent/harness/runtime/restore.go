package runtime

// restore.go ports harness/runtime/restore.ts.

import (
	"fmt"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// DurableLaneState aliases the session LaneState (durable form).
type DurableLaneState = session.LaneState

// ClassifiedLaneStorage mirrors the TS union.
type ClassifiedLaneStorage struct {
	Kind          string // absent | branch | lane
	Tip           *session.StoredValue
	Configuration *session.StoredValue
	LaneState     *session.StoredValue
}

func classifyLaneStorage(lane string, tip, configuration, laneState *session.StoredValue) (ClassifiedLaneStorage, error) {
	switch {
	case tip == nil && configuration == nil && laneState == nil:
		return ClassifiedLaneStorage{Kind: "absent"}, nil
	case tip != nil && configuration == nil && laneState == nil:
		return ClassifiedLaneStorage{Kind: "branch", Tip: tip}, nil
	case tip == nil:
		return ClassifiedLaneStorage{}, &session.SessionInvariantError{Message: fmt.Sprintf("Lane %q is missing branch.tip", lane)}
	case configuration == nil:
		return ClassifiedLaneStorage{}, &session.SessionInvariantError{Message: fmt.Sprintf("Lane %q is missing lane.config", lane)}
	case laneState == nil:
		return ClassifiedLaneStorage{}, &session.SessionInvariantError{Message: fmt.Sprintf("Lane %q is missing lane.state", lane)}
	default:
		return ClassifiedLaneStorage{Kind: "lane", Tip: tip, Configuration: configuration, LaneState: laneState}, nil
	}
}

// ReadLaneStorage reads one lane's three durable values.
func ReadLaneStorage(reader session.SessionReader, lane string, ctx contextContextAlias) (ClassifiedLaneStorage, error) {
	tip, err := reader.GetValue(session.BranchTip(lane), ctx)
	if err != nil {
		return ClassifiedLaneStorage{}, err
	}
	configuration, err := reader.GetValue(session.LaneConfig(lane), ctx)
	if err != nil {
		return ClassifiedLaneStorage{}, err
	}
	laneState, err := reader.GetValue(session.LaneStateValue(lane), ctx)
	if err != nil {
		return ClassifiedLaneStorage{}, err
	}
	return classifyLaneStorage(lane, tip, configuration, laneState)
}

// RuntimeLaneState mirrors runtime/types.ts LaneState.
type RuntimeLaneState struct {
	TipID           *string
	Configuration   session.LaneConfiguration
	Inbox           []session.InboxItem
	LastOperationID *string
	Operation       *session.Operation
}

func isSummaryState(state *session.OperationState) bool {
	return len(state.At) >= 8 && state.At[:8] == "summary."
}

func taskBoundaryKind(task *jsonx.Obj) string {
	if task == nil {
		return ""
	}
	if boundary, ok := task.Get("boundary"); ok {
		if boundaryObj, ok := boundary.(*jsonx.Obj); ok {
			if kind, ok := boundaryObj.Get("kind"); ok {
				if s, ok := kind.(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

func intentKind(meta *session.OperationMeta) string {
	if meta.Intent == nil {
		return ""
	}
	if kind, ok := meta.Intent.Get("kind"); ok {
		if s, ok := kind.(string); ok {
			return s
		}
	}
	return ""
}

// StateMatchesIntent validates that a durable state satisfies its intent.
func StateMatchesIntent(meta *session.OperationMeta, state *session.OperationState) bool {
	intent := intentKind(meta)
	switch intent {
	case "compaction":
		return isSummaryState(state) && taskBoundaryKind(state.Task) == "finish"
	case "navigation":
		if v, ok := meta.Intent.Get("summarize"); ok && v == true {
			return isSummaryState(state) &&
				taskBoundaryKind(state.Task) == "commit_navigation" &&
				boundaryTargetMatches(state.Task, meta) &&
				boundaryLabelMatches(state.Task, meta) &&
				customInstructionsMatch(state.Task, meta)
		}
		if state.At != "navigation.ready_to_commit" {
			return false
		}
		return targetMatches(state, meta) && labelMatches(state, meta)
	default: // run
		if state.At == "navigation.ready_to_commit" {
			return false
		}
		if isSummaryState(state) {
			return taskBoundaryKind(state.Task) == "resume_checkpoint"
		}
		return true
	}
}

func boundaryTargetMatches(task *jsonx.Obj, meta *session.OperationMeta) bool {
	target, _ := meta.Intent.Get("targetId")
	if boundary, ok := task.Get("boundary"); ok {
		if boundaryObj, ok := boundary.(*jsonx.Obj); ok {
			if v, ok := boundaryObj.Get("targetId"); ok {
				if target == nil {
					return v == nil
				}
				return v == target
			}
		}
	}
	return false
}

func boundaryLabelMatches(task *jsonx.Obj, meta *session.OperationMeta) bool {
	label, hasLabel := meta.Intent.Get("label")
	if boundary, ok := task.Get("boundary"); ok {
		if boundaryObj, ok := boundary.(*jsonx.Obj); ok {
			v, has := boundaryObj.Get("label")
			if !hasLabel || label == nil {
				return !has || v == nil
			}
			return has && v == label
		}
	}
	return false
}

func customInstructionsMatch(task *jsonx.Obj, meta *session.OperationMeta) bool {
	instructions, has := meta.Intent.Get("customInstructions")
	taskInstructions, hasTask := task.Get("customInstructions")
	if !has || instructions == nil {
		return !hasTask || taskInstructions == nil
	}
	return hasTask && taskInstructions == instructions
}

func targetMatches(state *session.OperationState, meta *session.OperationMeta) bool {
	target, _ := meta.Intent.Get("targetId")
	if target == nil {
		return state.TargetID == nil
	}
	if state.TargetID == nil {
		return false
	}
	if s, ok := target.(string); ok {
		return *state.TargetID == s
	}
	return false
}

func labelMatches(state *session.OperationState, meta *session.OperationMeta) bool {
	label, has := meta.Intent.Get("label")
	if !has || label == nil {
		return state.Label == nil
	}
	if state.Label == nil {
		return false
	}
	if s, ok := label.(string); ok {
		return *state.Label == s
	}
	return false
}

// RestoreSession restores every complete configured lane in one coherent
// mutation.
func RestoreSession(sess session.Session, ctx contextContextAlias) (map[string]*RuntimeLaneState, error) {
	out := map[string]*RuntimeLaneState{}
	err := sess.Mutate(func(mutator session.SessionMutator, ctx contextContextAlias) error {
		tips, err := mutator.ScanValues(session.BranchTip(""), ctx)
		if err != nil {
			return err
		}
		configurations, err := mutator.ScanValues(session.LaneConfig(""), ctx)
		if err != nil {
			return err
		}
		states, err := mutator.ScanValues(session.LaneStateValue(""), ctx)
		if err != nil {
			return err
		}
		tipByLane := map[string]*session.StoredValue{}
		for i := range tips {
			tipByLane[tips[i].Address.Key] = &tips[i]
		}
		configByLane := map[string]*session.StoredValue{}
		for i := range configurations {
			configByLane[configurations[i].Address.Key] = &configurations[i]
		}
		stateByLane := map[string]*session.StoredValue{}
		for i := range states {
			stateByLane[states[i].Address.Key] = &states[i]
		}
		names := map[string]bool{}
		for lane := range tipByLane {
			names[lane] = true
		}
		for lane := range configByLane {
			names[lane] = true
		}
		for lane := range stateByLane {
			names[lane] = true
		}
		for lane := range names {
			classified, err := classifyLaneStorage(lane, tipByLane[lane], configByLane[lane], stateByLane[lane])
			if err != nil {
				return err
			}
			if classified.Kind != "lane" {
				continue
			}
			restored, err := RestoreLaneState(mutator, lane, classified, ctx)
			if err != nil {
				return err
			}
			out[lane] = restored
		}
		return nil
	}, ctx)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RestoreLane restores one configured lane.
func RestoreLane(sess session.Session, lane string, ctx contextContextAlias) (*RuntimeLaneState, error) {
	var restored *RuntimeLaneState
	err := sess.Mutate(func(mutator session.SessionMutator, ctx contextContextAlias) error {
		classified, err := ReadLaneStorage(mutator, lane, ctx)
		if err != nil {
			return err
		}
		switch classified.Kind {
		case "absent":
			return &session.SessionInvariantError{Message: fmt.Sprintf("Lane %q is missing branch.tip", lane)}
		case "branch":
			return &session.SessionInvariantError{Message: fmt.Sprintf("Lane %q is missing lane.config", lane)}
		}
		restored, err = RestoreLaneState(mutator, lane, classified, ctx)
		return err
	}, ctx)
	if err != nil {
		return nil, err
	}
	return restored, nil
}

// RestoreLaneState reads the current operation (meta + state) and validates
// it against the intent.
func RestoreLaneState(reader session.SessionReader, lane string, stored ClassifiedLaneStorage, ctx contextContextAlias) (*RuntimeLaneState, error) {
	durableLaneState := durableLaneStateFromValue(stored.LaneState.Value)
	operationID := durableLaneState.CurrentOperationID
	var operation *session.Operation
	if operationID != nil {
		meta, err := reader.GetValue(session.OperationMetaValue(*operationID), ctx)
		if err != nil {
			return nil, err
		}
		state, err := reader.GetValue(session.OperationStateValue(*operationID), ctx)
		if err != nil {
			return nil, err
		}
		if meta == nil {
			return nil, &session.SessionInvariantError{Message: "Operation " + *operationID + " is missing op.meta"}
		}
		if state == nil {
			return nil, &session.SessionInvariantError{Message: "Operation " + *operationID + " is missing op.state"}
		}
		metaRecord, err := operationMetaFromValue(meta.Value)
		if err != nil {
			return nil, err
		}
		if metaRecord.OperationID != *operationID {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Operation %s metadata names operation %q", *operationID, metaRecord.OperationID)}
		}
		stateRecord, err := operationStateFromValue(state.Value)
		if err != nil {
			return nil, err
		}
		if !StateMatchesIntent(metaRecord, stateRecord) {
			return nil, &session.SessionInvariantError{Message: "Operation " + *operationID + " state does not match its intent"}
		}
		operation = &session.Operation{Meta: *metaRecord, State: *stateRecord}
	}
	tip := tipFromValue(stored.Tip.Value)
	return &RuntimeLaneState{
		TipID:           tip,
		Configuration:   laneConfigurationFromValue(stored.Configuration.Value),
		Inbox:           durableLaneState.Inbox,
		LastOperationID: durableLaneState.LastOperationID,
		Operation:       operation,
	}, nil
}

func durableLaneStateFromValue(v any) (state DurableLaneState) {
	if obj, ok := v.(*jsonx.Obj); ok {
		if current, ok := obj.Get("currentOperationId"); ok {
			if s, ok := current.(string); ok {
				state.CurrentOperationID = &s
			}
		}
		if last, ok := obj.Get("lastOperationId"); ok {
			if s, ok := last.(string); ok {
				state.LastOperationID = &s
			}
		}
		if inboxValue, ok := obj.Get("inbox"); ok {
			if inbox, ok := inboxValue.([]any); ok {
				for _, item := range inbox {
					if itemObj, ok := item.(*jsonx.Obj); ok {
						state.Inbox = append(state.Inbox, session.InboxItem{
							EntryID: stringOf(itemObj, "entryId"),
							Kind:    stringOf(itemObj, "kind"),
						})
					}
				}
			}
		}
	}
	return state
}

func tipFromValue(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func laneConfigurationFromValue(v any) (config session.LaneConfiguration) {
	if obj, ok := v.(*jsonx.Obj); ok {
		if modelValue, ok := obj.Get("model"); ok {
			if model, ok := modelValue.(*jsonx.Obj); ok {
				config.Model.Provider = stringOf(model, "provider")
				config.Model.ModelID = stringOf(model, "modelId")
			}
		}
		config.ThinkingLevel = stringOf(obj, "thinkingLevel")
		if toolsValue, ok := obj.Get("activeToolNames"); ok {
			if tools, ok := toolsValue.([]any); ok {
				for _, tool := range tools {
					if s, ok := tool.(string); ok {
						config.ActiveToolNames = append(config.ActiveToolNames, s)
					}
				}
			}
		}
	}
	return config
}

func operationMetaFromValue(v any) (*session.OperationMeta, error) {
	obj, ok := v.(*jsonx.Obj)
	if !ok {
		return nil, &session.SessionInvariantError{Message: "Operation metadata is not an object"}
	}
	meta := &session.OperationMeta{
		OperationID: stringOf(obj, "operationId"),
		Lane:        stringOf(obj, "lane"),
		StartedAt:   floatOf(obj, "startedAt"),
	}
	if sourceTip, ok := obj.Get("sourceTipId"); ok {
		if s, ok := sourceTip.(string); ok {
			meta.SourceTipID = &s
		}
	}
	if intentValue, ok := obj.Get("intent"); ok {
		if intent, ok := intentValue.(*jsonx.Obj); ok {
			meta.Intent = intent
		}
	}
	return meta, nil
}

func operationStateFromValue(v any) (*session.OperationState, error) {
	obj, ok := v.(*jsonx.Obj)
	if !ok {
		return nil, &session.SessionInvariantError{Message: "Operation state is not an object"}
	}
	state := &session.OperationState{At: stringOf(obj, "at")}
	if controlValue, ok := obj.Get("control"); ok {
		if control, ok := controlValue.(*jsonx.Obj); ok {
			state.Control.Status = stringOf(control, "status")
		}
	}
	if settings, ok := obj.Get("settings"); ok {
		if settingsObj, ok := settings.(*jsonx.Obj); ok {
			state.Settings = settingsObj
		}
	}
	if latest, ok := obj.Get("latestAssistantEntryId"); ok {
		if s, ok := latest.(string); ok {
			state.LatestAssistantEntryID = &s
		}
	}
	if taskValue, ok := obj.Get("task"); ok {
		if task, ok := taskValue.(*jsonx.Obj); ok {
			state.Task = task
		}
	}
	if batchValue, ok := obj.Get("batch"); ok {
		if batch, ok := batchValue.(*jsonx.Obj); ok {
			state.Batch = batch
		}
	}
	if target, ok := obj.Get("targetId"); ok {
		if s, ok := target.(string); ok {
			state.TargetID = &s
		}
	}
	if label, ok := obj.Get("label"); ok {
		if s, ok := label.(string); ok {
			state.Label = &s
		}
	}
	return state, nil
}

func stringOf(obj *jsonx.Obj, key string) string {
	if v, ok := obj.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func floatOf(obj *jsonx.Obj, key string) float64 {
	if v, ok := obj.Get(key); ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}
