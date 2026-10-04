package codingagent

// Tests for the summary components (adapted from branch-summary and
// compaction-summary tests).

import (
	"strings"
	"testing"
)

func TestBranchSummaryCollapsed(t *testing.T) {
	component := NewBranchSummaryMessageComponent("the branch content")
	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[branch]") {
		t.Fatalf("label = %q", joined)
	}
	if !strings.Contains(joined, "Branch summary (") {
		t.Fatalf("hint = %q", joined)
	}
	if !strings.Contains(joined, "to expand)") {
		t.Fatalf("hint = %q", joined)
	}
	// Collapsed hides the content.
	if strings.Contains(joined, "the branch content") {
		t.Fatalf("leaked = %q", joined)
	}
}

func TestBranchSummaryExpanded(t *testing.T) {
	component := NewBranchSummaryMessageComponent("detailed summary text")
	component.SetExpanded(true)
	if !component.Expanded() {
		t.Fatal("state not set")
	}
	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Branch Summary") {
		t.Fatalf("header = %q", joined)
	}
	if !strings.Contains(joined, "detailed summary text") {
		t.Fatalf("content = %q", joined)
	}
}

func TestBranchSummaryToggle(t *testing.T) {
	component := NewBranchSummaryMessageComponent("x")
	component.Toggle()
	if !component.Expanded() {
		t.Fatal("toggle did not expand")
	}
	component.Toggle()
	if component.Expanded() {
		t.Fatal("toggle did not collapse")
	}
}

func TestCompactionSummaryCollapsed(t *testing.T) {
	component := NewCompactionSummaryMessageComponent("what happened", 52300)
	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[compaction]") {
		t.Fatalf("label = %q", joined)
	}
	if !strings.Contains(joined, "Compacted from 52,300 tokens (") {
		t.Fatalf("tokens = %q", joined)
	}
	if strings.Contains(joined, "what happened") {
		t.Fatalf("leaked = %q", joined)
	}
}

func TestCompactionSummaryExpanded(t *testing.T) {
	component := NewCompactionSummaryMessageComponent("the compaction summary", 1000)
	component.SetExpanded(true)
	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Compacted from 1,000 tokens") {
		t.Fatalf("header = %q", joined)
	}
	if !strings.Contains(joined, "the compaction summary") {
		t.Fatalf("content = %q", joined)
	}
}

func TestFormatLocaleInt(t *testing.T) {
	cases := map[int]string{
		0: "0", 5: "5", 999: "999",
		1000: "1,000", 52300: "52,300", 1000000: "1,000,000",
	}
	for input, want := range cases {
		if got := formatLocaleInt(input); got != want {
			t.Errorf("formatLocaleInt(%d) = %q, want %q", input, got, want)
		}
	}
}
