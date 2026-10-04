# Agent Note: Porting pi-coding-agent and pi-tui to Go

Status: proposed

English | [中文](2026-09-30-coding-agent-port.zh.md)

## Problem

The repo has completed the 1:1 port of the pi agent stack (`agent` + `ai`/`chord`/`telemetry` closures; PARITY.md P0–P9). The remaining product surface is `pi/packages/coding-agent` (~75.3K LOC source, 203 test files): the `pi` CLI with sessions, its own tool implementations, compaction, an extension system, print/json/rpc/interactive modes, and an `experimental/` tree. Porting it is not a mechanical extension of the existing packages: coding-agent's `core/` imports the pi-tui package pervasively (tool renderers, capability detection, keybindings, theme, extension API types), so pi-tui (~18.8K LOC + 48 test files) is a prerequisite library, not an interactive-mode detail. Go also forbids two things the reference relies on: TypeScript import cycles across directories (`core/agent-session.ts` ↔ `modes/interactive/theme/` via `SourceInfo`) and runtime evaluation of TypeScript extensions via jiti.

## Proposal

Port both packages 1:1 in phases CA0–CA10 tracked in PORTING.md, under the existing TS→Go mapping conventions, with these bindings:

- pi-tui is ported first, completely, as a standalone base package `tui/` — same precedent as typebox/diff/ignore (user-confirmed).
- coding-agent becomes the package tree `codingagent/` mirroring `src/` directories, with root files in the package root; the binary is `cmd/openagent` (user-confirmed name) and `rpc-entry.ts` becomes `cmd/openagent-rpc`.
- Import cycles break via a shared leaf package: types referenced from both directions (first case `core/source-info.ts`) sink into `codingagent/shared/`, each sink recorded in PARITY.md as a placement deviation.
- Forced deviations, each documented in PARITY.md: jiti runtime TS-extension loading becomes a Go-native `ExtensionAPI` with manifest discovery kept and TS-only extensions failing loud; NAPI clipboard becomes OSC52 + system clipboard commands; photon WASM image processing becomes a Go `image` pipeline preserving observable behavior; highlight.js/marked/grok-mermaid become minimal hand-written subsets or placeholders pinned by tests; `bun/` runtime files have no Go equivalent (cmd entries replace them); Windows-only paths are GOOS-gated.
- `experimental/` is in scope as the final phase (user-confirmed); it builds on the already-ported harness runtime and pico3.
- The near-zero dependency policy holds (yaml.v3 only): semver/minimatch/proper-lockfile become small hand-ported packages golden-tested against the npm originals; introducing `golang.org/x/sys` for Windows consoles requires its own note.

## Alternatives considered

**Port coding-agent headless-first on a partial tui subset, complete tui later.** Rejected (user decision): the subset already covers most of tui (colors, capabilities, component model), so the split saves little and leaves the component model pinned twice.

**Embed a JS runtime (goja) to keep jiti extension loading.** Rejected: pi extensions import npm packages and Node APIs an embedded engine cannot resolve; the failure modes are worse than an explicit Go-native API plus loud diagnostics.

**Skip experimental/ entirely.** Rejected (user decision): PARITY.md would carry a permanent ⬜ block; the harness runtime and pico3 it needs are already ported.

**Reuse `agent/harness/tools` for coding-agent's tools.** Rejected: coding-agent's tools are separate implementations (own shell utils, output accumulator, renderers, extension wrapping) with different behavior; porting them fresh preserves 1:1 fidelity.

## Acceptance criteria

- `tui/` and `codingagent/` mirror the reference file-by-file; PARITY.md carries full audit tables (✅/🟡/⬜ plus documented deviations).
- `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .` green; `node scripts/run-gates.mjs` passes.
- `cmd/openagent` runs print, JSON-event, RPC, and interactive modes with behavior aligned to the reference (faux-provider end-to-end smoke).
- Session JSONL, RPC protocol messages, and theme JSON validation are byte- or snapshot-pinned by tests.

## Risks

The scope is ~94K LOC of TypeScript plus a core test subset; the phased table in PORTING.md is the checkpoint for progress across many rounds. Terminal rendering fidelity (differential renderer, editor, markdown) is the hardest surface to pin byte-exactly — golden tests generated from the JS reference mitigate. Windows console support may force a second external dependency, which would need its own note and user sign-off.
