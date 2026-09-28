# Agent Note: Trajectory as a first-class module

Status: implemented

## Problem

Debugging, auditing, replaying, evaluating, and regression-testing an agent run all need the same artifact: a faithful, structured record of what the run did — model inputs and outputs, chain-of-thought, tool traffic, retries, errors, timings, token usage, images. The pi reference stack has no such capability (its harness session persists durable conversation state, not observations), and openagent's porting contract pins `agent/` and `ai/` to that reference, so no ported surface could grow this without inventing behavior. The question was where a DeepSeek-Harness-equivalent trajectory capability should live and what its object model should be.

## Decision

A new top-level package `trajectory/`, openagent-native (not a pi port; PORTING.md and PARITY.md carry no entry by design), importing `agent`/`ai`/`jsonx` with no reverse dependency. Its object model follows the DSH session-event design: an append-only log of typed records `{type, seq, time, turn, step, data, ignorable}` with commit-then-publish semantics; a `Trajectory.Subscribe` that is repeatable, disposer-returning, live-only, subscription-ordered, and panic-isolated; a `SubscribeAfter` catch-up primitive whose backlog delivery happens under the commit lock so sinks attaching late still write a valid file; a `Recorder` that collects through exactly two seams — one `Agent.Subscribe` listener plus a `StreamFn` wrapper — so no loop behavior changes; JSONL storage with torn-tail tolerance and read-side refusal of unknown non-ignorable kinds; folds for transcript/outline/usage; bounded stdout NDJSON as the default tap; and replay as script derivation onto the existing `ai` faux provider, honoring the previously unconsumed `AgentTool.Replay` marker. The full kind catalog, the Subscribe contract, and the explicit not-recorded list live in [the module page](../../../../docs/modules/trajectory.md).

## Alternatives considered

**Extend the harness session store (`agent/harness/session`) instead of a new package.** Rejected: that store is a pinned pi port whose shape is the durable conversation tree; folding observations (failed attempts, retry chains, per-chunk stream timings, full model inputs) into it would both perturb a ported surface and overload two different lifetimes — conversation state is rewritten by compaction, observations must never be.

**Make `Subscribe` replay history to late subscribers.** Rejected: replay-on-subscribe forces every listener to choose between missing history and double-delivery semantics, and it breaks the post-commit fire-and-forget contract that keeps publishers unaffected by observer cost. History reads stay explicit (`Snapshot`/`Query`); the one legitimate replay consumer — storage sinks — gets `SubscribeAfter` with an atomic backlog hand-off under the commit lock instead.

**Capture provider-internal retries by adding an observation hook to `ai.RetryProviderRequest`.** Rejected for v1: `ai` is parity-pinned and the hook would be a new surface with no reference; retries surface instead through `Recorder.RetryCallbacks()` (wired where the caller applies `ai.RetryAssistantCall`), and the module page documents the seam (retry outside the agent makes every attempt its own recorded run).

**Turn/step coordinates inside each record's payload (DSH-faithful) instead of in the envelope.** Rejected: Go consumers filter and group on turn/step constantly; envelope placement keeps the payload schemas payload-only and makes scope validation (`session/turn/step`) a single envelope rule instead of per-kind invariants.

## Consequences

Trajectory files are a parallel artifact, not the harness session log; tooling that needs both reads both, linked by trajectory id. The recorder duplicates nothing the loop already persists and never blocks it: observer failures are contained and reported through `Options.OnListenerError`. Replay regression (`CompareRecords`) ignores seq/time/usage/stream timings by normalization — deterministic tools compare verbatim, real-filesystem tools need pre-normalized fixtures. Known gaps are stated on the module page: no compression, locks, migration chain, or SQL index; the harness event bus is not yet a capture source; steer vs followUp is indistinguishable at loop-event level and recorded as `injection`. Verification: `go test ./trajectory/ ./examples/trajectory_demo/` (unit, full-flow, race-enabled) plus `gofmt -l .` and `go vet ./...`.
