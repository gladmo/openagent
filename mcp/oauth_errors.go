package mcp

// Port of packages/mcp/src/oauth/errors.ts.

import "fmt"

// OAuthError is an OAuth protocol error answer (RFC 6749 section 5.2).
type OAuthError struct {
	Code     string
	Message  string
	ErrorURI string
}

func (e *OAuthError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Message
}

// NewOAuthError builds the error; message falls back to the code.
func NewOAuthError(code, message, errorURI string) *OAuthError {
	return &OAuthError{Code: code, Message: message, ErrorURI: errorURI}
}

// OAuthIssuerMismatchError reports an issuer mismatch between the
// authorization server and an authorization response (RFC 9207).
type OAuthIssuerMismatchError struct {
	Expected string
	// Received is empty when an authorization response lacks the iss
	// parameter its server promised.
	Received    string
	hasReceived bool
}

func (e *OAuthIssuerMismatchError) Error() string {
	received := "none"
	if e.hasReceived {
		received = quoteJSON(e.Received)
	}
	return fmt.Sprintf("OAuth issuer mismatch: expected %s, received %s", quoteJSON(e.Expected), received)
}

// NewOAuthIssuerMismatchError builds the error; received absent when
// hasReceived is false.
func NewOAuthIssuerMismatchError(expected string, received string, hasReceived bool) *OAuthIssuerMismatchError {
	return &OAuthIssuerMismatchError{Expected: expected, Received: received, hasReceived: hasReceived}
}

// OAuthInsecureEndpointError refuses to send credentials over plain HTTP.
type OAuthInsecureEndpointError struct{ Endpoint string }

func (e *OAuthInsecureEndpointError) Error() string {
	return "Refusing to send OAuth credentials to non-HTTPS endpoint " + e.Endpoint
}

// OAuthRegistrationError reports a failed dynamic client registration.
type OAuthRegistrationError struct {
	Status int
	Body   string
}

func (e *OAuthRegistrationError) Error() string {
	return fmt.Sprintf("OAuth dynamic client registration failed with status %d: %s", e.Status, e.Body)
}

// McpOAuthAuthorizationRequiredError reports that OAuth authorization
// requires user interaction.
type McpOAuthAuthorizationRequiredError struct{}

func (e *McpOAuthAuthorizationRequiredError) Error() string {
	return "MCP OAuth authorization requires user interaction"
}

// quoteJSON renders a Go string the way JSON.stringify does in the
// reference messages.
func quoteJSON(value string) string {
	return stringifyValue(value)
}
