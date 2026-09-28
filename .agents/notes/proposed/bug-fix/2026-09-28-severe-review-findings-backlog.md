# Agent Note: Severe review findings backlog

Status: proposed

English | [中文](2026-09-28-severe-review-findings-backlog.zh.md)

## Problem

A full review of the initial import (commit `d4e0b58`) confirmed roughly twenty severe defects. Thirteen crash-class, race-class, and data-loss defects were fixed immediately with owning tests (see [storage concurrency](../../implemented/bug-fix/2026-09-28-storage-concurrency-and-close.md), [agent emit serialization](../../implemented/bug-fix/2026-09-28-agent-emit-serialization.md), [pico3 replay codec](../../implemented/bug-fix/2026-09-28-pico3-replay-jsonx-codec.md)); the remainder were verified and reproduced but remain unfixed, and without a durable record the review's evidence lives only in a conversation that ends.

## Proposal

Work the backlog below in order of cluster, promoting each cluster to its own implemented note as it lands. Every item was reproduced with throwaway probes or the race detector against the reviewed commit; none are speculative.

### Inventory

| # | Sev | Cluster | Defect and location |
|---|---|---|---|
| 1 | FIXED — [runtime state machine](../../implemented/bug-fix/2026-09-28-runtime-state-machine.md) — | CRIT | runtime accept | Captured steer/followUp/nextRun items never materialize as entries, so every accept→start run with a queued item dies at `StartRun` ("Run prompt entry is missing its message"); payloads orphaned in `pi.pending.entry` — [accept.go](../../../../agent/harness/runtime/accept.go) |
| 2 | FIXED — [runtime state machine](../../implemented/bug-fix/2026-09-28-runtime-state-machine.md) — | CRIT | runtime durability | Durable `op.state` serializes only 8 of ~25 `OperationState` fields (drops continuation, triggerEntryId, generationContext, responseEntryId, attempt, notBefore, poll…) — [lane_json.go](../../../../agent/harness/runtime/lane_json.go) |
| 3 | FIXED — [pico3 session line](../../implemented/bug-fix/2026-09-28-pico3-session-line.md) — | HIGH | pico3 session | Data races: global `sessionOwners` map unlocked; `LiveTasks`/`ConversationRecords` written on the tail goroutine vs unlocked `Subtree`/`Ancestors` reads; `listeners` iterated unlocked — [session.go](../../../../agent/harness/pico3/session.go) |
| 4 | FIXED — [pico3 session line](../../implemented/bug-fix/2026-09-28-pico3-session-line.md) — | HIGH | pico3 session | Re-entrant `Session.Commit` (from a listener or commit callback) deadlocks permanently on the single tail goroutine — [session.go](../../../../agent/harness/pico3/session.go) |
| 5 | FIXED — [pico3 session line](../../implemented/bug-fix/2026-09-28-pico3-session-line.md) — | HIGH | pico3 session | `Close` never closes the tail queue: one goroutine + 1024-slot buffer leaked per session — [session.go](../../../../agent/harness/pico3/session.go) |
| 6 | FIXED — [pico3 session line](../../implemented/bug-fix/2026-09-28-pico3-session-line.md) — | HIGH | pico3 tracker | `legacy_tracker` shallow-clones, so nested in-place mutations alias base and working; `Flush` emits zero ops — [legacy_tracker.go](../../../../agent/harness/pico3/legacy_tracker.go) |
| 7 | FIXED — [ai transport hardening](../../implemented/bug-fix/2026-09-28-ai-transport-hardening.md) — | HIGH | ai copilot | Copilot dynamic headers (`X-Initiator`/`Openai-Intent`/vision) never wired on the anthropic-messages transport — [api_anthropic_messages.go](../../../../ai/api_anthropic_messages.go) |
| 8 | FIXED — [abort atomicity](../../implemented/bug-fix/2026-09-28-abort-any-atomicity.md) — | HIGH | abort shim | `abort.Any()` check-then-register race silently drops aborts (123 lost per 200k in a stress test) — [abort.go](../../../../abort/abort.go) |
| 9 | FIXED — [runtime state machine](../../implemented/bug-fix/2026-09-28-runtime-state-machine.md) — | HIGH | runtime lane | Package-global `laneNameHolder` races and can write lane A's tip under lane B's name — [boundary.go](../../../../agent/harness/runtime/boundary.go) |
| 10 | FIXED — [runtime state machine](../../implemented/bug-fix/2026-09-28-runtime-state-machine.md) — | HIGH | runtime lane | `QueueMessage`/`CancelQueued` wipe durable `lastOperationId`; `AcceptRun` never advances durable `branch.tip`; `emitEvents` appends without the lane mutex — [queue.go](../../../../agent/harness/runtime/queue.go), [accept.go](../../../../agent/harness/runtime/accept.go), [lane.go](../../../../agent/harness/runtime/lane.go) |
| 11 | OPEN (partial) | runtime misc | Metadata capture and abort-aware backoff: FIXED — [runtime state machine](../../implemented/bug-fix/2026-09-28-runtime-state-machine.md). Remaining: `PlanResponseWrites` builds the response entry with nil parent (transcript orphaning when wired) — [response_settle.go](../../../../agent/harness/runtime/response_settle.go) |
| 12 | FIXED — [ai transport hardening](../../implemented/bug-fix/2026-09-28-ai-transport-hardening.md) — | MED | ai transports | Transport errors (conn refused/DNS/reset) never retried — the `Status == 0` SDK-parity branch is unreachable; anthropic delta/block kind mismatch nil-derefs the stream; codec/frame decoders panic on malformed persisted JSON (hits crash recovery); frame encoder mixes runes and bytes (invalid UTF-8 deltas); codex retry leaks `abort.Any` listeners — [provider_retry.go](../../../../ai/provider_retry.go) et al. |
| 13 | MED | ported libs | jsonx lone surrogates and out-of-range exponents deviate from JS; `Stringify` panics on non-string-key maps; chord delta empty-path ops panic; typebox Integer ≥2^63 false-reject and UTF-16 length counting; builder constraint options serialize but never validate; ignore exponential backtracking — see the ai/libs review records |

## Alternatives considered

**File one issue per defect in an external tracker.** Rejected: the repository's durable-decision home is Agent Notes; a tracker outside the tree would drift from the code it describes.

**Fix everything in one pass before recording anything.** Rejected: the runtime clusters need design decisions (re-entrancy semantics, sidecar retirement) that should be made against a recorded inventory, not from memory of a chat transcript.

**Record only the clusters, not the per-defect lines.** Rejected: the file:line evidence is what makes each item actionable without re-review; dropping it re-costs the review.

## Acceptance criteria

- Items 1–2 and 9–11 (runtime clusters): their behavior is pinned by owning tests in `agent/harness/runtime/` and each cluster's decision is recorded in its own implemented note.
- Items 3–6 (pico3 session cluster): `go test -race` covers concurrent `Subtree`/`Ancestors`/listener use, re-entrant commit resolves rather than hangs, and `Close` leaves no goroutine behind.
- Items 7–8 and 12–13: each fix ships with the reproducing test from the review (transport-retry classification, copilot headers on the wire, `abort.Any` stress, decoder panics).
- This note moves to `implemented/` (or is split per cluster with cross-links) as clusters land, per the supersession rules.

## Risks

The inventory's line numbers age as code moves; treat locations as entry points, not exact coordinates. The runtime accept/durability items (1–2) describe P7-era semantics that may be deliberately re-planned — if so, record the re-plan as a rejection of those items rather than silently dropping them. Fixing pico3 session re-entrancy (item 4) may change listener ordering semantics; that decision needs its own note when made.
