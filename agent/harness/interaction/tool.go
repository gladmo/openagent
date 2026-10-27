package interaction

// tool.go is the model-facing consumer: the ask_user_question harness
// tool over the Service. The tool is one consumer of the seam —
// embedders consume Service.Ask directly (hooks, facades, tests) — and
// depends on nothing but the definition: it ignores the execution tool
// context, so it rides any toolset adaptation unchanged.

import (
	"fmt"
	"strings"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

// AskUserQuestionToolName is the model-facing tool name.
const AskUserQuestionToolName = "ask_user_question"

// CreateAskUserQuestionTool builds the ask_user_question tool over the
// service.
func CreateAskUserQuestionTool(service *Service) *harness.AgentHarnessTool {
	schema := typebox.Object([]*typebox.Property{
		typebox.Prop("questions", typebox.Array(questionSchema(), typebox.Description(
			"One to four questions for the human; every answer references the question id."))),
	})
	return &harness.AgentHarnessTool{
		AgentTool: agent.AgentTool{
			Name:  AskUserQuestionToolName,
			Label: "ask user",
			Description: "Ask the human between one and four questions when a decision or missing information " +
				"blocks the work. Each question carries a stable id, a short header, the question text, and — " +
				"when choices exist — options with labels; answers come back keyed by id. Use it instead of " +
				"guessing preferences or fabricating missing facts.",
			Parameters: schema,
		},
		Execute: func(_ string, input any, _ harness.AgentHarnessToolUpdateCallback, _ any, _ harness.AgentHarnessToolInvocation, ctx harness.Context) (*agent.AgentToolResult, error) {
			request, err := ParseAskInput(input)
			if err != nil {
				return nil, err
			}
			answer, err := service.Ask(request, ctx)
			if err != nil {
				return nil, fmt.Errorf("ask_user_question failed: %w", err)
			}
			return answerResult(answer), nil
		},
	}
}

func questionSchema() *typebox.Schema {
	return typebox.Object([]*typebox.Property{
		typebox.Prop("id", typebox.String(typebox.Description("Stable question id, unique within this call; answers reference it."))),
		typebox.Prop("header", typebox.String(typebox.Description("Short heading shown above the question."))),
		typebox.Prop("question", typebox.String(typebox.Description("The question text."))),
		typebox.Prop("multiSelect", typebox.Optional(typebox.Boolean(typebox.Description("Allow several selected options; requires options.")))),
		typebox.Prop("options", typebox.Optional(typebox.Array(optionSchema(), typebox.Description("Selectable choices; omit for a free-text answer.")))),
	})
}

func optionSchema() *typebox.Schema {
	return typebox.Object([]*typebox.Property{
		typebox.Prop("label", typebox.String(typebox.Description("Choice text the answer references."))),
		typebox.Prop("description", typebox.Optional(typebox.String(typebox.Description("One sentence explaining the tradeoff or impact.")))),
	})
}

// ParseAskInput converts the tool's JSON arguments into an AskRequest,
// rejecting shapes the shared validation cannot express: multiSelect
// without options, and empty or duplicate option labels.
func ParseAskInput(input any) (*AskRequest, error) {
	obj, ok := input.(*jsonx.Obj)
	if !ok {
		return nil, fmt.Errorf("ask_user_question input is invalid: the arguments must be a JSON object")
	}
	questionsValue, _ := obj.Get("questions")
	items, _ := questionsValue.([]any)
	request := &AskRequest{Questions: make([]Question, 0, len(items))}
	for _, item := range items {
		questionObj, ok := item.(*jsonx.Obj)
		if !ok {
			return nil, fmt.Errorf("ask_user_question input is invalid: every question must be an object")
		}
		question := Question{
			ID:       stringField(questionObj, "id"),
			Header:   stringField(questionObj, "header"),
			Question: stringField(questionObj, "question"),
		}
		if value, present := questionObj.Get("multiSelect"); present {
			if enabled, isBool := value.(bool); isBool {
				question.MultiSelect = enabled
			}
		}
		optionsValue, _ := questionObj.Get("options")
		optionItems, _ := optionsValue.([]any)
		for _, optionItem := range optionItems {
			optionObj, ok := optionItem.(*jsonx.Obj)
			if !ok {
				return nil, fmt.Errorf("ask_user_question input is invalid: every option must be an object")
			}
			question.Options = append(question.Options, Option{
				Label:       stringField(optionObj, "label"),
				Description: stringField(optionObj, "description"),
			})
		}
		request.Questions = append(request.Questions, question)
	}
	if err := ValidateAskRequest(request); err != nil {
		return nil, err
	}
	for _, question := range request.Questions {
		if question.MultiSelect && len(question.Options) == 0 {
			return nil, fmt.Errorf("ask_user_question input is invalid: question %q sets multiSelect without options", question.ID)
		}
		seen := map[string]bool{}
		for _, option := range question.Options {
			if option.Label == "" {
				return nil, fmt.Errorf("ask_user_question input is invalid: question %q has an option with an empty label", question.ID)
			}
			if seen[option.Label] {
				return nil, fmt.Errorf("ask_user_question input is invalid: question %q repeats option label %q", question.ID, option.Label)
			}
			seen[option.Label] = true
		}
	}
	return request, nil
}

// answerResult renders the answer for the model: one line per question
// in Content, the machine-readable form in StructuredContent.
func answerResult(answer *Answer) *agent.AgentToolResult {
	lines := make([]string, 0, len(answer.Items))
	items := make([]any, 0, len(answer.Items))
	for _, item := range answer.Items {
		lines = append(lines, fmt.Sprintf("%s: %s", item.QuestionID, strings.Join(item.Labels, ", ")))
		labels := make([]any, 0, len(item.Labels))
		for _, label := range item.Labels {
			labels = append(labels, label)
		}
		items = append(items, jsonx.ObjFrom("questionId", item.QuestionID, "labels", labels))
	}
	return &agent.AgentToolResult{
		Content:           []ai.ContentBlock{ai.TextContent{Text: strings.Join(lines, "\n")}},
		StructuredContent: jsonx.ObjFrom("answers", items),
	}
}

func stringField(obj *jsonx.Obj, key string) string {
	if value, present := obj.Get(key); present {
		if text, isStr := value.(string); isStr {
			return text
		}
	}
	return ""
}
