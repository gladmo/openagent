package kinds

// Ports of kinds/job.ts behaviors.

import (
	"errors"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent/harness/pico3"
	"github.com/gladmo/openagent/jsonx"
)

type fakeProcessHost struct {
	started  []string
	statuses map[string]ProcessStatusSnapshot
	killed   []string
	startErr error
}

func (h *fakeProcessHost) Start(key string, _ JobInputFields) error {
	h.started = append(h.started, key)
	if h.startErr != nil {
		return h.startErr
	}
	if h.statuses == nil {
		h.statuses = map[string]ProcessStatusSnapshot{}
	}
	h.statuses[key] = ProcessStatusSnapshot{Status: "running"}
	return nil
}

func (h *fakeProcessHost) Status(key string) (ProcessStatusSnapshot, error) {
	if status, ok := h.statuses[key]; ok {
		return status, nil
	}
	return ProcessStatusSnapshot{Status: "unknown"}, nil
}

func (h *fakeProcessHost) Kill(key, signal string) error {
	h.killed = append(h.killed, key+":"+signal)
	return nil
}

func jobRuntime(host ProcessHost) *JobRuntime {
	return &JobRuntime{
		Now:  func() float64 { return 1000 },
		Host: host,
	}
}

func jobTask(id int64, input any, checkpoint pico3.Checkpoint) *pico3.Task {
	return &pico3.Task{ID: id, ConversationID: 1, Kind: "pi.job", Input: input, Status: "running", Checkpoint: checkpoint}
}

func TestJobInitialNoHost(t *testing.T) {
	rt := jobRuntime(nil)
	step, err := JobInitial(jobTask(7, jsonx.NewObj(), nil), JobInputFields{}, rt)
	if err != nil {
		t.Fatal(err)
	}
	failure := step.Done.MustGet("failure").(*jsonx.Obj)
	if failure.MustGet("reason") != "spawn" || failure.MustGet("detail") != "no process host" {
		t.Fatalf("failure = %v", failure)
	}
}

func TestJobInitialNotBeforeWaits(t *testing.T) {
	host := &fakeProcessHost{}
	rt := jobRuntime(host)
	notBefore := float64(5000)
	step, err := JobInitial(jobTask(7, jsonx.NewObj(), nil), JobInputFields{NotBefore: &notBefore}, rt)
	if err != nil {
		t.Fatal(err)
	}
	if !step.HasNextPhase || step.NextPhase != JobPhaseWaiting {
		t.Fatalf("step = %+v", step)
	}
	phase, until, occurrence, _ := JobCheckpointFields(step.Checkpoint)
	if phase != JobPhaseWaiting || until != 5000 || occurrence != 1 {
		t.Fatalf("checkpoint = %v %v %v", phase, until, occurrence)
	}
	if len(host.started) != 0 {
		t.Fatal("process started during wait")
	}
}

func TestJobInitialSpawnsImmediately(t *testing.T) {
	host := &fakeProcessHost{}
	rt := jobRuntime(host)
	step, err := JobInitial(jobTask(7, jsonx.NewObj(), nil), JobInputFields{}, rt)
	if err != nil {
		t.Fatal(err)
	}
	// Running checkpoint (the spawning effect already happened).
	if !step.HasNextPhase || step.NextPhase != JobPhaseRunning {
		t.Fatalf("step = %+v", step)
	}
	if len(host.started) != 1 || host.started[0] != "7:1" {
		t.Fatalf("started = %v", host.started)
	}
	_, _, occurrence, key := JobCheckpointFields(step.Checkpoint)
	if key != "7:1" || occurrence != 1 {
		t.Fatalf("checkpoint key = %s occurrence = %v", key, occurrence)
	}
}

func TestJobSpawnFailureNotifies(t *testing.T) {
	host := &fakeProcessHost{startErr: errors.New("boom")}
	var notified []string
	rt := jobRuntime(host)
	rt.Notify = func(text string) error {
		notified = append(notified, text)
		return nil
	}
	step, err := JobSpawn(jobTask(9, jsonx.NewObj(), nil), 1, JobInputFields{Notify: true}, rt)
	if err != nil {
		t.Fatal(err)
	}
	failure := step.Done.MustGet("failure").(*jsonx.Obj)
	if failure.MustGet("reason") != "spawn" || failure.MustGet("detail") != "boom" {
		t.Fatalf("failure = %v", failure)
	}
	if len(notified) != 1 || !strings.Contains(notified[0], "failed to start") {
		t.Fatalf("notified = %v", notified)
	}
}

func TestJobReconcileUnknown(t *testing.T) {
	host := &fakeProcessHost{statuses: map[string]ProcessStatusSnapshot{}}
	rt := jobRuntime(host)
	checkpoint := jsonx.ObjFrom("phase", JobPhaseRunning, "key", "7:1", "occurrence", float64(1))
	// Without rerun: interrupted.
	step, err := JobReconcile(jobTask(7, jsonx.NewObj(), checkpoint), JobInputFields{}, checkpoint, rt)
	if err != nil {
		t.Fatal(err)
	}
	failure := step.Done.MustGet("failure").(*jsonx.Obj)
	if failure.MustGet("reason") != "interrupted" || failure.MustGet("detail") != "process outcome unknown" {
		t.Fatalf("failure = %v", failure)
	}
	// With rerun: respawn.
	step, err = JobReconcile(jobTask(7, jsonx.NewObj(), checkpoint), JobInputFields{Rerun: true}, checkpoint, rt)
	if err != nil {
		t.Fatal(err)
	}
	if !step.HasNextPhase || step.NextPhase != JobPhaseRunning || len(host.started) != 1 {
		t.Fatalf("step = %+v started = %v", step, host.started)
	}
}

func TestJobReconcileSpawningAdvancesToRunning(t *testing.T) {
	host := &fakeProcessHost{statuses: map[string]ProcessStatusSnapshot{
		"7:1": {Status: "running"},
	}}
	rt := jobRuntime(host)
	checkpoint := jsonx.ObjFrom("phase", JobPhaseSpawning, "key", "7:1", "occurrence", float64(1))
	step, err := JobReconcile(jobTask(7, jsonx.NewObj(), checkpoint), JobInputFields{}, checkpoint, rt)
	if err != nil {
		t.Fatal(err)
	}
	if !step.HasNextPhase || step.NextPhase != JobPhaseRunning {
		t.Fatalf("step = %+v", step)
	}
}

func TestJobPollExits(t *testing.T) {
	host := &fakeProcessHost{}
	rt := jobRuntime(host)
	var notified []string
	rt.Notify = func(text string) error { notified = append(notified, text); return nil }
	status := ProcessStatusSnapshot{Status: "exited", ExitCode: 3, HasExitCode: true, Stdout: "out", Stderr: "err"}

	// No every: completed.
	step, err := JobPoll(jobTask(7, jsonx.NewObj(), nil), 2, JobInputFields{Notify: true}, "7:2", status, rt)
	if err != nil {
		t.Fatal(err)
	}
	result := step.Done.MustGet("result").(*jsonx.Obj)
	if result.MustGet("exitCode") != float64(3) || result.MustGet("occurrences") != float64(2) ||
		result.MustGet("stdout") != "out" || result.MustGet("stderr") != "err" {
		t.Fatalf("result = %v", result)
	}
	if len(notified) != 1 || !strings.Contains(notified[0], "exited with code") {
		t.Fatalf("notified = %v", notified)
	}

	// With every: next waiting occurrence.
	every := float64(60000)
	step, err = JobPoll(jobTask(7, jsonx.NewObj(), nil), 2, JobInputFields{Every: &every}, "7:2", status, rt)
	if err != nil {
		t.Fatal(err)
	}
	if !step.HasNextPhase || step.NextPhase != JobPhaseWaiting {
		t.Fatalf("step = %+v", step)
	}
	phase, until, occurrence, _ := JobCheckpointFields(step.Checkpoint)
	if phase != JobPhaseWaiting || until != 61000 || occurrence != 3 {
		t.Fatalf("checkpoint = %v %v %v", phase, until, occurrence)
	}
}

func TestJobPollRunningContinues(t *testing.T) {
	rt := jobRuntime(&fakeProcessHost{})
	step, err := JobPoll(jobTask(7, jsonx.NewObj(), nil), 1, JobInputFields{}, "7:1", ProcessStatusSnapshot{Status: "running"}, rt)
	if err != nil {
		t.Fatal(err)
	}
	if step.Done != nil || step.HasNextPhase {
		t.Fatalf("step = %+v", step)
	}
}

func TestJobBackoff(t *testing.T) {
	cases := map[int64]float64{0: 100, 1: 200, 2: 400, 3: 800, 4: 1000, 5: 1000, 10: 1000}
	for attempt, want := range cases {
		if got := JobBackoffMS(attempt); got != want {
			t.Fatalf("attempt %d = %v want %v", attempt, got, want)
		}
	}
}

func TestJobAbortKilled(t *testing.T) {
	host := &fakeProcessHost{}
	// No checkpoint -> not killed.
	killed, err := JobAbortKilled(nil, host)
	if err != nil || killed {
		t.Fatalf("killed = %v err = %v", killed, err)
	}
	// Waiting -> not killed.
	waiting := jsonx.ObjFrom("phase", JobPhaseWaiting, "untilMs", float64(1), "occurrence", float64(1))
	killed, err = JobAbortKilled(waiting, host)
	if err != nil || killed {
		t.Fatalf("killed = %v err = %v", killed, err)
	}
	// Running -> SIGTERM then SIGKILL.
	running := jsonx.ObjFrom("phase", JobPhaseRunning, "key", "7:1", "occurrence", float64(1))
	killed, err = JobAbortKilled(running, host)
	if err != nil || !killed {
		t.Fatalf("killed = %v err = %v", killed, err)
	}
	if len(host.killed) != 2 || host.killed[0] != "7:1:SIGTERM" || host.killed[1] != "7:1:SIGKILL" {
		t.Fatalf("killed = %v", host.killed)
	}
	// No host -> not killed.
	if killed, err := JobAbortKilled(running, nil); err != nil || killed {
		t.Fatalf("killed = %v err = %v", killed, err)
	}
}

func TestJobDescribe(t *testing.T) {
	task := jobTask(7, nil, jsonx.ObjFrom("phase", "spawning"))
	value := JobDescribe(task, jsonx.ObjFrom("stdout", "partial"))
	obj := value.(*jsonx.Obj)
	if obj.MustGet("stage") != "spawning" {
		t.Fatalf("stage = %v", obj.MustGet("stage"))
	}
	if obj.MustGet("stdout") != "partial" {
		t.Fatal("slot fields not merged")
	}
	// No checkpoint -> starting.
	value = JobDescribe(jobTask(7, nil, nil), nil)
	if value.(*jsonx.Obj).MustGet("stage") != "starting" {
		t.Fatal("default stage")
	}
}
