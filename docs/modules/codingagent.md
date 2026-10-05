# codingagent

English | [中文](codingagent.zh.md)

`codingagent` is the Go port of pi-coding-agent v0.87.1: the coding-agent CLI with sessions, its own tool implementations, compaction, an extension system, and print/JSON-event/RPC/interactive modes. The binary entry is `cmd/openagent`. The port inventory is in [PARITY.md](../../PARITY.md); per-round records in [PORTING.md](../../PORTING.md).

## Configuration

- `config.go` resolves install method, package-asset paths (themes, export templates, interactive assets via PI_PACKAGE_DIR override), app identity from package.json (name/version → env var names), and user config paths under `~/.pi/agent/` (models.json, auth.json, settings.json, sessions).
- `settings_manager.go` merges global (`~/.pi/agent/settings.json`) and project (`<cwd>/.pi/settings.json`) scopes with recursive deep merge, migration of legacy keys, and project-trust gating.
- `package_manager.go` resolves package sources plus auto-discovered skills: project `<cwd>/.pi/skills` and every ancestor `.agents/skills` (ancestor walk stops at the git repo root; both project-trust-gated), then user `~/.pi/agent/skills` and the cross-tool `~/.agents/skills` pool; settings `skills` patterns gate each path (`!glob` excludes, `+path` force-includes, `-path` force-excludes), and output orders by precedence with canonical dedupe.

## Model stack

- `model_runtime.go` composes built-in providers with models.json overlays and extension registrations; the availability snapshot drives model selection.
- `model_resolver.go` resolves model patterns (exact, partial with alias preference, globs) and thinking-level suffixes.
- `provider_composer.go` layers built-in + models.json + extension provider configs.

## Sessions

- `session_manager.go` persists append-only JSONL trees: entry types (messages, model/thinking changes, compaction, branch summaries, context edits, labels), versioned migrations (v1→v2→v3), lazy file creation, branching with summaries, and provenance-preserving projection.

## Tools

`core/tools` ports bash (with the shared shell-tool factory covering PowerShell), edit (+ diff), read (image attachments, truncation notices), write, grep (ripgrep JSON events), find (fd globs), ls — each with pluggable operations for remote delegation and the mutation queue serializing same-file writes.

## Compaction

- `compaction.go` computes token estimates and cut points; the LLM summarization call lands with the runtime phase.

## Modes

- `print_mode.go` serves `--mode text|json`; `rpc_mode.go` dispatches JSONL commands over stdin/stdout; `interactive_mode.go` drives the TUI session (editor input dispatch, live agent-event rendering, real runs, status/footer layout, compaction via `/compact`). `cli_args.go` parses the flag table; `cmd/openagent/main.go` is the binary entry (interactive requires a TTY).

## Compaction

`session_compaction.go` executes the full chain: path preparation (cut-point planning over the session projection), LLM summarization (initial/update prompts, split-turn merge), and the persisted checkpoint (`CompactionEntry` + `compaction_end` event). `ShouldAutoCompact` gates threshold-based auto-compaction.

## Extensions

`extension_runner.go` dispatches events to Go-native extensions (the jiti TS-loading deviation); registrations cover tools, commands, flags, and shortcuts.

## Known limitations

- 17 selector-class interactive components (tree/config/session/settings selectors, armin, mermaid) ride seam messages; their commands report unavailability rather than opening selectors.
- The extensions loader (TS module discovery/validation) and `experimental/` (CA9) are documented deviations per the minimal-core scope decision; see PARITY.md.
- resource-loader (session wiring for resolved resources; extensions/prompts/themes auto-discovery and settings local entries ride with it), sdk.ts, cache-warmer, migrations, rpc-client, startup-ui, theme-controller, and changelog are unported; see PARITY.md's gap list.
- remote-catalog refresh and the radius provider are documented gaps.
