// summarization.go: the structured-summary prompts, the retry-wrapped
// summarization choke point, and Compact — ported from
// codingagent/compaction_summarization.go. Transport credentials live in
// the injected SummarizationStreamFn, so the reference's ApiKey/Headers/Env
// plumbing is absent here.
package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gladmo/openagent/abort"
	oa "github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// CompactionSummaryPrefix frames compacted history.
const CompactionSummaryPrefix = `The conversation history before this point was compacted into the following summary:

<summary>
`

// CompactionSummarySuffix closes the summary frame.
const CompactionSummarySuffix = `
</summary>`

// FramedSummary renders a checkpoint summary as the user message a
// continued conversation sees.
func FramedSummary(summary string) string {
	return CompactionSummaryPrefix + summary + CompactionSummarySuffix
}

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

// TurnPrefixSummarizationPromptText is the turn-prefix instruction block
// for conversations where a turn splits across the compaction boundary.
const TurnPrefixSummarizationPromptText = `The messages above are earlier context from an ongoing conversation. Later messages are stored separately and do not need to be reconstructed.

Create a concise checkpoint of the user's request and the progress shown above. This checkpoint will be placed before the later messages so the conversation can continue with the necessary context.

## Original Request
[What did the user ask for?]

## Progress So Far
- [Key decisions and work completed in these messages]

## Context Needed to Continue
- [Information from these messages needed to understand the later work]

Only summarize information explicitly present above. Do not infer or recreate later messages.`

// TurnContextSeparator joins the history and turn-prefix summaries when a
// compaction splits a turn.
const TurnContextSeparator = "\n\n---\n\n**Turn Context (split turn):**\n\n"

// GetSummarizationFailure reports why a summarization response cannot be
// persisted: a length stop carries partial text and must not become a
// session checkpoint.
func GetSummarizationFailure(response *ai.AssistantMessage, label string) string {
	switch response.StopReason {
	case ai.StopError:
		message := "Unknown error"
		if response.ErrorMessage != nil && *response.ErrorMessage != "" {
			message = *response.ErrorMessage
		}
		return fmt.Sprintf("%s failed: %s", label, message)
	case ai.StopLength:
		return fmt.Sprintf("%s failed: generation hit the token cap and the summary is incomplete", label)
	}
	return ""
}

// SummarizationOptions configure one summarization call. Transport-level
// concerns (auth, headers) belong to the SummarizationStreamFn.
type SummarizationOptions struct {
	MaxTokens     int
	ThinkingLevel string
	SessionID     string
	// CacheRetention: one-off summaries avoid cache writes ("none").
	CacheRetention string
}

// SummarizationStreamFn performs one LLM call and returns the assistant
// message (the provider seam).
type SummarizationStreamFn func(ctx context.Context, model *ai.Model, contextMessages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error)

// abortSignalFromContext bridges a context onto an abort signal.
func abortSignalFromContext(ctx context.Context) *abort.Signal {
	if ctx == nil {
		return nil
	}
	controller := abort.NewController()
	go func() {
		<-ctx.Done()
		controller.Abort()
	}()
	return controller.Signal()
}

// CompleteSummarization is the shared choke point for every compaction
// call: it avoids cache writes for one-off summaries, reuses caller
// routing or a fresh session id, and retries through the ai package's
// policy so transient stream drops do not fail the whole compaction on
// the first attempt.
func CompleteSummarization(ctx context.Context, model *ai.Model, contextMessages []ai.Message, options *SummarizationOptions, streamFn SummarizationStreamFn, retry *ai.RetryPolicy) (*ai.AssistantMessage, error) {
	if options == nil {
		options = &SummarizationOptions{}
	}
	requestOptions := *options
	requestOptions.CacheRetention = "none"
	if requestOptions.SessionID == "" {
		requestOptions.SessionID = ai.UUIDv7()
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
			return &ai.AssistantMessage{StopReason: ai.StopError, ErrorMessage: &text}
		}
		callErr = nil
		return message
	}
	response := ai.RetryAssistantCall(produce, retry, abortSignalFromContext(ctx), nil)
	if callErr != nil && response != nil && response.StopReason == ai.StopError {
		return response, callErr
	}
	return response, nil
}

// SummaryResult carries the generated text and its provider usage.
type SummaryResult struct {
	Text  string
	Usage ai.Usage
}

// extractTextBlocks joins the text blocks of an assistant message's
// content. Blocks arrive as VALUE types from providers and decode paths
// (ai.TextContent implements ContentBlock by value); pointer forms are
// accepted defensively.
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

// BuildSummarizationContext builds the standalone summary request (system
// prompt + one user turn holding the wrapped conversation).
func BuildSummarizationContext(promptText string) []ai.Message {
	return []ai.Message{
		&ai.SystemMessage{Content: ai.StringContent(SummarizationSystemPrompt)},
		&ai.UserMessage{Content: ai.StringContent(promptText)},
	}
}

// GenerateSummaryOptions configure one summary generation.
type GenerateSummaryOptions struct {
	// ReserveTokens bounds the summary budget (maxTokens = min(0.8 *
	// reserve, model max)).
	ReserveTokens      int
	CustomInstructions string
	PreviousSummary    string
	ThinkingLevel      string
	SessionID          string
	Retry              *ai.RetryPolicy
	// Signal aborts the summarization call; nil runs unbounded.
	Signal context.Context
}

// GenerateSummary generates or updates a conversation summary (update
// prompt merges into PreviousSummary).
func GenerateSummary(currentMessages []oa.AgentMessage, model *ai.Model, options *GenerateSummaryOptions, streamFn SummarizationStreamFn) (*SummaryResult, error) {
	if options == nil {
		options = &GenerateSummaryOptions{}
	}

	maxTokens := int(float64(options.ReserveTokens) * 0.8)
	if model != nil && model.MaxTokens > 0 && model.MaxTokens < float64(maxTokens) {
		maxTokens = int(model.MaxTokens)
	}

	basePrompt := SummarizationPrompt
	if options.PreviousSummary != "" {
		basePrompt = UpdateSummarizationPrompt
	}
	if options.CustomInstructions != "" {
		basePrompt = basePrompt + "\n\nAdditional focus: " + options.CustomInstructions
	}

	// Serialize the conversation so the model does not continue it.
	conversationText := SerializeConversation(messagesToLlm(currentMessages))

	promptText := "<conversation>\n" + conversationText + "\n</conversation>\n\n"
	if options.PreviousSummary != "" {
		promptText += "<previous-summary>\n" + options.PreviousSummary + "\n</previous-summary>\n\n"
	}
	promptText += basePrompt

	response, err := CompleteSummarization(options.Signal, model, BuildSummarizationContext(promptText),
		&SummarizationOptions{
			MaxTokens:     maxTokens,
			ThinkingLevel: summarizationThinkingLevel(model, options.ThinkingLevel),
			SessionID:     options.SessionID,
		}, streamFn, options.Retry)
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

// summarizationThinkingLevel enables reasoning only for models that
// support it and a non-off level.
func summarizationThinkingLevel(model *ai.Model, thinkingLevel string) string {
	if model != nil && model.Reasoning && thinkingLevel != "" && thinkingLevel != "off" {
		return thinkingLevel
	}
	return ""
}

// GenerateTurnPrefixSummary summarizes the head of a split turn under a
// smaller budget (0.5 * reserve) with the turn-prefix checkpoint format.
func GenerateTurnPrefixSummary(messages []oa.AgentMessage, model *ai.Model, reserveTokens int, options *GenerateSummaryOptions, streamFn SummarizationStreamFn) (*SummaryResult, error) {
	if options == nil {
		options = &GenerateSummaryOptions{}
	}
	maxTokens := int(float64(reserveTokens) * 0.5)
	if model != nil && model.MaxTokens > 0 && model.MaxTokens < float64(maxTokens) {
		maxTokens = int(model.MaxTokens)
	}

	conversationText := SerializeConversation(messagesToLlm(messages))
	promptText := "# Conversation\n" + conversationText + "\n\n# Instructions\n" + TurnPrefixSummarizationPromptText

	response, err := CompleteSummarization(options.Signal, model,
		BuildSummarizationContext(promptText),
		&SummarizationOptions{
			MaxTokens:     maxTokens,
			ThinkingLevel: summarizationThinkingLevel(model, options.ThinkingLevel),
			SessionID:     options.SessionID,
		}, streamFn, options.Retry)
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

// Compact executes a prepared compaction: the history summary, plus the
// turn-prefix summary merged with the split-turn separator when a turn
// splits across the boundary, then the file-operation lists.
func Compact(preparation *Preparation, model *ai.Model, options *GenerateSummaryOptions, streamFn SummarizationStreamFn) (*Outcome, error) {
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

	return &Outcome{
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
