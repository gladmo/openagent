package ai

// auth_provider_test.go + env_api_keys tests: ports of env-api-keys.test.ts
// and error-body.test.ts essentials.

import (
	"strings"
	"testing"
)

type fakeAuthContext struct {
	env map[string]string
}

func (c fakeAuthContext) Env(name string) string      { return c.env[name] }
func (c fakeAuthContext) FileExists(path string) bool { return false }

func TestGetEnvAPIKeyAnthropicSkipsAuthToken(t *testing.T) {
	env := ProviderEnv{
		EnvAnthropicAuthToken: "bearer-token",
		EnvAnthropicAPIKey:    "sk-test",
	}
	if got := GetEnvAPIKey("anthropic", env); got != "sk-test" {
		t.Fatalf("expected sk-test, got %q", got)
	}

	envOnlyToken := ProviderEnv{EnvAnthropicAuthToken: "bearer-token"}
	if got := GetEnvAPIKey("anthropic", envOnlyToken); got != "" {
		t.Fatalf("expected empty (AUTH_TOKEN must not surface as key), got %q", got)
	}
}

func TestGetEnvAPIKeySimpleProviders(t *testing.T) {
	cases := map[string]string{
		"openai":        "OPENAI_API_KEY",
		"deepseek":      "DEEPSEEK_API_KEY",
		"openrouter":    "OPENROUTER_API_KEY",
		"zai":           "ZAI_API_KEY",
		"zai-coding-cn": "ZAI_CODING_CN_API_KEY",
	}
	for provider, envVar := range cases {
		env := ProviderEnv{envVar: "key-" + provider}
		if got := GetEnvAPIKey(provider, env); got != "key-"+provider {
			t.Errorf("%s: expected key-%s, got %q", provider, provider, got)
		}
		env = ProviderEnv{"SOME_OTHER_VAR": "value"}
		if got := GetEnvAPIKey(provider, env); got != "" {
			t.Errorf("%s: expected empty, got %q", provider, got)
		}
	}
}

func TestEnvAPIKeyAuthResolutionOrder(t *testing.T) {
	auth := EnvAPIKeyAuth("DeepSeek API key", "DEEPSEEK_API_KEY")
	ctx := fakeAuthContext{env: map[string]string{"DEEPSEEK_API_KEY": "env-key"}}

	// Ambient env resolution.
	result, err := auth.Resolve(AuthResolveInput{Ctx: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Auth.APIKey == nil || *result.Auth.APIKey != "env-key" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Source != "DEEPSEEK_API_KEY" {
		t.Fatalf("expected source DEEPSEEK_API_KEY, got %q", result.Source)
	}

	// Stored credential wins over env.
	key := "stored-key"
	result, err = auth.Resolve(AuthResolveInput{
		Ctx:        ctx,
		Credential: &Credential{Type: CredentialTypeAPIKey, APIKey: &key},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Auth.APIKey == nil || *result.Auth.APIKey != "stored-key" {
		t.Fatalf("expected stored-key, got %+v", result)
	}
	if result.Source != "stored credential" {
		t.Fatalf("expected stored credential source, got %q", result.Source)
	}

	// Unconfigured.
	result, err = auth.Resolve(AuthResolveInput{Ctx: fakeAuthContext{}})
	if err != nil || result != nil {
		t.Fatalf("expected nil result, got %+v err %v", result, err)
	}
}

func TestAnthropicAPIKeyAuthBearerToken(t *testing.T) {
	auth := AnthropicAPIKeyAuth()
	ctx := fakeAuthContext{env: map[string]string{EnvAnthropicAuthToken: "tok"}}

	result, err := auth.Resolve(AuthResolveInput{Ctx: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	if result.Auth.APIKey != nil {
		t.Fatalf("expected no apiKey, got %v", *result.Auth.APIKey)
	}
	if bearer := result.Auth.Headers["Authorization"]; bearer == nil || *bearer != "Bearer tok" {
		t.Fatalf("expected Bearer header, got %v", result.Auth.Headers)
	}
	if result.Source != EnvAnthropicAuthToken {
		t.Fatalf("expected source %s, got %q", EnvAnthropicAuthToken, result.Source)
	}
}

func TestResolveProviderAuthStoredCredentialWins(t *testing.T) {
	provider := NewProvider(CreateProviderOptions{
		ID:   "deepseek",
		Name: "DeepSeek",
		Auth: ProviderAuth{APIKey: EnvAPIKeyAuth("DeepSeek API key", "DEEPSEEK_API_KEY")},
		API:  &stubStreams{},
	})
	store := NewInMemoryCredentialStore()
	key := "stored"
	if _, err := store.Modify("deepseek", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialTypeAPIKey, APIKey: &key}, nil
	}); err != nil {
		t.Fatal(err)
	}

	result, err := ResolveProviderAuth(provider, store, fakeAuthContext{env: map[string]string{"DEEPSEEK_API_KEY": "env"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Auth.APIKey == nil || *result.Auth.APIKey != "stored" {
		t.Fatalf("expected stored credential to win, got %+v", result)
	}
}

type stubProviderView struct {
	id   string
	auth ProviderAuth
}

func (v stubProviderView) ID() string         { return v.id }
func (v stubProviderView) Auth() ProviderAuth { return v.auth }

type stubStreams struct{}

func (s *stubStreams) Stream(*Model, *TranscriptContext, *StreamOptions) *AssistantMessageEventStream {
	return NewAssistantMessageEventStream()
}

func (s *stubStreams) StreamSimple(*Model, *TranscriptContext, *SimpleStreamOptions) *AssistantMessageEventStream {
	return NewAssistantMessageEventStream()
}

func TestResolveProviderAuthUnconfigured(t *testing.T) {
	view := stubProviderView{id: "deepseek", auth: ProviderAuth{APIKey: EnvAPIKeyAuth("DeepSeek API key", "DEEPSEEK_API_KEY")}}
	result, err := ResolveProviderAuth(view, NewInMemoryCredentialStore(), fakeAuthContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("expected nil (unconfigured), got %+v", result)
	}
}

func TestResolveProviderAuthOverrideKey(t *testing.T) {
	view := stubProviderView{id: "deepseek", auth: ProviderAuth{APIKey: EnvAPIKeyAuth("DeepSeek API key", "DEEPSEEK_API_KEY")}}
	override := "explicit"
	result, err := ResolveProviderAuth(view, NewInMemoryCredentialStore(), fakeAuthContext{}, &AuthResolutionOverrides{APIKey: &override})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Auth.APIKey == nil || *result.Auth.APIKey != "explicit" {
		t.Fatalf("expected override key, got %+v", result)
	}
}

// ---------------------------------------------------------------------------
// error-body
// ---------------------------------------------------------------------------

func TestTruncateErrorText(t *testing.T) {
	if got := TruncateErrorText("short", 10); got != "short" {
		t.Fatalf("unexpected %q", got)
	}
	long := strings.Repeat("a", 5000)
	got := TruncateErrorText(long, 4000)
	if !strings.HasSuffix(got, "... [truncated 1000 chars]") {
		t.Fatalf("unexpected suffix: %q", got[len(got)-40:])
	}
}

func TestFormatProviderError(t *testing.T) {
	// Message carries the body (no status/body duplication).
	norm := NormalizedProviderError{Status: 403, Message: "403 Forbidden: {\"error\":\"x\"}", Body: `{"error":"x"}`, MessageCarriesBody: true}
	if got := FormatProviderError(norm, ""); got != "403 Forbidden: {\"error\":\"x\"}" {
		t.Fatalf("unexpected %q", got)
	}
	// Status + body surfaced.
	norm = NormalizeProviderError(&ProviderHTTPError{Status: 429, Body: "rate limited"})
	if got := FormatProviderError(norm, ""); got != "429: rate limited" {
		t.Fatalf("unexpected %q", got)
	}
	if got := FormatProviderError(norm, "OpenAI"); got != "OpenAI (429): rate limited" {
		t.Fatalf("unexpected %q", got)
	}
	// Message-carrying path with prefix.
	norm = NormalizeProviderError(&ProviderHTTPError{Status: 500, Message: "internal", Body: ""})
	if got := FormatProviderError(norm, "Anthropic"); got != "Anthropic (500): internal" {
		t.Fatalf("unexpected %q", got)
	}
}
