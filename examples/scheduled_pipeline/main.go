// Scheduled Pipeline —— agent/harness/tasks 宿主管线示例。
//
// 演示自带回合队列的宿主运行时如何接入调度器：实现
// tasks.PipelineSubmitter（Submit 内解析 new/fixed 会话策略、交回
// Done/Cancel），再用 tasks.NewPipelineExecutor 包装成 Executor——
// 等待、超时取消与终态分类全部来自 tasks 包，宿主零自研。
//
// 假宿主 hostRuntime 每会话一条串行队列：队列项完成时向自己的
// done channel 投递终态；fixed 会话的多次触发在队列上排队串行。
// 不经网络与模型，结果可预期。
//
// 运行：go run ./examples/scheduled_pipeline [-duration 13s] [-every 10s]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gladmo/openagent/agent/harness/tasks"
)

func main() {
	duration := flag.Duration("duration", 13*time.Second, "示例运行时长")
	every := flag.Duration("every", 10*time.Second, "心跳任务间隔（受 -min-every 约束）")
	minEvery := flag.Duration("min-every", 10*time.Second, "every 调度最小间隔（测试可调小）")
	atDelay := flag.Duration("at", 6*time.Second, "一次性任务的触发延迟")
	turnDelay := flag.Duration("turn", 30*time.Millisecond, "宿主侧每回合的模拟耗时")
	slowDelay := flag.Duration("slow-turn", 300*time.Millisecond, "slow 任务的回合耗时（演示超时取消）")
	flag.Parse()

	result, err := runDemo(demoConfig{
		store:     tasks.NewMemoryStore(),
		every:     *every,
		minEvery:  *minEvery,
		atDelay:   *atDelay,
		turnDelay: *turnDelay,
		slowDelay: *slowDelay,
		runFor:    *duration,
		onEvent:   func(event tasks.TaskEvent) { fmt.Printf("[事件] %s\n", event) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "示例失败: %v\n", err)
		os.Exit(1)
	}
	defer result.stop()

	fmt.Printf("\n── 宿主回合记录 ──\n")
	for _, entry := range result.host.snapshot() {
		fmt.Printf("会话 %s: 第 %d 轮 %s\n", entry.sessionID, entry.turn, entry.status)
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
	store     tasks.Store
	every     time.Duration
	minEvery  time.Duration
	atDelay   time.Duration
	turnDelay time.Duration
	slowDelay time.Duration
	runFor    time.Duration
	onEvent   func(tasks.TaskEvent)
}

type demoResult struct {
	scheduler *tasks.Scheduler
	host      *hostRuntime
	stop      func()
}

// runDemo 装配宿主管线与调度器，注册三类任务，手动触发两次固定会话
// 任务（队列串行、轮次递增），注册一个慢任务（回合耗时超过任务
// Timeout，演示超时取消分类），再等待 at/every 自然触发。
func runDemo(config demoConfig) (*demoResult, error) {
	host := newHostRuntime(config.turnDelay, config.slowDelay)
	executor := tasks.NewPipelineExecutor(host, tasks.PipelineExecutorOptions{})
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
		Prompt: "巡检：新会话执行一轮", Enabled: true,
	})
	register(tasks.TaskSpec{
		ID: "weekly", Schedule: mustWeeklySchedule(), SessionStrategy: tasks.StrategyFixed,
		FixedSessionID: "weekly-report", Prompt: "生成周报：追加到既有会话", Enabled: true,
	})
	register(tasks.TaskSpec{
		ID: "once", Schedule: tasks.NewAt(time.Now().Add(config.atDelay)),
		Prompt: "一次性提醒：演示 at 调度", Enabled: true,
	})

	host.ensureSession("weekly-report")

	// 手动触发两次固定会话任务：回合在同一会话队列上串行，轮次递增。
	for i := 1; i <= 2; i++ {
		run, err := scheduler.RunNow("weekly")
		if err != nil {
			cancel()
			return nil, err
		}
		fmt.Printf("手动触发 weekly #%d: %s\n", i, run.Status)
	}
	// 慢任务：回合耗时超过任务 Timeout，终态由 PipelineExecutor 分类为
	// 超时失败而非宿主的 success。
	register(tasks.TaskSpec{
		ID: "slow", Schedule: tasks.NewAt(time.Now().Add(50 * time.Millisecond)),
		Prompt: "慢任务：注定超时", Timeout: 40 * time.Millisecond, Enabled: true,
	})

	time.Sleep(config.runFor)
	return &demoResult{scheduler: scheduler, host: host, stop: func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = scheduler.Stop(stopCtx)
		host.Close()
		cancel()
	}}, nil
}

func mustWeeklySchedule() tasks.Schedule {
	schedule, err := tasks.ParseSchedule("0 9 * * 1")
	if err != nil {
		panic(err)
	}
	return schedule
}

// turnEntry 是宿主侧一轮回合的记录。
type turnEntry struct {
	sessionID string
	turn      int
	status    tasks.RunStatus
}

type turnLog struct {
	mu      sync.Mutex
	entries []turnEntry
}

func (l *turnLog) add(sessionID string, turn int, status tasks.RunStatus) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, turnEntry{sessionID: sessionID, turn: turn, status: status})
}

func (l *turnLog) snapshot() []turnEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]turnEntry(nil), l.entries...)
}

// hostRuntime 模拟自带回合队列的宿主：每会话一条串行队列与一个
// worker，队列项完成时向自己的 done channel 投递终态；Cancel 把
// 排队或运行中的项置为中止，由 worker 投递 interrupted。它实现
// tasks.PipelineSubmitter——宿主与调度器之间的全部协议就这一处。
type hostRuntime struct {
	// turnDelay 是常规回合的模拟耗时；slowDelay 是 slow 任务的回合
	// 耗时（构造期一次定值，worker 只读）。
	turnDelay time.Duration
	slowDelay time.Duration

	mu       sync.Mutex
	sessions map[string]*hostSession
	next     int
	log      turnLog
}

func newHostRuntime(turnDelay, slowDelay time.Duration) *hostRuntime {
	return &hostRuntime{turnDelay: turnDelay, slowDelay: slowDelay, sessions: map[string]*hostSession{}}
}

// ensureSession 预建一个宿主会话（fixed 策略目标）。
func (h *hostRuntime) ensureSession(id string) *hostSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessionLocked(id)
}

func (h *hostRuntime) sessionLocked(id string) *hostSession {
	session, ok := h.sessions[id]
	if !ok {
		session = &hostSession{id: id, queue: make(chan *hostTurn, 16)}
		h.sessions[id] = session
		go session.loop(h)
	}
	return session
}

// Submit 实现 tasks.PipelineSubmitter：new 策略造新会话，fixed 策略
// 解析既有会话（缺失响亮失败），回合入队即返回、不等待运行本身。
func (h *hostRuntime) Submit(fire tasks.Fire) (tasks.PipelineSubmission, error) {
	h.mu.Lock()
	var session *hostSession
	switch fire.SessionStrategy {
	case tasks.StrategyNew:
		h.next++
		session = h.sessionLocked(fmt.Sprintf("sess-%d", h.next))
	case tasks.StrategyFixed:
		existing, ok := h.sessions[fire.FixedSessionID]
		if !ok {
			h.mu.Unlock()
			return tasks.PipelineSubmission{}, fmt.Errorf("fixed session %q not found", fire.FixedSessionID)
		}
		session = existing
	default:
		h.mu.Unlock()
		return tasks.PipelineSubmission{}, fmt.Errorf("unknown session strategy %q", fire.SessionStrategy)
	}
	h.mu.Unlock()
	turn := &hostTurn{fire: fire, done: make(chan tasks.RunResult, 1)}
	session.queue <- turn
	return tasks.PipelineSubmission{
		SessionID: session.id,
		Done:      turn.done,
		Cancel:    func() { turn.aborted.Store(true) },
	}, nil
}

// Close 停止全部会话 worker。
func (h *hostRuntime) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, session := range h.sessions {
		close(session.queue)
	}
	h.sessions = map[string]*hostSession{}
}

func (h *hostRuntime) snapshot() []turnEntry { return h.log.snapshot() }

// hostSession 是宿主的一条会话队列。
type hostSession struct {
	id    string
	queue chan *hostTurn
	turns int
}

// loop 串行消费本会话的回合；被取消的回合投递 interrupted。
func (s *hostSession) loop(host *hostRuntime) {
	for turn := range s.queue {
		s.turns++
		if turn.aborted.Load() {
			turn.done <- tasks.RunResult{Status: tasks.RunStatusInterrupted, SessionID: s.id}
			host.log.add(s.id, s.turns, tasks.RunStatusInterrupted)
			continue
		}
		delay := host.turnDelay
		if turn.fire.TaskID == "slow" {
			delay = host.slowDelay
		}
		time.Sleep(delay)
		turn.done <- tasks.RunResult{Status: tasks.RunStatusSuccess, Tokens: 10, SessionID: s.id}
		host.log.add(s.id, s.turns, tasks.RunStatusSuccess)
	}
}

// hostTurn 是一个入队的回合。
type hostTurn struct {
	fire    tasks.Fire
	done    chan tasks.RunResult
	aborted atomic.Bool
}
