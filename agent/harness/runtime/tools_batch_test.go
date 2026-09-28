package runtime

// Ports of drive/tools.ts batch helper behaviors.

import (
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func batchOf(calls ...*jsonx.Obj) *jsonx.Obj {
	items := make([]any, 0, len(calls))
	for _, call := range calls {
		items = append(items, call)
	}
	return jsonx.ObjFrom("assistantEntryId", "a1", "turnId", "t1", "calls", items)
}

func callAt(sourceIndex int64, resultEntryID, status string) *jsonx.Obj {
	return jsonx.ObjFrom(
		"sourceIndex", float64(sourceIndex),
		"resultEntryId", resultEntryID,
		"status", status,
	)
}

func TestCurrentBatch(t *testing.T) {
	batch := batchOf(callAt(0, "r1", "planned"))
	state := &RuntimeLaneState{}
	// No operation.
	if op, got := CurrentBatch(state); op != nil || got != nil {
		t.Fatal("no-op returned a batch")
	}
	// Wrong leaf.
	state.Operation = &session.Operation{State: session.OperationState{At: session.AtStarting}}
	if op, _ := CurrentBatch(state); op != nil {
		t.Fatal("wrong leaf returned")
	}
	// Tools leaf returns both.
	state.Operation.State.At = session.AtTools
	state.Operation.State.Batch = batch
	op, returned := CurrentBatch(state)
	if op == nil || returned != batch {
		t.Fatal("tools leaf batch missing")
	}
}

func TestFindCall(t *testing.T) {
	batch := batchOf(callAt(0, "r1", "planned"), callAt(1, "r2", "effect_pending"))
	if call := FindCall(batch, 1, "r2"); call == nil || call.MustGet("status") != "effect_pending" {
		t.Fatalf("call = %v", call)
	}
	// Wrong entry id.
	if call := FindCall(batch, 1, "other"); call != nil {
		t.Fatal("wrong entry matched")
	}
	// Wrong index.
	if call := FindCall(batch, 5, "r1"); call != nil {
		t.Fatal("wrong index matched")
	}
	// Nil batch.
	if call := FindCall(nil, 0, "r1"); call != nil {
		t.Fatal("nil batch matched")
	}
}

func TestReplaceCall(t *testing.T) {
	batch := batchOf(callAt(0, "r1", "planned"), callAt(1, "r2", "effect_pending"))
	replacement := callAt(1, "r2", "completed")
	replacement.Set("terminate", true)
	updated := ReplaceCall(batch, replacement)
	calls := updated.MustGet("calls").([]any)
	if len(calls) != 2 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[1].(*jsonx.Obj).MustGet("status") != "completed" || calls[1].(*jsonx.Obj).MustGet("terminate") != true {
		t.Fatal("replacement not applied")
	}
	// The untouched call survives.
	if calls[0].(*jsonx.Obj).MustGet("resultEntryId") != "r1" {
		t.Fatal("sibling mutated")
	}
	// Other batch fields preserved.
	if updated.MustGet("turnId") != "t1" {
		t.Fatal("batch fields lost")
	}
	// Unknown call appends.
	extra := callAt(9, "r9", "planned")
	updated = ReplaceCall(batch, extra)
	if len(updated.MustGet("calls").([]any)) != 3 {
		t.Fatal("unknown call not appended")
	}
}

func TestValidateMemoName(t *testing.T) {
	if err := ValidateMemoName(""); err == nil {
		t.Fatal("empty accepted")
	}
	if err := ValidateMemoName("a:b"); err == nil {
		t.Fatal("colon accepted")
	}
	if err := ValidateMemoName("valid-name"); err != nil {
		t.Fatalf("valid rejected: %v", err)
	}
}

func TestMemoAndArgsAddresses(t *testing.T) {
	memo := MemoAddress("op1", "inv1", "bash")
	if memo.Namespace != "pi.op.tool_memo" || memo.Key != "op1:inv1:bash" {
		t.Fatalf("memo = %+v", memo)
	}
	args := ArgsAddress("op1", "step1", 2)
	if args.Namespace != "pi.op.tool_args" || args.Key != "op1:step1:2" {
		t.Fatalf("args = %+v", args)
	}
}

func TestInterruptionMarkerAndError(t *testing.T) {
	if InterruptionMarker == "" || !contains(InterruptionMarker, "interrupted") {
		t.Fatal("marker malformed")
	}
	err := &ToolInvocationEnded{}
	if err.Error() != "Tool invocation no longer owns its durable effect" {
		t.Fatalf("err = %q", err.Error())
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
