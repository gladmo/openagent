package tasks

// scheduler_test.go: scheduler behavior with a fake executor over the
// real clock — fires, skips, concurrency caps, lifecycle, persistence,
// restart semantics, and events.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func everySpec(id string, interval time.Duration) TaskSpec {
	return TaskSpec{
		ID:       id,
		Schedule: Every{Interval: interval, Anchor: time.Now()},
		Prompt:   "do " + id,
		Enabled:  true,
	}
}

func TestSchedulerRegisterValidatesLoudly(t *testing.T) {
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), &fakeExecutor{}, nil)
	cron, _ := ParseScheduleIn("0 9 * * 1", zone)
	cases := []TaskSpec{
		{ID: "x"},                 // no schedule
		{ID: "x", Schedule: cron}, // no prompt
		{ID: "x", Schedule: cron, Prompt: "p", SessionStrategy: "whenever"},
		{ID: "x", Schedule: Every{Interval: 5 * time.Millisecond, Anchor: time.Now()}, Prompt: "p"}, // below MinEvery
		{ID: "x", Schedule: NewAt(time.Now().Add(-time.Hour)), Prompt: "p"},                         // exhausted
	}
	for i, spec := range cases {
		if err := scheduler.Register(spec); err == nil {
			t.Fatalf("case %d: Register accepted %+v", i, spec)
		}
	}
	if err := scheduler.Register(everySpec("dup", time.Second)); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := scheduler.Register(everySpec("dup", time.Second)); err == nil {
		t.Fatal("duplicate id accepted")
	}
}

func TestSchedulerEveryFiresWithoutDrift(t *testing.T) {
	executor := &fakeExecutor{}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	interval := 60 * time.Millisecond
	if err := scheduler.Register(everySpec("tick", interval)); err != nil {
		t.Fatalf("register: %v", err)
	}
	waitFor(t, "three fires", func() bool { return len(executor.recorded()) >= 3 })
	spec, err := scheduler.Get("tick")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	anchor := everyAnchor(t, scheduler, "tick")
	for i, fire := range executor.recorded() {
		want := anchor.Add(time.Duration(i+1) * interval)
		if drift := fire.FireTime.Sub(want); drift != 0 {
			t.Fatalf("fire %d at %s, grid %s (drift %s)", i, fire.FireTime, want, drift)
		}
	}
	if spec.RunCount < 3 {
		t.Fatalf("run count %d", spec.RunCount)
	}
}

func everyAnchor(t *testing.T, scheduler *Scheduler, id string) time.Time {
	t.Helper()
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	entry, ok := scheduler.entries[id]
	if !ok {
		t.Fatalf("task %q missing", id)
	}
	return entry.record.Spec.Schedule.(Every).Anchor
}

func TestSchedulerOverlappingFireSkipped(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	first := true
	executor := &fakeExecutor{behavior: func(ctx context.Context, fire Fire) RunResult {
		mu.Lock()
		defer mu.Unlock()
		if first {
			first = false
			<-release
		}
		return RunResult{Status: RunStatusSuccess}
	}}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if err := scheduler.Register(everySpec("busy", 50*time.Millisecond)); err != nil {
		t.Fatalf("register: %v", err)
	}
	waitFor(t, "a skipped run recorded", func() bool {
		status, err := scheduler.Get("busy")
		return err == nil && status.LastRun != nil && status.LastRun.SkipReason == "previous run of this task still active"
	})
	close(release)
	waitFor(t, "next fire after release succeeds", func() bool {
		status, err := scheduler.Get("busy")
		return err == nil && status.LastRun != nil && status.LastRun.Status == RunStatusSuccess
	})
}

func TestSchedulerGlobalConcurrencyCapSkips(t *testing.T) {
	release := make(chan struct{})
	executor := &fakeExecutor{behavior: func(ctx context.Context, fire Fire) RunResult {
		<-release
		return RunResult{Status: RunStatusSuccess}
	}}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, func(o *SchedulerOptions) {
		o.MaxConcurrentRuns = 1
	})
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if err := scheduler.Register(everySpec("a", 50*time.Millisecond)); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := scheduler.Register(everySpec("b", 50*time.Millisecond)); err != nil {
		t.Fatalf("register b: %v", err)
	}
	waitFor(t, "global cap skip recorded", func() bool {
		for _, status := range scheduler.List() {
			if status.LastRun != nil && status.LastRun.SkipReason == "max concurrent runs (1) reached" {
				return true
			}
		}
		return false
	})
	close(release)
}

func TestSchedulerRunNowImmediateAndScheduleUntouched(t *testing.T) {
	executor := &fakeExecutor{}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if err := scheduler.Register(everySpec("manual", time.Hour)); err != nil {
		t.Fatalf("register: %v", err)
	}
	status, _ := scheduler.Get("manual")
	nextBefore := *status.NextFire

	run, err := scheduler.RunNow("manual")
	if err != nil {
		t.Fatalf("run now: %v", err)
	}
	if run.Status != RunStatusSuccess || run.Trigger != TriggerManual {
		t.Fatalf("run: %+v", run)
	}
	status, _ = scheduler.Get("manual")
	if !status.NextFire.Equal(nextBefore) {
		t.Fatalf("next fire moved: %s -> %s", nextBefore, *status.NextFire)
	}
	if status.RunCount != 1 {
		t.Fatalf("run count %d, want 1", status.RunCount)
	}
}

func TestSchedulerRunNowSkippedWhileRunning(t *testing.T) {
	release := make(chan struct{})
	executor := &fakeExecutor{behavior: func(ctx context.Context, fire Fire) RunResult {
		<-release
		return RunResult{Status: RunStatusSuccess}
	}}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if err := scheduler.Register(everySpec("r", time.Hour)); err != nil {
		t.Fatalf("register: %v", err)
	}
	done := make(chan *TaskRun, 1)
	go func() {
		run, err := scheduler.RunNow("r")
		if err != nil {
			t.Errorf("first run now: %v", err)
		}
		done <- run
	}()
	waitFor(t, "first manual run in flight", func() bool {
		status, err := scheduler.Get("r")
		return err == nil && status.Running
	})
	second, err := scheduler.RunNow("r")
	if err != nil {
		t.Fatalf("second run now: %v", err)
	}
	if second.Status != RunStatusSkipped || second.SkipReason == "" {
		t.Fatalf("second run: %+v", second)
	}
	close(release)
	<-done
}

func TestSchedulerPauseAndResume(t *testing.T) {
	executor := &fakeExecutor{}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if err := scheduler.Register(everySpec("p", 60*time.Millisecond)); err != nil {
		t.Fatalf("register: %v", err)
	}
	waitFor(t, "first fire", func() bool { return len(executor.recorded()) >= 1 })
	if err := scheduler.Pause("p"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	baseline := len(executor.recorded())
	time.Sleep(150 * time.Millisecond)
	if got := len(executor.recorded()); got != baseline {
		t.Fatalf("paused task fired %d more times", got-baseline)
	}
	if err := scheduler.Resume("p"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	waitFor(t, "fire after resume", func() bool { return len(executor.recorded()) > baseline })
}

func TestSchedulerMaxRunsCompletesTask(t *testing.T) {
	executor := &fakeExecutor{}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	spec := everySpec("once", 50*time.Millisecond)
	spec.MaxRuns = 1
	if err := scheduler.Register(spec); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Completed flips on RunCount >= MaxRuns at run START, so the final
	// run may still be in flight (Running=true) when Completed first
	// reads true. Wait for the terminal state before sampling the fire
	// baseline, or the in-flight run lands during the sleep and looks
	// like an extra fire.
	waitFor(t, "completion", func() bool {
		status, err := scheduler.Get("once")
		return err == nil && status.Completed && !status.Running &&
			status.LastRun != nil && status.LastRun.Status.Terminal()
	})
	status, _ := scheduler.Get("once")
	if status.NextFire != nil {
		t.Fatalf("completed task still has next fire %s", status.NextFire)
	}
	fires := len(executor.recorded())
	time.Sleep(120 * time.Millisecond)
	if got := len(executor.recorded()); got != fires {
		t.Fatalf("completed task fired %d more times", got-fires)
	}
}

// gatedRecorder holds each run at the executor seam before recording it,
// widening the start-vs-land window the Completed flag must survive.
type gatedRecorder struct {
	gate  chan struct{}
	mu    sync.Mutex
	fires []Fire
}

func (g *gatedRecorder) Run(ctx context.Context, fire Fire) RunResult {
	<-g.gate
	g.mu.Lock()
	g.fires = append(g.fires, fire)
	g.mu.Unlock()
	return RunResult{Status: RunStatusSuccess}
}

func (g *gatedRecorder) recorded() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.fires)
}

func TestSchedulerMaxRunsCompletedWhileFinalRunInFlight(t *testing.T) {
	executor := &gatedRecorder{gate: make(chan struct{})}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	spec := everySpec("gated", 50*time.Millisecond)
	spec.MaxRuns = 1
	if err := scheduler.Register(spec); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Completed means the schedule will not fire again; it flips when
	// the final run STARTS, before the executor is called, and Running
	// disambiguates the in-flight final run.
	waitFor(t, "completed with final run in flight", func() bool {
		status, err := scheduler.Get("gated")
		return err == nil && status.Completed && status.Running &&
			status.NextFire == nil
	})
	if got := executor.recorded(); got != 0 {
		t.Fatalf("executor ran %d times before gate release", got)
	}

	close(executor.gate)
	waitFor(t, "final run terminal", func() bool {
		status, err := scheduler.Get("gated")
		return err == nil && status.Completed && !status.Running &&
			status.LastRun != nil && status.LastRun.Status == RunStatusSuccess
	})
	time.Sleep(150 * time.Millisecond)
	if got := executor.recorded(); got != 1 {
		t.Fatalf("fired %d times after completion, want exactly 1", got)
	}
}

func TestSchedulerAtFiresOnceThenCompletes(t *testing.T) {
	executor := &fakeExecutor{}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	spec := TaskSpec{ID: "once-only", Schedule: NewAt(time.Now().Add(80 * time.Millisecond)), Prompt: "once", Enabled: true}
	if err := scheduler.Register(spec); err != nil {
		t.Fatalf("register: %v", err)
	}
	waitFor(t, "one-shot fire", func() bool { return len(executor.recorded()) >= 1 })
	time.Sleep(150 * time.Millisecond)
	if got := len(executor.recorded()); got != 1 {
		t.Fatalf("one-shot fired %d times", got)
	}
	status, _ := scheduler.Get("once-only")
	if !status.Completed {
		t.Fatal("at task not completed after firing")
	}
}

func TestSchedulerAtFiresOnTime(t *testing.T) {
	executor := &fakeExecutor{}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	fireAt := time.Now().Add(300 * time.Millisecond)
	spec := TaskSpec{ID: "precision", Schedule: NewAt(fireAt), Prompt: "now", Enabled: true}
	if err := scheduler.Register(spec); err != nil {
		t.Fatalf("register: %v", err)
	}
	waitFor(t, "scheduled fire", func() bool { return len(executor.recorded()) >= 1 })
	latency := time.Since(fireAt)
	if latency < -50*time.Millisecond || latency > time.Second {
		t.Fatalf("fire latency %s outside tolerance", latency)
	}
}

func TestSchedulerUpdateKeepsHistoryAndReschedules(t *testing.T) {
	executor := &fakeExecutor{}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if err := scheduler.Register(everySpec("u", time.Hour)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := scheduler.RunNow("u"); err != nil {
		t.Fatalf("run now: %v", err)
	}
	updated := everySpec("u", 60*time.Millisecond)
	updated.Name = "renamed"
	if err := scheduler.Update(updated); err != nil {
		t.Fatalf("update: %v", err)
	}
	status, err := scheduler.Get("u")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if status.Name != "renamed" || status.RunCount != 1 || status.LastRun == nil {
		t.Fatalf("update lost history: %+v", status)
	}
	waitFor(t, "fire on new schedule", func() bool { return len(executor.recorded()) >= 2 })
}

func TestSchedulerHistoryTrimmed(t *testing.T) {
	executor := &fakeExecutor{}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, func(o *SchedulerOptions) {
		o.HistoryLimit = 2
	})
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if err := scheduler.Register(everySpec("trim", 40*time.Millisecond)); err != nil {
		t.Fatalf("register: %v", err)
	}
	waitFor(t, "four fires", func() bool { return len(executor.recorded()) >= 4 })
	scheduler.mu.Lock()
	history := len(scheduler.entries["trim"].record.History)
	runCount := scheduler.entries["trim"].record.RunCount
	scheduler.mu.Unlock()
	if history > 2 {
		t.Fatalf("history kept %d runs, want at most 2", history)
	}
	if runCount < 4 {
		t.Fatalf("run count %d undercounts fires", runCount)
	}
}

func TestSchedulerRestartMarksInterruptedAndSkipsMissedFires(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStore(filepath.Join(dir, "tasks.jsonl"))
	executor := &fakeExecutor{}
	scheduler, _ := newTestScheduler(t, store, executor, func(o *SchedulerOptions) { o.MinEvery = 50 * time.Millisecond })
	mustStart(t, scheduler)

	// Seed an orphaned running run directly in the store.
	spec := everySpec("restart", 400*time.Millisecond)
	if err := scheduler.Register(spec); err != nil {
		t.Fatalf("register: %v", err)
	}
	waitFor(t, "first fire", func() bool { return len(executor.recorded()) >= 1 })
	stopScheduler(t, scheduler)

	// Record the state as if killed mid-run.
	scheduler.mu.Lock()
	entries := scheduler.snapshotLocked()
	scheduler.mu.Unlock()
	entries[0].History = append(entries[0].History, TaskRun{
		RunID: "orphan", TaskID: "restart", Status: RunStatusRunning,
		FireTime: time.Now(), StartedAt: time.Now(),
	})
	if err := store.Save(entries); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	// Miss the next scheduled fire while stopped.
	time.Sleep(600 * time.Millisecond)

	executor2 := &fakeExecutor{}
	scheduler2, _ := newTestScheduler(t, store, executor2, func(o *SchedulerOptions) { o.MinEvery = 50 * time.Millisecond })
	mustStart(t, scheduler2)
	defer stopScheduler(t, scheduler2)

	if _, err := scheduler2.Get("restart"); err != nil {
		t.Fatalf("get: %v", err)
	}
	interrupted := false
	scheduler2.mu.Lock()
	for _, run := range scheduler2.entries["restart"].record.History {
		if run.RunID == "orphan" && run.Status == RunStatusInterrupted {
			interrupted = true
		}
	}
	scheduler2.mu.Unlock()
	if !interrupted {
		t.Fatal("orphaned running run not marked interrupted")
	}

	// No backfill: the missed fire is skipped and the next fire is
	// strictly after the restart, within one interval.
	restartAt := time.Now()
	baseline := len(executor2.recorded())
	if baseline != 0 {
		t.Fatalf("fired %d times immediately at restart", baseline)
	}
	waitFor(t, "first post-restart fire", func() bool { return len(executor2.recorded()) >= 1 })
	first := executor2.recorded()[0]
	if !first.FireTime.After(restartAt) {
		t.Fatalf("backfilled fire at %s before restart %s", first.FireTime, restartAt)
	}
	if first.FireTime.Sub(restartAt) > 2*400*time.Millisecond {
		t.Fatalf("post-restart fire %s too far after restart", first.FireTime)
	}
}

func TestSchedulerStartDegradesCorruptLinesLoudly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.jsonl")
	record := TaskRecord{Spec: everySpec("good", time.Hour), RunCount: 0}
	if err := NewFileStore(path).Save([]TaskRecord{record}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := appendLine(path, "{ this is not json"); err != nil {
		t.Fatalf("append corrupt line: %v", err)
	}

	scheduler, _ := newTestScheduler(t, NewFileStore(path), &fakeExecutor{}, nil)
	var degraded sync.Mutex
	var seen []TaskEventType
	dispose := scheduler.OnEvent(func(event TaskEvent) {
		degraded.Lock()
		seen = append(seen, event.Type)
		degraded.Unlock()
	})
	defer dispose()
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	waitFor(t, "degrade event", func() bool {
		degraded.Lock()
		defer degraded.Unlock()
		for _, eventType := range seen {
			if eventType == EventStoreDegraded {
				return true
			}
		}
		return false
	})
	if _, err := scheduler.Get("good"); err != nil {
		t.Fatalf("healthy record lost: %v", err)
	}
}

func appendLine(path, line string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(line + "\n")
	return err
}

func TestSchedulerEventsAndDisposer(t *testing.T) {
	executor := &fakeExecutor{}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)

	var mu sync.Mutex
	var types []TaskEventType
	dispose := scheduler.OnEvent(func(event TaskEvent) {
		mu.Lock()
		defer mu.Unlock()
		types = append(types, event.Type)
	})
	if err := scheduler.Register(everySpec("e", time.Hour)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := scheduler.RunNow("e"); err != nil {
		t.Fatalf("run now: %v", err)
	}
	waitFor(t, "run finished event", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return containsType(types, EventRunFinished)
	})
	mu.Lock()
	ordered := append([]TaskEventType(nil), types...)
	mu.Unlock()
	wantPrefix := []TaskEventType{EventTaskRegistered, EventFired, EventRunStarted}
	for i, want := range wantPrefix {
		if ordered[i] != want {
			t.Fatalf("event order: got %v", ordered)
		}
	}
	dispose()
	if _, err := scheduler.RunNow("e"); err != nil {
		t.Fatalf("second run now: %v", err)
	}
	mu.Lock()
	after := len(types)
	mu.Unlock()
	// The disposed listener must not receive further events; types is
	// only appended by the live listener, which we disposed.
	if after != len(ordered) {
		t.Fatalf("disposed listener kept receiving events: %d -> %d", len(ordered), after)
	}
}

func containsType(types []TaskEventType, want TaskEventType) bool {
	for _, candidate := range types {
		if candidate == want {
			return true
		}
	}
	return false
}

func TestSchedulerCRUDBeforeStart(t *testing.T) {
	executor := &fakeExecutor{}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	if err := scheduler.Register(everySpec("early", time.Hour)); err != nil {
		t.Fatalf("register before start: %v", err)
	}
	run, err := scheduler.RunNow("early")
	if err != nil || run.Status != RunStatusSuccess {
		t.Fatalf("run now before start: %+v err=%v", run, err)
	}
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if _, err := scheduler.Get("early"); err != nil {
		t.Fatalf("get after start: %v", err)
	}
}

func TestSchedulerUnknownTaskOperationsFail(t *testing.T) {
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), &fakeExecutor{}, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	for _, action := range []string{"get", "pause", "resume", "unregister", "run"} {
		var err error
		switch action {
		case "get":
			_, err = scheduler.Get("ghost")
		case "pause":
			err = scheduler.Pause("ghost")
		case "resume":
			err = scheduler.Resume("ghost")
		case "unregister":
			err = scheduler.Unregister("ghost")
		case "run":
			_, err = scheduler.RunNow("ghost")
		}
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("%s ghost: err=%v, want not found", action, err)
		}
	}
}

func TestSchedulerUnregisterDropsFinishedResult(t *testing.T) {
	release := make(chan struct{})
	executor := &fakeExecutor{behavior: func(ctx context.Context, fire Fire) RunResult {
		<-release
		return RunResult{Status: RunStatusSuccess}
	}}
	store := NewMemoryStore()
	scheduler, _ := newTestScheduler(t, store, executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if err := scheduler.Register(everySpec("gone", time.Hour)); err != nil {
		t.Fatalf("register: %v", err)
	}
	go func() { _, _ = scheduler.RunNow("gone") }()
	waitFor(t, "run in flight", func() bool {
		status, err := scheduler.Get("gone")
		return err == nil && status.Running
	})
	if err := scheduler.Unregister("gone"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	close(release)
	time.Sleep(100 * time.Millisecond)
	if _, err := scheduler.Get("gone"); err == nil {
		t.Fatal("unregistered task still present")
	}
	records, _ := store.Load()
	for _, record := range records {
		if record.Spec.ID == "gone" {
			t.Fatal("unregistered task still persisted")
		}
	}
}

func TestSchedulerStopDrainsInFlightRun(t *testing.T) {
	release := make(chan struct{})
	executor := &fakeExecutor{behavior: func(ctx context.Context, fire Fire) RunResult {
		<-release
		return RunResult{Status: RunStatusSuccess}
	}}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	if err := scheduler.Register(everySpec("drain", time.Hour)); err != nil {
		t.Fatalf("register: %v", err)
	}
	go func() { _, _ = scheduler.RunNow("drain") }()
	waitFor(t, "run in flight", func() bool {
		status, err := scheduler.Get("drain")
		return err == nil && status.Running
	})
	stopped := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = scheduler.Stop(ctx)
		close(stopped)
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case <-stopped:
		t.Fatal("Stop returned while a run was in flight")
	default:
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not drain")
	}
}

func TestSchedulerExecutorFailureRecordsFailedRun(t *testing.T) {
	executor := &fakeExecutor{behavior: func(ctx context.Context, fire Fire) RunResult {
		return RunResult{Status: RunStatusFailed, Err: "boom"}
	}}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if err := scheduler.Register(everySpec("failing", time.Hour)); err != nil {
		t.Fatalf("register: %v", err)
	}
	run, err := scheduler.RunNow("failing")
	if err != nil {
		t.Fatalf("run now: %v", err)
	}
	if run.Status != RunStatusFailed || run.Err != "boom" {
		t.Fatalf("run: %+v", run)
	}
}

func TestSchedulerExecutorPanicBecomesFailedRun(t *testing.T) {
	executor := &fakeExecutor{behavior: func(ctx context.Context, fire Fire) RunResult {
		panic("executor lost its mind")
	}}
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), executor, nil)
	mustStart(t, scheduler)
	defer stopScheduler(t, scheduler)
	if err := scheduler.Register(everySpec("panic", time.Hour)); err != nil {
		t.Fatalf("register: %v", err)
	}
	run, err := scheduler.RunNow("panic")
	if err != nil {
		t.Fatalf("run now: %v", err)
	}
	if run.Status != RunStatusFailed || !strings.Contains(run.Err, "panic") {
		t.Fatalf("run: %+v", run)
	}
}

func TestNewSchedulerRequiresSeams(t *testing.T) {
	if _, err := NewScheduler(SchedulerOptions{Executor: &fakeExecutor{}}); err == nil {
		t.Fatal("missing store accepted")
	}
	if _, err := NewScheduler(SchedulerOptions{Store: NewMemoryStore()}); err == nil {
		t.Fatal("missing executor accepted")
	}
}
