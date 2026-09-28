# Glossary

English | [中文](glossary.zh.md)

The closed vocabulary this repository uses in documentation, code review, and decision records. Add a term when prose starts leaning on it; keep definitions to one line and link the owning document.

| Term | Definition |
|---|---|
| pi | The TypeScript monorepo this repository rewrites 1:1; reference commit `ff72faba2` ([PORTING.md](../PORTING.md)) |
| Parity | Agreement with pi or npm reference behavior, audited in [PARITY.md](../PARITY.md) and pinned by golden tests |
| Turn | One agent-loop pass: model events, tool calls, tool results ([architecture](architecture.md)) |
| Standing order | A one-to-three-line rule in an `AGENTS.md` that links the document owning the full contract |
| Tier | One row of the documentation taxonomy; each fact has exactly one owning tier ([standard](AGENTS.md)) |
| Agent Note | An RFC-style decision record with lifecycle, classification, and mandatory alternatives ([rules](../.agents/notes/README.md)) |
| Gate | A deterministic zero-dependency check under `scripts/`, aggregated by `run-gates` |
| Skill | A reusable agent workflow under `.agents/skills/` with YAML frontmatter |
| Postmortem | A numbered incident record; the only tier where narrative belongs ([rules](postmortem/README.md)) |
| Supersession check | The search for older Agent Notes a new note replaces, required on every new note ([rule](../.agents/notes/AGENTS.md)) |
