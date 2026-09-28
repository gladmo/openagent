# Trajectory

English | [中文](trajectory.zh.md)

The `trajectory/` package records an agent run as an engineering object: an append-only log of typed records with commit-then-publish semantics. It serves debugging, auditing, replay, evaluation, and regression testing. It is an openagent-native capability modeled on the DeepSeek Harness session-event design — not a pi port.

## Composition

- **Record** (`record.go`) — the envelope: `{type, seq, time, turn, step, data, ignorable}`. `seq` is dense from 1, assigned at commit; `time` is Unix-ms, stamped at commit. `turn` counts loop turn cycles (each holds one model call plus its tool executions); `step` is reserved within the turn (the current loop always yields exactly one step per turn). `data` is a jsonx value. `ignorable=true` marks records a foreign reader may skip.
- **Trajectory** (`trajectory.go`) — the log object: `Append` (validate → commit → publish), `Subscribe`/`SubscribeAfter`, `Snapshot`, `Query`, `Close`, `RegisterKind`.
- **Recorder** (`recorder.go`) — `Attach(agent, options)` collects: one `Agent.Subscribe` listener plus a `StreamFn` wrapper; `Dispose` restores both.
- **Stream accumulator** (`stream.go`) — timed delta runs: `{type:"text-chunks"|"thinking-chunks"|"toolcall-chunks", time0, index, dt[], texts[]|args[]}` and raw `{type:"chunk", time, event}` records, in arrival order.
- **JSONL store** (`jsonl.go`) — header line + one record per line; `ReadFile` tolerates a torn final line and refuses unknown non-ignorable kinds and broken sequences.
- **Query / assembly** (`query.go`, `assemble.go`) — filtering over records; `Transcript`, `Outline`, `UsageSummaryFrom`, `Combine` folds.
- **Sinks / exporters** (`export.go`) — `StdoutSink` (bounded NDJSON), `FileSink`, `MultiSink`, `PipeSubscribe(After)`; `ExportJSONL`, `ExportJSON`, `ExportMarkdown`.
- **Replay** (`replay.go`) — `DeriveScript` → `BuildReplayAgent` (faux provider), `LoadReplayOverride`/`ApplyOverride` sidecar, `CompareRecords` regression assertion.

## Record kinds

- Session scope (`turn=0`): `session/start`, `session/end`, `error`.
- Turn scope: `turn/start`, `turn/end` (reason `completed|aborted|error|length|deferred`), `user/message` (`source: prompt|injection`), `system/message`, `agent/message` (custom roles, ignorable).
- Step scope: `step/start`, `request/header` (logical head: model, thinking level, tool declarations, message count, system prompt length), `model/input` (fidelity `full|summary|off`), `assistant/message` (message JSON, timed stream, usage, `durationMs`, `ttftMs`, `interrupted`), `assistant/attempt` (a model invocation whose final outcome was an error), `tool/call` (raw argument string), `tool/update` (optional), `tool/result` (message JSON, `isError`, `durationMs`), `llm/retry` + `llm/retry-started` (via `Recorder.RetryCallbacks()`), `step/end`.
- Reserved: `subagent/start`, `subagent/end` — emitted only through the extension API; nested agents record their own trajectories linked by `parentTrajectoryId`.

## Subscribe contract

- **Repeatable:** any number of independent subscriptions; each call returns its own disposer; disposal is idempotent. `Agent.Subscribe` is untouched — the recorder is just another listener.
- **Live-only:** `Subscribe` delivers only records committed after registration. `SubscribeAfter(afterSeq, …)` delivers the backlog (records with `seq > afterSeq`; `0` = whole log) under the commit lock, then live records — atomic hand-off, no gaps or reordering. Sinks attached after the first record use `PipeSubscribeAfter`.
- **Ordering & isolation:** listeners fire synchronously in subscription order, post-commit. A listener panic is contained and reported via `Options.OnListenerError`; it never aborts the publisher or starves other listeners (unlike `Agent.Subscribe`, where listener failures propagate — an observation plane must not break the observed agent).
- **Reentrancy:** `Append` from inside a listener fails with `ErrReentrantAppend`; `SubscribeAfter` must not be called from a listener.

## Configuration

`RecorderOptions` zero value (plus `Trajectory`) captures full model input, timed streams, and inline images capped at 256 KiB (over-cap images keep `mimeType`, `dataBytes`, `dataOmitted:true`). Fields: `ModelInput` (`InputFull|InputSummary|InputOff`), `DisableStreamDeltas`, `IncludeToolUpdates`, `MaxInlineImageBytes`, `Now` (injectable clock). `StdoutSink` bounds strings at 8 KiB and lines at 32 KiB, marking cuts with `truncated:true`.

## Not recorded

- API keys and credentials — never cross the recorder.
- Raw provider HTTP wire bodies and headers — `request/header` is the logical head; `model/input` is the normalized post-transform input.
- Provider-internal HTTP retries (`ai.RetryProviderRequest` has no observation hook) and retry-attempt partial content hidden inside a caller's retry wrapper — retries surface only through `Recorder.RetryCallbacks()` metadata. The extension seam: route retries outside the agent so each attempt is its own recorded run.
- The steer vs followUp distinction — both enter the loop as plain user messages; recorded as `source:"injection"`.
- Tool filesystem side effects (only returned content), interactive stdin/UI state, process metrics, harness-session/compaction internals (the harness session store owns its own log), sub-agent internals (reserved kinds plus parent linkage).
- Content dropped by the image cap is marked, never silently omitted.

## Known limitations

- Single-writer files, no compression, no cross-process lock, no format migration chain (format v1 only).
- Query is in-memory/linear over records; no SQL/FTS index.
- The harness event bus (`agent/harness/events.go`) is not yet a capture source.

See the demo under `examples/trajectory_demo` for the full pipeline (stdout tap + file sink + query + markdown export + replay assertion).
