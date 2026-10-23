package codingagent

import "testing"

// Ports of the tool-list helpers (ddaa0a034, 04b97ef00).
func TestToolListModifiers(t *testing.T) {
	if message := GetToolListError([]string{"read", "+codemode"}); message != "tool names cannot be mixed with +name or -name entries" {
		t.Fatalf("mixed = %q", message)
	}
	if message := GetToolListError([]string{"+mcp__x__*"}); message == "" {
		t.Fatal("pattern modifier accepted")
	}
	if message := GetToolListError([]string{"read", "bash"}); message != "" {
		t.Fatalf("plain = %q", message)
	}
	modified := ApplyToolModifiers([]string{"read", "bash"}, []string{"-bash", "+codemode"})
	if len(modified) != 2 || modified[0] != "read" || modified[1] != "codemode" {
		t.Fatalf("modified = %v", modified)
	}
}

func TestCreateToolNameMatcher(t *testing.T) {
	matcher := CreateToolNameMatcher([]string{"read", "mcp__x__*"})
	if !matcher("read") || !matcher("mcp__x__anything") || matcher("bash") {
		t.Fatal("matcher semantics wrong")
	}
	if !IsMcpToolName("mcp__srv__tool") || !IsMcpToolName("read_mcp_resource") || IsMcpToolName("bash") {
		t.Fatal("isMcpToolName wrong")
	}
}

func TestApplyPrintToolSelectionModifiersAndNoMcp(t *testing.T) {
	dir := t.TempDir()
	settings := SettingsManagerCreate(dir, dir, nil)
	// +codemode adds to the defaults and keeps MCP tools enabled.
	config := &AgentSessionConfig{}
	applyPrintToolSelection(config, settings, PrintSessionOptions{Tools: []string{"+codemode"}})
	if config.AllowedToolNames != nil {
		t.Fatalf("modifiers allowlist = %v", config.AllowedToolNames)
	}
	if !containsString(config.InitialActiveToolNames, "codemode") || !containsString(config.InitialActiveToolNames, "read") {
		t.Fatalf("modifier selection = %v", config.InitialActiveToolNames)
	}
	// A plain list without mcp__ entries becomes the allowlist.
	config = &AgentSessionConfig{}
	applyPrintToolSelection(config, settings, PrintSessionOptions{Tools: []string{"read"}})
	if len(config.AllowedToolNames) != 1 || config.AllowedToolNames[0] != "read" {
		t.Fatalf("plain allowlist = %v", config.AllowedToolNames)
	}
	// --no-mcp drops MCP tools from the selection.
	config = &AgentSessionConfig{}
	applyPrintToolSelection(config, settings, PrintSessionOptions{NoMcp: true})
	for _, name := range config.InitialActiveToolNames {
		if IsMcpToolName(name) {
			t.Fatalf("no-mcp kept %q", name)
		}
	}
	if config.AllowedToolNames != nil {
		t.Fatalf("no-mcp allowlist = %v", config.AllowedToolNames)
	}
}
