package runtime

// compaction.go ports the compaction decision core used by the runtime:
// shouldCompact and the threshold preparation over bounded entries
// (prepareCompaction's cut-point search lands with the full compaction
// port; here the trigger decision + invariant checks are faithful).

import (
	session "github.com/gladmo/openagent/agent/harness/session"
)

// ShouldCompact returns whether context usage exceeds the configured
// compaction threshold.
func ShouldCompact(contextTokens, contextWindow float64, settings CompactionSettings) bool {
	if !settings.Enabled {
		return false
	}
	return contextTokens > contextWindow-settings.ReserveTokens
}

// TriggerCompactionResult decides whether a checkpoint must divert into a
// compaction summary. It mirrors prepareCompactionThreshold's guards:
// disabled settings or an unavailable model never compact; a compaction
// entry at or after the trigger never re-compacts; a missing trigger is an
// invariant. The caller supplies the estimated context tokens.
type TriggerCompactionResult struct {
	// Compact is true when the threshold fired.
	Compact bool
	// TokensBefore is the context estimate at the trigger.
	TokensBefore float64
}

// TriggerCompaction evaluates the threshold decision for one checkpoint.
func TriggerCompaction(
	settings CompactionSettings,
	modelAvailable bool,
	contextWindow float64,
	boundedEntries []*session.Entry,
	triggerEntryID string,
	tokensBefore float64,
) (*TriggerCompactionResult, error) {
	if !settings.Enabled || !modelAvailable {
		return &TriggerCompactionResult{}, nil
	}
	triggerIndex := -1
	newestCompactionIndex := -1
	for index, entry := range boundedEntries {
		if entry.ID == triggerEntryID {
			triggerIndex = index
		}
	}
	for index := len(boundedEntries) - 1; index >= 0; index-- {
		if boundedEntries[index].Type == session.EntryTypeCompaction {
			newestCompactionIndex = index
			break
		}
	}
	if newestCompactionIndex >= triggerIndex && newestCompactionIndex != -1 {
		return &TriggerCompactionResult{}, nil
	}
	if triggerIndex == -1 {
		return nil, &session.SessionInvariantError{Message: "Checkpoint trigger " + triggerEntryID + " is missing from its Branch"}
	}
	if !ShouldCompact(tokensBefore, contextWindow, settings) {
		return &TriggerCompactionResult{}, nil
	}
	return &TriggerCompactionResult{Compact: true, TokensBefore: tokensBefore}, nil
}
