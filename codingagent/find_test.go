package codingagent

// Tests for the find tool and RelativizeFindResultPath (ported from
// tools/find.test.ts surfaces).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelativizeFindResultPath(t *testing.T) {
	if got := RelativizeFindResultPath("/base/sub/file.txt", "/base"); got != "sub/file.txt" {
		t.Fatalf("absolute = %q", got)
	}
	if got := RelativizeFindResultPath("already/relative", "/base"); got != "already/relative" {
		t.Fatalf("relative = %q", got)
	}
	if got := RelativizeFindResultPath("/base/dir/", "/base"); got != "dir/" {
		t.Fatalf("trailing = %q", got)
	}
}

func TestFindToolBasic(t *testing.T) {
	if GetToolPath("fd") == "" {
		t.Skip("fd not available")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "a.ts"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "c.ts"), []byte("x"), 0o644)

	definition := CreateFindToolDefinition(dir, nil)
	result, err := definition.Execute("t", FindToolInput{Pattern: "*.ts"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*FindToolOutput)
	if !strings.Contains(output.Text, "a.ts") {
		t.Fatalf("a.ts missing: %q", output.Text)
	}
	if !strings.Contains(output.Text, "src/c.ts") {
		t.Fatalf("src/c.ts missing: %q", output.Text)
	}
	if strings.Contains(output.Text, "b.txt") {
		t.Fatalf("non-match leaked: %q", output.Text)
	}
}

func TestFindToolPathPattern(t *testing.T) {
	if GetToolPath("fd") == "" {
		t.Skip("fd not available")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src", "spec"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "spec", "a.spec.ts"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.ts"), []byte("x"), 0o644)

	definition := CreateFindToolDefinition(dir, nil)
	result, err := definition.Execute("t", FindToolInput{Pattern: "src/**/*.spec.ts"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*FindToolOutput)
	if !strings.Contains(output.Text, "src/spec/a.spec.ts") {
		t.Fatalf("deep match missing: %q", output.Text)
	}
}

func TestFindToolNoMatches(t *testing.T) {
	if GetToolPath("fd") == "" {
		t.Skip("fd not available")
	}
	dir := t.TempDir()
	definition := CreateFindToolDefinition(dir, nil)
	result, err := definition.Execute("t", FindToolInput{Pattern: "*.zzz"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*FindToolOutput)
	if output.Text != "No files found matching pattern" {
		t.Fatalf("text = %q", output.Text)
	}
}

func TestFindToolLimit(t *testing.T) {
	if GetToolPath("fd") == "" {
		t.Skip("fd not available")
	}
	dir := t.TempDir()
	for i := 0; i < 6; i++ {
		os.WriteFile(filepath.Join(dir, string(rune('a'+i))+".txt"), []byte("x"), 0o644)
	}

	definition := CreateFindToolDefinition(dir, nil)
	result, err := definition.Execute("t", FindToolInput{Pattern: "*.txt", Limit: 2}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*FindToolOutput)
	if !strings.Contains(output.Text, "2 results limit reached. Use limit=4 for more") {
		t.Fatalf("notice = %q", output.Text)
	}
	if output.Details == nil || output.Details.ResultLimitReached != 2 {
		t.Fatalf("details = %+v", output.Details)
	}
}

func TestFindToolCustomGlob(t *testing.T) {
	dir := t.TempDir()
	ops := &stubFindOps{results: []string{filepath.Join(dir, "x.txt"), filepath.Join(dir, "y.txt")}}
	definition := CreateFindToolDefinition(dir, &FindToolOptions{Operations: ops})

	result, err := definition.Execute("t", FindToolInput{Pattern: "*.txt"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*FindToolOutput)
	if output.Text != "x.txt\ny.txt" {
		t.Fatalf("text = %q", output.Text)
	}
	if !ops.globCalled {
		t.Fatal("custom glob not invoked")
	}

	// Custom glob limit notice omits the refine hint.
	ops.limitResults = 2
	ops.results = []string{dir + "/a", dir + "/b"}
	result, err = definition.Execute("t", FindToolInput{Pattern: "*", Limit: 2}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute 2: %v", err)
	}
	output = result.(*FindToolOutput)
	if !strings.Contains(output.Text, "2 results limit reached") {
		t.Fatalf("notice = %q", output.Text)
	}
	if strings.Contains(output.Text, "refine pattern") {
		t.Fatalf("custom glob notice should omit refine hint: %q", output.Text)
	}
}

func TestFindToolMissingPath(t *testing.T) {
	dir := t.TempDir()
	ops := &stubFindOps{}
	definition := CreateFindToolDefinition(dir, &FindToolOptions{Operations: ops})
	if _, err := definition.Execute("t", FindToolInput{Pattern: "*", Path: "missing"}, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "Path not found") {
		t.Fatalf("err = %v", err)
	}
}

type stubFindOps struct {
	results      []string
	limitResults int
	globCalled   bool
}

func (o *stubFindOps) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (o *stubFindOps) Glob(pattern, cwd string, options FindGlobOptions) ([]string, error) {
	o.globCalled = true
	if o.limitResults > 0 && len(o.results) > o.limitResults {
		return o.results[:o.limitResults], nil
	}
	return o.results, nil
}
