package jsonl

// storage.go ports harness/session/jsonl/storage.ts: JsonlStorage over an
// injected FileSystem, including the legacy v3 backing and the
// upgrade-on-first-commit path.

import (
	"fmt"
	"sync"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
)

// jsonlBacking mirrors JsonlBacking.
type jsonlBacking struct {
	kind   string // "v4" | "v3"
	source *LegacyV3Source
}

// JsonlStorage implements session.Storage.
type JsonlStorage struct {
	mu           sync.Mutex
	fileSystem   harness.FileSystem
	path         string
	nowFn        func() float64
	Header       StorageHeader
	backing      jsonlBacking
	storageState *session.InMemoryStorageState
	queue        chan func()
	started      bool
	status       string // open | closing | closed
	closeOnce    sync.Once
	closed       chan struct{}
}

func (s *JsonlStorage) now() float64 { return s.nowFn() }

func newJsonlStorage(fs harness.FileSystem, path string, header StorageHeader, backing jsonlBacking, nowFn func() float64) *JsonlStorage {
	if nowFn == nil {
		nowFn = defaultNow
	}
	return &JsonlStorage{
		fileSystem:   fs,
		path:         path,
		nowFn:        nowFn,
		Header:       header,
		backing:      backing,
		storageState: session.NewInMemoryStorageState(),
		status:       "open",
		closed:       make(chan struct{}),
	}
}

// CreateJsonlStorage mirrors JsonlStorage.create.
func CreateJsonlStorage(fs harness.FileSystem, path string, header StorageHeader, initialWrites []session.Write, ctx harness.Context, nowFn func() float64) (*JsonlStorage, error) {
	storage := newJsonlStorage(fs, path, header, jsonlBacking{kind: "v4"}, nowFn)
	prepared, err := storage.storageState.PrepareCommit(initialWrites, storage.now())
	if err != nil {
		return nil, err
	}
	err = publishJsonl(fs, path, header, ctx, func(appendFn func([]session.CommittedWrite) error) error {
		if len(prepared.Writes) != 0 {
			return appendFn(prepared.Writes)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	storage.storageState.ApplyValidated(prepared.Writes)
	return storage, nil
}

// OpenJsonlStorage mirrors JsonlStorage.open: header line decides v4 vs
// legacy v3.
func OpenJsonlStorage(fs harness.FileSystem, path string, ctx harness.Context, nowFn func() float64) (*JsonlStorage, error) {
	readerResult := fs.OpenTextLineReader(path, ctx)
	if !readerResult.Ok {
		return nil, wrapAction("Failed to read JSONL storage "+path, readerResult.Error)
	}
	reader := readerResult.Value
	parsed, err := ReadHeaderFromReader(reader, path, ctx)
	_ = reader.Close(ctx)
	if err != nil {
		return nil, err
	}
	if parsed.Format == "v3-legacy" {
		return openLegacyV3(fs, path, ctx, nowFn)
	}
	return openV4(fs, path, parsed.Header, ctx, nowFn)
}

func openV4(fs harness.FileSystem, path string, header StorageHeader, ctx harness.Context, nowFn func() float64) (*JsonlStorage, error) {
	contentResult := fs.ReadTextFile(path, ctx)
	if !contentResult.Ok {
		return nil, wrapAction("Failed to read JSONL storage "+path, contentResult.Error)
	}
	lines, torn := SplitCompleteLines(contentResult.Value)
	if header.StorageVersion != JSONLStorageVersion {
		return nil, fmt.Errorf("Session %s uses unsupported storage version %d", header.ID, header.StorageVersion)
	}
	storage := newJsonlStorage(fs, path, header, jsonlBacking{kind: "v4"}, nowFn)
	for index := 1; index < len(lines); index++ {
		writes, err := ParseTransaction(lines[index])
		if err != nil {
			return nil, fmt.Errorf("Invalid JSONL storage %s: line %d", path, index+1)
		}
		storage.replayCommitted(writes)
	}
	if header.NextSeq != nil {
		if err := storage.storageState.AdvanceNextSeq(*header.NextSeq); err != nil {
			return nil, err
		}
	}
	if torn != "" {
		if err := PublishFileAtomically(fs, path, ctx, func(appendFn func(string) error) error {
			return appendFn(joinLines(lines) + "\n")
		}); err != nil {
			return nil, err
		}
	}
	return storage, nil
}

func joinLines(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}

func openLegacyV3(fs harness.FileSystem, path string, ctx harness.Context, nowFn func() float64) (*JsonlStorage, error) {
	source, err := ReadLegacyV3Source(fs, path, ctx)
	if err != nil {
		return nil, err
	}
	header := source.Header
	nextSeq := source.NextSeq
	header.NextSeq = &nextSeq
	storage := newJsonlStorage(fs, path, header, jsonlBacking{kind: "v3", source: source}, nowFn)
	writes, err := source.Writes(ctx)
	if err != nil {
		return nil, err
	}
	for _, write := range writes {
		storage.replayCommitted([]session.CommittedWrite{write})
	}
	return storage, nil
}

func (s *JsonlStorage) replayCommitted(writes []session.CommittedWrite) {
	// Validation mirrors the TS validateCommitted at the current high-water.
	s.storageState.ApplyValidated(writes)
}

// enqueue starts the worker on first use and hands it the job. The send
// happens under mu so it can never race Close's close(queue); a closed
// storage reports an error instead of panicking.
func (s *JsonlStorage) enqueue(job func()) error {
	s.mu.Lock()
	if s.status != "open" {
		s.mu.Unlock()
		return fmt.Errorf("JsonlStorage is closed")
	}
	if !s.started {
		s.queue = make(chan func(), 1024)
		s.started = true
		go func(jobs chan func()) {
			for j := range jobs {
				j()
			}
		}(s.queue)
	}
	s.queue <- job
	s.mu.Unlock()
	return nil
}

// Commit serializes one transaction; append-before-apply.
func (s *JsonlStorage) Commit(writes []session.Write, ctx harness.Context) (session.CommitResult, error) {
	done := make(chan struct{})
	var result session.CommitResult
	var err error
	if enqueueErr := s.enqueue(func() {
		result, err = s.applyCommit(writes, ctx)
		close(done)
	}); enqueueErr != nil {
		return session.CommitResult{}, enqueueErr
	}
	<-done
	return result, err
}

func (s *JsonlStorage) applyCommit(writes []session.Write, ctx harness.Context) (session.CommitResult, error) {
	s.mu.Lock()
	backing := s.backing
	s.mu.Unlock()
	if backing.kind == "v3" && len(writes) != 0 {
		return s.upgradeLegacyV3ToV4(backing.source, writes, ctx)
	}
	prepared, err := s.storageState.PrepareCommit(writes, s.now())
	if err != nil {
		return session.CommitResult{}, err
	}
	if len(prepared.Writes) != 0 {
		if appendResult := s.fileSystem.AppendFile(s.path, []byte(SerializeTransaction(prepared.Writes)+"\n"), ctx); !appendResult.Ok {
			return session.CommitResult{}, wrapAction("Failed to append JSONL storage "+s.path, appendResult.Error)
		}
	}
	stats := s.storageState.ApplyValidated(prepared.Writes)
	return session.CommitResult{FirstSeq: prepared.FirstSeq, Seqs: prepared.Seqs, Timestamp: prepared.Timestamp, Stats: s.withImportedUsage(stats)}, nil
}

// upgradeLegacyV3ToV4 atomically rewrites the file and preserves the first
// caller write as a v4 transaction.
func (s *JsonlStorage) upgradeLegacyV3ToV4(source *LegacyV3Source, callerWrites []session.Write, ctx harness.Context) (session.CommitResult, error) {
	timestamp := s.now()
	importRow := session.UsageRow{
		ID:         ai.UUIDv7(timestamp),
		Usage:      source.ImportedUsage,
		Adjustment: true,
		Details:    map[string]any{"source": "v3-import"},
	}
	allWrites := append([]session.Write{session.InsertUsage(importRow)}, callerWrites...)
	prepared, err := s.storageState.PrepareCommit(allWrites, timestamp)
	if err != nil {
		return session.CommitResult{}, err
	}
	nextSeq := prepared.FirstSeq + int64(len(prepared.Writes))
	upgradedHeader := s.Header
	upgradedHeader.NextSeq = &nextSeq
	err = publishJsonl(s.fileSystem, s.path, upgradedHeader, ctx, func(appendFn func([]session.CommittedWrite) error) error {
		sourceWrites, err := source.Writes(ctx)
		if err != nil {
			return err
		}
		for _, write := range sourceWrites {
			if err := appendFn([]session.CommittedWrite{write}); err != nil {
				return err
			}
		}
		return appendFn(prepared.Writes)
	})
	if err != nil {
		return session.CommitResult{}, err
	}
	stats := s.storageState.ApplyValidated(prepared.Writes)
	s.mu.Lock()
	s.backing = jsonlBacking{kind: "v4"}
	s.mu.Unlock()
	// First sequence belongs to the internal usage adjustment.
	return session.CommitResult{
		FirstSeq:  prepared.FirstSeq + 1,
		Seqs:      prepared.Seqs[1:],
		Timestamp: prepared.Timestamp,
		Stats:     stats,
	}, nil
}

func (s *JsonlStorage) assertOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != "open" {
		return fmt.Errorf("JsonlStorage is closed")
	}
	return nil
}

func (s *JsonlStorage) GetEntries(ids []string, _ harness.Context) (map[string]*session.Entry, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storageState.GetEntries(ids), nil
}

func (s *JsonlStorage) GetValue(address session.Value, _ harness.Context) (*session.StoredValue, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storageState.GetValue(address)
}

func (s *JsonlStorage) ScanValues(prefix session.Value, _ harness.Context) ([]session.StoredValue, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storageState.ScanValues(prefix), nil
}

func (s *JsonlStorage) ReadList(address session.ValueList, options *session.ListReadOptions, _ harness.Context) ([]session.ListElement, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storageState.ReadList(address, options), nil
}

func (s *JsonlStorage) ScanBranch(query session.StorageBranchScan, _ harness.Context) ([]*session.Entry, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storageState.ScanBranch(query)
}

func (s *JsonlStorage) ScanBranchStructure(query session.StorageBranchScan, _ harness.Context) ([]session.EntryStructure, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storageState.ScanBranchStructure(query)
}

func (s *JsonlStorage) ScanEntries(query session.EntryScan, _ harness.Context) ([]*session.Entry, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storageState.ScanEntries(query), nil
}

func (s *JsonlStorage) ScanUsage(query session.UsageScan, _ harness.Context) ([]session.UsageRow, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storageState.ScanUsage(query), nil
}

func (s *JsonlStorage) GetStats(_ harness.Context) (session.SessionStats, error) {
	if err := s.assertOpen(); err != nil {
		return session.SessionStats{}, err
	}
	return s.withImportedUsage(s.storageState.GetStats()), nil
}

func (s *JsonlStorage) withImportedUsage(stats session.SessionStats) session.SessionStats {
	s.mu.Lock()
	backing := s.backing
	s.mu.Unlock()
	if backing.kind == "v4" {
		return stats
	}
	stats.Usage = backing.source.ImportedUsage
	return stats
}

// IsLegacyV3 reports the backing kind.
func (s *JsonlStorage) IsLegacyV3() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backing.kind == "v3"
}

// CaptureForkNextSeq reserves a boundary slot in the queue.
func (s *JsonlStorage) CaptureForkNextSeq(_ harness.Context) (int64, error) {
	if err := s.assertOpen(); err != nil {
		return 0, err
	}
	done := make(chan struct{})
	var next int64
	if enqueueErr := s.enqueue(func() {
		next = s.storageState.GetNextSeq()
		close(done)
	}); enqueueErr != nil {
		return 0, enqueueErr
	}
	<-done
	return next, nil
}

// Close drains the queue, stops the worker, and rejects later enqueues.
// The sentinel send and the close(queue) both happen under mu, so no
// in-flight enqueue can send on the closed channel.
func (s *JsonlStorage) Close(_ harness.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.status = "closing"
		queue := s.queue
		s.mu.Unlock()
		if queue != nil {
			sentinel := make(chan struct{})
			s.mu.Lock()
			queue <- func() { close(sentinel) }
			s.mu.Unlock()
			<-sentinel
			s.mu.Lock()
			close(queue)
			s.status = "closed"
			s.mu.Unlock()
		} else {
			s.mu.Lock()
			s.status = "closed"
			s.mu.Unlock()
		}
		close(s.closed)
	})
	<-s.closed
	return nil
}

func publishJsonl(fs harness.FileSystem, destinationPath string, header StorageHeader, ctx harness.Context, writeTransactions func(appendFn func([]session.CommittedWrite) error) error) error {
	return PublishFileAtomically(fs, destinationPath, ctx, func(appendFn func(string) error) error {
		if err := appendFn(SerializeHeader(header) + "\n"); err != nil {
			return err
		}
		return writeTransactions(func(writes []session.CommittedWrite) error {
			return appendFn(SerializeTransaction(writes) + "\n")
		})
	})
}
