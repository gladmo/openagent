package codingagent

// Tests for the bash tool and output accumulator (ported from
// tools/bash.test.ts and output-accumulator.test.ts surfaces).

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/abort"
)

func TestOutputAccumulatorBasic(t *testing.T) {
	accumulator := NewOutputAccumulator(nil)

	accumulator.Append([]byte("hello\n"))
	accumulator.Append([]byte("world\n"))
	accumulator.Finish()

	snapshot := accumulator.Snapshot(false)
	// Untruncated snapshots keep the content verbatim, trailing newline
	// included (the truncator only trims within truncation paths).
	if snapshot.Content != "hello\nworld\n" {
		t.Fatalf("content = %q", snapshot.Content)
	}
	if snapshot.Truncation.Truncated {
		t.Fatalf("truncation = %+v", snapshot.Truncation)
	}
	if snapshot.FullOutputPath != "" {
		t.Fatalf("spill path = %q", snapshot.FullOutputPath)
	}
}

func TestOutputAccumulatorStreamingSplitRunes(t *testing.T) {
	accumulator := NewOutputAccumulator(nil)

	// Split a multi-byte rune across chunks.
	text := "héllo"
	accumulator.Append([]byte(text[:2]))
	accumulator.Append([]byte(text[2:]))
	accumulator.Finish()

	snapshot := accumulator.Snapshot(false)
	if snapshot.Content != "héllo" {
		t.Fatalf("content = %q", snapshot.Content)
	}
}

func TestOutputAccumulatorTruncationSpill(t *testing.T) {
	smallBytes := 20
	accumulator := NewOutputAccumulator(&OutputAccumulatorOptions{MaxBytes: &smallBytes})

	for i := 0; i < 10; i++ {
		accumulator.Append([]byte(strings.Repeat("x", 10) + "\n"))
	}
	accumulator.Finish()

	snapshot := accumulator.Snapshot(true)
	if !snapshot.Truncation.Truncated {
		t.Fatalf("truncation = %+v", snapshot.Truncation)
	}
	if snapshot.FullOutputPath == "" {
		t.Fatal("spill path missing")
	}
	if err := accumulator.CloseTempFile(); err != nil {
		t.Fatalf("close: %v", err)
	}
	spilled, err := os.ReadFile(snapshot.FullOutputPath)
	if err != nil {
		t.Fatalf("read spill: %v", err)
	}
	if len(spilled) != 110 {
		t.Fatalf("spilled = %d bytes, want 110", len(spilled))
	}
	defer os.Remove(snapshot.FullOutputPath)

	// Content keeps only the tail within limits.
	if snapshot.Truncation.OutputBytes > smallBytes {
		t.Fatalf("output bytes = %d > %d", snapshot.Truncation.OutputBytes, smallBytes)
	}
}

func TestOutputAccumulatorRollingTail(t *testing.T) {
	smallBytes := 50
	rolling := NewOutputAccumulator(&OutputAccumulatorOptions{MaxBytes: &smallBytes})

	var total strings.Builder
	for i := 0; i < 100; i++ {
		chunk := strings.Repeat("a", 10) + "\n"
		total.WriteString(chunk)
		rolling.Append([]byte(chunk))
	}
	rolling.Finish()

	snapshot := rolling.Snapshot(false)
	if !snapshot.Truncation.Truncated {
		t.Fatal("expected truncation")
	}
	if snapshot.Truncation.TotalBytes != total.Len() {
		t.Fatalf("total = %d, want %d", snapshot.Truncation.TotalBytes, total.Len())
	}
	// The tail is bounded despite the full output size.
	if len(snapshot.Content) > 4*smallBytes {
		t.Fatalf("tail too large: %d", len(snapshot.Content))
	}
}

func TestOutputAccumulatorFinishThenAppendPanics(t *testing.T) {
	accumulator := NewOutputAccumulator(nil)
	accumulator.Finish()
	defer func() {
		if recover() == nil {
			t.Fatal("append after finish should panic")
		}
	}()
	accumulator.Append([]byte("x"))
}

func TestOutputAccumulatorLastLineBytes(t *testing.T) {
	accumulator := NewOutputAccumulator(nil)
	accumulator.Append([]byte("first\nopen line"))
	if got := accumulator.GetLastLineBytes(); got != len("open line") {
		t.Fatalf("last line = %d", got)
	}
	accumulator.Finish()
	if got := accumulator.GetLastLineBytes(); got != len("open line") {
		t.Fatalf("after finish = %d", got)
	}
}

func TestBashToolBasic(t *testing.T) {
	dir := t.TempDir()
	definition := CreateBashToolDefinition(dir, nil)

	result, err := definition.Execute("t", BashToolInput{Command: "echo hello"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*BashToolOutput)
	if strings.TrimSpace(output.Text) != "hello" {
		t.Fatalf("text = %q", output.Text)
	}
}

func TestBashToolExitCodeError(t *testing.T) {
	dir := t.TempDir()
	definition := CreateBashToolDefinition(dir, nil)

	_, err := definition.Execute("t", BashToolInput{Command: "echo oops >&2; exit 3"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Command exited with code 3") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "oops") {
		t.Fatalf("stderr missing: %v", err)
	}
}

func TestBashToolNoOutput(t *testing.T) {
	dir := t.TempDir()
	definition := CreateBashToolDefinition(dir, nil)

	result, err := definition.Execute("t", BashToolInput{Command: "true"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*BashToolOutput)
	if output.Text != "(no output)" {
		t.Fatalf("text = %q", output.Text)
	}
}

func TestBashToolTimeout(t *testing.T) {
	dir := t.TempDir()
	definition := CreateBashToolDefinition(dir, nil)

	timeout := 1.0
	_, err := definition.Execute("t", BashToolInput{Command: "sleep 5", Timeout: &timeout}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Command timed out after 1 seconds") {
		t.Fatalf("err = %v", err)
	}
}

func TestBashToolAbort(t *testing.T) {
	dir := t.TempDir()
	definition := CreateBashToolDefinition(dir, nil)

	controller := abort.NewController()
	go func() {
		time.Sleep(150 * time.Millisecond)
		controller.Abort()
	}()
	_, err := definition.Execute("t", BashToolInput{Command: "sleep 5"}, controller.Signal(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Command aborted") {
		t.Fatalf("err = %v", err)
	}
}

func TestBashToolInvalidTimeout(t *testing.T) {
	dir := t.TempDir()
	definition := CreateBashToolDefinition(dir, nil)

	negative := -1.0
	if _, err := definition.Execute("t", BashToolInput{Command: "true", Timeout: &negative}, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "Invalid timeout") {
		t.Fatalf("err = %v", err)
	}
}

func TestBashToolCwd(t *testing.T) {
	dir := t.TempDir()
	definition := CreateBashToolDefinition(t.TempDir(), nil)

	result, err := definition.Execute("t", BashToolInput{Command: "pwd"}, nil, nil, &ExtensionContext{Cwd: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*BashToolOutput)
	gotDir := strings.TrimSpace(output.Text)
	// macOS resolves /var -> /private/var; compare canonically.
	wantDir := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		wantDir = resolved
	}
	if resolved, err := filepath.EvalSymlinks(gotDir); err == nil {
		gotDir = resolved
	}
	if gotDir != wantDir {
		t.Fatalf("pwd = %q, want %q", gotDir, wantDir)
	}
}

func TestBashToolMissingCwd(t *testing.T) {
	definition := CreateBashToolDefinition("/nonexistent/dir", nil)
	_, err := definition.Execute("t", BashToolInput{Command: "true"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Working directory does not exist") {
		t.Fatalf("err = %v", err)
	}
}

func TestBashToolCommandPrefix(t *testing.T) {
	dir := t.TempDir()
	definition := CreateBashToolDefinition(dir, &BashToolOptions{CommandPrefix: "export PREFIXED=1"})

	result, err := definition.Execute("t", BashToolInput{Command: "echo $PREFIXED"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*BashToolOutput)
	if strings.TrimSpace(output.Text) != "1" {
		t.Fatalf("prefix = %q", output.Text)
	}
}

func TestBashToolTruncationSpill(t *testing.T) {
	dir := t.TempDir()
	definition := CreateBashToolDefinition(dir, nil)

	var lines []string
	for i := 0; i < DefaultMaxLines+100; i++ {
		lines = append(lines, strings.Repeat("y", 30))
	}
	command := "for i in $(seq 1 " + itoaGo(DefaultMaxLines+100) + "); do echo " + strings.Repeat("y", 30) + "; done"
	_ = lines

	result, err := definition.Execute("t", BashToolInput{Command: command}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*BashToolOutput)
	if !strings.Contains(output.Text, "Full output: ") {
		t.Fatalf("spill notice = %q", strings.TrimSpace(output.Text[len(output.Text)-200:]))
	}
	if output.Details == nil || output.Details.FullOutputPath == "" {
		t.Fatalf("details = %+v", output.Details)
	}
	if _, err := os.Stat(output.Details.FullOutputPath); err != nil {
		t.Fatalf("spill file: %v", err)
	}
	defer os.Remove(output.Details.FullOutputPath)
}

func TestBashToolSpawnHook(t *testing.T) {
	dir := t.TempDir()
	hooked := CreateBashToolDefinition(dir, &BashToolOptions{
		SpawnHook: func(context BashSpawnContext) BashSpawnContext {
			context.Command = "echo hooked"
			return context
		},
	})

	result, err := hooked.Execute("t", BashToolInput{Command: "echo original"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*BashToolOutput)
	if strings.TrimSpace(output.Text) != "hooked" {
		t.Fatalf("hooked = %q", output.Text)
	}
}

func TestBashToolSessionEnvironment(t *testing.T) {
	dir := t.TempDir()
	expose := false
	definition := CreateBashToolDefinition(dir, &BashToolOptions{ExposeSessionEnvironment: &expose})

	result, err := definition.Execute("t", BashToolInput{Command: "echo $PI_SESSION_ID"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*BashToolOutput)
	if strings.TrimSpace(output.Text) != "" {
		t.Fatalf("session env leaked: %q", output.Text)
	}
}

func TestResolveSpawnContextEnv(t *testing.T) {
	context := resolveSpawnContext("cmd", "/tmp", nil, true, sessionEnvironmentSource{
		SessionID: "s1",
		Provider:  "anthropic",
		ModelID:   "m1",
	})
	if context.Env["PI_SESSION_ID"] != "s1" || context.Env["PI_PROVIDER"] != "anthropic" || context.Env["PI_MODEL"] != "m1" {
		t.Fatalf("env = %v", context.Env)
	}
	if _, has := context.Env["PI_SESSION_FILE"]; has {
		t.Fatal("empty session file should be omitted")
	}
}

func TestResolveTimeoutMs(t *testing.T) {
	value := 2.5
	if ms, err := resolveTimeoutMs(&value); err != nil || ms != 2500 {
		t.Fatalf("ms = %d err=%v", ms, err)
	}
	if _, err := resolveTimeoutMs(nil); err != nil {
		t.Fatalf("nil err = %v", err)
	}
	zero := 0.0
	if _, err := resolveTimeoutMs(&zero); err == nil {
		t.Fatal("zero timeout should error")
	}
}

func TestBashToolStdinTransportShell(t *testing.T) {
	// The legacy-WSL stdin transport writes the command via stdin.
	config := ShellConfig{Shell: "/bin/bash", Args: []string{"-c"}}
	ops := CreateLocalShellOperations("bash", func() (ShellConfig, error) { return config, nil })
	stdinOps := CreateLocalShellOperations("bash", func() (ShellConfig, error) {
		return ShellConfig{Shell: "/bin/bash", Args: []string{"-s"}, CommandTransport: "stdin"}, nil
	})

	var output strings.Builder
	result, err := ops.Exec("echo argv", t.TempDir(), BashExecOptions{OnData: func(data []byte) { output.Write(data) }})
	if err != nil {
		t.Fatalf("argv exec: %v", err)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("argv exit = %+v", result)
	}
	if strings.TrimSpace(output.String()) != "argv" {
		t.Fatalf("argv output = %q", output.String())
	}

	output.Reset()
	result, err = stdinOps.Exec("echo stdin", t.TempDir(), BashExecOptions{OnData: func(data []byte) { output.Write(data) }})
	if err != nil {
		t.Fatalf("stdin exec: %v", err)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("stdin exit = %+v", result)
	}
	if strings.TrimSpace(output.String()) != "stdin" {
		t.Fatalf("stdin output = %q", output.String())
	}
}

func TestBashToolCustomOperations(t *testing.T) {
	dir := t.TempDir()
	ops := &stubBashOps{exitCode: 0}
	definition := CreateBashToolDefinition(dir, &BashToolOptions{Operations: ops})

	if _, err := definition.Execute("t", BashToolInput{Command: "anything"}, nil, nil, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !ops.execCalled {
		t.Fatal("custom exec not invoked")
	}
	if ops.lastCommand != "anything" || ops.lastCwd != dir {
		t.Fatalf("exec args = %q %q", ops.lastCommand, ops.lastCwd)
	}
}

type stubBashOps struct {
	execCalled  bool
	lastCommand string
	lastCwd     string
	exitCode    int
}

func (o *stubBashOps) Exec(command, cwd string, options BashExecOptions) (BashExecResult, error) {
	o.execCalled = true
	o.lastCommand = command
	o.lastCwd = cwd
	code := o.exitCode
	if options.OnData != nil {
		options.OnData([]byte("stubbed\n"))
	}
	return BashExecResult{ExitCode: &code}, nil
}

func TestBashToolKilledProcessTree(t *testing.T) {
	// A child surviving its parent's exit is killed with the tree.
	dir := t.TempDir()
	definition := CreateBashToolDefinition(dir, nil)

	result, err := definition.Execute("t", BashToolInput{Command: "(sleep 10 &) ; echo done"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*BashToolOutput)
	if strings.TrimSpace(output.Text) != "done" {
		t.Fatalf("text = %q", output.Text)
	}
}

func TestBashToolStreamingUpdates(t *testing.T) {
	dir := t.TempDir()
	definition := CreateBashToolDefinition(dir, nil)

	updateCount := 0
	result, err := definition.Execute("t", BashToolInput{Command: "echo one; echo two"}, nil, func(partial any) {
		updateCount++
	}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	_ = result
	if updateCount == 0 {
		t.Fatal("no updates emitted")
	}
}

func itoaGo(n int) string { return strconv.Itoa(n) }
