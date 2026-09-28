package session

// session.go ports harness/session/session.ts: StorageBackedSession and
// StorageBackedBranch.

import (
	"fmt"
	"strings"
	"sync"

	"github.com/gladmo/openagent/ai"
)

// Session error types.
type SessionInvariantError struct{ Message string }

func (e *SessionInvariantError) Error() string { return e.Message }

type SessionInvalidBranchError struct {
	Branch string
	Reason string
}

func (e *SessionInvalidBranchError) Error() string {
	return fmt.Sprintf("Invalid branch %q: %s", e.Branch, e.Reason)
}

type SessionBranchExistsError struct{ Branch string }

func (e *SessionBranchExistsError) Error() string { return "Branch already exists: " + e.Branch }

type SessionPendingAssistantMessageError struct{}

func (e *SessionPendingAssistantMessageError) Error() string {
	return "Cannot persist a pending assistant message"
}

type SessionUnknownTargetError struct{ TargetID string }

func (e *SessionUnknownTargetError) Error() string { return "Unknown target: " + e.TargetID }

// StorageBackedSessionOptions mirrors the TS interface.
type StorageBackedSessionOptions struct {
	MutationLine *MutationLine
	IDGenerator  IdGenerator
	OnClose      func()
}

// storageBackedSessionMutation implements SessionMutation.
type storageBackedSessionMutation struct {
	storage      Storage
	release      func()
	active       bool
	commitDone   chan struct{}
	commitResult CommitResult
	commitErr    error
	committed    bool
}

func (m *storageBackedSessionMutation) Commit(writes []Write, ctx Context) (CommitResult, error) {
	if !m.active {
		return CommitResult{}, fmt.Errorf("SessionMutator cannot be used outside its mutation callback")
	}
	if m.committed {
		return CommitResult{}, fmt.Errorf("SessionMutator commit already attempted")
	}
	m.committed = true
	for _, write := range writes {
		if write.Kind == "entry" && write.Entry != nil && write.Entry.Type == EntryTypeMessage &&
			write.Entry.Message.Role == "assistant" {
			if msg := write.Entry.Message.Message; msg != nil {
				if stop, ok := msg.Get("stopReason"); ok && stop == ai.StopPending {
					m.commitErr = &SessionPendingAssistantMessageError{}
					return CommitResult{}, m.commitErr
				}
			}
		}
	}
	m.commitResult, m.commitErr = m.storage.Commit(writes, ctx)
	return m.commitResult, m.commitErr
}

func (m *storageBackedSessionMutation) End(Context) error {
	if !m.active {
		return nil
	}
	m.active = false
	m.release()
	return nil
}

func (m *storageBackedSessionMutation) GetEntries(ids []string, ctx Context) (map[string]*Entry, error) {
	if !m.active {
		return nil, fmt.Errorf("SessionMutator cannot be used outside its mutation callback")
	}
	return m.storage.GetEntries(ids, ctx)
}
func (m *storageBackedSessionMutation) GetStats(ctx Context) (SessionStats, error) {
	if !m.active {
		return SessionStats{}, fmt.Errorf("SessionMutator cannot be used outside its mutation callback")
	}
	return m.storage.GetStats(ctx)
}
func (m *storageBackedSessionMutation) GetValue(address Value, ctx Context) (*StoredValue, error) {
	if !m.active {
		return nil, fmt.Errorf("SessionMutator cannot be used outside its mutation callback")
	}
	return m.storage.GetValue(address, ctx)
}
func (m *storageBackedSessionMutation) ScanValues(prefix Value, ctx Context) ([]StoredValue, error) {
	if !m.active {
		return nil, fmt.Errorf("SessionMutator cannot be used outside its mutation callback")
	}
	return m.storage.ScanValues(prefix, ctx)
}
func (m *storageBackedSessionMutation) ReadList(address ValueList, options *ListReadOptions, ctx Context) ([]ListElement, error) {
	if !m.active {
		return nil, fmt.Errorf("SessionMutator cannot be used outside its mutation callback")
	}
	return m.storage.ReadList(address, options, ctx)
}
func (m *storageBackedSessionMutation) ScanBranch(query StorageBranchScan, ctx Context) ([]*Entry, error) {
	if !m.active {
		return nil, fmt.Errorf("SessionMutator cannot be used outside its mutation callback")
	}
	return m.storage.ScanBranch(query, ctx)
}

// StorageBackedBranch implements Branch.
type StorageBackedBranch struct {
	name    string
	session *StorageBackedSession
}

// Name returns the branch name.
func (b *StorageBackedBranch) Name() string { return b.name }

// GetTipID returns the branch tip id (nil for root).
func (b *StorageBackedBranch) GetTipID(ctx Context) (*string, error) {
	return b.session.GetBranchTip(b.name, ctx)
}

// FindEntries scans the branch ancestry.
func (b *StorageBackedBranch) FindEntries(query *BranchScan, ctx Context) ([]*Entry, error) {
	scan := BranchScan{Order: "newestFirst"}
	if query != nil {
		scan = *query
		if scan.Order == "" {
			scan.Order = "newestFirst"
		}
	}
	if scan.Start == nil {
		tip, err := b.GetTipID(ctx)
		if err != nil {
			return nil, err
		}
		if tip == nil {
			return nil, nil
		}
		scan.Start = tip
	}
	return b.session.ScanBranch(StorageBranchScan{BranchScan: scan, StartID: *scan.Start}, ctx)
}

// FindEntry returns the first match.
func (b *StorageBackedBranch) FindEntry(query *BranchScan, ctx Context) (*Entry, error) {
	scan := BranchScan{}
	if query != nil {
		scan = *query
	}
	one := int64(1)
	scan.Limit = &one
	entries, err := b.FindEntries(&scan, ctx)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	return entries[0], nil
}

// AppendMessage appends a message entry at the branch tip.
func (b *StorageBackedBranch) AppendMessage(message AgentMessagePayload, ctx Context) (string, error) {
	return b.session.AppendToBranch(b.name, &Entry{
		EntryBase: EntryBase{Type: EntryTypeMessage},
		Message:   message,
	}, ctx)
}

// AppendCustomEntry appends a custom entry at the branch tip.
func (b *StorageBackedBranch) AppendCustomEntry(customType string, data JsonValue, ctx Context) (string, error) {
	return b.session.AppendToBranch(b.name, &Entry{
		EntryBase: EntryBase{Type: EntryTypeCustom, CustomType: &customType},
		Data:      data,
	}, ctx)
}

// StorageBackedSession implements Session over one Storage.
type StorageBackedSession struct {
	metadata     SessionMetadata
	idGenerator  IdGenerator
	storage      Storage
	mutationLine *MutationLine
	onClose      func()

	mu        sync.Mutex
	branches  map[string]*StorageBackedBranch
	state     string // open | closing | closed
	closeOnce sync.Once
	closed    chan struct{}
}

// NewStorageBackedSession builds a session.
func NewStorageBackedSession(metadata SessionMetadata, storage Storage, options ...StorageBackedSessionOptions) *StorageBackedSession {
	opts := StorageBackedSessionOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	line := opts.MutationLine
	if line == nil {
		line = &MutationLine{}
	}
	gen := opts.IDGenerator
	if gen == nil {
		gen = uuidv7Generator{}
	}
	return &StorageBackedSession{
		metadata:     metadata,
		idGenerator:  gen,
		storage:      storage,
		mutationLine: line,
		onClose:      opts.OnClose,
		branches:     map[string]*StorageBackedBranch{},
		state:        "open",
		closed:       make(chan struct{}),
	}
}

type uuidv7Generator struct{}

func (uuidv7Generator) Next(timestampMs ...float64) string {
	return ai.UUIDv7(timestampMs...)
}

// Metadata returns the session metadata.
func (s *StorageBackedSession) Metadata() SessionMetadata { return s.metadata }

// IDGenerator returns the id generator.
func (s *StorageBackedSession) IDGenerator() IdGenerator { return s.idGenerator }

func (s *StorageBackedSession) assertOpen() error {
	if s.state != "open" {
		return fmt.Errorf("Session is closed")
	}
	return nil
}

// BeginMutation acquires the session's mutation line.
func (s *StorageBackedSession) BeginMutation(ctx Context) (SessionMutation, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	granted := make(chan SessionMutation, 1)
	released := make(chan struct{})
	// The line job grants the mutation and waits for release; BeginMutation
	// must not wait on the Run outcome channel (it only settles after
	// release), only on the grant itself.
	s.mutationLine.Run(func() error {
		mutation := &storageBackedSessionMutation{
			storage: s.storage,
			release: func() { close(released) },
			active:  true,
		}
		granted <- mutation
		<-released
		return nil
	})
	return <-granted, nil
}

// Mutate runs one exclusive callback over the mutation line.
func (s *StorageBackedSession) Mutate(mutation func(mutator SessionMutator, ctx Context) error, ctx Context) error {
	mutator, err := s.BeginMutation(ctx)
	if err != nil {
		return err
	}
	defer mutator.End(ctx)
	return mutation(mutator, ctx)
}

func (s *StorageBackedSession) GetEntries(ids []string, ctx Context) (map[string]*Entry, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storage.GetEntries(ids, ctx)
}

func (s *StorageBackedSession) GetEntry(id string, ctx Context) (*Entry, error) {
	entries, err := s.GetEntries([]string{id}, ctx)
	if err != nil {
		return nil, err
	}
	return entries[id], nil
}

func (s *StorageBackedSession) GetValue(address Value, ctx Context) (*StoredValue, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storage.GetValue(address, ctx)
}

func (s *StorageBackedSession) ScanValues(prefix Value, ctx Context) ([]StoredValue, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storage.ScanValues(prefix, ctx)
}

func (s *StorageBackedSession) ReadList(address ValueList, options *ListReadOptions, ctx Context) ([]ListElement, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storage.ReadList(address, options, ctx)
}

func (s *StorageBackedSession) ScanBranch(query StorageBranchScan, ctx Context) ([]*Entry, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storage.ScanBranch(query, ctx)
}

func (s *StorageBackedSession) GetStats(ctx Context) (SessionStats, error) {
	if err := s.assertOpen(); err != nil {
		return SessionStats{}, err
	}
	return s.storage.GetStats(ctx)
}

func (s *StorageBackedSession) GetName(ctx Context) (*string, error) {
	stored, err := s.GetValue(SessionName, ctx)
	if err != nil || stored == nil {
		return nil, err
	}
	if str, ok := stored.Value.(string); ok {
		return &str, nil
	}
	return nil, nil
}

func (s *StorageBackedSession) GetLabel(targetID string, ctx Context) (*string, error) {
	stored, err := s.GetValue(EntryLabel(targetID), ctx)
	if err != nil || stored == nil {
		return nil, err
	}
	if str, ok := stored.Value.(string); ok {
		return &str, nil
	}
	return nil, nil
}

func (s *StorageBackedSession) FindEntries(query *EntryQuery, ctx Context) ([]*Entry, error) {
	q := EntryQuery{}
	if query != nil {
		q = *query
	}
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	order := q.Order
	if order == "" {
		order = "desc"
	}
	scan := EntryScan{Type: q.Type, CustomType: q.CustomType, Order: order, Limit: q.Limit}
	if q.Cursor != nil {
		if order == "asc" && q.Cursor.Seq >= 9007199254740991 {
			return nil, nil
		}
		if order == "desc" && q.Cursor.Seq <= 1 {
			return nil, nil
		}
		if order == "asc" {
			from := q.Cursor.Seq + 1
			scan.FromSeq = &from
		} else {
			to := q.Cursor.Seq - 1
			scan.ToSeq = &to
		}
	}
	return s.storage.ScanEntries(scan, ctx)
}

func (s *StorageBackedSession) FindEntry(query *EntryQuery, ctx Context) (*Entry, error) {
	q := EntryQuery{}
	if query != nil {
		q = *query
	}
	one := int64(1)
	q.Limit = &one
	entries, err := s.FindEntries(&q, ctx)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	return entries[0], nil
}

func (s *StorageBackedSession) Branch(name string, ctx Context) (Branch, error) {
	if err := s.assertValidBranchName(name); err != nil {
		return nil, err
	}
	stored, err := s.GetValue(BranchTip(name), ctx)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, nil
	}
	return s.getOrCreateBranch(name), nil
}

func (s *StorageBackedSession) CreateBranch(name string, at *string, ctx Context) (Branch, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	if err := s.assertValidBranchName(name); err != nil {
		return nil, err
	}
	err := s.Mutate(func(mutator SessionMutator, ctx Context) error {
		stored, err := mutator.GetValue(BranchTip(name), ctx)
		if err != nil {
			return err
		}
		if stored != nil {
			return &SessionBranchExistsError{Branch: name}
		}
		if at != nil {
			entries, err := mutator.GetEntries([]string{*at}, ctx)
			if err != nil {
				return err
			}
			if _, exists := entries[*at]; !exists {
				return &SessionUnknownTargetError{TargetID: *at}
			}
		}
		_, err = mutator.Commit([]Write{WriteFromValue(SetValue(BranchTip(name), stringValue(at)))}, ctx)
		return err
	}, ctx)
	if err != nil {
		return nil, err
	}
	return s.getOrCreateBranch(name), nil
}

func stringValue(p *string) JsonValue {
	if p == nil {
		return nil
	}
	return *p
}

func (s *StorageBackedSession) SetValue(address Value, next JsonValue, ctx Context) error {
	return s.Mutate(func(mutator SessionMutator, ctx Context) error {
		_, err := mutator.Commit([]Write{WriteFromValue(SetValue(address, next))}, ctx)
		return err
	}, ctx)
}

func (s *StorageBackedSession) DeleteValue(address Value, ctx Context) error {
	return s.Mutate(func(mutator SessionMutator, ctx Context) error {
		_, err := mutator.Commit([]Write{WriteFromValue(DeleteValue(address))}, ctx)
		return err
	}, ctx)
}

func (s *StorageBackedSession) AppendList(address ValueList, element JsonValue, ctx Context) error {
	return s.Mutate(func(mutator SessionMutator, ctx Context) error {
		_, err := mutator.Commit([]Write{WriteFromList(AppendList(address, element))}, ctx)
		return err
	}, ctx)
}

func (s *StorageBackedSession) DeleteList(address ValueList, ctx Context) error {
	return s.Mutate(func(mutator SessionMutator, ctx Context) error {
		_, err := mutator.Commit([]Write{WriteFromList(DeleteList(address))}, ctx)
		return err
	}, ctx)
}

func (s *StorageBackedSession) SetName(name *string, ctx Context) error {
	if name == nil {
		return s.DeleteValue(SessionName, ctx)
	}
	return s.SetValue(SessionName, *name, ctx)
}

func (s *StorageBackedSession) SetLabel(targetID string, label *string, ctx Context) error {
	address := EntryLabel(targetID)
	if label == nil {
		return s.DeleteValue(address, ctx)
	}
	return s.SetValue(address, *label, ctx)
}

func (s *StorageBackedSession) Close(ctx Context) error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.state = "closing"
		<-s.mutationLine.Seal(fmt.Errorf("Session is closed"))
		closeErr = s.storage.Close(ctx)
		s.state = "closed"
		close(s.closed)
		if s.onClose != nil {
			s.onClose()
		}
	})
	<-s.closed
	return closeErr
}

// GetBranchTip returns the branch tip or fails with an invariant error.
func (s *StorageBackedSession) GetBranchTip(name string, ctx Context) (*string, error) {
	stored, err := s.GetValue(BranchTip(name), ctx)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, &SessionInvariantError{Message: "Unknown branch: " + name}
	}
	if str, ok := stored.Value.(string); ok {
		return &str, nil
	}
	return nil, nil
}

// AppendToBranch appends one entry at the branch tip in a single
// transaction (entry + tip write).
func (s *StorageBackedSession) AppendToBranch(name string, entry *Entry, ctx Context) (string, error) {
	if err := s.assertOpen(); err != nil {
		return "", err
	}
	if entry.Type == EntryTypeMessage && entry.Message.Role == "assistant" {
		if msg := entry.Message.Message; msg != nil {
			if stop, ok := msg.Get("stopReason"); ok && stop == ai.StopPending {
				return "", &SessionPendingAssistantMessageError{}
			}
		}
	}
	id := s.idGenerator.Next()
	err := s.Mutate(func(mutator SessionMutator, ctx Context) error {
		tip, err := mutator.GetValue(BranchTip(name), ctx)
		if err != nil {
			return err
		}
		if tip == nil {
			return &SessionInvariantError{Message: "Unknown branch: " + name}
		}
		newEntry := *entry
		newEntry.ID = id
		newEntry.ParentID = nil
		if str, ok := tip.Value.(string); ok {
			newEntry.ParentID = &str
		}
		_, err = mutator.Commit([]Write{
			InsertEntry(&newEntry),
			WriteFromValue(SetValue(BranchTip(name), id)),
		}, ctx)
		return err
	}, ctx)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *StorageBackedSession) getOrCreateBranch(name string) *StorageBackedBranch {
	s.mu.Lock()
	defer s.mu.Unlock()
	if branch, ok := s.branches[name]; ok {
		return branch
	}
	branch := &StorageBackedBranch{name: name, session: s}
	s.branches[name] = branch
	return branch
}

func (s *StorageBackedSession) assertValidBranchName(name string) error {
	if len(name) == 0 {
		return &SessionInvalidBranchError{Branch: name, Reason: "branch name must not be empty"}
	}
	if strings.ContainsRune(name, 0) {
		return &SessionInvalidBranchError{Branch: name, Reason: "branch name must not contain \\u0000"}
	}
	return nil
}
