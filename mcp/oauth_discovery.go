package mcp

// Port of packages/mcp/src/oauth/discovery.ts (adapted from
// modelcontextprotocol/typescript-sdk v1.29.0, MIT): RFC 8414 / OIDC
// discovery for MCP servers, with authorization-server issuer validation.

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var errInvalidURL = errors.New("invalid URL")

// parseLooseURL parses an absolute URL like `new URL(text)` in the
// reference: a scheme is required, relative references are not accepted.
func parseLooseURL(text string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(text))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Opaque != "" {
		return nil, errInvalidURL
	}
	return parsed, nil
}

// ParseURLLoose is the exported parseLooseURL for sibling packages that
// need URL.canParse semantics.
func ParseURLLoose(text string) (*url.URL, error) { return parseLooseURL(text) }

// DiscoveryOptions tunes discovery requests.
type DiscoveryOptions struct {
	ResourceMetadataURL  string
	Fetch                McpFetch
	ProtocolVersion      string
	SkipIssuerValidation bool
}

// isDiscoveryMiss: 4xx and 502 mean "not here", so discovery tries the
// next candidate URL.
func isDiscoveryMiss(status int) bool {
	return (status >= 400 && status < 500) || status == 502
}

// pathSuffix is the path suffix for /.well-known/<kind><path>; empty for
// the root path.
func pathSuffix(pathname string) string {
	return strings.TrimSuffix(pathname, "/")
}

func compileWwwAuthField(name string) *regexp.Regexp {
	escaped := regexp.QuoteMeta(name)
	return regexp.MustCompile(`(?i)(?:^|[,\s])` + escaped + `=(?:"([^"]*)"|([^\s,]+))`)
}

// ParseWwwAuthenticate extracts challenge fields from a bearer or DPoP
// WWW-Authenticate header. An empty value (scope="") carries no
// information, so it counts as absent.
func ParseWwwAuthenticate(header string) OAuthChallenge {
	if header == "" {
		return OAuthChallenge{}
	}
	trimmed := strings.TrimLeft(header, " \t")
	scheme := trimmed
	if space := strings.IndexAny(trimmed, " \t"); space >= 0 {
		scheme = trimmed[:space]
	}
	scheme = strings.ToLower(scheme)
	if scheme != "bearer" && scheme != "dpop" {
		return OAuthChallenge{}
	}
	field := func(name string) string {
		pattern := compileWwwAuthField(name)
		match := pattern.FindStringSubmatch(header)
		if match == nil {
			return ""
		}
		if match[1] != "" {
			return match[1]
		}
		return match[2]
	}
	challenge := OAuthChallenge{
		Scope:            field("scope"),
		Error:            field("error"),
		ErrorDescription: field("error_description"),
	}
	if resourceMetadata := field("resource_metadata"); resourceMetadata != "" {
		if _, err := parseLooseURL(resourceMetadata); err == nil {
			challenge.ResourceMetadataURL = resourceMetadata
		}
	}
	return challenge
}

func fetchMetadata(target string, fetch McpFetch, protocolVersion string) (*FetchResponse, error) {
	if fetch == nil {
		fetch = DefaultMcpFetch
	}
	if protocolVersion == "" {
		protocolVersion = LATEST_PROTOCOL_VERSION
	}
	return fetch(&FetchRequest{
		Method:  "GET",
		URL:     target,
		Headers: [][2]string{{"Accept", "application/json"}, {"MCP-Protocol-Version", protocolVersion}},
	})
}

// DiscoverProtectedResourceMetadata loads RFC 8414 protected-resource
// metadata for the server URL, falling back to the root well-known path
// when the server has a non-root path and the suffixed lookup misses.
func DiscoverProtectedResourceMetadata(serverURL string, options DiscoveryOptions) (*OAuthProtectedResourceMetadata, error) {
	server, err := parseLooseURL(serverURL)
	if err != nil {
		return nil, err
	}
	fetch := options.Fetch
	if fetch == nil {
		fetch = DefaultMcpFetch
	}
	version := options.ProtocolVersion
	if version == "" {
		version = LATEST_PROTOCOL_VERSION
	}
	origin := server.Scheme + "://" + server.Host
	var target string
	if options.ResourceMetadataURL != "" {
		target = options.ResourceMetadataURL
	} else {
		target = origin + "/.well-known/oauth-protected-resource" + pathSuffix(server.Path)
	}
	response, err := fetchMetadata(target, fetch, version)
	if err != nil {
		return nil, err
	}
	if options.ResourceMetadataURL == "" && server.Path != "/" && isDiscoveryMiss(response.Status) {
		response.Discard()
		response, err = fetchMetadata(origin+"/.well-known/oauth-protected-resource", fetch, version)
		if err != nil {
			return nil, err
		}
	}
	if !response.OK() {
		response.Discard()
		return nil, fmt.Errorf("HTTP %d loading OAuth protected resource metadata", response.Status)
	}
	body, err := response.JSON()
	if err != nil {
		return nil, err
	}
	return ParseProtectedResourceMetadata(body)
}

// AuthorizationServerDiscoveryURL is one well-known candidate.
type AuthorizationServerDiscoveryURL struct {
	URL  string
	Type string // "oauth" | "oidc"
}

// BuildAuthorizationServerDiscoveryUrls lists the well-known candidates
// for an issuer: the OAuth path with the issuer path suffix, the OIDC path
// with it, and the OIDC path under the issuer path (RFC 8414 section 5).
func BuildAuthorizationServerDiscoveryUrls(authorizationServerURL string) []AuthorizationServerDiscoveryURL {
	issuer, err := parseLooseURL(authorizationServerURL)
	if err != nil {
		return nil
	}
	origin := issuer.Scheme + "://" + issuer.Host
	path := pathSuffix(issuer.Path)
	urls := []AuthorizationServerDiscoveryURL{
		{URL: origin + "/.well-known/oauth-authorization-server" + path, Type: "oauth"},
		{URL: origin + "/.well-known/openid-configuration" + path, Type: "oidc"},
	}
	if path != "" {
		urls = append(urls, AuthorizationServerDiscoveryURL{
			URL:  origin + path + "/.well-known/openid-configuration",
			Type: "oidc",
		})
	}
	return urls
}

// DiscoverAuthorizationServerMetadata loads authorization-server metadata
// from the first well-known candidate that answers.
func DiscoverAuthorizationServerMetadata(authorizationServerURL string, options DiscoveryOptions) (*AuthorizationServerMetadata, error) {
	fetch := options.Fetch
	if fetch == nil {
		fetch = DefaultMcpFetch
	}
	version := options.ProtocolVersion
	if version == "" {
		version = LATEST_PROTOCOL_VERSION
	}
	for _, candidate := range BuildAuthorizationServerDiscoveryUrls(authorizationServerURL) {
		response, err := fetchMetadata(candidate.URL, fetch, version)
		if err != nil {
			return nil, err
		}
		if !response.OK() {
			response.Discard()
			if isDiscoveryMiss(response.Status) {
				continue
			}
			return nil, fmt.Errorf("HTTP %d loading authorization server metadata from %s", response.Status, candidate.URL)
		}
		body, err := response.JSON()
		if err != nil {
			return nil, err
		}
		metadata, err := ParseAuthorizationServerMetadata(body)
		if err != nil {
			return nil, err
		}
		if !options.SkipIssuerValidation {
			expected := authorizationServerURL
			// URL parsing adds a trailing slash to bare origins, so compare
			// without one on either side.
			if trimTrailingSlash(metadata.Issuer) != trimTrailingSlash(expected) {
				return nil, NewOAuthIssuerMismatchError(expected, metadata.Issuer, true)
			}
		}
		return metadata, nil
	}
	return nil, nil
}

func trimTrailingSlash(value string) string {
	return strings.TrimSuffix(value, "/")
}

// OAuthServerInfoOptions tunes DiscoverOAuthServerInfo.
type OAuthServerInfoOptions struct {
	ResourceMetadataURL string
	// AuthorizationServerMetadataURL is a metadata document to use instead
	// of discovery. It is trusted as configured, so its issuer is not
	// checked.
	AuthorizationServerMetadataURL string
	Fetch                          McpFetch
	SkipIssuerValidation           bool
}

// DiscoverOAuthServerInfo resolves the authorization server for an MCP
// server URL through its protected-resource metadata. Network failures
// propagate; a missing metadata document leaves it unset.
func DiscoverOAuthServerInfo(serverURL string, options OAuthServerInfoOptions) (*OAuthServerInfo, error) {
	var resourceMetadata *OAuthProtectedResourceMetadata
	if metadata, err := DiscoverProtectedResourceMetadata(serverURL, DiscoveryOptions{
		ResourceMetadataURL: options.ResourceMetadataURL,
		Fetch:               options.Fetch,
	}); err == nil {
		resourceMetadata = metadata
	} else if isNetworkError(err) {
		return nil, err
	}
	if options.AuthorizationServerMetadataURL != "" {
		response, err := fetchMetadata(options.AuthorizationServerMetadataURL, options.Fetch, LATEST_PROTOCOL_VERSION)
		if err != nil {
			return nil, err
		}
		if !response.OK() {
			response.Discard()
			return nil, fmt.Errorf("HTTP %d loading authorization server metadata from %s",
				response.Status, options.AuthorizationServerMetadataURL)
		}
		body, err := response.JSON()
		if err != nil {
			return nil, err
		}
		metadata, err := ParseAuthorizationServerMetadata(body)
		if err != nil {
			return nil, err
		}
		return &OAuthServerInfo{
			AuthorizationServerURL:      metadata.Issuer,
			AuthorizationServerMetadata: metadata,
			ResourceMetadata:            resourceMetadata,
		}, nil
	}
	authorizationServerURL := rootURL(serverURL)
	if resourceMetadata != nil && len(resourceMetadata.AuthorizationServers) > 0 {
		authorizationServerURL = resourceMetadata.AuthorizationServers[0]
	}
	metadata, err := DiscoverAuthorizationServerMetadata(authorizationServerURL, DiscoveryOptions{
		Fetch:                options.Fetch,
		SkipIssuerValidation: options.SkipIssuerValidation,
	})
	if err != nil {
		return nil, err
	}
	return &OAuthServerInfo{
		AuthorizationServerURL:      authorizationServerURL,
		AuthorizationServerMetadata: metadata,
		ResourceMetadata:            resourceMetadata,
	}, nil
}

// rootURL renders the server URL reduced to its origin root, like
// new URL("/", serverUrl).
func rootURL(serverURL string) string {
	parsed, err := parseLooseURL(serverURL)
	if err != nil {
		return serverURL
	}
	return parsed.Scheme + "://" + parsed.Host + "/"
}

// ResourceURLFromServerURL strips the fragment from a server URL.
func ResourceURLFromServerURL(value string) (string, error) {
	parsed, err := parseLooseURL(value)
	if err != nil {
		return "", err
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}

// SelectResource picks the resource parameter for token requests from the
// protected-resource metadata, checking it covers the server URL.
func SelectResource(serverURL string, metadata *OAuthProtectedResourceMetadata) (string, error) {
	if metadata == nil {
		return "", nil
	}
	requested, err := parseLooseURL(serverURL)
	if err != nil {
		return "", err
	}
	configured, err := parseLooseURL(metadata.Resource)
	if err != nil {
		return "", err
	}
	if requested.Scheme+"://"+requested.Host != configured.Scheme+"://"+configured.Host {
		return "", fmt.Errorf("Protected resource %s does not match MCP server %s", metadata.Resource, requested.String())
	}
	requestedPath := requested.Path
	if !strings.HasSuffix(requestedPath, "/") {
		requestedPath += "/"
	}
	configuredPath := configured.Path
	if !strings.HasSuffix(configuredPath, "/") {
		configuredPath += "/"
	}
	if !strings.HasPrefix(requestedPath, configuredPath) {
		return "", fmt.Errorf("Protected resource %s does not match MCP server %s", metadata.Resource, requested.String())
	}
	return metadata.Resource, nil
}
