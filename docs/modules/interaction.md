# Interaction

English | [中文](interaction.zh.md)

The `agent/harness/interaction/` package is the ask-user-question capability seam: a running agent can pause a tool call until a human answers. It is openagent-native (not a 1:1 port; same provenance class as `agent/harness/toolset`); the seam decision is recorded in an [Agent Note](../../.agents/notes/implemented/architecture/2026-10-27-interaction-seam.md).

## Roles

- **Definition**: the vocabulary (`AskRequest`, `Question`, `Option`, `Answer`, `InteractionError` codes) and the `Service` chain. Depends only on `agent/harness`; imports no provider or consumer.
- **Provider**: `Answerer` implementations. `Service.Register` appends one and returns an idempotent disposer (registrations are effects). An answerer blocks until answered, declined (`ErrDeclined`), or aborted by selecting on the context's abort signal; pending-question state belongs to the answerer, not the service.
- **Consumer**: `CreateAskUserQuestionTool(service)` exposes the model-facing `ask_user_question` harness tool; embedders also call `Service.Ask` directly. The tool ignores the execution tool context, so it rides `toolset.Adapt`/`NewLocal` unchanged.

## Semantics

`Service.Ask` validates the request (`EMPTY_QUESTIONS`, `BAD_QUESTION`, `DUPLICATE_QUESTION_ID`), walks a snapshot of the chain in registration order, and validates every returned answer (`BAD_ANSWER`: each question named exactly once, labels among the options without repeats, several labels only with multiSelect, exactly one label for free-text). An empty chain or a full round of declines fails closed with `NO_ANSWERER`. An aborted context fails with `ASK_ABORTED` — checked before the chain and after every answerer return, so an answer landing after abort does not win. Registrations during a blocked ask apply to the next ask.

The tool schema mirrors the vocabulary (`questions[]` with `id`, `header`, `question`, optional `multiSelect` and `options[]` of `{label, description?}`); input beyond the shared validation — `multiSelect` without options, empty or duplicate option labels — is rejected at parse. The tool renders the answer as one `id: labels` line per question in `Content` and the machine-readable form in `StructuredContent`.

## Known limitations

- No timed wait and no RPC exposure; answerers wrap their own deadlines.
- The tool answers synchronously within the tool call; asynchronous continued questions and steering the answer back as a user message are out of scope.
- No separate approval seam; approval-shaped interception stays with the `before_tool` hook plus the gate.
