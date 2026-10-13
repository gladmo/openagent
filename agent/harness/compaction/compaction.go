// Package compaction extracts the provider-agnostic compaction core from
// the coding-agent session machinery (codingagent/compaction*.go, porting
// pi core/compaction/compaction.ts) for reuse by non-codingagent
// consumers. Adaptations from the reference, documented here once:
//
//   - The entry model is a linear PathEntry path (the harness session
//     branch shape) instead of codingagent's SessionEntry projection.
//     Context-edit projection logic has no equivalent and is omitted.
//   - Custom coding-agent message roles (bashExecution, custom,
//     branchSummary, compactionSummary) do not exist here; a compaction
//     checkpoint's context contribution materializes as a framed user
//     message, so the cut-point and estimation predicates cover the four
//     ai roles only.
//   - File-operation tracking keeps the reference's read/write/edit tool
//     names and `path` argument; agents with other tool names contribute
//     no file lists.
//   - Branch summarization (an interactive-UI feature) is not ported.
//
// The summarization prompts, the retry-wrapped summary call, the
// split-turn handling, and the cut-point walk are the reference behavior.
package compaction

import (
	"encoding/json"

	oa "github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// Settings configure auto-compaction.
type Settings struct {
	Enabled          bool
	ReserveTokens    int
	KeepRecentTokens int
}

// DefaultSettings mirror the reference defaults.
var DefaultSettings = Settings{
	Enabled:          true,
	ReserveTokens:    16384,
	KeepRecentTokens: 20000,
}

// Details stores file tracking in a compaction entry's details payload.
type Details struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// ShouldCompact reports whether context usage crossed the reserve
// threshold.
func ShouldCompact(contextTokens, contextWindow int, settings Settings) bool {
	if !settings.Enabled {
		return false
	}
	return contextTokens > contextWindow-settings.ReserveTokens
}

// PathEntry is one session entry's contribution on a linear branch path.
// ID is the durable entry id; IsCompaction marks a compaction checkpoint
// whose Messages carry the framed summary plus the retained tail, whose
// Summary holds the raw checkpoint text, and whose Details carries the
// file-operation payload. Entries that contribute no context (display-only
// custom entries) carry empty Messages.
type PathEntry struct {
	ID           string
	IsCompaction bool
	Summary      string
	Details      []byte
	Messages     []oa.AgentMessage
}

// Preparation is the cut-point plan one compaction executes.
type Preparation struct {
	FirstKeptEntryID    string
	MessagesToSummarize []oa.AgentMessage
	TurnPrefixMessages  []oa.AgentMessage
	IsSplitTurn         bool
	TokensBefore        int64
	PreviousSummary     string
	FileOps             *FileOperations
	Settings            Settings
}

// Outcome is the executed compaction.
type Outcome struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int64
	Usage            ai.Usage
	ReadFiles        []string
	ModifiedFiles    []string
}

// isCutPointMessage names the roles a valid cut point may land on; never
// tool results.
func isCutPointMessage(message interface{ Role() string }) bool {
	switch message.Role() {
	case "user", "assistant":
		return true
	}
	return false
}

// isTurnStartMessage names the roles that start a turn.
func isTurnStartMessage(message interface{ Role() string }) bool {
	return message.Role() == "user"
}

// entryProduces reports whether one entry's context messages satisfy the
// predicate; compaction checkpoints replay through their own summary and
// never cut.
func entryProduces(entry PathEntry, predicate func(interface{ Role() string }) bool) bool {
	if entry.IsCompaction {
		return false
	}
	for _, message := range entry.Messages {
		if typed, ok := message.(interface{ Role() string }); ok && predicate(typed) {
			return true
		}
	}
	return false
}

// findValidCutPoints lists indices of context-visible user or assistant
// entries; never tool results.
func findValidCutPoints(entries []PathEntry, startIndex, endIndex int) []int {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		if entryProduces(entries[i], isCutPointMessage) {
			cutPoints = append(cutPoints, i)
		}
	}
	return cutPoints
}

// findTurnStartIndex scans backwards for the turn's user-role start.
func findTurnStartIndex(entries []PathEntry, entryIndex, startIndex int) int {
	for i := entryIndex; i >= startIndex; i-- {
		if entryProduces(entries[i], isTurnStartMessage) {
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
func FindCutPoint(entries []PathEntry, startIndex, endIndex int, keepRecentTokens int) CutPointResult {
	cutPoints := findValidCutPoints(entries, startIndex, endIndex)
	if len(cutPoints) == 0 {
		return CutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1}
	}

	accumulatedTokens := 0
	cutIndex := cutPoints[0]

	for i := endIndex - 1; i >= startIndex; i-- {
		messageTokens := 0
		for _, message := range entries[i].Messages {
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
		if prevEntry.IsCompaction {
			break
		}
		if len(prevEntry.Messages) > 0 {
			break
		}
		cutIndex--
	}

	cutEntry := entries[cutIndex]
	startsTurn := entryProduces(cutEntry, isTurnStartMessage)
	turnStartIndex := -1
	if !startsTurn {
		turnStartIndex = findTurnStartIndex(entries, cutIndex, startIndex)
	}
	return CutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		IsSplitTurn:         !startsTurn && turnStartIndex != -1,
	}
}

// extractFileOperations merges a previous compaction's details with the
// tool calls in the messages.
func extractFileOperations(messages []oa.AgentMessage, entries []PathEntry, prevCompactionIndex int) *FileOperations {
	fileOps := CreateFileOps()

	if prevCompactionIndex >= 0 && prevCompactionIndex < len(entries) {
		if details := entries[prevCompactionIndex].Details; len(details) > 0 {
			var parsed Details
			if err := json.Unmarshal(details, &parsed); err == nil {
				for _, file := range parsed.ReadFiles {
					fileOps.Read[file] = true
				}
				for _, file := range parsed.ModifiedFiles {
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

// messagesForCompaction extracts the summarizable messages from one entry
// (compaction entries replay through their own summary; system messages
// are prompt state).
func messagesForCompaction(entry PathEntry) []oa.AgentMessage {
	if entry.IsCompaction {
		return nil
	}
	var out []oa.AgentMessage
	for _, message := range entry.Messages {
		if asAI, ok := message.(interface{ Role() string }); ok && asAI.Role() == "system" {
			continue
		}
		out = append(out, message)
	}
	return out
}

// PrepareCompaction plans one compaction over a linear path (nil when
// nothing is compressible: the path already ends in a compaction, or no
// message falls before the cut point).
func PrepareCompaction(path []PathEntry, settings Settings) *Preparation {
	if len(path) > 0 && path[len(path)-1].IsCompaction && len(path[len(path)-1].Messages) > 0 {
		return nil
	}

	prevCompactionIndex := -1
	for i, entry := range path {
		if entry.IsCompaction && len(entry.Messages) > 0 {
			prevCompactionIndex = i
			break
		}
	}

	previousSummary := ""
	boundaryStart := 0
	if prevCompactionIndex >= 0 {
		previousSummary = path[prevCompactionIndex].Summary
		boundaryStart = prevCompactionIndex + 1
	}
	boundaryEnd := len(path)

	messages := flattenPathMessages(path)
	tokensBefore := EstimateContextTokens(messages).Tokens
	cutPoint := FindCutPoint(path, boundaryStart, boundaryEnd, settings.KeepRecentTokens)

	if cutPoint.FirstKeptEntryIndex < 0 || cutPoint.FirstKeptEntryIndex >= len(path) {
		return nil
	}
	firstKeptEntryID := path[cutPoint.FirstKeptEntryIndex].ID
	if firstKeptEntryID == "" {
		return nil
	}

	historyEnd := cutPoint.FirstKeptEntryIndex
	if cutPoint.IsSplitTurn && cutPoint.TurnStartIndex >= 0 {
		historyEnd = cutPoint.TurnStartIndex
	}

	var messagesToSummarize []oa.AgentMessage
	for _, entry := range slicePath(path, boundaryStart, historyEnd) {
		messagesToSummarize = append(messagesToSummarize, messagesForCompaction(entry)...)
	}
	var turnPrefixMessages []oa.AgentMessage
	if cutPoint.IsSplitTurn && cutPoint.TurnStartIndex >= 0 {
		for _, entry := range slicePath(path, cutPoint.TurnStartIndex, cutPoint.FirstKeptEntryIndex) {
			turnPrefixMessages = append(turnPrefixMessages, messagesForCompaction(entry)...)
		}
	}

	if len(messagesToSummarize) == 0 && len(turnPrefixMessages) == 0 {
		return nil
	}

	fileOps := extractFileOperations(messagesToSummarize, path, prevCompactionIndex)
	for _, message := range turnPrefixMessages {
		ExtractFileOpsFromMessage(message, fileOps)
	}

	return &Preparation{
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

// slicePath bounds a path slice [start, end).
func slicePath(entries []PathEntry, start, end int) []PathEntry {
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

// flattenPathMessages returns every entry's context messages in order.
func flattenPathMessages(entries []PathEntry) []oa.AgentMessage {
	var out []oa.AgentMessage
	for _, entry := range entries {
		out = append(out, entry.Messages...)
	}
	return out
}
