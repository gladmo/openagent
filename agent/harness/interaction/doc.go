// Package interaction is the ask-user-question capability seam: a
// running agent can pause a tool call until a human answers a question.
//
// The package is openagent-native (not a 1:1 port; same provenance
// class as agent/harness/toolset) and splits three roles so each side
// of the seam is replaceable:
//
//   - Definition: this package's vocabulary (AskRequest, Answer,
//     InteractionError) plus the Service chain in service.go.
//   - Provider: Answerer implementations register with Service
//     (registration returns its disposer); Ask walks them in order and
//     fails closed with NO_ANSWERER when none accepts.
//   - Consumer: CreateAskUserQuestionTool is the model-facing tool;
//     embedders also consume Service.Ask directly. The definition
//     imports no consumer.
//
// Semantics: Ask validates the request and every returned answer; an
// aborted context fails with ASK_ABORTED before the chain and after
// every answerer return. Blocking waits belong to answerers — they
// select on the context's abort signal — so the service holds no
// pending-question registry. Timed waits, asynchronous continued
// questions, and a separate approval seam are out of scope.
package interaction
