package codingagent

// Tests for the bash execution component (adapted from
// bash-execution.test.ts).

import (
	"strings"
	"testing"
)

func TestStripAnsiCodes(t *testing.T) {
	if got := StripAnsiCodes("\x1b[31mred\x1b[0m plain"); got != "red plain" {
		t.Fatalf("sgr = %q", got)
	}
	if got := StripAnsiCodes("\x1b]8;;http://x\x07link\x1b]8;;\x07"); got != "link" {
		t.Fatalf("osc = %q", got)
	}
	if got := StripAnsiCodes("no escapes"); got != "no escapes" {
		t.Fatalf("plain = %q", got)
	}
}

func TestBashExecutionInitialState(t *testing.T) {
	component := NewBashExecutionComponent("echo hi", nil, false)
	if component.Status() != "running" {
		t.Fatalf("status = %q", component.Status())
	}
	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "$ echo hi") {
		t.Fatalf("header = %q", joined)
	}
	if !strings.Contains(joined, "Running...") {
		t.Fatalf("loader = %q", joined)
	}
}

func TestBashExecutionAppendOutput(t *testing.T) {
	component := NewBashExecutionComponent("cat", nil, false)
	component.AppendOutput("first\n")
	component.AppendOutput("second\n")

	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "first") || !strings.Contains(joined, "second") {
		t.Fatalf("output = %q", joined)
	}
}

func TestBashExecutionAppendOutputIncompleteLine(t *testing.T) {
	component := NewBashExecutionComponent("x", nil, false)
	component.AppendOutput("partial")
	component.AppendOutput("-continuation\n")

	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "partial-continuation") {
		t.Fatalf("joined = %q", joined)
	}
}

func TestBashExecutionAppendOutputStripsAnsi(t *testing.T) {
	component := NewBashExecutionComponent("x", nil, false)
	component.AppendOutput("\x1b[32mgreen output\x1b[0m\n")

	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "green output") {
		t.Fatalf("output = %q", joined)
	}
	if strings.Contains(joined, "\x1b[32m") {
		t.Fatalf("ansi leaked = %q", joined)
	}
}

func TestBashExecutionComplete(t *testing.T) {
	component := NewBashExecutionComponent("true", nil, false)
	component.AppendOutput("done\n")
	code := 0
	component.SetComplete(&code, false, 0, 0, "")

	if component.Status() != "complete" {
		t.Fatalf("status = %q", component.Status())
	}
	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Completed") {
		t.Fatalf("footer = %q", joined)
	}
	if strings.Contains(joined, "Running...") {
		t.Fatalf("loader survived: %q", joined)
	}
}

func TestBashExecutionErrorStatus(t *testing.T) {
	component := NewBashExecutionComponent("false", nil, false)
	code := 1
	component.SetComplete(&code, false, 0, 0, "")

	if component.Status() != "error" {
		t.Fatalf("status = %q", component.Status())
	}
	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "Exited with code 1") {
		t.Fatalf("footer = %v", lines)
	}
}

func TestBashExecutionCancelled(t *testing.T) {
	component := NewBashExecutionComponent("sleep 100", nil, false)
	component.SetComplete(nil, true, 0, 0, "")
	if component.Status() != "cancelled" {
		t.Fatalf("status = %q", component.Status())
	}
	if !strings.Contains(strings.Join(component.Render(60), "\n"), "Cancelled") {
		t.Fatal("no cancelled footer")
	}
}

func TestBashExecutionPreviewHidden(t *testing.T) {
	component := NewBashExecutionComponent("big", nil, false)
	var chunk strings.Builder
	for i := 0; i < 60; i++ {
		chunk.WriteString("line-")
		chunk.WriteString(itoaBash(i))
		chunk.WriteString("\n")
	}
	component.AppendOutput(chunk.String())

	// Collapsed: the earliest lines hide behind the preview window.
	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "earlier lines hidden") {
		t.Fatalf("hidden hint = %q", joined)
	}
	if strings.Contains(joined, "line-0\n") && !strings.Contains(joined, "hidden") {
		t.Fatalf("earliest visible = %q", joined)
	}

	// Expanded: everything shows.
	component.SetExpanded(true)
	if !component.Expanded() {
		t.Fatal("not expanded")
	}
	lines = component.Render(60)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "line-0") {
		t.Fatalf("expanded missing early = %q", joined[:min(200, len(joined))])
	}
}

func TestBashExecutionFullPath(t *testing.T) {
	component := NewBashExecutionComponent("big", nil, false)
	code := 0
	component.SetComplete(&code, false, 10, 500, "/tmp/out.txt")
	if !strings.Contains(strings.Join(component.Render(60), "\n"), "/tmp/out.txt") {
		t.Fatal("path missing")
	}
}
