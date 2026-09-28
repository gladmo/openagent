package kinds

// Ports of kinds/post-tools.ts decision behaviors.

import (
	"testing"

	"github.com/gladmo/openagent/agent/harness/pico3"
	"github.com/gladmo/openagent/jsonx"
)

func completedTask(entry int64, control *jsonx.Obj) *pico3.Task {
	result := jsonx.NewObj()
	result.Set("entry", float64(entry))
	if control != nil {
		result.Set("control", control)
	}
	outcome := jsonx.ObjFrom("status", "completed", "result", result)
	return &pico3.Task{Outcome: outcome}
}

func abortedTask(entry *int64) *pico3.Task {
	outcome := jsonx.NewObj()
	outcome.Set("status", "aborted")
	if entry != nil {
		outcome.Set("result", jsonx.ObjFrom("entry", float64(*entry)))
	} else {
		outcome.Set("result", jsonx.NewObj())
	}
	return &pico3.Task{Outcome: outcome}
}

func toolCallAt(id string) *jsonx.Obj {
	return jsonx.ObjFrom("type", "toolCall", "id", id, "name", "bash")
}

func TestResolveToolRows(t *testing.T) {
	entryA := int64(10)
	entryB := int64(20)
	calls := []*jsonx.Obj{toolCallAt("c1"), toolCallAt("c2"), toolCallAt("c3"), toolCallAt("c4")}
	tools := []*pico3.Task{
		completedTask(entryA, jsonx.ObjFrom("terminate", true)),
		abortedTask(&entryB),
		abortedTask(nil),
		nil, // no task at all
	}
	rows := ResolveToolRows(calls, tools)
	// Completed carries entry + control.
	if rows[0].Entry == nil || *rows[0].Entry != 10 || rows[0].Control == nil || rows[0].Missing != "" {
		t.Fatalf("row0 = %+v", rows[0])
	}
	// Aborted with entry: no missing.
	if rows[1].Entry == nil || *rows[1].Entry != 20 || rows[1].Missing != "" {
		t.Fatalf("row1 = %+v", rows[1])
	}
	// Aborted without entry: missing aborted.
	if rows[2].Entry != nil || rows[2].Missing != "aborted" {
		t.Fatalf("row2 = %+v", rows[2])
	}
	// Missing task: orphaned.
	if rows[3].Missing != "orphaned" {
		t.Fatalf("row3 = %+v", rows[3])
	}
	// Non-completed/non-aborted outcome: orphaned.
	pending := &pico3.Task{Outcome: jsonx.ObjFrom("status", "faulted", "error", "x")}
	rows = ResolveToolRows([]*jsonx.Obj{toolCallAt("c5")}, []*pico3.Task{pending})
	if rows[0].Missing != "orphaned" {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestAssistantToolCalls(t *testing.T) {
	message := jsonx.ObjFrom("content", []any{
		jsonx.ObjFrom("type", "text", "text", "hi"),
		jsonx.ObjFrom("type", "toolCall", "id", "c1"),
		jsonx.ObjFrom("type", "toolCall", "id", "c2"),
	})
	entry := &pico3.Entry{Model: []any{message}}
	calls := AssistantToolCalls(entry)
	if len(calls) != 2 || CallString(calls[0], "id") != "c1" {
		t.Fatalf("calls = %d", len(calls))
	}
	// No model / no content.
	if calls := AssistantToolCalls(&pico3.Entry{}); calls != nil {
		t.Fatal("empty entry produced calls")
	}
}

func TestMergeControl(t *testing.T) {
	entry := int64(1)
	rows := []ToolRow{
		{Call: toolCallAt("c1"), Control: jsonx.ObjFrom("addTools", []any{"read"})},
		{Call: toolCallAt("c2"), Control: jsonx.ObjFrom("addTools", []any{"read", "write"}, "terminate", true)},
		{Call: toolCallAt("c3"), Control: jsonx.ObjFrom("handoff", "next agent")},
		{Call: toolCallAt("c4"), Missing: "aborted"},
	}
	decision := MergeControl(rows, []string{"bash"})
	if len(decision.SelectedTools) != 3 || !containsString(decision.SelectedTools, "read") || !containsString(decision.SelectedTools, "write") {
		t.Fatalf("tools = %v", decision.SelectedTools)
	}
	if !decision.Changed {
		t.Fatal("change not detected")
	}
	if !decision.Terminate || !decision.HasHandoff || decision.Handoff != "next agent" {
		t.Fatalf("decision = %+v", decision)
	}
	if len(decision.Missing) != 1 || decision.Missing[0].Missing != "aborted" {
		t.Fatal("missing not collected")
	}
	// No change: Changed false.
	decision = MergeControl([]ToolRow{{Call: toolCallAt("c"), Entry: &entry}}, []string{"bash"})
	if decision.Changed {
		t.Fatal("false change")
	}
}

func TestSynthesizedResult(t *testing.T) {
	result := SynthesizedResult(toolCallAt("c9"), "orphaned", 1234)
	if result.MustGet("toolCallId") != "c9" || result.MustGet("toolName") != "bash" {
		t.Fatalf("result = %v", result)
	}
	if result.MustGet("isError") != true || result.MustGet("timestamp") != float64(1234) {
		t.Fatal("flags")
	}
	text := result.MustGet("content").([]any)[0].(*jsonx.Obj).MustGet("text").(string)
	if text != "Tool result unavailable: task orphaned." {
		t.Fatalf("text = %q", text)
	}
}

func TestDecideBoundary(t *testing.T) {
	inputs := []int64{1, 2}
	triggers := []int64{3}

	// Handoff with triggers: handoff + successor.
	outcome := DecideBoundary(true, false, false, inputs, triggers)
	if outcome.Kind != "handoff" || !outcome.HasSuccessor || len(outcome.SuccessorInputs) != 1 || outcome.SuccessorInputs[0] != 3 {
		t.Fatalf("outcome = %+v", outcome)
	}
	// Terminate without triggers: terminate, no successor.
	outcome = DecideBoundary(false, true, false, inputs, nil)
	if outcome.Kind != "terminate" || outcome.HasSuccessor {
		t.Fatalf("outcome = %+v", outcome)
	}
	// Terminated: unanswered resolution, successor on triggers.
	outcome = DecideBoundary(false, false, true, inputs, triggers)
	if outcome.Kind != "terminated" || !outcome.HasSuccessor {
		t.Fatalf("outcome = %+v", outcome)
	}
	// Continue: inputs + triggers chained.
	outcome = DecideBoundary(false, false, false, inputs, triggers)
	if outcome.Kind != "continue" || len(outcome.SuccessorInputs) != 3 || outcome.SuccessorInputs[0] != 1 || outcome.SuccessorInputs[2] != 3 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestAbortResolution(t *testing.T) {
	resolution := AbortResolution([]int64{5})
	if resolution["status"] != "unanswered" || resolution["reason"] != "aborted" {
		t.Fatalf("resolution = %v", resolution)
	}
}

func TestPostToolsConfigDefaults(t *testing.T) {
	defaults := PostToolsConfigDefaults()
	if defaults["steeringMode"] != "one-at-a-time" || defaults["followUpMode"] != "one-at-a-time" {
		t.Fatalf("defaults = %v", defaults)
	}
}
