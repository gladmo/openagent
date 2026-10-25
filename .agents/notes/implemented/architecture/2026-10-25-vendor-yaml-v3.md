# Agent Note: Vendoring gopkg.in/yaml.v3 to reach zero external dependencies

Status: implemented

English | [中文](2026-10-25-vendor-yaml-v3.zh.md)

## Problem

go.mod carries one external dependency, `gopkg.in/yaml.v3 v3.0.1`, and go.sum additionally carries its test-only dependency `gopkg.in/check.v1`. Upstream has been frozen since 2022-04: stable, but no maintenance is coming. The consumed surface is three `yaml.Unmarshal` call sites parsing `---` frontmatter (codingagent `ParseFrontmatter`, harness skills, harness prompt templates), yet the input is arbitrary user-authored YAML and its semantics — scalar resolution, flow sequences, the accept/reject boundary — are pinned to yaml.v3 by parity tests (`argument-hint: [file]` resolves to a sequence, so a string assertion fails). The problem: remove the external module dependency without drifting any pinned behavior.

## Decision

Copy all 13 non-test source files plus LICENSE and NOTICE from the v3.0.1 module verbatim into [yamlv3/](../../../../yamlv3/README.md); the package name stays `yaml`, so consumers change only the import path (codingagent/utils_core.go, agent/harness/skills.go, agent/harness/prompt_templates.go) and no call-site code moves. The single mechanical deviation is `gofmt` (Go ≥ 1.19 doc-comment canonicalization) applied to 9 files — comment whitespace only, no code — and yamlv3/README.md owns the deviation list plus the audit recipe (`diff -r` against the pristine module cache). Upstream's test suite (the sole reason `gopkg.in/check.v1` existed here) does not come along; yamlv3/yamlv3_test.go pins the consumed subset instead — map/struct decode, scalar resolution, flow-sequence-is-not-a-string, empty/error documents — behind the existing frontmatter, skills, and template tests. go.mod ends with zero `require` lines and go.sum is deleted.

## Alternatives considered

**Hand-rolled YAML-subset parser.** The three call sites only need block mappings of scalars, but the input is arbitrary user YAML: matching yaml.v3's accept/reject boundary and error semantics on every input is a rewrite of a correct YAML subset plus permanent self-maintenance, and any miss is a parity defect by the port's own rules. Rejected.

**Decode-only vendoring (drop encode.go/emitterc.go/writerc.go/sorter.go).** About 2.7k fewer lines, but it edits upstream source and forfeits line-for-line `diff -r` auditability; the dead encode path is harmless in a frozen copy. Rejected.

**`go mod vendor`.** Puts source in-tree but keeps the go.mod requirement and module-graph metadata; the property this repo wants — `GOPROXY=off` builds with an empty module graph — is not achieved. Rejected.

**Keep the dependency.** A dormant upstream means "stable" and nothing more; a security response would still require a local fork to patch, and the port's near-zero-dependency convention (hand-rolled NFD instead of `golang.org/x/text`) already set the direction. Rejected.

## Consequences

go.mod has no requirements; `go list -m all` reports only the main module and `GOPROXY=off` builds and tests pass, so compilation no longer touches the network or the module cache. `gopkg.in/check.v1` leaves the dependency graph together with the upstream tests that pulled it. Security ownership moves in-tree: any future CVE must be assessed and backported into yamlv3/ locally and recorded in that README's Updating section — accepted because the parsed inputs are local user files (SKILL.md, prompt templates), not network payloads. Behavior is unchanged by construction: verbatim source, and the consumer tests pass unmodified, including the `[file]` pin. Verified with `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./...`, and `node scripts/run-gates.mjs`. Supersession check at creation: no active note owns the dependency strategy; the coding-agent-port proposal's "yaml.v3 only" bullet is background context, not a decision owner.
