# Architecture

English | [中文](architecture.zh.md)

How openagent composes and where behavior extends. Rationale lives in [Agent Notes](../.agents/notes/README.md); per-module detail in [module pages](modules/README.md); the port inventory in [PORTING.md](../PORTING.md) and [PARITY.md](../PARITY.md).

## Overview

openagent is a Go 1:1 rewrite of the pi monorepo's TypeScript agent stack (`agent` v0.87.1 with its `ai`, `chord`, and `telemetry` closures; commit `ff72faba2`). Packages mirror the source in name and behavior; npm dependencies become standalone Go packages; layering matches the reference: base libraries, chord, the ai provider stack, the agent loop.

## Module map

```
abort/        AbortSignal / AbortController shim, nil-safe
jsonx/        JS-semantics JSON model
typebox/      TypeBox 1.3.27 subset
partialjson/  partial-json 0.1.7 port
diff/         jsdiff 8.0.4 port
ignore/       npm ignore 7.0.8 semantics
telemetry/    Span contract, NOOP / in-memory contexts
chord/        JSON contracts, context/, delta/, services/
ai/           Types, streaming, retry, catalog, providers
agent/        Agent loop, proxy, search, harness/ and tools
examples/     Runnable example agents
```

## Core flows

- **Provider stream:** a turn calls an `ai` provider over the HTTP/SSE seam (`http_sse.go`); codec, retry, and transcript shape the abort-aware events into messages.
- **Agent loop:** turn → model events → tool calls → tool results → next turn, until a final message or abort.
- **State delta:** `chord/delta` ops apply over the `jsonx` model, keeping ordering and number formatting JavaScript-identical.

## Extension points

- **Providers:** implement the provider core in `ai/`, register with its catalog entry.
- **Tools:** define tools in `agent/harness/tools`; the loop's dispatch calls them.
- **Telemetry:** span schemas via `telemetry/` contexts; NOOP and in-memory ship.

## Invariants

- JSON crossing package boundaries goes through `jsonx` / `chord`, preserving JS semantics: insertion order, number formatting.
- Cancellation propagates from controller to provider stream; aborted paths leak no work.
- Ported behavior is pinned by golden tests against the npm and pi references; deviation is a defect.
