package pico3

// Ports of pico3 memory-storage test behaviors.

import (
	"strings"
	"testing"

	chorddelta "github.com/gladmo/openagent/chord/delta"
	"github.com/gladmo/openagent/jsonx"
)

type pico3Alias = *jsonx.Obj

func baseOp() []Op { return replaceObj(nil, "a", float64(1)) }

func setOp(key string, value any) []Op {
	return []Op{&chorddelta.Set{Path: chorddelta.Path{key}, Value: value}}
}

func get(t *testing.T, obj pico3Alias, key string) any {
	t.Helper()
	if obj == nil {
		return nil
	}
	v, ok := obj.Get(key)
	if !ok {
		return nil
	}
	return v
}

func TestCommitAtomicBatch(t *testing.T) {
	s := NewMemoryStorage()
	// A valid conversation + a duplicate id in the same batch -> whole
	// batch rejects.
	if _, err := s.Commit([]Write{
		NewConversationWrite(&Conversation{ID: 1}),
		NewEntryWrite(&Entry{ID: 2, ConversationID: 1, Kind: "msg"}),
		NewConversationWrite(&Conversation{ID: 1}),
	}); err == nil {
		t.Fatal("duplicate accepted")
	}
	// Nothing changed: no conversation, no entry, no seq advance.
	if c, _ := s.Conversation(1); c != nil {
		t.Fatal("conversation leaked")
	}
	if e, _ := s.Task(2); e != nil {
		t.Fatal("entry leaked into tasks")
	}
	entries, _ := s.Entries([]Id{2})
	if len(entries) != 0 {
		t.Fatal("entry leaked")
	}
	if s.seq != 0 {
		t.Fatalf("seq = %d", s.seq)
	}
	// Minted-then-failed ids are reusable: commit the same ids again.
	if _, err := s.Commit([]Write{
		NewConversationWrite(&Conversation{ID: 1}),
		NewEntryWrite(&Entry{ID: 2, ConversationID: 1, Kind: "msg"}),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCommitDuplicateAcrossBatches(t *testing.T) {
	s := NewMemoryStorage()
	if _, err := s.Commit([]Write{NewConversationWrite(&Conversation{ID: 1})}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit([]Write{NewConversationWrite(&Conversation{ID: 1})}); err == nil {
		t.Fatal("cross-batch duplicate accepted")
	}
}

func TestCommitInvalidID(t *testing.T) {
	s := NewMemoryStorage()
	if _, err := s.Commit([]Write{NewEntryWrite(&Entry{ID: 0, ConversationID: 1, Kind: "m"})}); err == nil {
		t.Fatal("zero id accepted")
	}
	if _, err := s.Commit([]Write{NewEntryWrite(&Entry{ID: -3, ConversationID: 1, Kind: "m"})}); err == nil {
		t.Fatal("negative id accepted")
	}
}

func TestTaskPatchSemantics(t *testing.T) {
	s := NewMemoryStorage()
	// Patch a task created in the same batch.
	running := "running"
	terminal := "terminal"
	outcome := jsonx.NewObj()
	outcome.Set("status", "completed")
	outcome.Set("result", float64(42))
	checkpoint := jsonx.NewObj()
	checkpoint.Set("phase", "work")
	if _, err := s.Commit([]Write{
		NewTaskWrite(&Task{ID: 7, ConversationID: 1, Kind: "job", Input: "in", Status: "pending", After: []Id{}, Owns: []Id{}}),
		NewTaskPatchWrite(&TaskPatch{ID: 7, Status: &running, Checkpoint: checkpoint}),
	}); err != nil {
		t.Fatal(err)
	}
	task, _ := s.Task(7)
	if task.Status != "running" || get(t, task.Checkpoint, "phase") != "work" {
		t.Fatalf("task = %+v", task)
	}
	// Patch unknown task rejects.
	if _, err := s.Commit([]Write{NewTaskPatchWrite(&TaskPatch{ID: 99, Status: &running})}); err == nil {
		t.Fatal("unknown task patched")
	}
	// Checkpoint null clears; absent checkpoint field leaves it.
	if _, err := s.Commit([]Write{NewTaskPatchWrite(&TaskPatch{ID: 7, Status: &terminal, Outcome: outcome, HasOutcome: true, HasCheckpointNull: true})}); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(7)
	if task.Status != "terminal" || task.Checkpoint != nil {
		t.Fatalf("task = %+v", task)
	}
	if task.Outcome == nil || get(t, task.Outcome, "status") != "completed" {
		t.Fatalf("outcome = %v", task.Outcome)
	}
}

func TestScanEntriesForkAware(t *testing.T) {
	s := NewMemoryStorage()
	// Parent conversation with entries 2,3; child forked at entry 2 with
	// entry 4.
	if _, err := s.Commit([]Write{
		NewConversationWrite(&Conversation{ID: 1}),
		NewEntryWrite(&Entry{ID: 2, ConversationID: 1, Kind: "msg"}),
		NewEntryWrite(&Entry{ID: 3, ConversationID: 1, Kind: "msg"}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit([]Write{
		NewConversationWrite(&Conversation{ID: 10, Parent: &ConversationRef{ConversationId: 1, At: 2}}),
		NewEntryWrite(&Entry{ID: 4, ConversationID: 10, Kind: "msg"}),
	}); err != nil {
		t.Fatal(err)
	}
	// Child scan: own newest entry 4 first, then parent's entries below the
	// fork point (entry 2 only; 3 is after the fork).
	entries, _ := s.ScanEntries(EntryScan{ConversationID: 10, Limit: 10})
	ids := []Id{}
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	if len(ids) != 2 || ids[0] != 4 || ids[1] != 2 {
		t.Fatalf("ids = %v", ids)
	}
	// Kind filter.
	entries, _ = s.ScanEntries(EntryScan{ConversationID: 10, Limit: 10, Kind: "other"})
	if len(entries) != 0 {
		t.Fatalf("kind filter = %d", len(entries))
	}
	// Limit.
	entries, _ = s.ScanEntries(EntryScan{ConversationID: 10, Limit: 1})
	if len(entries) != 1 || entries[0].ID != 4 {
		t.Fatalf("limit = %+v", entries)
	}
}

func TestSessionDocDefaultAndApply(t *testing.T) {
	s := NewMemoryStorage()
	// Unwritten session doc reads as {plugins:{}}.
	doc, _ := s.Doc(DocRef{Doc: "session"})
	if doc == nil || get(t, doc, "plugins") == nil {
		t.Fatalf("doc = %v", doc)
	}
	// Replace then set.
	if _, err := s.Commit([]Write{NewDocWrite(&DocRef{Doc: "session"}, baseOp())}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit([]Write{NewDocWrite(&DocRef{Doc: "session"}, setOp("b", float64(2)))}); err != nil {
		t.Fatal(err)
	}
	doc, _ = s.Doc(DocRef{Doc: "session"})
	if get(t, doc, "a") != float64(1) || get(t, doc, "b") != float64(2) {
		t.Fatalf("doc = %v", doc)
	}
}

func TestRewindableFoldAndDocAsOf(t *testing.T) {
	s := NewMemoryStorage()
	if _, err := s.Commit([]Write{
		NewConversationWrite(&Conversation{ID: 1}),
		NewEntryWrite(&Entry{ID: 2, ConversationID: 1, Kind: "msg"}),
		NewDocWrite(&DocRef{Doc: "rewindable", ConversationID: 1}, baseOp()),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit([]Write{
		NewEntryWrite(&Entry{ID: 3, ConversationID: 1, Kind: "msg"}),
		NewDocWrite(&DocRef{Doc: "rewindable", ConversationID: 1}, setOp("b", float64(2))),
	}); err != nil {
		t.Fatal(err)
	}
	// Current state folds from the base: a=1, b=2.
	doc, _ := s.Doc(DocRef{Doc: "rewindable", ConversationID: 1})
	if get(t, doc, "a") != float64(1) || get(t, doc, "b") != float64(2) {
		t.Fatalf("doc = %v", doc)
	}
	// As of entry 2 (first commit): only a=1.
	doc, _ = s.DocAsOf(1, 2)
	if doc == nil || get(t, doc, "a") != float64(1) {
		t.Fatalf("asOf = %v", doc)
	}
	if get(t, doc, "b") != nil {
		t.Fatal("future op leaked into asOf")
	}
	// As of a missing entry is nil.
	if doc, _ := s.DocAsOf(1, 99); doc != nil {
		t.Fatal("missing entry asOf")
	}
	// A later base truncates the fold start.
	if _, err := s.Commit([]Write{NewDocWrite(&DocRef{Doc: "rewindable", ConversationID: 1}, replaceObj(t, "c", float64(3)))}); err != nil {
		t.Fatal(err)
	}
	doc, _ = s.Doc(DocRef{Doc: "rewindable", ConversationID: 1})
	if get(t, doc, "c") != float64(3) || get(t, doc, "a") != nil {
		t.Fatalf("after base = %v", doc)
	}
}

func TestStickyTruncate(t *testing.T) {
	s := NewMemoryStorage()
	if _, err := s.Commit([]Write{
		NewConversationWrite(&Conversation{ID: 1}),
		NewDocWrite(&DocRef{Doc: "sticky", ConversationID: 1}, baseOp()),
		NewDocWrite(&DocRef{Doc: "sticky", ConversationID: 1}, setOp("b", float64(2))),
	}); err != nil {
		t.Fatal(err)
	}
	log := s.stickyLog[1]
	if len(log) != 2 {
		t.Fatalf("log = %d", len(log))
	}
	// Truncate keeps from the last base (index 0) onward.
	if err := s.Truncate(DocRef{Doc: "sticky", ConversationID: 1}); err != nil {
		t.Fatal(err)
	}
	if len(s.stickyLog[1]) != 2 {
		t.Fatalf("after truncate = %d", len(s.stickyLog[1]))
	}
	// A new base then truncate drops earlier history.
	if _, err := s.Commit([]Write{NewDocWrite(&DocRef{Doc: "sticky", ConversationID: 1}, replaceObj(t, "z", float64(9)))}); err != nil {
		t.Fatal(err)
	}
	if err := s.Truncate(DocRef{Doc: "sticky", ConversationID: 1}); err != nil {
		t.Fatal(err)
	}
	if len(s.stickyLog[1]) != 1 {
		t.Fatalf("after base truncate = %d", len(s.stickyLog[1]))
	}
	doc, _ := s.Doc(DocRef{Doc: "sticky", ConversationID: 1})
	if get(t, doc, "z") != float64(9) {
		t.Fatalf("doc = %v", doc)
	}
	// Truncate on non-sticky is a no-op.
	if err := s.Truncate(DocRef{Doc: "session"}); err != nil {
		t.Fatal(err)
	}
}

func TestInputByRequest(t *testing.T) {
	s := NewMemoryStorage()
	requestID := "req-1"
	if _, err := s.Commit([]Write{NewInputWrite(&Input{ID: 5, ConversationID: 1, RequestID: &requestID, Status: "queued"})}); err != nil {
		t.Fatal(err)
	}
	found, _ := s.InputByRequest(1, "req-1")
	if found == nil || found.ID != 5 {
		t.Fatalf("found = %+v", found)
	}
	// Different conversation does not match.
	if found, _ := s.InputByRequest(2, "req-1"); found != nil {
		t.Fatal("cross-conversation match")
	}
	// Overwrite the same request id.
	if _, err := s.Commit([]Write{NewInputWrite(&Input{ID: 6, ConversationID: 1, RequestID: &requestID, Status: "placed"})}); err != nil {
		t.Fatal(err)
	}
	found, _ = s.InputByRequest(1, "req-1")
	if found.ID != 6 {
		t.Fatalf("overwrite = %+v", found)
	}
}

func TestClosedStorageRejects(t *testing.T) {
	s := NewMemoryStorage()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit([]Write{NewConversationWrite(&Conversation{ID: 1})}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadsReturnClones(t *testing.T) {
	s := NewMemoryStorage()
	if _, err := s.Commit([]Write{
		NewConversationWrite(&Conversation{ID: 1}),
		NewTaskWrite(&Task{ID: 7, ConversationID: 1, Kind: "job", Input: "in", Status: "pending", After: []Id{}, Owns: []Id{}}),
	}); err != nil {
		t.Fatal(err)
	}
	task, _ := s.Task(7)
	task.Status = "hacked"
	again, _ := s.Task(7)
	if again.Status != "pending" {
		t.Fatal("read result aliased stored state")
	}
}

func replaceObj(_ *testing.T, key string, value float64) []Op {
	obj := jsonx.NewObj()
	obj.Set(key, value)
	return []Op{&chorddelta.Replace{Value: obj}}
}
