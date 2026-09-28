package kinds

// Ports of kinds/tool.ts decision behaviors.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent/harness/pico3"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

func toolDeclaration() *pico3.ToolDeclaration {
	return &pico3.ToolDeclaration{
		Name: "bash",
		Parameters: typebox.Object([]*typebox.Property{
			typebox.Prop("command", typebox.String()),
		}),
		Replay: "unsafe",
	}
}

func toolCallOf(name string, args map[string]any) StoredToolCall {
	call := jsonx.NewObj()
	call.Set("id", "c1")
	call.Set("name", name)
	call.Set("namespace", "core")
	if args != nil {
		argsObj := jsonx.NewObj()
		for k, v := range args {
			argsObj.Set(k, v)
		}
		call.Set("arguments", argsObj)
	}
	return call
}

func toolInputOf(name string, args map[string]any, offered ...string) ToolInputFields {
	return ToolInputFields{
		Assistant: 1,
		Call:      toolCallOf(name, args),
		Offered:   offered,
		Index:     0,
	}
}

func passthroughHook(call StoredToolCall) (bool, string, StoredToolCall, error) {
	return false, "", call, nil
}

func TestEvaluateToolInitialGuards(t *testing.T) {
	// Not offered.
	decision, err := EvaluateToolInitial(toolInputOf("bash", map[string]any{"command": "ls"}, "other"), toolDeclaration(), passthroughHook)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Code != "not_offered" || !decision.Done.MustGet("isError").(bool) {
		t.Fatalf("decision = %+v", decision)
	}
	if !strings.Contains(decision.Done.MustGet("content").([]any)[0].(*jsonx.Obj).MustGet("text").(string), "was not offered") {
		t.Fatal("message")
	}

	// Not registered.
	decision, _ = EvaluateToolInitial(toolInputOf("ghost", nil, "ghost"), nil, passthroughHook)
	if decision.Code != "missing_tool" {
		t.Fatalf("code = %s", decision.Code)
	}

	// Invalid arguments.
	decision, _ = EvaluateToolInitial(toolInputOf("bash", map[string]any{}, "bash"), toolDeclaration(), passthroughHook)
	if decision.Code != "invalid_arguments" || !strings.Contains(textOfSynthetic(decision.Done), "invalid arguments") {
		t.Fatalf("code = %s text = %s", decision.Code, textOfSynthetic(decision.Done))
	}

	// Blocked by hook.
	blockHook := func(StoredToolCall) (bool, string, StoredToolCall, error) {
		return true, "policy", nil, nil
	}
	decision, _ = EvaluateToolInitial(toolInputOf("bash", map[string]any{"command": "ls"}, "bash"), toolDeclaration(), blockHook)
	if decision.Code != "blocked" || !strings.Contains(textOfSynthetic(decision.Done), "blocked: policy") {
		t.Fatalf("code = %s", decision.Code)
	}

	// Identity changed by hook.
	identityHook := func(call StoredToolCall) (bool, string, StoredToolCall, error) {
		replaced := toolCallOf("other", map[string]any{"command": "ls"})
		return false, "", replaced, nil
	}
	decision, _ = EvaluateToolInitial(toolInputOf("bash", map[string]any{"command": "ls"}, "bash"), toolDeclaration(), identityHook)
	if decision.Code != "blocked" || !strings.Contains(textOfSynthetic(decision.Done), "identity changed") {
		t.Fatalf("code = %s", decision.Code)
	}

	// Valid: proceeds with the durable started write.
	decision, _ = EvaluateToolInitial(toolInputOf("bash", map[string]any{"command": "ls"}, "bash"), toolDeclaration(), passthroughHook)
	if !decision.WriteCheckpoint || decision.Done != nil {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.Replay != "unsafe" {
		t.Fatalf("replay = %s", decision.Replay)
	}
}

func textOfSynthetic(result *jsonx.Obj) string {
	return result.MustGet("content").([]any)[0].(*jsonx.Obj).MustGet("text").(string)
}

func TestEvaluateToolInitialHookRewrite(t *testing.T) {
	// A hook may rewrite arguments (same identity); revalidation applies.
	rewriteHook := func(call StoredToolCall) (bool, string, StoredToolCall, error) {
		replaced := jsonx.NewObj()
		replaced.Set("id", CallString(call, "id"))
		replaced.Set("name", CallString(call, "name"))
		replaced.Set("namespace", CallString(call, "namespace"))
		replaced.Set("arguments", jsonx.ObjFrom("command", "rewritten"))
		return false, "", replaced, nil
	}
	decision, err := EvaluateToolInitial(toolInputOf("bash", map[string]any{"command": "ls"}, "bash"), toolDeclaration(), rewriteHook)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.WriteCheckpoint {
		t.Fatalf("decision = %+v", decision)
	}
	args, _ := decision.Call.Get("arguments")
	if args.(*jsonx.Obj).MustGet("command") != "rewritten" {
		t.Fatal("rewrite lost")
	}

	// A rewrite that invalidates arguments fails final validation.
	invalidRewrite := func(call StoredToolCall) (bool, string, StoredToolCall, error) {
		replaced := jsonx.NewObj()
		replaced.Set("id", CallString(call, "id"))
		replaced.Set("name", CallString(call, "name"))
		replaced.Set("namespace", CallString(call, "namespace"))
		replaced.Set("arguments", jsonx.NewObj())
		return false, "", replaced, nil
	}
	decision, _ = EvaluateToolInitial(toolInputOf("bash", map[string]any{"command": "ls"}, "bash"), toolDeclaration(), invalidRewrite)
	if decision.Code != "invalid_arguments" || !strings.Contains(textOfSynthetic(decision.Done), "after hook") {
		t.Fatalf("code = %s", decision.Code)
	}
}

func TestEvaluateToolStartedGuards(t *testing.T) {
	// Unavailable after restart.
	checkpoint := ToolCheckpointFields{Phase: "started", Replay: "safe", Call: toolCallOf("bash", map[string]any{"command": "ls"})}
	decision, err := EvaluateToolStarted(checkpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Code != "unavailable" {
		t.Fatalf("code = %s", decision.Code)
	}

	// Unsafe checkpoint replay -> interrupted.
	unsafeCheckpoint := ToolCheckpointFields{Phase: "started", Replay: "unsafe", Call: checkpoint.Call}
	decision, _ = EvaluateToolStarted(unsafeCheckpoint, toolDeclaration())
	if decision.Code != "interrupted" {
		t.Fatalf("code = %s", decision.Code)
	}

	// Safe checkpoint but unsafe declaration -> interrupted.
	safeDecl := toolDeclaration()
	safeDecl.Replay = "unsafe"
	decision, _ = EvaluateToolStarted(ToolCheckpointFields{Phase: "started", Replay: "safe", Call: checkpoint.Call}, safeDecl)
	if decision.Code != "interrupted" {
		t.Fatalf("code = %s", decision.Code)
	}

	// Both safe + valid args -> invoke.
	safeDecl.Replay = "safe"
	decision, _ = EvaluateToolStarted(checkpoint, safeDecl)
	if decision.Done != nil || decision.Call == nil {
		t.Fatalf("decision = %+v", decision)
	}

	// Both safe but args no longer validate -> interrupted.
	badCall := toolCallOf("bash", map[string]any{})
	decision, _ = EvaluateToolStarted(ToolCheckpointFields{Phase: "started", Replay: "safe", Call: badCall}, safeDecl)
	if decision.Code != "interrupted" || !strings.Contains(textOfSynthetic(decision.Done), "no longer validate") {
		t.Fatalf("code = %s", decision.Code)
	}
}

func TestAbortSlotPatch(t *testing.T) {
	slot := jsonx.ObjFrom("status", "running", "waitingOn", "hook.ns")
	AbortSlotPatch(slot, 42)
	if slot.MustGet("status") != "aborted" || slot.MustGet("entry") != float64(42) {
		t.Fatalf("slot = %v", slot)
	}
	if _, has := slot.Get("waitingOn"); has {
		t.Fatal("waitingOn survived")
	}
	// Nil slot is a no-op.
	AbortSlotPatch(nil, 1)
}

func TestInvalidArgumentsFormats(t *testing.T) {
	declaration := toolDeclaration()
	bad := InvalidArguments(declaration, toolCallOf("bash", map[string]any{"command": float64(42)}))
	if bad == "" || !strings.Contains(bad, "must be") {
		t.Fatalf("bad = %q", bad)
	}
	// Valid args produce no errors.
	if bad := InvalidArguments(declaration, toolCallOf("bash", map[string]any{"command": "ls"})); bad != "" {
		t.Fatalf("bad = %q", bad)
	}
}
