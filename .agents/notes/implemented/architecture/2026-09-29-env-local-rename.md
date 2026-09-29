# Agent Note: Renaming harness ExecutionEnv package from env/nodejs to env/local

Status: implemented

English | [中文](2026-09-29-env-local-rename.zh.md)

## Problem

The 1:1 port kept the reference's `harness/env/nodejs.ts` filename as the Go package `agent/harness/env/nodejs`. In the TypeScript reference that name distinguishes implementations by host runtime (Node.js versus a potential Deno/browser port of the same `ExecutionEnv` interface). The Go port has exactly one host implementation, built entirely on `os`, `os/exec`, and `syscall` — no Node.js runtime is involved — so the name advertised a dependency that does not exist and misled readers of a pure-Go codebase.

## Decision

The package lives at `agent/harness/env/local` with package name `local`, type `LocalExecutionEnv`, and constructor `local.New(cwd, ...)` (`WithShellPath`/`WithShellEnv` unchanged). Behavior is pinned to the reference exactly as before: the package comment and the type comment keep the `nodejs.ts` / `NodeExecutionEnv` provenance, PARITY.md records the mapping, and the owning tests were moved (renamed), not rewritten. This is a rename only — zero behavior change. Future alternative environments (in-memory, sandbox) name in parallel as `env/<name>`.

## Alternatives considered

**Keep the name `nodejs`, clarify only in comments.** Rejected: every new reader pays the confusion cost once; a comment corrects intent but grep, import lists, and godoc still show a misleading public identifier forever.

**Name it `env/native`.** Rejected: "native" is ambiguous (native to what — the CPU? the OS? cgo?); "local" states the actual distinction — a real local-filesystem environment, as opposed to in-memory or sandboxed ones.

**Name it `env/osexec`.** Rejected: it freezes an implementation detail (the `os/exec` backing) into the public API name; the package's contract is the `ExecutionEnv` interface, not the mechanism.

**Keep a deprecation alias package at the old import path.** Rejected: the project is pre-stable (AGENTS.md: public APIs follow the reference and may change), the only importers were the coding_agent example and the package's own tests, and an alias violates one-home-per-fact.

## Consequences

The import path `agent/harness/env/nodejs` is gone; importers (examples/coding_agent) updated in the same commit, together with the owning tests, PARITY.md, and PORTING.md mentions, per the repo's API-change rule. Directory and file renames went through `git mv` to preserve history. Verified with `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./agent/harness/env/... ./examples/coding_agent/...`, and `node scripts/run-gates.mjs`. Supersession check at creation: no prior note covers env package naming.
