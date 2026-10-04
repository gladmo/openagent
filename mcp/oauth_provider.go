package mcp

// Port of packages/mcp/src/oauth/provider.ts: the default stateful
// OAuthClientProvider for one exact MCP server URL. Applications inject
// durable storage if needed.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/gladmo/openagent/jsonx"
)

// McpOAuthState is the persisted OAuth state for one server URL.
type McpOAuthState struct {
	ServerURL         string
	ClientInformation *OAuthClientInformation
	Tokens            *OAuthTokens
	// TokensExpireAt is when the access token expires, in milliseconds
	// since the epoch, from expires_in at the time it was saved; nil when
	// unknown.
	TokensExpireAt *float64
	CodeVerifier   string
	OAuthState     string
	Discovery      *OAuthDiscoveryState
}

// ToObj renders the state as a JSON object.
func (s *McpOAuthState) ToObj() *jsonx.Obj {
	obj := jsonx.ObjFrom("serverUrl", s.ServerURL)
	if s.ClientInformation != nil {
		obj.Set("clientInformation", clientInformationToObj(s.ClientInformation))
	}
	if s.Tokens != nil {
		obj.Set("tokens", tokensToObj(s.Tokens))
	}
	if s.TokensExpireAt != nil {
		obj.Set("tokensExpireAt", *s.TokensExpireAt)
	}
	if s.CodeVerifier != "" {
		obj.Set("codeVerifier", s.CodeVerifier)
	}
	if s.OAuthState != "" {
		obj.Set("oauthState", s.OAuthState)
	}
	if s.Discovery != nil {
		obj.Set("discovery", discoveryStateToObj(s.Discovery))
	}
	return obj
}

// McpOAuthStateFromObj parses a persisted state.
func McpOAuthStateFromObj(value any) (*McpOAuthState, error) {
	obj, isObj := isObject(value)
	if !isObj {
		return nil, errors.New("Invalid MCP OAuth state")
	}
	state := &McpOAuthState{ServerURL: optionalString(obj, "serverUrl")}
	if raw, has := obj.Get("clientInformation"); has && raw != nil {
		information, err := ParseClientInformation(raw)
		if err != nil {
			return nil, err
		}
		state.ClientInformation = information
	}
	if raw, has := obj.Get("tokens"); has && raw != nil {
		tokens, err := ParseOAuthTokens(raw)
		if err != nil {
			return nil, err
		}
		state.Tokens = tokens
	}
	if raw, has := obj.Get("tokensExpireAt"); has {
		if number, isNumber := raw.(float64); isNumber {
			state.TokensExpireAt = &number
		}
	}
	state.CodeVerifier = optionalString(obj, "codeVerifier")
	state.OAuthState = optionalString(obj, "oauthState")
	if raw, has := obj.Get("discovery"); has && raw != nil {
		if discoveryObj, ok := raw.(*jsonx.Obj); ok {
			state.Discovery = discoveryStateFromObj(discoveryObj)
		}
	}
	return state, nil
}

func clientInformationToObj(information *OAuthClientInformation) *jsonx.Obj {
	if information.Raw != nil {
		return jsonx.Clone(information.Raw).(*jsonx.Obj)
	}
	obj := jsonx.ObjFrom("client_id", information.ClientID)
	if information.ClientSecret != "" {
		obj.Set("client_secret", information.ClientSecret)
	}
	return obj
}

func tokensToObj(tokens *OAuthTokens) *jsonx.Obj {
	obj := jsonx.ObjFrom("access_token", tokens.AccessToken, "token_type", tokens.TokenType)
	if tokens.HasExpiresIn {
		obj.Set("expires_in", tokens.ExpiresIn)
	}
	if tokens.Scope != "" {
		obj.Set("scope", tokens.Scope)
	}
	if tokens.RefreshToken != "" {
		obj.Set("refresh_token", tokens.RefreshToken)
	}
	if tokens.IDToken != "" {
		obj.Set("id_token", tokens.IDToken)
	}
	return obj
}

func discoveryStateToObj(state *OAuthDiscoveryState) *jsonx.Obj {
	obj := jsonx.ObjFrom("authorizationServerUrl", state.AuthorizationServerURL)
	if state.AuthorizationServerMetadata != nil {
		obj.Set("authorizationServerMetadata", jsonx.Clone(state.AuthorizationServerMetadata.Raw))
	}
	if state.ResourceMetadata != nil {
		obj.Set("resourceMetadata", jsonx.Clone(state.ResourceMetadata.Raw))
	}
	if state.ResourceMetadataURL != "" {
		obj.Set("resourceMetadataUrl", state.ResourceMetadataURL)
	}
	return obj
}

func discoveryStateFromObj(obj *jsonx.Obj) *OAuthDiscoveryState {
	state := &OAuthDiscoveryState{AuthorizationServerURL: optionalString(obj, "authorizationServerUrl")}
	if raw, has := obj.Get("authorizationServerMetadata"); has && raw != nil {
		if metadata, err := ParseAuthorizationServerMetadata(raw); err == nil {
			state.AuthorizationServerMetadata = metadata
		}
	}
	if raw, has := obj.Get("resourceMetadata"); has && raw != nil {
		if metadata, err := ParseProtectedResourceMetadata(raw); err == nil {
			state.ResourceMetadata = metadata
		}
	}
	state.ResourceMetadataURL = optionalString(obj, "resourceMetadataUrl")
	return state
}

// McpOAuthStateStore loads and saves the state durably.
type McpOAuthStateStore interface {
	Load() (*McpOAuthState, error)
	Save(state *McpOAuthState) error
}

// MemoryOAuthStateStore keeps the state in memory.
type MemoryOAuthStateStore struct {
	mu    sync.Mutex
	value *McpOAuthState
}

// Load implements McpOAuthStateStore.
func (s *MemoryOAuthStateStore) Load() (*McpOAuthState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value == nil {
		return nil, nil
	}
	return mcpOAuthStateClone(s.value), nil
}

// Save implements McpOAuthStateStore.
func (s *MemoryOAuthStateStore) Save(state *McpOAuthState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = mcpOAuthStateClone(state)
	return nil
}

func mcpOAuthStateClone(state *McpOAuthState) *McpOAuthState {
	if state == nil {
		return nil
	}
	cloned := *state
	return &cloned
}

// McpOAuthProviderOptions configure the provider.
type McpOAuthProviderOptions struct {
	ServerURL      string
	RedirectURL    string
	ClientMetadata *OAuthClientMetadata
	// ClientMetadataDocument identifies the client by a Client ID
	// Metadata Document instead of registering dynamically; see
	// OAuthClientProvider.ClientMetadataDocument.
	ClientMetadataDocument func(metadata *AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error)
	ClientID               string
	ClientSecret           string
	Store                  McpOAuthStateStore
	OnRedirect             func(url string) error
}

// McpOAuthProvider is the default stateful OAuthClientProvider for one
// exact MCP server URL.
type McpOAuthProvider struct {
	serverURL              string
	redirectURL            string
	clientMetadata         *OAuthClientMetadata
	clientMetadataDocument func(metadata *AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error)
	configuredClient       *OAuthClientInformation
	store                  McpOAuthStateStore
	onRedirect             func(url string) error

	mu sync.Mutex
}

// NewMcpOAuthProvider builds the provider; defaults fill the metadata.
func NewMcpOAuthProvider(options McpOAuthProviderOptions) *McpOAuthProvider {
	serverURL := normalizedURLString(options.ServerURL)
	metadata := &OAuthClientMetadata{}
	if options.ClientMetadata != nil {
		*metadata = *options.ClientMetadata
	}
	if metadata.RedirectURIs == nil {
		metadata.RedirectURIs = []string{options.RedirectURL}
	}
	if metadata.GrantTypes == nil {
		metadata.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	if metadata.ResponseTypes == nil {
		metadata.ResponseTypes = []string{"code"}
	}
	if metadata.TokenEndpointAuthMethod == "" {
		if options.ClientSecret != "" {
			metadata.TokenEndpointAuthMethod = "client_secret_post"
		} else {
			metadata.TokenEndpointAuthMethod = "none"
		}
	}
	var configuredClient *OAuthClientInformation
	if options.ClientID != "" {
		configuredClient = &OAuthClientInformation{ClientID: options.ClientID, ClientSecret: options.ClientSecret}
	}
	store := options.Store
	if store == nil {
		store = &MemoryOAuthStateStore{}
	}
	return &McpOAuthProvider{
		serverURL:              serverURL,
		redirectURL:            options.RedirectURL,
		clientMetadata:         metadata,
		clientMetadataDocument: options.ClientMetadataDocument,
		configuredClient:       configuredClient,
		store:                  store,
		onRedirect:             options.OnRedirect,
	}
}

// normalizedURLString renders the URL the way String(new URL(value)) does.
func normalizedURLString(value string) string {
	parsed, err := parseLooseURL(value)
	if err != nil {
		return value
	}
	return parsed.String()
}

// AsProvider exposes the provider as the flow's OAuthClientProvider.
func (p *McpOAuthProvider) AsProvider() *OAuthClientProvider {
	return &OAuthClientProvider{
		RedirectURL:             p.redirectURL,
		ClientMetadata:          p.clientMetadata,
		ClientMetadataDocument:  p.clientMetadataDocument,
		State:                   p.State,
		ClientInformation:       p.ClientInformation,
		SaveClientInformation:   p.SaveClientInformation,
		Tokens:                  p.Tokens,
		SaveTokens:              p.SaveTokens,
		RedirectToAuthorization: p.RedirectToAuthorization,
		SaveCodeVerifier:        p.SaveCodeVerifier,
		CodeVerifier:            p.CodeVerifier,
		InvalidateCredentials:   p.InvalidateCredentials,
		SaveDiscoveryState:      p.SaveDiscoveryState,
		DiscoveryState:          p.DiscoveryState,
	}
}

// State returns the stored state parameter, generating one when absent.
func (p *McpOAuthProvider) State() (string, error) {
	state, err := p.load()
	if err != nil {
		return "", err
	}
	if state.OAuthState != "" {
		return state.OAuthState, nil
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	generated := hex.EncodeToString(bytes)
	if err := p.update(func(state *McpOAuthState) { state.OAuthState = generated }); err != nil {
		return "", err
	}
	return generated, nil
}

// ClientInformation returns the configured or stored registration.
func (p *McpOAuthProvider) ClientInformation() (*OAuthClientInformation, error) {
	if p.configuredClient != nil {
		return p.configuredClient, nil
	}
	state, err := p.load()
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, nil
	}
	return state.ClientInformation, nil
}

// SaveClientInformation stores a registration unless one is configured.
func (p *McpOAuthProvider) SaveClientInformation(information *OAuthClientInformation) error {
	if p.configuredClient != nil {
		return nil
	}
	return p.update(func(state *McpOAuthState) { state.ClientInformation = information })
}

// Tokens returns the stored tokens.
func (p *McpOAuthProvider) Tokens() (*OAuthTokens, error) {
	state, err := p.load()
	if err != nil || state == nil {
		return nil, err
	}
	return state.Tokens, nil
}

// SaveTokens stores tokens and their expiry from expires_in.
func (p *McpOAuthProvider) SaveTokens(tokens *OAuthTokens) error {
	return p.update(func(state *McpOAuthState) {
		state.Tokens = tokens
		if tokens.HasExpiresIn {
			expiresAt := nowMs() + tokens.ExpiresIn*1000
			state.TokensExpireAt = &expiresAt
		} else {
			state.TokensExpireAt = nil
		}
	})
}

// RedirectToAuthorization hands the URL to the application.
func (p *McpOAuthProvider) RedirectToAuthorization(url string) error {
	return p.onRedirect(url)
}

// SaveCodeVerifier stores the PKCE verifier.
func (p *McpOAuthProvider) SaveCodeVerifier(verifier string) error {
	return p.update(func(state *McpOAuthState) { state.CodeVerifier = verifier })
}

// CodeVerifier returns the stored PKCE verifier.
func (p *McpOAuthProvider) CodeVerifier() (string, error) {
	state, err := p.load()
	if err != nil {
		return "", err
	}
	if state.CodeVerifier == "" {
		return "", errors.New("No OAuth PKCE code verifier is stored")
	}
	return state.CodeVerifier, nil
}

// InvalidateCredentials drops the selected stored credentials.
func (p *McpOAuthProvider) InvalidateCredentials(kind CredentialKind) error {
	return p.update(func(state *McpOAuthState) {
		if kind == InvalidateAll || kind == InvalidateClient {
			state.ClientInformation = nil
		}
		if kind == InvalidateAll || kind == InvalidateTokens {
			state.Tokens = nil
			state.TokensExpireAt = nil
		}
		if kind == InvalidateAll || kind == InvalidateVerifier {
			state.CodeVerifier = ""
		}
		if kind == InvalidateAll || kind == InvalidateDiscovery {
			state.Discovery = nil
		}
		if kind == InvalidateAll {
			state.OAuthState = ""
		}
	})
}

// SaveDiscoveryState caches discovery results.
func (p *McpOAuthProvider) SaveDiscoveryState(discovery *OAuthDiscoveryState) error {
	return p.update(func(state *McpOAuthState) { state.Discovery = discovery })
}

// DiscoveryState returns the cached discovery results.
func (p *McpOAuthProvider) DiscoveryState() (*OAuthDiscoveryState, error) {
	state, err := p.load()
	if err != nil || state == nil {
		return nil, err
	}
	return state.Discovery, nil
}

func (p *McpOAuthProvider) load() (*McpOAuthState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	loaded, err := p.store.Load()
	if err != nil {
		return nil, err
	}
	return p.own(loaded), nil
}

func (p *McpOAuthProvider) update(update func(state *McpOAuthState)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	loaded, err := p.store.Load()
	if err != nil {
		return err
	}
	state := p.own(loaded)
	update(state)
	return p.store.Save(state)
}

// own drops stored state for another server URL, so credentials never
// leak across servers.
func (p *McpOAuthProvider) own(state *McpOAuthState) *McpOAuthState {
	if state != nil && state.ServerURL == p.serverURL {
		return state
	}
	return &McpOAuthState{ServerURL: p.serverURL}
}

func nowMs() float64 {
	return float64(time.Now().UnixMilli())
}
