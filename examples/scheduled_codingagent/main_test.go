package main

// scheduled_codingagent 自测：验证组合缝装配（CustomTools 挂载 task_manage、
// fixed 策略绑定主会话）以及定时注入与用户输入的串行执行。不经网络、
// 不经 TUI、不经模型。

import (
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/ai"
)

func TestAssemblyExposesTaskManageTool(t *testing.T) {
	session, scheduler, err := assemble(20 * time.Millisecond)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	_ = scheduler
	tools := session.GetAllTools()
	found := false
	for _, name := range tools {
		if name == "task_manage" {
			found = true
		}
	}
	if !found {
		t.Fatalf("task_manage 未注册到会话工具列表: %v", tools)
	}
	if session.Agent == nil || session.Agent.SessionID == nil || *session.Agent.SessionID != fixedSessionID {
		t.Fatal("会话未使用调用方构造并绑定调度 id 的 agent")
	}
}

func TestScheduledInjectionsSerializeWithUserTurns(t *testing.T) {
	result, err := runDemo(demoConfig{
		every:     80 * time.Millisecond,
		minEvery:  20 * time.Millisecond,
		runFor:    450 * time.Millisecond,
		userAfter: 120 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("runDemo: %v", err)
	}
	defer result.stop()

	waitFor(t, "多次巡检注入", func() bool {
		for _, status := range result.scheduler.List() {
			if status.ID == "inspect" && status.RunCount >= 3 {
				return true
			}
		}
		return false
	})

	users := 0
	scheduled := 0
	failed := 0
	for _, message := range result.session.Agent.Messages() {
		switch typed := message.(type) {
		case *ai.UserMessage:
			users++
			if strings.Contains(preview(typed), "巡检") {
				scheduled++
			}
		case *ai.AssistantMessage:
			if typed.ErrorMessage != nil {
				failed++
			}
		}
	}
	if scheduled < 3 {
		t.Fatalf("调度注入 %d 次, want >= 3", scheduled)
	}
	if users < 4 {
		t.Fatalf("用户消息合计 %d（含 1 条手动模拟）, want >= 4", users)
	}
	if failed > 0 {
		t.Fatalf("出现 %d 条错误助手消息（串行注入被拒绝）", failed)
	}
	for _, status := range result.scheduler.List() {
		if status.ID == "inspect" && status.LastRun != nil && status.LastRun.SessionID != fixedSessionID {
			t.Fatalf("巡检运行落在会话 %s, want %s", status.LastRun.SessionID, fixedSessionID)
		}
	}
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
