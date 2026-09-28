package pico3

// view_projection.go ports the view.ts helper projections: input ids,
// generation status, private tool-state stripping, task describe, the
// touchesKey plugin detector, and the sync helpers that keep the tracked
// view state in minimal-flux order.

import (
	"github.com/gladmo/openagent/jsonx"
)

// InputIds reads the numeric inputs array off a task's input payload.
func InputIds(task *Task) []Id {
	inputObj, ok := task.Input.(*jsonx.Obj)
	if !ok {
		return nil
	}
	inputsValue, ok := inputObj.Get("inputs")
	if !ok {
		return nil
	}
	inputs, ok := inputsValue.([]any)
	if !ok {
		return nil
	}
	var out []Id
	for _, value := range inputs {
		if f, ok := value.(float64); ok {
			out = append(out, Id(f))
		}
	}
	return out
}

// GenerationStatus mirrors the TS union as a JSON object.
func GenerationStatus(task *Task, streaming bool) *jsonx.Obj {
	if task.Status == "pending" && len(task.After) > 0 {
		return jsonx.ObjFrom("stage", "waiting", "on", "compaction")
	}
	checkpoint := taskCheckpoint(task)
	phase := checkpointString(checkpoint, "phase")
	switch phase {
	case "requesting":
		stage := "requesting"
		if streaming {
			stage = "streaming"
		}
		return jsonx.ObjFrom("stage", stage, "attempt", checkpointNumber(checkpoint, "attempt", 1))
	case "retrying":
		return jsonx.ObjFrom(
			"stage", "retrying",
			"attempt", checkpointNumber(checkpoint, "attempt", 1),
			"retryAt", checkpointNumber(checkpoint, "untilMs", 0),
			"lastError", checkpointString(checkpoint, "lastError"),
		)
	case "deferred":
		return jsonx.ObjFrom(
			"stage", "deferred",
			"attempt", checkpointNumber(checkpoint, "attempt", 1),
			"pollAt", checkpointNumber(checkpoint, "pollAt", 0),
		)
	default:
		return jsonx.ObjFrom("stage", "preparing")
	}
}

func taskCheckpoint(task *Task) *jsonx.Obj {
	if task.Checkpoint == nil {
		return nil
	}
	return task.Checkpoint
}

func checkpointString(checkpoint *jsonx.Obj, key string) string {
	if checkpoint == nil {
		return ""
	}
	if v, ok := checkpoint.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func checkpointNumber(checkpoint *jsonx.Obj, key string, fallback float64) float64 {
	if checkpoint == nil {
		return fallback
	}
	if v, ok := checkpoint.Get(key); ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return fallback
}

// StripPrivateToolState clones a tool slot without its memos.
func StripPrivateToolState(slot *jsonx.Obj) *jsonx.Obj {
	if slot == nil {
		return nil
	}
	out := jsonx.NewObj()
	for _, key := range slot.Keys() {
		if key == "memos" {
			continue
		}
		value, _ := slot.Get(key)
		out.Set(key, cloneJSONValue(value))
	}
	return out
}

// DescribeKindFn is a kind's describe hook.
type DescribeKindFn func(task *Task, slot *jsonx.Obj) any

// DescribeTask mirrors describe: without a hook the phase view; with one,
// the task + public slot through the kind's describe, forced through
// strict JSON.
func DescribeTask(describe DescribeKindFn, task *Task, slot *jsonx.Obj) (any, error) {
	if describe == nil {
		phase := "running"
		if checkpoint := taskCheckpoint(task); checkpoint != nil {
			if p := checkpointString(checkpoint, "phase"); p != "" {
				phase = p
			}
		} else if task.Status == "pending" {
			phase = "pending"
		}
		return jsonx.ObjFrom("phase", phase), nil
	}
	var publicSlot *jsonx.Obj
	if slot != nil {
		publicSlot = StripPrivateToolState(slot)
	}
	value := describe(task, publicSlot)
	// strictJson round trip: non-JSON values error.
	encoded := jsonx.Stringify(value)
	parsed, err := jsonx.Parse(encoded)
	if err != nil {
		return nil, &TaskContractFault{Kind: task.Kind, Message: "task kind " + task.Kind + " describe returned a non-JSON value"}
	}
	return parsed, nil
}

// TouchesKey reports whether an op batch writes the session-level key
// (Replace touches everything).
func TouchesKey(ops []opAlias, key string) bool {
	for _, op := range ops {
		if opIsReplace(op) {
			return true
		}
		if len(opPath(op)) > 0 && opPath(op)[0] == chordPathString(key) {
			return true
		}
	}
	return false
}

// SyncOptional mirrors syncOptional: undefined deletes, equal values stay,
// records sync in place, others clone-assign.
func SyncOptional(target *jsonx.Obj, key string, next any) {
	if next == nil {
		target.Delete(key)
		return
	}
	current, has := target.Get(key)
	if has && jsonx.Stringify(current) == jsonx.Stringify(next) {
		return
	}
	if currentObj, ok := current.(*jsonx.Obj); ok {
		if nextObj, ok := next.(*jsonx.Obj); ok {
			SyncRecord(currentObj, nextObj)
			return
		}
	}
	target.Set(key, cloneJSONValue(next))
}

// SyncRecord mirrors syncRecord: keys absent from next delete; equal
// values stay; nested records recurse; the rest clone-assign.
func SyncRecord(target, next *jsonx.Obj) {
	for _, key := range target.Keys() {
		if _, ok := next.Get(key); !ok {
			target.Delete(key)
		}
	}
	for _, key := range next.Keys() {
		nextValue, _ := next.Get(key)
		current, has := target.Get(key)
		if has && jsonx.Stringify(current) == jsonx.Stringify(nextValue) {
			continue
		}
		if currentObj, ok := current.(*jsonx.Obj); ok {
			if nextObj, ok := nextValue.(*jsonx.Obj); ok {
				SyncRecord(currentObj, nextObj)
				continue
			}
		}
		target.Set(key, cloneJSONValue(nextValue))
	}
}

func chordPathString(key string) string { return key }
