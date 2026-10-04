package codingagent

// Tests for crash persistence and extension stack matching (adapted from
// crash-log.test.ts surfaces).

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func crashFilePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "crashes.json")
}

func TestReadCrashLogEmpty(t *testing.T) {
	if got := ReadCrashLog(crashFilePath(t)); len(got) != 0 {
		t.Fatalf("missing = %v", got)
	}

	path := crashFilePath(t)
	os.WriteFile(path, []byte("{broken"), 0o644)
	if got := ReadCrashLog(path); len(got) != 0 {
		t.Fatalf("broken = %v", got)
	}
}

func TestRecordCrash(t *testing.T) {
	path := crashFilePath(t)
	record := RecordCrash("fatal_error", errors.New("boom"), "/session.jsonl", "/cwd", path)
	if record == nil {
		t.Fatal("nil record")
	}
	if record.Message != "boom" || record.Kind != "fatal_error" {
		t.Fatalf("record = %+v", record)
	}
	if record.SessionFile != "/session.jsonl" || record.Cwd != "/cwd" {
		t.Fatalf("record = %+v", record)
	}

	// Persisted.
	records := ReadCrashLog(path)
	if len(records) != 1 || records[0].Message != "boom" {
		t.Fatalf("records = %+v", records)
	}
}

func TestRecordCrashBounded(t *testing.T) {
	path := crashFilePath(t)
	for i := 0; i < maxCrashRecords+3; i++ {
		RecordCrash("fatal_error", errors.New("crash"), "", "", path)
	}
	records := ReadCrashLog(path)
	if len(records) != maxCrashRecords {
		t.Fatalf("records = %d", len(records))
	}
}

func TestTakeUnnotifiedCrash(t *testing.T) {
	path := crashFilePath(t)
	now := time.Now()

	// Old crash: past the age window.
	old := CrashRecord{
		Timestamp: now.Add(-8 * 24 * time.Hour).Format(time.RFC3339),
		Message:   "old",
	}
	recent := CrashRecord{
		Timestamp: now.Add(-1 * time.Hour).Format(time.RFC3339),
		Message:   "recent",
	}
	encoded, _ := json.Marshal([]CrashRecord{old, recent})
	os.WriteFile(path, encoded, 0o644)

	crash := TakeUnnotifiedCrash(path, now.UnixMilli())
	if crash == nil || crash.Message != "recent" {
		t.Fatalf("crash = %+v", crash)
	}

	// All records now marked notified: no second handoff.
	if crash := TakeUnnotifiedCrash(path, now.UnixMilli()); crash != nil {
		t.Fatalf("resurfaced = %+v", crash)
	}
}

func TestTakeUnnotifiedCrashNone(t *testing.T) {
	if crash := TakeUnnotifiedCrash(crashFilePath(t), time.Now().UnixMilli()); crash != nil {
		t.Fatalf("empty = %+v", crash)
	}
}

func TestClearCrashLog(t *testing.T) {
	path := crashFilePath(t)
	RecordCrash("fatal_error", errors.New("x"), "", "", path)
	ClearCrashLog(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("log survived")
	}
}

func TestFindExtensionStackMatches(t *testing.T) {
	extensions := []ExtensionStackMetadata{
		{
			Path:         "/ext/first",
			ResolvedPath: "/ext/first/index.js",
			SourceInfo:   SourceInfo{Origin: OriginTopLevel},
		},
		{
			Path:         "/ext/second.js",
			ResolvedPath: "/ext/second.js",
			SourceInfo:   SourceInfo{Origin: OriginTopLevel},
		},
	}

	// Stack referencing the first extension's directory entry.
	stack := "Error: boom\n    at fn (/ext/first/helper.js:1:1)\n    at main (app.js:2:2)"
	matches := FindExtensionStackMatches(stack, extensions)
	if len(matches) != 1 || matches[0] != "/ext/first" {
		t.Fatalf("matches = %v", matches)
	}

	// Exact-file match for the second.
	stack = "Error: boom\n    at fn (/ext/second.js:1:1)"
	matches = FindExtensionStackMatches(stack, extensions)
	if len(matches) != 1 || matches[0] != "/ext/second.js" {
		t.Fatalf("exact = %v", matches)
	}

	// No match.
	if matches := FindExtensionStackMatches("Error: boom\n    at fn (elsewhere.js:1:1)", extensions); len(matches) != 0 {
		t.Fatalf("none = %v", matches)
	}

	// Empty stack.
	if matches := FindExtensionStackMatches("", extensions); len(matches) != 0 {
		t.Fatalf("empty stack = %v", matches)
	}
}

func TestFindExtensionStackMatchesPackageRoot(t *testing.T) {
	extensions := []ExtensionStackMetadata{
		{
			Path:         "npm:pkg",
			ResolvedPath: "/install/pkg/index.js",
			SourceInfo: SourceInfo{
				Origin:  OriginPackage,
				Source:  "npm:pkg",
				BaseDir: "/install/pkg",
			},
		},
	}
	stack := "Error\n    at fn (/install/pkg/deep/file.js:1:1)"
	matches := FindExtensionStackMatches(stack, extensions)
	if len(matches) != 1 || matches[0] != "npm:pkg" {
		t.Fatalf("package = %v", matches)
	}
}

func TestFindExtensionStackMatchesPrefixDelimiters(t *testing.T) {
	// A path that is a prefix of a longer different path must not match
	// without a delimiter.
	extensions := []ExtensionStackMetadata{
		{Path: "/ext/file.js", ResolvedPath: "/ext/file.js"},
	}
	stack := "Error\n    at fn (/ext/file.js.bak:1:1)"
	if matches := FindExtensionStackMatches(stack, extensions); len(matches) != 0 {
		t.Fatalf("prefix = %v", matches)
	}
	// With a delimiter it matches.
	stack = "Error\n    at fn (/ext/file.js:1:1)"
	if matches := FindExtensionStackMatches(stack, extensions); len(matches) != 1 {
		t.Fatalf("delimited = %v", matches)
	}
}

func TestNormalizeStackPath(t *testing.T) {
	if got := normalizeStackPath(`C:\dir\file.js`); got != "C:/dir/file.js" {
		t.Fatalf("windows = %q", got)
	}
	if got := normalizeStackPath("/trailing/"); got != "/trailing" {
		t.Fatalf("trailing = %q", got)
	}
}
