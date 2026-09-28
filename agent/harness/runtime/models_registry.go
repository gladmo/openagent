package runtime

// models_registry.go ports the lane's models surface (lane.models in
// types.ts): resolve a model by lane configuration identity, and expose
// the streaming/deferred operations the drive procedures call. The
// registry wraps the ai Models registry so the drive procedures have one
// injection point.

import (
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// ModelsRegistry is the lane-facing models surface.
type ModelsRegistry struct {
	models ai.Models
}

// NewModelsRegistry wraps an ai models registry.
func NewModelsRegistry(models ai.Models) *ModelsRegistry {
	return &ModelsRegistry{models: models}
}

// Resolve mirrors lane.models.resolve(identity): the registered model for
// a {provider, modelId} identity, nil when absent.
func (r *ModelsRegistry) Resolve(identity *jsonx.Obj) *ai.Model {
	if r == nil || r.models == nil || identity == nil {
		return nil
	}
	providerValue, _ := identity.Get("provider")
	modelIDValue, _ := identity.Get("modelId")
	provider, pok := providerValue.(string)
	modelID, mok := modelIDValue.(string)
	if !pok || !mok {
		return nil
	}
	return r.models.GetModel(provider, modelID)
}

// ContextWindowOf exposes the model's context window (0 when unknown).
func ContextWindowOf(model *ai.Model) float64 {
	if model == nil {
		return 0
	}
	return model.ContextWindow
}

// MaxTokensOf exposes the model's max output tokens (0 when unknown).
func MaxTokensOf(model *ai.Model) float64 {
	if model == nil {
		return 0
	}
	return model.MaxTokens
}

// StreamRequest carries one assistant stream call.
type StreamRequest struct {
	Messages      []ai.Message
	ThinkingLevel string
}

// StreamEvent relays one assistant stream event to the caller.
type StreamEvent struct {
	// Done carries the terminal done event.
	Done *ai.EventDone
	// Frame carries one incremental event.
	Frame *ai.AssistantMessageEvent
	// Err carries a stream failure.
	Err error
}

// Stream mirrors models.stream: consumes the provider event stream and
// relays events on a channel; the terminal done event closes it.
func (r *ModelsRegistry) Stream(model *ai.Model, request StreamRequest, options *ai.StreamOptions) (<-chan StreamEvent, error) {
	if r == nil || r.models == nil || model == nil {
		return nil, &noModelError{}
	}
	stream := r.models.Stream(model, ai.Context{Messages: request.Messages}, options)
	events := make(chan StreamEvent, 64)
	go func() {
		defer close(events)
		for {
			event, ok := stream.Next()
			if !ok {
				return
			}
			switch t := event.(type) {
			case *ai.EventDone:
				events <- StreamEvent{Done: t}
				return
			case *ai.EventError:
				events <- StreamEvent{Err: &providerStreamError{message: t.Error}}
				return
			default:
				events <- StreamEvent{Frame: &t}
			}
		}
	}()
	return events, nil
}

type providerStreamError struct{ message *ai.AssistantMessage }

func (e *providerStreamError) Error() string { return "provider stream error" }

// DeferredResult mirrors fetchDeferred's union.
type DeferredResult struct {
	// StillDeferred carries the new handle when the poll defers again.
	StillDeferred *ai.DeferredHandle
	// Message carries the settled assistant message.
	Message *ai.AssistantMessage
}

// FetchDeferred mirrors models.fetchDeferred: poll one deferred handle;
// the result stream settles to an assistant message whose deferred field
// carries a new handle when still deferred.
func (r *ModelsRegistry) FetchDeferred(model *ai.Model, handle *ai.DeferredHandle, options *ai.DeferredFetchOptions) (*DeferredResult, error) {
	if r == nil || r.models == nil || model == nil || handle == nil {
		return nil, &noModelError{}
	}
	stream := r.models.FetchDeferred(model, handle, options)
	message := stream.Result()
	if message == nil {
		return nil, &noModelError{}
	}
	if message.Deferred != nil {
		return &DeferredResult{StillDeferred: message.Deferred}, nil
	}
	return &DeferredResult{Message: message}, nil
}

// CancelDeferred mirrors models.cancelDeferred (best-effort).
func (r *ModelsRegistry) CancelDeferred(model *ai.Model, handle *ai.DeferredHandle, options *ai.DeferredCancelOptions) error {
	if r == nil || r.models == nil || model == nil || handle == nil {
		return &noModelError{}
	}
	return r.models.CancelDeferred(model, handle, options)
}

type noModelError struct{}

func (e *noModelError) Error() string { return "model unavailable" }
