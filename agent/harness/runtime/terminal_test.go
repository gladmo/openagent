package runtime

// Ports of drive/terminal.test.ts behaviors.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func TestOperationResultRecordErrorInvariant(t *testing.T) {
	meta := &session.OperationMeta{OperationID: "op-1", Lane: "main", StartedAt: 5}
	// completed with error -> invariant.
	if _, err := OperationResultRecord(meta, session.StatusCompleted, nil, &session.OperationError{Code: "x"}); err == nil {
		t.Fatal("completed+error accepted")
	}
	// failed without error -> invariant.
	if _, err := OperationResultRecord(meta, session.StatusFailed, nil, nil); err == nil {
		t.Fatal("failed without error accepted")
	}
	// failed with error ok.
	record, err := OperationResultRecord(meta, session.StatusFailed, nil, &session.OperationError{Code: "boom", Message: "m"})
	if err != nil || record.Error == nil || record.Error.Code != "boom" {
		t.Fatalf("record = %+v err = %v", record, err)
	}
	// completed clean ok; kind derives from intent.
	runMeta := &session.OperationMeta{OperationID: "op-2", StartedAt: 5}
	runMeta.Intent = jsonx.ObjFrom("kind", "run")
	sourceTip := "tip-0"
	tip := "tip-9"
	runMeta.SourceTipID = &sourceTip
	record, err = OperationResultRecord(runMeta, session.StatusCompleted, &tip, nil)
	if err != nil || record.Kind != "run" || *record.FromTipID != "tip-0" || *record.TipID != "tip-9" {
		t.Fatalf("record = %+v err = %v", record, err)
	}
	if record.EndedAt <= 0 {
		t.Fatal("endedAt not set")
	}
}

func newTerminalEnv(t *testing.T) session.Session {
	t.Helper()
	storage := session.NewMemoryStorage()
	metadata := session.SessionMetadata{ID: "s1", StorageVersion: 1}
	return session.NewStorageBackedSession(metadata, storage)
}

func TestOperationCleanupWritesDeletesOperationOwnedValues(t *testing.T) {
	sess := newTerminalEnv(t)
	ctx := harnessBackground()
	operationID := "op-1"

	// Seed one value under each operation-owned prefix.
	if err := sess.SetValue(session.OperationToolArgs(operationID, "step-1", 0), jsonx.MustParseString(`{"command":"ls"}`), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.OperationToolMemo(operationID, "inv-1", "read"), "memo", ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.OperationPreparation(operationID, "task-1"), jsonx.MustParseString(`{"kind":"compaction"}`), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.PendingToolOutput(operationID, "inv-1"), "partial", ctx); err != nil {
		t.Fatal(err)
	}
	// Seed op.meta/op.state + an unrelated value that must survive.
	if err := sess.SetValue(session.OperationMetaValue(operationID), jsonx.MustParseString(`{"operationId":"op-1"}`), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.OperationStateValue(operationID), jsonx.MustParseString(`{"at":"starting"}`), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.SessionName, "keep-me", ctx); err != nil {
		t.Fatal(err)
	}

	state := &session.OperationState{At: session.AtStarting}
	writes, err := OperationCleanupWrites(sess, operationID, state, ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Apply the cleanup in one transaction.
	if err := sess.Mutate(func(mutator session.SessionMutator, ctx contextContextAlias) error {
		_, err := mutator.Commit(writes, ctx)
		return err
	}, ctx); err != nil {
		t.Fatal(err)
	}

	// Every operation-owned value is gone.
	for name, address := range map[string]session.Value{
		"toolArgs":    session.OperationToolArgs(operationID, "step-1", 0),
		"toolMemo":    session.OperationToolMemo(operationID, "inv-1", "read"),
		"preparation": session.OperationPreparation(operationID, "task-1"),
		"toolOutput":  session.PendingToolOutput(operationID, "inv-1"),
		"opMeta":      session.OperationMetaValue(operationID),
		"opState":     session.OperationStateValue(operationID),
	} {
		stored, _ := sess.GetValue(address, ctx)
		if stored != nil {
			t.Fatalf("%s survived: %+v", name, stored)
		}
	}
	// Unrelated value survives.
	if stored, _ := sess.GetValue(session.SessionName, ctx); stored == nil || stored.Value != "keep-me" {
		t.Fatal("unrelated value deleted")
	}
}

func TestOperationCleanupWritesToolsPendingEntries(t *testing.T) {
	sess := newTerminalEnv(t)
	ctx := harnessBackground()
	operationID := "op-tools"

	// Outcome-ready call owns a pending entry; planned call does not.
	if err := sess.SetValue(session.PendingEntryValue("ready-1"), jsonx.MustParseString(`{"type":"message","payload":{"role":"user"}}`), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.PendingEntryValue("planned-1"), jsonx.MustParseString(`{"type":"message","payload":{"role":"user"}}`), ctx); err != nil {
		t.Fatal(err)
	}
	batch := jsonx.MustParseString(`{"assistantEntryId":"a1","turnId":"t1","calls":[
		{"sourceIndex":0,"resultEntryId":"ready-1","status":"outcome_ready"},
		{"sourceIndex":1,"resultEntryId":"planned-1","status":"planned"}
	]}`).(*jsonx.Obj)
	state := &session.OperationState{At: session.AtTools, Batch: batch}

	writes, err := OperationCleanupWrites(sess, operationID, state, ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one pending-entry delete: the outcome_ready call's.
	pendingDeletes := 0
	for _, write := range writes {
		if write.Namespace == "pi.pending.entry" {
			pendingDeletes++
		}
	}
	if pendingDeletes != 1 {
		t.Fatalf("pendingDeletes = %d", pendingDeletes)
	}
	// Apply and verify.
	if err := sess.Mutate(func(mutator session.SessionMutator, ctx contextContextAlias) error {
		_, err := mutator.Commit(writes, ctx)
		return err
	}, ctx); err != nil {
		t.Fatal(err)
	}
	if stored, _ := sess.GetValue(session.PendingEntryValue("ready-1"), ctx); stored != nil {
		t.Fatal("outcome_ready pending entry survived")
	}
	if stored, _ := sess.GetValue(session.PendingEntryValue("planned-1"), ctx); stored == nil {
		t.Fatal("planned pending entry deleted")
	}
}

func TestOperationCleanupWritesEffectPendingFrameList(t *testing.T) {
	sess := newTerminalEnv(t)
	ctx := harnessBackground()
	operationID := "op-gen"

	// Seed two frames on the pending assistant list.
	address := session.PendingAssistantFrames(operationID, "resp-1")
	for _, frame := range []string{`{"content":[]}`, `{"content":[{"type":"text","text":"x"}]}`} {
		if err := sess.AppendList(address, jsonx.MustParseString(frame), ctx); err != nil {
			t.Fatal(err)
		}
	}

	state := &session.OperationState{At: session.AtAssistantEffectPending, ResponseEntryID: "resp-1"}
	writes, err := OperationCleanupWrites(sess, operationID, state, ctx)
	if err != nil {
		t.Fatal(err)
	}
	hasListDelete := false
	for _, write := range writes {
		if write.Kind == "list" && write.Namespace == "pi.pending.assistant_frame" {
			hasListDelete = true
		}
	}
	if !hasListDelete {
		t.Fatal("frame list delete missing")
	}
	if err := sess.Mutate(func(mutator session.SessionMutator, ctx contextContextAlias) error {
		_, err := mutator.Commit(writes, ctx)
		return err
	}, ctx); err != nil {
		t.Fatal(err)
	}
	frames, _ := sess.ReadList(address, nil, ctx)
	if len(frames) != 0 {
		t.Fatalf("frames = %d", len(frames))
	}
	_ = strings.TrimSpace
}
