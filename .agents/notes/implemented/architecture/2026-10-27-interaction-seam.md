# Agent Note: split the ask-user-question capability into a three-role interaction seam

Status: implemented

[English](2026-10-27-interaction-seam.md) | 中文

## Problem

The harness had no way for a running agent to obtain a human decision: the loop is pure prompt-in/events-out, and approval-shaped interception was left to embedders as an unnamed combination of the `before_tool` hook and a gate — no question vocabulary, no answer validation, no fail-closed semantics, nothing the model could call. Every embedder needing "ask the human" would invent its own mechanism, and the copies are where the contracts rot (the same failure mode that produced `toolset`).

## Decision

Add `agent/harness/interaction` (module page: [docs/modules/interaction.md](../../../../docs/modules/interaction.md)), openagent-native (same provenance class as `toolset`), splitting three roles so each side of the seam is replaceable:

- **Definition** — vocabulary (`AskRequest`, `Question`, `Option`, `Answer`) plus the error taxonomy (`InteractionError` codes cross the seam) and the `Service` answerer chain. Imports only `agent/harness`; imports no provider or consumer.
- **Provider** — `Answerer` implementations register via `Service.Register`, which returns an idempotent disposer (registrations are effects). Ask walks a per-call snapshot in registration order; an answerer declines with the `ErrDeclined` sentinel. Blocking waits belong to answerers (they select on the context's abort signal), so the service holds no pending-question registry.
- **Consumer** — `CreateAskUserQuestionTool` is the model-facing `ask_user_question` harness tool; embedders also consume `Service.Ask` directly. The tool is one consumer among possible ones and depends on nothing but the definition.

Failure semantics: empty chain or full declines fails closed with `NO_ANSWERER`; an aborted context fails with `ASK_ABORTED`, checked before the chain and after every answerer return so an answer landing after abort does not win; every returned answer is validated against its request (`BAD_ANSWER`).

## Alternatives considered

**Inline the capability into `agent/harness/tools`.** Rejected: `tools` mirrors the reference's `harness/tools/*` 1:1 and is env-bound; a native capability with no TS counterpart belongs in its own provenance class, and the ask tool needs no `ExecutionEnv`.

**Express asking as a `before_tool` hook plus a callback.** Rejected: hooks are transform points — they can veto or rewrite, but they have no answer vocabulary and no way to return a value to the model inside the tool call.

**A single injectable `Ask` function, no chain.** Rejected: one closure removes provider composition (UI answerer plus automation fallback), and fail-closed needs an explicit "declined" concept to distinguish "no answerer wants this" from "the answerer failed".

**A service-owned pending-question registry with timed waits.** Deferred: pending state and deadlines are answerer concerns until a second consumer demands them; the definition stays minimal (the capability-seam rule: do not split or add machinery before a second role appears).

## Consequences

- The error codes are stable API: the tool folds them into its error text, embedders switch on them.
- The tool ignores the execution tool context, so it rides `toolset.Adapt`/`NewLocal` unchanged — the first in-tree harness tool that is not env-bound.
- Timed waits, asynchronous continued questions, RPC exposure, and a separate approval seam are recorded as known limitations on the module page; each lands on this seam when its consumer appears.
- The supersession check found no prior note covering human interaction; this note opens the topic.
