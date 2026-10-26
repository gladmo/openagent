package tasks

// testutil_test.go: shared fakes — a scripted stream for agent.Agent,
// a fake executor, and a wait helper.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

func testAssistant(text string, usage ai.Usage) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Content:     []ai.ContentBlock{ai.TextContent{Text: text}},
		API:         "test",
		Provider:    "test",
		Model:       "mock",
		StopReason:  ai.StopStop,
		Usage:       usage,
		TimestampMs: float64(time.Now().UnixMilli()),
	}
}

// scriptedStreamFn serves the given assistant messages in order,
// repeating the last one.
func scriptedStreamFn(messages ...*ai.AssistantMessage) agent.StreamFn {
	index := 0
	return func(_ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		message := messages[0]
		if index < len(messages) {
			message = messages[index]
		}
		index++
		go stream.Push(&ai.EventDone{Reason: message.StopReason, Message: message})
		return stream
	}
}

// blockingStreamFn never finishes until released; it honors the abort
// signal like a real provider stream would.
func blockingStreamFn(release <-chan struct{}) agent.StreamFn {
	return func(_ *ai.Model, _ *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		go func() {
			select {
			case <-release:
				stream.Push(&ai.EventDone{Reason: ai.StopStop, Message: testAssistant("released", ai.Usage{})})
			case <-options.Signal.Done():
				aborted := testAssistant("", ai.Usage{})
				aborted.StopReason = ai.StopAborted
				stream.Push(&ai.EventDone{Reason: ai.StopAborted, Message: aborted})
			}
		}()
		return stream
	}
}

func newTestAgent(stream agent.StreamFn) *agent.Agent {
	return agent.NewAgent(agent.AgentOptions{StreamFn: stream})
}

// fakeExecutor records fires and delegates outcomes to behavior.
type fakeExecutor struct {
	mu       sync.Mutex
	fires    []Fire
	behavior func(ctx context.Context, fire Fire) RunResult
}

func (f *fakeExecutor) Run(ctx context.Context, fire Fire) RunResult {
	f.mu.Lock()
	f.fires = append(f.fires, fire)
	behavior := f.behavior
	f.mu.Unlock()
	if behavior != nil {
		return behavior(ctx, fire)
	}
	return RunResult{Status: RunStatusSuccess, SessionID: "s-" + fire.TaskID}
}

func (f *fakeExecutor) recorded() []Fire {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Fire(nil), f.fires...)
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newTestScheduler(t *testing.T, store Store, executor Executor, tweak func(*SchedulerOptions)) (*Scheduler, *SchedulerOptions) {
	t.Helper()
	options := SchedulerOptions{
		Store:    store,
		Executor: executor,
		Now:      time.Now,
		MinEvery: 20 * time.Millisecond,
	}
	if tweak != nil {
		tweak(&options)
	}
	scheduler, err := NewScheduler(options)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return scheduler, &options
}

func mustStart(t *testing.T, scheduler *Scheduler) {
	t.Helper()
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func stopScheduler(t *testing.T, scheduler *Scheduler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := scheduler.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
