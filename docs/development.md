# Development guide

English | [中文](development.zh.md)

The setup tutorial takes a new contributor from prerequisites to a verified checkout; the contributor reference covers daily workflow and CI organization. Testing policy lives in [testing.md](testing.md); design rationale lives in [Agent Notes](../.agents/notes/README.md).

## Setup tutorial

### Prerequisites

- Go ≥ 1.27 (matches `go.mod`).
- Node ≥ 18 for the documentation gates under `scripts/`; git for hooks.
- Optional: a checkout of the pi TypeScript reference at commit `ff72faba2` when working on parity ([PORTING.md](../PORTING.md)), and API keys for live-provider tests ([testing.md](testing.md)).

### First-time setup

```sh
go mod download
go build ./... && go vet ./...
```

`go mod download` fetches the single dependency (`gopkg.in/yaml.v3`); nothing is generated afterward. Hooks are optional: run `lefthook install`, or wire `node scripts/run-gates.mjs` into `.git/hooks/pre-commit` by hand.

Setup is complete when `go build ./... && go vet ./...` exits successfully.

## Contributor reference

### Daily workflow

Branch from `main`, change, then gather focused evidence per [pre-push-checks](../.agents/skills/pre-push-checks/SKILL.md): `gofmt -l .` prints nothing, `go vet ./...` passes, and the owning package test — `go test ./agent/` or `-run TestName` — covers the behavior you touched. Port work updates [PARITY.md](../PARITY.md) for the files it covers in the same change. Commit subjects state the what and the why.

### CI organization

No CI lanes are configured yet. The intended set CI will own: `gofmt -l .`, `go vet ./...`, `go test ./...`, and `node scripts/run-gates.mjs`.

### TODO marker semantics

`FIXME` marks a defect to fix before the next release; `TODO` marks accepted work not yet scheduled; `XXX` marks a landmine that needs a design decision. Each marker carries enough context for its future owner.
