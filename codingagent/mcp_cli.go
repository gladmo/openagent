package codingagent

// Port of extensions/mcp/cli.ts at reference commit a276dabe: `openagent
// mcp` — add, remove, and check MCP servers and sign in to them outside a
// session. Agents run it through bash to configure servers, verify an
// mcp.json they wrote, and start an OAuth sign-in; the user only approves
// access in the browser. Running sessions pick up new credentials on
// their next turn.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/mcp"
)

func mcpHelpText() string {
	app := AppName()
	configDir := ConfigDirName()
	return fmt.Sprintf(`%sUsage:%s
  %s mcp add <server> [options] -- <command> [args...]
  %s mcp add <server> [options] --url <url>
  %s mcp remove <server> [-l]
  %s mcp list [--json]
  %s mcp login <server> [--timeout <seconds>]
  %s mcp logout <server>

Configure and check MCP servers and sign in to OAuth servers without starting a session.
Reads ~/%s/agent/mcp.json and, in trusted projects, %s/mcp.json.

Commands:
  add <server>            Add or replace a server in mcp.json
  remove <server>         Remove a server from mcp.json
  list                    Show state, tools, and errors (exits 1 on failure)
  login <server>          Sign in through the browser
  logout <server>         Delete the stored OAuth credentials

Options for add and remove:
  -l, --local             Use %s/mcp.json in the current project instead of the global file

Options for add:
  --url <url>             Streamable HTTP server URL (instead of a command)
  --env <KEY=VALUE>       Environment variable for a stdio server (repeatable)
  --cwd <dir>             Working directory for a stdio server
  --header <KEY=VALUE>    HTTP header (repeatable)
  --bearer-token-env-var <NAME>
                          Send "Authorization: Bearer $%s"
  --oauth-client-id <id>  Pre-registered OAuth client id
  --oauth-client-secret <secret>
                          OAuth client secret (may be $%s or !command)
  --oauth-callback-port <port>
                          Fixed OAuth callback port
  --oauth-client-name <name>
                          Client name sent when registering with the OAuth server
  --exposure <mode>       codemode (default), deferred, direct, or hidden
  --description <text>    What the server offers, shown in the system prompt

Other options:
  --json                  Print the list as JSON
  --timeout <seconds>     Time limit for the whole sign-in (default: 300)`,
		"\x1b[1m", "\x1b[22m", app, app, app, app, app, app, configDir, configDir, configDir, "{NAME}", "{NAME}")
}

func mcpHelpHint() string {
	return fmt.Sprintf("\x1b[2mUse \"%s mcp --help\" for usage.\x1b[22m", AppName())
}

const mcpDefaultLoginTimeoutSeconds = 300

// McpCommandOptions tune the mcp command's environment.
type McpCommandOptions struct {
	Cwd      string
	AgentDir string
	// Credentials defaults to mcp-auth.json in the agent directory.
	Credentials *McpOAuthCredentialStore
	// OpenURL defaults to the platform browser.
	OpenURL func(url string)
	// Log defaults to stdout; Error defaults to stderr.
	Log   func(line string)
	Error func(line string)
}

// mcpServerReport is one server's entry in `mcp list --json`.
type mcpServerReport struct {
	Name         string            `json:"name"`
	Scope        string            `json:"scope"`
	Source       string            `json:"source"`
	Override     string            `json:"override,omitempty"`
	Enabled      bool              `json:"enabled"`
	Exposure     string            `json:"exposure"`
	Transport    string            `json:"transport"`
	State        string            `json:"state"`
	Tools        []string          `json:"tools"`
	ToolExposure map[string]string `json:"toolExposure,omitempty"`
	// OffersResources says the server offers resources. They are not
	// listed: listing every one can take long.
	OffersResources bool   `json:"resources,omitempty"`
	Error           string `json:"error,omitempty"`
}

// mcpListReport is the `mcp list --json` payload.
type mcpListReport struct {
	Servers []mcpServerReport `json:"servers"`
	Errors  []string          `json:"errors"`
	Note    string            `json:"note,omitempty"`
}

func mcpDescribeTransport(entry McpServerEntry) string {
	config := entry.Config
	if config.IsHTTP() {
		return config.URL()
	}
	return strings.Join(append([]string{config.Command()}, config.Args()...), " ")
}

func mcpCreateConnection(entry McpServerEntry, options McpCommandOptions, credentials *McpOAuthCredentialStore) *McpServerConnection {
	return NewMcpServerConnection(McpServerConnectionOptions{
		Entry:           entry,
		Cwd:             options.Cwd,
		CreateTransport: CreateDefaultTransport,
		Credentials:     credentials,
		Log:             NewMcpServerLog(filepath.Join(options.AgentDir, "mcp.log")),
	})
}

// mcpOptionAliases maps short spellings; -l/--local match `openagent
// install`.
var mcpOptionAliases = map[string]string{"-l": "--local"}

type mcpParsedOptions struct {
	positional []string
	values     map[string]mcpOptionValue
	lists      map[string][]string
}

type mcpOptionValue struct {
	text  string
	isSet bool
}

// mcpParseOptions parses --name value options; it reports unknown ones.
// `--` ends the options, as does reaching maxPositionals positional
// arguments: the remaining arguments are positional, so a command's own
// options (add <server> <command> --flag) are passed through.
func mcpParseOptions(
	args []string,
	known map[string]string,
	error func(line string),
	maxPositionals int,
) *mcpParsedOptions {
	positional := []string{}
	values := map[string]mcpOptionValue{}
	lists := map[string][]string{}
	for index := 0; index < len(args); index++ {
		arg, isAliased := mcpOptionAliases[args[index]]
		if !isAliased {
			arg = args[index]
		}
		atMax := maxPositionals >= 0 && len(positional) >= maxPositionals
		if arg == "--" || atMax {
			rest := args[index:]
			if arg == "--" {
				rest = args[index+1:]
			}
			positional = append(positional, rest...)
			break
		}
		if !strings.HasPrefix(arg, "--") {
			positional = append(positional, arg)
			continue
		}
		name := arg[2:]
		kind, isKnown := known[name]
		if !isKnown {
			error(fmt.Sprintf("Unknown option %s.\n%s", arg, mcpHelpHint()))
			return nil
		}
		if kind == "flag" {
			values[name] = mcpOptionValue{isSet: true}
			continue
		}
		if index+1 >= len(args) {
			error(fmt.Sprintf("%s needs a value.", arg))
			return nil
		}
		index++
		value := args[index]
		if kind == "list" {
			lists[name] = append(lists[name], value)
		} else {
			values[name] = mcpOptionValue{text: value, isSet: true}
		}
	}
	return &mcpParsedOptions{positional: positional, values: values, lists: lists}
}

// RunMcpCommand runs `openagent mcp <args>` and returns the exit code.
func RunMcpCommand(args []string, options McpCommandOptions) int {
	log := options.Log
	if log == nil {
		log = func(line string) { fmt.Println(line) }
	}
	errorOut := options.Error
	if errorOut == nil {
		errorOut = func(line string) { fmt.Fprintln(os.Stderr, line) }
	}
	var command string
	if len(args) > 0 {
		command = args[0]
	}
	hasHelpFlag := false
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			hasHelpFlag = true
		}
	}
	if command == "" || command == "help" || hasHelpFlag {
		log(mcpHelpText())
		return 0
	}
	rest := args[1:]

	projectConfig := filepath.Join(options.Cwd, ConfigDirName(), "mcp.json")
	if command == "add" || command == "remove" {
		if command == "add" {
			return mcpAdd(rest, projectConfig, options, log, errorOut)
		}
		return mcpRemove(rest, projectConfig, options, log, errorOut)
	}
	projectTrusted := false
	if decision, recorded := NewProjectTrustStore(options.AgentDir).Get(options.Cwd); recorded && decision {
		projectTrusted = true
	}
	loaded := LoadMcpConfig(struct {
		AgentDir       string
		Cwd            string
		ProjectTrusted bool
	}{AgentDir: options.AgentDir, Cwd: options.Cwd, ProjectTrusted: projectTrusted})
	untrustedNote := ""
	if !projectTrusted {
		if _, err := os.Stat(projectConfig); err == nil {
			untrustedNote = fmt.Sprintf(
				"%s is ignored because the project is not trusted. Start %s in the project to trust it.",
				projectConfig, AppName())
		}
	}
	credentials := options.Credentials
	if credentials == nil {
		credentials = NewMcpOAuthCredentialStore()
	}

	switch command {
	case "list":
		parsed := mcpParseOptions(rest, map[string]string{"json": "flag"}, errorOut, -1)
		if parsed == nil {
			return 1
		}
		if len(parsed.positional) > 0 {
			errorOut(fmt.Sprintf("Usage: %s mcp list [--json]\n%s", AppName(), mcpHelpHint()))
			return 1
		}
		return mcpList(loaded, parsed.values["json"].isSet, untrustedNote, options, credentials, log)
	case "login", "logout":
		known := map[string]string{}
		if command == "login" {
			known["timeout"] = "value"
		}
		parsed := mcpParseOptions(rest, known, errorOut, -1)
		if parsed == nil {
			return 1
		}
		name := ""
		extra := []string{}
		if len(parsed.positional) > 0 {
			name = parsed.positional[0]
			extra = parsed.positional[1:]
		}
		if name == "" || len(extra) > 0 {
			errorOut(fmt.Sprintf("Usage: %s mcp %s <server>\n%s", AppName(), command, mcpHelpHint()))
			return 1
		}
		var entry *McpServerEntry
		for index := range loaded.Servers {
			if loaded.Servers[index].Name == name {
				server := loaded.Servers[index]
				entry = &server
				break
			}
		}
		if entry == nil {
			names := make([]string, 0, len(loaded.Servers))
			for _, server := range loaded.Servers {
				names = append(names, server.Name)
			}
			configured := strings.Join(names, ", ")
			if configured == "" {
				configured = "none"
			}
			note := ""
			if untrustedNote != "" {
				note = " " + untrustedNote
			}
			errorOut(fmt.Sprintf("No MCP server named %q.%s Configured: %s.", name, note, configured))
			return 1
		}
		connection := mcpCreateConnection(*entry, options, credentials)
		serverURL := connection.OAuthURL()
		if serverURL == "" {
			errorOut(fmt.Sprintf("MCP server %q does not use OAuth. Only HTTP servers without an Authorization header do.", name))
			return 1
		}
		if command == "logout" {
			removed := credentials.Remove(name, serverURL)
			if removed {
				log(fmt.Sprintf("Signed out of MCP server %q.", name))
			} else {
				log(fmt.Sprintf("No stored credentials for MCP server %q.", name))
			}
			return 0
		}
		timeoutSeconds := mcpDefaultLoginTimeoutSeconds
		if parsed.values["timeout"].isSet {
			parsedTimeout, err := strconv.Atoi(parsed.values["timeout"].text)
			if err != nil || parsedTimeout <= 0 {
				errorOut("--timeout must be a positive number of seconds.")
				return 1
			}
			timeoutSeconds = parsedTimeout
		}
		code := mcpLogin(*entry, connection, serverURL, time.Duration(timeoutSeconds)*time.Second, options, credentials, log, errorOut)
		_ = connection.Close()
		return code
	default:
		errorOut(fmt.Sprintf("Unknown mcp command %q.\n%s", command, mcpHelpHint()))
		return 1
	}
}

// mcpParsePairs parses KEY=VALUE pairs of a repeatable option.
func mcpParsePairs(option string, pairs []string, error func(line string)) (map[string]string, bool) {
	record := map[string]string{}
	for _, pair := range pairs {
		separator := strings.Index(pair, "=")
		if separator <= 0 {
			error(fmt.Sprintf("--%s expects KEY=VALUE, got %q.", option, pair))
			return nil, false
		}
		record[pair[:separator]] = pair[separator+1:]
	}
	return record, true
}

func mcpAdd(args []string, projectConfig string, options McpCommandOptions, log func(string), errorOut func(string)) int {
	usage := fmt.Sprintf("Usage: %s mcp add <server> [options] (--url <url> | -- <command> [args...])\n%s", AppName(), mcpHelpHint())
	parsed := mcpParseOptions(args, map[string]string{
		"local":                "flag",
		"url":                  "value",
		"env":                  "list",
		"cwd":                  "value",
		"header":               "list",
		"bearer-token-env-var": "value",
		"oauth-client-id":      "value",
		"oauth-client-secret":  "value",
		"oauth-callback-port":  "value",
		"oauth-client-name":    "value",
		"exposure":             "value",
		"description":          "value",
	}, errorOut, 2)
	if parsed == nil {
		return 1
	}
	name := ""
	command := []string{}
	if len(parsed.positional) > 0 {
		name = parsed.positional[0]
		command = parsed.positional[1:]
	}
	url := parsed.values["url"]
	if name == "" || url.isSet == (len(command) > 0) {
		errorOut(usage)
		return 1
	}
	value := func(option string) (string, bool) {
		return parsed.values[option].text, parsed.values[option].isSet
	}
	exposure, hasExposure := value("exposure")
	httpOnly := []string{"header", "bearer-token-env-var", "oauth-client-id", "oauth-client-secret", "oauth-callback-port", "oauth-client-name"}
	stdioOnly := []string{"env", "cwd"}
	misplacedOptions := stdioOnly
	if !url.isSet {
		misplacedOptions = httpOnly
	}
	var misplaced string
	for _, option := range misplacedOptions {
		if parsed.values[option].isSet || len(parsed.lists[option]) > 0 {
			misplaced = option
			break
		}
	}
	if misplaced != "" {
		kind := "stdio servers"
		if !url.isSet {
			kind = "HTTP servers (--url)"
		}
		errorOut(fmt.Sprintf("--%s only applies to %s.", misplaced, kind))
		return 1
	}

	var config *jsonx.Obj
	if url.isSet {
		headers, ok := mcpParsePairs("header", parsed.lists["header"], errorOut)
		if !ok {
			return 1
		}
		if bearer, hasBearer := value("bearer-token-env-var"); hasBearer {
			headers["Authorization"] = "Bearer ${" + bearer + "}"
		}
		oauth := jsonx.NewObj()
		if clientID, has := value("oauth-client-id"); has {
			oauth.Set("clientId", clientID)
		}
		if clientSecret, has := value("oauth-client-secret"); has {
			oauth.Set("clientSecret", clientSecret)
		}
		if port, has := value("oauth-callback-port"); has {
			if number, err := strconv.ParseFloat(port, 64); err == nil {
				oauth.Set("callbackPort", number)
			}
		}
		if clientName, has := value("oauth-client-name"); has {
			oauth.Set("clientName", clientName)
		}
		config = jsonx.ObjFrom("url", url.text)
		if len(headers) > 0 {
			headersObj := jsonx.NewObj()
			for header, headerValue := range headers {
				headersObj.Set(header, headerValue)
			}
			config.Set("headers", headersObj)
		}
		if oauth.Len() > 0 {
			config.Set("oauth", oauth)
		}
	} else {
		env, ok := mcpParsePairs("env", parsed.lists["env"], errorOut)
		if !ok {
			return 1
		}
		executable := ""
		commandArgs := []string{}
		if len(command) > 0 {
			executable = command[0]
			commandArgs = command[1:]
		}
		config = jsonx.ObjFrom("command", executable)
		if len(commandArgs) > 0 {
			argsList := make([]any, 0, len(commandArgs))
			for _, arg := range commandArgs {
				argsList = append(argsList, arg)
			}
			config.Set("args", argsList)
		}
		if len(env) > 0 {
			envObj := jsonx.NewObj()
			for key, envValue := range env {
				envObj.Set(key, envValue)
			}
			config.Set("env", envObj)
		}
		if workingDir, has := value("cwd"); has {
			config.Set("cwd", workingDir)
		}
	}
	if hasExposure {
		config.Set("exposure", exposure)
	}
	if description, has := value("description"); has {
		config.Set("description", description)
	}
	validated, validateErr := ValidateMcpServerConfig(name, config)
	if validateErr != nil {
		errorOut(validateErr.Error())
		return 1
	}

	project := parsed.values["local"].isSet
	path := filepath.Join(options.AgentDir, "mcp.json")
	scope := "global"
	if project {
		path = projectConfig
		scope = "project"
	}
	replaced, addErr := AddMcpServerConfig(path, name, validated)
	if addErr != nil {
		errorOut(fmt.Sprintf("Could not update %s: %s", path, addErr.Error()))
		return 1
	}
	action := "Added"
	if replaced {
		action = "Replaced"
	}
	log(fmt.Sprintf("%s %s MCP server %q in %s.", action, scope, name, path))
	if project {
		if decision, recorded := NewProjectTrustStore(options.AgentDir).Get(options.Cwd); !recorded || !decision {
			log(fmt.Sprintf("The project is not trusted, so %s is ignored until you start %s in the project and trust it.", path, AppName()))
		}
	}
	// HTTP servers without an Authorization header may use OAuth.
	mayNeedSignIn := validated.IsHTTP() && !validatedHasAuthorization(validated)
	loginHint := ""
	if mayNeedSignIn {
		loginHint = fmt.Sprintf(". If it requires sign-in: %s mcp login %s", AppName(), name)
	}
	log(fmt.Sprintf("Check it with: %s mcp list%s", AppName(), loginHint))
	return 0
}

func validatedHasAuthorization(config *McpServerConfig) bool {
	for header := range config.Headers() {
		if strings.EqualFold(header, "authorization") {
			return true
		}
	}
	return false
}

func mcpRemove(args []string, projectConfig string, options McpCommandOptions, log func(string), errorOut func(string)) int {
	parsed := mcpParseOptions(args, map[string]string{"local": "flag"}, errorOut, -1)
	if parsed == nil {
		return 1
	}
	name := ""
	extra := []string{}
	if len(parsed.positional) > 0 {
		name = parsed.positional[0]
		extra = parsed.positional[1:]
	}
	if name == "" || len(extra) > 0 {
		errorOut(fmt.Sprintf("Usage: %s mcp remove <server> [-l]\n%s", AppName(), mcpHelpHint()))
		return 1
	}
	project := parsed.values["local"].isSet
	path := filepath.Join(options.AgentDir, "mcp.json")
	scope := "global"
	if project {
		path = projectConfig
		scope = "project"
	}
	removed, removeErr := RemoveMcpServerConfig(path, name)
	if removeErr != nil {
		errorOut(fmt.Sprintf("Could not update %s: %s", path, removeErr.Error()))
		return 1
	}
	if removed {
		log(fmt.Sprintf("Removed %s MCP server %q from %s.", scope, name, path))
		return 0
	}
	var other *McpServerEntry
	for _, server := range LoadMcpConfig(struct {
		AgentDir       string
		Cwd            string
		ProjectTrusted bool
	}{AgentDir: options.AgentDir, Cwd: options.Cwd, ProjectTrusted: true}).Servers {
		if server.Name == name && server.Scope != scope {
			entry := server
			other = &entry
			break
		}
	}
	hint := ""
	if other != nil {
		if other.Scope == "project" {
			hint = "; use --local"
		} else {
			hint = "; omit --local"
		}
		hint = fmt.Sprintf(" It is defined in %s%s.", other.Source, hint)
	}
	errorOut(fmt.Sprintf("No %s MCP server named %q in %s.%s", scope, name, path, hint))
	return 1
}

func mcpList(loaded *LoadedMcpConfig, jsonOutput bool, untrustedNote string, options McpCommandOptions, credentials *McpOAuthCredentialStore, log func(string)) int {
	report := &mcpListReport{Errors: loaded.Errors, Servers: []mcpServerReport{}}
	if untrustedNote != "" {
		report.Note = untrustedNote
	}
	failed := len(loaded.Errors) > 0
	for _, entry := range loaded.Servers {
		server := mcpListOne(entry, options, credentials)
		report.Servers = append(report.Servers, server)
		if server.Enabled && server.State != string(McpServerConnected) {
			failed = true
		}
	}
	if jsonOutput {
		log(stringifyJSONIndent(reportToJSON(report), "  "))
		if failed {
			return 1
		}
		return 0
	}
	if len(report.Servers) == 0 && len(loaded.Errors) == 0 {
		log(fmt.Sprintf("No MCP servers configured. Add them to %s or .pi/mcp.json.", filepath.Join(options.AgentDir, "mcp.json")))
	}
	for _, server := range report.Servers {
		state := server.State
		switch state {
		case string(McpServerConnected):
			state = fmt.Sprintf("connected, %d tool%s", len(server.Tools), pluralSuffix(len(server.Tools)))
		case string(McpServerNeedsAuth):
			state = "needs sign-in"
		}
		log(fmt.Sprintf("%s: %s (%s, %s)", server.Name, state, server.Exposure, server.Scope))
		log("  " + server.Transport)
		if server.Override != "" {
			log("  project override: " + server.Override)
		}
		if server.State == string(McpServerNeedsAuth) {
			log(fmt.Sprintf("  sign in with: %s mcp login %s", AppName(), server.Name))
		}
		if len(server.Tools) > 0 {
			tools := make([]string, 0, len(server.Tools))
			for _, tool := range server.Tools {
				if exposure, has := server.ToolExposure[tool]; has {
					tools = append(tools, fmt.Sprintf("%s [%s]", tool, exposure))
				} else {
					tools = append(tools, tool)
				}
			}
			log("  tools: " + strings.Join(tools, ", "))
		}
		if server.OffersResources {
			log("  offers resources")
		}
		if server.Error != "" {
			log("  " + strings.ReplaceAll(server.Error, "\n", "\n  "))
		}
	}
	for _, configError := range loaded.Errors {
		log("config error: " + configError)
	}
	if untrustedNote != "" {
		log(untrustedNote)
	}
	if failed {
		return 1
	}
	return 0
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func mcpListOne(entry McpServerEntry, options McpCommandOptions, credentials *McpOAuthCredentialStore) mcpServerReport {
	scope := entry.Scope
	if scope == "" {
		scope = "global"
	}
	report := mcpServerReport{
		Name:      entry.Name,
		Scope:     scope,
		Source:    entry.Source,
		Override:  entry.Override,
		Enabled:   entry.Config.Enabled(),
		Exposure:  string(entry.Config.Exposure()),
		Transport: mcpDescribeTransport(entry),
		State:     "disabled",
		Tools:     []string{},
	}
	if !report.Enabled {
		return report
	}
	connection := mcpCreateConnection(entry, options, credentials)
	if _, err := connection.GetClient(); err != nil {
		// The connection records the state and error.
		_ = err
	}
	report.State = string(connection.State())
	report.Tools = make([]string, 0, len(connection.Tools))
	toolExposure := map[string]string{}
	for _, tool := range connection.Tools {
		report.Tools = append(report.Tools, tool.Name)
		exposure := GetMcpToolExposure(entry.Config, tool.Name)
		if string(exposure) != report.Exposure {
			toolExposure[tool.Name] = string(exposure)
		}
	}
	if len(toolExposure) > 0 {
		report.ToolExposure = toolExposure
	}
	if connection.HasResources() {
		report.OffersResources = true
	}
	if connection.State() != McpServerConnected {
		report.Error = connection.Error()
	}
	_ = connection.Close()
	return report
}

func reportToJSON(report *mcpListReport) any {
	servers := make([]any, 0, len(report.Servers))
	for _, server := range report.Servers {
		obj := jsonx.ObjFrom(
			"name", server.Name,
			"scope", server.Scope,
			"source", server.Source,
			"enabled", server.Enabled,
			"exposure", server.Exposure,
			"transport", server.Transport,
			"state", server.State,
			"tools", stringsToAnyList(server.Tools),
		)
		if server.Override != "" {
			obj.Set("override", server.Override)
		}
		if server.ToolExposure != nil {
			exposures := jsonx.NewObj()
			for tool, exposure := range server.ToolExposure {
				exposures.Set(tool, exposure)
			}
			obj.Set("toolExposure", exposures)
		}
		if server.OffersResources {
			obj.Set("resources", true)
		}
		if server.Error != "" {
			obj.Set("error", server.Error)
		}
		servers = append(servers, obj)
	}
	root := jsonx.ObjFrom("servers", servers, "errors", stringsToAnyList(report.Errors))
	if report.Note != "" {
		root.Set("note", report.Note)
	}
	return root
}

func stringsToAnyList(values []string) []any {
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	return items
}

func mcpLogin(entry McpServerEntry, connection *McpServerConnection, serverURL string, timeout time.Duration, options McpCommandOptions, credentials *McpOAuthCredentialStore, log func(string), errorOut func(string)) int {
	name := entry.Name
	// Connecting first answers whether a sign-in is needed and records the
	// server's challenge.
	if _, err := connection.GetClient(); err == nil {
		log(fmt.Sprintf("Already signed in to MCP server %q (%d tools).", name, len(connection.Tools)))
		return 0
	} else if connection.State() != McpServerNeedsAuth {
		errorOut(fmt.Sprintf("MCP server %q failed to connect: %s", name, orUnknown(connection.Error())))
		return 1
	}

	openURL := options.OpenURL
	if openURL == nil {
		openURL = OpenBrowser
	}
	interactive := stdinIsTTY() && options.OpenURL == nil
	// --timeout limits the whole sign-in (the prompt wait carries no timer
	// of its own).
	signInController := abort.NewController()
	signInTimer := time.AfterFunc(timeout, signInController.Abort)
	defer signInTimer.Stop()
	err := SignInMcpServer(struct {
		ServerURL string
		Store     mcp.McpOAuthStateStore
		Settings  McpOAuthSettings
		Challenge *mcp.OAuthChallenge
		Prompt    McpSignInPrompt
		Signal    *abort.Signal
	}{
		ServerURL: serverURL,
		Store:     credentials.ForServer(name, serverURL),
		Settings:  connection.OAuthSettings(),
		Challenge: connection.Challenge(),
		Prompt: McpSignInPrompt{
			ShowAuthorizationURL: func(authorizationURL string) {
				log(fmt.Sprintf("Sign in to MCP server %q in your browser:\n%s", name, authorizationURL))
				openURL(authorizationURL)
			},
			PromptForRedirectURL: func(signal *abort.Signal) (string, error) {
				return mcpWaitForRedirectURL(signal, interactive)
			},
		},
		Signal: signInController.Signal(),
	})
	if err != nil {
		var cancelled *McpSignInCancelledError
		if errors.As(err, &cancelled) {
			errorOut(fmt.Sprintf("Sign-in to MCP server %q was cancelled or not completed within %d seconds.", name, int(timeout.Seconds())))
		} else {
			errorOut(fmt.Sprintf("Sign-in to MCP server %q failed: %s", name, err.Error()))
		}
		return 1
	}
	connection.ClearChallenge()
	if err := connection.Reconnect(); err != nil {
		errorOut(fmt.Sprintf("Signed in, but %s", err.Error()))
		return 1
	}
	log(fmt.Sprintf("Signed in to MCP server %q (%d tools).", name, len(connection.Tools)))
	return 0
}

func orUnknown(message string) string {
	if message == "" {
		return "unknown error"
	}
	return message
}

func stdinIsTTY() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// mcpWaitForRedirectURL reads the pasted redirect URL in a terminal;
// otherwise only the browser callback can finish the sign-in. It returns
// "" (cancelling the sign-in) after the timeout, or when the callback
// arrived.
func mcpWaitForRedirectURL(signal *abort.Signal, interactive bool) (string, error) {
	if signal != nil && signal.Aborted() {
		// OnAbort never fires post-abort; an already-cancelled sign-in
		// returns immediately.
		return "", nil
	}
	controller := abort.NewController()
	remove := signal.OnAbort(controller.Abort)
	defer remove()
	if signal.Aborted() {
		return "", nil
	}
	if !interactive {
		<-controller.Signal().Done()
		return "", nil
	}
	line := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		fmt.Fprint(os.Stderr, "If the browser cannot reach this machine, paste the URL it was redirected to: ")
		text, _ := reader.ReadString('\n')
		line <- strings.TrimSpace(text)
	}()
	select {
	case <-controller.Signal().Done():
		return "", nil
	case text := <-line:
		return text, nil
	}
}
