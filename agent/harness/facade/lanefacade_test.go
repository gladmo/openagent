package harnessfacade

// Ports of the lane facade forwarding.

import (
	"testing"

	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/runtime"
	"github.com/gladmo/openagent/jsonx"
)

func newFacadeLane(t *testing.T) *FacadeLane {
	t.Helper()
	h := newFacadeHarness(t)
	lane, err := h.ForLane("main", harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	return lane
}

func TestFacadeLaneTip(t *testing.T) {
	lane := newFacadeLane(t)
	tip, err := lane.GetTipID(harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh lane's tip is null.
	if tip != nil {
		t.Fatalf("tip = %v", tip)
	}
}

func TestFacadeLaneQueueForwarding(t *testing.T) {
	lane := newFacadeLane(t)
	ctx := harness.BackgroundContext
	steer, err := lane.Steer("left", ctx)
	if err != nil {
		t.Fatal(err)
	}
	follow, err := lane.FollowUp("more", ctx)
	if err != nil {
		t.Fatal(err)
	}
	next, err := lane.NextRun("again", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if steer.EntryID == "" || follow.EntryID == "" || next.EntryID == "" {
		t.Fatal("queue ids empty")
	}
	// Kinds are distinct in the inbox.
	inbox := lane.lane.State().Inbox
	if len(inbox) != 3 || inbox[0].Kind != "steer" || inbox[1].Kind != "followUp" || inbox[2].Kind != "nextRun" {
		t.Fatalf("inbox = %+v", inbox)
	}
	// Cancel one.
	result, err := lane.CancelQueued(follow.EntryID, ctx)
	if err != nil || result.Kind != runtime.CancelCancelled {
		t.Fatalf("result = %+v err = %v", result, err)
	}
	if len(lane.lane.State().Inbox) != 2 {
		t.Fatal("inbox not pruned")
	}
}

func TestFacadeLaneConfig(t *testing.T) {
	lane := newFacadeLane(t)
	ctx := harness.BackgroundContext
	// Defaults.
	level, err := lane.GetThinkingLevel(ctx)
	if err != nil || level != "off" {
		t.Fatalf("level = %s err = %v", level, err)
	}
	tools, err := lane.GetActiveTools(ctx)
	if err != nil || len(tools) != 0 {
		t.Fatalf("tools = %v err = %v", tools, err)
	}
	// Setters round-trip.
	if err := lane.SetThinkingLevel("high", ctx); err != nil {
		t.Fatal(err)
	}
	if err := lane.SetActiveTools([]string{"bash"}, ctx); err != nil {
		t.Fatal(err)
	}
	level, _ = lane.GetThinkingLevel(ctx)
	if level != "high" {
		t.Fatalf("level = %s", level)
	}
	tools, _ = lane.GetActiveTools(ctx)
	if len(tools) != 1 || tools[0] != "bash" {
		t.Fatalf("tools = %v", tools)
	}
}

func TestFacadeLaneAppendMessage(t *testing.T) {
	lane := newFacadeLane(t)
	ctx := harness.BackgroundContext
	// Append through the facade: the tip advances.
	firstID, err := lane.AppendMessage(jsonx.ObjFrom("role", "user", "content", "one", "timestamp", float64(1)), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if firstID == "" {
		t.Fatal("no entry id")
	}
	tip, _ := lane.GetTipID(ctx)
	if tip == nil || *tip != firstID {
		t.Fatalf("tip = %v first = %s", tip, firstID)
	}
	// A second append chains from the first.
	secondID, err := lane.AppendMessage(jsonx.ObjFrom("role", "user", "content", "two", "timestamp", float64(2)), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if secondID == firstID {
		t.Fatal("ids collide")
	}
	tip, _ = lane.GetTipID(ctx)
	if tip == nil || *tip != secondID {
		t.Fatalf("tip = %v", tip)
	}
}

func TestFacadeLaneGetResult(t *testing.T) {
	lane := newFacadeLane(t)
	ctx := harness.BackgroundContext
	// No result yet.
	record, err := lane.GetResult("op-none", ctx)
	if err != nil || record != nil {
		t.Fatalf("record = %v err = %v", record, err)
	}
}
