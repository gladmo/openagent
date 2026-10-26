package main

// scheduled_agent 自测：用收紧的 MinEvery 与毫秒级间隔驱动与 main 相同
// 的装配，验证 fixed 策略上下文累积、new 策略会话隔离、at 一次性触发
// 与 task_manage 工具挂载。不经网络与模型。

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/agent/harness/tasks"
	"github.com/gladmo/openagent/jsonx"
)

func TestScheduledFlowFixedAccumulatesAndNewIsolates(t *testing.T) {
	result, err := runDemo(demoConfig{
		store:    tasks.NewMemoryStore(),
		streamFn: echoResponder(),
		every:    80 * time.Millisecond,
		minEvery: 20 * time.Millisecond,
		atDelay:  120 * time.Millisecond,
		runFor:   400 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("runDemo: %v", err)
	}
	defer result.stop()

	waitFor(t, "at 与 every 任务至少各触发一次", func() bool {
		once, errOnce := result.scheduler.Get("once")
		heartbeat, errHeartbeat := result.scheduler.Get("heartbeat")
		return errOnce == nil && errHeartbeat == nil &&
			once.RunCount >= 1 && heartbeat.RunCount >= 2
	})

	entries := result.log.snapshot()
	if len(entries) < 4 {
		t.Fatalf("运行记录 %d 条, want >= 4", len(entries))
	}
	// 前两条来自 weekly 的两次同步 RunNow：同一会话，消息计数递增。
	first, second := entries[0], entries[1]
	if first.sessionID != second.sessionID {
		t.Fatalf("固定会话在两次触发间漂移: %s -> %s", first.sessionID, second.sessionID)
	}
	if got := parseUserCount(t, first.reply); got != 1 {
		t.Fatalf("weekly 第一次计数 %d, want 1 (%q)", got, first.reply)
	}
	if got := parseUserCount(t, second.reply); got != 2 {
		t.Fatalf("weekly 第二次计数 %d, want 2（fixed 未累积）(%q)", got, second.reply)
	}

	// 其余记录来自 once/heartbeat（new 策略）：会话互不相同且都不复用
	// 固定会话，每会话只有 1 条用户消息。
	weeklySession := first.sessionID
	seen := map[string]bool{}
	for _, entry := range entries[2:] {
		if entry.sessionID == weeklySession || seen[entry.sessionID] {
			t.Fatalf("new 策略会话复用: %+v", entry)
		}
		seen[entry.sessionID] = true
		if got := parseUserCount(t, entry.reply); got != 1 {
			t.Fatalf("new 策略会话应从零开始, got %d (%q)", got, entry.reply)
		}
	}
}

func parseUserCount(t *testing.T, reply string) int {
	t.Helper()
	start := strings.IndexFunc(reply, func(r rune) bool { return r >= '0' && r <= '9' })
	if start < 0 {
		t.Fatalf("回复中无计数: %q", reply)
	}
	end := start
	for end < len(reply) && reply[end] >= '0' && reply[end] <= '9' {
		end++
	}
	value, err := strconv.Atoi(reply[start:end])
	if err != nil {
		t.Fatalf("解析计数 %q: %v", reply[start:end], err)
	}
	return value
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTaskManageToolExecutesThroughDemoScheduler(t *testing.T) {
	result, err := runDemo(demoConfig{
		store:    tasks.NewMemoryStore(),
		streamFn: echoResponder(),
		every:    time.Hour,
		minEvery: 20 * time.Millisecond,
		atDelay:  time.Hour,
		runFor:   10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("runDemo: %v", err)
	}
	defer result.stop()

	tool := tasks.TaskManageTool(result.scheduler)
	task := jsonx.NewObj()
	task.Set("id", "nl-created")
	task.Set("schedule", "@daily")
	task.Set("prompt", "通过 task_manage 创建的任务")
	params := jsonx.NewObj()
	params.Set("action", "create")
	params.Set("task", task)
	toolResult, err := tool.Execute("call-1", any(params), nil, nil)
	if err != nil {
		t.Fatalf("task_manage create: %v", err)
	}
	if toolResult == nil || len(toolResult.Content) == 0 {
		t.Fatal("task_manage 结果为空")
	}
	if _, err := result.scheduler.Get("nl-created"); err != nil {
		t.Fatalf("task_manage 创建的任务不可见: %v", err)
	}
}
