package pico3

// Ports of pico3 jsonl-storage test behaviors.

import (
	"os"

	"path/filepath"
	"strings"
	"testing"

	chorddelta "github.com/gladmo/openagent/chord/delta"

	"github.com/gladmo/openagent/jsonx"
)

func openTempJsonl(t *testing.T) *JsonlStorage {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "session")
	storage, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return storage
}

func TestJsonlCommitAndReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	storage, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Commit([]Write{
		NewConversationWrite(&Conversation{ID: 1}),
		NewEntryWrite(&Entry{ID: 2, ConversationID: 1, Kind: "msg"}),
	}); err != nil {
		t.Fatal(err)
	}
	doc := jsonx.NewObj()
	doc.Set("x", float64(1))
	if _, err := storage.Commit([]Write{
		NewDocWrite(&DocRef{Doc: "sticky", ConversationID: 1}, []Op{&replaceOp{Value: doc}}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	conversation, _ := reopened.Conversation(1)
	if conversation == nil {
		t.Fatal("conversation lost")
	}
	entries, _ := reopened.Entries([]Id{2})
	if len(entries) != 1 {
		t.Fatal("entry lost")
	}
	docValue, _ := reopened.Doc(DocRef{Doc: "sticky", ConversationID: 1})
	if docValue == nil || docValue.MustGet("x") != float64(1) {
		t.Fatalf("doc = %v", docValue)
	}
}

// replaceOp adapts a jsonx obj into a Replace op for tests.
type replaceOp = chorddelta.Replace

func TestJsonlTaskSidecarLifecycle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	storage, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	terminal := "terminal"
	running := "running"
	if _, err := storage.Commit([]Write{
		NewTaskWrite(&Task{ID: 7, ConversationID: 1, Kind: "job", Status: "pending", After: []Id{}, Owns: []Id{}}),
	}); err != nil {
		t.Fatal(err)
	}
	// Non-terminal patches go to the task sidecar.
	if _, err := storage.Commit([]Write{
		NewTaskPatchWrite(&TaskPatch{ID: 7, Status: &running}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "task-7.jsonl")); err != nil {
		t.Fatal("task sidecar missing while live")
	}
	// Terminal patch publishes in main and retires the sidecar.
	outcome := jsonx.NewObj()
	outcome.Set("status", "completed")
	outcome.Set("result", float64(1))
	if _, err := storage.Commit([]Write{
		NewTaskPatchWrite(&TaskPatch{ID: 7, Status: &terminal, Outcome: outcome, HasOutcome: true}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "task-7.jsonl")); !os.IsNotExist(err) {
		t.Fatal("task sidecar survived terminal")
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen: task restored terminal, sidecar not resurrected.
	reopened, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	task, _ := reopened.Task(7)
	if task == nil || task.Status != "terminal" {
		t.Fatalf("task = %+v", task)
	}
}

func TestJsonlTornTailTruncated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	storage, _ := OpenJsonlStorage(dir, false)
	if _, err := storage.Commit([]Write{NewConversationWrite(&Conversation{ID: 1})}); err != nil {
		t.Fatal(err)
	}
	storage.Close()
	// Simulate a torn write: append partial bytes without a newline.
	main := filepath.Join(dir, "main.jsonl")
	f, _ := os.OpenFile(main, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"seq":2,"maxId":1,"writes":[{"type":"conver`)
	f.Close()

	reopened, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	conversation, _ := reopened.Conversation(1)
	if conversation == nil {
		t.Fatal("torn write destroyed committed data")
	}
	// The torn tail was physically removed.
	data, _ := os.ReadFile(main)
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatal("torn tail not truncated")
	}
}

func TestJsonlUnconfirmedSidecarTailIgnored(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	storage, _ := OpenJsonlStorage(dir, false)
	// One confirmed sticky write.
	doc := jsonx.NewObj()
	doc.Set("a", float64(1))
	if _, err := storage.Commit([]Write{
		NewConversationWrite(&Conversation{ID: 1}),
		NewDocWrite(&DocRef{Doc: "sticky", ConversationID: 1}, []Op{&replaceOp{Value: doc}}),
	}); err != nil {
		t.Fatal(err)
	}
	storage.Close()
	// Simulate an unconfirmed sidecar tail: append a record that main
	// never published.
	sidecar := filepath.Join(dir, "sticky-1.jsonl")
	f, _ := os.OpenFile(sidecar, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"seq":9,"maxId":5,"writes":[{"type":"doc","ref":{"doc":"sticky","conversationId":1},"ops":[{"p":["z"],"v":99,"o":"s"}]}]}` + "\n")
	f.Close()

	reopened, err := OpenJsonlStorage(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	docValue, _ := reopened.Doc(DocRef{Doc: "sticky", ConversationID: 1})
	if docValue.MustGet("a") != float64(1) {
		t.Fatalf("confirmed state lost: %v", docValue)
	}
	if docValue.MustGet("z") != nil {
		t.Fatal("unconfirmed sidecar applied")
	}
	// The unconfirmed tail was truncated away.
	data, _ := os.ReadFile(sidecar)
	if strings.Contains(string(data), `"seq":9`) {
		t.Fatal("unconfirmed tail survived")
	}
}

func TestJsonlMalformedRecordRejected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.jsonl"), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJsonlStorage(dir, false); err == nil {
		t.Fatal("malformed record accepted")
	}
}

func TestJsonlSequenceMustIncrease(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line1 := `{"seq":2,"maxId":1,"writes":[{"type":"conversation","conversation":{"id":1}}]}`
	line2 := `{"seq":2,"maxId":1,"writes":[{"type":"conversation","conversation":{"id":2}}]}`
	if err := os.WriteFile(filepath.Join(dir, "main.jsonl"), []byte(line1+"\n"+line2+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJsonlStorage(dir, false); err == nil || !strings.Contains(err.Error(), "not increasing") {
		t.Fatalf("err = %v", err)
	}
}

func TestJsonlCloseOnce(t *testing.T) {
	storage := openTempJsonl(t)
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal("double close errored")
	}
	if _, err := storage.Commit([]Write{NewConversationWrite(&Conversation{ID: 9})}); err == nil {
		t.Fatal("commit after close accepted")
	}
}

type replaceOpReal = chorddeltaReplaceAlias
