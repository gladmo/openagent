package codingagent

// Tests for the extension runner (adapted from runner.test.ts surfaces to
// the Go-native extension model).

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func newTestExtension(path string) *Extension {
	return NewExtension(path, path, CreateSyntheticSourceInfo(path, "local", ScopeUser, OriginTopLevel, ""))
}

func TestExtensionRunnerEmit(t *testing.T) {
	extension := newTestExtension("/ext/a")
	var mu sync.Mutex
	received := []string{}
	extension.On("session_start", func(event any) error {
		mu.Lock()
		received = append(received, "a")
		mu.Unlock()
		return nil
	})

	other := newTestExtension("/ext/b")
	other.On("session_start", func(event any) error {
		mu.Lock()
		received = append(received, "b")
		mu.Unlock()
		return nil
	})

	runner := NewExtensionRunner([]*Extension{extension, other}, "/cwd")
	if !runner.HasHandlers("session_start") {
		t.Fatal("handlers not detected")
	}
	if runner.HasHandlers("unsubscribed") {
		t.Fatal("phantom handlers")
	}

	runner.Emit("session_start", nil)
	mu.Lock()
	count := len(received)
	mu.Unlock()
	if count != 2 {
		t.Fatalf("received = %d", count)
	}

	// No handlers: emit is a no-op.
	runner.Emit("nobody-home", nil)
}

func TestExtensionRunnerErrorReporting(t *testing.T) {
	extension := newTestExtension("/ext/err")
	extension.On("session_start", func(event any) error {
		return errors.New("handler failure")
	})

	runner := NewExtensionRunner([]*Extension{extension}, "/cwd")
	var mu sync.Mutex
	reported := []*ExtensionError{}
	runner.OnError(func(report *ExtensionError) {
		mu.Lock()
		reported = append(reported, report)
		mu.Unlock()
	})

	runner.Emit("session_start", nil)
	mu.Lock()
	count := len(reported)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("reported = %d", count)
	}
	mu.Lock()
	report := reported[0]
	mu.Unlock()
	if report.ExtensionPath != "/ext/err" || report.Event != "session_start" || !strings.Contains(report.Error, "handler failure") {
		t.Fatalf("report = %+v", report)
	}

	// String form carries all three.
	if !strings.Contains(report.String(), "/ext/err") || !strings.Contains(report.String(), "session_start") {
		t.Fatalf("string = %q", report.String())
	}
}

func TestExtensionRunnerPanicContainment(t *testing.T) {
	extension := newTestExtension("/ext/panic")
	extension.On("session_start", func(event any) error {
		panic("boom")
	})

	runner := NewExtensionRunner([]*Extension{extension}, "/cwd")
	var mu sync.Mutex
	reports := 0
	runner.OnError(func(report *ExtensionError) { mu.Lock(); reports++; mu.Unlock() })

	runner.Emit("session_start", nil)
	mu.Lock()
	count := reports
	mu.Unlock()
	if count != 1 {
		t.Fatalf("reports = %d", count)
	}

	// The panic did not propagate.
	reached := false
	extension.On("after_panic", func(event any) error {
		reached = true
		return nil
	})
	runner.Emit("after_panic", nil)
	if !reached {
		t.Fatal("runner dead after panic")
	}
}

func TestExtensionRunnerInvalidate(t *testing.T) {
	runner := NewExtensionRunner(nil, "/cwd")
	if runner.StaleMessage() != "" {
		t.Fatal("initially stale")
	}
	runner.Invalidate("ctx is stale")
	if !strings.Contains(runner.StaleMessage(), "stale") {
		t.Fatalf("message = %q", runner.StaleMessage())
	}
}

func TestExtensionRegistrations(t *testing.T) {
	extension := newTestExtension("/ext/reg")
	extension.RegisterTool(&RegisteredTool{
		Definition: &ToolDefinition{Name: "custom-tool", Description: "d"},
	})
	extension.RegisterCommand(&RegisteredCommand{Name: "my-cmd", Description: "cmd desc"})
	extension.RegisterFlag(&ExtensionFlag{Name: "ext-flag", Type: "string"})
	extension.RegisterShortcut(&ExtensionShortcut{KeyID: "ctrl+k", Command: "my-cmd"})

	runner := NewExtensionRunner([]*Extension{extension}, "/cwd")

	tools := runner.AllTools()
	if len(tools) != 1 || tools["custom-tool"] == nil {
		t.Fatalf("tools = %v", tools)
	}
	commands := runner.AllCommands()
	if len(commands) != 1 || commands["my-cmd"].Description != "cmd desc" {
		t.Fatalf("commands = %v", commands)
	}
	flags := runner.AllFlags()
	if len(flags) != 1 || flags["ext-flag"].Type != "string" {
		t.Fatalf("flags = %v", flags)
	}
	shortcuts := extension.Shortcuts()
	if len(shortcuts) != 1 || shortcuts["ctrl+k"].Command != "my-cmd" {
		t.Fatalf("shortcuts = %v", shortcuts)
	}
}

func TestExtensionRunnerAggregationFirstWins(t *testing.T) {
	first := newTestExtension("/ext/first")
	first.RegisterCommand(&RegisteredCommand{Name: "shared", Description: "first wins"})
	second := newTestExtension("/ext/second")
	second.RegisterCommand(&RegisteredCommand{Name: "shared", Description: "second loses"})

	runner := NewExtensionRunner([]*Extension{first, second}, "/cwd")
	commands := runner.AllCommands()
	if len(commands) != 1 || commands["shared"].Description != "first wins" {
		t.Fatalf("aggregate = %+v", commands)
	}
}

func TestExtensionRunnerExtensions(t *testing.T) {
	extension := newTestExtension("/ext/x")
	runner := NewExtensionRunner([]*Extension{extension}, "/cwd")
	if list := runner.Extensions(); len(list) != 1 || list[0].Path != "/ext/x" {
		t.Fatalf("extensions = %v", list)
	}
}
