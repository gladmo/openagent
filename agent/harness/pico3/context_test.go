package pico3

// Ports of context.ts deriveContext + reorder and hooks.ts scoping.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func assistantMessage(calls ...[2]string) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("role", "assistant")
	obj.Set("timestamp", float64(1))
	content := make([]any, 0, len(calls))
	for _, call := range calls {
		callObj := jsonx.NewObj()
		callObj.Set("type", "toolCall")
		callObj.Set("id", call[0])
		callObj.Set("name", call[1])
		content = append(content, callObj)
	}
	obj.Set("content", content)
	return obj
}

func toolResultMessage(callID string) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("role", "toolResult")
	obj.Set("toolCallId", callID)
	obj.Set("timestamp", float64(2))
	return obj
}

func TestReorderToolResultsCallOrder(t *testing.T) {
	assistant := assistantMessage([2]string{"c1", "read"}, [2]string{"c2", "bash"})
	messages := []any{
		assistant,
		toolResultMessage("c2"), // finished first
		toolResultMessage("c1"),
	}
	reordered := ReorderToolResults(messages)
	if len(reordered) != 3 {
		t.Fatalf("len = %d", len(reordered))
	}
	if reordered[1].(*jsonx.Obj).MustGet("toolCallId") != "c1" {
		t.Fatal("c1 not first")
	}
	if reordered[2].(*jsonx.Obj).MustGet("toolCallId") != "c2" {
		t.Fatal("c2 not second")
	}
}

func TestReorderToolResultsSynthesizesMissing(t *testing.T) {
	assistant := assistantMessage([2]string{"c1", "read"}, [2]string{"c2", "bash"})
	messages := []any{
		assistant,
		toolResultMessage("c1"), // c2 lost to a fork cut
	}
	reordered := ReorderToolResults(messages)
	if len(reordered) != 3 {
		t.Fatalf("len = %d", len(reordered))
	}
	synthesized := reordered[2].(*jsonx.Obj)
	if synthesized.MustGet("toolCallId") != "c2" || synthesized.MustGet("toolName") != "bash" {
		t.Fatalf("synth = %v", synthesized)
	}
	if synthesized.MustGet("isError") != true {
		t.Fatal("synth not error")
	}
	details := synthesized.MustGet("details").(*jsonx.Obj)
	if details.MustGet("reason") != "missing_after_fork" {
		t.Fatalf("details = %v", details)
	}
	if !strings.Contains(synthesized.MustGet("content").([]any)[0].(*jsonx.Obj).MustGet("text").(string), "unavailable") {
		t.Fatal("synth text")
	}
}

func TestReorderToolResultsNoCalls(t *testing.T) {
	text := jsonx.ObjFrom("role", "user", "content", "hi")
	assistant := jsonx.ObjFrom("role", "assistant", "content", []any{}, "timestamp", float64(1))
	reordered := ReorderToolResults([]any{text, assistant, toolResultMessage("x")})
	if len(reordered) != 3 {
		t.Fatalf("len = %d", len(reordered))
	}
	// A stray tool result after a no-call assistant stays put.
	if reordered[2].(*jsonx.Obj).MustGet("toolCallId") != "x" {
		t.Fatal("stray result moved")
	}
}

// --- hooks ---

func hookKind(name string) *KindPhases {
	return &KindPhases{Name: name, Phases: map[string]Handler{}}
}

func TestHookScoping(t *testing.T) {
	kind := hookKind("app.kind")
	other := hookKind("app.other")
	conv1 := Id(1)
	var reported []error
	registrations := []HookRegistration{
		{Kind: kind, Namespace: HookNamespace{ID: "global"}, Handlers: HookHandlers{"onX": "g"}},
		{Kind: kind, Namespace: HookNamespace{ID: "conv1"}, Handlers: HookHandlers{"onX": "c1"}, ConversationID: &conv1},
		{Kind: other, Namespace: HookNamespace{ID: "wrong-kind"}, Handlers: HookHandlers{}},
	}
	factory := CreateHookRunners(
		func() []HookRegistration { return registrations },
		func(Id) []Id { return nil },
		func(err error) { reported = append(reported, err) },
	)
	runner := factory(kind, HookInfo{ConversationID: 2})
	bindings := runner.Bindings()
	if len(bindings) != 1 || bindings[0].Namespace.ID != "global" {
		t.Fatalf("bindings = %d", len(bindings))
	}
	api := bindings[0].API
	if api.MustGet("conversationId") != float64(2) || api.MustGet("kind") != "app.kind" {
		t.Fatalf("api = %v", api)
	}
	// Conversation 1 sees both.
	runner = factory(kind, HookInfo{ConversationID: 1})
	if len(runner.Bindings()) != 2 {
		t.Fatalf("bindings = %d", len(runner.Bindings()))
	}
}

func TestHookSubtreeScoping(t *testing.T) {
	kind := hookKind("app.kind")
	root := Id(1)
	leaf := Id(9)
	registrations := []HookRegistration{
		{Kind: kind, Namespace: HookNamespace{ID: "root-subtree"}, Handlers: HookHandlers{}, ConversationID: &root, Subtree: true},
	}
	factory := CreateHookRunners(
		func() []HookRegistration { return registrations },
		func(conversationID Id) []Id {
			if conversationID == leaf {
				return []Id{Id(5), root}
			}
			return nil
		},
		nil,
	)
	// The leaf inherits the root registration through its ancestors.
	runner := factory(kind, HookInfo{ConversationID: leaf})
	if len(runner.Bindings()) != 1 {
		t.Fatalf("bindings = %d", len(runner.Bindings()))
	}
	// A conversation outside the subtree does not.
	runner = factory(kind, HookInfo{ConversationID: 2})
	if len(runner.Bindings()) != 0 {
		t.Fatal("outside subtree matched")
	}
}

func TestHookEachErrorIsolation(t *testing.T) {
	kind := hookKind("app.kind")
	var reported []error
	registrations := []HookRegistration{
		{Kind: kind, Namespace: HookNamespace{ID: "bad"}, Handlers: HookHandlers{}},
		{Kind: kind, Namespace: HookNamespace{ID: "good"}, Handlers: HookHandlers{}},
	}
	factory := CreateHookRunners(
		func() []HookRegistration { return registrations },
		func(Id) []Id { return nil },
		func(err error) { reported = append(reported, err) },
	)
	runner := factory(kind, HookInfo{ConversationID: 1})
	var visited []string
	runner.Each(nil, func(handlers HookHandlers, api *jsonx.Obj) (any, error) {
		namespace := api.MustGet("kind")
		_ = namespace
		// First binding errors, second records.
		if len(visited) == 0 {
			visited = append(visited, "bad")
			return nil, errHookTest("boom")
		}
		visited = append(visited, "good")
		return "value", nil
	}, nil)
	if len(visited) != 2 || visited[0] != "bad" || visited[1] != "good" {
		t.Fatalf("visited = %v", visited)
	}
	if len(reported) != 1 {
		t.Fatalf("reported = %d", len(reported))
	}
}

func TestHookEachOnValueStops(t *testing.T) {
	kind := hookKind("app.kind")
	registrations := []HookRegistration{
		{Kind: kind, Handlers: HookHandlers{}},
		{Kind: kind, Handlers: HookHandlers{}},
	}
	factory := CreateHookRunners(func() []HookRegistration { return registrations }, func(Id) []Id { return nil }, nil)
	runner := factory(kind, HookInfo{ConversationID: 1})
	calls := 0
	runner.Each(nil, func(HookHandlers, *jsonx.Obj) (any, error) {
		calls++
		return "v", nil
	}, func(any) bool { return true })
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func errHookTest(msg string) error { return &hookTestError{msg} }

type hookTestError struct{ msg string }

func (e *hookTestError) Error() string { return e.msg }
