package codingagent

// Ported from core/tools/truncate.ts tests and utils/shell.ts tests
// (truncate.test.ts, shell.test.ts surfaces testable cross-platform).

import (
	"os"
	"strings"
	"testing"
)

func TestTruncateHead(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = strings.Repeat("x", 10)
	}
	content := strings.Join(lines, "\n")

	maxLines := 3
	result := TruncateHead(content, TruncationOptions{MaxLines: &maxLines})
	if !result.Truncated || result.TruncatedBy != "lines" {
		t.Fatalf("result = %+v", result)
	}
	if result.OutputLines != 3 {
		t.Fatalf("output lines = %d", result.OutputLines)
	}
	if result.TotalLines != 10 {
		t.Fatalf("total lines = %d", result.TotalLines)
	}
	if result.Content != strings.Join(lines[:3], "\n") {
		t.Fatalf("content = %q", result.Content)
	}
	if result.LastLinePartial {
		t.Fatal("head never partial")
	}

	// Within limits: unchanged.
	result = TruncateHead(content, TruncationOptions{})
	if result.Truncated {
		t.Fatalf("untruncated flagged: %+v", result)
	}

	// First line exceeds byte limit.
	maxBytes := 5
	result = TruncateHead(content, TruncationOptions{MaxBytes: &maxBytes})
	if !result.FirstLineExceedsLimit || result.Content != "" || result.TruncatedBy != "bytes" {
		t.Fatalf("first line: %+v", result)
	}

	// Byte limit mid-stream.
	maxBytes2 := 25
	result = TruncateHead(content, TruncationOptions{MaxBytes: &maxBytes2})
	if result.TruncatedBy != "bytes" {
		t.Fatalf("bytes: %+v", result)
	}
	if result.OutputLines != 2 { // 10 + 1 + 10 = 21 <= 25; +1+10 = 32 > 25
		t.Fatalf("byte-clipped lines = %d", result.OutputLines)
	}
}

func TestTruncateTail(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = strings.Repeat("x", 10)
	}
	content := strings.Join(lines, "\n")

	maxLines := 3
	result := TruncateTail(content, TruncationOptions{MaxLines: &maxLines})
	if !result.Truncated || result.TruncatedBy != "lines" {
		t.Fatalf("result = %+v", result)
	}
	if result.OutputLines != 3 {
		t.Fatalf("output lines = %d", result.OutputLines)
	}
	if result.Content != strings.Join(lines[7:], "\n") {
		t.Fatalf("content = %q", result.Content)
	}

	// Tail keeps the last lines.
	maxLines2 := 1
	result = TruncateTail(content, TruncationOptions{MaxLines: &maxLines2})
	if result.Content != lines[9] {
		t.Fatalf("single = %q", result.Content)
	}

	// Partial line when the final line alone exceeds the byte limit.
	longLast := "short\n" + strings.Repeat("y", 100)
	maxBytes := 30
	result = TruncateTail(longLast, TruncationOptions{MaxBytes: &maxBytes})
	if !result.LastLinePartial {
		t.Fatalf("partial: %+v", result)
	}
	if !strings.HasSuffix(result.Content, "y") || len(result.Content) != 30 {
		t.Fatalf("partial content = %q (%d bytes)", result.Content, len(result.Content))
	}
}

func TestTruncateLine(t *testing.T) {
	short := "hello"
	result := TruncateLine(short, 500)
	if result.Text != short || result.WasTruncated {
		t.Fatalf("short = %+v", result)
	}

	long := strings.Repeat("a", 600)
	result = TruncateLine(long, 500)
	if !result.WasTruncated {
		t.Fatal("long not truncated")
	}
	if len([]rune(result.Text)) != 500+len("... [truncated]") {
		t.Fatalf("text len = %d", len(result.Text))
	}
	if !strings.HasSuffix(result.Text, "... [truncated]") {
		t.Fatalf("suffix = %q", result.Text)
	}
}

func TestFormatSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1024 * 1024, "1.0MB"},
		{int64(1024 * 1024 * 1.5), "1.5MB"},
	}
	for _, tc := range cases {
		if got := FormatSize(tc.in); got != tc.want {
			t.Errorf("FormatSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeBinaryOutput(t *testing.T) {
	if got := SanitizeBinaryOutput("hello"); got != "hello" {
		t.Fatalf("plain = %q", got)
	}
	if got := SanitizeBinaryOutput("a\tb\nc\rd"); got != "a\tb\nc\rd" {
		t.Fatalf("whitespace = %q", got)
	}
	if got := SanitizeBinaryOutput("a\x00b\x1fc"); got != "abc" {
		t.Fatalf("control = %q", got)
	}
	if got := SanitizeBinaryOutput("a\ufff9b\ufffbc"); got != "abc" {
		t.Fatalf("format = %q", got)
	}
}

func TestGetShellConfigUnix(t *testing.T) {
	config, err := GetShellConfig("")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if config.Shell == "" || len(config.Args) == 0 {
		t.Fatalf("config = %+v", config)
	}
	if config.Shell != "/bin/bash" {
		// Fallbacks: which-resolved bash or sh.
		if config.Shell != "sh" && !strings.HasPrefix(config.Shell, "/") {
			t.Fatalf("unexpected shell %q", config.Shell)
		}
	}
}

func TestGetShellConfigCustomPath(t *testing.T) {
	if _, err := GetShellConfig("/nonexistent/bash"); err == nil {
		t.Fatal("missing custom path should error")
	}
	// /bin/sh exists on Unix test hosts; use it as the custom path.
	if config, err := GetShellConfig("/bin/sh"); err != nil || config.Shell != "/bin/sh" {
		t.Fatalf("custom = %+v err=%v", config, err)
	}
}

func TestLegacyWslBashPath(t *testing.T) {
	if !isLegacyWslBashPath(`C:\Windows\System32\bash.exe`) {
		t.Fatal("system32 path should be legacy WSL")
	}
	if !isLegacyWslBashPath(`C:\Windows\SysNative\bash.exe`) {
		t.Fatal("sysnative path should be legacy WSL")
	}
	if isLegacyWslBashPath("/usr/bin/bash") {
		t.Fatal("unix path should not be legacy WSL")
	}
	config := getBashShellConfig(`C:\Windows\System32\bash.exe`)
	if config.CommandTransport != "stdin" || len(config.Args) != 1 || config.Args[0] != "-s" {
		t.Fatalf("wsl config = %+v", config)
	}
	config = getBashShellConfig("/bin/bash")
	if config.CommandTransport != "" || config.Args[0] != "-c" {
		t.Fatalf("unix config = %+v", config)
	}
}

func TestGetShellEnvAddsBinDir(t *testing.T) {
	env := GetShellEnv()
	found := false
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "PATH") {
			if value == GetBinDir() || strings.HasPrefix(value, GetBinDir()+string(os.PathListSeparator)) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("bin dir missing from PATH")
	}
}

func TestExpandPath(t *testing.T) {
	if got := ExpandPath("@file.txt"); got != "file.txt" {
		t.Fatalf("at = %q", got)
	}
	if got := ExpandPath("a\u3000b"); got != "a b" {
		t.Fatalf("unicode space = %q", got)
	}
}

func TestResolveToCwd(t *testing.T) {
	if got := ResolveToCwd("/abs/path", "/base"); got != "/abs/path" {
		t.Fatalf("abs = %q", got)
	}
	if got := ResolveToCwd("rel", "/base"); got != "/base/rel" {
		t.Fatalf("rel = %q", got)
	}
}
