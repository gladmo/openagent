// Trajectory 演示:在一个 faux 模型驱动的多步工具循环上挂轨迹采集,
// 展示完整工程闭环——
//
//	采集   trajectory.Attach(Agent) 订阅 agent 事件 + 包装 StreamFn
//	订阅   Trajectory.Subscribe(可重复调用,各自返回退订函数)
//	输出   默认 stdout NDJSON(逐行 8KB/32KB 截断上限)+ 文件 sink
//	组装   Transcript / Outline / UsageSummary 从记录折叠派生视图
//	查询   Query 按类型/turn/文本过滤
//	回放   DeriveScript → BuildReplayAgent 无网络复现,CompareRecords 断言等价
//	导出   ExportMarkdown 人类可读账本,ReadFile 从磁盘读回
//
// 运行:go run ./examples/trajectory_demo(无需任何 API key)
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/trajectory"
)

func main() {
	dir, err := os.MkdirTemp("", "trajectory-demo-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "临时目录创建失败:", err)
		os.Exit(1)
	}

	// 1. 采集:脚本化模型 + 一个确定性工具,挂上 recorder。
	session := buildScriptedAgent()
	traj := trajectory.New(trajectory.Options{ID: "demo-session"})
	recorder, dispose, err := trajectory.Attach(session, trajectory.RecorderOptions{Trajectory: traj})
	if err != nil {
		fmt.Fprintln(os.Stderr, "挂载采集器失败:", err)
		os.Exit(1)
	}

	// 2. 默认 stdout 记录 + 文件记录(两个独立的 Subscribe)。
	stopStdout := trajectory.PipeSubscribe(traj, trajectory.NewStdoutSink(os.Stdout))
	logPath := filepath.Join(dir, "demo.trajectory.jsonl")
	fileSink, err := trajectory.NewFileSink(logPath, trajectory.FileHeader{
		ID: traj.ID(), CreatedAt: float64(nowMillis()), Model: "faux-1", Provider: "demo-prov",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建轨迹文件失败:", err)
		os.Exit(1)
	}
	stopFile := trajectory.PipeSubscribeAfter(traj, fileSink, 0)

	// 3. 跑一轮带工具调用的多步会话。结束后先 dispose recorder(补
	// session/end 记录,由已挂载的 sink 落盘),再撤订阅、关文件。
	if err := session.PromptText("把 hello trajectory 原样回显出来"); err != nil {
		fmt.Fprintln(os.Stderr, "Agent 运行失败:", err)
		os.Exit(1)
	}
	dispose()
	stopStdout()
	stopFile()
	_ = fileSink.Close()

	// 4. 组装与查询。
	outline := trajectory.Outline(traj.Snapshot())
	fmt.Printf("\n── 轮次摘要 ──\n")
	for _, turn := range outline {
		fmt.Printf("  turn %d:%d step,%d 次工具,%s\n", turn.Turn, turn.Steps, turn.ToolCalls, preview(turn.Response))
	}
	hits := traj.Query(trajectory.Query{Types: []string{trajectory.KindToolCall}})
	fmt.Printf("── 查询 tool/call ──\n  命中 %d 条\n", len(hits))

	// 5. 存储 → 读回 → 导出 Markdown。
	file, err := trajectory.ReadFile(logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读回轨迹失败:", err)
		os.Exit(1)
	}
	mdPath := filepath.Join(dir, "demo.trajectory.md")
	mdFile, err := os.Create(mdPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建导出失败:", err)
		os.Exit(1)
	}
	if err := trajectory.ExportMarkdown(mdFile, file.Header, file.Records); err != nil {
		fmt.Fprintln(os.Stderr, "导出失败:", err)
		os.Exit(1)
	}
	_ = mdFile.Close()
	fmt.Printf("── 导出 ──\n  JSONL %s(%d 条记录)\n  Markdown %s\n", logPath, len(file.Records), mdPath)

	// 6. 回放:从记录派生脚本,重建 agent 复现,断言两次运行等价。
	script, err := trajectory.DeriveScriptFromRecords(file.Records)
	if err != nil {
		fmt.Fprintln(os.Stderr, "派生回放脚本失败:", err)
		os.Exit(1)
	}
	replay, _, err := trajectory.BuildReplayAgent(script, trajectory.ReplayOptions{
		Tools: []*agent.AgentTool{echoTool()}, SystemPrompt: demoPrompt,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "构建回放失败:", err)
		os.Exit(1)
	}
	live := trajectory.New(trajectory.Options{ID: "demo-replay"})
	_, replayDispose, err := trajectory.Attach(replay, trajectory.RecorderOptions{Trajectory: live})
	if err != nil {
		fmt.Fprintln(os.Stderr, "挂载回放采集失败:", err)
		os.Exit(1)
	}
	if err := replay.PromptText("把 hello trajectory 原样回显出来"); err != nil {
		fmt.Fprintln(os.Stderr, "回放运行失败:", err)
		os.Exit(1)
	}
	replayDispose()
	if err := trajectory.CompareRecords(file.Records, live.Snapshot()); err != nil {
		fmt.Fprintln(os.Stderr, "回放与录制不等价:", err)
		os.Exit(1)
	}
	fmt.Println("── 回放 ──\n  等价性校验通过(录制与回放的记录序列一致)")
	_ = recorder
}

const demoPrompt = "You are a demo agent."

func buildScriptedAgent() *agent.Agent {
	toolUse := ai.StopToolUse
	faux := ai.FauxProvider(ai.RegisterFauxProviderOptions{API: "demo-api", Provider: "demo-prov"})
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStep(ai.FauxAssistantMessage([]ai.ContentBlock{
			ai.FauxText("我来调用 echo 工具。"),
			ai.FauxToolCall("echo", jsonx.ObjFrom("text", "hello trajectory")),
		}, ai.FauxAssistantMessageOptions{StopReason: &toolUse})),
		ai.FauxStep(ai.FauxAssistantMessage([]ai.ContentBlock{ai.FauxText("工具返回:hello trajectory")})),
	})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider)
	thinking := agent.ThinkingOff
	prompt := demoPrompt
	return agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			SystemPrompt:  &prompt,
			Model:         faux.GetModel(),
			ThinkingLevel: &thinking,
			Tools:         []*agent.AgentTool{echoTool()},
		},
		StreamFn: func(m *ai.Model, ctx *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			return models.StreamSimple(m, ai.Context{Messages: ctx.Messages}, opts)
		},
	})
}

// echoTool 原样返回 text 参数(确定性工具,回放可复现)。
func echoTool() *agent.AgentTool {
	return &agent.AgentTool{
		Name:        "echo",
		Description: "原样回显 text 参数。",
		Execute: func(_ string, params any, _ *abort.Signal, _ agent.AgentToolUpdateCallback) (*agent.AgentToolResult, error) {
			text := "(空)"
			if obj, ok := params.(*jsonx.Obj); ok {
				if v, ok := obj.Get("text"); ok {
					if s, ok := v.(string); ok {
						text = s
					}
				}
			}
			return &agent.AgentToolResult{Content: []ai.ContentBlock{ai.TextContent{Text: text}}}, nil
		},
	}
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func preview(s string) string {
	runes := []rune(s)
	if len(runes) > 40 {
		return string(runes[:40]) + "…"
	}
	return s
}
