package codingagent

// Ports of the MCP connection, log, and credential-store cases from
// packages/coding-agent test/mcp-extension.test.ts at reference commit
// a276dabe.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/mcp"
)

// mcpFakeServer answers initialize, tools/list, and tools/call over an
// in-memory transport.
type mcpFakeServerOptions struct {
	expireFirstCall bool
	methods         *[]string
	noTools         bool
}

func mcpFakeTransport(options mcpFakeServerOptions) mcp.McpTransport {
	client, server := mcp.CreateInMemoryTransportPair()
	server.OnMessage(func(message *mcp.JsonRpcMessage) {
		if message.Kind != mcp.KindRequest {
			return
		}
		if options.methods != nil {
			*options.methods = append(*options.methods, message.Method())
		}
		var response *mcp.JsonRpcMessage
		switch message.Method() {
		case "initialize":
			capabilities := jsonx.ObjFrom("tools", jsonx.NewObj())
			if options.noTools {
				capabilities = jsonx.ObjFrom("prompts", jsonx.NewObj())
			}
			response = mcp.NewSuccessResponse(message.ID(), jsonx.ObjFrom(
				"protocolVersion", mcp.LATEST_PROTOCOL_VERSION,
				"capabilities", capabilities,
				"serverInfo", jsonx.ObjFrom("name", "fake", "version", "1.0.0"),
			))
		case "tools/list":
			if options.noTools {
				response = mcp.NewErrorResponse(message.ID(), mcp.JSON_RPC_ERROR_CODES.MethodNotFound,
					"Method not found", nil)
			} else {
				response = mcp.NewSuccessResponse(message.ID(), jsonx.ObjFrom("tools", []any{}))
			}
		case "resources/read":
			response = mcp.NewSuccessResponse(message.ID(), jsonx.ObjFrom(
				"contents", []any{jsonx.ObjFrom("uri", "docs://a", "text", "ok")},
			))
		default:
			response = mcp.NewSuccessResponse(message.ID(), jsonx.ObjFrom(
				"content", []any{jsonx.ObjFrom("type", "text", "text", "ok")},
			))
		}
		go func() { _ = server.Send(response, nil) }()
	})
	if err := server.Start(); err != nil {
		panic(err)
	}
	if options.expireFirstCall {
		return &expiringTransport{inner: client}
	}
	return client
}

// expiringTransport simulates the HTTP transport's 404 for a session the
// server no longer knows, for the first tools/call.
type expiringTransport struct {
	inner   mcp.McpTransport
	expired bool
}

func (t *expiringTransport) Start() error { return t.inner.Start() }
func (t *expiringTransport) OnMessage(listener mcp.McpTransportMessageListener) func() {
	return t.inner.OnMessage(listener)
}
func (t *expiringTransport) OnError(listener mcp.McpTransportErrorListener) func() {
	return t.inner.OnError(listener)
}
func (t *expiringTransport) OnClose(listener mcp.McpTransportCloseListener) func() {
	return t.inner.OnClose(listener)
}
func (t *expiringTransport) SetProtocolVersion(version string) {
	t.inner.SetProtocolVersion(version)
}
func (t *expiringTransport) Close() error { return t.inner.Close() }
func (t *expiringTransport) Send(message *mcp.JsonRpcMessage, options *mcp.McpTransportSendOptions) error {
	if message.Kind == mcp.KindRequest && message.Method() == "tools/call" && !t.expired {
		t.expired = true
		return mcp.NewMcpSessionExpiredError("gone")
	}
	return t.inner.Send(message, options)
}

func mcpTestEntry(t *testing.T, raw string) McpServerEntry {
	t.Helper()
	config, err := ValidateMcpServerConfig("fake", mustParseJSON(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	return McpServerEntry{Name: "fake", Config: config, Source: "test", Scope: "global"}
}

func newMcpTestConnection(entry McpServerEntry, transports []func(t *testing.T) mcp.McpTransport, log *McpServerLog) (*McpServerConnection, func() int) {
	opened := 0
	connection := NewMcpServerConnection(McpServerConnectionOptions{
		Entry: entry,
		Cwd:   ".",
		CreateTransport: func(McpServerEntry, string, *mcp.AuthProvider) (mcp.McpTransport, error) {
			index := opened
			opened++
			return transports[index](nil), nil
		},
		Credentials: NewMcpOAuthCredentialStoreAt(filepath.Join(os.TempDir(), "mcp-auth-unused.json")),
		Log:         log,
		OnTools:     func(*McpServerConnection) {},
	})
	return connection, func() int { return opened }
}

func TestMcpConnectionSessionExpiredRetries(t *testing.T) {
	entry := mcpTestEntry(t, `{"command": "x"}`)
	var methods []string
	options := mcpFakeServerOptions{expireFirstCall: true, methods: &methods}
	connection, opened := newMcpTestConnection(entry, []func(t *testing.T) mcp.McpTransport{
		func(*testing.T) mcp.McpTransport { return mcpFakeTransport(options) },
		func(*testing.T) mcp.McpTransport { return mcpFakeTransport(mcpFakeServerOptions{methods: &methods}) },
	}, nil)
	if _, err := connection.GetClient(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	result, err := connection.CallTool("t", nil, mcp.McpRequestOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].(mcp.TextContent).Text != "ok" {
		t.Fatalf("result = %+v", result)
	}
	// A retry opens a new session on a new transport.
	if count := opened(); count != 2 {
		t.Fatalf("opened = %d", count)
	}
	_ = connection.Close()
}

func TestMcpConnectionWithoutToolsCapability(t *testing.T) {
	entry := mcpTestEntry(t, `{"command": "x"}`)
	connection, _ := newMcpTestConnection(entry, []func(t *testing.T) mcp.McpTransport{
		func(t *testing.T) mcp.McpTransport {
			return mcpFakeTransport(mcpFakeServerOptions{noTools: true})
		},
	}, nil)
	if _, err := connection.GetClient(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if len(connection.Tools) != 0 {
		t.Fatalf("tools = %+v", connection.Tools)
	}
	_ = connection.Close()
}

func TestMcpConnectionReconnectsAfterDrop(t *testing.T) {
	entry := mcpTestEntry(t, `{"command": "x"}`)
	var servers []*mcp.InMemoryTransport
	factory := func(*testing.T) mcp.McpTransport {
		transport := mcpFakeTransport(mcpFakeServerOptions{})
		servers = append(servers, transport.(*mcp.InMemoryTransport))
		return transport
	}
	connection, opened := newMcpTestConnection(entry, []func(t *testing.T) mcp.McpTransport{factory, factory}, nil)
	if _, err := connection.GetClient(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := servers[0].Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && connection.State() != McpServerDisconnected {
		time.Sleep(5 * time.Millisecond)
	}
	if connection.State() != McpServerDisconnected {
		t.Fatalf("state = %s", connection.State())
	}
	// The next call reconnects.
	if _, err := connection.CallTool("t", nil, mcp.McpRequestOptions{}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if count := opened(); count != 2 {
		t.Fatalf("opened = %d", count)
	}
	_ = connection.Close()
}

func TestMcpConnectionRetriesTransientHTTPErrors(t *testing.T) {
	entry := mcpTestEntry(t, `{"url": "https://example.test/mcp"}`)
	attempts := 0
	failing := func(t *testing.T) mcp.McpTransport {
		attempts++
		if attempts == 1 {
			return failingTransport{}
		}
		return mcpFakeTransport(mcpFakeServerOptions{})
	}
	connection, opened := newMcpTestConnection(entry, []func(t *testing.T) mcp.McpTransport{failing, failing}, nil)
	if _, err := connection.GetClient(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if count := opened(); count != 2 {
		t.Fatalf("opened = %d", count)
	}
	_ = connection.Close()
}

type failingTransport struct{}

func (failingTransport) Start() error { return nil }
func (failingTransport) Send(*mcp.JsonRpcMessage, *mcp.McpTransportSendOptions) error {
	return mcp.NewMcpHttpError(503, "overloaded", "")
}
func (failingTransport) Close() error                                     { return nil }
func (failingTransport) OnMessage(mcp.McpTransportMessageListener) func() { return func() {} }
func (failingTransport) OnError(mcp.McpTransportErrorListener) func()     { return func() {} }
func (failingTransport) OnClose(mcp.McpTransportCloseListener) func()     { return func() {} }
func (failingTransport) SetProtocolVersion(string)                        {}

func TestMcpConnectionMarksNeedsAuth(t *testing.T) {
	entry := mcpTestEntry(t, `{"url": "https://example.test/mcp"}`)
	connection := NewMcpServerConnection(McpServerConnectionOptions{
		Entry: entry,
		Cwd:   ".",
		CreateTransport: func(McpServerEntry, string, *mcp.AuthProvider) (mcp.McpTransport, error) {
			return nil, &mcp.McpOAuthAuthorizationRequiredError{}
		},
		Credentials: NewMcpOAuthCredentialStoreAt(filepath.Join(t.TempDir(), "mcp-auth.json")),
		OnTools:     func(*McpServerConnection) {},
	})
	// A stdio-less URL entry uses OAuth; the connect fails asking for a
	// sign-in.
	if _, err := connection.GetClient(); err == nil || !strings.Contains(err.Error(), "requires sign-in") {
		t.Fatalf("err = %v", err)
	}
	if connection.State() != McpServerNeedsAuth {
		t.Fatalf("state = %s", connection.State())
	}
	_ = connection.Close()
}

func TestMcpConnectionAppendsServerLogs(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "mcp.log")
	log := NewMcpServerLog(logPath)
	entry := mcpTestEntry(t, `{"command": "x"}`)
	connection, _ := newMcpTestConnection(entry, []func(t *testing.T) mcp.McpTransport{
		func(t *testing.T) mcp.McpTransport { return mcpFakeTransport(mcpFakeServerOptions{}) },
	}, log)
	if _, err := connection.GetClient(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	log.Write("fake", mustParseJSON(t, `{"level": "warning", "logger": "db", "data": "slow query"}`))
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "[fake] warning db: slow query") {
		t.Fatalf("log = %q", text)
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatal("log line not newline-terminated")
	}
	_ = connection.Close()
}

func TestFormatMcpLogMessage(t *testing.T) {
	line := FormatMcpLogMessage("srv", mustParseJSON(t, `{"data": {"nested": true}}`), time.Unix(0, 0).UTC())
	if !strings.Contains(line, "[srv] info ") || !strings.Contains(line, `{"nested":true}`) {
		t.Fatalf("line = %q", line)
	}
	// Continuation lines are indented.
	line = FormatMcpLogMessage("srv", mustParseJSON(t, `{"data": "a\nb"}`), time.Unix(0, 0).UTC())
	if !strings.Contains(line, "a\n    b") {
		t.Fatalf("line = %q", line)
	}
	// A non-object params value is wrapped as data.
	line = FormatMcpLogMessage("srv", "plain", time.Unix(0, 0).UTC())
	if !strings.Contains(line, "[srv] info plain") {
		t.Fatalf("line = %q", line)
	}
}

func TestMcpOAuthCredentialStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-auth.json")
	store := NewMcpOAuthCredentialStoreAt(path)
	slice := store.ForServer("docs", "https://example.test/mcp")
	state := &mcp.McpOAuthState{
		ServerURL: "https://example.test/mcp",
		Tokens:    &mcp.OAuthTokens{AccessToken: "a1", TokenType: "Bearer", RefreshToken: "r1"},
	}
	if err := slice.Save(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.ForServer("docs", "https://example.test/mcp").Load()
	if err != nil || loaded == nil || loaded.Tokens.AccessToken != "a1" {
		t.Fatalf("load = %+v %v", loaded, err)
	}
	// Another server with the same URL keeps separate accounts: no state
	// of its own, and never the first server's tokens.
	other, err := store.ForServer("other", "https://example.test/mcp").Load()
	if err != nil || (other != nil && other.Tokens != nil) {
		t.Fatalf("cross-server = %+v %v", other, err)
	}
	// Tokens reads without legacy takeover.
	if tokens := store.Tokens("docs", "https://example.test/mcp"); tokens == nil || tokens.AccessToken != "a1" {
		t.Fatalf("tokens = %+v", tokens)
	}
	if !store.Remove("docs", "https://example.test/mcp") {
		t.Fatal("remove reported nothing stored")
	}
	if store.Remove("docs", "https://example.test/mcp") {
		t.Fatal("double remove")
	}
}

func TestMcpOAuthCredentialStoreLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-auth.json")
	legacyKey := "https://example.test/mcp"
	legacyState := &mcp.McpOAuthState{
		ServerURL: legacyKey,
		Tokens:    &mcp.OAuthTokens{AccessToken: "legacy", TokenType: "Bearer"},
	}
	root := jsonx.ObjFrom(legacyKey, legacyState.ToObj())
	if err := os.WriteFile(path, []byte(stringifyJSONIndent(root, "  ")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewMcpOAuthCredentialStoreAt(path)
	// The first server to load legacy state takes it over.
	loaded, err := store.ForServer("docs", legacyKey).Load()
	if err != nil || loaded == nil || loaded.Tokens.AccessToken != "legacy" {
		t.Fatalf("load = %+v %v", loaded, err)
	}
	// The legacy key is gone; others with the same URL sign in again.
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "\""+legacyKey+"\": {") && !strings.Contains(string(data), "mcp__docs") {
		t.Fatalf("file = %s", string(data))
	}
	other, err := store.ForServer("other", legacyKey).Load()
	if err != nil || (other != nil && other.Tokens != nil) {
		t.Fatalf("second server = %+v %v", other, err)
	}
}

func TestMcpServerLogRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.log")
	log := NewMcpServerLog(path)
	if err := os.WriteFile(path, make([]byte, maxMcpLogBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	log.Write("srv", "hello")
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated log missing: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("log = %q", string(data))
	}
}

func TestMcpCallbackSettingsDerivation(t *testing.T) {
	settings := mcpCallbackSettingsFrom(McpOAuthSettings{CallbackURL: "http://localhost:8080/oauth/callback"})
	if settings.Host != "127.0.0.1" || settings.RedirectHost != "localhost" || settings.Path != "/oauth/callback" {
		t.Fatalf("settings = %+v", settings)
	}
	if settings.FixedRedirectURL != "http://localhost:8080/oauth/callback" {
		t.Fatalf("fixed = %q", settings.FixedRedirectURL)
	}
	settings = mcpCallbackSettingsFrom(McpOAuthSettings{CallbackPort: 9123})
	if settings.FixedRedirectURL != "http://127.0.0.1:9123/callback" {
		t.Fatalf("fixed = %q", settings.FixedRedirectURL)
	}
	settings = mcpCallbackSettingsFrom(McpOAuthSettings{})
	if settings.FixedRedirectURL != "" || settings.Path != "/callback" {
		t.Fatalf("defaults = %+v", settings)
	}
}

func TestMcpWaitLockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guard")
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	inside := 0
	go func() {
		_ = withExclusiveFileLock(path, 20*time.Second, 5*time.Second, func() error {
			mu.Lock()
			inside++
			mu.Unlock()
			entered <- struct{}{}
			<-release
			return nil
		})
	}()
	<-entered
	err := withExclusiveFileLock(path, 20*time.Second, 200*time.Millisecond, func() error {
		return errors.New("must not run")
	})
	if err == nil || !strings.Contains(err.Error(), "could not acquire lock") {
		t.Fatalf("err = %v", err)
	}
	close(release)
	if err := withExclusiveFileLock(path, 20*time.Second, 5*time.Second, func() error { return nil }); err != nil {
		t.Fatalf("second acquire: %v", err)
	}
}

// Port of the close-while-connecting rule (8c911797c): Close resolves only
// after an in-flight connect attempt settles, and its client is closed.
func TestMcpCloseWaitsForConnectingAttempt(t *testing.T) {
	connectStarted := make(chan struct{})
	releaseConnect := make(chan struct{})
	entry := mcpTestEntry(t, `{"type":"stdio","command":"true"}`)
	connection := NewMcpServerConnection(McpServerConnectionOptions{
		Entry: entry,
		CreateTransport: func(McpServerEntry, string, *mcp.AuthProvider) (mcp.McpTransport, error) {
			<-connectStarted
			<-releaseConnect
			client, _ := mcp.CreateInMemoryTransportPair()
			return client, nil
		},
	})
	go func() {
		close(connectStarted)
		_, _ = connection.GetClient()
	}()
	<-connectStarted
	closed := make(chan struct{})
	go func() {
		_ = connection.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while the connect attempt was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseConnect)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not resolve after the connect attempt settled")
	}
	if connection.State() != McpServerClosed {
		t.Fatalf("state = %s", connection.State())
	}
}
