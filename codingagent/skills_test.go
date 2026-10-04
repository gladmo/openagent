package codingagent

// Tests for skill loading and validation (ported from skills.test.ts
// surfaces).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, dir, name, frontmatter string) string {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "SKILL.md")
	os.WriteFile(path, []byte("---\n"+frontmatter+"\n---\n\nbody"), 0o644)
	_ = name
	return path
}

func TestValidateSkillName(t *testing.T) {
	for _, valid := range []string{"a", "code-review", "deploy-2", strings.Repeat("a", 64)} {
		if errs := validateSkillName(valid); len(errs) > 0 {
			t.Errorf("valid %q rejected: %v", valid, errs)
		}
	}
	for name, wantSubstring := range map[string]string{
		strings.Repeat("a", 65): "exceeds",
		"Upper":                 "invalid characters",
		"under_score":           "invalid characters",
		"-leading":              "start or end",
		"trailing-":             "start or end",
		"double--hyphen":        "consecutive",
	} {
		errs := validateSkillName(name)
		found := false
		for _, err := range errs {
			if strings.Contains(err, wantSubstring) {
				found = true
			}
		}
		if !found {
			t.Errorf("invalid %q missing %q: %v", name, wantSubstring, errs)
		}
	}
}

func TestLoadSkillsFromDirSkillRoot(t *testing.T) {
	dir := t.TempDir()
	// A SKILL.md dir is a skill root; nested SKILL.md dirs do not recurse.
	writeSkill(t, dir, "root", "name: root-skill\ndescription: root skill")
	nested := filepath.Join(dir, "nested")
	os.MkdirAll(nested, 0o755)
	writeSkill(t, nested, "nested", "name: nested-skill\ndescription: nested")

	result := LoadSkillsFromDir(dir, "user")
	if len(result.Skills) != 1 {
		t.Fatalf("skills = %d", len(result.Skills))
	}
	if result.Skills[0].Name != "root-skill" {
		t.Fatalf("name = %q", result.Skills[0].Name)
	}
	if result.Skills[0].SourceInfo.Scope != ScopeUser {
		t.Fatalf("scope = %q", result.Skills[0].SourceInfo.Scope)
	}
}

func TestLoadSkillsFromDirRootMarkdown(t *testing.T) {
	dir := t.TempDir()
	// No SKILL.md: root .md files load (with descriptions).
	os.WriteFile(filepath.Join(dir, "one.md"), []byte("---\ndescription: one skill\n---\nbody"), 0o644)
	os.WriteFile(filepath.Join(dir, "no-desc.md"), []byte("# no description"), 0o644)
	// Subdirectory with SKILL.md found by recursion.
	sub := filepath.Join(dir, "sub")
	os.MkdirAll(sub, 0o755)
	writeSkill(t, sub, "sub", "name: sub-skill\ndescription: sub")

	result := LoadSkillsFromDir(dir, "user")
	if len(result.Skills) != 2 {
		t.Fatalf("skills = %d", len(result.Skills))
	}
	names := map[string]bool{}
	for _, skill := range result.Skills {
		names[skill.Name] = true
	}
	// Root .md files fall back to the parent dir name (the skill root).
	if !names[filepath.Base(dir)] || !names["sub-skill"] {
		t.Fatalf("names = %v", names)
	}
}

func TestLoadSkillsFromDirNameFallback(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "x", "description: uses dir name")
	result := LoadSkillsFromDir(dir, "user")
	if len(result.Skills) != 1 || result.Skills[0].Name != filepath.Base(dir) {
		t.Fatalf("fallback = %+v", result.Skills)
	}
}

func TestLoadSkillsFromDirIgnoreFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("skipped/\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "skipped"), 0o755)
	writeSkill(t, filepath.Join(dir, "skipped"), "s", "name: skipped\ndescription: hidden")
	kept := filepath.Join(dir, "kept")
	os.MkdirAll(kept, 0o755)
	writeSkill(t, kept, "k", "name: kept\ndescription: visible")

	result := LoadSkillsFromDir(dir, "user")
	if len(result.Skills) != 1 || result.Skills[0].Name != "kept" {
		t.Fatalf("ignored leak = %+v", result.Skills)
	}
}

func TestLoadSkillsFromDirDiagnostics(t *testing.T) {
	dir := t.TempDir()
	// SKILL.md without description: warning + no skill.
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: bad\n---\nbody"), 0o644)
	result := LoadSkillsFromDir(dir, "user")
	if len(result.Skills) != 0 {
		t.Fatalf("skill loaded = %+v", result.Skills)
	}
	if len(result.Diagnostics) == 0 {
		t.Fatal("no diagnostics")
	}

	// Invalid name keeps the skill but warns.
	os.RemoveAll(dir)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: BAD_NAME\ndescription: d\n---\nb"), 0o644)
	result = LoadSkillsFromDir(dir, "user")
	if len(result.Skills) != 1 || result.Skills[0].Name != "BAD_NAME" {
		t.Fatalf("skill = %+v", result.Skills)
	}
	found := false
	for _, diagnostic := range result.Diagnostics {
		if strings.Contains(diagnostic.Message, "invalid characters") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

func TestLoadSkillsCollisionsAndDedup(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "agent")
	cwd := filepath.Join(base, "work")

	// Same name in user and project: user wins (loaded first), collision recorded.
	userSkill := filepath.Join(agentDir, "skills", "review")
	writeSkill(t, userSkill, "u", "name: review\ndescription: user copy")
	projectSkill := filepath.Join(cwd, ConfigDirName(), "skills", "review")
	writeSkill(t, projectSkill, "p", "name: review\ndescription: project copy")

	result := LoadSkills(LoadSkillsOptions{
		Cwd: cwd, AgentDir: agentDir, IncludeDefaults: true,
	})
	if len(result.Skills) != 1 {
		t.Fatalf("skills = %d", len(result.Skills))
	}
	if result.Skills[0].Description != "user copy" {
		t.Fatalf("winner = %q", result.Skills[0].Description)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Type != "collision" {
		t.Fatalf("collisions = %+v", result.Diagnostics)
	}
	if result.Diagnostics[0].WinnerPath != filepath.Join(userSkill, "SKILL.md") {
		t.Fatalf("winner path = %q", result.Diagnostics[0].WinnerPath)
	}
}

func TestLoadSkillsExplicitPaths(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "agent")
	cwd := filepath.Join(base, "work")
	os.MkdirAll(cwd, 0o755)

	// Explicit file path.
	explicit := filepath.Join(base, "custom.md")
	os.WriteFile(explicit, []byte("---\ndescription: explicit\n---\nb"), 0o644)

	// Non-md and missing paths warn.
	notMd := filepath.Join(base, "script.sh")
	os.WriteFile(notMd, []byte("#!/bin/sh\n"), 0o644)
	result := LoadSkills(LoadSkillsOptions{
		Cwd: cwd, AgentDir: agentDir,
		SkillPaths: []string{explicit, filepath.Join(base, "missing"), notMd},
	})
	if len(result.Skills) != 1 || result.Skills[0].FilePath != explicit {
		t.Fatalf("skills = %+v", result.Skills)
	}
	warnings := 0
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Type == "warning" {
			warnings++
		}
	}
	if warnings != 2 {
		t.Fatalf("warnings = %d (%+v)", warnings, result.Diagnostics)
	}
}

func TestFormatSkillsForPromptXML(t *testing.T) {
	if got := FormatSkillsForPromptXML(nil, "read"); got != "" {
		t.Fatalf("empty = %q", got)
	}

	skills := []FullSkill{
		{Name: "review", Description: "Review & improve", FilePath: "/skills/review/SKILL.md"},
		{Name: "hidden", Description: "hidden", FilePath: "/hidden", DisableModelInvocation: true},
	}
	got := FormatSkillsForPromptXML(skills, "read")
	if !strings.Contains(got, "<available_skills>") || !strings.Contains(got, "<name>review</name>") {
		t.Fatalf("prompt = %q", got)
	}
	if !strings.Contains(got, "&amp;") {
		t.Fatal("xml escaping missing")
	}
	if strings.Contains(got, "hidden") {
		t.Fatal("disabled skill leaked")
	}
	if !strings.Contains(got, "Use the read tool") {
		t.Fatal("read attribution missing")
	}

	if got := FormatSkillsForPromptXML(skills, "bash"); !strings.Contains(got, "Use bash to load") {
		t.Fatal("bash attribution missing")
	}
}
