package harness

// utils_exec_capture.go is openagent-native (no TS counterpart): the
// reference inlines this composition in env/nodejs.ts because it ships a
// single environment; openagent exposes it so custom ExecutionEnvs reuse
// the bounded-view + spill semantics without re-porting the feed
// algorithm. Decision record: Agent Note exec-capture-extraction.

import "os"

// ExecCapture composes the bounded output view with spill-to-temp-file
// persistence for one shell execution. Push chunks from the read loop,
// Finish when output ends, gate the exec result on SpillError, and take
// Snapshot for the final view.
//
// Spill behavior (options.Spill): chunks buffer until the view first
// truncates, then the buffered prefix plus every later chunk append to a
// temp file created through the env, so the file holds the complete
// output from the beginning; the path publishes on the capture. Writes
// are synchronous — the caller's bounded pipe applies backpressure. A
// failed create/write records ExecErrSpillError.
type ExecCapture struct {
	capture  *OutputCapture
	onUpdate func(ShellOutputUpdate, Context)
	ctx      Context
	env      ExecutionEnv
	spill    bool
	prefix   [][]byte
	file     *os.File
	path     string
	err      *ExecutionError
}

// NewExecCapture builds the capture over env (spill temp files land in
// the env's temp space). Limits default; onUpdate receives the final
// view on Snapshot.
func NewExecCapture(env ExecutionEnv, options *ShellOutputCaptureOptions, onUpdate func(ShellOutputUpdate, Context), ctx Context) *ExecCapture {
	limits := ShellOutputLimits{MaxBytes: DefaultMaxBytes, MaxLines: DefaultMaxLines, Retain: RetainTail}
	spill := false
	if options != nil {
		limits = options.Limits
		spill = options.Spill
	}
	return &ExecCapture{
		capture:  NewOutputCapture(&ShellOutputCaptureOptions{Limits: limits}, ctx, nil, nil),
		onUpdate: onUpdate,
		ctx:      ctx,
		env:      env,
		spill:    spill,
	}
}

// Push records one chunk. Chunks before the first truncation are buffered
// (copied; callers typically reuse their read buffer) so the spill file
// can start at the beginning of the output.
func (c *ExecCapture) Push(chunk []byte) {
	wasTruncated := c.capture.Truncated()
	c.capture.Push(string(chunk))
	if !c.spill || len(chunk) == 0 || c.err != nil {
		return
	}
	switch {
	case c.path != "" || wasTruncated:
		c.writeSpill(chunk)
	case c.capture.Truncated():
		for _, buffered := range c.prefix {
			c.writeSpill(buffered)
		}
		c.prefix = nil
		c.writeSpill(chunk)
	default:
		c.prefix = append(c.prefix, append([]byte(nil), chunk...))
	}
}

// writeSpill appends to the spill file, creating it on first use.
func (c *ExecCapture) writeSpill(chunk []byte) {
	if c.err != nil {
		return
	}
	if c.file == nil {
		prefix := "pi-output-"
		suffix := ".log"
		created := c.env.CreateTempFile(&CreateTempFileOptions{Prefix: &prefix, Suffix: &suffix}, c.ctx)
		if !created.Ok {
			c.err = NewExecutionError(ExecErrSpillError, created.Error.Message)
			return
		}
		file, err := os.OpenFile(created.Value, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			c.err = NewExecutionError(ExecErrSpillError, err.Error())
			return
		}
		c.file = file
		c.path = created.Value
		c.capture.SetSpillPath(c.path)
	}
	if _, err := c.file.Write(chunk); err != nil {
		c.err = NewExecutionError(ExecErrSpillError, err.Error())
	}
}

// Finish flushes the capture and closes the spill file.
func (c *ExecCapture) Finish() {
	c.capture.Finish()
	if c.file != nil {
		_ = c.file.Close()
		c.file = nil
	}
}

// SpillError reports a failed spill, if any.
func (c *ExecCapture) SpillError() *ExecutionError { return c.err }

// Snapshot returns the bounded view and publishes it via onUpdate.
func (c *ExecCapture) Snapshot() ShellOutputView {
	view := c.capture.Snapshot()
	if c.onUpdate != nil {
		c.onUpdate(&ShellOutputReplace{Output: view}, c.ctx)
	}
	return view
}
