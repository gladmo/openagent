package codingagent

// Tests for the bash executor and stripAnsi (ported from
// bash-executor.test.ts and ansi.test.ts surfaces).

import (
	"os"
	"strings"
	"testing"

	"github.com/gladmo/openagent/abort"
)

func TestStripAnsi(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"\x1b[1;32mbold green\x1b[0m", "bold green"},
		{"\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"a\x07b", "ab"},
		{"a\nb\tc", "a\nb\tc"},
	}
	for _, tc := range cases {
		if got := StripAnsi(tc.in); got != tc.want {
			t.Errorf("StripAnsi(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

type stubExecOperations struct {
	output   []string
	exitCode int
	onData   func(data []byte)
}

func (o *stubExecOperations) Exec(command, cwd string, options BashExecOptions) (BashExecResult, error) {
	for _, chunk := range o.output {
		if options.OnData != nil {
			options.OnData([]byte(chunk))
		}
	}
	code := o.exitCode
	return BashExecResult{ExitCode: &code}, nil
}

func TestExecuteBashBasic(t *testing.T) {
	ops := &stubExecOperations{output: []string{"hello ", "world\n"}, exitCode: 0}
	result, err := ExecuteBashWithOperations("cmd", t.TempDir(), ops, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Output != "hello world\n" {
		t.Fatalf("output = %q", result.Output)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exit = %v", result.ExitCode)
	}
	if result.Truncated || result.FullOutputPath != "" || result.Cancelled {
		t.Fatalf("flags = %+v", result)
	}
}

func TestExecuteBashStreamingChunks(t *testing.T) {
	var chunks []string
	ops := &stubExecOperations{output: []string{"one\n", "two\n"}, exitCode: 0}
	result, err := ExecuteBashWithOperations("cmd", t.TempDir(), ops, &BashExecutorOptions{
		OnChunk: func(chunk string) { chunks = append(chunks, chunk) },
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(chunks) != 2 || chunks[0] != "one\n" {
		t.Fatalf("chunks = %v", chunks)
	}
	if result.Output != "one\ntwo\n" {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestExecuteBashSanitization(t *testing.T) {
	ops := &stubExecOperations{output: []string{"\x1b[31mcolored\x1b[0m\r\ntext\x00"}, exitCode: 0}
	result, err := ExecuteBashWithOperations("cmd", t.TempDir(), ops, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Output != "colored\ntext" {
		t.Fatalf("sanitized = %q", result.Output)
	}
}

func TestExecuteBashTruncationSpill(t *testing.T) {
	// Output beyond DEFAULT_MAX_BYTES spills to a temp file and truncates.
	var outputs []string
	big := strings.Repeat("x", 100) + "\n"
	for i := 0; i < DefaultMaxBytes/100+10; i++ {
		outputs = append(outputs, big)
	}
	ops := &stubExecOperations{output: outputs, exitCode: 0}
	result, err := ExecuteBashWithOperations("cmd", t.TempDir(), ops, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Truncated {
		t.Fatal("not truncated")
	}
	if result.FullOutputPath == "" {
		t.Fatal("no spill file")
	}
	if _, err := os.Stat(result.FullOutputPath); err != nil {
		t.Fatalf("spill: %v", err)
	}
	defer os.Remove(result.FullOutputPath)

	// The spill contains the full output.
	spilled, _ := os.ReadFile(result.FullOutputPath)
	if len(spilled) <= DefaultMaxBytes {
		t.Fatalf("spill = %d bytes", len(spilled))
	}
	// The returned output is the tail within limits.
	if len(result.Output) > DefaultMaxBytes {
		t.Fatalf("output = %d", len(result.Output))
	}
}

func TestExecuteBashCancellation(t *testing.T) {
	controller := abort.NewController()
	controller.Abort()

	ops := &execCancelOps{controller: controller}
	result, err := ExecuteBashWithOperations("sleep", t.TempDir(), ops, &BashExecutorOptions{
		Signal: controller.Signal(),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Cancelled {
		t.Fatal("not cancelled")
	}
	if result.ExitCode != nil {
		t.Fatalf("exit = %v", *result.ExitCode)
	}
}

type execCancelOps struct {
	controller *abort.Controller
}

func (o *execCancelOps) Exec(command, cwd string, options BashExecOptions) (BashExecResult, error) {
	// Simulate an aborted process.
	return BashExecResult{}, &abortError{"aborted"}
}

func TestExecuteBashSplitRunes(t *testing.T) {
	// A multi-byte rune split across chunks decodes once complete.
	ops := &stubExecOperations{output: []string{"h\xc3", "\xa9llo"}, exitCode: 0}
	result, err := ExecuteBashWithOperations("cmd", t.TempDir(), ops, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Output != "héllo" {
		t.Fatalf("output = %q", result.Output)
	}
}
