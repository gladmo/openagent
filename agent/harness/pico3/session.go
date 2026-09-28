package pico3

// session.go ports harness/pico3/session.ts core: the serialized commit
// line, the live task/conversation index, the owning-Session guard, and
// the listener fan-out with fault semantics. TxImpl's full validation
// surface (kinds/namespaces/docs) lands with their ports; here a Tx
// collects writes and task/conversation changes.

import (
	"fmt"
	"sync"
	"time"
)

// Invoker mirrors the TS union.
type Invoker struct {
	Type           string // "kernel" | "task" | "plugin"
	ID             Id
	ConversationID Id
	Mode           string // run | abort
	Token          *InvocationToken
}

// InvocationToken reports invocation liveness.
type InvocationToken struct {
	mu     sync.Mutex
	aliveV bool
	revoke func()
}

// NewInvocationToken builds a live token with a revoke hook.
func NewInvocationToken(onRevoke func()) *InvocationToken {
	return &InvocationToken{aliveV: true, revoke: onRevoke}
}

// Alive reports liveness.
func (t *InvocationToken) Alive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.aliveV
}

// Revoke kills the token once.
func (t *InvocationToken) Revoke() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.aliveV {
		return
	}
	t.aliveV = false
	if t.revoke != nil {
		t.revoke()
	}
}

// Forbidden mirrors the TS error class.
type Forbidden struct{ Message string }

func (e *Forbidden) Error() string { return e.Message }

// Faulted mirrors the TS error class.
type Faulted struct{ Cause error }

func (e *Faulted) Error() string { return fmt.Sprintf("session faulted: %v", e.Cause) }

// TxChanges collects what one transaction changed.
type TxChanges struct {
	Tasks         []*Task
	Conversations []*Conversation
	Inputs        []*Input
	Events        []string
}

// Tx is the callback-scoped transaction surface.
type Tx struct {
	invoker Invoker
	writes  []Write
	changes TxChanges
	storage Storage
}

// Task reads through storage.
func (tx *Tx) Task(id Id) (*Task, error) { return tx.storage.Task(id) }

// SetTask stages a task write; an existing task is patched, a new one
// created (the kernel control surface semantics).
func (tx *Tx) SetTask(task *Task) error {
	existing, err := tx.storage.Task(task.ID)
	if err != nil {
		return err
	}
	if existing != nil {
		patch := &TaskPatch{ID: task.ID, Status: &task.Status}
		if task.Checkpoint != nil {
			patch.Checkpoint = task.Checkpoint
		} else {
			patch.HasCheckpointNull = true
		}
		abort := task.Abort
		patch.Abort = &abort
		if task.Outcome != nil {
			patch.Outcome = task.Outcome
			patch.HasOutcome = true
		}
		if task.Owns != nil {
			patch.Owns = task.Owns
			patch.HasOwns = true
		}
		tx.writes = append(tx.writes, NewTaskPatchWrite(patch))
	} else {
		tx.writes = append(tx.writes, NewTaskWrite(task))
	}
	tx.changes.Tasks = append(tx.changes.Tasks, task)
	return nil
}

// PatchTask stages a task patch.
func (tx *Tx) PatchTask(patch *TaskPatch) error {
	tx.writes = append(tx.writes, NewTaskPatchWrite(patch))
	return nil
}

// NewConversation stages a conversation write.
func (tx *Tx) NewConversation(c *Conversation) error {
	tx.writes = append(tx.writes, NewConversationWrite(c))
	tx.changes.Conversations = append(tx.changes.Conversations, c)
	return nil
}

// NewEntry stages an entry write.
func (tx *Tx) NewEntry(e *Entry) error {
	tx.writes = append(tx.writes, NewEntryWrite(e))
	return nil
}

// NewInput stages an input write.
func (tx *Tx) NewInput(i *Input) error {
	tx.writes = append(tx.writes, NewInputWrite(i))
	tx.changes.Inputs = append(tx.changes.Inputs, i)
	return nil
}

// DocOp stages a document op.
func (tx *Tx) DocOp(ref *DocRef, ops []Op) error {
	tx.writes = append(tx.writes, NewDocWrite(ref, ops))
	return nil
}

// CommitResult mirrors the TS interface.
type CommitResult struct {
	Value   any
	Seq     *Seq
	Changes *TxChanges
}

// SessionListener receives post-commit change notifications.
type SessionListener func(result *CommitResult)

// Session ports the TS class core.
type Session struct {
	mu                  sync.Mutex
	Storage             Storage
	LiveTasks           map[Id]*Task
	ConversationRecords map[Id]*Conversation
	OwnerTaskCache      map[Id]*Task
	lineListeners       []SessionListener
	listeners           []SessionListener
	OnReport            func(err error)
	closed              bool
	fault               *Faulted
	nowFn               func() float64
	tail                chan func()
	tailStarted         bool
}

var sessionOwners = map[Storage]bool{}

// NewSession builds a session owning one storage.
func NewSession(storage Storage) (*Session, error) {
	if sessionOwners[storage] {
		return nil, fmt.Errorf("this Storage already has an owning Session")
	}
	sessionOwners[storage] = true
	return &Session{
		Storage:             storage,
		LiveTasks:           map[Id]*Task{},
		ConversationRecords: map[Id]*Conversation{},
		OwnerTaskCache:      map[Id]*Task{},
		nowFn:               func() float64 { return float64(time.Now().UnixMilli()) },
	}, nil
}

// AddListener subscribes a post-commit listener.
func (s *Session) AddListener(listener SessionListener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, listener)
}

func (s *Session) assertUsable() error {
	if s.fault != nil {
		return s.fault
	}
	if s.closed {
		return fmt.Errorf("session is closed")
	}
	return nil
}

func (s *Session) enqueue(job func()) {
	s.mu.Lock()
	if !s.tailStarted {
		s.tail = make(chan func(), 1024)
		s.tailStarted = true
		go func(jobs chan func()) {
			for j := range jobs {
				j()
			}
		}(s.tail)
	}
	queue := s.tail
	s.mu.Unlock()
	queue <- job
}

// Commit runs one transaction on the serialized line.
func (s *Session) Commit(invoker Invoker, fn func(tx *Tx) (any, error)) (*CommitResult, error) {
	done := make(chan struct{})
	var result *CommitResult
	var err error
	s.enqueue(func() {
		defer close(done)
		if err = s.assertUsable(); err != nil {
			return
		}
		if invoker.Type == "task" {
			// A captured runtime cannot write after its invocation returned
			// or after terminalization.
			if invoker.Token != nil && !invoker.Token.Alive() {
				err = &Forbidden{Message: "commit from a finished invocation"}
				return
			}
			live, ok := s.LiveTasks[invoker.ID]
			if !ok {
				err = &Forbidden{Message: "commit from a task that is not live"}
				return
			}
			if invoker.Mode == "run" && live.Abort {
				err = &Forbidden{Message: "commit from a marked run invocation"}
				return
			}
		}
		tx := &Tx{invoker: invoker, storage: s.Storage}
		value, fnErr := fn(tx)
		if fnErr != nil {
			err = fnErr
			return
		}
		var seq Seq
		if len(tx.writes) > 0 {
			committed, commitErr := s.Storage.Commit(tx.writes)
			if commitErr != nil {
				fault := &Faulted{Cause: commitErr}
				s.fault = fault
				_ = s.Storage.Close()
				delete(sessionOwners, s.Storage)
				err = fault
				return
			}
			seq = committed
			s.applyChanges(&tx.changes)
			result = &CommitResult{Value: value, Seq: &seq, Changes: &tx.changes}
		} else {
			result = &CommitResult{Value: value, Changes: &tx.changes}
		}
		// Fan out to listeners; their errors are reported, never surfaced
		// to the writer.
		for _, listener := range s.listeners {
			func() {
				defer func() {
					if r := recover(); r != nil && s.OnReport != nil {
						s.OnReport(fmt.Errorf("%v", r))
					}
				}()
				listener(result)
			}()
		}
	})
	<-done
	return result, err
}

func (s *Session) applyChanges(changes *TxChanges) {
	for _, task := range changes.Tasks {
		if task.Status == "terminal" {
			delete(s.LiveTasks, task.ID)
			s.OwnerTaskCache[task.ID] = task
		} else {
			s.LiveTasks[task.ID] = task
		}
	}
	for _, conversation := range changes.Conversations {
		s.ConversationRecords[conversation.ID] = conversation
	}
}

// Subtree computes the conversation subtree under root via owner tasks.
func (s *Session) Subtree(root Id) map[Id]bool {
	out := map[Id]bool{root: true}
	grew := true
	for grew {
		grew = false
		for _, c := range s.ConversationRecords {
			if out[c.ID] || c.Owner == nil {
				continue
			}
			ownerTask := s.LiveTasks[*c.Owner]
			if ownerTask == nil {
				ownerTask = s.OwnerTaskCache[*c.Owner]
			}
			if ownerTask != nil && out[ownerTask.ConversationID] {
				out[c.ID] = true
				grew = true
			}
		}
	}
	return out
}

// Ancestors walks owner tasks up from a conversation.
func (s *Session) Ancestors(id Id) []Id {
	chain := []Id{}
	c := s.ConversationRecords[id]
	for c != nil && c.Owner != nil {
		var t *Task
		if live, ok := s.LiveTasks[*c.Owner]; ok {
			t = live
		} else if cached, ok := s.OwnerTaskCache[*c.Owner]; ok {
			t = cached
		}
		if t == nil {
			break
		}
		chain = append([]Id{t.ConversationID}, chain...)
		c = s.ConversationRecords[t.ConversationID]
	}
	return chain
}

// Close marks the session closed and releases storage ownership.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	delete(sessionOwners, s.Storage)
	return s.Storage.Close()
}
