package runtime

// request_dispatch.go ports harness/runtime/lane.ts accept(): the
// request-kind dispatch that builds prompt messages before the AcceptRun
// core installs the operation. Skills and prompt templates resolve
// against the admission-time config resources (loaded values the
// application supplied at harness construction); loading from directories
// stays with harness.LoadSkills/LoadSourcedSkills in the caller.

import (
	"fmt"
	"time"

	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/jsonx"
)

// UnknownSkill mirrors the TS error: a skill name absent from resources.
type UnknownSkill struct {
	Name    string
	Message string
}

func (e *UnknownSkill) Error() string { return e.Message }

// UnknownTemplate mirrors the TS error: a template name absent from
// resources.
type UnknownTemplate struct {
	Name    string
	Message string
}

func (e *UnknownTemplate) Error() string { return e.Message }

// RunRequestKind discriminates the run request union.
type RunRequestKind string

const (
	RunRequestPrompt         RunRequestKind = "prompt"
	RunRequestSkill          RunRequestKind = "skill"
	RunRequestPromptTemplate RunRequestKind = "prompt_template"
)

// RunRequest is one run admission request. For the prompt kind either
// Messages (pre-built) or Prompt plus Images builds the user message.
type RunRequest struct {
	Kind RunRequestKind
	// Prompt is the prompt-kind text form.
	Prompt string
	// Messages carries pre-built agent messages for the prompt kind.
	Messages []*jsonx.Obj
	// Images appends image content blocks after the prompt text.
	Images []any
	// Name selects the skill or prompt template.
	Name string
	// AdditionalInstructions extends an invoked skill's content.
	AdditionalInstructions string
	// Args substitute into an invoked prompt template.
	Args []string
}

// AcceptRunRequest mirrors accept(): dispatch builds the request's
// messages (prompt text/images, skill expansion, template substitution),
// rejects unknown resource names and pending assistants, then installs
// the run via AcceptRun with the config's queue modes.
func AcceptRunRequest(
	lane *Lane,
	request RunRequest,
	nextID func(timestamp float64) string,
	ctx sessionCtxAlias,
) (*AcceptResult, error) {
	config := lane.readConfigSnapshot()
	if config == nil {
		return nil, fmt.Errorf("lane %q has no config source", lane.Name)
	}
	switch request.Kind {
	case RunRequestPrompt, RunRequestSkill, RunRequestPromptTemplate:
	default:
		// The reference union is closed; an unknown kind is a caller bug.
		return nil, fmt.Errorf("unknown run request kind: %s", request.Kind)
	}
	startedAt := float64(time.Now().UnixMilli())

	messages, reject := buildRunRequestMessages(request, config, startedAt)
	if reject != nil {
		return reject, nil
	}
	for _, message := range messages {
		if stringOfObjKey(message, "role") == "assistant" && stringOfObjKey(message, "stopReason") == "pending" {
			return &AcceptResult{Invalid: &InvalidMessage{Lane: lane.Name, Reason: "pending_assistant"}}, nil
		}
	}

	prompts := make([]AcceptPrompt, 0, len(messages))
	for _, message := range messages {
		prompts = append(prompts, AcceptPrompt{ID: nextID(startedAt), Message: message})
	}
	return AcceptRun(lane, prompts, config.SteeringMode, config.FollowUpMode, nextID, ctx)
}

// buildRunRequestMessages is the request-kind switch; a non-nil result
// rejects admission before any write.
func buildRunRequestMessages(request RunRequest, config *Config, startedAt float64) ([]*jsonx.Obj, *AcceptResult) {
	switch request.Kind {
	case RunRequestPrompt:
		if request.Messages != nil {
			return request.Messages, nil
		}
		content := make([]any, 0, len(request.Images)+1)
		if request.Prompt != "" {
			content = append(content, jsonx.ObjFrom("type", "text", "text", request.Prompt))
		}
		content = append(content, request.Images...)
		if len(content) == 0 {
			return nil, nil
		}
		return []*jsonx.Obj{userTextMessage(content, startedAt)}, nil

	case RunRequestSkill:
		var skill *harness.Skill
		for i := range config.Resources.Skills {
			if config.Resources.Skills[i].Name == request.Name {
				skill = &config.Resources.Skills[i]
				break
			}
		}
		if skill == nil {
			return nil, &AcceptResult{UnknownSkill: &UnknownSkill{
				Name: request.Name, Message: "Unknown skill: " + request.Name,
			}}
		}
		text := harness.FormatSkillInvocation(*skill, request.AdditionalInstructions)
		return []*jsonx.Obj{userTextMessage([]any{jsonx.ObjFrom("type", "text", "text", text)}, startedAt)}, nil

	case RunRequestPromptTemplate:
		var template *harness.PromptTemplate
		for i := range config.Resources.PromptTemplates {
			if config.Resources.PromptTemplates[i].Name == request.Name {
				template = &config.Resources.PromptTemplates[i]
				break
			}
		}
		if template == nil {
			return nil, &AcceptResult{UnknownTemplate: &UnknownTemplate{
				Name: request.Name, Message: "Unknown prompt template: " + request.Name,
			}}
		}
		content := harness.FormatPromptTemplateInvocation(*template, request.Args...)
		if content == "" {
			return nil, nil
		}
		return []*jsonx.Obj{userTextMessage([]any{jsonx.ObjFrom("type", "text", "text", content)}, startedAt)}, nil

	default:
		return nil, nil
	}
}

func userTextMessage(content []any, startedAt float64) *jsonx.Obj {
	return jsonx.ObjFrom("role", "user", "content", content, "timestamp", startedAt)
}
