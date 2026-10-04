package codingagent

// Tests for fs watching, WSL detection, user agent, and child-process
// helpers (adapted from their reference tests).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileWatcherDetectsChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watched.txt")
	os.WriteFile(path, []byte("v1"), 0o644)

	events := make(chan string, 4)
	watcher := WatchWithErrorHandler(path, func(p string) {
		events <- p
	}, func() {
		t.Error("watcher error")
	})
	defer watcher.Close()

	// Modify the file.
	time.Sleep(600 * time.Millisecond)
	os.WriteFile(path, []byte("v2"), 0o644)

	select {
	case got := <-events:
		if got != path {
			t.Fatalf("event path = %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event received")
	}
}

func TestFileWatcherCloseIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	os.WriteFile(path, []byte("x"), 0o644)
	watcher := WatchWithErrorHandler(path, nil, nil)
	watcher.Close()
	watcher.Close() // no panic

	CloseWatcher(nil) // no panic
	CloseWatcher(watcher)
}

func TestIsWSLFull(t *testing.T) {
	// On non-WSL hosts the env markers are absent and /proc/version has no
	// microsoft marker (macOS lacks the file entirely).
	result := IsWSLFull()
	_ = result
}

func TestSpawnProcessSync(t *testing.T) {
	stdout, code, ok := SpawnProcessSync("echo", []string{"hello"})
	if !ok || code != 0 {
		t.Fatalf("ok = %v code = %d", ok, code)
	}
	if strings.TrimSpace(stdout) != "hello" {
		t.Fatalf("stdout = %q", stdout)
	}

	// Failing command.
	_, code, ok = SpawnProcessSync("false", nil)
	if ok || code == 1 {
		// exit code 1 reported, ok=false
	}
	if ok {
		t.Fatal("false reported ok")
	}

	// Missing command.
	_, _, ok = SpawnProcessSync("definitely-not-a-command-xyz", nil)
	if ok {
		t.Fatal("missing reported ok")
	}
}

func TestWaitForChildProcessGrace(t *testing.T) {
	// A child that exits immediately with pipes open: the wait completes
	// without hanging on the pipes.
	cmd, _ := SpawnProcess("echo", []string{"grace"})
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	done := make(chan *WaitResult, 1)
	go func() {
		done <- WaitForChildProcess(cmd, stdout, stderr)
	}()
	select {
	case result := <-done:
		if result.ExitCode != 0 {
			t.Fatalf("exit = %d", result.ExitCode)
		}
		if !strings.Contains(result.Stdout, "grace") {
			t.Fatalf("stdout = %q", result.Stdout)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait hung past the grace window")
	}
}
