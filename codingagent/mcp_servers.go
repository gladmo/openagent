package codingagent

// Port of core/mcp-servers.ts at reference commit a276dabe: MCP server
// configuration validation and the servers extensions register with
// pi.registerMcpServer(). The core only validates and stores
// registrations; the MCP extension (built in, or another extension that
// handles mcp_servers_change) connects them next to the servers from
// mcp.json.

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/mcp"
)

// McpExposure controls how a server's tools reach the model.
//
//   - McpExposureCodemode: tools are callable from codemode scripts but
//     neither declared to the model nor listed in the codemode
//     description, which lists only the server's namespace. Scripts find
//     them with searchTools(). "codemode-deferred" is accepted as an
//     alias.
//   - McpExposureDeferred: not declared to the model until the
//     tool_search tool loads them; the model then calls them directly.
//     Does not need codemode.
//   - McpExposureDirect: tools are declared to the model like any other
//     tool (and callable from codemode).
//   - McpExposureHidden: tools are registered but unreachable.
type McpExposure string

const (
	McpExposureCodemode McpExposure = "codemode"
	McpExposureDeferred McpExposure = "deferred"
	McpExposureDirect   McpExposure = "direct"
	McpExposureHidden   McpExposure = "hidden"
)

// mcpExposures is the closed set of exposure names.
var mcpExposures = []McpExposure{McpExposureCodemode, McpExposureDeferred, McpExposureDirect, McpExposureHidden}

// mcpExposureAliases maps older exposure names to their current name.
var mcpExposureAliases = map[string]McpExposure{"codemode-deferred": McpExposureCodemode}

var mcpLoopbackHosts = []string{"localhost", "127.0.0.1", "[::1]"}

// McpServerConfig is one validated server entry of the mcpServers shape.
// Type is "stdio" or "http"; Raw keeps the whole entry so unknown fields
// survive and edits rewrite only the keys they own.
type McpServerConfig struct {
	Type string
	Raw  *jsonx.Obj
}

// IsHTTP reports whether the server is a streamable HTTP server.
func (c *McpServerConfig) IsHTTP() bool { return c.Type == "http" }

// IsStdio reports whether the server is a stdio child process.
func (c *McpServerConfig) IsStdio() bool { return c.Type == "stdio" }

// Exposure returns the server-level exposure; the default is codemode.
func (c *McpServerConfig) Exposure() McpExposure {
	if value, ok := c.Raw.MustGet("exposure").(string); ok {
		if exposure, valid := exposureValue(value); valid {
			return exposure
		}
	}
	return McpExposureCodemode
}

// Enabled reports whether the entry connects; entries default to enabled.
func (c *McpServerConfig) Enabled() bool {
	enabled, has := c.Raw.MustGet("enabled").(bool)
	return !has || enabled
}

// Description returns the configured description ("" when absent).
func (c *McpServerConfig) Description() string {
	text, _ := c.Raw.MustGet("description").(string)
	return text
}

// ToolExposure returns the per-tool exposure overrides in insertion
// order.
func (c *McpServerConfig) ToolExposure() [][2]string {
	overrides, ok := c.Raw.MustGet("toolExposure").(*jsonx.Obj)
	if !ok {
		return nil
	}
	var entries [][2]string
	for _, entry := range overrides.Entries() {
		tool, _ := entry[0].(string)
		exposure, _ := entry[1].(string)
		entries = append(entries, [2]string{tool, exposure})
	}
	return entries
}

// TimeoutSeconds returns the per-request timeout; the default is 60.
func (c *McpServerConfig) TimeoutSeconds() float64 {
	seconds, ok := c.Raw.MustGet("timeout").(float64)
	if !ok || seconds <= 0 {
		return 60
	}
	return seconds
}

// URL returns the http server URL ("" for stdio servers).
func (c *McpServerConfig) URL() string {
	text, _ := c.Raw.MustGet("url").(string)
	return text
}

// OAuth returns the http server's OAuth settings object, or nil.
func (c *McpServerConfig) OAuth() *jsonx.Obj {
	obj, _ := c.Raw.MustGet("oauth").(*jsonx.Obj)
	return obj
}

// AuthProvider returns the configured auth.provider login ("" when the
// server does not authenticate with a provider login).
func (c *McpServerConfig) AuthProvider() string {
	auth, ok := c.Raw.MustGet("auth").(*jsonx.Obj)
	if !ok {
		return ""
	}
	provider, _ := auth.MustGet("provider").(string)
	return provider
}

// Command returns the stdio server's command ("" for http servers).
func (c *McpServerConfig) Command() string {
	text, _ := c.Raw.MustGet("command").(string)
	return text
}

// Args returns the stdio server's arguments.
func (c *McpServerConfig) Args() []string {
	list, ok := c.Raw.MustGet("args").([]any)
	if !ok {
		return nil
	}
	var args []string
	for _, item := range list {
		text, _ := item.(string)
		args = append(args, text)
	}
	return args
}

// Env returns the stdio server's environment overrides.
func (c *McpServerConfig) Env() map[string]string {
	obj, ok := c.Raw.MustGet("env").(*jsonx.Obj)
	if !ok {
		return nil
	}
	env := map[string]string{}
	for _, entry := range obj.Entries() {
		key, _ := entry[0].(string)
		value, _ := entry[1].(string)
		env[key] = value
	}
	return env
}

// Cwd returns the stdio server's working directory ("" when absent).
func (c *McpServerConfig) Cwd() string {
	text, _ := c.Raw.MustGet("cwd").(string)
	return text
}

// Headers returns the http server's headers (values may reference
// environment variables or commands).
func (c *McpServerConfig) Headers() map[string]string {
	obj, ok := c.Raw.MustGet("headers").(*jsonx.Obj)
	if !ok {
		return nil
	}
	headers := map[string]string{}
	for _, entry := range obj.Entries() {
		key, _ := entry[0].(string)
		value, _ := entry[1].(string)
		headers[key] = value
	}
	return headers
}

// Clone deep-copies the config.
func (c *McpServerConfig) Clone() *McpServerConfig {
	return &McpServerConfig{Type: c.Type, Raw: jsonx.Clone(c.Raw).(*jsonx.Obj)}
}

// IsLoopbackRedirectURI reports whether a redirect URI can be served by
// the loopback callback server.
func IsLoopbackRedirectURI(value string) bool {
	parsed, err := parsePublicURL(value)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	for _, host := range mcpLoopbackHosts {
		if parsed.Hostname() == host || parsed.Host == host {
			return true
		}
	}
	return false
}

// parsePublicURL parses an absolute URL like URL.canParse in the
// reference.
func parsePublicURL(value string) (*url.URL, error) {
	return mcp.ParseURLLoose(value)
}

// McpNamespace returns the namespace of a server's tools:
// mcp__<server> with "-" replaced by "_", like the tool names.
func McpNamespace(server string) string {
	return "mcp__" + strings.ReplaceAll(server, "-", "_")
}

// GetMcpToolExposure returns the exposure of one tool of a server: its
// toolExposure entry, else the server's exposure. An exact name wins over
// patterns; among patterns the first match in the object wins.
func GetMcpToolExposure(config *McpServerConfig, toolName string) McpExposure {
	for _, entry := range config.ToolExposure() {
		if entry[0] == toolName {
			return exposureOrDefault(entry[1])
		}
	}
	for _, entry := range config.ToolExposure() {
		if strings.Contains(entry[0], "*") && mcpToolPatternRegexp(entry[0]).MatchString(toolName) {
			return exposureOrDefault(entry[1])
		}
	}
	return config.Exposure()
}

func exposureOrDefault(value string) McpExposure {
	if exposure, valid := exposureValue(value); valid {
		return exposure
	}
	return McpExposureCodemode
}

func exposureValue(value string) (McpExposure, bool) {
	if resolved, isAlias := mcpExposureAliases[value]; isAlias {
		return resolved, true
	}
	for _, exposure := range mcpExposures {
		if string(exposure) == value {
			return exposure, true
		}
	}
	return "", false
}

// exposureValueAny validates a JSON value as an exposure name.
func exposureValueAny(value any) (McpExposure, bool) {
	text, isString := value.(string)
	if !isString {
		return "", false
	}
	return exposureValue(text)
}

// mcpToolPatternRegexp compiles a toolExposure pattern where * matches
// any characters.
func mcpToolPatternRegexp(pattern string) *regexp.Regexp {
	parts := strings.Split(pattern, "*")
	quoted := make([]string, len(parts))
	for index, part := range parts {
		quoted[index] = regexp.QuoteMeta(part)
	}
	return regexp.MustCompile("^" + strings.Join(quoted, ".*") + "$")
}

func isJSONRecord(value any) (*jsonx.Obj, bool) {
	obj, ok := value.(*jsonx.Obj)
	return obj, ok
}

func isStringRecord(value any) bool {
	obj, ok := value.(*jsonx.Obj)
	if !ok {
		return false
	}
	for _, entry := range obj.Entries() {
		if _, isString := entry[1].(string); !isString {
			return false
		}
	}
	return true
}

// resolveExposureAliases returns a copy of the entry with exposure
// aliases replaced by their current names.
func resolveExposureAliases(raw *jsonx.Obj) *jsonx.Obj {
	resolved := jsonx.Clone(raw).(*jsonx.Obj)
	if value, has := resolved.Get("exposure"); has {
		if text, isString := value.(string); isString {
			if replacement, isAlias := mcpExposureAliases[text]; isAlias {
				resolved.Set("exposure", string(replacement))
			}
		}
	}
	if toolExposure, ok := resolved.MustGet("toolExposure").(*jsonx.Obj); ok {
		replaced := jsonx.NewObj()
		for _, entry := range toolExposure.Entries() {
			key, _ := entry[0].(string)
			value := entry[1]
			if text, isString := value.(string); isString {
				if replacement, isAlias := mcpExposureAliases[text]; isAlias {
					value = string(replacement)
				}
			}
			replaced.Set(key, value)
		}
		resolved.Set("toolExposure", replaced)
	}
	return resolved
}

func validateMcpOAuth(value any) error {
	if value == nil {
		return nil
	}
	oauth, ok := isJSONRecord(value)
	if !ok {
		return fmt.Errorf("oauth must be an object")
	}
	if clientID, has := oauth.Get("clientId"); has {
		if _, isString := clientID.(string); !isString {
			return fmt.Errorf("oauth.clientId must be a string")
		}
	}
	if clientSecret, has := oauth.Get("clientSecret"); has {
		if _, isString := clientSecret.(string); !isString {
			return fmt.Errorf("oauth.clientSecret must be a string")
		}
	}
	if port, has := oauth.Get("callbackPort"); has {
		number, isNumber := port.(float64)
		if !isNumber || number != math.Trunc(number) || number < 1 || number > 65535 {
			return fmt.Errorf("oauth.callbackPort must be a port number")
		}
	}
	if callbackURL, has := oauth.Get("callbackUrl"); has {
		text, isString := callbackURL.(string)
		if !isString || !IsLoopbackRedirectURI(text) {
			return fmt.Errorf("oauth.callbackUrl must be an http URI on localhost, 127.0.0.1, or [::1] without query or fragment")
		}
		if parsed, err := parsePublicURL(text); err == nil && parsed.Port() != "" {
			urlPort, portErr := strconv.Atoi(parsed.Port())
			if port, has := oauth.Get("callbackPort"); has && portErr == nil {
				number, _ := port.(float64)
				if number != float64(urlPort) {
					return fmt.Errorf("oauth.callbackUrl and oauth.callbackPort name different ports")
				}
			}
		}
	}
	if scope, has := oauth.Get("scope"); has {
		if _, isString := scope.(string); !isString {
			return fmt.Errorf("oauth.scope must be a string")
		}
	}
	if clientName, has := oauth.Get("clientName"); has {
		text, isString := clientName.(string)
		if !isString || strings.TrimSpace(text) == "" {
			return fmt.Errorf("oauth.clientName must be a non-empty string")
		}
	}
	if registration, has := oauth.Get("clientRegistration"); has && registration != nil {
		text, _ := registration.(string)
		if text != "dcr" {
			if text != "cimd" {
				return fmt.Errorf(`oauth.clientRegistration must be "dcr" or "cimd"`)
			}
			if _, hasClient := oauth.Get("clientId"); hasClient {
				return fmt.Errorf(`oauth.clientRegistration "cimd" cannot be combined with oauth.clientId or oauth.clientName`)
			}
			if _, hasName := oauth.Get("clientName"); hasName {
				return fmt.Errorf(`oauth.clientRegistration "cimd" cannot be combined with oauth.clientId or oauth.clientName`)
			}
			if callback, ok := oauth.MustGet("callbackUrl").(string); ok {
				if parsed, err := parsePublicURL(callback); err == nil {
					if parsed.Hostname() == "[::1]" || parsed.Path != "/callback" {
						return fmt.Errorf(`oauth.clientRegistration "cimd" requires oauth.callbackUrl on localhost or 127.0.0.1 with path /callback`)
					}
				}
			}
		}
	}
	if metadataURL, has := oauth.Get("authServerMetadataUrl"); has && metadataURL != nil {
		text, isString := metadataURL.(string)
		var parsed *url.URL
		var err error
		if isString {
			parsed, err = parsePublicURL(text)
		}
		if err != nil || !isString || parsed == nil {
			return fmt.Errorf("oauth.authServerMetadataUrl must be an https URL, or http on localhost, 127.0.0.1, or [::1]")
		}
		https := parsed.Scheme == "https"
		loopback := false
		for _, host := range mcpLoopbackHosts {
			if parsed.Hostname() == host {
				loopback = true
			}
		}
		if !https && !(parsed.Scheme == "http" && loopback) {
			return fmt.Errorf("oauth.authServerMetadataUrl must be an https URL, or http on localhost, 127.0.0.1, or [::1]")
		}
	}
	return nil
}

var mcpServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidateMcpServerConfig validates one server entry of the mcpServers
// shape. It returns a copy of the config with exposure aliases resolved.
func ValidateMcpServerConfig(name string, raw any) (*McpServerConfig, error) {
	if !mcpServerNamePattern.MatchString(name) {
		return nil, fmt.Errorf("invalid server name %q (use letters, digits, \"_\" and \"-\")", name)
	}
	obj, ok := isJSONRecord(raw)
	if !ok {
		return nil, fmt.Errorf("server %q must be an object", name)
	}
	value := resolveExposureAliases(obj)

	exposureList := make([]string, len(mcpExposures))
	for index, exposure := range mcpExposures {
		exposureList[index] = `"` + string(exposure) + `"`
	}
	exposures := strings.Join(exposureList, ", ")
	if rawExposure, has := value.Get("exposure"); has && rawExposure != nil {
		if _, valid := exposureValueAny(rawExposure); !valid {
			return nil, fmt.Errorf("server %q: exposure must be one of %s", name, exposures)
		}
	}
	if rawToolExposure, has := value.Get("toolExposure"); has && rawToolExposure != nil {
		toolExposure, isRecord := isJSONRecord(rawToolExposure)
		if !isRecord {
			return nil, fmt.Errorf("server %q: toolExposure must map tool names to exposures", name)
		}
		for _, entry := range toolExposure.Entries() {
			tool, _ := entry[0].(string)
			if _, valid := exposureValueAny(entry[1]); !valid {
				return nil, fmt.Errorf("server %q: toolExposure %q must be one of %s", name, tool, exposures)
			}
		}
	}
	if enabled, has := value.Get("enabled"); has && enabled != nil {
		if _, isBool := enabled.(bool); !isBool {
			return nil, fmt.Errorf("server %q: enabled must be a boolean", name)
		}
	}
	if description, has := value.Get("description"); has && description != nil {
		if _, isString := description.(string); !isString {
			return nil, fmt.Errorf("server %q: description must be a string", name)
		}
	}
	if timeout, has := value.Get("timeout"); has && timeout != nil {
		number, isNumber := timeout.(float64)
		if !isNumber || !(number > 0) {
			return nil, fmt.Errorf("server %q: timeout must be a positive number of seconds", name)
		}
	}
	serverType, _ := value.MustGet("type").(string)
	if serverType == "sse" {
		return nil, fmt.Errorf("server %q: legacy SSE transport is not supported; use the streamable HTTP URL", name)
	}

	rawURL, hasURL := value.MustGet("url").(string)
	if hasURL && (serverType == "" || serverType == "http" || serverType == "streamable-http") {
		parsed, err := parsePublicURL(rawURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("server %q: url must be an http or https URL", name)
		}
		if headers, has := value.Get("headers"); has && headers != nil && !isStringRecord(headers) {
			return nil, fmt.Errorf("server %q: headers must map names to strings", name)
		}
		if err := validateMcpOAuth(mustGetJSON(value, "oauth")); err != nil {
			return nil, fmt.Errorf("server %q: %s", name, err.Error())
		}
		if auth, has := value.Get("auth"); has && auth != nil {
			authRecord, isRecord := isJSONRecord(auth)
			provider := ""
			if isRecord {
				provider, _ = authRecord.MustGet("provider").(string)
			}
			if !isRecord || provider == "" {
				return nil, fmt.Errorf("server %q: auth.provider must be a provider name", name)
			}
			parsed, err := parsePublicURL(rawURL)
			if err != nil {
				return nil, fmt.Errorf("server %q: url must be an http or https URL", name)
			}
			https := parsed.Scheme == "https"
			loopback := false
			for _, host := range mcpLoopbackHosts {
				if parsed.Hostname() == host {
					loopback = true
				}
			}
			if !https && !loopback {
				return nil, fmt.Errorf("server %q: auth requires an https URL, or http on localhost, 127.0.0.1, or [::1]", name)
			}
		}
		return &McpServerConfig{Type: "http", Raw: value}, nil
	}
	rawCommand, hasCommand := value.MustGet("command").(string)
	if hasCommand && (serverType == "" || serverType == "stdio") {
		if args, has := value.Get("args"); has && args != nil {
			list, isList := args.([]any)
			if !isList {
				return nil, fmt.Errorf("server %q: args must be an array of strings", name)
			}
			for _, arg := range list {
				if _, isString := arg.(string); !isString {
					return nil, fmt.Errorf("server %q: args must be an array of strings", name)
				}
			}
		}
		if env, has := value.Get("env"); has && env != nil && !isStringRecord(env) {
			return nil, fmt.Errorf("server %q: env must map names to strings", name)
		}
		if cwd, has := value.Get("cwd"); has && cwd != nil {
			if _, isString := cwd.(string); !isString {
				return nil, fmt.Errorf("server %q: cwd must be a string", name)
			}
		}
		return &McpServerConfig{Type: "stdio", Raw: value}, nil
	}
	_ = rawCommand
	return nil, fmt.Errorf("server %q needs either \"command\" (stdio) or \"url\" (streamable HTTP)", name)
}

func mustGetJSON(obj *jsonx.Obj, key string) any {
	value, _ := obj.Get(key)
	return value
}

// RegisteredMcpServer is a server an extension registered with
// pi.registerMcpServer().
type RegisteredMcpServer struct {
	Name   string
	Config *McpServerConfig
	// ExtensionPath is the path of the extension that registered the
	// server.
	ExtensionPath string
}

// McpServerRegistry holds the servers registered by the extensions of one
// runtime.
type McpServerRegistry struct {
	mu             sync.Mutex
	servers        map[string]*RegisteredMcpServer
	order          []string
	changeListener func()
}

// NewMcpServerRegistry builds an empty registry.
func NewMcpServerRegistry() *McpServerRegistry {
	return &McpServerRegistry{servers: map[string]*RegisteredMcpServer{}}
}

// Register adds or replaces a server. The caller checks ownership.
func (r *McpServerRegistry) Register(server *RegisteredMcpServer) {
	r.mu.Lock()
	if _, exists := r.servers[server.Name]; !exists {
		r.order = append(r.order, server.Name)
	}
	r.servers[server.Name] = server
	listener := r.changeListener
	r.mu.Unlock()
	if listener != nil {
		listener()
	}
}

// Unregister removes a server registered by extensionPath. Servers of
// other extensions are left alone.
func (r *McpServerRegistry) Unregister(name, extensionPath string) {
	r.mu.Lock()
	if current, exists := r.servers[name]; !exists || current.ExtensionPath != extensionPath {
		r.mu.Unlock()
		return
	}
	delete(r.servers, name)
	for index, entry := range r.order {
		if entry == name {
			r.order = append(r.order[:index], r.order[index+1:]...)
			break
		}
	}
	listener := r.changeListener
	r.mu.Unlock()
	if listener != nil {
		listener()
	}
}

// Get returns one registered server.
func (r *McpServerRegistry) Get(name string) *RegisteredMcpServer {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.servers[name]
}

// List returns copies of the registered servers, in registration order.
func (r *McpServerRegistry) List() []*RegisteredMcpServer {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := make([]*RegisteredMcpServer, 0, len(r.servers))
	for _, name := range r.order {
		server := r.servers[name]
		list = append(list, &RegisteredMcpServer{
			Name:          server.Name,
			Config:        server.Config.Clone(),
			ExtensionPath: server.ExtensionPath,
		})
	}
	return list
}

// SetChangeListener sets the callback run after every change. The runner
// sets it when it binds, to emit mcp_servers_change.
func (r *McpServerRegistry) SetChangeListener(listener func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changeListener = listener
}
