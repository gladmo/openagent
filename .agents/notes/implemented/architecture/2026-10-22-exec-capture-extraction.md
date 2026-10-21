# Agent Note: extract ExecCapture so custom environments reuse spill semantics

Status: implemented

[English](2026-10-22-exec-capture-extraction.md) | 中文

## Problem

Spill-to-temp-file lived inside `agent/harness/env/local` as private `execCapture` plumbing: the feed algorithm (buffer chunks until the view first truncates, flush the buffered prefix so the file holds the complete output, append, publish the path, fail the exec with `spill_error` on write failure) was reachable only through the local env. The reference inlines the same logic in its single `env/nodejs.ts` — reasonable for a product with one environment, wrong for openagent as a general agent framework: every custom `ExecutionEnv` (remote execution, containers, sandboxes) would have to re-port the algorithm, and it is exactly the kind of code copies get wrong (reused read buffers, missing prefix flush).

## Decision

Lift the composition into the exported `harness.ExecCapture` (`utils_exec_capture.go`, openagent-native): `NewExecCapture(env, options, onUpdate, ctx)` takes the env as an injected dependency — spill temp files land in that env's temp space via its `CreateTempFile`, mirroring the reference's `this.createTempFile`. The interface is the read loop's shape: `Push` (bounded view + spill feed), `Finish` (close the file), `SpillError` (gates the exec result), `Snapshot` (final view, publishes onUpdate). `env/local` keeps only process orchestration and consumes the component; behavior is unchanged — the existing spill tests still pass through the local env, and new harness-level tests drive the component over a non-local fake env (the reusability the extraction is for).

## Alternatives considered

**Extract only a SpillWriter (capture stays separate).** Rejected: callers would still orchestrate the capture/spill timing (the wasTruncated handshake between push and truncate check) — a shallow interface that pushes the subtle part back to every env author.

**Fold spill into OutputCapture itself.** Rejected: it would couple the currently pure in-memory view accumulator to file IO and abandon the reference's shape where the env drives `setSpillPath` from outside.

**Leave it local-private.** Rejected: the framework premise — `ShellOutputCaptureOptions.Spill` is part of the exported `ShellExecOptions` contract, so every env implementing `Exec` faces the option without the machinery to honor it.

## Consequences

- Custom environments get bounded-view + full-output persistence in four calls (NewExecCapture / Push / Finish / Snapshot); the feed algorithm has one home.
- `OutputCapture` keeps its reference shape (`SetSpillPath` externally driven); `ExecCapture` is the single openagent-native addition, provenance noted in PARITY.
- Synchronous writes assume the caller's pipe bounds the producer; environments pushing from unbounded sources must apply their own backpressure (documented on the type).
