package codingagent

// Port of core/provider-composer.ts (model layers): built-in + models.json +
// extension composition. The extension OAuth/login flows degrade to their
// ambient API-key paths where the TS closure machinery has no Go equivalent
// (documented deviation; the composed model/auth surface is preserved).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/gladmo/openagent/ai"
)

// AuthStatus reports how a provider's auth is configured.
type AuthStatus struct {
	Configured bool
	Source     string // stored | runtime | environment | fallback | models_json_key | models_json_command
	Label      string
}

// ExtensionOAuthConfig is the extension OAuth surface.
type ExtensionOAuthConfig struct {
	Name           string
	IsSubscription bool
}

// ProviderModelConfigBase is the shared model-definition base.
type ProviderModelConfigBase struct {
	ID          string
	Name        string
	API         string
	BaseURL     string
	Input       []string
	InputLimits json.RawMessage
	Cost        *ModelsJsonCost
	Headers     map[string]string
}

// ProviderModelConfig is one extension model definition.
type ProviderModelConfig struct {
	ProviderModelConfigBase
	Type             string // "" (chat) | "image" | "classifier"
	Reasoning        bool
	ThinkingLevelMap map[string]*string
	PromptCache      json.RawMessage
	ContextWindow    float64
	MaxTokens        float64
	SamplingParams   map[string]any
	Compat           json.RawMessage
	Output           []string // image models
}

// ProviderConfigInput is the extension registerProvider API.
type ProviderConfigInput struct {
	Name         string
	BaseURL      string
	APIKey       string
	HasAPIKey    bool
	API          string
	StreamSimple func(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream
	Headers      map[string]string
	AuthHeader   *bool
	OAuth        *ExtensionOAuthConfig
	Models       []ProviderModelConfig
}

// ClearAPIKeyCache re-exports the config-value cache clear.
var ClearAPIKeyCache = ClearConfigValueCache

// getAllProviderModels reads every model type from a provider.
func getAllProviderModels(base ai.Provider) []*ai.Model {
	if base == nil {
		return nil
	}
	return base.GetModels()
}

// mergeCompat merges compat objects with nested-key deep merge.
func mergeCompat(base, override json.RawMessage) json.RawMessage {
	if len(override) == 0 {
		return base
	}
	var baseMap, overrideMap map[string]any
	if len(base) > 0 {
		_ = json.Unmarshal(base, &baseMap)
	}
	if baseMap == nil {
		baseMap = map[string]any{}
	}
	_ = json.Unmarshal(override, &overrideMap)
	for key, value := range overrideMap {
		if nested(nestedValue(baseMap[key]), nestedValue(value)) {
			baseMap[key] = mergeAnyMaps(nestedValue(baseMap[key]), nestedValue(value))
			continue
		}
		baseMap[key] = value
	}
	merged, err := json.Marshal(baseMap)
	if err != nil {
		return base
	}
	return merged
}

func nestedValue(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func nested(a, b map[string]any) bool {
	return a != nil && b != nil
}

func mergeAnyMaps(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if nested(nestedValue(out[k]), nestedValue(v)) {
			out[k] = mergeAnyMaps(nestedValue(out[k]), nestedValue(v))
			continue
		}
		out[k] = v
	}
	return out
}

// applyModelOverride layers a models.json override onto a chat model.
func applyModelOverride(model *ai.Model, override *ModelsJsonModelOverride) *ai.Model {
	updated := *model
	if override.Name != "" {
		updated.Name = override.Name
	}
	if override.Reasoning != nil {
		updated.Reasoning = *override.Reasoning
	}
	if override.ContextWindow != nil {
		updated.ContextWindow = *override.ContextWindow
	}
	if override.MaxTokens != nil {
		updated.MaxTokens = *override.MaxTokens
	}
	if override.Input != nil {
		updated.Input = override.Input
	}
	if override.Headers != nil {
		headers := map[string]string{}
		for k, v := range model.Headers {
			headers[k] = v
		}
		for k, v := range override.Headers {
			headers[k] = v
		}
		updated.Headers = headers
	}
	return &updated
}

// modelFromJson builds a custom model from a models.json definition.
func modelFromJson(providerID string, definition *ModelsJsonModel, providerConfig *ModelsJsonProvider, defaults *ai.Model) (*ai.Model, error) {
	apiName := definition.API
	if apiName == "" {
		apiName = providerConfig.API
	}
	if apiName == "" && defaults != nil {
		apiName = defaults.API
	}
	if apiName == "" {
		return nil, fmt.Errorf(`Provider %s, model %s: no "api" specified. Set at provider or model level.`, providerID, definition.ID)
	}

	baseURL := definition.BaseURL
	if baseURL == "" {
		baseURL = providerConfig.BaseURL
	}
	if baseURL == "" && defaults != nil {
		baseURL = defaults.BaseURL
	}
	if baseURL == "" {
		return nil, fmt.Errorf(`Provider %s: "baseUrl" is required when defining custom models.`, providerID)
	}
	if definition.ContextWindow != nil && *definition.ContextWindow <= 0 {
		return nil, fmt.Errorf("Provider %s, model %s: invalid contextWindow", providerID, definition.ID)
	}
	if definition.MaxTokens != nil && *definition.MaxTokens <= 0 {
		return nil, fmt.Errorf("Provider %s, model %s: invalid maxTokens", providerID, definition.ID)
	}

	model := &ai.Model{
		ID:       definition.ID,
		Name:     definition.ID,
		API:      apiName,
		Provider: providerID,
		BaseURL:  baseURL,
		Input:    []string{"text"},
	}
	if definition.Name != "" {
		model.Name = definition.Name
	}
	if definition.Reasoning != nil {
		model.Reasoning = *definition.Reasoning
	}
	if definition.Input != nil {
		model.Input = definition.Input
	}
	model.ContextWindow = 128000
	if definition.ContextWindow != nil {
		model.ContextWindow = *definition.ContextWindow
	}
	model.MaxTokens = 16384
	if definition.MaxTokens != nil {
		model.MaxTokens = *definition.MaxTokens
	}
	return model, nil
}

// findModelDefaults picks merge defaults for custom chat models.
func findModelDefaults(models []*ai.Model, modelID, apiName string) *ai.Model {
	var chatModels []*ai.Model
	for _, model := range models {
		if model.Type == "" || model.Type == "chat" {
			chatModels = append(chatModels, model)
		}
	}
	for _, model := range chatModels {
		if model.ID == modelID {
			return model
		}
	}
	if apiName != "" {
		for _, model := range chatModels {
			if model.API == apiName {
				return model
			}
		}
	}
	for _, model := range chatModels {
		if model.API == "openai-completions" {
			return model
		}
	}
	if len(chatModels) > 0 {
		return chatModels[0]
	}
	return nil
}

// applyModelsJson layers a models.json provider config over base models.
func applyModelsJson(providerID string, baseModels []*ai.Model, config *ModelsJsonProvider) ([]*ai.Model, error) {
	if config == nil {
		return append([]*ai.Model(nil), baseModels...), nil
	}
	if config.OAuth != "" && config.BaseURL == "" {
		return nil, fmt.Errorf(`Provider %s: "baseUrl" is required when "oauth" is set.`, providerID)
	}
	hasOverrides := len(config.ModelOverrides) > 0
	if len(config.Models) == 0 && config.BaseURL == "" && len(config.Headers) == 0 &&
		len(config.Compat) == 0 && !hasOverrides && config.APIKey == "" &&
		config.OAuth == "" && config.AuthHeader == nil {
		return nil, fmt.Errorf(`Provider %s: must specify "baseUrl", "headers", "compat", "modelOverrides", or "models".`, providerID)
	}

	models := make([]*ai.Model, 0, len(baseModels))
	for _, model := range baseModels {
		updated := *model
		baseURL := updated.BaseURL
		if config.OAuth != "radius" && config.BaseURL != "" {
			baseURL = config.BaseURL
		}
		updated.BaseURL = baseURL
		models = append(models, &updated)
	}
	for i := range config.Models {
		definition := &config.Models[i]
		existingIndex := -1
		for j, model := range models {
			if model.ID == definition.ID {
				existingIndex = j
				break
			}
		}
		apiName := definition.API
		if apiName == "" {
			apiName = config.API
		}
		defaults := findModelDefaults(models, definition.ID, apiName)
		model, err := modelFromJson(providerID, definition, config, defaults)
		if err != nil {
			return nil, err
		}
		if existingIndex >= 0 {
			models[existingIndex] = model
		} else {
			models = append(models, model)
		}
	}
	return models, nil
}

// applyExtension layers an extension config over models.
func applyExtension(providerID string, models []*ai.Model, config *ProviderConfigInput) ([]*ai.Model, error) {
	if config == nil {
		return append([]*ai.Model(nil), models...), nil
	}
	if len(config.Models) == 0 {
		if config.BaseURL == "" {
			return append([]*ai.Model(nil), models...), nil
		}
		updated := make([]*ai.Model, len(models))
		for i, model := range models {
			clone := *model
			clone.BaseURL = config.BaseURL
			updated[i] = &clone
		}
		return updated, nil
	}

	out := make([]*ai.Model, 0, len(config.Models))
	for i := range config.Models {
		definition := &config.Models[i]
		defaults := findModelDefaults(models, definition.ID, definition.API)
		apiName := definition.API
		if apiName == "" {
			apiName = config.API
		}
		if apiName == "" && defaults != nil {
			apiName = defaults.API
		}
		if apiName == "" {
			return nil, fmt.Errorf(`Provider %s, model %s: no "api" specified. Set it at model level or provider level.`, providerID, definition.ID)
		}
		baseURL := definition.BaseURL
		if baseURL == "" {
			baseURL = config.BaseURL
		}
		if baseURL == "" && defaults != nil {
			baseURL = defaults.BaseURL
		}
		if baseURL == "" {
			return nil, fmt.Errorf(`Provider %s: "baseUrl" is required when defining custom models.`, providerID)
		}
		model := &ai.Model{
			ID:        definition.ID,
			Name:      definition.Name,
			API:       apiName,
			Provider:  providerID,
			BaseURL:   baseURL,
			Input:     definition.Input,
			Reasoning: definition.Reasoning,
			Type:      definition.Type,
		}
		out = append(out, model)
	}
	return out, nil
}

// ValidateExtensionProvider reports structural errors eagerly.
func ValidateExtensionProvider(providerID string, base ai.Provider, modelsConfig *ModelsJsonProvider, extension *ProviderConfigInput) error {
	if extension != nil && extension.StreamSimple != nil && extension.API == "" {
		return fmt.Errorf(`Provider %s: "api" is required when registering streamSimple.`, providerID)
	}
	_, err := applyExtension(providerID, applyModelsJsonLayers(providerID, base, modelsConfig), extension)
	return err
}

func applyModelsJsonLayers(providerID string, base ai.Provider, modelsConfig *ModelsJsonProvider) []*ai.Model {
	layered, err := applyModelsJson(providerID, getAllProviderModels(base), modelsConfig)
	if err != nil {
		return nil
	}
	return layered
}

// rawModelHeaders collects model-level header layers.
func rawModelHeaders(model *ai.Model, config *ModelsJsonProvider, extension *ProviderConfigInput) map[string]string {
	headers := map[string]string{}
	if config != nil && model.Type == "" || model.Type == "chat" {
		if config != nil {
			if override, ok := config.ModelOverrides[model.ID]; ok && override.Headers != nil {
				for k, v := range override.Headers {
					headers[k] = v
				}
			}
			for _, entry := range config.Models {
				if entry.ID == model.ID && entry.Headers != nil {
					for k, v := range entry.Headers {
						headers[k] = v
					}
				}
			}
		}
	}
	if extension != nil {
		for _, entry := range extension.Models {
			entryType := entry.Type
			if entryType == "" {
				entryType = "chat"
			}
			modelType := model.Type
			if modelType == "" {
				modelType = "chat"
			}
			if entryType == modelType && entry.ID == model.ID && entry.Headers != nil {
				for k, v := range entry.Headers {
					headers[k] = v
				}
			}
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// CompatibilityRequestConfig carries headers and the authHeader flag.
type CompatibilityRequestConfig struct {
	Headers    map[string]string
	AuthHeader bool
}

// ResolveCompatibilityRequestConfig computes request headers for a model.
func ResolveCompatibilityRequestConfig(model *ai.Model, config *ModelsJsonProvider, extension *ProviderConfigInput) CompatibilityRequestConfig {
	configured := map[string]string{}
	if config != nil {
		for k, v := range config.Headers {
			configured[k] = v
		}
	}
	if extension != nil {
		for k, v := range extension.Headers {
			configured[k] = v
		}
	}
	for k, v := range rawModelHeaders(model, config, extension) {
		configured[k] = v
	}

	authHeader := false
	if extension != nil && extension.AuthHeader != nil {
		authHeader = *extension.AuthHeader
	} else if config != nil && config.AuthHeader != nil {
		authHeader = *config.AuthHeader
	}

	headers := map[string]string{}
	for k, v := range model.Headers {
		headers[k] = v
	}
	for k, v := range configured {
		headers[k] = v
	}
	if len(headers) == 0 {
		return CompatibilityRequestConfig{AuthHeader: authHeader}
	}
	return CompatibilityRequestConfig{Headers: headers, AuthHeader: authHeader}
}

// ResolveConfiguredModelHeaders resolves model-level headers with env.
func ResolveConfiguredModelHeaders(model *ai.Model, config *ModelsJsonProvider, extension *ProviderConfigInput, env map[string]string) (map[string]string, error) {
	return ResolveHeadersOrThrow(rawModelHeaders(model, config, extension),
		fmt.Sprintf("model %q/%q", model.Provider, model.ID), env)
}

// ConfiguredRequestAuthStatus inspects the configured key's availability.
func ConfiguredRequestAuthStatus(config *ModelsJsonProvider, extension *ProviderConfigInput) *AuthStatus {
	value := ""
	if extension != nil && extension.HasAPIKey {
		value = extension.APIKey
	} else if config != nil {
		value = config.APIKey
	} else {
		return nil
	}
	if value == "" {
		if config != nil && config.APIKey == "" && (extension == nil || !extension.HasAPIKey) {
			return nil
		}
	}

	if IsCommandConfigValue(value) {
		return &AuthStatus{Configured: true, Source: "models_json_command"}
	}
	names := GetConfigValueEnvVarNames(value)
	if len(names) > 0 {
		if IsConfigValueConfigured(value, nil) {
			return &AuthStatus{Configured: true, Source: "environment", Label: strings.Join(names, ", ")}
		}
		return &AuthStatus{Configured: false}
	}
	source := "models_json_key"
	if extension != nil && extension.HasAPIKey {
		source = "fallback"
	}
	return &AuthStatus{Configured: true, Source: source}
}

// ComposedProvider adapts the composed layers to ai.Provider. Transport
// comes from the built-in base layer; a composition without a base (a
// models.json-only provider) has no stream transport in this port and
// fails loud instead of returning a nil stream.
type ComposedProvider struct {
	id     string
	name   string
	auth   ai.ProviderAuth
	models func() []*ai.Model
	base   ai.Provider
}

// ID implements ai.Provider.
func (p *ComposedProvider) ID() string { return p.id }

// Name implements ai.Provider.
func (p *ComposedProvider) Name() string { return p.name }

// Auth implements ai.Provider.
func (p *ComposedProvider) Auth() ai.ProviderAuth { return p.auth }

// GetModels implements ai.Provider.
func (p *ComposedProvider) GetModels() []*ai.Model { return p.models() }

// Stream delegates to the base layer's transport with the composed model
// and resolved request options.
func (p *ComposedProvider) Stream(model *ai.Model, context *ai.TranscriptContext, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	if p.base == nil {
		return noTransportStream(p.id)
	}
	return p.base.Stream(model, context, options)
}

// StreamSimple delegates to the base layer's transport.
func (p *ComposedProvider) StreamSimple(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	if p.base == nil {
		return noTransportStream(p.id)
	}
	return p.base.StreamSimple(model, context, options)
}

// FetchDeferred delegates to the base layer's transport.
func (p *ComposedProvider) FetchDeferred(model *ai.Model, handle *ai.DeferredHandle, options *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
	if p.base == nil {
		return noTransportStream(p.id)
	}
	return p.base.FetchDeferred(model, handle, options)
}

// noTransportStream fails loud for a composed provider without a base:
// the port ships transports for its built-in providers only.
func noTransportStream(providerID string) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	text := fmt.Sprintf("Provider %q has no stream transport (a models.json-only provider needs a built-in provider or an extension that streams).", providerID)
	stream.Push(&ai.EventError{
		Reason: ai.StopError,
		Error:  &ai.AssistantMessage{StopReason: ai.StopError, ErrorMessage: &text},
	})
	return stream
}

// CancelDeferred delegates to the base layer's transport.
func (p *ComposedProvider) CancelDeferred(model *ai.Model, handle *ai.DeferredHandle, options *ai.DeferredCancelOptions) error {
	if p.base == nil {
		return nil
	}
	return p.base.CancelDeferred(model, handle, options)
}

// ComposeModelProvider layers built-in, models.json, and extension configs
// without reading credentials.
func ComposeModelProvider(providerID string, base ai.Provider, modelConfig *ModelConfig, extension *ProviderConfigInput) (*ComposedProvider, error) {
	var config *ModelsJsonProvider
	if modelConfig != nil {
		config = modelConfig.GetProvider(providerID)
	}

	getAllModels := func() []*ai.Model {
		layered, err := applyModelsJson(providerID, getAllProviderModels(base), config)
		if err != nil {
			return nil
		}
		applied, err := applyExtension(providerID, layered, extension)
		if err != nil {
			return nil
		}
		out := make([]*ai.Model, 0, len(applied))
		for _, model := range applied {
			if config != nil {
				if override, ok := config.ModelOverrides[model.ID]; ok && (model.Type == "" || model.Type == "chat") {
					out = append(out, applyModelOverride(model, &override))
					continue
				}
			}
			out = append(out, model)
		}
		return out
	}

	// Validate eagerly so registration reports structural errors immediately.
	if models := getAllModels(); models == nil {
		if _, err := applyModelsJson(providerID, getAllProviderModels(base), config); err != nil {
			return nil, err
		}
		if _, err := applyExtension(providerID, getAllProviderModels(base), extension); err != nil {
			return nil, err
		}
	}

	// Compose the auth surface: extension/static key over inherited.
	auth := ai.ProviderAuth{}
	var apiKeyResolve func(ai.AuthResolveInput) (*ai.AuthResult, error)
	rawKey := ""
	if extension != nil && extension.HasAPIKey {
		rawKey = extension.APIKey
	} else if config != nil {
		rawKey = config.APIKey
	}
	if rawKey != "" || base != nil {
		apiKeyResolve = func(input ai.AuthResolveInput) (*ai.AuthResult, error) {
			if rawKey != "" {
				value, err := ResolveConfigValueOrThrow(rawKey, fmt.Sprintf("API key for provider %q", providerID), credentialEnv(input.Credential))
				if err != nil {
					return nil, err
				}
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: &value}, Source: "configured API key"}, nil
			}
			if base != nil && base.Auth().APIKey != nil && base.Auth().APIKey.Resolve != nil {
				return base.Auth().APIKey.Resolve(input)
			}
			return nil, nil
		}
	}
	if apiKeyResolve != nil {
		name := "API key"
		if base != nil && base.Auth().APIKey != nil {
			name = base.Auth().APIKey.Name
		}
		auth.APIKey = &ai.ProviderApiKeyAuth{Name: name, Resolve: apiKeyResolve}
	}
	if extension != nil && extension.OAuth != nil {
		auth.OAuth = &ai.ProviderOAuthAuth{Name: extension.OAuth.Name, IsSubscription: extension.OAuth.IsSubscription}
	} else if base != nil {
		auth.OAuth = base.Auth().OAuth
	}
	if auth.APIKey == nil && auth.OAuth == nil {
		return nil, fmt.Errorf("Provider %s: no authentication method configured.", providerID)
	}

	name := providerID
	if extension != nil && extension.Name != "" {
		name = extension.Name
	} else if config != nil && config.Name != "" {
		name = config.Name
	} else if base != nil {
		name = base.Name()
	} else if extension != nil && extension.OAuth != nil {
		name = extension.OAuth.Name
	}

	return &ComposedProvider{id: providerID, name: name, auth: auth, models: getAllModels, base: base}, nil
}

// sortModelIDs orders model ids (test helper).
func sortModelIDs(models []*ai.Model) []string {
	ids := make([]string, len(models))
	for i, model := range models {
		ids[i] = model.ID
	}
	sort.Strings(ids)
	return ids
}

// credentialEnv extracts the stored credential's env map.
func credentialEnv(credential *ai.Credential) map[string]string {
	if credential == nil {
		return nil
	}
	return credential.Env
}
