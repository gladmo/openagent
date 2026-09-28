package pico3

// Ports of view_build projection behaviors.

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func testKinds() ViewKinds {
	return func(kind string) *KindViewInfo {
		switch kind {
		case KindGeneration:
			return &KindViewInfo{Name: kind, Turn: true}
		case KindPostTools:
			return &KindViewInfo{Name: kind, Turn: true}
		case KindCollapse:
			return &KindViewInfo{Name: kind}
		case "app.job":
			return &KindViewInfo{Name: kind, Describe: func(task *Task, slot *jsonx.Obj) any {
				out := jsonx.NewObj()
				out.Set("phase", "work")
				if slot != nil {
					out.Set("slotSeen", true)
				}
				return out
			}}
		default:
			return nil
		}
	}
}

func stickyWithTurn(message any, tools ...*jsonx.Obj) *jsonx.Obj {
	sticky := NewStickyState()
	turn := sticky.MustGet(KeyTurn).(*jsonx.Obj)
	if message != nil {
		turn.Set("message", message)
	}
	toolArr := make([]any, 0, len(tools))
	for _, tool := range tools {
		toolArr = append(toolArr, tool)
	}
	turn.Set("tools", toolArr)
	return sticky
}

func TestBuildTurnViewAbsentWithoutTurnTasks(t *testing.T) {
	live := []*Task{{ID: 1, ConversationID: 5, Kind: "app.job", Status: "running"}}
	if turn := BuildTurnView(testKinds(), live, 5, NewStickyState()); turn != nil {
		t.Fatal("turn built without turn tasks")
	}
}

func TestBuildTurnViewGeneration(t *testing.T) {
	live := []*Task{
		{ID: 1, ConversationID: 5, Kind: KindGeneration, Status: "running",
			Input:      jsonx.ObjFrom("inputs", []any{float64(10), float64(11)}),
			Checkpoint: jsonx.ObjFrom("phase", "requesting", "attempt", float64(2))},
	}
	message := jsonx.ObjFrom("role", "assistant", "content", []any{})
	tool := jsonx.ObjFrom("callId", "c1", "status", "running", "memos", jsonx.ObjFrom("x", 1))
	sticky := stickyWithTurn(message, tool)

	turn := BuildTurnView(testKinds(), live, 5, sticky)
	inputs := turn.MustGet("inputs").([]any)
	if len(inputs) != 2 || inputs[0] != float64(10) {
		t.Fatalf("inputs = %v", inputs)
	}
	// requesting + streaming message -> streaming stage.
	generation := turn.MustGet("generation").(*jsonx.Obj)
	if generation.MustGet("stage") != "streaming" {
		t.Fatalf("generation = %v", generation)
	}
	if turn.MustGet("message") == nil {
		t.Fatal("message missing")
	}
	tools := turn.MustGet("tools").([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %d", len(tools))
	}
	if _, has := tools[0].(*jsonx.Obj).Get("memos"); has {
		t.Fatal("memos leaked")
	}
}

func TestBuildTurnViewPostToolsFallback(t *testing.T) {
	// No generation: post_tools supplies inputs; no generation key.
	live := []*Task{
		{ID: 2, ConversationID: 5, Kind: KindPostTools, Status: "running",
			Input: jsonx.ObjFrom("inputs", []any{float64(7)})},
	}
	turn := BuildTurnView(testKinds(), live, 5, stickyWithTurn(nil))
	inputs := turn.MustGet("inputs").([]any)
	if len(inputs) != 1 || inputs[0] != float64(7) {
		t.Fatalf("inputs = %v", inputs)
	}
	if _, has := turn.Get("generation"); has {
		t.Fatal("generation key present without generation task")
	}
	if _, has := turn.Get("message"); has {
		t.Fatal("message key present without streaming message")
	}
	// Streaming false without a message reflects in requesting.
	live2 := []*Task{
		{ID: 2, ConversationID: 5, Kind: KindGeneration, Status: "running",
			Input:      jsonx.ObjFrom("inputs", []any{}),
			Checkpoint: jsonx.ObjFrom("phase", "requesting", "attempt", float64(2))},
	}
	turn = BuildTurnView(testKinds(), live2, 5, stickyWithTurn(nil))
	if turn.MustGet("generation").(*jsonx.Obj).MustGet("stage") != "requesting" {
		t.Fatal("not requesting without message")
	}
}

func TestBuildCompactionView(t *testing.T) {
	live := []*Task{}
	if view := BuildCompactionView(live, 5); view != nil {
		t.Fatal("compaction without task")
	}
	// Summarizing default.
	live = []*Task{{ID: 9, ConversationID: 5, Kind: KindCollapse, Status: "running",
		Input: jsonx.ObjFrom("reason", "threshold")}}
	view := BuildCompactionView(live, 5)
	if view.MustGet("taskId") != float64(9) || view.MustGet("reason") != "threshold" {
		t.Fatalf("view = %v", view)
	}
	if view.MustGet("stage") != "summarizing" || view.MustGet("attempt") != float64(1) {
		t.Fatalf("view = %v", view)
	}
	// Retrying carries retryAt.
	live[0].Checkpoint = jsonx.ObjFrom("phase", "retrying", "attempt", float64(3), "untilMs", float64(77))
	view = BuildCompactionView(live, 5)
	if view.MustGet("stage") != "retrying" || view.MustGet("attempt") != float64(3) || view.MustGet("retryAt") != float64(77) {
		t.Fatalf("view = %v", view)
	}
	// Different conversation excluded.
	if view := BuildCompactionView(live, 6); view != nil {
		t.Fatal("cross-conversation matched")
	}
}

func TestBuildTasksView(t *testing.T) {
	sticky := NewStickyState()
	slots := sticky.MustGet(KeyTasks).(*jsonx.Obj)
	publicSlot := jsonx.ObjFrom("status", "running", "memos", jsonx.ObjFrom("secret", 1))
	slots.Set("1", publicSlot)

	live := []*Task{
		// Included: plain job with a slot.
		{ID: 1, ConversationID: 5, Kind: "app.job", Status: "running"},
		// Excluded: turn kind.
		{ID: 2, ConversationID: 5, Kind: KindGeneration, Status: "running"},
		// Excluded: collapse kind.
		{ID: 3, ConversationID: 5, Kind: KindCollapse, Status: "running"},
		// Excluded: other conversation.
		{ID: 4, ConversationID: 6, Kind: "app.job", Status: "running"},
		// Included: marked background job without a slot.
		{ID: 5, ConversationID: 5, Kind: "app.job", Status: "running", Background: true, Abort: true},
	}
	out := BuildTasksView(testKinds(), live, 5, sticky)
	if len(out.Keys()) != 2 {
		t.Fatalf("tasks = %v", out.Keys())
	}
	first := out.MustGet("1").(*jsonx.Obj)
	status := first.MustGet("status").(*jsonx.Obj)
	if status.MustGet("phase") != "work" || status.MustGet("slotSeen") != true {
		t.Fatalf("status = %v", status)
	}
	fifth := out.MustGet("5").(*jsonx.Obj)
	if fifth.MustGet("background") != true || fifth.MustGet("marked") != true {
		t.Fatalf("fifth = %v", fifth)
	}
	// Unknown kind excluded.
	live = append(live, &Task{ID: 6, ConversationID: 5, Kind: "unknown.kind", Status: "running"})
	out = BuildTasksView(testKinds(), live, 5, sticky)
	if len(out.Keys()) != 2 {
		t.Fatal("unknown kind included")
	}
}
