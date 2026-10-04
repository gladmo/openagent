package codingagent

// Tests for the app keybinding catalog (adapted from keybindings.test.ts
// surfaces).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUseWindowsKeybindings(t *testing.T) {
	if !UseWindowsKeybindings("windows", nil) {
		t.Fatal("windows not detected")
	}
	if !UseWindowsKeybindings("linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}) {
		t.Fatal("WSL_DISTRO_NAME not detected")
	}
	if !UseWindowsKeybindings("linux", map[string]string{"WSL_INTEROP": "1"}) {
		t.Fatal("WSL_INTEROP not detected")
	}
	if UseWindowsKeybindings("linux", nil) {
		t.Fatal("plain linux misdetected")
	}
	if UseWindowsKeybindings("darwin", nil) {
		t.Fatal("darwin misdetected")
	}
}

func TestAppKeybindingsCatalog(t *testing.T) {
	definitions := AppKeybindings()

	// Tui layer present.
	if _, ok := definitions["tui.editor.cursorUp"]; !ok {
		t.Fatal("tui layer missing")
	}

	// App layer essentials.
	for _, expected := range []string{
		"app.interrupt", "app.clear", "app.exit",
		"app.thinking.cycle", "app.model.cycleForward",
		"app.session.rename", "app.tree.filter.cycleForward",
	} {
		if _, ok := definitions[expected]; !ok {
			t.Errorf("missing %s", expected)
		}
	}

	if got := definitions["app.interrupt"].DefaultKeys; len(got) != 1 || got[0] != "escape" {
		t.Fatalf("interrupt = %v", got)
	}
	if got := definitions["app.model.cycleForward"].DefaultKeys; len(got) != 1 || got[0] != "ctrl+p" {
		t.Fatalf("model forward = %v", got)
	}
	// No-default commands have empty keys.
	if got := definitions["app.session.new"].DefaultKeys; len(got) != 0 {
		t.Fatalf("session new = %v", got)
	}
}

func TestAppKeybindingsPlatformOverrides(t *testing.T) {
	definitions := AppKeybindings()

	// The tui undo override is one of ctrl+z / alt+z / ctrl+-.
	undo := definitions["tui.editor.undo"].DefaultKeys
	if len(undo) != 1 {
		t.Fatalf("undo = %v", undo)
	}
	valid := map[string]bool{"ctrl+z": true, "alt+z": true, "ctrl+-": true}
	if !valid[string(undo[0])] {
		t.Fatalf("undo key = %v", undo)
	}

	// Search is ctrl+f or ctrl+shift+f.
	search := definitions["tui.altScreen.search"].DefaultKeys
	if len(search) != 1 || (string(search[0]) != "ctrl+f" && string(search[0]) != "ctrl+shift+f") {
		t.Fatalf("search = %v", search)
	}
}

func TestKeybindingNameMigrations(t *testing.T) {
	if KeybindingNameMigrations["undo"] != "tui.editor.undo" {
		t.Fatalf("undo = %q", KeybindingNameMigrations["undo"])
	}
	if KeybindingNameMigrations["interrupt"] != "app.interrupt" {
		t.Fatalf("interrupt = %q", KeybindingNameMigrations["interrupt"])
	}
	if KeybindingNameMigrations["treeFoldOrUp"] != "app.tree.foldOrUp" {
		t.Fatal("tree fold mapping missing")
	}
}

func TestToKeybindingsConfig(t *testing.T) {
	config := ToKeybindingsConfig(map[string]any{
		"app.interrupt": "ctrl+esc",
		"multi":         []any{"a", "b"},
		"invalid-mixed": []any{"a", 1},
		"invalid-type":  42,
	})
	if keys, ok := config["app.interrupt"]; !ok || len(keys) != 1 || keys[0] != "ctrl+esc" {
		t.Fatalf("single = %v", config["app.interrupt"])
	}
	if keys := config["multi"]; len(keys) != 2 {
		t.Fatalf("multi = %v", keys)
	}
	if _, ok := config["invalid-mixed"]; ok {
		t.Fatal("mixed array accepted")
	}
	if _, ok := config["invalid-type"]; ok {
		t.Fatal("non-string accepted")
	}
}

func TestLoadUserKeybindings(t *testing.T) {
	dir := t.TempDir()

	// Missing file: empty config.
	if config := LoadUserKeybindings(dir); len(config) != 0 {
		t.Fatalf("missing = %v", config)
	}

	// Legacy names migrate.
	os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(`{
		"undo": "ctrl+u",
		"app.interrupt": "ctrl+escape"
	}`), 0o644)
	config := LoadUserKeybindings(dir)
	if keys, ok := config["tui.editor.undo"]; !ok || len(keys) != 1 || keys[0] != "ctrl+u" {
		t.Fatalf("undo = %v", config["tui.editor.undo"])
	}
	if keys := config["app.interrupt"]; len(keys) != 1 || keys[0] != "ctrl+escape" {
		t.Fatalf("interrupt = %v", keys)
	}

	// Broken JSON: empty config.
	os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(`{broken`), 0o644)
	if config := LoadUserKeybindings(dir); len(config) != 0 {
		t.Fatalf("broken = %v", config)
	}
}
