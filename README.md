# openagent

English | [中文](README.zh.md)

A Go 1:1 rewrite of the pi agent stack — the `agent`, `ai`, `chord`, and `telemetry` packages plus their dependency closures (TypeBox, jsdiff, ignore, partial-json rewritten as pure Go) — preserving module names, behavior, and JavaScript JSON semantics. [PORTING.md](PORTING.md) tracks the port phases; [PARITY.md](PARITY.md) audits per-file parity against the TypeScript reference.

## Start here

- [Architecture](docs/architecture.md): the ordered map of modules and flows.
- [AGENTS.md](AGENTS.md): standing orders that apply to every change.
- [Development guide](docs/development.md): setup and daily workflow.
- [Agent Notes](.agents/notes/README.md): decision records; reusable workflows live under [.agents/skills/](.agents/skills/).

## Verification

```sh
go build ./... && go vet ./... && go test ./...
node scripts/run-gates.mjs
```

Go owns compile, static analysis, and behavior tests; the zero-dependency Node gates own commit hygiene, bilingual documentation pairing, and decision-record format on Node ≥ 18.
