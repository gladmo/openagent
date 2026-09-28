package ai

// providers_all_test.go ports the providers.test.ts / model-data essentials:
// registry wiring, catalog loading, model lookups, and auth resolution
// against env overrides.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func TestBuiltinModelsRegistry(t *testing.T) {
	models := BuiltinModels(CreateModelsOptions{AuthContext: fakeAuthContext{env: map[string]string{
		"ANTHROPIC_API_KEY":  "sk-a",
		"OPENAI_API_KEY":     "sk-o",
		"DEEPSEEK_API_KEY":   "sk-d",
		"OPENROUTER_API_KEY": "sk-r",
		"ZAI_API_KEY":        "sk-z",
	}}})

	ids := []string{}
	for _, provider := range models.GetProviders() {
		ids = append(ids, provider.ID())
	}
	// Map order is not stable; sort before comparing.
	sortedIDs := append([]string(nil), ids...)
	for i := 1; i < len(sortedIDs); i++ {
		for j := i; j > 0 && sortedIDs[j] < sortedIDs[j-1]; j-- {
			sortedIDs[j], sortedIDs[j-1] = sortedIDs[j-1], sortedIDs[j]
		}
	}
	wantSorted := "anthropic,deepseek,openai,openai-codex,openrouter,zai,zai-coding-cn"
	if strings.Join(sortedIDs, ",") != wantSorted {
		t.Fatalf("unexpected providers: %v", sortedIDs)
	}

	if model := models.GetModel("anthropic", "claude-opus-5"); model == nil {
		t.Fatal("expected claude-opus-5 in the anthropic catalog")
	}
	if model := models.GetModel("openai", "gpt-5.1"); model == nil {
		t.Fatal("expected gpt-5.1 in the openai catalog")
	}
	if model := models.GetModel("deepseek", "deepseek-v4-pro"); model == nil {
		t.Fatal("expected deepseek-v4-pro in the deepseek catalog")
	}
	if model := models.GetModel("zai", "glm-5.2"); model == nil {
		t.Fatal("expected glm-5.2 in the zai catalog")
	}
	if model := models.GetModel("openrouter", "anthropic/claude-opus-5"); model == nil {
		t.Fatal("expected anthropic/claude-opus-5 in the openrouter catalog")
	}
}

func TestCatalogLoadsAllShards(t *testing.T) {
	shards, manifest, err := LoadModelCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) < 42 {
		t.Fatalf("expected >= 42 shards, got %d", len(shards))
	}
	if manifest.SchemaVersion != 6 {
		t.Fatalf("expected schema version 6, got %d", manifest.SchemaVersion)
	}
	if manifest.GeneratedAt == "" || manifest.StructureHash == "" {
		t.Fatal("expected manifest metadata")
	}
	for _, provider := range []string{"anthropic", "openai", "openai-codex", "deepseek", "zai", "zai-coding-cn", "openrouter"} {
		if len(shards[provider].Chat) == 0 {
			t.Errorf("expected chat models for %s", provider)
		}
	}
	// openrouter catalog carries image/classifier models beyond chat.
	nonChat := 0
	for _, entry := range shards["openrouter"].All {
		if entry.Model.Type != "" && entry.Model.Type != "chat" {
			nonChat++
		}
	}
	if nonChat == 0 {
		t.Error("expected non-chat entries in the openrouter shard")
	}
}

func TestCatalogModelDecoding(t *testing.T) {
	deepseek := GetCatalogChatModels("deepseek")
	if len(deepseek) == 0 {
		t.Fatal("no deepseek models")
	}
	var pro *Model
	for _, model := range deepseek {
		if model.ID == "deepseek-v4-pro" {
			pro = model
			break
		}
	}
	if pro == nil {
		t.Fatal("missing deepseek-v4-pro")
	}
	if !pro.Reasoning {
		t.Error("expected reasoning model")
	}
	if pro.API != "openai-completions" || pro.Provider != "deepseek" {
		t.Errorf("unexpected identity: %s/%s", pro.Provider, pro.API)
	}
	if compat, ok := JxCompatObj(pro); !ok {
		t.Error("expected compat metadata")
	} else if format, _ := JxString(compat, "thinkingFormat"); format != "deepseek" {
		t.Errorf("expected deepseek thinking format, got %q", format)
	}
	if pro.ThinkingLevelMap == nil || pro.ThinkingLevelMap["high"] == nil || *pro.ThinkingLevelMap["high"] != "high" {
		t.Error("expected thinking level map high->high")
	}
	if pro.ThinkingLevelMap["minimal"] != nil {
		t.Error("expected minimal -> null")
	}
}

func TestOpenRouterModelAPIRouting(t *testing.T) {
	provider := OpenRouterProvider()
	models := provider.GetModels()
	anthropicCount, completionsCount := 0, 0
	for _, model := range models {
		switch model.API {
		case "anthropic-messages":
			anthropicCount++
		case "openai-completions":
			completionsCount++
		default:
			t.Errorf("unexpected api in openrouter chat models: %s", model.API)
		}
	}
	if anthropicCount == 0 || completionsCount == 0 {
		t.Fatalf("expected both apis, got anthropic=%d completions=%d", anthropicCount, completionsCount)
	}
}

func TestProviderAuthResolutionViaRegistry(t *testing.T) {
	models := BuiltinModels(CreateModelsOptions{AuthContext: fakeAuthContext{env: map[string]string{
		"DEEPSEEK_API_KEY": "sk-deepseek",
	}}})

	result, ok := models.GetAuth("deepseek")
	if !ok || result == nil {
		t.Fatal("expected configured auth for deepseek")
	}
	if result.Auth.APIKey == nil || *result.Auth.APIKey != "sk-deepseek" {
		t.Fatalf("expected env key, got %+v", result)
	}
	if result.Source != "DEEPSEEK_API_KEY" {
		t.Fatalf("expected source env var, got %q", result.Source)
	}

	if _, ok := models.GetAuth("openai"); ok {
		t.Fatal("openai should be unconfigured without OPENAI_API_KEY")
	}
}

func TestModelsStreamRequiresConfiguredAuth(t *testing.T) {
	models := BuiltinModels(CreateModelsOptions{AuthContext: fakeAuthContext{}})
	model := models.GetModel("anthropic", "claude-opus-5")
	if model == nil {
		t.Fatal("missing anthropic model")
	}
	_, result := collectStream(t, models.Stream(model, Context{Messages: []Message{&UserMessage{Content: StringContent("hi"), TimestampMs: 1}}}, &StreamOptions{}))
	if result.StopReason != StopError {
		t.Fatalf("expected error, got %s", result.StopReason)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "Provider is not configured") {
		t.Fatalf("unexpected error: %v", result.ErrorMessage)
	}
}

func TestBuiltinModelDataGeneratedAt(t *testing.T) {
	generatedAt := GetBuiltinModelDataGeneratedAt()
	if generatedAt == "" {
		t.Fatal("expected snapshot generation timestamp")
	}
	_ = jsonx.NewObj()
}
