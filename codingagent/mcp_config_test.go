package codingagent

// Ports of the MCP config cases from packages/coding-agent
// test/mcp-extension.test.ts at reference commit a276dabe: config loading,
// validation, overrides, and exposure resolution.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

// mcpConfigFixture writes global and project mcp.json files and returns
// the load options. Config values are resolved at connect time, so the
// literal reference must survive loading.
type mcpConfigFixture struct {
	agentDir string
	cwd      string
}

func newMcpConfigFixture(t *testing.T, global, project string) mcpConfigFixture {
	t.Helper()
	root := t.TempDir()
	fixture := mcpConfigFixture{
		agentDir: filepath.Join(root, "agent"),
		cwd:      filepath.Join(root, "project"),
	}
	if err := os.MkdirAll(fixture.agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.cwd, ConfigDirName()), 0o755); err != nil {
		t.Fatal(err)
	}
	if global != "" {
		if err := os.WriteFile(filepath.Join(fixture.agentDir, "mcp.json"), []byte(global), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if project != "" {
		if err := os.WriteFile(filepath.Join(fixture.cwd, ConfigDirName(), "mcp.json"), []byte(project), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (f mcpConfigFixture) load(trusted bool) *LoadedMcpConfig {
	return LoadMcpConfig(struct {
		AgentDir       string
		Cwd            string
		ProjectTrusted bool
	}{AgentDir: f.agentDir, Cwd: f.cwd, ProjectTrusted: trusted})
}

func (f mcpConfigFixture) projectConfig() string {
	return filepath.Join(f.cwd, ConfigDirName(), "mcp.json")
}

func requireErrorContains(t *testing.T, err string, substrings ...string) {
	t.Helper()
	for _, substring := range substrings {
		if !strings.Contains(err, substring) {
			t.Fatalf("error %q does not contain %q", err, substring)
		}
	}
}

func TestLoadMcpConfigMergesGlobalAndProject(t *testing.T) {
	fixture := newMcpConfigFixture(t, `{
		"mcpServers": {
			"shared": { "command": "global-cmd" },
			"remote": { "url": "https://example.com/mcp", "headers": { "Authorization": "Bearer ${TOKEN}" } },
			"off": { "command": "x", "enabled": false },
			"bad": { "args": ["no command"] },
			"legacy": { "type": "sse", "url": "https://example.com/sse" },
			"badUrl": { "url": "example.com/mcp" },
			"bad name": { "command": "x" }
		}
	}`, `{"mcpServers": {"shared": {"command": "project-cmd", "exposure": "direct"}}}`)

	trusted := fixture.load(true)
	// Disabled servers are kept so /mcp can enable them again.
	type row struct{ name, scope string }
	var rows []row
	for _, server := range trusted.Servers {
		rows = append(rows, row{server.Name, server.Scope})
	}
	expected := []row{{"shared", "project"}, {"remote", "global"}, {"off", "global"}}
	if len(rows) != len(expected) {
		t.Fatalf("servers = %+v (errors %v)", rows, trusted.Errors)
	}
	for index, wanted := range expected {
		if rows[index] != wanted {
			t.Fatalf("servers[%d] = %+v, want %+v", index, rows[index], wanted)
		}
	}
	if trusted.Servers[1].Config.Headers()["Authorization"] != "Bearer ${TOKEN}" {
		t.Fatal("literal config value reference must survive loading")
	}
	if len(trusted.Errors) != 4 {
		t.Fatalf("errors = %v", trusted.Errors)
	}
	requireErrorContains(t, trusted.Errors[0], `server "bad" needs either "command"`)
	requireErrorContains(t, trusted.Errors[1], "legacy SSE transport is not supported")
	requireErrorContains(t, trusted.Errors[2], `server "badUrl": url must be an http or https URL`)
	requireErrorContains(t, trusted.Errors[3], `invalid server name "bad name"`)

	// Untrusted projects cannot add or override servers, since stdio
	// servers run commands.
	untrusted := fixture.load(false)
	for _, server := range untrusted.Servers {
		if server.Name == "shared" && server.Config.Command() != "global-cmd" {
			t.Fatal("untrusted project replaced the global server")
		}
	}
}

func TestLoadMcpConfigProjectOverrides(t *testing.T) {
	fixture := newMcpConfigFixture(t,
		`{"mcpServers": {"tools": {"command": "x", "env": {"TOKEN": "secret"}}}}`,
		`{"mcpServers": {"tools": {"enabled": false, "args": ["y"]}, "missing": {"enabled": false}}}`)
	project := fixture.projectConfig()
	loaded := fixture.load(true)
	if len(loaded.Servers) != 1 || loaded.Servers[0].Name != "tools" {
		t.Fatalf("servers = %+v", loaded.Servers)
	}
	if loaded.Servers[0].Override != "" {
		t.Fatalf("override = %q", loaded.Servers[0].Override)
	}
	if len(loaded.Errors) != 2 {
		t.Fatalf("errors = %v", loaded.Errors)
	}
	requireErrorContains(t, loaded.Errors[0], `server "tools": an override can only set enabled, exposure, toolExposure`)
	requireErrorContains(t, loaded.Errors[1], `server "missing" needs "command" or "url", or a global server to override`)

	if err := os.WriteFile(project, []byte(`{"mcpServers": {"tools": {"enabled": false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	servers := fixture.load(true).Servers
	if len(servers) != 1 || servers[0].Override != project || servers[0].Config.Enabled() {
		t.Fatalf("servers = %+v", servers)
	}
	if servers[0].Config.Env()["TOKEN"] != "secret" {
		t.Fatal("override dropped the global env")
	}

	// Overrides keep enabled: true, since it replaces the global value.
	if err := UpdateMcpServerConfig(project, "tools", McpServerConfigPatch{Enabled: boolPtr(true)}, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(project)
	value, _ := jsonx.Parse(string(data))
	tools := value.(*jsonx.Obj).MustGet("mcpServers").(*jsonx.Obj).MustGet("tools").(*jsonx.Obj)
	if tools.Len() != 1 || !tools.Has("enabled") || tools.MustGet("enabled") != true {
		t.Fatalf("tools entry = %s", jsonx.Stringify(tools))
	}
}

// Regression: pi #10239.
func TestLoadMcpConfigRejectsNamespaceClashes(t *testing.T) {
	fixture := newMcpConfigFixture(t,
		`{"mcpServers": {"work-files": {"command": "a"}, "work_files": {"command": "b"}}}`, "")
	loaded := fixture.load(false)
	if len(loaded.Servers) != 1 || loaded.Servers[0].Name != "work-files" {
		t.Fatalf("servers = %+v", loaded.Servers)
	}
	if len(loaded.Errors) != 1 {
		t.Fatalf("errors = %v", loaded.Errors)
	}
	requireErrorContains(t, loaded.Errors[0], `server "work_files" conflicts with "work-files"`)
}

func TestLoadMcpConfigValidatesExposure(t *testing.T) {
	fixture := newMcpConfigFixture(t, `{
		"autoEnableCodemode": false,
		"mcpServers": {
			"later": { "command": "x", "exposure": "deferred" },
			"scripts": { "command": "x", "exposure": "codemode-deferred", "toolExposure": { "a": "codemode-deferred" } },
			"off": { "command": "x", "exposure": "hidden" },
			"wrong": { "command": "x", "exposure": "model-only" },
			"described": { "command": "x", "description": "Docs search" },
			"badDescription": { "command": "x", "description": 1 }
		}
	}`, `{"autoEnableCodemode": "yes", "mcpServers": {}}`)

	untrusted := fixture.load(false)
	if untrusted.AutoEnableCodemode == nil || *untrusted.AutoEnableCodemode {
		t.Fatal("autoEnableCodemode not read")
	}
	type row struct{ name, exposure string }
	var rows []row
	for _, server := range untrusted.Servers {
		rows = append(rows, row{server.Name, string(server.Config.Exposure())})
	}
	expected := []row{{"later", "deferred"}, {"scripts", "codemode"}, {"off", "hidden"}, {"described", "codemode"}}
	if len(rows) != len(expected) {
		t.Fatalf("servers = %+v", rows)
	}
	for index, wanted := range expected {
		if rows[index] != wanted {
			t.Fatalf("servers[%d] = %+v, want %+v", index, rows[index], wanted)
		}
	}
	// The alias is resolved in toolExposure too.
	for _, entry := range untrusted.Servers[1].Config.ToolExposure() {
		if entry[0] == "a" && entry[1] != "codemode" {
			t.Fatalf("toolExposure = %+v", entry)
		}
	}
	if untrusted.Servers[3].Config.Description() != "Docs search" {
		t.Fatal("description lost")
	}
	if len(untrusted.Errors) != 2 {
		t.Fatalf("errors = %v", untrusted.Errors)
	}
	requireErrorContains(t, untrusted.Errors[0], `server "wrong": exposure must be one of`)
	requireErrorContains(t, untrusted.Errors[1], `server "badDescription": description must be a string`)

	trusted := fixture.load(true)
	if *trusted.AutoEnableCodemode {
		t.Fatal("project must not override a global boolean")
	}
	found := false
	for _, err := range trusted.Errors {
		if strings.Contains(err, "autoEnableCodemode must be a boolean") {
			found = true
		}
	}
	if !found {
		t.Fatalf("errors = %v", trusted.Errors)
	}
}

func TestLoadMcpConfigValidatesOAuth(t *testing.T) {
	fixture := newMcpConfigFixture(t, `{
		"mcpServers": {
			"ok": { "url": "https://a.example/mcp", "oauth": { "callbackUrl": "http://localhost:8080/callback", "scope": "a b" } },
			"ipv6": { "url": "https://a.example/mcp", "oauth": { "callbackUrl": "http://[::1]/cb", "callbackPort": 9000 } },
			"same": { "url": "https://a.example/mcp", "oauth": { "callbackUrl": "http://127.0.0.1:2/cb", "callbackPort": 2 } },
			"remote": { "url": "https://a.example/mcp", "oauth": { "callbackUrl": "https://example.com/callback" } },
			"both": { "url": "https://a.example/mcp", "oauth": { "callbackUrl": "http://127.0.0.1:1/cb", "callbackPort": 2 } },
			"scope": { "url": "https://a.example/mcp", "oauth": { "scope": ["a"] } },
			"named": { "url": "https://a.example/mcp", "oauth": { "clientName": "Claude Code" } },
			"unnamed": { "url": "https://a.example/mcp", "oauth": { "clientName": " " } },
			"metadata": { "url": "https://a.example/mcp", "oauth": { "authServerMetadataUrl": "https://idp.example/m" } },
			"plainMetadata": { "url": "https://a.example/mcp", "oauth": { "authServerMetadataUrl": "http://idp.example/m" } },
			"cimd": { "url": "https://a.example/mcp", "oauth": { "clientRegistration": "cimd", "callbackUrl": "http://localhost/callback" } },
			"badRegistration": { "url": "https://a.example/mcp", "oauth": { "clientRegistration": "auto" } },
			"cimdClient": { "url": "https://a.example/mcp", "oauth": { "clientRegistration": "cimd", "clientId": "x" } },
			"cimdPath": { "url": "https://a.example/mcp", "oauth": { "clientRegistration": "cimd", "callbackUrl": "http://127.0.0.1/cb" } }
		}
	}`, "")
	loaded := fixture.load(false)
	var names []string
	for _, server := range loaded.Servers {
		names = append(names, server.Name)
	}
	wanted := []string{"ok", "ipv6", "same", "named", "metadata", "cimd"}
	if len(names) != len(wanted) {
		t.Fatalf("servers = %v (errors %v)", names, loaded.Errors)
	}
	for index, name := range wanted {
		if names[index] != name {
			t.Fatalf("servers[%d] = %s, want %s", index, names[index], name)
		}
	}
	expectedErrors := []string{
		`server "remote": oauth.callbackUrl must be an http URI on localhost`,
		`server "both": oauth.callbackUrl and oauth.callbackPort name different ports`,
		`server "scope": oauth.scope must be a string`,
		`server "unnamed": oauth.clientName must be a non-empty string`,
		`server "plainMetadata": oauth.authServerMetadataUrl must be an https URL`,
		`oauth.clientRegistration must be "dcr" or "cimd"`,
		`oauth.clientRegistration "cimd" cannot be combined`,
		`oauth.clientRegistration "cimd" requires oauth.callbackUrl`,
	}
	if len(loaded.Errors) != len(expectedErrors) {
		t.Fatalf("errors = %v", loaded.Errors)
	}
	for index, substring := range expectedErrors {
		requireErrorContains(t, loaded.Errors[index], substring)
	}
}

func TestGetMcpToolExposureResolution(t *testing.T) {
	fixture := newMcpConfigFixture(t, `{
		"mcpServers": {
			"gh": {
				"command": "x",
				"exposure": "deferred",
				"toolExposure": { "get_*": "codemode", "get_me": "direct", "*delete*": "hidden", "get_file.*": "direct" }
			},
			"bad": { "command": "x", "toolExposure": { "a": "visible" } }
		}
	}`, "")
	loaded := fixture.load(false)
	if len(loaded.Errors) != 1 {
		t.Fatalf("errors = %v", loaded.Errors)
	}
	requireErrorContains(t, loaded.Errors[0], `server "bad": toolExposure "a" must be one of`)
	config := loaded.Servers[0].Config
	cases := []struct {
		tool string
		want McpExposure
	}{
		{"get_me", McpExposureDirect},
		{"get_issue", McpExposureCodemode},
		{"get_delete_hint", McpExposureCodemode},
		{"delete_repo", McpExposureHidden},
		{"list_issues", McpExposureDeferred},
	}
	for _, test := range cases {
		if got := GetMcpToolExposure(config, test.tool); got != test.want {
			t.Fatalf("exposure(%s) = %s, want %s", test.tool, got, test.want)
		}
	}
	// Only * is special.
	simple, err := ValidateMcpServerConfig("plain", mustParseJSON(t, `{"command": "x", "toolExposure": {"get_file.*": "direct"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := GetMcpToolExposure(simple, "get_file_x"); got != McpExposureCodemode {
		t.Fatalf("exposure(get_file_x) = %s", got)
	}
}

func TestLoadMcpConfigValidatesProviderAuth(t *testing.T) {
	fixture := newMcpConfigFixture(t, `{
		"mcpServers": {
			"radius": { "url": "https://radius.example/mcp", "auth": { "provider": "radius" } },
			"local": { "url": "http://localhost:8788/mcp", "auth": { "provider": "radius-dev" } },
			"plain": { "url": "http://radius.example/mcp", "auth": { "provider": "radius" } },
			"empty": { "url": "https://radius.example/mcp", "auth": { "provider": "" } }
		}
	}`, `{"mcpServers": {"radius": {"url": "https://evil.example/mcp", "auth": {"provider": "radius"}}}}`)
	loaded := fixture.load(true)
	// The project entry cannot replace the global one: it would send the
	// credential to its own URL.
	type row struct{ name, scope, url string }
	var rows []row
	for _, server := range loaded.Servers {
		rows = append(rows, row{server.Name, server.Scope, server.Config.URL()})
	}
	expected := []row{
		{"radius", "global", "https://radius.example/mcp"},
		{"local", "global", "http://localhost:8788/mcp"},
	}
	if len(rows) != len(expected) {
		t.Fatalf("servers = %+v (errors %v)", rows, loaded.Errors)
	}
	for index, wanted := range expected {
		if rows[index] != wanted {
			t.Fatalf("servers[%d] = %+v, want %+v", index, rows[index], wanted)
		}
	}
	expectedErrors := []string{
		`server "plain": auth requires an https URL`,
		`server "empty": auth.provider must be a provider name`,
		`server "radius": auth is only allowed in the global mcp.json`,
	}
	if len(loaded.Errors) != len(expectedErrors) {
		t.Fatalf("errors = %v", loaded.Errors)
	}
	for index, substring := range expectedErrors {
		requireErrorContains(t, loaded.Errors[index], substring)
	}
}

func TestAddRemoveMcpServerConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "mcp.json")
	config, err := ValidateMcpServerConfig("docs", mustParseJSON(t, `{"url": "https://example.com/mcp"}`))
	if err != nil {
		t.Fatal(err)
	}
	replaced, err := AddMcpServerConfig(path, "docs", config)
	if err != nil || replaced {
		t.Fatalf("add = %v %v", replaced, err)
	}
	replaced, err = AddMcpServerConfig(path, "docs", config)
	if err != nil || !replaced {
		t.Fatalf("re-add = %v %v", replaced, err)
	}
	// Unknown other content and indentation are kept.
	if err := UpdateMcpServerConfig(path, "docs", McpServerConfigPatch{Exposure: McpExposureDirect}, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.Contains(text, "\"exposure\": \"direct\"") || !strings.HasSuffix(text, "\n") {
		t.Fatalf("file = %s", text)
	}
	removed, err := RemoveMcpServerConfig(path, "docs")
	if err != nil || !removed {
		t.Fatalf("remove = %v %v", removed, err)
	}
	removed, err = RemoveMcpServerConfig(path, "docs")
	if err != nil || removed {
		t.Fatalf("re-remove = %v %v", removed, err)
	}
}

func TestMcpServerRegistry(t *testing.T) {
	registry := NewMcpServerRegistry()
	changes := 0
	registry.SetChangeListener(func() { changes++ })
	config, err := ValidateMcpServerConfig("local", mustParseJSON(t, `{"command": "x"}`))
	if err != nil {
		t.Fatal(err)
	}
	registry.Register(&RegisteredMcpServer{Name: "local", Config: config, ExtensionPath: "/ext/a.js"})
	if changes != 1 || registry.Get("local") == nil {
		t.Fatalf("changes = %d", changes)
	}
	// Another extension cannot unregister it.
	registry.Unregister("local", "/ext/b.js")
	if registry.Get("local") == nil {
		t.Fatal("server removed by another extension")
	}
	registry.Unregister("local", "/ext/a.js")
	if registry.Get("local") != nil || changes != 2 {
		t.Fatalf("changes = %d", changes)
	}
	if list := registry.List(); len(list) != 0 {
		t.Fatalf("list = %+v", list)
	}
}

func mustParseJSON(t *testing.T, text string) any {
	t.Helper()
	value, err := jsonx.Parse(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return value
}

func boolPtr(value bool) *bool { return &value }

// Port of bdd9f0399: mcp.json files with a UTF-8 BOM load.
func TestMcpConfigAcceptsBom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	bomConfig := "\ufeff" + `{"mcpServers":{"srv":{"type":"stdio","command":"true"}}}`
	if err := os.WriteFile(path, []byte(bomConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := LoadMcpConfig(struct {
		AgentDir       string
		Cwd            string
		ProjectTrusted bool
	}{AgentDir: dir})
	if len(loaded.Servers) != 1 || loaded.Servers[0].Name != "srv" {
		t.Fatalf("servers = %+v (errors %v)", loaded.Servers, loaded.Errors)
	}
}
