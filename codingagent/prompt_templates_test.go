package codingagent

// Tests for prompt templates (ported from prompt-templates.test.ts
// surfaces: arg parsing, substitution forms, loading, expansion).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCommandArgs(t *testing.T) {
	if got := ParseCommandArgs(""); len(got) != 0 {
		t.Fatalf("empty = %v", got)
	}
	if got := ParseCommandArgs("a b c"); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("plain = %v", got)
	}
	if got := ParseCommandArgs(`say "hello world" now`); len(got) != 3 || got[1] != "hello world" {
		t.Fatalf("double-quoted = %v", got)
	}
	// The apostrophe opens a quote that never closes; the remainder stays
	// inside it (reference behavior).
	if got := ParseCommandArgs(`it's one`); len(got) != 1 || got[0] != "its one" {
		t.Fatalf("single-quoted = %v", got)
	}
	if got := ParseCommandArgs(`"unclosed`); len(got) != 1 || got[0] != "unclosed" {
		t.Fatalf("unclosed = %v", got)
	}
	if got := ParseCommandArgs("  spaced   out  "); len(got) != 2 {
		t.Fatalf("whitespace = %v", got)
	}
}

func TestSubstituteArgsPositional(t *testing.T) {
	args := []string{"alpha", "beta"}
	if got := SubstituteArgs("first: $1", args); got != "first: alpha" {
		t.Fatalf("$1 = %q", got)
	}
	if got := SubstituteArgs("second: $2", args); got != "second: beta" {
		t.Fatalf("$2 = %q", got)
	}
	if got := SubstituteArgs("missing: $3", args); got != "missing: " {
		t.Fatalf("$3 = %q", got)
	}
	if got := SubstituteArgs("all: $@", args); got != "all: alpha beta" {
		t.Fatalf("$@ = %q", got)
	}
	if got := SubstituteArgs("all: $ARGUMENTS", args); got != "all: alpha beta" {
		t.Fatalf("$ARGUMENTS = %q", got)
	}
}

func TestSubstituteArgsDefaults(t *testing.T) {
	args := []string{"alpha"}
	if got := SubstituteArgs("${1:-fallback}", args); got != "alpha" {
		t.Fatalf("present = %q", got)
	}
	if got := SubstituteArgs("${2:-fallback}", args); got != "fallback" {
		t.Fatalf("absent = %q", got)
	}
	if got := SubstituteArgs("${@:-none}", nil); got != "none" {
		t.Fatalf("empty all = %q", got)
	}
	if got := SubstituteArgs("${ARGUMENTS:-none}", nil); got != "none" {
		t.Fatalf("empty arguments = %q", got)
	}
}

func TestSubstituteArgsSlices(t *testing.T) {
	args := []string{"a", "b", "c", "d"}
	if got := SubstituteArgs("${@:2}", args); got != "b c d" {
		t.Fatalf("from = %q", got)
	}
	if got := SubstituteArgs("${@:1:2}", args); got != "a b" {
		t.Fatalf("length = %q", got)
	}
	if got := SubstituteArgs("${@:0}", args); got != "a b c d" {
		t.Fatalf("zero-as-one = %q", got)
	}
	if got := SubstituteArgs("${@:10}", args); got != "" {
		t.Fatalf("beyond = %q", got)
	}
	if got := SubstituteArgs("${@:1:10}", args); got != "a b c d" {
		t.Fatalf("length clamp = %q", got)
	}
}

func TestSubstituteArgsNoRecursion(t *testing.T) {
	// Values containing patterns are not substituted again.
	args := []string{"$1"}
	if got := SubstituteArgs("$1", args); got != "$1" {
		t.Fatalf("recursive = %q", got)
	}
}

func TestLoadPromptTemplates(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "agent")
	cwd := filepath.Join(base, "work")

	// Global template with full frontmatter.
	globalDir := filepath.Join(agentDir, "prompts")
	os.MkdirAll(globalDir, 0o755)
	os.WriteFile(filepath.Join(globalDir, "review.md"), []byte("---\ndescription: Review code\nargument-hint: \"[file]\"\n---\nReview $1"), 0o644)

	// Project template relying on first-line description.
	projectDir := filepath.Join(cwd, ConfigDirName(), "prompts")
	os.MkdirAll(projectDir, 0o755)
	os.WriteFile(filepath.Join(projectDir, "deploy.md"), []byte("# Deploy the service to production"), 0o644)

	// Non-md file ignored.
	os.WriteFile(filepath.Join(projectDir, "notes.txt"), []byte("not a template"), 0o644)

	result := LoadPromptTemplates(LoadPromptTemplatesOptions{
		Cwd: cwd, AgentDir: agentDir, IncludeDefaults: true,
	})
	if len(result.Templates) != 2 {
		t.Fatalf("templates = %d", len(result.Templates))
	}
	byName := map[string]PromptTemplate{}
	for _, template := range result.Templates {
		byName[template.Name] = template
	}
	review := byName["review"]
	if review.Description != "Review code" || review.ArgumentHint != "[file]" {
		t.Fatalf("review = %+v", review)
	}
	if review.Content != "Review $1" {
		t.Fatalf("content = %q", review.Content)
	}
	if review.SourceInfo.Scope != ScopeUser {
		t.Fatalf("review scope = %q", review.SourceInfo.Scope)
	}
	deploy := byName["deploy"]
	if deploy.Description != "# Deploy the service to production" {
		t.Fatalf("deploy desc = %q", deploy.Description)
	}
	if deploy.SourceInfo.Scope != ScopeProject {
		t.Fatalf("deploy scope = %q", deploy.SourceInfo.Scope)
	}
}

func TestLoadPromptTemplatesExplicitPaths(t *testing.T) {
	base := t.TempDir()
	cwd := filepath.Join(base, "work")
	os.MkdirAll(cwd, 0o755)

	// Explicit file.
	explicit := filepath.Join(base, "custom.md")
	os.WriteFile(explicit, []byte("---\ndescription: custom\n---\nCustom $@"), 0o644)

	// Explicit directory.
	explicitDir := filepath.Join(base, "more")
	os.MkdirAll(explicitDir, 0o755)
	os.WriteFile(filepath.Join(explicitDir, "extra.md"), []byte("---\ndescription: extra\n---\nExtra"), 0o644)

	result := LoadPromptTemplates(LoadPromptTemplatesOptions{
		Cwd: cwd, AgentDir: base,
		PromptPaths: []string{explicit, explicitDir, filepath.Join(base, "missing")},
	})
	if len(result.Templates) != 2 {
		t.Fatalf("templates = %d (%+v)", len(result.Templates), result.Templates)
	}
	found := map[string]bool{}
	for _, template := range result.Templates {
		found[template.Name] = true
	}
	if !found["custom"] || !found["extra"] {
		t.Fatalf("found = %v", found)
	}
}

func TestExpandPromptTemplate(t *testing.T) {
	templates := []PromptTemplate{
		{Name: "review", Content: "Review $1 with $2"},
		{Name: "deploy", Content: "Deploy $@"},
	}

	if got := ExpandPromptTemplate("/review a.txt b.txt", templates); got != "Review a.txt with b.txt" {
		t.Fatalf("expand = %q", got)
	}
	if got := ExpandPromptTemplate("/deploy now please", templates); got != "Deploy now please" {
		t.Fatalf("all args = %q", got)
	}
	// Quoted args parse.
	if got := ExpandPromptTemplate(`/review "two words" x`, templates); got != "Review two words with x" {
		t.Fatalf("quoted = %q", got)
	}
	// Unknown command passes through.
	if got := ExpandPromptTemplate("/unknown stays", templates); got != "/unknown stays" {
		t.Fatalf("unknown = %q", got)
	}
	// Non-command passes through.
	if got := ExpandPromptTemplate("plain text", templates); got != "plain text" {
		t.Fatalf("plain = %q", got)
	}
}

func TestPromptTemplateDescriptionTruncation(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 100)
	os.WriteFile(filepath.Join(dir, "long.md"), []byte(long), 0o644)
	result := loadTemplatesFromDir(dir, func(path string) SourceInfo { return SourceInfo{} })
	if len(result.Templates) != 1 {
		t.Fatalf("templates = %d", len(result.Templates))
	}
	description := result.Templates[0].Description
	if len(description) != 63 || !strings.HasSuffix(description, "...") {
		t.Fatalf("truncated = %q (%d)", description, len(description))
	}
}
