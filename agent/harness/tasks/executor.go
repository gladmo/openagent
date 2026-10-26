package tasks

// executor.go: the seam between the scheduler and the agent framework.
// The scheduler only knows Fire→RunResult; AgentExecutor is the default
// adapter driving one full agent run per fire without touching the
// agent loop internals.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// Fire is one trigger event handed to an Executor.
type Fire struct {
	TaskID          string
	TaskName        string
	Prompt          string
	Trigger         string // TriggerSchedule or TriggerManual
	FireTime        time.Time
	SessionStrategy string
	FixedSessionID  string
}

// RunResult reports one executed (or refused) fire.
type RunResult struct {
	Status     RunStatus
	Err        string
	SkipReason string
	Tokens     int64
	SessionID  string
}

// Executor runs one fire to completion. Run must respect ctx
// cancellation (aborting any underlying agent work) and must not panic;
// long blocking belongs inside the run, not in queueing.
type Executor interface {
	Run(ctx context.Context, fire Fire) RunResult
}

// BusyPolicy values for fixed sessions whose agent is mid-run.
const (
	// BusyWait waits for the session to go idle (bounded by the run
	// context), serializing injections on that session.
	BusyWait = "wait"
	// BusySkip refuses immediately with a skipped result.
	BusySkip = "skip"
)

// AgentExecutorOptions configure AgentExecutor. NewAgent backs the new
// session strategy; LoadAgent restores a fixed-session agent after a
// restart (agents are cached per session for the process lifetime).
type AgentExecutorOptions struct {
	NewAgent   func() *agent.Agent
	LoadAgent  func(sessionID string) (*agent.Agent, error)
	BusyPolicy string
	// CancelGrace bounds the wait for the agent loop to unwind after
	// Abort before reporting anyway (default 10s).
	CancelGrace time.Duration
}

// AgentExecutor executes fires against agent.Agent instances.
type AgentExecutor struct {
	options    AgentExecutorOptions
	mu         sync.Mutex
	fixedCache map[string]*agent.Agent
}

// NewAgentExecutor resolves defaults (busy=wait, grace=10s) and builds
// the executor.
func NewAgentExecutor(options AgentExecutorOptions) *AgentExecutor {
	if options.BusyPolicy == "" {
		options.BusyPolicy = BusyWait
	}
	if options.CancelGrace <= 0 {
		options.CancelGrace = 10 * time.Second
	}
	return &AgentExecutor{options: options, fixedCache: map[string]*agent.Agent{}}
}

// Run resolves the target agent per session strategy and drives one
// prompt to completion.
func (e *AgentExecutor) Run(ctx context.Context, fire Fire) RunResult {
	target, sessionID, result := e.resolveAgent(fire)
	if target == nil {
		return result
	}
	if busy := e.awaitIdle(ctx, target); busy != nil {
		return *busy
	}
	started := time.Now()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("executor panic: %v", r)
			}
		}()
		done <- target.PromptText(fire.Prompt)
	}()
	select {
	case err := <-done:
		return e.mapCompletion(target, sessionID, err)
	case <-ctx.Done():
		target.Abort()
		grace := e.options.CancelGrace
		select {
		case <-done:
		case <-time.After(grace):
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return RunResult{Status: RunStatusFailed, Err: fmt.Sprintf("timeout after %s", time.Since(started).Round(time.Millisecond)), SessionID: sessionID}
		}
		return RunResult{Status: RunStatusInterrupted, Err: "run cancelled", SessionID: sessionID}
	}
}

// resolveAgent picks the agent for the fire; a nil agent return with a
// non-success status is a refusal.
func (e *AgentExecutor) resolveAgent(fire Fire) (*agent.Agent, string, RunResult) {
	switch fire.SessionStrategy {
	case StrategyNew:
		if e.options.NewAgent == nil {
			return nil, "", RunResult{Status: RunStatusFailed, Err: "new-session agent factory not configured"}
		}
		created := e.options.NewAgent()
		sessionID := ai.UUIDv7()
		created.SessionID = &sessionID
		return created, sessionID, RunResult{Status: RunStatusSuccess}
	case StrategyFixed:
		if fire.FixedSessionID == "" {
			return nil, "", RunResult{Status: RunStatusFailed, Err: "fixed session strategy without a session id"}
		}
		e.mu.Lock()
		cached, ok := e.fixedCache[fire.FixedSessionID]
		e.mu.Unlock()
		if ok {
			return cached, fire.FixedSessionID, RunResult{Status: RunStatusSuccess}
		}
		if e.options.LoadAgent == nil {
			return nil, "", RunResult{Status: RunStatusFailed, Err: fmt.Sprintf("fixed session %q has no live agent and no loader is configured", fire.FixedSessionID)}
		}
		loaded, err := e.options.LoadAgent(fire.FixedSessionID)
		if err != nil {
			return nil, "", RunResult{Status: RunStatusFailed, Err: fmt.Sprintf("load fixed session %q: %v", fire.FixedSessionID, err)}
		}
		e.mu.Lock()
		e.fixedCache[fire.FixedSessionID] = loaded
		e.mu.Unlock()
		return loaded, fire.FixedSessionID, RunResult{Status: RunStatusSuccess}
	default:
		return nil, "", RunResult{Status: RunStatusFailed, Err: fmt.Sprintf("unknown session strategy %q", fire.SessionStrategy)}
	}
}

// awaitIdle serializes same-session injections: with BusyWait it blocks
// until the agent stops streaming (bounded by ctx); with BusySkip it
// refuses while busy. A nil return means ready to prompt.
func (e *AgentExecutor) awaitIdle(ctx context.Context, target *agent.Agent) *RunResult {
	check := func() bool { return target.IsStreaming() }
	if !check() {
		return nil
	}
	if e.options.BusyPolicy == BusySkip {
		return &RunResult{Status: RunStatusSkipped, SkipReason: "target session busy with another run"}
	}
	for check() {
		target.WaitForIdle()
		if !check() {
			return nil
		}
		select {
		case <-ctx.Done():
			reason := RunResult{Status: RunStatusFailed, Err: "timeout waiting for session to become idle", SessionID: sessionIDOf(target)}
			if errors.Is(ctx.Err(), context.Canceled) {
				reason.Status = RunStatusInterrupted
			}
			return &reason
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}

// sessionIDOf reads the agent's session id without nil risk.
func sessionIDOf(target *agent.Agent) string {
	if target.SessionID == nil {
		return ""
	}
	return *target.SessionID
}

// mapCompletion translates the agent loop outcome (whose failures land
// in a synthetic assistant message) into a RunResult.
func (e *AgentExecutor) mapCompletion(target *agent.Agent, sessionID string, promptErr error) RunResult {
	if promptErr != nil {
		return RunResult{Status: RunStatusFailed, Err: promptErr.Error(), SessionID: sessionID}
	}
	status := RunStatusSuccess
	errText := ""
	tokens := int64(0)
	messages := target.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		assistant, ok := messages[i].(*ai.AssistantMessage)
		if !ok {
			continue
		}
		if assistant.ErrorMessage != nil && *assistant.ErrorMessage != "" {
			status = RunStatusFailed
			errText = *assistant.ErrorMessage
		}
		tokens = usageTokens(assistant.Usage)
		break
	}
	return RunResult{Status: status, Err: errText, Tokens: tokens, SessionID: sessionID}
}

// usageTokens prefers the provider total, else sums the components.
func usageTokens(usage ai.Usage) int64 {
	total := usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	if usage.TotalTokens > total {
		total = usage.TotalTokens
	}
	return int64(total)
}
