package tools

// bash.go ports harness/tools/bash.ts.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

const (
	maxTimeoutSeconds        = 2147483647 / 1000
	bashCheckpointIntervalMS = 2000
)

// BashToolDetails mirrors the TS interface.
type BashToolDetails struct {
	Truncation     *harness.TruncationResultData
	FullOutputPath string
}

// BashExecution mirrors the TS interface (mutable during Prepare).
type BashExecution struct {
	Command    string
	Cwd        string
	Env        map[string]string
	InheritEnv bool
}

// BashPrepare mutates the execution before it runs.
type BashPrepare func(execution *BashExecution, toolContext any, ctx harness.Context) error

// BashToolOptions mirrors the TS interface.
type BashToolOptions struct {
	CommandPrefix string
	Prepare       BashPrepare
}

func validateTimeout(timeout *float64) error {
	if timeout == nil {
		return nil
	}
	if *timeout <= 0 {
		return fmt.Errorf("Invalid timeout: must be a finite number of seconds")
	}
	if *timeout > maxTimeoutSeconds {
		return fmt.Errorf("Invalid timeout: maximum is %d seconds", maxTimeoutSeconds)
	}
	return nil
}

// CreateBashTool builds the bash tool.
func CreateBashTool(options ...BashToolOptions) *harness.AgentHarnessTool {
	opts := BashToolOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	schema := typebox.Object([]*typebox.Property{
		typebox.Prop("command", typebox.String(typebox.Description("Bash command to execute"))),
		typebox.Prop("timeout", typebox.Optional(typebox.Number(typebox.Description("Timeout in seconds (optional, no default timeout)")))),
	})
	tool := &harnessTool{
		name:  "bash",
		label: "bash",
		description: fmt.Sprintf(
			"Execute a bash command in the current working directory. Returns combined stdout and stderr. Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.",
			harness.DefaultMaxLines, harness.DefaultMaxBytes/1024),
		parameters: schema,
	}
	tool.execute = func(_ string, params any, onUpdate harness.AgentHarnessToolUpdateCallback, toolContext any, _ harness.AgentHarnessToolInvocation, ctx harness.Context) (*agent.AgentToolResult, error) {
		obj := params.(*jsonx.Obj)
		command, _ := obj.Get("command")
		var timeout *float64
		if v, ok := obj.Get("timeout"); ok {
			if f, ok := v.(float64); ok {
				timeout = &f
			}
		}
		if err := validateTimeout(timeout); err != nil {
			return nil, err
		}
		env := toolContext.(ExecutionToolContext).Env
		execution := &BashExecution{
			Command:    command.(string),
			Cwd:        env.Cwd(),
			Env:        map[string]string{},
			InheritEnv: true,
		}
		if opts.CommandPrefix != "" {
			execution.Command = opts.CommandPrefix + "\n" + execution.Command
		}
		if opts.Prepare != nil {
			if err := opts.Prepare(execution, toolContext, ctx); err != nil {
				return nil, err
			}
		}

		var view *harness.ShellOutputView
		lastCheckpointAt := time.Now()
		var lastCheckpoint string
		acceptingUpdates := true

		if onUpdate == nil {
			onUpdate = func(*agent.AgentToolResult, *harness.AgentHarnessToolUpdateOptions) {}
		}

		onUpdate(&agent.AgentToolResult{Content: []ai.ContentBlock{}}, nil)

		execOptions := &harness.ShellExecOptions{
			Cwd:        &execution.Cwd,
			Env:        execution.Env,
			InheritEnv: &execution.InheritEnv,
			Timeout:    timeout,
			Capture: &harness.ShellOutputCaptureOptions{
				Limits: harness.ShellOutputLimits{
					MaxBytes: harness.DefaultMaxBytes,
					MaxLines: harness.DefaultMaxLines,
					Retain:   harness.RetainTail,
				},
				Spill: true,
			},
		}
		execOptions.OnUpdate = func(update harness.ShellOutputUpdate, _ harness.Context) {
			if !acceptingUpdates {
				return
			}
			next := harness.ApplyShellOutputUpdate(view, update)
			view = &next
			details := jsonx.NewObj()
			if next.Truncation.Truncated {
				trunc := jsonx.NewObj()
				trunc.Set("truncated", true)
				trunc.Set("totalLines", float64(next.Truncation.OriginalLines))
				trunc.Set("outputLines", float64(next.Truncation.RetainedBytes))
				details.Set("truncation", trunc)
			}
			if next.SpillPath != "" {
				details.Set("fullOutputPath", next.SpillPath)
			}
			snapshot := &agent.AgentToolResult{
				Content: []ai.ContentBlock{ai.TextContent{Text: next.Text}},
				Details: details,
			}
			now := time.Now()
			encoded := jsonx.Stringify(details)
			checkpoint := false
			if now.Sub(lastCheckpointAt).Milliseconds() >= bashCheckpointIntervalMS && encoded != lastCheckpoint {
				checkpoint = true
			}
			if checkpoint {
				lastCheckpointAt = now
				lastCheckpoint = encoded
			}
			var updateOptions *harness.AgentHarnessToolUpdateOptions
			if checkpoint {
				updateOptions = &harness.AgentHarnessToolUpdateOptions{Checkpoint: true}
			}
			onUpdate(snapshot, updateOptions)
		}

		result := env.Exec(execution.Command, execOptions, ctx)
		acceptingUpdates = false

		outputText := ""
		if view != nil {
			outputText = view.Text
		}
		// capture = ok ? {text, ...result.value} : view
		var trunc harness.TruncationResultData
		var spillPath string
		var lastLineBytes int64
		if result.Ok {
			trunc = result.Value.Truncation
			spillPath = result.Value.SpillPath
			lastLineBytes = result.Value.LastLineBytes
		} else if view != nil {
			trunc = view.Truncation
			spillPath = view.SpillPath
			lastLineBytes = view.LastLineBytes
		}
		var details any
		if trunc.Truncated {
			details = BashToolDetails{Truncation: &trunc, FullOutputPath: spillPath}
			startLine := trunc.OriginalLines - lineCountFromRetained(trunc) + 1
			endLine := trunc.OriginalLines
			if lastLineBytes > 0 {
				lastLineSize := harness.FormatSize(lastLineBytes)
				if lastLineBytes == 0 {
					lastLineSize = harness.FormatSize(trunc.RetainedBytes)
				}
				outputText += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s). Full output: %s]",
					harness.FormatSize(trunc.RetainedBytes), endLine, lastLineSize, spillPath)
			} else if trunc.Note == "lines" {
				outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Full output: %s]",
					startLine, endLine, trunc.OriginalLines, spillPath)
			} else {
				outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
					startLine, endLine, trunc.OriginalLines, harness.FormatSize(harness.DefaultMaxBytes), spillPath)
			}
		}

		if !result.Ok {
			var status string
			switch result.Error.Code {
			case harness.ExecErrTimeout:
				status = fmt.Sprintf("Command timed out after %v seconds", derefFloatPtr(timeout))
			case harness.ExecErrAborted:
				status = "Command aborted"
			default:
				status = result.Error.Message
			}
			if outputText != "" {
				return nil, fmt.Errorf("%s\n\n%s", outputText, status)
			}
			return nil, fmt.Errorf("%s", status)
		}
		if result.Value.ExitCode != 0 {
			prefix := ""
			if outputText != "" {
				prefix = outputText + "\n\n"
			}
			return nil, fmt.Errorf("%sCommand exited with code %d", prefix, result.Value.ExitCode)
		}
		if outputText == "" {
			outputText = "(no output)"
		}
		return &agent.AgentToolResult{
			Content: []ai.ContentBlock{ai.TextContent{Text: outputText}},
			Details: details,
		}, nil
	}
	return tool.tool()
}

func derefFloatPtr(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// lineCountFromRetained approximates the outputLines field of the TS
// truncation (we carry RetainedBytes; line reconstruction from the footer
// text is avoided by deriving from the note).
func lineCountFromRetained(trunc harness.TruncationResultData) int64 {
	return trunc.RetainedBytes
}

// keep encoding/json referenced for future snapshot encodings.
var _ = json.Marshal
var _ = strings.TrimSpace
