---
name: code-review
description: Use when reviewing a pull request in this repository — orients the reviewer to the project's standards (AGENTS.md conventions, decision records, documentation tiers, verification gates) and the review checks that code alone cannot show.
---

# Reviewing a Pull Request

**This skill is guidance, not a complete checklist.** Verify the PR's live base and exact head, inspect the complete diff and enough surrounding code to understand the design. Prioritize correctness, security, lifecycle, and broken required behavior over style; a short review with one substantiated blocker beats a list of nits.

## Sources of truth

- [AGENTS.md](../../../AGENTS.md): standing repository rules.
- [docs/AGENTS.md](../../../docs/AGENTS.md): documentation placement and prose discipline.
- [docs/testing.md](../../../docs/testing.md): required evidence tiers.
- [Agent Notes](../../notes/README.md): design rationale. Disagreement with a note is a design discussion, not an automatic veto.

## Blocking requirements

1. **Docs match the code.** Config, defaults, errors, and public behavior update the owning README and reference page in the same diff.
2. **Decision records are in order.** A durable decision in the diff carries its Agent Note; a note implementing a proposal is rewritten to present-tense shipped state; every new note passed the supersession check.
3. **Bilingual pairs update together:** `.md` and `.zh.md` counterparts change in the same diff with mirrored structure.
4. **Evidence exists.** The author ran the checks the diff requires, selected per [pre-push-checks](../pre-push-checks/SKILL.md); review the semantic gaps automated checks cannot detect.

## Manual checks

- **Interface contracts:** trace both sides of every changed interface, including errors, cancellation, ownership, and cleanup.
- **Lifecycle and concurrency:** for async setup, callbacks, processes, or teardown, check races before publication, cancellation during awaits, and complete cleanup on every path.
- **Scope and necessity:** map each new abstraction, option, or compatibility path to a current contract and production consumer; challenge speculative generality.
- **Configuration and defaults:** ask what current-consumer evidence supports each default and public choice; require an explicit value or deferral when absent.
- **Test strength:** assertions fail on the intended regression and verify observable state rather than restating the implementation.
- **Failure paths:** exercise denial and error paths through the operation that enforces them, not through callers that can be bypassed.

## Reporting findings

State the defect, location, impact, and evidence. Place a specific defect inline on the tightest relevant diff range; use a PR-level comment for cross-cutting concerns. Separate blockers from suggestions and omit issues a green gate already enforces. When receiving review, verify each claim and fix or rebut it on technical grounds without performative agreement.
