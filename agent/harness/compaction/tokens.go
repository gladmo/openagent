// tokens.go: context-token estimation over agent messages — the usage-based
// estimate with a chars/4 fallback, faithful to the reference except that
// coding-agent-only custom roles (which never exist on a PathEntry) are
// absent.
package compaction

import (
	"encoding/json"
	"math"

	oa "github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// CalculateContextTokens totals usage (native totalTokens, else
// components).
func CalculateContextTokens(usage ai.Usage) int {
	if usage.TotalTokens != 0 {
		return int(usage.TotalTokens)
	}
	return int(usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite)
}

// ContextUsageEstimate breaks the context estimate into parts.
type ContextUsageEstimate struct {
	Tokens         int
	UsageTokens    int
	TrailingTokens int
	LastUsageIndex int // -1 when none
}

// getAssistantUsage skips aborted/error/all-zero messages.
func getAssistantUsage(message oa.AgentMessage) *ai.Usage {
	assistant, ok := message.(*ai.AssistantMessage)
	if !ok {
		return nil
	}
	if assistant.StopReason == ai.StopAborted || assistant.StopReason == ai.StopError {
		return nil
	}
	if CalculateContextTokens(assistant.Usage) > 0 {
		return &assistant.Usage
	}
	return nil
}

// getLastAssistantUsageInfo scans backwards for valid usage.
func getLastAssistantUsageInfo(messages []oa.AgentMessage) (*ai.Usage, int) {
	for i := len(messages) - 1; i >= 0; i-- {
		if usage := getAssistantUsage(messages[i]); usage != nil {
			return usage, i
		}
	}
	return nil, -1
}

// EstimateContextTokens estimates from the last assistant usage plus
// trailing estimates.
func EstimateContextTokens(messages []oa.AgentMessage) ContextUsageEstimate {
	usage, usageIndex := getLastAssistantUsageInfo(messages)
	if usage == nil {
		estimated := 0
		for _, message := range messages {
			estimated += EstimateTokens(message)
		}
		return ContextUsageEstimate{Tokens: estimated, TrailingTokens: estimated, LastUsageIndex: -1}
	}

	usageTokens := CalculateContextTokens(*usage)
	trailingTokens := 0
	for i := usageIndex + 1; i < len(messages); i++ {
		trailingTokens += EstimateTokens(messages[i])
	}
	return ContextUsageEstimate{
		Tokens:         usageTokens + trailingTokens,
		UsageTokens:    usageTokens,
		TrailingTokens: trailingTokens,
		LastUsageIndex: usageIndex,
	}
}

// EstimateTokens approximates one message's token cost (chars/4).
func EstimateTokens(message oa.AgentMessage) int {
	estimate := func(chars int) int { return int(math.Ceil(float64(chars) / 4)) }
	contentChars := func(content ai.Content) int {
		if content.IsText {
			return len(content.Text)
		}
		chars := 0
		for _, block := range content.Blocks {
			if text, ok := block.(ai.TextContent); ok {
				chars += len(text.Text)
			}
		}
		return chars
	}

	switch typed := message.(type) {
	case *ai.SystemMessage:
		chars := contentChars(typed.Content)
		// Sections append their full text.
		for _, section := range typed.Sections {
			if section != nil {
				chars += len(*section)
			}
		}
		return estimate(chars)
	case *ai.UserMessage:
		return estimate(contentChars(typed.Content))
	case *ai.AssistantMessage:
		chars := 0
		for _, block := range typed.Content {
			switch blockTyped := block.(type) {
			case ai.TextContent:
				chars += len(blockTyped.Text)
			case ai.ThinkingContent:
				chars += len(blockTyped.Thinking)
			case *ai.ToolCall:
				encoded, _ := json.Marshal(blockTyped.Arguments)
				chars += len(blockTyped.Name) + len(encoded)
			}
		}
		return estimate(chars)
	case *ai.ToolResultMessage:
		return estimate(contentChars(ai.Content{Blocks: typed.Content}))
	}
	return 0
}
