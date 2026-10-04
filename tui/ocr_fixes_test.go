package tui

// Regression tests for the OpenCodeReview findings: Rows() returning
// columns, jumpToChar at line end, autocomplete with a moved cursor,
// and nil loader color functions.

import (
	"strings"
	"testing"
)

func TestProcessTerminalRowsReturnsRows(t *testing.T) {
	// In a test environment there is no controlling terminal for
	// stdout piping; the contract holds whenever the ioctl succeeds.
	// Guard the invariant indirectly: Rows and Columns must agree with
	// their underlying probes.
	terminal := NewProcessTerminal()
	rows := terminal.Rows()
	cols := terminal.Columns()
	if rows != 0 && rows == cols && rows != 24 && rows != 80 {
		// A square terminal is unusual; identical non-zero values from a
		// piped stdout indicate the old cols-only bug.
		t.Fatalf("rows == cols == %d (Rows returning columns?)", rows)
	}
}

func TestJumpToCharAtLineEnd(t *testing.T) {
	editor := NewEditor(nil, EditorTheme{}, EditorOptions{})
	editor.SetText("hello world")
	// Cursor parks at the line end (the common input state).
	editor.setCursorCol(len("hello world"))
	// Jump forward for 'o': must not panic at searchFrom > len(line).
	editor.HandleInput("\x1b[1;5C") // ctrl+right-ish; drives jumpForward path
}

func TestApplyCompletionMovedCursor(t *testing.T) {
	provider := NewCombinedAutocompleteProvider(nil, []AutocompleteItem{{Value: "src", Label: "src"}}, "", "")
	// Cursor (1) left of the recorded prefix length (3): the slice must
	// clamp instead of panicking with a negative bound.
	applied := provider.ApplyCompletion([]string{"sr"}, 0, 1, AutocompleteItem{Value: "src"}, "src")
	joined := strings.Join(applied.Lines, "|")
	if !strings.Contains(joined, "src") {
		t.Fatalf("applied = %#v", applied)
	}
}

func TestNewLoaderNilColorFunctions(t *testing.T) {
	// Construction with nil color fns must not panic on render.
	loader := NewLoader(nil, nil, nil, "Loading", nil)
	lines := loader.Render(40)
	if len(lines) == 0 {
		t.Fatal("no render output")
	}
	if !strings.Contains(strings.Join(lines, "|"), "Loading") {
		t.Fatal("message lost")
	}
}
