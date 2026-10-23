package codingagent

// Port of core/compaction/compaction.ts (summarization half): the
// structured-summary prompt constants, the failure classifier for
// persisted checkpoints, summarization option construction, and the
// retry-wrapped LLM call that every compaction/branch summary shares.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// SummarizationPrompt creates the initial structured checkpoint summary.
const SummarizationPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// UpdateSummarizationInstructions merges new messages into an existing
// summary while preserving prior information.
const UpdateSummarizationInstructions = `Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// UpdateSummarizationPrompt wraps the new-turn merge instruction.
const UpdateSummarizationPrompt = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

` + UpdateSummarizationInstructions

// TurnPrefixSummarizationPrompt summarizes earlier context when a turn
// splits across the compaction boundary.
const TurnPrefixSummarizationPrompt = `The messages above are earlier context from an ongoing conversation. Later messages are stored separately and do not need to be reconstructed.`

// GetSummarizationFailure reports why a summarization response cannot be
// persisted: a length stop carries partial text and must not become a
// session checkpoint.
func GetSummarizationFailure(response *ai.AssistantMessage, label string) string {
	switch response.StopReason {
	case "error":
		message := "Unknown error"
		if response.ErrorMessage != nil && *response.ErrorMessage != "" {
			message = *response.ErrorMessage
		}
		return fmt.Sprintf("%s failed: %s", label, message)
	case "length":
		return fmt.Sprintf("%s failed: generation hit the token cap and the summary is incomplete", label)
	}
	return ""
}

// SummarizationOptions configure one summarization call.
type SummarizationOptions struct {
	MaxTokens     int
	ApiKey        string
	Headers       map[string]string
	Env           map[string]string
	Signal        context.Context
	ThinkingLevel string
	SessionID     string
	// CacheRetention: one-off summaries avoid cache writes ("none").
	CacheRetention string
}

// CreateSummarizationOptions builds the simple-stream options (reasoning
// only for models that support it and a non-off level). Summary requests
// carry no maxTokens cap: the model's own output limit applies (#9512).
func CreateSummarizationOptions(model *ai.Model, apiKey string, headers, env map[string]string, signal context.Context, thinkingLevel, sessionID string) *SummarizationOptions {
	options := &SummarizationOptions{
		ApiKey:    apiKey,
		Headers:   headers,
		Env:       env,
		Signal:    signal,
		SessionID: sessionID,
	}
	if model != nil && model.Reasoning && thinkingLevel != "" && thinkingLevel != "off" {
		options.ThinkingLevel = thinkingLevel
	}
	return options
}

// SummarizationStreamFn performs one LLM call and returns the assistant
// message (the ai-provider seam).
type SummarizationStreamFn func(ctx context.Context, model *ai.Model, contextMessages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error)

// CompleteSummarization is the shared choke point for every compaction
// and branch-summary call: it avoids cache writes for one-off summaries,
// reuses caller routing or a fresh session id, and retries through the
// ai package's policy so transient stream drops do not fail the whole
// compaction on the first attempt.
func CompleteSummarization(ctx context.Context, model *ai.Model, contextMessages []ai.Message, options *SummarizationOptions, streamFn SummarizationStreamFn, retry *ai.RetryPolicy) (*ai.AssistantMessage, error) {
	if options == nil {
		options = &SummarizationOptions{}
	}
	requestOptions := *options
	requestOptions.CacheRetention = "none"
	if requestOptions.SessionID == "" {
		requestOptions.SessionID = generateSessionID()
	}
	if streamFn == nil {
		return nil, errors.New("summarization requires a stream function")
	}

	var callErr error
	produce := func() *ai.AssistantMessage {
		message, err := streamFn(ctx, model, contextMessages, &requestOptions)
		if err != nil {
			// Surface transport failures through the message's error
			// stop reason so the shared retry policy applies.
			callErr = err
			text := err.Error()
			return &ai.AssistantMessage{StopReason: "error", ErrorMessage: &text}
		}
		callErr = nil
		return message
	}
	response := ai.RetryAssistantCall(produce, retry, abortSignalFromContext(ctx), nil)
	if callErr != nil && response != nil && response.StopReason == "error" {
		return response, callErr
	}
	return response, nil
}

var _ = agent.AgentMessage(nil)

// SummaryResult carries the generated text and its provider usage.
type SummaryResult struct {
	Text  string
	Usage ai.Usage
}

// extractTextBlocks joins the text blocks of an assistant message's
// content. Blocks arrive as VALUE types from providers and decode
// paths (ai.TextContent implements ContentBlock by value); pointer
// forms are accepted defensively.
func extractTextBlocks(content []ai.ContentBlock) string {
	var parts []string
	for _, block := range content {
		switch typed := block.(type) {
		case ai.TextContent:
			parts = append(parts, typed.Text)
		case *ai.TextContent:
			if typed != nil {
				parts = append(parts, typed.Text)
			}
		}
	}
	return strings.Join(parts, "")
}

// BuildSummarizationContext builds the standalone summary request
// (system prompt + one user turn holding the wrapped conversation).
func BuildSummarizationContext(promptText string) []ai.Message {
	return []ai.Message{
		&ai.SystemMessage{Content: ai.StringContent(SummarizationSystemPrompt)},
		&ai.UserMessage{Content: ai.StringContent(promptText)},
	}
}

// GenerateSummaryOptions configure one summary generation.
type GenerateSummaryOptions struct {
	// ReserveTokens thresholds when compaction runs; it no longer caps
	// reserve, model max)).
	ReserveTokens      int
	ApiKey             string
	Headers            map[string]string
	Env                map[string]string
	Signal             context.Context
	CustomInstructions string
	PreviousSummary    string
	ThinkingLevel      string
	SessionID          string
	Retry              *ai.RetryPolicy
}

// GenerateSummary generates or updates a conversation summary
// (update prompt merges into PreviousSummary).
func GenerateSummary(currentMessages []agent.AgentMessage, model *ai.Model, options *GenerateSummaryOptions, streamFn SummarizationStreamFn) (*SummaryResult, error) {
	if options == nil {
		options = &GenerateSummaryOptions{}
	}

	basePrompt := SummarizationPrompt
	if options.PreviousSummary != "" {
		basePrompt = UpdateSummarizationPrompt
	}
	if options.CustomInstructions != "" {
		basePrompt = basePrompt + "\n\nAdditional focus: " + options.CustomInstructions
	}

	// Serialize the conversation so the model does not continue it.
	llmMessages := ConvertToLlm(currentMessages)
	conversationText := SerializeConversation(llmMessages)

	promptText := "<conversation>\n" + conversationText + "\n</conversation>\n\n"
	if options.PreviousSummary != "" {
		promptText += "<previous-summary>\n" + options.PreviousSummary + "\n</previous-summary>\n\n"
	}
	promptText += basePrompt

	completionOptions := CreateSummarizationOptions(
		model, options.ApiKey, options.Headers, options.Env,
		options.Signal, options.ThinkingLevel, options.SessionID,
	)

	response, err := CompleteSummarization(options.Signal, model, BuildSummarizationContext(promptText), completionOptions, streamFn, options.Retry)
	if err != nil {
		return nil, err
	}
	if failure := GetSummarizationFailure(response, "Summarization"); failure != "" {
		return nil, errors.New(failure)
	}
	for _, block := range response.Content {
		if _, isToolCall := block.(*ai.ToolCall); isToolCall {
			return nil, errors.New("Summarization attempted to call a tool")
		}
	}

	return &SummaryResult{Text: extractTextBlocks(response.Content), Usage: response.Usage}, nil
}

// GenerateBranchSummaryOptions configure branch summarization.
type GenerateBranchSummaryOptions struct {
	ApiKey             string
	Headers            map[string]string
	Env                map[string]string
	Signal             context.Context
	CustomInstructions string
	// ReplaceInstructions swaps the stock prompt for the custom text.
	ReplaceInstructions bool
	ReserveTokens       int
	Retry               *ai.RetryPolicy
}

// GenerateBranchSummary summarizes abandoned branch entries: budget the
// entries by context window, serialize the conversation, call the shared
// summarization choke point, then prepend the preamble and append the
// file-operation lists.
func GenerateBranchSummary(entries []SessionEntry, model *ai.Model, options *GenerateBranchSummaryOptions, streamFn SummarizationStreamFn) *BranchSummaryResult {
	if options == nil {
		options = &GenerateBranchSummaryOptions{}
	}
	reserveTokens := options.ReserveTokens
	if reserveTokens == 0 {
		reserveTokens = 16384
	}

	contextWindow := 128000.0
	if model != nil && model.ContextWindow > 0 {
		contextWindow = model.ContextWindow
	}
	tokenBudget := int(contextWindow) - reserveTokens

	preparation := PrepareBranchEntries(entries, tokenBudget)
	if len(preparation.Messages) == 0 {
		return &BranchSummaryResult{Summary: "No content to summarize"}
	}

	// AgentMessages -> LLM messages -> text (so the model does not treat
	// the transcript as a conversation to continue). Branch entries carry
	// raw payloads, so materialize typed messages first.
	agentMessages := make([]agent.AgentMessage, 0, len(preparation.Messages))
	for _, message := range preparation.Messages {
		switch typed := message.(type) {
		case rawAgentMessage:
			if decoded, ok := decodeAgentMessage(typed.Raw()).(agent.AgentMessage); ok {
				agentMessages = append(agentMessages, decoded)
			}
		case *rawAgentMessage:
			if decoded, ok := decodeAgentMessage(typed.Raw()).(agent.AgentMessage); ok {
				agentMessages = append(agentMessages, decoded)
			}
		case agent.AgentMessage:
			agentMessages = append(agentMessages, typed)
		}
	}
	conversationText := SerializeConversation(ConvertToLlm(agentMessages))

	instructions := BranchSummaryPrompt
	if options.ReplaceInstructions && options.CustomInstructions != "" {
		instructions = options.CustomInstructions
	} else if options.CustomInstructions != "" {
		instructions = BranchSummaryPrompt + "\n\nAdditional focus: " + options.CustomInstructions
	}
	promptText := "<conversation>\n" + conversationText + "\n</conversation>\n\n" + instructions

	response, err := CompleteSummarization(options.Signal, model,
		BuildSummarizationContext(promptText),
		&SummarizationOptions{
			ApiKey:  options.ApiKey,
			Headers: options.Headers,
			Env:     options.Env,
			Signal:  options.Signal,
		}, streamFn, options.Retry)
	if err != nil {
		return &BranchSummaryResult{Error: err.Error()}
	}

	if response.StopReason == "aborted" {
		return &BranchSummaryResult{Aborted: true}
	}
	if failure := GetSummarizationFailure(response, "Branch summarization"); failure != "" {
		return &BranchSummaryResult{Error: failure}
	}
	for _, block := range response.Content {
		if _, isToolCall := block.(*ai.ToolCall); isToolCall {
			return &BranchSummaryResult{Error: "Branch summarization attempted to call a tool"}
		}
	}

	summary := BranchSummaryPreamble + extractTextBlocks(response.Content)

	// File-operation lists append to the summary.
	readFiles, modifiedFiles := ComputeFileLists(preparation.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)

	if summary == "" {
		summary = "No summary generated"
	}
	usage := response.Usage
	encoded, _ := json.Marshal(usage)
	raw := json.RawMessage(encoded)
	return &BranchSummaryResult{
		Summary:       summary,
		Usage:         &raw,
		ReadFiles:     readFiles,
		ModifiedFiles: modifiedFiles,
	}
}

// TurnPrefixSummarizationPromptText is the full turn-prefix instruction
// block (the short constant above names the subject; this carries the
// checkpoint format the reference sends).
const TurnPrefixSummarizationPromptText = `The messages above are earlier context from an ongoing conversation. Later messages are stored separately and do not need to be reconstructed.

Create a concise checkpoint of the user's request and the progress shown above. This checkpoint will be placed before the later messages so the conversation can continue with the necessary context.

## Original Request
[What did the user ask for?]

## Progress So Far
- [Key decisions and work completed in these messages]

## Context Needed to Continue
- [Information from these messages needed to understand the later work]

Only summarize information explicitly present above. Do not infer or recreate later messages.`

// TurnContextSeparator joins the history and turn-prefix summaries when
// a compaction splits a turn.
const TurnContextSeparator = "\n\n---\n\n**Turn Context (split turn):**\n\n"

// CombineUsage sums two usage records (optional fields surface only when
// either side reported them).
func CombineUsage(first, second ai.Usage) ai.Usage {
	combined := ai.Usage{
		Input:       first.Input + second.Input,
		Output:      first.Output + second.Output,
		CacheRead:   first.CacheRead + second.CacheRead,
		CacheWrite:  first.CacheWrite + second.CacheWrite,
		TotalTokens: first.TotalTokens + second.TotalTokens,
	}
	if first.CacheWrite1h != nil || second.CacheWrite1h != nil {
		combined.CacheWrite1h = usageFloatPtr(usageFloatOr(first.CacheWrite1h, 0) + usageFloatOr(second.CacheWrite1h, 0))
	}
	if first.Reasoning != nil || second.Reasoning != nil {
		combined.Reasoning = usageFloatPtr(usageFloatOr(first.Reasoning, 0) + usageFloatOr(second.Reasoning, 0))
	}
	return combined
}

func usageFloatOr(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

func usageFloatPtr(value float64) *float64 { return &value }

// CompactionPreparation is the cut-point plan one compaction executes.
type CompactionPreparation struct {
	FirstKeptEntryID    string
	MessagesToSummarize []agent.AgentMessage
	TurnPrefixMessages  []agent.AgentMessage
	IsSplitTurn         bool
	TokensBefore        int64
	PreviousSummary     string
	FileOps             *FileOperations
	Settings            CompactionSettings
}

// CompactionOutcome is the executed compaction.
type CompactionOutcome struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int64
	Usage            ai.Usage
	ReadFiles        []string
	ModifiedFiles    []string
}

// GenerateTurnPrefixSummary summarizes the head of a split turn under a
// smaller budget (0.5 * reserve) with the turn-prefix checkpoint format.
// GenerateTurnPrefixSummary summarizes the split turn's prefix; the
// reserveTokens parameter is unused (summaries use the model's output
// limit, #9512) but kept for caller parity.
func GenerateTurnPrefixSummary(messages []agent.AgentMessage, model *ai.Model, reserveTokens int, options *GenerateSummaryOptions, streamFn SummarizationStreamFn) (*SummaryResult, error) {
	if options == nil {
		options = &GenerateSummaryOptions{}
	}

	conversationText := SerializeConversation(ConvertToLlm(messages))
	promptText := "# Conversation\n" + conversationText + "\n\n# Instructions\n" + TurnPrefixSummarizationPromptText
	if options.CustomInstructions != "" {
		promptText += "\n\nAdditional focus: " + options.CustomInstructions
	}

	response, err := CompleteSummarization(options.Signal, model,
		BuildSummarizationContext(promptText),
		CreateSummarizationOptions(model, options.ApiKey, options.Headers, options.Env, options.Signal, options.ThinkingLevel, options.SessionID),
		streamFn, options.Retry)
	if err != nil {
		return nil, err
	}
	if failure := GetSummarizationFailure(response, "Turn prefix summarization"); failure != "" {
		return nil, errors.New(failure)
	}
	for _, block := range response.Content {
		if _, isToolCall := block.(*ai.ToolCall); isToolCall {
			return nil, errors.New("Turn prefix summarization attempted to call a tool")
		}
	}

	return &SummaryResult{Text: extractTextBlocks(response.Content), Usage: response.Usage}, nil
}

// Compact executes a prepared compaction: the history summary, plus the
// turn-prefix summary merged with the split-turn separator when a turn
// splits across the boundary, then the file-operation lists.
func Compact(preparation *CompactionPreparation, model *ai.Model, options *GenerateSummaryOptions, streamFn SummarizationStreamFn) (*CompactionOutcome, error) {
	if preparation == nil {
		return nil, errors.New("compaction requires a preparation")
	}
	if preparation.FirstKeptEntryID == "" {
		return nil, errors.New("First kept entry has no UUID - session may need migration")
	}

	var summary string
	var usage ai.Usage

	if preparation.IsSplitTurn && len(preparation.TurnPrefixMessages) > 0 {
		historyText := preparation.PreviousSummary
		if historyText == "" {
			historyText = "No prior history."
		}
		var historyUsage *ai.Usage
		if len(preparation.MessagesToSummarize) > 0 {
			historyResult, err := GenerateSummary(preparation.MessagesToSummarize, model,
				summaryOptionsWithPrevious(options, preparation.Settings.ReserveTokens, preparation.PreviousSummary), streamFn)
			if err != nil {
				return nil, err
			}
			historyText = historyResult.Text
			historyUsage = &historyResult.Usage
		}
		turnPrefixResult, err := GenerateTurnPrefixSummary(preparation.TurnPrefixMessages, model, preparation.Settings.ReserveTokens, options, streamFn)
		if err != nil {
			return nil, err
		}
		summary = historyText + TurnContextSeparator + turnPrefixResult.Text
		if historyUsage != nil {
			usage = CombineUsage(*historyUsage, turnPrefixResult.Usage)
		} else {
			usage = turnPrefixResult.Usage
		}
	} else {
		result, err := GenerateSummary(preparation.MessagesToSummarize, model,
			summaryOptionsWithPrevious(options, preparation.Settings.ReserveTokens, preparation.PreviousSummary), streamFn)
		if err != nil {
			return nil, err
		}
		summary = result.Text
		usage = result.Usage
	}

	readFiles, modifiedFiles := ComputeFileLists(preparation.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)

	return &CompactionOutcome{
		Summary:          summary,
		FirstKeptEntryID: preparation.FirstKeptEntryID,
		TokensBefore:     preparation.TokensBefore,
		Usage:            usage,
		ReadFiles:        readFiles,
		ModifiedFiles:    modifiedFiles,
	}, nil
}

// summaryOptionsWithPrevious copies the caller options with the
// preparation's reserve budget and previous summary applied.
func summaryOptionsWithPrevious(options *GenerateSummaryOptions, reserveTokens int, previousSummary string) *GenerateSummaryOptions {
	copied := GenerateSummaryOptions{ReserveTokens: reserveTokens, PreviousSummary: previousSummary}
	if options != nil {
		copied = *options
		copied.ReserveTokens = reserveTokens
		copied.PreviousSummary = previousSummary
	}
	return &copied
}

// EstimateProjectedContextTokens estimates the projection's context
// tokens, honoring the most recent provider usage unless a context edit
// or compaction invalidated it.
func EstimateProjectedContextTokens(projection *SessionProjection, branchEntries []SessionEntry) ContextUsageEstimate {
	if projection == nil {
		return ContextUsageEstimate{LastUsageIndex: -1}
	}
	// Flatten the projection's per-entry messages into one transcript.
	var messages []agent.AgentMessage
	for _, entry := range projection.Entries {
		for _, message := range entry.Messages {
			if asAgent, ok := message.(agent.AgentMessage); ok {
				messages = append(messages, asAgent)
			}
		}
	}
	estimate := EstimateContextTokens(messages)
	if estimate.LastUsageIndex >= 0 {
		// Attribute the usage to its source entry, then check whether a
		// later context edit or compaction invalidated it.
		projectedIndex := 0
		usageEntryID := ""
		for _, entry := range projection.Entries {
			nextIndex := projectedIndex + len(entry.Messages)
			if estimate.LastUsageIndex < nextIndex {
				usageEntryID = entry.SourceEntry.base().ID
				break
			}
			projectedIndex = nextIndex
		}
		usageEntryIndex := -1
		if usageEntryID != "" {
			for i, entry := range branchEntries {
				if entry.base().ID == usageEntryID {
					usageEntryIndex = i
					break
				}
			}
		}
		latestInvalidatingIndex := -1
		for i := len(branchEntries) - 1; i >= 0; i-- {
			switch branchEntries[i].(type) {
			case *ContextEditEntry, *CompactionEntry:
				latestInvalidatingIndex = i
			}
			if latestInvalidatingIndex >= 0 {
				break
			}
		}
		if usageEntryIndex > latestInvalidatingIndex {
			return estimate
		}
	}

	// No usable usage: estimate from the current system message plus the
	// non-system messages.
	tokens := 0
	if currentSystem := getCurrentSystemMessage(flattenProjectedMessages(projection)); currentSystem != nil {
		tokens += EstimateTokens(currentSystem)
	}
	for _, message := range messages {
		if message.Role() != "system" {
			tokens += EstimateTokens(message)
		}
	}
	return ContextUsageEstimate{Tokens: tokens, TrailingTokens: tokens, LastUsageIndex: -1}
}

// getCurrentSystemMessage returns the newest system message (nil when
// none survives projection).
func getCurrentSystemMessage(messages []interface{}) agent.AgentMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		if asAgent, ok := messages[i].(agent.AgentMessage); ok && asAgent.Role() == "system" {
			return asAgent
		}
	}
	return nil
}

// ShouldCompact reports whether context usage crossed the reserve
// threshold.
func ShouldCompact(contextTokens, contextWindow int, settings CompactionSettings) bool {
	if !settings.Enabled {
		return false
	}
	return contextTokens > contextWindow-settings.ReserveTokens
}

// messagesFromProjectedEntryForCompaction extracts the summarizable
// messages from one projected entry (compaction entries replay through
// their own summary; system messages are prompt state).
func messagesFromProjectedEntryForCompaction(entry ProjectedSessionEntry) []agent.AgentMessage {
	if _, isCompaction := entry.SourceEntry.(*CompactionEntry); isCompaction {
		return nil
	}
	var out []agent.AgentMessage
	for _, message := range entry.Messages {
		asAgent, ok := message.(agent.AgentMessage)
		if !ok || asAgent.Role() == "system" {
			continue
		}
		out = append(out, asAgent)
	}
	return out
}

// PrepareCompaction plans one compaction over a session path (nil when
// nothing is compressible: the path already ends in a compaction, or no
// message falls before the cut point).
func PrepareCompaction(pathEntries []SessionEntry, settings CompactionSettings) *CompactionPreparation {
	if len(pathEntries) > 0 {
		if _, isCompaction := pathEntries[len(pathEntries)-1].(*CompactionEntry); isCompaction {
			return nil
		}
	}

	byID := map[string]SessionEntry{}
	for _, entry := range pathEntries {
		byID[entry.base().ID] = entry
	}
	var leafID *string
	if len(pathEntries) > 0 {
		id := pathEntries[len(pathEntries)-1].base().ID
		leafID = &id
	}
	projection := BuildSessionProjection(pathEntries, leafID, byID)
	if projection == nil {
		return nil
	}
	projectedEntries := projection.Entries

	// The newest compaction is projected first; older compaction entries
	// can still appear in its retained range with an empty contribution.
	prevCompactionIndex := -1
	for i, entry := range projectedEntries {
		if _, isCompaction := entry.SourceEntry.(*CompactionEntry); isCompaction && len(entry.Messages) > 0 {
			prevCompactionIndex = i
			break
		}
	}

	previousSummary := ""
	boundaryStart := 0
	if prevCompactionIndex >= 0 {
		if compaction, ok := projectedEntries[prevCompactionIndex].SourceEntry.(*CompactionEntry); ok {
			previousSummary = compaction.Summary
		}
		boundaryStart = prevCompactionIndex + 1
	}
	boundaryEnd := len(projectedEntries)

	tokensBefore := EstimateProjectedContextTokens(projection, pathEntries).Tokens
	cutPoint := FindProjectedCutPoint(projectedEntries, boundaryStart, boundaryEnd, settings.KeepRecentTokens)

	if cutPoint.FirstKeptEntryIndex < 0 || cutPoint.FirstKeptEntryIndex >= len(projectedEntries) {
		return nil
	}
	firstKeptEntry := projectedEntries[cutPoint.FirstKeptEntryIndex].SourceEntry
	firstKeptEntryID := firstKeptEntry.base().ID
	if firstKeptEntryID == "" {
		return nil
	}

	historyEnd := cutPoint.FirstKeptEntryIndex
	if cutPoint.IsSplitTurn && cutPoint.TurnStartIndex >= 0 {
		historyEnd = cutPoint.TurnStartIndex
	}

	var messagesToSummarize []agent.AgentMessage
	for _, entry := range sliceProjected(projectedEntries, boundaryStart, historyEnd) {
		messagesToSummarize = append(messagesToSummarize, messagesFromProjectedEntryForCompaction(entry)...)
	}
	var turnPrefixMessages []agent.AgentMessage
	if cutPoint.IsSplitTurn && cutPoint.TurnStartIndex >= 0 {
		for _, entry := range sliceProjected(projectedEntries, cutPoint.TurnStartIndex, cutPoint.FirstKeptEntryIndex) {
			turnPrefixMessages = append(turnPrefixMessages, messagesFromProjectedEntryForCompaction(entry)...)
		}
	}

	if len(messagesToSummarize) == 0 && len(turnPrefixMessages) == 0 {
		return nil
	}

	fileOps := extractFileOperations(messagesToSummarize, pathEntries, prevCompactionIndex)
	for _, message := range turnPrefixMessages {
		ExtractFileOpsFromMessage(message, fileOps)
	}

	return &CompactionPreparation{
		FirstKeptEntryID:    firstKeptEntryID,
		MessagesToSummarize: messagesToSummarize,
		TurnPrefixMessages:  turnPrefixMessages,
		IsSplitTurn:         cutPoint.IsSplitTurn,
		TokensBefore:        int64(tokensBefore),
		PreviousSummary:     previousSummary,
		FileOps:             fileOps,
		Settings:            settings,
	}
}

func sliceProjected(entries []ProjectedSessionEntry, start, end int) []ProjectedSessionEntry {
	if start < 0 {
		start = 0
	}
	if end > len(entries) {
		end = len(entries)
	}
	if start >= end {
		return nil
	}
	return entries[start:end]
}

// flattenProjectedMessages returns the projection's messages in order.
func flattenProjectedMessages(projection *SessionProjection) []interface{} {
	var out []interface{}
	for _, entry := range projection.Entries {
		out = append(out, entry.Messages...)
	}
	return out
}
