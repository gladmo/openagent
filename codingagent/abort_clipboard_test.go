package codingagent

// Tests for the clipboard command runner (adapted from
// clipboard-command.test.ts).

import (
	"strings"
	"testing"
	"time"
)

func TestRunClipboardCommandSuccess(t *testing.T) {
	output, ok := RunClipboardCommand("echo", []string{"clipboard"}, nil)
	if !ok {
		t.Fatal("echo failed")
	}
	if strings.TrimSpace(string(output)) != "clipboard" {
		t.Fatalf("output = %q", output)
	}
}

func TestRunClipboardCommandFailure(t *testing.T) {
	if _, ok := RunClipboardCommand("false", nil, nil); ok {
		t.Fatal("false succeeded")
	}
	if _, ok := RunClipboardCommand("definitely-missing-cmd-xyz", nil, nil); ok {
		t.Fatal("missing succeeded")
	}
}

func TestRunClipboardCommandTimeout(t *testing.T) {
	start := time.Now()
	if _, ok := RunClipboardCommand("sleep", []string{"30"}, &ClipboardCommandOptions{TimeoutMs: 200}); ok {
		t.Fatal("sleep succeeded")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout ignored: %v", elapsed)
	}
}

func TestRunClipboardCommandMaxBuffer(t *testing.T) {
	if _, ok := RunClipboardCommand("echo", []string{"way too much output for the cap"}, &ClipboardCommandOptions{MaxBufferBytes: 4}); ok {
		t.Fatal("oversized accepted")
	}
}
