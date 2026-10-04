package codingagent

// Port of utils/clipboard-command.ts: the daemonization-safe clipboard
// command runner (the abort seam lives in utils_core.go).

import (
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// ClipboardCommandOptions bound the clipboard runner.
type ClipboardCommandOptions struct {
	Input          string
	HasInput       bool
	TimeoutMs      int
	MaxBufferBytes int
}

// RunClipboardCommand executes a clipboard helper. Clipboard writers can
// daemonize, so they get no retained output pipes; nil bytes = failure,
// an empty buffer = success.
func RunClipboardCommand(command string, args []string, options *ClipboardCommandOptions) ([]byte, bool) {
	if options == nil {
		options = &ClipboardCommandOptions{}
	}
	timeoutMs := options.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 3000
	}
	maxBuffer := options.MaxBufferBytes
	if maxBuffer <= 0 {
		maxBuffer = 50 * 1024 * 1024
	}

	cmd := exec.Command(command, args...)
	if options.HasInput {
		cmd.Stdin = strings.NewReader(options.Input)
	} else {
		cmd.Stdin = nil
	}
	var stdout strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, false
	}

	var aborted atomic.Bool
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
		aborted.Store(true)
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	})
	defer timer.Stop()

	err := <-done
	if aborted.Load() || err != nil {
		return nil, false
	}

	output := []byte(stdout.String())
	if len(output) > maxBuffer {
		return nil, false
	}
	return output, true
}
