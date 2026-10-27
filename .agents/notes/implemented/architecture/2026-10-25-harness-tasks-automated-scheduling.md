# Agent Note: Automated task scheduling as a composed module (agent/harness/tasks)

Status: implemented

## Problem

The ported stack is request-response: a caller prompts, the agent loop runs to completion, the process waits. Nothing in the repo can wake an agent on a schedule (cron, fixed interval, or one-shot time), persist those task definitions safely, or bound and observe the resulting runs. The capability must not live in `codingagent` (it is a CLI port pinned to the reference), and it must not fork the agent loop.

## Decision

`agent/harness/tasks/` is an openagent-native package in the compaction provenance class (new capability, no TypeScript counterpart to port). It owns four pieces behind small interfaces: a `Schedule` seam (cron/every/at), a JSONL `Store` with atomic whole-file replace and per-line degradation, an `Executor` seam whose default `AgentExecutor` adapter drives `agent.Agent` through `PromptText`/`Abort`/`WaitForIdle`/`IsStreaming` only, and a `Scheduler` whose single loop goroutine sleeps to the earliest next fire and wakes on CRUD. Tasks compose from outside: host code supplies agent factories (`NewAgent`/`LoadAgent`); codingagent sessions bind via the existing public seams `AgentSessionConfig.Agent` plus `CustomTools` (`CreateToolDefinitionFromAgentTool`), with zero codingagent changes.

Scheduling semantics follow the DeepSeek Harness cron-plugin model: missed fires are never backfilled (next always computed from now, at most one catch-up fire per late wake), a busy task refuses new fires as recorded `skipped` runs, and a global concurrency cap refuses beyond the limit. `every` schedules anchor at registration and persist that anchor, so fire grids never drift. Fixed-session injections serialize on the session (busy policy `wait` default), because the agent already rejects concurrent prompts.

The cron parser is self-implemented to the robfig/cron v3.0.0 5-field semantics (names, lists, ranges, steps, `?` in day fields, `@every`, the both-day-fields-restricted OR rule, 4-year window), pinned by golden vectors ported from robfig's test expectations. Source tagging (scheduler-injected vs user-typed) lives on `Fire.Trigger` and `TaskRun` records; `ai.UserMessage` is untouched. The package `Scheduler` is unrelated to `agent/harness/pico3`'s scheduler (a task-phase state machine); `doc.go` and the module page state the distinction.

## Alternatives considered

### Why not implement it in codingagent?

The requirement excludes it, and the port pins codingagent to the reference; a scheduling subsystem there would be an unportable deviation and unavailable to plain-agent consumers (`examples/data_query_agent`, `examples/coding_agent` style hosts).

### Why not vendor robfig/cron v3 source under the package?

The needed subset is ~300 lines; vendoring drags the 6-field seconds machinery, descriptor plumbing we partly do not expose, and a MIT notice obligation, for behavior we would still have to pin with our own tests. Golden vectors pin the reference semantics; the `Schedule` interface remains the seam for swapping robfig in later.

### Why not per-task timers or a robfig Cron runner?

Per-task timer chains duplicate pause/resume/update bookkeeping N times; robfig's runner owns its own goroutine and store model. One loop with per-task `next` plus a wake channel keeps missed-fire collapse, pause, and update as single lock-protected mutations, and keeps `Now` injectable for tests.

### Why not queue fires when the task is busy?

Queues hide overload and blur the fire grid; a recorded skip with reason is observable and lets callers build their own catch-up on run history. Session-level serialization (busy-wait) covers the one case where waiting is the correct behavior.

### Why not one JSON document instead of JSONL?

A corrupted document would lose every task; per-line decode degrades to the damaged records (NFR-3), and JSONL matches the session/trajectory storage convention. Writes still replace the whole file atomically.

### Why not add a source field to ai.UserMessage?

`ai` is a pinned port; adding fields is a deviation golden tests must own. The trigger fact already persists in `TaskRun` (and `Fire`), which is where observability needs it.

## Consequences

- Host processes own wiring: the scheduler starts and stops with the process that constructed it; restart marks orphaned `running` runs `interrupted` and never backfills.
- Single-process assumption: no leader election or cross-instance locking; one store file per process.
- Minute cron granularity; second-level work uses `every` (floored at `MinEvery`, default 10s).
- `Executor` has three real adapters (test fake, `AgentExecutor`, and the host-pipeline adapter decided in [2026-10-27-tasks-pipeline-executor](2026-10-27-tasks-pipeline-executor.md)); a harness-lane drive adapter can join the seam later without scheduler changes.
- `TaskRun.RunID` (UUIDv7 string, kind-less) is shaped to register in a future general background-task registry.
- The natural-language surface (`task_manage`) is an ordinary `agent.AgentTool`, so any loop-level consumer gets it; codingagent wraps it through its existing tool-definition adapter.
