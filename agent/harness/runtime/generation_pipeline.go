package runtime

// generation_pipeline.go assembles the ReadyProcedure pipeline:
// prepare -> intent -> perform (stream) -> publish, wiring the pieces the
// previous rounds ported. The perform stage consumes the ModelsRegistry
// stream and hands the settled message to the classification core.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// PipelineDeps carries the lane-facing dependencies the pipeline needs.
type PipelineDeps struct {
	Registry       *ModelsRegistry
	ActiveTools    []string
	AvailableTools []ToolDescriptor
	SystemPrompt   string
	// NextID mints uuidv7 ids.
	NextID func() string
	// ReadBounded reads the bounded context messages.
	ReadBounded func(lane *Lane, drive *Drive) ([]*jsonx.Obj, bool, error)
	// Perform consumes the provider stream and returns the settled
	// message (nil + error on failure).
	Perform func(lane *Lane, drive *Drive, prepared *PreparedGeneration, intent *GenerationIntent) (*jsonx.Obj, error)
	// ClassifyAndPublish runs the publishResponse core.
	ClassifyAndPublish func(lane *Lane, drive *Drive, at string, response *jsonx.Obj, intent *GenerationIntent, prepared *PreparedGeneration) (*ProcedureResult, error)
}

// RunReadyPipeline executes the assistant.ready leaf end-to-end.
func RunReadyPipeline(lane *Lane, drive *Drive, ready *session.OperationState, deps *PipelineDeps) (*ProcedureResult, error) {
	// Identity + tool names come from the generation context.
	identity, active := generationContextIdentity(ready)
	preparation, err := PrepareGeneration(
		deps.Registry, identity, active, deps.AvailableTools,
		func() ([]*jsonx.Obj, bool, error) {
			if deps.ReadBounded == nil {
				return nil, false, nil
			}
			return deps.ReadBounded(lane, drive)
		},
		func() (string, error) { return deps.SystemPrompt, nil },
		func() (*jsonx.Obj, error) { return nil, nil },
		nil,
	)
	if err != nil {
		return nil, err
	}
	switch preparation.Kind {
	case GenerationPrepFailure:
		return PublishConfigurationFailure(lane, drive, ready, preparation.Error)
	case GenerationPrepCancelled:
		return &ProcedureResult{Kind: "continue"}, nil
	}

	intent, err := PublishGenerationIntent(lane, drive, ready, preparation.Prepared.Model, deps.NextID)
	if err != nil {
		return nil, err
	}
	if intent == nil {
		return &ProcedureResult{Kind: "continue"}, nil
	}

	response, err := deps.Perform(lane, drive, preparation.Prepared, intent)
	if err != nil {
		return nil, err
	}
	return deps.ClassifyAndPublish(lane, drive, session.AtAssistantEffectPending, response, intent, preparation.Prepared)
}

func generationContextIdentity(ready *session.OperationState) (*jsonx.Obj, []string) {
	if ready.GenerationContext == nil {
		return nil, nil
	}
	configurationValue, ok := ready.GenerationContext.Get("configuration")
	if !ok {
		return nil, nil
	}
	configuration, ok := configurationValue.(*jsonx.Obj)
	if !ok {
		return nil, nil
	}
	identityValue, _ := configuration.Get("model")
	identity, _ := identityValue.(*jsonx.Obj)
	namesValue, _ := configuration.Get("activeToolNames")
	names, _ := namesValue.([]any)
	active := make([]string, 0, len(names))
	for _, name := range names {
		if s, ok := name.(string); ok {
			active = append(active, s)
		}
	}
	return identity, active
}

// InstallReadyPipeline wires RunReadyPipeline as the ReadyProcedure with
// the given dependencies.
func InstallReadyPipeline(deps *PipelineDeps) {
	ReadyProcedure = func(lane *Lane, drive *Drive, generation *session.OperationState) (*ProcedureResult, error) {
		return RunReadyPipeline(lane, drive, generation, deps)
	}
}
