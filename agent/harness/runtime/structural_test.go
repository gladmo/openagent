package runtime

// Ports of structural.ts decision behaviors.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func TestValidateStructuralPreparation(t *testing.T) {
	// Navigation needs messages.
	branchPrep := jsonx.ObjFrom("messages", []any{}, "fileOps", jsonx.ObjFrom(), "totalTokens", float64(1))
	if err := ValidateStructuralPreparation("commit_navigation", branchPrep); err != nil {
		t.Fatal(err)
	}
	compactionPrep := jsonx.ObjFrom("messagesToSummarize", []any{}, "turnPrefixMessages", []any{})
	if err := ValidateStructuralPreparation("commit_navigation", compactionPrep); err == nil || !strings.Contains(err.Error(), "Navigation task") {
		t.Fatalf("err = %v", err)
	}
	// Compaction needs messagesToSummarize.
	if err := ValidateStructuralPreparation("resume_checkpoint", compactionPrep); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStructuralPreparation("resume_checkpoint", branchPrep); err == nil || !strings.Contains(err.Error(), "Compaction task") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyStructuralHook(t *testing.T) {
	if outcome := ApplyStructuralHook(nil); outcome != "ready" {
		t.Fatalf("nil hook = %s", outcome)
	}
	if outcome := ApplyStructuralHook(&StructuralHookDecision{}); outcome != "ready" {
		t.Fatalf("empty hook = %s", outcome)
	}
	if outcome := ApplyStructuralHook(&StructuralHookDecision{Declined: true}); outcome != "declined" {
		t.Fatalf("declined hook = %s", outcome)
	}
	if outcome := ApplyStructuralHook(&StructuralHookDecision{HasSummary: true, Summary: jsonx.ObjFrom("kind", "branch_summary")}); outcome != "branch_summary" {
		t.Fatalf("summary hook = %s", outcome)
	}
}

func TestCompactionReason(t *testing.T) {
	if reason := CompactionReason(jsonx.ObjFrom("reason", "threshold")); reason != "threshold" {
		t.Fatalf("reason = %s", reason)
	}
	// Absent or empty reason -> manual.
	if reason := CompactionReason(jsonx.NewObj()); reason != "manual" {
		t.Fatalf("reason = %s", reason)
	}
	if reason := CompactionReason(jsonx.ObjFrom("reason", "")); reason != "manual" {
		t.Fatalf("reason = %s", reason)
	}
}

func TestStructuralRecordFor(t *testing.T) {
	if status := StructuralRecordFor(StructuralDeclined); status != "declined" {
		t.Fatalf("status = %s", status)
	}
	if status := StructuralRecordFor(StructuralCompleted); status != "completed" {
		t.Fatalf("status = %s", status)
	}
	if status := StructuralRecordFor(StructuralBranchSummary); status != "completed" {
		t.Fatalf("status = %s", status)
	}
}
