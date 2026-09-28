package harness

// Ports of hook aggregation semantics (from hooks tests and
// execution-tools tests) and stream-options patch behavior.

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/telemetry"
)

func hookRegistryWithErrors(errors *[]string) *HookRegistry {
	return NewHookRegistry(func(err error, hook, lane string, _ Context) {
		*errors = append(*errors, hook+": "+err.Error()+" lane="+lane)
	})
}

func hookEvent(pairs ...any) *jsonx.Obj {
	event := jsonx.NewObj()
	for i := 0; i+1 < len(pairs); i += 2 {
		event.Set(pairs[i].(string), pairs[i+1])
	}
	return event
}

func TestHookBeforeRunAccumulates(t *testing.T) {
	var errors []string
	registry := hookRegistryWithErrors(&errors)
	registry.On(HookBeforeRun, func(event *jsonx.Obj, _ Context) (*jsonx.Obj, error) {
		return jsonx.ObjFrom("messages", []any{"m1"}), nil
	})
	registry.On(HookBeforeRun, func(event *jsonx.Obj, _ Context) (*jsonx.Obj, error) {
		// Sees the accumulated prompt.
		prompt := event.MustGet("prompt").([]any)
		if len(prompt) != 1 || prompt[0] != "m1" {
			t.Fatalf("prompt = %v", prompt)
		}
		return jsonx.ObjFrom("messages", []any{"m2"}), nil
	})
	registry.On(HookBeforeRun, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		return nil, errTest("boom")
	})
	result, err := registry.Run(HookBeforeRun, hookEvent("prompt", []any{}), BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || jsonx.Stringify(result.MustGet("messages")) != `["m1","m2"]` {
		t.Fatalf("result = %s", jsonx.Stringify(result))
	}
	if len(errors) != 1 {
		t.Fatalf("errors = %v", errors)
	}
}

func errTest(msg string) error { return ToError(msg) }

func TestHookBeforeToolBlockBreaksAndArgsLastWins(t *testing.T) {
	var errors []string
	registry := hookRegistryWithErrors(&errors)
	registry.On(HookBeforeTool, func(event *jsonx.Obj, _ Context) (*jsonx.Obj, error) {
		return jsonx.ObjFrom("args", jsonx.ObjFrom("v", float64(2))), nil
	})
	registry.On(HookBeforeTool, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		return jsonx.ObjFrom("block", jsonx.ObjFrom("reason", "denied")), nil
	})
	// Never invoked: block breaks.
	ran := false
	registry.On(HookBeforeTool, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		ran = true
		return nil, nil
	})
	result, err := registry.Run(HookBeforeTool, hookEvent("args", jsonx.ObjFrom("v", float64(1))), BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("handler after block ran")
	}
	args, _ := result.Get("args")
	if v, _ := args.(*jsonx.Obj).Get("v"); v != float64(2) {
		t.Fatalf("args = %s", jsonx.Stringify(args))
	}
	block, _ := result.Get("block")
	if reason, _ := block.(*jsonx.Obj).Get("reason"); reason != "denied" {
		t.Fatalf("block = %s", jsonx.Stringify(block))
	}

	// A throwing handler blocks with its message.
	var errors2 []string
	registry2 := hookRegistryWithErrors(&errors2)
	registry2.On(HookBeforeTool, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		return nil, errTest("exploded")
	})
	result2, err := registry2.Run(HookBeforeTool, hookEvent("args", jsonx.NewObj()), BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	block2, _ := result2.Get("block")
	if reason, _ := block2.(*jsonx.Obj).Get("reason"); reason != "exploded" {
		t.Fatalf("block2 = %s", jsonx.Stringify(block2))
	}
}

func TestHookAfterToolOverlay(t *testing.T) {
	var errors []string
	registry := hookRegistryWithErrors(&errors)
	registry.On(HookAfterTool, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		return jsonx.ObjFrom("content", "patched", "terminate", true), nil
	})
	registry.On(HookAfterTool, func(event *jsonx.Obj, _ Context) (*jsonx.Obj, error) {
		// Sees the first handler's content overlay.
		if event.MustGet("content") != "patched" {
			t.Fatalf("content = %v", event.MustGet("content"))
		}
		return jsonx.ObjFrom("isError", true), nil
	})
	result, err := registry.Run(HookAfterTool, hookEvent("content", "original", "isError", false), BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if result.MustGet("content") != "patched" || result.MustGet("isError") != true || result.MustGet("terminate") != true {
		t.Fatalf("result = %s", jsonx.Stringify(result))
	}
}

func TestHookTransformContextFolds(t *testing.T) {
	var errors []string
	registry := hookRegistryWithErrors(&errors)
	registry.On(HookTransformContext, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		return jsonx.ObjFrom("messages", []any{"a"}), nil
	})
	registry.On(HookTransformContext, func(event *jsonx.Obj, _ Context) (*jsonx.Obj, error) {
		if jsonx.Stringify(event.MustGet("messages")) != `["a"]` {
			t.Fatalf("messages = %v", event.MustGet("messages"))
		}
		return jsonx.ObjFrom("messages", []any{"a", "b"}, "systemPrompt", "new"), nil
	})
	result, err := registry.Run(HookTransformContext, hookEvent("messages", []any{}, "systemPrompt", "old"), BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if jsonx.Stringify(result.MustGet("messages")) != `["a","b"]` || result.MustGet("systemPrompt") != "new" {
		t.Fatalf("result = %s", jsonx.Stringify(result))
	}
}

func TestHookFirstStructural(t *testing.T) {
	var errors []string
	registry := hookRegistryWithErrors(&errors)
	registry.On(HookBeforeCompaction, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		return jsonx.ObjFrom("decline", true), nil
	})
	ran := false
	registry.On(HookBeforeCompaction, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		ran = true
		return nil, nil
	})
	result, err := registry.Run(HookBeforeCompaction, hookEvent(), BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.MustGet("decline") != true {
		t.Fatalf("result = %s", jsonx.Stringify(result))
	}
	if ran {
		t.Fatal("second handler ran after decline")
	}

	// decline + compaction is an error, handler skipped.
	var errors2 []string
	registry2 := hookRegistryWithErrors(&errors2)
	registry2.On(HookBeforeCompaction, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		return jsonx.ObjFrom("decline", true, "compaction", "x"), nil
	})
	registry2.On(HookBeforeCompaction, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		return jsonx.ObjFrom("compaction", "valid"), nil
	})
	result2, _ := registry2.Run(HookBeforeCompaction, hookEvent(), BackgroundContext)
	if result2 == nil || result2.MustGet("compaction") != "valid" {
		t.Fatalf("result2 = %s", jsonx.Stringify(result2))
	}
	if len(errors2) != 1 {
		t.Fatalf("errors2 = %v", errors2)
	}
}

func TestHookBeforeDriveFailsClosed(t *testing.T) {
	var errors []string
	registry := hookRegistryWithErrors(&errors)
	registry.On(HookBeforeDrive, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		return nil, errTest("gate denied")
	})
	ran := false
	registry.On(HookBeforeDrive, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		ran = true
		return nil, nil
	})
	_, err := registry.Run(HookBeforeDrive, hookEvent(), BackgroundContext)
	if err == nil || err.Error() != "gate denied" {
		t.Fatalf("err = %v", err)
	}
	if ran {
		t.Fatal("handler after failure ran")
	}
}

func TestApplyStreamOptionsPatch(t *testing.T) {
	transport := "sse"
	base := AgentHarnessStreamOptions{Transport: &transport, Headers: map[string]string{"a": "1", "b": "2"}}
	next := "websocket"
	set, unset := next, ""
	patch := AgentHarnessStreamOptionsPatch{
		HasTransport: true, Transport: &next,
		HasHeaders: true,
		Headers:    map[string]*string{"b": &unset, "c": &set},
	}
	result := ApplyStreamOptionsPatch(base, patch)
	if result.Transport == nil || *result.Transport != "websocket" {
		t.Fatal("transport")
	}
	if result.Headers["a"] != "1" || result.Headers["b"] != "" || result.Headers["c"] != "websocket" {
		t.Fatalf("headers = %v", result.Headers)
	}
	_ = set
	_ = next

	// Clear-all headers via nil map.
	clearPatch := AgentHarnessStreamOptionsPatch{HasHeaders: true, ClearHeaders: true}
	cleared := ApplyStreamOptionsPatch(result, clearPatch)
	if cleared.Headers != nil {
		t.Fatalf("headers = %v", cleared.Headers)
	}
}

func TestCreateStreamOptionsPatchDiff(t *testing.T) {
	base := AgentHarnessStreamOptions{Headers: map[string]string{"keep": "1", "drop": "2"}}
	value := AgentHarnessStreamOptions{Headers: map[string]string{"keep": "1", "add": "3"}}
	patch := CreateStreamOptionsPatch(base, value)
	if !patch.HasHeaders {
		t.Fatal("headers not marked")
	}
	if patch.Headers == nil || patch.Headers["drop"] != nil {
		t.Fatalf("drop = %v", patch.Headers["drop"])
	}
	if patch.Headers["add"] == nil || *patch.Headers["add"] != "3" {
		t.Fatal("add missing")
	}
	if patch.Headers["keep"] != nil {
		t.Fatal("unchanged key in patch")
	}
	// Identical options produce an empty patch.
	if got := CreateStreamOptionsPatch(value, value); got.HasHeaders || got.HasTransport {
		t.Fatalf("empty patch = %+v", got)
	}
}

func TestHookSpansRecorded(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	ctx := WithTelemetryContext(recorder, BackgroundContext)
	var errors []string
	registry := hookRegistryWithErrors(&errors)
	registry.On(HookBeforeTool, func(*jsonx.Obj, Context) (*jsonx.Obj, error) {
		return jsonx.ObjFrom("block", jsonx.ObjFrom("reason", "no")), nil
	})
	_, err := registry.Run(HookBeforeTool, hookEvent("lane", "main", "runId", "op1"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	spans := recorder.GetSpans()
	if len(spans) != 1 || spans[0].Name != HarnessSpanHook {
		t.Fatalf("spans = %+v", spans)
	}
	if outcome, _ := spans[0].Attributes["pi.hook.outcome"]; outcome != "blocked" {
		t.Fatalf("outcome = %v", spans[0].Attributes["pi.hook.outcome"])
	}
	if name, _ := spans[0].Attributes["pi.hook.name"]; name != HookBeforeTool {
		t.Fatalf("hook name = %v", name)
	}
	if lane, _ := spans[0].Attributes["pi.lane.name"]; lane != "main" {
		t.Fatalf("lane = %v", lane)
	}
}
