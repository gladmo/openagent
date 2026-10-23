package codingagent

// Tests for system-prompt construction (ported from system-prompt.test.ts
// surfaces).

import (
	"strings"
	"testing"
)

func TestBuildSystemPromptSectionsDefaults(t *testing.T) {
	sections, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		Cwd: "/work",
		ToolSnippets: map[string]string{
			"read": "Read files", "bash": "Run commands",
			"edit": "Edit files", "write": "Write files",
		},
	})
	if err != nil {
		t.Fatalf("sections: %v", err)
	}
	if !strings.Contains(sections["preamble"], "expert coding assistant") {
		t.Fatalf("preamble = %q", sections["preamble"])
	}
	if !strings.Contains(sections["tools"], "- bash") || !strings.Contains(sections["tools"], "- read") {
		t.Fatalf("tools = %q", sections["tools"])
	}
	if !strings.Contains(sections["rules"], "- Be concise in your responses") {
		t.Fatalf("rules = %q", sections["rules"])
	}
	if !strings.Contains(sections["docs"], "Pi documentation") {
		t.Fatalf("docs missing")
	}
	if sections["cwd"] != "<cwd>\n/work\n</cwd>" {
		t.Fatalf("cwd = %q", sections["cwd"])
	}
}

func TestBuildSystemPromptSectionsCustomPrompt(t *testing.T) {
	sections, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		CustomPrompt: "You are custom.",
		Cwd:          "/w",
	})
	if err != nil {
		t.Fatalf("sections: %v", err)
	}
	if sections["preamble"] != "You are custom." {
		t.Fatalf("preamble = %q", sections["preamble"])
	}
	if _, has := sections["tools"]; has {
		t.Fatal("custom prompt should drop tools section")
	}
	if _, has := sections["rules"]; has {
		t.Fatal("custom prompt should drop rules section")
	}
}

func TestBuildSystemPromptSectionsRules(t *testing.T) {
	sections, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		Cwd:              "/w",
		SelectedTools:    []string{"read", "edit"},
		ToolGuidelines:   map[string][]string{"read": {"Use read first", "Use read first"}},
		PromptGuidelines: []string{"Always test", ""},
	})
	if err != nil {
		t.Fatalf("sections: %v", err)
	}
	rules := sections["rules"]
	if strings.Count(rules, "Use read first") != 1 {
		t.Fatalf("dedupe failed: %q", rules)
	}
	if !strings.Contains(rules, "Always test") {
		t.Fatalf("guideline missing: %q", rules)
	}
	// No bash/powershell and no grep/find/ls: no file-operations rule.
	if strings.Contains(rules, "file operations") {
		t.Fatalf("unexpected rule: %q", rules)
	}
}

func TestBuildSystemPromptSectionsBashFallbackRule(t *testing.T) {
	sections, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		Cwd:           "/w",
		SelectedTools: []string{"read", "bash", "edit"},
	})
	if err != nil {
		t.Fatalf("sections: %v", err)
	}
	if !strings.Contains(sections["rules"], "Use bash for file operations") {
		t.Fatalf("rule = %q", sections["rules"])
	}

	// With grep present the fallback rule drops.
	sections, _ = BuildSystemPromptSections(BuildSystemPromptOptions{
		Cwd:           "/w",
		SelectedTools: []string{"read", "bash", "grep"},
	})
	if strings.Contains(sections["rules"], "file operations") {
		t.Fatalf("fallback not suppressed: %q", sections["rules"])
	}
}

func TestBuildSystemPromptSectionsLayers(t *testing.T) {
	sections, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		Cwd:                "/w",
		AppendSystemPrompt: "appended guidance",
		ContextFiles:       []ContextFile{{Path: "CLAUDE.md", Content: "project rule"}},
		Skills:             []Skill{{Name: "review", Description: "Review code"}},
	})
	if err != nil {
		t.Fatalf("sections: %v", err)
	}
	if sections["addendum"] != "<addendum>\nappended guidance\n</addendum>" {
		t.Fatalf("addendum = %q", sections["addendum"])
	}
	if !strings.Contains(sections["project_context"], `<project_instructions path="CLAUDE.md">`) ||
		!strings.Contains(sections["project_context"], "project rule") {
		t.Fatalf("context = %q", sections["project_context"])
	}
	if !strings.Contains(sections["skills"], "- review: Review code (use the read tool") {
		t.Fatalf("skills = %q", sections["skills"])
	}
}

func TestBuildSystemPromptSectionsCustomSections(t *testing.T) {
	sections, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		Cwd:      "/w",
		Sections: map[string]string{"custom_zone": "extra", "empty": ""},
	})
	if err != nil {
		t.Fatalf("sections: %v", err)
	}
	if sections["custom_zone"] != "<custom_zone>\nextra\n</custom_zone>" {
		t.Fatalf("custom = %q", sections["custom_zone"])
	}
	if _, has := sections["empty"]; has {
		t.Fatal("empty custom section should drop")
	}

	// Invalid names are rejected.
	if _, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		Cwd:      "/w",
		Sections: map[string]string{"BadName": "x"},
	}); err == nil {
		t.Fatal("invalid name accepted")
	}
	if _, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		Cwd:      "/w",
		Sections: map[string]string{"preamble": "override"},
	}); err == nil {
		t.Fatal("preamble section name rejected")
	}
}

func TestBuildSystemPromptState(t *testing.T) {
	forced := "forced prompt"
	state, err := BuildSystemPromptState(BuildSystemPromptOptions{Cwd: "/w", ForceSystemPrompt: &forced})
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Content != "forced prompt" || state.Sections != nil {
		t.Fatalf("forced = %+v", state)
	}

	state, err = BuildSystemPromptState(BuildSystemPromptOptions{Cwd: "/w"})
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Content != "" || state.Sections == nil {
		t.Fatalf("structured = %+v", state)
	}
}

func TestDiffSystemPromptSections(t *testing.T) {
	previous := map[string]*string{"a": strPtr("old-a"), "b": strPtr("same")}
	current := SystemPromptSections{"a": "new-a", "b": "same", "c": "new-c"}

	patch := DiffSystemPromptSections(previous, current)
	if patch == nil {
		t.Fatal("patch nil")
	}
	if *patch["a"] != "new-a" {
		t.Fatalf("a = %v", patch["a"])
	}
	if _, has := patch["b"]; has {
		t.Fatal("unchanged section in patch")
	}
	if *patch["c"] != "new-c" {
		t.Fatalf("c = %v", patch["c"])
	}

	// No changes: nil.
	patch = DiffSystemPromptSections(map[string]*string{"a": strPtr("x")}, SystemPromptSections{"a": "x"})
	if patch != nil {
		t.Fatalf("no-change patch = %v", patch)
	}

	// Removed section: null.
	patch = DiffSystemPromptSections(map[string]*string{"gone": strPtr("x")}, SystemPromptSections{})
	if patch == nil || patch["gone"] != nil {
		t.Fatalf("removal = %v", patch)
	}
}

func strPtr(s string) *string { return &s }

// Port of the hidden-tools prompt behavior (c30840c2e): hidden tools stay
// out of the tool list and rules; a hidden reader keeps skills with no
// named tool.
func TestSystemPromptHidesHiddenTools(t *testing.T) {
	sections, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		SelectedTools: []string{"read", "bash", "secret_tool"},
		HiddenTools:   []string{"secret_tool"},
		ToolSnippets: map[string]string{
			"read": "reads files", "bash": "runs commands", "secret_tool": "internal",
		},
		ToolGuidelines: map[string][]string{"secret_tool": {"never mention this"}},
		Cwd:            "/x",
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt := strings.Join(mapValues(sections), "\n")
	if strings.Contains(prompt, "secret_tool") {
		t.Fatalf("hidden tool leaked: %q", prompt)
	}
	if !strings.Contains(prompt, "reads files") || !strings.Contains(prompt, "runs commands") {
		t.Fatalf("declared tools missing: %q", prompt)
	}
}

func TestSystemPromptSkillsHintIndirect(t *testing.T) {
	sections, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		SelectedTools: []string{"bash"},
		HiddenTools:   []string{"bash"},
		ToolSnippets:  map[string]string{"bash": "runs commands"},
		Skills:        []Skill{{Name: "research", Description: "does research"}},
		Cwd:           "/x",
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt := strings.Join(mapValues(sections), "\n")
	if !strings.Contains(prompt, "research") {
		t.Fatalf("skills dropped: %q", prompt)
	}
	if strings.Contains(prompt, "use the bash tool") {
		t.Fatalf("hidden reader named: %q", prompt)
	}
	if !strings.Contains(prompt, "Load a skill's file when the task matches") {
		t.Fatalf("indirect hint missing: %q", prompt)
	}
}

func mapValues(sections SystemPromptSections) []string {
	values := make([]string, 0, len(sections))
	for _, value := range sections {
		values = append(values, value)
	}
	return values
}
