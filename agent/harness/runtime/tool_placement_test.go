package runtime

// Ports of drive/tool-placement.ts helper behaviors.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

type placementEnv struct {
	sess session.Session
}

func newPlacementEnv(t *testing.T) *placementEnv {
	t.Helper()
	storage := session.NewMemoryStorage()
	sess := session.NewStorageBackedSession(session.SessionMetadata{ID: "s1", StorageVersion: 1}, storage)
	// Root + assistant entry.
	ctx := harnessBackground()
	assistantID := "assistant-1"
	assistantMessage := jsonx.MustParseString(`{"role":"assistant","content":[
		{"type":"text","text":"hi"},
		{"type":"toolCall","id":"c0","name":"read"},
		{"type":"text","text":"mid"},
		{"type":"toolCall","id":"c3","name":"bash"}
	]}`).(*jsonx.Obj)
	assistantEntry := &session.Entry{
		EntryBase: session.EntryBase{ID: assistantID, Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "assistant", Message: assistantMessage},
	}
	rootEntry := &session.Entry{
		EntryBase: session.EntryBase{ID: "root", Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "user"},
	}
	if err := sess.Mutate(func(mutator session.SessionMutator, ctx sessionCtxAlias) error {
		_, err := mutator.Commit([]session.Write{session.InsertEntry(rootEntry), session.InsertEntry(assistantEntry)}, ctx)
		return err
	}, ctx); err != nil {
		t.Fatal(err)
	}
	return &placementEnv{sess: sess}
}

func placementBatch() *jsonx.Obj {
	return jsonx.ObjFrom(
		"assistantEntryId", "assistant-1",
		"turnId", "t1",
		"calls", []any{
			jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "outcome_ready"),
			jsonx.ObjFrom("sourceIndex", float64(3), "resultEntryId", "r2", "status", "planned"),
		},
	)
}

func TestReadToolBatchSource(t *testing.T) {
	env := newPlacementEnv(t)
	source, err := ReadToolBatchSource(env.sess, placementBatch(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	if source.Assistant == nil || stringOfObj(source.Assistant, "role") != "assistant" {
		t.Fatalf("assistant = %v", source.Assistant)
	}
	if len(source.Calls) != 2 {
		t.Fatalf("calls = %d", len(source.Calls))
	}
	first, ok := source.Calls[1]
	if !ok || stringOfObj(first, "id") != "c0" {
		t.Fatalf("first = %v", first)
	}
	second := source.Calls[3]
	if stringOfObj(second, "name") != "bash" {
		t.Fatalf("second = %v", second)
	}
}

func TestReadToolBatchSourceInvalidEntry(t *testing.T) {
	env := newPlacementEnv(t)
	// Missing assistant entry.
	batch := placementBatch()
	batch.Set("assistantEntryId", "ghost")
	if _, err := ReadToolBatchSource(env.sess, batch, harnessBackground()); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("err = %v", err)
	}
	// A non-assistant message entry.
	userEntry := &session.Entry{
		EntryBase: session.EntryBase{ID: "user-1", Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "user"},
	}
	ctx := harnessBackground()
	if err := env.sess.Mutate(func(mutator session.SessionMutator, ctx sessionCtxAlias) error {
		_, err := mutator.Commit([]session.Write{session.InsertEntry(userEntry)}, ctx)
		return err
	}, ctx); err != nil {
		t.Fatal(err)
	}
	batch.Set("assistantEntryId", "user-1")
	if _, err := ReadToolBatchSource(env.sess, batch, harnessBackground()); err == nil {
		t.Fatal("user entry accepted")
	}
	// A sourceIndex that does not name a toolCall block.
	badBatch := jsonx.ObjFrom(
		"assistantEntryId", "assistant-1",
		"calls", []any{jsonx.ObjFrom("sourceIndex", float64(0), "resultEntryId", "r1", "status", "planned")},
	)
	if _, err := ReadToolBatchSource(env.sess, badBatch, harnessBackground()); err == nil || !strings.Contains(err.Error(), "does not name a tool-call block") {
		t.Fatalf("err = %v", err)
	}
}

func TestToolCallFor(t *testing.T) {
	env := newPlacementEnv(t)
	source, err := ReadToolBatchSource(env.sess, placementBatch(), harnessBackground())
	if err != nil {
		t.Fatal(err)
	}
	block, err := ToolCallFor(source, jsonx.ObjFrom("sourceIndex", float64(1)))
	if err != nil || stringOfObj(block, "name") != "read" {
		t.Fatalf("block = %v err = %v", block, err)
	}
	if _, err := ToolCallFor(source, jsonx.ObjFrom("sourceIndex", float64(9))); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("err = %v", err)
	}
}

func TestWithToolBatch(t *testing.T) {
	scope := &session.OperationState{Control: session.Control{Status: "running"}}
	batch := placementBatch()
	next := WithToolBatch(scope, batch)
	if next.At != session.AtTools || next.Batch != batch {
		t.Fatalf("next = %+v", next)
	}
	if next.Control.Status != "running" {
		t.Fatal("scope lost")
	}
}

func TestExtractPlacementRun(t *testing.T) {
	// First non-completed is outcome_ready; the planned call stops the run.
	run := ExtractPlacementRun(placementBatch())
	if run == nil || len(run.Ready) != 1 || stringOfObj(run.Ready[0], "resultEntryId") != "r1" {
		t.Fatalf("run = %+v", run)
	}
	// All completed: no run.
	allDone := jsonx.ObjFrom(
		"assistantEntryId", "a",
		"calls", []any{jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "completed")},
	)
	if run := ExtractPlacementRun(allDone); run != nil {
		t.Fatalf("run = %+v", run)
	}
	// Consecutive ready calls extend the run; a completed call mid-run
	// stops it (the first non-completed prefix must be contiguous).
	mixed := jsonx.ObjFrom(
		"assistantEntryId", "a",
		"calls", []any{
			jsonx.ObjFrom("sourceIndex", float64(1), "resultEntryId", "r1", "status", "outcome_ready"),
			jsonx.ObjFrom("sourceIndex", float64(3), "resultEntryId", "r2", "status", "outcome_ready"),
			jsonx.ObjFrom("sourceIndex", float64(5), "resultEntryId", "r3", "status", "effect_pending"),
		},
	)
	run = ExtractPlacementRun(mixed)
	if len(run.Ready) != 2 {
		t.Fatalf("ready = %d", len(run.Ready))
	}
}
