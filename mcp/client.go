package mcp

// Port of packages/mcp/src/client.ts: the MCP client — request/response
// correlation with timeouts, aborts, and progress; server request
// handling; initialize handshake; and the paginated list helpers.

import (
	"errors"
	"math"
	"sync"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

const (
	defaultRequestTimeoutMs = 30_000
	maxListPages            = 1_000
)

// ClientState is the connection lifecycle state.
type ClientState string

const (
	ClientIdle       ClientState = "idle"
	ClientConnecting ClientState = "connecting"
	ClientConnected  ClientState = "connected"
	ClientClosed     ClientState = "closed"
)

// RootsSource supplies the roots for roots/list server requests; static
// roots use StaticRoots.
type RootsSource func() ([]Root, error)

// StaticRoots fixes the roots at construction time.
func StaticRoots(roots []Root) RootsSource {
	copied := append([]Root(nil), roots...)
	return func() ([]Root, error) { return copied, nil }
}

// McpClientOptions configures the client identity and defaults.
type McpClientOptions struct {
	Name    string
	Version string
	Title   string

	Capabilities    *ClientCapabilities
	ProtocolVersion string
	// RequestTimeoutMs defaults to 30000; a negative value disables the
	// timeout (the reference uses Infinity).
	RequestTimeoutMs int
	Roots            RootsSource
}

// McpRequestOptions tunes one request.
type McpRequestOptions struct {
	Signal *abort.Signal
	// TimeoutMs overrides the client default; zero inherits it, a
	// negative value disables the timeout.
	TimeoutMs  int
	OnProgress func(progress ProgressNotification)
}

// RequestHandler answers one server request.
type RequestHandler func(params any, signal *abort.Signal) (any, error)

// handlerEntry gives set-remove identity to function registrations.
type handlerEntry struct{ fn RequestHandler }

// notificationEntry gives set-remove identity to function registrations.
type notificationEntry struct{ fn func(params any) }

type errorEntry struct{ fn func(err error) }

type closeEntry struct{ fn func() }

type pendingRequest struct {
	done chan pendingResult
	mu   sync.Mutex
	// timeoutMs, timer are guarded by mu.
	timeoutMs     int
	timer         *time.Timer
	signal        *abort.Signal
	removeAbort   func()
	cancellable   bool
	onProgress    func(ProgressNotification)
	progressToken *JsonRpcID
}

type pendingResult struct {
	value any
	err   error
}

// McpClient speaks MCP over one transport.
type McpClient struct {
	Options McpClientOptions

	mu                      sync.Mutex
	state                   ClientState
	transport               McpTransport
	nextRequestID           int64
	serverInfoValue         *Implementation
	serverCapabilitiesValue *ServerCapabilities
	instructionsValue       *string
	protocolVersionValue    *string
	pending                 map[JsonRpcID]*pendingRequest
	progressRequests        map[JsonRpcID]JsonRpcID
	incoming                map[JsonRpcID]*abort.Controller
	requestHandlers         map[string]*handlerEntry
	notificationListeners   map[string][]*notificationEntry
	errorListeners          []*errorEntry
	closeListeners          []*closeEntry
	disposers               []func()
}

// NewMcpClient builds a client in the idle state. The client answers ping
// requests, and roots/list when Roots is set.
func NewMcpClient(options McpClientOptions) *McpClient {
	client := &McpClient{
		Options:               options,
		state:                 ClientIdle,
		nextRequestID:         1,
		pending:               map[JsonRpcID]*pendingRequest{},
		progressRequests:      map[JsonRpcID]JsonRpcID{},
		incoming:              map[JsonRpcID]*abort.Controller{},
		requestHandlers:       map[string]*handlerEntry{},
		notificationListeners: map[string][]*notificationEntry{},
	}
	client.requestHandlers["ping"] = &handlerEntry{fn: func(any, *abort.Signal) (any, error) {
		return jsonx.NewObj(), nil
	}}
	if options.Roots != nil {
		client.requestHandlers["roots/list"] = &handlerEntry{fn: func(any, *abort.Signal) (any, error) {
			roots, err := options.Roots()
			if err != nil {
				return nil, err
			}
			list := jsonx.NewObj()
			items := make([]any, 0, len(roots))
			for _, root := range roots {
				entry := jsonx.ObjFrom("uri", root.URI)
				if root.Name != "" {
					entry.Set("name", root.Name)
				}
				items = append(items, entry)
			}
			list.Set("roots", items)
			return list, nil
		}}
	}
	return client
}

// ConnectionState returns the current lifecycle state.
func (c *McpClient) ConnectionState() ClientState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// ServerInfo returns the server identity once connected.
func (c *McpClient) ServerInfo() *Implementation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.serverInfoValue
}

// ServerCapabilities returns the server capabilities once connected.
func (c *McpClient) ServerCapabilities() *ServerCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.serverCapabilitiesValue
}

// Instructions returns the server instructions once connected.
func (c *McpClient) Instructions() *string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.instructionsValue
}

// ProtocolVersion returns the negotiated protocol version.
func (c *McpClient) ProtocolVersion() *string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.protocolVersionValue
}

// Connect starts the transport and runs the initialize handshake.
func (c *McpClient) Connect(transport McpTransport) (*InitializeResult, error) {
	c.mu.Lock()
	if c.state != ClientIdle {
		state := c.state
		c.mu.Unlock()
		return nil, errors.New("Cannot connect MCP client in " + string(state) + " state")
	}
	c.state = ClientConnecting
	c.transport = transport
	c.mu.Unlock()
	c.disposers = append(c.disposers,
		transport.OnMessage(func(message *JsonRpcMessage) { c.handleMessage(message) }),
		// Transport errors are reported only. Pending requests fail when
		// the transport closes.
		transport.OnError(func(err error) { c.EmitError(err) }),
		transport.OnClose(func() { c.handleTransportClose() }),
	)

	if err := transport.Start(); err != nil {
		_ = c.Close()
		return nil, err
	}
	capabilities := capabilitiesToObj(c.Options.Capabilities)
	if c.Options.Roots != nil && c.Options.Capabilities == nil {
		capabilities.Set("roots", jsonx.NewObj())
	} else if c.Options.Roots != nil && c.Options.Capabilities != nil && c.Options.Capabilities.Roots == nil {
		capabilities.Set("roots", jsonx.NewObj())
	}
	version := c.Options.ProtocolVersion
	if version == "" {
		version = LATEST_PROTOCOL_VERSION
	}
	clientInfo := jsonx.ObjFrom("name", c.Options.Name, "version", c.Options.Version)
	if c.Options.Title != "" {
		clientInfo.Set("title", c.Options.Title)
	}
	params := jsonx.ObjFrom(
		"protocolVersion", version,
		"capabilities", capabilities,
		"clientInfo", clientInfo,
	)
	answer, err := c.requestInternal("initialize", params, McpRequestOptions{}, true)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	result, err := validateInitializeResult(answer)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	if !IsSupportedProtocolVersion(result.ProtocolVersion) {
		err := errors.New("MCP server selected unsupported protocol version " + result.ProtocolVersion)
		_ = c.Close()
		return nil, err
	}
	transport.SetProtocolVersion(result.ProtocolVersion)
	if err := c.notifyInternal("notifications/initialized", nil, true); err != nil {
		_ = c.Close()
		return nil, err
	}
	c.mu.Lock()
	c.protocolVersionValue = &result.ProtocolVersion
	c.serverInfoValue = &result.ServerInfo
	c.serverCapabilitiesValue = &result.Capabilities
	instructions := result.Instructions
	if instructions != "" {
		c.instructionsValue = &instructions
	}
	c.state = ClientConnected
	c.mu.Unlock()
	return result, nil
}

func capabilitiesToObj(capabilities *ClientCapabilities) *jsonx.Obj {
	obj := jsonx.NewObj()
	if capabilities == nil {
		return obj
	}
	if capabilities.Experimental != nil {
		obj.Set("experimental", capabilities.Experimental)
	}
	if capabilities.Roots != nil {
		roots := jsonx.NewObj()
		if capabilities.Roots.ListChanged {
			roots.Set("listChanged", true)
		}
		obj.Set("roots", roots)
	}
	if capabilities.Sampling != nil {
		obj.Set("sampling", capabilities.Sampling)
	}
	if capabilities.Elicitation != nil {
		obj.Set("elicitation", capabilities.Elicitation)
	}
	return obj
}

func validateInitializeResult(value any) (*InitializeResult, error) {
	obj, isObj := isObject(value)
	invalid := NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid MCP initialize result", nil)
	if !isObj {
		return nil, invalid
	}
	protocolVersion, hasVersion := obj.MustGet("protocolVersion").(string)
	capabilitiesObj, hasCapabilities := obj.MustGet("capabilities").(*jsonx.Obj)
	serverInfoObj, hasServerInfo := obj.MustGet("serverInfo").(*jsonx.Obj)
	if !hasVersion || !hasCapabilities || !hasServerInfo {
		return nil, invalid
	}
	serverName, hasName := serverInfoObj.MustGet("name").(string)
	serverVersion, hasVersionField := serverInfoObj.MustGet("version").(string)
	if !hasName || !hasVersionField {
		return nil, invalid
	}
	if instructions, hasInstructions := obj.Get("instructions"); hasInstructions {
		if _, isString := instructions.(string); !isString {
			return nil, invalid
		}
	}
	return &InitializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    parseServerCapabilities(capabilitiesObj),
		ServerInfo: Implementation{
			Name:    serverName,
			Version: serverVersion,
			Title:   optionalString(serverInfoObj, "title"),
		},
		Instructions: optionalString(obj, "instructions"),
	}, nil
}

func parseServerCapabilities(obj *jsonx.Obj) ServerCapabilities {
	capabilities := ServerCapabilities{}
	if obj == nil {
		return capabilities
	}
	if experimental, ok := obj.MustGet("experimental").(*jsonx.Obj); ok {
		capabilities.Experimental = experimental
	}
	if logging, ok := obj.MustGet("logging").(*jsonx.Obj); ok {
		capabilities.Logging = logging
	}
	if prompts, ok := obj.MustGet("prompts").(*jsonx.Obj); ok {
		capabilities.Prompts = &PromptsCapability{ListChanged: boolField(prompts, "listChanged")}
	}
	if resources, ok := obj.MustGet("resources").(*jsonx.Obj); ok {
		capabilities.Resources = &ResourcesCapability{
			Subscribe:   boolField(resources, "subscribe"),
			ListChanged: boolField(resources, "listChanged"),
		}
	}
	if tools, ok := obj.MustGet("tools").(*jsonx.Obj); ok {
		capabilities.Tools = &ToolsCapability{ListChanged: boolField(tools, "listChanged")}
	}
	if completions, ok := obj.MustGet("completions").(*jsonx.Obj); ok {
		capabilities.Completions = completions
	}
	return capabilities
}

func boolField(obj *jsonx.Obj, key string) bool {
	value, ok := obj.MustGet(key).(bool)
	return ok && value
}

// Request sends one JSON-RPC request and awaits its result.
func (c *McpClient) Request(method string, params *jsonx.Obj, options McpRequestOptions) (any, error) {
	return c.requestInternal(method, params, options, false)
}

// Notify sends one notification.
func (c *McpClient) Notify(method string, params *jsonx.Obj) error {
	return c.notifyInternal(method, params, false)
}

// SetRequestHandler answers server requests with the method; the returned
// disposer removes the handler when it is still current.
func (c *McpClient) SetRequestHandler(method string, handler RequestHandler) func() {
	entry := &handlerEntry{fn: handler}
	c.mu.Lock()
	c.requestHandlers[method] = entry
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.requestHandlers[method] == entry {
			delete(c.requestHandlers, method)
		}
	}
}

// OnNotification subscribes to one notification method; the returned
// disposer removes the listener.
func (c *McpClient) OnNotification(method string, listener func(params any)) func() {
	entry := &notificationEntry{fn: listener}
	c.mu.Lock()
	c.notificationListeners[method] = append(c.notificationListeners[method], entry)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		listeners := c.notificationListeners[method]
		updated := listeners[:0]
		for _, candidate := range listeners {
			if candidate != entry {
				updated = append(updated, candidate)
			}
		}
		c.notificationListeners[method] = updated
	}
}

// OnError subscribes to client errors; the returned disposer removes the
// listener.
func (c *McpClient) OnError(listener func(err error)) func() {
	entry := &errorEntry{fn: listener}
	c.mu.Lock()
	c.errorListeners = append(c.errorListeners, entry)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		updated := c.errorListeners[:0]
		for _, candidate := range c.errorListeners {
			if candidate != entry {
				updated = append(updated, candidate)
			}
		}
		c.errorListeners = updated
	}
}

// OnClose runs once when the connection closes, whether the transport
// dropped or Close was called; the returned disposer removes the listener.
func (c *McpClient) OnClose(listener func()) func() {
	entry := &closeEntry{fn: listener}
	c.mu.Lock()
	c.closeListeners = append(c.closeListeners, entry)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		updated := c.closeListeners[:0]
		for _, candidate := range c.closeListeners {
			if candidate != entry {
				updated = append(updated, candidate)
			}
		}
		c.closeListeners = updated
	}
}

// Ping checks the connection.
func (c *McpClient) Ping(options McpRequestOptions) error {
	_, err := c.Request("ping", nil, options)
	return err
}

// ListTools returns every tool, following nextCursor through all pages.
func (c *McpClient) ListTools(options McpRequestOptions) ([]Tool, error) {
	items, err := c.listAll("tools/list", "tools", isToolItem, options)
	if err != nil {
		return nil, err
	}
	tools := make([]Tool, 0, len(items))
	for _, item := range items {
		tool, _ := ParseTool(item)
		tools = append(tools, tool)
	}
	return tools, nil
}

// ListResources returns every resource, following nextCursor through all
// pages.
func (c *McpClient) ListResources(options McpRequestOptions) ([]Resource, error) {
	items, err := c.listAll("resources/list", "resources", isResourceItem, options)
	if err != nil {
		return nil, err
	}
	resources := make([]Resource, 0, len(items))
	for _, item := range items {
		resource, _ := parseResource(item)
		resources = append(resources, ToResource(resource))
	}
	return resources, nil
}

// ListResourcesPage lists one page of resources, starting at cursor.
func (c *McpClient) ListResourcesPage(cursor string, options McpRequestOptions) (*ListResourcesResult, error) {
	page, err := c.listPage("resources/list", "resources", isResourceItem, cursor, options)
	if err != nil {
		return nil, err
	}
	result := &ListResourcesResult{NextCursor: page.nextCursor}
	for _, item := range page.items {
		resource, _ := parseResource(item)
		result.Resources = append(result.Resources, ToResource(resource))
	}
	return result, nil
}

// ListResourceTemplates returns every resource template, following
// nextCursor through all pages.
func (c *McpClient) ListResourceTemplates(options McpRequestOptions) ([]ResourceTemplate, error) {
	items, err := c.listAll("resources/templates/list", "resourceTemplates", isResourceTemplateItem, options)
	if err != nil {
		return nil, err
	}
	templates := make([]ResourceTemplate, 0, len(items))
	for _, item := range items {
		template, _ := parseResourceTemplate(item)
		templates = append(templates, ToResourceTemplate(template))
	}
	return templates, nil
}

// ListResourceTemplatesPage lists one page of resource templates, starting
// at cursor.
func (c *McpClient) ListResourceTemplatesPage(cursor string, options McpRequestOptions) (*ListResourceTemplatesResult, error) {
	page, err := c.listPage("resources/templates/list", "resourceTemplates", isResourceTemplateItem, cursor, options)
	if err != nil {
		return nil, err
	}
	result := &ListResourceTemplatesResult{NextCursor: page.nextCursor}
	for _, item := range page.items {
		template, _ := parseResourceTemplate(item)
		result.ResourceTemplates = append(result.ResourceTemplates, ToResourceTemplate(template))
	}
	return result, nil
}

// ReadResource reads one resource.
func (c *McpClient) ReadResource(uri string, options McpRequestOptions) (*ReadResourceResult, error) {
	params := jsonx.ObjFrom("uri", uri)
	answer, err := c.Request("resources/read", params, options)
	if err != nil {
		return nil, err
	}
	return validateReadResourceResult(answer)
}

// CallTool invokes one tool.
func (c *McpClient) CallTool(name string, args *jsonx.Obj, options McpRequestOptions) (*CallToolResult, error) {
	params := jsonx.ObjFrom("name", name)
	if args != nil {
		params.Set("arguments", args)
	}
	answer, err := c.Request("tools/call", params, options)
	if err != nil {
		return nil, err
	}
	return ParseCallToolResult(answer)
}

func isToolItem(value any) bool {
	tool, ok := ParseTool(value)
	return ok && tool.Name != "" && tool.InputSchema != nil
}

func isResourceItem(value any) bool {
	_, ok := parseResource(value)
	return ok
}

func isResourceTemplateItem(value any) bool {
	_, ok := parseResourceTemplate(value)
	return ok
}

type listPageResult struct {
	items      []*jsonx.Obj
	nextCursor string
}

func (c *McpClient) listPage(method, key string, isItem func(any) bool, cursor string, options McpRequestOptions) (*listPageResult, error) {
	var params *jsonx.Obj
	if cursor != "" {
		params = jsonx.ObjFrom("cursor", cursor)
	}
	answer, err := c.Request(method, params, options)
	if err != nil {
		return nil, err
	}
	return validateListPage(method, key, answer, isItem)
}

// listAll returns every item of a paginated list method.
func (c *McpClient) listAll(method, key string, isItem func(any) bool, options McpRequestOptions) ([]*jsonx.Obj, error) {
	items := []*jsonx.Obj{}
	cursors := map[string]bool{}
	cursor := ""
	for pageNumber := 0; pageNumber < maxListPages; pageNumber++ {
		page, err := c.listPage(method, key, isItem, cursor, options)
		if err != nil {
			return nil, err
		}
		items = append(items, page.items...)
		if page.nextCursor == "" {
			return items, nil
		}
		if cursors[page.nextCursor] {
			return nil, errors.New("MCP " + method + " returned duplicate cursor: " + page.nextCursor)
		}
		cursors[page.nextCursor] = true
		cursor = page.nextCursor
	}
	return nil, errors.New("MCP " + method + " exceeded " + itoa(maxListPages) + " pages")
}

// validateListPage checks one page of a paginated list: the items under
// key, each checked by isItem.
func validateListPage(method, key string, value any, isItem func(any) bool) (*listPageResult, error) {
	obj, isObj := isObject(value)
	invalid := NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid MCP "+method+" result", nil)
	if !isObj {
		return nil, invalid
	}
	rawItems, hasItems := obj.MustGet(key).([]any)
	if !hasItems {
		return nil, invalid
	}
	page := &listPageResult{}
	for _, item := range rawItems {
		itemObj, ok := isObject(item)
		if !ok || !isItem(itemObj) {
			return nil, NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid entry in MCP "+method+" result", nil)
		}
		page.items = append(page.items, itemObj)
	}
	// Some servers end pagination with null or "" instead of omitting the
	// cursor.
	if next, hasNext := obj.Get("nextCursor"); hasNext {
		text, isString := next.(string)
		if next == nil || (isString && text == "") {
			return page, nil
		}
		if !isString {
			return nil, NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid MCP "+method+" cursor", nil)
		}
		page.nextCursor = text
	}
	return page, nil
}

func validateReadResourceResult(value any) (*ReadResourceResult, error) {
	obj, isObj := isObject(value)
	if !isObj {
		return nil, NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid MCP resources/read result", nil)
	}
	rawContents, hasContents := obj.MustGet("contents").([]any)
	if !hasContents {
		return nil, NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid MCP resources/read result", nil)
	}
	result := &ReadResourceResult{}
	for _, item := range rawContents {
		contentsObj, ok := isObject(item)
		if !ok {
			return nil, NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid contents in MCP resources/read result", nil)
		}
		contents, ok := parseResourceContents(contentsObj)
		if !ok {
			return nil, NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid contents in MCP resources/read result", nil)
		}
		result.Contents = append(result.Contents, contents)
	}
	return result, nil
}

// Close releases the connection; pending requests fail with
// McpConnectionClosedError.
func (c *McpClient) Close() error {
	c.mu.Lock()
	transport := c.transport
	c.transport = nil
	c.mu.Unlock()
	c.disposeTransportListeners()
	c.markClosed(NewMcpConnectionClosedError(""))
	if transport != nil {
		return transport.Close()
	}
	return nil
}

func (c *McpClient) requestInternal(method string, params *jsonx.Obj, options McpRequestOptions, allowConnecting bool) (any, error) {
	transport, err := c.requireTransport(allowConnecting)
	if err != nil {
		return nil, err
	}
	if options.Signal != nil && options.Signal.Aborted() {
		return nil, &McpAbortError{}
	}
	c.mu.Lock()
	id := NewNumberID(float64(c.nextRequestID))
	c.nextRequestID++
	var progressToken *JsonRpcID
	if options.OnProgress != nil {
		token := id
		progressToken = &token
	}
	messageParams := params
	if progressToken != nil {
		messageParams = jsonx.NewObj()
		if params != nil {
			for _, entry := range params.Entries() {
				messageParams.Set(entry[0].(string), entry[1])
			}
		}
		meta := jsonx.NewObj()
		if params != nil {
			if existing, ok := params.MustGet("_meta").(*jsonx.Obj); ok {
				for _, entry := range existing.Entries() {
					meta.Set(entry[0].(string), entry[1])
				}
			}
		}
		meta.Set("progressToken", progressToken.Value())
		messageParams.Set("_meta", meta)
	}
	message := NewRequest(id, method, messageParams)
	timeoutMs := c.Options.RequestTimeoutMs
	if options.TimeoutMs != 0 {
		timeoutMs = options.TimeoutMs
	}
	if timeoutMs == 0 {
		timeoutMs = defaultRequestTimeoutMs
	}
	entry := &pendingRequest{
		done:          make(chan pendingResult, 1),
		timeoutMs:     timeoutMs,
		signal:        options.Signal,
		cancellable:   method != "initialize",
		onProgress:    options.OnProgress,
		progressToken: progressToken,
	}
	c.pending[id] = entry
	if progressToken != nil {
		c.progressRequests[*progressToken] = id
	}
	c.mu.Unlock()

	if entry.signal != nil {
		requestID := id
		requestMethod := method
		entry.removeAbort = entry.signal.OnAbort(func() {
			c.cancelPending(requestID, &McpAbortError{}, requestMethod != "initialize", abortReasonText(entry.signal))
		})
		if entry.signal.Aborted() {
			c.removePending(requestID, entry)
			return nil, &McpAbortError{}
		}
	}
	c.armTimeout(id, entry)
	if sendErr := transport.Send(message); sendErr != nil {
		c.cancelPending(id, sendErr, false, "")
	}
	result := <-entry.done
	return result.value, result.err
}

func abortReasonText(signal *abort.Signal) string {
	if signal == nil {
		return "Aborted"
	}
	switch reason := signal.Reason().(type) {
	case nil:
		return "Aborted"
	case string:
		if reason == "" {
			return "Aborted"
		}
		return reason
	case error:
		if reason.Error() == "" {
			return "Aborted"
		}
		return reason.Error()
	}
	return "Aborted"
}

func (c *McpClient) notifyInternal(method string, params *jsonx.Obj, allowConnecting bool) error {
	transport, err := c.requireTransport(allowConnecting)
	if err != nil {
		return err
	}
	return transport.Send(NewNotification(method, params))
}

func (c *McpClient) requireTransport(allowConnecting bool) (McpTransport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transport != nil && (c.state == ClientConnected || (allowConnecting && c.state == ClientConnecting)) {
		return c.transport, nil
	}
	return nil, NewMcpConnectionClosedError("MCP client is " + string(c.state))
}

func (c *McpClient) handleMessage(message *JsonRpcMessage) {
	switch message.Kind {
	case KindSuccessResponse, KindErrorResponse:
		c.handleResponse(message)
	case KindRequest:
		go c.handleRequest(message)
	case KindNotification:
		c.handleNotification(message.Method(), message.Params())
	default:
		c.EmitError(NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Received invalid JSON-RPC message", nil))
	}
}

func (c *McpClient) handleResponse(message *JsonRpcMessage) {
	id := message.ID()
	c.mu.Lock()
	entry := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if entry == nil {
		c.EmitError(errors.New("Received response for unknown MCP request " + id.String()))
		return
	}
	c.teardownPending(id, entry)
	if message.Kind == KindErrorResponse {
		errorObject := message.ErrorObject()
		entry.done <- pendingResult{err: NewMcpError(errorObject.Code, errorObject.Message, errorObject.Data)}
	} else {
		entry.done <- pendingResult{value: message.Result()}
	}
}

func (c *McpClient) handleRequest(message *JsonRpcMessage) {
	c.mu.Lock()
	transport := c.transport
	entry := c.requestHandlers[message.Method()]
	c.mu.Unlock()
	if transport == nil {
		return
	}
	if entry == nil {
		if err := transport.Send(NewErrorResponse(message.ID(), JSON_RPC_ERROR_CODES.MethodNotFound,
			"Method not found: "+message.Method(), nil)); err != nil {
			c.EmitError(err)
		}
		return
	}
	controller := abort.NewController()
	id := message.ID()
	c.mu.Lock()
	c.incoming[id] = controller
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.incoming, id)
		c.mu.Unlock()
	}()
	result, err := entry.fn(message.Params(), controller.Signal())
	if err != nil {
		responseCode := JSON_RPC_ERROR_CODES.InternalError
		responseMessage := err.Error()
		var responseData any
		var mcpErr *McpError
		if errors.As(err, &mcpErr) {
			responseCode, responseData = mcpErr.Code, mcpErr.Data
		}
		if sendErr := transport.Send(NewErrorResponse(id, responseCode, responseMessage, responseData)); sendErr != nil {
			c.EmitError(sendErr)
		}
		return
	}
	if result == nil {
		result = jsonx.NewObj()
	}
	if sendErr := transport.Send(NewSuccessResponse(id, result)); sendErr != nil {
		c.EmitError(sendErr)
	}
}

func (c *McpClient) handleNotification(method string, params any) {
	if method == "notifications/progress" {
		c.handleProgress(params)
	} else if method == "notifications/cancelled" {
		c.handleCancelled(params)
	}
	c.mu.Lock()
	listeners := append([]*notificationEntry(nil), c.notificationListeners[method]...)
	c.mu.Unlock()
	for _, listener := range listeners {
		safeCall(func() { listener.fn(params) }, c.EmitError)
	}
}

func (c *McpClient) handleProgress(params any) {
	obj, ok := isObject(params)
	if !ok {
		return
	}
	token, hasToken := isJsonRpcID(mustGet(obj, "progressToken"))
	progress, hasProgress := obj.MustGet("progress").(float64)
	if !hasToken || !hasProgress || math.IsNaN(progress) {
		return
	}
	c.mu.Lock()
	requestID, hasRequest := c.progressRequests[token]
	entry := c.pending[requestID]
	c.mu.Unlock()
	if !hasRequest || entry == nil {
		return
	}
	c.armTimeout(requestID, entry)
	if entry.onProgress != nil {
		notification := ProgressNotification{
			ProgressToken: token,
			Progress:      progress,
			Message:       optionalString(obj, "message"),
		}
		notification.HasMessage = obj.Has("message")
		if total, hasTotal := obj.MustGet("total").(float64); hasTotal {
			notification.HasTotal = true
			notification.Total = total
		}
		safeCall(func() { entry.onProgress(notification) }, c.EmitError)
	}
}

func (c *McpClient) handleCancelled(params any) {
	obj, ok := isObject(params)
	if !ok {
		return
	}
	requestID, hasID := isJsonRpcID(mustGet(obj, "requestId"))
	if !hasID {
		return
	}
	c.mu.Lock()
	controller := c.incoming[requestID]
	c.mu.Unlock()
	if controller != nil {
		reason := optionalString(obj, "reason")
		if reason == "" {
			controller.Abort()
		} else {
			controller.AbortReason(reason)
		}
	}
}

func (c *McpClient) armTimeout(id JsonRpcID, entry *pendingRequest) {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.timer != nil {
		entry.timer.Stop()
	}
	if entry.timeoutMs <= 0 {
		return
	}
	timeoutMs := entry.timeoutMs
	entry.timer = time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
		c.cancelPending(id, &McpTimeoutError{TimeoutMs: timeoutMs}, entry.cancellable, "Request timed out")
	})
}

func (c *McpClient) cancelPending(id JsonRpcID, err error, notifyServer bool, reason string) {
	c.mu.Lock()
	entry := c.pending[id]
	delete(c.pending, id)
	var transport McpTransport
	if notifyServer {
		transport = c.transport
	}
	c.mu.Unlock()
	if entry == nil {
		return
	}
	c.teardownPending(id, entry)
	entry.done <- pendingResult{err: err}
	if notifyServer && transport != nil {
		params := jsonx.ObjFrom("requestId", id.Value())
		if reason != "" {
			params.Set("reason", reason)
		}
		if sendErr := transport.Send(NewNotification("notifications/cancelled", params)); sendErr != nil {
			c.EmitError(sendErr)
		}
	}
}

func (c *McpClient) teardownPending(id JsonRpcID, entry *pendingRequest) {
	entry.mu.Lock()
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
	entry.mu.Unlock()
	if entry.removeAbort != nil {
		entry.removeAbort()
	}
	c.mu.Lock()
	if entry.progressToken != nil {
		delete(c.progressRequests, *entry.progressToken)
	}
	c.mu.Unlock()
}

func (c *McpClient) removePending(id JsonRpcID, entry *pendingRequest) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
	c.teardownPending(id, entry)
}

func (c *McpClient) rejectPending(err error) {
	c.mu.Lock()
	entries := make([]*pendingRequest, 0, len(c.pending))
	for id, entry := range c.pending {
		entries = append(entries, entry)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	for _, entry := range entries {
		entry.done <- pendingResult{err: err}
	}
}

func (c *McpClient) handleTransportClose() {
	c.markClosed(NewMcpConnectionClosedError(""))
}

// markClosed is idempotent: rejects in-flight requests, aborts server
// requests we are serving, and flips the state.
func (c *McpClient) markClosed(err error) {
	c.mu.Lock()
	wasClosed := c.state == ClientClosed
	c.state = ClientClosed
	controllers := make([]*abort.Controller, 0, len(c.incoming))
	for _, controller := range c.incoming {
		controllers = append(controllers, controller)
	}
	c.incoming = map[JsonRpcID]*abort.Controller{}
	c.mu.Unlock()
	c.rejectPending(err)
	for _, controller := range controllers {
		controller.AbortReason(err)
	}
	if wasClosed {
		return
	}
	c.mu.Lock()
	listeners := append([]*closeEntry(nil), c.closeListeners...)
	c.mu.Unlock()
	for _, listener := range listeners {
		safeCall(listener.fn, c.EmitError)
	}
}

// EmitError reports an error to the registered listeners.
func (c *McpClient) EmitError(err error) {
	if err == nil {
		err = toError(nil)
	}
	c.mu.Lock()
	listeners := append([]*errorEntry(nil), c.errorListeners...)
	c.mu.Unlock()
	for _, listener := range listeners {
		listener.fn(err)
	}
}

func (c *McpClient) disposeTransportListeners() {
	c.mu.Lock()
	disposers := c.disposers
	c.disposers = nil
	c.mu.Unlock()
	for _, dispose := range disposers {
		dispose()
	}
}

// safeCall runs fn, reporting panics to onError like the reference's
// try/catch around listeners.
func safeCall(fn func(), onError func(err error)) {
	defer func() {
		if recovered := recover(); recovered != nil {
			onError(toError(recovered))
		}
	}()
	fn()
}
