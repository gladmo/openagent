package runtime

// structural.go ports harness/runtime/drive/structural.ts's decision
// core: preparation-kind validation, the navigation/compaction branch
// split, hook-decline routing, and the compaction-reason derivation.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// PreparationKind discriminates the durable structural preparation.
type PreparationKind string

const (
	PreparationCompaction    PreparationKind = "compaction"
	PreparationBranchSummary PreparationKind = "branch_summary"
)

// ValidateStructuralPreparation mirrors the two invariant checks: a
// navigation (commit_navigation) task needs a messages-carrying (branch
// summary) preparation; a compaction task needs a
// messagesToSummarize-carrying preparation.
func ValidateStructuralPreparation(boundaryKind string, preparation *jsonx.Obj) error {
	if boundaryKind == "commit_navigation" {
		if _, ok := preparation.Get("messages"); !ok {
			return &session.SessionInvariantError{Message: "Navigation task has invalid durable preparation"}
		}
		return nil
	}
	if _, ok := preparation.Get("messagesToSummarize"); !ok {
		return &session.SessionInvariantError{Message: "Compaction task has invalid durable preparation"}
	}
	return nil
}

// StructuralHookDecision captures the before_navigation/before_compaction
// hook outcome.
type StructuralHookDecision struct {
	// Declined short-circuits to the declined outcome.
	Declined bool
	// Summary supplies a hook-provided summary (branch summary result).
	Summary    *jsonx.Obj
	HasSummary bool
}

// ApplyStructuralHook mirrors the hook routing: decline -> declined
// outcome; a summary -> branch_summary from hook; otherwise proceed to
// the ready state.
func ApplyStructuralHook(hook *StructuralHookDecision) string {
	if hook != nil && hook.Declined {
		return "declined"
	}
	if hook != nil && hook.HasSummary {
		return "branch_summary"
	}
	return "ready"
}

// CompactionReason mirrors compactionReason: the task's reason when set,
// else manual for a compaction boundary.
func CompactionReason(task *jsonx.Obj) string {
	if reason, ok := task.Get("reason"); ok {
		if s, ok := reason.(string); ok && s != "" {
			return s
		}
	}
	return "manual"
}

// StructuralOutcomeKind enumerates publishStructuralOutcome's inputs.
type StructuralOutcomeKind string

const (
	StructuralDeclined      StructuralOutcomeKind = "declined"
	StructuralCompleted     StructuralOutcomeKind = "completed"
	StructuralBranchSummary StructuralOutcomeKind = "branch_summary"
)

// StructuralRecordFor mirrors the terminal record per outcome kind:
// declined maps to the declined status; everything else completes.
func StructuralRecordFor(kind StructuralOutcomeKind) string {
	if kind == StructuralDeclined {
		return session.StatusDeclined
	}
	return session.StatusCompleted
}
