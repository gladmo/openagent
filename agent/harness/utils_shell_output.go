package harness

// utils_shell_output.go ports harness/utils/shell-output.ts.

import (
	chordcontext "github.com/gladmo/openagent/chord/context"
)

// ShellCaptureProgress mirrors the TS interface.
type ShellCaptureProgress struct {
	Output            string
	Truncation        TruncationResultData
	FullOutputPath    string
	HasFullOutputPath bool
	LastLineBytes     int64
}

// ShellCaptureOptions mirrors the TS interface.
type ShellCaptureOptions struct {
	Cwd        *string
	Env        map[string]string
	InheritEnv *bool
	Timeout    *float64
	OnChunk    func(chunk string, getProgress func() ShellCaptureProgress, ctx Context)
	// ReturnExecutionErrors returns failures with captured output.
	ReturnExecutionErrors bool
}

// ShellCaptureResult mirrors the TS interface.
type ShellCaptureResult struct {
	ShellCaptureProgress
	ExitCode       *int64
	Cancelled      bool
	Truncated      bool
	ExecutionError *ExecutionError
}

func progressFrom(output ShellOutputView) ShellCaptureProgress {
	return ShellCaptureProgress{
		Output:            output.Text,
		Truncation:        output.Truncation,
		FullOutputPath:    output.SpillPath,
		HasFullOutputPath: output.HasSpillPath,
		LastLineBytes:     output.LastLineBytes,
	}
}

// ExecuteShellWithCapture is the compatibility collector producing one
// bounded final view.
func ExecuteShellWithCapture(
	env ExecutionEnv,
	command string,
	options *ShellCaptureOptions,
	ctx Context,
) Result[ShellCaptureResult, *ExecutionError] {
	var output *ShellOutputView
	execOptions := &ShellExecOptions{
		Capture: &ShellOutputCaptureOptions{
			Limits: ShellOutputLimits{MaxBytes: DefaultMaxBytes, MaxLines: DefaultMaxLines, Retain: RetainTail},
			Spill:  true,
		},
	}
	if options != nil {
		execOptions.Cwd = options.Cwd
		execOptions.Env = options.Env
		execOptions.InheritEnv = options.InheritEnv
		execOptions.Timeout = options.Timeout
	}
	execOptions.OnUpdate = func(update ShellOutputUpdate, updateContext Context) {
		previous := output
		next := ApplyShellOutputUpdate(output, update)
		output = &next
		var chunk string
		switch u := update.(type) {
		case *ShellOutputAppend:
			chunk = u.Text
		case *ShellOutputSlide:
			chunk = u.Text
		case *ShellOutputReplace:
			if previous == nil {
				chunk = next.Text
			}
		}
		// Metadata-only updates and post-cap replacements carry no new
		// incremental chunk; reporting their full view would duplicate bytes
		// for accumulating callers.
		if chunk != "" && options != nil && options.OnChunk != nil {
			current := output
			options.OnChunk(chunk, func() ShellCaptureProgress { return progressFrom(*current) }, updateContext)
		}
	}

	result := env.Exec(command, execOptions, ctx)

	if output == nil {
		empty := TruncateTail("", nil)
		view := ShellOutputView{
			Text: empty.Content,
			ShellOutputMetadata: ShellOutputMetadata{Truncation: TruncationResultData{
				Truncated: empty.Truncated,
			}},
		}
		output = &view
	}
	progress := progressFrom(*output)
	if !result.Ok {
		if result.Error.Code == ExecErrAborted || ctx.AbortSignal().Aborted() {
			return Ok[ShellCaptureResult, *ExecutionError](ShellCaptureResult{
				ShellCaptureProgress: progress,
				Cancelled:            true,
				Truncated:            progress.Truncation.Truncated,
			})
		}
		if options != nil && options.ReturnExecutionErrors {
			return Ok[ShellCaptureResult, *ExecutionError](ShellCaptureResult{
				ShellCaptureProgress: progress,
				Truncated:            progress.Truncation.Truncated,
				ExecutionError:       result.Error,
			})
		}
		return Err[ShellCaptureResult, *ExecutionError](result.Error)
	}
	exit := result.Value.ExitCode
	return Ok[ShellCaptureResult, *ExecutionError](ShellCaptureResult{
		ShellCaptureProgress: progress,
		ExitCode:             &exit,
		Truncated:            result.Value.Truncation.Truncated,
	})
}

// SanitizeBinaryOutput is the TS alias.
func SanitizeBinaryOutput(text string) string { return SanitizeShellOutput(text) }

// keep import used if future files reference chordcontext here.
var _ = chordcontext.BackgroundContext
