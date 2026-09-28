# Agent Note: runtime state machine correctness

Status: implemented

English | [中文](2026-09-28-runtime-state-machine.zh.md)

## Problem

Seven confirmed defects in the lane state machine: `AcceptRun` captured queued steer/followUp/nextRun ids into the run intent but materialized only the prompts, so every accept→start run with a queued item died at `StartRun` ("Run prompt entry is missing its message") and orphaned the pending payloads; the durable `op.state` codec serialized 8 of ~25 `OperationState` fields, so crash restore silently lost the operation's position (continuation, trigger, generation context, retry schedule); a package-global `laneNameHolder` raced cross-lane commits and could write lane A's tip under lane B's name; `QueueMessage`/`CancelQueued`/`AcceptRun` wrote durable lane.state with `lastOperationId: nil`, wiping the previous result linkage; accept never advanced the durable `branch.tip`, so snapshots and restore lost the run's opening transcript; `emitEvents` appended to the sink without the lane mutex; and `StreamHarnessAssistant` captured response metadata before providers produce it (plus a data race), so `AfterResponse` hooks always saw zero metadata. The retry backoff also slept abort-unaware.

## Decision

Accept materializes the whole capture: prompts and selected inbox items chain from the tip as entries (message payloads and projected writes alike, via `PendingEntryWrite`), the consumed pending payloads are deleted, and both the in-memory tip and the durable `branch.tip` advance to the last entry. `StartRun` resolves intent ids by entry existence — message entries join the prompt, materialized write entries are transcript context without a prompt message. The op.state codec is symmetric over every `OperationState` field (jsonx values stay in the jsonx model). `PlanBoundaryInbox` takes the lane name as a parameter; the global is gone. Queue writes thread `state.LastOperationID` through, and queue_update events carry the post-commit queues (the staged item joins the snapshot directly since its payload is not yet durable at plan time). `emitEvents` takes the lane mutex; response metadata is mutex-guarded and read at hook time; the backoff wait observes the drive's abort signal.

## Alternatives considered

**Resolve pending payloads in StartRun instead of materializing at accept.** Rejected: the payloads were already removed from the inbox at accept, so the boundary planner would never materialize them either; materializing at accept keeps exactly one consumer of a pending payload and makes the accept commit the atomic point where queued conversation becomes transcript.

**Serialize OperationState through its json tags with encoding/json.** Rejected: `*jsonx.Obj` fields cannot round trip through struct decoding (unexported state) — the same silent-empty class the pico3 replay note closed; the hand-written codec stays.

**Keep laneNameHolder but guard it with a mutex.** Rejected: serialization would stop the data race but not the cross-lane logical corruption — the tip write must name the lane whose commit produced it, which only per-call threading guarantees.

## Consequences

Runs accepted with queued conversation start and replay correctly; crash restore recovers the full operation position; two lanes driven concurrently cannot corrupt each other's tips (-race pinned); queue writes preserve the last-result linkage and their events reflect what watchers actually see. `PlanBoundaryInbox`/`durableStateJSON` signatures changed (pre-stable surface). Old sessions whose captured items were never materialized still fail StartRun — they failed before the fix too; there is no working caller to regress.
