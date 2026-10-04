package codingagent

// Tests for the extension foundations: event bus, timings, source info,
// pi manifest, exec, and the custom message types (ported from their
// reference test files).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

func TestEventBusEmitSubscribe(t *testing.T) {
	bus := CreateEventBus()
	defer bus.Clear()

	received := []string{}
	unsubscribe := bus.On("channel", func(data any) {
		if text, ok := data.(string); ok {
			received = append(received, text)
		}
	})

	bus.Emit("channel", "one")
	bus.Emit("channel", "two")
	bus.Emit("other", "ignored")

	if len(received) != 2 || received[0] != "one" || received[1] != "two" {
		t.Fatalf("received = %v", received)
	}

	unsubscribe()
	bus.Emit("channel", "three")
	if len(received) != 2 {
		t.Fatalf("after unsubscribe = %v", received)
	}
}

func TestEventBusMultipleSubscribers(t *testing.T) {
	bus := CreateEventBus()
	defer bus.Clear()

	seen := 0
	bus.On("c", func(data any) { seen++ })
	bus.On("c", func(data any) { seen++ })
	bus.Emit("c", nil)
	if seen != 2 {
		t.Fatalf("seen = %d", seen)
	}
}

func TestEventBusHandlerErrorsContained(t *testing.T) {
	bus := CreateEventBus()
	defer bus.Clear()

	reached := false
	bus.On("c", func(data any) { panic("handler boom") })
	bus.On("c", func(data any) { reached = true })

	bus.Emit("c", nil)
	if !reached {
		t.Fatal("second handler not reached after first panicked")
	}
}

func TestEventBusClear(t *testing.T) {
	bus := CreateEventBus()
	called := false
	bus.On("c", func(data any) { called = true })
	bus.Clear()
	bus.Emit("c", nil)
	if called {
		t.Fatal("handler called after clear")
	}
}

func TestSourceInfo(t *testing.T) {
	info := CreateSourceInfo("/path", PathMetadata{
		Source: "src", Scope: ScopeUser, Origin: OriginPackage, BaseDir: "/base",
	})
	if info.Path != "/path" || info.Source != "src" || info.Scope != ScopeUser || info.Origin != OriginPackage || info.BaseDir != "/base" {
		t.Fatalf("info = %+v", info)
	}

	synthetic := CreateSyntheticSourceInfo("/p", "s", "", "", "")
	if synthetic.Scope != ScopeTemporary || synthetic.Origin != OriginTopLevel {
		t.Fatalf("synthetic defaults = %+v", synthetic)
	}
}

func TestReadPiManifest(t *testing.T) {
	dir := t.TempDir()

	// Valid manifest.
	valid := filepath.Join(dir, "package.json")
	os.WriteFile(valid, []byte(`{"name":"x","pi":{"extensions":["a.ts","b.ts"],"skills":["s"],"prompts":["p"],"themes":["t"]}}`), 0o644)
	manifest := ReadPiManifest(valid)
	if manifest == nil {
		t.Fatal("manifest nil")
	}
	if len(manifest.Extensions) != 2 || manifest.Extensions[0] != "a.ts" {
		t.Fatalf("extensions = %v", manifest.Extensions)
	}
	if len(manifest.Skills) != 1 || len(manifest.Prompts) != 1 || len(manifest.Themes) != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}

	// No pi field.
	plain := filepath.Join(dir, "plain.json")
	os.WriteFile(plain, []byte(`{"name":"x"}`), 0o644)
	if ReadPiManifest(plain) != nil {
		t.Fatal("plain manifest should be nil")
	}

	// Non-string entries are skipped per field.
	mixed := filepath.Join(dir, "mixed.json")
	os.WriteFile(mixed, []byte(`{"pi":{"extensions":["ok",42]}}`), 0o644)
	manifest = ReadPiManifest(mixed)
	if manifest == nil || manifest.Extensions != nil {
		t.Fatalf("mixed = %+v", manifest)
	}

	// Invalid JSON.
	broken := filepath.Join(dir, "broken.json")
	os.WriteFile(broken, []byte(`{nope`), 0o644)
	if ReadPiManifest(broken) != nil {
		t.Fatal("broken manifest should be nil")
	}

	// Missing file.
	if ReadPiManifest(filepath.Join(dir, "nope.json")) != nil {
		t.Fatal("missing manifest should be nil")
	}
}

func TestExecCommand(t *testing.T) {
	result := ExecCommand("echo", []string{"hi"}, t.TempDir(), nil)
	if result.Stdout != "hi\n" || result.Code != 0 || result.Killed {
		t.Fatalf("result = %+v", result)
	}

	// Nonzero exit.
	result = ExecCommand("sh", []string{"-c", "echo err >&2; exit 2"}, t.TempDir(), nil)
	if result.Code != 2 || !strings.Contains(result.Stderr, "err") {
		t.Fatalf("result = %+v", result)
	}

	// Timeout kills.
	result = ExecCommand("sleep", []string{"5"}, t.TempDir(), &ExecOptions{Timeout: 100})
	if !result.Killed {
		t.Fatalf("timeout result = %+v", result)
	}

	// Abort kills.
	controller := abort.NewController()
	go func() {
		time.Sleep(100 * time.Millisecond)
		controller.Abort()
	}()
	result = ExecCommand("sleep", []string{"5"}, t.TempDir(), &ExecOptions{Signal: controller.Signal()})
	if !result.Killed {
		t.Fatalf("abort result = %+v", result)
	}

	// Missing command resolves with code 1.
	result = ExecCommand("definitely-not-a-command-xyz", []string{}, t.TempDir(), nil)
	if result.Code != 1 {
		t.Fatalf("missing result = %+v", result)
	}
}

func TestBashExecutionToText(t *testing.T) {
	msg := &BashExecutionMessage{Command: "ls", Output: "file"}
	if got := BashExecutionToText(msg); !strings.Contains(got, "Ran `ls`") || !strings.Contains(got, "```") || !strings.Contains(got, "file") {
		t.Fatalf("text = %q", got)
	}

	// No output.
	msg = &BashExecutionMessage{Command: "true"}
	if got := BashExecutionToText(msg); !strings.Contains(got, "(no output)") {
		t.Fatalf("empty = %q", got)
	}

	// Cancelled.
	msg = &BashExecutionMessage{Command: "x", Cancelled: true}
	if got := BashExecutionToText(msg); !strings.Contains(got, "(command cancelled)") {
		t.Fatalf("cancelled = %q", got)
	}

	// Nonzero exit.
	code := 3
	msg = &BashExecutionMessage{Command: "x", ExitCode: &code}
	if got := BashExecutionToText(msg); !strings.Contains(got, "Command exited with code 3") {
		t.Fatalf("exit = %q", got)
	}

	// Truncated with spill.
	msg = &BashExecutionMessage{Command: "x", Truncated: true, FullOutputPath: "/tmp/spill"}
	if got := BashExecutionToText(msg); !strings.Contains(got, "Full output: /tmp/spill") {
		t.Fatalf("truncated = %q", got)
	}
}

func TestCreateMessages(t *testing.T) {
	branch := CreateBranchSummaryMessage("sum", "from-id", "2024-01-01T00:00:00Z")
	if branch.Role() != RoleBranchSummary || branch.Summary != "sum" || branch.FromID == nil || *branch.FromID != "from-id" {
		t.Fatalf("branch = %+v", branch)
	}
	if branch.TimestampMs == 0 {
		t.Fatal("timestamp not parsed")
	}

	compaction := CreateCompactionSummaryMessage("sum", 500, "2024-01-01T00:00:00Z")
	if compaction.Role() != RoleCompactionSummary || compaction.TokensBefore != 500 {
		t.Fatalf("compaction = %+v", compaction)
	}

	custom := CreateCustomMessage("type1", "content", true, nil, "2024-01-01T00:00:00Z")
	if custom.Role() != RoleCustom || custom.CustomType != "type1" || custom.Display != true {
		t.Fatalf("custom = %+v", custom)
	}
}

func TestConvertToLlm(t *testing.T) {
	messages := []agent.AgentMessage{
		&BashExecutionMessage{Command: "ls", Output: "x", TimestampMs: 1},
		&BashExecutionMessage{Command: "hidden", ExcludeFromContext: true, TimestampMs: 2},
		&CustomMessage{CustomType: "t", Content: "text", TimestampMs: 3},
		&BranchSummaryMessage{Summary: "branch", TimestampMs: 4},
		&CompactionSummaryMessage{Summary: "compact", TimestampMs: 5},
		&ai.UserMessage{Content: ai.StringContent("plain"), TimestampMs: 6},
	}

	llm := ConvertToLlm(messages)
	// Excluded bash message drops out; 5 remain.
	if len(llm) != 5 {
		t.Fatalf("llm = %d messages", len(llm))
	}

	first := llm[0].(*ai.UserMessage)
	if first.Content.IsText || len(first.Content.Blocks) == 0 {
		t.Fatalf("first content = %+v", first.Content)
	}

	// Custom string becomes text content.
	second := llm[1].(*ai.UserMessage)
	if !second.Content.IsText || second.Content.Text != "text" {
		t.Fatalf("second = %+v", second.Content)
	}

	// Branch/compaction prefixes apply.
	third := llm[2].(*ai.UserMessage)
	if !strings.Contains(third.Content.Blocks[0].(ai.TextContent).Text, BranchSummaryPrefix) {
		t.Fatalf("third = %+v", third.Content)
	}
	fourth := llm[3].(*ai.UserMessage)
	if !strings.Contains(fourth.Content.Blocks[0].(ai.TextContent).Text, CompactionSummaryPrefix) {
		t.Fatalf("fourth = %+v", fourth.Content)
	}

	// Plain user message passes through.
	fifth := llm[4].(*ai.UserMessage)
	if !fifth.Content.IsText || fifth.Content.Text != "plain" {
		t.Fatalf("fifth = %+v", fifth.Content)
	}
}

func TestTimingsDisabled(t *testing.T) {
	// PI_TIMING unset: no-ops, no panics.
	ResetTimings(TimingMain)
	Time("label", TimingMain)
	PrintTimings()
}
