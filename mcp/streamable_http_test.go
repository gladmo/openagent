package mcp

// Ports of the SSE and Streamable HTTP cases from
// packages/mcp/test/streamable-http.test.ts, driven through an injectable
// McpFetch.

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gladmo/openagent/jsonx"

	"github.com/gladmo/openagent/abort"
)

func TestConsumeSseStreamParsesChunkedEvents(t *testing.T) {
	var events []SseEvent
	ids := []string{}
	retries := []int{}
	stream := strings.Join([]string{
		": comment\r",
		"event: message",
		"id: 1",
		"data: first",
		"data: line",
		"",
		"retry: 2500",
		"id: two",
		"data:{\"ok\":true}",
		"",
		"event: custom",
		"data: skipped",
		"",
		"",
	}, "\n")
	err := ConsumeSseStream(strings.NewReader(stream), ConsumeSseOptions{
		MaxEventBytes: 1024,
		OnEvent:       func(event SseEvent) { events = append(events, event) },
		OnID:          func(id string) { ids = append(ids, id) },
		OnRetry:       func(delayMs int) { retries = append(retries, delayMs) },
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Event != "message" || events[0].Data != "first\nline" || events[0].ID != "1" || !events[0].HasID {
		t.Fatalf("event 0 = %+v", events[0])
	}
	if events[1].Data != `{"ok":true}` || events[1].ID != "two" || events[1].HasEvent {
		t.Fatalf("event 1 = %+v", events[1])
	}
	// Events with another event type still dispatch at the SSE layer; the
	// transport filters them.
	if events[2].Event != "custom" || events[2].Data != "skipped" {
		t.Fatalf("event 2 = %+v", events[2])
	}
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "two" {
		t.Fatalf("ids = %v", ids)
	}
	if len(retries) != 1 || retries[0] != 2500 {
		t.Fatalf("retries = %v", retries)
	}
}

func TestConsumeSseStreamRejectsOversizedData(t *testing.T) {
	stream := "data: " + strings.Repeat("x", 200) + "\n"
	err := ConsumeSseStream(strings.NewReader(stream), ConsumeSseOptions{MaxEventBytes: 100})
	if err == nil || !strings.Contains(err.Error(), "MCP SSE event exceeds 100 bytes") {
		t.Fatalf("err = %v", err)
	}
	// Without a terminating blank line the buffered remainder trips the
	// same limit.
	err = ConsumeSseStream(strings.NewReader("data: "+strings.Repeat("y", 200)), ConsumeSseOptions{MaxEventBytes: 100})
	if err == nil {
		t.Fatal("expected overflow error")
	}
}

// fakeHTTPServer answers JSON POSTs like a streamable HTTP MCP server.
type fakeHTTPServer struct {
	mu        sync.Mutex
	requests  []*FetchRequest
	nextID    int
	sessionID string
	answers   map[string]func(request *FetchRequest) (*FetchResponse, error)
}

func (s *fakeHTTPServer) fetch(request *FetchRequest) (*FetchResponse, error) {
	s.mu.Lock()
	s.requests = append(s.requests, request)
	answer, has := s.answers[request.Method+":"+request.URL]
	if !has {
		answer, has = s.answers[request.Method]
	}
	s.nextID++
	s.mu.Unlock()
	if !has {
		return &FetchResponse{Status: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("not found"))}, nil
	}
	return answer(request)
}

func (s *fakeHTTPServer) setAnswer(method string, answer func(request *FetchRequest) (*FetchResponse, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.answers == nil {
		s.answers = map[string]func(*FetchRequest) (*FetchResponse, error){}
	}
	s.answers[method] = answer
}

func jsonResponse(status int, body string, headers map[string]string) *FetchResponse {
	header := http.Header{}
	for name, value := range headers {
		header.Set(name, value)
	}
	return &FetchResponse{Status: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func sseResponse(lines []string, headers map[string]string) *FetchResponse {
	header := http.Header{}
	header.Set("content-type", "text/event-stream")
	for name, value := range headers {
		header.Set(name, value)
	}
	return &FetchResponse{Status: 200, Header: header, Body: io.NopCloser(strings.NewReader(strings.Join(lines, "\n") + "\n\n"))}
}

func initializeAnswer(requestID JsonRpcID, sessionID string) *FetchResponse {
	result := jsonx.ObjFrom(
		"protocolVersion", LATEST_PROTOCOL_VERSION,
		"capabilities", jsonx.ObjFrom("tools", jsonx.NewObj()),
		"serverInfo", jsonx.ObjFrom("name", "http-fixture", "version", "1.0.0"),
	)
	return jsonResponse(200, jsonx.Stringify(NewSuccessResponse(requestID, result).Raw),
		map[string]string{"mcp-session-id": sessionID, "content-type": "application/json"})
}

func connectHTTPClient(t *testing.T, server *fakeHTTPServer) *McpClient {
	t.Helper()
	transport, err := NewStreamableHttpTransport(StreamableHttpTransportOptions{
		URL:   "https://example.test/mcp",
		Fetch: server.fetch,
	})
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	client := NewMcpClient(McpClientOptions{Name: "http-test", Version: "1.0.0"})
	if _, err := client.Connect(transport); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestStreamableHttpJSONAndSSEResponses(t *testing.T) {
	server := &fakeHTTPServer{}
	server.setAnswer("POST", func(request *FetchRequest) (*FetchResponse, error) {
		message, err := ParseJsonRpcText(request.Body)
		if err != nil {
			return nil, err
		}
		if message.Kind != KindRequest {
			return jsonResponse(202, "", nil), nil
		}
		switch message.Method() {
		case "initialize":
			return initializeAnswer(message.ID(), "session-1"), nil
		case "tools/list":
			return jsonResponse(200, jsonx.Stringify(jsonx.ObjFrom(
				"jsonrpc", "2.0",
				"id", message.ID().Value(),
				"result", jsonx.ObjFrom("tools", []any{
					jsonx.ObjFrom("name", "echo", "inputSchema", jsonx.ObjFrom("type", "object")),
				}),
			)), map[string]string{"content-type": "application/json"}), nil
		case "tools/call":
			return sseResponse([]string{
				`event: message`,
				`id: 7`,
				`data: {"jsonrpc":"2.0","id":` + message.ID().String() + `,"result":{"content":[{"type":"text","text":"sse"}]}}`,
			}, nil), nil
		}
		return jsonResponse(404, "no", nil), nil
	})
	client := connectHTTPClient(t, server)
	tools, err := client.ListTools(McpRequestOptions{})
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v %v", tools, err)
	}
	result, err := client.CallTool("echo", nil, McpRequestOptions{})
	if err != nil || len(result.Content) != 1 || result.Content[0].(TextContent).Text != "sse" {
		t.Fatalf("sse call = %+v %v", result, err)
	}
	if session := client.ConnectionState(); session != ClientConnected {
		t.Fatalf("state = %s", session)
	}
	// The session and protocol headers ride on every request after
	// initialize captured them.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		last := server.requests[len(server.requests)-1]
		server.mu.Unlock()
		found := false
		for _, header := range last.Headers {
			if header[0] == "Mcp-Session-Id" && header[1] == "session-1" {
				found = true
			}
		}
		if found {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session header not sent")
}

func TestStreamableHttpClassifiesAuthFailures(t *testing.T) {
	server := &fakeHTTPServer{}
	server.setAnswer("POST", func(request *FetchRequest) (*FetchResponse, error) {
		message, err := ParseJsonRpcText(request.Body)
		if err != nil {
			return nil, err
		}
		if message.Kind == KindRequest && message.Method() == "initialize" {
			return initializeAnswer(message.ID(), ""), nil
		}
		if message.Kind == KindRequest && message.Method() == "tools/call" {
			return jsonResponse(401, "sign in", map[string]string{
				"www-authenticate": `Bearer resource_metadata="https://auth.test/.well-known/oauth-protected-resource", error="invalid_token"`,
			}), nil
		}
		return jsonResponse(202, "", nil), nil
	})
	client := connectHTTPClient(t, server)
	_, err := client.CallTool("echo", nil, McpRequestOptions{})
	var authRequired *McpAuthRequiredError
	if err == nil {
		t.Fatal("401 accepted")
	}
	if !errors.As(err, &authRequired) {
		t.Fatalf("err = %v", err)
	}
	if authRequired.WWWAuthenticate == "" || !strings.Contains(authRequired.WWWAuthenticate, "resource_metadata") {
		t.Fatalf("www-authenticate = %q", authRequired.WWWAuthenticate)
	}
}

func TestStreamableHttpExpiredSession(t *testing.T) {
	server := &fakeHTTPServer{}
	server.setAnswer("POST", func(request *FetchRequest) (*FetchResponse, error) {
		message, err := ParseJsonRpcText(request.Body)
		if err != nil {
			return nil, err
		}
		if message.Kind == KindRequest && message.Method() == "initialize" {
			return initializeAnswer(message.ID(), "session-9"), nil
		}
		if message.Kind == KindRequest && message.Method() == "tools/call" {
			return jsonResponse(404, "unknown session", nil), nil
		}
		return jsonResponse(202, "", nil), nil
	})
	client := connectHTTPClient(t, server)
	_, err := client.CallTool("echo", nil, McpRequestOptions{})
	var expired *McpSessionExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamableHttpAcceptedWithoutResponse(t *testing.T) {
	server := &fakeHTTPServer{}
	server.setAnswer("POST", func(request *FetchRequest) (*FetchResponse, error) {
		message, err := ParseJsonRpcText(request.Body)
		if err != nil {
			return nil, err
		}
		if message.Kind == KindRequest && message.Method() == "initialize" {
			return initializeAnswer(message.ID(), ""), nil
		}
		if message.Kind == KindRequest {
			return jsonResponse(202, "", nil), nil
		}
		return jsonResponse(202, "", nil), nil
	})
	client := connectHTTPClient(t, server)
	_, err := client.CallTool("echo", nil, McpRequestOptions{})
	var httpErr *McpHttpError
	if !errors.As(err, &httpErr) || !strings.Contains(httpErr.Error(), "without a response") {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamableHttpIncludesBodyInErrors(t *testing.T) {
	server := &fakeHTTPServer{}
	server.setAnswer("POST", func(request *FetchRequest) (*FetchResponse, error) {
		message, err := ParseJsonRpcText(request.Body)
		if err != nil {
			return nil, err
		}
		if message.Kind == KindRequest && message.Method() == "initialize" {
			return initializeAnswer(message.ID(), ""), nil
		}
		if message.Kind != KindRequest {
			return jsonResponse(202, "", nil), nil
		}
		return jsonResponse(500, "kaboom "+strings.Repeat("x", 800), nil), nil
	})
	client := connectHTTPClient(t, server)
	_, err := client.CallTool("echo", nil, McpRequestOptions{})
	var httpErr *McpHttpError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(httpErr.Error(), "kaboom") || len(httpErr.Error()) > 600 {
		t.Fatalf("message = %q", httpErr.Error())
	}
}

func TestStreamableHttpAuthProviderRetry(t *testing.T) {
	server := &fakeHTTPServer{}
	requests := 0
	server.setAnswer("POST", func(request *FetchRequest) (*FetchResponse, error) {
		message, err := ParseJsonRpcText(request.Body)
		if err != nil {
			return nil, err
		}
		if message.Kind == KindRequest && message.Method() == "initialize" {
			return initializeAnswer(message.ID(), ""), nil
		}
		requests++
		if requests == 1 {
			return jsonResponse(403, "denied", map[string]string{
				"www-authenticate": `Bearer error="insufficient_scope", scope="read:extra"`,
			}), nil
		}
		return jsonResponse(200, jsonx.Stringify(NewSuccessResponse(message.ID(), jsonx.ObjFrom(
			"content", []any{jsonx.ObjFrom("type", "text", "text", "ok")},
		)).Raw), map[string]string{"content-type": "application/json"}), nil
	})
	transport, err := NewStreamableHttpTransport(StreamableHttpTransportOptions{
		URL:   "https://example.test/mcp",
		Fetch: server.fetch,
		AuthProvider: &AuthProvider{
			Token: func() (string, bool) { return "stale-token", true },
			OnUnauthorized: func(context *UnauthorizedContext) error {
				if !context.HasToken || context.Token != "stale-token" {
					t.Errorf("context token = %q", context.Token)
				}
				if context.ServerURL != "https://example.test/mcp" {
					t.Errorf("server url = %q", context.ServerURL)
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	client := NewMcpClient(McpClientOptions{Name: "http-test", Version: "1.0.0"})
	if _, err := client.Connect(transport); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()
	result, err := client.CallTool("echo", nil, McpRequestOptions{})
	if err != nil || len(result.Content) != 1 || result.Content[0].(TextContent).Text != "ok" {
		t.Fatalf("retry result = %+v %v", result, err)
	}
}

func TestStreamableHttpResponseStreamFailure(t *testing.T) {
	server := &fakeHTTPServer{}
	server.setAnswer("POST", func(request *FetchRequest) (*FetchResponse, error) {
		message, err := ParseJsonRpcText(request.Body)
		if err != nil {
			return nil, err
		}
		if message.Kind == KindRequest && message.Method() == "initialize" {
			return initializeAnswer(message.ID(), ""), nil
		}
		if message.Kind == KindRequest && message.Method() == "tools/call" {
			// The stream ends without the response and carries no event
			// IDs, so only this request fails.
			return sseResponse([]string{"data: not-json-rpc"}, nil), nil
		}
		return jsonResponse(202, "", nil), nil
	})
	client := connectHTTPClient(t, server)
	_, err := client.CallTool("echo", nil, McpRequestOptions{})
	var mcpErr *McpError
	if !errors.As(err, &mcpErr) || !strings.Contains(mcpErr.Message, "MCP response stream failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamableHttpGETStreamDeclined(t *testing.T) {
	server := &fakeHTTPServer{}
	server.setAnswer("GET", func(*FetchRequest) (*FetchResponse, error) {
		return jsonResponse(405, "", nil), nil
	})
	server.setAnswer("POST", func(request *FetchRequest) (*FetchResponse, error) {
		message, err := ParseJsonRpcText(request.Body)
		if err != nil {
			return nil, err
		}
		if message.Kind == KindRequest && message.Method() == "initialize" {
			return initializeAnswer(message.ID(), ""), nil
		}
		return jsonResponse(202, "", nil), nil
	})
	client := connectHTTPClient(t, server)
	// The GET stream is declined with 405; the client stays connected.
	time.Sleep(100 * time.Millisecond)
	if client.ConnectionState() != ClientConnected {
		t.Fatalf("state = %s", client.ConnectionState())
	}
}

// Port of the cancelled-send rule (da0eadf04): a request whose sender gave
// up during the authorization recovery must not be retried afterwards.
func TestStreamableHttpDoesNotRetryCancelledSendAfterRefresh(t *testing.T) {
	server := &fakeHTTPServer{}
	posts := 0
	var sendController *abort.Controller
	server.setAnswer("POST", func(request *FetchRequest) (*FetchResponse, error) {
		message, err := ParseJsonRpcText(request.Body)
		if err != nil {
			return nil, err
		}
		if message.Kind != KindRequest {
			return jsonResponse(202, "", nil), nil
		}
		switch message.Method() {
		case "initialize":
			return initializeAnswer(message.ID(), ""), nil
		case "boom":
			posts++
			if posts == 1 {
				return jsonResponse(401, `{"error":"unauthorized"}`, nil), nil
			}
			return jsonResponse(200, jsonx.Stringify(jsonx.ObjFrom(
				"jsonrpc", "2.0",
				"id", message.ID().Value(),
				"result", jsonx.NewObj(),
			)), map[string]string{"content-type": "application/json"}), nil
		}
		return jsonResponse(404, "no", nil), nil
	})
	transport, err := NewStreamableHttpTransport(StreamableHttpTransportOptions{
		URL:   "https://example.test/mcp",
		Fetch: server.fetch,
		AuthProvider: &AuthProvider{
			Token: func() (string, bool) { return "token", true },
			OnUnauthorized: func(*UnauthorizedContext) error {
				// The sender gives up while the recovery runs.
				sendController.Abort()
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	client := NewMcpClient(McpClientOptions{Name: "http-test", Version: "1.0.0"})
	if _, err := client.Connect(transport); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	sendController = abort.NewController()
	err = transport.Send(NewRequest(NewStringID("b1"), "boom", nil), &McpTransportSendOptions{Signal: sendController.Signal()})
	if _, isAbort := err.(*McpAbortError); !isAbort {
		t.Fatalf("err = %v", err)
	}
	if posts != 1 {
		t.Fatalf("dispatched %d times after cancellation", posts)
	}
}
