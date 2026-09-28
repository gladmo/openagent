# Agent Note: ai transport hardening

Status: implemented

English | [中文](2026-09-28-ai-transport-hardening.zh.md)

## Problem

Six confirmed transport defects: the anthropic-messages transport never wired the Copilot dynamic headers (`X-Initiator`/`Openai-Intent`/vision) even though the catalog ships Copilot Claude models on it; fetch-layer failures (connection refused, DNS, reset) reached `IsRetryableProviderError` as raw `*url.Error` and were never retried — the `Status == 0` SDK-parity branch was unreachable; a `content_block_delta` whose type did not match the indexed block's kind nil-dereferenced and killed the stream; `blockFromJSON` used unchecked type assertions, so one truncated persisted block panicked the crash-recovery path; the frame encoder counted the covered boundary in runes but delta accounting and slicing in bytes, emitting invalid UTF-8; and the codex retry loop derived `abort.Any` per attempt without disposal, leaking listeners on the session-lived signal.

## Decision

Classification happens at the PostJSON/PostJSONStream seam: any non-`*ProviderHTTPError`, non-abort fetch error wraps as `&ProviderHTTPError{Status: 0}` (the APIConnectionError role), so the existing retry policy and exponential backoff apply. The anthropic transport builds Copilot dynamic headers exactly like both OpenAI transports. Mismatched deltas are skipped by gating each delta type on the block state's kind (text/thinking/toolCall), mirroring TS tolerance. `blockFromJSON` decodes via the safe `stringField`/`strPtr` accessors — missing or wrong-typed fields decode as zero values (this also fixed a shadowing bug where `thoughtSignature`/`namespace` were read off the arguments object). The frame encoder counts the covered boundary in bytes, matching delta accounting and slicing. The codex loop derives via `abort.AnyWithDispose` and disposes immediately after each attempt.

## Alternatives considered

**Wrap transport errors in DefaultFetch.** Rejected: custom `Fetch` implementations injected through options return their own errors; PostJSON is the single seam every transport crosses, so classification there covers both.

**Retry transport errors with a dedicated flag instead of Status 0.** Rejected: the status-0 branch already encodes the pinned SDKs' connection-error policy (retryable, exponential backoff, no retry-after headers); a parallel mechanism would drift from it.

**Emit an error event for mismatched deltas.** Rejected: the TS reference appends where it can and ignores where it cannot; failing the turn for a tolerable gateway quirk is the behavior the fix removes.

## Consequences

Transient network failures now retry with the SDK policy (conn-refused with MaxRetries=2 yields three attempts, pinned in [transport_defects_test.go](../../../../ai/transport_defects_test.go)); Copilot Claude requests carry the gateway routing headers on the wire; malformed persisted JSON decodes to zero values instead of panicking recovery; multibyte covered boundaries slice valid UTF-8; codex retry loops leave no listener residue. Aborts remain non-retryable (they pass through `transportError` untouched).
