package tasks

// executor_pipeline_test.go: PipelineExecutor behavior over a fake
// host pipeline — pass-through mapping, submit failure, deadline
// cancel with timeout classification, cancellation, grace drain, and
// the closed-channel guard.

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakePipeline stands in for a host runtime: Submit lands the fire on
// an in-memory pipeline whose terminal outcome and cancel behavior are
// scripted per test.
type fakePipeline struct {
	submit func(fire Fire) (PipelineSubmission, error)
}

func (f *fakePipeline) Submit(fire Fire) (PipelineSubmission, error) {
	return f.submit(fire)
}

// immediatePipeline delivers result on Submit without cancel.
func immediatePipeline(result RunResult) *fakePipeline {
	return &fakePipeline{submit: func(Fire) (PipelineSubmission, error) {
		done := make(chan RunResult, 1)
		done <- result
		return PipelineSubmission{SessionID: "sess-1", Done: done}, nil
	}}
}

func TestPipelineExecutorPassesThroughSuccess(t *testing.T) {
	executor := NewPipelineExecutor(immediatePipeline(
		RunResult{Status: RunStatusSuccess, Tokens: 42, SessionID: "host-sess"},
	), PipelineExecutorOptions{})
	result := executor.Run(context.Background(), Fire{TaskID: "t", Prompt: "q"})
	if result.Status != RunStatusSuccess || result.Tokens != 42 {
		t.Fatalf("result: %+v", result)
	}
	// The submission's session id is authoritative when set.
	if result.SessionID != "sess-1" {
		t.Fatalf("session id %q, want sess-1", result.SessionID)
	}
}

func TestPipelineExecutorKeepsHostSessionWhenSubmissionOmitsIt(t *testing.T) {
	executor := NewPipelineExecutor(&fakePipeline{submit: func(Fire) (PipelineSubmission, error) {
		done := make(chan RunResult, 1)
		done <- RunResult{Status: RunStatusSuccess, SessionID: "host-sess"}
		return PipelineSubmission{Done: done}, nil
	}}, PipelineExecutorOptions{})
	result := executor.Run(context.Background(), Fire{TaskID: "t"})
	if result.SessionID != "host-sess" {
		t.Fatalf("session id %q, want host-sess", result.SessionID)
	}
}

func TestPipelineExecutorPassesThroughFailure(t *testing.T) {
	executor := NewPipelineExecutor(immediatePipeline(
		RunResult{Status: RunStatusFailed, Err: "host turn failed"},
	), PipelineExecutorOptions{})
	result := executor.Run(context.Background(), Fire{TaskID: "t"})
	if result.Status != RunStatusFailed || result.Err != "host turn failed" {
		t.Fatalf("result: %+v", result)
	}
}

func TestPipelineExecutorSubmitErrorFailsRun(t *testing.T) {
	executor := NewPipelineExecutor(&fakePipeline{submit: func(Fire) (PipelineSubmission, error) {
		return PipelineSubmission{}, context.DeadlineExceeded
	}}, PipelineExecutorOptions{})
	result := executor.Run(context.Background(), Fire{TaskID: "t"})
	if result.Status != RunStatusFailed || !strings.Contains(result.Err, "submit fire") {
		t.Fatalf("result: %+v", result)
	}
}

func TestPipelineExecutorNilSubmitterFailsLoud(t *testing.T) {
	executor := NewPipelineExecutor(nil, PipelineExecutorOptions{})
	result := executor.Run(context.Background(), Fire{TaskID: "t"})
	if result.Status != RunStatusFailed || !strings.Contains(result.Err, "submitter not configured") {
		t.Fatalf("result: %+v", result)
	}
}

func TestPipelineExecutorClosedDoneFailsRun(t *testing.T) {
	executor := NewPipelineExecutor(&fakePipeline{submit: func(Fire) (PipelineSubmission, error) {
		done := make(chan RunResult)
		close(done)
		return PipelineSubmission{Done: done}, nil
	}}, PipelineExecutorOptions{})
	result := executor.Run(context.Background(), Fire{TaskID: "t"})
	if result.Status != RunStatusFailed || !strings.Contains(result.Err, "closed without a result") {
		t.Fatalf("result: %+v", result)
	}
}

func TestPipelineExecutorDeadlineCancelsAndClassifiesTimeout(t *testing.T) {
	var cancels atomic.Int32
	terminal := make(chan RunResult, 1)
	var cancelled sync.WaitGroup
	cancelled.Add(1)
	executor := NewPipelineExecutor(&fakePipeline{submit: func(Fire) (PipelineSubmission, error) {
		return PipelineSubmission{
			SessionID: "sess-1",
			Done:      terminal,
			Cancel: func() {
				cancels.Add(1)
				// The host maps its aborted turn onto interrupted; the
				// executor must still classify the deadline as a timeout.
				terminal <- RunResult{Status: RunStatusInterrupted, SessionID: "sess-1"}
				cancelled.Done()
			},
		}, nil
	}}, PipelineExecutorOptions{CancelGrace: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	result := executor.Run(ctx, Fire{TaskID: "t"})
	if result.Status != RunStatusFailed || !strings.Contains(result.Err, "timeout after") {
		t.Fatalf("result: %+v", result)
	}
	if result.SessionID != "sess-1" {
		t.Fatalf("session id %q, want sess-1", result.SessionID)
	}
	if cancels.Load() != 1 {
		t.Fatalf("cancel called %d times, want 1", cancels.Load())
	}
}

func TestPipelineExecutorCancelClassifiesInterrupted(t *testing.T) {
	terminal := make(chan RunResult, 1)
	executor := NewPipelineExecutor(&fakePipeline{submit: func(Fire) (PipelineSubmission, error) {
		return PipelineSubmission{
			Done:   terminal,
			Cancel: func() { terminal <- RunResult{Status: RunStatusFailed, Err: "cancelled by host"} },
		}, nil
	}}, PipelineExecutorOptions{CancelGrace: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	result := executor.Run(ctx, Fire{TaskID: "t"})
	if result.Status != RunStatusInterrupted || result.Err != "run cancelled" {
		t.Fatalf("result: %+v", result)
	}
}

func TestPipelineExecutorGraceBoundsStuckPipeline(t *testing.T) {
	// Cancel never unblocks Done: the executor must report within the
	// grace window instead of hanging on the host.
	terminal := make(chan RunResult)
	executor := NewPipelineExecutor(&fakePipeline{submit: func(Fire) (PipelineSubmission, error) {
		return PipelineSubmission{Done: terminal, Cancel: func() {}}, nil
	}}, PipelineExecutorOptions{CancelGrace: 80 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	result := executor.Run(ctx, Fire{TaskID: "t"})
	elapsed := time.Since(start)
	if result.Status != RunStatusFailed || !strings.Contains(result.Err, "timeout after") {
		t.Fatalf("result: %+v", result)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("executor returned after %s; CancelGrace not bounded", elapsed)
	}
}

func TestPipelineExecutorSuccessPathNeverCancels(t *testing.T) {
	var cancels atomic.Int32
	done := make(chan RunResult, 1)
	done <- RunResult{Status: RunStatusSuccess}
	executor := NewPipelineExecutor(&fakePipeline{submit: func(Fire) (PipelineSubmission, error) {
		return PipelineSubmission{Done: done, Cancel: func() { cancels.Add(1) }}, nil
	}}, PipelineExecutorOptions{})
	if result := executor.Run(context.Background(), Fire{TaskID: "t"}); result.Status != RunStatusSuccess {
		t.Fatalf("result: %+v", result)
	}
	if cancels.Load() != 0 {
		t.Fatalf("cancel called %d times on the success path, want 0", cancels.Load())
	}
}

func TestPipelineExecutorServesScheduler(t *testing.T) {
	// The adapter plugs into the scheduler like any Executor: a fire
	// runs through the host pipeline and lands a success run record.
	store := NewMemoryStore()
	pipeline := immediatePipeline(RunResult{Status: RunStatusSuccess, Tokens: 7, SessionID: "sess-1"})
	executor := NewPipelineExecutor(pipeline, PipelineExecutorOptions{})
	scheduler, _ := newTestScheduler(t, store, executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if err := scheduler.Register(TaskSpec{ID: "once", Prompt: "ping", Enabled: true,
		Schedule: At{Time: time.Now().Add(30 * time.Millisecond)}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	waitFor(t, "run to finish", func() bool {
		status, err := scheduler.Get("once")
		return err == nil && status.LastRun != nil && status.LastRun.Status.Terminal()
	})
	status, err := scheduler.Get("once")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if status.LastRun.Status != RunStatusSuccess || status.LastRun.SessionID != "sess-1" {
		t.Fatalf("last run: %+v", status.LastRun)
	}
}
