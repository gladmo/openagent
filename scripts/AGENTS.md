# AGENTS.md — Repository scripts

Gate scripts are zero-dependency Node (`node:` builtins only), resolve the repository root from their own location, and run to completion without network access. Each gate states the exact rule it enforces, prints the corpus size it checked so an empty corpus is visible rather than silent, and owns every temporary path it creates. The aggregator [run-gates.mjs](run-gates.mjs) runs them sequentially and stays the single entrypoint wired into hooks and CI.
