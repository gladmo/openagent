package mcp

// Port of packages/mcp/src/oauth/flow.ts (adapted from
// modelcontextprotocol/typescript-sdk v1.29.0, MIT): the authorization
// code flow with PKCE, dynamic client registration, refresh grants, and
// the transport AuthProvider adapter.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/gladmo/openagent/jsonx"
)

// AddClientAuthentication injects client credentials into a token request.
type AddClientAuthentication func(headers *[][2]string, params url.Values, target string, metadata *AuthorizationServerMetadata) error

// OAuthClientMetadataDocument is a Client ID Metadata Document: an https
// URL used as client_id, and a redirect URI it lists.
type OAuthClientMetadataDocument struct {
	URL         string
	RedirectURL string
}

// CredentialKind selects which stored credentials InvalidateCredentials
// drops.
type CredentialKind string

const (
	InvalidateAll       CredentialKind = "all"
	InvalidateClient    CredentialKind = "client"
	InvalidateTokens    CredentialKind = "tokens"
	InvalidateVerifier  CredentialKind = "verifier"
	InvalidateDiscovery CredentialKind = "discovery"
)

// OAuthClientProvider is the stateful client the flow drives; nil
// function fields are optional steps the provider does not support.
type OAuthClientProvider struct {
	RedirectURL             string
	ClientMetadata          *OAuthClientMetadata
	ClientMetadataDocument  func(metadata *AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error)
	State                   func() (string, error)
	ClientInformation       func() (*OAuthClientInformation, error)
	SaveClientInformation   func(information *OAuthClientInformation) error
	Tokens                  func() (*OAuthTokens, error)
	SaveTokens              func(tokens *OAuthTokens) error
	RedirectToAuthorization func(target string) error
	SaveCodeVerifier        func(verifier string) error
	CodeVerifier            func() (string, error)
	AddClientAuthentication AddClientAuthentication
	InvalidateCredentials   func(kind CredentialKind) error
	SaveDiscoveryState      func(state *OAuthDiscoveryState) error
	DiscoveryState          func() (*OAuthDiscoveryState, error)
}

// OAuthFlowOptions tunes one authorizeMcp run.
type OAuthFlowOptions struct {
	ServerURL string
	// AuthorizationCode completes the flow from an authorization response.
	AuthorizationCode string
	// Iss is the iss parameter of the authorization response that
	// delivered AuthorizationCode (RFC 9207).
	Iss                 string
	HasIss              bool
	Scope               string
	ResourceMetadataURL string
	// AuthorizationServerMetadataURL is a metadata document to use instead
	// of discovery, for servers that advertise a wrong authorization
	// server or none. It is trusted as configured. Must use https, except
	// on loopback.
	AuthorizationServerMetadataURL string
	Fetch                          McpFetch
	SkipIssuerValidation           bool
	// SkipRefresh goes straight to the authorization redirect instead of
	// refreshing stored tokens, for example when the server asks for
	// scopes the current grant lacks (a refresh keeps the old scope).
	SkipRefresh bool
}

// OAuthFlowResult reports the flow outcome.
type OAuthFlowResult string

const (
	FlowAuthorized OAuthFlowResult = "AUTHORIZED"
	FlowRedirect   OAuthFlowResult = "REDIRECT"
)

// TokenRequestOptions tune one token endpoint request.
type TokenRequestOptions struct {
	Metadata                *AuthorizationServerMetadata
	ClientInformation       *OAuthClientInformation
	Resource                string
	AddClientAuthentication AddClientAuthentication
	Fetch                   McpFetch
}

func loopbackHost(hostname string) bool {
	return hostname == "localhost" || hostname == "127.0.0.1" || hostname == "[::1]" || hostname == "::1"
}

func secureEndpoint(value string) (string, error) {
	parsed, err := parseLooseURL(value)
	if err != nil {
		return "", &OAuthInsecureEndpointError{Endpoint: value}
	}
	if parsed.Scheme != "https" && !loopbackHost(parsed.Hostname()) {
		return "", &OAuthInsecureEndpointError{Endpoint: parsed.String()}
	}
	return parsed.String(), nil
}

type clientAuthMethod string

const (
	authMethodBasic clientAuthMethod = "client_secret_basic"
	authMethodPost  clientAuthMethod = "client_secret_post"
	authMethodNone  clientAuthMethod = "none"
)

func selectClientAuthMethod(information *OAuthClientInformation, supported []string) clientAuthMethod {
	hinted := ""
	if information != nil && information.HasTokenAuthMethod {
		hinted = information.TokenEndpointAuthMethod
	}
	offers := func(method string) bool {
		if len(supported) == 0 {
			return true
		}
		for _, candidate := range supported {
			if candidate == method {
				return true
			}
		}
		return false
	}
	switch hinted {
	case "client_secret_basic", "client_secret_post", "none":
		if offers(hinted) {
			return clientAuthMethod(hinted)
		}
	}
	hasSecret := information != nil && information.ClientSecret != ""
	if len(supported) == 0 {
		if hasSecret {
			return authMethodBasic
		}
		return authMethodNone
	}
	if hasSecret && offers("client_secret_basic") {
		return authMethodBasic
	}
	if hasSecret && offers("client_secret_post") {
		return authMethodPost
	}
	if offers("none") {
		return authMethodNone
	}
	if hasSecret {
		return authMethodPost
	}
	return authMethodNone
}

func applyClientAuthentication(method clientAuthMethod, information *OAuthClientInformation, headers *[][2]string, params url.Values) error {
	setHeader := func(name, value string) {
		for index, header := range *headers {
			if strings.EqualFold(header[0], name) {
				(*headers)[index] = [2]string{name, value}
				return
			}
		}
		*headers = append(*headers, [2]string{name, value})
	}
	if method == authMethodBasic {
		if information == nil || information.ClientSecret == "" {
			return errors.New("client_secret_basic requires a client secret")
		}
		setHeader("Authorization", BasicAuth(information.ClientID, information.ClientSecret))
		return nil
	}
	params.Set("client_id", information.ClientID)
	if method == authMethodPost && information.ClientSecret != "" {
		params.Set("client_secret", information.ClientSecret)
	}
	return nil
}

// pkce generates a verifier and its S256 challenge.
func pkce() (verifier, challenge string, err error) {
	bytes := make([]byte, 32)
	if _, err = rand.Read(bytes); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(bytes)
	digest := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(digest[:])
	return verifier, challenge, nil
}

// StartAuthorizationOptions tune the authorization redirect.
type StartAuthorizationOptions struct {
	Metadata          *AuthorizationServerMetadata
	ClientInformation *OAuthClientInformation
	RedirectURL       string
	Scope             string
	State             string
	Resource          string
}

// StartAuthorizationResult carries the authorization URL and verifier.
type StartAuthorizationResult struct {
	URL      *url.URL
	Verifier string
}

// StartAuthorization builds the authorization URL and the PKCE verifier.
func StartAuthorization(authorizationServerURL string, options StartAuthorizationOptions) (*StartAuthorizationResult, error) {
	metadata := options.Metadata
	if metadata != nil && !containsString(metadata.ResponseTypesSupported, "code") {
		return nil, errors.New("Authorization server does not support authorization codes")
	}
	if metadata != nil && metadata.CodeChallengeMethodsSupported != nil &&
		!containsString(metadata.CodeChallengeMethodsSupported, "S256") {
		return nil, errors.New("Authorization server does not support PKCE S256")
	}
	endpoint := ""
	if metadata != nil && metadata.AuthorizationEndpoint != "" {
		endpoint = metadata.AuthorizationEndpoint
	} else {
		base, err := parseLooseURL(authorizationServerURL)
		if err != nil {
			return nil, err
		}
		endpoint = base.Scheme + "://" + base.Host + "/authorize"
	}
	target, err := parseLooseURL(endpoint)
	if err != nil {
		return nil, err
	}
	verifier, challenge, err := pkce()
	if err != nil {
		return nil, err
	}
	query := target.Query()
	query.Set("response_type", "code")
	query.Set("client_id", options.ClientInformation.ClientID)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("redirect_uri", options.RedirectURL)
	if options.State != "" {
		query.Set("state", options.State)
	}
	if options.Scope != "" {
		query.Set("scope", options.Scope)
		if containsString(strings.Fields(options.Scope), "offline_access") {
			query.Set("prompt", "consent")
		}
	}
	if options.Resource != "" {
		query.Set("resource", options.Resource)
	}
	target.RawQuery = query.Encode()
	return &StartAuthorizationResult{URL: target, Verifier: verifier}, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func tokenRequest(authorizationServerURL string, options TokenRequestOptions, params url.Values) (*OAuthTokens, error) {
	tokenEndpoint := ""
	if options.Metadata != nil && options.Metadata.TokenEndpoint != "" {
		tokenEndpoint = options.Metadata.TokenEndpoint
	} else {
		base, err := parseLooseURL(authorizationServerURL)
		if err != nil {
			return nil, err
		}
		tokenEndpoint = base.Scheme + "://" + base.Host + "/token"
	}
	target, err := secureEndpoint(tokenEndpoint)
	if err != nil {
		return nil, err
	}
	headers := [][2]string{
		{"Accept", "application/json"},
		{"content-type", "application/x-www-form-urlencoded"},
	}
	if options.Resource != "" {
		params.Set("resource", options.Resource)
	}
	if options.AddClientAuthentication != nil {
		if err := options.AddClientAuthentication(&headers, params, target, options.Metadata); err != nil {
			return nil, err
		}
	} else {
		supported := []string{}
		if options.Metadata != nil && options.Metadata.TokenEndpointAuthMethodsSupported != nil {
			supported = options.Metadata.TokenEndpointAuthMethodsSupported
		}
		if err := applyClientAuthentication(
			selectClientAuthMethod(options.ClientInformation, supported),
			options.ClientInformation,
			&headers,
			params,
		); err != nil {
			return nil, err
		}
	}
	fetch := options.Fetch
	if fetch == nil {
		fetch = DefaultMcpFetch
	}
	response, err := fetch(&FetchRequest{
		Method:  "POST",
		URL:     target,
		Headers: headers,
		Body:    params.Encode(),
	})
	if err != nil {
		return nil, err
	}
	text, err := response.Text()
	if err != nil {
		return nil, err
	}
	var value any
	if parsed, parseErr := jsonx.Parse(text); parseErr == nil {
		value = parsed
	}
	// Servers may report OAuth errors with any status, so check the body
	// before the status.
	if obj, isObj := isObject(value); isObj {
		if errorCode, isString := obj.MustGet("error").(string); isString {
			description := errorCode
			if text, isString := obj.MustGet("error_description").(string); isString {
				description = text
			}
			uri := ""
			if text, isString := obj.MustGet("error_uri").(string); isString {
				uri = text
			}
			return nil, NewOAuthError(errorCode, description, uri)
		}
	}
	if !response.OK() {
		return nil, NewOAuthError("server_error", fmt.Sprintf("HTTP %d: %s", response.Status, text), "")
	}
	return ParseOAuthTokens(value)
}

// RegisterClient performs dynamic client registration.
func RegisterClient(authorizationServerURL string, metadata *AuthorizationServerMetadata, clientMetadata *OAuthClientMetadata, scope string, fetch McpFetch) (*OAuthClientInformation, error) {
	endpoint := ""
	if metadata != nil && metadata.RegistrationEndpoint != "" {
		endpoint = metadata.RegistrationEndpoint
	} else if metadata != nil {
		return nil, errors.New("Authorization server does not support dynamic client registration")
	} else {
		base, err := parseLooseURL(authorizationServerURL)
		if err != nil {
			return nil, err
		}
		endpoint = base.Scheme + "://" + base.Host + "/register"
	}
	payload := clientMetadata.ToObj()
	if scope != "" {
		payload.Set("scope", scope)
	}
	if fetch == nil {
		fetch = DefaultMcpFetch
	}
	response, err := fetch(&FetchRequest{
		Method:  "POST",
		URL:     endpoint,
		Headers: [][2]string{{"Accept", "application/json"}, {"content-type", "application/json"}},
		Body:    jsonx.Stringify(payload),
	})
	if err != nil {
		return nil, err
	}
	if !response.OK() {
		text, _ := response.Text()
		return nil, &OAuthRegistrationError{Status: response.Status, Body: text}
	}
	body, err := response.JSON()
	if err != nil {
		return nil, err
	}
	return ParseClientInformation(body)
}

// ExchangeAuthorizationCodeOptions tune a code exchange.
type ExchangeAuthorizationCodeOptions struct {
	TokenRequestOptions
	Code         string
	CodeVerifier string
	RedirectURL  string
}

// ExchangeAuthorizationCode trades the code for tokens.
func ExchangeAuthorizationCode(authorizationServerURL string, options ExchangeAuthorizationCodeOptions) (*OAuthTokens, error) {
	params := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {options.Code},
		"code_verifier": {options.CodeVerifier},
		"redirect_uri":  {options.RedirectURL},
	}
	return tokenRequest(authorizationServerURL, options.TokenRequestOptions, params)
}

// RefreshAuthorization refreshes an access token; the refresh token is
// kept when the answer omits it.
func RefreshAuthorization(authorizationServerURL string, options TokenRequestOptions, refreshToken string) (*OAuthTokens, error) {
	params := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	tokens, err := tokenRequest(authorizationServerURL, options, params)
	if err != nil {
		return nil, err
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = refreshToken
	}
	return tokens, nil
}

// withScope fills an absent scope with the requested one: a response
// without scope grants the requested scope (RFC 6749 section 5.1), and a
// refresh without scope keeps the scope of the grant (section 6).
func withScope(tokens *OAuthTokens, scope string) *OAuthTokens {
	if tokens.Scope == "" && scope != "" {
		tokens.Scope = scope
	}
	return tokens
}

// StepUpScope computes scopes for a step-up authorization: the challenged
// scopes plus the ones granted so far, since a challenge may list only the
// missing scopes and a token with just those would lose access the old one
// had (SEP-2350). Without challenged scopes, "" lets the flow pick its
// default.
func StepUpScope(granted, challenged string) string {
	if challenged == "" {
		return ""
	}
	seen := map[string]bool{}
	var scopes []string
	for _, scope := range []string{granted, challenged} {
		for _, item := range strings.Fields(scope) {
			if !seen[item] {
				seen[item] = true
				scopes = append(scopes, item)
			}
		}
	}
	return strings.Join(scopes, " ")
}

func runFlow(provider *OAuthClientProvider, options OAuthFlowOptions) (OAuthFlowResult, error) {
	var metadataURL string
	if options.AuthorizationServerMetadataURL != "" {
		secured, err := secureEndpoint(options.AuthorizationServerMetadataURL)
		if err != nil {
			return "", err
		}
		metadataURL = secured
	}
	// With a configured metadata URL, discovery is not cached, so changing
	// the URL applies at once.
	var discovered *OAuthServerInfo
	if metadataURL == "" && provider.DiscoveryState != nil {
		if cached, err := provider.DiscoveryState(); err == nil && cached != nil && cached.AuthorizationServerURL != "" {
			discovered = &OAuthServerInfo{
				AuthorizationServerURL:      cached.AuthorizationServerURL,
				AuthorizationServerMetadata: cached.AuthorizationServerMetadata,
				ResourceMetadata:            cached.ResourceMetadata,
			}
			if discovered.AuthorizationServerMetadata == nil {
				metadata, err := DiscoverAuthorizationServerMetadata(discovered.AuthorizationServerURL, DiscoveryOptions{
					Fetch:                options.Fetch,
					SkipIssuerValidation: options.SkipIssuerValidation,
				})
				if err != nil {
					return "", err
				}
				discovered.AuthorizationServerMetadata = metadata
			}
		}
	}
	if discovered == nil {
		info, err := DiscoverOAuthServerInfo(options.ServerURL, OAuthServerInfoOptions{
			ResourceMetadataURL:            options.ResourceMetadataURL,
			AuthorizationServerMetadataURL: metadataURL,
			Fetch:                          options.Fetch,
			SkipIssuerValidation:           options.SkipIssuerValidation,
		})
		if err != nil {
			return "", err
		}
		discovered = info
	}
	if metadataURL == "" && provider.SaveDiscoveryState != nil {
		state := &OAuthDiscoveryState{
			AuthorizationServerURL:      discovered.AuthorizationServerURL,
			AuthorizationServerMetadata: discovered.AuthorizationServerMetadata,
			ResourceMetadata:            discovered.ResourceMetadata,
			ResourceMetadataURL:         options.ResourceMetadataURL,
		}
		if err := provider.SaveDiscoveryState(state); err != nil {
			return "", err
		}
	}
	metadata := discovered.AuthorizationServerMetadata
	resource, err := SelectResource(options.ServerURL, discovered.ResourceMetadata)
	if err != nil {
		return "", err
	}
	// An empty scope (for example from scopes_supported: []) falls through
	// to the next source.
	scope := options.Scope
	if scope == "" && discovered.ResourceMetadata != nil && len(discovered.ResourceMetadata.ScopesSupported) > 0 {
		scope = strings.Join(discovered.ResourceMetadata.ScopesSupported, " ")
	}
	if scope == "" && provider.ClientMetadata != nil {
		scope = provider.ClientMetadata.Scope
	}
	var stored *OAuthClientInformation
	if provider.ClientInformation != nil {
		if stored, err = provider.ClientInformation(); err != nil {
			return "", err
		}
	}
	var clientDocument *OAuthClientMetadataDocument
	if stored == nil && provider.ClientMetadataDocument != nil {
		if clientDocument, err = provider.ClientMetadataDocument(metadata); err != nil {
			return "", err
		}
	}
	if clientDocument != nil {
		documentURL, err := parseLooseURL(clientDocument.URL)
		if err != nil || documentURL.Scheme != "https" || documentURL.Path == "/" || documentURL.Path == "" {
			return "", errors.New("Invalid OAuth client metadata URL")
		}
	}
	client := stored
	if client == nil && clientDocument != nil {
		client = &OAuthClientInformation{ClientID: clientDocument.URL}
	}
	if client == nil {
		if options.AuthorizationCode != "" {
			return "", errors.New("OAuth client information is missing during code exchange")
		}
		if provider.SaveClientInformation == nil {
			return "", errors.New("OAuth client information cannot be persisted")
		}
		if client, err = RegisterClient(discovered.AuthorizationServerURL, metadata, provider.ClientMetadata, scope, options.Fetch); err != nil {
			return "", err
		}
		if err = provider.SaveClientInformation(client); err != nil {
			return "", err
		}
	}
	// The document's redirect URI may differ from the provider's, for
	// example by a server-specific path.
	redirectURL := provider.RedirectURL
	if clientDocument != nil && clientDocument.RedirectURL != "" {
		redirectURL = clientDocument.RedirectURL
	}
	tokenOptions := TokenRequestOptions{
		Metadata:                metadata,
		ClientInformation:       client,
		Resource:                resource,
		AddClientAuthentication: provider.AddClientAuthentication,
		Fetch:                   options.Fetch,
	}
	if options.AuthorizationCode != "" {
		// RFC 9207: never send a code from another authorization server to
		// this one.
		if metadata != nil && (options.HasIss || boolValue(metadata.AuthorizationResponseIssParameterSupported)) {
			if options.Iss != metadata.Issuer {
				return "", NewOAuthIssuerMismatchError(metadata.Issuer, options.Iss, options.HasIss)
			}
		}
		verifier, err := provider.CodeVerifier()
		if err != nil {
			return "", err
		}
		tokens, err := ExchangeAuthorizationCode(discovered.AuthorizationServerURL, ExchangeAuthorizationCodeOptions{
			TokenRequestOptions: tokenOptions,
			Code:                options.AuthorizationCode,
			CodeVerifier:        verifier,
			RedirectURL:         redirectURL,
		})
		if err != nil {
			return "", err
		}
		// A response without scope grants the requested scope (RFC 6749
		// section 5.1). Recorded so a step-up can keep it.
		if err := provider.SaveTokens(withScope(tokens, scope)); err != nil {
			return "", err
		}
		return FlowAuthorized, nil
	}
	var existing *OAuthTokens
	if !options.SkipRefresh && provider.Tokens != nil {
		if existing, err = provider.Tokens(); err != nil {
			return "", err
		}
	}
	if existing != nil && existing.RefreshToken != "" {
		tokens, refreshErr := RefreshAuthorization(discovered.AuthorizationServerURL, tokenOptions, existing.RefreshToken)
		if refreshErr == nil {
			if err := provider.SaveTokens(withScope(tokens, existing.Scope)); err != nil {
				return "", err
			}
			return FlowAuthorized, nil
		}
		var insecure *OAuthInsecureEndpointError
		if errors.As(refreshErr, &insecure) {
			return "", refreshErr
		}
		var oauthErr *OAuthError
		if errors.As(refreshErr, &oauthErr) && oauthErr.Code != "server_error" {
			return "", refreshErr
		}
	}
	var state string
	if provider.State != nil {
		if state, err = provider.State(); err != nil {
			return "", err
		}
	}
	authorization, err := StartAuthorization(discovered.AuthorizationServerURL, StartAuthorizationOptions{
		Metadata:          metadata,
		ClientInformation: client,
		RedirectURL:       redirectURL,
		Scope:             scope,
		State:             state,
		Resource:          resource,
	})
	if err != nil {
		return "", err
	}
	if err := provider.SaveCodeVerifier(authorization.Verifier); err != nil {
		return "", err
	}
	if err := provider.RedirectToAuthorization(authorization.URL.String()); err != nil {
		return "", err
	}
	return FlowRedirect, nil
}

// AuthorizeMcp runs the flow once, invalidating credentials and retrying
// on the OAuth errors that mean stored state went stale.
func AuthorizeMcp(provider *OAuthClientProvider, options OAuthFlowOptions) (OAuthFlowResult, error) {
	result, err := runFlow(provider, options)
	if err == nil {
		return result, nil
	}
	var oauthErr *OAuthError
	if !errors.As(err, &oauthErr) {
		return "", err
	}
	kind := InvalidateAll
	switch oauthErr.Code {
	case "invalid_client", "unauthorized_client":
	case "invalid_grant":
		kind = InvalidateTokens
	default:
		return "", err
	}
	if provider.InvalidateCredentials != nil {
		if invalidateErr := provider.InvalidateCredentials(kind); invalidateErr != nil {
			return "", invalidateErr
		}
	}
	return runFlow(provider, options)
}

func boolValue(value *bool) bool { return value != nil && *value }

// refreshFlight is one shared unauthorized refresh.
type refreshFlight struct {
	done chan struct{}
	err  error
}

// AdaptOAuthProvider builds the AuthProvider for a transport. After a 401
// it refreshes the tokens, or fails with McpOAuthAuthorizationRequiredError
// when the user has to authorize (again). Concurrent 401s share one
// refresh, and a request whose token was already replaced is just retried:
// with rotating refresh tokens, a second refresh with the old refresh
// token would fail and discard the new grant.
func AdaptOAuthProvider(provider *OAuthClientProvider) *AuthProvider {
	var mu sync.Mutex
	var inFlight *refreshFlight
	adapted := &AuthProvider{
		Token: func() (string, bool) {
			if provider.Tokens == nil {
				return "", false
			}
			tokens, err := provider.Tokens()
			if err != nil || tokens == nil {
				return "", false
			}
			return tokens.AccessToken, tokens.AccessToken != ""
		},
	}
	adapted.OnUnauthorized = func(context *UnauthorizedContext) error {
		challenge := ParseWwwAuthenticate(context.Response.HeaderGet("www-authenticate"))
		insufficientScope := challenge.Error == "insufficient_scope"
		mu.Lock()
		flight := inFlight
		started := flight == nil
		if started {
			flight = &refreshFlight{done: make(chan struct{})}
			inFlight = flight
		}
		mu.Unlock()
		if !started {
			<-flight.done
			return flight.err
		}
		// A token that was replaced since the request went out means
		// another request already refreshed it.
		if !insufficientScope && context.HasToken && provider.Tokens != nil {
			if tokens, err := provider.Tokens(); err == nil && tokens != nil &&
				tokens.AccessToken != "" && tokens.AccessToken != context.Token {
				mu.Lock()
				if inFlight == flight {
					inFlight = nil
				}
				mu.Unlock()
				close(flight.done)
				return nil
			}
		}
		go func() {
			flight.err = runUnauthorizedRefresh(provider, context, challenge, insufficientScope)
			mu.Lock()
			if inFlight == flight {
				inFlight = nil
			}
			mu.Unlock()
			close(flight.done)
		}()
		<-flight.done
		return flight.err
	}
	return adapted
}

func runUnauthorizedRefresh(provider *OAuthClientProvider, context *UnauthorizedContext, challenge OAuthChallenge, insufficientScope bool) error {
	grantedScope := ""
	if insufficientScope && provider.Tokens != nil {
		if tokens, err := provider.Tokens(); err == nil && tokens != nil {
			grantedScope = tokens.Scope
		}
	}
	scope := challenge.Scope
	if insufficientScope {
		scope = StepUpScope(grantedScope, challenge.Scope)
	}
	result, err := AuthorizeMcp(provider, OAuthFlowOptions{
		ServerURL:           context.ServerURL,
		ResourceMetadataURL: challenge.ResourceMetadataURL,
		Scope:               scope,
		Fetch:               context.Fetch,
		SkipRefresh:         insufficientScope,
	})
	if err != nil {
		return err
	}
	if result == FlowRedirect {
		return &McpOAuthAuthorizationRequiredError{}
	}
	return nil
}
