package ai

// provider_core.go ports models.ts createProvider: it assembles provider
// definitions (id/name/baseUrl/auth/models plus one API implementation or an
// API-per-model map) into a Provider, dispatching each stream call to the
// implementation registered for the model's API. The TS lazy-module loading
// has no Go equivalent (clients are plain packages); dynamic fetchModels /
// refreshModels and the images/classifier slots are out of this port's
// scope.

import (
	"fmt"
	"sync"
)

// ProviderStreams is the API-level stream capability set (TS ProviderStreams
// minus the optional deferred/images members).
type ProviderStreams interface {
	Stream(model *Model, context *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream
	StreamSimple(model *Model, context *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream
}

// DeferredProviderStreams adds the deferred-response capabilities.
type DeferredProviderStreams interface {
	ProviderStreams
	FetchDeferred(model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream
	CancelDeferred(model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error
}

// CreateProviderOptions mirrors the TS input (chat subset).
type CreateProviderOptions struct {
	ID      string
	Name    string
	BaseURL string
	Headers ProviderHeaders
	Auth    ProviderAuth
	Models  []*Model
	// API is the single implementation used for every model.
	API ProviderStreams
	// APIByModel selects the implementation per model.API (multi-API
	// providers such as openrouter).
	APIByModel map[string]ProviderStreams
}

type providerInstance struct {
	mu       sync.RWMutex
	id       string
	name     string
	baseURL  string
	headers  ProviderHeaders
	auth     ProviderAuth
	models   []*Model
	single   ProviderStreams
	byAPI    map[string]ProviderStreams
	deferred bool
}

// NewProvider ports createProvider. Panics when no stream implementation at
// all is provided (TS throws synchronously).
func NewProvider(input CreateProviderOptions) Provider {
	single := input.API
	byAPI := input.APIByModel
	streams := []ProviderStreams{}
	if single != nil {
		streams = append(streams, single)
	}
	for _, entry := range byAPI {
		if entry != nil {
			streams = append(streams, entry)
		}
	}
	if len(streams) == 0 {
		panic(fmt.Sprintf("Provider %s: at least one of \"api\", \"images\", or \"classifiers\" is required.", input.ID))
	}
	deferred := false
	for _, entry := range streams {
		if _, ok := entry.(DeferredProviderStreams); ok {
			deferred = true
			break
		}
	}
	name := input.Name
	if name == "" {
		name = input.ID
	}
	models := input.Models
	if models == nil {
		models = []*Model{}
	}
	return &providerInstance{
		id:       input.ID,
		name:     name,
		baseURL:  input.BaseURL,
		headers:  input.Headers,
		auth:     input.Auth,
		models:   models,
		single:   single,
		byAPI:    byAPI,
		deferred: deferred,
	}
}

// ID implements Provider.
func (p *providerInstance) ID() string { return p.id }

// Name implements Provider.
func (p *providerInstance) Name() string { return p.name }

// BaseURL returns the provider base URL ("" when unset).
func (p *providerInstance) BaseURL() string { return p.baseURL }

// Headers returns the provider-level static headers.
func (p *providerInstance) Headers() ProviderHeaders { return p.headers }

// Auth implements Provider.
func (p *providerInstance) Auth() ProviderAuth { return p.auth }

// GetModels implements Provider.
func (p *providerInstance) GetModels() []*Model {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Model, len(p.models))
	copy(out, p.models)
	return out
}

// SetModels replaces the known model list (dynamic providers).
func (p *providerInstance) SetModels(models []*Model) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.models = models
}

func (p *providerInstance) apiFor(model *Model) ProviderStreams {
	if p.single != nil {
		return p.single
	}
	return p.byAPI[model.API]
}

func (p *providerInstance) dispatchStream(model *Model, run func(streams ProviderStreams) *AssistantMessageEventStream) *AssistantMessageEventStream {
	streams := p.apiFor(model)
	if streams == nil {
		return errorStream(NewModelsError(ModelsErrorCodeStream,
			fmt.Sprintf("Provider %s has no API implementation for \"%s\"", p.id, model.API), nil))
	}
	return run(streams)
}

// Stream implements Provider.
func (p *providerInstance) Stream(model *Model, context *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream {
	return p.dispatchStream(model, func(streams ProviderStreams) *AssistantMessageEventStream {
		return streams.Stream(model, context, options)
	})
}

// StreamSimple implements Provider.
func (p *providerInstance) StreamSimple(model *Model, context *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
	return p.dispatchStream(model, func(streams ProviderStreams) *AssistantMessageEventStream {
		return streams.StreamSimple(model, context, options)
	})
}

// FetchDeferred implements the optional deferred capability.
func (p *providerInstance) FetchDeferred(model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream {
	implementation, ok := p.apiFor(model).(DeferredProviderStreams)
	if !ok {
		return errorStream(NewModelsError(ModelsErrorCodeProvider,
			fmt.Sprintf("Provider %s does not support deferred responses for \"%s\"", p.id, model.API), nil))
	}
	return implementation.FetchDeferred(model, handle, options)
}

// CancelDeferred implements the optional deferred capability.
func (p *providerInstance) CancelDeferred(model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error {
	implementation, ok := p.apiFor(model).(DeferredProviderStreams)
	if !ok {
		return NewModelsError(ModelsErrorCodeProvider,
			fmt.Sprintf("Provider %s cannot cancel deferred responses for \"%s\"", p.id, model.API), nil)
	}
	return implementation.CancelDeferred(model, handle, options)
}
