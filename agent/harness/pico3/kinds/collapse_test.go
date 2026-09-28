package kinds

// Ports of kinds/collapse.ts behaviors.

import (
	"testing"

	"github.com/gladmo/openagent/agent/harness/pico3"
	"github.com/gladmo/openagent/jsonx"
)

func collapseEntryOf(id int64, role string, tokens float64) *pico3.Entry {
	message := jsonx.ObjFrom("role", role, "tokensForTest", tokens)
	return &pico3.Entry{ID: id, Model: []any{message}}
}

func tokenEstimator(messages []any) float64 {
	total := 0.0
	for _, message := range messages {
		if obj, ok := message.(*jsonx.Obj); ok {
			if v, ok := obj.Get("tokensForTest"); ok {
				if f, ok := v.(float64); ok {
					total += f
				}
			}
		}
	}
	return total
}

func TestParseCollapseInputAndCheckpoint(t *testing.T) {
	input := jsonx.ObjFrom("reason", "threshold", "through", float64(42), "instructions", "fast")
	fields := ParseCollapseInput(input)
	if fields.Reason != "threshold" || fields.Through != 42 || !fields.HasInstructions || fields.Instructions != "fast" {
		t.Fatalf("fields = %+v", fields)
	}

	through := int64(9)
	checkpoint := jsonx.ObjFrom(
		"phase", "retrying",
		"expectedHead", float64(through),
		"instructions", "sum",
		"model", jsonx.ObjFrom("provider", "p", "modelId", "m"),
		"thinkingLevel", "low",
		"retry", jsonx.ObjFrom("enabled", true, "maxRetries", float64(3), "baseDelayMs", float64(500), "maxAgentDelayMs", float64(9000)),
		"attempt", float64(2),
		"untilMs", float64(777),
		"lastError", "boom",
	)
	phase, base, until, lastErr, summary := ParseCollapseCheckpoint(checkpoint)
	if phase != "retrying" || until != 777 || lastErr != "boom" || summary != "" {
		t.Fatalf("phase = %s until = %v lastErr = %s", phase, until, lastErr)
	}
	if !base.HasExpectedHead || base.ExpectedHead == nil || *base.ExpectedHead != 9 {
		t.Fatal("expectedHead")
	}
	if base.ThinkingLevel != "low" || base.Attempt != 2 || !base.Retry.Enabled || base.Retry.MaxRetries != 3 {
		t.Fatalf("base = %+v", base)
	}
	if base.Retry.MaxAgentDelayMs == nil || *base.Retry.MaxAgentDelayMs != 9000 {
		t.Fatal("maxAgentDelay")
	}
}

func TestHeadMoved(t *testing.T) {
	observed := int64(5)
	expected := int64(5)
	if HeadMoved(&expected, true, &observed) {
		t.Fatal("equal heads moved")
	}
	other := int64(6)
	if !HeadMoved(&expected, true, &other) {
		t.Fatal("different heads not moved")
	}
	if !HeadMoved(&expected, true, nil) {
		t.Fatal("nil observed not moved")
	}
	if HeadMoved(nil, false, nil) {
		t.Fatal("absent expected with nil observed moved")
	}
	if !HeadMoved(nil, false, &observed) {
		t.Fatal("absent expected with observed not moved")
	}
}

func TestEvaluatePrepared(t *testing.T) {
	expected := int64(10)
	observed := int64(10)
	entries := []*pico3.Entry{
		{ID: 3, Kind: "pi.user"},
		{ID: 5, Kind: "pi.user"}, // first retained after through=4
		{ID: 9, Kind: "pi.user"},
	}
	decision := EvaluatePrepared(&expected, true, &observed, entries, 4)
	if decision.Stale {
		t.Fatal("stale without movement")
	}
	if decision.HeadSelf || decision.HeadID != 5 {
		t.Fatalf("head = %d self = %v", decision.HeadID, decision.HeadSelf)
	}
	// Nothing after through -> head self.
	decision = EvaluatePrepared(&expected, true, &observed, entries[:1], 4)
	if !decision.HeadSelf {
		t.Fatal("head self missing")
	}
	// Moved head -> stale.
	moved := int64(11)
	decision = EvaluatePrepared(&expected, true, &moved, entries, 4)
	if !decision.Stale {
		t.Fatal("moved head not stale")
	}
}

func TestChooseThrough(t *testing.T) {
	// Exchange groups: user(1) assistant(2)+toolResult(3) user(4) assistant(5).
	entries := []*pico3.Entry{
		collapseEntryOf(1, "user", 100),
		collapseEntryOf(2, "assistant", 200),
		collapseEntryOf(3, "toolResult", 50),
		collapseEntryOf(4, "user", 100),
		collapseEntryOf(5, "assistant", 200),
	}
	// Everything fits: no cut.
	if through, ok := ChooseThrough(entries, 10000, tokenEstimator); ok {
		t.Fatalf("through = %d", through)
	}
	// Retain only the last exchange (200): the boundary exchange is (4),
	// cut at its last = 4.
	if through, ok := ChooseThrough(entries, 200, tokenEstimator); !ok || through != 4 {
		t.Fatalf("through = %d ok = %v", through, ok)
	}
	// Retain the last two (100+200): boundary is (2,3) group -> last = 3.
	if through, ok := ChooseThrough(entries, 300, tokenEstimator); !ok || through != 3 {
		t.Fatalf("through = %d ok = %v", through, ok)
	}
	// KeepRecent smaller than the newest exchange alone: only the newest
	// remains retained, cut at the previous exchange's last = 4.
	if through, ok := ChooseThrough(entries, 150, tokenEstimator); !ok || through != 4 {
		t.Fatalf("through = %d ok = %v", through, ok)
	}
}

func TestAfterFailure(t *testing.T) {
	base := CollapseBaseFields{Attempt: 1}
	// Retry decision -> retrying checkpoint fields.
	decision := AfterFailure(base, "boom", 1000, func(CollapseBaseFields, float64) (bool, string, float64, bool) {
		return false, "", 2500, true
	})
	if decision.Fail || !decision.HasRetry || decision.UntilMS != 2500 {
		t.Fatalf("decision = %+v", decision)
	}
	// Fail decision -> failed completion reason.
	decision = AfterFailure(base, "boom", 1000, func(CollapseBaseFields, float64) (bool, string, float64, bool) {
		return true, "retries_exhausted", 0, false
	})
	if !decision.Fail || decision.Reason != "retries_exhausted" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestCollapseFailedCompletion(t *testing.T) {
	completion := CollapseFailedCompletion("declined", "declined by beforeCollapse")
	failure := completion.MustGet("failure").(*jsonx.Obj)
	if completion.MustGet("status") != "failed" || failure.MustGet("reason") != "declined" {
		t.Fatalf("completion = %v", completion)
	}
}
