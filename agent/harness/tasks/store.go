package tasks

// store.go: task persistence. FileStore keeps one JSONL record per task
// and replaces the whole file atomically (temp file + rename). Loading
// degrades per line: a corrupt record is reported and skipped, never
// fatal (NFR-3).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// TaskRecord is the persisted state of one task: its spec plus a
// bounded, newest-last run history.
type TaskRecord struct {
	Spec     TaskSpec  `json:"spec"`
	RunCount int       `json:"runCount"`
	History  []TaskRun `json:"history"`
}

// StoreIssue describes one degraded line at load.
type StoreIssue struct {
	Line int
	Err  error
}

// Store persists the full task set. Save replaces the previous content
// atomically; Load returns the records and any per-record degradations.
type Store interface {
	Load() ([]TaskRecord, []StoreIssue)
	Save(records []TaskRecord) error
}

// fileIO is the minimal filesystem seam FileStore needs; tests inject
// an in-memory fake.
type fileIO interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm fs.FileMode) error
	Rename(oldpath, newpath string) error
	MkdirAll(path string, perm fs.FileMode) error
}

type osFS struct{}

func (osFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (osFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(path, data, perm)
}
func (osFS) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }
func (osFS) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

// FileStore is a JSONL-backed Store. One process must own the file:
// the Scheduler serializes all access.
type FileStore struct {
	path string
	fs   fileIO
}

// NewFileStore builds a store over one JSONL file.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path, fs: osFS{}}
}

// Load reads the file line by line. A missing file is an empty store;
// each corrupt line becomes a StoreIssue and is skipped.
func (s *FileStore) Load() ([]TaskRecord, []StoreIssue) {
	content, err := s.fs.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []StoreIssue{{Line: 0, Err: err}}
	}
	lines := bytes.Split(content, []byte("\n"))
	var records []TaskRecord
	var issues []StoreIssue
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record TaskRecord
		if err := json.Unmarshal(line, &record); err != nil {
			issues = append(issues, StoreIssue{Line: i + 1, Err: err})
			continue
		}
		if err := record.Spec.Validate(); err != nil {
			issues = append(issues, StoreIssue{Line: i + 1, Err: err})
			continue
		}
		records = append(records, record)
	}
	return records, issues
}

// Save writes every record as one JSON line through a temp file and
// rename, so a crash mid-write leaves the previous file intact.
func (s *FileStore) Save(records []TaskRecord) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return fmt.Errorf("tasks: encode record %q: %w", record.Spec.ID, err)
		}
	}
	dir := filepath.Dir(s.path)
	if err := s.fs.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tasks: create store dir %s: %w", dir, err)
	}
	tmp := s.path + ".tmp"
	if err := s.fs.WriteFile(tmp, buffer.Bytes(), 0o644); err != nil {
		return fmt.Errorf("tasks: write store temp file: %w", err)
	}
	if err := s.fs.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("tasks: rename store into place: %w", err)
	}
	return nil
}

// MemoryStore is an in-memory Store for tests and ephemeral processes.
type MemoryStore struct {
	mu      sync.Mutex
	records []TaskRecord
}

// NewMemoryStore builds an empty store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

// Load returns a copy of the current records.
func (s *MemoryStore) Load() ([]TaskRecord, []StoreIssue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TaskRecord(nil), s.records...), nil
}

// Save replaces the records.
func (s *MemoryStore) Save(records []TaskRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append([]TaskRecord(nil), records...)
	return nil
}
