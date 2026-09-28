package pico3

// session_line_defects_test.go pins review fixes: re-entrant Commit from a
// listener completes (inline on the tail line), concurrent Subtree /
// Ancestors stay race-free against commits, Close drains and stops the
// tail goroutine, and the tracker observes nested in-place mutations.

import (
	"runtime"
	"sync"
	"testing"
	"time"

	jsonx "github.com/gladmo/openagent/jsonx"
)

func newDefectSession(t *testing.T) *Session {
	t.Helper()
	storage, err := OpenJsonlStorage(t.TempDir()+"/session", false)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(storage)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestReentrantCommitFromListenerCompletes(t *testing.T) {
	session := newDefectSession(t)
	defer session.Close()

	inner := make(chan error, 1)
	// Listeners dispatch serially on the tail line, so a plain flag is
	// safe; react only to the OUTER commit's result so the listener does
	// not recurse into itself for the inner commit.
	reacted := false
	session.AddListener(func(result *CommitResult) {
		if reacted || result.Changes == nil {
			return
		}
		for _, conversation := range result.Changes.Conversations {
			if conversation.ID != 1 {
				continue
			}
			reacted = true
			// A listener writing after commit is the natural plugin
			// pattern; on the TS promise chain it queues behind the
			// outer commit, here it runs inline on the tail line.
			_, err := session.Commit(Invoker{Type: "kernel"}, func(tx *Tx) (any, error) {
				_ = tx.NewConversation(&Conversation{ID: 42})
				return nil, nil
			})
			inner <- err
			return
		}
	})

	if _, err := session.Commit(Invoker{Type: "kernel"}, func(tx *Tx) (any, error) {
		_ = tx.NewConversation(&Conversation{ID: 1})
		return nil, nil
	}); err != nil {
		t.Fatalf("outer commit: %v", err)
	}
	if err := <-inner; err != nil {
		t.Fatalf("inner (re-entrant) commit: %v", err)
	}
	if got := session.Subtree(1)[1]; !got {
		t.Fatal("outer conversation missing")
	}
	if got := session.Subtree(42)[42]; !got {
		t.Fatal("inner conversation missing")
	}
}

func TestConcurrentReadsDuringCommits(t *testing.T) {
	session := newDefectSession(t)
	defer session.Close()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = session.Subtree(1)
				_ = session.Ancestors(1)
			}
		}()
	}
	var writers sync.WaitGroup
	for i := 0; i < 8; i++ {
		writers.Add(1)
		id := Id(i + 1)
		go func() {
			defer writers.Done()
			_, _ = session.Commit(Invoker{Type: "kernel"}, func(tx *Tx) (any, error) {
				_ = tx.NewConversation(&Conversation{ID: id})
				return nil, nil
			})
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()
}

func TestCloseStopsTailGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 10; i++ {
		session := newDefectSession(t)
		if _, err := session.Commit(Invoker{Type: "kernel"}, func(tx *Tx) (any, error) {
			_ = tx.NewConversation(&Conversation{ID: Id(i + 1)})
			return nil, nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// Let closers settle.
	for i := 0; i < 50; i++ {
		runtime.Gosched()
		if runtime.NumGoroutine() <= before+1 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("goroutines before=%d after=%d (tail goroutines leaked)", before, runtime.NumGoroutine())
}

func TestTrackerFlushSeesNestedMutations(t *testing.T) {
	initial := jsonx.NewObj()
	turn := jsonx.NewObj()
	turn.Set("step", float64(1))
	initial.Set("turn", turn)

	tracker := Track(initial)
	// Consume the forced base op.
	tracker.Flush()

	state := tracker.State()
	turnValue, _ := state.Get("turn")
	nested, ok := turnValue.(*jsonx.Obj)
	if !ok {
		t.Fatal("turn missing")
	}
	nested.Set("step", float64(2))

	ops := tracker.Flush()
	if len(ops) == 0 {
		t.Fatal("nested in-place mutation lost: flush emitted no ops")
	}
	found := false
	for _, op := range ops {
		if set, ok := op.(*setOpAlias); ok {
			found = true
			_ = set
		}
	}
	if !found {
		t.Fatalf("expected a Set op, got %T", ops[0])
	}
	// The second flush is quiet (idempotent adoption).
	if ops := tracker.Flush(); len(ops) != 0 {
		t.Fatalf("second flush emitted %d ops", len(ops))
	}
}
