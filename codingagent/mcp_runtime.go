package codingagent

// Port of extensions/mcp/runtime.ts at reference commit a276dabe: the
// part of the MCP integration that talks to servers — connections,
// transports, and OAuth sign-in state.

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/mcp"

	"github.com/gladmo/openagent/abort"
)

const (
	mcpDefaultTimeoutSeconds = 60
	mcpStderrTailChars       = 2000
)

// mcpConnectRetryDelays are the delays between attempts to connect to an
// HTTP server that failed with a transient error.
var mcpConnectRetryDelays = []time.Duration{250 * time.Millisecond, time.Second}

// McpServerState is one server connection's lifecycle state.
// McpServerDisconnected means the connection dropped (for example the
// stdio server exited); the next call reconnects.
type McpServerState string

const (
	McpServerConnecting   McpServerState = "connecting"
	McpServerConnected    McpServerState = "connected"
	McpServerDisconnected McpServerState = "disconnected"
	McpServerNeedsAuth    McpServerState = "needs-auth"
	McpServerFailed       McpServerState = "failed"
	McpServerClosed       McpServerState = "closed"
)

// McpTransportFactory builds one server's transport.
type McpTransportFactory func(entry McpServerEntry, cwd string, authProvider *mcp.AuthProvider) (mcp.McpTransport, error)

// mcpErrorMessage renders an error's message like the reference.
func mcpErrorMessage(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// mcpIsTransientError reports network failures and overloaded or
// restarting servers, which are worth another attempt.
func mcpIsTransientError(err error) bool {
	var httpErr *mcp.McpHttpError
	if errors.As(err, &httpErr) {
		return httpErr.Status == 408 || httpErr.Status == 429 || (httpErr.Status >= 500 && httpErr.Status != 501)
	}
	return mcp.IsNetworkError(err)
}

func mcpSignInRequiredMessage(entry McpServerEntry) string {
	provider := ""
	if entry.Config.IsHTTP() {
		provider = entry.Config.AuthProvider()
	}
	if provider != "" {
		return fmt.Sprintf("MCP server %q requires sign-in. Run /login %s to sign in.", entry.Name, provider)
	}
	return fmt.Sprintf("MCP server %q requires sign-in. Run /mcp to sign in.", entry.Name)
}

// mcpUsesOAuth: HTTP servers authenticate with OAuth unless the config
// supplies an Authorization header or auth.
func mcpUsesOAuth(entry McpServerEntry) bool {
	config := entry.Config
	if !config.IsHTTP() || config.AuthProvider() != "" {
		return false
	}
	for header := range config.Headers() {
		if strings.EqualFold(header, "authorization") {
			return false
		}
	}
	return true
}

// mcpExpandHome resolves ~ and ~/… (also ~\… on Windows) to the home
// directory, like in a shell.
func mcpExpandHome(value string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return value
	}
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") {
		return filepath.Join(home, value[2:])
	}
	return value
}

// CreateDefaultTransport builds the stdio or streamable HTTP transport
// for one server entry.
func CreateDefaultTransport(entry McpServerEntry, cwd string, authProvider *mcp.AuthProvider) (mcp.McpTransport, error) {
	config := entry.Config
	if config.IsHTTP() {
		headers, err := ResolveHeadersOrThrow(config.Headers(), fmt.Sprintf("MCP server %q", entry.Name), nil)
		if err != nil {
			return nil, err
		}
		var headerPairs [][2]string
		for name, value := range headers {
			headerPairs = append(headerPairs, [2]string{name, value})
		}
		transport, err := mcp.NewStreamableHttpTransport(mcp.StreamableHttpTransportOptions{
			URL:          config.URL(),
			Headers:      headerPairs,
			AuthProvider: authProvider,
		})
		if err != nil {
			return nil, err
		}
		return transport, nil
	}
	env := map[string]string{}
	for key, value := range config.Env() {
		resolved, err := ResolveConfigValueOrThrow(value, fmt.Sprintf("MCP server %q env %q", entry.Name, key), nil)
		if err != nil {
			return nil, err
		}
		env[key] = resolved
	}
	args := make([]string, 0, len(config.Args()))
	for _, arg := range config.Args() {
		args = append(args, mcpExpandHome(arg))
	}
	workingDir := config.Cwd()
	if workingDir == "" {
		workingDir = "."
	}
	resolved, err := filepath.Abs(filepath.Join(cwd, mcpExpandHome(workingDir)))
	if err != nil {
		resolved = mcpExpandHome(workingDir)
	}
	return mcp.NewStdioTransport(mcp.StdioTransportOptions{
		Command: mcpExpandHome(config.Command()),
		Args:    args,
		Cwd:     resolved,
		Env:     env,
	}), nil
}

// mcpWithoutTemplates: servers that do not implement
// resources/templates/list have no templates. Other errors propagate to
// the caller's catch, which swallows them like the reference.
func mcpWithoutTemplates(err error) error {
	if err == nil {
		return nil
	}
	var mcpErr *mcp.McpError
	if errors.As(err, &mcpErr) && mcpErr.Code == mcp.JSON_RPC_ERROR_CODES.MethodNotFound {
		return nil
	}
	return err
}

// McpServerConnectionOptions configure one server connection.
type McpServerConnectionOptions struct {
	Entry           McpServerEntry
	Cwd             string
	CreateTransport McpTransportFactory
	Credentials     *McpOAuthCredentialStore
	// ProviderToken returns the current token of a pi provider, for
	// servers with auth.provider; nil when no registry is available.
	ProviderToken func(provider string) (string, bool)
	OnTools       func(connection *McpServerConnection)
	// OnChange is called when State, Error, or Tools change.
	OnChange func(connection *McpServerConnection)
	// Log receives the server's log messages (notifications/message).
	Log *McpServerLog
}

// McpServerConnection is one configured server. It reconnects lazily
// when a call finds the connection gone.
type McpServerConnection struct {
	// Entry is the server's configuration entry.
	Entry McpServerEntry
	// Tools are the names the server currently offers.
	Tools []mcp.Tool

	mu           sync.Mutex
	state        McpServerState
	err          string
	hasResources bool
	instructions string
	challenge    *mcp.OAuthChallenge
	client       *mcp.McpClient
	opening      *mcpOpenWaiter
	// shutdown is aborted by Close; it cancels a connect in progress,
	// including the wait between retries.
	shutdown   *abort.Controller
	stderrTail string

	cwd             string
	createTransport McpTransportFactory
	authProvider    *McpAuthProviderExt
	onTools         func(*McpServerConnection)
	onChange        func(*McpServerConnection)
	log             *McpServerLog
}

type mcpOpenResult struct {
	client *mcp.McpClient
	err    error
}

// mcpOpenWaiter shares one open() outcome with every waiter: the opener
// stores the result and closes done once; any number of GetClient callers,
// Reconnect, SignOut, and Close observe the same outcome (the reference
// shares one promise).
type mcpOpenWaiter struct {
	done   chan struct{}
	client *mcp.McpClient
	err    error
}

func (w *mcpOpenWaiter) wait() (*mcp.McpClient, error) {
	<-w.done
	return w.client, w.err
}

func (w *mcpOpenWaiter) resolve(client *mcp.McpClient, err error) {
	w.client, w.err = client, err
	close(w.done)
}

// NewMcpServerConnection prepares one server's connection.
func NewMcpServerConnection(options McpServerConnectionOptions) *McpServerConnection {
	connection := &McpServerConnection{
		Entry:           options.Entry,
		state:           McpServerConnecting,
		cwd:             options.Cwd,
		createTransport: options.CreateTransport,
		onTools:         options.OnTools,
		onChange:        options.OnChange,
		log:             options.Log,
		shutdown:        abort.NewController(),
	}
	oauthURL := connection.OAuthURL()
	provider := ""
	if options.Entry.Config.IsHTTP() {
		provider = options.Entry.Config.AuthProvider()
	}
	if oauthURL != "" && options.Credentials != nil {
		connection.authProvider = createMcpAuthProvider(struct {
			ServerURL   string
			Store       McpOAuthServerStore
			Settings    func() McpOAuthSettings
			OnChallenge func(challenge mcp.OAuthChallenge)
		}{
			ServerURL: oauthURL,
			Store:     options.Credentials.ForServer(options.Entry.Name, oauthURL),
			Settings:  connection.OAuthSettings,
			OnChallenge: func(challenge mcp.OAuthChallenge) {
				connection.mu.Lock()
				copy := challenge
				connection.challenge = &copy
				connection.mu.Unlock()
			},
		})
	} else if provider != "" {
		// Read on every request, so the provider's refreshes apply; MCP
		// stores no copy.
		connection.authProvider = &McpAuthProviderExt{
			Token: func() (string, bool) {
				if options.ProviderToken == nil {
					return "", false
				}
				return options.ProviderToken(provider)
			},
		}
	}
	return connection
}

// Name returns the configured server name.
func (c *McpServerConnection) Name() string { return c.Entry.Name }

// TimeoutMs returns the per-request timeout; progress notifications from
// the server reset it.
func (c *McpServerConnection) TimeoutMs() int {
	seconds := c.Entry.Config.TimeoutSeconds()
	return int(seconds * 1000)
}

// OAuthURL returns the server URL when the server authenticates with
// OAuth, else "".
func (c *McpServerConnection) OAuthURL() string {
	if mcpUsesOAuth(c.Entry) && c.Entry.Config.IsHTTP() {
		return c.Entry.Config.URL()
	}
	return ""
}

// OAuthSettings resolves the server's OAuth settings.
func (c *McpServerConnection) OAuthSettings() McpOAuthSettings {
	oauth := c.Entry.Config.OAuth()
	if oauth == nil {
		return McpOAuthSettings{}
	}
	stringField := func(key string) string {
		text, _ := oauth.MustGet(key).(string)
		return text
	}
	settings := McpOAuthSettings{
		ClientID:    stringField("clientId"),
		CallbackURL: stringField("callbackUrl"),
		Scope:       stringField("scope"),
		ClientName:  stringField("clientName"),
	}
	if secret := stringField("clientSecret"); secret != "" {
		resolved, err := ResolveConfigValueOrThrow(secret, fmt.Sprintf("MCP server %q oauth.clientSecret", c.Entry.Name), nil)
		if err == nil {
			settings.ClientSecret = resolved
		}
	}
	if port, ok := oauth.MustGet("callbackPort").(float64); ok {
		settings.CallbackPort = int(port)
	}
	settings.ClientRegistration = stringField("clientRegistration")
	if metadataURL := stringField("authServerMetadataUrl"); metadataURL != "" {
		settings.AuthServerMetadataURL = metadataURL
	}
	return settings
}

// State returns the current connection state.
func (c *McpServerConnection) State() McpServerState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Error returns the last failure message ("" when none).
func (c *McpServerConnection) Error() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// HasResources reports whether the server offers resources.
func (c *McpServerConnection) HasResources() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hasResources
}

// Instructions returns the server instructions from initialize,
// describing its tools as a group.
func (c *McpServerConnection) Instructions() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.instructions
}

// Challenge returns the last OAuth challenge from the server; sign-in
// uses its resource metadata URL and scope.
func (c *McpServerConnection) Challenge() *mcp.OAuthChallenge {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.challenge
}

// ClearChallenge drops the answered challenge.
func (c *McpServerConnection) ClearChallenge() {
	c.mu.Lock()
	c.challenge = nil
	c.mu.Unlock()
}

// StderrTail returns the stderr of the last stdio server that failed to
// connect.
func (c *McpServerConnection) StderrTail() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stderrTail
}

type mcpClientWaiter struct {
	client *mcp.McpClient
	err    error
}

// GetClient returns the connected client, connecting when needed.
func (c *McpServerConnection) GetClient() (*mcp.McpClient, error) {
	c.mu.Lock()
	if c.shutdown.Signal().Aborted() {
		c.mu.Unlock()
		return nil, fmt.Errorf("MCP server %q is shut down", c.Entry.Name)
	}
	if c.client != nil && c.client.ConnectionState() == mcp.ClientConnected {
		client := c.client
		c.mu.Unlock()
		return client, nil
	}
	if c.opening != nil {
		opening := c.opening
		c.mu.Unlock()
		return opening.wait()
	}
	opening := &mcpOpenWaiter{done: make(chan struct{})}
	c.opening = opening
	c.mu.Unlock()
	client, err := c.open()
	c.mu.Lock()
	c.opening = nil
	c.mu.Unlock()
	opening.resolve(client, err)
	return client, err
}

// CallTool invokes one tool on the server.
func (c *McpServerConnection) CallTool(name string, args *jsonx.Obj, options mcp.McpRequestOptions) (*mcp.CallToolResult, error) {
	return withClient(c, func(client *mcp.McpClient) (*mcp.CallToolResult, error) {
		return client.CallTool(name, args, options)
	}, false)
}

// ReadResource reads one resource.
func (c *McpServerConnection) ReadResource(uri string, options mcp.McpRequestOptions) (*mcp.ReadResourceResult, error) {
	return withClient(c, func(client *mcp.McpClient) (*mcp.ReadResourceResult, error) {
		return client.ReadResource(uri, options)
	}, true)
}

// ResourcesPage lists one page of the server's resources.
func (c *McpServerConnection) ResourcesPage(cursor string, options mcp.McpRequestOptions) (*mcp.ListResourcesResult, error) {
	return withClient(c, func(client *mcp.McpClient) (*mcp.ListResourcesResult, error) {
		return client.ListResourcesPage(cursor, options)
	}, true)
}

// ResourceTemplatesPage lists one page of the server's templates.
func (c *McpServerConnection) ResourceTemplatesPage(cursor string, options mcp.McpRequestOptions) (*mcp.ListResourceTemplatesResult, error) {
	return withClient(c, func(client *mcp.McpClient) (*mcp.ListResourceTemplatesResult, error) {
		result, err := client.ListResourceTemplatesPage(cursor, options)
		if err != nil {
			var mcpErr *mcp.McpError
			if errors.As(err, &mcpErr) && mcpErr.Code == mcp.JSON_RPC_ERROR_CODES.MethodNotFound {
				return &mcp.ListResourceTemplatesResult{}, nil
			}
		}
		return result, err
	}, true)
}

// AllResources lists every resource of the server.
func (c *McpServerConnection) AllResources(options mcp.McpRequestOptions) ([]mcp.Resource, error) {
	return withClient(c, func(client *mcp.McpClient) ([]mcp.Resource, error) {
		return client.ListResources(options)
	}, true)
}

// AllResourceTemplates lists every template of the server.
func (c *McpServerConnection) AllResourceTemplates(options mcp.McpRequestOptions) ([]mcp.ResourceTemplate, error) {
	return withClient(c, func(client *mcp.McpClient) ([]mcp.ResourceTemplate, error) {
		templates, err := client.ListResourceTemplates(options)
		if err != nil {
			var mcpErr *mcp.McpError
			if errors.As(err, &mcpErr) && mcpErr.Code == mcp.JSON_RPC_ERROR_CODES.MethodNotFound {
				return nil, nil
			}
			return nil, err
		}
		return templates, nil
	}, true)
}

// withClient runs a request, reconnecting when needed. readOnly requests
// are retried once after a transient HTTP error; tool calls are not,
// since they may have run.
func withClient[T any](c *McpServerConnection, run func(client *mcp.McpClient) (T, error), readOnly bool) (T, error) {
	var zero T
	for attempt := 1; ; attempt++ {
		client, err := c.GetClient()
		if err != nil {
			return zero, err
		}
		result, runErr := run(client)
		if runErr == nil {
			return result, nil
		}
		if readOnly && attempt == 1 {
			var httpErr *mcp.McpHttpError
			if errors.As(runErr, &httpErr) && mcpIsTransientError(runErr) {
				time.Sleep(mcpConnectRetryDelays[0])
				continue
			}
		}
		if attempt == 1 {
			// The server no longer knows the session (restart, deploy), so
			// it did not run the request. Retry once on a new session. The
			// old client is detached but not closed: closing would fail its
			// other in-flight calls, which instead get the same 404 and
			// retry the same way.
			var expired *mcp.McpSessionExpiredError
			if errors.As(runErr, &expired) {
				c.mu.Lock()
				if c.client == client {
					c.client = nil
				}
				c.mu.Unlock()
				continue
			}
		}
		if !c.needsSignIn(runErr) {
			return zero, runErr
		}
		c.dropClient(client)
		c.markNeedsAuth()
		return zero, errors.New(mcpSignInRequiredMessage(c.Entry))
	}
}

// Reconnect connects again with fresh credentials, for example after
// signing in.
func (c *McpServerConnection) Reconnect() error {
	c.mu.Lock()
	opening := c.opening
	client := c.client
	c.mu.Unlock()
	if opening != nil {
		opening.wait()
	}
	if client != nil {
		c.dropClient(client)
	}
	_, err := c.GetClient()
	return err
}

// SignOut disconnects after the stored credentials were removed.
func (c *McpServerConnection) SignOut() error {
	c.mu.Lock()
	opening := c.opening
	client := c.client
	c.mu.Unlock()
	if opening != nil {
		opening.wait()
	}
	if client != nil {
		c.dropClient(client)
	}
	if !c.isClosed() {
		c.markNeedsAuth()
	}
	return nil
}

// needsSignIn: OAuth servers that still reject the request after a
// refresh need the user to sign in again.
func (c *McpServerConnection) needsSignIn(err error) bool {
	var required *mcp.McpOAuthAuthorizationRequiredError
	if errors.As(err, &required) {
		return true
	}
	var authRequired *mcp.McpAuthRequiredError
	if c.authProvider != nil && errors.As(err, &authRequired) {
		return true
	}
	return false
}

func (c *McpServerConnection) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shutdown.Signal().Aborted()
}

func (c *McpServerConnection) markNeedsAuth() {
	c.mu.Lock()
	c.state = McpServerNeedsAuth
	c.err = ""
	c.mu.Unlock()
	c.changed()
}

func (c *McpServerConnection) changed() {
	if c.onChange != nil {
		c.onChange(c)
	}
}

func (c *McpServerConnection) dropClient(client *mcp.McpClient) {
	c.mu.Lock()
	if c.client == client {
		c.client = nil
	}
	c.mu.Unlock()
	_ = client.Close()
}

func (c *McpServerConnection) open() (*mcp.McpClient, error) {
	c.mu.Lock()
	c.state = McpServerConnecting
	c.mu.Unlock()
	c.changed()
	retries := mcpConnectRetryDelays
	if !c.Entry.Config.IsHTTP() {
		retries = nil
	}
	for attempt := 0; ; attempt++ {
		c.mu.Lock()
		c.stderrTail = ""
		c.mu.Unlock()
		client, err := c.connectOnce()
		if err == nil {
			return client, nil
		}
		var delay time.Duration
		if attempt < len(retries) {
			delay = retries[attempt]
		}
		if c.isClosed() || delay == 0 || !mcpIsTransientError(err) {
			return nil, c.connectFailed(err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-c.shutdown.Signal().Done():
			timer.Stop()
			return nil, c.connectFailed(err)
		}
		if c.isClosed() {
			return nil, c.connectFailed(err)
		}
	}
}

func (c *McpServerConnection) connectOnce() (*mcp.McpClient, error) {
	client := mcp.NewMcpClient(mcp.McpClientOptions{
		Name:    AppName(),
		Version: Version(),
		// A negative value would disable the timeout; the per-server
		// default applies.
		RequestTimeoutMs: c.TimeoutMs(),
		Roots:            mcp.StaticRoots([]mcp.Root{{URI: mcpFileURL(c.cwd), Name: mcpBasename(c.cwd)}}),
	})
	if c.log != nil {
		serverName := c.Entry.Name
		log := c.log
		client.OnNotification("notifications/message", func(params any) {
			log.Write(serverName, params)
		})
	}
	var authProvider *mcp.AuthProvider
	if c.authProvider != nil {
		authProvider = c.authProvider.AsAuthProvider()
	}
	transport, err := c.createTransport(c.Entry, c.cwd, authProvider)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	// Closing the client fails its pending initialize or discovery
	// requests, so an aborted shutdown ends the connect promptly (the
	// reference registers shutdown -> closeClient here).
	c.mu.Lock()
	shutdown := c.shutdown
	c.mu.Unlock()
	removeAbort := shutdown.Signal().OnAbort(func() { _ = client.Close() })
	defer removeAbort()
	// Shutdown may have aborted before the listener registered (OnAbort
	// never fires post-abort); close immediately in that window.
	if shutdown.Signal().Aborted() {
		_ = client.Close()
		return nil, errors.New("shut down while connecting")
	}
	if _, err := client.Connect(transport); err != nil {
		_ = client.Close()
		if stdio, isStdio := transport.(*mcp.StdioTransport); isStdio {
			c.captureStderrTail(stdio)
		}
		return nil, err
	}
	client.OnNotification("notifications/tools/list_changed", func(any) {
		c.refreshTools(client)
	})
	stdio, _ := transport.(*mcp.StdioTransport)
	client.OnClose(func() { c.handleClientClose(client, stdio) })
	// Servers without the tools capability (prompts or resources only) do
	// not answer tools/list.
	var tools []mcp.Tool
	if capabilities := client.ServerCapabilities(); capabilities != nil && capabilities.Tools != nil {
		tools, _ = client.ListTools(mcp.McpRequestOptions{})
	}
	// Resources are not listed at connect: listing every resource can take
	// long (a large drive, say). The resource tools list and read on demand.
	hasResources := client.ServerCapabilities() != nil && client.ServerCapabilities().Resources != nil
	if c.isClosed() {
		return nil, errors.New("shut down while connecting")
	}
	if client.ConnectionState() != mcp.ClientConnected {
		return nil, errors.New("connection closed during setup")
	}
	instructions := ""
	if text := client.Instructions(); text != nil {
		instructions = strings.TrimSpace(*text)
	}
	c.mu.Lock()
	c.client = client
	c.Tools = tools
	c.hasResources = hasResources
	c.instructions = instructions
	c.state = McpServerConnected
	c.err = ""
	c.mu.Unlock()
	if c.onTools != nil {
		c.onTools(c)
	}
	c.changed()
	return client, nil
}

func (c *McpServerConnection) captureStderrTail(stdio *mcp.StdioTransport) {
	tail := strings.TrimSpace(stdio.Stderr())
	if len(tail) > mcpStderrTailChars {
		tail = tail[len(tail)-mcpStderrTailChars:]
	}
	if tail == "" {
		return
	}
	c.mu.Lock()
	c.stderrTail = tail
	c.mu.Unlock()
}

func (c *McpServerConnection) connectFailed(err error) error {
	if c.needsSignIn(err) && !c.isClosed() {
		c.markNeedsAuth()
		return errors.New(mcpSignInRequiredMessage(c.Entry))
	}
	c.mu.Lock()
	if c.shutdown.Signal().Aborted() {
		c.state = McpServerClosed
	} else {
		c.state = McpServerFailed
	}
	message := mcpErrorMessage(err)
	if tail := c.stderrTail; tail != "" {
		message = message + "\n" + tail
	}
	c.err = message
	c.mu.Unlock()
	c.changed()
	return fmt.Errorf("MCP server %q failed to connect: %s", c.Entry.Name, message)
}

// handleClientClose: the transport dropped. The next call reconnects;
// until then the status shows why.
func (c *McpServerConnection) handleClientClose(client *mcp.McpClient, stdio *mcp.StdioTransport) {
	c.mu.Lock()
	if c.client != client || c.shutdown.Signal().Aborted() {
		c.mu.Unlock()
		return
	}
	c.client = nil
	c.state = McpServerDisconnected
	errorText := "Connection closed"
	if stdio != nil {
		if tail := strings.TrimSpace(stdio.Stderr()); len(tail) > 0 {
			if len(tail) > mcpStderrTailChars {
				tail = tail[len(tail)-mcpStderrTailChars:]
			}
			errorText = "Connection closed\n" + tail
		}
	}
	c.err = errorText
	c.mu.Unlock()
	c.changed()
}

func (c *McpServerConnection) refreshTools(client *mcp.McpClient) {
	tools, err := client.ListTools(mcp.McpRequestOptions{})
	c.mu.Lock()
	if c.client != client || c.shutdown.Signal().Aborted() {
		c.mu.Unlock()
		return
	}
	if err != nil {
		c.err = "Failed to refresh tools: " + mcpErrorMessage(err)
	} else {
		c.Tools = tools
	}
	c.mu.Unlock()
	if err == nil && c.onTools != nil {
		c.onTools(c)
	}
	c.changed()
}

// Close shuts the connection down.
func (c *McpServerConnection) Close() error {
	c.mu.Lock()
	c.state = McpServerClosed
	client := c.client
	c.client = nil
	opening := c.opening
	shutdown := c.shutdown
	c.mu.Unlock()
	// Abort outside the lock: the abort listeners run synchronously and one
	// closes the connecting client, whose OnClose handler re-enters c.mu.
	shutdown.Abort()
	c.changed()
	if client != nil {
		_ = client.Close()
	}
	// Resolve once no transport of this server is open, including one that
	// was still connecting: the shutdown abort above fails its pending
	// initialize or discovery requests, so open() returns promptly.
	if opening != nil {
		if client, _ := opening.wait(); client != nil {
			_ = client.Close()
		}
	}
	// A refresh the server already answered may have rotated the refresh
	// token; exiting before the new tokens are saved would lose the
	// grant.
	if c.authProvider != nil && c.authProvider.Settled != nil {
		_ = c.authProvider.Settled()
	}
	return nil
}

// mcpFileURL renders a path as a file: URL like pathToFileURL.
func mcpFileURL(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String()
}

// mcpBasename is path basename.
func mcpBasename(path string) string {
	return filepath.Base(path)
}
