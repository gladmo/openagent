# Agent Notes: decision records

English | [中文](README.zh.md)

One kind of design doc lives here. An **Agent Note** records a decision or proposal that affects this codebase — the *why* and *what we gave up*, the parts code and docs cannot carry. This file defines where notes live, when to write one, and the in-file format.

## Layout and naming

Every note has two axes, both encoded in its **path** — `{lifecycle}/{class}/yyyy-mm-dd-topic-title.md`:

- **Lifecycle** (top-level folder) is the note's status, and a note moves between folders as that status changes: **`proposed/`** (reviewed before implementation), **`implemented/`** (the decision shipped, kept current with what shipped), **`rejected/`** (considered and declined), **`archived/`** (frozen history; implemented notes only).
- **Class** (nested folder) is the *kind* of decision — see [Classification](#classification).

The date in the filename is when the topic was **first proposed**. Cross-references between notes use relative Markdown links, never bare prose or numbers, so they are mechanically checkable and survive moves between folders. Do not add a centralized `INDEX.md`; the lifecycle tree is the working inventory.

## Classification

Each note belongs to one path-encoded class from the closed set below; `verify-agent-note-classification` rejects other folders.

| Class | What it covers |
|---|---|
| `feature` | A new user- or model-facing capability. |
| `bug-fix` | Corrects a defect or closes a gap a postmortem surfaced. |
| `simplification` | Removes code, behavior, or surface area without adding a capability. |
| `architecture` | A structural decision about the shipped source — how modules relate, what the vocabulary is. |
| `process` | Tooling, policy, or workflow around the code, not runtime behavior. |
| `testing` | Test infrastructure and strategy. |

The `architecture` / `process` line: architecture is about the source we ship; process is the surrounding tooling and workflow. (`refactor` is deliberately absent — it overlaps `simplification`, whose discriminator, "does observable behavior change?", already covers it.)

## Archiving and deletion

Archive an implemented note when the shipped decision is complete and its rationale is unlikely to guide future work. Never archive a proposed note: reject an obsolete proposal. Keep a rejected note only while its rationale prevents a plausible mistake; otherwise delete the complete pair. The archive is path-encoded as `archived/{class}/yyyy-mm-dd-topic-title.md`.

An archival change moves the complete pair, retains `Status: implemented`, inserts the same `Archived: YYYY-MM-DD` line immediately below the status in both language files, and repairs or deletes inbound links. Once sealed, an archived pair is permanently frozen: do not edit, translate, reformat, update, or treat it as authority for current behavior ([archive policy](archived/AGENTS.md)).

## When to write one

Add or update a note in the same change only for lasting decision rationale that code, tests, and existing documentation do not explain. A proposal for substantial future work starts in `proposed/`; a decision already made starts in `implemented/`. Updating the note that already owns the decision satisfies the rule; do not create a duplicate. Mechanical or local edits are exempt.

A note is never edited into a *different decision*: supersede it with a new one and keep both cross-linked, unless the old note is fully consolidated into the new owner — preserving every unique rationale, alternative, consequence, and required verification, and repairing every inbound link, in the same change that deletes it.

## The file format

Every active note follows one format, enforced by `verify-agent-note-format`. Archived notes retain the format they had when sealed plus the archive-date line.

### The header block

The first three lines of every note are exactly:

```markdown
# Agent Note: <title>

Status: <status>
```

followed by a blank line. The `Status:` value is one of three forms and must agree with the lifecycle folder:

- `Status: proposed`
- `Status: implemented`
- `Status: rejected — <why, in one line>`

The status carries no dates: the filename holds the first-proposed date and git holds everything else. The rejection reason is the one status with content, because a rejected note's verdict is the fact readers come for.

### The body skeleton

Every note opens its body with `## Problem` — the motivation, written to stand without the solution. Recurring sections use these canonical names; genuinely bespoke technical sections remain free-form between the required ones.

`proposed/`:

```markdown
## Problem
## Proposal
…bespoke sections…
## Alternatives considered
## Acceptance criteria
## Risks
```

`implemented/`:

```markdown
## Problem
## Decision
…bespoke sections…
## Alternatives considered
## Consequences
```

`## Decision` describes shipped reality in the present tense, and the whole file stays current with it per [implemented/AGENTS.md](implemented/AGENTS.md). Proposal-era headings are spec-speak in `implemented/` and the gate rejects them. A rejected note is the proposal frozen: it keeps whatever proposal-time sections it had, and the verdict lives on the `Status:` line.

### Alternatives considered — mandatory

Every note carries an `## Alternatives considered` section: each genuine alternative and why it lost, one bold-led paragraph per alternative or a `### Why not <X>?` subsection per contested one. A decision recorded without what it beat invites re-litigation — the failure Agent Notes exist to prevent.

### Moving between lifecycles

Moving a file between lifecycle folders means updating the `Status:` line and re-satisfying that folder's skeleton in the same change. Concretely, `proposed/` → `implemented/` rewrites `## Proposal` into a present-tense `## Decision` and folds `## Acceptance criteria` and `## Risks` into `## Consequences`; `proposed/` → `rejected/` only adds the reason to the `Status:` line and freezes the file.

### Chinese counterparts

A `.zh.md` counterpart mirrors its English sibling section-for-section; the machine-checked tokens (`# Agent Note: `, the `Status:` line, and the section headings `verify-agent-note-format` requires) stay in English verbatim. `verify-agent-note-format` checks both languages; `verify-doc-pairs` checks the pairing.
