package codingagent

// Port of extensions/mcp/oauth.ts at reference commit a276dabe: OAuth
// sign-in for remote MCP servers.
//
// Connections never start a browser flow on their own. They send the
// stored access token and, after a 401, try the stored refresh token.
// When that is not possible they fail with
// McpOAuthAuthorizationRequiredError, and the user signs in through /mcp
// or `openagent mcp login`, which runs the authorization code flow (PKCE,
// a Client ID Metadata Document or dynamic client registration) against
// a loopback callback.
//
// Credentials live in <agent-dir>/mcp-auth.json, keyed by server name and
// URL.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/mcp"
)

const (
	mcpCallbackHost = "127.0.0.1"
	mcpCallbackPath = "/callback"
	// mcpClientMetadataBaseURL is where pi.dev serves pi's Client ID
	// Metadata Documents: client.json and <callback ID>/client.json.
	mcpClientMetadataBaseURL = "https://pi.dev/oauth"
	// mcpFallbackRedirectURL is the redirect URI for refreshes when none
	// is stored. Refreshing never redirects the user.
	mcpFallbackRedirectURL = "http://" + mcpCallbackHost + mcpCallbackPath
	// refreshSkew: access tokens this close to expiry are refreshed
	// before they are sent.
	mcpRefreshSkew = 30 * time.Second
	// mcpRefreshRequestTimeout bounds each request of a refresh, so it
	// cannot hold the refresh lock or delay shutdown for long.
	mcpOAuthRequestTimeout = 15 * time.Second
	// mcpRefreshLockStale: a refresh lock its holder stopped renewing is
	// taken over after this.
	mcpRefreshLockStale = 20 * time.Second
	// mcpRefreshLockWait: how long to wait for another process's refresh;
	// longer than a stale lock lives.
	mcpRefreshLockWait  = 25 * time.Second
	mcpRefreshLockRetry = 100 * time.Millisecond
)

// McpOAuthSettings carry a server's OAuth client settings, resolved.
type McpOAuthSettings struct {
	ClientID     string
	ClientSecret string
	CallbackPort int
	// CallbackURL is a loopback redirect URI; see
	// McpOAuthConfig.callbackUrl.
	CallbackURL string
	// Scope lists scopes to request, separated by spaces.
	Scope string
	// ClientName is the client_name for dynamic client registration.
	// Default: AppName().
	ClientName string
	// ClientRegistration is "dcr" or "cimd"; see
	// McpOAuthConfig.clientRegistration.
	ClientRegistration string
	// AuthServerMetadataURL is the authorization server metadata document
	// to use instead of discovery; see McpOAuthConfig.authServerMetadataUrl.
	AuthServerMetadataURL string
}

// mcpCallbackSettings describe where the loopback callback server
// listens and the redirect URI it serves.
type mcpCallbackSettings struct {
	// Host is the address to listen on.
	Host string
	// RedirectHost is the host name in the redirect URI.
	RedirectHost string
	Port         int
	Path         string
	// FixedRedirectURL is the exact redirect URI, when the port is fixed.
	FixedRedirectURL string
}

func mcpCallbackSettingsFrom(settings McpOAuthSettings) mcpCallbackSettings {
	raw := settings.CallbackURL
	if raw == "" {
		raw = "http://" + mcpCallbackHost + mcpCallbackPath
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		parsed = &url.URL{Host: mcpCallbackHost, Path: mcpCallbackPath}
	}
	address := strings.Trim(parsed.Hostname(), "[]")
	port := 0
	if parsed.Port() != "" {
		port, _ = strconv.Atoi(parsed.Port())
	} else if settings.CallbackPort != 0 {
		port = settings.CallbackPort
	}
	fixedRedirectURL := ""
	// A configured URI with a port is sent exactly as written, since
	// servers compare it as a string.
	if parsed.Port() != "" {
		fixedRedirectURL = raw
	} else if port != 0 {
		withPort := *parsed
		withPort.Host = fmt.Sprintf("%s:%d", parsed.Hostname(), port)
		fixedRedirectURL = withPort.String()
	}
	host := address
	// localhost is served on 127.0.0.1; browsers fall back to it when ::1
	// refuses.
	if address == "localhost" {
		host = mcpCallbackHost
	}
	return mcpCallbackSettings{
		Host:             host,
		RedirectHost:     address,
		Port:             port,
		Path:             parsed.Path,
		FixedRedirectURL: fixedRedirectURL,
	}
}

// mergeMcpScopes returns the scopes of both lists, each once.
func mergeMcpScopes(scopes ...string) string {
	seen := map[string]bool{}
	var merged []string
	for _, scope := range scopes {
		for _, item := range strings.Fields(scope) {
			if !seen[item] {
				seen[item] = true
				merged = append(merged, item)
			}
		}
	}
	return strings.Join(merged, " ")
}

// mcpStoreKeys returns the keys of a server's state: by name and URL, so
// servers sharing a URL keep separate accounts, and the legacy key by
// URL alone, written by older versions.
func mcpStoreKeys(name, serverURL string) (key, legacyKey string) {
	legacyKey = mcpNormalizedURLString(serverURL)
	return McpNamespace(name) + "|" + legacyKey, legacyKey
}

func mcpNormalizedURLString(value string) string {
	parsed, err := mcp.ParseURLLoose(value)
	if err != nil {
		return value
	}
	return parsed.String()
}

func parseMcpStates(content string) map[string]*jsonx.Obj {
	states := map[string]*jsonx.Obj{}
	if strings.TrimSpace(content) == "" {
		return states
	}
	value, err := jsonx.Parse(content)
	if err != nil {
		return states
	}
	root, isRecord := isJSONRecord(value)
	if !isRecord {
		return states
	}
	for _, entry := range root.Entries() {
		key, _ := entry[0].(string)
		if obj, ok := entry[1].(*jsonx.Obj); ok {
			states[key] = obj
		}
	}
	return states
}

func serializeMcpStates(states map[string]*jsonx.Obj) string {
	root := jsonx.NewObj()
	for key, state := range states {
		root.Set(key, state)
	}
	return stringifyJSONIndent(root, "  ") + "\n"
}

// McpOAuthServerStore is one server's slice of the credential store.
type McpOAuthServerStore interface {
	Load() (*mcp.McpOAuthState, error)
	Save(state *mcp.McpOAuthState) error
	// WithRefreshLock runs fn while no other process refreshes the
	// server's tokens.
	WithRefreshLock(fn func() error) error
}

// McpOAuthCredentialStore keeps per-server OAuth state (client
// registration, tokens, pending PKCE verifier) in mcp-auth.json.
type McpOAuthCredentialStore struct {
	path string
	// lockDir is the directory for the refresh lock files; empty keeps
	// refreshes serialized in this process only.
	lockDir string
	mu      sync.Mutex
}

// NewMcpOAuthCredentialStore opens (creating when needed) the default
// credential store in the agent directory.
func NewMcpOAuthCredentialStore() *McpOAuthCredentialStore {
	return &McpOAuthCredentialStore{path: filepath.Join(GetAgentDir(), "mcp-auth.json"), lockDir: GetAgentDir()}
}

// NewMcpOAuthCredentialStoreAt opens a store at an explicit path; no
// cross-process refresh locks are taken.
func NewMcpOAuthCredentialStoreAt(path string) *McpOAuthCredentialStore {
	return &McpOAuthCredentialStore{path: path}
}

// ForServer returns the store slice for one server name and URL.
func (s *McpOAuthCredentialStore) ForServer(name, serverURL string) McpOAuthServerStore {
	key, legacyKey := mcpStoreKeys(name, serverURL)
	return &mcpServerStoreSlice{store: s, key: key, legacyKey: legacyKey}
}

type mcpServerStoreSlice struct {
	store     *McpOAuthCredentialStore
	key       string
	legacyKey string
}

// Load implements McpOAuthServerStore. The first server to load legacy
// state takes it over; others with the same URL sign in again.
func (slice *mcpServerStoreSlice) Load() (*mcp.McpOAuthState, error) {
	var result *mcp.McpOAuthState
	_, err := slice.store.withFileLock(func(current string) (string, bool) {
		states := parseMcpStates(current)
		if stateObj, exists := states[slice.key]; exists {
			result, _ = mcp.McpOAuthStateFromObj(stateObj)
			return "", false
		}
		legacy := states[slice.legacyKey]
		if legacy == nil {
			return "", false
		}
		delete(states, slice.legacyKey)
		states[slice.key] = legacy
		result, _ = mcp.McpOAuthStateFromObj(legacy)
		return serializeMcpStates(states), true
	})
	return result, err
}

// Save implements McpOAuthServerStore.
func (slice *mcpServerStoreSlice) Save(state *mcp.McpOAuthState) error {
	_, err := slice.store.withFileLock(func(current string) (string, bool) {
		states := parseMcpStates(current)
		states[slice.key] = state.ToObj()
		return serializeMcpStates(states), true
	})
	return err
}

// WithRefreshLock implements McpOAuthServerStore: a lock file per server,
// held from reading the tokens to saving new ones. When the process exits
// the lock file is removed; when it is killed the lock goes stale and the
// next process takes it over.
func (slice *mcpServerStoreSlice) WithRefreshLock(fn func() error) error {
	if slice.store.lockDir == "" {
		return fn()
	}
	if err := os.MkdirAll(slice.store.lockDir, 0o700); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(slice.key))
	hash := hex.EncodeToString(digest[:])[:16]
	lockPath := filepath.Join(slice.store.lockDir, "mcp-auth-refresh-"+hash)
	return withExclusiveFileLock(lockPath, mcpRefreshLockStale, mcpRefreshLockWait, fn)
}

// serverURLOf reads the serverUrl field of a stored state object.
func (s *McpOAuthCredentialStore) serverURLOf(state *jsonx.Obj) string {
	if state == nil {
		return ""
	}
	text, _ := state.MustGet("serverUrl").(string)
	return text
}

// withFileLock runs one guarded read-modify-write on mcp-auth.json; next
// is written when changed.
func (s *McpOAuthCredentialStore) withFileLock(fn func(current string) (string, bool)) (string, error) {
	var result string
	err := withExclusiveFileLock(s.path, mcpRefreshLockStale, mcpRefreshLockWait, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		current := ""
		if data, err := os.ReadFile(s.path); err == nil {
			current = string(data)
		} else if !os.IsNotExist(err) {
			return err
		}
		next, changed := fn(current)
		if !changed {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
			return err
		}
		result = next
		return os.WriteFile(s.path, []byte(next), 0o600)
	})
	return result, err
}

// Tokens returns the stored tokens of a server, for noticing sign-ins
// done by another process. Does not take over legacy state.
func (s *McpOAuthCredentialStore) Tokens(name, serverURL string) *mcp.OAuthTokens {
	key, legacyKey := mcpStoreKeys(name, serverURL)
	current := ""
	if data, err := os.ReadFile(s.path); err == nil {
		current = string(data)
	}
	states := parseMcpStates(current)
	stateObj := states[key]
	if stateObj == nil {
		stateObj = states[legacyKey]
	}
	if stateObj == nil {
		return nil
	}
	state, err := mcp.McpOAuthStateFromObj(stateObj)
	if err != nil || state == nil {
		return nil
	}
	return state.Tokens
}

// Remove deletes the stored credentials of a server; it reports whether
// credentials were stored. Removes legacy state the server would take
// over.
func (s *McpOAuthCredentialStore) Remove(name, serverURL string) bool {
	key, legacyKey := mcpStoreKeys(name, serverURL)
	removed := false
	_, _ = s.withFileLock(func(current string) (string, bool) {
		states := parseMcpStates(current)
		stored := ""
		if _, exists := states[key]; exists {
			stored = key
		} else if _, exists := states[legacyKey]; exists {
			stored = legacyKey
		}
		if stored == "" {
			return "", false
		}
		delete(states, stored)
		removed = true
		return serializeMcpStates(states), true
	})
	return removed
}

// registeredMcpRedirectURLs lists the redirect URIs of a registered
// client.
func registeredMcpRedirectURLs(client *mcp.OAuthClientInformation) []string {
	if client == nil || client.RedirectURIs == nil {
		return nil
	}
	return client.RedirectURIs
}

// mcpCallbackID computes the 12 characters identifying an MCP server URL
// in callback paths, the way Codex does.
func mcpCallbackID(serverURL string) string {
	resource, err := mcp.ResourceURLFromServerURL(serverURL)
	if err != nil {
		resource = serverURL
	}
	digest := sha256.Sum256([]byte(resource))
	return base64.RawURLEncoding.EncodeToString(digest[:9])
}

// mcpClientMetadataDocument is pi's Client ID Metadata Document for
// clientRegistration "cimd", chosen like Codex chooses its own. The
// configuration ensures the default callback path. Without the iss
// parameter in authorization responses (RFC 9207), the redirect URI and
// the document are specific to the MCP server, so a response cannot be
// mixed up with one from another authorization server (RFC 9700 section
// 4.4.2.2).
func mcpClientMetadataDocument(serverURL, redirectURL string, metadata *mcp.AuthorizationServerMetadata) (*mcp.OAuthClientMetadataDocument, error) {
	supportsCimd := metadata != nil && metadata.ClientIDMetadataDocumentSupported != nil && *metadata.ClientIDMetadataDocumentSupported
	supportsNone := false
	if metadata != nil {
		for _, method := range metadata.TokenEndpointAuthMethodsSupported {
			if method == "none" {
				supportsNone = true
			}
		}
	}
	if !supportsCimd || !supportsNone {
		return nil, errors.New(`The authorization server does not support Client ID Metadata Documents for public clients; remove oauth.clientRegistration "cimd"`)
	}
	if metadata.AuthorizationResponseIssParameterSupported != nil && *metadata.AuthorizationResponseIssParameterSupported {
		return &mcp.OAuthClientMetadataDocument{
			URL:         mcpClientMetadataBaseURL + "/client.json",
			RedirectURL: redirectURL,
		}, nil
	}
	id := mcpCallbackID(serverURL)
	redirect, err := url.Parse(redirectURL)
	if err != nil {
		return nil, err
	}
	redirect.Path = mcpCallbackPath + "/" + id
	return &mcp.OAuthClientMetadataDocument{
		URL:         mcpClientMetadataBaseURL + "/" + id + "/client.json",
		RedirectURL: redirect.String(),
	}, nil
}

func createMcpProvider(serverURL string, store mcp.McpOAuthStateStore, settings McpOAuthSettings, redirectURL string, onRedirect func(url string) error) *mcp.McpOAuthProvider {
	clientName := settings.ClientName
	if clientName == "" {
		clientName = AppName()
	}
	var documentFunc func(metadata *mcp.AuthorizationServerMetadata) (*mcp.OAuthClientMetadataDocument, error)
	if settings.ClientRegistration == "cimd" {
		documentFunc = func(metadata *mcp.AuthorizationServerMetadata) (*mcp.OAuthClientMetadataDocument, error) {
			return mcpClientMetadataDocument(serverURL, redirectURL, metadata)
		}
	}
	return mcp.NewMcpOAuthProvider(mcp.McpOAuthProviderOptions{
		ServerURL:              serverURL,
		RedirectURL:            redirectURL,
		ClientMetadata:         &mcp.OAuthClientMetadata{ClientName: clientName},
		ClientMetadataDocument: documentFunc,
		ClientID:               settings.ClientID,
		ClientSecret:           settings.ClientSecret,
		Store:                  store,
		OnRedirect:             onRedirect,
	})
}

// McpAuthProviderExt is the mcp.AuthProvider for MCP connections plus a
// Settled hook that resolves when no refresh is running, so shutdown does
// not drop rotated tokens before they are saved.
type McpAuthProviderExt struct {
	Token          func() (string, bool)
	OnUnauthorized func(context *mcp.UnauthorizedContext) error
	Settled        func() error
}

// AsAuthProvider exposes the provider to a transport.
func (p *McpAuthProviderExt) AsAuthProvider() *mcp.AuthProvider {
	return &mcp.AuthProvider{Token: p.Token, OnUnauthorized: p.OnUnauthorized}
}

// createMcpAuthProvider builds the auth provider for MCP connections: it
// sends the stored access token and refreshes it when it is about to
// expire or after a 401. It fails with McpOAuthAuthorizationRequiredError
// when the user has to sign in, including when the server asks for more
// scope (insufficient_scope). onChallenge receives the server's
// WWW-Authenticate challenge so sign-in can use its resource metadata
// URL and scope. settings is only called when a refresh is needed, so a
// secret that fails to resolve fails the refresh instead of the whole
// connection setup.
//
// Many servers rotate refresh tokens, so two refreshes with the same
// refresh token lose the grant. Requests in this process share one
// refresh, and other processes are kept out by the store's refresh lock,
// held from reading the tokens to saving new ones. Tokens that changed
// meanwhile (another process refreshed them, or the user signed in) are
// used without refreshing.
func createMcpAuthProvider(options struct {
	ServerURL   string
	Store       McpOAuthServerStore
	Settings    func() McpOAuthSettings
	OnChallenge func(challenge mcp.OAuthChallenge)
}) *McpAuthProviderExt {
	serverURL := options.ServerURL
	store := options.Store
	var mu sync.Mutex
	var refreshing *mcpRefreshFlight

	refresh := func(staleToken string, fetch mcp.McpFetch, challenge *mcp.OAuthChallenge) error {
		mu.Lock()
		flight := refreshing
		if flight != nil {
			mu.Unlock()
			<-flight.done
			return flight.err
		}
		flight = &mcpRefreshFlight{done: make(chan struct{})}
		refreshing = flight
		mu.Unlock()
		defer func() {
			mu.Lock()
			if refreshing == flight {
				refreshing = nil
			}
			mu.Unlock()
			close(flight.done)
		}()
		flight.err = store.WithRefreshLock(func() error {
			state, err := store.Load()
			if err != nil {
				return err
			}
			currentToken := ""
			if state != nil && state.Tokens != nil {
				currentToken = state.Tokens.AccessToken
			}
			// Tokens that changed meanwhile (another process refreshed
			// them, or the user signed in) are used without refreshing.
			if currentToken != staleToken {
				return nil
			}
			if state == nil || state.Tokens == nil || state.Tokens.RefreshToken == "" {
				return &mcp.McpOAuthAuthorizationRequiredError{}
			}
			settings := options.Settings()
			redirectURL := mcpCallbackSettingsFrom(settings).FixedRedirectURL
			if redirectURL == "" {
				if registered := registeredMcpRedirectURLs(state.ClientInformation); len(registered) > 0 {
					redirectURL = registered[0]
				} else {
					redirectURL = mcpFallbackRedirectURL
				}
			}
			provider := createMcpProvider(serverURL, store, settings, redirectURL, func(string) error { return nil })
			// Refreshes the tokens, or reports that a new sign-in is
			// needed.
			result, err := mcp.AuthorizeMcp(provider.AsProvider(), mcp.OAuthFlowOptions{
				ServerURL:                      serverURL,
				ResourceMetadataURL:            challengeResourceMetadata(challenge),
				AuthorizationServerMetadataURL: settings.AuthServerMetadataURL,
				Scope:                          challengeScope(challenge),
				Fetch:                          boundedMcpFetch(fetch, mcpOAuthRequestTimeout),
			})
			if err != nil {
				return err
			}
			if result == mcp.FlowRedirect {
				return &mcp.McpOAuthAuthorizationRequiredError{}
			}
			return nil
		})
		return flight.err
	}

	provider := &McpAuthProviderExt{}
	provider.Token = func() (string, bool) {
		mu.Lock()
		pending := refreshing
		mu.Unlock()
		if pending != nil {
			<-pending.done
		}
		state, err := store.Load()
		if err != nil || state == nil || state.Tokens == nil {
			return "", false
		}
		token := state.Tokens.AccessToken
		expired := state.TokensExpireAt != nil &&
			*state.TokensExpireAt-float64(mcpRefreshSkew.Milliseconds()) <= float64(time.Now().UnixMilli())
		if !expired || state.Tokens.RefreshToken == "" {
			return token, token != ""
		}
		// Failures fall through: the request goes out with the old token
		// and a 401 decides what happens.
		_ = refresh(token, nil, nil)
		if next, err := store.Load(); err == nil && next != nil && next.Tokens != nil {
			return next.Tokens.AccessToken, next.Tokens.AccessToken != ""
		}
		return token, token != ""
	}
	provider.OnUnauthorized = func(context *mcp.UnauthorizedContext) error {
		challenge := mcp.ParseWwwAuthenticate(context.Response.HeaderGet("www-authenticate"))
		options.OnChallenge(challenge)
		// A refresh keeps the granted scope, so more scope needs a new
		// sign-in.
		if challenge.Error == "insufficient_scope" {
			return &mcp.McpOAuthAuthorizationRequiredError{}
		}
		return refresh(context.Token, context.Fetch, &challenge)
	}
	provider.Settled = func() error {
		mu.Lock()
		pending := refreshing
		mu.Unlock()
		if pending != nil {
			<-pending.done
		}
		return nil
	}
	return provider
}

// mcpRefreshFlight is one shared in-flight refresh.
type mcpRefreshFlight struct {
	done chan struct{}
	err  error
}

func challengeResourceMetadata(challenge *mcp.OAuthChallenge) string {
	if challenge == nil {
		return ""
	}
	return challenge.ResourceMetadataURL
}

func challengeScope(challenge *mcp.OAuthChallenge) string {
	if challenge == nil {
		return ""
	}
	return challenge.Scope
}

// boundedMcpFetch wraps a fetch so each request of a refresh is bounded
// in time; nil wraps the default fetch.
func boundedMcpFetch(fetch mcp.McpFetch, timeout time.Duration) mcp.McpFetch {
	return func(request *mcp.FetchRequest) (*mcp.FetchResponse, error) {
		controller := abort.NewController()
		timer := time.AfterFunc(timeout, controller.Abort)
		defer timer.Stop()
		bounded := *request
		bounded.Signal = controller.Signal()
		if fetch == nil {
			fetch = mcp.DefaultMcpFetch
		}
		return fetch(&bounded)
	}
}

// McpSignInPrompt surfaces the authorization URL to the user.
type McpSignInPrompt struct {
	// ShowAuthorizationURL shows the URL and opens it in a browser.
	ShowAuthorizationURL func(url string)
	// PromptForRedirectURL asks for the redirect URL from the browser
	// address bar, for when the browser cannot reach the loopback
	// callback (for example over SSH). Aborted once the callback arrives.
	// Returns "" when the user cancels.
	PromptForRedirectURL func(signal *abort.Signal) (string, error)
}

// McpSignInCancelledError reports a cancelled sign-in.
type McpSignInCancelledError struct{}

func (e *McpSignInCancelledError) Error() string { return "Sign-in cancelled" }

// mcpAuthorizationResponse is the code plus iss of an authorization
// response.
type mcpAuthorizationResponse struct {
	Code   string
	Iss    string
	HasIss bool
}

func responseFromRedirectURL(input, state, redirectURL string) (*mcpAuthorizationResponse, error) {
	parsed, err := mcp.ParseURLLoose(strings.TrimSpace(input))
	if err != nil {
		return nil, errors.New("Expected the full redirect URL from the browser address bar")
	}
	// A server-specific redirect URI tells authorization servers apart, so
	// it must match exactly.
	expected, err := mcp.ParseURLLoose(redirectURL)
	if err != nil {
		return nil, errors.New("Expected the full redirect URL from the browser address bar")
	}
	if parsed.Scheme != expected.Scheme || parsed.Host != expected.Host || parsed.Path != expected.Path {
		return nil, errors.New("The redirect URL does not match this sign-in's redirect URI")
	}
	query := parsed.Query()
	if err := query.Get("error"); err != "" {
		description := query.Get("error_description")
		if description == "" {
			description = err
		}
		return nil, errors.New(description)
	}
	if query.Get("state") != state {
		return nil, errors.New("The redirect URL belongs to a different sign-in")
	}
	code := query.Get("code")
	if code == "" {
		return nil, errors.New("The redirect URL does not contain an authorization code")
	}
	response := &mcpAuthorizationResponse{Code: code}
	if iss := query.Get("iss"); iss != "" {
		response.Iss, response.HasIss = iss, true
	}
	return response, nil
}

// waitForMcpAuthorizationResponse waits for the browser callback or a
// pasted redirect URL, whichever comes first.
func waitForMcpAuthorizationResponse(
	callback *mcp.OAuthCallbackServer,
	state, redirectURL string,
	prompt McpSignInPrompt,
	signal *abort.Signal,
) (*mcpAuthorizationResponse, error) {
	controller := abort.NewController()
	// AbortSignal.any: an already-aborted sign-in signal aborts the prompt
	// immediately (OnAbort alone never fires post-abort).
	promptSignal := controller.Signal()
	if signal != nil {
		promptSignal = abort.Any(controller.Signal(), signal)
	}
	fromBrowser := callback.WaitForCallback(state, redirectURLPath(redirectURL))
	type outcome struct {
		response *mcpAuthorizationResponse
		err      error
	}
	fromUser := make(chan outcome, 1)
	go func() {
		input, err := prompt.PromptForRedirectURL(promptSignal)
		if err != nil {
			fromUser <- outcome{err: err}
			return
		}
		if strings.TrimSpace(input) == "" {
			fromUser <- outcome{err: &McpSignInCancelledError{}}
			return
		}
		response, err := responseFromRedirectURL(input, state, redirectURL)
		fromUser <- outcome{response: response, err: err}
	}()
	select {
	case browserOutcome := <-fromBrowser:
		controller.Abort()
		if browserOutcome.Err != nil {
			return nil, browserOutcome.Err
		}
		return &mcpAuthorizationResponse{
			Code:   browserOutcome.Callback.Code,
			Iss:    browserOutcome.Callback.Iss,
			HasIss: browserOutcome.Callback.Iss != "",
		}, nil
	case userOutcome := <-fromUser:
		return userOutcome.response, userOutcome.err
	}
}

func redirectURLPath(redirectURL string) string {
	if parsed, err := mcp.ParseURLLoose(redirectURL); err == nil {
		return parsed.Path
	}
	return ""
}

// listenForMcpCallback listens on port, or on a free port when it is
// taken and not required.
func listenForMcpCallback(settings mcpCallbackSettings, extraPaths []string, port int, required bool) (*mcp.OAuthCallbackServer, error) {
	options := mcp.OAuthCallbackServerOptions{
		Host:         settings.Host,
		RedirectHost: settings.RedirectHost,
		Path:         settings.Path,
		ExtraPaths:   extraPaths,
		RenderPage: func(page mcp.OAuthCallbackPage) string {
			if page.OK {
				return ai.OauthSuccessHTML("Signed in to the MCP server. You may now close this page.")
			}
			return ai.OauthErrorHTML(page.Message, page.Details)
		},
	}
	server, err := mcp.OAuthCallbackServerListen(optionsWithPort(options, port))
	if err == nil {
		return server, nil
	}
	if required || port == 0 {
		return nil, err
	}
	return mcp.OAuthCallbackServerListen(options)
}

func optionsWithPort(options mcp.OAuthCallbackServerOptions, port int) mcp.OAuthCallbackServerOptions {
	options.Port = port
	return options
}

// SignInMcpServer signs in to an MCP server. It uses the stored refresh
// token when possible; otherwise it runs the browser authorization code
// flow. Tokens are saved to store. Signal cancels the sign-in, which fails
// with McpSignInCancelledError.
func SignInMcpServer(options struct {
	ServerURL string
	Store     mcp.McpOAuthStateStore
	Settings  McpOAuthSettings
	Challenge *mcp.OAuthChallenge
	Prompt    McpSignInPrompt
	Signal    *abort.Signal
}) error {
	if options.Signal != nil && options.Signal.Aborted() {
		return &McpSignInCancelledError{}
	}
	serverURL := options.ServerURL
	store := options.Store
	settings := options.Settings
	stored, err := store.Load()
	if err != nil {
		return err
	}
	stepUp := options.Challenge != nil && options.Challenge.Error == "insufficient_scope"
	callbackOptions := mcpCallbackSettingsFrom(settings)
	// Reuse the port of the registered redirect URI so the registered
	// client stays valid.
	preferredPort := callbackOptions.Port
	if registered := registeredMcpRedirectURLs(clientInformationOf(stored)); len(registered) > 0 {
		if parsed, parseErr := mcp.ParseURLLoose(registered[0]); parseErr == nil && parsed.Port() != "" {
			preferredPort, _ = strconv.Atoi(parsed.Port())
		}
	}
	cimd := settings.ClientRegistration == "cimd"
	extraPaths := []string{}
	if cimd {
		// The redirect URI of a server-specific Client ID Metadata
		// Document.
		extraPaths = append(extraPaths, mcpCallbackPath+"/"+mcpCallbackID(serverURL))
	}
	callback, err := listenForMcpCallback(callbackOptions, extraPaths, preferredPort, callbackOptions.Port != 0)
	if err != nil {
		return err
	}
	defer callback.Close()

	redirectURL := callbackOptions.FixedRedirectURL
	if redirectURL == "" {
		redirectURL = callback.RedirectURL()
	}
	if stored != nil {
		next := *stored
		// Every sign-in gets a fresh state parameter.
		next.OAuthState = ""
		// A registered client cannot use another redirect URI, and its
		// tokens belong to it. A Client ID Metadata Document is not
		// stored, so with one, a stored client was registered before and
		// is replaced.
		keepClient := settings.ClientID != ""
		if !keepClient && cimd {
			keepClient = stored.ClientInformation == nil
		}
		if !keepClient && !cimd {
			for _, registered := range registeredMcpRedirectURLs(stored.ClientInformation) {
				if registered == redirectURL {
					keepClient = true
				}
			}
		}
		if !keepClient {
			next.ClientInformation = nil
			next.Tokens = nil
			next.TokensExpireAt = nil
		}
		if err := store.Save(&next); err != nil {
			return err
		}
	}

	var authorizationURL string
	provider := createMcpProvider(serverURL, store, settings, redirectURL, func(target string) error {
		authorizationURL = target
		return nil
	})
	scope := mergeMcpScopes(settings.Scope, mcpChallengeScopeFor(options.Challenge, stepUp, stored))
	flowOptions := mcp.OAuthFlowOptions{
		ServerURL:                      serverURL,
		ResourceMetadataURL:            challengeResourceMetadata(options.Challenge),
		AuthorizationServerMetadataURL: settings.AuthServerMetadataURL,
		Scope:                          scope,
		Fetch:                          boundedMcpFetch(nil, mcpOAuthRequestTimeout),
		Signal:                         options.Signal,
		// A refresh keeps the granted scope; a server asking for more
		// needs the browser flow.
		SkipRefresh: stepUp,
	}
	if result, err := mcp.AuthorizeMcp(provider.AsProvider(), flowOptions); err != nil {
		return err
	} else if result == mcp.FlowAuthorized {
		return nil
	}
	if authorizationURL == "" {
		return errors.New("OAuth flow did not produce an authorization URL")
	}

	state, err := provider.State()
	if err != nil {
		return err
	}
	// The flow picks the redirect URI, which may be specific to the MCP
	// server.
	authorizationRedirectURL := redirectURL
	if parsed, parseErr := mcp.ParseURLLoose(authorizationURL); parseErr == nil {
		if uri := parsed.Query().Get("redirect_uri"); uri != "" {
			authorizationRedirectURL = uri
		}
	}
	options.Prompt.ShowAuthorizationURL(authorizationURL)
	response, err := waitForMcpAuthorizationResponse(callback, state, authorizationRedirectURL, options.Prompt, options.Signal)
	if err != nil {
		if options.Signal != nil && options.Signal.Aborted() {
			return &McpSignInCancelledError{}
		}
		return err
	}
	exchangeOptions := flowOptions
	exchangeOptions.AuthorizationCode = response.Code
	exchangeOptions.Iss = response.Iss
	exchangeOptions.HasIss = response.HasIss
	exchangeOptions.SkipRefresh = false
	if _, err := mcp.AuthorizeMcp(provider.AsProvider(), exchangeOptions); err != nil {
		if options.Signal != nil && options.Signal.Aborted() {
			return &McpSignInCancelledError{}
		}
		return err
	}
	return nil
}

func clientInformationOf(state *mcp.McpOAuthState) *mcp.OAuthClientInformation {
	if state == nil {
		return nil
	}
	return state.ClientInformation
}

// mcpChallengeScopeFor computes the requested scope: the configured scope
// plus, on a step-up, the challenged scopes and the scope granted so far
// (the challenge may list only the missing scopes).
func mcpChallengeScopeFor(challenge *mcp.OAuthChallenge, stepUp bool, stored *mcp.McpOAuthState) string {
	if challenge == nil {
		return ""
	}
	if !stepUp {
		return challenge.Scope
	}
	granted := ""
	if stored != nil && stored.Tokens != nil {
		granted = stored.Tokens.Scope
	}
	return mcp.StepUpScope(granted, challenge.Scope)
}
