# Agent Note: Porting the MCP stack ahead of the reference baseline

Status: implemented

English | [中文](2026-10-14-mcp-port-ahead-of-baseline.zh.md)

## Problem

The pi reference moved from `ff72faba2` (this port's baseline) to `a276dabe5`, and the range's landmark feature is MCP: a new `packages/mcp` client library plus the coding-agent integration (`core/mcp-servers.ts`, `extensions/mcp/*`). The whole range is far larger than one change can absorb (78K inserted lines: durable runtime, codemode, TUI rewrites), and most of it builds on extension-host surfaces (CA4/CA6) this port has not reached. Porting nothing until the baseline catches up would leave the Go project without MCP — the feature the range exists for — while porting everything would couple an unfinished port to a moving reference.

## Decision

Port the MCP stack from `a276dabe5` now, as a pinned exception to the `ff72faba2` baseline, in three layers with explicit boundaries:

1. **`mcp/` package, complete.** `packages/mcp` 1:1: JSON-RPC model over jsonx, protocol
types, stdio / Streamable HTTP / in-memory transports, SSE consumer, the client with timeout/abort/progress semantics, and the OAuth closure (discovery, PKCE flow, dynamic registration, provider state, loopback callback server). Tests port the reference suites.
2. **`codingagent` MCP runtime, complete.** Config validation and the server registry
(`mcp_servers.go`), `mcp.json` load/merge/override/edit (`mcp_config.go`), server log (`mcp_log.go`), credential store + auth provider + browser sign-in (`mcp_oauth.go`), tool/result conversion (`mcp_tools.go`, `mcp_resources.go` helpers), the reconnecting connection (`mcp_runtime.go`), and the `openagent mcp add/remove/list/login/logout` CLI (`mcp_cli.go`). Supporting additions: `ai.OauthSuccessHTML/OauthErrorHTML` (pi-ai `utils/oauth-page.ts`), `TruncateMiddle` (`core/tools/truncate.ts`), and `AgentToolResult.StructuredContent/IsError` (agent `types.ts` from the same range).
3. **Extension-host wiring, deferred with a named owner.** `extensions/mcp/index.ts` (session
lifecycle, tool exposure/activation, `/mcp` manager) and `ui.ts` need the extension event host, `ToolDefinition` exposure/namespace fields, `setActiveTools`, codemode/tool_search, and provider-token lookup from the model registry — CA4/CA6 surfaces. The gap is recorded in PORTING.md/PARITY.md as the remaining MCP work, not as a deviation from the ported files' behavior.

Forced deviations (each also in PARITY.md):

- **Cross-process locks.** proper-lockfile's mkdir locks with renewal timers map to O_EXCL
lock files with mtime-staleness takeover (`mcp_file_lock.go`); same-host guarantees, no renewal timer (a refresh's fetches are bounded at 15 s, below the 20 s stale window).
- **Process-exit cleanup.** Go has no `process.once("exit")` hook, so the stdio transport
cannot kill live server process groups at host exit; sessions close connections on shutdown, and a killed host leaves servers to exit on stdin close (or the stale path).
- **Client identity.** `initialize` sends `AppName()`/`Version()` where the reference sends
the literal `"pi"`; servers see the real client.
- **Timeout encoding.** `Number.POSITIVE_INFINITY` has no Go port, so per-request timeouts
use ints where zero inherits the default chain and negative disables.
- **`AdaptOAuthProvider`/provider structs.** The TS optional-method interfaces map to
struct-of-function-fields, with single-flight refreshes sharing one error.

## Alternatives considered

**Wait for the baseline to reach `a276dabe5`.** Rejected: MCP is the requested capability and
is layered to port cleanly today; waiting couples it to TUI/durable work it never touches.

**Port the whole range.** Rejected: codemode (QuickJS), pi-durable, and the TUI rewrite dwarf
the MCP stack and depend on unported hosts; the port would be a partial copy of everything.

**Stub the extension wiring now.** Rejected: a fake session host would freeze wrong semantics;
the boundary at the connection layer is where the reference itself splits (runtime.lazy.ts).

## Consequences

`mcp/` pins `packages/mcp@a276dabe5` while the rest of the tree stays at `ff72faba2`; PARITY.md records per-file mappings and the deferred list, and PORTING.md carries the phase row. The `openagent mcp` CLI is user-usable today (verified end to end against a local stdio server); in-session MCP tools arrive with CA6 extension-host work. Verified with `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./...`, and `node scripts/run-gates.mjs`. Supersession check at creation: no prior note covers MCP or reference-baseline exceptions.
