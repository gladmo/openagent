package kinds

// generation_stream.go ports harness/pico3/kinds/generation.ts's request
// derivation decisions: the sendable-message filter for estimation, the
// overflow check, and the request build with tool loadout removal — the
// provider streaming itself rides on the runtime models surface.

import (
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// EstimateGeneration mirrors the generation estimate: pi-ai drops system
// messages and aborted/error assistant messages before sending, so the
// estimate filters them.
func EstimateGeneration(messages []ai.Message) float64 {
	sendable := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Role() {
		case "system":
			continue
		case "assistant":
			assistant := message.(*ai.AssistantMessage)
			if assistant.StopReason == "aborted" || assistant.StopReason == "error" {
				continue
			}
		}
		sendable = append(sendable, message)
	}
	return ai.EstimateContextTokens(sendable).Tokens
}

// CheckOverflow mirrors the overflow check: the estimated request must fit
// the window minus the model's max output tokens.
func CheckOverflow(estimatedTokens, contextWindow, maxTokens float64) bool {
	return estimatedTokens > contextWindow-maxTokens
}

// RequestBuild carries the derived request.
type RequestBuild struct {
	Messages []any
	// RemovedTools lists the tool names peeled out of the context as a
	// system message (empty when none).
	RemovedTools []string
}

// BuildRequest mirrors derive's tail: messages plus the beforeRequest hook
// (injectable), then the tool-loadout removal system message.
func BuildRequest(
	derivedMessages []any,
	effectiveTools func(messages []any) []string,
	beforeRequest func(messages []any) ([]any, error),
	now float64,
) (*RequestBuild, error) {
	messages := derivedMessages
	if beforeRequest != nil {
		rewritten, err := beforeRequest(messages)
		if err != nil {
			return nil, err
		}
		if rewritten != nil {
			messages = rewritten
		}
	}
	tools := effectiveTools(messages)
	if len(tools) == 0 {
		return &RequestBuild{Messages: messages}, nil
	}
	removal := jsonx.ObjFrom(
		"role", "system",
		"content", "",
		"toolsRemoved", toolNameObjects(tools),
		"timestamp", now,
	)
	return &RequestBuild{
		Messages:     append(append([]any{}, messages...), removal),
		RemovedTools: tools,
	}, nil
}

func toolNameObjects(tools []string) []any {
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, jsonx.ObjFrom("name", tool))
	}
	return out
}

// DeferredPollAt mirrors the deferred poll schedule: now + pollAfterMs
// defaulting to 5s.
func DeferredPollAt(now, pollAfterMS float64) float64 {
	if pollAfterMS <= 0 {
		pollAfterMS = 5000
	}
	return now + pollAfterMS
}

// ClassifyTerminalDecision captures classify's terminal routing inputs.
type ClassifyTerminalDecision struct {
	// Fail carries a generation failure when set.
	FailReason string
	FailDetail string
	// RetryAfterError requests the retrying phase.
	RetryAfterError bool
	// Otherwise the message classifies normally.
	OK bool
}

// ClassifyTerminal mirrors classify's terminal-message routing: error
// stopReason routes to retry/fail; otherwise the message appends and the
// turn proceeds.
func ClassifyTerminal(stopReason, errorMessage string) *ClassifyTerminalDecision {
	if stopReason == "error" {
		return &ClassifyTerminalDecision{RetryAfterError: true, FailDetail: errorMessage}
	}
	return &ClassifyTerminalDecision{OK: true}
}
