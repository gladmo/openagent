package codingagent

// Tests for the settings manager (ported from settings-manager.test.ts
// surfaces: merge, migrations, trust, modified-field persistence).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsManagerMerge(t *testing.T) {
	storage := NewInMemorySettingsStorage()
	writeSettings(t, storage, SettingsScopeGlobal, Settings{
		"theme":  "dark",
		"nested": map[string]any{"a": float64(1), "b": float64(2)},
	})
	writeSettings(t, storage, SettingsScopeProject, Settings{
		"theme":       "light",
		"nested":      map[string]any{"b": float64(3), "c": float64(4)},
		"projectOnly": true,
	})

	manager := SettingsManagerFromStorage(storage, nil)

	if theme, _ := manager.GetSetting("theme"); theme != "light" {
		t.Fatalf("override = %v", theme)
	}
	nested, _ := manager.GetSetting("nested")
	asMap := nested.(map[string]any)
	if asMap["a"] != 1.0 || asMap["b"] != 3.0 || asMap["c"] != 4.0 {
		t.Fatalf("nested merge = %v", asMap)
	}
	if projectOnly, _ := manager.GetSetting("projectOnly"); projectOnly != true {
		t.Fatal("project-only setting lost")
	}
	// Global scope untouched by project overrides.
	if theme := manager.GetGlobalSettings()["theme"]; theme != "dark" {
		t.Fatalf("global = %v", theme)
	}
}

func writeSettings(t *testing.T, storage SettingsStorage, scope SettingsScope, settings Settings) {
	t.Helper()
	encoded, _ := json.Marshal(settings)
	storage.WithLock(scope, func(current string) string { return string(encoded) })
}

func TestSettingsManagerMigrations(t *testing.T) {
	settings := MigrateSettings(Settings{
		"queueMode":  "all",
		"websockets": false,
		"skills": map[string]any{
			"enableSkillCommands": false,
			"customDirectories":   []any{"/a", "/b"},
		},
		"retry": map[string]any{
			"maxDelayMs": float64(30000),
		},
	})

	if settings["steeringMode"] != "all" {
		t.Fatalf("steering = %v", settings["steeringMode"])
	}
	if _, has := settings["queueMode"]; has {
		t.Fatal("queueMode survived")
	}
	if settings["transport"] != "sse" {
		t.Fatalf("transport = %v", settings["transport"])
	}
	if _, has := settings["websockets"]; has {
		t.Fatal("websockets survived")
	}
	if settings["enableSkillCommands"] != false {
		t.Fatalf("skill commands = %v", settings["enableSkillCommands"])
	}
	if skills, ok := settings["skills"].([]any); !ok || len(skills) != 2 {
		t.Fatalf("skills = %v", settings["skills"])
	}
	retry := settings["retry"].(map[string]any)
	if _, has := retry["maxDelayMs"]; has {
		t.Fatal("maxDelayMs survived")
	}
	provider := retry["provider"].(map[string]any)
	if provider["maxRetryDelayMs"] != 30000.0 {
		t.Fatalf("provider = %v", provider)
	}
}

func TestSettingsManagerTrust(t *testing.T) {
	storage := NewInMemorySettingsStorage()
	writeSettings(t, storage, SettingsScopeProject, Settings{"theme": "project"})

	untrusted := false
	manager := SettingsManagerFromStorage(storage, &SettingsManagerCreateOptions{ProjectTrusted: &untrusted})
	if theme, _ := manager.GetSetting("theme"); theme != nil {
		t.Fatalf("untrusted leaked = %v", theme)
	}
	if manager.IsProjectTrusted() {
		t.Fatal("trust state wrong")
	}

	// Writing project settings while untrusted errors.
	if err := manager.SetProjectSetting("x", 1); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("err = %v", err)
	}

	// Trusting reloads the project scope.
	manager.SetProjectTrusted(true)
	if theme, _ := manager.GetSetting("theme"); theme != "project" {
		t.Fatalf("trusted theme = %v", theme)
	}

	// Untrusting drops it again.
	manager.SetProjectTrusted(false)
	if theme, _ := manager.GetSetting("theme"); theme != nil {
		t.Fatalf("re-untrusted = %v", theme)
	}
}

func TestSettingsManagerSetPersists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvAgentDirName(), dir)
	cwd := filepath.Join(dir, "project")
	os.MkdirAll(cwd, 0o755)

	manager := SettingsManagerCreate(cwd, dir, nil)

	manager.SetDefaultProvider("anthropic")
	manager.SetDefaultModel("claude-opus-4-8")

	// The global file persisted both fields.
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var persisted Settings
	json.Unmarshal(raw, &persisted)
	if persisted["defaultProvider"] != "anthropic" || persisted["defaultModel"] != "claude-opus-4-8" {
		t.Fatalf("persisted = %v", persisted)
	}

	// Reload sees them.
	manager.Reload()
	if manager.GetDefaultProvider() != "anthropic" || manager.GetDefaultModel() != "claude-opus-4-8" {
		t.Fatalf("reload = %s/%s", manager.GetDefaultProvider(), manager.GetDefaultModel())
	}
}

func TestSettingsManagerNestedPersistence(t *testing.T) {
	storage := NewInMemorySettingsStorage()
	writeSettings(t, storage, SettingsScopeGlobal, Settings{
		"modelThinkingLevels": map[string]any{"other/model": "high"},
		"theme":               "keep-me",
	})
	manager := SettingsManagerFromStorage(storage, nil)

	// Nested write merges into the stored nested object.
	manager.SetModelThinkingLevel("anthropic/model", "low")

	global := manager.GetGlobalSettings()
	levels := global["modelThinkingLevels"].(map[string]any)
	if levels["anthropic/model"] != "low" || levels["other/model"] != "high" {
		t.Fatalf("levels = %v", levels)
	}
	if global["theme"] != "keep-me" {
		t.Fatalf("theme lost = %v", global["theme"])
	}
}

func TestSettingsManagerAccessors(t *testing.T) {
	manager := SettingsManagerInMemory(Settings{}, nil)

	if manager.GetSteeringMode() != "one-at-a-time" {
		t.Fatalf("steering default = %q", manager.GetSteeringMode())
	}
	if manager.GetFollowUpMode() != "one-at-a-time" {
		t.Fatalf("follow-up default = %q", manager.GetFollowUpMode())
	}

	manager.SetSteeringMode("all")
	if manager.GetSteeringMode() != "all" {
		t.Fatal("steering write failed")
	}

	// Path-like themes are filtered by GetTheme.
	manager.SetTheme("custom/my-theme")
	if manager.GetTheme() != "" {
		t.Fatalf("path theme = %q", manager.GetTheme())
	}
	if manager.GetThemeSetting() != "custom/my-theme" {
		t.Fatalf("raw theme = %q", manager.GetThemeSetting())
	}
	manager.SetTheme("dark")
	if manager.GetTheme() != "dark" {
		t.Fatal("plain theme filtered")
	}

	// Bool defaults.
	if manager.GetBoolSetting("quietStartup", false) {
		t.Fatal("bool default wrong")
	}
	manager.SetSetting("quietStartup", true)
	if !manager.GetBoolSetting("quietStartup", false) {
		t.Fatal("bool write failed")
	}
}

func TestSettingsManagerLoadError(t *testing.T) {
	storage := NewInMemorySettingsStorage()
	storage.WithLock(SettingsScopeGlobal, func(current string) string { return "{broken" })

	manager := SettingsManagerFromStorage(storage, nil)
	if manager.GetGlobalSettingsLoadError() == nil {
		t.Fatal("load error nil")
	}
	errors := manager.DrainErrors()
	if len(errors) != 1 || errors[0].Scope != SettingsScopeGlobal {
		t.Fatalf("errors = %+v", errors)
	}
	if drained := manager.DrainErrors(); len(drained) != 0 {
		t.Fatalf("drained = %+v", drained)
	}
	// The manager still works with defaults.
	if manager.GetSteeringMode() != "one-at-a-time" {
		t.Fatal("broken global broke defaults")
	}
}

func TestSettingsManagerInMemoryRoundTrip(t *testing.T) {
	manager := SettingsManagerInMemory(Settings{"theme": "x"}, nil)
	if manager.GetTheme() != "x" {
		t.Fatalf("theme = %q", manager.GetTheme())
	}

	// ApplyOverrides layers on top.
	manager.ApplyOverrides(Settings{"theme": "y", "extra": true})
	if manager.GetTheme() != "y" {
		t.Fatal("override failed")
	}
	if extra, _ := manager.GetSetting("extra"); extra != true {
		t.Fatal("extra missing")
	}
	// Overrides do not persist back to the stored scope.
	if theme := manager.GetGlobalSettings()["theme"]; theme != "x" {
		t.Fatalf("global mutated = %v", theme)
	}
}

func TestSettingsManagerSessionDir(t *testing.T) {
	manager := SettingsManagerInMemory(Settings{}, nil)
	if manager.GetSessionDir() != "" {
		t.Fatalf("empty = %q", manager.GetSessionDir())
	}
	manager.SetSetting("sessionDir", "~/sessions")
	if got := manager.GetSessionDir(); got == "~/sessions" || !strings.HasSuffix(got, "sessions") {
		t.Fatalf("session dir = %q", got)
	}
}

func TestSettingsManagerChangelog(t *testing.T) {
	manager := SettingsManagerInMemory(Settings{}, nil)
	if manager.GetLastChangelogVersion() != "" {
		t.Fatal("initial changelog non-empty")
	}
	manager.SetLastChangelogVersion("1.2.3")
	if manager.GetLastChangelogVersion() != "1.2.3" {
		t.Fatal("changelog write failed")
	}
}
