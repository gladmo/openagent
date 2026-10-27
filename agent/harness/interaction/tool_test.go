package interaction

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/toolset"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

func answeringService(t *testing.T) *Service {
	t.Helper()
	service := NewService()
	service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		return &Answer{Items: []AnswerItem{
			{QuestionID: "confirm", Labels: []string{"yes"}},
			{QuestionID: "notes", Labels: []string{"ship it"}},
		}}, nil
	}))
	return service
}

func askParams() *jsonx.Obj {
	return jsonx.ObjFrom("questions", []any{
		jsonx.ObjFrom("id", "confirm", "header", "Confirm", "question", "Proceed?",
			"options", []any{
				jsonx.ObjFrom("label", "yes", "description", "Go ahead"),
				jsonx.ObjFrom("label", "no"),
			}),
		jsonx.ObjFrom("id", "notes", "header", "Notes", "question", "Any notes?"),
	})
}

func TestToolSchemaMatchesVocabulary(t *testing.T) {
	tool := CreateAskUserQuestionTool(answeringService(t))
	if tool.Name != AskUserQuestionToolName || tool.Parameters == nil {
		t.Fatalf("tool = %+v", tool)
	}
	validator := typebox.Compile(tool.Parameters)
	if !validator.Check(askParams()) {
		t.Fatal("valid arguments must pass the schema")
	}
	missing := jsonx.ObjFrom("questions", []any{
		jsonx.ObjFrom("header", "Confirm", "question", "Proceed?"),
	})
	if validator.Check(missing) {
		t.Fatal("a question without id must fail the schema")
	}
}

func TestToolExecuteAnswersThroughService(t *testing.T) {
	tool := CreateAskUserQuestionTool(answeringService(t))
	result, err := tool.Execute("call-1", askParams(), nil, nil, nil, harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content = %+v", result.Content)
	}
	text, ok := result.Content[0].(ai.TextContent)
	if !ok || !strings.Contains(text.Text, "confirm: yes") || !strings.Contains(text.Text, "notes: ship it") {
		t.Fatalf("content = %+v", result.Content)
	}
	if result.StructuredContent == nil {
		t.Fatal("structured content missing")
	}
}

func TestToolExecuteRejectsInvalidInput(t *testing.T) {
	tool := CreateAskUserQuestionTool(answeringService(t))
	duplicate := jsonx.ObjFrom("questions", []any{
		jsonx.ObjFrom("id", "q", "question", "One?"),
		jsonx.ObjFrom("id", "q", "question", "Two?"),
	})
	if _, err := tool.Execute("call-1", duplicate, nil, nil, nil, harness.BackgroundContext); err == nil || !strings.Contains(err.Error(), CodeDuplicateQuestionID) {
		t.Fatalf("err = %v", err)
	}
	multiNoOptions := jsonx.ObjFrom("questions", []any{
		jsonx.ObjFrom("id", "q", "question", "Pick?", "multiSelect", true),
	})
	if _, err := tool.Execute("call-1", multiNoOptions, nil, nil, nil, harness.BackgroundContext); err == nil || !strings.Contains(err.Error(), "multiSelect") {
		t.Fatalf("err = %v", err)
	}
}

func TestToolExecuteFailsClosedWithoutAnswerer(t *testing.T) {
	tool := CreateAskUserQuestionTool(NewService())
	if _, err := tool.Execute("call-1", askParams(), nil, nil, nil, harness.BackgroundContext); err == nil || !strings.Contains(err.Error(), CodeNoAnswerer) {
		t.Fatalf("err = %v", err)
	}
}

func TestToolRidesToolsetAdaptation(t *testing.T) {
	bundle := toolset.NewLocal(t.TempDir(), []*harness.AgentHarnessTool{
		CreateAskUserQuestionTool(answeringService(t)),
	})
	defer func() { _ = bundle.Close() }()
	adapted := bundle.AgentTools()
	if len(adapted) != 1 || adapted[0].Name != AskUserQuestionToolName {
		t.Fatalf("tools = %+v", adapted)
	}
	result, err := adapted[0].Execute("call-1", askParams(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := result.Content[0].(ai.TextContent); !ok || !strings.Contains(text.Text, "confirm: yes") {
		t.Fatalf("content = %+v", result.Content)
	}
}
