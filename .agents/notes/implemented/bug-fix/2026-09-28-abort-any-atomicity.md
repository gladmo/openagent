# Agent Note: abort derived-signal atomicity and disposal

Status: implemented

English | [中文](2026-09-28-abort-any-atomicity.zh.md)

## Problem

`abort.Any()` swept its sources with `Aborted()` and only then registered `OnAbort` listeners — two separate steps. A source aborting between them never fired the derived signal: a 200k-iteration stress reproduced 123 lost aborts, and the codex transport derives per attempt from the caller's long-lived signal, so a user cancel landing in the window left a fetch running uncanceled. Two adjacent gaps shared the root: `OnAbort` (by JS contract) never fires on an already-aborted signal, and `Any` kept no disposers, so every derivation leaked listeners on the sources for their lifetime.

## Decision

Registration is atomic with the aborted-check: `Signal.register(fn, fireIfAborted)` performs the check and the listener append under one lock acquisition; when the source has already aborted and `fireIfAborted` is set, `fn` runs after the unlock (abort state is final, so deferring past the lock loses nothing). `Any` builds on it, and the new exported `AnyWithDispose` returns the aggregated disposer that removes every registration made on the sources (the derived signal keeps its state). `ToGoContext` registers with `fireIfAborted`, so an already-aborted signal yields an immediately-canceled context.

## Alternatives considered

**Poll the sources' Done() channels instead of listeners.** Rejected: a derived signal that only observes Done still needs a goroutine per source to translate reasons, and the reason of the first source to abort would race the poll.

**Return disposers from OnAbort only and have Any callers dispose.** Rejected as insufficient: the atomicity window is between check and register, not at removal; disposal alone fixes the leak, not the lost abort.

## Consequences

`Any` loses no aborts (pinned by a -race stress in [any_race_test.go](../../../../abort/any_race_test.go)); per-attempt derivations (the codex retry loop) dispose via `AnyWithDispose` instead of accumulating. `OnAbort` on an already-aborted signal now returns a no-op remove instead of retaining a listener that can never run. `FromGoContext`'s goroutine leak for never-done contexts is the remaining same-family gap (needs a disposer-owning API); it stays on the backlog.
