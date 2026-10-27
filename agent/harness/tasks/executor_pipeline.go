package tasks

// executor_pipeline.go: the host-runtime adapter. PipelineSubmitter is
// the seam for runtimes that execute prompts as asynchronous,
// cancellable units of work (queue-admitted turns, job runners);
// PipelineExecutor supplies the shared wait, cancel, and context
// classification around it, mirroring AgentExecutor's semantics without
// touching the agent loop.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// PipelineSubmission is one fire already landed on the host pipeline.
type PipelineSubmission struct {
	// SessionID lands on the run record; empty keeps whatever the
	// submitted result itself carries.
	SessionID string
	// Done delivers the terminal RunResult exactly once. The status
	// must be terminal; the executor adds no further mapping of host
	// outcomes.
	Done <-chan RunResult
	// Cancel aborts the submission best-effort — queued or running —
	// causing Done to deliver. Calling it after Done delivered, or never
	// calling it, must be safe.
	Cancel func()
}

// PipelineSubmitter lands fires on a host runtime's own execution
// pipeline. Submit must not block on the run itself; session-strategy
// resolution (new/fixed) belongs to the host, which owns its sessions.
// A Submit error fails that run loud.
type PipelineSubmitter interface {
	Submit(fire Fire) (PipelineSubmission, error)
}

// PipelineExecutorOptions configure NewPipelineExecutor.
type PipelineExecutorOptions struct {
	// CancelGrace bounds the wait for Done after Cancel before the
	// outcome is reported anyway (default 10s, matching AgentExecutor):
	// the host's own terminal writes settle first; a stuck pipeline
	// never blocks the report.
	CancelGrace time.Duration
}

// PipelineExecutor executes fires through a PipelineSubmitter.
type PipelineExecutor struct {
	submitter PipelineSubmitter
	grace     time.Duration
}

// NewPipelineExecutor resolves defaults (grace=10s) and builds the
// executor. A nil submitter fails every run loud. There is no busy
// policy: host pipelines serialize runs themselves (queues), and the
// scheduler already refuses overlapping fires per task and beyond the
// global concurrency cap.
func NewPipelineExecutor(submitter PipelineSubmitter, options PipelineExecutorOptions) *PipelineExecutor {
	grace := options.CancelGrace
	if grace <= 0 {
		grace = 10 * time.Second
	}
	return &PipelineExecutor{submitter: submitter, grace: grace}
}

// Run submits the fire and waits for its terminal result. When the
// context expires first, Cancel is called, Done is drained within
// CancelGrace, and the outcome is classified from the context error —
// deadline exceeded reports failed with the elapsed timeout, plain
// cancellation reports interrupted — regardless of what the host
// pipeline then reports for the aborted run.
func (e *PipelineExecutor) Run(ctx context.Context, fire Fire) RunResult {
	if e.submitter == nil {
		return RunResult{Status: RunStatusFailed, Err: "pipeline submitter not configured"}
	}
	submission, err := e.submitter.Submit(fire)
	if err != nil {
		return RunResult{Status: RunStatusFailed, Err: fmt.Sprintf("submit fire: %v", err)}
	}
	started := time.Now()
	select {
	case result, ok := <-submission.Done:
		if !ok {
			return RunResult{Status: RunStatusFailed, Err: "pipeline closed without a result", SessionID: submission.SessionID}
		}
		if submission.SessionID != "" {
			result.SessionID = submission.SessionID
		}
		return result
	case <-ctx.Done():
		if submission.Cancel != nil {
			submission.Cancel()
		}
		e.drainWithinGrace(submission.Done)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return RunResult{
				Status:    RunStatusFailed,
				Err:       fmt.Sprintf("timeout after %s", time.Since(started).Round(time.Millisecond)),
				SessionID: submission.SessionID,
			}
		}
		return RunResult{Status: RunStatusInterrupted, Err: "run cancelled", SessionID: submission.SessionID}
	}
}

// drainWithinGrace waits for the host's terminal writes to settle after
// Cancel; the delivered value itself is unused (context classification
// wins once the context has expired).
func (e *PipelineExecutor) drainWithinGrace(done <-chan RunResult) {
	timer := time.NewTimer(e.grace)
	defer timer.Stop()
	select {
	case _, _ = <-done:
	case <-timer.C:
	}
}
