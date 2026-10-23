package mcp

// Ports of the core packages/mcp/test/oauth.test.ts cases: discovery,
// registration, the PKCE authorization-code flow, refresh-on-401 through
// AdaptOAuthProvider, issuer checks, and the loopback callback server.

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gladmo/openagent/jsonx"
)

// testOAuthProvider is the in-memory OAuthClientProvider of the reference
// tests.
type testOAuthProvider struct {
	mu               sync.Mutex
	redirectURL      string
	client           *OAuthClientInformation
	tokenSet         *OAuthTokens
	verifier         string
	discovery        *OAuthDiscoveryState
	authorizationURL string
	redirectedTo     string
}

func newTestOAuthProvider(redirectURL string) *testOAuthProvider {
	return &testOAuthProvider{redirectURL: redirectURL}
}

func (p *testOAuthProvider) asProvider() *OAuthClientProvider {
	return &OAuthClientProvider{
		RedirectURL: p.redirectURL,
		ClientMetadata: &OAuthClientMetadata{
			RedirectURIs:            []string{p.redirectURL},
			ClientName:              "openagent-mcp-test",
			GrantTypes:              []string{"authorization_code", "refresh_token"},
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: "none",
		},
		State: func() (string, error) { return "expected-state", nil },
		ClientInformation: func() (*OAuthClientInformation, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.client, nil
		},
		SaveClientInformation: func(information *OAuthClientInformation) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.client = information
			return nil
		},
		Tokens: func() (*OAuthTokens, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.tokenSet, nil
		},
		SaveTokens: func(tokens *OAuthTokens) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.tokenSet = tokens
			return nil
		},
		RedirectToAuthorization: func(target string) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.authorizationURL = target
			p.redirectedTo = target
			return nil
		},
		SaveCodeVerifier: func(verifier string) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.verifier = verifier
			return nil
		},
		CodeVerifier: func() (string, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.verifier == "" {
				return "", errors.New("Missing code verifier")
			}
			return p.verifier, nil
		},
		InvalidateCredentials: func(kind CredentialKind) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			if kind == InvalidateAll || kind == InvalidateClient {
				p.client = nil
			}
			if kind == InvalidateAll || kind == InvalidateTokens {
				p.tokenSet = nil
			}
			if kind == InvalidateAll || kind == InvalidateVerifier {
				p.verifier = ""
			}
			if kind == InvalidateAll || kind == InvalidateDiscovery {
				p.discovery = nil
			}
			return nil
		},
		SaveDiscoveryState: func(state *OAuthDiscoveryState) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.discovery = state
			return nil
		},
		DiscoveryState: func() (*OAuthDiscoveryState, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.discovery, nil
		},
	}
}

// oauthTestServer is a full authorization server + resource metadata
// endpoint driven through an injectable fetch.
type oauthTestServer struct {
	mu            sync.Mutex
	origin        string
	registered    int
	tokenGrants   int
	refreshes     int
	rotateTokens  bool
	lastTokenBody string
}

func (s *oauthTestServer) fetch(request *FetchRequest) (*FetchResponse, error) {
	target, err := url.Parse(request.URL)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.lastTokenBody = ""
	s.mu.Unlock()
	switch target.Path {
	case "/.well-known/oauth-protected-resource/mcp":
		return jsonResponse(200, jsonx.Stringify(jsonx.ObjFrom(
			"resource", s.origin+"/mcp",
			"authorization_servers", []any{s.origin},
			"scopes_supported", []any{"org:read"},
		)), map[string]string{"content-type": "application/json"}), nil
	case "/.well-known/oauth-authorization-server":
		return jsonResponse(200, jsonx.Stringify(jsonx.ObjFrom(
			"issuer", s.origin,
			"authorization_endpoint", s.origin+"/authorize",
			"token_endpoint", s.origin+"/token",
			"registration_endpoint", s.origin+"/register",
			"response_types_supported", []any{"code"},
			"grant_types_supported", []any{"authorization_code", "refresh_token"},
			"token_endpoint_auth_methods_supported", []any{"none"},
			"code_challenge_methods_supported", []any{"S256"},
		)), map[string]string{"content-type": "application/json"}), nil
	case "/register":
		s.mu.Lock()
		s.registered++
		s.mu.Unlock()
		return jsonResponse(200, jsonx.Stringify(jsonx.ObjFrom(
			"client_id", "client-1",
			"redirect_uris", []any{"http://127.0.0.1/callback"},
			"token_endpoint_auth_method", "none",
		)), map[string]string{"content-type": "application/json"}), nil
	case "/token":
		body, _ := url.ParseQuery(request.Body)
		s.mu.Lock()
		s.lastTokenBody = request.Body
		refreshToken := body.Get("refresh_token")
		if body.Get("grant_type") == "refresh_token" {
			s.refreshes++
			if refreshToken != "refresh-1" && refreshToken != "refresh-2" {
				s.mu.Unlock()
				return jsonResponse(400, `{"error":"invalid_grant"}`, nil), nil
			}
		} else {
			s.tokenGrants++
			// The authorization code exchange must carry the PKCE pair.
			if body.Get("code_verifier") == "" {
				s.mu.Unlock()
				return jsonResponse(400, `{"error":"invalid_request"}`, nil), nil
			}
		}
		next := "refresh-2"
		if s.rotateTokens {
			next = "refresh-" + itoa(s.refreshes+2)
		}
		s.mu.Unlock()
		return jsonResponse(200, jsonx.Stringify(jsonx.ObjFrom(
			"access_token", "access-"+itoa(s.tokenGrants+s.refreshes),
			"token_type", "Bearer",
			"expires_in", float64(3600),
			"scope", "org:read",
			"refresh_token", next,
		)), map[string]string{"content-type": "application/json"}), nil
	}
	return jsonResponse(404, "not found", nil), nil
}

func TestOAuthDiscoversRegistersAuthorizesAndRefreshes(t *testing.T) {
	server := &oauthTestServer{origin: "https://auth.test"}
	provider := newTestOAuthProvider("http://127.0.0.1/callback")

	// First run: discovery, registration, and the authorization redirect.
	result, err := AuthorizeMcp(provider.asProvider(), OAuthFlowOptions{
		ServerURL: "https://auth.test/mcp",
		Fetch:     server.fetch,
	})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if result != FlowRedirect {
		t.Fatalf("result = %s", result)
	}
	if server.registered != 1 {
		t.Fatalf("registrations = %d", server.registered)
	}
	parsed, err := url.Parse(provider.authorizationURL)
	if err != nil {
		t.Fatalf("authorization url: %v", err)
	}
	query := parsed.Query()
	if query.Get("client_id") != "client-1" || query.Get("state") != "expected-state" ||
		query.Get("code_challenge_method") != "S256" || query.Get("scope") != "org:read" {
		t.Fatalf("authorization query = %v", query)
	}
	challenge := query.Get("code_challenge")
	digest := sha256.Sum256([]byte(provider.verifier))
	if challenge != base64.RawURLEncoding.EncodeToString(digest[:]) {
		t.Fatal("PKCE challenge does not match the stored verifier")
	}

	// Second run with the code: the exchange stores the tokens.
	result, err = AuthorizeMcp(provider.asProvider(), OAuthFlowOptions{
		ServerURL:         "https://auth.test/mcp",
		AuthorizationCode: "code-1",
		Fetch:             server.fetch,
	})
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if result != FlowAuthorized {
		t.Fatalf("result = %s", result)
	}
	if provider.tokenSet.AccessToken == "" || provider.tokenSet.RefreshToken == "" {
		t.Fatalf("tokens = %+v", provider.tokenSet)
	}

	// A 401 through the adapted provider refreshes and retries.
	adapted := AdaptOAuthProvider(provider.asProvider())
	// The rejected request carried the current token, as the transport
	// would have.
	currentToken := provider.tokenSet.AccessToken
	unauthorized := &UnauthorizedContext{
		Response:  &FetchResponse{Status: 401, Header: http.Header{}},
		ServerURL: "https://auth.test/mcp",
		Fetch:     server.fetch,
		Token:     currentToken,
		HasToken:  true,
	}
	if err := adapted.OnUnauthorized(unauthorized); err != nil {
		t.Fatalf("on unauthorized: %v", err)
	}
	if server.refreshes != 1 {
		t.Fatalf("refreshes = %d", server.refreshes)
	}
	if provider.tokenSet.AccessToken == "stale" || provider.tokenSet.AccessToken == "" {
		t.Fatalf("refreshed token = %q", provider.tokenSet.AccessToken)
	}
}

func TestOAuthStepUpAndChallengeParsing(t *testing.T) {
	if got := StepUpScope("a b", "b c"); got != "a b c" {
		t.Fatalf("step up = %q", got)
	}
	if got := StepUpScope("", ""); got != "" {
		t.Fatalf("empty step up = %q", got)
	}
	challenge := ParseWwwAuthenticate(`Bearer resource_metadata="https://r.test/.well-known/oauth-protected-resource", error="insufficient_scope", scope="admin:write"`)
	if challenge.ResourceMetadataURL != "https://r.test/.well-known/oauth-protected-resource" ||
		challenge.Error != "insufficient_scope" || challenge.Scope != "admin:write" {
		t.Fatalf("challenge = %+v", challenge)
	}
	// Quoted empty values count as absent.
	challenge = ParseWwwAuthenticate(`Bearer scope=""`)
	if challenge.Scope != "" {
		t.Fatalf("empty scope = %q", challenge.Scope)
	}
	// Non-bearer schemes carry nothing.
	if challenge := ParseWwwAuthenticate(`Basic realm="x"`); challenge.Error != "" || challenge.Scope != "" {
		t.Fatalf("basic challenge = %+v", challenge)
	}
}

func TestOAuthIssuerValidation(t *testing.T) {
	server := &oauthTestServer{origin: "https://auth.test"}
	_, err := DiscoverAuthorizationServerMetadata("https://other.test", DiscoveryOptions{Fetch: server.fetch})
	var mismatch *OAuthIssuerMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v", err)
	}
	if _, err := DiscoverAuthorizationServerMetadata("https://auth.test", DiscoveryOptions{SkipIssuerValidation: true, Fetch: server.fetch}); err != nil {
		t.Fatalf("skip validation: %v", err)
	}
}

func TestOAuthIssParameterGuard(t *testing.T) {
	server := &oauthTestServer{origin: "https://auth.test"}
	provider := newTestOAuthProvider("http://127.0.0.1/callback")
	// Prime registration and the verifier.
	if _, err := AuthorizeMcp(provider.asProvider(), OAuthFlowOptions{
		ServerURL: "https://auth.test/mcp",
		Fetch:     server.fetch,
	}); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	_, err := AuthorizeMcp(provider.asProvider(), OAuthFlowOptions{
		ServerURL:         "https://auth.test/mcp",
		AuthorizationCode: "code-1",
		Iss:               "https://evil.test",
		HasIss:            true,
		Fetch:             server.fetch,
	})
	var mismatch *OAuthIssuerMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v", err)
	}
	// The matching iss exchanges cleanly.
	if _, err := AuthorizeMcp(provider.asProvider(), OAuthFlowOptions{
		ServerURL:         "https://auth.test/mcp",
		AuthorizationCode: "code-1",
		Iss:               "https://auth.test",
		HasIss:            true,
		Fetch:             server.fetch,
	}); err != nil {
		t.Fatalf("matching iss: %v", err)
	}
}

func TestOAuthInsufficientScopeAsksForAuthorization(t *testing.T) {
	server := &oauthTestServer{origin: "https://auth.test"}
	provider := newTestOAuthProvider("http://127.0.0.1/callback")
	// Seed stored tokens with a refresh token.
	provider.mu.Lock()
	provider.tokenSet = &OAuthTokens{AccessToken: "stale", TokenType: "Bearer", RefreshToken: "refresh-1", Scope: "org:read"}
	provider.mu.Unlock()
	adapted := AdaptOAuthProvider(provider.asProvider())
	insufficient := &FetchResponse{Status: 403, Header: http.Header{}}
	insufficient.Header.Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="admin:write"`)
	err := adapted.OnUnauthorized(&UnauthorizedContext{
		Response:  insufficient,
		ServerURL: "https://auth.test/mcp",
		Fetch:     server.fetch,
		Token:     "stale",
		HasToken:  true,
	})
	var required *McpOAuthAuthorizationRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("err = %v", err)
	}
	if server.refreshes != 0 {
		t.Fatalf("insufficient scope must not refresh, refreshes = %d", server.refreshes)
	}
}

func TestMcpOAuthProviderBindsStateToServerURL(t *testing.T) {
	store := &MemoryOAuthStateStore{}
	first := NewMcpOAuthProvider(McpOAuthProviderOptions{
		ServerURL:   "https://a.test/mcp",
		RedirectURL: "http://127.0.0.1/callback",
		Store:       store,
		OnRedirect:  func(string) error { return nil },
	})
	_ = first.SaveTokens(&OAuthTokens{AccessToken: "a", TokenType: "Bearer"})
	// A provider for another server URL sees no tokens of the first.
	second := NewMcpOAuthProvider(McpOAuthProviderOptions{
		ServerURL:   "https://b.test/mcp",
		RedirectURL: "http://127.0.0.1/callback",
		Store:       store,
		OnRedirect:  func(string) error { return nil },
	})
	tokens, err := second.Tokens()
	if err != nil || tokens != nil {
		t.Fatalf("cross-server tokens = %+v %v", tokens, err)
	}
	if state, err := first.State(); err != nil || state == "" {
		t.Fatalf("state = %q %v", state, err)
	}
	if err := first.InvalidateCredentials(InvalidateAll); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if tokens, _ := first.Tokens(); tokens != nil {
		t.Fatal("tokens survived invalidation")
	}
}

func TestOAuthCallbackServer(t *testing.T) {
	server, err := OAuthCallbackServerListen(OAuthCallbackServerOptions{Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if !strings.HasPrefix(server.RedirectURL(), "http://127.0.0.1:") {
		t.Fatalf("redirect url = %q", server.RedirectURL())
	}
	wait := server.WaitForCallback("state-1", "")
	go func() {
		response, err := http.Get(server.RedirectURL() + "?code=the-code&state=state-1")
		if err != nil {
			t.Errorf("callback: %v", err)
			return
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || !strings.Contains(string(body), "Authorization complete") {
			t.Errorf("callback page = %d %s", response.StatusCode, string(body))
		}
	}()
	select {
	case outcome := <-wait:
		if outcome.Err != nil || outcome.Callback.Code != "the-code" || outcome.Callback.State != "state-1" {
			t.Fatalf("outcome = %+v", outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback not received")
	}
	// A state mismatch answers 400 and leaves the wait to time out.
	wait = server.WaitForCallback("state-2", "")
	response, err := http.Get(server.RedirectURL() + "?code=x&state=other")
	if err != nil {
		t.Fatalf("mismatch request: %v", err)
	}
	mismatchBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 400 || !strings.Contains(string(mismatchBody), "Invalid or expired OAuth state") {
		t.Fatalf("mismatch answer = %d %s", response.StatusCode, string(mismatchBody))
	}
	select {
	case outcome := <-wait:
		if outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "timed out") {
			t.Fatalf("outcome = %+v", outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not time out")
	}
	if err := server.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestOAuthInsecureEndpointRefused(t *testing.T) {
	server := &oauthTestServer{origin: "http://auth.test"}
	provider := newTestOAuthProvider("http://127.0.0.1/callback")
	// Discovery and registration over http work; the token request is
	// refused because credentials would cross plain HTTP.
	if _, err := AuthorizeMcp(provider.asProvider(), OAuthFlowOptions{
		ServerURL: "http://auth.test/mcp",
		Fetch:     server.fetch,
	}); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	_, err := AuthorizeMcp(provider.asProvider(), OAuthFlowOptions{
		ServerURL:         "http://auth.test/mcp",
		AuthorizationCode: "code-1",
		Fetch:             server.fetch,
	})
	var insecure *OAuthInsecureEndpointError
	if !errors.As(err, &insecure) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildAuthorizationServerDiscoveryUrls(t *testing.T) {
	urls := BuildAuthorizationServerDiscoveryUrls("https://auth.test/oauth")
	expected := []string{
		"https://auth.test/.well-known/oauth-authorization-server/oauth",
		"https://auth.test/.well-known/openid-configuration/oauth",
		"https://auth.test/oauth/.well-known/openid-configuration",
	}
	if len(urls) != len(expected) {
		t.Fatalf("urls = %+v", urls)
	}
	for index, wanted := range expected {
		if urls[index].URL != wanted {
			t.Fatalf("urls[%d] = %q, want %q", index, urls[index].URL, wanted)
		}
	}
	// Bare origins get the two root paths only.
	urls = BuildAuthorizationServerDiscoveryUrls("https://auth.test")
	if len(urls) != 2 || urls[0].Type != "oauth" || urls[1].Type != "oidc" {
		t.Fatalf("urls = %+v", urls)
	}
}

// Port of the iss rejection without discovered metadata (#11206): any iss
// is rejected before the code exchange.
func TestOAuthIssRejectedWithoutMetadata(t *testing.T) {
	server := &oauthTestServer{origin: "https://auth.test"}
	// Suppress authorization-server discovery so the flow runs without
	// metadata.
	noMetadataFetch := func(request *FetchRequest) (*FetchResponse, error) {
		if strings.HasSuffix(request.URL, "/.well-known/oauth-authorization-server") {
			return jsonResponse(404, "{}", map[string]string{"content-type": "application/json"}), nil
		}
		return server.fetch(request)
	}
	provider := newTestOAuthProvider("http://127.0.0.1/callback")
	if _, err := AuthorizeMcp(provider.asProvider(), OAuthFlowOptions{
		ServerURL: "https://auth.test/mcp",
		Fetch:     noMetadataFetch,
	}); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	_, err := AuthorizeMcp(provider.asProvider(), OAuthFlowOptions{
		ServerURL:         "https://auth.test/mcp",
		AuthorizationCode: "code-1",
		HasIss:            true,
		Iss:               "https://evil.example",
		Fetch:             noMetadataFetch,
	})
	mismatch, ok := err.(*OAuthIssuerMismatchError)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	if mismatch.hasExpected {
		t.Fatalf("expected none, got %q", mismatch.Expected)
	}
	if !strings.Contains(mismatch.Error(), "expected none") {
		t.Fatalf("message = %q", mismatch.Error())
	}
	if server.tokenGrants != 0 {
		t.Fatalf("code exchange ran %d times", server.tokenGrants)
	}
}

func TestDeriveApplicationType(t *testing.T) {
	if deriveApplicationType([]string{"http://127.0.0.1:8080/callback"}) != "native" {
		t.Fatal("loopback should be native")
	}
	if deriveApplicationType([]string{"myapp://oauth/callback"}) != "native" {
		t.Fatal("custom scheme should be native")
	}
	if deriveApplicationType([]string{"https://example.com/callback"}) != "web" {
		t.Fatal("https should be web")
	}
	if deriveApplicationType([]string{"not a url", "https://example.com/callback"}) != "web" {
		t.Fatal("unparseable URIs are ignored")
	}
}
