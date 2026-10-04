package codingagent

// Port of core/model-runtime.ts: the configured pi-ai Models collection
// used by coding-agent. Composes builtin + models.json + extension layers
// via provider-composer, tracks availability snapshots, and exposes the
// registry facade surface.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/gladmo/openagent/ai"
)

// CreateModelRuntimeOptions construct the runtime.
type CreateModelRuntimeOptions struct {
	// Credentials overrides credential storage (defaults to file at
	// AuthPath).
	Credentials     ai.CredentialStore
	AuthPath        string
	ModelsPath      string
	HasModelsPath   bool
	ModelsStore     ai.ModelsStore
	ModelsStorePath string
	// AllowModelNetwork permits catalog refreshes over the network.
	AllowModelNetwork bool
	// ModelRefreshTimeoutMs bounds the create-time refresh.
	ModelRefreshTimeoutMs int
	CatalogBaseUrl        string
	// RefreshOnCreate skips the initial refresh when false (default true).
	RefreshOnCreate *bool
}

// ModelRuntimeSnapshot is the cached model/availability view.
type ModelRuntimeSnapshot struct {
	All                 []*ai.Model
	Available           []*ai.Model
	ConfiguredProviders map[string]bool
	StoredProviders     map[string]bool
}

// ModelRuntime is the configured Models collection.
type ModelRuntime struct {
	mu sync.RWMutex

	models      ai.MutableModels
	credentials *RuntimeCredentials

	defaultBuiltins map[string]ai.Provider
	builtins        map[string]ai.Provider

	nativeExtensionProviders map[string]ai.Provider
	extensionProviders       map[string]*ProviderConfigInput
	compositionErrors        map[string]string

	modelsPath          string
	modelNetworkEnabled bool
	config              *ModelConfig
	modelsStore         ai.ModelsStore

	snapshot ModelRuntimeSnapshot

	availabilityError string
}

// NewModelRuntime builds the runtime (synchronous form of create(); the
// initial availability refresh runs under Refresh).
func NewModelRuntime(options *CreateModelRuntimeOptions) (*ModelRuntime, error) {
	if options == nil {
		options = &CreateModelRuntimeOptions{}
	}

	var credentialStore ai.CredentialStore
	if options.Credentials != nil {
		credentialStore = options.Credentials
	} else {
		credentialStore = NewAuthStorage(options.AuthPath, nil)
	}
	credentials := NewRuntimeCredentials(credentialStore)

	modelsPath := ""
	if options.HasModelsPath {
		modelsPath = options.ModelsPath
	} else if options.ModelsPath != "" {
		modelsPath = options.ModelsPath
	} else {
		modelsPath = GetModelsPath()
	}

	config := LoadModelConfig(modelsPath)

	modelsStore := options.ModelsStore
	if modelsStore == nil {
		if modelsPath != "" {
			storePath := options.ModelsStorePath
			if storePath == "" {
				storePath = filepath.Join(filepath.Dir(modelsPath), "models-store.json")
			}
			modelsStore = NewFileModelsStore(storePath)
		} else {
			modelsStore = NewInMemoryCodingAgentModelsStore()
		}
	}

	providers := ai.BuiltinProviders()
	defaultBuiltins := map[string]ai.Provider{}
	for _, provider := range providers {
		defaultBuiltins[provider.ID()] = provider
	}

	modelNetworkEnabled := osGetenv("PI_OFFLINE") == ""

	runtime := &ModelRuntime{
		credentials:              credentials,
		config:                   config,
		modelsPath:               modelsPath,
		modelNetworkEnabled:      modelNetworkEnabled,
		defaultBuiltins:          defaultBuiltins,
		builtins:                 map[string]ai.Provider{},
		nativeExtensionProviders: map[string]ai.Provider{},
		extensionProviders:       map[string]*ProviderConfigInput{},
		compositionErrors:        map[string]string{},
		snapshot: ModelRuntimeSnapshot{
			ConfiguredProviders: map[string]bool{},
			StoredProviders:     map[string]bool{},
		},
	}
	runtime.models = ai.CreateModels(ai.CreateModelsOptions{
		Credentials: credentials,
	})
	runtime.modelsStore = modelsStore
	runtime.configureRadiusProviders()
	runtime.rebuildProviders()
	return runtime, nil
}

func osGetenv(name string) string { return envLookup(name) }

func (r *ModelRuntime) configureRadiusProviders() {
	r.builtins = map[string]ai.Provider{}
	for id, provider := range r.defaultBuiltins {
		r.builtins[id] = provider
	}
	for _, providerID := range r.config.GetProviderIDs() {
		config := r.config.GetProvider(providerID)
		if config == nil || config.OAuth != "radius" || config.BaseURL == "" {
			continue
		}
		name := config.Name
		if name == "" {
			name = providerID
		}
		gateway := strings.TrimSuffix(strings.TrimSuffix(config.BaseURL, "/v1/"), "/v1")
		if provider := aiRadiusProvider(providerID, name, gateway); provider != nil {
			r.builtins[providerID] = provider
		}
	}
}

func (r *ModelRuntime) providerIDs() map[string]bool {
	ids := map[string]bool{}
	for id := range r.builtins {
		ids[id] = true
	}
	for id := range r.nativeExtensionProviders {
		ids[id] = true
	}
	for _, id := range r.config.GetProviderIDs() {
		ids[id] = true
	}
	for id := range r.extensionProviders {
		ids[id] = true
	}
	return ids
}

func (r *ModelRuntime) recomposeProvider(providerID string) {
	base := r.nativeExtensionProviders[providerID]
	if base == nil {
		base = r.builtins[providerID]
	}
	extension := r.extensionProviders[providerID]

	if base == nil && r.config.GetProvider(providerID) == nil && extension == nil {
		r.models.DeleteProvider(providerID)
		delete(r.compositionErrors, providerID)
		return
	}
	if base != nil && r.config.GetProvider(providerID) == nil && extension == nil {
		// No overlays: the builtin keeps its exact auth/login/stream behavior.
		r.models.SetProvider(base)
		delete(r.compositionErrors, providerID)
		return
	}
	composed, err := ComposeModelProvider(providerID, base, r.config, extension)
	if err != nil {
		r.compositionErrors[providerID] = err.Error()
		if base != nil {
			r.models.SetProvider(base)
		} else {
			r.models.DeleteProvider(providerID)
		}
		return
	}
	r.models.SetProvider(composed)
	delete(r.compositionErrors, providerID)
}

func (r *ModelRuntime) rebuildProviders() {
	r.models.ClearProviders()
	r.compositionErrors = map[string]string{}
	for providerID := range r.providerIDs() {
		r.recomposeProvider(providerID)
	}
	r.updateModelSnapshot()
}

func (r *ModelRuntime) updateModelSnapshot() {
	all := r.models.GetModels()
	available := make([]*ai.Model, 0, len(all))
	for _, model := range all {
		if r.snapshot.ConfiguredProviders[model.Provider] {
			available = append(available, model)
		}
	}
	r.snapshot.All = all
	r.snapshot.Available = available
}

// GetError returns the availability refresh error ("" when healthy).
func (r *ModelRuntime) GetError() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.availabilityError
}

// GetModels returns every registered chat model.
func (r *ModelRuntime) GetModels() []*ai.Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]*ai.Model(nil), r.snapshot.All...)
}

// GetAvailableSnapshot returns models whose providers are configured.
func (r *ModelRuntime) GetAvailableSnapshot() []*ai.Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]*ai.Model(nil), r.snapshot.Available...)
}

// GetModel finds one model.
func (r *ModelRuntime) GetModel(provider, modelID string) *ai.Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, model := range r.snapshot.All {
		if model.Provider == provider && model.ID == modelID {
			return model
		}
	}
	return nil
}

// GetProvider finds one composed provider.
func (r *ModelRuntime) GetProvider(providerID string) ai.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.models.GetProvider(providerID)
}

// HasConfiguredAuth reports whether the provider has usable auth.
func (r *ModelRuntime) HasConfiguredAuth(providerID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot.ConfiguredProviders[providerID]
}

// GetCompositionErrors reports per-provider compose failures.
func (r *ModelRuntime) GetCompositionErrors() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]string{}
	for k, v := range r.compositionErrors {
		out[k] = v
	}
	return out
}

// Refresh recomputes the availability snapshot.
func (r *ModelRuntime) Refresh() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	configured := map[string]bool{}
	for _, provider := range r.models.GetProviders() {
		if provider == nil {
			continue
		}
		auth := provider.Auth()
		if auth.APIKey != nil && auth.APIKey.Resolve != nil {
			// Resolve through the credential store so stored keys and
			// runtime --api-key overrides count as configured (ambient env
			// is the fallback inside the resolver).
			if result, err := ai.ResolveProviderAuth(provider, r.credentials, ai.DefaultAuthContext(), nil); err == nil && result != nil && result.Auth.APIKey != nil {
				configured[provider.ID()] = true
				continue
			}
		}
		if auth.OAuth != nil {
			configured[provider.ID()] = true
		}
	}

	stored := map[string]bool{}
	if infos, err := r.credentials.List(); err == nil {
		for _, info := range infos {
			stored[info.ProviderID] = true
		}
	}

	r.snapshot.ConfiguredProviders = configured
	r.snapshot.StoredProviders = stored
	r.updateModelSnapshot()
	return nil
}

// RegisterProvider registers an extension provider by config.
func (r *ModelRuntime) RegisterProvider(providerName string, config *ProviderConfigInput) error {
	if config == nil {
		return fmt.Errorf("Provider config is required when registering by name")
	}
	if err := ValidateExtensionProvider(providerName, r.baseProvider(providerName), r.config.GetProvider(providerName), config); err != nil {
		r.mu.Lock()
		r.extensionProviders[providerName] = config
		r.rebuildProviders()
		r.mu.Unlock()
		return err
	}
	r.mu.Lock()
	r.extensionProviders[providerName] = config
	r.recomposeProvider(providerName)
	r.updateModelSnapshot()
	r.mu.Unlock()
	return nil
}

// RegisterNativeProvider registers a precomposed provider.
func (r *ModelRuntime) RegisterNativeProvider(provider ai.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nativeExtensionProviders[provider.ID()] = provider
	r.recomposeProvider(provider.ID())
	r.updateModelSnapshot()
}

// UnregisterProvider removes an extension registration.
func (r *ModelRuntime) UnregisterProvider(providerName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.extensionProviders, providerName)
	delete(r.nativeExtensionProviders, providerName)
	r.recomposeProvider(providerName)
	r.updateModelSnapshot()
}

// GetRegisteredProviderConfig returns the extension config.
func (r *ModelRuntime) GetRegisteredProviderConfig(providerName string) *ProviderConfigInput {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.extensionProviders[providerName]
}

// GetRegisteredNativeProvider returns the native registration.
func (r *ModelRuntime) GetRegisteredNativeProvider(providerName string) ai.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nativeExtensionProviders[providerName]
}

// GetRegisteredProviderIDs lists extension registrations sorted.
func (r *ModelRuntime) GetRegisteredProviderIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.extensionProviders)+len(r.nativeExtensionProviders))
	for id := range r.extensionProviders {
		ids = append(ids, id)
	}
	for id := range r.nativeExtensionProviders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (r *ModelRuntime) baseProvider(providerID string) ai.Provider {
	if native, ok := r.nativeExtensionProviders[providerID]; ok {
		return native
	}
	return r.builtins[providerID]
}

// SetRuntimeApiKey installs a non-persistent API-key override for one
// provider (model-runtime.ts setRuntimeApiKey); the next Refresh sees it.
func (r *ModelRuntime) SetRuntimeApiKey(providerID, apiKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.credentials.SetRuntimeApiKey(providerID, apiKey)
}

// HasRuntimeApiKey reports whether a runtime API-key override is set.
func (r *ModelRuntime) HasRuntimeApiKey(providerID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.credentials.HasRuntimeApiKey(providerID)
}

// Stream proxies through the composed provider.
func (r *ModelRuntime) Stream(model *ai.Model, context ai.Context, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	return r.models.Stream(model, context, options)
}

// StreamSimple proxies through the composed provider.
func (r *ModelRuntime) StreamSimple(model *ai.Model, context ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	return r.models.StreamSimple(model, context, options)
}

// Complete proxies through the composed provider.
func (r *ModelRuntime) Complete(model *ai.Model, context ai.Context, options *ai.StreamOptions) (*ai.AssistantMessage, error) {
	return r.models.Complete(model, context, options)
}

// GetAuth resolves provider auth.
func (r *ModelRuntime) GetAuth(providerID string) (*ai.AuthResult, bool) {
	return r.models.GetAuth(providerID)
}

// GetProviderAuthStatus reports how a provider's auth is configured.
func (r *ModelRuntime) GetProviderAuthStatus(providerID string) AuthStatus {
	config := r.config.GetProvider(providerID)
	extension := r.extensionProviders[providerID]
	if status := ConfiguredRequestAuthStatus(config, extension); status != nil {
		return *status
	}
	if r.HasConfiguredAuth(providerID) {
		return AuthStatus{Configured: true, Source: "stored"}
	}
	return AuthStatus{}
}

// GetProviderDisplayName resolves the display name.
func (r *ModelRuntime) GetProviderDisplayName(providerID string) string {
	if provider := r.GetProvider(providerID); provider != nil && provider.Name() != "" {
		return provider.Name()
	}
	return providerID
}

// GetApiKeyForProvider resolves the provider's api key.
func (r *ModelRuntime) GetApiKeyForProvider(providerID string) (string, bool) {
	result, ok := r.GetAuth(providerID)
	if !ok || result == nil || result.Auth.APIKey == nil {
		return "", false
	}
	return *result.Auth.APIKey, true
}

// IsUsingOAuth reports whether the provider uses OAuth auth.
func (r *ModelRuntime) IsUsingOAuth(providerID string) bool {
	if provider := r.GetProvider(providerID); provider != nil {
		return provider.Auth().OAuth != nil
	}
	return false
}

// aiRadiusProvider builds a radius OAuth provider; the ported catalog does
// not ship radius, so the slot stays empty until its provider is ported
// (documented gap; radius models.json entries keep their composed layer).
func aiRadiusProvider(id, name, gateway string) ai.Provider {
	return nil
}
