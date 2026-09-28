package harnessfacade

// Ports of the facade skeleton behaviors.

import (
	"testing"

	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/runtime"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
)

func newFacadeHarness(t *testing.T) *AgentHarness {
	t.Helper()
	storage := session.NewMemoryStorage()
	models := ai.CreateModels()
	handle := ai.FauxProvider(ai.RegisterFauxProviderOptions{})
	models.SetProvider(handle.Provider)
	model := models.GetModel(ai.FauxDefaultProvider, ai.FauxDefaultModelID)
	sess := session.NewStorageBackedSession(session.SessionMetadata{ID: "s1", StorageVersion: 1}, storage)
	t.Cleanup(func() { _ = sess.Close(harness.BackgroundContext) })
	h, _, err := CreateAgentHarness(AgentHarnessOptions{
		Session:         sess,
		Models:          models,
		Model:           model,
		ActiveToolNames: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(harness.BackgroundContext) })
	return h
}

func TestLaneAcquireAndList(t *testing.T) {
	h := newFacadeHarness(t)
	ctx := harness.BackgroundContext
	lane, err := h.Lane("main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lane.Name != "main" {
		t.Fatalf("lane = %s", lane.Name)
	}
	// Second acquire returns the SAME lane object.
	again, err := h.Lane("main", ctx)
	if err != nil || again != lane {
		t.Fatal("lane not memoized")
	}
	// The lane's durable values landed.
	config, err := h.Session().GetValue(session.LaneConfig("main"), ctx)
	if err != nil || config == nil {
		t.Fatalf("config = %v err = %v", config, err)
	}
	// Lanes lists it.
	lanes := h.Lanes()
	if len(lanes) != 1 || lanes[0].Name != "main" {
		t.Fatalf("lanes = %+v", lanes)
	}
}

func TestLaneRestore(t *testing.T) {
	// A session with an existing lane restores at harness creation.
	storage := session.NewMemoryStorage()
	models := ai.CreateModels()
	handle := ai.FauxProvider(ai.RegisterFauxProviderOptions{})
	models.SetProvider(handle.Provider)
	model := models.GetModel(ai.FauxDefaultProvider, ai.FauxDefaultModelID)
	sess := session.NewStorageBackedSession(session.SessionMetadata{ID: "s1", StorageVersion: 1}, storage)
	defer sess.Close(harness.BackgroundContext)
	ctx := harness.BackgroundContext

	// Seed a complete lane.
	if err := sess.SetValue(session.BranchTip("work"), nil, ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.LaneConfig("work"), runtime.LaneConfigJSONForTest(), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.LaneStateValue("work"), runtime.DurableLaneStateJSON(nil, nil, nil), ctx); err != nil {
		t.Fatal(err)
	}

	h, restored, err := CreateAgentHarness(AgentHarnessOptions{Session: sess, Models: models, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(ctx)
	if _, ok := restored["work"]; !ok {
		t.Fatalf("restored = %v", restored)
	}
	lanes := h.Lanes()
	if len(lanes) != 1 || lanes[0].Name != "work" {
		t.Fatalf("lanes = %+v", lanes)
	}
}

func TestFacadeNameAndLabels(t *testing.T) {
	h := newFacadeHarness(t)
	ctx := harness.BackgroundContext
	name := "my-session"
	if err := h.SetName(&name, ctx); err != nil {
		t.Fatal(err)
	}
	got, err := h.GetName(ctx)
	if err != nil || got == nil || *got != "my-session" {
		t.Fatalf("name = %v err = %v", got, err)
	}
	// Labels.
	label := "checkpoint"
	if err := h.SetLabel("e1", &label, ctx); err != nil {
		t.Fatal(err)
	}
	gotLabel, _ := h.GetLabel("e1", ctx)
	if gotLabel == nil || *gotLabel != "checkpoint" {
		t.Fatal("label round trip")
	}
}

func TestFacadeConfigSetters(t *testing.T) {
	h := newFacadeHarness(t)
	ctx := harness.BackgroundContext
	if _, err := h.Lane("main", ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.SetThinkingLevel("main", "high", ctx); err != nil {
		t.Fatal(err)
	}
	config, _ := h.Session().GetValue(session.LaneConfig("main"), ctx)
	if config == nil {
		t.Fatal("config missing")
	}
	if err := h.SetActiveTools("main", []string{"bash"}, ctx); err != nil {
		t.Fatal(err)
	}
}

func TestFacadeClose(t *testing.T) {
	h := newFacadeHarness(t)
	if h.Closed() {
		t.Fatal("closed at creation")
	}
	if err := h.Close(harness.BackgroundContext); err != nil {
		t.Fatal(err)
	}
	if !h.Closed() {
		t.Fatal("not closed")
	}
	// Idempotent.
	if err := h.Close(harness.BackgroundContext); err != nil {
		t.Fatal(err)
	}
	// Lane acquisition after close rejects.
	if _, err := h.Lane("other", harness.BackgroundContext); err == nil {
		t.Fatal("lane after close")
	}
}
