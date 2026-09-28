# Agent Note: Agent event dispatch serialization

Status: implemented

English | [中文](2026-09-28-agent-emit-serialization.zh.md)

## Problem

`Agent.processEvents` ([agent.go](../../../../agent/agent.go)) reduced state under `a.mu` but invoked listeners after releasing it, so the default parallel tool execution ([agent_loop.go](../../../../agent/agent_loop.go) `executeToolCallsParallel`) ran listener dispatch — and the `AfterToolCall` hook — simultaneously from multiple goroutines whenever a model response carried two or more tool calls. That contradicted the documented contract ("invoked sequentially in subscription order") and raced in any consumer whose listener touches shared state, which is the normal case. Two adjacent defects made the entry path hostile too: `NewAgent` assigned `options.StreamFn` with no fallback, so relying on the documented `SetDefaultStreamFn` default crashed with a nil-function SIGSEGV mid-run instead of failing loud; and a tool returning `(nil, nil)` nil-dereferenced the loop's finalization.

## Decision

Event dispatch on the `Agent` is serialized end-to-end: `processEvents` holds a dedicated `emitMu` across both the state reduction and the listener invocations. The lock order is fixed at `emitMu` → `a.mu` and `emitMu` is never taken anywhere else, so no path can acquire it in reverse. Both loop entry points resolve a nil `streamFn` through `GetDefaultStreamFn()` before the first emit, returning its error verbatim — misconfiguration surfaces at the earliest resolvable point. `executePreparedToolCall` converts a `(nil, nil)` tool return into an isErrored result, mirroring how the TS loop converts a throwing tool.

## Alternatives considered

**Funnel parallel tool events through a channel consumed by the loop goroutine.** Rejected for now: it preserves ordering by construction but moves every emit through an extra hop and reorders the code path the TS reference shares between sequential and parallel execution; the mutex delivers the same contract with a smaller diff. The channel design remains the right shape if event ordering across tools ever needs to be deterministic, not just serialized.

**Serialize only listener invocation, leaving state reduction under `a.mu`.** Rejected as insufficient: a listener must observe the state produced by its own event, so the reduction and the dispatch have to be one critical section.

**Resolve the default stream function inside `NewAgent`.** Rejected: `NewAgent` returns no error, so the earliest point that can fail loud with the registry's message is loop entry, before any event is emitted.

## Consequences

Listeners and `AfterToolCall` hooks keep their sequential, subscription-order contract under parallel tools without having to lock themselves; [severe_defects_test.go](../../../../agent/severe_defects_test.go) pins this under `-race` with a deliberately unsynchronized listener. A listener that synchronously triggers another emit on the same agent would now deadlock instead of racing — such re-entrancy was never contractually supported and previously "worked" by corrupting ordering.
