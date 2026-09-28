package pico3

// Ports of pico3 bash.ts behaviors.

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gladmo/openagent/abort"
	chordcontext "github.com/gladmo/openagent/chord/context"
)

type recordingAPI struct {
	mu     sync.Mutex
	stream []byte
}

func (a *recordingAPI) Stream(chunk []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stream = append(a.stream, chunk...)
}
func (a *recordingAPI) Progress(string)        {}
func (a *recordingAPI) Memo(string, any) error { return nil }

func (a *recordingAPI) output() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return string(a.stream)
}

func TestBashToolStreamsOutput(t *testing.T) {
	tool := BashTool(OutputPolicy{})
	api := &recordingAPI{}
	result, err := tool.Execute(map[string]any{"command": "echo hello-pico"}, api, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("echo failed")
	}
	if !strings.Contains(api.output(), "hello-pico") {
		t.Fatalf("streamed = %q", api.output())
	}
	if result.Details == nil || result.Details.ExitCode == nil || *result.Details.ExitCode != 0 {
		t.Fatalf("details = %+v", result.Details)
	}
	if result.Details.MS < 0 {
		t.Fatal("ms negative")
	}
}

func TestBashToolNonZeroExit(t *testing.T) {
	tool := BashTool(OutputPolicy{})
	result, err := tool.Execute(map[string]any{"command": "exit 5"}, &recordingAPI{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("exit 5 not error")
	}
	if result.Details.ExitCode == nil || *result.Details.ExitCode != 5 {
		t.Fatalf("exit = %v", result.Details.ExitCode)
	}
}

func TestBashToolCwd(t *testing.T) {
	tool := BashTool(OutputPolicy{})
	api := &recordingAPI{}
	result, err := tool.Execute(map[string]any{"command": "pwd", "cwd": "/tmp"}, api, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("pwd failed")
	}
	if !strings.Contains(api.output(), "/tmp") {
		t.Fatalf("pwd = %q", api.output())
	}
}

func TestBashToolAbortKills(t *testing.T) {
	tool := BashTool(OutputPolicy{})
	controller := abort.NewController()
	ctx := chordcontext.WithAbortSignal(controller.Signal(), chordcontext.BackgroundContext)

	done := make(chan *ToolResult, 1)
	go func() {
		result, err := tool.Execute(map[string]any{"command": "sleep 30"}, &recordingAPI{}, ctx)
		if err != nil {
			t.Error(err)
		}
		done <- result
	}()
	// Give the child a moment to start, then abort.
	time.Sleep(150 * time.Millisecond)
	controller.Abort()
	select {
	case result := <-done:
		if !result.IsError {
			t.Fatal("aborted sleep reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abort did not kill the child")
	}
}

func TestBashToolDeclaration(t *testing.T) {
	tool := BashTool(OutputPolicy{MaxLines: int64Ptr(10)})
	if tool.Name != "bash" || tool.Description != "Run a shell command" {
		t.Fatalf("tool = %+v", tool)
	}
	if tool.Replay != "unsafe" {
		t.Fatalf("replay = %s", tool.Replay)
	}
	if tool.Output.MaxLines == nil || *tool.Output.MaxLines != 10 {
		t.Fatal("output bounds lost")
	}
	// Parameters: command required, cwd optional.
	if tool.Parameters == nil || tool.Parameters.Type != "object" {
		t.Fatal("parameters not an object")
	}
	hasCommand := false
	for _, name := range tool.Parameters.Required {
		if name == "command" {
			hasCommand = true
		}
	}
	if !hasCommand {
		t.Fatalf("required = %v", tool.Parameters.Required)
	}
	if _, hasCwd := tool.Parameters.Properties.Get("cwd"); !hasCwd {
		t.Fatal("cwd missing")
	}
}

func int64Ptr(v int64) *int64 { return &v }
