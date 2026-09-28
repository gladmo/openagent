package pico3

// bash.go ports harness/pico3/bash.ts: the bash tool declaration. Output
// is piped to the kernel through the stream callback; bounds come from the
// output policy; abort SIGKILLs the process group.

import (
	"os/exec"
	"syscall"
	"time"

	"github.com/gladmo/openagent/typebox"
)

// BashParameters builds the {command, cwd?} schema.
func BashParameters() *typebox.Schema {
	return typebox.Object([]*typebox.Property{
		typebox.Prop("command", typebox.String()),
		typebox.Prop("cwd", typebox.Optional(typebox.String())),
	})
}

// ToolDeclaration mirrors the pico3 tool surface.
type ToolDeclaration struct {
	Name        string
	Description string
	Parameters  *typebox.Schema
	Replay      string // "unsafe"
	Output      OutputPolicy
	Execute     func(args map[string]any, api ToolAPI, ctx ContextAlias) (*ToolResult, error)
}

// OutputPolicy carries the output bounds.
type OutputPolicy struct {
	MaxBytes *int64
	MaxLines *int64
}

// ToolAPI is the kernel-facing API handed to tools.
type ToolAPI interface {
	Stream(chunk []byte)
	Progress(text string)
	Memo(key string, value any) error
}

// ToolResult mirrors the TS shape.
type ToolResult struct {
	IsError bool
	Details *struct {
		ExitCode *int64
		Signal   string
		MS       float64
	}
}

// BashTool builds the bash tool declaration. Run a shell command; output
// is piped to the kernel; abort kills the process.
func BashTool(output OutputPolicy) *ToolDeclaration {
	return &ToolDeclaration{
		Name:        "bash",
		Description: "Run a shell command",
		Parameters:  BashParameters(),
		Replay:      "unsafe",
		Output:      output,
		Execute: func(args map[string]any, api ToolAPI, ctx ContextAlias) (*ToolResult, error) {
			started := time.Now()
			command, _ := args["command"].(string)
			cwd, _ := args["cwd"].(string)

			child := exec.Command("bash", "-c", command)
			if cwd != "" {
				child.Dir = cwd
			}
			child.Stdin = nil
			child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			stdout, err := child.StdoutPipe()
			if err != nil {
				return nil, err
			}
			child.Stderr = child.Stdout
			if err := child.Start(); err != nil {
				return nil, err
			}

			kill := func() {
				_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
			}
			if ctx != nil && ctx.AbortSignal() != nil {
				remove := ctx.AbortSignal().OnAbort(kill)
				defer remove()
			}

			buf := make([]byte, 32*1024)
			for {
				n, readErr := stdout.Read(buf)
				if n > 0 && api != nil {
					api.Stream(buf[:n])
				}
				if readErr != nil {
					break
				}
			}
			waitErr := child.Wait()

			result := &ToolResult{}
			signal := ""
			exitCode := int64(0)
			if waitErr != nil {
				exitCode = -1
				result.IsError = true
				if exitErr, ok := waitErr.(*exec.ExitError); ok {
					exitCode = int64(exitErr.ExitCode())
					// Signal-terminated processes report -1 exit codes on
					// Unix; the abort path SIGKILLs, so attribute that.
					if exitCode == -1 {
						signal = "SIGKILL"
					}
				}
			}
			details := &struct {
				ExitCode *int64
				Signal   string
				MS       float64
			}{ExitCode: &exitCode, Signal: signal, MS: float64(time.Since(started).Milliseconds())}
			result.Details = details
			return result, nil
		},
	}
}
