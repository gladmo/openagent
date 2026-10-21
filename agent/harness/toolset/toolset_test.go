package toolset

// Behavior tests for the toolset package: Adapt drives harness tools
// through the pi execution pipeline onto the agent-loop signature, and
// Local owns the env+gate lifecycle behind one idempotent Close.

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/env/local"
	"github.com/gladmo/openagent/agent/harness/tools"
	"github.com/gladmo/openagent/jsonx"
)

func byName(t *testing.T, tools []*agent.AgentTool) map[string]*agent.AgentTool {
	t.Helper()
	out := map[string]*agent.AgentTool{}
	for _, tool := range tools {
		out[tool.Name] = tool
	}
	return out
}

func TestAdaptWriteThenRead(t *testing.T) {
	env := local.New(t.TempDir())
	gate, control := harness.CreateGate()
	defer control.Close(nil)
	tools := byName(t, Adapt(Coding(), env, gate))

	result, err := tools["write"].Execute("c1", jsonx.ObjFrom("path", "nested/hi.txt", "content", "hello harness"), nil, nil)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if !strings.Contains(resultText(result), "nested/hi.txt") {
		t.Fatalf("write result missing path echo: %q", resultText(result))
	}

	readResult, err := tools["read"].Execute("c2", jsonx.ObjFrom("path", "nested/hi.txt"), nil, nil)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(resultText(readResult), "hello harness") {
		t.Fatalf("read result missing content: %q", resultText(readResult))
	}
}

func TestAdaptEditReplacesText(t *testing.T) {
	env := local.New(t.TempDir())
	gate, control := harness.CreateGate()
	defer control.Close(nil)
	tools := byName(t, Adapt(Coding(), env, gate))

	if _, err := tools["write"].Execute("c1", jsonx.ObjFrom("path", "a.txt", "content", "one\ntwo\nthree"), nil, nil); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	edits := []any{jsonx.ObjFrom("oldText", "two", "newText", "TWO")}
	if _, err := tools["edit"].Execute("c2", jsonx.ObjFrom("path", "a.txt", "edits", edits), nil, nil); err != nil {
		t.Fatalf("edit failed: %v", err)
	}

	readResult, err := tools["read"].Execute("c3", jsonx.ObjFrom("path", "a.txt"), nil, nil)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	text := resultText(readResult)
	if !strings.Contains(text, "TWO") || strings.Contains(text, "two") {
		t.Fatalf("edit not applied, file now: %q", text)
	}
}

func TestAdaptBashRunsAndReportsFailure(t *testing.T) {
	env := local.New(t.TempDir())
	gate, control := harness.CreateGate()
	defer control.Close(nil)
	tools := byName(t, Adapt(Coding(), env, gate))

	result, err := tools["bash"].Execute("c1", jsonx.ObjFrom("command", "echo bash-ok"), nil, nil)
	if err != nil {
		t.Fatalf("bash echo failed: %v", err)
	}
	if !strings.Contains(resultText(result), "bash-ok") {
		t.Fatalf("bash output missing echo text: %q", resultText(result))
	}

	_, err = tools["bash"].Execute("c2", jsonx.ObjFrom("command", "exit 7"), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "exited with code 7") {
		t.Fatalf("bash exit 7 should surface as error, got: %v", err)
	}
}

func TestAdaptInvalidArgsFailLoud(t *testing.T) {
	env := local.New(t.TempDir())
	gate, control := harness.CreateGate()
	defer control.Close(nil)
	tools := byName(t, Adapt(Coding(), env, gate))

	if _, err := tools["write"].Execute("c1", jsonx.ObjFrom("content", "no path"), nil, nil); err == nil {
		t.Fatal("write without path should fail")
	}
	if _, err := tools["bash"].Execute("c2", jsonx.NewObj(), nil, nil); err == nil {
		t.Fatal("bash without command should fail")
	}
}

func TestAdaptStreamsUpdateCallback(t *testing.T) {
	env := local.New(t.TempDir())
	gate, control := harness.CreateGate()
	defer control.Close(nil)
	tools := byName(t, Adapt(Coding(), env, gate))

	var mu sync.Mutex
	updates := 0
	onUpdate := func(*agent.AgentToolResult) {
		mu.Lock()
		updates++
		mu.Unlock()
	}
	if _, err := tools["bash"].Execute("c1", jsonx.ObjFrom("command", "echo one"), nil, onUpdate); err != nil {
		t.Fatalf("bash failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if updates == 0 {
		t.Fatal("update callback never fired")
	}
}

func TestAdaptRequiresEnvAndGate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil env should panic at assembly")
		}
	}()
	Adapt(Coding(), nil, nil)
}

func TestLocalCloseLifecycle(t *testing.T) {
	bundle := NewLocal(t.TempDir(), Coding())
	tools := byName(t, bundle.AgentTools())

	if _, err := tools["write"].Execute("c1", jsonx.ObjFrom("path", "a.txt", "content", "x"), nil, nil); err != nil {
		t.Fatalf("write before close failed: %v", err)
	}

	if err := bundle.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	// Close is idempotent: the second call returns the first result.
	if err := bundle.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}

	// After Close the gate rejects admission; the pipeline converts it to
	// an error return instead of a panic escape.
	_, err := tools["write"].Execute("c2", jsonx.ObjFrom("path", "b.txt", "content", "y"), nil, nil)
	if err == nil {
		t.Fatal("execute after close should fail")
	}
	if !strings.Contains(err.Error(), "gate closed") {
		t.Fatalf("execute after close should report the closed gate, got: %v", err)
	}
}

func TestReleaseMutationQueueAllowsRequeue(t *testing.T) {
	env := local.New(t.TempDir())
	gate, control := harness.CreateGate()
	defer control.Close(nil)
	adapted := byName(t, Adapt(Coding(), env, gate))

	if _, err := adapted["write"].Execute("c1", jsonx.ObjFrom("path", "a.txt", "content", "first"), nil, nil); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	tools.ReleaseMutationQueue(env)
	if _, err := adapted["write"].Execute("c2", jsonx.ObjFrom("path", "a.txt", "content", "second"), nil, nil); err != nil {
		t.Fatalf("write after release failed: %v", err)
	}
	readResult, err := adapted["read"].Execute("c3", jsonx.ObjFrom("path", "a.txt"), nil, nil)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(resultText(readResult), "second") {
		t.Fatalf("file content after requeue = %q", resultText(readResult))
	}
}

func TestAdaptBashSpillFootnoteCarriesPath(t *testing.T) {
	bundle := NewLocal(t.TempDir(), Coding())
	defer func() { _ = bundle.Close() }()
	adapted := byName(t, bundle.AgentTools())

	result, err := adapted["bash"].Execute("c1", jsonx.ObjFrom("command", "seq 1 6000"), nil, nil)
	if err != nil {
		t.Fatalf("bash failed: %v", err)
	}
	text := resultText(result)
	if !strings.Contains(text, "Full output: ") {
		t.Fatalf("truncation footnote missing: %q", text[max(0, len(text)-200):])
	}
	details, ok := result.Details.(tools.BashToolDetails)
	if !ok {
		t.Fatalf("details type = %T", result.Details)
	}
	if details.FullOutputPath == "" || !strings.Contains(text, details.FullOutputPath) {
		t.Fatalf("footnote path vs details path %q", details.FullOutputPath)
	}
	if info, err := os.Stat(details.FullOutputPath); err != nil || info.Size() == 0 {
		t.Fatalf("spill file not materialized: %v", err)
	}
}
