package harness

// Ports of prompt-templates.test.ts cases.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPromptTemplatesFromDir(t *testing.T) {
	env := newFakeEnv(t)
	if err := os.WriteFile(filepath.Join(env.root, "review.md"), []byte("---\ndescription: Review code\n---\nPlease review $1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.root, "plain.txt"), []byte("not markdown"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.root, "no-frontmatter.md"), []byte("First body line that is long enough to be truncated at sixty characters plus more"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := LoadPromptTemplates(env, []string{env.root}, BackgroundContext)
	if len(result.PromptTemplates) != 2 {
		t.Fatalf("templates = %d diagnostics = %+v", len(result.PromptTemplates), result.Diagnostics)
	}
	if result.PromptTemplates[0].Name != "no-frontmatter" || result.PromptTemplates[1].Name != "review" {
		names := []string{}
		for _, tpl := range result.PromptTemplates {
			names = append(names, tpl.Name)
		}
		t.Fatalf("names = %v", names)
	}
	review := result.PromptTemplates[1]
	if review.Description != "Review code" {
		t.Fatalf("description = %q", review.Description)
	}
	// Description fallback: first non-empty body line, 60 chars + "...".
	fallback := result.PromptTemplates[0].Description
	if !strings.HasSuffix(fallback, "...") || len([]rune(fallback)) != 63 {
		t.Fatalf("fallback = %q (%d runes)", fallback, len([]rune(fallback)))
	}
}

func TestLoadPromptTemplatesExplicitFile(t *testing.T) {
	env := newFakeEnv(t)
	if err := os.WriteFile(filepath.Join(env.root, "notes.md"), []byte("no frontmatter here"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := LoadPromptTemplates(env, []string{filepath.Join(env.root, "notes.md")}, BackgroundContext)
	if len(result.PromptTemplates) != 1 || result.PromptTemplates[0].Name != "notes" {
		t.Fatalf("templates = %+v", result.PromptTemplates)
	}
	if result.PromptTemplates[0].Content != "no frontmatter here" {
		t.Fatalf("content = %q", result.PromptTemplates[0].Content)
	}
	// Missing path is skipped silently.
	result = LoadPromptTemplates(env, []string{filepath.Join(env.root, "missing.md")}, BackgroundContext)
	if len(result.PromptTemplates) != 0 || len(result.Diagnostics) != 0 {
		t.Fatalf("templates = %+v diagnostics = %+v", result.PromptTemplates, result.Diagnostics)
	}
}

func TestParseCommandArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a b c", []string{"a", "b", "c"}},
		{`say "hello world" tail`, []string{"say", "hello world", "tail"}},
		{"it's quoted", []string{"its quoted"}},
		{"  spaced   out  ", []string{"spaced", "out"}},
		{`"unclosed`, []string{"unclosed"}},
		{"", nil},
	}
	for _, tc := range cases {
		got := ParseCommandArgs(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("ParseCommandArgs(%q) = %v want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("ParseCommandArgs(%q) = %v want %v", tc.in, got, tc.want)
			}
		}
	}
}

func TestSubstituteArgs(t *testing.T) {
	cases := []struct {
		content string
		args    []string
		want    string
	}{
		{"fix $1 please", []string{"the", "bug"}, "fix the please"},
		{"fix $2 now", []string{"the", "bug"}, "fix bug now"},
		{"missing $5", []string{"a"}, "missing "},
		{"all: $@", []string{"a", "b"}, "all: a b"},
		{"args: $ARGUMENTS", []string{"x", "y"}, "args: x y"},
		{"${@:2}", []string{"a", "b", "c"}, "b c"},
		{"${@:1:2}", []string{"a", "b", "c"}, "a b"},
		{"$1 and $@", []string{"one"}, "one and one"},
	}
	for _, tc := range cases {
		if got := SubstituteArgs(tc.content, tc.args); got != tc.want {
			t.Fatalf("SubstituteArgs(%q, %v) = %q want %q", tc.content, tc.args, got, tc.want)
		}
	}
}

func TestFormatPromptTemplateInvocation(t *testing.T) {
	template := PromptTemplate{Name: "review", Content: "Review $1 focusing on ${@:2}"}
	if got := FormatPromptTemplateInvocation(template, "code", "style", "tests"); got != "Review code focusing on style tests" {
		t.Fatalf("got %q", got)
	}
	if got := FormatPromptTemplateInvocation(template); got != "Review  focusing on " {
		t.Fatalf("no-args got %q", got)
	}
}
