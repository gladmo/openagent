package pico3

// jsonl_replay_test.go pins the replay codec: jsonx-typed carrier fields
// (entry data/model, task checkpoint/outcome, conversation section data)
// must survive the JSONL round trip, patch checkpoint tri-state semantics
// must match live application, and malformed records must error rather
// than panic.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	jsonx "github.com/gladmo/openagent/jsonx"
)

func commitOrFail(t *testing.T, storage Storage, writes ...Write) {
	t.Helper()
	if _, err := storage.Commit(writes); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestJsonlReplayKeepsJsonxCarriers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	storage, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	data := jsonx.NewObj()
	data.Set("text", "hello")
	modelMessage := jsonx.ObjFrom("role", "assistant", "content", "x")
	checkpoint := jsonx.NewObj()
	checkpoint.Set("step", float64(3))
	outcome := jsonx.NewObj()
	outcome.Set("done", true)

	commitOrFail(t, storage,
		NewConversationWrite(&Conversation{ID: 1}),
		NewEntryWrite(&Entry{ID: 2, ConversationID: 1, Kind: "msg", Data: data, Model: []StoredMsg{modelMessage}}),
	)
	running := "running"
	commitOrFail(t, storage, NewTaskWrite(&Task{ID: 7, ConversationID: 1, Kind: "gen", Status: "pending"}))
	commitOrFail(t, storage, NewTaskPatchWrite(&TaskPatch{ID: 7, Status: &running, Checkpoint: checkpoint}))
	commitOrFail(t, storage, NewTaskWrite(&Task{ID: 8, ConversationID: 1, Kind: "job", Status: "running", Checkpoint: checkpoint, Outcome: outcome}))
	storage.Close()

	reopened, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	entries, err := reopened.Entries([]Id{2})
	if err != nil {
		t.Fatal(err)
	}
	entry := entries[2]
	if entry.Data == nil || entry.Data.Len() == 0 || entry.Data.MustGet("text") != "hello" {
		t.Fatalf("entry.Data lost on replay: %v", entry.Data)
	}
	if len(entry.Model) != 1 {
		t.Fatalf("entry.Model lost: %v", entry.Model)
	}
	if _, isObj := entry.Model[0].(*jsonx.Obj); !isObj {
		t.Fatalf("entry.Model element decoded as %T, want *jsonx.Obj", entry.Model[0])
	}

	patched, err := reopened.Task(7)
	if err != nil {
		t.Fatal(err)
	}
	if patched.Checkpoint == nil || patched.Checkpoint.MustGet("step") != float64(3) {
		t.Fatalf("task 7 checkpoint lost on replay: %v", patched.Checkpoint)
	}

	task, err := reopened.Task(8)
	if err != nil {
		t.Fatal(err)
	}
	if task.Checkpoint == nil || task.Checkpoint.MustGet("step") != float64(3) {
		t.Fatalf("task 8 checkpoint lost on replay: %v", task.Checkpoint)
	}
	if task.Outcome == nil || task.Outcome.MustGet("done") != true {
		t.Fatalf("task 8 outcome lost on replay: %v", task.Outcome)
	}
}

func TestJsonlReplayPatchCheckpointTriState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	storage, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := jsonx.NewObj()
	checkpoint.Set("progress", float64(9))
	commitOrFail(t, storage,
		NewConversationWrite(&Conversation{ID: 1}),
		NewTaskWrite(&Task{ID: 5, ConversationID: 1, Kind: "gen", Status: "pending"}),
	)
	// Live-task patches route to the task sidecar; the checkpoint must
	// replay from it.
	setStatus := "running"
	commitOrFail(t, storage, NewTaskPatchWrite(&TaskPatch{ID: 5, Status: &setStatus, Checkpoint: checkpoint}))
	storage.Close()

	reopened, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	live, err := reopened.Task(5)
	if err != nil {
		t.Fatal(err)
	}
	if live.Checkpoint == nil || live.Checkpoint.MustGet("progress") != float64(9) {
		t.Fatalf("checkpoint lost on sidecar replay: %v", live.Checkpoint)
	}

	// A status-only patch (checkpoint key absent) leaves the checkpoint
	// untouched through a round trip.
	waiting := "waiting"
	commitOrFail(t, reopened, NewTaskPatchWrite(&TaskPatch{ID: 5, Status: &waiting}))
	reopened.Close()

	second, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatalf("second reopen: %v", err)
	}
	afterStatusOnly, err := second.Task(5)
	if err != nil {
		t.Fatal(err)
	}
	if afterStatusOnly.Checkpoint == nil || afterStatusOnly.Checkpoint.MustGet("progress") != float64(9) {
		t.Fatalf("status-only patch wiped checkpoint on replay: %v", afterStatusOnly.Checkpoint)
	}

	// An explicit null clear survives the round trip as a clear.
	third := "waiting"
	commitOrFail(t, second, NewTaskPatchWrite(&TaskPatch{ID: 5, Status: &third, HasCheckpointNull: true}))
	second.Close()

	final, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatalf("third reopen: %v", err)
	}
	defer final.Close()
	cleared, err := final.Task(5)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Checkpoint != nil {
		t.Fatalf("explicit null checkpoint clear lost: %v", cleared.Checkpoint)
	}
}

func TestJsonlMalformedPayloadsErrorNotPanic(t *testing.T) {
	cases := []string{
		`{"seq":1,"maxId":1,"writes":[{"type":"doc","ops":[{"p":["z"],"v":1,"o":"s"}]}]}`,
		`{"seq":1,"maxId":1,"writes":[{"type":"task.patch","patch":null}]}`,
		`{"seq":1,"maxId":1,"writes":[{"type":"entry"}]}`,
	}
	for _, line := range cases {
		dir := filepath.Join(t.TempDir(), "session")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("malformed record panicked for %s: %v", line, r)
				}
			}()
			_, err := OpenJsonlStorage(dir, false)
			if err == nil || !strings.Contains(err.Error(), "malformed record") {
				t.Fatalf("err = %v, want malformed record error", err)
			}
		}()
	}
}
