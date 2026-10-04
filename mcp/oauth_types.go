package mcp

// Port of packages/mcp/src/oauth/types.ts (adapted from
// modelcontextprotocol/typescript-sdk v1.29.0, MIT): OAuth metadata and
// token shapes with dependency-free structural validation. Null and ""
// count as absent for optional fields: servers send them for fields they
// have no value for.

import (
	"errors"
	"math"

	"github.com/gladmo/openagent/jsonx"
)

// OAuthProtectedResourceMetadata is RFC 8414 protected-resource metadata.
type OAuthProtectedResourceMetadata struct {
	Resource             string
	AuthorizationServers []string
	ScopesSupported      []string
	Raw                  *jsonx.Obj
}

// AuthorizationServerMetadata is RFC 8414 or OpenID Connect discovery
// metadata.
type AuthorizationServerMetadata struct {
	Issuer                                     string
	AuthorizationEndpoint                      string
	TokenEndpoint                              string
	RegistrationEndpoint                       string // "" when absent
	ScopesSupported                            []string
	ResponseTypesSupported                     []string
	GrantTypesSupported                        []string
	TokenEndpointAuthMethodsSupported          []string
	CodeChallengeMethodsSupported              []string
	ClientIDMetadataDocumentSupported          *bool
	AuthorizationResponseIssParameterSupported *bool
	Raw                                        *jsonx.Obj
}

// OAuthTokens is a token endpoint response.
type OAuthTokens struct {
	AccessToken  string
	TokenType    string
	ExpiresIn    float64
	HasExpiresIn bool
	Scope        string // "" when absent
	RefreshToken string // "" when absent
	IDToken      string // "" when absent
}

// OAuthClientMetadata is the dynamic-registration request payload.
type OAuthClientMetadata struct {
	RedirectURIs            []string
	TokenEndpointAuthMethod string // "" when absent
	GrantTypes              []string
	ResponseTypes           []string
	ClientName              string // "" when absent
	ClientURI               string // "" when absent
	LogoURI                 string // "" when absent
	Scope                   string // "" when absent
	Contacts                []string
	TosURI                  string // "" when absent
	PolicyURI               string // "" when absent
	JwksURI                 string // "" when absent
	Jwks                    any
	SoftwareID              string // "" when absent
	SoftwareVersion         string // "" when absent
	SoftwareStatement       string // "" when absent
}

// ToObj renders the metadata for a registration request body.
func (m *OAuthClientMetadata) ToObj() *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("redirect_uris", stringsToAny(m.RedirectURIs))
	if m.TokenEndpointAuthMethod != "" {
		obj.Set("token_endpoint_auth_method", m.TokenEndpointAuthMethod)
	}
	if m.GrantTypes != nil {
		obj.Set("grant_types", stringsToAny(m.GrantTypes))
	}
	if m.ResponseTypes != nil {
		obj.Set("response_types", stringsToAny(m.ResponseTypes))
	}
	if m.ClientName != "" {
		obj.Set("client_name", m.ClientName)
	}
	if m.ClientURI != "" {
		obj.Set("client_uri", m.ClientURI)
	}
	if m.LogoURI != "" {
		obj.Set("logo_uri", m.LogoURI)
	}
	if m.Scope != "" {
		obj.Set("scope", m.Scope)
	}
	if m.Contacts != nil {
		obj.Set("contacts", stringsToAny(m.Contacts))
	}
	if m.TosURI != "" {
		obj.Set("tos_uri", m.TosURI)
	}
	if m.PolicyURI != "" {
		obj.Set("policy_uri", m.PolicyURI)
	}
	if m.JwksURI != "" {
		obj.Set("jwks_uri", m.JwksURI)
	}
	if m.Jwks != nil {
		obj.Set("jwks", m.Jwks)
	}
	if m.SoftwareID != "" {
		obj.Set("software_id", m.SoftwareID)
	}
	if m.SoftwareVersion != "" {
		obj.Set("software_version", m.SoftwareVersion)
	}
	if m.SoftwareStatement != "" {
		obj.Set("software_statement", m.SoftwareStatement)
	}
	return obj
}

func stringsToAny(values []string) []any {
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	return items
}

// OAuthClientInformation is a client registration answer (the information
// half; the full answer also carries the registered metadata).
type OAuthClientInformation struct {
	ClientID              string
	ClientSecret          string // "" when absent
	ClientIDIssuedAt      *float64
	ClientSecretExpiresAt *float64
	// Registered metadata, present on full registration answers.
	TokenEndpointAuthMethod string
	HasTokenAuthMethod      bool
	RedirectURIs            []string
	Raw                     *jsonx.Obj
}

// OAuthDiscoveryState caches discovery results between flows.
type OAuthDiscoveryState struct {
	AuthorizationServerURL      string
	AuthorizationServerMetadata *AuthorizationServerMetadata
	ResourceMetadata            *OAuthProtectedResourceMetadata
	ResourceMetadataURL         string // "" when absent
}

// OAuthServerInfo is the discovery outcome for one server.
type OAuthServerInfo struct {
	AuthorizationServerURL      string
	AuthorizationServerMetadata *AuthorizationServerMetadata
	ResourceMetadata            *OAuthProtectedResourceMetadata
}

// OAuthChallenge is a WWW-Authenticate bearer challenge.
type OAuthChallenge struct {
	ResourceMetadataURL string // "" when absent
	Scope               string // "" when absent
	Error               string // "" when absent
	ErrorDescription    string // "" when absent
}

// absent treats null and "" as missing.
func oauthAbsent(value any) bool {
	if value == nil {
		return true
	}
	if text, isString := value.(string); isString && text == "" {
		return true
	}
	return false
}

func optionalStringField(value any, name string) (string, error) {
	if oauthAbsent(value) {
		return "", nil
	}
	text, isString := value.(string)
	if !isString || len(text) == 0 {
		return "", errors.New("Invalid " + name)
	}
	return text, nil
}

func optionalStringsField(value any, name string) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	list, isList := value.([]any)
	if !isList {
		return nil, errors.New("Invalid " + name)
	}
	items := make([]string, 0, len(list))
	for _, item := range list {
		text, isString := item.(string)
		if !isString {
			return nil, errors.New("Invalid " + name)
		}
		items = append(items, text)
	}
	return items, nil
}

// safeURL validates a required https-or-http URL, rejecting script
// schemes.
func safeURL(value any, name string) (string, error) {
	text, isString := value.(string)
	if !isString || len(text) == 0 {
		return "", errors.New("Invalid " + name)
	}
	parsed, err := parseLooseURL(text)
	if err != nil {
		return "", errors.New("Invalid " + name)
	}
	switch parsed.Scheme {
	case "javascript", "data", "vbscript":
		return "", errors.New("Invalid " + name)
	}
	return text, nil
}

func optionalURL(value any, name string) (string, error) {
	if oauthAbsent(value) {
		return "", nil
	}
	return safeURL(value, name)
}

// ParseProtectedResourceMetadata validates protected-resource metadata.
func ParseProtectedResourceMetadata(value any) (*OAuthProtectedResourceMetadata, error) {
	obj, isObj := isObject(value)
	if !isObj {
		return nil, errors.New("Invalid OAuth protected resource metadata")
	}
	metadata := &OAuthProtectedResourceMetadata{Raw: obj}
	var err error
	if metadata.Resource, err = safeURL(mustGet(obj, "resource"), "OAuth protected resource metadata resource"); err != nil {
		return nil, err
	}
	servers, err := optionalStringsField(mustGet(obj, "authorization_servers"), "authorization_servers")
	if err != nil {
		return nil, err
	}
	for _, server := range servers {
		if _, err := safeURL(server, "authorization server URL"); err != nil {
			return nil, err
		}
	}
	metadata.AuthorizationServers = servers
	if metadata.ScopesSupported, err = optionalStringsField(mustGet(obj, "scopes_supported"), "scopes_supported"); err != nil {
		return nil, err
	}
	return metadata, nil
}

// ParseAuthorizationServerMetadata validates authorization-server
// metadata.
func ParseAuthorizationServerMetadata(value any) (*AuthorizationServerMetadata, error) {
	obj, isObj := isObject(value)
	if !isObj {
		return nil, errors.New("Invalid authorization server metadata")
	}
	metadata := &AuthorizationServerMetadata{Raw: obj}
	var err error
	responseTypes, err := optionalStringsField(mustGet(obj, "response_types_supported"), "response_types_supported")
	if err != nil {
		return nil, err
	}
	if responseTypes == nil {
		return nil, errors.New("Invalid response_types_supported")
	}
	metadata.ResponseTypesSupported = responseTypes
	if metadata.Issuer, err = safeURL(mustGet(obj, "issuer"), "authorization server issuer"); err != nil {
		return nil, err
	}
	if metadata.AuthorizationEndpoint, err = safeURL(mustGet(obj, "authorization_endpoint"), "authorization endpoint"); err != nil {
		return nil, err
	}
	if metadata.TokenEndpoint, err = safeURL(mustGet(obj, "token_endpoint"), "token endpoint"); err != nil {
		return nil, err
	}
	if metadata.RegistrationEndpoint, err = optionalURL(mustGet(obj, "registration_endpoint"), "registration endpoint"); err != nil {
		return nil, err
	}
	if metadata.ScopesSupported, err = optionalStringsField(mustGet(obj, "scopes_supported"), "scopes_supported"); err != nil {
		return nil, err
	}
	if metadata.GrantTypesSupported, err = optionalStringsField(mustGet(obj, "grant_types_supported"), "grant_types_supported"); err != nil {
		return nil, err
	}
	if metadata.TokenEndpointAuthMethodsSupported, err = optionalStringsField(
		mustGet(obj, "token_endpoint_auth_methods_supported"), "token_endpoint_auth_methods_supported"); err != nil {
		return nil, err
	}
	if metadata.CodeChallengeMethodsSupported, err = optionalStringsField(
		mustGet(obj, "code_challenge_methods_supported"), "code_challenge_methods_supported"); err != nil {
		return nil, err
	}
	if supported, has := obj.Get("client_id_metadata_document_supported"); has {
		if boolean, isBool := supported.(bool); isBool {
			metadata.ClientIDMetadataDocumentSupported = &boolean
		}
	}
	if supported, has := obj.Get("authorization_response_iss_parameter_supported"); has {
		if boolean, isBool := supported.(bool); isBool {
			metadata.AuthorizationResponseIssParameterSupported = &boolean
		}
	}
	return metadata, nil
}

// ParseOAuthTokens validates a token endpoint response.
func ParseOAuthTokens(value any) (*OAuthTokens, error) {
	obj, isObj := isObject(value)
	if !isObj {
		return nil, errors.New("Invalid OAuth token response")
	}
	tokens := &OAuthTokens{}
	var err error
	if tokens.AccessToken, err = optionalStringField(mustGet(obj, "access_token"), "access_token"); err != nil {
		return nil, err
	}
	if tokens.TokenType, err = optionalStringField(mustGet(obj, "token_type"), "token_type"); err != nil {
		return nil, err
	}
	// Number(null) is 0, which would mark the token as expired at once.
	if expires, has := obj.Get("expires_in"); has && !oauthAbsent(expires) {
		number, isNumber := expires.(float64)
		if !isNumber || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, errors.New("Invalid expires_in")
		}
		tokens.ExpiresIn, tokens.HasExpiresIn = number, true
	}
	if tokens.Scope, err = optionalStringField(mustGet(obj, "scope"), "scope"); err != nil {
		return nil, err
	}
	if tokens.RefreshToken, err = optionalStringField(mustGet(obj, "refresh_token"), "refresh_token"); err != nil {
		return nil, err
	}
	if tokens.IDToken, err = optionalStringField(mustGet(obj, "id_token"), "id_token"); err != nil {
		return nil, err
	}
	return tokens, nil
}

// ParseClientInformation validates a dynamic-registration response.
func ParseClientInformation(value any) (*OAuthClientInformation, error) {
	obj, isObj := isObject(value)
	if !isObj {
		return nil, errors.New("Invalid OAuth client registration response")
	}
	information := &OAuthClientInformation{Raw: obj}
	var err error
	if information.ClientID, err = optionalStringField(mustGet(obj, "client_id"), "client_id"); err != nil {
		return nil, err
	}
	if information.ClientSecret, err = optionalStringField(mustGet(obj, "client_secret"), "client_secret"); err != nil {
		return nil, err
	}
	if issued, has := obj.Get("client_id_issued_at"); has {
		if number, isNumber := issued.(float64); isNumber {
			information.ClientIDIssuedAt = &number
		}
	}
	if expires, has := obj.Get("client_secret_expires_at"); has {
		if number, isNumber := expires.(float64); isNumber {
			information.ClientSecretExpiresAt = &number
		}
	}
	redirects, err := optionalStringsField(mustGet(obj, "redirect_uris"), "redirect_uris")
	if err != nil {
		return nil, err
	}
	if redirects == nil {
		redirects = []string{}
	}
	information.RedirectURIs = redirects
	if method, has := obj.Get("token_endpoint_auth_method"); has {
		if text, isString := method.(string); isString {
			information.TokenEndpointAuthMethod = text
			information.HasTokenAuthMethod = true
		}
	}
	return information, nil
}
