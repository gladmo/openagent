// Package nodejs ports harness/env/nodejs.ts: the real-filesystem
// ExecutionEnv backed by Go's os/exec and os packages.
package nodejs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gladmo/openagent/agent/harness"
)

// NodeExecutionEnv mirrors NodeExecutionEnv{cwd, shellPath?, shellEnv?}.
type NodeExecutionEnv struct {
	cwd       string
	shellPath string
	shellEnv  map[string]string

	mu        sync.Mutex
	childPIDs []int
}

// New creates an execution env rooted at cwd.
func New(cwd string, options ...func(*NodeExecutionEnv)) *NodeExecutionEnv {
	env := &NodeExecutionEnv{cwd: cwd}
	for _, option := range options {
		option(env)
	}
	return env
}

// WithShellPath sets a custom shell path.
func WithShellPath(path string) func(*NodeExecutionEnv) {
	return func(e *NodeExecutionEnv) { e.shellPath = path }
}

// WithShellEnv sets extra environment variables.
func WithShellEnv(shellEnv map[string]string) func(*NodeExecutionEnv) {
	return func(e *NodeExecutionEnv) { e.shellEnv = shellEnv }
}

// Cwd returns the working directory.
func (e *NodeExecutionEnv) Cwd() string { return e.cwd }

const (
	maxTimeoutMS       = 2147483647
	maxTimeoutSeconds  = maxTimeoutMS / 1000
	exitStdioGraceMS   = 100
	spillHighWaterMark = 8 * 1024 * 1024
)

func resolveTimeoutMS(timeout *float64) (float64, bool, *harness.ExecutionError) {
	if timeout == nil {
		return 0, false, nil
	}
	if *timeout <= 0 {
		return 0, false, harness.NewExecutionError(harness.ExecErrTimeout, "Invalid timeout: must be a finite number of seconds")
	}
	timeoutMS := *timeout * 1000
	if timeoutMS > maxTimeoutMS {
		return 0, false, harness.NewExecutionError(harness.ExecErrTimeout, fmt.Sprintf("Invalid timeout: maximum is %d seconds", maxTimeoutSeconds))
	}
	return timeoutMS, true, nil
}

// resolvePath mirrors the TS resolution: ~ expansion, file:// URLs, then
// absolute/relative resolution against cwd.
func (e *NodeExecutionEnv) resolvePath(path string) string {
	normalized := path
	home, homeErr := os.UserHomeDir()
	if normalized == "~" && homeErr == nil {
		normalized = home
	} else if strings.HasPrefix(normalized, "~/") && homeErr == nil {
		normalized = filepath.Join(home, normalized[2:])
	} else if strings.HasPrefix(normalized, "file://") {
		trimmed := strings.TrimPrefix(normalized, "file://")
		// Malformed URLs fall through as ordinary paths.
		if !strings.Contains(trimmed, "://") {
			normalized = trimmed
		}
	}
	if filepath.IsAbs(normalized) {
		if abs, err := filepath.Abs(normalized); err == nil {
			return abs
		}
		return normalized
	}
	abs, err := filepath.Abs(filepath.Join(e.cwd, normalized))
	if err != nil {
		return normalized
	}
	return abs
}

func fileKindFromInfo(info os.FileInfo) (string, bool) {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return harness.FileKindSymlink, true
	case info.IsDir():
		return harness.FileKindDirectory, true
	case info.Mode().IsRegular():
		return harness.FileKindFile, true
	default:
		return "", false
	}
}

func fileInfoFromInfo(path string, info os.FileInfo) harness.Result[harness.FileInfo, *harness.FileError] {
	kind, ok := fileKindFromInfo(info)
	if !ok {
		return harness.Err[harness.FileInfo, *harness.FileError](harness.NewFileError(harness.FileErrInvalid, "Unsupported file type", path))
	}
	return harness.Ok[harness.FileInfo, *harness.FileError](harness.FileInfo{
		Name:    filepath.Base(path),
		Path:    path,
		Kind:    kind,
		Size:    info.Size(),
		MtimeMs: float64(info.ModTime().UnixMilli()),
	})
}

// toFileError maps Go errno errors to the stable file error codes.
func toFileError(err error, fallbackPath string) *harness.FileError {
	if fileErr, ok := err.(*harness.FileError); ok {
		return fileErr
	}
	if err == nil {
		return harness.NewFileError(harness.FileErrUnknown, "unknown error", fallbackPath)
	}
	pathErr, isPathErr := err.(*os.PathError)
	code := ""
	if isPathErr {
		err = pathErr.Err
	}
	if errno, ok := err.(syscall.Errno); ok {
		code = errno.Error()
	}
	message := err.Error()
	path := fallbackPath
	if isPathErr {
		path = pathErr.Path
	}
	switch {
	case strings.Contains(code, "no such file"), os.IsNotExist(err):
		return harness.NewFileError(harness.FileErrNotFound, message, path)
	case os.IsPermission(err):
		return harness.NewFileError(harness.FileErrPermissionDenied, message, path)
	case strings.Contains(code, "not a directory"):
		return harness.NewFileError(harness.FileErrNotDirectory, message, path)
	case strings.Contains(code, "is a directory"):
		return harness.NewFileError(harness.FileErrIsDirectory, message, path)
	case strings.Contains(code, "invalid argument"):
		return harness.NewFileError(harness.FileErrInvalid, message, path)
	default:
		return harness.NewFileError(harness.FileErrUnknown, message, path)
	}
}

func abortFileError[T any](signalPath string) harness.Result[T, *harness.FileError] {
	return harness.Err[T, *harness.FileError](harness.NewFileError(harness.FileErrAborted, "aborted", signalPath))
}

// ---------------------------------------------------------------------------
// FileSystem
// ---------------------------------------------------------------------------

func (e *NodeExecutionEnv) AbsolutePath(path string, ctx harness.Context) harness.Result[string, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[string](path)
	}
	return harness.Ok[string, *harness.FileError](e.resolvePath(path))
}

func (e *NodeExecutionEnv) JoinPath(parts []string, ctx harness.Context) harness.Result[string, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[string]("")
	}
	return harness.Ok[string, *harness.FileError](filepath.Join(parts...))
}

func (e *NodeExecutionEnv) ReadTextFile(path string, ctx harness.Context) harness.Result[string, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[string](path)
	}
	resolved := e.resolvePath(path)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return harness.Err[string, *harness.FileError](toFileError(err, resolved))
	}
	return harness.Ok[string, *harness.FileError](string(data))
}

func (e *NodeExecutionEnv) ReadTextLines(path string, options *harness.ReadTextLinesOptions, ctx harness.Context) harness.Result[[]string, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[[]string](path)
	}
	resolved := e.resolvePath(path)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return harness.Err[[]string, *harness.FileError](toFileError(err, resolved))
	}
	content := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if options != nil && options.MaxLines != nil && len(lines) > *options.MaxLines {
		lines = lines[:*options.MaxLines]
	}
	return harness.Ok[[]string, *harness.FileError](lines)
}

func (e *NodeExecutionEnv) ReadBinaryFile(path string, ctx harness.Context) harness.Result[[]byte, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[[]byte](path)
	}
	resolved := e.resolvePath(path)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return harness.Err[[]byte, *harness.FileError](toFileError(err, resolved))
	}
	return harness.Ok[[]byte, *harness.FileError](data)
}

func (e *NodeExecutionEnv) WriteFile(path string, content []byte, ctx harness.Context) harness.Result[struct{}, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[struct{}](path)
	}
	resolved := e.resolvePath(path)
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return harness.Err[struct{}, *harness.FileError](toFileError(err, resolved))
	}
	if err := os.WriteFile(resolved, content, 0o644); err != nil {
		return harness.Err[struct{}, *harness.FileError](toFileError(err, resolved))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}

func (e *NodeExecutionEnv) AppendFile(path string, content []byte, ctx harness.Context) harness.Result[struct{}, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[struct{}](path)
	}
	resolved := e.resolvePath(path)
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return harness.Err[struct{}, *harness.FileError](toFileError(err, resolved))
	}
	f, err := os.OpenFile(resolved, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return harness.Err[struct{}, *harness.FileError](toFileError(err, resolved))
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		return harness.Err[struct{}, *harness.FileError](toFileError(err, resolved))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}

func (e *NodeExecutionEnv) RenameFile(sourcePath, destinationPath string, ctx harness.Context) harness.Result[struct{}, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[struct{}](sourcePath)
	}
	source := e.resolvePath(sourcePath)
	destination := e.resolvePath(destinationPath)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return harness.Err[struct{}, *harness.FileError](toFileError(err, destination))
	}
	if err := os.Rename(source, destination); err != nil {
		return harness.Err[struct{}, *harness.FileError](toFileError(err, source))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}

func (e *NodeExecutionEnv) FileInfo(path string, ctx harness.Context) harness.Result[harness.FileInfo, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[harness.FileInfo](path)
	}
	resolved := e.resolvePath(path)
	info, err := os.Lstat(resolved)
	if err != nil {
		return harness.Err[harness.FileInfo, *harness.FileError](toFileError(err, resolved))
	}
	result := fileInfoFromInfo(resolved, info)
	// TS resolves symlinks lazily via canonicalPath; kind stays symlink here.
	return result
}

func (e *NodeExecutionEnv) ListDir(path string, ctx harness.Context) harness.Result[[]harness.FileInfo, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[[]harness.FileInfo](path)
	}
	resolved := e.resolvePath(path)
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return harness.Err[[]harness.FileInfo, *harness.FileError](toFileError(err, resolved))
	}
	out := make([]harness.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result := fileInfoFromInfo(filepath.Join(resolved, entry.Name()), info)
		if result.Ok {
			out = append(out, result.Value)
		}
	}
	return harness.Ok[[]harness.FileInfo, *harness.FileError](out)
}

func (e *NodeExecutionEnv) CanonicalPath(path string, ctx harness.Context) harness.Result[string, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[string](path)
	}
	resolved := e.resolvePath(path)
	canonical, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return harness.Err[string, *harness.FileError](toFileError(err, resolved))
	}
	return harness.Ok[string, *harness.FileError](canonical)
}

func (e *NodeExecutionEnv) Exists(path string, ctx harness.Context) harness.Result[bool, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[bool](path)
	}
	resolved := e.resolvePath(path)
	if _, err := os.Lstat(resolved); err != nil {
		if os.IsNotExist(err) {
			return harness.Ok[bool, *harness.FileError](false)
		}
		return harness.Err[bool, *harness.FileError](toFileError(err, resolved))
	}
	return harness.Ok[bool, *harness.FileError](true)
}

func (e *NodeExecutionEnv) CreateDir(path string, options *harness.CreateDirOptions, ctx harness.Context) harness.Result[struct{}, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[struct{}](path)
	}
	resolved := e.resolvePath(path)
	recursive := true
	if options != nil && options.Recursive != nil {
		recursive = *options.Recursive
	}
	var err error
	if recursive {
		err = os.MkdirAll(resolved, 0o755)
	} else {
		err = os.Mkdir(resolved, 0o755)
	}
	if err != nil {
		return harness.Err[struct{}, *harness.FileError](toFileError(err, resolved))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}

func (e *NodeExecutionEnv) Remove(path string, options *harness.RemoveOptions, ctx harness.Context) harness.Result[struct{}, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[struct{}](path)
	}
	resolved := e.resolvePath(path)
	recursive := false
	force := false
	if options != nil {
		if options.Recursive != nil {
			recursive = *options.Recursive
		}
		if options.Force != nil {
			force = *options.Force
		}
	}
	var err error
	if recursive {
		err = os.RemoveAll(resolved)
	} else {
		err = os.Remove(resolved)
	}
	if err != nil {
		if os.IsNotExist(err) && force {
			return harness.Ok[struct{}, *harness.FileError](struct{}{})
		}
		return harness.Err[struct{}, *harness.FileError](toFileError(err, resolved))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}

func (e *NodeExecutionEnv) CreateTempDir(prefix string, ctx harness.Context) harness.Result[string, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[string]("")
	}
	if prefix == "" {
		prefix = "tmp-"
	}
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return harness.Err[string, *harness.FileError](toFileError(err, ""))
	}
	return harness.Ok[string, *harness.FileError](dir)
}

func (e *NodeExecutionEnv) CreateTempFile(options *harness.CreateTempFileOptions, ctx harness.Context) harness.Result[string, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[string]("")
	}
	prefix, suffix := "", ""
	if options != nil {
		if options.Prefix != nil {
			prefix = *options.Prefix
		}
		if options.Suffix != nil {
			suffix = *options.Suffix
		}
	}
	f, err := os.CreateTemp("", prefix+"*"+suffix)
	if err != nil {
		return harness.Err[string, *harness.FileError](toFileError(err, ""))
	}
	defer f.Close()
	return harness.Ok[string, *harness.FileError](f.Name())
}

func (e *NodeExecutionEnv) Cleanup(ctx harness.Context) error { return nil }

// ---------------------------------------------------------------------------
// Shell
// ---------------------------------------------------------------------------

type shellConfig struct {
	shell            string
	args             []string
	commandTransport string // "argv" | "stdin"
}

func findBashOnPath() (string, bool) {
	lookPath, err := exec.LookPath("bash")
	if err != nil || lookPath == "" {
		return "", false
	}
	if _, err := os.Stat(lookPath); err != nil {
		return "", false
	}
	return lookPath, true
}

func getBashShellConfig(shell string) shellConfig {
	// Legacy WSL bash.exe ships `bash -s` + stdin transport; the regex
	// [a-z]:\windows\(system32|sysnative)\bash.exe is Windows-only and not
	// reachable on this platform.
	return shellConfig{shell: shell, args: []string{"-c"}, commandTransport: "argv"}
}

func (e *NodeExecutionEnv) getShellConfig() (shellConfig, *harness.ExecutionError) {
	if e.shellPath != "" {
		if _, err := os.Stat(e.shellPath); err == nil {
			return getBashShellConfig(e.shellPath), nil
		}
		return shellConfig{}, harness.NewExecutionError(harness.ExecErrShellUnavailable, "Custom shell path not found: "+e.shellPath)
	}
	if _, err := os.Stat("/bin/bash"); err == nil {
		return getBashShellConfig("/bin/bash"), nil
	}
	if bash, ok := findBashOnPath(); ok {
		return getBashShellConfig(bash), nil
	}
	return shellConfig{shell: "sh", args: []string{"-c"}, commandTransport: "argv"}, nil
}

func (e *NodeExecutionEnv) getShellEnv(extraEnv map[string]string, inheritEnv bool) []string {
	if !inheritEnv {
		env := []string{}
		for k, v := range extraEnv {
			env = append(env, k+"="+v)
		}
		return env
	}
	env := os.Environ()
	for k, v := range e.shellEnv {
		env = append(env, k+"="+v)
	}
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}
	return env
}

func killProcessTree(pid int) {
	// Unix: kill the process group first, then the process.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// Exec mirrors the TS shell execution: bash -c command with combined
// stdout/stderr capture, bounded view, spill file, timeout and abort.
func (e *NodeExecutionEnv) Exec(command string, options *harness.ShellExecOptions, ctx harness.Context) harness.Result[harness.ShellExecResult, *harness.ExecutionError] {
	var timeout *float64
	var captureOptions *harness.ShellOutputCaptureOptions
	var onUpdate func(harness.ShellOutputUpdate, harness.Context)
	cwd := e.cwd
	env := map[string]string{}
	inheritEnv := true
	if options != nil {
		timeout = options.Timeout
		captureOptions = options.Capture
		onUpdate = options.OnUpdate
		if options.Cwd != nil {
			cwd = *options.Cwd
		}
		if options.Env != nil {
			env = options.Env
		}
		if options.InheritEnv != nil {
			inheritEnv = *options.InheritEnv
		}
	}
	timeoutMS, hasTimeout, timeoutErr := resolveTimeoutMS(timeout)
	if timeoutErr != nil {
		return harness.Err[harness.ShellExecResult, *harness.ExecutionError](timeoutErr)
	}
	if ctx.AbortSignal().Aborted() {
		return harness.Err[harness.ShellExecResult, *harness.ExecutionError](
			harness.NewExecutionError(harness.ExecErrAborted, "aborted"))
	}

	config, configErr := e.getShellConfig()
	if configErr != nil {
		return harness.Err[harness.ShellExecResult, *harness.ExecutionError](configErr)
	}

	args := append(append([]string{}, config.args...), command)
	cmd := exec.Command(config.shell, args...)
	if config.commandTransport == "stdin" {
		cmd = exec.Command(config.shell, config.args...)
		cmd.Stdin = strings.NewReader(command)
	}
	cmd.Dir = cwd
	cmd.Env = e.getShellEnv(env, inheritEnv)
	sysProcAttr(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return harness.Err[harness.ShellExecResult, *harness.ExecutionError](
			harness.NewExecutionError(harness.ExecErrSpawnError, err.Error()))
	}
	cmd.Stderr = cmd.Stdout // combined

	capture := newExecCapture(captureOptions, onUpdate, ctx)
	if err := cmd.Start(); err != nil {
		return harness.Err[harness.ShellExecResult, *harness.ExecutionError](
			harness.NewExecutionError(harness.ExecErrSpawnError, err.Error()))
	}
	// cmd.Process exists only after a successful Start.
	e.trackPID(cmd.Process.Pid)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := stdout.Read(buf)
			if n > 0 {
				capture.push(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
		capture.finish()
		done <- cmd.Wait()
	}()

	var timer *time.Timer
	// Buffered signal so the timer goroutine never blocks; a receive below
	// carries the happens-before edge the bare bool write lacked.
	timeoutFired := make(chan struct{}, 1)
	if hasTimeout {
		timer = time.AfterFunc(time.Duration(timeoutMS)*time.Millisecond, func() {
			select {
			case timeoutFired <- struct{}{}:
			default:
			}
			if cmd.Process != nil {
				killProcessTree(cmd.Process.Pid)
			}
		})
	}

	abortRemove := ctx.AbortSignal().OnAbort(func() {
		if cmd.Process != nil {
			killProcessTree(cmd.Process.Pid)
		}
	})
	defer abortRemove()

	waitErr := <-done
	if timer != nil {
		timer.Stop()
	}

	if ctx.AbortSignal().Aborted() {
		return harness.Err[harness.ShellExecResult, *harness.ExecutionError](
			harness.NewExecutionError(harness.ExecErrAborted, "aborted"))
	}
	select {
	case <-timeoutFired:
		return harness.Err[harness.ShellExecResult, *harness.ExecutionError](
			harness.NewExecutionError(harness.ExecErrTimeout, "Command timed out"))
	default:
	}

	exitCode := int64(0)
	if waitErr != nil {
		exitCode = 1
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = int64(exitErr.ExitCode())
		}
	}
	view := capture.snapshot()
	return harness.Ok[harness.ShellExecResult, *harness.ExecutionError](harness.ShellExecResult{
		ShellOutputMetadata: view.ShellOutputMetadata,
		ExitCode:            exitCode,
	})
}

func (e *NodeExecutionEnv) trackPID(pid int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.childPIDs = append(e.childPIDs, pid)
}

// CleanupShell kills tracked child processes.
func (e *NodeExecutionEnv) CleanupShell(ctx harness.Context) error {
	e.mu.Lock()
	pids := append([]int{}, e.childPIDs...)
	e.childPIDs = nil
	e.mu.Unlock()
	for _, pid := range pids {
		killProcessTree(pid)
	}
	return nil
}

// OpenTextLineReader opens a pull-based line reader.
func (e *NodeExecutionEnv) OpenTextLineReader(path string, ctx harness.Context) harness.Result[harness.TextLineReader, *harness.FileError] {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[harness.TextLineReader](path)
	}
	resolved := e.resolvePath(path)
	f, err := os.Open(resolved)
	if err != nil {
		return harness.Err[harness.TextLineReader, *harness.FileError](toFileError(err, resolved))
	}
	return harness.Ok[harness.TextLineReader, *harness.FileError](&nodeTextLineReader{file: f, offset: 0})
}

// nodeTextLineReader reads 64KB chunks at explicit byte offsets with strict
// LF termination semantics.
type nodeTextLineReader struct {
	file   *os.File
	offset int64
	buf    []byte
	eof    bool
}

func (r *nodeTextLineReader) ReadLine(ctx harness.Context) (harness.Result[*harness.TextLine, *harness.FileError], error) {
	if ctx.AbortSignal().Aborted() {
		return abortFileError[*harness.TextLine](""), nil
	}
	for {
		if idx := indexByte(r.buf, '\n'); idx >= 0 {
			line := string(r.buf[:idx])
			r.buf = r.buf[idx+1:]
			return harness.Ok[*harness.TextLine, *harness.FileError](&harness.TextLine{
				Text:       line,
				Terminated: true,
			}), nil
		}
		if r.eof {
			if len(r.buf) == 0 {
				return harness.Ok[*harness.TextLine, *harness.FileError](nil), nil
			}
			line := string(r.buf)
			r.buf = nil
			return harness.Ok[*harness.TextLine, *harness.FileError](&harness.TextLine{
				Text:       line,
				Terminated: false,
			}), nil
		}
		chunk := make([]byte, 64*1024)
		n, err := r.file.Read(chunk)
		if n > 0 {
			r.buf = append(r.buf, chunk[:n]...)
			r.offset += int64(n)
		}
		if err != nil {
			r.eof = true
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func (r *nodeTextLineReader) Close(harness.Context) error { return r.file.Close() }

// execCapture adapts OutputCapture for exec streaming.
type execCapture struct {
	capture  *harness.OutputCapture
	onUpdate func(harness.ShellOutputUpdate, harness.Context)
	ctx      harness.Context
}

func newExecCapture(options *harness.ShellOutputCaptureOptions, onUpdate func(harness.ShellOutputUpdate, harness.Context), ctx harness.Context) *execCapture {
	limits := harness.ShellOutputLimits{MaxBytes: harness.DefaultMaxBytes, MaxLines: harness.DefaultMaxLines, Retain: harness.RetainTail}
	spill := false
	if options != nil {
		limits = options.Limits
		spill = options.Spill
	}
	_ = spill // spill file support lands with the temp-file plumbing
	return &execCapture{
		capture:  harness.NewOutputCapture(&harness.ShellOutputCaptureOptions{Limits: limits}, ctx, nil, nil),
		onUpdate: onUpdate,
		ctx:      ctx,
	}
}

func (c *execCapture) push(chunk []byte) { c.capture.Push(string(chunk)) }
func (c *execCapture) finish()           { c.capture.Finish() }
func (c *execCapture) snapshot() harness.ShellOutputView {
	view := c.capture.Snapshot()
	if c.onUpdate != nil {
		c.onUpdate(&harness.ShellOutputReplace{Output: view}, c.ctx)
	}
	return view
}

// sysProcAttr gives the child its own process group so killProcessTree can
// signal the whole tree.
func sysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
