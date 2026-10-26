package tasks

// store_test.go: JSONL persistence — round trip, atomic replace,
// per-line degradation, missing file, and restart interruption data.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testRecord(id string) TaskRecord {
	cron, _ := ParseScheduleIn("*/5 * * * *", zone)
	return TaskRecord{
		Spec:     TaskSpec{ID: id, Schedule: cron, Prompt: "p", Enabled: true},
		RunCount: 2,
		History: []TaskRun{
			{RunID: "r1", TaskID: id, Status: RunStatusSuccess, FireTime: time.Date(2026, 10, 25, 9, 0, 0, 0, zone)},
		},
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.jsonl")
	store := NewFileStore(path)
	records := []TaskRecord{testRecord("a"), testRecord("b")}
	if err := store.Save(records); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, issues := store.Load()
	if len(issues) != 0 {
		t.Fatalf("issues: %v", issues)
	}
	if len(loaded) != 2 || loaded[0].Spec.ID != "a" || loaded[1].Spec.ID != "b" {
		t.Fatalf("loaded: %+v", loaded)
	}
	if loaded[0].RunCount != 2 || len(loaded[0].History) != 1 {
		t.Fatalf("record content: %+v", loaded[0])
	}
}

func TestFileStoreMissingFileIsEmpty(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "absent.jsonl"))
	records, issues := store.Load()
	if records != nil || issues != nil {
		t.Fatalf("missing file: records=%v issues=%v", records, issues)
	}
}

func TestFileStoreDegradesPerLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.jsonl")
	good, _ := json.Marshal(testRecord("good"))
	content := []byte("not json at all\n" + string(good) + "\n\n" + `{"spec":{"id":"broken","prompt":"p","schedule":{"type":"cron"}}}` + "\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	store := NewFileStore(path)
	records, issues := store.Load()
	if len(records) != 1 || records[0].Spec.ID != "good" {
		t.Fatalf("records: %+v", records)
	}
	if len(issues) != 2 {
		t.Fatalf("issues: %v", issues)
	}
}

// failingFS wraps the OS seam so WriteFile fails after switched on.
type failingFS struct {
	failWrite bool
}

func (f *failingFS) ReadFile(string) ([]byte, error) { return nil, os.ErrNotExist }
func (f *failingFS) WriteFile(string, []byte, os.FileMode) error {
	if f.failWrite {
		return os.ErrPermission
	}
	return nil
}
func (f *failingFS) Rename(string, string) error        { return nil }
func (f *failingFS) MkdirAll(string, os.FileMode) error { return nil }

func TestFileStoreSaveFailureKeepsPreviousContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.jsonl")
	store := NewFileStore(path)
	if err := store.Save([]TaskRecord{testRecord("kept")}); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	before, _ := os.ReadFile(path)

	fs := &failingFS{}
	failing := &FileStore{path: path, fs: fs}
	fs.failWrite = true
	if err := failing.Save([]TaskRecord{testRecord("lost")}); err == nil {
		t.Fatal("failing save returned nil")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("store mutated on failed save:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestFileStoreWritesAtomicallyViaTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.jsonl")
	store := NewFileStore(path)
	if err := store.Save([]TaskRecord{testRecord("v1")}); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	if err := store.Save([]TaskRecord{testRecord("v2")}); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %d entries", len(entries))
	}
	records, issues := store.Load()
	if len(issues) != 0 || len(records) != 1 || records[0].Spec.ID != "v2" {
		t.Fatalf("after replace: %+v issues=%v", records, issues)
	}
}

func TestMemoryStoreRoundTrip(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Save([]TaskRecord{testRecord("m")}); err != nil {
		t.Fatalf("save: %v", err)
	}
	records, issues := store.Load()
	if len(issues) != 0 || len(records) != 1 || records[0].Spec.ID != "m" {
		t.Fatalf("load: %+v", records)
	}
}
