package codingagent

// Port of core/exec.ts: shared command execution for extensions and custom
// tools, with abort and timeout support.

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/gladmo/openagent/abort"
)

// ExecOptions bound a command execution.
type ExecOptions struct {
	Signal  *abort.Signal
	Timeout int // milliseconds
	Cwd     string
}

// ExecResult reports the captured output and termination.
type ExecResult struct {
	Stdout string
	Stderr string
	Code   int
	Killed bool
}

// ExecCommand runs a command and returns stdout/stderr/code; failures
// resolve with code 1 rather than rejecting (mirroring the reference).
func ExecCommand(command string, args []string, cwd string, options *ExecOptions) ExecResult {
	ctx := context.Background()
	var cancel context.CancelFunc
	if options != nil && options.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(options.Timeout)*time.Millisecond)
		defer cancel()
	}
	if options != nil && options.Signal != nil {
		if done := options.Signal.Done(); done != nil {
			var cancelSignal context.CancelFunc
			ctx, cancelSignal = context.WithCancel(ctx)
			defer cancelSignal()
			go func() {
				select {
				case <-done:
					cancelSignal()
				case <-ctx.Done():
				}
			}()
		}
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = cwd
	cmd.Stdin = nil
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	killed := ctx.Err() != nil
	code := 0
	if err != nil {
		code = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
	}
	return ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: code, Killed: killed}
}
