package codingagent

// Port of core/messages.ts: coding-agent custom message types (bash
// execution, extension-injected custom, branch/compaction summaries) and
// the LLM transformation, built on the ported agent/ai packages.

import (
	"fmt"
	"time"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// CompactionSummaryPrefix frames compacted history.
const CompactionSummaryPrefix = `The conversation history before this point was compacted into the following summary:

<summary>
`

// CompactionSummarySuffix closes the summary frame.
const CompactionSummarySuffix = `
</summary>`

// BranchSummaryPrefix frames a returned branch summary.
const BranchSummaryPrefix = `The following is a summary of a branch that this conversation came back from:

<summary>
`

// BranchSummarySuffix closes the branch summary frame.
const BranchSummarySuffix = `</summary>`

// Custom message roles (the TS declaration-merge keys).
const (
	RoleBashExecution     = "bashExecution"
	RoleCustom            = "custom"
	RoleBranchSummary     = "branchSummary"
	RoleCompactionSummary = "compactionSummary"
)

// BashExecutionMessage records a ! command execution.
type BashExecutionMessage struct {
	TimestampMs        float64
	Command            string
	Output             string
	ExitCode           *int
	Cancelled          bool
	Truncated          bool
	FullOutputPath     string
	ExcludeFromContext bool
}

// Role implements AgentMessage.
func (m *BashExecutionMessage) Role() string { return RoleBashExecution }

// CustomMessage is an extension-injected message (sendMessage()).
type CustomMessage struct {
	TimestampMs float64
	CustomType  string
	Content     any // string or []ai.ContentBlock
	Display     bool
	Details     any
}

// Role implements AgentMessage.
func (m *CustomMessage) Role() string { return RoleCustom }

// BranchSummaryMessage records a summary of an abandoned branch.
type BranchSummaryMessage struct {
	TimestampMs float64
	Summary     string
	FromID      *string
}

// Role implements AgentMessage.
func (m *BranchSummaryMessage) Role() string { return RoleBranchSummary }

// CompactionSummaryMessage records compacted history.
type CompactionSummaryMessage struct {
	TimestampMs  float64
	Summary      string
	TokensBefore int64
}

// Role implements AgentMessage.
func (m *CompactionSummaryMessage) Role() string { return RoleCompactionSummary }

// BashExecutionToText renders a bash execution as user-visible text.
func BashExecutionToText(msg *BashExecutionMessage) string {
	text := fmt.Sprintf("Ran `%s`\n", msg.Command)
	if msg.Output != "" {
		text += "```\n" + msg.Output + "\n```"
	} else {
		text += "(no output)"
	}
	if msg.Cancelled {
		text += "\n\n(command cancelled)"
	} else if msg.ExitCode != nil && *msg.ExitCode != 0 {
		text += fmt.Sprintf("\n\nCommand exited with code %d", *msg.ExitCode)
	}
	if msg.Truncated && msg.FullOutputPath != "" {
		text += "\n\n[Output truncated. Full output: " + msg.FullOutputPath + "]"
	}
	return text
}

// parseTimestamp accepts RFC3339 strings or epoch milliseconds.
func parseTimestamp(timestamp string) float64 {
	if parsed, err := time.Parse(time.RFC3339, timestamp); err == nil {
		return float64(parsed.UnixMilli())
	}
	var millis float64
	if _, err := fmt.Sscanf(timestamp, "%f", &millis); err == nil {
		return millis
	}
	return 0
}

// CreateBranchSummaryMessage builds a branch summary from ISO timestamps.
func CreateBranchSummaryMessage(summary, fromID, timestamp string) *BranchSummaryMessage {
	var id *string
	if fromID != "" {
		id = &fromID
	}
	return &BranchSummaryMessage{
		Summary:     summary,
		FromID:      id,
		TimestampMs: parseTimestamp(timestamp),
	}
}

// CreateCompactionSummaryMessage builds a compaction summary.
func CreateCompactionSummaryMessage(summary string, tokensBefore int64, timestamp string) *CompactionSummaryMessage {
	return &CompactionSummaryMessage{
		Summary:      summary,
		TokensBefore: tokensBefore,
		TimestampMs:  parseTimestamp(timestamp),
	}
}

// CreateCustomMessage builds an extension-injected message.
func CreateCustomMessage(customType string, content any, display bool, details any, timestamp string) *CustomMessage {
	return &CustomMessage{
		CustomType:  customType,
		Content:     content,
		Display:     display,
		Details:     details,
		TimestampMs: parseTimestamp(timestamp),
	}
}

// ConvertToLlm transforms AgentMessages (including the custom roles) into
// LLM-compatible messages; context-excluded entries drop out.
func ConvertToLlm(messages []agent.AgentMessage) []ai.Message {
	var out []ai.Message
	for _, m := range messages {
		switch typed := m.(type) {
		case *BashExecutionMessage:
			if typed.ExcludeFromContext {
				continue
			}
			out = append(out, &ai.UserMessage{
				Content: ai.BlocksContent(ai.TextContent{Text: BashExecutionToText(typed)}),
			})
		case *CustomMessage:
			if text, ok := typed.Content.(string); ok {
				out = append(out, &ai.UserMessage{Content: ai.StringContent(text)})
				continue
			}
			if blocks, ok := typed.Content.([]ai.ContentBlock); ok {
				out = append(out, &ai.UserMessage{Content: ai.BlocksContent(blocks...)})
			}
		case *BranchSummaryMessage:
			out = append(out, &ai.UserMessage{
				Content: ai.BlocksContent(ai.TextContent{
					Text: BranchSummaryPrefix + typed.Summary + BranchSummarySuffix,
				}),
			})
		case *CompactionSummaryMessage:
			out = append(out, &ai.UserMessage{
				Content: ai.BlocksContent(ai.TextContent{
					Text: CompactionSummaryPrefix + typed.Summary + CompactionSummarySuffix,
				}),
			})
		default:
			// Standard roles pass through unchanged.
			if aiMessage, ok := m.(ai.Message); ok {
				out = append(out, aiMessage)
			}
		}
	}
	return out
}
