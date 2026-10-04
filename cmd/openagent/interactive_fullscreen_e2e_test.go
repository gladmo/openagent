package main

// End-to-end proof that --tui-mode fullscreen renders through the chat
// viewport: the reply is painted and the transcript scroll view is the
// primary follow-end view.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/codingagent"
	"github.com/gladmo/openagent/tui"
)

func TestInteractiveFullscreenChatsThroughProvider(t *testing.T) {
	host, captured := newFakeProviderSession(t)

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdinW.Close()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinR, stdoutW
	defer func() { os.Stdin, os.Stdout = origStdin, origStdout }()

	mode := codingagent.NewInteractiveMode(codingagent.InteractiveModeOptions{
		Session:  host.Session(),
		TuiMode:  "fullscreen",
		Terminal: tui.NewProcessTerminal(),
		Version:  "test",
	})
	alt, ok := mode.TUI().(*tui.TuiAltScreen)
	if !ok {
		t.Fatalf("renderer = %T, want alt screen", mode.TUI())
	}
	if !alt.IsFollowingOutput() {
		t.Fatal("fullscreen transcript must follow output")
	}

	runDone := make(chan error, 1)
	go func() { runDone <- mode.Run() }()

	if _, err := stdinW.Write([]byte("hello\r")); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	var painted strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(stripANSI(painted.String()), "pong") && time.Now().Before(deadline) {
		chunk := make([]byte, 4096)
		_ = stdoutR.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		if n, err := stdoutR.Read(chunk); err == nil && n > 0 {
			painted.Write(chunk[:n])
		}
	}
	if !strings.Contains(stripANSI(painted.String()), "pong") {
		t.Fatalf("fullscreen reply not painted; output:\n%q", stripANSI(painted.String()))
	}

	requested, authHeader, _ := captured.snapshot()
	if !requested || authHeader != "Bearer secret-key" {
		t.Fatalf("requested=%v auth=%q", requested, authHeader)
	}

	if _, err := stdinW.Write([]byte("/quit\r")); err != nil {
		t.Fatalf("write /quit: %v", err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit after /quit")
	}
}
