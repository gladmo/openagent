# Agent Note: Agent-collaboration scaffold adoption

Status: implemented

English | [中文](2026-09-28-agent-scaffold-adoption.zh.md)

## Problem

The repository carried Go sources, tests, and two port-tracking documents ([PORTING.md](../../../../PORTING.md), [PARITY.md](../../../../PARITY.md)) with no standing orders for agents, no documentation standard, no decision records, and no deterministic gates: nothing mechanically kept documentation paired, within budget, or structurally consistent, and durable decisions had nowhere durable to live.

## Decision

The repository adopts the meta-project scaffold (distilled from deepseek-harness): root AGENTS.md standing orders with `CLAUDE.md` symlinked to it; the tiered bilingual documentation standard under [docs/](../../../../docs/AGENTS.md); RFC-style [Agent Notes](../../README.md); the pre-push-checks and code-review skills; and zero-dependency Node gates aggregated by [run-gates.mjs](../../../../scripts/run-gates.mjs), wired as lefthook's pre-commit. The template's optional performance-lane and website tiers are declined: their budgets and build were calibrated on the source machine and dependency tree, and no Go measurement surface or docs site exists yet; both can be copied from the template later. PARITY.md and PORTING.md — single-language port work-papers that retire when the port completes — join the unpaired name set in [scripts/lib/md.mjs](../../../../scripts/lib/md.mjs) instead of being translated.

## Alternatives considered

**Translate PARITY/PORTING into bilingual pairs.** Rejected: they are transient tracking documents of an in-progress port; the pairing tax on every edit buys nothing for files slated to shrink into an audit result.

**Adopt the deepseek-harness layout wholesale.** Rejected: it is TypeScript-specific (translation catalogs, TS-only gates); the template already carries the language-agnostic distillation.

**No scaffold.** Rejected: the port is agent-driven work; without standing orders and gates, documentation drifts and decision rationale evaporates.

**Keep the benchmark lane.** Rejected for now: its only case measures the gate scripts against budgets calibrated on the template's machine; re-recording is a separate decision once a Go measurement surface exists.

## Consequences

Every commit runs run-gates locally once the hook is installed (sub-second, offline); human-facing documents live as `.md`/`.zh.md` pairs that update together; durable decisions land as Agent Notes with a supersession check. Adding the benchmarks or website tier later means copying it from the template and recording its expectations from a fresh calibration run.
