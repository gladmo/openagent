package local

// Ports of nodejs-env.test.ts (representative cases) and tools.test.ts
// bash cases.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/tools"
	"github.com/gladmo/openagent/jsonx"
)

func newEnv(t *testing.T) *LocalExecutionEnv {
	t.Helper()
	return New(t.TempDir())
}

func TestAbsolutePathResolution(t *testing.T) {
	env := newEnv(t)
	result := env.AbsolutePath("a/b.txt", harness.BackgroundContext)
	if !result.Ok {
		t.Fatal(result.Error)
	}
	if !filepath.IsAbs(result.Value) || !strings.HasSuffix(filepath.ToSlash(result.Value), "/a/b.txt") {
		t.Fatalf("path = %q", result.Value)
	}
	// Absolute path passes through.
	result = env.AbsolutePath("/etc/hosts", harness.BackgroundContext)
	if result.Value != "/etc/hosts" {
		t.Fatalf("abs = %q", result.Value)
	}
}

func TestWriteReadExistsRemove(t *testing.T) {
	env := newEnv(t)
	if w := env.WriteFile("dir/nested/file.txt", []byte("hello"), harness.BackgroundContext); !w.Ok {
		t.Fatal(w.Error)
	}
	if r := env.ReadTextFile("dir/nested/file.txt", harness.BackgroundContext); !r.Ok || r.Value != "hello" {
		t.Fatalf("read = %+v", r)
	}
	if x := env.Exists("dir/nested/file.txt", harness.BackgroundContext); !x.Ok || !x.Value {
		t.Fatalf("exists = %+v", x)
	}
	if r := env.Remove("dir", &harness.RemoveOptions{Recursive: boolPtr(true)}, harness.BackgroundContext); !r.Ok {
		t.Fatal(r.Error)
	}
	if x := env.Exists("dir/nested/file.txt", harness.BackgroundContext); !x.Ok || x.Value {
		t.Fatalf("exists after remove = %+v", x)
	}
	// force remove of missing path succeeds.
	if r := env.Remove("missing", &harness.RemoveOptions{Force: boolPtr(true)}, harness.BackgroundContext); !r.Ok {
		t.Fatal(r.Error)
	}
}

func TestReadMissingFileIsNotFound(t *testing.T) {
	env := newEnv(t)
	result := env.ReadTextFile("no-such-file.txt", harness.BackgroundContext)
	if result.Ok || result.Error.Code != harness.FileErrNotFound {
		t.Fatalf("result = %+v", result)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestFileInfoKinds(t *testing.T) {
	env := newEnv(t)
	if w := env.WriteFile("f.txt", []byte("x"), harness.BackgroundContext); !w.Ok {
		t.Fatal(w.Error)
	}
	if c := env.CreateDir("d", nil, harness.BackgroundContext); !c.Ok {
		t.Fatal(c.Error)
	}
	if info := env.FileInfo("f.txt", harness.BackgroundContext); !info.Ok || info.Value.Kind != harness.FileKindFile {
		t.Fatalf("file info = %+v", info)
	}
	if info := env.FileInfo("d", harness.BackgroundContext); !info.Ok || info.Value.Kind != harness.FileKindDirectory {
		t.Fatalf("dir info = %+v", info)
	}
}

func TestListDir(t *testing.T) {
	env := newEnv(t)
	for _, name := range []string{"a.txt", "b.txt", "z.txt"} {
		if w := env.WriteFile(name, []byte("x"), harness.BackgroundContext); !w.Ok {
			t.Fatal(w.Error)
		}
	}
	result := env.ListDir(".", harness.BackgroundContext)
	if !result.Ok {
		t.Fatal(result.Error)
	}
	if len(result.Value) != 3 {
		names := []string{}
		for _, info := range result.Value {
			names = append(names, info.Name)
		}
		t.Fatalf("entries = %v", names)
	}
}

func TestRenameAndCanonical(t *testing.T) {
	env := newEnv(t)
	if w := env.WriteFile("old.txt", []byte("data"), harness.BackgroundContext); !w.Ok {
		t.Fatal(w.Error)
	}
	if r := env.RenameFile("old.txt", "sub/new.txt", harness.BackgroundContext); !r.Ok {
		t.Fatal(r.Error)
	}
	if x := env.Exists("sub/new.txt", harness.BackgroundContext); !x.Ok || !x.Value {
		t.Fatal("rename target missing")
	}
	c := env.CanonicalPath("sub/new.txt", harness.BackgroundContext)
	if !c.Ok {
		t.Fatal(c.Error)
	}
	if !strings.HasSuffix(filepath.ToSlash(c.Value), "/sub/new.txt") {
		t.Fatalf("canonical = %q", c.Value)
	}
}

func TestReadTextLines(t *testing.T) {
	env := newEnv(t)
	if w := env.WriteFile("lines.txt", []byte("one\ntwo\nthree\n"), harness.BackgroundContext); !w.Ok {
		t.Fatal(w.Error)
	}
	result := env.ReadTextLines("lines.txt", nil, harness.BackgroundContext)
	if !result.Ok || len(result.Value) != 3 || result.Value[2] != "three" {
		t.Fatalf("lines = %+v", result)
	}
	limit := 2
	limited := env.ReadTextLines("lines.txt", &harness.ReadTextLinesOptions{MaxLines: &limit}, harness.BackgroundContext)
	if !limited.Ok || len(limited.Value) != 2 {
		t.Fatalf("limited = %+v", limited.Value)
	}
}

func TestTextLineReaderTermination(t *testing.T) {
	env := newEnv(t)
	if w := env.WriteFile("t.txt", []byte("first\nsecond\nunterminated"), harness.BackgroundContext); !w.Ok {
		t.Fatal(w.Error)
	}
	result := env.OpenTextLineReader("t.txt", harness.BackgroundContext)
	if !result.Ok {
		t.Fatal(result.Error)
	}
	reader := result.Value
	first, _ := reader.ReadLine(harness.BackgroundContext)
	if !first.Ok || first.Value.Text != "first" || !first.Value.Terminated {
		t.Fatalf("first = %+v", first)
	}
	second, _ := reader.ReadLine(harness.BackgroundContext)
	if !second.Ok || second.Value.Text != "second" || !second.Value.Terminated {
		t.Fatalf("second = %+v", second)
	}
	third, _ := reader.ReadLine(harness.BackgroundContext)
	if !third.Ok || third.Value.Text != "unterminated" || third.Value.Terminated {
		t.Fatalf("third = %+v", third)
	}
	end, _ := reader.ReadLine(harness.BackgroundContext)
	if !end.Ok || end.Value != nil {
		t.Fatalf("end = %+v", end)
	}
	_ = reader.Close(harness.BackgroundContext)
}

func TestExecEcho(t *testing.T) {
	env := newEnv(t)
	var view *harness.ShellOutputView
	options := &harness.ShellExecOptions{
		OnUpdate: func(update harness.ShellOutputUpdate, _ harness.Context) {
			if replace, ok := update.(*harness.ShellOutputReplace); ok {
				view = &replace.Output
			}
		},
	}
	result := env.Exec("echo hello", options, harness.BackgroundContext)
	if !result.Ok {
		t.Fatalf("exec err = %+v", result.Error)
	}
	if result.Value.ExitCode != 0 {
		t.Fatalf("exit = %d", result.Value.ExitCode)
	}
	if view == nil || !strings.Contains(view.Text, "hello") {
		t.Fatalf("view = %+v", view)
	}
}

func TestExecNonZeroExit(t *testing.T) {
	env := newEnv(t)
	result := env.Exec("exit 3", nil, harness.BackgroundContext)
	if !result.Ok {
		t.Fatalf("exec err = %+v", result.Error)
	}
	if result.Value.ExitCode != 3 {
		t.Fatalf("exit = %d", result.Value.ExitCode)
	}
}

func TestExecTimeout(t *testing.T) {
	env := newEnv(t)
	timeout := 0.3
	result := env.Exec("sleep 5", &harness.ShellExecOptions{Timeout: &timeout}, harness.BackgroundContext)
	if result.Ok || result.Error.Code != harness.ExecErrTimeout {
		t.Fatalf("result = %+v err = %+v", result.Value, result.Error)
	}
}

func TestExecInvalidTimeout(t *testing.T) {
	env := newEnv(t)
	zero := 0.0
	result := env.Exec("echo hi", &harness.ShellExecOptions{Timeout: &zero}, harness.BackgroundContext)
	if result.Ok || result.Error.Code != harness.ExecErrTimeout {
		t.Fatalf("result = %+v", result.Error)
	}
}

func TestExecAborted(t *testing.T) {
	env := newEnv(t)
	ctx := harness.WithAbortSignal(abortedSignal(), harness.BackgroundContext)
	result := env.Exec("echo hi", nil, ctx)
	if result.Ok || result.Error.Code != harness.ExecErrAborted {
		t.Fatalf("result = %+v err = %+v", result.Value, result.Error)
	}
}

func TestCustomShellPathMissing(t *testing.T) {
	env := newEnv(t)
	env.shellPath = "/nonexistent/bash"
	result := env.Exec("echo hi", nil, harness.BackgroundContext)
	if result.Ok || result.Error.Code != harness.ExecErrShellUnavailable {
		t.Fatalf("err = %+v", result.Error)
	}
}

func TestBashToolEndToEnd(t *testing.T) {
	env := newEnv(t)
	tool := tools.CreateBashTool()
	toolCtx := tools.ExecutionToolContext{Env: env}

	result, err := tool.Execute("c1", jsonx.ObjFrom("command", "echo tool-output"), nil, toolCtx, nil, harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) == 0 {
		t.Fatal("no content")
	}
	text := bashText(result)
	if !strings.Contains(text, "tool-output") {
		t.Fatalf("text = %q", text)
	}

	// Non-zero exit surfaces the exit code error.
	_, err = tool.Execute("c2", jsonx.ObjFrom("command", "exit 7"), nil, toolCtx, nil, harness.BackgroundContext)
	if err == nil || !strings.Contains(err.Error(), "Command exited with code 7") {
		t.Fatalf("err = %v", err)
	}

	// Timeout.
	_, err = tool.Execute("c3", jsonx.ObjFrom("command", "sleep 5", "timeout", float64(0.3)), nil, toolCtx, nil, harness.BackgroundContext)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}

	// Invalid timeout (zero).
	_, err = tool.Execute("c4", jsonx.ObjFrom("command", "echo hi", "timeout", float64(0)), nil, toolCtx, nil, harness.BackgroundContext)
	if err == nil || !strings.Contains(err.Error(), "Invalid timeout") {
		t.Fatalf("err = %v", err)
	}

	// No output becomes "(no output)".
	result, err = tool.Execute("c5", jsonx.ObjFrom("command", "true"), nil, toolCtx, nil, harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if text := bashText(result); text != "(no output)" {
		t.Fatalf("text = %q", text)
	}
}

func TestBashToolCommandPrefixAndPrepare(t *testing.T) {
	env := newEnv(t)
	prepareSeen := ""
	tool := tools.CreateBashTool(tools.BashToolOptions{
		CommandPrefix: "export PREFIXED=1",
		Prepare: func(execution *tools.BashExecution, _ any, _ harness.Context) error {
			prepareSeen = execution.Command
			return nil
		},
	})
	result, err := tool.Execute("c1", jsonx.ObjFrom("command", "echo $PREFIXED"), nil, tools.ExecutionToolContext{Env: env}, nil, harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepareSeen, "export PREFIXED=1") {
		t.Fatalf("prepare saw = %q", prepareSeen)
	}
	if text := bashText(result); !strings.Contains(text, "1") {
		t.Fatalf("text = %q", text)
	}
}

func bashText(result *agentToolResultAlias) string {
	return agentContentText(result.Content)
}

func abortedSignal() *abortSignalAlias {
	return newAbortedSignalForTest()
}

var _ = os.Getenv
