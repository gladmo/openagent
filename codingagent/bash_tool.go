package codingagent

// Port of core/tools/bash.ts: shared shell tool execution (bash and
// powershell) with streamed output, throttled updates, timeout/abort
// handling, and temp-file preservation of truncated output.

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/typebox"
)

const maxTimeoutMs = 2_147_483_647
const maxTimeoutSeconds = maxTimeoutMs / 1000

func resolveTimeoutMs(timeout *float64) (int, error) {
	if timeout == nil {
		return 0, nil
	}
	if *timeout <= 0 {
		return 0, fmt.Errorf("Invalid timeout: must be a finite number of seconds")
	}
	timeoutMs := int(*timeout * 1000)
	if timeoutMs > maxTimeoutMs {
		return 0, fmt.Errorf("Invalid timeout: maximum is %d seconds", maxTimeoutSeconds)
	}
	return timeoutMs, nil
}

// BashToolSystemPromptContribution is the bash tool's prompt section.
var BashToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet:    "Execute bash commands (ls, grep, find, etc.)",
	Guidelines: []string{"You can inspect PI_* environment variables for current model and session details."},
}

// BashToolInput is the tool's validated input.
type BashToolInput struct {
	Command string   `json:"command"`
	Timeout *float64 `json:"timeout,omitempty"`
}

// BashToolDetails carries truncation metadata and the spill-file path.
type BashToolDetails struct {
	Truncation     *TruncationResult
	FullOutputPath string
}

// BashExecOptions carry the streaming callbacks and limits.
type BashExecOptions struct {
	OnData  func(data []byte)
	Signal  *abort.Signal
	Timeout *float64
	Env     []string
}

// BashExecResult reports the exit code; nil means termination without one.
type BashExecResult struct {
	ExitCode *int
}

// BashOperations execute commands; overrides delegate to remote systems.
type BashOperations interface {
	Exec(command, cwd string, options BashExecOptions) (BashExecResult, error)
}

// localShellOperations runs commands through the resolved shell.
type localShellOperations struct {
	shellName          string
	resolveShellConfig func() (ShellConfig, error)
}

// CreateLocalShellOperations builds the shared local shell backend.
func CreateLocalShellOperations(shellName string, resolveShellConfig func() (ShellConfig, error)) BashOperations {
	return &localShellOperations{shellName: shellName, resolveShellConfig: resolveShellConfig}
}

// CreateLocalBashOperations builds pi's standard local bash backend.
func CreateLocalBashOperations(shellPath string) BashOperations {
	return CreateLocalShellOperations("bash", func() (ShellConfig, error) {
		return GetShellConfig(shellPath)
	})
}

func (o *localShellOperations) Exec(command, cwd string, options BashExecOptions) (BashExecResult, error) {
	timeoutMs, err := resolveTimeoutMs(options.Timeout)
	if err != nil {
		return BashExecResult{}, err
	}
	if options.Signal != nil && options.Signal.Aborted() {
		return BashExecResult{}, fmt.Errorf("aborted")
	}
	shellConfig, err := o.resolveShellConfig()
	if err != nil {
		return BashExecResult{}, err
	}
	if _, err := os.Stat(cwd); err != nil {
		return BashExecResult{}, fmt.Errorf("Working directory does not exist: %s\nCannot execute %s commands.", cwd, o.shellName)
	}

	commandFromStdin := shellConfig.CommandTransport == "stdin"
	args := shellConfig.Args
	if !commandFromStdin {
		args = append(append([]string(nil), shellConfig.Args...), command)
	}

	env := options.Env
	if env == nil {
		env = GetShellEnv()
	}

	cmd := exec.Command(shellConfig.Shell, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.SysProcAttr = detachedSysProcAttr()
	var stdinWriter *os.File
	if commandFromStdin {
		stdinReader, writer, pipeErr := os.Pipe()
		if pipeErr != nil {
			return BashExecResult{}, pipeErr
		}
		cmd.Stdin = stdinReader
		stdinWriter = writer
	} else {
		cmd.Stdin = nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return BashExecResult{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return BashExecResult{}, err
	}

	if err := cmd.Start(); err != nil {
		return BashExecResult{}, fmt.Errorf("Failed to run %s: %s", o.shellName, err.Error())
	}
	if stdinWriter != nil {
		go func() {
			_, _ = stdinWriter.Write([]byte(command))
			stdinWriter.Close()
		}()
	}

	if cmd.Process != nil {
		TrackDetachedChildPid(cmd.Process.Pid)
	}

	var timedOut atomicBool
	var timeoutTimer *time.Timer
	stopTimeout := func() {
		if timeoutTimer != nil {
			timeoutTimer.Stop()
			timeoutTimer = nil
		}
	}

	var outputWG sync.WaitGroup
	outputWG.Add(2)
	stream := func(reader *os.File) {
		defer outputWG.Done()
		buffer := make([]byte, 32*1024)
		for {
			n, readErr := reader.Read(buffer)
			if n > 0 && options.OnData != nil {
				chunk := make([]byte, n)
				copy(chunk, buffer[:n])
				options.OnData(chunk)
			}
			if readErr != nil {
				return
			}
		}
	}
	go stream(stdout.(*os.File))
	go stream(stderr.(*os.File))

	var removeAbort func()
	if options.Signal != nil {
		onAbort := func() {
			if cmd.Process != nil {
				KillProcessTree(cmd.Process.Pid)
			}
		}
		if options.Signal.Aborted() {
			onAbort()
		} else {
			removeAbort = options.Signal.OnAbort(onAbort)
		}
	}

	if timeoutMs > 0 {
		timeoutTimer = time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
			timedOut.Store(true)
			if cmd.Process != nil {
				KillProcessTree(cmd.Process.Pid)
			}
		})
	}

	outputWG.Wait()
	waitErr := cmd.Wait()
	stopTimeout()
	if removeAbort != nil {
		removeAbort()
	}
	if cmd.Process != nil {
		UntrackDetachedChildPid(cmd.Process.Pid)
	}

	if options.Signal != nil && options.Signal.Aborted() {
		return BashExecResult{}, fmt.Errorf("aborted")
	}
	if timedOut.Load() {
		return BashExecResult{}, fmt.Errorf("timeout:%s", formatTimeoutSeconds(options.Timeout))
	}

	exitCode := 1
	if waitErr == nil {
		zero := 0
		exitCode = zero
	} else if exitErr, ok := waitErr.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			// Signal-killed shells use the 128+signal convention.
			exitCode = 128 + int(status.Signal())
		}
	} else {
		return BashExecResult{}, fmt.Errorf("Failed to run %s: %s", o.shellName, waitErr.Error())
	}
	return BashExecResult{ExitCode: &exitCode}, nil
}

func formatTimeoutSeconds(timeout *float64) string {
	if timeout == nil {
		return ""
	}
	return strconv.FormatFloat(*timeout, 'g', -1, 64)
}

// BashSpawnContext is the resolved command/cwd/env triple.
type BashSpawnContext struct {
	Command string
	Cwd     string
	Env     map[string]string
}

// BashSpawnHook adjusts the spawn context before execution.
type BashSpawnHook func(context BashSpawnContext) BashSpawnContext

// sessionEnvironmentSource supplies PI_* session metadata (the full
// ExtensionContext surface lands with the extensions phase).
type sessionEnvironmentSource struct {
	SessionID     string
	SessionFile   string
	Provider      string
	ModelID       string
	ThinkingLevel string
}

func resolveSpawnContext(command, cwd string, spawnHook BashSpawnHook, exposeSessionEnvironment bool, session sessionEnvironmentSource) BashSpawnContext {
	env := shellEnvMap(GetShellEnv())
	delete(env, "PI_SESSION_ID")
	delete(env, "PI_SESSION_FILE")
	delete(env, "PI_PROVIDER")
	delete(env, "PI_MODEL")
	delete(env, "PI_REASONING_LEVEL")
	if exposeSessionEnvironment {
		if session.SessionID != "" {
			env["PI_SESSION_ID"] = session.SessionID
		}
		if session.SessionFile != "" {
			env["PI_SESSION_FILE"] = session.SessionFile
		}
		if session.Provider != "" {
			env["PI_PROVIDER"] = session.Provider
		}
		if session.ModelID != "" {
			env["PI_MODEL"] = session.ModelID
		}
		if session.ThinkingLevel != "" {
			env["PI_REASONING_LEVEL"] = session.ThinkingLevel
		}
	}
	baseContext := BashSpawnContext{Command: command, Cwd: cwd, Env: env}
	if spawnHook != nil {
		return spawnHook(baseContext)
	}
	return baseContext
}

func shellEnvMap(env []string) map[string]string {
	out := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		out[key] = value
	}
	return out
}

func envMapToSlice(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sortStrings(keys)
	out := make([]string, 0, len(env))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// BashToolOptions configure the shell tool.
type BashToolOptions struct {
	Operations BashOperations
	// CommandPrefix prepends setup commands to every command.
	CommandPrefix string
	// ShellPath overrides the shell from settings.
	ShellPath string
	// ExposeSessionEnvironment sets PI_* variables (default true).
	ExposeSessionEnvironment *bool
	// SpawnHook adjusts command/cwd/env before execution.
	SpawnHook BashSpawnHook
}

// ShellToolConfig parameterizes the shared shell tool.
type ShellToolConfig struct {
	Name             string
	Label            string
	ShellName        string
	Prompt           string
	PromptSnippet    string
	PromptGuidelines []string
	TempFilePrefix   string
}

// bashRenderState carries the execution clock for renderers.
type bashRenderState struct {
	StartedAt int64
	EndedAt   int64
}

// BashToolOutput is the bash result shape.
type BashToolOutput struct {
	Text    string
	Details *BashToolDetails
}

const bashUpdateThrottleMs = 100

// CreateShellToolDefinition builds a shell tool from the config.
func CreateShellToolDefinition(cwd string, config ShellToolConfig, options *BashToolOptions) *ToolDefinition {
	var ops BashOperations
	var commandPrefix string
	exposeSessionEnvironment := true
	var spawnHook BashSpawnHook
	if options != nil {
		if options.Operations != nil {
			ops = options.Operations
		}
		commandPrefix = options.CommandPrefix
		if options.ExposeSessionEnvironment != nil {
			exposeSessionEnvironment = *options.ExposeSessionEnvironment
		}
		spawnHook = options.SpawnHook
	}
	if ops == nil {
		ops = CreateLocalBashOperations(shellPathFrom(options))
	}

	return &ToolDefinition{
		Name:  config.Name,
		Label: config.Label,
		Description: fmt.Sprintf(
			"Execute a %s command in the current working directory. Returns stdout and stderr. Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.",
			config.ShellName, DefaultMaxLines, DefaultMaxBytes/1024),
		PromptSnippet:    config.PromptSnippet,
		PromptGuidelines: shellPromptGuidelines(config, exposeSessionEnvironment),
		Parameters: typebox.Object([]*typebox.Property{
			typebox.Prop("command", typebox.String(typebox.Description("Shell command to execute"))),
			typebox.Prop("timeout", typebox.Optional(typebox.Number(typebox.Description("Timeout in seconds (optional, no default timeout)")))),
		}),
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
		Execute: func(toolCallID string, params any, signal *abort.Signal, onUpdate func(partial any), ctx *ExtensionContext) (any, error) {
			input, ok := params.(BashToolInput)
			if !ok {
				mapped, mappedOk := mapBashToolInput(params)
				if !mappedOk {
					return nil, fmt.Errorf("%s: invalid params %T", config.Name, params)
				}
				input = mapped
			}

			resolvedCommand := input.Command
			if commandPrefix != "" {
				resolvedCommand = commandPrefix + "\n" + input.Command
			}
			base := cwd
			if ctx != nil && ctx.Cwd != "" {
				base = ctx.Cwd
			}
			spawnContext := resolveSpawnContext(resolvedCommand, base, spawnHook, exposeSessionEnvironment, sessionEnvironmentSource{})

			output := NewOutputAccumulator(&OutputAccumulatorOptions{TempFilePrefix: config.TempFilePrefix})
			var acceptingOutput = true

			var updateTimer *time.Timer
			updateDirty := false
			lastUpdateAt := time.Time{}

			stopTimer := func() {
				if updateTimer != nil {
					updateTimer.Stop()
					updateTimer = nil
				}
			}

			emitOutputUpdate := func() {
				if onUpdate == nil || !updateDirty {
					return
				}
				updateDirty = false
				lastUpdateAt = time.Now()
				snapshot := output.Snapshot(true)
				var details *BashToolDetails
				if snapshot.Truncation.Truncated {
					details = &BashToolDetails{Truncation: &snapshot.Truncation, FullOutputPath: snapshot.FullOutputPath}
				}
				_ = details
			}

			scheduleOutputUpdate := func() {
				if onUpdate == nil {
					return
				}
				updateDirty = true
				delay := time.Duration(bashUpdateThrottleMs)*time.Millisecond - time.Since(lastUpdateAt)
				if delay <= 0 {
					stopTimer()
					emitOutputUpdate()
					return
				}
				if updateTimer == nil {
					updateTimer = time.AfterFunc(delay, func() {
						updateTimer = nil
						emitOutputUpdate()
					})
				}
			}

			if onUpdate != nil {
				onUpdate(nil)
			}

			handleData := func(data []byte) {
				if !acceptingOutput {
					return
				}
				output.Append(data)
				scheduleOutputUpdate()
			}

			finishOutput := func() OutputSnapshot {
				acceptingOutput = false
				output.Finish()
				stopTimer()
				emitOutputUpdate()
				snapshot := output.Snapshot(true)
				_ = output.CloseTempFile()
				return snapshot
			}

			formatOutput := func(snapshot OutputSnapshot, emptyText string) (string, *BashToolDetails) {
				truncation := snapshot.Truncation
				text := snapshot.Content
				if text == "" {
					text = emptyText
				}
				var details *BashToolDetails
				if truncation.Truncated {
					details = &BashToolDetails{Truncation: &truncation, FullOutputPath: snapshot.FullOutputPath}
					startLine := truncation.TotalLines - truncation.OutputLines + 1
					endLine := truncation.TotalLines
					switch {
					case truncation.LastLinePartial:
						lastLineSize := FormatSize(int64(output.GetLastLineBytes()))
						text += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s). Full output: %s]",
							FormatSize(int64(truncation.OutputBytes)), endLine, lastLineSize, snapshot.FullOutputPath)
					case truncation.TruncatedBy == "lines":
						text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Full output: %s]",
							startLine, endLine, truncation.TotalLines, snapshot.FullOutputPath)
					default:
						text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
							startLine, endLine, truncation.TotalLines, FormatSize(DefaultMaxBytes), snapshot.FullOutputPath)
					}
				}
				return text, details
			}

			appendStatus := func(text, status string) string {
				if text != "" {
					return text + "\n\n" + status
				}
				return status
			}

			defer stopTimer()

			result, execErr := ops.Exec(spawnContext.Command, spawnContext.Cwd, BashExecOptions{
				OnData:  handleData,
				Signal:  signal,
				Timeout: input.Timeout,
				Env:     envMapToSlice(spawnContext.Env),
			})
			if execErr != nil {
				snapshot := finishOutput()
				text, _ := formatOutput(snapshot, "")
				message := execErr.Error()
				if message == "aborted" {
					return nil, fmt.Errorf("%s", appendStatus(text, "Command aborted"))
				}
				if strings.HasPrefix(message, "timeout:") {
					timeoutSecs := strings.SplitN(message, ":", 2)[1]
					return nil, fmt.Errorf("%s", appendStatus(text, "Command timed out after "+timeoutSecs+" seconds"))
				}
				return nil, execErr
			}

			snapshot := finishOutput()
			outputText, details := formatOutput(snapshot, "(no output)")
			if result.ExitCode == nil {
				return nil, fmt.Errorf("%s", appendStatus(outputText, "Command terminated without an exit code"))
			}
			if *result.ExitCode != 0 {
				return nil, fmt.Errorf("%s", appendStatus(outputText, fmt.Sprintf("Command exited with code %d", *result.ExitCode)))
			}
			return &BashToolOutput{Text: outputText, Details: details}, nil
		},
	}
}

func shellPromptGuidelines(config ShellToolConfig, exposeSessionEnvironment bool) []string {
	if exposeSessionEnvironment && config.PromptGuidelines != nil {
		return append([]string(nil), config.PromptGuidelines...)
	}
	return nil
}

func shellPathFrom(options *BashToolOptions) string {
	if options == nil {
		return ""
	}
	return options.ShellPath
}

var bashToolConfig = ShellToolConfig{
	Name:             "bash",
	Label:            "bash",
	ShellName:        "bash",
	Prompt:           "$",
	PromptSnippet:    BashToolSystemPromptContribution.Snippet,
	PromptGuidelines: append([]string(nil), BashToolSystemPromptContribution.Guidelines...),
	TempFilePrefix:   "pi-bash",
}

// CreateBashToolDefinition builds the bash tool.
func CreateBashToolDefinition(cwd string, options *BashToolOptions) *ToolDefinition {
	return CreateShellToolDefinition(cwd, bashToolConfig, options)
}

// CreateBashTool wraps the bash definition into an AgentTool.
func CreateBashTool(cwd string, options *BashToolOptions) any {
	return WrapToolDefinition(CreateBashToolDefinition(cwd, options), nil)
}

func mapBashToolInput(params any) (BashToolInput, bool) {
	switch typed := params.(type) {
	case BashToolInput:
		return typed, true
	case map[string]any:
		input := BashToolInput{}
		if command, ok := typed["command"].(string); ok {
			input.Command = command
		}
		if timeout, ok := toFloat(typed["timeout"]); ok {
			value := timeout
			input.Timeout = &value
		}
		return input, true
	}
	return BashToolInput{}, false
}

// atomicBool wraps the race-detector-clean flag for timeout termination.
type atomicBool struct{ value atomic.Bool }

func (b *atomicBool) Store(v bool) { b.value.Store(v) }
func (b *atomicBool) Load() bool   { return b.value.Load() }
