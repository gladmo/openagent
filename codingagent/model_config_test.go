package codingagent

// Tests for config value resolution and models.json loading (ported from
// resolve-config-value.test.ts and model-config.test.ts surfaces).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigValueLiterals(t *testing.T) {
	if value, ok := ResolveConfigValue("plain-value", nil); !ok || value != "plain-value" {
		t.Fatalf("literal = %q %v", value, ok)
	}
	// $$ escapes dollar.
	if value, ok := ResolveConfigValue("a$$b", nil); !ok || value != "a$b" {
		t.Fatalf("dollar escape = %q", value)
	}
	// $! escapes bang.
	if value, ok := ResolveConfigValue("a$!b", nil); !ok || value != "a!b" {
		t.Fatalf("bang escape = %q", value)
	}
	// Lone dollar stays.
	if value, ok := ResolveConfigValue("a$ b", nil); !ok || value != "a$ b" {
		t.Fatalf("lone dollar = %q", value)
	}
	// Unterminated brace stays literal.
	if value, ok := ResolveConfigValue("${nope", nil); !ok || value != "${nope" {
		t.Fatalf("unterminated = %q", value)
	}
	// Invalid var name in braces stays literal.
	if value, ok := ResolveConfigValue("${1bad}", nil); !ok || value != "${1bad}" {
		t.Fatalf("invalid name = %q", value)
	}
}

func TestResolveConfigValueEnv(t *testing.T) {
	env := map[string]string{"MY_KEY": "secret", "OTHER": "x"}

	if value, ok := ResolveConfigValue("$MY_KEY", env); !ok || value != "secret" {
		t.Fatalf("bare = %q %v", value, ok)
	}
	if value, ok := ResolveConfigValue("${MY_KEY}", env); !ok || value != "secret" {
		t.Fatalf("braced = %q", value)
	}
	if value, ok := ResolveConfigValue("prefix-$MY_KEY-suffix", env); !ok || value != "prefix-secret-suffix" {
		t.Fatalf("embedded = %q", value)
	}
	if value, ok := ResolveConfigValue("$MY_KEY$OTHER", env); !ok || value != "secretx" {
		t.Fatalf("adjacent = %q", value)
	}
	// Missing env var fails.
	if _, ok := ResolveConfigValue("$MISSING_VAR_XYZ", env); ok {
		t.Fatal("missing should fail")
	}

	// Process env fallback.
	t.Setenv("PROCESS_ENV_VAR", "from-process")
	if value, ok := ResolveConfigValue("$PROCESS_ENV_VAR", nil); !ok || value != "from-process" {
		t.Fatalf("process env = %q", value)
	}
}

func TestResolveConfigValueCommand(t *testing.T) {
	ClearConfigValueCache()

	if !IsCommandConfigValue("!echo hi") {
		t.Fatal("command not detected")
	}
	if IsCommandConfigValue("echo hi") {
		t.Fatal("literal misdetected as command")
	}

	value, ok := ResolveConfigValue("!printf hello", nil)
	if !ok || value != "hello" {
		t.Fatalf("command = %q %v", value, ok)
	}

	// Failing command fails.
	if _, ok := ResolveConfigValue("!exit 1", nil); ok {
		t.Fatal("failing command should fail")
	}
}

func TestResolveConfigValueCommandCache(t *testing.T) {
	ClearConfigValueCache()

	// First call executes and caches.
	value1, ok1 := ResolveConfigValue("!echo cached", nil)
	value2, ok2 := ResolveConfigValue("!echo cached", nil)
	if !ok1 || !ok2 || value1 != value2 {
		t.Fatalf("cache = %q/%q %v/%v", value1, value2, ok1, ok2)
	}

	// Uncached bypasses.
	if _, ok := ResolveConfigValueUncached("!echo other", nil); !ok {
		t.Fatal("uncached failed")
	}
}

func TestGetConfigValueEnvVarNames(t *testing.T) {
	if name := GetConfigValueEnvVarName("$SOLE"); name != "SOLE" {
		t.Fatalf("sole = %q", name)
	}
	if name := GetConfigValueEnvVarName("$A$B"); name != "" {
		t.Fatalf("non-sole = %q", name)
	}
	if name := GetConfigValueEnvVarName("!cmd"); name != "" {
		t.Fatalf("command = %q", name)
	}

	names := GetConfigValueEnvVarNames("$A ${B} x $A")
	if len(names) != 2 || names[0] != "A" || names[1] != "B" {
		t.Fatalf("names = %v", names)
	}

	missing := GetMissingConfigValueEnvVarNames("$A $B", map[string]string{"A": "1"})
	if len(missing) != 1 || missing[0] != "B" {
		t.Fatalf("missing = %v", missing)
	}
	if !IsConfigValueConfigured("$A $B", map[string]string{"A": "1", "B": "2"}) {
		t.Fatal("configured reported missing")
	}
}

func TestResolveConfigValueOrThrow(t *testing.T) {
	if value, err := ResolveConfigValueOrThrow("$VAR_OK", "API key", map[string]string{"VAR_OK": "v"}); err != nil || value != "v" {
		t.Fatalf("resolve = %q err=%v", value, err)
	}

	_, err := ResolveConfigValueOrThrow("$MISSING_ONE", "API key", nil)
	if err == nil || !strings.Contains(err.Error(), "environment variable: MISSING_ONE") {
		t.Fatalf("single missing = %v", err)
	}

	_, err = ResolveConfigValueOrThrow("$M1$M2", "API key", nil)
	if err == nil || !strings.Contains(err.Error(), "environment variables: M1, M2") {
		t.Fatalf("multi missing = %v", err)
	}

	_, err = ResolveConfigValueOrThrow("!false", "API key", nil)
	if err == nil || !strings.Contains(err.Error(), "shell command") {
		t.Fatalf("command = %v", err)
	}
}

func TestResolveHeaders(t *testing.T) {
	env := map[string]string{"HDR": "resolved"}
	headers := map[string]string{"a": "$HDR", "b": "literal", "c": "$MISSING"}

	resolved := ResolveHeaders(headers, env)
	if resolved == nil || resolved["a"] != "resolved" || resolved["b"] != "literal" {
		t.Fatalf("resolved = %v", resolved)
	}
	if _, has := resolved["c"]; has {
		t.Fatal("unresolved header should drop")
	}

	if ResolveHeaders(nil, nil) != nil {
		t.Fatal("nil headers should stay nil")
	}

	// OrThrow fails on missing.
	if _, err := ResolveHeadersOrThrow(headers, `provider "p"`, env); err == nil {
		t.Fatal("missing header should throw")
	}
}

func TestLoadModelConfig(t *testing.T) {
	dir := t.TempDir()

	// Valid config with comments (JSONC).
	valid := filepath.Join(dir, "models.json")
	os.WriteFile(valid, []byte(`{
		// custom provider
		"providers": {
			"custom-openai": {
				"name": "Custom OpenAI",
				"baseUrl": "https://api.example.com/v1",
				"apiKey": "$CUSTOM_KEY",
				"api": "openai-completions",
				"models": [
					{"id": "custom-model", "contextWindow": 128000, "maxTokens": 4096}
				],
				"modelOverrides": {
					"existing-model": {"contextWindow": 200000}
				}
			}
		}
	}`), 0o644)

	config := LoadModelConfig(valid)
	if config.GetError() != "" {
		t.Fatalf("error = %s", config.GetError())
	}
	provider := config.GetProvider("custom-openai")
	if provider == nil || provider.Name != "Custom OpenAI" || provider.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("provider = %+v", provider)
	}
	if len(provider.Models) != 1 || provider.Models[0].ID != "custom-model" {
		t.Fatalf("models = %+v", provider.Models)
	}
	if override, ok := provider.ModelOverrides["existing-model"]; !ok || override.ContextWindow == nil || *override.ContextWindow != 200000 {
		t.Fatalf("overrides = %+v", provider.ModelOverrides)
	}
	if ids := config.GetProviderIDs(); len(ids) != 1 || ids[0] != "custom-openai" {
		t.Fatalf("ids = %v", ids)
	}

	// Missing file: empty, no error.
	missing := LoadModelConfig(filepath.Join(dir, "nope.json"))
	if missing.GetError() != "" || len(missing.GetProviderIDs()) != 0 {
		t.Fatalf("missing = %q %v", missing.GetError(), missing.GetProviderIDs())
	}

	// Empty path: empty.
	empty := LoadModelConfig("")
	if empty.GetError() != "" || len(empty.GetProviderIDs()) != 0 {
		t.Fatal("empty path should be empty")
	}

	// Malformed JSON reports an error.
	broken := filepath.Join(dir, "broken.json")
	os.WriteFile(broken, []byte(`{"providers": nope}`), 0o644)
	if got := LoadModelConfig(broken).GetError(); !strings.Contains(got, "Failed to parse") {
		t.Fatalf("broken = %q", got)
	}

	// Schema violation reports an error.
	invalid := filepath.Join(dir, "invalid.json")
	os.WriteFile(invalid, []byte(`{"providers": {"p": {"models": [{"id": ""}]}}}`), 0o644)
	if got := LoadModelConfig(invalid).GetError(); !strings.Contains(got, "Invalid models.json schema") {
		t.Fatalf("invalid = %q", got)
	}

	// Missing providers key is invalid.
	noProviders := filepath.Join(dir, "noproviders.json")
	os.WriteFile(noProviders, []byte(`{}`), 0o644)
	if got := LoadModelConfig(noProviders).GetError(); !strings.Contains(got, "Invalid models.json schema") {
		t.Fatalf("noproviders = %q", got)
	}
}
