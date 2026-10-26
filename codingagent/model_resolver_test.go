package codingagent

// Tests for model resolution (ported from model-resolver.test.ts surfaces:
// exact/partial matching, alias preference, thinking-level splitting, glob
// scoping, dedup, fallback construction).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
)

func testModel(provider, id string) *ai.Model {
	return &ai.Model{Provider: provider, ID: id, Name: id, API: "anthropic-messages", Input: []string{"text"}}
}

func resolverTestModels() []*ai.Model {
	return []*ai.Model{
		testModel("anthropic", "claude-sonnet-4-5"),
		testModel("anthropic", "claude-sonnet-4-5-20250929"),
		testModel("anthropic", "claude-opus-4-8"),
		testModel("openai", "gpt-5.5"),
		testModel("openai", "gpt-5.5-mini"),
		testModel("openrouter", "moonshotai/kimi-k2.6"),
		testModel("zai", "glm-5.3"),
	}
}

func TestFindExactModelReferenceMatch(t *testing.T) {
	models := resolverTestModels()

	// Canonical provider/id.
	if match := FindExactModelReferenceMatch("anthropic/claude-opus-4-8", models); match == nil || match.ID != "claude-opus-4-8" {
		t.Fatalf("canonical = %v", match)
	}
	// Case-insensitive.
	if match := FindExactModelReferenceMatch("Anthropic/CLAUDE-OPUS-4-8", models); match == nil {
		t.Fatal("case-insensitive canonical failed")
	}
	// Bare id.
	if match := FindExactModelReferenceMatch("gpt-5.5", models); match == nil || match.Provider != "openai" {
		t.Fatalf("bare = %v", match)
	}
	// Id-with-slash model via canonical reference.
	if match := FindExactModelReferenceMatch("openrouter/moonshotai/kimi-k2.6", models); match == nil {
		t.Fatal("slash id failed")
	}
	// Missing.
	if match := FindExactModelReferenceMatch("nope", models); match != nil {
		t.Fatalf("missing = %v", match)
	}
	// Empty.
	if match := FindExactModelReferenceMatch("  ", models); match != nil {
		t.Fatalf("empty = %v", match)
	}
}

func TestTryMatchAliasPreference(t *testing.T) {
	models := resolverTestModels()

	// Partial "claude-sonnet" prefers the alias over the dated version.
	result := ParseModelPattern("claude-sonnet", models, nil)
	if result.Model == nil || result.Model.ID != "claude-sonnet-4-5" {
		t.Fatalf("alias = %+v", result.Model)
	}
}

func TestParseModelPatternThinkingLevel(t *testing.T) {
	models := resolverTestModels()

	// pattern:high splits into model + level.
	result := ParseModelPattern("claude-opus-4-8:high", models, nil)
	if result.Model == nil || result.Model.ID != "claude-opus-4-8" {
		t.Fatalf("model = %+v", result.Model)
	}
	if !result.HasThinkingLevel || result.ThinkingLevel != "high" {
		t.Fatalf("level = %q has=%v", result.ThinkingLevel, result.HasThinkingLevel)
	}

	// Invalid suffix warns and recurses.
	result = ParseModelPattern("claude-opus-4-8:ultra", models, nil)
	if result.Model == nil || result.Warning == "" || !strings.Contains(result.Warning, "Invalid thinking level") {
		t.Fatalf("invalid = %+v warning=%q", result.Model, result.Warning)
	}
	if result.HasThinkingLevel {
		t.Fatal("invalid level must not set thinking level")
	}

	// Strict mode: invalid suffix fails instead of falling back.
	strict := false
	result = ParseModelPattern("claude-opus-4-8:ultra", models, &ParseModelOptions{AllowInvalidThinkingLevelFallback: &strict})
	if result.Model != nil {
		t.Fatalf("strict = %+v", result.Model)
	}

	// No match without colons.
	if result := ParseModelPattern("does-not-exist", models, nil); result.Model != nil {
		t.Fatalf("no match = %+v", result.Model)
	}
}

func TestResolveModelScopeExact(t *testing.T) {
	models := resolverTestModels()
	result := ResolveModelScopeFromModels([]string{"anthropic/claude-opus-4-8", "zai/glm-5.3"}, models)
	if len(result.ScopedModels) != 2 {
		t.Fatalf("scoped = %d", len(result.ScopedModels))
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

func TestResolveModelScopeGlob(t *testing.T) {
	models := resolverTestModels()

	result := ResolveModelScopeFromModels([]string{"anthropic/*"}, models)
	if len(result.ScopedModels) != 3 {
		t.Fatalf("provider glob = %d", len(result.ScopedModels))
	}

	// Partial-content glob across providers.
	result = ResolveModelScopeFromModels([]string{"*sonnet*"}, models)
	if len(result.ScopedModels) != 2 {
		t.Fatalf("content glob = %d", len(result.ScopedModels))
	}

	// Glob with thinking level suffix.
	result = ResolveModelScopeFromModels([]string{"anthropic/claude-opus-4-8:high"}, models)
	if len(result.ScopedModels) != 1 {
		t.Fatalf("glob+level = %d", len(result.ScopedModels))
	}
	if !result.ScopedModels[0].HasThinkingLevel || result.ScopedModels[0].ThinkingLevel != "high" {
		t.Fatalf("level = %+v", result.ScopedModels[0])
	}

	// No match diagnostic.
	result = ResolveModelScopeFromModels([]string{"*zzz*"}, models)
	if len(result.ScopedModels) != 0 || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "no-match" {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

func TestResolveModelScopeDedup(t *testing.T) {
	models := resolverTestModels()
	result := ResolveModelScopeFromModels([]string{"gpt-5.5", "openai/gpt-5.5", "gpt-5.5"}, models)
	if len(result.ScopedModels) != 1 {
		t.Fatalf("dedup = %d", len(result.ScopedModels))
	}
}

func TestResolveModelScopePartialWithLevel(t *testing.T) {
	models := resolverTestModels()
	result := ResolveModelScopeFromModels([]string{"claude-opus:medium"}, models)
	if len(result.ScopedModels) != 1 {
		t.Fatalf("scoped = %+v", result.ScopedModels)
	}
	if !result.ScopedModels[0].HasThinkingLevel || result.ScopedModels[0].ThinkingLevel != "medium" {
		t.Fatalf("level = %+v", result.ScopedModels[0])
	}
}

func TestBuildFallbackModel(t *testing.T) {
	// Unknown id on a known provider builds a custom model from the default.
	fallback := buildFallbackModelFromList([]*ai.Model{referenceModel("anthropic", "claude-sonnet-4-5")}, "claude-future-9")
	if fallback == nil || fallback.ID != "claude-future-9" || fallback.Name != "claude-future-9" {
		t.Fatalf("fallback = %+v", fallback)
	}
	if fallback.Provider != "anthropic" {
		t.Fatalf("provider = %q", fallback.Provider)
	}

	// Unknown provider yields nil.
	if fallback := buildFallbackModelFromList(nil, "x"); fallback != nil {
		t.Fatalf("unknown provider = %+v", fallback)
	}
}

func TestIsAlias(t *testing.T) {
	if !isAlias("claude-sonnet-4-5") {
		t.Fatal("plain id should be alias")
	}
	if !isAlias("devstral-medium-latest") {
		t.Fatal("-latest should be alias")
	}
	if isAlias("claude-sonnet-4-5-20250929") {
		t.Fatal("dated id should not be alias")
	}
}

func TestDefaultModelPerProviderCoverage(t *testing.T) {
	if DefaultModelPerProvider["anthropic"] != "claude-opus-4-8" {
		t.Fatalf("anthropic default = %q", DefaultModelPerProvider["anthropic"])
	}
	if DefaultModelPerProvider["zai"] != "glm-5.3" {
		t.Fatalf("zai default = %q", DefaultModelPerProvider["zai"])
	}
}

func TestResolveCliModel(t *testing.T) {
	runtime := newTestRuntime(t, "")
	cases := []struct {
		name        string
		provider    string
		model       string
		wantModel   string // "provider/id" or "" for no model
		wantLevel   string
		wantWarning string
		wantError   string
	}{
		{
			name:      "provider and model",
			provider:  "zai",
			model:     "glm-5.3",
			wantModel: "zai/glm-5.3",
		},
		{
			name:      "slash inference",
			model:     "zai/glm-5.3",
			wantModel: "zai/glm-5.3",
		},
		{
			name:      "case-insensitive provider",
			provider:  "ZAI",
			model:     "glm-5.3",
			wantModel: "zai/glm-5.3",
		},
		{
			name:      "redundant slash prefix tolerated",
			provider:  "zai",
			model:     "zai/glm-5.3",
			wantModel: "zai/glm-5.3",
		},
		{
			name:      "thinking suffix parsed",
			model:     "zai/glm-5.3:high",
			wantModel: "zai/glm-5.3",
			wantLevel: "high",
		},
		{
			name:      "unknown provider errors",
			provider:  "nope",
			model:     "whatever",
			wantError: `Unknown provider "nope"`,
		},
		{
			name:        "unknown model for known provider falls back to custom id",
			provider:    "zai",
			model:       "glm-9.9",
			wantModel:   "zai/glm-9.9",
			wantWarning: `Model "glm-9.9" not found for provider "zai". Using custom model ID.`,
		},
		{
			name:      "empty cli model resolves nothing",
			model:     "",
			wantModel: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := ResolveCliModel(ResolveCliModelOptions{
				CliProvider:  c.provider,
				CliModel:     c.model,
				ModelRuntime: runtime,
			})
			switch {
			case c.wantError != "":
				if result.Error == "" || !strings.Contains(result.Error, c.wantError) {
					t.Fatalf("error = %q, want containing %q", result.Error, c.wantError)
				}
				if result.Model != nil {
					t.Fatalf("model = %v, want nil with error", result.Model)
				}
			case c.wantModel == "":
				if result.Model != nil || result.Error != "" {
					t.Fatalf("result = %+v, want empty", result)
				}
			default:
				if result.Error != "" {
					t.Fatalf("unexpected error: %s", result.Error)
				}
				if result.Model == nil {
					t.Fatal("no model resolved")
				}
				if got := result.Model.Provider + "/" + result.Model.ID; got != c.wantModel {
					t.Fatalf("model = %s, want %s", got, c.wantModel)
				}
				if result.ThinkingLevel != c.wantLevel {
					t.Fatalf("thinking level = %q, want %q", result.ThinkingLevel, c.wantLevel)
				}
				if c.wantWarning == "" && result.Warning != "" {
					t.Fatalf("unexpected warning: %s", result.Warning)
				}
				if c.wantWarning != "" && !strings.Contains(result.Warning, c.wantWarning) {
					t.Fatalf("warning = %q, want containing %q", result.Warning, c.wantWarning)
				}
			}
		})
	}
}

func TestResolveCliModelBareIdAmbiguityPrefersAuth(t *testing.T) {
	// my-gateway is the only authenticated provider; a bare id that exists
	// there resolves to it over the unauthenticated duplicate.
	runtime := newTestRuntime(t, `{
		"providers": {
			"my-gateway": {
				"name": "My Gateway",
				"baseUrl": "https://gw.example.com/v1",
				"apiKey": "static-key",
				"api": "openai-completions",
				"models": [{"id": "shared-model"}]
			}
		}
	}`)
	if err := runtime.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	result := ResolveCliModel(ResolveCliModelOptions{CliModel: "shared-model", ModelRuntime: runtime})
	if result.Error != "" || result.Model == nil {
		t.Fatalf("result = %+v", result)
	}
	if result.Model.Provider != "my-gateway" {
		t.Fatalf("provider = %s, want my-gateway", result.Model.Provider)
	}
}
