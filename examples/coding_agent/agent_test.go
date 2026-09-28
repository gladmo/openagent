package main

// coding_agent 自测:不经网络与模型,直接驱动适配后的 harness 工具,
// 验证适配器把 agent 循环签名正确接到 pi 执行管线上。

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/env/nodejs"
	"github.com/gladmo/openagent/agent/harness/tools"
	chordcontext "github.com/gladmo/openagent/chord/context"
	"github.com/gladmo/openagent/jsonx"
)

// testToolset 把四个 harness 工具适配到同一个临时执行环境上。
type testToolset struct {
	write, read, edit, bash *agent.AgentTool
}

func newTestToolset(t *testing.T) testToolset {
	t.Helper()
	env := nodejs.New(t.TempDir())
	t.Cleanup(func() { _ = env.Cleanup(chordcontext.BackgroundContext) })
	gate, control := harness.CreateGate()
	t.Cleanup(func() { control.Close(nil) })
	return testToolset{
		write: adaptHarnessTool(tools.CreateWriteTool(), env, gate),
		read:  adaptHarnessTool(tools.CreateReadTool(), env, gate),
		edit:  adaptHarnessTool(tools.CreateEditTool(), env, gate),
		bash:  adaptHarnessTool(tools.CreateBashTool(), env, gate),
	}
}

func TestAdaptedWriteThenRead(t *testing.T) {
	set := newTestToolset(t)

	result, err := set.write.Execute("c1", jsonx.ObjFrom("path", "nested/hi.txt", "content", "hello harness"), nil, nil)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if !strings.Contains(resultText(result), "nested/hi.txt") {
		t.Fatalf("write result missing path echo: %q", resultText(result))
	}

	readResult, err := set.read.Execute("c2", jsonx.ObjFrom("path", "nested/hi.txt"), nil, nil)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(resultText(readResult), "hello harness") {
		t.Fatalf("read result missing file content: %q", resultText(readResult))
	}
}

func TestAdaptedEditReplacesText(t *testing.T) {
	set := newTestToolset(t)

	if _, err := set.write.Execute("c1", jsonx.ObjFrom("path", "a.txt", "content", "one\ntwo\nthree"), nil, nil); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	edits := []any{jsonx.ObjFrom("oldText", "two", "newText", "TWO")}
	if _, err := set.edit.Execute("c2", jsonx.ObjFrom("path", "a.txt", "edits", edits), nil, nil); err != nil {
		t.Fatalf("edit failed: %v", err)
	}

	readResult, err := set.read.Execute("c3", jsonx.ObjFrom("path", "a.txt"), nil, nil)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	text := resultText(readResult)
	if !strings.Contains(text, "TWO") || strings.Contains(text, "two") {
		t.Fatalf("edit not applied, file now: %q", text)
	}
}

func TestAdaptedBashRunsAndReportsFailure(t *testing.T) {
	set := newTestToolset(t)

	result, err := set.bash.Execute("c1", jsonx.ObjFrom("command", "echo bash-ok"), nil, nil)
	if err != nil {
		t.Fatalf("bash echo failed: %v", err)
	}
	if !strings.Contains(resultText(result), "bash-ok") {
		t.Fatalf("bash output missing echo text: %q", resultText(result))
	}

	_, err = set.bash.Execute("c2", jsonx.ObjFrom("command", "exit 7"), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "exited with code 7") {
		t.Fatalf("bash exit 7 should surface as error, got: %v", err)
	}
}

func TestAdaptedToolInvalidArgsFailLoud(t *testing.T) {
	set := newTestToolset(t)

	// 缺必填参数:适配器内的 PrepareToolCall 校验(或工具自身的类型
	// 断言防线)必须转成错误返回,而不是 panic 逃逸。
	if _, err := set.write.Execute("c1", jsonx.ObjFrom("content", "no path"), nil, nil); err == nil {
		t.Fatal("write without path should fail")
	}
	if _, err := set.bash.Execute("c2", jsonx.NewObj(), nil, nil); err == nil {
		t.Fatal("bash without command should fail")
	}
}
