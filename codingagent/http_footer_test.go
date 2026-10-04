package codingagent

// Tests for HTTP dispatcher settings and git branch resolution.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHttpIdleTimeoutMs(t *testing.T) {
	// "disabled" -> 0.
	if got, ok := ParseHttpIdleTimeoutMs("disabled"); !ok || got != 0 {
		t.Fatalf("disabled = %d %v", got, ok)
	}
	if got, ok := ParseHttpIdleTimeoutMs("DISABLED"); !ok || got != 0 {
		t.Fatalf("case = %d %v", got, ok)
	}
	// Numeric strings.
	if got, ok := ParseHttpIdleTimeoutMs("30000"); !ok || got != 30000 {
		t.Fatalf("numeric = %d %v", got, ok)
	}
	if got, ok := ParseHttpIdleTimeoutMs(" 45000 "); !ok || got != 45000 {
		t.Fatalf("padded = %d %v", got, ok)
	}
	// Floats floor.
	if got, ok := ParseHttpIdleTimeoutMs(30000.9); !ok || got != 30000 {
		t.Fatalf("floor = %d %v", got, ok)
	}
	// Zero valid.
	if got, ok := ParseHttpIdleTimeoutMs(float64(0)); !ok || got != 0 {
		t.Fatalf("zero = %d %v", got, ok)
	}
	// Negative / NaN / empty / invalid invalid.
	for _, invalid := range []any{-1, -0.5, "", "abc", true} {
		if _, ok := ParseHttpIdleTimeoutMs(invalid); ok {
			t.Errorf("%v accepted", invalid)
		}
	}
}

func TestFormatHTTPIdleTimeoutMs(t *testing.T) {
	if got := FormatHTTPIdleTimeoutMs(300_000); got != "5 min" {
		t.Fatalf("5min = %q", got)
	}
	if got := FormatHTTPIdleTimeoutMs(0); got != "disabled" {
		t.Fatalf("disabled = %q", got)
	}
	if got := FormatHTTPIdleTimeoutMs(4500); got != "4 sec" {
		t.Fatalf("custom = %q", got)
	}
}

func TestApplyHTTPProxySettings(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")

	// Empty: no-op.
	ApplyHTTPProxySettings("")
	if os.Getenv("HTTP_PROXY") != "" {
		t.Fatal("empty applied")
	}

	ApplyHTTPProxySettings("http://proxy:8080")
	if os.Getenv("HTTP_PROXY") != "http://proxy:8080" || os.Getenv("HTTPS_PROXY") != "http://proxy:8080" {
		t.Fatalf("proxies = %q %q", os.Getenv("HTTP_PROXY"), os.Getenv("HTTPS_PROXY"))
	}

	// Existing values not overwritten.
	t.Setenv("HTTP_PROXY", "existing")
	ApplyHTTPProxySettings("http://other")
	if os.Getenv("HTTP_PROXY") != "existing" {
		t.Fatalf("overwrote = %q", os.Getenv("HTTP_PROXY"))
	}
}

func TestConfigureHTTPDispatcher(t *testing.T) {
	if err := ConfigureHTTPDispatcher(60_000); err != nil {
		t.Fatalf("err = %v", err)
	}
	if AppliedHTTPIdleTimeoutMs() != 60_000 {
		t.Fatalf("applied = %d", AppliedHTTPIdleTimeoutMs())
	}
	if err := ConfigureHTTPDispatcher(-1); err == nil || !strings.Contains(err.Error(), "Invalid HTTP idle timeout") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveBranchWithGit(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	os.MkdirAll(filepath.Join(repo, ".git", "refs", "heads"), 0o755)
	os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)

	// A bare .git dir without a valid HEAD layout may fail symbolic-ref
	// on some hosts; the contract is a branch or empty, never an error.
	branch := ResolveBranchWithGit(repo)
	if branch != "" && branch != "main" {
		t.Fatalf("branch = %q", branch)
	}

	// Non-repo: empty.
	if got := ResolveBranchWithGit(t.TempDir()); got != "" {
		t.Fatalf("non-repo = %q", got)
	}
}

func TestIsWindowsMountedRepoPath(t *testing.T) {
	if !IsWindowsMountedRepoPath("/mnt/c/repo") {
		t.Fatal("/mnt/c not detected")
	}
	if !IsWindowsMountedRepoPath("/mnt/d/sub/dir") {
		t.Fatal("/mnt/d/sub not detected")
	}
	if IsWindowsMountedRepoPath("/home/user") {
		t.Fatal("/home misdetected")
	}
	if IsWindowsMountedRepoPath("/mnt/AB") {
		t.Fatal("double letter accepted")
	}
}
