package mcp

// Port of packages/mcp/test/client.test.ts: McpClient behavior over an
// in-memory transport pair.

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

// testServer answers requests like the reference's vitest helper.
type testServer struct {
	transport *InMemoryTransport
	mu        sync.Mutex
	messages  []*JsonRpcMessage
	handlers  map[string]func(request *JsonRpcMessage) (any, error)
}

func (s *testServer) record(message *JsonRpcMessage) {
	s.mu.Lock()
	s.messages = append(s.messages, message)
	s.mu.Unlock()
}

func (s *testServer) sentMessages() []*JsonRpcMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*JsonRpcMessage(nil), s.messages...)
}

func (s *testServer) setHandler(method string, handler func(request *JsonRpcMessage) (any, error)) {
	s.mu.Lock()
	s.handlers[method] = handler
	s.mu.Unlock()
}

func (s *testServer) respond(request *JsonRpcMessage) {
	s.mu.Lock()
	handler := s.handlers[request.Method()]
	s.mu.Unlock()
	if handler == nil {
		_ = s.transport.Send(NewErrorResponse(request.ID(), JSON_RPC_ERROR_CODES.MethodNotFound,
			"Method not found: "+request.Method(), nil))
		return
	}
	result, err := handler(request)
	if err != nil {
		mcpError, ok := err.(*McpError)
		if !ok {
			mcpError = NewMcpError(JSON_RPC_ERROR_CODES.InternalError, err.Error(), nil)
		}
		_ = s.transport.Send(NewErrorResponse(request.ID(), mcpError.Code, mcpError.Message, mcpError.Data))
		return
	}
	if result == nil {
		result = jsonx.NewObj()
	}
	_ = s.transport.Send(NewSuccessResponse(request.ID(), result))
}

func createTestServer(t *testing.T) (*InMemoryTransport, *testServer) {
	t.Helper()
	clientTransport, serverTransport := CreateInMemoryTransportPair()
	server := &testServer{transport: serverTransport, handlers: map[string]func(*JsonRpcMessage) (any, error){}}
	serverTransport.OnMessage(func(message *JsonRpcMessage) {
		server.record(message)
		if message.Kind != KindRequest {
			return
		}
		go server.respond(message)
	})
	if err := serverTransport.Start(); err != nil {
		t.Fatalf("start server transport: %v", err)
	}
	server.setHandler("initialize", func(*JsonRpcMessage) (any, error) {
		return mustParse(t, `{
			"protocolVersion": "`+LATEST_PROTOCOL_VERSION+`",
			"capabilities": { "tools": { "listChanged": true } },
			"serverInfo": { "name": "test-server", "version": "1.0.0" },
			"instructions": "Use test tools."
		}`), nil
	})
	return clientTransport, server
}

func connectTestClient(t *testing.T) (*McpClient, *testServer) {
	t.Helper()
	clientTransport, server := createTestServer(t)
	client := NewMcpClient(McpClientOptions{Name: "test-client", Version: "2.0.0"})
	if _, err := client.Connect(clientTransport); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, server
}

func requireEqualJSON(t *testing.T, actual any, expected string) {
	t.Helper()
	wanted := mustParse(t, expected)
	if !jsonx.Equal(actual, wanted) {
		t.Fatalf("mismatch:\n got: %s\nwant: %s", jsonx.Stringify(actual), jsonx.Stringify(wanted))
	}
}

// waitUntil polls until the condition holds; in-memory delivery runs on
// its own goroutine like the reference's microtasks.
func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestClientInitializesBeforeExposingServerInfo(t *testing.T) {
	client, server := connectTestClient(t)
	if client.ConnectionState() != ClientConnected {
		t.Fatalf("state = %s", client.ConnectionState())
	}
	if version := client.ProtocolVersion(); version == nil || *version != LATEST_PROTOCOL_VERSION {
		t.Fatal("protocol version not negotiated")
	}
	info := client.ServerInfo()
	if info == nil || info.Name != "test-server" || info.Version != "1.0.0" {
		t.Fatalf("server info = %+v", info)
	}
	capabilities := client.ServerCapabilities()
	if capabilities == nil || capabilities.Tools == nil || !capabilities.Tools.ListChanged {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	if instructions := client.Instructions(); instructions == nil || *instructions != "Use test tools." {
		t.Fatal("instructions missing")
	}
	waitUntil(t, func() bool { return len(server.sentMessages()) >= 2 })
	messages := server.sentMessages()
	if len(messages) != 2 {
		t.Fatalf("messages = %d", len(messages))
	}
	requireEqualJSON(t, messages[0].Raw, `{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "initialize",
		"params": {
			"protocolVersion": "`+LATEST_PROTOCOL_VERSION+`",
			"capabilities": {},
			"clientInfo": { "name": "test-client", "version": "2.0.0" }
		}
	}`)
	requireEqualJSON(t, messages[1].Raw, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

func TestClientPaginatesTools(t *testing.T) {
	client, server := connectTestClient(t)
	server.setHandler("tools/list", func(request *JsonRpcMessage) (any, error) {
		params := request.ParamsObj()
		if params == nil {
			return mustParse(t, `{
				"tools": [{ "name": "search", "description": "Search", "inputSchema": { "type": "object" } }],
				"nextCursor": "page-2"
			}`), nil
		}
		if !params.Has("cursor") {
			return mustParse(t, `{
				"tools": [{ "name": "search", "description": "Search", "inputSchema": { "type": "object" } }],
				"nextCursor": "page-2"
			}`), nil
		}
		// Some servers end pagination with an empty cursor instead of
		// omitting it.
		return mustParse(t, `{
			"tools": [{ "name": "read", "inputSchema": { "type": "object" }, "outputSchema": { "type": "object" }, "annotations": { "readOnlyHint": true } }],
			"nextCursor": ""
		}`), nil
	})
	tools, err := client.ListTools(McpRequestOptions{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "search" || tools[1].Name != "read" {
		t.Fatalf("tools = %+v", tools)
	}
	if tools[1].Annotations == nil || tools[1].Annotations.ReadOnlyHint == nil || !*tools[1].Annotations.ReadOnlyHint {
		t.Fatalf("annotations = %+v", tools[1].Annotations)
	}
	if tools[1].OutputSchema == nil {
		t.Fatal("output schema dropped")
	}
}

func TestClientListsAndReadsResources(t *testing.T) {
	client, server := connectTestClient(t)
	server.setHandler("resources/list", func(request *JsonRpcMessage) (any, error) {
		params := request.ParamsObj()
		if params == nil || !params.Has("cursor") {
			return mustParse(t, `{ "resources": [{ "uri": "file:///a", "name": "a", "mimeType": "text/plain" }], "nextCursor": "2" }`), nil
		}
		return mustParse(t, `{ "resources": [{ "uri": "file:///b" }] }`), nil
	})
	server.setHandler("resources/templates/list", func(*JsonRpcMessage) (any, error) {
		return mustParse(t, `{ "resourceTemplates": [{ "uriTemplate": "repo://{owner}/{repo}", "name": "repo" }] }`), nil
	})
	server.setHandler("resources/read", func(request *JsonRpcMessage) (any, error) {
		uri, _ := request.ParamsObj().MustGet("uri").(string)
		return mustParse(t, `{ "contents": [{ "uri": "`+uri+`", "text": "hello" }] }`), nil
	})
	resources, err := client.ListResources(McpRequestOptions{})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	// A missing name falls back to the URI.
	if len(resources) != 2 || resources[1].Name != "file:///b" {
		t.Fatalf("resources = %+v", resources)
	}
	templates, err := client.ListResourceTemplates(McpRequestOptions{})
	if err != nil || len(templates) != 1 || templates[0].URITemplate != "repo://{owner}/{repo}" {
		t.Fatalf("templates = %+v %v", templates, err)
	}
	// Single pages pass the cursor through.
	page, err := client.ListResourcesPage("", McpRequestOptions{})
	if err != nil || len(page.Resources) != 1 || page.NextCursor != "2" {
		t.Fatalf("page = %+v %v", page, err)
	}
	page, err = client.ListResourcesPage("2", McpRequestOptions{})
	if err != nil || len(page.Resources) != 1 || page.NextCursor != "" {
		t.Fatalf("page 2 = %+v %v", page, err)
	}
	result, err := client.ReadResource("file:///a", McpRequestOptions{})
	if err != nil || len(result.Contents) != 1 || result.Contents[0].Text != "hello" {
		t.Fatalf("read = %+v %v", result, err)
	}
	server.setHandler("resources/read", func(*JsonRpcMessage) (any, error) {
		return mustParse(t, `{ "contents": [{ "uri": "file:///a" }] }`), nil
	})
	_, err = client.ReadResource("file:///a", McpRequestOptions{})
	if err == nil || !strings.Contains(err.Error(), "Invalid contents in MCP resources/read result") {
		t.Fatalf("invalid contents: %v", err)
	}
	server.setHandler("resources/list", func(*JsonRpcMessage) (any, error) {
		return mustParse(t, `{ "resources": [{ "name": "no uri" }] }`), nil
	})
	_, err = client.ListResources(McpRequestOptions{})
	if err == nil || !strings.Contains(err.Error(), "Invalid entry in MCP resources/list result") {
		t.Fatalf("invalid entry: %v", err)
	}
}

func TestClientReturnsStructuredContentAndErrors(t *testing.T) {
	client, server := connectTestClient(t)
	server.setHandler("tools/call", func(request *JsonRpcMessage) (any, error) {
		name, _ := request.ParamsObj().MustGet("name").(string)
		if name == "fail" {
			return nil, NewMcpError(1234, "tool failed", mustParse(t, `{"retryable":false}`))
		}
		count := any(nil)
		if params := request.ParamsObj(); params != nil {
			if arguments, ok := params.MustGet("arguments").(*jsonx.Obj); ok {
				count = arguments.MustGet("count")
			}
		}
		return jsonx.ObjFrom(
			"content", []any{jsonx.ObjFrom("type", "text", "text", "ok")},
			"structuredContent", jsonx.ObjFrom("count", count),
		), nil
	})
	args, _ := jsonx.Parse(`{"count":3}`)
	result, err := client.CallTool("count", args.(*jsonx.Obj), McpRequestOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].(TextContent).Text != "ok" {
		t.Fatalf("content = %+v", result.Content)
	}
	if count, _ := result.StructuredContent.MustGet("count").(float64); count != 3 {
		t.Fatalf("structured = %v", result.StructuredContent.MustGet("count"))
	}
	_, err = client.CallTool("fail", nil, McpRequestOptions{})
	mcpError, ok := err.(*McpError)
	if !ok || mcpError.Code != 1234 || mcpError.Message != "tool failed" {
		t.Fatalf("error = %v", err)
	}
	if !jsonx.Equal(mcpError.Data, mustParse(t, `{"retryable":false}`)) {
		t.Fatalf("data = %v", mcpError.Data)
	}
}

func TestClientRenewsTimeoutOnProgress(t *testing.T) {
	client, server := connectTestClient(t)
	progressTokenSeen := make(chan JsonRpcID, 1)
	server.setHandler("tools/call", func(request *JsonRpcMessage) (any, error) {
		token := idOf(request.ParamsObj().MustGet("_meta").(*jsonx.Obj).MustGet("progressToken"))
		progressTokenSeen <- token
		go func() {
			// Progress arrives before the (longer) request timeout.
			time.Sleep(60 * time.Millisecond)
			_ = server.transport.Send(NewNotification("notifications/progress", jsonx.ObjFrom(
				"progressToken", token.Value(),
				"progress", float64(1),
				"total", float64(2),
			)))
		}()
		time.Sleep(120 * time.Millisecond)
		return jsonx.ObjFrom("content", []any{jsonx.ObjFrom("type", "text", "text", "done")}), nil
	})
	progress := make(chan ProgressNotification, 1)
	callDone := make(chan *CallToolResult, 1)
	callFailed := make(chan error, 1)
	go func() {
		result, err := client.CallTool("slow", jsonx.NewObj(), McpRequestOptions{
			TimeoutMs:  100,
			OnProgress: func(notification ProgressNotification) { progress <- notification },
		})
		if err != nil {
			callFailed <- err
			return
		}
		callDone <- result
	}()
	token := <-progressTokenSeen
	select {
	case notification := <-progress:
		if !notification.ProgressToken.Equal(token) || notification.Progress != 1 || !notification.HasTotal || notification.Total != 2 {
			t.Fatalf("progress = %+v", notification)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no progress notification")
	}
	select {
	case result := <-callDone:
		if len(result.Content) != 1 || result.Content[0].(TextContent).Text != "done" {
			t.Fatalf("result = %+v", result.Content)
		}
	case err := <-callFailed:
		t.Fatalf("slow call: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("slow call did not finish")
	}
}

func TestClientCancelsAbortedAndTimedOutRequests(t *testing.T) {
	client, server := connectTestClient(t)
	server.setHandler("tools/call", func(*JsonRpcMessage) (any, error) {
		time.Sleep(5 * time.Second)
		return nil, nil
	})
	controller := abort.NewController()
	aborted := make(chan error, 1)
	go func() {
		_, err := client.CallTool("wait", nil, McpRequestOptions{Signal: controller.Signal()})
		aborted <- err
	}()
	time.Sleep(50 * time.Millisecond)
	controller.AbortReason("stop")
	select {
	case err := <-aborted:
		if _, isAbort := err.(*McpAbortError); !isAbort {
			t.Fatalf("abort error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("aborted call did not fail")
	}
	_, err := client.CallTool("wait", nil, McpRequestOptions{TimeoutMs: 5})
	if _, isTimeout := err.(*McpTimeoutError); !isTimeout {
		t.Fatalf("timeout error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, message := range server.sentMessages() {
			if message.Kind == KindNotification && message.Method() == "notifications/cancelled" {
				requireEqualJSON(t, message.Raw, `{
					"jsonrpc": "2.0",
					"method": "notifications/cancelled",
					"params": { "requestId": 2, "reason": "stop" }
				}`)
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cancelled notification not sent")
}

func TestClientReportsTransportErrorsWithoutFailingPending(t *testing.T) {
	clientTransport, server := createTestServer(t)
	client := NewMcpClient(McpClientOptions{Name: "test-client", Version: "1.0.0"})
	if _, err := client.Connect(clientTransport); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()
	errors := make(chan error, 4)
	client.OnError(func(err error) { errors <- err })
	respond := make(chan struct{})
	server.setHandler("tools/call", func(*JsonRpcMessage) (any, error) {
		<-respond
		return jsonx.ObjFrom("content", []any{}), nil
	})
	call := make(chan error, 1)
	go func() {
		_, err := client.CallTool("wait", nil, McpRequestOptions{})
		call <- err
	}()
	time.Sleep(50 * time.Millisecond)
	clientTransport.EmitErrorError(errString("stray log line"))
	close(respond)
	select {
	case err := <-call:
		if err != nil {
			t.Fatalf("pending failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call did not finish")
	}
	select {
	case err := <-errors:
		if err.Error() != "stray log line" {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("error not reported")
	}
}

func TestClientAcceptsOlderProtocolVersions(t *testing.T) {
	clientTransport, server := createTestServer(t)
	server.setHandler("initialize", func(*JsonRpcMessage) (any, error) {
		return mustParse(t, `{
			"protocolVersion": "2024-11-05",
			"capabilities": {},
			"serverInfo": { "name": "old-server", "version": "0.1.0" }
		}`), nil
	})
	client := NewMcpClient(McpClientOptions{Name: "test-client", Version: "1.0.0"})
	if _, err := client.Connect(clientTransport); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()
	if version := client.ProtocolVersion(); version == nil || *version != "2024-11-05" {
		t.Fatal("older version not accepted")
	}

	unsupportedTransport, unsupported := createTestServer(t)
	unsupported.setHandler("initialize", func(*JsonRpcMessage) (any, error) {
		return mustParse(t, `{
			"protocolVersion": "1999-01-01",
			"capabilities": {},
			"serverInfo": { "name": "ancient-server", "version": "0.1.0" }
		}`), nil
	})
	rejected := NewMcpClient(McpClientOptions{Name: "test-client", Version: "1.0.0"})
	if _, err := rejected.Connect(unsupportedTransport); err == nil ||
		!strings.Contains(err.Error(), "unsupported protocol version") {
		t.Fatalf("unsupported version accepted: %v", err)
	}
	if rejected.ConnectionState() != ClientClosed {
		t.Fatalf("state = %s", rejected.ConnectionState())
	}
}

func TestClientDefaultsMissingContentToEmptyList(t *testing.T) {
	client, server := connectTestClient(t)
	server.setHandler("tools/call", func(*JsonRpcMessage) (any, error) {
		return mustParse(t, `{ "structuredContent": { "ok": true } }`), nil
	})
	result, err := client.CallTool("structured", nil, McpRequestOptions{})
	if err != nil || len(result.Content) != 0 || result.StructuredContent.MustGet("ok") != true {
		t.Fatalf("result = %+v %v", result, err)
	}
	server.setHandler("tools/call", func(*JsonRpcMessage) (any, error) {
		return mustParse(t, `{ "content": "not a list" }`), nil
	})
	if _, err := client.CallTool("broken", nil, McpRequestOptions{}); err == nil ||
		!strings.Contains(err.Error(), "Invalid MCP tools/call result") {
		t.Fatalf("broken content: %v", err)
	}
}

func TestClientDoesNotCancelTimedOutInitialize(t *testing.T) {
	clientTransport, server := createTestServer(t)
	server.setHandler("initialize", func(*JsonRpcMessage) (any, error) {
		time.Sleep(5 * time.Second)
		return nil, nil
	})
	client := NewMcpClient(McpClientOptions{Name: "test-client", Version: "1.0.0", RequestTimeoutMs: 5})
	_, err := client.Connect(clientTransport)
	var timeout *McpTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("initialize error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	for _, message := range server.sentMessages() {
		if message.Kind == KindNotification && message.Method() == "notifications/cancelled" {
			t.Fatal("initialize must not be cancelled")
		}
	}
}

func TestClientNotifiesCloseListenersOnce(t *testing.T) {
	client, server := connectTestClient(t)
	closed := make(chan struct{}, 4)
	client.OnClose(func() { closed <- struct{}{} })
	server.setHandler("tools/call", func(*JsonRpcMessage) (any, error) {
		time.Sleep(5 * time.Second)
		return nil, nil
	})
	pending := make(chan error, 1)
	go func() {
		_, err := client.CallTool("wait", nil, McpRequestOptions{})
		pending <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := server.transport.Close(); err != nil {
		t.Fatalf("close server: %v", err)
	}
	select {
	case err := <-pending:
		var closedErr *McpConnectionClosedError
		if !errors.As(err, &closedErr) {
			t.Fatalf("pending error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending did not fail")
	}
	if client.ConnectionState() != ClientClosed {
		t.Fatalf("state = %s", client.ConnectionState())
	}
	_ = client.Close()
	// Exactly one close event even though Close ran again.
	time.Sleep(50 * time.Millisecond)
	count := 0
	for {
		select {
		case <-closed:
			count++
			continue
		default:
		}
		break
	}
	if count != 1 {
		t.Fatalf("close listeners fired %d times", count)
	}
}

func TestClientAnswersRootsAndDispatchesNotifications(t *testing.T) {
	clientTransport, server := createTestServer(t)
	client := NewMcpClient(McpClientOptions{
		Name:    "test-client",
		Version: "1.0.0",
		Roots:   StaticRoots([]Root{{URI: "file:///workspace", Name: "workspace"}}),
	})
	if _, err := client.Connect(clientTransport); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()
	changed := make(chan any, 4)
	client.OnNotification("notifications/tools/list_changed", func(params any) { changed <- params })
	if err := server.transport.Send(NewRequest(NewStringID("roots"), "roots/list", nil)); err != nil {
		t.Fatalf("send roots/list: %v", err)
	}
	if err := server.transport.Send(NewNotification("notifications/tools/list_changed", nil)); err != nil {
		t.Fatalf("send notification: %v", err)
	}
	select {
	case params := <-changed:
		if params != nil {
			t.Fatalf("params = %v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification not dispatched")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, message := range server.sentMessages() {
			if message.Kind == KindSuccessResponse && message.ID().Equal(NewStringID("roots")) {
				requireEqualJSON(t, message.Raw, `{
					"jsonrpc": "2.0",
					"id": "roots",
					"result": { "roots": [{ "uri": "file:///workspace", "name": "workspace" }] }
				}`)
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("roots/list not answered")
}

type errString string

func (e errString) Error() string { return string(e) }
