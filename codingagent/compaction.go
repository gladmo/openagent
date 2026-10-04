package codingagent

// Port of core/compaction/compaction.ts (pure-function core): token
// estimation, usage extraction, cut-point finding over session entries and
// projections, and compaction settings. The LLM-call summarization path
// lands with the runtime streaming phase.

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// CompactionSettings configure auto-compaction.
type CompactionSettings struct {
	Enabled          bool
	ReserveTokens    int
	KeepRecentTokens int
}

// DefaultCompactionSettings mirror the reference defaults.
var DefaultCompactionSettings = CompactionSettings{
	Enabled:          true,
	ReserveTokens:    16384,
	KeepRecentTokens: 20000,
}

// CompactionDetails stores file tracking in CompactionEntry.Details.
type CompactionDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// extractFileOperations merges previous compaction details (when
// pi-generated) with tool calls in the messages.
func extractFileOperations(messages []agent.AgentMessage, entries []SessionEntry, prevCompactionIndex int) *FileOperations {
	fileOps := CreateFileOps()

	if prevCompactionIndex >= 0 && prevCompactionIndex < len(entries) {
		if prevCompaction, ok := entries[prevCompactionIndex].(*CompactionEntry); ok && !prevCompaction.FromHook && len(prevCompaction.Details) > 0 {
			var details CompactionDetails
			if err := json.Unmarshal(prevCompaction.Details, &details); err == nil {
				for _, file := range details.ReadFiles {
					fileOps.Read[file] = true
				}
				for _, file := range details.ModifiedFiles {
					fileOps.Edited[file] = true
				}
			}
		}
	}

	for _, message := range messages {
		ExtractFileOpsFromMessage(message, fileOps)
	}

	return fileOps
}

// ============================================================================
// Token calculation
// ============================================================================

// CalculateContextTokens totals usage (native totalTokens, else components).
func CalculateContextTokens(usage ai.Usage) int {
	if usage.TotalTokens != 0 {
		return int(usage.TotalTokens)
	}
	return int(usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite)
}

// getAssistantUsage skips aborted/error/all-zero messages.
func getAssistantUsage(message agent.AgentMessage) *ai.Usage {
	assistant, ok := message.(*ai.AssistantMessage)
	if !ok {
		return nil
	}
	if assistant.StopReason == "aborted" || assistant.StopReason == "error" {
		return nil
	}
	if CalculateContextTokens(assistant.Usage) > 0 {
		return &assistant.Usage
	}
	return nil
}

// GetLastAssistantUsage scans entries backwards for valid usage.
func GetLastAssistantUsage(entries []SessionEntry) *ai.Usage {
	for i := len(entries) - 1; i >= 0; i-- {
		message, ok := entries[i].(*SessionMessageEntry)
		if !ok {
			continue
		}
		var assistant ai.AssistantMessage
		if err := json.Unmarshal(message.Message, &assistant); err != nil {
			continue
		}
		if usage := getAssistantUsage(&assistant); usage != nil {
			return usage
		}
	}
	return nil
}

// ContextUsageEstimate breaks the context estimate into parts.
type ContextUsageEstimate struct {
	Tokens         int
	UsageTokens    int
	TrailingTokens int
	LastUsageIndex int // -1 when none
}

// EstimateTokens approximates one message's token cost (chars/4).
func EstimateTokens(message agent.AgentMessage) int {
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
		chars += systemSectionChars(typed)
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
	case *CustomMessage:
		if text, ok := typed.Content.(string); ok {
			return estimate(len(text))
		}
		return 0
	case *ai.ToolResultMessage:
		return estimate(contentChars(ai.Content{Blocks: typed.Content}))
	case *BashExecutionMessage:
		return estimate(len(typed.Command) + len(typed.Output))
	case *BranchSummaryMessage:
		return estimate(len(typed.Summary))
	case *CompactionSummaryMessage:
		return estimate(len(typed.Summary))
	case rawAgentMessage:
		// Projected entries carry raw payloads; decode before estimating
		// so cut-point accumulation sees real token counts.
		if decoded, ok := decodeAgentMessage(typed.Raw()).(agent.AgentMessage); ok {
			return EstimateTokens(decoded)
		}
		return 0
	}
	return 0
}

// systemSectionChars sums section text (sections live on the ported
// SystemMessage as raw JSON).
func systemSectionChars(system *ai.SystemMessage) int {
	chars := 0
	for _, section := range system.Sections {
		if section != nil {
			chars += len(*section)
		}
	}
	return chars
}

func getLastAssistantUsageInfo(messages []agent.AgentMessage) (*ai.Usage, int) {
	for i := len(messages) - 1; i >= 0; i-- {
		if usage := getAssistantUsage(messages[i]); usage != nil {
			return usage, i
		}
	}
	return nil, -1
}

// EstimateContextTokens estimates from the last assistant usage plus
// trailing estimates.
func EstimateContextTokens(messages []agent.AgentMessage) ContextUsageEstimate {
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

// ============================================================================
// Cut points
// ============================================================================

func isCutPointMessage(message interface{ Role() string }) bool {
	switch message.Role() {
	case "user", "assistant", "bashExecution", "custom", "branchSummary", "compactionSummary":
		return true
	}
	return false
}

func isTurnStartMessage(message interface{ Role() string }) bool {
	switch message.Role() {
	case "user", "bashExecution", "custom", "branchSummary", "compactionSummary":
		return true
	}
	return false
}

func entryMessagesProduce(entry SessionEntry, predicate func(interface{ Role() string }) bool) bool {
	if _, ok := entry.(*CompactionEntry); ok {
		return false
	}
	for _, message := range sessionEntryToContextMessages(entry) {
		if typed, ok := message.(interface{ Role() string }); ok && predicate(typed) {
			return true
		}
	}
	return false
}

func isTurnStartEntry(entry SessionEntry) bool {
	return entryMessagesProduce(entry, isTurnStartMessage)
}

// FindValidCutPoints lists indices of context-visible user-like or
// assistant messages; never tool results.
func findValidCutPoints(entries []SessionEntry, startIndex, endIndex int) []int {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		if entryMessagesProduce(entries[i], isCutPointMessage) {
			cutPoints = append(cutPoints, i)
		}
	}
	return cutPoints
}

// FindTurnStartIndex scans backwards for the turn's user-role start.
func FindTurnStartIndex(entries []SessionEntry, entryIndex, startIndex int) int {
	for i := entryIndex; i >= startIndex; i-- {
		if isTurnStartEntry(entries[i]) {
			return i
		}
	}
	return -1
}

// CutPointResult describes a compaction cut.
type CutPointResult struct {
	// FirstKeptEntryIndex is the first entry to keep.
	FirstKeptEntryIndex int
	// TurnStartIndex is the user message starting the split turn (-1 when
	// not splitting).
	TurnStartIndex int
	// IsSplitTurn reports a mid-turn cut.
	IsSplitTurn bool
}

// FindCutPoint walks backwards accumulating estimated tokens until the
// keep-recent budget is met, then adjusts to a valid cut point.
func FindCutPoint(entries []SessionEntry, startIndex, endIndex int, keepRecentTokens int) CutPointResult {
	cutPoints := findValidCutPoints(entries, startIndex, endIndex)
	if len(cutPoints) == 0 {
		return CutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1}
	}

	accumulatedTokens := 0
	cutIndex := cutPoints[0]

	for i := endIndex - 1; i >= startIndex; i-- {
		entry := entries[i]
		messageTokens := 0
		for _, message := range decodedEntryMessages(entry) {
			messageTokens += EstimateTokens(message)
		}
		if messageTokens == 0 {
			continue
		}
		accumulatedTokens += messageTokens
		if accumulatedTokens >= keepRecentTokens {
			cutIndex = cutPoints[len(cutPoints)-1]
			for _, candidate := range cutPoints {
				if candidate >= i {
					cutIndex = candidate
					break
				}
			}
			break
		}
	}

	// Include adjacent metadata entries that do not affect context.
	for cutIndex > startIndex {
		prevEntry := entries[cutIndex-1]
		if _, isCompaction := prevEntry.(*CompactionEntry); isCompaction {
			break
		}
		if len(sessionEntryToContextMessages(prevEntry)) > 0 {
			break
		}
		cutIndex--
	}

	cutEntry := entries[cutIndex]
	startsTurn := isTurnStartEntry(cutEntry)
	turnStartIndex := -1
	if !startsTurn {
		turnStartIndex = FindTurnStartIndex(entries, cutIndex, startIndex)
	}
	return CutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		IsSplitTurn:         !startsTurn && turnStartIndex != -1,
	}
}

// ============================================================================
// Projected cut points (extensions)
// ============================================================================

func isProjectedTurnStart(entry ProjectedSessionEntry) bool {
	if _, ok := entry.SourceEntry.(*CompactionEntry); ok {
		return false
	}
	for _, message := range entry.Messages {
		if typed, ok := message.(interface{ Role() string }); ok && isTurnStartMessage(typed) {
			return true
		}
	}
	return false
}

// FindProjectedCutPoint is FindCutPoint over projected entries.
func FindProjectedCutPoint(entries []ProjectedSessionEntry, startIndex, endIndex int, keepRecentTokens int) CutPointResult {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		entry := entries[i]
		if _, isCompaction := entry.SourceEntry.(*CompactionEntry); isCompaction {
			continue
		}
		for _, message := range entry.Messages {
			if typed, ok := message.(interface{ Role() string }); ok && isCutPointMessage(typed) {
				cutPoints = append(cutPoints, i)
				break
			}
		}
	}
	if len(cutPoints) == 0 {
		return CutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1}
	}

	accumulatedTokens := 0
	cutIndex := cutPoints[0]
	for i := endIndex - 1; i >= startIndex; i-- {
		messageTokens := 0
		for _, message := range entries[i].Messages {
			if typed, ok := message.(agent.AgentMessage); ok {
				messageTokens += EstimateTokens(typed)
			}
		}
		if messageTokens == 0 {
			continue
		}
		accumulatedTokens += messageTokens
		if accumulatedTokens >= keepRecentTokens {
			cutIndex = cutPoints[len(cutPoints)-1]
			for _, candidate := range cutPoints {
				if candidate >= i {
					cutIndex = candidate
					break
				}
			}
			break
		}
	}

	// A recovery attempt and its omission edits are context-invisible after
	// the last visible input: advance only for a closed suffix containing an
	// omitted assistant attempt.
	suffix := entries[cutIndex+1 : endIndex]
	isIntrinsicallyVisible := func(entry ProjectedSessionEntry) bool {
		if _, isEdit := entry.SourceEntry.(*ContextEditEntry); isEdit {
			return false
		}
		return len(sessionEntryToContextMessages(entry.SourceEntry)) > 0
	}
	isOmitted := func(entry ProjectedSessionEntry) bool {
		if len(entry.Messages) != 0 {
			return false
		}
		if _, isEdit := entry.SourceEntry.(*ContextEditEntry); !isEdit {
			return false
		}
		messages := sessionEntryToContextMessages(entry.SourceEntry)
		for _, message := range messages {
			if typed, ok := message.(interface{ Role() string }); ok && typed.Role() == "assistant" {
				return true
			}
		}
		return false
	}
	hasOmittedAssistant := false
	closed := true
	for _, entry := range suffix {
		if isIntrinsicallyVisible(entry) {
			closed = false
			break
		}
		if isOmitted(entry) {
			hasOmittedAssistant = true
		}
	}
	if closed && hasOmittedAssistant {
		for cutIndex+1 < endIndex {
			next := entries[cutIndex+1]
			if isIntrinsicallyVisible(next) {
				break
			}
			cutIndex++
		}
	}

	cutEntry := entries[cutIndex]
	startsTurn := isProjectedTurnStart(cutEntry)
	turnStartIndex := -1
	if !startsTurn {
		for i := cutIndex; i >= startIndex; i-- {
			if isProjectedTurnStart(entries[i]) {
				turnStartIndex = i
				break
			}
		}
	}
	return CutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		IsSplitTurn:         !startsTurn && turnStartIndex != -1,
	}
}

var _ = sort.Strings

// decodedEntryMessages materializes an entry's context messages into
// typed shapes for token estimation.
func decodedEntryMessages(entry SessionEntry) []agent.AgentMessage {
	var out []agent.AgentMessage
	for _, message := range sessionEntryToContextMessages(entry) {
		switch typed := message.(type) {
		case rawAgentMessage:
			if decoded := decodeAgentMessage(typed.raw); decoded != nil {
				if asAgent, ok := decoded.(agent.AgentMessage); ok {
					out = append(out, asAgent)
				}
			}
		default:
			if asAgent, ok := message.(agent.AgentMessage); ok {
				out = append(out, asAgent)
			}
		}
	}
	return out
}
