// trajectory/jsonl_test.go: file store round trip, torn-tail tolerance, and
// read-side admission rules.
package trajectory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func TestFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.trajectory.jsonl")
	store, err := NewFileStore(path, FileHeader{ID: "s1", CreatedAt: 1727000000000, Model: "m1", Provider: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	records := []Record{
		{Type: KindSessionStart, Turn: 0, Data: jsonx.ObjFrom("sessionId", "s1")},
		{Type: KindTurnStart, Turn: 1, Data: jsonx.ObjFrom("turn", float64(1))},
		{Type: KindToolCall, Turn: 1, Step: 1, Data: jsonx.ObjFrom("callId", "c1", "arguments", `{"a":1}`)},
	}
	for i := range records {
		records[i].Seq = int64(i) + 1
		records[i].TimeMs = float64(1000 + i)
		if err := store.Write(&records[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal("Close not idempotent")
	}

	file, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Header.ID != "s1" || file.Header.Model != "m1" || file.Header.FormatVersion != TrajectoryFormatVersion {
		t.Fatalf("header = %+v", file.Header)
	}
	if len(file.Records) != len(records) {
		t.Fatalf("records = %d", len(file.Records))
	}
	for i := range records {
		if file.Records[i].Type != records[i].Type || file.Records[i].Seq != records[i].Seq {
			t.Fatalf("record %d mismatch: %+v", i+1, file.Records[i])
		}
		if !jsonx.Equal(file.Records[i].Data, records[i].Data) {
			t.Fatalf("record %d data mismatch", i+1)
		}
	}
}

func TestReadFileToleratesTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.trajectory.jsonl")
	store, err := NewFileStore(path, FileHeader{ID: "s1", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{Type: KindSessionStart, Seq: 1, TimeMs: 1, Data: jsonx.NewObj()}
	if err := store.Write(&rec); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// Simulate a crashed writer: a partial line without trailing newline.
	handle, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.WriteString(`{"type":"turn/start","seq":2,"time"`); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !file.TornTail {
		t.Fatal("torn tail not detected")
	}
	if len(file.Records) != 1 {
		t.Fatalf("records = %d", len(file.Records))
	}
}

func TestReadFileRefusesUnknownRequiredKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.trajectory.jsonl")
	store, err := NewFileStore(path, FileHeader{ID: "s1", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	unknown := Record{Type: "plugin/custom", Seq: 1, TimeMs: 1, Data: jsonx.NewObj()}
	if err := store.Write(&unknown); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(path); err == nil {
		t.Fatal("unknown non-ignorable kind accepted on read")
	} else if !strings.Contains(err.Error(), "plugin/custom") {
		t.Fatalf("error does not name the kind: %v", err)
	}

	// The same kind with ignorable=true survives a foreign read.
	path2 := filepath.Join(t.TempDir(), "s2.trajectory.jsonl")
	store2, err := NewFileStore(path2, FileHeader{ID: "s2", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	ignorable := Record{Type: "plugin/custom", Seq: 1, TimeMs: 1, Data: jsonx.NewObj(), Ignorable: true}
	if err := store2.Write(&ignorable); err != nil {
		t.Fatal(err)
	}
	if err := store2.Close(); err != nil {
		t.Fatal(err)
	}
	file2, err := ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}
	if len(file2.Records) != 1 || !file2.Records[0].Ignorable {
		t.Fatalf("ignorable record lost: %+v", file2.Records)
	}
}

func TestReadFileRefusesBrokenSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.trajectory.jsonl")
	store, err := NewFileStore(path, FileHeader{ID: "s1", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	skipped := Record{Type: KindSessionStart, Seq: 5, TimeMs: 1, Data: jsonx.NewObj()}
	if err := store.Write(&skipped); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(path); err == nil {
		t.Fatal("non-dense sequence accepted")
	}
}
