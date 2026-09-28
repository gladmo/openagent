package kinds

// post_tools.go ports harness/pico3/kinds/post-tools.ts: the pi.post_tools
// kind's decision core — collecting tool rows (completed/aborted/orphaned),
// merging ToolControl (addTools/terminate/handoff), synthesizing missing
// tool results, and the three-way boundary outcome (handoff/terminate vs
// terminated vs continue).

import (
	"github.com/gladmo/openagent/agent/harness/pico3"
	"github.com/gladmo/openagent/jsonx"
)

// PostToolsKindName identifies the kind.
const PostToolsKindName = "pi.post_tools"

// PostToolsConfigDefaults mirrors postToolsConfig.
func PostToolsConfigDefaults() map[string]any {
	return map[string]any{
		"steeringMode": "one-at-a-time",
		"followUpMode": "one-at-a-time",
	}
}

// ToolRow is one tool call's resolved state.
type ToolRow struct {
	Call    *jsonx.Obj
	Entry   *int64
	Control *jsonx.Obj
	Missing string // "" | orphaned | aborted
}

// ResolveToolRows mirrors the initial phase's row collection: for each
// tool id paired with its call (by index), read the task outcome —
// completed carries {entry, control}; aborted carries entry and marks
// missing=aborted only when entryless; anything else is orphaned.
func ResolveToolRows(
	calls []*jsonx.Obj,
	toolTasks []*pico3.Task,
) []ToolRow {
	rows := make([]ToolRow, 0, len(toolTasks))
	for index, task := range toolTasks {
		var call *jsonx.Obj
		if index < len(calls) {
			call = calls[index]
		}
		row := ToolRow{Call: call}
		if task == nil || task.Outcome == nil {
			row.Missing = "orphaned"
			rows = append(rows, row)
			continue
		}
		status, _ := task.Outcome.Get("status")
		switch status {
		case "completed":
			if result, ok := task.Outcome.Get("result"); ok {
				if resultObj, ok := result.(*jsonx.Obj); ok {
					if v, ok := resultObj.Get("entry"); ok {
						if f, ok := v.(float64); ok {
							id := int64(f)
							row.Entry = &id
						}
					}
					if v, ok := resultObj.Get("control"); ok {
						if controlObj, ok := v.(*jsonx.Obj); ok {
							row.Control = controlObj
						}
					}
				}
			}
		case "aborted":
			if result, ok := task.Outcome.Get("result"); ok {
				if resultObj, ok := result.(*jsonx.Obj); ok {
					if v, ok := resultObj.Get("entry"); ok {
						if f, ok := v.(float64); ok {
							id := int64(f)
							row.Entry = &id
						}
					}
				}
			}
			if row.Entry == nil {
				row.Missing = "aborted"
			}
		default:
			row.Missing = "orphaned"
		}
		rows = append(rows, row)
	}
	return rows
}

// AssistantToolCalls extracts the toolCall blocks off an assistant entry's
// first model message.
func AssistantToolCalls(assistant *pico3.Entry) []*jsonx.Obj {
	if assistant == nil || len(assistant.Model) == 0 {
		return nil
	}
	message, ok := assistant.Model[0].(*jsonx.Obj)
	if !ok {
		return nil
	}
	contentValue, ok := message.Get("content")
	if !ok {
		return nil
	}
	content, ok := contentValue.([]any)
	if !ok {
		return nil
	}
	var calls []*jsonx.Obj
	for _, item := range content {
		if obj, ok := item.(*jsonx.Obj); ok {
			if t, _ := obj.Get("type"); t == "toolCall" {
				calls = append(calls, obj)
			}
		}
	}
	return calls
}

// ControlDecision merges the rows' ToolControl effects.
type ControlDecision struct {
	SelectedTools []string
	Changed       bool
	Terminate     bool
	Handoff       string
	HasHandoff    bool
	Missing       []ToolRow
}

// MergeControl mirrors the control merge: addTools appends dedup,
// terminate ORs, handoff last-wins; selectedTools change is tracked.
func MergeControl(rows []ToolRow, selectedTools []string) *ControlDecision {
	decision := &ControlDecision{SelectedTools: append([]string{}, selectedTools...)}
	for _, row := range rows {
		if row.Control != nil {
			if v, ok := row.Control.Get("addTools"); ok {
				if arr, ok := v.([]any); ok {
					for _, item := range arr {
						if name, ok := item.(string); ok {
							if !containsString(decision.SelectedTools, name) {
								decision.SelectedTools = append(decision.SelectedTools, name)
							}
						}
					}
				}
			}
			if v, ok := row.Control.Get("terminate"); ok && v == true {
				decision.Terminate = true
			}
			if v, ok := row.Control.Get("handoff"); ok {
				if s, ok := v.(string); ok {
					decision.Handoff = s
					decision.HasHandoff = true
				}
			}
		}
		if row.Missing != "" {
			decision.Missing = append(decision.Missing, row)
		}
	}
	decision.Changed = len(decision.SelectedTools) != len(selectedTools)
	return decision
}

// SynthesizedResult mirrors the missing tool result synthesis.
func SynthesizedResult(call *jsonx.Obj, missing string, timestamp float64) *jsonx.Obj {
	result := jsonx.NewObj()
	result.Set("role", "toolResult")
	result.Set("toolCallId", CallString(call, "id"))
	result.Set("toolName", CallString(call, "name"))
	result.Set("content", []any{jsonx.ObjFrom(
		"type", "text",
		"text", "Tool result unavailable: task "+missing+".",
	)})
	result.Set("isError", true)
	result.Set("timestamp", timestamp)
	return result
}

// BoundaryOutcome mirrors the three-way done decision.
type BoundaryOutcome struct {
	Kind            string // "handoff" | "terminate" | "terminated" | "continue"
	HasSuccessor    bool
	SuccessorInputs []int64
}

// DecideBoundary mirrors the boundary outcomes: handoff/terminate resolve
// inputs done and carry a successor when triggers exist; terminated
// resolves unanswered; otherwise continue chains inputs+triggers.
func DecideBoundary(hasHandoff, terminate, terminated bool, inputs, triggers []int64) *BoundaryOutcome {
	switch {
	case hasHandoff || terminate:
		ended := "terminate"
		if hasHandoff {
			ended = "handoff"
		}
		outcome := &BoundaryOutcome{Kind: ended}
		if len(triggers) > 0 {
			outcome.HasSuccessor = true
			outcome.SuccessorInputs = triggers
		}
		return outcome
	case terminated:
		outcome := &BoundaryOutcome{Kind: "terminated"}
		if len(triggers) > 0 {
			outcome.HasSuccessor = true
			outcome.SuccessorInputs = triggers
		}
		return outcome
	default:
		merged := make([]int64, 0, len(inputs)+len(triggers))
		merged = append(merged, inputs...)
		merged = append(merged, triggers...)
		return &BoundaryOutcome{Kind: "continue", HasSuccessor: true, SuccessorInputs: merged}
	}
}

// AbortResolution mirrors the abort closure: turn cleared, inputs
// unanswered with reason aborted.
func AbortResolution(inputs []int64) map[string]any {
	return map[string]any{
		"status": "unanswered",
		"reason": "aborted",
		"inputs": inputs,
	}
}
