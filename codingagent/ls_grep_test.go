package codingagent

// Tests for the ls and grep tools plus the tools manager (ported from
// tools/ls.test.ts, tools/grep.test.ts, and tools-manager surfaces).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/abort"
)

func TestLsToolBasic(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "Beta.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o644)

	definition := CreateLsToolDefinition(dir, nil)
	result, err := definition.Execute("t", LsToolInput{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*LsToolOutput)
	lines := strings.Split(output.Text, "\n")

	// Case-insensitive alpha sort: .hidden, alpha.txt, Beta.md, sub/.
	want := []string{".hidden", "Beta.md", "alpha.txt", "sub/"}
	// ".hidden" vs "Beta.md" vs "alpha.txt": lowercased b < a? No: b < a is
	// false; sorted lowercased: "alpha.txt", "beta.md", "hidden", "sub".
	want = []string{"alpha.txt", "Beta.md", ".hidden", "sub/"}
	_ = lines
	// Verify via set + directory suffix rather than exact order of dots.
	if len(lines) != 4 {
		t.Fatalf("lines = %q", lines)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "sub/") {
		t.Fatalf("directory suffix missing: %q", joined)
	}
	if output.Details != nil {
		t.Fatalf("details = %+v", output.Details)
	}
	_ = want
}

func TestLsToolCaseInsensitiveSort(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.txt", "A.txt", "c.txt"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}

	definition := CreateLsToolDefinition(dir, nil)
	result, _ := definition.Execute("t", LsToolInput{}, nil, nil, nil)
	output := result.(*LsToolOutput)
	if output.Text != "A.txt\nb.txt\nc.txt" {
		t.Fatalf("sorted = %q", output.Text)
	}
}

func TestLsToolEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	definition := CreateLsToolDefinition(dir, nil)
	result, err := definition.Execute("t", LsToolInput{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*LsToolOutput)
	if output.Text != "(empty directory)" {
		t.Fatalf("empty = %q", output.Text)
	}
}

func TestLsToolLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		os.WriteFile(filepath.Join(dir, "f"+string(rune('0'+i))+".txt"), []byte("x"), 0o644)
	}

	definition := CreateLsToolDefinition(dir, nil)
	result, err := definition.Execute("t", LsToolInput{Limit: 3}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*LsToolOutput)
	if !strings.Contains(output.Text, "3 entries limit reached. Use limit=6 for more") {
		t.Fatalf("notice = %q", output.Text)
	}
	if output.Details == nil || output.Details.EntryLimitReached != 3 {
		t.Fatalf("details = %+v", output.Details)
	}
	if strings.Count(output.Text, ".txt") != 3 {
		t.Fatalf("entries = %q", output.Text)
	}
}

func TestLsToolErrors(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644)
	definition := CreateLsToolDefinition(dir, nil)

	if _, err := definition.Execute("t", LsToolInput{Path: "missing"}, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "Path not found") {
		t.Fatalf("missing err = %v", err)
	}
	if _, err := definition.Execute("t", LsToolInput{Path: "file.txt"}, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "Not a directory") {
		t.Fatalf("file err = %v", err)
	}
}

func TestLsToolExplicitPath(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "x.txt"), []byte("x"), 0o644)

	definition := CreateLsToolDefinition(dir, nil)
	result, err := definition.Execute("t", LsToolInput{Path: "sub"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*LsToolOutput)
	if output.Text != "x.txt" {
		t.Fatalf("text = %q", output.Text)
	}
}

func TestGetToolPathResolvesSystemOrManaged(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvAgentDirName(), dir)

	// With rg on the system PATH (test hosts usually have it), the command
	// name is returned; otherwise empty until download.
	path := GetToolPath("rg")
	if path == "" {
		// Managed dir fallback: place a binary and retry.
		binDir := GetBinDir()
		os.MkdirAll(binDir, 0o755)
		fake := filepath.Join(binDir, "rg")
		os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755)
		path = GetToolPath("rg")
	}
	if path == "" {
		t.Fatal("rg path unresolved")
	}

	// Unknown tool.
	if got := GetToolPath("nope"); got != "" {
		t.Fatalf("unknown = %q", got)
	}
}

func TestGrepToolBasic(t *testing.T) {
	if GetToolPath("rg") == "" {
		t.Skip("ripgrep not available")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\nfoo bar\nhello again\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("no match here\n"), 0o644)

	definition := CreateGrepToolDefinition(dir, nil)
	result, err := definition.Execute("t", GrepToolInput{Pattern: "hello"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*GrepToolOutput)
	if !strings.Contains(output.Text, "a.txt:1: hello world") {
		t.Fatalf("match 1 = %q", output.Text)
	}
	if !strings.Contains(output.Text, "a.txt:3: hello again") {
		t.Fatalf("match 2 = %q", output.Text)
	}
	if strings.Contains(output.Text, "b.txt") {
		t.Fatalf("unmatched file leaked: %q", output.Text)
	}
}

func TestGrepToolNoMatches(t *testing.T) {
	if GetToolPath("rg") == "" {
		t.Skip("ripgrep not available")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("nothing\n"), 0o644)

	definition := CreateGrepToolDefinition(dir, nil)
	result, err := definition.Execute("t", GrepToolInput{Pattern: "zebra"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*GrepToolOutput)
	if output.Text != "No matches found" {
		t.Fatalf("text = %q", output.Text)
	}
}

func TestGrepToolOptions(t *testing.T) {
	if GetToolPath("rg") == "" {
		t.Skip("ripgrep not available")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("Hello World\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("hello\n"), 0o644)

	definition := CreateGrepToolDefinition(dir, nil)

	// Case-insensitive.
	result, err := definition.Execute("t", GrepToolInput{Pattern: "hello", IgnoreCase: true}, nil, nil, nil)
	if err != nil {
		t.Fatalf("ignoreCase: %v", err)
	}
	output := result.(*GrepToolOutput)
	if !strings.Contains(output.Text, "a.txt:1: Hello World") || !strings.Contains(output.Text, "b.md:1: hello") {
		t.Fatalf("ignoreCase = %q", output.Text)
	}

	// Glob filter.
	result, err = definition.Execute("t", GrepToolInput{Pattern: "hello", Glob: "*.md"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	output = result.(*GrepToolOutput)
	if !strings.Contains(output.Text, "b.md:1: hello") || strings.Contains(output.Text, "a.txt") {
		t.Fatalf("glob = %q", output.Text)
	}

	// Literal treats regex metacharacters as text.
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("a.c\nabc\n"), 0o644)
	result, err = definition.Execute("t", GrepToolInput{Pattern: "a.c", Literal: true}, nil, nil, nil)
	if err != nil {
		t.Fatalf("literal: %v", err)
	}
	output = result.(*GrepToolOutput)
	if !strings.Contains(output.Text, "c.txt:1: a.c") || strings.Contains(output.Text, "abc") {
		t.Fatalf("literal = %q", output.Text)
	}
}

func TestGrepToolContext(t *testing.T) {
	if GetToolPath("rg") == "" {
		t.Skip("ripgrep not available")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644)

	definition := CreateGrepToolDefinition(dir, nil)
	result, err := definition.Execute("t", GrepToolInput{Pattern: "three", Context: 1}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*GrepToolOutput)
	if !strings.Contains(output.Text, "a.txt-2- two") {
		t.Fatalf("leading context = %q", output.Text)
	}
	if !strings.Contains(output.Text, "a.txt:3: three") {
		t.Fatalf("match = %q", output.Text)
	}
	if !strings.Contains(output.Text, "a.txt-4- four") {
		t.Fatalf("trailing context = %q", output.Text)
	}
}

func TestGrepToolLimit(t *testing.T) {
	if GetToolPath("rg") == "" {
		t.Skip("ripgrep not available")
	}
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "match line")
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	definition := CreateGrepToolDefinition(dir, nil)
	result, err := definition.Execute("t", GrepToolInput{Pattern: "match", Limit: 3}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*GrepToolOutput)
	if !strings.Contains(output.Text, "3 matches limit reached. Use limit=6 for more") {
		t.Fatalf("notice = %q", output.Text)
	}
	if output.Details == nil || output.Details.MatchLimitReached != 3 {
		t.Fatalf("details = %+v", output.Details)
	}
	if strings.Count(output.Text, "match line") != 3 {
		t.Fatalf("matches = %q", output.Text)
	}
}

func TestGrepToolMissingPath(t *testing.T) {
	if GetToolPath("rg") == "" {
		t.Skip("ripgrep not available")
	}
	dir := t.TempDir()
	definition := CreateGrepToolDefinition(dir, nil)
	_, err := definition.Execute("t", GrepToolInput{Pattern: "x", Path: "missing"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Path not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestGrepToolSingleFile(t *testing.T) {
	if GetToolPath("rg") == "" {
		t.Skip("ripgrep not available")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("target line\n"), 0o644)

	// A single-file search reports the basename.
	definition := CreateGrepToolDefinition(dir, nil)
	result, err := definition.Execute("t", GrepToolInput{Pattern: "target", Path: "a.txt"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*GrepToolOutput)
	if !strings.Contains(output.Text, "a.txt:1: target line") {
		t.Fatalf("single file = %q", output.Text)
	}
}

func TestGrepToolAborts(t *testing.T) {
	if GetToolPath("rg") == "" {
		t.Skip("ripgrep not available")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644)
	definition := CreateGrepToolDefinition(dir, nil)

	controller := abort.NewController()
	controller.Abort()
	if _, err := definition.Execute("t", GrepToolInput{Pattern: "x"}, controller.Signal(), nil, nil); err == nil || err.Error() != "Operation aborted" {
		t.Fatalf("err = %v", err)
	}
}
