package codingagent

// Tests for the project-trust store, resource detection, session-cwd
// guards, and the trust resolution order (adapted from trust-manager.test.ts
// and project-trust.test.ts surfaces).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectTrustStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewProjectTrustStore(dir)
	project := filepath.Join(dir, "project")
	os.MkdirAll(project, 0o755)

	// Unrecorded.
	if _, exists := store.Get(project); exists {
		t.Fatal("phantom decision")
	}

	// Record true.
	trusted := true
	store.Set(project, &trusted)
	if decision, ok := store.Get(project); !ok || !decision {
		t.Fatalf("decision = %v %v", decision, ok)
	}

	// Flip false.
	falseValue := false
	store.Set(project, &falseValue)
	if decision, _ := store.Get(project); decision {
		t.Fatal("flip lost")
	}

	// Clear (nil).
	store.Set(project, nil)
	if _, exists := store.Get(project); exists {
		t.Fatal("clear lost")
	}
}

func TestProjectTrustStoreNearestAncestor(t *testing.T) {
	dir := t.TempDir()
	store := NewProjectTrustStore(dir)
	parent := filepath.Join(dir, "parent")
	child := filepath.Join(parent, "child")
	os.MkdirAll(child, 0o755)

	trusted := true
	store.Set(parent, &trusted)
	// The child inherits the parent's decision.
	if decision, ok := store.Get(child); !ok || !decision {
		t.Fatalf("child = %v %v", decision, ok)
	}
}

func TestProjectTrustParentPath(t *testing.T) {
	if parent := GetProjectTrustParentPath("/a/b/c"); parent != "/a/b" {
		t.Fatalf("parent = %q", parent)
	}
	if parent := GetProjectTrustParentPath("/"); parent != "" {
		t.Fatalf("root parent = %q", parent)
	}
}

func TestHasTrustRequiringProjectResources(t *testing.T) {
	dir := t.TempDir()

	// Bare dir: no trust needed.
	if HasTrustRequiringProjectResources(dir) {
		t.Fatal("bare dir requires trust")
	}

	// .pi/settings.json gates trust.
	configDir := filepath.Join(dir, ConfigDirName())
	os.MkdirAll(configDir, 0o755)
	os.WriteFile(filepath.Join(configDir, "settings.json"), []byte("{}"), 0o644)
	if !HasTrustRequiringProjectResources(dir) {
		t.Fatal("settings.json not detected")
	}

	// Ancestor .agents/skills gates trust for the child.
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	os.MkdirAll(filepath.Join(outer, ".agents", "skills"), 0o755)
	os.MkdirAll(inner, 0o755)
	if !HasTrustRequiringProjectResources(inner) {
		t.Fatal("ancestor skills not detected")
	}
}

func TestGetMissingSessionCwdIssue(t *testing.T) {
	dir := t.TempDir()
	manager, err := SessionManagerCreate(dir, "", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	manager.AppendMessage(map[string]any{"role": "user", "content": "x"})

	// Valid cwd (the manager's dir): nil.
	if issue := GetMissingSessionCwdIssue(manager, "/fallback"); issue != nil {
		t.Fatalf("valid = %+v", issue)
	}

	// Point the manager at a vanished dir: issue detected.
	vanished := filepath.Join(dir, "gone")
	manager2, _ := SessionManagerCreate(vanished, "", nil)
	os.RemoveAll(vanished)
	manager2.AppendMessage(map[string]any{"role": "user", "content": "x"})
	issue := GetMissingSessionCwdIssue(manager2, "/fallback")
	if issue == nil {
		t.Fatal("vanished cwd not detected")
	}
	if issue.SessionFile == "" || issue.FallbackCwd != "/fallback" {
		t.Fatalf("issue = %+v", issue)
	}

	// Error + prompt forms.
	formatted := FormatMissingSessionCwdError(issue)
	if !strings.Contains(formatted, "does not exist") || !strings.Contains(formatted, "Session file:") {
		t.Fatalf("error = %q", formatted)
	}
	prompt := FormatMissingSessionCwdPrompt(issue)
	if !strings.Contains(prompt, "continue in current cwd") {
		t.Fatalf("prompt = %q", prompt)
	}

	// Assert form.
	err = AssertSessionCwdExists(manager2, "/fallback")
	if err == nil {
		t.Fatal("assert passed")
	}
	if _, isCwdError := err.(*MissingSessionCwdError); !isCwdError {
		t.Fatalf("error type = %T", err)
	}
}

func TestResolveProjectTrustedOrder(t *testing.T) {
	dir := t.TempDir()
	store := NewProjectTrustStore(dir)
	project := filepath.Join(dir, "p")
	os.MkdirAll(project, 0o755)

	base := ResolveProjectTrustedOptions{
		Cwd:                        project,
		TrustStore:                 store,
		HasTrustRequiringResources: true,
	}

	// Override wins.
	override := true
	options := base
	options.TrustOverride = &override
	if !ResolveProjectTrusted(options) {
		t.Fatal("override lost")
	}
	override = false
	options.TrustOverride = &override
	if ResolveProjectTrusted(options) {
		t.Fatal("false override lost")
	}

	// No trust-requiring resources: trusted.
	options = base
	options.HasTrustRequiringResources = false
	if !ResolveProjectTrusted(options) {
		t.Fatal("clean dir untrusted")
	}

	// Stored decision.
	options = base
	trusted := true
	store.Set(project, &trusted)
	if !ResolveProjectTrusted(options) {
		t.Fatal("stored true lost")
	}

	// Default policy always/never.
	os.RemoveAll(filepath.Join(dir, "trust.json"))
	store = NewProjectTrustStore(dir)
	options = base
	options.TrustStore = store
	options.DefaultProjectTrust = "always"
	if !ResolveProjectTrusted(options) {
		t.Fatal("always untrusted")
	}
	options.DefaultProjectTrust = "never"
	if ResolveProjectTrusted(options) {
		t.Fatal("never trusted")
	}

	// No UI: untrusted.
	options.DefaultProjectTrust = ""
	if ResolveProjectTrusted(options) {
		t.Fatal("headless trusted")
	}

	// UI prompt decision.
	options.HasUI = true
	options.SelectPrompt = func(cwd string) (bool, bool, bool) {
		return true, true, true
	}
	if !ResolveProjectTrusted(options) {
		t.Fatal("prompt trusted lost")
	}
	// Remembered.
	if decision, ok := store.Get(project); !ok || !decision {
		t.Fatalf("remembered = %v %v", decision, ok)
	}
}

func TestFormatProjectTrustPrompt(t *testing.T) {
	prompt := FormatProjectTrustPrompt("/my/project")
	if !strings.Contains(prompt, "Trust project folder?") || !strings.Contains(prompt, "/my/project") {
		t.Fatalf("prompt = %q", prompt)
	}
	if !strings.Contains(prompt, AppName()) || !strings.Contains(prompt, ConfigDirName()) {
		t.Fatalf("prompt segments = %q", prompt)
	}
}
