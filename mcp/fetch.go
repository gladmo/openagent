package mcp

// Port of packages/mcp/src/auth-provider.ts and the fetch surface the
// reference builds on (Request/Response): a minimal request/response pair
// so tests and OAuth flows can inject fetch behavior.

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

// McpFetch performs one HTTP request; implementations replace the platform
// fetch in tests and OAuth flows.
type McpFetch func(request *FetchRequest) (*FetchResponse, error)

// FetchRequest is one outgoing HTTP request.
type FetchRequest struct {
	Method  string
	URL     string
	Headers [][2]string
	Body    string
	Signal  *abort.Signal
}

// SetHeader replaces one header value.
func (r *FetchRequest) SetHeader(name, value string) {
	for index, header := range r.Headers {
		if strings.EqualFold(header[0], name) {
			r.Headers[index] = [2]string{name, value}
			return
		}
	}
	r.Headers = append(r.Headers, [2]string{name, value})
}

// FetchResponse is one HTTP response.
type FetchResponse struct {
	Status int
	Header http.Header
	Body   io.ReadCloser
}

// OK reports a 2xx status.
func (r *FetchResponse) OK() bool { return r.Status >= 200 && r.Status < 300 }

// HeaderGet reads one header, case-insensitively; "" when absent.
func (r *FetchResponse) HeaderGet(name string) string {
	if r.Header == nil {
		return ""
	}
	return r.Header.Get(name)
}

// ContentType returns the media type without parameters, lowercased; ""
// when the header is missing.
func (r *FetchResponse) ContentType() string {
	value := r.HeaderGet("content-type")
	if value == "" {
		return ""
	}
	mediaType := value
	if semicolon := strings.IndexByte(value, ';'); semicolon >= 0 {
		mediaType = value[:semicolon]
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

// Text reads the whole body as text and closes it.
func (r *FetchResponse) Text() (string, error) {
	if r.Body == nil {
		return "", nil
	}
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// JSON reads the whole body as one JSON value and closes it.
func (r *FetchResponse) JSON() (any, error) {
	text, err := r.Text()
	if err != nil {
		return nil, err
	}
	return jsonx.Parse(text)
}

// Discard closes the body, ignoring errors.
func (r *FetchResponse) Discard() {
	if r.Body != nil {
		_ = r.Body.Close()
	}
}

// BasicAuth encodes credentials for the Authorization header.
func BasicAuth(clientID, clientSecret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(clientID+":"+clientSecret))
}

// DefaultMcpFetch adapts net/http; network failures surface as *url.Error
// (the Go counterpart of the reference's TypeError).
func DefaultMcpFetch(request *FetchRequest) (*FetchResponse, error) {
	goRequest, err := http.NewRequest(request.Method, request.URL, strings.NewReader(request.Body))
	if err != nil {
		return nil, err
	}
	for _, header := range request.Headers {
		goRequest.Header.Set(header[0], header[1])
	}
	if request.Body != "" {
		goRequest.Header.Set("Content-Length", itoa(len(request.Body)))
	}
	goContext, cancel := abort.ToGoContext(context.Background(), request.Signal)
	defer cancel()
	response, err := http.DefaultClient.Do(goRequest.WithContext(goContext))
	if err != nil {
		return nil, err
	}
	return &FetchResponse{Status: response.StatusCode, Header: response.Header, Body: response.Body}, nil
}

// UnauthorizedContext carries the 401 response (or a 403 response whose
// challenge reports insufficient_scope) to the auth provider.
type UnauthorizedContext struct {
	Response  *FetchResponse
	ServerURL string
	Fetch     McpFetch
	// Token the rejected request carried, when any. A different current
	// token means another request already refreshed it.
	Token    string
	HasToken bool
}

// AuthProvider supplies bearer tokens to an MCP HTTP transport and may
// refresh them after a 401 response. OnUnauthorized nil means the provider
// cannot react to rejections.
type AuthProvider struct {
	Token          func() (string, bool)
	OnUnauthorized func(context *UnauthorizedContext) error
}
