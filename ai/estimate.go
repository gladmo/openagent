package ai

import (
	"math"

	"github.com/gladmo/openagent/jsonx"
)

// estimate.go ports utils/estimate.ts.

// ContextUsageEstimate mirrors the TS interface.
type ContextUsageEstimate struct {
	Tokens         float64
	UsageTokens    float64
	TrailingTokens float64
	LastUsageIndex int // -1 = null
}

const (
	charsPerToken       = 4.0
	estimatedImageChars = 4800.0
)

// CalculateContextTokens mirrors calculateContextTokens.
func CalculateContextTokens(usage Usage) float64 {
	total := usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	if usage.TotalTokens != 0 || total == 0 {
		return usage.TotalTokens
	}
	return total
}

// EstimateTextTokens is ceil(len/4).
func EstimateTextTokens(text string) float64 {
	return ceilDiv(float64(len([]rune(text))), charsPerToken)
}

// EstimateTextAndImageContentTokens estimates text/image content blocks.
func EstimateTextAndImageContentTokens(content Content) float64 {
	if content.IsText {
		return ceilDiv(float64(len([]rune(content.Text))), charsPerToken)
	}
	chars := 0.0
	for _, block := range content.Blocks {
		switch b := block.(type) {
		case TextContent:
			chars += float64(len([]rune(b.Text)))
		default:
			chars += estimatedImageChars
		}
	}
	return ceilDiv(chars, charsPerToken)
}

// EstimateMessageTokens estimates one message's token cost.
func EstimateMessageTokens(message Message) float64 {
	switch m := message.(type) {
	case *SystemMessage:
		return EstimateTextTokens(GetSystemMessageText(m)) +
			estimateToolsTokens(m.ToolsAdded) +
			estimateToolsTokensToolsRemoved(m.ToolsRemoved)
	case *UserMessage:
		return EstimateTextAndImageContentTokens(m.Content)
	case *ToolResultMessage:
		return EstimateTextAndImageContentTokens(BlocksContent(m.Content...))
	}
	assistant := message.(*AssistantMessage)
	chars := 0.0
	for _, block := range assistant.Content {
		switch b := block.(type) {
		case TextContent:
			chars += float64(len([]rune(b.Text)))
		case ThinkingContent:
			chars += float64(len([]rune(b.Thinking)))
		case *ToolCall:
			chars += float64(len([]rune(b.Name))) + float64(len(jsonxStringifyToolCallArgs(b)))
		}
	}
	return ceilDiv(chars, charsPerToken)
}

func jsonxStringifyToolCallArgs(call *ToolCall) string {
	if call.Arguments == nil {
		return "{}"
	}
	return jsonStringify(call.Arguments)
}

func estimateToolsTokens(tools []Tool) float64 {
	if len(tools) == 0 {
		return 0
	}
	return EstimateTextTokens(jsonStringify(toolsToJSONList(tools)))
}

func estimateToolsTokensToolsRemoved(refs []ToolReference) float64 {
	if len(refs) == 0 {
		return 0
	}
	list := make([]any, 0, len(refs))
	for _, ref := range refs {
		list = append(list, map[string]any{"name": ref.Name})
	}
	return EstimateTextTokens(jsonStringify(list))
}

// EstimateContextTokens estimates the token count of a transcript. Note: TS
// string lengths are UTF-16 code units; the Go port counts runes, which
// matches for BMP text and is more stable for astral characters.
func EstimateContextTokens(messages []Message) ContextUsageEstimate {
	index, usage, has := getLastAssistantUsageInfo(messages)
	if has {
		usageTokens := CalculateContextTokens(usage)
		trailingTokens := 0.0
		for i := index + 1; i < len(messages); i++ {
			trailingTokens += EstimateMessageTokens(messages[i])
		}
		return ContextUsageEstimate{
			Tokens:         usageTokens + trailingTokens,
			UsageTokens:    usageTokens,
			TrailingTokens: trailingTokens,
			LastUsageIndex: index,
		}
	}
	tokens := 0.0
	for _, message := range messages {
		tokens += EstimateMessageTokens(message)
	}
	return ContextUsageEstimate{Tokens: tokens, UsageTokens: 0, TrailingTokens: tokens, LastUsageIndex: -1}
}

func getLastAssistantUsageInfo(messages []Message) (int, Usage, bool) {
	latestPrefixTimestamp := mathInfNeg()
	usageInfo := Usage{}
	usageIndex := -1
	found := false
	for i, message := range messages {
		if assistant, ok := message.(*AssistantMessage); ok {
			usageAppliesToPrefix := assistant.TimestampMs >= latestPrefixTimestamp
			if usageAppliesToPrefix &&
				assistant.StopReason != StopAborted &&
				assistant.StopReason != StopError &&
				CalculateContextTokens(assistant.Usage) > 0 {
				usageInfo = assistant.Usage
				usageIndex = i
				found = true
			}
		}
		latestPrefixTimestamp = mathMax(latestPrefixTimestamp, messageTimestamp(message))
	}
	return usageIndex, usageInfo, found
}

func messageTimestamp(message Message) float64 {
	switch m := message.(type) {
	case *SystemMessage:
		return m.TimestampMs
	case *UserMessage:
		return m.TimestampMs
	case *AssistantMessage:
		return m.TimestampMs
	case *ToolResultMessage:
		return m.TimestampMs
	default:
		return 0
	}
}

func ceilDiv(v, unit float64) float64 {
	return math.Ceil(v / unit)
}

func mathInfNeg() float64 { return math.Inf(-1) }

func mathMax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func jsonStringify(v any) string { return jsonx.Stringify(v) }

func toolsToJSONList(tools []Tool) []any {
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, ToolToJSON(tool))
	}
	return out
}
