# Module reference

English | [中文](README.zh.md)

One reference page per module in openagent: its types, configuration, and semantics. Behavior narration belongs to the [architecture map](../architecture.md); decision rationale belongs to [Agent Notes](../../.agents/notes/README.md).

## Conventions

- One page per module, named after the module; the page is the module's lookup scope, not a tutorial.
- Document current contracts: configuration fields with their defaults and validation, public types, invariants, and failure behavior.
- State limitations and deferred work under an explicit `## Known limitations` section; ordinary cleanup stays in a TODO marker.
- When a type or table has an authoritative source (a generator, a schema, an export), link it instead of restating it.
- A page appears in the architecture module map the same commit it is created.
