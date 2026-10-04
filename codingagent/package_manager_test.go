package codingagent

// Tests for the package manager (ported from package-manager.test.ts
// surfaces: parsing, settings add/remove, install paths, manifest
// resolution).

import (
	"os"
	"path/filepath"
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
