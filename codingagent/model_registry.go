package codingagent

// Port of core/model-registry.ts: the synchronous compatibility facade over
// ModelRuntime that extensions consume.

import (
	"github.com/gladmo/openagent/ai"
)

// ModelRegistry is the extension-facing facade; internals use ModelRuntime.
type ModelRegistry struct {
	runtime *ModelRuntime
}

// NewModelRegistry wraps a runtime.
func NewModelRegistry(runtime *ModelRuntime) *ModelRegistry {
	return &ModelRegistry{runtime: runtime}
}

// GetError returns the refresh error ("" when healthy).
func (r *ModelRegistry) GetError() string { return r.runtime.GetError() }

// GetAll returns every registered chat model.
func (r *ModelRegistry) GetAll() []*ai.Model { return r.runtime.GetModels() }

// GetAvailable returns models whose providers are configured.
func (r *ModelRegistry) GetAvailable() []*ai.Model { return r.runtime.GetAvailableSnapshot() }

// Find looks up one model.
func (r *ModelRegistry) Find(provider, modelID string) *ai.Model {
	return r.runtime.GetModel(provider, modelID)
}

// HasConfiguredAuth reports usable auth for a model's provider.
func (r *ModelRegistry) HasConfiguredAuth(model *ai.Model) bool {
	return r.runtime.HasConfiguredAuth(model.Provider)
}

// GetProviderAuthStatus reports how a provider's auth is configured.
func (r *ModelRegistry) GetProviderAuthStatus(provider string) AuthStatus {
	return r.runtime.GetProviderAuthStatus(provider)
}

// GetProvider returns one composed provider.
func (r *ModelRegistry) GetProvider(provider string) ai.Provider {
	return r.runtime.GetProvider(provider)
}

// Stream streams through the composed provider.
func (r *ModelRegistry) Stream(model *ai.Model, context ai.Context, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	return r.runtime.Stream(model, context, options)
}

// StreamSimple streams with unified options.
func (r *ModelRegistry) StreamSimple(model *ai.Model, context ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	return r.runtime.StreamSimple(model, context, options)
}

// Complete resolves one full completion.
func (r *ModelRegistry) Complete(model *ai.Model, context ai.Context, options *ai.StreamOptions) (*ai.AssistantMessage, error) {
	return r.runtime.Complete(model, context, options)
}

// GetProviderDisplayName resolves the provider's display name.
func (r *ModelRegistry) GetProviderDisplayName(provider string) string {
	return r.runtime.GetProviderDisplayName(provider)
}

// GetProviderAuth resolves provider auth.
func (r *ModelRegistry) GetProviderAuth(provider string) (*ai.AuthResult, bool) {
	return r.runtime.GetAuth(provider)
}

// GetApiKeyForProvider resolves the provider's api key.
func (r *ModelRegistry) GetApiKeyForProvider(provider string) (string, bool) {
	return r.runtime.GetApiKeyForProvider(provider)
}

// IsUsingOAuth reports OAuth auth for a model's provider.
func (r *ModelRegistry) IsUsingOAuth(model *ai.Model) bool {
	return r.runtime.IsUsingOAuth(model.Provider)
}

// RegisterProvider registers an extension provider by config.
func (r *ModelRegistry) RegisterProvider(providerName string, config *ProviderConfigInput) error {
	return r.runtime.RegisterProvider(providerName, config)
}

// RegisterNativeProvider registers a precomposed provider.
func (r *ModelRegistry) RegisterNativeProvider(provider ai.Provider) {
	r.runtime.RegisterNativeProvider(provider)
}

// UnregisterProvider removes an extension registration.
func (r *ModelRegistry) UnregisterProvider(providerName string) {
	r.runtime.UnregisterProvider(providerName)
}

// GetRegisteredProviderConfig returns the extension config.
func (r *ModelRegistry) GetRegisteredProviderConfig(providerName string) *ProviderConfigInput {
	return r.runtime.GetRegisteredProviderConfig(providerName)
}

// GetRegisteredNativeProvider returns the native registration.
func (r *ModelRegistry) GetRegisteredNativeProvider(providerName string) ai.Provider {
	return r.runtime.GetRegisteredNativeProvider(providerName)
}

// GetRegisteredProviderIDs lists extension registrations.
func (r *ModelRegistry) GetRegisteredProviderIDs() []string {
	return r.runtime.GetRegisteredProviderIDs()
}

// Runtime exposes the backing runtime.
func (r *ModelRegistry) Runtime() *ModelRuntime { return r.runtime }
