package codingagent

// Port of model-resolver.test.ts resolveModelReference table (d7f7e0dfe).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
)

func referenceModel(provider, id string) *ai.Model {
	return &ai.Model{
		ID:            id,
		Name:          id,
		API:           "openai-completions",
		Provider:      provider,
		BaseURL:       "https://example.invalid",
		Reasoning:     true,
		Input:         []string{"text"},
		ContextWindow: 128000,
		MaxTokens:     8192,
	}
}

var referenceCatalog = []*ai.Model{
	referenceModel("anthropic", "claude-sonnet-4-5"),
	referenceModel("anthropic", "claude-sonnet-4-5-20250929"),
	referenceModel("anthropic", "claude-opus-4-8"),
	referenceModel("anthropic", "exact"),
	// Fuzzy-matches "shared"; an ambiguous exact "shared" must not fall
	// through to it.
	referenceModel("anthropic", "shared-pro"),
	referenceModel("openai", "gpt-4o"),
	referenceModel("openrouter", "qwen/qwen3-coder:exacto"),
	referenceModel("openrouter", "openai/gpt-4o:extended"),
	referenceModel("gw-a", "shared"),
	referenceModel("gw-a", "anthropic/future"),
	referenceModel("gw-a", "anthropic/exact"),
	referenceModel("gw-a", "anthropic/claude-opus-4.8"),
	referenceModel("gw-a", "anthropic/claude-opus"),
	referenceModel("gw-b", "shared"),
	referenceModel("gw-b", "anthropic/future"),
	referenceModel("gw-b", "claude-opus-4-8"),
	referenceModel("gw-b", "gw-b/free"),
	referenceModel("gw-b", "zz:free"),
}

func referenceKey(model *ai.Model) string { return model.Provider + "/" + model.ID }

func referenceNames(models []*ai.Model) []string {
	out := make([]string, 0, len(models))
	for _, model := range models {
		out = append(out, referenceKey(model))
	}
	return out
}

func referenceUsable(usable []string) func(string) bool {
	return func(provider string) bool {
		for _, candidate := range usable {
			if candidate == provider {
				return true
			}
		}
		return false
	}
}

func TestResolveModelReferenceTable(t *testing.T) {
	type resolved struct {
		model         string
		thinkingLevel string
		warningPrefix string
	}
	type ambiguous struct {
		candidates  []string
		usableCount int
	}
	cases := []struct {
		name           string
		text           string
		expected       any // resolved | ambiguous | "not-found"
		usable         []string
		provider       string
		thinkingSuffix ThinkingSuffixMode
		fuzzy          bool
	}{
		// Reference syntax
		{name: "exact id", text: "claude-sonnet-4-5", expected: resolved{model: "anthropic/claude-sonnet-4-5"}},
		{name: "exact id is case-insensitive", text: "GPT-4O", expected: resolved{model: "openai/gpt-4o"}},
		{name: "fuzzy prefers aliases over dated ids", text: "sonnet", expected: resolved{model: "anthropic/claude-sonnet-4-5"}},
		{name: "thinking suffix", text: "sonnet:high", expected: resolved{model: "anthropic/claude-sonnet-4-5", thinkingLevel: "high"}},
		{name: "colon in a literal id is not a thinking suffix", text: "qwen/qwen3-coder:exacto", expected: resolved{model: "openrouter/qwen/qwen3-coder:exacto"}},
		{name: "provider prefix, colon id, and thinking suffix", text: "openrouter/qwen/qwen3-coder:exacto:high", expected: resolved{model: "openrouter/qwen/qwen3-coder:exacto", thinkingLevel: "high"}},
		{name: "raw id wins when the provider prefix names no matching model", text: "openai/gpt-4o:extended", expected: resolved{model: "openrouter/openai/gpt-4o:extended"}},
		{name: "fuzzy searches the prefixed provider first", text: "openrouter/qwen", expected: resolved{model: "openrouter/qwen/qwen3-coder:exacto"}},
		{name: "strict keeps an invalid suffix in the id", text: "gpt-4o:random", expected: "not-found"},
		{name: "lenient drops an invalid suffix with a warning", text: "gpt-4o:random", thinkingSuffix: ThinkingSuffixLenient, expected: resolved{model: "openai/gpt-4o", warningPrefix: `Invalid thinking level "random"`}},
		{name: "lenient invalid outer suffix discards the inner thinking level", text: "qwen/qwen3-coder:exacto:high:random", thinkingSuffix: ThinkingSuffixLenient, expected: resolved{model: "openrouter/qwen/qwen3-coder:exacto", warningPrefix: `Invalid thinking level "random"`}},
		{name: "none keeps the suffix in the id", text: "gpt-4o:high", thinkingSuffix: ThinkingSuffixNone, expected: "not-found"},
		{name: "exact-only skips fuzzy matches", text: "sonnet", fuzzy: false, expected: "not-found", thinkingSuffix: ThinkingSuffixNone},
		{name: "empty text", text: "  ", expected: "not-found"},

		// Exact matches across providers (#7327)
		{name: "sole usable provider wins", text: "shared", usable: []string{"gw-b"}, expected: resolved{model: "gw-b/shared"}},
		{name: "no usable provider is ambiguous instead of falling through to fuzzy", text: "shared", expected: ambiguous{candidates: []string{"gw-a/shared", "gw-b/shared"}, usableCount: 0}},
		{name: "several usable providers are ambiguous", text: "shared", usable: []string{"gw-a", "gw-b"}, expected: ambiguous{candidates: []string{"gw-a/shared", "gw-b/shared"}, usableCount: 2}},
		{name: "thinking suffix does not change provider selection", text: "shared:high", usable: []string{"gw-b"}, expected: resolved{model: "gw-b/shared", thinkingLevel: "high"}},
		{name: "thinking suffix does not bypass ambiguity", text: "shared:high", usable: []string{"gw-a", "gw-b"}, expected: ambiguous{candidates: []string{"gw-a/shared", "gw-b/shared"}, usableCount: 2}},
		// #10160
		{name: "provider prefix without a direct model selects the usable raw id", text: "anthropic/future", usable: []string{"gw-b"}, expected: resolved{model: "gw-b/anthropic/future"}},
		// #5823
		{name: "usable qualified match beats a usable raw id", text: "anthropic/exact", usable: []string{"anthropic", "gw-a"}, expected: resolved{model: "anthropic/exact"}},
		// #5643
		{name: "usable raw id beats an unusable qualified match", text: "anthropic/exact", usable: []string{"gw-a"}, expected: resolved{model: "gw-a/anthropic/exact"}},
		{name: "unusable qualified match is kept when nothing is usable", text: "anthropic/exact", expected: resolved{model: "anthropic/exact"}},
		{name: "named provider's usable fuzzy match beats a raw id elsewhere", text: "anthropic/claude-opus", usable: []string{"anthropic"}, expected: resolved{model: "anthropic/claude-opus-4-8"}},
		{name: "usable raw id beats the named provider's unusable fuzzy match", text: "anthropic/claude-opus", usable: []string{"gw-a"}, expected: resolved{model: "gw-a/anthropic/claude-opus"}},
		{name: "named provider's fuzzy match is kept when nothing is usable", text: "anthropic/claude-opus", expected: resolved{model: "anthropic/claude-opus-4-8"}},
		{name: "named provider's literal id beats its fuzzy matches", text: "gw-b/free", expected: resolved{model: "gw-b/gw-b/free"}},

		// Fuzzy matches across providers
		{name: "fuzzy prefers usable providers", text: "opus", usable: []string{"gw-a"}, expected: resolved{model: "gw-a/anthropic/claude-opus-4.8"}},
		{name: "fuzzy id tie across usable providers is ambiguous", text: "opus", usable: []string{"anthropic", "gw-a", "gw-b"}, expected: ambiguous{candidates: []string{"anthropic/claude-opus-4-8", "gw-b/claude-opus-4-8"}, usableCount: 2}},
		{name: "fuzzy id tie resolves to the sole usable provider", text: "opus", usable: []string{"anthropic"}, expected: resolved{model: "anthropic/claude-opus-4-8"}},

		// Explicit provider constraint
		{name: "explicit provider ignores usable providers elsewhere", text: "shared", provider: "gw-a", usable: []string{"gw-b"}, expected: resolved{model: "gw-a/shared"}},
		{name: "explicit provider accepts its own prefix", text: "GW-B/shared", provider: "gw-b", expected: resolved{model: "gw-b/shared"}},
		{name: "explicit provider allows fuzzy matches", text: "4o", provider: "openai", expected: resolved{model: "openai/gpt-4o"}},
		{name: "explicit provider never leaves the provider", text: "gpt-4o", provider: "gw-a", expected: "not-found"},
	}

	// The TS default request has fuzzy=true and thinkingSuffix=strict;
	// the Go zero values differ, so set them per case.
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			request := ModelReferenceRequest{
				Text:           tc.text,
				Provider:       tc.provider,
				ThinkingSuffix: tc.thinkingSuffix,
				Fuzzy:          tc.fuzzy,
			}
			if request.ThinkingSuffix == "" && !tc.fuzzy {
				request.ThinkingSuffix = ThinkingSuffixStrict
				request.Fuzzy = true
			}
			result := ResolveModelReference(request, referenceCatalog, referenceUsable(tc.usable))
			switch expected := tc.expected.(type) {
			case resolved:
				if result.Status != "resolved" {
					t.Fatalf("status = %s (%+v)", result.Status, result)
				}
				if referenceKey(result.Model) != expected.model {
					t.Fatalf("model = %s, want %s", referenceKey(result.Model), expected.model)
				}
				if result.ThinkingLevel != expected.thinkingLevel {
					t.Fatalf("thinking = %q, want %q", result.ThinkingLevel, expected.thinkingLevel)
				}
				if expected.warningPrefix != "" && !strings.HasPrefix(result.Warning, expected.warningPrefix) {
					t.Fatalf("warning = %q", result.Warning)
				}
			case ambiguous:
				if result.Status != "ambiguous" {
					t.Fatalf("status = %s (model %v)", result.Status, result.Model)
				}
				got := strings.Join(referenceNames(result.Candidates), ",")
				want := strings.Join(expected.candidates, ",")
				if got != want {
					t.Fatalf("candidates = %s, want %s", got, want)
				}
				if result.UsableCount != expected.usableCount {
					t.Fatalf("usableCount = %d, want %d", result.UsableCount, expected.usableCount)
				}
			default:
				if result.Status != "not-found" {
					t.Fatalf("status = %s (%+v)", result.Status, result)
				}
			}
		})
	}
}

// ProviderModels must stay inside the target provider (review of d7f7e0dfe):
// with gpt-6.1-sol on both github-copilot and openai-codex, an
// openai-codex custom id must clone an openai-codex model.
func TestReferenceTargetProviderModelsStayInProvider(t *testing.T) {
	models := []*ai.Model{
		referenceModel("github-copilot", "gpt-6.1-sol"),
		referenceModel("openai-codex", "gpt-6.1-sol"),
		referenceModel("openai-codex", "other"),
	}
	result := ResolveModelReference(ModelReferenceRequest{Text: "openai-codex/brand-new-id"}, models, referenceUsable(nil))
	if result.Status != "not-found" || !result.HasTarget {
		t.Fatalf("result = %+v", result)
	}
	for _, model := range result.Target.ProviderModels {
		if model.Provider != "openai-codex" {
			t.Fatalf("foreign model %s/%s in target", model.Provider, model.ID)
		}
	}
	if len(result.Target.ProviderModels) != 2 {
		t.Fatalf("providerModels = %v", result.Target.ProviderModels)
	}
}

// --api-key does not choose the provider (d7f7e0dfe inlineApiKey).
func TestResolveCliModelInlineApiKeyDoesNotChooseProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvAgentDirName(), dir)
	runtime, err := NewModelRuntime(&CreateModelRuntimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result := ResolveCliModel(ResolveCliModelOptions{
		CliModel:     "shared",
		ModelRuntime: runtime,
	})
	if result.Error == "" {
		t.Fatal("ambiguous bare id should error")
	}
	result = ResolveCliModel(ResolveCliModelOptions{
		CliModel:     "gpt-4o",
		ModelRuntime: runtime,
	})
	if result.Error != "" {
		t.Fatalf("unique id resolved: %s", result.Error)
	}
	_ = result
}
