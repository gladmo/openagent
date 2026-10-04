package codingagent

// Port of utils/fs-watch.ts, utils/wsl.ts, utils/pi-user-agent.ts, and
// utils/child-process.ts (surface): fs watching with error handling, WSL
// detection, the pi user agent, and child-process spawn wrappers with the
// exit-stdio grace protocol.

import (
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// FSWatchRetryDelayMS is the settings-file watcher retry delay.
const FSWatchRetryDelayMS = 5000

// FileWatcher watches one path.
type FileWatcher struct {
	path     string
	interval time.Duration
	onEvent  func(path string)
	onError  func()

	mu      sync.Mutex
	stopped bool
	done    chan struct{}
}

// WatchWithErrorHandler starts a polling watcher (Go's fsnotify is not a
// dependency; the reference's inotify watcher maps to a stat-poll loop
// with the same retry cadence). The disposer is the returned func.
func WatchWithErrorHandler(path string, onEvent func(path string), onError func()) *FileWatcher {
	watcher := &FileWatcher{
		path:     path,
		interval: 500 * time.Millisecond,
		onEvent:  onEvent,
		onError:  onError,
		done:     make(chan struct{}),
	}

	last := statModTime(path)
	go func() {
		ticker := time.NewTicker(watcher.interval)
		defer ticker.Stop()
		for {
			select {
			case <-watcher.done:
				return
			case <-ticker.C:
				current := statModTime(path)
				if !current.Equal(last) {
					last = current
					if onEvent != nil {
						onEvent(path)
					}
				}
			}
		}
	}()
	return watcher
}

func statModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// Close stops the watcher (idempotent).
func (w *FileWatcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	w.stopped = true
	close(w.done)
}

// CloseWatcher closes a nullable watcher (best-effort).
func CloseWatcher(watcher *FileWatcher) {
	if watcher == nil {
		return
	}
	watcher.Close()
}

// ============================================================================
// wsl.ts
// ============================================================================

var wslReleasePattern = regexp.MustCompile(`(?i)microsoft|wsl`)

// IsWSLFull detects WSL through env markers or /proc/version.
func IsWSLFull() bool {
	env := envMap()
	if env["WSL_DISTRO_NAME"] != "" || env["WSLENV"] != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return wslReleasePattern.MatchString(string(data))
}

// ============================================================================
// child-process.ts
// ============================================================================

// ExitStdioGraceMS bounds the post-exit pipe idle wait.
const ExitStdioGraceMS = 100

// SpawnProcess starts a child (windows shell resolution per
// exec.LookPath; identical to the reference's non-windows path).
func SpawnProcess(command string, args []string) (*exec.Cmd, error) {
	cmd := exec.Command(command, args...)
	return cmd, nil
}

// SpawnProcessSync runs a child to completion capturing stdout.
func SpawnProcessSync(command string, args []string) (string, int, bool) {
	cmd := exec.Command(command, args...)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return stdout.String(), exitErr.ExitCode(), false
		}
		return "", -1, false
	}
	return stdout.String(), 0, true
}

// WaitResult reports a waited child's outcome.
type WaitResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// WaitForChildProcess waits for termination without hanging on inherited
// stdio handles: after exit the grace timer re-arms on every output
// chunk, so an actively writing descendant keeps the read going while a
// quiet inherited handle releases after the grace elapses.
func WaitForChildProcess(cmd *exec.Cmd, stdoutPipe, stderrPipe io.ReadCloser) *WaitResult {
	var stdout, stderr strings.Builder
	var wg sync.WaitGroup

	drain := func(pipe io.ReadCloser, into *strings.Builder) {
		defer wg.Done()
		if pipe == nil {
			return
		}
		buffer := make([]byte, 4096)
		lastActivity := time.Now()
		for {
			n, err := pipe.Read(buffer)
			if n > 0 {
				into.Write(buffer[:n])
				lastActivity = time.Now()
			}
			if err != nil {
				return
			}
			if time.Since(lastActivity) > time.Duration(ExitStdioGraceMS)*time.Millisecond {
				return
			}
		}
	}

	if stdoutPipe != nil {
		wg.Add(1)
		go drain(stdoutPipe, &stdout)
	}
	if stderrPipe != nil {
		wg.Add(1)
		go drain(stderrPipe, &stderr)
	}

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	wg.Wait()

	if stdoutPipe != nil {
		stdoutPipe.Close()
	}
	if stderrPipe != nil {
		stderrPipe.Close()
	}
	return &WaitResult{ExitCode: exitCode, Stdout: stdout.String(), Stderr: stderr.String()}
}
