package codingagent

// Port of core/compaction/branch-summarization.ts (pure-function core):
// branch-entry collection, token-budgeted preparation, and the summary
// framing constants. The LLM-call path lands with the runtime streaming
// phase; the framing helpers are exercised via unit tests.

import (
	"encoding/json"

	"github.com/gladmo/openagent/agent"
)

// BranchSummaryResult is the generation outcome.
type BranchSummaryResult struct {
	Summary       string
	Usage         *json.RawMessage
	ReadFiles     []string
	ModifiedFiles []string
	Aborted       bool
	Error         string
}

// BranchSummaryDetails stores file tracking in BranchSummaryEntry.Details.
type BranchSummaryDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// BranchPreparation carries the budgeted messages and file operations.
type BranchPreparation struct {
	// Messages extracted for summarization, chronological.
	Messages []interface{}
	// FileOps extracted from tool calls and nested summaries.
	FileOps *FileOperations
	// TotalTokens is the estimated budget consumed.
	TotalTokens int
}

// CollectEntriesResult reports the branch slice to summarize.
type CollectEntriesResult struct {
	// Entries to summarize, chronological.
	Entries []SessionEntry
	// CommonAncestorID is the shared node ("" when none).
	CommonAncestorID string
}

// CollectEntriesForBranchSummary walks from oldLeafId back to the common
// ancestor with targetId, collecting entries (compaction boundaries
// included; their summaries become context).
func CollectEntriesForBranchSummary(getBranch func(id string) []SessionEntry, getEntry func(id string) SessionEntry, oldLeafID, targetID string) CollectEntriesResult {
	if oldLeafID == "" {
		return CollectEntriesResult{}
	}

	oldPath := map[string]bool{}
	for _, entry := range getBranch(oldLeafID) {
		oldPath[entry.base().ID] = true
	}
	targetPath := getBranch(targetID)

	commonAncestorID := ""
	for i := len(targetPath) - 1; i >= 0; i-- {
		if oldPath[targetPath[i].base().ID] {
			commonAncestorID = targetPath[i].base().ID
			break
		}
	}

	var entries []SessionEntry
	current := oldLeafID
	for current != "" && current != commonAncestorID {
		entry := getEntry(current)
		if entry == nil {
			break
		}
		entries = append(entries, entry)
		parentID := entry.base().ParentID
		if parentID == nil {
			break
		}
		current = *parentID
	}

	// Reverse to chronological order.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return CollectEntriesResult{Entries: entries, CommonAncestorID: commonAncestorID}
}

// getMessageFromBranchEntry converts one entry to its message (compaction
// entries included; tool results skipped).
func getMessageFromBranchEntry(entry SessionEntry) interface{} {
	switch typed := entry.(type) {
	case *SessionMessageEntry:
		var probe struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(typed.Message, &probe); err != nil {
			return nil
		}
		if probe.Role == "toolResult" {
			return nil
		}
		return rawAgentMessage{raw: typed.Message}
	case *CustomMessageEntry:
		return CreateCustomMessage(typed.CustomType, json.RawMessage(typed.Content), typed.Display, json.RawMessage(typed.Details), typed.Timestamp)
	case *BranchSummaryEntry:
		return CreateBranchSummaryMessage(typed.Summary, typed.FromID, typed.Timestamp)
	case *CompactionEntry:
		return CreateCompactionSummaryMessage(typed.Summary, typed.TokensBefore, typed.Timestamp)
	}
	return nil
}

// PrepareBranchEntries walks newest-to-oldest adding messages until the
// token budget (0 = unlimited), keeping summary entries even over budget
// within 90%, and collects cumulative file operations from all entries.
func PrepareBranchEntries(entries []SessionEntry, tokenBudget int) BranchPreparation {
	var messages []interface{}
	fileOps := CreateFileOps()
	totalTokens := 0

	// First pass: file ops from ALL entries (cumulative nested summaries,
	// pi-generated only).
	for _, entry := range entries {
		branch, ok := entry.(*BranchSummaryEntry)
		if !ok || branch.FromHook || len(branch.Details) == 0 {
			continue
		}
		var details BranchSummaryDetails
		if err := json.Unmarshal(branch.Details, &details); err != nil {
			continue
		}
		for _, file := range details.ReadFiles {
			fileOps.Read[file] = true
		}
		for _, file := range details.ModifiedFiles {
			fileOps.Edited[file] = true
		}
	}

	// Second pass: newest to oldest within budget.
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		message := getMessageFromBranchEntry(entry)
		if message == nil {
			continue
		}

		if agentMessage, ok := message.(interface {
			Role() string
		}); ok {
			_ = agentMessage
		}
		if raw, isRaw := message.(rawAgentMessage); isRaw {
			if decoded := decodeAgentMessage(raw.raw); decoded != nil {
				if asAgent, ok := decoded.(agentMessageAlias); ok {
					ExtractFileOpsFromMessage(asAgent, fileOps)
				}
			}
		}

		tokens := estimateMessageTokens(message)

		if tokenBudget > 0 && totalTokens+tokens > tokenBudget {
			switch entry.(type) {
			case *CompactionEntry, *BranchSummaryEntry:
				if float64(totalTokens) < float64(tokenBudget)*0.9 {
					messages = prepend(messages, message)
					totalTokens += tokens
				}
			}
			break
		}

		messages = prepend(messages, message)
		totalTokens += tokens
	}

	return BranchPreparation{Messages: messages, FileOps: fileOps, TotalTokens: totalTokens}
}

// agentMessageAlias matches the agent message interface for file-op
// extraction without importing the agent package here.
type agentMessageAlias = interface {
	Role() string
}

func prepend(messages []interface{}, message interface{}) []interface{} {
	return append([]interface{}{message}, messages...)
}

func estimateMessageTokens(message interface{}) int {
	if raw, ok := message.(rawAgentMessage); ok {
		if decoded := decodeAgentMessage(raw.raw); decoded != nil {
			return estimateDecodedTokens(decoded)
		}
		return 0
	}
	return estimateDecodedTokens(message)
}

// estimateDecodedTokens estimates a typed or custom message.
func estimateDecodedTokens(message interface{}) int {
	if asAgent, ok := message.(agent.AgentMessage); ok {
		return EstimateTokens(asAgent)
	}
	switch typed := message.(type) {
	case *CustomMessage:
		if text, ok := typed.Content.(json.RawMessage); ok {
			var asString string
			if err := json.Unmarshal(text, &asString); err == nil {
				return (len(asString) + 3) / 4
			}
		}
	case *BranchSummaryMessage:
		return (len(typed.Summary) + 3) / 4
	case *CompactionSummaryMessage:
		return (len(typed.Summary) + 3) / 4
	}
	return 0
}

// BranchSummaryPreamble frames the returned exploration summary.
const BranchSummaryPreamble = `The user explored a different conversation branch before returning here.
Summary of that exploration:

`

// BranchSummaryPrompt instructs the structured branch summary.
const BranchSummaryPrompt = `Create a structured summary of this conversation branch for context when returning later.

Use this EXACT format:

## Goal
[What was the user trying to accomplish in this branch?]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Work that was started but not finished]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next to continue this work]

Keep each section concise. Preserve exact file paths, function names, and error messages.`
