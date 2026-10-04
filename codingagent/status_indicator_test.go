package codingagent

// Tests for status_indicator.go — the ported working indicator that renders
// inside the editor's top border.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/tui"
)

func TestWorkingStatusIndicatorRendersInBorder(t *testing.T) {
	mode, _ := newInteractiveForTest(t)
	indicator := NewWorkingStatusIndicator(mode.TUI(), "Working")
	defer indicator.Dispose()
	indicator.Start()

	line := indicator.RenderInBorder(60)
	if !strings.Contains(line, "Working") {
		t.Fatalf("border line = %q, want the message", line)
	}
	if strings.Contains(line, "─") {
		t.Fatalf("border line = %q, must not be the plain rule", line)
	}
	if tui.VisibleWidth(line) > 60 {
		t.Fatalf("border line wider than the border: %q", line)
	}
	if spinner := indicator.RenderSpinnerInBorder(10); spinner == "" {
		t.Fatal("spinner glyph missing")
	}
	if got := indicator.RenderInBorder(0); got != "" {
		t.Fatalf("zero width = %q, want empty", got)
	}
}
