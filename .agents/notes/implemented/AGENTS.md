# AGENTS.md — Implemented Agent Notes

These notes describe shipped decisions. Follow the [root instructions](../../../AGENTS.md), the [documentation standard](../../../docs/AGENTS.md), and the [Agent Note format](../README.md#the-file-format); `verify-agent-note-format` gates the lifecycle-specific structure.

## Keep an implemented note current with what actually shipped

Keep paths, symbols, defaults, and mechanisms current in the same change that alters them. Rewrite stale facts in place; do not append change history.

When a shipped note is unlikely to guide future work, archive its complete pair per the [archiving policy](../README.md#archiving-and-deletion) instead of continuing to maintain it.

### This is not a license to rewrite the *decision*

Update factual realization in place. A reversal of the decision or its rationale requires a new Agent Note and cross-link; a fully superseded old note may be deleted only through the consolidation rule in the [Agent Note README](../README.md).
