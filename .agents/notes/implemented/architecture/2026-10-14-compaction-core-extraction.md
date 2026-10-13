# Agent Note: extract the compaction core for reuse outside codingagent

Status: implemented

[English](2026-10-14-compaction-core-extraction.md) | 中文

## Problem

The compaction machinery (threshold decision, cut-point search, split-turn handling, retry-wrapped LLM summarization) lived only in `codingagent`, welded to its `SessionManager`/`SessionEntry` projection model. Every other consumer of the agent stack — session services, web backends, custom harnesses — that needs durable context management faces a choice between importing the banned-for-them coding agent (an entire CLI with TUI coupling) and rewriting the algorithms from scratch. The trigger decision (`agent/harness/runtime.ShouldCompact`) was already reusable, which made the missing executor an asymmetric gap.

## Decision

Extract the provider-agnostic compaction core into `agent/harness/compaction` (module page: [docs/modules/compaction.md](../../../../docs/modules/compaction.md)). The package owns: token estimation (`EstimateTokens`, `EstimateContextTokens`), cut-point planning over a linear `PathEntry` path (`FindCutPoint`, `PrepareCompaction`), the reference summarization prompts, and the retry-wrapped summarization executor (`GenerateSummary`, `GenerateTurnPrefixSummary`, `Compact`) behind an injected `SummarizationStreamFn`. `codingagent` keeps its existing implementation unchanged; the new package is the seam for non-codingagent consumers, documented as an openagent-native extraction (like `trajectory/`), not a pi 1:1 port.

## Adaptations carried by the extraction

- Linear `PathEntry` replaces the codingagent `SessionEntry` projection; context-edit recovery logic has no linear equivalent and is omitted.
- Custom coding-agent message roles materialize as framed user messages (`FramedSummary`), so cut-point predicates cover the four ai roles.
- Transport credentials (`ApiKey`/`Headers`/`Env`) stay with the injected stream function.
- Branch summarization (interactive-UI feature) is not ported.

## Alternatives considered

**Let consumers import `codingagent`.** Rejected: `codingagent` is a product surface (CLI + TUI), not a library; downstream projects (yian-agent among them) ban the import by policy, and the dependency would drag session files, extension runners, and UI code into every service.

**Copy the algorithms into each consumer.** Rejected: the cut-point walk and split-turn semantics are subtle, pinned by the reference; divergent copies would drift and golden tests would not protect them.

**Adopt the harness `runtime` compaction pipeline instead.** Rejected for now: the runtime's `PrepareCompactionThreshold` executor seam expects a lane/drive integration that no production consumer drives yet; extracting the pure core serves both the direct path (agents over `Agent.SetMessages`) and a future runtime injection.

## Consequences

- Non-codingagent sessions gain reference-faithful compaction: same prompts, same retry choke point, same failure refusals (length stops never persist, tool-call summaries rejected).
- Two compaction integrations exist in the tree (codingagent's and this package's); they share provenance but not code. Consolidating codingagent onto the extracted core is future work gated on migrating `SessionManager` projections.
- The `PathEntry` contract requires callers to materialize context contributions as ai messages; anything else estimates as zero tokens (documented limitation).
