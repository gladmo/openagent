# Agent Note: Pipeline executor seam for host runtimes (agent/harness/tasks)

Status: implemented

## Problem

The `Executor` seam shipped with two adapters — the test fake and `AgentExecutor` — both shaped by the synchronous agent loop: construct/find an `agent.Agent`, prompt it, wait. A host runtime with its own execution pipeline (queue-admitted turns, job runners) can drive fires only by re-implementing the same skeleton per host: submit asynchronously, wait on a terminal channel, propagate context expiry into its own cancel API, and classify deadline-versus-cancellation outcomes. That skeleton is framework knowledge, not host knowledge; every host rebuilding it is the duplication a framework exists to remove. Separately, hosts with an existing relational database asked whether openagent should grow a directly usable store so adapters stop being hand-written.

## Decision

`agent/harness/tasks/executor_pipeline.go` adds a third adapter around a narrower seam: `PipelineSubmitter.Submit(fire)` lands one fire on the host pipeline and returns a `PipelineSubmission` (`Done` delivers the terminal `RunResult` exactly once; `Cancel` aborts a queued or running submission). `NewPipelineExecutor` owns everything else — Submit errors fail the run loud; context deadline reports `failed: timeout after …`, plain cancellation reports `interrupted`, both after a `CancelGrace` (default 10s) drain that lets the host's terminal writes settle; context classification wins over whatever the host reports for the aborted run. `ExecutorFunc` adapts a plain function onto the seam. Session-strategy resolution stays inside `Submit`: `Fire` already carries the strategy, and the host owns its sessions. `AgentExecutor` is untouched. Storage stays as decided: `Store` remains the only abstraction, its adapter contract now stated on the interface (single-caller serialization, whole-snapshot atomic replace, order-irrelevant load, per-record degradation, host-owned schema) — openagent ships no relational implementation. `examples/scheduled_pipeline` pins the pattern with a queue-backed fake host.

## Alternatives considered

### Why not generalize AgentExecutor behind agent-shaped interfaces?

Hosts do not drive raw agents; their real shape is "enqueue a unit of work, await its terminal event, cancel it by id". Abstracting `PromptText`/`Abort` further would still force every host to wrap its pipeline into an agent lookalike — the adapter would own the same wait/timeout code the seam was meant to share. `PipelineSubmitter` names the shape hosts actually have.

### Why not let the host's terminal result win after context expiry?

Both executors must classify identically regardless of how a host maps its own aborts (yian maps a user cancel and a timeout to the same row status). Context-error classification is the deterministic rule; the host result wins only while the context is alive. The grace drain still orders the report after the host's terminal writes.

### Why no busy policy on PipelineExecutor?

Host pipelines serialize runs themselves (per-session queues), and the scheduler already refuses overlapping fires per task and beyond the global concurrency cap. A busy policy would re-implement serialization the host already owns.

### Why not ship a relational Store implementation?

`go.mod` reached zero dependencies by vendoring `yamlv3`; a GORM or `database/sql` store would reverse that for one package. Schema and migrations belong to the process that owns the database, and the whole-snapshot `Save` contract makes a transactional upsert-plus-delete adapter small enough to live in each host.

### Why not an incremental per-record save interface?

`SaveRecord`/`DeleteRecord` capability detection would add a second persistence path inside the scheduler, weaken multi-record atomicity (startup recovery rewrites every record), and save only write volume at task-set scale. The snapshot contract keeps one path.

## Consequences

- `Executor` now has three adapters (test fake, `AgentExecutor`, `PipelineExecutor`); the scheduler and every other seam are unchanged.
- Hosts integrating through `PipelineSubmitter` write only domain code: strategy resolution, submission, and cancel — the wait/timeout/cancel skeleton comes from the package. A stuck host pipeline bounds reporting by `CancelGrace`; after that the run is classified from the context error while the host may still be winding down.
- Store adapters for relational databases remain host-owned; the interface comment carries the contract, and `TaskRecord`/`TaskSpec` JSON round-trip supports JSON columns directly.
- The 2026-10-25 tasks note's "two real adapters" consequence is extended, not superseded; this note links from there.
