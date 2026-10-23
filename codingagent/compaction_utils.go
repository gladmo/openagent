package codingagent

// Port of core/compaction/utils.ts: file-operation tracking and conversation
// serialization for summarization.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// ============================================================================
// File Operation Tracking
// ============================================================================

// FileOperations tracks file reads/writes/edits from tool calls.
type FileOperations struct {
	Read    map[string]bool
	Written map[string]bool
	Edited  map[string]bool
}

// CreateFileOps builds an empty tracker.
func CreateFileOps() *FileOperations {
	return &FileOperations{
		Read:    map[string]bool{},
		Written: map[string]bool{},
		Edited:  map[string]bool{},
	}
}

// ExtractFileOpsFromMessage records read/write/edit tool calls in one
// assistant message.
func ExtractFileOpsFromMessage(message agent.AgentMessage, fileOps *FileOperations) {
	assistant, ok := message.(*ai.AssistantMessage)
	if !ok {
		return
	}
	for _, block := range assistant.Content {
		toolCall, ok := block.(*ai.ToolCall)
		if !ok {
			continue
		}
		path := ""
		if toolCall.Arguments != nil {
			if value, ok := toolCall.Arguments.Get("path"); ok {
				path, _ = value.(string)
			}
		}
		if path == "" {
			continue
		}
		switch toolCall.Name {
		case "read":
			fileOps.Read[path] = true
		case "write":
			fileOps.Written[path] = true
		case "edit":
			fileOps.Edited[path] = true
		}
	}
}

// ComputeFileLists derives the read-only and modified file lists (a nil
// tracker yields empty lists).
func ComputeFileLists(fileOps *FileOperations) (readFiles, modifiedFiles []string) {
	if fileOps == nil {
		return nil, nil
	}
	modified := map[string]bool{}
	for path := range fileOps.Edited {
		modified[path] = true
	}
	for path := range fileOps.Written {
		modified[path] = true
	}
	for path := range fileOps.Read {
		if !modified[path] {
			readFiles = append(readFiles, path)
		}
	}
	for path := range modified {
		modifiedFiles = append(modifiedFiles, path)
	}
	sort.Strings(readFiles)
	sort.Strings(modifiedFiles)
	return readFiles, modifiedFiles
}

// FormatFileOperations renders the file lists as XML tags for summaries.
func FormatFileOperations(readFiles, modifiedFiles []string) string {
	var sections []string
	if len(readFiles) > 0 {
		sections = append(sections, "<read-files>\n"+strings.Join(readFiles, "\n")+"\n</read-files>")
	}
	if len(modifiedFiles) > 0 {
		sections = append(sections, "<modified-files>\n"+strings.Join(modifiedFiles, "\n")+"\n</modified-files>")
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

// ============================================================================
// Message Serialization
// ============================================================================

// ToolResultMaxChars bounds tool results in serialized summaries.
const ToolResultMaxChars = 2000

// truncateForSummary keeps the beginning and appends a marker.
func truncateForSummary(text string, maxChars int) string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	truncatedChars := len(runes) - maxChars
	return string(runes[:maxChars]) + fmt.Sprintf("\n\n[... %d more characters truncated]", truncatedChars)
}

// SerializeConversation renders LLM messages as labeled text so the model
// summarizes instead of continuing them. Tool results are truncated.
func SerializeConversation(messages []ai.Message) string {
	var parts []string

	for _, message := range messages {
		switch typed := message.(type) {
		case *ai.UserMessage:
			content := ai.ContentText(typed.Content, "")
			if content != "" {
				parts = append(parts, "[User]: "+content)
			}
		case *ai.AssistantMessage:
			// Thinking is omitted: providers return summaries of the real
			// reasoning, and replayed thinking is often not part of the
			// model's live context (a block that does not match its request
			// is dropped). Including it can make the summarization request
			// larger than the context it summarizes. Visible text, tool
			// calls, and tool results record what happened (#10893).
			var toolCalls []string
			hasText := false
			for _, block := range typed.Content {
				switch blockTyped := block.(type) {
				case *ai.ToolCall:
					var argParts []string
					if blockTyped.Arguments != nil {
						keys := blockTyped.Arguments.Keys()
						for _, key := range keys {
							value, _ := blockTyped.Arguments.Get(key)
							encoded, _ := json.Marshal(value)
							argParts = append(argParts, key+"="+string(encoded))
						}
					}
					toolCalls = append(toolCalls, blockTyped.Name+"("+strings.Join(argParts, ", ")+")")
				case ai.TextContent:
					hasText = true
				}
			}

			if hasText {
				parts = append(parts, "[Assistant]: "+ai.ContentText(ai.Content{Blocks: typed.Content}, ""))
			}
			if len(toolCalls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(toolCalls, "; "))
			}
		case *ai.ToolResultMessage:
			content := ai.ContentText(ai.Content{Blocks: typed.Content}, "")
			if content != "" {
				parts = append(parts, "[Tool result]: "+truncateForSummary(content, ToolResultMaxChars))
			}
		}
	}

	return strings.Join(parts, "\n\n")
}

// ============================================================================
// Summarization System Prompt
// ============================================================================

// SummarizationSystemPrompt frames the summarization request.
const SummarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`
