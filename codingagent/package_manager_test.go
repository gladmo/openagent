package codingagent

// Tests for the package manager (ported from package-manager.test.ts
// surfaces: parsing, settings add/remove, install paths, manifest
// resolution).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newPackageManager(t *testing.T) (*PackageManager, string, string) {
	t.Helper()
	base := t.TempDir()
	t.Setenv(EnvAgentDirName(), filepath.Join(base, "agent"))
	agentDir := filepath.Join(base, "agent")
	cwd := filepath.Join(base, "work")
	os.MkdirAll(agentDir, 0o755)
	os.MkdirAll(cwd, 0o755)
	settings := SettingsManagerCreate(cwd, agentDir, nil)
	return NewPackageManager(PackageManagerOptions{
		Cwd: cwd, AgentDir: agentDir, SettingsManager: settings,
	}), cwd, agentDir
}

func TestParsePackageSource(t *testing.T) {
	npm := ParsePackageSource("npm:@scope/pkg@1.2.3")
	if npm == nil || npm.Type != "npm" || npm.Spec != "@scope/pkg@1.2.3" {
		t.Fatalf("npm = %+v", npm)
	}
	if !npm.Autoload {
		t.Fatal("npm not autoload")
	}

	git := ParsePackageSource("git:github.com/user/repo")
	if git == nil || git.Type != "git" || git.Git == nil || git.Git.Path != "user/repo" {
		t.Fatalf("git = %+v", git)
	}

	local := ParsePackageSource("/abs/path")
	if local == nil || local.Type != "local" || local.Path != "/abs/path" {
		t.Fatalf("local = %+v", local)
	}

	if ParsePackageSource("") != nil {
		t.Fatal("empty accepted")
	}
}

func TestPackageManagerAddRemoveSource(t *testing.T) {
	manager, _, _ := newPackageManager(t)

	if !manager.AddSourceToSettings("npm:test-pkg", false) {
		t.Fatal("add returned false")
	}
	// Duplicate: no change.
	if manager.AddSourceToSettings("npm:test-pkg", false) {
		t.Fatal("duplicate add returned true")
	}

	packages := manager.currentPackages(false)
	if len(packages) != 1 || packages[0] != "npm:test-pkg" {
		t.Fatalf("packages = %v", packages)
	}

	// Project scope is separate.
	if !manager.AddSourceToSettings("npm:proj-pkg", true) {
		t.Fatal("project add failed")
	}
	if len(manager.currentPackages(false)) != 1 {
		t.Fatalf("global mutated = %v", manager.currentPackages(false))
	}
	if len(manager.currentPackages(true)) != 1 {
		t.Fatalf("project = %v", manager.currentPackages(true))
	}

	// Remove.
	if !manager.RemoveSourceFromSettings("npm:test-pkg", false) {
		t.Fatal("remove returned false")
	}
	if len(manager.currentPackages(false)) != 0 {
		t.Fatalf("after remove = %v", manager.currentPackages(false))
	}
	// Absent remove: false.
	if manager.RemoveSourceFromSettings("npm:test-pkg", false) {
		t.Fatal("absent remove returned true")
	}
}

func TestPackageManagerGetInstalledPath(t *testing.T) {
	manager, _, agentDir := newPackageManager(t)

	// Simulate an npm install with a package.json.
	installDir := filepath.Join(agentDir, "packages", "npm", "test-pkg")
	os.MkdirAll(installDir, 0o755)
	os.WriteFile(filepath.Join(installDir, "marker"), []byte("x"), 0o644)

	if got := manager.GetInstalledPath("npm:test-pkg", ScopeUser); got != installDir {
		t.Fatalf("npm path = %q, want %q", got, installDir)
	}

	// Missing package: empty.
	if got := manager.GetInstalledPath("npm:not-installed", ScopeUser); got != "" {
		t.Fatalf("missing = %q", got)
	}

	// Local path resolves relative to the packages base.
	localDir := filepath.Join(agentDir, "local-ext")
	os.MkdirAll(localDir, 0o755)
	// Absolute local paths resolve directly.
	if got := manager.GetInstalledPath(localDir, ScopeUser); got != localDir {
		t.Fatalf("local = %q", got)
	}
}

func TestPackageManagerResolveManifest(t *testing.T) {
	manager, _, agentDir := newPackageManager(t)
	// Isolate ~/.agents/skills so the exact-count assertion stays hermetic.
	t.Setenv("HOME", filepath.Dir(agentDir))

	// Install a fake package with a pi manifest.
	installDir := filepath.Join(agentDir, "packages", "npm", "full-pkg")
	os.MkdirAll(filepath.Join(installDir, "exts"), 0o755)
	os.MkdirAll(filepath.Join(installDir, "skills"), 0o755)
	os.WriteFile(filepath.Join(installDir, "package.json"), []byte(`{
		"name": "full-pkg",
		"pi": {
			"extensions": ["exts/main.js"],
			"skills": ["skills/review"]
		}
	}`), 0o644)

	manager.AddSourceToSettings("npm:full-pkg", false)
	result := manager.Resolve(nil)

	if len(result.Extensions) != 1 {
		t.Fatalf("extensions = %+v", result.Extensions)
	}
	expected := filepath.Join(installDir, "exts/main.js")
	if result.Extensions[0].Path != expected {
		t.Fatalf("ext path = %q, want %q", result.Extensions[0].Path, expected)
	}
	if !result.Extensions[0].Enabled {
		t.Fatal("not enabled")
	}
	meta := result.Extensions[0].Metadata
	if meta.Source != "npm:full-pkg" || meta.Scope != ScopeUser || meta.Origin != OriginPackage {
		t.Fatalf("metadata = %+v", meta)
	}
	if meta.PackageRoot != installDir {
		t.Fatalf("packageRoot = %q", meta.PackageRoot)
	}

	if len(result.Skills) != 1 || result.Skills[0].Path != filepath.Join(installDir, "skills/review") {
		t.Fatalf("skills = %+v", result.Skills)
	}
	if len(result.Prompts) != 0 || len(result.Themes) != 0 {
		t.Fatalf("empty kinds = %+v %+v", result.Prompts, result.Themes)
	}
}

func TestPackageManagerResolveMissing(t *testing.T) {
	manager, _, _ := newPackageManager(t)
	manager.AddSourceToSettings("npm:never-installed", false)

	missingReported := ""
	result := manager.Resolve(func(source string) string {
		missingReported = source
		return "skip"
	})
	if missingReported != "npm:never-installed" {
		t.Fatalf("missing = %q", missingReported)
	}
	if len(result.Extensions) != 0 {
		t.Fatal("missing package produced resources")
	}
}

func TestPackageManagerListConfigured(t *testing.T) {
	manager, _, agentDir := newPackageManager(t)

	installDir := filepath.Join(agentDir, "packages", "npm", "listed")
	os.MkdirAll(installDir, 0o755)
	manager.AddSourceToSettings("npm:listed", false)
	manager.AddSourceToSettings("npm:missing", false)

	configured := manager.ListConfiguredPackages()
	if len(configured) != 2 {
		t.Fatalf("configured = %+v", configured)
	}
	bySource := map[string]ConfiguredPackage{}
	for _, entry := range configured {
		bySource[entry.Source] = entry
	}
	if entry := bySource["npm:listed"]; entry.InstalledPath == "" {
		t.Fatalf("listed = %+v", entry)
	}
	if entry := bySource["npm:missing"]; entry.InstalledPath != "" {
		t.Fatalf("missing = %+v", entry)
	}
}

func TestPackageManagerProgress(t *testing.T) {
	manager, _, _ := newPackageManager(t)
	events := []ProgressEvent{}
	manager.SetProgressCallback(func(event ProgressEvent) {
		events = append(events, event)
	})
	manager.emitProgress(ProgressEvent{Type: "start", Action: "install", Source: "npm:x"})
	if len(events) != 1 || events[0].Action != "install" {
		t.Fatalf("events = %+v", events)
	}
	// nil callback is a no-op.
	manager.SetProgressCallback(nil)
	manager.emitProgress(ProgressEvent{Type: "complete"})
}

// --- .agents/skills auto-discovery (package-manager.test.ts) ---

func writeSkillFile(t *testing.T, path, name, description string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findSkill(result *ResolvedPaths, path string) *ResolvedResource {
	for i, entry := range result.Skills {
		if entry.Path == path {
			return &result.Skills[i]
		}
	}
	return nil
}

func newResolveManager(t *testing.T, cwd, agentDir string) *PackageManager {
	t.Helper()
	os.MkdirAll(cwd, 0o755)
	os.MkdirAll(agentDir, 0o755)
	return NewPackageManager(PackageManagerOptions{
		Cwd: cwd, AgentDir: agentDir,
		SettingsManager: SettingsManagerCreate(cwd, agentDir, nil),
	})
}

func TestPackageManagerResolveAgentsSkillsToGitRoot(t *testing.T) {
	base := t.TempDir()
	repoRoot := filepath.Join(base, "repo")
	nestedCwd := filepath.Join(repoRoot, "packages", "feature")
	os.MkdirAll(nestedCwd, 0o755)
	os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755)

	aboveRepoSkill := filepath.Join(base, ".agents", "skills", "above-repo", "SKILL.md")
	writeSkillFile(t, aboveRepoSkill, "above-repo", "above")
	repoRootSkill := filepath.Join(repoRoot, ".agents", "skills", "repo-root", "SKILL.md")
	writeSkillFile(t, repoRootSkill, "repo-root", "repo")
	nestedSkill := filepath.Join(repoRoot, "packages", ".agents", "skills", "nested", "SKILL.md")
	writeSkillFile(t, nestedSkill, "nested", "nested")

	manager := newResolveManager(t, nestedCwd, filepath.Join(base, "agent"))
	result := manager.Resolve(nil)

	if entry := findSkill(result, repoRootSkill); entry == nil || !entry.Enabled {
		t.Fatalf("repo-root skill = %+v", entry)
	}
	if entry := findSkill(result, nestedSkill); entry == nil || !entry.Enabled {
		t.Fatalf("nested skill = %+v", entry)
	}
	if findSkill(result, aboveRepoSkill) != nil {
		t.Fatal("above-repo skill discovered")
	}
}

func TestPackageManagerResolveAgentsSkillsWithoutGitRepo(t *testing.T) {
	base := t.TempDir()
	nestedCwd := filepath.Join(base, "non-repo", "a", "b")

	rootSkill := filepath.Join(base, "non-repo", ".agents", "skills", "root", "SKILL.md")
	writeSkillFile(t, rootSkill, "root", "root")
	middleSkill := filepath.Join(base, "non-repo", "a", ".agents", "skills", "middle", "SKILL.md")
	writeSkillFile(t, middleSkill, "middle", "middle")

	manager := newResolveManager(t, nestedCwd, filepath.Join(base, "agent"))
	result := manager.Resolve(nil)

	if entry := findSkill(result, rootSkill); entry == nil || !entry.Enabled {
		t.Fatalf("root skill = %+v", entry)
	}
	if entry := findSkill(result, middleSkill); entry == nil || !entry.Enabled {
		t.Fatalf("middle skill = %+v", entry)
	}
}

func TestPackageManagerResolveAgentsSkillsMarkdownModes(t *testing.T) {
	base := t.TempDir()
	cwd := filepath.Join(base, "work")
	os.MkdirAll(cwd, 0o755)
	agentsSkills := filepath.Join(base, ".agents", "skills")

	rootSkill := filepath.Join(agentsSkills, "root-file.md")
	writeSkillFile(t, rootSkill, "root-file", "Root markdown file")
	nestedSkill := filepath.Join(agentsSkills, "nested-skill", "SKILL.md")
	writeSkillFile(t, nestedSkill, "nested-skill", "Nested skill")
	nestedMarkdownSkill := filepath.Join(agentsSkills, "third-party", "child-skill.md")
	writeSkillFile(t, nestedMarkdownSkill, "child-skill", "Nested markdown skill")
	deeplyNestedSkill := filepath.Join(agentsSkills, "third-party", "vendor", "pack", "deep-skill.md")
	writeSkillFile(t, deeplyNestedSkill, "deep-skill", "Deeply nested markdown skill")

	manager := newResolveManager(t, cwd, filepath.Join(base, "agent"))
	result := manager.Resolve(nil)

	if findSkill(result, rootSkill) != nil {
		t.Fatal("root markdown discovered in agents mode")
	}
	for _, path := range []string{nestedSkill, nestedMarkdownSkill, deeplyNestedSkill} {
		if entry := findSkill(result, path); entry == nil || !entry.Enabled {
			t.Fatalf("skill %s = %+v", path, entry)
		}
	}
}

func TestPackageManagerResolveAgentsSkillsUserScopeUnderHome(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)

	cwd := filepath.Join(base, "scratch", "nested")
	os.MkdirAll(cwd, 0o755)
	agentDir := filepath.Join(base, ".pi", "agent")
	os.MkdirAll(agentDir, 0o755)

	homeSkill := filepath.Join(base, ".agents", "skills", "home-skill", "SKILL.md")
	writeSkillFile(t, homeSkill, "home-skill", "home")

	manager := NewPackageManager(PackageManagerOptions{
		Cwd: cwd, AgentDir: agentDir,
		SettingsManager: SettingsManagerInMemory(Settings{}, nil),
	})

	result := manager.Resolve(nil)

	matches := 0
	var match *ResolvedResource
	for i, entry := range result.Skills {
		if entry.Path == homeSkill {
			matches++
			match = &result.Skills[i]
		}
	}
	if matches != 1 || match == nil || !match.Enabled {
		t.Fatalf("home skill matches = %d (%+v)", matches, match)
	}
	if match.Metadata.Scope != ScopeUser || match.Metadata.Source != "auto" {
		t.Fatalf("metadata = %+v", match.Metadata)
	}
}

func TestPackageManagerResolveAgentsSkillsSymlinkDedupe(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)

	agentDir := filepath.Join(base, "agent")
	os.MkdirAll(agentDir, 0o755)
	agentsSkillsDir := filepath.Join(base, ".agents", "skills")
	skillPath := filepath.Join(agentsSkillsDir, "foo", "SKILL.md")
	writeSkillFile(t, skillPath, "foo", "foo")

	if err := os.Symlink(agentsSkillsDir, filepath.Join(agentDir, "skills")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	cwd := filepath.Join(base, "work")
	manager := newResolveManager(t, cwd, agentDir)

	result := manager.Resolve(nil)

	fooCount := 0
	for _, entry := range result.Skills {
		if strings.HasSuffix(filepath.ToSlash(entry.Path), "foo/SKILL.md") {
			fooCount++
		}
	}
	if fooCount != 1 {
		t.Fatalf("foo/SKILL.md entries = %d, want 1", fooCount)
	}
}

func TestPackageManagerResolveAutoDiscoversRootMarkdownSkills(t *testing.T) {
	manager, _, agentDir := newPackageManager(t)

	skillFile := filepath.Join(agentDir, "skills", "single-file.md")
	writeSkillFile(t, skillFile, "single-file", "A root markdown skill")

	result := manager.Resolve(nil)
	if entry := findSkill(result, skillFile); entry == nil || !entry.Enabled {
		t.Fatalf("root markdown skill = %+v", entry)
	}
}

func TestPackageManagerResolveParentGitignoreDoesNotGatePiDiscovery(t *testing.T) {
	manager, cwd, _ := newPackageManager(t)

	if err := os.WriteFile(filepath.Join(cwd, ".gitignore"), []byte(".pi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(cwd, ConfigDirName(), "skills", "auto-skill", "SKILL.md")
	writeSkillFile(t, skillPath, "auto-skill", "Auto")

	result := manager.Resolve(nil)
	if entry := findSkill(result, skillPath); entry == nil || !entry.Enabled {
		t.Fatalf("auto-discovered skill = %+v", entry)
	}
}

// Port of 6a51a39d4: resource setting patterns expand ~.
func TestResourcePatternsExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	normalized := normalizeExactPattern("~/skills/**")
	if !strings.HasPrefix(normalized, filepath.ToSlash(home)+"/") {
		t.Fatalf("normalized = %q", normalized)
	}
	if normalizeExactPattern("./relative") != "relative" {
		t.Fatal("relative prefix kept")
	}
}
