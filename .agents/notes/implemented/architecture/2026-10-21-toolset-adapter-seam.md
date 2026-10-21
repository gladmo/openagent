# Agent Note: place the harness→agent-loop tool adapter in its own toolset package

Status: implemented

[English](2026-10-21-toolset-adapter-seam.md) | 中文

## Problem

Loading harness tools into a bare `agent.NewAgent` loop required every caller to hand-assemble five facts: create an execution env (plus remember its cleanup), create a gate (two views, plus `Close`), pick tool factories, hand-write the pipeline adapter (Prepare → Before → Execute → Finalize, abort→chord context weaving, update-callback adaptation, error-text extraction), and loop them into `AgentOptions`. The only in-tree implementation lived in `examples/coding_agent`, so every new consumer would copy it — and the copy is exactly where subtle contracts live: the agent loop's `Execute` must return errors, while the pipeline signals admission rejection by unwinding.

## Decision

Extract the adapter into `agent/harness/toolset` (module page: [docs/modules/toolset.md](../../../../docs/modules/toolset.md)) with a two-tier interface: `Adapt(toolset, env, gate)` is a pure wrapper for callers that own their env and gate; `NewLocal(cwd, toolset).AgentTools()/Close()` bundles one local env plus one gate behind a single idempotent disposer (`Close` order: gate → shell children → env cleanup → mutation-queue release). `Coding()` provides the read/write/edit/bash four. The package is openagent-native (same provenance class as `agent/harness/compaction`), not a 1:1 port.

At the adapter seam, admission panics (`*harness.AbortRequested`, gate-closed) convert to error returns: the loop's `Execute` contract. The pipeline's own panic conversion covers only tool bodies; an admission panic escaping the adapter would crash the loop, which has no recover.

## Alternatives considered

**Put the adapter in the `agent/harness` root package.** Rejected on the import graph: the adapter needs `tools.ExecutionToolContext`, and `tools` imports `harness`, so the root package would cycle.

**Put it in `agent/harness/tools`.** Rejected to keep the 1:1 port package mirroring the reference's `harness/tools/*`; the adapter has no TS counterpart (the reference loads tools through the coding-agent session layer).

**Only ship the `Local` bundle.** Rejected: hiding env/gate creation removes the pure tier for callers that already own them (shared gate across toolsets, remote envs) and makes the bundle untestable without filesystem lifecycle.

**Only ship the pure `Adapt`.** Rejected: the bundle tier is the point of the request — three lines from nothing to `AgentOptions.Tools` — and the disposer convention ("registrations are effects") wants one owner for env + gate + queue state.

## Consequences

- `tools.ReleaseMutationQueue(env)` becomes exported API: the port replaced the reference's WeakMap with a plain map, so queue state leaked per env; the bundle's `Close` now completes the registration-cleanup convention.
- `Local.Close` also runs `CleanupShell` (kill tracked child processes), which the example previously skipped.
- `Adapt` builds `tools.ExecutionToolContext{Env: env}` for every tool; tools requiring another context type need their own adaptation (documented limitation).
- The example drops to a showcase of the bundle; its pipeline-level tests keep running against `toolset.Adapt`.
