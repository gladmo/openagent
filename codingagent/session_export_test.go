package codingagent

// Tests for session JSONL export and the ZIP archive writer (adapted from
// session-export.test.ts and zip.test.ts).

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSerializeSessionBranch(t *testing.T) {
	manager := newTestSessionManager(t, true)
	manager.AppendMessage(map[string]any{"role": "user", "content": "one"})
	manager.AppendMessage(map[string]any{"role": "user", "content": "two"})

	serialized, err := SerializeSessionBranch(manager, nil)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(serialized), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}

	// Header.
	var header map[string]any
	json.Unmarshal([]byte(lines[0]), &header)
	if header["type"] != "session" || header["version"] != float64(CurrentSessionVersion) {
		t.Fatalf("header = %v", header)
	}
	if header["id"] != manager.GetSessionID() {
		t.Fatalf("id = %v", header["id"])
	}

	// Chain: first entry's parentId null, second parents to the first.
	var first, second map[string]any
	json.Unmarshal([]byte(lines[1]), &first)
	json.Unmarshal([]byte(lines[2]), &second)
	if first["parentId"] != nil {
		t.Fatalf("first parent = %v", first["parentId"])
	}
	if second["parentId"] != first["id"] {
		t.Fatalf("second parent = %v want %v", second["parentId"], first["id"])
	}
}

func TestSerializeSessionBranchTrailing(t *testing.T) {
	manager := newTestSessionManager(t, true)
	manager.AppendMessage(map[string]any{"role": "user", "content": "x"})

	serialized, err := SerializeSessionBranch(manager, func(parentID *string, timestamp string) []any {
		if parentID == nil {
			t.Fatal("trailing parent nil")
		}
		if timestamp == "" {
			t.Fatal("trailing timestamp empty")
		}
		return []any{map[string]any{"type": "custom", "customType": "pi.share", "parentId": *parentID}}
	})
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(serialized), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	var trailing map[string]any
	json.Unmarshal([]byte(lines[2]), &trailing)
	if trailing["customType"] != "pi.share" {
		t.Fatalf("trailing = %v", trailing)
	}
}

func TestExportSessionToJsonl(t *testing.T) {
	manager := newTestSessionManager(t, true)
	manager.AppendMessage(map[string]any{"role": "user", "content": "x"})

	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "out.jsonl")
	returned, err := ExportSessionToJsonl(manager, path, nil)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if returned != path {
		t.Fatalf("returned = %q", returned)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatal("missing trailing newline")
	}

	// Default path form.
	wd := t.TempDir()
	t.Chdir(wd)
	returned, err = ExportSessionToJsonl(manager, "", nil)
	if err != nil {
		t.Fatalf("default export: %v", err)
	}
	if !strings.Contains(returned, "session-") || !strings.HasSuffix(returned, ".jsonl") {
		t.Fatalf("default = %q", returned)
	}
}

func TestCreateZipArchive(t *testing.T) {
	entries := []ZipEntry{
		{Name: "report.json", Data: []byte(`{"ok": true}`)},
		{Name: "summary.md", Data: []byte("summary text\n")},
	}
	archive := CreateZipArchive(entries)
	if len(archive) == 0 {
		t.Fatal("empty archive")
	}

	// End-of-central-directory signature.
	if !bytes.Equal(archive[len(archive)-22:len(archive)-18], []byte{0x50, 0x4b, 0x05, 0x06}) {
		t.Fatal("EOCD signature missing")
	}
}

func TestWriteZipArchiveRoundTrip(t *testing.T) {
	entries := []ZipEntry{
		{Name: "a.txt", Data: []byte("content a")},
		{Name: "b.txt", Data: []byte("content b with more bytes to compress")},
	}
	path := filepath.Join(t.TempDir(), "out.zip")
	if err := WriteZipArchive(path, entries); err != nil {
		t.Fatalf("write: %v", err)
	}

	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()
	if len(reader.File) != 2 {
		t.Fatalf("members = %d", len(reader.File))
	}
	byName := map[string]string{}
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("member open: %v", err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		byName[file.Name] = string(data)
	}
	if byName["a.txt"] != "content a" {
		t.Fatalf("a = %q", byName["a.txt"])
	}
	if byName["b.txt"] != "content b with more bytes to compress" {
		t.Fatalf("b = %q", byName["b.txt"])
	}
}

func TestDosDateTime(t *testing.T) {
	date := time.Date(2024, 6, 15, 14, 30, 45, 0, time.UTC)
	dosTime, dosDay := dosDateTime(date)
	// 14<<11 | 30<<5 | 45>>1
	if dosTime != uint16(14<<11|30<<5|45>>1) {
		t.Fatalf("time = %d", dosTime)
	}
	// (2024-1980)<<9 | 6<<5 | 15
	if dosDay != uint16((2024-1980)<<9|6<<5|15) {
		t.Fatalf("day = %d", dosDay)
	}

	// Pre-1980 clamps.
	_, dosDay = dosDateTime(time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC))
	if dosDay != uint16(1<<5|1) {
		t.Fatalf("clamped day = %d", dosDay)
	}
}
