package codingagent

// Port of core/model-config.ts: the immutable, credential-blind models.json
// snapshot with full TypeBox validation.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gladmo/openagent/typebox"
)

// ModelsJsonModel is one custom model definition.
type ModelsJsonModel struct {
	ID               string             `json:"id"`
	Name             string             `json:"name,omitempty"`
	API              string             `json:"api,omitempty"`
	BaseURL          string             `json:"baseUrl,omitempty"`
	Reasoning        *bool              `json:"reasoning,omitempty"`
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap,omitempty"`
	Input            []string           `json:"input,omitempty"`
	InputLimits      json.RawMessage    `json:"inputLimits,omitempty"`
	Cost             *ModelsJsonCost    `json:"cost,omitempty"`
	PromptCache      json.RawMessage    `json:"promptCache,omitempty"`
	ContextWindow    *float64           `json:"contextWindow,omitempty"`
	MaxTokens        *float64           `json:"maxTokens,omitempty"`
	SamplingParams   map[string]any     `json:"samplingParams,omitempty"`
	Headers          map[string]string  `json:"headers,omitempty"`
	Compat           json.RawMessage    `json:"compat,omitempty"`
}

// ModelsJsonCost carries model cost rates.
type ModelsJsonCost struct {
	Input      float64          `json:"input"`
	Output     float64          `json:"output"`
	CacheRead  float64          `json:"cacheRead"`
	CacheWrite float64          `json:"cacheWrite"`
	Tiers      []ModelsJsonTier `json:"tiers,omitempty"`
}

// ModelsJsonTier is one cost tier.
type ModelsJsonTier struct {
	InputTokensAbove float64 `json:"inputTokensAbove"`
	Input            float64 `json:"input"`
	Output           float64 `json:"output"`
	CacheRead        float64 `json:"cacheRead"`
	CacheWrite       float64 `json:"cacheWrite"`
}

// ModelsJsonModelOverride overrides one built-in model.
type ModelsJsonModelOverride struct {
	Name             string                  `json:"name,omitempty"`
	Reasoning        *bool                   `json:"reasoning,omitempty"`
	ThinkingLevelMap map[string]*string      `json:"thinkingLevelMap,omitempty"`
	Input            []string                `json:"input,omitempty"`
	InputLimits      json.RawMessage         `json:"inputLimits,omitempty"`
	Cost             *ModelsJsonOverrideCost `json:"cost,omitempty"`
	PromptCache      json.RawMessage         `json:"promptCache,omitempty"`
	ContextWindow    *float64                `json:"contextWindow,omitempty"`
	MaxTokens        *float64                `json:"maxTokens,omitempty"`
	SamplingParams   map[string]any          `json:"samplingParams,omitempty"`
	Headers          map[string]string       `json:"headers,omitempty"`
	Compat           json.RawMessage         `json:"compat,omitempty"`
}

// ModelsJsonOverrideCost partially overrides cost rates.
type ModelsJsonOverrideCost struct {
	Input      *float64         `json:"input,omitempty"`
	Output     *float64         `json:"output,omitempty"`
	CacheRead  *float64         `json:"cacheRead,omitempty"`
	CacheWrite *float64         `json:"cacheWrite,omitempty"`
	Tiers      []ModelsJsonTier `json:"tiers,omitempty"`
}

// ModelsJsonProvider is one provider entry.
type ModelsJsonProvider struct {
	Name           string                             `json:"name,omitempty"`
	BaseURL        string                             `json:"baseUrl,omitempty"`
	APIKey         string                             `json:"apiKey,omitempty"`
	API            string                             `json:"api,omitempty"`
	OAuth          string                             `json:"oauth,omitempty"`
	Headers        map[string]string                  `json:"headers,omitempty"`
	Compat         json.RawMessage                    `json:"compat,omitempty"`
	AuthHeader     *bool                              `json:"authHeader,omitempty"`
	Models         []ModelsJsonModel                  `json:"models,omitempty"`
	ModelOverrides map[string]ModelsJsonModelOverride `json:"modelOverrides,omitempty"`
}

func compatSchema() *typebox.Schema {
	// The compat union validates shape; the ported Model carries it as raw
	// JSON so providers consume their own fields.
	openAICompletions := typebox.Object([]*typebox.Property{
		typebox.Prop("supportsStore", typebox.Optional(typebox.Boolean())),
		typebox.Prop("supportsDeveloperRole", typebox.Optional(typebox.Boolean())),
		typebox.Prop("thinkingFormat", typebox.Optional(typebox.String())),
		typebox.Prop("chatTemplateKwargs", typebox.Optional(typebox.Record(typebox.Any()))),
		typebox.Prop("openRouterRouting", typebox.Optional(typebox.Record(typebox.Any()))),
	})
	openAIResponses := typebox.Object([]*typebox.Property{
		typebox.Prop("supportsDeveloperRole", typebox.Optional(typebox.Boolean())),
		typebox.Prop("sessionAffinityFormat", typebox.Optional(typebox.String())),
		typebox.Prop("supportsMaxOutputTokens", typebox.Optional(typebox.Boolean())),
	})
	anthropicMessages := typebox.Object([]*typebox.Property{
		typebox.Prop("supportsEagerToolInputStreaming", typebox.Optional(typebox.Boolean())),
		typebox.Prop("supportsLongCacheRetention", typebox.Optional(typebox.Boolean())),
		typebox.Prop("supportsTemperature", typebox.Optional(typebox.Boolean())),
		typebox.Prop("allowedFallbackModels", typebox.Optional(typebox.Array(typebox.Object([]*typebox.Property{
			typebox.Prop("provider", typebox.String(typebox.NumberOption("minLength", 1))),
			typebox.Prop("model", typebox.String(typebox.NumberOption("minLength", 1))),
		})))),
	})
	return typebox.Union([]*typebox.Schema{openAICompletions, openAIResponses, anthropicMessages})
}

func modelDefinitionSchema() *typebox.Schema {
	return typebox.Object([]*typebox.Property{
		typebox.Prop("id", typebox.String(typebox.NumberOption("minLength", 1))),
		typebox.Prop("name", typebox.Optional(typebox.String(typebox.NumberOption("minLength", 1)))),
		typebox.Prop("api", typebox.Optional(typebox.String(typebox.NumberOption("minLength", 1)))),
		typebox.Prop("baseUrl", typebox.Optional(typebox.String(typebox.NumberOption("minLength", 1)))),
		typebox.Prop("reasoning", typebox.Optional(typebox.Boolean())),
		typebox.Prop("thinkingLevelMap", typebox.Optional(typebox.Record(typebox.Union([]*typebox.Schema{typebox.String(), typebox.Null()})))),
		typebox.Prop("input", typebox.Optional(typebox.Array(typebox.Union([]*typebox.Schema{typebox.Literal("text"), typebox.Literal("image")})))),
		typebox.Prop("inputLimits", typebox.Optional(typebox.Record(typebox.Any()))),
		typebox.Prop("cost", typebox.Optional(typebox.Record(typebox.Any()))),
		typebox.Prop("promptCache", typebox.Optional(typebox.Record(typebox.Any()))),
		typebox.Prop("contextWindow", typebox.Optional(typebox.Number())),
		typebox.Prop("maxTokens", typebox.Optional(typebox.Number())),
		typebox.Prop("samplingParams", typebox.Optional(typebox.Record(typebox.Any()))),
		typebox.Prop("headers", typebox.Optional(typebox.Record(typebox.String()))),
		typebox.Prop("compat", typebox.Optional(compatSchema())),
	})
}

func modelOverrideSchema() *typebox.Schema {
	return typebox.Object([]*typebox.Property{
		typebox.Prop("name", typebox.Optional(typebox.String(typebox.NumberOption("minLength", 1)))),
		typebox.Prop("reasoning", typebox.Optional(typebox.Boolean())),
		typebox.Prop("thinkingLevelMap", typebox.Optional(typebox.Record(typebox.Union([]*typebox.Schema{typebox.String(), typebox.Null()})))),
		typebox.Prop("input", typebox.Optional(typebox.Array(typebox.Union([]*typebox.Schema{typebox.Literal("text"), typebox.Literal("image")})))),
		typebox.Prop("inputLimits", typebox.Optional(typebox.Record(typebox.Any()))),
		typebox.Prop("cost", typebox.Optional(typebox.Record(typebox.Any()))),
		typebox.Prop("promptCache", typebox.Optional(typebox.Record(typebox.Any()))),
		typebox.Prop("contextWindow", typebox.Optional(typebox.Number())),
		typebox.Prop("maxTokens", typebox.Optional(typebox.Number())),
		typebox.Prop("samplingParams", typebox.Optional(typebox.Record(typebox.Any()))),
		typebox.Prop("headers", typebox.Optional(typebox.Record(typebox.String()))),
		typebox.Prop("compat", typebox.Optional(compatSchema())),
	})
}

func providerConfigSchema() *typebox.Schema {
	return typebox.Object([]*typebox.Property{
		typebox.Prop("name", typebox.Optional(typebox.String(typebox.NumberOption("minLength", 1)))),
		typebox.Prop("baseUrl", typebox.Optional(typebox.String(typebox.NumberOption("minLength", 1)))),
		typebox.Prop("apiKey", typebox.Optional(typebox.String(typebox.NumberOption("minLength", 1)))),
		typebox.Prop("api", typebox.Optional(typebox.String(typebox.NumberOption("minLength", 1)))),
		typebox.Prop("oauth", typebox.Optional(typebox.Literal("radius"))),
		typebox.Prop("headers", typebox.Optional(typebox.Record(typebox.String()))),
		typebox.Prop("compat", typebox.Optional(compatSchema())),
		typebox.Prop("authHeader", typebox.Optional(typebox.Boolean())),
		typebox.Prop("models", typebox.Optional(typebox.Array(modelDefinitionSchema()))),
		typebox.Prop("modelOverrides", typebox.Optional(typebox.Record(modelOverrideSchema()))),
	})
}

var validateModelsConfig = typebox.Compile(
	typebox.Object([]*typebox.Property{
		typebox.Prop("providers", typebox.Record(providerConfigSchema())),
	}),
)

// ModelConfig is one immutable load of models.json.
type ModelConfig struct {
	providers map[string]ModelsJsonProvider
	err       string
}

// LoadModelConfig reads and validates models.json ("" path yields empty).
func LoadModelConfig(modelsJsonPath string) *ModelConfig {
	if modelsJsonPath == "" {
		return &ModelConfig{providers: map[string]ModelsJsonProvider{}}
	}
	path := NormalizePathDefault(modelsJsonPath)
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ModelConfig{providers: map[string]ModelsJsonProvider{}}
		}
		return &ModelConfig{providers: map[string]ModelsJsonProvider{}, err: fmt.Sprintf("Failed to load models.json: %s\n\nFile: %s", err.Error(), path)}
	}

	var parsed any
	if err := json.Unmarshal([]byte(StripJsonComments(StripBom(string(content)))), &parsed); err != nil {
		return &ModelConfig{providers: map[string]ModelsJsonProvider{}, err: fmt.Sprintf("Failed to parse models.json: %s\n\nFile: %s", err.Error(), path)}
	}

	if !validateModelsConfig.Check(parsed) {
		var errorLines []string
		for _, validationError := range validateModelsConfig.Errors(parsed) {
			errorLines = append(errorLines, fmt.Sprintf("  - %s: %s", formatValidationPath(validationError), validationError.Message))
		}
		if len(errorLines) == 0 {
			errorLines = append(errorLines, "Unknown schema error")
		}
		return &ModelConfig{providers: map[string]ModelsJsonProvider{}, err: "Invalid models.json schema:\n" + joinLines(errorLines) + "\n\nFile: " + path}
	}

	var config struct {
		Providers map[string]ModelsJsonProvider `json:"providers"`
	}
	if err := json.Unmarshal([]byte(StripJsonComments(StripBom(string(content)))), &config); err != nil {
		return &ModelConfig{providers: map[string]ModelsJsonProvider{}, err: fmt.Sprintf("Failed to parse models.json: %s\n\nFile: %s", err.Error(), path)}
	}
	if config.Providers == nil {
		config.Providers = map[string]ModelsJsonProvider{}
	}
	return &ModelConfig{providers: config.Providers}
}

func joinLines(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}

func formatValidationPath(err *typebox.Error) string {
	// InstancePath is /-separated; render dotted like the reference.
	path := strings.TrimPrefix(err.InstancePath, "/")
	if path == "" {
		return "root"
	}
	return strings.ReplaceAll(path, "/", ".")
}

// GetProvider returns one provider entry.
func (c *ModelConfig) GetProvider(providerID string) *ModelsJsonProvider {
	if provider, ok := c.providers[providerID]; ok {
		return &provider
	}
	return nil
}

// GetProviderIDs lists provider ids sorted.
func (c *ModelConfig) GetProviderIDs() []string {
	ids := make([]string, 0, len(c.providers))
	for id := range c.providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// GetError returns the load error ("" when healthy).
func (c *ModelConfig) GetError() string { return c.err }
