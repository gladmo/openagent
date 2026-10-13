# Compaction

English | [中文](compaction.zh.md)

The `agent/harness/compaction/` package is the provider-agnostic compaction core: token estimation, cut-point planning over a linear session path, and the retry-wrapped LLM summarization that turns a planned cut into a checkpoint summary. It is extracted from the `codingagent` compaction port (pi `core/compaction/compaction.ts`) so non-codingagent consumers can compact sessions without importing the coding agent.

## Composition

- **Settings** (`compaction.go`) — `Enabled`, `ReserveTokens`, `KeepRecentTokens`; `DefaultSettings` mirrors the reference (true, 16384, 20000). `ShouldCompact(contextTokens, contextWindow, settings)` is the threshold decision.
- **PathEntry** (`compaction.go`) — one session entry's context contribution on a linear branch path: durable `ID`, `IsCompaction` marker, raw `Summary` text and `Details` payload (compaction entries), and `Messages` (the entry's context-visible agent messages; a compaction checkpoint's messages carry the framed summary plus the retained tail, display-only entries carry none).
- **Cut points** (`compaction.go`) — `FindCutPoint` walks backwards accumulating estimated tokens until the keep-recent budget is met, snaps to the nearest valid cut entry (user or assistant; never tool results), pulls adjacent zero-contribution entries into the kept suffix, and reports split turns (`CutPointResult.IsSplitTurn`, `TurnStartIndex`).
- **Preparation** (`compaction.go`) — `PrepareCompaction(path, settings)` returns nil when nothing is compressible (the path already ends in a contributing compaction, or no message falls before the cut point); otherwise a `Preparation` carrying the messages to summarize, the split-turn prefix, the previous checkpoint summary, token totals, and file operations.
- **Token estimation** (`tokens.go`) — `EstimateTokens` (chars/4 per message, per-block for assistants), `CalculateContextTokens` (native totalTokens, else components), `EstimateContextTokens` (last valid assistant usage plus trailing estimates; aborted/errored/zero-usage assistants carry no usable usage).
- **Summarization** (`summarization.go`) — the structured-summary prompts (initial, update, turn-prefix), `GenerateSummary` / `GenerateTurnPrefixSummary`, `CompleteSummarization` (the retry-wrapped choke point through `ai.RetryAssistantCall`; one-off summaries get `CacheRetention: "none"` and a fresh session id), and `Compact(preparation, model, options, streamFn)` which produces the `Outcome`.
- **Serialization** (`utils.go`) — `SerializeConversation` (labeled text so the model summarizes instead of continuing; tool results truncated at `ToolResultMaxChars` 2000), file-operation tracking (`read`/`write`/`edit` tool calls with a `path` argument), `FormatFileOperations` (the `<read-files>`/`<modified-files>` suffix), and `SummarizationSystemPrompt`.

## Summarization seam

`SummarizationStreamFn` is the provider seam: `func(ctx, model, contextMessages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error)`. Transport credentials live in the injected function; `SummarizationOptions` carries only advisory call parameters (`MaxTokens`, `ThinkingLevel`, `SessionID`, `CacheRetention`). `GenerateSummaryOptions.ReserveTokens` bounds the summary budget (`maxTokens = min(0.8 × reserve, model.MaxTokens)`; turn-prefix summaries use 0.5 × reserve).

## Failure behavior

- `GetSummarizationFailure`: an error stop surfaces the provider message; a length stop refuses to persist ("the summary is incomplete") — partial text never becomes a checkpoint.
- A summary that attempts a tool call is rejected.
- Transient stream errors retry through the supplied `*ai.RetryPolicy`; deterministic errors (for example invalid credentials) do not retry.
- `Compact` fails on a nil preparation or a preparation whose first-kept entry lacks an ID.

## Adaptations from the codingagent reference

- The entry model is the linear `PathEntry` path (the harness session branch shape) instead of codingagent's `SessionEntry` projection; context-edit projection logic has no equivalent.
- Coding-agent custom roles (`bashExecution`, `custom`, `branchSummary`, `compactionSummary`) do not exist here; a checkpoint's context contribution materializes as a framed user message (`FramedSummary`), so predicates cover the four ai roles.
- The reference's `ApiKey`/`Headers`/`Env` plumbing is absent (the stream seam owns transport).
- Branch summarization (an interactive-UI feature) is not ported.

The summarization prompts, the retry-wrapped call, the split-turn handling, and the cut-point walk are the reference behavior.

## Known limitations

- File-operation tracking recognizes only the reference's `read`/`write`/`edit` tool names with a `path` argument; agents with other tools produce no file lists.
- `EstimateTokens` returns 0 for non-ai message types; callers materialize every `PathEntry.Messages` entry as an ai message.
