// Package mcp is the Go port of pi's MCP client stack
// (packages/mcp at reference commit a276dabe): a dependency-free Model
// Context Protocol client with stdio and Streamable HTTP transports,
// in-memory transports for tests, and OAuth 2.1 authorization-code flows
// with PKCE, dynamic client registration, and Client ID Metadata
// Documents.
//
// The JSON model is jsonx (insertion-ordered objects, JS number
// formatting) so protocol values round-trip like the reference's
// structuredClone/JSON.stringify pipeline. AbortSignal semantics use the
// abort package shim. Deviations from the TypeScript original are
// recorded in PARITY.md.
package mcp
