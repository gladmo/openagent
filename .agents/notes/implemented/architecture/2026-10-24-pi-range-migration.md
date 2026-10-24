# Agent Note: Migrating the pi a276dabe5..bd3c9b823 range

Status: implemented

English | [中文](2026-10-24-pi-range-migration.zh.md)

## Problem

The pi reference moved 333 commits past the port's pinned surfaces (`ff72faba2` for the tree, `a276dabe5` for MCP). The range mixes fixes to already-ported surfaces with four whole new subsystems (durable ≈27K lines, the chord2 host runtime ≈9.4K, pi-env SSH environments, codemode enhancements). Porting everything would couple an unfinished port to a moving reference; porting nothing would leave the Go project without the range's fixes to surfaces it ships daily.

## Decision

Migrate only the changes that land on ported surfaces, in seven ordered batches (telemetry foundation → identity/timing → ai fixes → mcp → tui → codingagent → catalog refresh), each batch landing with ported reference tests. Defer the four new subsystems and unported provider surfaces with a written gap ledger in PARITY.md; they have no consumer among the ported surfaces (coding-agent imports chord zero times at HEAD; nothing imports durable or pi-env).

## Alternatives considered

**Port the whole range.** Rejected: durable alone is larger than everything migrated here and is hard-blocked on the chord2 runtime; the port would be a partial copy of everything.

**Cherry-pick fixes only, skip features.** Rejected: the range's fixes and features are interleaved (the tool-support rework is both), and the port discipline pins behavior to the reference, not to a hand-picked subset of it.

## Consequences

- The ported surfaces now track `bd3c9b823`; the reference pin is per-surface (tree at `ff72faba2` + range migrations; MCP stack `a276dabe5` + range fixes; catalog snapshot at `bd3c9b823`). PARITY.md owns the mapping and the deferred ledger.
- Documented Go deviations introduced by the range: telemetry carrier tables are instance-scoped (no WeakMap); RenderTrace orders attributes by key; span timestamps truncate to whole milliseconds; the detailed User-Agent reports the Go runtime; exit-restore rides `signal.Notify` (direct `os.Exit` and foreign-goroutine panics stay unrestorable); RE2's missing lookarounds are replaced by boundary-consuming classes (status codes) and a procedural scan (terminal replies); CJK-friendly emphasis flanking is approximated by the punct+word closer rule plus the rune-width fix the approximation exposed.
- The exit-restore signal handler owns process-lifetime semantics for SIGINT/SIGTERM/SIGHUP while a TUI runs: it restores, resets the disposition, re-raises, and exits — even when racing a normal Stop, and it preempts any host application's own `os/signal` handlers for those signals (none exist in this repo today; the TS reference used a process-exit hook and never kills).
- The deferred ledger is the input to any future durable/chord2 decision; deferral blocks nothing in the ported tree today.
- Verified per batch with the owning package tests, then `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...`, and `node scripts/run-gates.mjs`. Supersession check at creation: the [MCP port-ahead note](2026-10-14-mcp-port-ahead-of-baseline.md) stays active (its M2 deferral is unchanged and now also listed in the range ledger); no other note covers baseline moves.
