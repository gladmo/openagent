package runtime

// Ports of accept()'s request-kind dispatch tests (accept.test.ts):
// skill/template expansion before acceptance, unknown-resource
// rejections, prompt building, and config-source requirement.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func dispatchResources() harness.AgentHarnessResources {
	return harness.AgentHarnessResources{
		Skills: []harness.Skill{{
			Name: "review", Description: "Review", Content: "Inspect it", FilePath: "/skills/review/SKILL.md",
		}},
		PromptTemplates: []harness.PromptTemplate{{Name: "fix", Content: "Fix $1 then $@"}},
	}
}

func newDispatchLane(t *testing.T, config *Config) (*Lane, session.Session) {
	t.Helper()
	lane, sess := newAcceptLane(t)
	lane.readConfig = func() *Config { return config }
	return lane, sess
}

func dispatchConfig() *Config {
	return &Config{
		Resources:    dispatchResources(),
		SteeringMode: "all",
		FollowUpMode: "all",
	}
}

func firstTextBlock(t *testing.T, message *jsonx.Obj) string {
	t.Helper()
	contentValue, _ := message.Get("content")
	blocks, ok := contentValue.([]any)
	if !ok || len(blocks) == 0 {
		t.Fatalf("content blocks = %#v", contentValue)
	}
	first, ok := blocks[0].(*jsonx.Obj)
	if !ok {
		t.Fatalf("first block = %#v", blocks[0])
	}
	text, _ := first.Get("text")
	s, _ := text.(string)
	return s
}

func TestAcceptRunRequestFormatsSkillBeforeAcceptance(t *testing.T) {
	lane, sess := newDispatchLane(t, dispatchConfig())

	result, err := AcceptRunRequest(lane, RunRequest{
		Kind: RunRequestSkill, Name: "review", AdditionalInstructions: "Be strict",
	}, acceptNextID(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Admitted {
		t.Fatalf("result = %+v", result)
	}
	tip := lane.State().TipID
	if tip == nil {
		t.Fatal("no tip after admission")
	}
	entry, err := sess.GetEntry(*tip, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	text := firstTextBlock(t, entry.Message.Message)
	if !strings.Contains(text, `<skill name="review"`) {
		t.Fatalf("skill block = %q", text)
	}
	if !strings.Contains(text, "Be strict") {
		t.Fatalf("additional instructions missing: %q", text)
	}
	if !strings.Contains(text, "/skills/review") {
		t.Fatalf("skill location missing: %q", text)
	}
}

func TestAcceptRunRequestFormatsTemplateBeforeAcceptance(t *testing.T) {
	lane, sess := newDispatchLane(t, dispatchConfig())

	result, err := AcceptRunRequest(lane, RunRequest{
		Kind: RunRequestPromptTemplate, Name: "fix", Args: []string{"A", "B"},
	}, acceptNextID(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Admitted {
		t.Fatalf("result = %+v", result)
	}
	entry, err := sess.GetEntry(*lane.State().TipID, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if text := firstTextBlock(t, entry.Message.Message); text != "Fix A then A B" {
		t.Fatalf("template text = %q", text)
	}
}

func TestAcceptRunRequestUnknownResources(t *testing.T) {
	lane, _ := newDispatchLane(t, dispatchConfig())

	skillResult, err := AcceptRunRequest(lane, RunRequest{Kind: RunRequestSkill, Name: "missing"}, acceptNextID(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if skillResult.UnknownSkill == nil || skillResult.UnknownSkill.Name != "missing" {
		t.Fatalf("skill result = %+v", skillResult)
	}
	if skillResult.Admitted || skillResult.Invalid != nil || skillResult.Busy != nil {
		t.Fatalf("skill result = %+v", skillResult)
	}

	templateResult, err := AcceptRunRequest(lane, RunRequest{Kind: RunRequestPromptTemplate, Name: "missing"}, acceptNextID(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if templateResult.UnknownTemplate == nil || templateResult.UnknownTemplate.Name != "missing" {
		t.Fatalf("template result = %+v", templateResult)
	}
	if lane.State().Operation != nil {
		t.Fatal("rejection installed an operation")
	}
}

func TestAcceptRunRequestBuildsPromptMessage(t *testing.T) {
	lane, sess := newDispatchLane(t, dispatchConfig())

	result, err := AcceptRunRequest(lane, RunRequest{Kind: RunRequestPrompt, Prompt: "hello"}, acceptNextID(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Admitted {
		t.Fatalf("result = %+v", result)
	}
	entry, err := sess.GetEntry(*lane.State().TipID, harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if entry.Message.Role != "user" {
		t.Fatalf("role = %q", entry.Message.Role)
	}
	if text := firstTextBlock(t, entry.Message.Message); text != "hello" {
		t.Fatalf("prompt text = %q", text)
	}
}

func TestAcceptRunRequestEmptyPromptInvalid(t *testing.T) {
	lane, _ := newDispatchLane(t, dispatchConfig())

	result, err := AcceptRunRequest(lane, RunRequest{Kind: RunRequestPrompt}, acceptNextID(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if result.Invalid == nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestAcceptRunRequestPendingAssistantInvalid(t *testing.T) {
	lane, _ := newDispatchLane(t, dispatchConfig())
	pending := jsonx.ObjFrom(
		"role", "assistant", "stopReason", "pending", "content", "draft",
	)

	result, err := AcceptRunRequest(lane, RunRequest{Kind: RunRequestPrompt, Messages: []*jsonx.Obj{pending}}, acceptNextID(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if result.Invalid == nil || result.Invalid.Reason != "pending_assistant" {
		t.Fatalf("result = %+v", result)
	}
}

func TestAcceptRunRequestRequiresConfigSource(t *testing.T) {
	lane, _ := newAcceptLane(t) // no ReadConfig

	if _, err := AcceptRunRequest(lane, RunRequest{Kind: RunRequestPrompt, Prompt: "hi"}, acceptNextID(), harnessBackground()); err == nil {
		t.Fatal("nil config source accepted")
	}
}
