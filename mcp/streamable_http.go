package mcp

// Port of packages/mcp/src/transports/streamable-http.ts: the Streamable
// HTTP transport with session handling, SSE response streams, GET stream
// reconnection with backoff, and auth-provider retry.

import (
	"errors"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

const (
	maxErrorBodyBytes        = 8 * 1024
	errorMessageBodyChars    = 500
	defaultReconnectInitial  = 1 * time.Second
	defaultReconnectMax      = 30 * time.Second
	defaultReconnectMaxCount = 5
)

// StreamableHttpReconnectOptions tunes reconnection of dropped SSE
// streams (the GET stream, and response streams that carry event IDs).
type StreamableHttpReconnectOptions struct {
	// InitialDelay is the delay before the first reconnection attempt,
	// unless the server sent a retry field. Default 1s.
	InitialDelay time.Duration
	// MaxDelay is the upper bound for the exponential backoff. Default 30s.
	MaxDelay time.Duration
	// MaxRetries is the number of consecutive failed attempts before
	// giving up on a stream. Default 5.
	MaxRetries int
}

// StreamableHttpTransportOptions configures the transport.
type StreamableHttpTransportOptions struct {
	URL     string
	Headers [][2]string
	Fetch   McpFetch
	// OpenGetStream opens the server-to-client GET stream after
	// initialization. Default true.
	OpenGetStream   *bool
	MaxMessageBytes int
	AuthProvider    *AuthProvider
	Reconnect       *StreamableHttpReconnectOptions
}

// McpHttpError reports a non-2xx HTTP answer.
type McpHttpError struct {
	Status  int
	Body    string
	message string
}

func (e *McpHttpError) Error() string { return e.message }

// NewMcpHttpError builds the error with a rendered message.
func NewMcpHttpError(status int, message, body string) *McpHttpError {
	return &McpHttpError{Status: status, Body: body, message: message}
}

// McpAuthRequiredError reports that the server requires authentication.
type McpAuthRequiredError struct {
	McpHttpError
	WWWAuthenticate string // "" when the header is absent
}

// NewMcpAuthRequiredError builds the 401 error.
func NewMcpAuthRequiredError(wwwAuthenticate, body string) *McpAuthRequiredError {
	return &McpAuthRequiredError{
		McpHttpError:    McpHttpError{Status: 401, Body: body, message: "MCP server requires authentication"},
		WWWAuthenticate: wwwAuthenticate,
	}
}

// McpSessionExpiredError reports a 404 on an established session.
type McpSessionExpiredError struct{ McpHttpError }

// NewMcpSessionExpiredError builds the 404 error.
func NewMcpSessionExpiredError(body string) *McpSessionExpiredError {
	return &McpSessionExpiredError{McpHttpError{
		Status:  404,
		Body:    body,
		message: "MCP session expired",
	}}
}

var insufficientScopePattern = regexp.MustCompile(`(?:^|[\s,])error="?insufficient_scope"?`)

// needsAuthorization: 401, or 403 with an insufficient_scope bearer
// challenge (step-up authorization).
func needsAuthorization(response *FetchResponse) bool {
	if response.Status == 401 {
		return true
	}
	if response.Status != 403 {
		return false
	}
	return insufficientScopePattern.MatchString(response.HeaderGet("www-authenticate"))
}

// isTransientStatus: statuses worth retrying when a stream fails to
// (re)open.
func isTransientStatus(status int) bool {
	return status == 408 || status == 429 || status >= 500
}

func describeHttpFailure(status int, body string) string {
	text := strings.TrimSpace(body)
	snippet := text
	if len(text) > errorMessageBodyChars {
		snippet = text[:errorMessageBodyChars-3] + "..."
	}
	if snippet == "" {
		return "MCP HTTP request failed with status " + itoa(status)
	}
	return "MCP HTTP request failed with status " + itoa(status) + ": " + snippet
}

// streamCursor tracks resumption position of one SSE stream.
type streamCursor struct {
	lastEventID string
	hasLastID   bool
	retryMs     int
	hasRetry    bool
	// received reports whether the stream delivered any event since it was
	// (re)opened.
	received bool
}

// StreamableHttpTransport speaks the MCP Streamable HTTP protocol.
type StreamableHttpTransport struct {
	TransportEvents
	url     string
	options StreamableHttpTransportOptions
	fetch   McpFetch

	mu               sync.Mutex
	controller       *abort.Controller
	started          bool
	closed           bool
	sessionID        string
	protocolVersion  string
	getStreamStarted bool
	// lastToken is the token cached by the last headers() call; close()
	// deletes the session with it instead of asking the provider, whose
	// token() may refresh over the network during shutdown.
	lastToken      string
	lastTokenValid bool
}

// NewStreamableHttpTransport builds the transport.
func NewStreamableHttpTransport(options StreamableHttpTransportOptions) (*StreamableHttpTransport, error) {
	if _, err := url.ParseRequestURI(options.URL); err != nil {
		return nil, err
	}
	headers := append([][2]string(nil), options.Headers...)
	fetch := options.Fetch
	if fetch == nil {
		fetch = DefaultMcpFetch
	}
	return &StreamableHttpTransport{
		url:        options.URL,
		options:    StreamableHttpTransportOptions{URL: options.URL, Headers: headers, Fetch: options.Fetch, OpenGetStream: options.OpenGetStream, MaxMessageBytes: options.MaxMessageBytes, AuthProvider: options.AuthProvider, Reconnect: options.Reconnect},
		fetch:      fetch,
		controller: abort.NewController(),
	}, nil
}

// SessionID returns the server-assigned session id, or "".
func (t *StreamableHttpTransport) SessionID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessionID
}

// Start implements McpTransport.
func (t *StreamableHttpTransport) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return errors.New("MCP Streamable HTTP transport already started")
	}
	if t.closed {
		return NewMcpConnectionClosedError("")
	}
	t.started = true
	return nil
}

// SetProtocolVersion implements McpTransport.
func (t *StreamableHttpTransport) SetProtocolVersion(version string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.protocolVersion = version
}

// Send implements McpTransport.
func (t *StreamableHttpTransport) Send(message *JsonRpcMessage, sendOptions *McpTransportSendOptions) error {
	t.mu.Lock()
	started, closed := t.started, t.closed
	t.mu.Unlock()
	if !started || closed {
		return NewMcpConnectionClosedError("")
	}
	var sendSignal *abort.Signal
	if sendOptions != nil {
		sendSignal = sendOptions.Signal
	}
	response, err := t.authorizedFetch("POST", [][2]string{
		{"accept", "application/json, text/event-stream"},
		{"content-type", "application/json"},
	}, jsonx.Stringify(message.Raw), sendSignal)
	if err != nil {
		return err
	}
	if err := t.checkResponse(response); err != nil {
		return err
	}
	t.captureSession(response)

	if message.Kind != KindRequest {
		// Notifications and responses are acknowledged with 202 and carry
		// no reply; ignore any body.
		response.Discard()
		// The server-to-client stream may only open once the session is
		// initialized.
		if message.Kind == KindNotification && message.Method() == "notifications/initialized" {
			t.startGetStream()
		}
		return nil
	}
	if response.Status == 202 || response.Status == 204 {
		response.Discard()
		return NewMcpHttpError(response.Status,
			"MCP server accepted request "+message.Method()+" without a response", "")
	}
	mediaType := response.ContentType()
	if mediaType == "application/json" {
		body, err := response.JSON()
		if err != nil {
			return err
		}
		items := []any{body}
		if list, ok := body.([]any); ok {
			items = list
		}
		for _, item := range items {
			parsed, err := ParseJsonRpcMessage(item)
			if err != nil {
				return err
			}
			t.EmitMessage(parsed)
		}
		return nil
	}
	if mediaType == "text/event-stream" && response.Body != nil {
		requestID := message.ID()
		go func() {
			_ = t.consumeResponseStream(response.Body, requestID)
		}()
		return nil
	}
	mediaTypeName := mediaType
	if mediaTypeName == "" {
		mediaTypeName = "missing"
	}
	response.Discard()
	return NewMcpHttpError(response.Status, "Unsupported MCP response content type: "+mediaTypeName, "")
}

// Close implements McpTransport.
func (t *StreamableHttpTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	started, sessionID := t.started, t.sessionID
	t.mu.Unlock()
	t.controller.Abort()
	if started && sessionID != "" {
		t.mu.Lock()
		token, hasToken := t.lastToken, t.lastTokenValid
		protocolVersion := t.protocolVersion
		t.mu.Unlock()
		// The DELETE itself is bounded (the reference aborts it after 1s)
		// so the goroutine cannot outlive Close on a dead server.
		deleteController := abort.NewController()
		deleteTimer := time.AfterFunc(time.Second, deleteController.Abort)
		defer deleteTimer.Stop()
		done := make(chan struct{})
		go func() {
			defer close(done)
			headers := append([][2]string(nil), t.options.Headers...)
			headers = append(headers, [2]string{"Mcp-Session-Id", sessionID})
			if protocolVersion != "" {
				headers = append(headers, [2]string{"MCP-Protocol-Version", protocolVersion})
			}
			// DELETE with the cached token; the provider's token() may
			// refresh over the network during shutdown.
			if hasToken && token != "" {
				headers = append(headers, [2]string{"Authorization", "Bearer " + token})
			}
			request := &FetchRequest{Method: "DELETE", URL: t.url, Headers: headers, Signal: deleteController.Signal()}
			if response, err := t.fetch(request); err == nil {
				response.Discard()
			}
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
	t.EmitClose()
	return nil
}

// authorizedFetch fetches with auth headers. A 401 (or a 403 asking for
// more scope) is handed to the auth provider once, and the request is
// retried with whatever credentials it left behind.
// authorizedFetch fetches with auth headers. A 401 (or a 403 asking for
// more scope) is handed to the auth provider once, and the request is
// retried with whatever credentials it left behind. sendSignal only stops
// dispatch: a request its sender gave up on must not reach the server; it
// does not abort the shared recovery, which other requests may wait for.
func (t *StreamableHttpTransport) authorizedFetch(method string, extra [][2]string, body string, sendSignal *abort.Signal) (*FetchResponse, error) {
	var onUnauthorized func(*UnauthorizedContext) error
	if t.options.AuthProvider != nil {
		onUnauthorized = t.options.AuthProvider.OnUnauthorized
	}
	for attempt := 0; ; attempt++ {
		headers, token, hasToken, err := t.headers(extra)
		if err != nil {
			return nil, err
		}
		if sendSignal != nil && sendSignal.Aborted() {
			return nil, &McpAbortError{}
		}
		request := &FetchRequest{Method: method, URL: t.url, Headers: headers, Body: body, Signal: t.controller.Signal()}
		response, err := t.fetch(request)
		if err != nil {
			return nil, err
		}
		if attempt > 0 || onUnauthorized == nil || !needsAuthorization(response) {
			return response, nil
		}
		context := &UnauthorizedContext{Response: response, ServerURL: t.url, Fetch: t.fetch, Token: token, HasToken: hasToken}
		err = onUnauthorized(context)
		response.Discard()
		if err != nil {
			return nil, err
		}
		if sendSignal != nil && sendSignal.Aborted() {
			return nil, &McpAbortError{}
		}
	}
}

func (t *StreamableHttpTransport) headers(extra [][2]string) ([][2]string, string, bool, error) {
	t.mu.Lock()
	sessionID, protocolVersion := t.sessionID, t.protocolVersion
	t.mu.Unlock()
	headers := append([][2]string(nil), t.options.Headers...)
	if extra != nil {
		headers = append(headers, extra...)
	}
	if sessionID != "" {
		headers = append(headers, [2]string{"Mcp-Session-Id", sessionID})
	}
	if protocolVersion != "" {
		headers = append(headers, [2]string{"MCP-Protocol-Version", protocolVersion})
	}
	token, hasToken := "", false
	if provider := t.options.AuthProvider; provider != nil && provider.Token != nil {
		token, hasToken = provider.Token()
		t.mu.Lock()
		t.lastToken, t.lastTokenValid = token, hasToken
		t.mu.Unlock()
	}
	if hasToken && token != "" {
		headers = append(headers, [2]string{"Authorization", "Bearer " + token})
	}
	return headers, token, hasToken, nil
}

func (t *StreamableHttpTransport) captureSession(response *FetchResponse) {
	if sessionID := response.HeaderGet("mcp-session-id"); sessionID != "" {
		t.mu.Lock()
		t.sessionID = sessionID
		t.mu.Unlock()
	}
}

func (t *StreamableHttpTransport) checkResponse(response *FetchResponse) error {
	if response.OK() {
		return nil
	}
	text, _ := response.Text()
	if len(text) > maxErrorBodyBytes {
		text = text[:maxErrorBodyBytes]
	}
	if response.Status == 401 {
		return NewMcpAuthRequiredError(response.HeaderGet("www-authenticate"), text)
	}
	t.mu.Lock()
	sessionID := t.sessionID
	t.mu.Unlock()
	if response.Status == 404 && sessionID != "" {
		return NewMcpSessionExpiredError(text)
	}
	return NewMcpHttpError(response.Status, describeHttpFailure(response.Status, text), text)
}

func (t *StreamableHttpTransport) consumeSse(stream io.Reader, cursor *streamCursor, onMessage func(*JsonRpcMessage)) error {
	return ConsumeSseStream(stream, ConsumeSseOptions{
		MaxEventBytes: t.maxMessageBytes(),
		OnID:          func(id string) { cursor.lastEventID, cursor.hasLastID = id, true },
		OnRetry:       func(delayMs int) { cursor.retryMs, cursor.hasRetry = delayMs, true },
		OnEvent: func(event SseEvent) {
			cursor.received = true
			// Events without data prime resumption; other event types are
			// not JSON-RPC.
			if strings.TrimSpace(event.Data) == "" || (event.HasEvent && event.Event != "message") {
				return
			}
			value, err := jsonx.Parse(event.Data)
			if err != nil {
				t.EmitError(toError(err))
				return
			}
			message, err := ParseJsonRpcMessage(value)
			if err != nil {
				t.EmitError(err)
				return
			}
			if onMessage != nil {
				onMessage(message)
			}
			t.EmitMessage(message)
		},
	})
}

// consumeResponseStream reads the SSE stream answering one request. When
// the stream ends or breaks before the response arrives and the server
// assigned event IDs, resume it with GET and Last-Event-ID, as the server
// may close response streams at will. Otherwise only this request fails.
func (t *StreamableHttpTransport) consumeResponseStream(body io.Reader, requestID JsonRpcID) error {
	cursor := &streamCursor{}
	answered := false
	onMessage := func(message *JsonRpcMessage) {
		if message.Kind == KindSuccessResponse || message.Kind == KindErrorResponse {
			if message.ID().Equal(requestID) {
				answered = true
			}
		}
	}
	var stream io.Reader = body
	var failure error
	for attempt := 0; ; {
		if stream != nil {
			if err := t.consumeSse(stream, cursor, onMessage); err != nil {
				failure = err
			} else {
				failure = nil
			}
		}
		if answered || t.isTransportClosed() {
			return nil
		}
		if failure != nil && !t.isRetryable(failure) {
			break
		}
		if !cursor.hasLastID || attempt >= t.maxRetries() {
			break
		}
		if cursor.received {
			attempt = 0
		}
		cursor.received = false
		if !t.sleepFor(t.reconnectDelay(attempt, cursor)) {
			return nil
		}
		attempt++
		opened, err := t.openSseStream(cursor.lastEventID)
		if err != nil {
			failure = err
			if !t.isRetryable(err) {
				break
			}
			stream = nil
			continue
		}
		stream = opened
	}
	if t.isTransportClosed() {
		return nil
	}
	reason := "stream ended without a response"
	if failure != nil {
		reason = failure.Error()
	}
	t.EmitMessage(NewErrorResponse(
		requestID,
		JSON_RPC_ERROR_CODES.InternalError,
		"MCP response stream failed: "+reason,
		nil,
	))
	return nil
}

func (t *StreamableHttpTransport) startGetStream() {
	openGetStream := true
	if t.options.OpenGetStream != nil {
		openGetStream = *t.options.OpenGetStream
	}
	t.mu.Lock()
	if !openGetStream || t.getStreamStarted || t.closed {
		t.mu.Unlock()
		return
	}
	t.getStreamStarted = true
	t.mu.Unlock()
	go func() { _ = t.runGetStream() }()
}

// runGetStream keeps the server-to-client stream open, reconnecting with
// backoff when it drops.
func (t *StreamableHttpTransport) runGetStream() error {
	cursor := &streamCursor{}
	for attempt := 0; !t.isTransportClosed(); {
		stream, err := t.openSseStream(cursor.lastEventID)
		if err == nil && stream == nil {
			// The server does not offer a GET stream.
			return nil
		}
		openedAt := time.Now()
		if err == nil {
			if consumeErr := t.consumeSse(stream, cursor, nil); consumeErr != nil {
				err = consumeErr
			} else {
				// A stream that stayed up for a while counts as healthy,
				// even if it was idle.
				if cursor.received || time.Since(openedAt) > t.maxDelay() {
					attempt = 0
				}
			}
		}
		if t.isTransportClosed() {
			return nil
		}
		if err != nil && !t.isRetryable(err) {
			t.EmitError(err)
			return nil
		}
		cursor.received = false
		if attempt >= t.maxRetries() {
			t.EmitError(errors.New("MCP server-to-client stream dropped and could not be reopened"))
			return nil
		}
		ok := t.sleepFor(t.reconnectDelay(attempt, cursor))
		attempt++
		if !ok {
			return nil
		}
	}
	return nil
}

// openSseStream opens a GET SSE stream; a nil stream means the server
// answered 405 (no GET stream).
func (t *StreamableHttpTransport) openSseStream(lastEventID string) (io.Reader, error) {
	headers := [][2]string{{"accept", "text/event-stream"}}
	if lastEventID != "" {
		headers = append(headers, [2]string{"last-event-id", lastEventID})
	}
	response, err := t.authorizedFetch("GET", headers, "", nil)
	if err != nil {
		return nil, err
	}
	if response.Status == 405 {
		response.Discard()
		return nil, nil
	}
	if err := t.checkResponse(response); err != nil {
		return nil, err
	}
	t.captureSession(response)
	mediaType := response.ContentType()
	if mediaType != "text/event-stream" || response.Body == nil {
		mediaTypeName := mediaType
		if mediaTypeName == "" {
			mediaTypeName = "missing"
		}
		response.Discard()
		return nil, NewMcpHttpError(response.Status, "Unsupported MCP GET response content type: "+mediaTypeName, "")
	}
	return response.Body, nil
}

// isRetryable: network failures and transient statuses are retried; auth,
// session, and protocol errors are not. Network failures map to the
// reference's TypeError (fetch reports them, including a connection
// dropped mid-body).
func (t *StreamableHttpTransport) isRetryable(err error) bool {
	var httpErr *McpHttpError
	if errors.As(err, &httpErr) {
		return isTransientStatus(httpErr.Status)
	}
	return isNetworkError(err)
}

func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errors.Is(errno, syscall.ECONNRESET) || errors.Is(errno, syscall.ECONNREFUSED) ||
			errors.Is(errno, syscall.ECONNABORTED) || errors.Is(errno, syscall.EPIPE) ||
			errors.Is(errno, syscall.ETIMEDOUT) || errors.Is(errno, syscall.EHOSTUNREACH) ||
			errors.Is(errno, syscall.ENETUNREACH)
	}
	return false
}

// IsNetworkError reports Go network failures, the counterpart of the
// reference's TypeError classification.
func IsNetworkError(err error) bool { return isNetworkError(err) }

func (t *StreamableHttpTransport) reconnectDelay(attempt int, cursor *streamCursor) time.Duration {
	if cursor.hasRetry {
		return time.Duration(cursor.retryMs) * time.Millisecond
	}
	initial := defaultReconnectInitial
	if t.options.Reconnect != nil && t.options.Reconnect.InitialDelay > 0 {
		initial = t.options.Reconnect.InitialDelay
	}
	delay := initial
	for scale := 0; scale < attempt; scale++ {
		delay *= 2
		if delay > t.maxDelay() {
			return t.maxDelay()
		}
	}
	if delay > t.maxDelay() {
		return t.maxDelay()
	}
	return delay
}

func (t *StreamableHttpTransport) maxDelay() time.Duration {
	if t.options.Reconnect != nil && t.options.Reconnect.MaxDelay > 0 {
		return t.options.Reconnect.MaxDelay
	}
	return defaultReconnectMax
}

func (t *StreamableHttpTransport) maxRetries() int {
	if t.options.Reconnect != nil && t.options.Reconnect.MaxRetries > 0 {
		return t.options.Reconnect.MaxRetries
	}
	return defaultReconnectMaxCount
}

func (t *StreamableHttpTransport) maxMessageBytes() int {
	if t.options.MaxMessageBytes > 0 {
		return t.options.MaxMessageBytes
	}
	return DefaultMaxMessageBytes
}

func (t *StreamableHttpTransport) isTransportClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// sleepFor waits for the duration; it reports false when the transport
// closed while waiting.
func (t *StreamableHttpTransport) sleepFor(duration time.Duration) bool {
	signal := t.controller.Signal()
	if signal.Aborted() {
		return false
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-signal.Done():
		return false
	case <-timer.C:
		return true
	}
}
