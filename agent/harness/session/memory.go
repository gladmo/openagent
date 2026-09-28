package session

// memory.go ports harness/session/memory.ts: MemoryStorage and
// MemorySessionRepo.

import (
	"fmt"
	"sync"
	"time"
)

// MemoryStorage implements Storage over InMemoryStorageState with a
// serialized commit queue.
type MemoryStorage struct {
	mu       sync.Mutex
	state    *InMemoryStorageState
	queue    chan func()
	started  bool
	status   string // open | closing | closed
	closeMtx sync.Once
	closed   chan struct{}
	nowFn    func() float64
}

// NewMemoryStorage builds an open storage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		state:  NewInMemoryStorageState(),
		status: "open",
		closed: make(chan struct{}),
		nowFn:  func() float64 { return float64(time.Now().UnixMilli()) },
	}
}

// enqueue starts the worker on first use and hands it the job. The send
// happens under mu so it can never race Close's close(queue); a closed
// storage reports an error instead of panicking.
func (m *MemoryStorage) enqueue(job func()) error {
	m.mu.Lock()
	if m.status != "open" {
		m.mu.Unlock()
		return fmt.Errorf("MemoryStorage is closed")
	}
	if !m.started {
		m.queue = make(chan func(), 1024)
		m.started = true
		go func(jobs chan func()) {
			for j := range jobs {
				j()
			}
		}(m.queue)
	}
	m.queue <- job
	m.mu.Unlock()
	return nil
}

// Commit serializes one transaction through the commit queue.
func (m *MemoryStorage) Commit(writes []Write, _ Context) (CommitResult, error) {
	done := make(chan struct{})
	var result CommitResult
	var err error
	if enqueueErr := m.enqueue(func() {
		prepared, prepareErr := m.state.PrepareCommit(writes, m.nowFn())
		if prepareErr != nil {
			err = prepareErr
		} else {
			stats := m.state.ApplyValidated(prepared.Writes)
			result = CommitResult{FirstSeq: prepared.FirstSeq, Seqs: prepared.Seqs, Timestamp: prepared.Timestamp, Stats: stats}
		}
		close(done)
	}); enqueueErr != nil {
		return CommitResult{}, enqueueErr
	}
	<-done
	return result, err
}

func (m *MemoryStorage) GetEntries(ids []string, _ Context) (map[string]*Entry, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	return m.state.GetEntries(ids), nil
}

func (m *MemoryStorage) GetValue(address Value, _ Context) (*StoredValue, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	return m.state.GetValue(address)
}

func (m *MemoryStorage) ScanValues(prefix Value, _ Context) ([]StoredValue, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	return m.state.ScanValues(prefix), nil
}

func (m *MemoryStorage) ReadList(address ValueList, options *ListReadOptions, _ Context) ([]ListElement, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	return m.state.ReadList(address, options), nil
}

func (m *MemoryStorage) ScanBranch(query StorageBranchScan, _ Context) ([]*Entry, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	return m.state.ScanBranch(query)
}

func (m *MemoryStorage) ScanBranchStructure(query StorageBranchScan, _ Context) ([]EntryStructure, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	return m.state.ScanBranchStructure(query)
}

func (m *MemoryStorage) ScanEntries(query EntryScan, _ Context) ([]*Entry, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	return m.state.ScanEntries(query), nil
}

func (m *MemoryStorage) ScanUsage(query UsageScan, _ Context) ([]UsageRow, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	return m.state.ScanUsage(query), nil
}

func (m *MemoryStorage) GetStats(Context) (SessionStats, error) {
	if err := m.assertOpen(); err != nil {
		return SessionStats{}, err
	}
	return m.state.GetStats(), nil
}

// Fork constructs a destination storage at one serialized boundary between
// source commits.
func (m *MemoryStorage) Fork(options ForkOptions) (*MemoryStorage, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	var destination *MemoryStorage
	var err error
	if enqueueErr := m.enqueue(func() {
		forkState, forkErr := m.state.CreateFork(options)
		if forkErr != nil {
			err = forkErr
		} else {
			next := NewMemoryStorage()
			next.state = forkState
			destination = next
		}
		close(done)
	}); enqueueErr != nil {
		return nil, enqueueErr
	}
	<-done
	return destination, err
}

// Close drains the queue, stops the worker, and rejects later enqueues.
// The sentinel send and the close(queue) both happen under mu, so no
// in-flight enqueue can send on the closed channel.
func (m *MemoryStorage) Close(Context) error {
	m.closeMtx.Do(func() {
		m.mu.Lock()
		m.status = "closing"
		queue := m.queue
		m.mu.Unlock()
		if queue != nil {
			sentinel := make(chan struct{})
			m.mu.Lock()
			queue <- func() { close(sentinel) }
			m.mu.Unlock()
			<-sentinel
			m.mu.Lock()
			close(queue)
			m.status = "closed"
			m.mu.Unlock()
		} else {
			m.mu.Lock()
			m.status = "closed"
			m.mu.Unlock()
		}
		close(m.closed)
	})
	<-m.closed
	return nil
}

func (m *MemoryStorage) assertOpen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status != "open" {
		return fmt.Errorf("MemoryStorage is closed")
	}
	return nil
}

// MemoryStorageVersion mirrors MEMORY_STORAGE_VERSION.
const MemoryStorageVersion = int64(1)

// MemorySessionRepo implements SessionRepo in memory with exclusive open
// tracking.
type MemorySessionRepo struct {
	mu       sync.Mutex
	records  map[string]*memorySessionRecord
	admitted map[string]bool
}

type memorySessionRecord struct {
	metadata SessionMetadata
	storage  *MemoryStorage
	session  *StorageBackedSession
	open     bool
}

// NewMemorySessionRepo builds an empty repo.
func NewMemorySessionRepo() *MemorySessionRepo {
	return &MemorySessionRepo{
		records:  map[string]*memorySessionRecord{},
		admitted: map[string]bool{},
	}
}

func (r *MemorySessionRepo) Create(options map[string]any, ctx Context) (Session, error) {
	id := ""
	if v, ok := options["id"].(string); ok {
		id = v
	}
	if id == "" {
		id = newSessionID()
	}
	r.mu.Lock()
	if r.admitted[id] {
		r.mu.Unlock()
		return nil, fmt.Errorf("Session %s already open", id)
	}
	r.admitted[id] = true
	r.mu.Unlock()

	release := func() {
		r.mu.Lock()
		delete(r.admitted, id)
		r.mu.Unlock()
	}
	created := float64(time.Now().UnixMilli())
	metadata := SessionMetadata{ID: id, CreatedAt: created, StorageVersion: MemoryStorageVersion}
	storage := NewMemoryStorage()
	record := &memorySessionRecord{metadata: metadata, open: true}
	session := NewStorageBackedSession(metadata, storage, StorageBackedSessionOptions{OnClose: func() {
		release()
		r.mu.Lock()
		record.open = false
		r.mu.Unlock()
	}})
	record.storage = storage
	record.session = session
	// Initialize the main branch at the root.
	if _, err := session.CreateBranch("main", nil, ctx); err != nil {
		_ = session.Close(ctx)
		return nil, err
	}
	r.mu.Lock()
	r.records[id] = record
	r.mu.Unlock()
	return session, nil
}

func (r *MemorySessionRepo) Open(metadata SessionMetadata, ctx Context) (Session, error) {
	r.mu.Lock()
	record, ok := r.records[metadata.ID]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("Unknown session: %s", metadata.ID)
	}
	if record.open {
		r.mu.Unlock()
		return nil, fmt.Errorf("Session %s already open", metadata.ID)
	}
	if r.admitted[metadata.ID] {
		r.mu.Unlock()
		return nil, fmt.Errorf("Session %s already open", metadata.ID)
	}
	r.admitted[metadata.ID] = true
	r.mu.Unlock()
	release := func() {
		r.mu.Lock()
		delete(r.admitted, metadata.ID)
		r.mu.Unlock()
	}
	// Reopen over the recorded materialized state: the previous storage was
	// closed with its session, but the state survives as data and is the
	// repo's record of the session's contents (Fork reads it too).
	storage := NewMemoryStorage()
	storage.state = record.storage.state
	session := NewStorageBackedSession(record.metadata, storage, StorageBackedSessionOptions{OnClose: func() {
		release()
		r.mu.Lock()
		record.open = false
		r.mu.Unlock()
	}})
	r.mu.Lock()
	record.storage = storage
	record.session = session
	record.open = true
	r.mu.Unlock()
	return session, nil
}

func (r *MemorySessionRepo) List(_ any, _ Context) ([]SessionMetadata, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []SessionMetadata{}
	for _, record := range r.records {
		out = append(out, record.metadata)
	}
	return out, nil
}

func (r *MemorySessionRepo) Delete(metadata SessionMetadata, _ Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[metadata.ID]
	if !ok {
		return fmt.Errorf("Unknown session: %s", metadata.ID)
	}
	if record.open {
		return fmt.Errorf("Session %s is open", metadata.ID)
	}
	delete(r.records, metadata.ID)
	return nil
}

func (r *MemorySessionRepo) Fork(source SessionMetadata, options ForkOptions, _ Context) (Session, error) {
	r.mu.Lock()
	record, ok := r.records[source.ID]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("Unknown session: %s", source.ID)
	}
	if record.open {
		r.mu.Unlock()
		return nil, fmt.Errorf("Source session %s is open", source.ID)
	}
	r.mu.Unlock()

	destinationID := newSessionID()
	if options.ID != nil {
		destinationID = *options.ID
	}
	// Build the fork state directly from the record's materialized state:
	// the source session's storage may already be closed (session closed),
	// but the in-memory state remains intact for the repo's lifetime.
	forkState, err := record.storage.state.CreateFork(options)
	if err != nil {
		return nil, err
	}
	destination := NewMemoryStorage()
	destination.state = forkState
	metadata := SessionMetadata{
		ID:              destinationID,
		CreatedAt:       float64(time.Now().UnixMilli()),
		StorageVersion:  MemoryStorageVersion,
		ParentSessionID: &source.ID,
	}
	session := NewStorageBackedSession(metadata, destination)
	r.mu.Lock()
	r.records[destinationID] = &memorySessionRecord{metadata: metadata, storage: destination, session: session, open: true}
	r.mu.Unlock()
	return session, nil

}

func newSessionID() string { return uuidv7Generator{}.Next() }
