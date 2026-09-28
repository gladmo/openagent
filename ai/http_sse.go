package ai

// http_sse.go is the Go stand-in for the OpenAI/Anthropic SDK HTTP+SSE
// transport the TS clients lean on: a fetch seam with abort/timeout support
// and an incremental server-sent-events reader.

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gladmo/openagent/abort"
)

// cancelOnCloseBody keeps the request context alive until the response body
// is closed (the fetch seam returns while the body still streams).
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// DefaultFetch performs a blocking HTTP round-trip with streaming body. It
// honors the abort signal (request and body reads) and an optional timeout
// covering the whole attempt including the streamed body. Non-2xx statuses
// are NOT errors here; callers decide (the SSE helpers convert them).
func DefaultFetch(req FetchRequest) (FetchResponse, error) {
	ctx := context.Background()
	var cancels []context.CancelFunc
	if req.Signal != nil {
		signalCtx, cancel := abort.ToGoContext(ctx, req.Signal)
		ctx = signalCtx
		cancels = append(cancels, cancel)
	}
	if req.TimeoutMs != nil && *req.TimeoutMs > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(*req.TimeoutMs*float64(time.Millisecond)))
		ctx = timeoutCtx
		cancels = append(cancels, cancel)
	}
	cancelAll := func() {
		for _, cancel := range cancels {
			cancel()
		}
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		cancelAll()
		return FetchResponse{}, err
	}
	for name, value := range req.Headers {
		httpReq.Header.Set(name, value)
	}

	if req.Signal != nil && req.Signal.Aborted() {
		cancelAll()
		return FetchResponse{}, abort.NewAbortError("Request aborted")
	}

	client := &http.Client{}
	response, err := client.Do(httpReq)
	if err != nil {
		cancelAll()
		if req.Signal != nil && req.Signal.Aborted() {
			return FetchResponse{}, abort.NewAbortError("Request aborted")
		}
		return FetchResponse{}, err
	}

	headers := map[string]string{}
	for name, values := range response.Header {
		if len(values) > 0 {
			headers[name] = values[0]
		}
	}
	return FetchResponse{
		Status:  response.StatusCode,
		Headers: headers,
		Body:    &cancelOnCloseBody{ReadCloser: response.Body, cancel: cancelAll},
	}, nil
}

// ResolveFetch returns the effective fetch function (options override or the
// default transport).
func ResolveFetch(options *StreamOptions) FetchFunction {
	if options != nil && options.Fetch != nil {
		return options.Fetch
	}
	return DefaultFetch
}

// transportError wraps fetch-layer failures (connection refused, DNS,
// reset, mid-body read errors) as a status-0 *ProviderHTTPError so the
// SDK-parity retry policy classifies them retryable — the role
// APIConnectionError plays in the pinned TS SDKs. Abort errors and already
// classified provider errors pass through untouched: cancellation is never
// retried.
func transportError(err error) error {
	if err == nil {
		return nil
	}
	if _, isAbort := err.(*abort.Error); isAbort {
		return err
	}
	if _, isHTTP := err.(*ProviderHTTPError); isHTTP {
		return err
	}
	return &ProviderHTTPError{Status: 0, Message: err.Error()}
}

// PostJSON sends a JSON POST through the fetch seam and validates the status.
// The response body is fully read; on non-2xx the error is a
// *ProviderHTTPError carrying status/headers/body.
func PostJSON(url string, headers map[string]string, payload []byte, options *StreamOptions) (*FetchResponseBuffered, error) {
	allHeaders := map[string]string{"Content-Type": "application/json"}
	for name, value := range headers {
		allHeaders[name] = value
	}
	request := FetchRequest{
		URL:     url,
		Method:  http.MethodPost,
		Headers: allHeaders,
		Body:    payload,
	}
	if options != nil {
		request.Signal = options.Signal
		request.TimeoutMs = options.TimeoutMs
	}
	response, err := ResolveFetch(options)(request)
	if err != nil {
		return nil, transportError(err)
	}
	defer func() {
		if closer, ok := response.Body.(io.Closer); ok {
			closer.Close()
		}
	}()
	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		if options != nil && options.Signal != nil && options.Signal.Aborted() {
			return nil, abort.NewAbortError("Request aborted")
		}
		return nil, transportError(readErr)
	}
	if response.Status < 200 || response.Status >= 300 {
		return nil, &ProviderHTTPError{
			Status:  response.Status,
			Headers: response.Headers,
			Body:    string(body),
		}
	}
	return &FetchResponseBuffered{Status: response.Status, Headers: response.Headers, Body: body}, nil
}

// FetchResponseBuffered is a fully-read fetch response.
type FetchResponseBuffered struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

// SSEStreamResponse carries an opened SSE stream plus response metadata.
type SSEStreamResponse struct {
	Status  int
	Headers map[string]string
	Stream  *SSEStream
}

// PostJSONStream sends a JSON POST and returns an SSE reader over the
// streaming body. Non-2xx statuses are read and returned as
// *ProviderHTTPError.
func PostJSONStream(url string, headers map[string]string, payload []byte, options *StreamOptions) (*SSEStreamResponse, error) {
	allHeaders := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "text/event-stream",
	}
	for name, value := range headers {
		allHeaders[name] = value
	}
	request := FetchRequest{
		URL:     url,
		Method:  http.MethodPost,
		Headers: allHeaders,
		Body:    payload,
	}
	if options != nil {
		request.Signal = options.Signal
		request.TimeoutMs = options.TimeoutMs
	}
	response, err := ResolveFetch(options)(request)
	if err != nil {
		return nil, transportError(err)
	}
	if response.Status < 200 || response.Status >= 300 {
		body, _ := io.ReadAll(response.Body)
		if closer, ok := response.Body.(io.Closer); ok {
			closer.Close()
		}
		return nil, &ProviderHTTPError{
			Status:  response.Status,
			Headers: response.Headers,
			Body:    string(body),
		}
	}
	return &SSEStreamResponse{Status: response.Status, Headers: response.Headers, Stream: NewSSEStream(response.Body)}, nil
}

// ---------------------------------------------------------------------------
// SSE reader
// ---------------------------------------------------------------------------

// SSEEvent is one dispatched server-sent event.
type SSEEvent struct {
	Event string
	Data  string
}

// SSEStream incrementally parses a text/event-stream body.
type SSEStream struct {
	reader  *bufio.Reader
	closer  io.Closer
	event   strings.Builder
	data    strings.Builder
	pending bool
	closed  bool
}

// NewSSEStream wraps a response body.
func NewSSEStream(body io.Reader) *SSEStream {
	stream := &SSEStream{reader: bufio.NewReaderSize(body, 64*1024)}
	if closer, ok := body.(io.Closer); ok {
		stream.closer = closer
	}
	return stream
}

// Close releases the underlying body (and the request context).
func (s *SSEStream) Close() error {
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

// Next returns the next dispatched event. io.EOF marks the end of stream.
func (s *SSEStream) Next() (SSEEvent, error) {
	for {
		if s.closed {
			return SSEEvent{}, io.EOF
		}
		line, err := s.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return SSEEvent{}, err
		}
		atEOF := err == io.EOF
		trimmed := strings.TrimRight(line, "\r\n")

		if trimmed == "" {
			// Empty line dispatches the pending event.
			if atEOF && !s.pending {
				return SSEEvent{}, io.EOF
			}
			if s.pending {
				event := SSEEvent{Event: s.event.String(), Data: s.data.String()}
				s.event.Reset()
				s.data.Reset()
				s.pending = false
				return event, nil
			}
			if atEOF {
				return SSEEvent{}, io.EOF
			}
			continue
		}

		if strings.HasPrefix(trimmed, ":") {
			// Comment/keep-alive.
			if atEOF {
				return SSEEvent{}, io.EOF
			}
			continue
		}

		name, value := trimmed, ""
		if idx := strings.Index(trimmed, ":"); idx >= 0 {
			name = trimmed[:idx]
			value = strings.TrimPrefix(trimmed[idx+1:], " ")
		}
		switch name {
		case "event":
			s.event.WriteString(value)
			s.pending = true
		case "data":
			if s.data.Len() > 0 {
				s.data.WriteString("\n")
			}
			s.data.WriteString(value)
			s.pending = true
		}
		if atEOF {
			s.closed = true
			if s.pending {
				// Dispatch remaining buffered event at EOF.
				event := SSEEvent{Event: s.event.String(), Data: s.data.String()}
				s.event.Reset()
				s.data.Reset()
				s.pending = false
				return event, nil
			}
			return SSEEvent{}, io.EOF
		}
	}
}

// SSEDone is the OpenAI-style stream terminator sentinel.
const SSEDone = "[DONE]"
