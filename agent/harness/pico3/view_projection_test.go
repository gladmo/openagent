package pico3

// Ports of view.ts projection helper behaviors.

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func TestInputIds(t *testing.T) {
	task := &Task{Input: jsonx.ObjFrom("inputs", []any{float64(1), "junk", float64(5)})}
	ids := InputIds(task)
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 5 {
		t.Fatalf("ids = %v", ids)
	}
	// Missing inputs.
	if ids := InputIds(&Task{Input: jsonx.NewObj()}); ids != nil {
		t.Fatalf("ids = %v", ids)
	}
}

func generationTask(status string, after []Id, checkpoint *jsonx.Obj) *Task {
	task := &Task{Status: status, After: after, Checkpoint: checkpoint}
	return task
}

func TestGenerationStatus(t *testing.T) {
	// Pending with deps waits on compaction.
	status := GenerationStatus(generationTask("pending", []Id{7}, nil), false)
	if status.MustGet("stage") != "waiting" || status.MustGet("on") != "compaction" {
		t.Fatalf("status = %v", status)
	}
	// Preparing by default.
	status = GenerationStatus(generationTask("running", nil, nil), false)
	if status.MustGet("stage") != "preparing" {
		t.Fatalf("status = %v", status)
	}
	// Requesting: streaming distinguishes.
	checkpoint := jsonx.ObjFrom("phase", "requesting", "attempt", float64(2))
	status = GenerationStatus(generationTask("running", nil, checkpoint), false)
	if status.MustGet("stage") != "requesting" || status.MustGet("attempt") != float64(2) {
		t.Fatalf("status = %v", status)
	}
	status = GenerationStatus(generationTask("running", nil, checkpoint), true)
	if status.MustGet("stage") != "streaming" {
		t.Fatalf("status = %v", status)
	}
	// Retrying carries retryAt + lastError.
	checkpoint = jsonx.ObjFrom("phase", "retrying", "attempt", float64(3), "untilMs", float64(99), "lastError", "boom")
	status = GenerationStatus(generationTask("running", nil, checkpoint), false)
	if status.MustGet("stage") != "retrying" || status.MustGet("retryAt") != float64(99) || status.MustGet("lastError") != "boom" {
		t.Fatalf("status = %v", status)
	}
	// Deferred carries pollAt.
	checkpoint = jsonx.ObjFrom("phase", "deferred", "pollAt", float64(42))
	status = GenerationStatus(generationTask("running", nil, checkpoint), false)
	if status.MustGet("stage") != "deferred" || status.MustGet("pollAt") != float64(42) {
		t.Fatalf("status = %v", status)
	}
	// Defaults when fields absent.
	checkpoint = jsonx.ObjFrom("phase", "requesting")
	status = GenerationStatus(generationTask("running", nil, checkpoint), false)
	if status.MustGet("attempt") != float64(1) {
		t.Fatalf("attempt default = %v", status.MustGet("attempt"))
	}
}

func TestStripPrivateToolState(t *testing.T) {
	slot := jsonx.ObjFrom(
		"callId", "c1",
		"name", "bash",
		"status", "running",
		"memos", jsonx.ObjFrom("private", true),
		"output", "text",
	)
	stripped := StripPrivateToolState(slot)
	if _, has := stripped.Get("memos"); has {
		t.Fatal("memos leaked")
	}
	if stripped.MustGet("callId") != "c1" || stripped.MustGet("output") != "text" {
		t.Fatalf("stripped = %v", stripped)
	}
	// Source untouched.
	if _, has := slot.Get("memos"); !has {
		t.Fatal("source mutated")
	}
}

func TestDescribeTaskWithoutHook(t *testing.T) {
	// Pending -> "pending".
	value, err := DescribeTask(nil, generationTask("pending", nil, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if value.(*jsonx.Obj).MustGet("phase") != "pending" {
		t.Fatalf("value = %v", value)
	}
	// Running without checkpoint -> "running".
	value, _ = DescribeTask(nil, generationTask("running", nil, nil), nil)
	if value.(*jsonx.Obj).MustGet("phase") != "running" {
		t.Fatalf("value = %v", value)
	}
	// Checkpoint phase wins.
	value, _ = DescribeTask(nil, generationTask("running", nil, jsonx.ObjFrom("phase", "work")), nil)
	if value.(*jsonx.Obj).MustGet("phase") != "work" {
		t.Fatalf("value = %v", value)
	}
}

func TestDescribeTaskWithHook(t *testing.T) {
	hook := func(task *Task, slot *jsonx.Obj) any {
		out := jsonx.NewObj()
		out.Set("kindPhase", "custom")
		if slot != nil {
			out.Set("slotStatus", slot.MustGet("status"))
		}
		return out
	}
	// The slot's memos are stripped before the hook sees it.
	slot := jsonx.ObjFrom("status", "running", "memos", jsonx.ObjFrom("secret", true))
	value, err := DescribeTask(hook, generationTask("running", nil, nil), slot)
	if err != nil {
		t.Fatal(err)
	}
	obj := value.(*jsonx.Obj)
	if obj.MustGet("kindPhase") != "custom" || obj.MustGet("slotStatus") != "running" {
		t.Fatalf("value = %v", obj)
	}
}

func TestTouchesKey(t *testing.T) {
	// Replace touches everything.
	if !TouchesKey([]opAlias{&replaceOpAlias{Value: jsonx.NewObj()}}, "plugins") {
		t.Fatal("replace missed")
	}
	// Path-rooted set touches only its root.
	if !TouchesKey([]opAlias{&setOpAlias{Path: pathOf("plugins", "x"), Value: float64(1)}}, "plugins") {
		t.Fatal("plugins set missed")
	}
	if TouchesKey([]opAlias{&setOpAlias{Path: pathOf("other", "x"), Value: float64(1)}}, "plugins") {
		t.Fatal("foreign root touched")
	}
}

func TestSyncOptionalAndRecord(t *testing.T) {
	target := jsonx.ObjFrom("a", float64(1), "b", float64(2), "nested", jsonx.ObjFrom("x", float64(1)))

	// Equal value: no flux (same content).
	SyncOptional(target, "a", float64(1))
	if target.MustGet("a") != float64(1) {
		t.Fatal("equal value changed")
	}
	// Nil deletes.
	SyncOptional(target, "b", nil)
	if _, has := target.Get("b"); has {
		t.Fatal("nil did not delete")
	}
	// Record syncs in place.
	SyncOptional(target, "nested", jsonx.ObjFrom("y", float64(2)))
	nested := target.MustGet("nested").(*jsonx.Obj)
	if _, has := nested.Get("x"); has {
		t.Fatal("stale key survived")
	}
	if nested.MustGet("y") != float64(2) {
		t.Fatalf("nested = %v", nested)
	}
	// Scalar replaces.
	SyncOptional(target, "a", "text")
	if target.MustGet("a") != "text" {
		t.Fatal("scalar replace failed")
	}
}
