// Scheduled Coding Agent —— 在 codingagent 创建的 Agent 上组合自动化任务。
//
// 不修改 codingagent：仅用两个公开组合缝。
//  1. AgentSessionConfig.Agent —— agent 由调用方构造后注入会话；
//  2. AgentSessionConfig.CustomTools —— 挂载 task_manage 工具
//     （CreateToolDefinitionFromAgentTool 一行完成 AgentTool→ToolDefinition 适配）。
//
// 调度器经 AgentExecutor 的 fixed 策略绑定同一会话：定时注入与用户输入
// 在同一个 agent 上串行执行（busy 时等待 idle 再注入）。默认使用内置
// echo 流（无需 API key）。
//
// 运行：go run ./examples/scheduled_codingagent [-duration 12s] [-every 10s]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/agent/harness/tasks"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/codingagent"
)

const fixedSessionID = "coding-main"

func main() {
	duration := flag.Duration("duration", 12*time.Second, "示例运行时长")
	every := flag.Duration("every", 10*time.Second, "巡检任务间隔（受 -min-every 约束）")
	minEvery := flag.Duration("min-every", 10*time.Second, "every 调度最小间隔（测试可调小）")
	flag.Parse()

	result, err := runDemo(demoConfig{
		every:     *every,
		minEvery:  *minEvery,
		runFor:    *duration,
		userAfter: 3 * time.Second,
		onEvent:   func(event tasks.TaskEvent) { fmt.Printf("[事件] %s\n", event) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "示例失败: %v\n", err)
		os.Exit(1)
	}
	defer result.stop()

	fmt.Println("\n── 会话末尾的转录 ──")
	for _, message := range result.session.Agent.Messages() {
		switch typed := message.(type) {
		case *ai.UserMessage:
			fmt.Printf("用户/调度> %s\n", preview(typed))
		case *ai.AssistantMessage:
			for _, block := range typed.Content {
				if text, ok := block.(ai.TextContent); ok && text.Text != "" {
					fmt.Printf("助手> %s\n", text.Text)
				}
			}
		}
	}
	fmt.Println("\n── 巡检任务状态 ──")
	for _, status := range result.scheduler.List() {
		fmt.Printf("- %-10s 运行数=%d 最后状态=%s\n", status.ID, status.RunCount, lastStatus(status.LastRun))
	}
}

type demoConfig struct {
	every     time.Duration
	minEvery  time.Duration
	runFor    time.Duration
	userAfter time.Duration
	onEvent   func(tasks.TaskEvent)
}

type demoResult struct {
	session   *codingagent.AgentSession
	scheduler *tasks.Scheduler
	stop      func()
}

// runDemo 组装会话与调度器，注册绑定主会话的巡检任务，并在中途模拟一次
// 用户输入：两者串行落在同一转录里。
func runDemo(config demoConfig) (*demoResult, error) {
	session, scheduler, err := assemble(config.minEvery)
	if err != nil {
		return nil, err
	}
	if config.onEvent != nil {
		defer scheduler.OnEvent(config.onEvent)()
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := scheduler.Start(ctx); err != nil {
		cancel()
		return nil, err
	}
	if err := scheduler.Register(tasks.TaskSpec{
		ID: "inspect", Schedule: tasks.Every{Interval: config.every, Anchor: time.Now()},
		SessionStrategy: tasks.StrategyFixed, FixedSessionID: fixedSessionID,
		Prompt: "巡检：报告当前会话累计收到多少条用户消息", Enabled: true,
	}); err != nil {
		cancel()
		return nil, err
	}

	// 模拟用户交互：与定时注入同会话串行。
	var userWait sync.WaitGroup
	userWait.Add(1)
	go func() {
		defer userWait.Done()
		time.Sleep(config.userAfter)
		if err := session.Agent.PromptText("用户：用一句话确认你在处理多个输入源"); err != nil {
			fmt.Fprintf(os.Stderr, "用户消息失败: %v\n", err)
		}
	}()

	time.Sleep(config.runFor)
	userWait.Wait()
	return &demoResult{session: session, scheduler: scheduler, stop: func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = scheduler.Stop(stopCtx)
		cancel()
	}}, nil
}

// assemble 是本示例的核心：三行组合缝（Agent 注入、CustomTools 挂载、
// LoadAgent 绑定）把自动化任务接到 codingagent 会话上。
func assemble(minEvery time.Duration) (*codingagent.AgentSession, *tasks.Scheduler, error) {
	dir, err := os.MkdirTemp("", "scheduled-codingagent-*")
	if err != nil {
		return nil, nil, err
	}
	mainAgent := agent.NewAgent(agent.AgentOptions{StreamFn: countingResponder()})
	mainAgent.SessionID = strPtr(fixedSessionID)

	sessionManager, err := codingagent.CreateSessionManager(codingagent.SessionManagerOptions{Cwd: dir, Persist: false})
	if err != nil {
		return nil, nil, err
	}
	executor := tasks.NewAgentExecutor(tasks.AgentExecutorOptions{
		NewAgent:  func() *agent.Agent { return agent.NewAgent(agent.AgentOptions{StreamFn: countingResponder()}) },
		LoadAgent: func(sessionID string) (*agent.Agent, error) { return mainAgent, nil },
	})
	scheduler, err := tasks.NewScheduler(tasks.SchedulerOptions{
		Store:    tasks.NewMemoryStore(),
		Executor: executor,
		MinEvery: minEvery,
	})
	if err != nil {
		return nil, nil, err
	}
	taskManage := codingagent.CreateToolDefinitionFromAgentTool(tasks.TaskManageTool(scheduler))

	session := codingagent.NewAgentSession(codingagent.AgentSessionRuntimeOptions{
		Config: codingagent.AgentSessionConfig{
			Agent:           mainAgent,
			SessionManager:  sessionManager,
			SettingsManager: codingagent.SettingsManagerInMemory(codingagent.Settings{}, nil),
			Cwd:             dir,
			CustomTools:     []*codingagent.ToolDefinition{taskManage},
		},
	})
	return session, scheduler, nil
}

// countingResponder 回复当前转录的用户消息计数，用于观察 fixed 会话的
// 上下文累积与串行注入。
func countingResponder() agent.StreamFn {
	return func(_ *ai.Model, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		users := 0
		for _, message := range transcript.Messages {
			if message.Role() == "user" {
				users++
			}
		}
		reply := fmt.Sprintf("echo：本会话已收到 %d 条用户消息", users)
		message := &ai.AssistantMessage{
			Content:     []ai.ContentBlock{ai.TextContent{Text: reply}},
			StopReason:  ai.StopStop,
			TimestampMs: float64(time.Now().UnixMilli()),
		}
		stream := ai.NewAssistantMessageEventStream()
		go stream.Push(&ai.EventDone{Reason: message.StopReason, Message: message})
		return stream
	}
}

func strPtr(value string) *string { return &value }

func preview(message *ai.UserMessage) string {
	if message.Content.IsText {
		return message.Content.Text
	}
	for _, block := range message.Content.Blocks {
		if text, ok := block.(ai.TextContent); ok {
			return text.Text
		}
	}
	return "(非文本消息)"
}

func lastStatus(run *tasks.TaskRun) string {
	if run == nil {
		return "-"
	}
	return string(run.Status)
}
