package ai

// auth_provider.go ports the env-key subset of pi/packages/ai/src/auth:
// types.ts (ModelAuth/Credential/AuthResult/CredentialStore),
// context.ts (default env context), credential-store.ts (in-memory store),
// helpers.ts (envApiKeyAuth), and resolve.ts (resolveProviderAuth).
//
// Scope deviation (PORTING.md): interactive OAuth login/refresh flows are not
// ported. Provider definitions may still advertise an OAuth slot; resolving a
// stored OAuth credential returns an explicit error instead of refreshing.

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/gladmo/openagent/abort"
)

// ---------------------------------------------------------------------------
// Request auth shapes (auth/types.ts)
// ---------------------------------------------------------------------------

// ModelAuth is request auth for a single model request: apiKey, extra
// headers, or a baseUrl override.
type ModelAuth struct {
	APIKey  *string
	Headers ProviderHeaders
	BaseURL *string
}

// Credential type tags.
const (
	CredentialTypeAPIKey = "api_key"
	CredentialTypeOAuth  = "oauth"
)

// CredentialInfo is non-secret credential metadata.
type CredentialInfo struct {
	ProviderID string
	Type       string
}

// AuthCheck reports that a provider has complete auth configuration.
type AuthCheck struct {
	Source string
	Type   string // "api_key" | "oauth"
}

// AuthResult is the outcome of resolving provider auth.
type AuthResult struct {
	Auth   ModelAuth
	Env    ProviderEnv
	Source string
}

// AuthContext is environment access for auth resolution. Injectable for
// tests. Env returns "" when unset (TS undefined).
type AuthContext interface {
	Env(name string) string
	// FileExists checks whether a file exists. Supports a leading '~'.
	FileExists(path string) bool
}

type defaultAuthContext struct{}

// DefaultAuthContext reads env vars from the process environment (blank and
// whitespace-only values count as unset) and checks files on disk.
func DefaultAuthContext() AuthContext { return defaultAuthContext{} }

func (defaultAuthContext) Env(name string) string {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return ""
	}
	return value
}

func (defaultAuthContext) FileExists(path string) bool {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		path = home + path[1:]
	}
	_, err := os.Stat(path)
	return err == nil
}

// EnvOverlayAuthContext overlays scoped env values on a base context
// (resolve.ts overlayEnvAuthContext).
type EnvOverlayAuthContext struct {
	Base    AuthContext
	Overlay ProviderEnv
}

// Env implements AuthContext.
func (c EnvOverlayAuthContext) Env(name string) string {
	if value, ok := c.Overlay[name]; ok && value != "" {
		return value
	}
	return c.Base.Env(name)
}

// FileExists implements AuthContext.
func (c EnvOverlayAuthContext) FileExists(path string) bool { return c.Base.FileExists(path) }

// ---------------------------------------------------------------------------
// Credential store (auth/credential-store.ts)
// ---------------------------------------------------------------------------

// CredentialStore is app-owned credential storage keyed by Provider.ID, one
// credential per provider. All methods are synchronous in the Go port; the
// in-memory implementation serializes writes per provider.
type CredentialStore interface {
	Read(providerID string) (*Credential, error)
	List() ([]CredentialInfo, error)
	// Modify is the only write path: fn sees the current credential and
	// returns the new one (nil leaves the entry unchanged). Returns the
	// post-write credential.
	Modify(providerID string, fn func(current *Credential) (*Credential, error)) (*Credential, error)
	Delete(providerID string) error
}

// InMemoryCredentialStore is the default in-memory store.
type InMemoryCredentialStore struct {
	mu          sync.Mutex
	credentials map[string]*Credential
}

// NewInMemoryCredentialStore builds an empty store.
func NewInMemoryCredentialStore() *InMemoryCredentialStore {
	return &InMemoryCredentialStore{credentials: map[string]*Credential{}}
}

// Read implements CredentialStore.
func (s *InMemoryCredentialStore) Read(providerID string) (*Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if credential, ok := s.credentials[providerID]; ok {
		return cloneCredential(credential), nil
	}
	return nil, nil
}

// List implements CredentialStore.
func (s *InMemoryCredentialStore) List() ([]CredentialInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CredentialInfo, 0, len(s.credentials))
	for providerID, credential := range s.credentials {
		out = append(out, CredentialInfo{ProviderID: providerID, Type: credential.Type})
	}
	return out, nil
}

// Modify implements CredentialStore.
func (s *InMemoryCredentialStore) Modify(providerID string, fn func(*Credential) (*Credential, error)) (*Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var current *Credential
	if credential, ok := s.credentials[providerID]; ok {
		current = cloneCredential(credential)
	}
	next, err := fn(current)
	if err != nil {
		return current, err
	}
	if next != nil {
		stored := cloneCredential(next)
		s.credentials[providerID] = stored
		return cloneCredential(stored), nil
	}
	return current, nil
}

// Delete implements CredentialStore.
func (s *InMemoryCredentialStore) Delete(providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.credentials, providerID)
	return nil
}

func cloneCredential(credential *Credential) *Credential {
	if credential == nil {
		return nil
	}
	cloned := *credential
	if credential.Env != nil {
		cloned.Env = make(ProviderEnv, len(credential.Env))
		for k, v := range credential.Env {
			cloned.Env[k] = v
		}
	}
	return &cloned
}

// ---------------------------------------------------------------------------
// Provider auth methods (auth/types.ts ApiKeyAuth/OAuthAuth)
// ---------------------------------------------------------------------------

// AuthResolveInput is the input to api-key resolve/check.
type AuthResolveInput struct {
	Ctx        AuthContext
	Credential *Credential // nil = ambient resolution
	Signal     *abort.Signal
}

// ProviderApiKeyAuth is api-key auth: a stored key plus ambient env sources.
type ProviderApiKeyAuth struct {
	// Name is the display name, e.g. "Anthropic API key".
	Name string
	// Resolve resolves auth from the stored credential and/or ambient
	// sources. Nil result = not configured.
	Resolve func(input AuthResolveInput) (*AuthResult, error)
	// Check is an optional side-effect-free availability check.
	Check func(input AuthResolveInput) (*AuthCheck, error)
}

// ProviderOAuthAuth advertises an OAuth method. Login/refresh are not
// implemented in this port (see file comment); the slot exists so provider
// definitions mirror pi and status UIs can show the option.
type ProviderOAuthAuth struct {
	Name           string
	IsSubscription bool
	LoginLabel     string
}

// ProviderAuth is provider auth: at least one of APIKey/OAuth.
type ProviderAuth struct {
	APIKey *ProviderApiKeyAuth
	OAuth  *ProviderOAuthAuth
}

// NewAPIKeyAuth builds static API-key auth (always configured).
func NewAPIKeyAuth(label string, key string) ProviderAuth {
	return ProviderAuth{APIKey: &ProviderApiKeyAuth{
		Name: label,
		Resolve: func(AuthResolveInput) (*AuthResult, error) {
			apiKey := key
			return &AuthResult{Auth: ModelAuth{APIKey: &apiKey}, Source: "static"}, nil
		},
	}}
}

// AlwaysConfiguredAuth is auth whose resolve reports an env-less ambient key
// (used by faux and local providers).
func AlwaysConfiguredAuth(label string) ProviderAuth {
	return NewAPIKeyAuth(label, "configured")
}

// EnvAPIKeyAuth ports auth/helpers.ts envApiKeyAuth: a stored credential key
// wins, otherwise the first set env var resolves. Login (interactive prompt)
// is out of scope.
func EnvAPIKeyAuth(name string, envVars ...string) *ProviderApiKeyAuth {
	return &ProviderApiKeyAuth{
		Name: name,
		Resolve: func(input AuthResolveInput) (*AuthResult, error) {
			if input.Signal != nil {
				if err := input.Signal.ThrowIfAborted(); err != nil {
					return nil, err
				}
			}
			if input.Credential != nil && input.Credential.APIKey != nil && *input.Credential.APIKey != "" {
				return &AuthResult{
					Auth:   ModelAuth{APIKey: input.Credential.APIKey},
					Env:    input.Credential.Env,
					Source: "stored credential",
				}, nil
			}
			for _, envVar := range envVars {
				value := input.Ctx.Env(envVar)
				if input.Signal != nil {
					if err := input.Signal.ThrowIfAborted(); err != nil {
						return nil, err
					}
				}
				if value != "" {
					apiKey := value
					return &AuthResult{Auth: ModelAuth{APIKey: &apiKey}, Source: envVar}, nil
				}
			}
			return nil, nil
		},
	}
}

// AnthropicAPIKeyAuth ports providers/anthropic.ts anthropicApiKeyAuth:
// stored key, or ANTHROPIC_AUTH_TOKEN as a Bearer header, then OAuth-token /
// API-key env vars.
func AnthropicAPIKeyAuth() *ProviderApiKeyAuth {
	return &ProviderApiKeyAuth{
		Name: "Anthropic API key",
		Resolve: func(input AuthResolveInput) (*AuthResult, error) {
			if input.Signal != nil {
				if err := input.Signal.ThrowIfAborted(); err != nil {
					return nil, err
				}
			}
			if input.Credential != nil && input.Credential.APIKey != nil && *input.Credential.APIKey != "" {
				return &AuthResult{
					Auth:   ModelAuth{APIKey: input.Credential.APIKey},
					Env:    input.Credential.Env,
					Source: "stored credential",
				}, nil
			}

			authToken := input.Ctx.Env(EnvAnthropicAuthToken)
			if input.Signal != nil {
				if err := input.Signal.ThrowIfAborted(); err != nil {
					return nil, err
				}
			}
			if authToken != "" {
				return &AuthResult{
					Auth: ModelAuth{Headers: ProviderHeaders{
						"Authorization": strPtr("Bearer " + authToken),
					}},
					Source: EnvAnthropicAuthToken,
				}, nil
			}

			for _, envVar := range []string{EnvAnthropicOAuthToken, EnvAnthropicAPIKey} {
				apiKey := input.Ctx.Env(envVar)
				if input.Signal != nil {
					if err := input.Signal.ThrowIfAborted(); err != nil {
						return nil, err
					}
				}
				if apiKey != "" {
					value := apiKey
					return &AuthResult{Auth: ModelAuth{APIKey: &value}, Source: envVar}, nil
				}
			}
			return nil, nil
		},
	}
}

// ---------------------------------------------------------------------------
// Auth resolution (auth/resolve.ts)
// ---------------------------------------------------------------------------

// AuthResolutionOverrides mirrors the TS interface. MinOAuthValidityMs is
// accepted but unused while OAuth refresh is out of scope.
type AuthResolutionOverrides struct {
	APIKey             *string
	Env                ProviderEnv
	MinOAuthValidityMs *float64
	Signal             *abort.Signal
}

type providerAuthView interface {
	ID() string
	Auth() ProviderAuth
}

// ResolveProviderAuth resolves auth shared by all operations in a Models
// collection: a stored credential owns the provider, ambient/env is consulted
// only when nothing is stored.
func ResolveProviderAuth(provider providerAuthView, credentials CredentialStore, authContext AuthContext, overrides *AuthResolutionOverrides) (*AuthResult, error) {
	if overrides != nil && overrides.Signal != nil {
		if err := overrides.Signal.ThrowIfAborted(); err != nil {
			return nil, err
		}
	}
	requestAuthContext := authContext
	if overrides != nil && overrides.Env != nil {
		requestAuthContext = EnvOverlayAuthContext{Base: authContext, Overlay: overrides.Env}
	}
	auth := provider.Auth()

	if overrides != nil && overrides.APIKey != nil && auth.APIKey != nil {
		return resolveApiKey(requestAuthContext, auth.APIKey, provider.ID(), &Credential{
			Type:   CredentialTypeAPIKey,
			APIKey: overrides.APIKey,
			Env:    overrides.Env,
		}, signalOf(overrides))
	}

	stored, err := readCredential(credentials, provider.ID())
	if err != nil {
		return nil, err
	}
	if stored != nil {
		switch {
		case stored.Type == CredentialTypeOAuth && auth.OAuth != nil:
			return nil, NewModelsError(ModelsErrorCodeOauth,
				fmt.Sprintf("OAuth credential resolution is not supported in this port (provider %s); re-login with an API key", provider.ID()), nil)
		case stored.Type == CredentialTypeAPIKey && auth.APIKey != nil:
			credential := stored
			if overrides != nil && overrides.Env != nil {
				merged := make(ProviderEnv, len(credential.Env)+len(overrides.Env))
				for k, v := range credential.Env {
					merged[k] = v
				}
				for k, v := range overrides.Env {
					merged[k] = v
				}
				credential = cloneCredential(credential)
				credential.Env = merged
			}
			return resolveApiKey(requestAuthContext, auth.APIKey, provider.ID(), credential, signalOf(overrides))
		}
		return nil, nil
	}

	// Ambient (env vars).
	if auth.APIKey != nil {
		return resolveApiKey(requestAuthContext, auth.APIKey, provider.ID(), nil, signalOf(overrides))
	}
	return nil, nil
}

func signalOf(overrides *AuthResolutionOverrides) *abort.Signal {
	if overrides == nil {
		return nil
	}
	return overrides.Signal
}

func resolveApiKey(authContext AuthContext, apiKey *ProviderApiKeyAuth, providerID string, credential *Credential, signal *abort.Signal) (*AuthResult, error) {
	result, err := apiKey.Resolve(AuthResolveInput{Ctx: authContext, Credential: credential, Signal: signal})
	if err != nil {
		return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("API key auth failed for provider %s", providerID), err)
	}
	return result, nil
}

func readCredential(credentials CredentialStore, providerID string) (*Credential, error) {
	if credentials == nil {
		return nil, nil
	}
	stored, err := credentials.Read(providerID)
	if err != nil {
		return nil, NewModelsError(ModelsErrorCodeAuth, fmt.Sprintf("Credential store read failed for %s", providerID), err)
	}
	return stored, nil
}
