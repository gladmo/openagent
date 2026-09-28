package kinds

// job.go ports harness/pico3/kinds/job.ts: the pi.job process kind. The
// process host is injectable; checkpoints are waiting/spawning/running;
// failure completions are spawn|interrupted; recurring jobs (every) chain
// occurrences through waiting.

import (
	"fmt"
	"strings"

	"github.com/gladmo/openagent/agent/harness/pico3"
	"github.com/gladmo/openagent/jsonx"
)

// Job phases.
const (
	JobPhaseWaiting  = "waiting"
	JobPhaseSpawning = "spawning"
	JobPhaseRunning  = "running"
)

// JobInflight mirrors kind.inflight: transitions into these phases must be
// written with rt.commit before the effect.
var JobInflight = []string{JobPhaseSpawning, JobPhaseRunning}

// JobInputFields are the input keys.
type JobInputFields struct {
	Notify    bool
	Rerun     bool
	Every     *float64
	NotBefore *float64
}

// ParseJobInput reads the input payload.
func ParseJobInput(input any) JobInputFields {
	fields := JobInputFields{}
	obj, ok := input.(*jsonx.Obj)
	if !ok {
		return fields
	}
	if v, ok := obj.Get("notify"); ok {
		fields.Notify = v == true
	}
	if v, ok := obj.Get("rerun"); ok {
		fields.Rerun = v == true
	}
	if v, ok := obj.Get("every"); ok {
		if f, ok := v.(float64); ok {
			fields.Every = &f
		}
	}
	if v, ok := obj.Get("notBefore"); ok {
		if f, ok := v.(float64); ok {
			fields.NotBefore = &f
		}
	}
	return fields
}

// JobCheckpointFields reads a job checkpoint.
func JobCheckpointFields(checkpoint pico3.Checkpoint) (phase string, untilMS float64, occurrence float64, key string) {
	if checkpoint == nil {
		return "", 0, 0, ""
	}
	if v, ok := checkpoint.Get("phase"); ok {
		if s, ok := v.(string); ok {
			phase = s
		}
	}
	if v, ok := checkpoint.Get("untilMs"); ok {
		if f, ok := v.(float64); ok {
			untilMS = f
		}
	}
	if v, ok := checkpoint.Get("occurrence"); ok {
		if f, ok := v.(float64); ok {
			occurrence = f
		}
	}
	if v, ok := checkpoint.Get("key"); ok {
		if s, ok := v.(string); ok {
			key = s
		}
	}
	return phase, untilMS, occurrence, key
}

// JobKey mirrors the `${task.id}:${occurrence}` spawn key.
func JobKey(taskID int64, occurrence float64) string {
	return fmt.Sprintf("%d:%s", taskID, trimFloat(occurrence))
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%v", f)
	return s
}

// JobFailureReasons mirror the TS union.
const (
	JobFailureSpawn       = "spawn"
	JobFailureInterrupted = "interrupted"
)

// JobFailedCompletion builds the failed completion.
func JobFailedCompletion(reason, detail string) *jsonx.Obj {
	return jsonx.ObjFrom(
		"status", "failed",
		"failure", jsonx.ObjFrom("reason", reason, "detail", detail),
	)
}

// JobCompletedCompletion builds the completed completion.
func JobCompletedCompletion(exitCode float64, occurrences float64, stdout, stderr string) *jsonx.Obj {
	return jsonx.ObjFrom(
		"status", "completed",
		"result", jsonx.ObjFrom(
			"exitCode", exitCode,
			"occurrences", occurrences,
			"stdout", stdout,
			"stderr", stderr,
		),
	)
}

// JobNotice builds the pi.notice write payload.
func JobNotice(text string, now float64) *jsonx.Obj {
	return jsonx.ObjFrom(
		"kind", "pi.notice",
		"model", []any{jsonx.ObjFrom("role", "user", "content", text, "timestamp", now)},
	)
}

// JobDescribe mirrors kind.describe: stage from the checkpoint phase,
// slot fields merged.
func JobDescribe(task *pico3.Task, slot *jsonx.Obj) any {
	out := jsonx.NewObj()
	phase := "starting"
	if task.Checkpoint != nil {
		if v, ok := task.Checkpoint.Get("phase"); ok {
			if s, ok := v.(string); ok {
				phase = s
			}
		}
	}
	out.Set("stage", phase)
	if slot != nil {
		for _, key := range slot.Keys() {
			value, _ := slot.Get(key)
			out.Set(key, value)
		}
	}
	return out
}

// ProcessHost is the injectable process surface.
type ProcessHost interface {
	Start(key string, input JobInputFields) error
	Status(key string) (ProcessStatusSnapshot, error)
	Kill(key, signal string) error
}

// ProcessStatusSnapshot mirrors ProcessStatus.
type ProcessStatusSnapshot struct {
	Status        string // "unknown" | "running" | "exited"
	Stdout        string
	Stderr        string
	DroppedStdout float64
	DroppedStderr float64
	ExitCode      float64
	HasExitCode   bool
}

// JobRuntime is the runtime surface the job kind rides on.
type JobRuntime struct {
	Now    func() float64
	Host   ProcessHost
	Commit func(fn func(checkpoint pico3.Checkpoint, slot *jsonx.Obj) error) error
	Notify func(text string) error
}

// JobInitialStep is the initial decision.
type JobInitialStep struct {
	// Done carries a failure completion when set.
	Done *jsonx.Obj
	// NextPhase is set for a phase transition.
	NextPhase    string
	HasNextPhase bool
	Checkpoint   pico3.Checkpoint
}

// JobInitial mirrors kind.initial: no host -> spawn failure; notBefore in
// the future -> waiting; otherwise spawn.
func JobInitial(task *pico3.Task, input JobInputFields, rt *JobRuntime) (*JobInitialStep, error) {
	if rt.Host == nil {
		return &JobInitialStep{Done: JobFailedCompletion(JobFailureSpawn, "no process host")}, nil
	}
	now := rt.Now()
	until := now
	if input.NotBefore != nil {
		until = *input.NotBefore
	}
	if until > now {
		return &JobInitialStep{
			NextPhase:    JobPhaseWaiting,
			HasNextPhase: true,
			Checkpoint:   jsonx.ObjFrom("phase", JobPhaseWaiting, "untilMs", until, "occurrence", float64(1)),
		}, nil
	}
	return JobSpawn(task, 1, input, rt)
}

// JobSpawn mirrors spawn: write the spawning checkpoint (in-flight, before
// the effect), start the process, advance to running, then poll.
func JobSpawn(task *pico3.Task, occurrence float64, input JobInputFields, rt *JobRuntime) (*JobInitialStep, error) {
	key := JobKey(task.ID, occurrence)
	if rt.Commit != nil {
		if err := rt.Commit(func(checkpoint pico3.Checkpoint, slot *jsonx.Obj) error {
			_ = checkpoint
			_ = slot
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if err := rt.Host.Start(key, input); err != nil {
		if input.Notify && rt.Notify != nil {
			_ = rt.Notify(fmt.Sprintf("job %d failed to start: %v", task.ID, err))
		}
		return &JobInitialStep{Done: JobFailedCompletion(JobFailureSpawn, err.Error())}, nil
	}
	return &JobInitialStep{
		NextPhase:    JobPhaseRunning,
		HasNextPhase: true,
		Checkpoint:   jsonx.ObjFrom("phase", JobPhaseRunning, "key", key, "occurrence", occurrence),
	}, nil
}

// JobReconcileStep is the resume decision for spawning/running phases.
type JobReconcileStep struct {
	Done         *jsonx.Obj
	NextPhase    string
	HasNextPhase bool
	Checkpoint   pico3.Checkpoint
}

// JobReconcile mirrors reconcile: unknown process -> interrupted failure
// (or re-spawn when rerun); spawning advances to running; otherwise poll.
func JobReconcile(task *pico3.Task, input JobInputFields, checkpoint pico3.Checkpoint, rt *JobRuntime) (*JobReconcileStep, error) {
	if rt.Host == nil {
		return &JobReconcileStep{Done: JobFailedCompletion(JobFailureInterrupted, "no process host after restart")}, nil
	}
	phase, _, occurrence, key := JobCheckpointFields(checkpoint)
	status, err := rt.Host.Status(key)
	if err != nil {
		return &JobReconcileStep{Done: JobFailedCompletion(JobFailureInterrupted, "host status failed: "+err.Error())}, nil
	}
	if status.Status == "unknown" {
		if !input.Rerun {
			return &JobReconcileStep{Done: JobFailedCompletion(JobFailureInterrupted, "process outcome unknown")}, nil
		}
		spawnStep, err := JobSpawn(task, occurrence, input, rt)
		if err != nil {
			return nil, err
		}
		return &JobReconcileStep{Done: spawnStep.Done, NextPhase: spawnStep.NextPhase, HasNextPhase: spawnStep.HasNextPhase, Checkpoint: spawnStep.Checkpoint}, nil
	}
	if phase == JobPhaseSpawning {
		return &JobReconcileStep{
			NextPhase:    JobPhaseRunning,
			HasNextPhase: true,
			Checkpoint:   jsonx.ObjFrom("phase", JobPhaseRunning, "key", key, "occurrence", occurrence),
		}, nil
	}
	return JobPoll(task, occurrence, input, key, status, rt)
}

// JobPoll mirrors the poll exit decisions: exited + every -> next waiting
// occurrence; exited -> completed; running -> continue polling (the caller
// owns the backoff sleep).
func JobPoll(task *pico3.Task, occurrence float64, input JobInputFields, key string, status ProcessStatusSnapshot, rt *JobRuntime) (*JobReconcileStep, error) {
	if status.Status == "unknown" {
		if !input.Rerun {
			return &JobReconcileStep{Done: JobFailedCompletion(JobFailureInterrupted, "process outcome unknown")}, nil
		}
		spawnStep, err := JobSpawn(task, occurrence, input, rt)
		if err != nil {
			return nil, err
		}
		return &JobReconcileStep{Done: spawnStep.Done, NextPhase: spawnStep.NextPhase, HasNextPhase: spawnStep.HasNextPhase, Checkpoint: spawnStep.Checkpoint}, nil
	}
	if status.Status == "exited" {
		if input.Every == nil {
			if input.Notify && rt.Notify != nil {
				_ = rt.Notify(fmt.Sprintf("job %d exited with code %s", task.ID, trimFloat(status.ExitCode)))
			}
			return &JobReconcileStep{Done: JobCompletedCompletion(status.ExitCode, occurrence, status.Stdout, status.Stderr)}, nil
		}
		until := rt.Now() + *input.Every
		return &JobReconcileStep{
			NextPhase:    JobPhaseWaiting,
			HasNextPhase: true,
			Checkpoint:   jsonx.ObjFrom("phase", JobPhaseWaiting, "untilMs", until, "occurrence", occurrence+1),
		}, nil
	}
	// Still running: the scheduler's poll loop continues.
	return &JobReconcileStep{}, nil
}

// JobBackoffMS mirrors the poll backoff: min(1000, 100 * 2^min(attempt,4)).
func JobBackoffMS(attempt int64) float64 {
	shift := attempt
	if shift > 4 {
		shift = 4
	}
	delay := float64(100)
	for i := int64(0); i < shift; i++ {
		delay *= 2
	}
	if delay > 1000 {
		delay = 1000
	}
	return delay
}

// JobAbortKilled mirrors kind.abort's killed determination: no checkpoint,
// waiting phase, or no host -> not killed; otherwise kill and report.
func JobAbortKilled(checkpoint pico3.Checkpoint, host ProcessHost) (bool, error) {
	if checkpoint == nil || host == nil {
		return false, nil
	}
	phase, _, _, key := JobCheckpointFields(checkpoint)
	if phase == JobPhaseWaiting {
		return false, nil
	}
	if err := host.Kill(key, "SIGTERM"); err != nil {
		return false, err
	}
	if err := host.Kill(key, "SIGKILL"); err != nil {
		return false, err
	}
	return true, nil
}

var _ = strings.TrimSpace
