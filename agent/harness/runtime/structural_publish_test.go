package runtime

// Ports of publishStructuralOutcome decision behaviors.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func TestSummaryKindOf(t *testing.T) {
	finishTask := jsonx.ObjFrom("taskId", "t1", "boundary", jsonx.ObjFrom("kind", "finish"))
	if kind := SummaryKindOf(finishTask); kind != "compaction" {
		t.Fatalf("kind = %s", kind)
	}
	navTask := jsonx.ObjFrom("taskId", "t2", "boundary", jsonx.ObjFrom("kind", "commit_navigation"))
	if kind := SummaryKindOf(navTask); kind != "branch_summary" {
		t.Fatalf("kind = %s", kind)
	}
	// Resume boundaries expect nothing.
	resumeTask := jsonx.ObjFrom("taskId", "t3", "boundary", jsonx.ObjFrom("kind", "resume_checkpoint"))
	if kind := SummaryKindOf(resumeTask); kind != "" {
		t.Fatalf("kind = %s", kind)
	}
}

func TestValidateStructuralKind(t *testing.T) {
	// Matching kinds pass.
	if err := ValidateStructuralKind("compaction", "compaction", "t1"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStructuralKind("branch_summary", "branch_summary", "t1"); err != nil {
		t.Fatal(err)
	}
	// Mismatch rejects with the exact message.
	err := ValidateStructuralKind("compaction", "branch_summary", "t9")
	if err == nil || !strings.Contains(err.Error(), "compaction result does not match branch_summary task t9") {
		t.Fatalf("err = %v", err)
	}
	// Declined never mismatches.
	if err := ValidateStructuralKind("declined", "compaction", "t1"); err != nil {
		t.Fatal(err)
	}
}

func TestHookUsageID(t *testing.T) {
	next := func() string { return "usage-1" }
	// Hook compaction with usage -> id reserved.
	outcome := &StructuralOutcomeInput{Kind: "compaction", FromHook: true, Result: jsonx.ObjFrom("usage", jsonx.ObjFrom("input", float64(1)))}
	if id, ok := HookUsageID(outcome, next); !ok || id != "usage-1" {
		t.Fatalf("id = %s ok = %v", id, ok)
	}
	// Non-hook result -> none.
	outcome.FromHook = false
	if _, ok := HookUsageID(outcome, next); ok {
		t.Fatal("non-hook usage reserved")
	}
	// Hook without usage -> none.
	outcome.FromHook = true
	outcome.Result = jsonx.ObjFrom("summary", "s")
	if _, ok := HookUsageID(outcome, next); ok {
		t.Fatal("usage-less hook reserved")
	}
	// Declined -> none.
	if _, ok := HookUsageID(&StructuralOutcomeInput{Kind: "declined"}, next); ok {
		t.Fatal("declined reserved usage")
	}
}

func TestCompactionEntryWrites(t *testing.T) {
	tip := "tip-1"
	result := jsonx.ObjFrom(
		"summary", "the summary",
		"tokensBefore", float64(4200),
		"retainedTail", []any{},
		"details", jsonx.ObjFrom("x", float64(1)),
	)
	writes, entry := CompactionEntryWrites("main", "c-1", &tip, result)
	if len(writes) != 2 {
		t.Fatalf("writes = %d", len(writes))
	}
	if entry.ID != "c-1" || entry.Type != session.EntryTypeCompaction {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.ParentID == nil || *entry.ParentID != "tip-1" {
		t.Fatal("parent not chained")
	}
	if entry.Summary != "the summary" || entry.TokensBefore != 4200 {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Details == nil {
		t.Fatal("details lost")
	}
	if writes[1].Namespace != "pi.branch.tip" || writes[1].Value != "c-1" {
		t.Fatalf("tip write = %+v", writes[1])
	}
}

func TestDeclinedRecord(t *testing.T) {
	tip := "tip-1"
	meta := &session.OperationMeta{OperationID: "op1", Lane: "main", StartedAt: 1}
	meta.Intent = jsonx.ObjFrom("kind", "compaction")
	record, err := DeclinedRecord(meta, &tip)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != session.StatusDeclined || record.Error != nil {
		t.Fatalf("record = %+v", record)
	}
	// Declined cannot carry an error (the xor invariant).
	if _, err := OperationResultRecord(meta, session.StatusDeclined, &tip, &session.OperationError{Code: "x"}); err == nil {
		t.Fatal("declined+error accepted")
	}
}
