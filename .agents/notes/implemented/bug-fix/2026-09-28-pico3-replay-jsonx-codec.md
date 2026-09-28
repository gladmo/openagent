# Agent Note: pico3 JSONL replay stays in the jsonx model

Status: implemented

English | [中文](2026-09-28-pico3-replay-jsonx-codec.zh.md)

## Problem

The pico3 JSONL replay path decoded persisted records back through `encoding/json` ([jsonl.go](../../../../agent/harness/pico3/jsonl.go) `jsonDecodeInto`), but jsonx objects keep their state in unexported fields, so every jsonx-typed carrier field decoded into a non-nil *empty* object: after a restart, `Entry.Data` was empty, `Entry.Model` elements came back as `map[string]any` instead of `*jsonx.Obj` (breaking every downstream role assertion), and `Task.Checkpoint`/`Outcome` were blanked. On top of that, `patchFromJSON` asserted the wrong type (`map[string]any` where the carrier holds `*jsonx.Obj`), so patch checkpoints and outcomes were dropped outright; an absent checkpoint key replayed as an explicit null (wiping checkpoints set by unrelated patches); the explicit-null case never serialized because the encoder read a private flag the producer never set; `Conversation` sections lost their `Data` on the write side for the same `encoding/json` reason; and malformed-but-parsed records (a doc write without `ref`, a patch without `patch`) nil-dereferenced the replay instead of erroring.

## Decision

The record codec is symmetric and stays in the jsonx model end to end. Write and read go through hand-written per-field carriers — `entryCarrierToJSON`/`FromJSON`, `taskCarrierToJSON`/`FromJSON`, `conversationCarrierToJSON`/`FromJSON`, `patchCarrierToJSON`/`FromJSON` — that move jsonx values as jsonx values; `encoding/json` is admitted only for carriers whose fields are all plain Go scalars (`Input`). `TaskPatchJSON` carries the checkpoint tri-state explicitly (`HasCheckpointNull` for a present null, a set `Checkpoint` for an object, nothing for an absent key), and the wire object emits `"checkpoint": null` only for explicit clears, so absent still means untouched after a round trip. `writesFromJSON` and `recordFromJSONString` reject missing payloads and non-object write entries as "malformed record" errors, replacing the panics.

## Alternatives considered

**Give jsonx objects exported fields or an `UnmarshalJSON` so `encoding/json` round trips work.** Rejected: jsonx deliberately preserves JS semantics (insertion order, numeric identity) in its internal representation; opening it to struct decoding would let `encoding/json`'s float/number and key-ordering behavior silently rewrite persisted values — the exact corruption class this note closes.

**Decode into `map[string]any` mirrors and convert per field at use sites.** Rejected: it doubles the codec surface and leaves every future carrier field one forgotten conversion away from the same silent-empty bug; per-field carriers put both directions of every field in one file.

**Treat absent checkpoint as null on replay (the previous behavior).** Rejected: the live application semantics are tri-state (untouched / clear / set); replaying absent-as-clear made replayed state diverge from live state for the same log.

## Consequences

Restart recovery preserves entry data and model roles, task checkpoints and outcomes, and conversation section data; patch checkpoint tri-state matches live application through close/reopen cycles. All of this is pinned by [jsonl_replay_test.go](../../../../agent/harness/pico3/jsonl_replay_test.go), including the malformed-record error cases. `TaskPatchJSON`'s field set changed shape (the tri-state flags replaced an `any`-typed field plus an unsettable private flag); it is pre-stable surface. Records written by the previous encoder cannot distinguish explicit null from absent — acceptable because both encoder and reader ship in the same tree and no external files exist.
