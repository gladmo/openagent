package main

// scheduled_pipeline 自测：用收紧的 MinEvery 与毫秒级间隔驱动与 main
// 相同的装配，验证 fixed 策略在宿主队列上串行累积、new 策略会话隔离、
// 超时取消分类穿透宿主结果、fixed 目标缺失响亮失败。不经网络与模型。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/agent/harness/tasks"
)

func TestPipelineFlowFixedSerializesAndNewIsolates(t *testing.T) {
	result, err := runDemo(demoConfig{
		store:     tasks.NewMemoryStore(),
		every:     80 * time.Millisecond,
		minEvery:  20 * time.Millisecond,
		atDelay:   120 * time.Millisecond,
		turnDelay: 5 * time.Millisecond,
		slowDelay: 5 * time.Millisecond,
		runFor:    400 * time.Millisecond,
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

	entries := result.host.snapshot()
	if len(entries) < 4 {
		t.Fatalf("回合记录 %d 条, want >= 4", len(entries))
	}
	// 前两条来自 weekly 的两次同步 RunNow：同一会话，轮次递增（宿主
	// 队列串行的直接证据）。
	first, second := entries[0], entries[1]
	if first.sessionID != second.sessionID || second.sessionID != "weekly-report" {
		t.Fatalf("固定会话在两次触发间漂移: %+v %+v", first, second)
	}
	if first.turn != 1 || second.turn != 2 {
		t.Fatalf("固定会话轮次应递增: %d -> %d", first.turn, second.turn)
	}

	// 其余记录来自 once/heartbeat（new 策略）：会话互不相同且都不复用
	// 固定会话，每会话只有 1 轮。
	seen := map[string]bool{}
	for _, entry := range entries[2:] {
		if entry.sessionID == "weekly-report" || seen[entry.sessionID] {
			t.Fatalf("new 策略会话复用: %+v", entry)
		}
		seen[entry.sessionID] = true
		if entry.turn != 1 {
			t.Fatalf("new 策略会话应从零开始, got 第 %d 轮", entry.turn)
		}
	}
}

func TestPipelineTimeoutClassificationBeatsHostResult(t *testing.T) {
	// slow 回合 60ms 超过任务 Timeout 20ms：宿主最终投递 success，但
	// 终态必须是 PipelineExecutor 的超时失败。
	result, err := runDemo(demoConfig{
		store:     tasks.NewMemoryStore(),
		every:     time.Hour,
		minEvery:  20 * time.Millisecond,
		atDelay:   time.Hour,
		turnDelay: 5 * time.Millisecond,
		slowDelay: 60 * time.Millisecond,
		runFor:    300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("runDemo: %v", err)
	}
	defer result.stop()
	waitFor(t, "slow 任务终态", func() bool {
		status, err := result.scheduler.Get("slow")
		return err == nil && status.LastRun != nil && status.LastRun.Status.Terminal()
	})
	status, err := result.scheduler.Get("slow")
	if err != nil {
		t.Fatalf("Get(slow): %v", err)
	}
	if status.LastRun.Status != tasks.RunStatusFailed || !strings.Contains(status.LastRun.Err, "timeout after") {
		t.Fatalf("slow 终态: %+v, want failed/timeout", status.LastRun)
	}
}

func TestPipelineFixedTargetMissingFailsLoud(t *testing.T) {
	// 未预建 weekly-report 会话：直接对宿主 Submit，验证缺失目标的
	// 响亮失败直达 RunResult。
	host := newHostRuntime(5*time.Millisecond, 5*time.Millisecond)
	executor := tasks.NewPipelineExecutor(host, tasks.PipelineExecutorOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := executor.Run(ctx, tasks.Fire{
		TaskID: "t", SessionStrategy: tasks.StrategyFixed, FixedSessionID: "nope",
	})
	if result.Status != tasks.RunStatusFailed || !strings.Contains(result.Err, "fixed session") {
		t.Fatalf("result: %+v", result)
	}
	host.Close()
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
