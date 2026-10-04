package codingagent

// Tests for the interactive helper surface (adapted from
// interactive-mode.test.ts helper cases).

import (
	"strings"
	"testing"
)

func TestFormatCrashExtensionHint(t *testing.T) {
	if got := FormatCrashExtensionHint(nil); got != "" {
		t.Fatalf("nil = %q", got)
	}
	if got := FormatCrashExtensionHint([]string{"", "  "}); got != "" {
		t.Fatalf("empty strings = %q", got)
	}

	one := FormatCrashExtensionHint([]string{"ext-a"})
	if !strings.Contains(one, "loaded extension `ext-a`") {
		t.Fatalf("one = %q", one)
	}
	if !strings.Contains(one, "disabling it with") {
		t.Fatalf("pronoun = %q", one)
	}

	two := FormatCrashExtensionHint([]string{"a", "b"})
	if !strings.Contains(two, "`a` and `b`") {
		t.Fatalf("two = %q", two)
	}
	if !strings.Contains(two, "extensions") || !strings.Contains(two, "them") {
		t.Fatalf("plural = %q", two)
	}

	three := FormatCrashExtensionHint([]string{"a", "b", "c"})
	if !strings.Contains(three, "`a`, `b`, and `c`") {
		t.Fatalf("three = %q", three)
	}
}

func TestIsAnthropicSubscriptionAuthKey(t *testing.T) {
	if !IsAnthropicSubscriptionAuthKey("sk-ant-oat123") {
		t.Fatal("subscription key not detected")
	}
	if IsAnthropicSubscriptionAuthKey("sk-ant-api123") {
		t.Fatal("api key misdetected")
	}
	if IsAnthropicSubscriptionAuthKey("") {
		t.Fatal("empty key detected")
	}
}

func TestIsUnknownModel(t *testing.T) {
	if !IsUnknownModel("unknown", "unknown", "unknown") {
		t.Fatal("unknown model not detected")
	}
	if IsUnknownModel("anthropic", "claude", "anthropic-messages") {
		t.Fatal("real model detected as unknown")
	}
}

func TestQuoteIfNeeded(t *testing.T) {
	// Safe values pass through.
	if got := QuoteIfNeeded("simple-value_v1.2/3"); got != "simple-value_v1.2/3" {
		t.Fatalf("safe = %q", got)
	}
	if got := QuoteIfNeeded(""); got != "''" {
		t.Fatalf("empty = %q", got)
	}
	// Spaces force quoting.
	if got := QuoteIfNeeded("has space"); got != "'has space'" {
		t.Fatalf("space = %q", got)
	}
	// Embedded single quotes escape.
	if got := QuoteIfNeeded("it's"); got != `'it'\''s'` {
		t.Fatalf("quote = %q", got)
	}
}

func TestFormatResumeCommand(t *testing.T) {
	dir := t.TempDir()
	manager, err := SessionManagerCreate(dir, "", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Unpersisted (no messages yet): no hint.
	if got := FormatResumeCommand(manager, true); got != "" {
		t.Fatalf("no messages = %q", got)
	}

	manager.AppendMessage(map[string]any{"role": "user", "content": "hi"})
	// TTY + persisted + file exists: hint with session id.
	got := FormatResumeCommand(manager, true)
	if got == "" {
		t.Fatal("expected hint")
	}
	if !strings.Contains(got, "--session "+manager.GetSessionID()) {
		t.Fatalf("hint = %q", got)
	}

	// Non-TTY suppresses.
	if got := FormatResumeCommand(manager, false); got != "" {
		t.Fatalf("non-tty = %q", got)
	}
}

func TestHasDefaultModelProvider(t *testing.T) {
	if !HasDefaultModelProvider("anthropic") {
		t.Fatal("anthropic missing")
	}
	if HasDefaultModelProvider("no-such-provider") {
		t.Fatal("unknown provider has default")
	}
}

func TestLlamaCppPostLoginGuidance(t *testing.T) {
	none := LlamaCppPostLoginGuidance("Logged in", 0)
	if !strings.Contains(none, "No llama.cpp models are loaded") {
		t.Fatalf("none = %q", none)
	}
	some := LlamaCppPostLoginGuidance("Logged in", 3)
	if !strings.Contains(some, "Use /model to select a loaded") {
		t.Fatalf("some = %q", some)
	}
}

func TestGetLoginProviderCompletionOptions(t *testing.T) {
	providers := []struct{ ID, Name, AuthType string }{
		{"zai", "ZAI", "api_key"},
		{"anthropic", "Anthropic", "oauth"},
		{"anthropic", "Anthropic", "api_key"},
		{"anthropic", "Anthropic", "oauth"}, // duplicate
	}

	options := GetLoginProviderCompletionOptions(providers)
	if len(options) != 2 {
		t.Fatalf("options = %+v", options)
	}
	// Sorted by name: Anthropic first.
	if options[0].ID != "anthropic" || options[1].ID != "zai" {
		t.Fatalf("order = %+v", options)
	}
	// Auth types deduped, oauth first (order map).
	anthropic := options[0]
	if len(anthropic.AuthTypes) != 2 || anthropic.AuthTypes[0] != "oauth" || anthropic.AuthTypes[1] != "api_key" {
		t.Fatalf("auth types = %v", anthropic.AuthTypes)
	}
}

func TestFormatAuthSelectorProviderType(t *testing.T) {
	if got := FormatAuthSelectorProviderType("oauth"); got != "OAuth" {
		t.Fatalf("oauth = %q", got)
	}
	if got := FormatAuthSelectorProviderType("api_key"); got != "API key" {
		t.Fatalf("api_key = %q", got)
	}
	if got := FormatAuthSelectorProviderType("other"); got != "other" {
		t.Fatalf("other = %q", got)
	}
}

func TestGetLoginProviderSearchText(t *testing.T) {
	provider := LoginProviderCompletionOption{
		ID:        "zai",
		Name:      "ZAI",
		AuthTypes: []string{"api_key"},
	}
	text := GetLoginProviderSearchText(provider)
	if !strings.Contains(text, "zai") || !strings.Contains(text, "ZAI") || !strings.Contains(text, "API key") {
		t.Fatalf("text = %q", text)
	}
}

func TestFormatLoginProviderCompletionDescription(t *testing.T) {
	sameName := LoginProviderCompletionOption{ID: "zai", Name: "zai", AuthTypes: []string{"api_key"}}
	if got := FormatLoginProviderCompletionDescription(sameName); got != "API key" {
		t.Fatalf("same = %q", got)
	}

	differentName := LoginProviderCompletionOption{ID: "anthropic", Name: "Anthropic", AuthTypes: []string{"oauth", "api_key"}}
	if got := FormatLoginProviderCompletionDescription(differentName); got != "Anthropic · OAuth/API key" {
		t.Fatalf("different = %q", got)
	}
}

func TestAuthGuidance(t *testing.T) {
	if got := GetProviderLoginHelp(); !containsAll(got, "Use /login", "providers.md", "models.md") {
		t.Fatalf("help = %q", got)
	}
	if got := FormatNoModelsAvailableMessage(); !strings.Contains(got, "No models available") {
		t.Fatalf("no models = %q", got)
	}
	if got := FormatNoModelSelectedMessage(); !strings.Contains(got, "No model selected") || !strings.Contains(got, "/model to select") {
		t.Fatalf("no selection = %q", got)
	}
	if got := FormatNoApiKeyFoundMessage("anthropic"); !strings.Contains(got, "No API key found for anthropic") {
		t.Fatalf("key = %q", got)
	}
	if got := FormatNoApiKeyFoundMessage("unknown"); !strings.Contains(got, "the selected model") {
		t.Fatalf("unknown provider = %q", got)
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}

func TestBuiltinSlashCommands(t *testing.T) {
	if len(BuiltinSlashCommands) != 24 {
		t.Fatalf("count = %d", len(BuiltinSlashCommands))
	}
	byName := map[string]BuiltinSlashCommand{}
	for _, command := range BuiltinSlashCommands {
		byName[command.Name] = command
	}
	if byName["settings"].Description != "Open settings menu" {
		t.Fatal("settings missing")
	}
	if byName["model"].ArgumentHint != "<provider/model>" {
		t.Fatal("model hint missing")
	}
	if !strings.Contains(byName["quit"].Description, AppName()) {
		t.Fatalf("quit = %q", byName["quit"].Description)
	}
}
