package tasks

// executor_test.go: AgentExecutor behavior over a real agent.Agent with
// a scripted provider stream — session isolation, accumulation, busy
// policies, timeout abort, and failure/token mapping.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

func TestAgentExecutorNewSessionIsolated(t *testing.T) {
	stream := scriptedStreamFn(testAssistant("hello", ai.Usage{Input: 10, Output: 5}))
	executor := NewAgentExecutor(AgentExecutorOptions{
		NewAgent: func() *agent.Agent { return newTestAgent(stream) },
	})
	first := executor.Run(context.Background(), Fire{TaskID: "t", Prompt: "q1", SessionStrategy: StrategyNew})
	second := executor.Run(context.Background(), Fire{TaskID: "t", Prompt: "q2", SessionStrategy: StrategyNew})
	if first.Status != RunStatusSuccess || second.Status != RunStatusSuccess {
		t.Fatalf("statuses: %+v %+v", first, second)
	}
	if first.SessionID == "" || second.SessionID == "" || first.SessionID == second.SessionID {
		t.Fatalf("session ids: %q vs %q", first.SessionID, second.SessionID)
	}
	if first.Tokens != 15 {
		t.Fatalf("tokens %d, want 15", first.Tokens)
	}
}

func TestAgentExecutorFixedSessionAccumulates(t *testing.T) {
	shared := newTestAgent(scriptedStreamFn(testAssistant("ok", ai.Usage{})))
	executor := NewAgentExecutor(AgentExecutorOptions{
		NewAgent:  func() *agent.Agent { return newTestAgent(scriptedStreamFn(testAssistant("fresh", ai.Usage{}))) },
		LoadAgent: func(sessionID string) (*agent.Agent, error) { return shared, nil },
	})
	fire := Fire{TaskID: "t", Prompt: "again", SessionStrategy: StrategyFixed, FixedSessionID: "sess-1"}
	first := executor.Run(context.Background(), fire)
	second := executor.Run(context.Background(), fire)
	if first.Status != RunStatusSuccess || second.Status != RunStatusSuccess {
		t.Fatalf("statuses: %+v %+v", first, second)
	}
	users := 0
	for _, message := range shared.Messages() {
		user, ok := message.(*ai.UserMessage)
		if !ok {
			continue
		}
		for _, block := range user.Content.Blocks {
			if text, ok := block.(ai.TextContent); ok && text.Text == "again" {
				users++
			}
		}
	}
	if users != 2 {
		t.Fatalf("fixed session saw %d injected prompts, want 2", users)
	}
	if second.SessionID != "sess-1" {
		t.Fatalf("session id %q, want sess-1", second.SessionID)
	}
}

func TestAgentExecutorBusySkip(t *testing.T) {
	release := make(chan struct{})
	shared := newTestAgent(blockingStreamFn(release))
	go func() { _ = shared.PromptText("long") }()
	waitFor(t, "shared agent to start streaming", shared.IsStreaming)
	defer close(release)

	executor := NewAgentExecutor(AgentExecutorOptions{
		LoadAgent:  func(string) (*agent.Agent, error) { return shared, nil },
		BusyPolicy: BusySkip,
	})
	result := executor.Run(context.Background(), Fire{TaskID: "t", Prompt: "x", SessionStrategy: StrategyFixed, FixedSessionID: "s"})
	if result.Status != RunStatusSkipped || result.SkipReason == "" {
		t.Fatalf("result: %+v", result)
	}
}

func TestAgentExecutorBusyWaitSerializes(t *testing.T) {
	release := make(chan struct{})
	shared := newTestAgent(blockingStreamFn(release))
	go func() { _ = shared.PromptText("long") }()
	waitFor(t, "shared agent to start streaming", shared.IsStreaming)

	go func() {
		time.Sleep(150 * time.Millisecond)
		close(release)
	}()
	executor := NewAgentExecutor(AgentExecutorOptions{
		LoadAgent:  func(string) (*agent.Agent, error) { return shared, nil },
		BusyPolicy: BusyWait,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	result := executor.Run(ctx, Fire{TaskID: "t", Prompt: "queued", SessionStrategy: StrategyFixed, FixedSessionID: "s"})
	if result.Status != RunStatusSuccess {
		t.Fatalf("result: %+v", result)
	}
}

func TestAgentExecutorTimeoutAbortsAndFails(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	shared := newTestAgent(blockingStreamFn(release))
	executor := NewAgentExecutor(AgentExecutorOptions{
		LoadAgent:   func(string) (*agent.Agent, error) { return shared, nil },
		CancelGrace: 200 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	result := executor.Run(ctx, Fire{TaskID: "t", Prompt: "slow", SessionStrategy: StrategyFixed, FixedSessionID: "s"})
	elapsed := time.Since(start)
	if result.Status != RunStatusFailed || !strings.Contains(result.Err, "timeout") {
		t.Fatalf("result: %+v", result)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("executor returned after %s; CancelGrace not honored", elapsed)
	}
	waitFor(t, "aborted agent to wind down", func() bool { return !shared.IsStreaming() })
}

func TestAgentExecutorMapsLoopFailure(t *testing.T) {
	failure := testAssistant("", ai.Usage{})
	errorText := "provider exploded"
	failure.StopReason = ai.StopError
	failure.ErrorMessage = &errorText
	executor := NewAgentExecutor(AgentExecutorOptions{
		NewAgent: func() *agent.Agent { return newTestAgent(scriptedStreamFn(failure)) },
	})
	result := executor.Run(context.Background(), Fire{TaskID: "t", Prompt: "q", SessionStrategy: StrategyNew})
	if result.Status != RunStatusFailed || result.Err != errorText {
		t.Fatalf("result: %+v", result)
	}
}

func TestAgentExecutorMissingFactoriesFailLoud(t *testing.T) {
	noFactory := NewAgentExecutor(AgentExecutorOptions{})
	if result := noFactory.Run(context.Background(), Fire{SessionStrategy: StrategyNew}); result.Status != RunStatusFailed {
		t.Fatalf("new without factory: %+v", result)
	}
	if result := noFactory.Run(context.Background(), Fire{SessionStrategy: StrategyFixed, FixedSessionID: "s"}); result.Status != RunStatusFailed {
		t.Fatalf("fixed without loader: %+v", result)
	}
}

func TestAgentExecutorLoadAgentFailureFailsRun(t *testing.T) {
	executor := NewAgentExecutor(AgentExecutorOptions{
		LoadAgent: func(string) (*agent.Agent, error) { return nil, context.DeadlineExceeded },
	})
	result := executor.Run(context.Background(), Fire{SessionStrategy: StrategyFixed, FixedSessionID: "s"})
	if result.Status != RunStatusFailed || !strings.Contains(result.Err, "load fixed session") {
		t.Fatalf("result: %+v", result)
	}
}
