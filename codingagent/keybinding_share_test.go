package codingagent

// Tests for keybinding hints, external editor, and session share
// (adapted from their reference tests).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatKeyText(t *testing.T) {
	if got := FormatKeyText("ctrl+shift+up", KeyTextFormatOptions{}); got != "ctrl+shift+up" {
		t.Fatalf("plain = %q", got)
	}
	if got := FormatKeyText("ctrl+shift+up", KeyTextFormatOptions{Capitalize: true}); got != "Ctrl+Shift+Up" {
		t.Fatalf("capital = %q", got)
	}
	// Alternatives split on /; darwin maps alt -> option.
	if got := FormatKeyText("ctrl+up/alt+enter", KeyTextFormatOptions{}); got != "ctrl+up/option+enter" && got != "ctrl+up/alt+enter" {
		t.Fatalf("alt = %q", got)
	}
	// Single parts pass through.
	if got := FormatKeyText("escape", KeyTextFormatOptions{Capitalize: true}); got != "Escape" {
		t.Fatalf("single = %q", got)
	}
}

func TestKeyTextOf(t *testing.T) {
	if got := KeyTextOf("app.interrupt"); got != "escape" {
		t.Fatalf("interrupt = %q", got)
	}
	// No defaults: empty.
	if got := KeyTextOf("app.session.new"); got != "" {
		t.Fatalf("no-default = %q", got)
	}
}

func TestRawKeyHint(t *testing.T) {
	hint := RawKeyHint("ctrl+p", "next model")
	if !strings.Contains(hint, "ctrl+p") || !strings.Contains(hint, "next model") {
		t.Fatalf("hint = %q", hint)
	}
	if !strings.Contains(hint, "\x1b[2m") {
		t.Fatalf("dim missing: %q", hint)
	}
}

func TestEditInExternalEditor(t *testing.T) {
	// A no-op "editor": cat copies stdin; use a script that leaves the
	// file unchanged via true.
	result := EditInExternalEditor("true", "unchanged content\n")
	if result == nil || result.Status != "complete" {
		t.Fatalf("result = %+v", result)
	}
	if result.Content != "unchanged content" {
		t.Fatalf("content = %q", result.Content)
	}

	// Failing editor.
	result = EditInExternalEditor("false", "x")
	if result == nil || result.Status != "failed" {
		t.Fatalf("failed = %+v", result)
	}

	// Missing editor.
	result = EditInExternalEditor("definitely-not-an-editor-xyz", "x")
	if result == nil || result.Status != "failed" {
		t.Fatalf("missing = %+v", result)
	}
}

func TestCreateShareTrailingEntries(t *testing.T) {
	session := newTestSession(t)
	_ = session

	parentID := "leaf-1"
	entries := CreateShareTrailingEntries(session, &parentID, "2024-01-01T00:00:00Z")
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	encoded, _ := json.Marshal(entries[0])
	var parsed map[string]any
	json.Unmarshal(encoded, &parsed)
	if parsed["type"] != "custom" || parsed["customType"] != "pi.share" {
		t.Fatalf("parsed = %v", parsed)
	}
	if parsed["parentId"] != "leaf-1" {
		t.Fatalf("parent = %v", parsed["parentId"])
	}
	if len(parsed["id"].(string)) != 8 {
		t.Fatalf("id = %v", parsed["id"])
	}
	data := parsed["data"].(map[string]any)
	if _, has := data["systemPrompt"]; !has {
		t.Fatal("system prompt missing")
	}
	if _, has := data["tools"]; !has {
		t.Fatal("tools missing")
	}
}

func TestExportSessionForShare(t *testing.T) {
	session := newTestSession(t)
	session.SessionManager.AppendMessage(map[string]any{"role": "user", "content": "hi"})

	dir := t.TempDir()
	path := filepath.Join(dir, "share.jsonl")
	if err := ExportSessionForShare(path, session); err != nil {
		t.Fatalf("export: %v", err)
	}

	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d (%q)", len(lines), string(data))
	}

	// Entries exclude the header (GetEntries), so the first line is the
	// user message.
	var first map[string]any
	json.Unmarshal([]byte(lines[0]), &first)
	if first["type"] != "message" {
		t.Fatalf("first = %v", first)
	}
	// The message role nests under the message object.
	message := first["message"].(map[string]any)
	if message["role"] != "user" {
		t.Fatalf("role = %v", message["role"])
	}

	// Trailing line: pi.share entry parented at the branch leaf.
	var trailing map[string]any
	json.Unmarshal([]byte(lines[len(lines)-1]), &trailing)
	if trailing["customType"] != "pi.share" {
		t.Fatalf("trailing = %v", trailing)
	}
	if trailing["parentId"] == nil {
		t.Fatal("trailing unparented")
	}
}

func TestGhCLIHelpers(t *testing.T) {
	// Availability is a boolean; status is an error or nil.
	available := GhCLIAvailable()
	_ = available
	if available {
		if err := GhCLIStatus(); err != nil {
			// gh installed but not logged in: the actionable error.
			if !strings.Contains(err.Error(), "gh auth login") {
				t.Fatalf("status = %v", err)
			}
		}
	}
}
