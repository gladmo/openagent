# AGENTS.md

openagent — a Go 1:1 rewrite of the pi agent stack (`agent` v0.87.1 plus its `ai`, `chord`, and `telemetry` closures). Read [docs/architecture.md](docs/architecture.md) before changing `agent/` or `ai/`; follow [docs/AGENTS.md](docs/AGENTS.md) for documentation.

## Stability

Pre-stable port in progress: [PORTING.md](PORTING.md) tracks phases against the TypeScript reference at commit `ff72faba2`, and [PARITY.md](PARITY.md) audits per-file parity. Until the port completes, public APIs follow the reference and may change; a behavior or API change updates the owning tests and PARITY.md in the same commit.

## Repository layout

```
agent/       Agent loop, proxy, search, harness, and tools
ai/          Model-agnostic provider stack: types, events, retry, catalog, providers
chord/       Shared contracts: JSON model, context chain, delta ops, services
telemetry/   Span telemetry with NOOP and in-memory contexts
abort/       AbortSignal/AbortController shim, nil-safe
jsonx/ typebox/ partialjson/ diff/ ignore/   Ported npm libraries, JS semantics preserved
examples/    Runnable example agents
docs/        Documentation; the tier standard lives in docs/AGENTS.md
scripts/     Zero-dependency verification gates (scripts/AGENTS.md)
.agents/     Agent workflows and decision records (.agents/notes/README.md)
```

## Commands

```sh
go mod download            # fetch dependencies (gopkg.in/yaml.v3 only)
go test ./...              # unit, parity, and golden tests
gofmt -l .                 # lint: prints nothing when formatting is clean
go vet ./...               # static analysis
go build ./...             # compile every package
node scripts/run-gates.mjs # commit hygiene, documentation, and decision-record gates
```

### Run relevant checks locally

Before pushing, follow [pre-push-checks](.agents/skills/pre-push-checks/SKILL.md) and report only the commands you ran. Match evidence to the surface: the owning package test for code changes, `run-gates` for docs and Agent Notes, real-API smoke tests for provider changes. Never default to the full suite; CI owns exhaustive coverage and the platform matrix.

## Secrets / .env

Never commit credentials. Tests that require secrets self-skip when they are absent; [docs/testing.md](docs/testing.md) owns the key policy.

Live provider smoke tests require `PI_SMOKE_TESTS=1` plus the target provider's key — for example `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `OPENROUTER_API_KEY`, `ZAI_CODING_CN_API_KEY`; the authoritative table is `ai/env_api_keys.go`.

## Conventions

- **One home per fact.** State a rule once at its owning tier and link there; [docs/AGENTS.md](docs/AGENTS.md) owns the tier table.
- **Durable decisions get an [Agent Note](.agents/notes/README.md)** recording the why, the alternatives considered, and the consequences; mechanical edits are exempt. Every new note triggers the [supersession check](.agents/notes/AGENTS.md).
- **Registrations are effects.** Every listener, registration, or side effect has a matching cleanup on shutdown; the registration API returns the disposer.
- **Explicit > implicit at boundaries.** Defaulting is one named resolve step in the owning module, never a hidden fallback inside execution logic.
- **Misconfiguration fails loud** at load, or at the earliest resolvable point; never silently skip a missing referent.
- **Switch on discriminant tags;** closed unions end in an exhaustive check.
- **Tests describe behavior, not correctness.** Change obsolete behavior together with its tests and explain why in the PR.
- **Docs accompany every code change:** update the affected README and reference docs in the same commit ([standard](docs/AGENTS.md)).
- **Comments state contracts, not reasoning transcripts.** Keep behavior, failure, timing, ownership, and safe-use facts; delete narration and code restatement.
- **Ported behavior is pinned to the reference:** a deviation from the pi or npm original is a defect, not a style choice; golden tests own the pinning.
- TODO markers: `FIXME` / `TODO` / `XXX` by urgency ([semantics](docs/development.md)).
- Files end with exactly one trailing newline.

## Decision records and skills

Decisions live in [Agent Notes](.agents/notes/README.md); reusable agent workflows live in [.agents/skills/](.agents/skills/), and `.claude/skills` symlinks there for Claude Code.

## Editing these instructions

`CLAUDE.md` symlinks this file; edit the real file. Keep each rule self-contained while linking its home; condense when clarity survives. Raise a ceiling in [scripts/doc-budgets.manifest.json](scripts/doc-budgets.manifest.json) only when the content genuinely needs the space, and justify the manifest diff in the PR.
