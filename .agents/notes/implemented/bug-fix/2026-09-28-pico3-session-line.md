# Agent Note: pico3 session line concurrency

Status: implemented

English | [中文](2026-09-28-pico3-session-line.zh.md)

## Problem

The pico3 Session ported single-threaded TS semantics onto goroutines unsafely: the package-global `sessionOwners` map, the `LiveTasks`/`ConversationRecords`/`OwnerTaskCache` state written by the tail goroutine and read unsynchronized by `Subtree`/`Ancestors`, and the listener list iterated without the lock were all data races (fatal for maps). A synchronous `Commit` issued from a commit callback or listener enqueued behind the tail job it was blocking, deadlocking both forever. `Close` never closed the tail queue, leaking one goroutine plus a 1024-slot buffer per session. The legacy tracker shallow-cloned its base and working copies, so nested in-place mutations aliased both sides and `Flush` emitted zero ops.

## Decision

All shared state is mutex-guarded (`s.mu`), including `sessionOwners` via package helpers; listeners are snapshotted under the lock and dispatched outside every session lock. Re-entrancy runs inline: the tail worker sets an `onTail` atomic exactly while executing a job, and a `Commit` observing it executes its job inline instead of enqueueing. The job body separates the transaction phase (invoker checks, storage commit, applyChanges — serialized on `txnMu`) from listener dispatch (never under `txnMu`), so the inline path can never deadlock against the worker and transactions never overlap even when a non-tail caller races the window where `onTail` is briefly true. Close drains the tail with a sentinel and closes the queue under the same mutex protocol as the storage queues. The tracker deep-copies base and working (chord CopyJson), making nested mutations visible to the diff.

## Alternatives considered

**Detect the tail goroutine precisely and error on foreign re-entrancy.** Rejected: Go exposes no sound goroutine identity; the atomic-observation design makes false positives benign (the job serializes on `txnMu` like any other) instead of converting the deadlock into an error that plugin code cannot handle.

**Dispatch listeners asynchronously after the job so their commits enqueue normally.** Rejected: it breaks the synchronous listener contract (tests and plugins await listener effects) and makes listener-versus-next-commit ordering nondeterministic; TS awaits listeners within the commit's continuation.

**Keep the shallow clone and diff recursively.** Rejected: a recursive diff would recover the ops but the aliased base still reports the working state after mutations are applied, so a no-op flush would emit phantom ops; deep copy restores true before/after semantics.

## Consequences

Concurrent `Subtree`/`Ancestors` during commits and listener-initiated commits are pinned under `-race`; Close leaves no goroutine behind; the tracker's nested mutations flush as Set ops and the next flush is quiet. Ordering note: an inline re-entrant commit completes before the outer `Commit` returns (nested), where the TS promise chain resolves the outer first — final state is identical, only intermediate event interleaving differs.
