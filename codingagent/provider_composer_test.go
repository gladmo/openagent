package codingagent

// Tests for provider composition (ported from provider-composer.test.ts
// surfaces: layering, validation, headers, auth status).

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
)

type stubBaseProvider struct {
	id     string
	name   string
	models []*ai.Model
	apiKey *ai.ProviderApiKeyAuth
	oauth  *ai.ProviderOAuthAuth
}

func (p *stubBaseProvider) ID() string   { return p.id }
func (p *stubBaseProvider) Name() string { return p.name }
func (p *stubBaseProvider) Auth() ai.ProviderAuth {
	return ai.ProviderAuth{APIKey: p.apiKey, OAuth: p.oauth}
}
func (p *stubBaseProvider) GetModels() []*ai.Model { return p.models }
func (p *stubBaseProvider) Stream(model *ai.Model, context *ai.TranscriptContext, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	return nil
}
func (p *stubBaseProvider) StreamSimple(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	return nil
}
func (p *stubBaseProvider) FetchDeferred(model *ai.Model, handle *ai.DeferredHandle, options *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
	return nil
}
func (p *stubBaseProvider) CancelDeferred(model *ai.Model, handle *ai.DeferredHandle, options *ai.DeferredCancelOptions) error {
	return nil
}

func baseProviderWithModels() *stubBaseProvider {
	return &stubBaseProvider{
		id:   "test-provider",
		name: "Test Provider",
		models: []*ai.Model{
			{ID: "model-a", Name: "Model A", API: "openai-completions", Provider: "test-provider", BaseURL: "https://base", Input: []string{"text"}},
			{ID: "model-b", Name: "Model B", API: "openai-completions", Provider: "test-provider", BaseURL: "https://base", Input: []string{"text"}},
		},
	}
}

func emptyModelConfig() *ModelConfig {
	return LoadModelConfig("")
}

func TestComposeModelProviderBasePassthrough(t *testing.T) {
	base := baseProviderWithModels()
	composed, err := ComposeModelProvider("test-provider", base, emptyModelConfig(), nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if composed.ID() != "test-provider" || composed.Name() != "Test Provider" {
		t.Fatalf("identity = %s/%s", composed.ID(), composed.Name())
	}
	models := composed.GetModels()
	if len(models) != 2 {
		t.Fatalf("models = %d", len(models))
	}
	if models[0].BaseURL != "https://base" {
		t.Fatalf("base url = %q", models[0].BaseURL)
	}
}

func TestComposeModelProviderEmptyBase(t *testing.T) {
	// No base, no config: no auth method -> error.
	if _, err := ComposeModelProvider("x", nil, emptyModelConfig(), nil); err == nil || !strings.Contains(err.Error(), "no authentication method") {
		t.Fatalf("err = %v", err)
	}

	// Static key via extension makes it valid.
	key := "sk-test"
	hasKey := true
	composed, err := ComposeModelProvider("x", nil, emptyModelConfig(), &ProviderConfigInput{APIKey: key, HasAPIKey: hasKey})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if composed.Auth().APIKey == nil {
		t.Fatal("api key auth missing")
	}
	result, resolveErr := composed.Auth().APIKey.Resolve(ai.AuthResolveInput{})
	if resolveErr != nil || result == nil || result.Auth.APIKey == nil || *result.Auth.APIKey != "sk-test" {
		t.Fatalf("resolve = %+v err=%v", result, resolveErr)
	}
}

func TestComposeModelProviderModelsJsonLayer(t *testing.T) {
	base := baseProviderWithModels()
	config := LoadModelConfig("")
	config.providers["test-provider"] = ModelsJsonProvider{
		Name:    "Custom Name",
		BaseURL: "https://override.example.com",
		APIKey:  "static-key",
		Models: []ModelsJsonModel{
			{ID: "model-custom", ContextWindow: float64Ptr(256000), MaxTokens: float64Ptr(8192)},
		},
		ModelOverrides: map[string]ModelsJsonModelOverride{
			"model-a": {Name: "Renamed A"},
		},
	}

	composed, err := ComposeModelProvider("test-provider", base, config, nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if composed.Name() != "Custom Name" {
		t.Fatalf("name = %q", composed.Name())
	}

	models := composed.GetModels()
	if len(models) != 3 {
		t.Fatalf("models = %d", len(models))
	}

	// Existing models get the overridden baseUrl.
	for _, model := range models {
		if model.BaseURL != "https://override.example.com" {
			t.Fatalf("base url = %q", model.BaseURL)
		}
	}

	// The override renames model-a.
	found := map[string]string{}
	for _, model := range models {
		found[model.ID] = model.Name
	}
	if found["model-a"] != "Renamed A" {
		t.Fatalf("rename = %q", found["model-a"])
	}

	// The custom model gets default contextWindow/maxTokens when specified.
	for _, model := range models {
		if model.ID == "model-custom" {
			if model.ContextWindow != 256000 || model.MaxTokens != 8192 {
				t.Fatalf("custom = %+v", model)
			}
		}
	}
}

func float64Ptr(v float64) *float64 { return &v }

func TestComposeModelProviderValidation(t *testing.T) {
	base := baseProviderWithModels()

	// Empty provider config errors.
	badConfig := LoadModelConfig("")
	badConfig.providers["test-provider"] = ModelsJsonProvider{}
	if _, err := ComposeModelProvider("test-provider", base, badConfig, nil); err == nil ||
		!strings.Contains(err.Error(), `must specify "baseUrl"`) {
		t.Fatalf("empty config err = %v", err)
	}

	// oauth without baseUrl errors.
	oauthConfig := LoadModelConfig("")
	oauthConfig.providers["test-provider"] = ModelsJsonProvider{OAuth: "radius"}
	if _, err := ComposeModelProvider("test-provider", base, oauthConfig, nil); err == nil ||
		!strings.Contains(err.Error(), `"baseUrl" is required when "oauth"`) {
		t.Fatalf("oauth err = %v", err)
	}

	// Custom model without api errors when no base supplies defaults.
	noAPIConfig := LoadModelConfig("")
	noAPIConfig.providers["fresh"] = ModelsJsonProvider{
		BaseURL: "https://x",
		APIKey:  "k",
		Models:  []ModelsJsonModel{{ID: "no-api"}},
	}
	if _, err := ComposeModelProvider("fresh", nil, noAPIConfig, nil); err == nil ||
		!strings.Contains(err.Error(), `no "api" specified`) {
		t.Fatalf("api err = %v", err)
	}

	// Custom model without baseUrl (no provider-level default) errors.
	noURLConfig := LoadModelConfig("")
	noURLConfig.providers["fresh"] = ModelsJsonProvider{
		APIKey: "k",
		API:    "openai-completions",
		Models: []ModelsJsonModel{{ID: "m"}},
	}
	if _, err := ComposeModelProvider("fresh", nil, noURLConfig, nil); err == nil ||
		!strings.Contains(err.Error(), `"baseUrl" is required`) {
		t.Fatalf("url err = %v", err)
	}

	// Invalid contextWindow errors.
	badWindow := LoadModelConfig("")
	badWindow.providers["test-provider"] = ModelsJsonProvider{
		BaseURL: "https://x",
		APIKey:  "k",
		API:     "openai-completions",
		Models:  []ModelsJsonModel{{ID: "m", ContextWindow: float64Ptr(-1)}},
	}
	if _, err := ComposeModelProvider("test-provider", base, badWindow, nil); err == nil ||
		!strings.Contains(err.Error(), "invalid contextWindow") {
		t.Fatalf("window err = %v", err)
	}
}

func TestComposeModelProviderExtensionLayer(t *testing.T) {
	base := baseProviderWithModels()

	// Extension baseUrl rewrites without models.
	extension := &ProviderConfigInput{BaseURL: "https://ext.example.com", APIKey: "k", HasAPIKey: true}
	composed, err := ComposeModelProvider("test-provider", base, emptyModelConfig(), extension)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	for _, model := range composed.GetModels() {
		if model.BaseURL != "https://ext.example.com" {
			t.Fatalf("base url = %q", model.BaseURL)
		}
	}

	// Extension models replace the list.
	extensionModel := &ProviderConfigInput{
		APIKey: "k", HasAPIKey: true,
		API: "openai-completions",
		Models: []ProviderModelConfig{
			{ProviderModelConfigBase: ProviderModelConfigBase{ID: "ext-model", Name: "Ext", API: "openai-completions", Input: []string{"text"}}},
		},
	}
	composed, err = ComposeModelProvider("test-provider", base, emptyModelConfig(), extensionModel)
	if err != nil {
		t.Fatalf("compose models: %v", err)
	}
	models := composed.GetModels()
	if len(models) != 1 || models[0].ID != "ext-model" {
		t.Fatalf("models = %+v", models)
	}
}

func TestValidateExtensionProvider(t *testing.T) {
	base := baseProviderWithModels()

	// streamSimple without api errors.
	streaming := &ProviderConfigInput{
		APIKey: "k", HasAPIKey: true,
		StreamSimple: func(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			return nil
		},
	}
	if err := ValidateExtensionProvider("test-provider", base, nil, streaming); err == nil ||
		!strings.Contains(err.Error(), `"api" is required when registering streamSimple`) {
		t.Fatalf("err = %v", err)
	}

	// Valid extension passes.
	if err := ValidateExtensionProvider("test-provider", base, nil, &ProviderConfigInput{APIKey: "k", HasAPIKey: true}); err != nil {
		t.Fatalf("valid err = %v", err)
	}
}

func TestResolveCompatibilityRequestConfig(t *testing.T) {
	base := baseProviderWithModels()
	model := base.models[0]

	config := LoadModelConfig("")
	config.providers["test-provider"] = ModelsJsonProvider{
		Headers: map[string]string{"X-Provider": "provider-value"},
	}

	extension := &ProviderConfigInput{
		Headers: map[string]string{"X-Ext": "ext-value"},
	}

	resolved := ResolveCompatibilityRequestConfig(model, config.GetProvider("test-provider"), extension)
	if resolved.Headers["X-Provider"] != "provider-value" || resolved.Headers["X-Ext"] != "ext-value" {
		t.Fatalf("headers = %v", resolved.Headers)
	}
	if resolved.AuthHeader {
		t.Fatal("authHeader default false")
	}

	// Model-level headers layer on top.
	withModelHeaders := LoadModelConfig("")
	withModelHeaders.providers["test-provider"] = ModelsJsonProvider{
		Headers: map[string]string{"X-P": "p"},
		Models: []ModelsJsonModel{
			{ID: "model-a", Headers: map[string]string{"X-P": "model-level"}},
		},
	}
	resolved = ResolveCompatibilityRequestConfig(model, withModelHeaders.GetProvider("test-provider"), nil)
	if resolved.Headers["X-P"] != "model-level" {
		t.Fatalf("model header = %v", resolved.Headers)
	}
}

func TestConfiguredRequestAuthStatus(t *testing.T) {
	// No key configured.
	if status := ConfiguredRequestAuthStatus(nil, nil); status != nil {
		t.Fatalf("nil status = %+v", status)
	}

	// Literal key.
	config := &ModelsJsonProvider{APIKey: "sk-literal"}
	status := ConfiguredRequestAuthStatus(config, nil)
	if status == nil || !status.Configured || status.Source != "models_json_key" {
		t.Fatalf("literal = %+v", status)
	}

	// Command key.
	config = &ModelsJsonProvider{APIKey: "!echo key"}
	status = ConfiguredRequestAuthStatus(config, nil)
	if status == nil || status.Source != "models_json_command" {
		t.Fatalf("command = %+v", status)
	}

	// Env var key, configured.
	t.Setenv("TEST_AUTH_VAR", "value")
	config = &ModelsJsonProvider{APIKey: "$TEST_AUTH_VAR"}
	status = ConfiguredRequestAuthStatus(config, nil)
	if status == nil || !status.Configured || status.Source != "environment" || status.Label != "TEST_AUTH_VAR" {
		t.Fatalf("env = %+v", status)
	}

	// Env var key, missing.
	config = &ModelsJsonProvider{APIKey: "$MISSING_AUTH_VAR_XYZ"}
	status = ConfiguredRequestAuthStatus(config, nil)
	if status == nil || status.Configured {
		t.Fatalf("missing env = %+v", status)
	}

	// Extension key uses "fallback" source.
	hasKey := true
	status = ConfiguredRequestAuthStatus(nil, &ProviderConfigInput{APIKey: "k", HasAPIKey: hasKey})
	if status == nil || status.Source != "fallback" {
		t.Fatalf("fallback = %+v", status)
	}
}

func TestMergeCompat(t *testing.T) {
	base := json.RawMessage(`{"a":1,"nested":{"x":1,"y":2}}`)
	override := json.RawMessage(`{"b":2,"nested":{"y":3,"z":4}}`)
	merged := mergeCompat(base, override)

	var parsed map[string]any
	if err := json.Unmarshal(merged, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed["a"] != float64(1) || parsed["b"] != float64(2) {
		t.Fatalf("top = %v", parsed)
	}
	nested, _ := parsed["nested"].(map[string]any)
	if nested["x"] != float64(1) || nested["y"] != float64(3) || nested["z"] != float64(4) {
		t.Fatalf("nested = %v", nested)
	}

	// Empty override returns base.
	if string(mergeCompat(base, nil)) != string(base) {
		t.Fatal("empty override should return base")
	}
}

func TestComposeModelProviderOAuthInheritance(t *testing.T) {
	base := baseProviderWithModels()
	base.oauth = &ai.ProviderOAuthAuth{Name: "Base OAuth"}
	base.apiKey = nil

	// OAuth-only base without extension key: preserved.
	composed, err := ComposeModelProvider("test-provider", base, emptyModelConfig(), nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if composed.Auth().OAuth == nil || composed.Auth().OAuth.Name != "Base OAuth" {
		t.Fatalf("oauth = %+v", composed.Auth().OAuth)
	}

	// Extension OAuth replaces.
	extOAuth := &ProviderConfigInput{OAuth: &ExtensionOAuthConfig{Name: "Ext OAuth"}, HasAPIKey: false}
	composed, err = ComposeModelProvider("test-provider", base, emptyModelConfig(), extOAuth)
	if err != nil {
		t.Fatalf("compose ext: %v", err)
	}
	if composed.Auth().OAuth == nil || composed.Auth().OAuth.Name != "Ext OAuth" {
		t.Fatalf("ext oauth = %+v", composed.Auth().OAuth)
	}
}

func TestComposeModelProviderEnvKeyResolution(t *testing.T) {
	base := baseProviderWithModels()
	t.Setenv("COMPOSER_TEST_KEY", "resolved-key")

	config := LoadModelConfig("")
	config.providers["test-provider"] = ModelsJsonProvider{APIKey: "$COMPOSER_TEST_KEY", BaseURL: "https://x"}

	composed, err := ComposeModelProvider("test-provider", base, config, nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	result, resolveErr := composed.Auth().APIKey.Resolve(ai.AuthResolveInput{})
	if resolveErr != nil || result == nil || result.Auth.APIKey == nil || *result.Auth.APIKey != "resolved-key" {
		t.Fatalf("resolve = %+v err=%v", result, resolveErr)
	}
	if result.Source != "configured API key" {
		t.Fatalf("source = %q", result.Source)
	}
}

// recordingBaseProvider captures transport delegation from a composed
// provider.
type recordingBaseProvider struct {
	*stubBaseProvider
	streamCalls       int
	streamSimpleCalls int
}

func (p *recordingBaseProvider) Stream(model *ai.Model, context *ai.TranscriptContext, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	p.streamCalls++
	stream := ai.NewAssistantMessageEventStream()
	go func() {
		text := ai.TextContent{Text: "delegated"}
		assistant := &ai.AssistantMessage{Content: []ai.ContentBlock{text}}
		stream.Push(&ai.EventStart{Partial: assistant})
		stream.Push(&ai.EventDone{Reason: "stop", Message: assistant})
	}()
	return stream
}

func (p *recordingBaseProvider) StreamSimple(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	p.streamSimpleCalls++
	return p.Stream(model, context, &options.StreamOptions)
}

func TestComposedProviderDelegatesTransport(t *testing.T) {
	base := &recordingBaseProvider{stubBaseProvider: baseProviderWithModels()}
	composed, err := ComposeModelProvider("test-provider", base, emptyModelConfig(), nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	model := composed.GetModels()[0]

	if stream := composed.Stream(model, ai.NewTranscriptContext(nil), &ai.StreamOptions{}); stream == nil {
		t.Fatal("composed Stream returned nil")
	}
	if base.streamCalls != 1 {
		t.Fatalf("base stream calls = %d, want 1", base.streamCalls)
	}
	if stream := composed.StreamSimple(model, ai.NewTranscriptContext(nil), &ai.SimpleStreamOptions{}); stream == nil {
		t.Fatal("composed StreamSimple returned nil")
	}
	if base.streamSimpleCalls != 1 {
		t.Fatalf("base streamSimple calls = %d, want 1", base.streamSimpleCalls)
	}
}

func TestComposedProviderWithoutBaseFailsLoud(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/models.json"
	if err := os.WriteFile(path, []byte(`{
		"providers": {
			"orphan": {
				"name": "Orphan",
				"baseUrl": "https://orphan.example.com/v1",
				"apiKey": "static-key",
				"api": "openai-completions",
				"models": [{"id": "orphan-1"}]
			}
		}
	}`), 0o644); err != nil {
		t.Fatalf("models.json: %v", err)
	}
	config := LoadModelConfig(path)
	composed, err := ComposeModelProvider("orphan", nil, config, nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	model := composed.GetModels()[0]

	stream := composed.Stream(model, ai.NewTranscriptContext(nil), &ai.StreamOptions{})
	if stream == nil {
		t.Fatal("composed Stream must not return nil")
	}
	for {
		if _, ok := stream.Next(); !ok {
			break
		}
	}
	result := stream.Result()
	if result == nil || result.StopReason != ai.StopError || result.ErrorMessage == nil {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(*result.ErrorMessage, "no stream transport") {
		t.Fatalf("error = %q", *result.ErrorMessage)
	}
}
