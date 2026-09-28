package runtime

// structural_publish.go ports harness/runtime/drive/structural.ts's
// publishStructuralOutcome: the hook-usage accounting, the
// kind-mismatch invariant, and the declined/completed terminal
// publication shared by the structural leaves.

import (
	"fmt"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// StructuralOutcomeInput captures the outcome union.
type StructuralOutcomeInput struct {
	// Kind: "declined" | "compaction" | "branch_summary" | "completed".
	Kind string
	// FromHook marks hook-provided results.
	FromHook bool
	// ResultEntryID is the reserved entry id for compaction/branch results.
	ResultEntryID string
	// Result is the outcome payload (compaction/branch summary object).
	Result *jsonx.Obj
}

// SummaryKindOf mirrors summaryKind: the task boundary names the expected
// result kind — finish -> compaction, commit_navigation -> branch_summary.
func SummaryKindOf(task *jsonx.Obj) string {
	boundaryValue, ok := task.Get("boundary")
	if !ok {
		return ""
	}
	boundary, ok := boundaryValue.(*jsonx.Obj)
	if !ok {
		return ""
	}
	kindValue, _ := boundary.Get("kind")
	kind, _ := kindValue.(string)
	switch kind {
	case "finish":
		return "compaction"
	case "commit_navigation":
		return "branch_summary"
	default:
		return ""
	}
}

// ValidateStructuralKind mirrors the mismatch invariant.
func ValidateStructuralKind(outcomeKind, expectedKind, taskID string) error {
	if (outcomeKind == "compaction" || outcomeKind == "branch_summary") && outcomeKind != expectedKind {
		return &session.SessionInvariantError{Message: fmt.Sprintf("Structural %s result does not match %s task %s", outcomeKind, expectedKind, taskID)}
	}
	return nil
}

// HookUsageID mirrors the hook-usage reservation: only hook-provided
// compaction/branch results carrying usage mint a usage row id.
func HookUsageID(outcome *StructuralOutcomeInput, nextID func() string) (string, bool) {
	if (outcome.Kind == "compaction" || outcome.Kind == "branch_summary") && outcome.FromHook && outcome.Result != nil {
		if usage, ok := outcome.Result.Get("usage"); ok && usage != nil {
			return nextID(), true
		}
	}
	return "", false
}

// CompactionEntryWrites builds the compaction entry + tip writes.
func CompactionEntryWrites(lane, resultEntryID string, tipID *string, result *jsonx.Obj) ([]session.Write, *session.Entry) {
	entry := &session.Entry{
		EntryBase: session.EntryBase{
			ID:       resultEntryID,
			ParentID: tipID,
			Type:     session.EntryTypeCompaction,
		},
	}
	if summary, ok := result.Get("summary"); ok {
		if s, ok := summary.(string); ok {
			entry.Summary = s
		}
	}
	if tokens, ok := result.Get("tokensBefore"); ok {
		if f, ok := tokens.(float64); ok {
			entry.TokensBefore = f
		}
	}
	if details, ok := result.Get("details"); ok && details != nil {
		entry.Details = details
	}
	writes := []session.Write{
		session.InsertEntry(entry),
		session.WriteFromValue(session.SetValue(session.BranchTip(lane), resultEntryID)),
	}
	return writes, entry
}

// DeclinedRecord builds the declined terminal record.
func DeclinedRecord(meta *session.OperationMeta, tipID *string) (*session.OperationResultRecord, error) {
	return OperationResultRecord(meta, session.StatusDeclined, tipID, nil)
}
