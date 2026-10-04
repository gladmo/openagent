package codingagent

// Port of modes/interactive/components/keybinding-hints.ts and
// modes/interactive/external-editor.ts: key-text formatting with
// darwin's alt->option mapping, and the external-editor round trip.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// KeyTextFormatOptions control key-part casing.
type KeyTextFormatOptions struct {
	Capitalize bool
}

func formatKeyPart(part string, options KeyTextFormatOptions) string {
	displayPart := part
	if runtime.GOOS == "darwin" && strings.EqualFold(part, "alt") {
		displayPart = "option"
	}
	if options.Capitalize && displayPart != "" {
		return strings.ToUpper(displayPart[:1]) + displayPart[1:]
	}
	return displayPart
}

// FormatKeyText renders a key spec (ctrl+shift+up/down alt forms) with
// per-part formatting.
func FormatKeyText(key string, options KeyTextFormatOptions) string {
	alternatives := strings.Split(key, "/")
	for i, alternative := range alternatives {
		parts := strings.Split(alternative, "+")
		for j, part := range parts {
			parts[j] = formatKeyPart(part, options)
		}
		alternatives[i] = strings.Join(parts, "+")
	}
	return strings.Join(alternatives, "/")
}

// KeyTextOf renders a binding's current keys.
func KeyTextOf(binding string) string {
	return formatKeysOf(keysOfBinding(binding), KeyTextFormatOptions{})
}

// KeyDisplayTextOf renders capitalized (already defined in
// clipboard_browser.go for the catalog; this variant takes raw keys).
func formatKeysOf(keys []string, options KeyTextFormatOptions) string {
	if len(keys) == 0 {
		return ""
	}
	return FormatKeyText(strings.Join(keys, "/"), options)
}

func keysOfBinding(binding string) []string {
	definitions := AppKeybindings()
	if definition, ok := definitions[Keybinding(binding)]; ok {
		out := make([]string, len(definition.DefaultKeys))
		for i, key := range definition.DefaultKeys {
			out[i] = string(key)
		}
		return out
	}
	return nil
}

// RawKeyHint renders a dim key + muted description hint.
func RawKeyHint(key, description string) string {
	return "\x1b[2m" + FormatKeyText(key, KeyTextFormatOptions{}) + "\x1b[22m" +
		"\x1b[90m " + description + "\x1b[39m"
}

// KeyHint renders a dim resolved key + muted description hint
// (keybinding-hints.ts keyHint).
func KeyHint(binding, description string) string {
	return "\x1b[2m" + KeyTextOf(binding) + "\x1b[22m" +
		"\x1b[90m " + description + "\x1b[39m"
}

// ============================================================================
// external-editor.ts
// ============================================================================

// ExternalEditorResult is the round-trip outcome.
type ExternalEditorResult struct {
	Status  string // "complete" | "failed"
	Content string
}

// EditInExternalEditor launches the editor command on a temp file and
// returns the edited content (BOM stripped, one trailing newline
// removed).
func EditInExternalEditor(command, content string) *ExternalEditorResult {
	directory, err := os.MkdirTemp(os.TempDir(), "pi-editor-")
	if err != nil {
		return &ExternalEditorResult{Status: "failed"}
	}
	defer os.RemoveAll(directory)

	filePath := filepath.Join(directory, "prompt.md")
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return &ExternalEditorResult{Status: "failed"}
	}

	parts := strings.Split(command, " ")
	editor := parts[0]
	editorArgs := append(append([]string(nil), parts[1:]...), filePath)

	os.Stdout.WriteString("Launching external editor: " + command + "\nPi will resume when the editor exits.\n")

	cmd := exec.Command(editor, editorArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return &ExternalEditorResult{Status: "failed"}
	}

	edited, err := os.ReadFile(filePath)
	if err != nil {
		return &ExternalEditorResult{Status: "failed"}
	}
	text := strings.TrimSuffix(StripBom(string(edited)), "\n")
	return &ExternalEditorResult{Status: "complete", Content: text}
}
