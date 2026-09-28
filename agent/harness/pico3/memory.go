package pico3

// memory.go ports harness/pico3/memory.ts: MemoryStorage, the reference
// backend and the read path of JsonlStorage. A batch is validated and
// staged in full before any table changes.

import (
	"math"
	"sync"

	chorddelta "github.com/gladmo/openagent/chord/delta"
	"github.com/gladmo/openagent/jsonx"
)

func jsonxNew() *jsonx.Obj { return jsonx.NewObj() }

type rewindableLogEntry struct {
	seq Seq
	ops []Op
}

// MemoryStorage implements Storage in memory.
type MemoryStorage struct {
	mu                    sync.Mutex
	conversationsByID     map[Id]*Conversation
	entriesByID           map[Id]*Entry
	entriesByConversation map[Id][]*Entry // ascending
	tasksByID             map[Id]*Task
	inputsByID            map[Id]*Input
	inputsByRequest       map[string]*Input
	rewindableLog         map[Id][]rewindableLogEntry
	stickyLog             map[Id][][]Op
	sessionDoc            JsonObject
	hasSessionDoc         bool
	entrySeq              map[Id]Seq
	nextIDValue           Id
	seq                   Seq
	closed                bool
	batchMaxID            Id
}

// NewMemoryStorage builds an open storage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		conversationsByID:     map[Id]*Conversation{},
		entriesByID:           map[Id]*Entry{},
		entriesByConversation: map[Id][]*Entry{},
		tasksByID:             map[Id]*Task{},
		inputsByID:            map[Id]*Input{},
		inputsByRequest:       map[string]*Input{},
		rewindableLog:         map[Id][]rewindableLogEntry{},
		stickyLog:             map[Id][][]Op{},
		entrySeq:              map[Id]Seq{},
		nextIDValue:           1,
	}
}

// MintID claims the next id.
func (s *MemoryStorage) MintID() Id {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextIDValue
	s.nextIDValue++
	return id
}

func (s *MemoryStorage) setNextID(n Id) {
	if n > s.nextIDValue {
		s.nextIDValue = n
	}
}

// ValidateBatch runs the full batch validation against the current state
// WITHOUT applying anything: duplicate/unknown ids, unknown patches, etc.
// Returns the staged ops a matching Commit would apply.
func (s *MemoryStorage) ValidateBatch(writes []Write, seq Seq) ([]func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateBatchLocked(writes, seq)
}

func (s *MemoryStorage) validateBatchLocked(writes []Write, seq Seq) ([]func(), error) {
	created := map[Id]bool{}
	maxID := Id(0)
	claim := func(id Id, what string) error {
		if id <= 0 {
			return errPico("%s: invalid id %d", what, id)
		}
		if created[id] {
			return errPico("%s: id %d created twice in one batch", what, id)
		}
		created[id] = true
		if id > maxID {
			maxID = id
		}
		return nil
	}
	var ops []func()
	for i := range writes {
		w := &writes[i]
		switch w.Type {
		case "conversation":
			if _, exists := s.conversationsByID[w.Conversation.ID]; exists {
				return nil, errPico("conversation %d exists", w.Conversation.ID)
			}
			if err := claim(w.Conversation.ID, "conversation"); err != nil {
				return nil, err
			}
			c := cloneConversation(w.Conversation)
			ops = append(ops, func() {
				s.conversationsByID[c.ID] = c
				s.entriesByConversation[c.ID] = []*Entry{}
			})
		case "entry":
			if _, exists := s.entriesByID[w.Entry.ID]; exists {
				return nil, errPico("entry %d exists", w.Entry.ID)
			}
			if err := claim(w.Entry.ID, "entry"); err != nil {
				return nil, err
			}
			e := cloneEntry(w.Entry)
			ops = append(ops, func() {
				s.entriesByID[e.ID] = e
				s.entriesByConversation[e.ConversationID] = append(s.entriesByConversation[e.ConversationID], e)
				s.entrySeq[e.ID] = seq
			})
		case "task":
			if _, exists := s.tasksByID[w.Task.ID]; exists {
				return nil, errPico("task %d exists", w.Task.ID)
			}
			if err := claim(w.Task.ID, "task"); err != nil {
				return nil, err
			}
			t := cloneTask(w.Task)
			ops = append(ops, func() { s.tasksByID[t.ID] = t })
		case "task.patch":
			patch := w.Patch
			prev := s.tasksByID[patch.ID]
			if prev == nil {
				// The task may be created earlier in this same batch.
				for j := range writes {
					candidate := &writes[j]
					if candidate.Type == "task" && candidate.Task.ID == patch.ID {
						prev = candidate.Task
						break
					}
				}
			}
			if prev == nil {
				return nil, errPico("patch for unknown task %d", patch.ID)
			}
			p := *patch
			ops = append(ops, func() {
				cur := s.tasksByID[p.ID]
				next := cloneTask(cur)
				if p.Status != nil {
					next.Status = *p.Status
				}
				if p.HasCheckpointNull {
					next.Checkpoint = nil
				} else if p.Checkpoint != nil {
					next.Checkpoint = p.Checkpoint
				}
				if p.Abort != nil {
					next.Abort = *p.Abort
				}
				if p.HasOutcome {
					next.Outcome = p.Outcome
				}
				if p.HasOwns {
					next.Owns = p.Owns
				}
				s.tasksByID[p.ID] = next
			})
		case "input":
			i := cloneInput(w.Input)
			if _, exists := s.inputsByID[i.ID]; !exists {
				if err := claim(i.ID, "input"); err != nil {
					return nil, err
				}
			}
			ops = append(ops, func() {
				s.inputsByID[i.ID] = i
				if i.RequestID != nil {
					s.inputsByRequest[keyOf(i.ConversationID, *i.RequestID)] = i
				}
			})
		case "doc":
			ref := *w.Ref
			batchOps := append([]Op{}, w.Ops...)
			switch ref.Doc {
			case "session":
				ops = append(ops, func() {
					base := jsonxNewObj()
					base.Set("plugins", jsonxNewObj())
					if s.hasSessionDoc {
						base = s.sessionDoc
					}
					applied, err := chorddelta.ApplyImmutable(base, batchOps)
					if err != nil {
						return
					}
					if obj, ok := applied.(JsonObject); ok {
						s.sessionDoc = obj
						s.hasSessionDoc = true
					}
				})
			case "rewindable":
				ops = append(ops, func() {
					s.rewindableLog[ref.ConversationID] = append(s.rewindableLog[ref.ConversationID], rewindableLogEntry{seq: seq, ops: batchOps})
				})
			default: // sticky
				ops = append(ops, func() {
					s.stickyLog[ref.ConversationID] = append(s.stickyLog[ref.ConversationID], batchOps)
				})
			}
		}
	}
	// Track the batch max id for the caller (Commit applies it).
	s.batchMaxID = maxID
	return ops, nil
}

// Commit validates and applies atomically.
func (s *MemoryStorage) Commit(writes []Write) (Seq, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errPico("storage closed")
	}
	seq := s.seq + 1
	ops, err := s.validateBatchLocked(writes, seq)
	if err != nil {
		return 0, err
	}
	for _, op := range ops {
		op()
	}
	s.setNextID(s.batchMaxID + 1)
	s.seq = seq
	return seq, nil
}

func keyOf(conversationID Id, requestID string) string {
	return strconvFormatID(conversationID) + ":" + requestID
}

func strconvFormatID(id Id) string {
	return string(strconvAppendInt(nil, id))
}

func strconvAppendInt(b []byte, n Id) []byte {
	if n < 0 {
		return append(b, []byte("-")...)
	}
	if n == 0 {
		return append(b, '0')
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return append(b, digits...)
}

// Conversation reads one conversation.
func (s *MemoryStorage) Conversation(id Id) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.conversationsByID[id]; ok {
		cloned := cloneConversation(c)
		return cloned, nil
	}
	return nil, nil
}

// Conversations lists all conversations.
func (s *MemoryStorage) Conversations() ([]*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*Conversation{}
	for _, c := range s.conversationsByID {
		out = append(out, cloneConversation(c))
	}
	return out, nil
}

// Entries reads entries by id.
func (s *MemoryStorage) Entries(ids []Id) (map[Id]*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[Id]*Entry{}
	for _, id := range ids {
		if e, ok := s.entriesByID[id]; ok {
			out[id] = cloneEntry(e)
		}
	}
	return out, nil
}

// ScanEntries is newest-first and fork-aware: after this conversation's own
// entries, the parent's up to the fork point, and so on.
func (s *MemoryStorage) ScanEntries(scan EntryScan) ([]*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*Entry{}
	conversationID := scan.ConversationID
	hasCap := scan.Before != nil
	capValue := Id(math.MaxInt64)
	if scan.Before != nil {
		capValue = *scan.Before
	}
	for conversationID != 0 && len(out) < scan.Limit {
		own := s.entriesByConversation[conversationID]
		for i := len(own) - 1; i >= 0 && len(out) < scan.Limit; i-- {
			e := own[i]
			if hasCap && e.ID >= capValue {
				continue
			}
			if scan.Kind != "" && e.Kind != scan.Kind {
				continue
			}
			if scan.WithHead && e.Head == nil {
				continue
			}
			out = append(out, cloneEntry(e))
		}
		c, ok := s.conversationsByID[conversationID]
		if !ok {
			break
		}
		if c.Parent != nil {
			conversationID = c.Parent.ConversationId
			if hasCap {
				parentCap := c.Parent.At + 1
				if parentCap < capValue {
					capValue = parentCap
				}
			} else {
				capValue = c.Parent.At + 1
				hasCap = true
			}
		} else {
			conversationID = 0
		}
	}
	return out, nil
}

// Task reads one task.
func (s *MemoryStorage) Task(id Id) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasksByID[id]; ok {
		return cloneTask(t), nil
	}
	return nil, nil
}

// ScanTasks filters tasks by conversation, status set, and kind.
func (s *MemoryStorage) ScanTasks(scan TaskScan) ([]*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*Task{}
	for _, t := range s.tasksByID {
		if scan.ConversationID != nil && t.ConversationID != *scan.ConversationID {
			continue
		}
		if scan.Status != nil && !containsStatus(scan.Status, t.Status) {
			continue
		}
		if scan.Kind != "" && t.Kind != scan.Kind {
			continue
		}
		out = append(out, cloneTask(t))
	}
	return out, nil
}

func containsStatus(statuses []string, status string) bool {
	for _, s := range statuses {
		if s == status {
			return true
		}
	}
	return false
}

// Input reads one input.
func (s *MemoryStorage) Input(id Id) (*Input, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := s.inputsByID[id]; ok {
		return cloneInput(i), nil
	}
	return nil, nil
}

// InputByRequest reads by conversation + request id.
func (s *MemoryStorage) InputByRequest(conversationID Id, requestID string) (*Input, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := s.inputsByRequest[keyOf(conversationID, requestID)]; ok {
		return cloneInput(i), nil
	}
	return nil, nil
}

// Doc folds a document's op log.
func (s *MemoryStorage) Doc(ref DocRef) (JsonObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ref.Doc == "session" {
		if !s.hasSessionDoc {
			fresh := jsonxNewObj()
			fresh.Set("plugins", jsonxNewObj())
			return fresh, nil
		}
		return cloneObject(s.sessionDoc), nil
	}
	var log [][]Op
	if ref.Doc == "rewindable" {
		for _, entry := range s.rewindableLog[ref.ConversationID] {
			log = append(log, entry.ops)
		}
	} else {
		log = s.stickyLog[ref.ConversationID]
	}
	if log == nil {
		return nil, nil
	}
	return s.fold(log), nil
}

// DocAsOf returns the rewindable state after the commit containing entry
// `at`, walking the fork chain for entries owned by ancestors.
func (s *MemoryStorage) DocAsOf(conversationID, at Id) (JsonObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ownerEntry, ok := s.entriesByID[at]
	if !ok {
		return nil, nil
	}
	owner := ownerEntry.ConversationID
	chain := []Id{}
	c := s.conversationsByID[conversationID]
	for c != nil && c.ID != owner {
		chain = append(chain, c.ID)
		if c.Parent != nil {
			c = s.conversationsByID[c.Parent.ConversationId]
		} else {
			c = nil
		}
	}
	if c == nil {
		return nil, nil
	}
	seqAt := s.entrySeq[at]
	var log [][]Op
	for _, entry := range s.rewindableLog[owner] {
		if entry.seq <= seqAt {
			log = append(log, entry.ops)
		}
	}
	if len(log) == 0 {
		return nil, nil
	}
	return s.fold(log), nil
}

// fold applies the log from the last base op onward.
func (s *MemoryStorage) fold(log [][]Op) JsonObject {
	start := 0
	for i := len(log) - 1; i >= 0; i-- {
		if chorddelta.IsBase(log[i]) {
			start = i
			break
		}
	}
	var state JsonValue = jsonxNewObj()
	for i := start; i < len(log); i++ {
		applied, err := chorddelta.ApplyImmutable(state, log[i])
		if err != nil {
			continue
		}
		state = applied
	}
	if obj, ok := state.(JsonObject); ok {
		return cloneObject(obj)
	}
	return jsonxNewObj()
}

// Truncate drops sticky history before the last base op.
func (s *MemoryStorage) Truncate(ref DocRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ref.Doc != "sticky" {
		return nil
	}
	log, ok := s.stickyLog[ref.ConversationID]
	if !ok {
		return nil
	}
	start := 0
	for i := len(log) - 1; i >= 0; i-- {
		if chorddelta.IsBase(log[i]) {
			start = i
			break
		}
	}
	s.stickyLog[ref.ConversationID] = append([][]Op{}, log[start:]...)
	return nil
}

// Close marks the storage closed.
func (s *MemoryStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// ---------------------------------------------------------------------------
// clone helpers (reads return deep copies; stored records are frozen in TS)
// ---------------------------------------------------------------------------

func cloneConversation(c *Conversation) *Conversation {
	out := &Conversation{ID: c.ID}
	if c.Parent != nil {
		parent := *c.Parent
		out.Parent = &parent
	}
	if c.Owner != nil {
		owner := *c.Owner
		out.Owner = &owner
	}
	if c.Sections != nil {
		out.Sections = append([]SectionSeedRef{}, c.Sections...)
	}
	return out
}

func cloneEntry(e *Entry) *Entry {
	out := &Entry{ID: e.ID, ConversationID: e.ConversationID, Kind: e.Kind}
	if e.Model != nil {
		out.Model = append([]StoredMsg{}, e.Model...)
	}
	if e.Data != nil {
		out.Data = cloneObject(e.Data)
	}
	if e.Head != nil {
		head := *e.Head
		out.Head = &head
	}
	if e.Edits != nil {
		out.Edits = append([]ContextEdit{}, e.Edits...)
	}
	if e.ByTaskID != nil {
		byTask := *e.ByTaskID
		out.ByTaskID = &byTask
	}
	return out
}

func cloneTask(t *Task) *Task {
	out := &Task{
		ID: t.ID, ConversationID: t.ConversationID, Kind: t.Kind,
		Input: Clone(t.Input), Status: t.Status,
		Abort: t.Abort, Background: t.Background,
	}
	if t.Checkpoint != nil {
		out.Checkpoint = cloneObject(t.Checkpoint)
	}
	if t.Outcome != nil {
		out.Outcome = cloneObject(t.Outcome)
	}
	out.After = append([]Id{}, t.After...)
	out.Owns = append([]Id{}, t.Owns...)
	return out
}

func cloneInput(i *Input) *Input {
	out := &Input{ID: i.ID, ConversationID: i.ConversationID, Status: i.Status}
	if i.RequestID != nil {
		requestID := *i.RequestID
		out.RequestID = &requestID
	}
	if i.Entry != nil {
		entry := *i.Entry
		out.Entry = &entry
	}
	if i.Answer != nil {
		answer := *i.Answer
		out.Answer = &answer
	}
	if i.Reason != nil {
		reason := *i.Reason
		out.Reason = &reason
	}
	if i.Detail != nil {
		detail := *i.Detail
		out.Detail = &detail
	}
	return out
}

func cloneObject(obj JsonObject) JsonObject {
	if obj == nil {
		return nil
	}
	cloned, err := chordCopy(obj)
	if err != nil {
		return jsonxNewObj()
	}
	if out, ok := cloned.(JsonObject); ok {
		return out
	}
	return jsonxNewObj()
}

// peekNextID exposes the high-water mark (jsonl validation probe).
func (s *MemoryStorage) peekNextID() Id {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextIDValue
}

// SetNextIDForValidation aligns a probe's high-water mark.
func (s *MemoryStorage) SetNextIDForValidation(n Id) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextIDValue = n
}

// setNextIdExport raises the high-water mark (replay).
func (s *MemoryStorage) setNextIdExport(n Id) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setNextID(n)
}
