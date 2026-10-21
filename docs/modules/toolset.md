# Toolset

English | [中文](toolset.zh.md)

The `agent/harness/toolset/` package composes harness tools onto the bare agent loop. It is openagent-native (not a 1:1 port; same provenance class as `agent/harness/compaction`): the reference has no equivalent bundle because its tools load through the coding-agent session layer.

## Interface

- **`Coding()`** returns the coding four — `read`, `write`, `edit`, `bash` — as fresh harness tools.
- **`Adapt(toolset, env, gate)`** is the pure tier: it wraps `*harness.AgentHarnessTool` values into `*agent.AgentTool` without creating or owning anything. Callers that already hold an execution env and a gate use this directly.
- **`NewLocal(cwd, toolset)`** is the bundle tier: one `local` env plus one gate behind a `Local` value. `AgentTools()` feeds `AgentOptions` (initial state or per-turn); `Close()` is the single, idempotent shutdown path. The cwd must already exist — `NewLocal` never creates it.

## Execution semantics

Each adapted call runs the pi execution pipeline: `PrepareToolCall` (typebox validation) → `ApplyBeforeToolDecision` (no hook by default) → `ExecuteToolCall` (gate admission, panic-to-error conversion for tool bodies) → `FinalizeToolCall`. The loop's `abort.Signal` is woven into the chord `Context`, so cancellation propagates into file and shell operations. Admission rejections (`AbortRequested`, gate closed) surface as error returns — the agent loop's `Execute` contract — instead of unwinding into the loop.

A failed or blocked call returns an error whose text carries the tool output (a bash failure keeps its stdout/stderr); the loop marks it into the message history.

## Close ordering

`Local.Close` runs once: `gate.Close(nil)` stops admissions, `CleanupShell` kills tracked shell children, `Cleanup` runs env cleanup, and `tools.ReleaseMutationQueue(env)` drops the env's file-mutation queue state (the reference collects it with a WeakMap; Go needs the explicit release). Errors join.

## Known limitations

- `Adapt` builds `tools.ExecutionToolContext{Env: env}` for every tool; harness tools with a different context type need their own adaptation.
- The bundle tier always uses the local env; remote execution envs belong to the pure tier with caller-owned lifecycle.
