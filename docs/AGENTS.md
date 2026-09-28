# AGENTS.md — The documentation standard

This file defines document structure, the tier taxonomy, writing rules, and word budgets. Placement questions resolve here; rationale for a decision lives in the linked [Agent Note](../.agents/notes/README.md), never inline.

## Document structure

A document's subject and tree position fix its scope: describe its own subject at appropriate detail and direct children only by purpose, responsibility, and high-level behavior; link the owning descendant for lower-level detail. Document type does not widen that scope, and a reference may be exhaustive only about its own subject.

Classify every document as a tutorial or a reference. Tutorials follow an ordered path to an outcome and introduce only what each step needs. References define a lookup scope and current behavior without a teaching sequence. Before writing a tutorial, order concepts by prerequisite and difficulty, and move unnecessary advanced material to a later tutorial or a reference.

Author in this order: locate the document in the tree; set its permitted detail; choose tutorial or reference; relocate descendant-owned detail; replace lower-level explanations with links to their owners.

## The tier taxonomy: one home per fact

Each fact has one home — the tier whose job it is; elsewhere, link there.

| Tier | Job | Does NOT belong there |
|---|---|---|
| Root `AGENTS.md` | Standing orders an agent needs in every session, one to three lines each, linking their home | Stories, worked examples, situational procedures, anything restated from a linked home |
| Subtree `AGENTS.md` (`docs/`, `scripts/`, `.agents/notes/`) | Orders specific to that subtree | Repo-wide rules the root file already carries |
| [architecture.md](architecture.md) | Ordered map: composition, modules, core flows, extension points; read before changing primary source | Per-module detail (→ module pages and READMEs), decision rationale (→ Agent Notes) |
| [modules/](modules/README.md) | One reference page per module: types, configuration, semantics | Behavior narration (→ architecture.md) |
| [Agent Notes](../.agents/notes/README.md) | Active decision records: the why, what was given up, alternatives, required verification | Migration plans and spec-speak once the decision has shipped |
| [postmortem/](postmortem/README.md) | Incident stories — the only tier where narrative belongs | — |
| [cookbook/](cookbook/README.md) | Step-by-step how-tos with numbered verify steps | Design rationale (→ the Agent Note each guide links) |
| [user/](user/index.md) | Product-facing guides | Contributor procedures, decision history |
| Module or package README | The per-module contract: config, semantics, limitations, extension points | Restatement of reference pages or other modules' concerns |
| [development.md](development.md) | Contributor setup, daily workflow, and a summary of CI | Runtime rationale (→ Agent Notes), check-by-check lists that drift from the actual scripts |
| Skills (`.agents/skills/`) | Reusable workflows and specialized decision standards | Product and runtime contracts (→ docs or source) |

Placement: incidents → postmortems; rationale → Agent Notes; procedures → cookbook; module contracts → module pages and READMEs; standing orders → root `AGENTS.md` with a rationale link.

## Writing rules

- **Document current state.** Keep history in commits, PRs, and Agent Notes; other prose names live mechanisms, not changes.
- **One physical line per paragraph** (`verify-md-wrap`): use editor soft-wrap. Code blocks, tables, and list structure keep their formatting.
- **Pairs update together:** a `.md` and its `.zh.md` counterpart change in the same commit with mirrored structure; machine-checked tokens stay in English verbatim.
- **Comments and docs state contracts, not reasoning transcripts.** Delete narration, test walkthroughs, review history, and code restatement; link the rationale instead.
- Write directly: name actors and facts. Name the exact check, type, API, or behavior instead of metaphorical labels.
- When a generator is authoritative for a catalog or graph, do not hand-restate it; link it.

## Word budgets

[scripts/doc-budgets.manifest.json](../scripts/doc-budgets.manifest.json) sets standing-doc ceilings; `verify-doc-budgets` rejects excess or missing files. When a budget goes red: **relocate** content that belongs in another tier, then **condense** what belongs here, and **raise** the ceiling only when the content genuinely needs the space, justifying the manifest diff in the PR. Ceilings are guardrails, not reduction targets; keep at least 5% headroom below target.

## The slop checklist

Hunt these in any document:

- Duplicated rules: search a distinctive phrase; keep one home and link the rest.
- History outside its permitted tier.
- Implementation-status annotations in prose ("implemented!", "future: …"); status rots.
- Hand-restated catalogs or inventories when source or a generator is authoritative.
- Reasoning transcripts: step-by-step narration, proof of obvious branches, or rejected local alternatives.
- Rationale repeated beside siblings instead of once at the owning capability.
- Paragraph walls carrying several rules at once; split them or demote the detail to its home.
- Emphasis inflation: bold or "critically" everywhere means nothing stands out.
- Spec-speak in `implemented/` Agent Notes: "should", plans, acceptance checklists ([note rules](../.agents/notes/README.md)).

## Repository references

Use relative Markdown links for current files and tags or PR numbers for historical references; `verify-md-links` checks local targets.
