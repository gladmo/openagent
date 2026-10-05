package harnessfacade

// Ports of the facade's skill/prompt-template drive entries: resources
// supplied at harness creation resolve by name and expand into the
// admitted run's opening user message.

import (
	"errors"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/runtime"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
)

func newResourceFacadeHarness(t *testing.T, resources harness.AgentHarnessResources) *AgentHarness {
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
		Resources:       resources,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(harness.BackgroundContext) })
	return h
}

func facadeSkillResources() harness.AgentHarnessResources {
	return harness.AgentHarnessResources{
		Skills: []harness.Skill{{
			Name: "review", Description: "Review", Content: "Inspect it", FilePath: "/skills/review/SKILL.md",
		}},
		PromptTemplates: []harness.PromptTemplate{{Name: "fix", Content: "Fix $1 then $@"}},
	}
}

func TestSkillDrivesWithExpandedBlock(t *testing.T) {
	h := newResourceFacadeHarness(t, facadeSkillResources())
	ctx := harness.BackgroundContext

	result, err := h.Skill("main", "review", "Be strict", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.OperationID == "" {
		t.Fatal("no operation id")
	}
	// The opening transcript entry carries the expanded skill block.
	lane, err := h.Lane("main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	tip := lane.State().TipID
	if tip == nil {
		t.Fatal("no tip")
	}
	entry, err := h.Session().GetEntry(*tip, ctx)
	if err != nil {
		t.Fatal(err)
	}
	contentValue, _ := entry.Message.Message.Get("content")
	blocks, ok := contentValue.([]any)
	if !ok || len(blocks) == 0 {
		t.Fatalf("content = %#v", contentValue)
	}
	text := ""
	if first, ok := blocks[0].(interface{ Get(string) (any, bool) }); ok {
		raw, _ := first.Get("text")
		text, _ = raw.(string)
	}
	if !strings.Contains(text, `<skill name="review"`) || !strings.Contains(text, "Be strict") {
		t.Fatalf("skill block = %q", text)
	}
}

func TestSkillUnknownNameRejects(t *testing.T) {
	h := newResourceFacadeHarness(t, facadeSkillResources())

	_, err := h.Skill("main", "missing", "", harness.BackgroundContext)
	var unknown *runtime.UnknownSkill
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v", err)
	}
}

func TestPromptFromTemplateDrivesWithSubstitution(t *testing.T) {
	h := newResourceFacadeHarness(t, facadeSkillResources())
	ctx := harness.BackgroundContext

	result, err := h.PromptFromTemplate("main", "fix", []string{"A", "B"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.OperationID == "" {
		t.Fatal("no operation id")
	}
	lane, err := h.Lane("main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := h.Session().GetEntry(*lane.State().TipID, ctx)
	if err != nil {
		t.Fatal(err)
	}
	contentValue, _ := entry.Message.Message.Get("content")
	blocks, _ := contentValue.([]any)
	text := ""
	if first, ok := blocks[0].(interface{ Get(string) (any, bool) }); ok {
		raw, _ := first.Get("text")
		text, _ = raw.(string)
	}
	if text != "Fix A then A B" {
		t.Fatalf("template text = %q", text)
	}
}
