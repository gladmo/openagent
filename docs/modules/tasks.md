# Tasks

English | [中文](tasks.zh.md)

The `agent/harness/tasks/` package (openagent-native, not a pi port) is the automated-task scheduler: it fires registered task definitions on cron / fixed-interval / one-shot schedules and drives one full agent run per fire through the `Executor` seam. The agent loop is never modified; the default executor injects the task prompt as a user message and waits for the run to settle.

## Composition

- **Schedule** (`schedule.go`, `schedule_cron.go`) — `Next(after)` returns the next fire time strictly after `after` (`ok=false` when exhausted). Adapters: `ParseSchedule` (5-field cron or `@descriptor`, local time; `ParseScheduleIn` takes a `*time.Location`), `Every` (anchor + k·interval, drift-free), `At` (one-shot). The cron grammar is the robfig/cron v3.0.0 5-field subset — names, lists, ranges, steps, `?` in day fields, `@every <duration>` — with robfig's both-dom-and-dow-restricted OR rule and a 4-year match window; seconds fields and Quartz `L`/`W`/`#` fail loud. Golden vectors in `schedule_cron_test.go` pin the semantics.
- **TaskSpec** (`spec.go`) — the persisted definition: `ID`, `Name` (defaults to ID at registration), `Schedule`, `SessionStrategy` (`new` default | `fixed`), `FixedSessionID`, `Prompt`, `Timeout`, `Enabled`, `MaxRuns` (0 = unlimited), `Metadata`. JSON encodes the schedule as a discriminant union (`{"type":"cron"|"every"|"at", ...}`); unknown types fail decode. `Validate` rejects missing id/schedule/prompt, unknown strategies, fixed-without-session, negative timeout/maxRuns.
- **Store** (`store.go`) — `FileStore` keeps one JSONL line per task (`TaskRecord`: spec, `RunCount`, bounded newest-last `History`) and replaces the whole file atomically (temp file + rename). `Load` degrades per line: corrupt or invalid records become `StoreIssue`s instead of failing startup; a missing file is an empty store. `MemoryStore` for tests and ephemeral processes.
- **Executor** (`executor.go`) — `Run(ctx, Fire) RunResult`; `Fire` carries the prompt, `Trigger` (`schedule` | `manual`), fire time, and session strategy. `AgentExecutor` is the default adapter over `agent.Agent`: `NewAgent` builds fresh sessions for the `new` strategy (assigned a fresh UUIDv7 session id); `LoadAgent` restores fixed sessions across restarts (results cached per process). Busy policy for fixed sessions: `wait` (default — the run waits for the session to go idle, bounded by the run context) or `skip`. Timeout/cancellation calls `Abort()` and honors a `CancelGrace` (default 10s) before returning `failed`/`interrupted`; provider failures surface through the synthetic error assistant message as `failed` with that message. `ExecutorFunc` adapts a plain function into the seam.
- **Host pipelines** (`executor_pipeline.go`) — `PipelineSubmitter` lands one fire as an asynchronous, cancellable unit of work on the host's own execution pipeline (queue-admitted turns, job runners): `Submit` resolves the session strategy (the host owns its sessions) and returns a `PipelineSubmission` whose `Done` delivers the terminal `RunResult` exactly once and whose `Cancel` aborts a queued or running submission. `NewPipelineExecutor` supplies the shared wait, cancel, and classification: a Submit error fails the run loud; context deadline reports `failed: timeout after …` and plain cancellation `interrupted`, after a `CancelGrace` (default 10s) drain — context classification wins over the host's result for an expired context.
- **Scheduler** (`scheduler.go`) — `NewScheduler(SchedulerOptions)` validates the `Store`/`Executor` seams and resolves defaults in one place: `MaxConcurrentRuns` 4, `HistoryLimit` 20, `DefaultTimeout` 5m (0 disables), `MinEvery` 10s, `Now` clock seam. `Start` loads the store, marks orphaned `running` runs `interrupted`, and starts the loop goroutine; `Stop` cancels the loop, drains in-flight runs until its context expires, then cancels them. CRUD: `Register`, `Update` (keeps history, reschedules from now), `Unregister` (in-flight result dropped), `Pause`/`Resume` (single `Enabled` switch; resume recomputes from now), `RunNow` (synchronous manual fire that leaves the schedule untouched). All mutations persist before mutating in-memory state; fire-path persistence is best-effort with `store_degraded` events.
- **Events** (`events.go`) — `OnEvent(listener)` returns its disposer; listeners run inline after the scheduler lock is released (they may call back into the scheduler), are serialized, and a panicking listener is contained. Event set: `task_registered/updated/deleted/paused/resumed`, `fired`, `run_started`, `run_skipped`, `run_finished`, `store_degraded`.
- **task_manage** (`tool.go`) — `TaskManageTool(scheduler) *agent.AgentTool`, the natural-language surface: actions `create/list/get/update/pause/resume/run/delete`; the task argument carries a schedule expression string. Misformed requests fail loud as tool errors.

## Scheduling semantics

- Missed fires are skipped, never backfilled: restart, resume, register, and update all compute the next fire from now; a long-overdue wake fires at most once per task, then collapses to `Schedule.Next(now)`.
- Per-task overlap is refused: a fire while the task's previous run is live records a `skipped` run with the reason. A global semaphore (`MaxConcurrentRuns`) refuses beyond the cap the same way. Fixed-session injections additionally serialize on the session itself (busy policy).
- `every` schedules anchor at registration; the anchor persists across restarts, so the fire grid never drifts.
- A task completes (derived, not stored) when its schedule is exhausted (`at` fired) or `MaxRuns` runs have started; completed tasks keep their history but never fire again.
- Registration fails loud on invalid expressions, `every` below `MinEvery`, already-exhausted schedules, duplicate ids, and `Validate` violations.

## Integration

The scheduler composes from outside; the agent packages never import it.

- **Plain agent**: pass your construction code as `AgentExecutorOptions.NewAgent`/`LoadAgent`; append `TaskManageTool(scheduler)` to the agent's tools. See `examples/scheduled_agent`.
- **codingagent session**: `AgentSessionConfig.Agent` (caller-constructed agent) binds the fixed strategy to the interactive session; `AgentSessionConfig.CustomTools` with `codingagent.CreateToolDefinitionFromAgentTool(tasks.TaskManageTool(...))` exposes the tool with no codingagent changes. See `examples/scheduled_codingagent`.
- **Host pipeline** (an own turn queue or job runner): implement `PipelineSubmitter` — resolve the session strategy inside `Submit`, hand back `Done`/`Cancel` — and wrap it with `NewPipelineExecutor`; the wait/timeout/cancel skeleton comes from the package. See `examples/scheduled_pipeline`.
- **Host-owned storage**: implement `Store` against an existing database. The adapter contract is stated on the interface: the scheduler serializes every call, `Save` is a whole-snapshot atomic replace (a transactional upsert plus delete-of-the-absent satisfies it), load order is irrelevant, degraded records surface as `StoreIssue`s, and schema plus migrations stay with the host; `TaskRecord`/`TaskSpec` JSON round-trip supports JSON columns directly.
- **Anything else**: implement `Executor` directly — the seam the test fake, `AgentExecutor`, and `PipelineExecutor` already share.

## Known limitations

- Single-process: no leader election or cross-instance locking; one process owns one store.
- No catch-up mechanism: consumers needing missed-run recovery build it on top of run history, not inside the scheduler.
- Source tagging lives on `Fire.Trigger`/`TaskRun` records and task prompts; `ai.UserMessage` (a pinned port type) carries no metadata field.
- Cron granularity is one minute; second-level scheduling uses `every`.
