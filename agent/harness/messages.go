package harness

// messages.go ports harness/messages.ts: the harness's custom AgentMessage
// roles (bashExecution, custom, branchSummary, compactionSummary) and the
// harness ConvertToLlm.

import (
	"fmt"
	"time"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// Summary framing constants.
const (
	CompactionSummaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	CompactionSummarySuffix = "\n</summary>"
	BranchSummaryPrefix     = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
	BranchSummarySuffix     = "</summary>"
)

// Custom message roles.
const (
	RoleBashExecution     = "bashExecution"
	RoleCustom            = "custom"
	RoleBranchSummary     = "branchSummary"
	RoleCompactionSummary = "compactionSummary"
)

// BashExecutionMessage mirrors the TS interface, implemented as the shared
// CustomAgentMessage carrier.
type BashExecutionMessage struct {
	agent.CustomAgentMessage
	Command            string
	Output             string
	ExitCode           *int64
	Cancelled          bool
	Truncated          bool
	FullOutputPath     string
	HasFullOutputPath  bool
	ExcludeFromContext bool
}

// Role implements AgentMessage.
func (m *BashExecutionMessage) Role() string { return RoleBashExecution }

// CustomMessage mirrors the TS interface.
type CustomMessage struct {
	agent.CustomAgentMessage
	CustomType string
	Content    ai.Content
	Display    bool
	Details    any
}

// Role implements AgentMessage.
func (m *CustomMessage) Role() string { return RoleCustom }

// BranchSummaryMessage mirrors the TS interface.
type BranchSummaryMessage struct {
	agent.CustomAgentMessage
	Summary string
	FromID  *string
}

// Role implements AgentMessage.
func (m *BranchSummaryMessage) Role() string { return RoleBranchSummary }

// CompactionSummaryMessage mirrors the TS interface.
type CompactionSummaryMessage struct {
	agent.CustomAgentMessage
	Summary      string
	TokensBefore float64
}

// Role implements AgentMessage.
func (m *CompactionSummaryMessage) Role() string { return RoleCompactionSummary }

// BashExecutionToText mirrors bashExecutionToText.
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
		text += fmt.Sprintf("\n\n[Output truncated. Full output: %s]", msg.FullOutputPath)
	}
	return text
}

func parseTimestamp(timestamp any) float64 {
	switch t := timestamp.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return float64(parsed.UnixMilli())
		}
	}
	return float64(time.Now().UnixMilli())
}

// CreateBranchSummaryMessage mirrors the TS factory.
func CreateBranchSummaryMessage(summary string, fromID *string, timestamp any) *BranchSummaryMessage {
	return &BranchSummaryMessage{
		CustomAgentMessage: agent.CustomAgentMessage{Role_: RoleBranchSummary, TimestampMs: parseTimestamp(timestamp)},
		Summary:            summary,
		FromID:             fromID,
	}
}

// CreateCompactionSummaryMessage mirrors the TS factory.
func CreateCompactionSummaryMessage(summary string, tokensBefore float64, timestamp any) *CompactionSummaryMessage {
	return &CompactionSummaryMessage{
		CustomAgentMessage: agent.CustomAgentMessage{Role_: RoleCompactionSummary, TimestampMs: parseTimestamp(timestamp)},
		Summary:            summary,
		TokensBefore:       tokensBefore,
	}
}

// CreateCustomMessage mirrors the TS factory.
func CreateCustomMessage(customType string, content ai.Content, display bool, details any, timestamp any) *CustomMessage {
	return &CustomMessage{
		CustomAgentMessage: agent.CustomAgentMessage{Role_: RoleCustom, TimestampMs: parseTimestamp(timestamp)},
		CustomType:         customType,
		Content:            content,
		Display:            display,
		Details:            details,
	}
}

// CreateBashExecutionMessage builds a bashExecution message.
func CreateBashExecutionMessage(command, output string, exitCode *int64, cancelled, truncated bool, fullOutputPath string, timestamp float64) *BashExecutionMessage {
	return &BashExecutionMessage{
		CustomAgentMessage: agent.CustomAgentMessage{Role_: RoleBashExecution, TimestampMs: timestamp},
		Command:            command,
		Output:             output,
		ExitCode:           exitCode,
		Cancelled:          cancelled,
		Truncated:          truncated,
		FullOutputPath:     fullOutputPath,
		HasFullOutputPath:  fullOutputPath != "",
	}
}

// ConvertToLlm mirrors harness convertToLlm: custom roles become user
// messages; excludeFromContext filters.
func ConvertToLlm(messages []agent.AgentMessage) []ai.Message {
	out := make([]ai.Message, 0, len(messages))
	for _, m := range messages {
		switch msg := m.(type) {
		case *BashExecutionMessage:
			if msg.ExcludeFromContext {
				continue
			}
			out = append(out, &ai.UserMessage{
				Content:     ai.BlocksContent(ai.TextContent{Text: BashExecutionToText(msg)}),
				TimestampMs: msg.TimestampMs,
			})
		case *CustomMessage:
			content := msg.Content
			if content.IsText {
				content = ai.BlocksContent(ai.TextContent{Text: content.Text})
			}
			out = append(out, &ai.UserMessage{Content: content, TimestampMs: msg.TimestampMs})
		case *BranchSummaryMessage:
			out = append(out, &ai.UserMessage{
				Content:     ai.BlocksContent(ai.TextContent{Text: BranchSummaryPrefix + msg.Summary + BranchSummarySuffix}),
				TimestampMs: msg.TimestampMs,
			})
		case *CompactionSummaryMessage:
			out = append(out, &ai.UserMessage{
				Content:     ai.BlocksContent(ai.TextContent{Text: CompactionSummaryPrefix + msg.Summary + CompactionSummarySuffix}),
				TimestampMs: msg.TimestampMs,
			})
		case *ai.SystemMessage, *ai.UserMessage, *ai.AssistantMessage, *ai.ToolResultMessage:
			out = append(out, msg.(ai.Message))
		}
	}
	return out
}

// keep jsonx referenced for future message field carriers.
var _ = jsonx.NewObj
