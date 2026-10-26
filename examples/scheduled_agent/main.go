// Scheduled Agent —— agent/harness/tasks 自动化任务示例。
//
// 演示三类调度（every 间隔 / cron 表达式 / at 一次性）、new 与 fixed
// 两种会话策略、RunNow 手动触发与事件流。默认使用内置 echo 流（无需
// API key，结果可预期）；-real 时切换真实模型流（需
// ZAI_CODING_CN_API_KEY，模型侧接线同 examples/coding_agent）。
//
// 固定会话 "weekly-report" 由调度器与手动触发共享：echo 回复中的
// "第 N 条用户消息" 随触发次数增长，直观展示 fixed 策略的上下文累积；
// new 策略的任务每次都会话归零。
//
// 运行：go run ./examples/scheduled_agent [-duration 13s] [-every 10s]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/agent/harness/tasks"
	"github.com/gladmo/openagent/ai"
)

const fixedSessionID = "weekly-report"

func main() {
	duration := flag.Duration("duration", 13*time.Second, "示例运行时长")
	every := flag.Duration("every", 10*time.Second, "心跳任务间隔（受 -min-every 约束）")
	minEvery := flag.Duration("min-every", 10*time.Second, "every 调度最小间隔（测试可调小）")
	atDelay := flag.Duration("at", 6*time.Second, "一次性任务的触发延迟")
	real := flag.Bool("real", false, "使用真实模型流（需 ZAI_CODING_CN_API_KEY）")
	flag.Parse()

	storePath := filepath.Join(os.TempDir(), "openagent-scheduled-demo", "tasks.jsonl")
	streamFn := echoResponder()
	if *real {
		if os.Getenv("ZAI_CODING_CN_API_KEY") == "" {
			fmt.Fprintln(os.Stderr, "-real 需要环境变量 ZAI_CODING_CN_API_KEY")
			os.Exit(1)
		}
		streamFn = realResponder()
	}

	result, err := runDemo(demoConfig{
		store:    tasks.NewFileStore(storePath),
		streamFn: streamFn,
		every:    *every,
		minEvery: *minEvery,
		atDelay:  *atDelay,
		runFor:   *duration,
		onEvent:  func(event tasks.TaskEvent) { fmt.Printf("[事件] %s\n", event) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "示例失败: %v\n", err)
		os.Exit(1)
	}
	defer result.stop()

	fmt.Printf("\n── 运行记录（store: %s）──\n", storePath)
	for _, entry := range result.log.snapshot() {
		fmt.Printf("会话 %.8s: %s\n", entry.sessionID, entry.reply)
	}
	fmt.Println("\n── 任务状态 ──")
	for _, status := range result.scheduler.List() {
		next := "-"
		if status.NextFire != nil {
			next = status.NextFire.Format(time.RFC3339)
		}
		fmt.Printf("- %-12s %-22s 运行数=%d 下次=%s\n", status.ID, status.Schedule, status.RunCount, next)
	}
}

// demoConfig 参数化示例，供 main 与测试共用。
type demoConfig struct {
	store    tasks.Store
	streamFn agent.StreamFn
	every    time.Duration
	minEvery time.Duration
	atDelay  time.Duration
	runFor   time.Duration
	onEvent  func(tasks.TaskEvent)
}

type demoResult struct {
	scheduler *tasks.Scheduler
	log       *runLog
	stop      func()
}

type replyEntry struct {
	sessionID string
	reply     string
}

type runLog struct {
	mu      sync.Mutex
	entries []replyEntry
}

func (l *runLog) add(sessionID, reply string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, replyEntry{sessionID: sessionID, reply: reply})
}

func (l *runLog) snapshot() []replyEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]replyEntry(nil), l.entries...)
}

// runDemo 装配 executor/scheduler，注册三类任务，立即手动触发两次固定
// 会话任务（展示上下文累积），再等待 at/every 自然触发。
func runDemo(config demoConfig) (*demoResult, error) {
	log := &runLog{}
	shared := instrumentedAgent(config.streamFn, log)
	executor := tasks.NewAgentExecutor(tasks.AgentExecutorOptions{
		NewAgent:  func() *agent.Agent { return instrumentedAgent(config.streamFn, log) },
		LoadAgent: func(string) (*agent.Agent, error) { return shared, nil },
	})
	scheduler, err := tasks.NewScheduler(tasks.SchedulerOptions{
		Store:    config.store,
		Executor: executor,
		MinEvery: config.minEvery,
	})
	if err != nil {
		return nil, err
	}
	if config.onEvent != nil {
		dispose := scheduler.OnEvent(config.onEvent)
		defer dispose()
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := scheduler.Start(ctx); err != nil {
		cancel()
		return nil, err
	}

	register := func(spec tasks.TaskSpec) {
		if err := scheduler.Register(spec); err != nil {
			fmt.Fprintf(os.Stderr, "注册任务 %s 失败: %v\n", spec.ID, err)
			os.Exit(1)
		}
	}
	register(tasks.TaskSpec{
		ID: "heartbeat", Schedule: tasks.Every{Interval: config.every, Anchor: time.Now()},
		Prompt: "巡检：报告当前会话的消息计数", Enabled: true,
	})
	weekly, err := tasks.ParseSchedule("0 9 * * 1")
	if err != nil {
		cancel()
		return nil, err
	}
	register(tasks.TaskSpec{
		ID: "weekly", Schedule: weekly, SessionStrategy: tasks.StrategyFixed,
		FixedSessionID: fixedSessionID, Prompt: "生成周报：确认你能看到本会话的历史消息", Enabled: true,
	})
	register(tasks.TaskSpec{
		ID: "once", Schedule: tasks.NewAt(time.Now().Add(config.atDelay)),
		Prompt: "一次性提醒：演示 at 调度", Enabled: true,
	})

	// 手动触发两次固定会话任务：第二次应看到累积后的消息计数。
	for i := 1; i <= 2; i++ {
		run, err := scheduler.RunNow("weekly")
		if err != nil {
			cancel()
			return nil, err
		}
		fmt.Printf("手动触发 weekly #%d: %s\n", i, run.Status)
	}

	time.Sleep(config.runFor)
	return &demoResult{scheduler: scheduler, log: log, stop: func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = scheduler.Stop(stopCtx)
		cancel()
	}}, nil
}

// instrumentedAgent 建一个带运行记录的 agent：每轮结束把最后一条助手
// 回复与会话 id 写入日志。
func instrumentedAgent(stream agent.StreamFn, log *runLog) *agent.Agent {
	ag := agent.NewAgent(agent.AgentOptions{StreamFn: stream})
	ag.Subscribe(func(event agent.AgentEvent, _ *abort.Signal) {
		end, ok := event.(*agent.EventAgentEnd)
		if !ok {
			return
		}
		reply := ""
		for _, message := range end.Messages {
			if assistant, ok := message.(*ai.AssistantMessage); ok {
				for _, block := range assistant.Content {
					if text, ok := block.(ai.TextContent); ok && text.Text != "" {
						reply = text.Text
					}
				}
			}
		}
		sessionID := ""
		if ag.SessionID != nil {
			sessionID = *ag.SessionID
		}
		log.add(sessionID, reply)
	})
	return ag
}

// echoResponder 回复本会话当前的用户消息计数：fixed 策略下该计数逐次
// 增长，new 策略下恒为 1。
func echoResponder() agent.StreamFn {
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

// realResponder 复用 examples/coding_agent 的真实模型接线。
func realResponder() agent.StreamFn {
	models := ai.CreateModels()
	models.SetProvider(ai.ZaiCodingCnProvider())
	model := models.GetModel("zai-coding-cn", "glm-5.3")
	return func(requested *ai.Model, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		return models.StreamSimple(model, ai.Context{Messages: transcript.Messages}, options)
	}
}
