// Coding Agent —— 基于 agent/harness 工具链的简易编码代理示例。
//
// 组合 harness 已完成的可复用件,展示 pi harness 工具接入 agent 循环的
// 完整方式(对照 data_query_agent 的"自定义工具"路径):
//
//	harness 组件                            本示例用途
//	──────────────────────────────          ─────────────────────────────
//	env/nodejs.NodeExecutionEnv             真实 FileSystem + Shell 执行环境
//	tools.CreateReadTool/Write/Edit/Bash    编码四件套(读/写/改/执行命令)
//	harness.Prepare/Execute/FinalizeToolCall 工具执行管线(参数校验、
//	                                        panic→错误结果、after-tool 补丁)
//	agent.NewAgent                          多轮 Agent 循环(模型驱动)
//
// 适配层 adaptHarnessTool 是本示例的核心:harness 工具的 Execute 签名
// (onUpdate / toolContext / invocation / chord Context)与 agent 循环的
// 签名(abort.Signal)不同,适配器逐段走 pi 的执行管线把两者接起来,
// read/write/edit/bash 由此直接跑在真实模型的多轮循环里。
//
// 运行:ZAI_CODING_CN_API_KEY=<你的密钥> go run ./examples/coding_agent ["任务"]
// 工作目录默认新建临时目录(可用 -workdir 指定),示例不触碰用户文件。
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/env/nodejs"
	"github.com/gladmo/openagent/agent/harness/tools"
	"github.com/gladmo/openagent/ai"
	chordcontext "github.com/gladmo/openagent/chord/context"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/trajectory"
)

const (
	providerName = "zai-coding-cn"
	modelID      = "glm-5.3"
)

// defaultTask 首个任务:依次走到 write(创建文件)与 bash(编译运行验证),
// 完整经历多轮工具循环。
const defaultTask = `创建 greet.go:程序向终端打印 "Hello from openagent harness!"。
然后用 bash 运行 go run greet.go 验证输出,最后总结你做了什么。`

func main() {
	workdir := flag.String("workdir", "", "agent 的工作目录(默认新建临时目录)")
	flag.Parse()

	if os.Getenv("ZAI_CODING_CN_API_KEY") == "" {
		fmt.Fprintln(os.Stderr, "请先设置环境变量 ZAI_CODING_CN_API_KEY")
		os.Exit(1)
	}

	// ---------------------------------------------------------------
	// 1. 执行环境 + 效果闸门(harness 侧的两个基础件)
	//
	// NodeExecutionEnv 提供真实文件系统与 Shell;Gate 是执行管线的
	// 准入闸门(本示例全程放行,关闭由 defer 统一收尾)。
	// ---------------------------------------------------------------
	dir, err := resolveWorkdir(*workdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "工作目录不可用:%v\n", err)
		os.Exit(1)
	}
	env := nodejs.New(dir)
	defer func() { _ = env.Cleanup(chordcontext.BackgroundContext) }()
	gate, gateControl := harness.CreateGate()
	defer gateControl.Close(nil)

	// ---------------------------------------------------------------
	// 2. 模型运行时(与 data_query_agent 同一条真实流路径)
	// ---------------------------------------------------------------
	models := ai.CreateModels()
	models.SetProvider(ai.ZaiCodingCnProvider())
	model := models.GetModel(providerName, modelID)
	if model == nil {
		fmt.Fprintf(os.Stderr, "没有找到可用模型 %s/%s\n", providerName, modelID)
		os.Exit(1)
	}

	// ---------------------------------------------------------------
	// 3. harness 编码工具 → agent 循环工具(本示例的核心接缝)
	// ---------------------------------------------------------------
	toolset := []*harness.AgentHarnessTool{
		tools.CreateReadTool(),
		tools.CreateWriteTool(),
		tools.CreateEditTool(),
		tools.CreateBashTool(),
	}
	agentTools := make([]*agent.AgentTool, 0, len(toolset))
	for _, tool := range toolset {
		agentTools = append(agentTools, adaptHarnessTool(tool, env, gate))
	}

	// ---------------------------------------------------------------
	// 4. 建会话(系统提示词描述工作目录与编码规则)
	// ---------------------------------------------------------------
	prompt := codingAgentPrompt(dir)
	thinking := agent.ThinkingOff
	session := agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			SystemPrompt:  &prompt,
			Model:         model,
			ThinkingLevel: &thinking,
			Tools:         agentTools,
		},
		StreamFn: func(m *ai.Model, ctx *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			return models.StreamSimple(m, ai.Context{Messages: ctx.Messages}, opts)
		},
	})

	traj := trajectory.New(trajectory.Options{ID: "demo-session"})
	_, dispose, err := trajectory.Attach(session, trajectory.RecorderOptions{Trajectory: traj})
	if err != nil {
		fmt.Fprintln(os.Stderr, "挂载采集器失败:", err)
		os.Exit(1)
	}
	defer dispose()
	// stdout NDJSON 记录:Attach 已提交 session/start,用补投模式接全量。
	stopStdout := trajectory.PipeSubscribeAfter(traj, trajectory.NewStdoutSink(os.Stdout), 0)
	defer stopStdout()

	var sawTextDelta bool
	unsubscribe := session.Subscribe(func(event agent.AgentEvent, _ *abort.Signal) {
		switch e := event.(type) {
		case *agent.EventAgentStart:
			fmt.Println("── agent_start ──")
		case *agent.EventMessageStart:
			if e.Message != nil && e.Message.Role() == "assistant" {
				sawTextDelta = false
				fmt.Print("助手：")
			}
		case *agent.EventMessageUpdate:
			if delta, ok := e.AssistantMessageEvent.(*ai.EventTextDelta); ok {
				sawTextDelta = true
				fmt.Print(delta.Delta)
			}
		case *agent.EventToolExecutionStart:
			fmt.Printf("\n[工具] %s(%v)…\n", e.ToolName, argsPreview(e.Args))
		case *agent.EventToolExecutionEnd:
			if e.IsError {
				fmt.Printf("[工具] %s 失败\n", e.ToolName)
			} else {
				fmt.Printf("[工具] %s 完成\n", e.ToolName)
			}
		case *agent.EventMessageEnd:
			if e.Message != nil && e.Message.Role() == "assistant" {
				// 终稿兜底:没有任何文字增量时打印完整回答
				if text := messageText(e.Message); text != "" && !sawTextDelta {
					fmt.Print(text)
				}
				fmt.Println()
			}
		case *agent.EventAgentEnd:
			fmt.Println("── agent_end ──")
		}
	})
	defer unsubscribe()

	// ---------------------------------------------------------------
	// 5. 首个任务(来自命令行,缺省用预设任务)+ 交互循环
	// ---------------------------------------------------------------
	task := strings.Join(flag.Args(), " ")
	if task == "" {
		task = defaultTask
	}
	fmt.Printf("工作目录:%s\n使用模型：%s/%s\n任务：%s\n\n", dir, model.Provider, model.ID, task)

	if err := session.PromptText(task); err != nil {
		fmt.Fprintf(os.Stderr, "Agent 运行失败：%v\n", err)
		os.Exit(1)
	}

	runPromptLoop(session)

	// ---------------------------------------------------------------
	// 6. 展示最终会话状态(验证工具结果已回传进消息历史)
	// ---------------------------------------------------------------
	fmt.Println("\n── 会话消息历史 ──")
	for _, message := range session.Messages() {
		switch message.Role() {
		case "user":
			fmt.Printf("  [user]      %s\n", truncate(messageText(message), 60))
		case "assistant":
			if text := messageText(message); text != "" {
				fmt.Printf("  [assistant] %s\n", truncate(text, 60))
			} else {
				fmt.Printf("  [assistant] （工具调用：%s）\n", toolCallNames(message))
			}
		case "toolResult":
			fmt.Printf("  [toolResult] %s\n", truncate(messageText(message), 60))
		}
	}
}

// resolveWorkdir 显式解析工作目录:空值新建临时目录,给定值取绝对路径
// (不存在则创建),失败即刻报错。
func resolveWorkdir(given string) (string, error) {
	if given == "" {
		return os.MkdirTemp("", "coding-agent-")
	}
	absolute, err := filepath.Abs(given)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return "", err
	}
	return absolute, nil
}

// runPromptLoop 逐行读取 stdin 继续对话,直到 exit/EOF。不做 Ctrl-C
// 中断处理(非本示例目标)。
func runPromptLoop(session *agent.Agent) {
	fmt.Print("\n继续对话(输入 exit 退出)：")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			return
		}
		if err := session.PromptText(line); err != nil {
			fmt.Fprintf(os.Stderr, "Agent 运行失败：%v\n", err)
			return
		}
		fmt.Print("\n继续对话(输入 exit 退出)：")
	}
}

// codingAgentPrompt 是 coding agent 的系统提示词:约束工具使用顺序与
// 验证习惯,任务完成即收束。
func codingAgentPrompt(workdir string) string {
	return "你是一名谨慎的 coding agent,工作目录是 " + workdir + "。\n" +
		"规则:\n" +
		"- 修改文件前先用 read 工具确认现状;新文件用 write,改动已有文件优先用 edit。\n" +
		"- 每完成一步改动,用 bash 工具验证(go build、go run、go test 或任务要求的命令)。\n" +
		"- 小步前进;命令失败时读取输出修正后重试。\n" +
		"- 任务完成后,用一段简短中文总结改动与验证结果。"
}

// adaptHarnessTool 把一个 harness 工具适配进 agent 循环。
//
// 逐段走 pi 的工具执行管线:PrepareToolCall(typebox 参数校验)→
// ApplyBeforeToolDecision 放行 → ExecuteToolCall(panic 转错误结果)→
// FinalizeToolCall。循环传入的 abort.Signal 织进 chord Context,取消
// 语义随信号传播到文件与 Shell 操作。IsError 以 error 返回:错误文本
// 携带工具输出(bash 失败时保留 stdout/stderr),由循环标记进消息历史。
func adaptHarnessTool(tool *harness.AgentHarnessTool, env harness.ExecutionEnv, gate harness.Gate) *agent.AgentTool {
	return &agent.AgentTool{
		Name:        tool.Name,
		Label:       tool.Label,
		Description: tool.Description,
		Parameters:  tool.Parameters,
		Execute: func(toolCallID string, params any, signal *abort.Signal, onUpdate agent.AgentToolUpdateCallback) (*agent.AgentToolResult, error) {
			arguments, _ := params.(*jsonx.Obj)
			if arguments == nil {
				arguments = jsonx.NewObj()
			}
			call := &ai.ToolCall{ID: toolCallID, Name: tool.Name, Arguments: arguments}
			prepared, immediate := harness.PrepareToolCall(call, []*harness.AgentHarnessTool{tool})
			if immediate != nil {
				return nil, errors.New(resultText(immediate.Result))
			}
			cleared, immediate := harness.ApplyBeforeToolDecision(prepared, nil)
			if immediate != nil {
				return nil, errors.New(resultText(immediate.Result))
			}
			ctx := chordcontext.WithAbortSignal(signal, chordcontext.BackgroundContext)
			executed, _ := harness.ExecuteToolCall(cleared, gate, adaptUpdateCallback(onUpdate), tools.ExecutionToolContext{Env: env}, nil, ctx)
			finalized := harness.FinalizeToolCall(cleared, executed, nil)
			if finalized.IsError {
				return nil, errors.New(resultText(finalized.Result))
			}
			return finalized.Result, nil
		},
	}
}

// adaptUpdateCallback 把循环的进度回调映射到 harness 回调签名。
func adaptUpdateCallback(onUpdate agent.AgentToolUpdateCallback) harness.AgentHarnessToolUpdateCallback {
	if onUpdate == nil {
		return nil
	}
	return func(partial *agent.AgentToolResult, _ *harness.AgentHarnessToolUpdateOptions) {
		onUpdate(partial)
	}
}

// resultText 提取工具结果里的全部文本块(bash 失败时错误文本含输出)。
func resultText(result *agent.AgentToolResult) string {
	if result == nil {
		return "tool failed without a result"
	}
	var out strings.Builder
	for _, block := range result.Content {
		if text, ok := block.(ai.TextContent); ok {
			out.WriteString(text.Text)
		}
	}
	return out.String()
}

// ---------- 输出辅助(与 data_query_agent 同款) ----------

func messageText(message ai.Message) string {
	switch m := message.(type) {
	case *ai.UserMessage:
		if m.Content.IsText {
			return m.Content.Text
		}
		var out string
		for _, block := range m.Content.Blocks {
			if text, ok := block.(ai.TextContent); ok {
				out += text.Text
			}
		}
		return out
	case *ai.AssistantMessage:
		var out string
		for _, block := range m.Content {
			if text, ok := block.(ai.TextContent); ok {
				out += text.Text
			}
		}
		return out
	case *ai.ToolResultMessage:
		var out string
		for _, block := range m.Content {
			if text, ok := block.(ai.TextContent); ok {
				out += text.Text
			}
		}
		return out
	}
	return ""
}

func toolCallNames(message ai.Message) string {
	assistant, ok := message.(*ai.AssistantMessage)
	if !ok {
		return ""
	}
	names := ""
	for _, block := range assistant.Content {
		if call, ok := block.(*ai.ToolCall); ok {
			if names != "" {
				names += ", "
			}
			names += call.Name
		}
	}
	return names
}

func argsPreview(args any) string {
	if obj, ok := args.(*jsonx.Obj); ok {
		return jsonx.Stringify(obj)
	}
	return fmt.Sprint(args)
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
