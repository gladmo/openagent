# Agent Note: Storage concurrency and close protocol

Status: implemented

English | [中文](2026-09-28-storage-concurrency-and-close.zh.md)

## Problem

The session storage layer was written as a 1:1 port of single-threaded TypeScript and crashed under any concurrency: [InMemoryStorageState](../../../../agent/harness/session/memory_state.go) declared a mutex it never took, so a read concurrent with a commit hit Go's unrecoverable `concurrent map read and map write` fatal; the `enqueue`/`Close` pairs in [memory.go](../../../../agent/harness/session/memory.go) and [jsonl/storage.go](../../../../agent/harness/session/jsonl/storage.go) checked status under the lock but sent on the queue after releasing it, racing `close(queue)`; and any `Commit` overlapping `Close` panicked (`JsonlStorage is closed`). Three more defects rounded out the family: `JsonlStorage.backing` was written by the worker goroutine during v3→v4 upgrade while callers read it unsynchronized, `MemorySessionRepo.Open` reopened sessions over a fresh empty state (close→reopen lost everything), and `SelectBranchFork` treated a root entry's nil parent as corruption because the JS `undefined`/`null` distinction had collapsed into one `nil`.

## Decision

The storages are safe for concurrent use and fail closed with errors. `InMemoryStorageState` takes its RWMutex on every method — write lock in `ApplyValidated`/`AdvanceNextSeq`, read lock in all readers — with unexported `*Locked` helpers for call paths that nest (`ScanBranchStructure`, `CreateFork`→`selectForkPlan`). The queue protocol holds the mutex across the send itself and across `close(queue)`, so a sender that passed the open-status check completes its send before `Close` can close the channel; jobs never take the storage mutex, which keeps a full buffer draining and rules out deadlock. `Commit` after (or racing) `Close` returns a "storage is closed" error — the faithful Go mapping of the reference's throw at an API whose signature already returns `error`, replacing the previous process-killing panic. `JsonlStorage.backing` is read and written under `s.mu`. `MemorySessionRepo.Open` reopens over the recorded state object, which survives its storage's `Close` as plain data. `SelectBranchFork`'s `GetParent` callback returns `(*string, bool)`: `(nil, true)` is a root, `(_, false)` is a missing entry.

## Alternatives considered

**Route all reads through the commit queue.** Rejected: it serializes reads behind every commit's file I/O and changes reader latency characteristics for no correctness gain over an RWMutex; the queue exists to serialize commits, not reads.

**Guard sends with a `closed` flag instead of lock-held sends.** Rejected: a check-then-send pair without the lock held across both still admits send-after-close; holding the mutex across a buffered-channel send is sound here precisely because jobs never re-acquire it.

**Keep the panic on Commit-after-Close (loud failure).** Rejected: the panic fired on ordinary shutdown races in the commit path, and `Commit` already returns `error`; a descriptive error is equally loud and recoverable, matching how the TS reference surfaces the same condition as a rejected promise rather than a process crash.

## Consequences

Concurrent session reads during commits, `Commit` racing `Close`, close→reopen cycles, and branch-scope forks are pinned by [concurrency_test.go](../../../../agent/harness/session/concurrency_test.go) under `-race`. Callers must treat a closed-storage error from `Commit` as terminal for that storage instance (previously they crashed instead, so no working caller can regress). The `GetParent` signature change is pre-stable API surface; it is the only external shape change in this note.
