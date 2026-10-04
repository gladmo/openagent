package codingagent

// Tests for the diff renderer (adapted from diff.test.ts).

import (
	"strings"
	"testing"
)

func TestParseDiffLine(t *testing.T) {
	added := parseDiffLine("+42 const x = 1;")
	if added == nil || added.prefix != "+" || added.lineNum != "42" || added.content != "const x = 1;" {
		t.Fatalf("added = %+v", added)
	}
	removed := parseDiffLine("-17 old line")
	if removed == nil || removed.prefix != "-" || removed.content != "old line" {
		t.Fatalf("removed = %+v", removed)
	}
	context := parseDiffLine(" 5 unchanged")
	if context == nil || context.prefix != " " || context.content != "unchanged" {
		t.Fatalf("context = %+v", context)
	}
	if parseDiffLine("no prefix here") != nil {
		t.Fatal("unparseable accepted")
	}
}

func TestReplaceTabs(t *testing.T) {
	if got := replaceTabs("a\tb"); got != "a   b" {
		t.Fatalf("tabs = %q", got)
	}
}

func TestRenderDiffContext(t *testing.T) {
	rendered := RenderDiff(" 5 unchanged line")
	if !strings.Contains(rendered, "unchanged line") {
		t.Fatalf("context = %q", rendered)
	}
	if !strings.HasPrefix(rendered, " 5 ") {
		t.Fatalf("prefix = %q", rendered)
	}
}

func TestRenderDiffAddedStandalone(t *testing.T) {
	rendered := RenderDiff("+10 brand new")
	if !strings.HasPrefix(rendered, "+10 ") {
		t.Fatalf("prefix = %q", rendered)
	}
	if !strings.Contains(rendered, "brand new") {
		t.Fatalf("content = %q", rendered)
	}
}

func TestRenderDiffSingleLineModification(t *testing.T) {
	// One removed + one added: intra-line diff with inverse markers.
	rendered := RenderDiff("-10 return oldValue\n+10 return newValue")
	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q", rendered)
	}
	if !strings.Contains(lines[0], "old") || !strings.Contains(lines[1], "new") {
		t.Fatalf("content = %q", rendered)
	}
	// The changed token carries the inverse sequence.
	if !strings.Contains(rendered, "\x1b[7m") {
		t.Fatalf("no inverse: %q", rendered)
	}
	// The common prefix "return " stays unhighlighted.
	if !strings.Contains(lines[0], "return ") {
		t.Fatalf("common = %q", lines[0])
	}
}

func TestRenderDiffMultiLineChange(t *testing.T) {
	// Two removed + one added: no intra-line pairing, plain lines.
	rendered := RenderDiff("-10 line one\n-11 line two\n+10 replacement")
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d (%q)", len(lines), rendered)
	}
	if !strings.HasPrefix(lines[0], "-10 line one") || !strings.HasPrefix(lines[1], "-11 line two") {
		t.Fatalf("removed = %v", lines[:2])
	}
	if !strings.HasPrefix(lines[2], "+10 replacement") {
		t.Fatalf("added = %q", lines[2])
	}
}

func TestRenderDiffUnparseablePassthrough(t *testing.T) {
	rendered := RenderDiff("some random text\n*** hunk header")
	if !strings.Contains(rendered, "some random text") || !strings.Contains(rendered, "*** hunk header") {
		t.Fatalf("passthrough = %q", rendered)
	}
}

func TestRenderDiffTabsExpanded(t *testing.T) {
	rendered := RenderDiff("+5 if\ttrue")
	if !strings.Contains(rendered, "if   true") {
		t.Fatalf("tabs = %q", rendered)
	}
}
