package pico3

// view_build.go ports view.ts's build/update projections: the turn view
// (generation/post-tools inputs + streaming message + public tools), the
// compaction view, and the tasks view (turn kinds excluded, private slots
// stripped, kind describe applied). These ride on the docs/kinds ports.

import (
	"github.com/gladmo/openagent/jsonx"
)

// Turn kind names.
const (
	KindGeneration = "pi.generation"
	KindPostTools  = "pi.post_tools"
	KindCollapse   = "pi.collapse"
)

// KindViewInfo is the view-relevant kind metadata.
type KindViewInfo struct {
	Name     string
	Turn     bool
	Describe DescribeKindFn
}

// ViewKinds resolves kind metadata by name (the session's kinds registry).
type ViewKinds func(kind string) *KindViewInfo

// BuildTurnView mirrors the turn projection: no turn tasks -> absent;
// otherwise inputs from generation (or post-tools), generation status when
// present, the streaming message when present, and public tool slots.
func BuildTurnView(kinds ViewKinds, liveTasks []*Task, conversationID Id, sticky *jsonx.Obj) *jsonx.Obj {
	var turnTasks []*Task
	for _, task := range liveTasks {
		if task.ConversationID != conversationID {
			continue
		}
		info := kinds(task.Kind)
		if info != nil && info.Turn {
			turnTasks = append(turnTasks, task)
		}
	}
	if len(turnTasks) == 0 {
		return nil
	}
	var generation, postTools *Task
	for _, task := range turnTasks {
		switch task.Kind {
		case KindGeneration:
			generation = task
		case KindPostTools:
			postTools = task
		}
	}
	inputTask := generation
	if inputTask == nil {
		inputTask = postTools
	}
	turn := jsonx.NewObj()
	inputs := []any{}
	if inputTask != nil {
		for _, id := range InputIds(inputTask) {
			inputs = append(inputs, float64(id))
		}
	}
	turn.Set("inputs", inputs)
	turnObj := stickyTurn(sticky)
	messageValue, hasMessage := turnObj.Get("message")
	streaming := turnObj != nil && hasMessage && messageValue != nil
	if generation != nil {
		turn.Set("generation", GenerationStatus(generation, streaming))
	}
	if turnObj != nil {
		if message, ok := turnObj.Get("message"); ok && message != nil {
			turn.Set("message", cloneJSONValue(message))
		}
	}
	tools := []any{}
	if turnObj != nil {
		if toolsValue, ok := turnObj.Get("tools"); ok {
			if arr, ok := toolsValue.([]any); ok {
				for _, slot := range arr {
					if slotObj, ok := slot.(*jsonx.Obj); ok {
						tools = append(tools, StripPrivateToolState(slotObj))
					}
				}
			}
		}
	}
	turn.Set("tools", tools)
	return turn
}

func stickyTurn(sticky *jsonx.Obj) *jsonx.Obj {
	if sticky == nil {
		return nil
	}
	if v, ok := sticky.Get(KeyTurn); ok {
		if obj, ok := v.(*jsonx.Obj); ok {
			return obj
		}
	}
	return nil
}

// BuildCompactionView mirrors the compaction projection: the live
// pi.collapse task renders {taskId, reason, stage (retrying vs
// summarizing), attempt, retryAt?}.
func BuildCompactionView(liveTasks []*Task, conversationID Id) *jsonx.Obj {
	for _, task := range liveTasks {
		if task.ConversationID != conversationID || task.Kind != KindCollapse {
			continue
		}
		view := jsonx.NewObj()
		view.Set("taskId", float64(task.ID))
		reason := ""
		if inputObj, ok := task.Input.(*jsonx.Obj); ok {
			if v, ok := inputObj.Get("reason"); ok {
				if s, ok := v.(string); ok {
					reason = s
				}
			}
		}
		view.Set("reason", reason)
		stage := "summarizing"
		attempt := float64(1)
		var retryAt any
		if checkpoint := taskCheckpoint(task); checkpoint != nil {
			phase := checkpointString(checkpoint, "phase")
			if phase == "retrying" {
				stage = "retrying"
			}
			attempt = checkpointNumber(checkpoint, "attempt", 1)
			if phase == "retrying" {
				if v, ok := checkpoint.Get("untilMs"); ok && v != nil {
					retryAt = v
				}
			}
		}
		view.Set("stage", stage)
		view.Set("attempt", attempt)
		if retryAt != nil {
			view.Set("retryAt", retryAt)
		}
		return view
	}
	return nil
}

// BuildTasksView mirrors the tasks projection: live tasks of this
// conversation excluding turn kinds and pi.collapse; each renders kind,
// optional background/marked flags, and the describe value over the
// task + its public sticky slot.
func BuildTasksView(kinds ViewKinds, liveTasks []*Task, conversationID Id, sticky *jsonx.Obj) *jsonx.Obj {
	out := jsonx.NewObj()
	slots := stickyTaskSlots(sticky)
	for _, task := range liveTasks {
		if task.ConversationID != conversationID {
			continue
		}
		info := kinds(task.Kind)
		if info == nil || info.Turn || task.Kind == KindCollapse {
			continue
		}
		entry := jsonx.NewObj()
		entry.Set("kind", task.Kind)
		if task.Background {
			entry.Set("background", true)
		}
		if task.Abort {
			entry.Set("marked", true)
		}
		var slot *jsonx.Obj
		if slots != nil {
			if v, ok := slots.Get(idKey(task.ID)); ok {
				if obj, ok := v.(*jsonx.Obj); ok {
					slot = obj
				}
			}
		}
		described, err := DescribeTask(info.Describe, task, slot)
		if err != nil {
			continue
		}
		entry.Set("status", described)
		out.Set(idKey(task.ID), entry)
	}
	return out
}

func stickyTaskSlots(sticky *jsonx.Obj) *jsonx.Obj {
	if sticky == nil {
		return nil
	}
	if v, ok := sticky.Get(KeyTasks); ok {
		if obj, ok := v.(*jsonx.Obj); ok {
			return obj
		}
	}
	return nil
}

func idKey(id Id) string {
	return jsonx.Stringify(float64(id))
}
