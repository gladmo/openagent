package ai

// models.go ports the models.ts subset the agent closure uses: the Provider
// and Models/MutableModels registries, createModels, calculateCost, thinking
// level helpers. Auth resolution is simplified to the always-configured
// faux-style static providers the agent tests use (refresh/login/logout are
// out of the closure and return explicit errors); see PORTING.md.

import (
	"fmt"
	"sync"
)

// ProviderHeaders mirrors Record<string, string | null>.
type ProviderHeaders = map[string]*string

// Provider mirrors the chat-model subset of the TS Provider interface.
type Provider interface {
	ID() string
	Name() string
	Auth() ProviderAuth
	// GetModels returns the current known chat models (must not throw).
	GetModels() []*Model
	// Stream streams a normalized transcript.
	Stream(model *Model, context *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream
	// StreamSimple streams with unified options.
	StreamSimple(model *Model, context *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream
	// FetchDeferred polls a deferred response, when supported.
	FetchDeferred(model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream
	// CancelDeferred cancels a deferred response, when supported.
	CancelDeferred(model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error
}

// optionalProvider marks the deferred capabilities as optional via interface
// upgrade, mirroring the TS optional methods.
type deferredCapableProvider interface {
	FetchDeferred(model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream
	CancelDeferred(model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error
}

// Models mirrors the read side of the TS interface.
type Models interface {
	GetProviders() []Provider
	GetProvider(id string) Provider
	GetModels(provider ...string) []*Model
	GetModel(provider, id string) *Model
	// Stream normalizes the caller's Context before dispatching.
	Stream(model *Model, context Context, options *StreamOptions) *AssistantMessageEventStream
	StreamSimple(model *Model, context Context, options *SimpleStreamOptions) *AssistantMessageEventStream
	Complete(model *Model, context Context, options *StreamOptions) (*AssistantMessage, error)
	CompleteSimple(model *Model, context Context, options *SimpleStreamOptions) (*AssistantMessage, error)
	FetchDeferred(model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream
	CancelDeferred(model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error
	// GetAuth resolves provider-scoped auth by provider id, or provider auth
	// plus static model headers when passed a model.
	GetAuth(modelOrProvider any) (*AuthResult, bool)
}

// MutableModels adds the write side.
type MutableModels interface {
	Models
	SetProvider(provider Provider)
	DeleteProvider(id string)
	ClearProviders()
}

// modelsImpl implements MutableModels.
type modelsImpl struct {
	mu        sync.RWMutex
	providers map[string]Provider

	credentials CredentialStore
	authContext AuthContext
}

// CreateModelsOptions mirrors the TS interface: credential/auth-context
// plumbing. Zero values default to an in-memory store and the process env.
type CreateModelsOptions struct {
	Credentials CredentialStore
	AuthContext AuthContext
}

// CreateModels builds a Models registry.
func CreateModels(options ...CreateModelsOptions) MutableModels {
	creds := CredentialStore(NewInMemoryCredentialStore())
	authCtx := AuthContext(DefaultAuthContext())
	if len(options) > 0 {
		if options[0].Credentials != nil {
			creds = options[0].Credentials
		}
		if options[0].AuthContext != nil {
			authCtx = options[0].AuthContext
		}
	}
	return &modelsImpl{providers: map[string]Provider{}, credentials: creds, authContext: authCtx}
}

func (m *modelsImpl) SetProvider(provider Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[provider.ID()] = provider
}

func (m *modelsImpl) DeleteProvider(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.providers, id)
}

func (m *modelsImpl) ClearProviders() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers = map[string]Provider{}
}

func (m *modelsImpl) GetProviders() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Provider, 0, len(m.providers))
	for _, p := range m.providers {
		out = append(out, p)
	}
	return out
}

func (m *modelsImpl) GetProvider(id string) Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.providers[id]
}

func (m *modelsImpl) GetModels(provider ...string) []*Model {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(provider) > 0 && provider[0] != "" {
		if p, ok := m.providers[provider[0]]; ok {
			return p.GetModels()
		}
		return nil
	}
	var out []*Model
	for _, p := range m.providers {
		out = append(out, p.GetModels()...)
	}
	return out
}

func (m *modelsImpl) GetModel(provider, id string) *Model {
	for _, model := range m.GetModels(provider) {
		if model.ID == id {
			return model
		}
	}
	return nil
}

func providerForModel(m *modelsImpl, model *Model) (Provider, error) {
	p := m.GetProvider(model.Provider)
	if p == nil {
		return nil, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s is not configured", model.Provider), nil)
	}
	for _, known := range p.GetModels() {
		if known.ID == model.ID {
			return p, nil
		}
	}
	return nil, NewModelsError(ModelsErrorCodeProvider, fmt.Sprintf("Provider %s has no model %s", model.Provider, model.ID), nil)
}

// applyAuth ports ModelsImpl.applyAuth: resolve provider auth, then merge
// per-field (explicit request options win) into the request model/options.
func (m *modelsImpl) applyAuth(model *Model, options *StreamOptions) (*Model, *StreamOptions, error) {
	provider, err := providerForModel(m, model)
	if err != nil {
		return nil, nil, err
	}
	resolution, err := ResolveProviderAuth(provider, m.credentials, m.authContext, &AuthResolutionOverrides{
		APIKey: options.APIKey,
		Env:    options.Env,
		Signal: options.Signal,
	})
	if err != nil {
		return nil, nil, err
	}
	if resolution == nil {
		return nil, nil, NewModelsError(ModelsErrorCodeAuth, "Provider is not configured: "+model.Provider, nil)
	}
	auth := resolution.Auth

	apiKey := options.APIKey
	if apiKey == nil {
		apiKey = auth.APIKey
	}
	headers := MergeHeaders(auth.Headers, options.Headers)
	var env ProviderEnv
	if resolution.Env != nil || options.Env != nil {
		env = ProviderEnv{}
		for name, value := range resolution.Env {
			env[name] = value
		}
		for name, value := range options.Env {
			env[name] = value
		}
	}
	requestModel := model
	if auth.BaseURL != nil && *auth.BaseURL != "" {
		copyModel := *model
		copyModel.BaseURL = *auth.BaseURL
		requestModel = &copyModel
	}
	requestOptions := *options
	requestOptions.APIKey = apiKey
	requestOptions.Headers = headers
	requestOptions.Env = env
	return requestModel, &requestOptions, nil
}

func (m *modelsImpl) Stream(model *Model, context Context, options *StreamOptions) *AssistantMessageEventStream {
	transcript := NormalizeContext(context)
	requestModel, requestOptions, err := m.applyAuth(model, options)
	if err != nil {
		return errorStream(err)
	}
	provider, err := providerForModel(m, requestModel)
	if err != nil {
		return errorStream(err)
	}
	return provider.Stream(requestModel, transcript, requestOptions)
}

func (m *modelsImpl) StreamSimple(model *Model, context Context, options *SimpleStreamOptions) *AssistantMessageEventStream {
	transcript := NormalizeContext(context)
	base := &StreamOptions{}
	if options != nil {
		base = &options.StreamOptions
	}
	requestModel, requestOptions, err := m.applyAuth(model, base)
	if err != nil {
		return errorStream(err)
	}
	provider, err := providerForModel(m, requestModel)
	if err != nil {
		return errorStream(err)
	}
	if options == nil {
		return provider.StreamSimple(requestModel, transcript, &SimpleStreamOptions{StreamOptions: *requestOptions})
	}
	merged := *options
	merged.StreamOptions = *requestOptions
	return provider.StreamSimple(requestModel, transcript, &merged)
}

func (m *modelsImpl) Complete(model *Model, context Context, options *StreamOptions) (*AssistantMessage, error) {
	return m.Stream(model, context, options).Result(), nil
}

func (m *modelsImpl) CompleteSimple(model *Model, context Context, options *SimpleStreamOptions) (*AssistantMessage, error) {
	return m.StreamSimple(model, context, options).Result(), nil
}

func (m *modelsImpl) FetchDeferred(model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream {
	provider, err := providerForModel(m, model)
	if err != nil {
		return errorStream(err)
	}
	capable, ok := provider.(deferredCapableProvider)
	if !ok {
		return errorStream(NewModelsError(ModelsErrorCodeStream, fmt.Sprintf("Provider %s does not support deferred responses", model.Provider), nil))
	}
	return capable.FetchDeferred(model, handle, options)
}

func (m *modelsImpl) CancelDeferred(model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error {
	provider, err := providerForModel(m, model)
	if err != nil {
		return err
	}
	capable, ok := provider.(deferredCapableProvider)
	if !ok {
		return NewModelsError(ModelsErrorCodeStream, fmt.Sprintf("Provider %s does not support deferred responses", model.Provider), nil)
	}
	return capable.CancelDeferred(model, handle, options)
}

func (m *modelsImpl) GetAuth(modelOrProvider any) (*AuthResult, bool) {
	var providerID string
	switch t := modelOrProvider.(type) {
	case *Model:
		providerID = t.Provider
	case Provider:
		return m.resolveProviderAuth(t)
	case string:
		providerID = t
	default:
		return nil, false
	}
	if p := m.GetProvider(providerID); p != nil {
		return m.resolveProviderAuth(p)
	}
	return nil, false
}

func (m *modelsImpl) resolveProviderAuth(provider Provider) (*AuthResult, bool) {
	result, err := ResolveProviderAuth(provider, m.credentials, m.authContext, nil)
	if err != nil {
		return nil, false
	}
	return result, result != nil
}

// errorStream returns a stream that terminates immediately with an error
// event (the stream contract: failures are encoded in the stream).
func errorStream(err error) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStream()
	msg := FauxAssistantMessage("")
	msg.StopReason = StopError
	message := err.Error()
	msg.ErrorMessage = &message
	stream.Push(&EventError{Reason: StopError, Error: msg})
	return stream
}

// ---------------------------------------------------------------------------
// Cost and thinking-level helpers
// ---------------------------------------------------------------------------

// HasApi reports whether the chat model uses the given API.
func HasApi(model *Model, api string) bool { return model != nil && model.API == api }

// CalculateCost fills usage.Cost from the model's pricing (tier selection;
// Anthropic 2x rate for 1h cache writes).
func CalculateCost(model *Model, usage *Usage) UsageCost {
	inputTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	rates := model.Cost.ModelCostRates
	matchedThreshold := -1.0
	for _, tier := range model.Cost.Tiers {
		if inputTokens > tier.InputTokensAbove && tier.InputTokensAbove > matchedThreshold {
			rates = tier.ModelCostRates
			matchedThreshold = tier.InputTokensAbove
		}
	}
	longWrite := 0.0
	if usage.CacheWrite1h != nil {
		longWrite = *usage.CacheWrite1h
	}
	shortWrite := usage.CacheWrite - longWrite
	usage.Cost.Input = (rates.Input / 1000000) * usage.Input
	usage.Cost.Output = (rates.Output / 1000000) * usage.Output
	usage.Cost.CacheRead = (rates.CacheRead / 1000000) * usage.CacheRead
	usage.Cost.CacheWrite = (rates.CacheWrite*shortWrite + rates.Input*2*longWrite) / 1000000
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
	return usage.Cost
}

var extendedThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// GetSupportedThinkingLevels returns the levels the model supports.
func GetSupportedThinkingLevels(model *Model) []string {
	if !model.Reasoning {
		return []string{ThinkingOff}
	}
	out := []string{}
	for _, level := range extendedThinkingLevels {
		mapped, hasMap := model.ThinkingLevelMap[level]
		if hasMap && mapped == nil {
			continue // null marks unsupported
		}
		if level == "xhigh" || level == "max" {
			if !hasMap {
				continue
			}
		}
		out = append(out, level)
	}
	return out
}

// ClampThinkingLevel maps a requested level to the closest supported one.
func ClampThinkingLevel(model *Model, level string) string {
	available := GetSupportedThinkingLevels(model)
	for _, candidate := range available {
		if candidate == level {
			return level
		}
	}
	requestedIndex := -1
	for i, candidate := range extendedThinkingLevels {
		if candidate == level {
			requestedIndex = i
			break
		}
	}
	if requestedIndex == -1 {
		if len(available) > 0 {
			return available[0]
		}
		return ThinkingOff
	}
	for i := requestedIndex; i < len(extendedThinkingLevels); i++ {
		if containsLevel(available, extendedThinkingLevels[i]) {
			return extendedThinkingLevels[i]
		}
	}
	for i := requestedIndex - 1; i >= 0; i-- {
		if containsLevel(available, extendedThinkingLevels[i]) {
			return extendedThinkingLevels[i]
		}
	}
	if len(available) > 0 {
		return available[0]
	}
	return ThinkingOff
}

func containsLevel(levels []string, level string) bool {
	for _, candidate := range levels {
		if candidate == level {
			return true
		}
	}
	return false
}

// ModelsAreEqual compares type (always chat here), id and provider.
func ModelsAreEqual(a, b *Model) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.Provider == b.Provider
}
