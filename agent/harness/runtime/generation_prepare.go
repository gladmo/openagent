package runtime

// generation_prepare.go ports harness/runtime/drive/generation.ts's
// prepareGeneration: model resolution, the configured-tools check, the
// bounded-context read, the system-prompt resolution, and the
// before-request patch application.

import (
	"fmt"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// GenerationPreparationKind discriminates the preparation.
type GenerationPreparationKind string

const (
	GenerationPrepReady     GenerationPreparationKind = "ready"
	GenerationPrepFailure   GenerationPreparationKind = "configuration_failure"
	GenerationPrepCancelled GenerationPreparationKind = "cancel_requested"
)

// GenerationConfigurationError mirrors configurationError.
func GenerationConfigurationError(code string, identity *jsonx.Obj) *session.OperationError {
	message := "The configured model is unavailable in this process"
	if code == "configured_tools_unavailable" {
		message = "One or more configured tools are unavailable in this process"
	}
	var details any
	if code == "model_unavailable" {
		details = identity
	}
	return &session.OperationError{Code: code, Message: message, Details: details}
}

// PreparedGeneration carries the ready preparation.
type PreparedGeneration struct {
	Model         *ai.Model
	Messages      []*jsonx.Obj
	SystemPrompt  string
	Tools         []*jsonx.Obj
	StreamOptions *jsonx.Obj
}

// GenerationPreparation is the preparation union.
type GenerationPreparation struct {
	Kind     GenerationPreparationKind
	Error    *session.OperationError
	Prepared *PreparedGeneration
}

// ToolDescriptor is the harness tool surface used by preparation.
type ToolDescriptor struct {
	Name        string
	Description string
	Parameters  *jsonx.Obj
}

// PrepareGeneration mirrors prepareGeneration's decision sequence.
func PrepareGeneration(
	registry *ModelsRegistry,
	identity *jsonx.Obj,
	activeToolNames []string,
	availableTools []ToolDescriptor,
	boundedMessages func() ([]*jsonx.Obj, bool, error),
	systemPrompt func() (string, error),
	beforeRequest func() (*jsonx.Obj, error),
	baseStreamOptions *jsonx.Obj,
) (*GenerationPreparation, error) {
	model := registry.Resolve(identity)
	if model == nil {
		return &GenerationPreparation{
			Kind:  GenerationPrepFailure,
			Error: GenerationConfigurationError("model_unavailable", identity),
		}, nil
	}
	// Configured-tools check.
	toolsByName := map[string]ToolDescriptor{}
	for _, tool := range availableTools {
		toolsByName[tool.Name] = tool
	}
	var missing []string
	for _, name := range activeToolNames {
		if _, ok := toolsByName[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		missingObj := jsonx.NewObj()
		toolsArr := make([]any, 0, len(missing))
		for _, name := range missing {
			toolsArr = append(toolsArr, name)
		}
		missingObj.Set("tools", toolsArr)
		return &GenerationPreparation{
			Kind: GenerationPrepFailure,
			Error: &session.OperationError{
				Code:    "configured_tools_unavailable",
				Message: "One or more configured tools are unavailable in this process",
				Details: missingObj,
			},
		}, nil
	}
	// Bounded context.
	messages, cancelled, err := boundedMessages()
	if err != nil {
		return nil, err
	}
	if cancelled {
		return &GenerationPreparation{Kind: GenerationPrepCancelled}, nil
	}
	prompt, err := systemPrompt()
	if err != nil {
		return nil, err
	}
	// before_request patch.
	streamOptions := baseStreamOptions
	if beforeRequest != nil {
		patch, err := beforeRequest()
		if err != nil {
			return nil, err
		}
		if patch != nil {
			streamOptions = applyStreamOptionsPatchJSON(baseStreamOptions, patch)
		}
	}
	// Tool projections.
	tools := make([]*jsonx.Obj, 0, len(activeToolNames))
	for _, name := range activeToolNames {
		tool, ok := toolsByName[name]
		if !ok {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Configured tool %s disappeared during resolution", name)}
		}
		toolObj := jsonx.NewObj()
		toolObj.Set("name", tool.Name)
		toolObj.Set("description", tool.Description)
		if tool.Parameters != nil {
			toolObj.Set("parameters", tool.Parameters)
		}
		tools = append(tools, toolObj)
	}
	return &GenerationPreparation{
		Kind: GenerationPrepReady,
		Prepared: &PreparedGeneration{
			Model:         model,
			Messages:      messages,
			SystemPrompt:  prompt,
			Tools:         tools,
			StreamOptions: streamOptions,
		},
	}, nil
}

// applyStreamOptionsPatchJSON merges a before-request patch over the base
// options (patch keys win; a null clears).
func applyStreamOptionsPatchJSON(base, patch *jsonx.Obj) *jsonx.Obj {
	if base == nil {
		return patch
	}
	merged := jsonx.NewObj()
	for _, key := range base.Keys() {
		value, _ := base.Get(key)
		merged.Set(key, value)
	}
	for _, key := range patch.Keys() {
		value, _ := patch.Get(key)
		if value == nil {
			merged.Delete(key)
			continue
		}
		merged.Set(key, value)
	}
	return merged
}
