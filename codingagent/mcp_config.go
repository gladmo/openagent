package codingagent

// Port of extensions/mcp/config.ts at reference commit a276dabe: MCP
// server configuration from mcp.json.
//
// Servers are read from mcp.json in the agent directory and, for trusted
// projects, from <project>/.pi/mcp.json. Both use the mcpServers shape
// shared by other MCP clients, so existing configurations can be copied
// over. Project entries replace global entries with the same name.
//
// A project entry without command, url, or type overrides only enabled,
// exposure, and toolExposure of the global server with the same name, for
// example to turn it off in one project. The rest of the global entry is
// kept, including credentials the project could not set itself.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// McpServerEntry is one configured server.
type McpServerEntry struct {
	Name   string
	Config *McpServerConfig
	// Source is the config file that defined the entry, or the path of
	// the extension that registered it.
	Source string
	// Scope is "global", "project", "extension", or "" for extension
	// registrations. Changes to extension servers are not saved.
	Scope string
	// Override is the project mcp.json with an override of this global
	// server's enabled, exposure, or toolExposure ("" when none).
	Override string
}

// LoadedMcpConfig is the merged MCP configuration.
type LoadedMcpConfig struct {
	Servers []McpServerEntry
	// AutoEnableCodemode activates the codemode tool when codemode servers
	// connect; nil keeps the default (true).
	AutoEnableCodemode *bool
	Errors             []string
	// ProjectConfig is the project mcp.json when the project is trusted,
	// where /mcp saves project overrides ("" when untrusted).
	ProjectConfig string
}

// mcpOverrideKeys are the keys a project override may set.
var mcpOverrideKeys = []string{"enabled", "exposure", "toolExposure"}

// isMcpOverrideEntry reports whether an entry overrides a server defined
// elsewhere instead of defining one.
func isMcpOverrideEntry(value *jsonx.Obj) bool {
	return !value.Has("command") && !value.Has("url") && !value.Has("type")
}

type mcpConfigState struct {
	servers            map[string]McpServerEntry
	order              []string
	autoEnableCodemode *bool
	errors             []string
}

func (state *mcpConfigState) set(name string, entry McpServerEntry) {
	if _, exists := state.servers[name]; !exists {
		state.order = append(state.order, name)
	}
	state.servers[name] = entry
}

func readMcpConfigFile(path, scope string, state *mcpConfigState) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		state.errors = append(state.errors, fmt.Sprintf("%s: %s", path, err.Error()))
		return
	}
	parsed, parseErr := jsonx.Parse(string(data))
	if parseErr != nil {
		state.errors = append(state.errors, fmt.Sprintf("%s: %s", path, parseErr.Error()))
		return
	}
	root, isRecord := isJSONRecord(parsed)
	if !isRecord {
		state.errors = append(state.errors, fmt.Sprintf("%s: expected an object with an \"mcpServers\" object", path))
		return
	}
	serversValue, hasServers := root.Get("mcpServers")
	if hasServers && serversValue != nil {
		if _, isRecord := serversValue.(*jsonx.Obj); !isRecord {
			state.errors = append(state.errors, fmt.Sprintf("%s: expected an object with an \"mcpServers\" object", path))
			return
		}
	}
	if autoEnable, has := root.Get("autoEnableCodemode"); has && autoEnable != nil {
		if enabled, isBool := autoEnable.(bool); isBool {
			state.autoEnableCodemode = &enabled
		} else {
			state.errors = append(state.errors, fmt.Sprintf("%s: autoEnableCodemode must be a boolean", path))
		}
	}
	servers, _ := serversValue.(*jsonx.Obj)
	if servers == nil {
		return
	}
	for _, entry := range servers.Entries() {
		name, _ := entry[0].(string)
		value := entry[1]
		valueObj, isRecord := isJSONRecord(value)
		if scope == "project" && isRecord && isMcpOverrideEntry(valueObj) {
			base, hasBase := state.servers[name]
			if !hasBase {
				state.errors = append(state.errors, fmt.Sprintf("%s: server %q needs \"command\" or \"url\", or a global server to override", path, name))
				continue
			}
			extra := []string{}
			for _, field := range valueObj.Entries() {
				key, _ := field[0].(string)
				known := false
				for _, allowed := range mcpOverrideKeys {
					if key == allowed {
						known = true
					}
				}
				if !known {
					extra = append(extra, key)
				}
			}
			if len(extra) > 0 {
				state.errors = append(state.errors, fmt.Sprintf("%s: server %q: an override can only set %s", path, name, strings.Join(mcpOverrideKeys, ", ")))
				continue
			}
			merged := jsonx.Clone(base.Config.Raw).(*jsonx.Obj)
			for _, field := range valueObj.Entries() {
				key, _ := field[0].(string)
				merged.Set(key, field[1])
			}
			config, configErr := ValidateMcpServerConfig(name, merged)
			if configErr != nil {
				state.errors = append(state.errors, fmt.Sprintf("%s: %s", path, configErr.Error()))
				continue
			}
			state.set(name, McpServerEntry{
				Name:     base.Name,
				Config:   config,
				Source:   base.Source,
				Scope:    base.Scope,
				Override: path,
			})
			continue
		}
		config, configErr := ValidateMcpServerConfig(name, value)
		if configErr != nil {
			state.errors = append(state.errors, fmt.Sprintf("%s: %s", path, configErr.Error()))
			continue
		}
		// Names that differ only in - and _ would share a namespace.
		clash := ""
		for _, other := range state.order {
			if other != name && McpNamespace(other) == McpNamespace(name) {
				clash = other
				break
			}
		}
		if clash != "" {
			state.errors = append(state.errors, fmt.Sprintf("%s: server %q conflicts with %q", path, name, clash))
			continue
		}
		if scope == "project" && config.IsHTTP() && config.AuthProvider() != "" {
			state.errors = append(state.errors, fmt.Sprintf("%s: server %q: auth is only allowed in the global mcp.json", path, name))
			continue
		}
		state.set(name, McpServerEntry{Name: name, Config: config, Source: path, Scope: scope})
	}
}

// LoadMcpConfig loads global and (when trusted) project MCP
// configuration. Disabled servers are included with enabled false, so
// they can be enabled again.
func LoadMcpConfig(options struct {
	AgentDir       string
	Cwd            string
	ProjectTrusted bool
}) *LoadedMcpConfig {
	state := &mcpConfigState{servers: map[string]McpServerEntry{}}
	readMcpConfigFile(filepath.Join(options.AgentDir, "mcp.json"), "global", state)
	projectConfig := ""
	if options.ProjectTrusted {
		projectConfig = filepath.Join(options.Cwd, ConfigDirName(), "mcp.json")
		readMcpConfigFile(projectConfig, "project", state)
	}
	loaded := &LoadedMcpConfig{Errors: state.errors, ProjectConfig: projectConfig}
	for _, name := range state.order {
		loaded.Servers = append(loaded.Servers, state.servers[name])
	}
	loaded.AutoEnableCodemode = state.autoEnableCodemode
	return loaded
}

// McpServerConfigPatch is the settings /mcp changes; enabled true and
// exposure codemode are the defaults and remove the key.
type McpServerConfigPatch struct {
	Enabled  *bool
	Exposure McpExposure // "" leaves it unchanged
}

// UpdateMcpServerConfig changes one server's settings in the mcp.json
// that defines or overrides it. With override, a missing entry is added
// as an override. Overrides keep default values, since they replace the
// global server's. Other content is kept; the file is rewritten with its
// indentation.
func UpdateMcpServerConfig(path, name string, patch McpServerConfigPatch, override bool) error {
	return editMcpServers(path, func(servers *jsonx.Obj, parsed *jsonx.Obj) (bool, error) {
		var server *jsonx.Obj
		if servers != nil {
			if value, has := servers.Get(name); has {
				server, _ = value.(*jsonx.Obj)
			}
		}
		if server == nil && override {
			server = jsonx.NewObj()
			if servers == nil {
				servers = jsonx.NewObj()
			}
			servers.Set(name, server)
			parsed.Set("mcpServers", servers)
		}
		if server == nil {
			return false, fmt.Errorf("%s does not define MCP server %q", path, name)
		}
		keepDefaults := isMcpOverrideEntry(server)
		if patch.Enabled != nil {
			if *patch.Enabled && !keepDefaults {
				server.Delete("enabled")
			} else {
				server.Set("enabled", *patch.Enabled)
			}
		}
		if patch.Exposure != "" {
			if patch.Exposure == McpExposureCodemode && !keepDefaults {
				server.Delete("exposure")
			} else {
				server.Set("exposure", string(patch.Exposure))
			}
		}
		return true, nil
	})
}

// AddMcpServerConfig adds a server to an mcp.json, creating the file when
// missing. An existing entry with the same name is replaced. It returns
// whether an entry was replaced.
func AddMcpServerConfig(path, name string, config *McpServerConfig) (bool, error) {
	replaced := false
	err := editMcpServers(path, func(servers *jsonx.Obj, parsed *jsonx.Obj) (bool, error) {
		target := servers
		if target == nil {
			target = jsonx.NewObj()
		}
		if _, has := target.Get(name); has {
			replaced = true
		}
		target.Set(name, jsonx.Clone(config.Raw))
		parsed.Set("mcpServers", target)
		return true, nil
	})
	return replaced, err
}

// RemoveMcpServerConfig removes a server from an mcp.json. It returns
// false when the file does not define it.
func RemoveMcpServerConfig(path, name string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	removed := false
	err := editMcpServers(path, func(servers *jsonx.Obj, _ *jsonx.Obj) (bool, error) {
		if servers == nil || !servers.Has(name) {
			return false, nil
		}
		servers.Delete(name)
		removed = true
		return true, nil
	})
	return removed, err
}

var mcpIndentPattern = regexp.MustCompile(`(?m)^([ \t]+)\S`)

// editMcpServers reads an mcp.json (an empty config when missing), lets
// edit change its mcpServers, and writes it back with its indentation
// when edit reports a change. Other content is kept.
func editMcpServers(path string, edit func(servers *jsonx.Obj, parsed *jsonx.Obj) (bool, error)) error {
	text := ""
	fileExists := true
	if data, err := os.ReadFile(path); err == nil {
		text = string(data)
	} else if os.IsNotExist(err) {
		fileExists = false
	} else {
		return err
	}
	parsed := jsonx.NewObj()
	if fileExists {
		value, parseErr := jsonx.Parse(text)
		if parseErr != nil {
			return parseErr
		}
		root, isRecord := isJSONRecord(value)
		if !isRecord {
			return fmt.Errorf("%s: expected an object with an \"mcpServers\" object", path)
		}
		parsed = root
	}
	if rawServers, has := parsed.Get("mcpServers"); has && rawServers != nil {
		if _, isRecord := rawServers.(*jsonx.Obj); !isRecord {
			return fmt.Errorf("%s: expected an object with an \"mcpServers\" object", path)
		}
	}
	var servers *jsonx.Obj
	if rawServers, has := parsed.Get("mcpServers"); has {
		servers, _ = rawServers.(*jsonx.Obj)
	}
	changed, err := edit(servers, parsed)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	indent := "  "
	if match := mcpIndentPattern.FindStringSubmatch(text); match != nil {
		indent = match[1]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded := stringifyJSONIndent(parsed, indent)
	return os.WriteFile(path, []byte(encoded+"\n"), 0o644)
}

// stringifyJSONIndent renders a jsonx value with the given indentation,
// like JSON.stringify(value, null, indent).
func stringifyJSONIndent(value any, indent string) string {
	var builder strings.Builder
	writeJSONIndent(&builder, value, indent, 0)
	return builder.String()
}

func writeJSONIndent(builder *strings.Builder, value any, indent string, depth int) {
	switch typed := value.(type) {
	case *jsonx.Obj:
		if typed.Len() == 0 {
			builder.WriteString("{}")
			return
		}
		inner := strings.Repeat(indent, depth+1)
		builder.WriteString("{\n")
		for index, entry := range typed.Entries() {
			builder.WriteString(inner)
			builder.WriteString(jsonx.Stringify(entry[0]))
			builder.WriteString(": ")
			writeJSONIndent(builder, entry[1], indent, depth+1)
			if index < typed.Len()-1 {
				builder.WriteString(",")
			}
			builder.WriteString("\n")
		}
		builder.WriteString(strings.Repeat(indent, depth))
		builder.WriteString("}")
	case []any:
		if len(typed) == 0 {
			builder.WriteString("[]")
			return
		}
		inner := strings.Repeat(indent, depth+1)
		builder.WriteString("[\n")
		for index, item := range typed {
			builder.WriteString(inner)
			writeJSONIndent(builder, item, indent, depth+1)
			if index < len(typed)-1 {
				builder.WriteString(",")
			}
			builder.WriteString("\n")
		}
		builder.WriteString(strings.Repeat(indent, depth))
		builder.WriteString("]")
	default:
		builder.WriteString(jsonx.Stringify(value))
	}
}
