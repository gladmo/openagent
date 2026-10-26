package tasks

// scheduler.go: the Scheduler — task CRUD, the fire loop, concurrency
// protection, and lifecycle. The loop goroutine computes each task's
// next fire time, sleeps to the earliest one (or a CRUD wake), and on
// due fires either launches a bounded run or records a skip.
//
// Locking contract: s.mu guards entries; events are collected while
// held and emitted after release, so listeners may call back into the
// scheduler.

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gladmo/openagent/ai"
)

// SchedulerOptions configure NewScheduler. Store and Executor are
// required; zero values elsewhere take the defaults listed on each
// field.
type SchedulerOptions struct {
	Store    Store
	Executor Executor
	// Now is the clock seam; tests inject a fake (default time.Now).
	Now func() time.Time
	// MaxConcurrentRuns caps globally in-flight runs (default 4).
	MaxConcurrentRuns int
	// HistoryLimit bounds persisted runs per task (default 20).
	HistoryLimit int
	// DefaultTimeout applies when a spec has no Timeout (default 5m;
	// zero disables).
	DefaultTimeout time.Duration
	// MinEvery is the smallest allowed Every interval (default 10s).
	MinEvery time.Duration
}

type resolvedOptions struct {
	store             Store
	executor          Executor
	now               func() time.Time
	maxConcurrentRuns int
	historyLimit      int
	defaultTimeout    time.Duration
	minEvery          time.Duration
}

// Scheduler owns one task set over one store. All methods are safe for
// concurrent use. After Stop the scheduler is single-use.
type Scheduler struct {
	opts resolvedOptions

	mu      sync.Mutex
	entries map[string]*taskEntry
	started bool
	stopped bool

	wake       chan struct{}
	loopCtx    context.Context
	loopCancel context.CancelFunc
	runCtx     context.Context
	runCancel  context.CancelFunc
	sem        chan struct{}

	events *eventBus
}

type taskEntry struct {
	record  TaskRecord
	next    time.Time
	hasNext bool
	running bool
}

func (e *taskEntry) lastRun() *TaskRun {
	if len(e.record.History) == 0 {
		return nil
	}
	run := e.record.History[len(e.record.History)-1]
	return &run
}

// NewScheduler validates the seams and resolves defaults in one place.
func NewScheduler(options SchedulerOptions) (*Scheduler, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("tasks: SchedulerOptions.Store is required")
	}
	if options.Executor == nil {
		return nil, fmt.Errorf("tasks: SchedulerOptions.Executor is required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	maxConcurrent := options.MaxConcurrentRuns
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	historyLimit := options.HistoryLimit
	if historyLimit <= 0 {
		historyLimit = 20
	}
	minEvery := options.MinEvery
	if minEvery <= 0 {
		minEvery = 10 * time.Second
	}
	return &Scheduler{
		opts: resolvedOptions{
			store:             options.Store,
			executor:          options.Executor,
			now:               now,
			maxConcurrentRuns: maxConcurrent,
			historyLimit:      historyLimit,
			defaultTimeout:    options.DefaultTimeout,
			minEvery:          minEvery,
		},
		entries: map[string]*taskEntry{},
		wake:    make(chan struct{}, 1),
		sem:     make(chan struct{}, maxConcurrent),
		events:  newEventBus(),
	}, nil
}

// OnEvent subscribes; the returned function unsubscribes.
func (s *Scheduler) OnEvent(listener TaskEventListener) func() {
	return s.events.subscribe(listener)
}

// Start loads the store, marks orphaned running runs interrupted,
// recomputes fire times from now (missed fires are never backfilled),
// and starts the loop goroutine. Load degradations emit
// EventStoreDegraded events instead of failing startup.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("tasks: scheduler already started")
	}
	if s.stopped {
		s.mu.Unlock()
		return fmt.Errorf("tasks: scheduler stopped; create a new one")
	}
	now := s.opts.now()
	records, issues := s.opts.store.Load()
	for i := range records {
		record := &records[i]
		for j := range record.History {
			if record.History[j].Status == RunStatusRunning {
				record.History[j].Status = RunStatusInterrupted
				record.History[j].FinishedAt = now
				record.History[j].Err = "interrupted by process restart"
			}
		}
		s.installRecordLocked(*record, now)
	}
	s.started = true
	s.loopCtx, s.loopCancel = context.WithCancel(ctx)
	s.runCtx, s.runCancel = context.WithCancel(ctx)
	persistErr := s.opts.store.Save(s.snapshotLocked())
	s.mu.Unlock()

	for i := range issues {
		issue := issues[i]
		s.events.emit(TaskEvent{Type: EventStoreDegraded, Issue: &issue, At: s.opts.now()})
	}
	if persistErr != nil {
		return fmt.Errorf("tasks: persist recovery state: %w", persistErr)
	}
	go s.loop()
	s.signalWake()
	return nil
}

// installRecordLocked rebuilds an entry from a loaded record, computing
// the next fire from now. Exhausted schedules never fire again.
func (s *Scheduler) installRecordLocked(record TaskRecord, now time.Time) {
	entry := &taskEntry{record: record}
	spec := record.Spec
	if spec.Enabled && s.scheduleRunnableLocked(spec, record.RunCount, now) {
		next, ok := spec.Schedule.Next(now)
		entry.next, entry.hasNext = next, ok
	}
	s.entries[spec.ID] = entry
}

func (s *Scheduler) scheduleRunnableLocked(spec TaskSpec, runCount int, now time.Time) bool {
	if spec.MaxRuns > 0 && runCount >= spec.MaxRuns {
		return false
	}
	_, ok := spec.Schedule.Next(now)
	return ok
}

// Stop cancels the fire loop and waits for in-flight runs to drain
// until ctx expires; then it cancels them (they record interrupted).
// Stop is idempotent.
func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped || !s.started {
		s.stopped = true
		s.mu.Unlock()
		return nil
	}
	s.loopCancel()
	s.stopped = true
	s.mu.Unlock()

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for s.inFlight() > 0 {
		select {
		case <-ctx.Done():
			s.runCancel()
			s.awaitDrain()
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

func (s *Scheduler) inFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, entry := range s.entries {
		if entry.running {
			count++
		}
	}
	return count
}

func (s *Scheduler) awaitDrain() {
	deadline := time.Now().Add(5 * time.Second)
	for s.inFlight() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
}

// Register validates and persists a new task. The task fires from now
// forward; an already-exhausted schedule (at in the past) is rejected.
func (s *Scheduler) Register(spec TaskSpec) error {
	normalized, err := s.validateSpec(spec)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if _, exists := s.entries[normalized.ID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("tasks: task %q already registered", normalized.ID)
	}
	entry := &taskEntry{record: TaskRecord{Spec: normalized}}
	if next, ok := normalized.Schedule.Next(s.opts.now()); ok {
		entry.next, entry.hasNext = next, true
	}
	s.entries[normalized.ID] = entry
	if err := s.opts.store.Save(s.snapshotLocked()); err != nil {
		delete(s.entries, normalized.ID)
		s.mu.Unlock()
		return fmt.Errorf("tasks: persist task %q: %w", normalized.ID, err)
	}
	s.mu.Unlock()
	s.signalWake()
	s.events.emit(TaskEvent{Type: EventTaskRegistered, TaskID: normalized.ID, At: s.opts.now()})
	return nil
}

// Update replaces a task's spec, keeping its run history. The next
// fire time restarts from now.
func (s *Scheduler) Update(spec TaskSpec) error {
	normalized, err := s.validateSpec(spec)
	if err != nil {
		return err
	}
	s.mu.Lock()
	entry, exists := s.entries[normalized.ID]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("tasks: task %q not found", normalized.ID)
	}
	previous := entry.record.Spec
	entry.record.Spec = normalized
	if normalized.Enabled {
		next, ok := normalized.Schedule.Next(s.opts.now())
		entry.next, entry.hasNext = next, ok
	} else {
		entry.hasNext = false
	}
	if err := s.opts.store.Save(s.snapshotLocked()); err != nil {
		entry.record.Spec = previous
		s.mu.Unlock()
		return fmt.Errorf("tasks: persist task %q: %w", normalized.ID, err)
	}
	s.mu.Unlock()
	s.signalWake()
	s.events.emit(TaskEvent{Type: EventTaskUpdated, TaskID: normalized.ID, At: s.opts.now()})
	return nil
}

// Unregister removes a task and its history. An in-flight run is
// allowed to finish; its result is dropped.
func (s *Scheduler) Unregister(id string) error {
	s.mu.Lock()
	entry, exists := s.entries[id]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("tasks: task %q not found", id)
	}
	delete(s.entries, id)
	if err := s.opts.store.Save(s.snapshotLocked()); err != nil {
		s.entries[id] = entry
		s.mu.Unlock()
		return fmt.Errorf("tasks: persist after deleting %q: %w", id, err)
	}
	s.mu.Unlock()
	s.events.emit(TaskEvent{Type: EventTaskDeleted, TaskID: id, At: s.opts.now()})
	return nil
}

// Pause stops firing a task without deleting it; pausing twice is a
// no-op.
func (s *Scheduler) Pause(id string) error {
	s.mu.Lock()
	entry, exists := s.entries[id]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("tasks: task %q not found", id)
	}
	if !entry.record.Spec.Enabled {
		s.mu.Unlock()
		return nil
	}
	spec := entry.record.Spec
	spec.Enabled = false
	entry.record.Spec = spec
	entry.hasNext = false
	if err := s.opts.store.Save(s.snapshotLocked()); err != nil {
		spec.Enabled = true
		entry.record.Spec = spec
		if next, ok := spec.Schedule.Next(s.opts.now()); ok {
			entry.next, entry.hasNext = next, ok
		}
		s.mu.Unlock()
		return fmt.Errorf("tasks: persist pausing %q: %w", id, err)
	}
	s.mu.Unlock()
	s.events.emit(TaskEvent{Type: EventTaskPaused, TaskID: id, At: s.opts.now()})
	return nil
}

// Resume re-enables firing from now (missed fires are not backfilled);
// resuming a running task is a no-op.
func (s *Scheduler) Resume(id string) error {
	s.mu.Lock()
	entry, exists := s.entries[id]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("tasks: task %q not found", id)
	}
	if entry.record.Spec.Enabled {
		s.mu.Unlock()
		return nil
	}
	spec := entry.record.Spec
	spec.Enabled = true
	entry.record.Spec = spec
	if next, ok := spec.Schedule.Next(s.opts.now()); ok {
		entry.next, entry.hasNext = next, true
	}
	if err := s.opts.store.Save(s.snapshotLocked()); err != nil {
		spec.Enabled = false
		entry.record.Spec = spec
		entry.hasNext = false
		s.mu.Unlock()
		return fmt.Errorf("tasks: persist resuming %q: %w", id, err)
	}
	s.mu.Unlock()
	s.signalWake()
	s.events.emit(TaskEvent{Type: EventTaskResumed, TaskID: id, At: s.opts.now()})
	return nil
}

// RunNow fires a task immediately, synchronously, without touching its
// schedule. It obeys the same per-task and global concurrency guards;
// a refused fire records and returns a skipped run.
func (s *Scheduler) RunNow(id string) (*TaskRun, error) {
	s.mu.Lock()
	entry, exists := s.entries[id]
	if !exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("tasks: task %q not found", id)
	}
	spec := entry.record.Spec
	now := s.opts.now()
	var pending []TaskEvent
	if refusal := s.beginRunLocked(entry, TriggerManual, now, &pending); refusal != nil {
		s.mu.Unlock()
		s.emitPending(pending)
		return refusal, nil
	}
	runID := entry.lastRun().RunID
	s.mu.Unlock()
	s.emitPending(pending)

	fire := s.buildFire(spec, TriggerManual, now)
	ctx, cancel := s.runContext(spec)
	defer cancel()
	result := s.callExecutor(ctx, fire)

	s.mu.Lock()
	pending = nil
	s.finishRunLocked(entry, runID, result, s.opts.now(), &pending)
	final := entry.lastRun()
	s.mu.Unlock()
	s.emitPending(pending)
	if final == nil {
		return nil, fmt.Errorf("tasks: run %q vanished during manual fire", runID)
	}
	return final, nil
}

// Get returns one task's status.
func (s *Scheduler) Get(id string) (*TaskStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[id]
	if !exists {
		return nil, fmt.Errorf("tasks: task %q not found", id)
	}
	status := s.statusLocked(entry)
	return &status, nil
}

// List returns every task's status ordered by ID.
func (s *Scheduler) List() []TaskStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusesLocked()
}

func (s *Scheduler) statusesLocked() []TaskStatus {
	ids := s.sortedIDsLocked()
	statuses := make([]TaskStatus, 0, len(ids))
	for _, id := range ids {
		statuses = append(statuses, s.statusLocked(s.entries[id]))
	}
	return statuses
}

func (s *Scheduler) statusLocked(entry *taskEntry) TaskStatus {
	spec := entry.record.Spec
	completed := !entry.hasNext
	if spec.MaxRuns > 0 && entry.record.RunCount >= spec.MaxRuns {
		completed = true
	}
	var next *time.Time
	maxRunsReached := spec.MaxRuns > 0 && entry.record.RunCount >= spec.MaxRuns
	if spec.Enabled && entry.hasNext && !maxRunsReached {
		next = &entry.next
	}
	var last *TaskRun
	if run := entry.lastRun(); run != nil {
		copyRun := *run
		last = &copyRun
	}
	return TaskStatus{
		ID:              spec.ID,
		Name:            spec.Name,
		Schedule:        spec.Schedule.String(),
		SessionStrategy: spec.normalizedSessionStrategy(),
		FixedSessionID:  spec.FixedSessionID,
		Enabled:         spec.Enabled,
		Completed:       completed,
		Running:         entry.running,
		NextFire:        next,
		RunCount:        entry.record.RunCount,
		LastRun:         last,
	}
}

// validateSpec normalizes defaults and enforces loud failure.
func (s *Scheduler) validateSpec(spec TaskSpec) (TaskSpec, error) {
	if spec.Name == "" {
		spec.Name = spec.ID
	}
	if spec.SessionStrategy == "" {
		spec.SessionStrategy = StrategyNew
	}
	if err := spec.Validate(); err != nil {
		return spec, err
	}
	if every, ok := spec.Schedule.(Every); ok && every.Interval < s.opts.minEvery {
		return spec, fmt.Errorf("tasks: task %q: every interval %s is below the minimum %s", spec.ID, every.Interval, s.opts.minEvery)
	}
	if _, ok := spec.Schedule.Next(s.opts.now()); !ok {
		return spec, fmt.Errorf("tasks: task %q: schedule is already exhausted", spec.ID)
	}
	return spec, nil
}

// loop fires due tasks until the loop context is cancelled.
func (s *Scheduler) loop() {
	for {
		if s.loopCtx.Err() != nil {
			return
		}
		s.mu.Lock()
		next, has := s.earliestNextLocked()
		s.mu.Unlock()
		var timerC <-chan time.Time
		if has {
			timer := time.NewTimer(s.fireDelayLocked(next))
			timerC = timer.C
			select {
			case <-timerC:
				timer.Stop()
			case <-s.wake:
				timer.Stop()
			case <-s.loopCtx.Done():
				timer.Stop()
				return
			}
		} else {
			select {
			case <-s.wake:
			case <-s.loopCtx.Done():
				return
			}
		}
		s.fireDue()
	}
}

func (s *Scheduler) fireDelayLocked(next time.Time) time.Duration {
	delay := next.Sub(s.opts.now())
	if delay < 0 {
		return 0
	}
	return delay
}

func (s *Scheduler) earliestNextLocked() (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, entry := range s.entries {
		if !entry.record.Spec.Enabled || !entry.hasNext {
			continue
		}
		if !found || entry.next.Before(earliest) {
			earliest = entry.next
			found = true
		}
	}
	return earliest, found
}

// fireDue launches or skips every fire whose time has come. On wake at
// most one catch-up fire runs per task; earlier missed fires are
// skipped, and the next fire time collapses to now's successor.
func (s *Scheduler) fireDue() {
	now := s.opts.now()
	var pending []TaskEvent
	s.mu.Lock()
	for _, id := range s.sortedIDsLocked() {
		entry := s.entries[id]
		if !entry.record.Spec.Enabled || !entry.hasNext || entry.next.After(now) {
			continue
		}
		fireTime := entry.next
		s.advanceNextLocked(entry, fireTime, now)
		spec := entry.record.Spec
		if refusal := s.beginRunLocked(entry, TriggerSchedule, fireTime, &pending); refusal != nil {
			pending = append(pending, TaskEvent{Type: EventRunSkipped, TaskID: spec.ID, RunID: refusal.RunID, Run: refusal, At: now})
			continue
		}
		runID := entry.lastRun().RunID
		go func(spec TaskSpec, fire Fire, runID string, entry *taskEntry) {
			ctx, cancel := s.runContext(spec)
			defer cancel()
			result := s.callExecutor(ctx, fire)
			s.mu.Lock()
			var finishEvents []TaskEvent
			s.finishRunLocked(entry, runID, result, s.opts.now(), &finishEvents)
			s.mu.Unlock()
			s.emitPending(finishEvents)
		}(spec, s.buildFire(spec, TriggerSchedule, fireTime), runID, entry)
	}
	s.mu.Unlock()
	s.emitPending(pending)
}

// advanceNextLocked moves the cadence forward from the scheduled fire
// time (drift-free for every) and collapses long misses to now.
func (s *Scheduler) advanceNextLocked(entry *taskEntry, fireTime, now time.Time) {
	next, ok := entry.record.Spec.Schedule.Next(fireTime)
	if ok && !next.After(now) {
		next, ok = entry.record.Spec.Schedule.Next(now)
	}
	entry.next, entry.hasNext = next, ok
}

// beginRunLocked guards concurrency (per task, then global) and, on
// success, records a running run. It returns the terminal run for a
// refused fire, or nil when the run started. Store failures at this
// point surface as EventStoreDegraded; the run record stays in memory.
func (s *Scheduler) beginRunLocked(entry *taskEntry, trigger string, fireTime time.Time, pending *[]TaskEvent) *TaskRun {
	spec := entry.record.Spec
	now := s.opts.now()
	run := TaskRun{
		RunID:     ai.UUIDv7(),
		TaskID:    spec.ID,
		Trigger:   trigger,
		Status:    RunStatusRunning,
		FireTime:  fireTime,
		StartedAt: now,
	}
	if entry.running {
		refusal := run
		refusal.Status = RunStatusSkipped
		refusal.SkipReason = "previous run of this task still active"
		refusal.StartedAt = time.Time{}
		s.appendRunLocked(entry, refusal)
		s.persistBestEffortLocked(pending)
		return &refusal
	}
	select {
	case s.sem <- struct{}{}:
	default:
		refusal := run
		refusal.Status = RunStatusSkipped
		refusal.SkipReason = fmt.Sprintf("max concurrent runs (%d) reached", s.opts.maxConcurrentRuns)
		refusal.StartedAt = time.Time{}
		s.appendRunLocked(entry, refusal)
		s.persistBestEffortLocked(pending)
		return &refusal
	}
	entry.running = true
	entry.record.RunCount++
	s.appendRunLocked(entry, run)
	s.persistBestEffortLocked(pending)
	*pending = append(*pending,
		TaskEvent{Type: EventFired, TaskID: spec.ID, RunID: run.RunID, At: now},
		TaskEvent{Type: EventRunStarted, TaskID: spec.ID, RunID: run.RunID, At: now})
	return nil
}

// finishRunLocked writes the terminal outcome of a started run. An
// unregistered task's result is dropped (the run record went with it).
func (s *Scheduler) finishRunLocked(entry *taskEntry, runID string, result RunResult, now time.Time, pending *[]TaskEvent) {
	current, exists := s.entries[entry.record.Spec.ID]
	if !exists || current != entry {
		s.releaseSem()
		return
	}
	entry.running = false
	s.releaseSem()
	for i := range entry.record.History {
		if entry.record.History[i].RunID == runID {
			target := &entry.record.History[i]
			target.Status = result.Status
			target.FinishedAt = now
			target.Err = result.Err
			target.SkipReason = result.SkipReason
			target.Tokens = result.Tokens
			target.SessionID = result.SessionID
			break
		}
	}
	if entry.record.Spec.MaxRuns > 0 && entry.record.RunCount >= entry.record.Spec.MaxRuns {
		entry.hasNext = false
	}
	s.persistBestEffortLocked(pending)
	eventType := EventRunFinished
	if result.Status == RunStatusSkipped {
		eventType = EventRunSkipped
	}
	*pending = append(*pending, TaskEvent{Type: eventType, TaskID: entry.record.Spec.ID, RunID: runID, At: now})
}

// callExecutor isolates executor failures as failed runs.
func (s *Scheduler) callExecutor(ctx context.Context, fire Fire) (result RunResult) {
	defer func() {
		if r := recover(); r != nil {
			result = RunResult{Status: RunStatusFailed, Err: fmt.Sprintf("executor panic: %v", r)}
		}
	}()
	return s.opts.executor.Run(ctx, fire)
}

// runContext derives the per-run context with the spec's timeout.
func (s *Scheduler) runContext(spec TaskSpec) (context.Context, context.CancelFunc) {
	base := s.runCtx
	if base == nil {
		base = context.Background()
	}
	timeout := spec.Timeout
	if timeout == 0 {
		timeout = s.opts.defaultTimeout
	}
	if timeout <= 0 {
		return base, func() {}
	}
	return context.WithTimeout(base, timeout)
}

func (s *Scheduler) buildFire(spec TaskSpec, trigger string, fireTime time.Time) Fire {
	return Fire{
		TaskID:          spec.ID,
		TaskName:        spec.Name,
		Prompt:          spec.Prompt,
		Trigger:         trigger,
		FireTime:        fireTime,
		SessionStrategy: spec.normalizedSessionStrategy(),
		FixedSessionID:  spec.FixedSessionID,
	}
}

func (s *Scheduler) sortedIDsLocked() []string {
	ids := make([]string, 0, len(s.entries))
	for id := range s.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// appendRunLocked upserts by RunID and trims oldest history beyond the
// limit.
func (s *Scheduler) appendRunLocked(entry *taskEntry, run TaskRun) {
	for i := range entry.record.History {
		if entry.record.History[i].RunID == run.RunID {
			entry.record.History[i] = run
			return
		}
	}
	entry.record.History = append(entry.record.History, run)
	if excess := len(entry.record.History) - s.opts.historyLimit; excess > 0 {
		entry.record.History = append([]TaskRun(nil), entry.record.History[excess:]...)
	}
}

func (s *Scheduler) releaseSem() { <-s.sem }

func (s *Scheduler) signalWake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) emitPending(pending []TaskEvent) {
	for _, event := range pending {
		s.events.emit(event)
	}
}

// persistBestEffortLocked saves the task set; failures surface as a
// degrade event rather than dropping in-memory run records.
func (s *Scheduler) persistBestEffortLocked(pending *[]TaskEvent) {
	if err := s.opts.store.Save(s.snapshotLocked()); err != nil {
		*pending = append(*pending, TaskEvent{Type: EventStoreDegraded, Issue: &StoreIssue{Err: err}, At: s.opts.now()})
	}
}

func (s *Scheduler) snapshotLocked() []TaskRecord {
	records := make([]TaskRecord, 0, len(s.entries))
	for _, id := range s.sortedIDsLocked() {
		entry := s.entries[id]
		records = append(records, TaskRecord{
			Spec:     entry.record.Spec,
			RunCount: entry.record.RunCount,
			History:  append([]TaskRun(nil), entry.record.History...),
		})
	}
	return records
}
