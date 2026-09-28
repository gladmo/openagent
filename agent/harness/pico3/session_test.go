package pico3

// Ports of pico3 session core test behaviors.

import (
	"strings"
	"sync"
	"testing"
)

func newSessionWithMemory(t *testing.T) *Session {
	t.Helper()
	sess, err := NewSession(NewMemoryStorage())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestSessionOwningGuard(t *testing.T) {
	storage := NewMemoryStorage()
	first, err := NewSession(storage)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := NewSession(storage); err == nil {
		t.Fatal("second owning session accepted")
	}
	// After close, ownership is released.
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := NewSession(storage)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
}

func kernelInvoker() Invoker { return Invoker{Type: "kernel"} }

func TestCommitPersistsAndIndexes(t *testing.T) {
	sess := newSessionWithMemory(t)
	result, err := sess.Commit(kernelInvoker(), func(tx *Tx) (any, error) {
		if err := tx.NewConversation(&Conversation{ID: 1}); err != nil {
			return nil, err
		}
		return "value", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "value" || result.Seq == nil || *result.Seq != 1 {
		t.Fatalf("result = %+v", result)
	}
	if _, ok := sess.ConversationRecords[1]; !ok {
		t.Fatal("conversation not indexed")
	}
	// Empty commit carries no seq.
	result, err = sess.Commit(kernelInvoker(), func(*Tx) (any, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.Seq != nil {
		t.Fatalf("seq = %v", result.Seq)
	}
}

func TestTaskLivenessIndex(t *testing.T) {
	sess := newSessionWithMemory(t)
	pending := &Task{ID: 7, ConversationID: 1, Kind: "job", Input: nil, Status: "pending", After: []Id{}, Owns: []Id{}}
	if _, err := sess.Commit(kernelInvoker(), func(tx *Tx) (any, error) {
		return nil, tx.SetTask(pending)
	}); err != nil {
		t.Fatal(err)
	}
	if live, ok := sess.LiveTasks[7]; !ok || live.Status != "pending" {
		t.Fatal("task not live")
	}
	// Terminal task moves to the owner cache.
	terminal := "terminal"
	if _, err := sess.Commit(kernelInvoker(), func(tx *Tx) (any, error) {
		patched := *pending
		patched.Status = terminal
		return nil, tx.SetTask(&patched)
	}); err != nil {
		t.Fatal(err)
	}
	if _, live := sess.LiveTasks[7]; live {
		t.Fatal("terminal task still live")
	}
	if cached, ok := sess.OwnerTaskCache[7]; !ok || cached.Status != "terminal" {
		t.Fatal("owner cache missing")
	}
}

func TestTaskInvokerAuthority(t *testing.T) {
	sess := newSessionWithMemory(t)
	// A task invoker for a task that is not live is forbidden.
	token := NewInvocationToken(nil)
	_, err := sess.Commit(Invoker{Type: "task", ID: 42, Token: token, Mode: "run"}, func(*Tx) (any, error) {
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "not live") {
		t.Fatalf("err = %v", err)
	}

	// Live task: run-mode commit is allowed while unmarked.
	pending := &Task{ID: 42, ConversationID: 1, Kind: "job", Status: "running", After: []Id{}, Owns: []Id{}}
	if _, err := sess.Commit(kernelInvoker(), func(tx *Tx) (any, error) {
		return nil, tx.SetTask(pending)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Commit(Invoker{Type: "task", ID: 42, Token: token, Mode: "run"}, func(*Tx) (any, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	// Marked (abort=true) run invocation is forbidden.
	marked := *pending
	marked.Abort = true
	if _, err := sess.Commit(kernelInvoker(), func(tx *Tx) (any, error) {
		return nil, tx.SetTask(&marked)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Commit(Invoker{Type: "task", ID: 42, Token: token, Mode: "run"}, func(*Tx) (any, error) {
		return nil, nil
	}); err == nil || !strings.Contains(err.Error(), "marked run") {
		t.Fatalf("err = %v", err)
	}

	// A finished invocation cannot commit.
	token.Revoke()
	if _, err := sess.Commit(Invoker{Type: "task", ID: 42, Token: token, Mode: "abort"}, func(*Tx) (any, error) {
		return nil, nil
	}); err == nil || !strings.Contains(err.Error(), "finished invocation") {
		t.Fatalf("err = %v", err)
	}
}

func TestCommitFaultIsTerminal(t *testing.T) {
	sess := newSessionWithMemory(t)
	// Force a storage failure: duplicate conversation id.
	if _, err := sess.Commit(kernelInvoker(), func(tx *Tx) (any, error) {
		return nil, tx.NewConversation(&Conversation{ID: 1})
	}); err != nil {
		t.Fatal(err)
	}
	_, err := sess.Commit(kernelInvoker(), func(tx *Tx) (any, error) {
		return nil, tx.NewConversation(&Conversation{ID: 1})
	})
	if err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, ok := err.(*Faulted); !ok {
		t.Fatalf("err type %T", err)
	}
	// Later commits fail with the same fault.
	if _, err := sess.Commit(kernelInvoker(), func(*Tx) (any, error) { return nil, nil }); err == nil || err.Error() != sess.fault.Error() {
		t.Fatalf("post-fault err = %v", err)
	}
}

func TestListenerFanOutIsolatedFromWriter(t *testing.T) {
	sess := newSessionWithMemory(t)
	var mu sync.Mutex
	notifications := 0
	sess.AddListener(func(*CommitResult) {
		mu.Lock()
		notifications++
		mu.Unlock()
		panic("listener exploded")
	})
	reports := 0
	sess.OnReport = func(error) {
		mu.Lock()
		reports++
		mu.Unlock()
	}
	if _, err := sess.Commit(kernelInvoker(), func(tx *Tx) (any, error) {
		return nil, tx.NewConversation(&Conversation{ID: 1})
	}); err != nil {
		t.Fatal(err)
	}
	if notifications != 1 || reports != 1 {
		t.Fatalf("notifications = %d reports = %d", notifications, reports)
	}
	// The session remains usable.
	if _, err := sess.Commit(kernelInvoker(), func(*Tx) (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
}

func TestSubtreeAndAncestors(t *testing.T) {
	sess := newSessionWithMemory(t)
	// Conversation 1 owned by task 10 (in conversation 1); conversation 2
	// owned by task 11 (in conversation 2); conversation 3 owned by task
	// 12 (in conversation 2).
	owner1 := Id(10)
	owner2 := Id(11)
	owner3 := Id(12)
	if _, err := sess.Commit(kernelInvoker(), func(tx *Tx) (any, error) {
		if err := tx.NewConversation(&Conversation{ID: 1}); err != nil {
			return nil, err
		}
		if err := tx.SetTask(&Task{ID: 10, ConversationID: 1, Kind: "k", Status: "running", After: []Id{}, Owns: []Id{}}); err != nil {
			return nil, err
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Commit(kernelInvoker(), func(tx *Tx) (any, error) {
		if err := tx.NewConversation(&Conversation{ID: 2, Owner: &owner1}); err != nil {
			return nil, err
		}
		if err := tx.SetTask(&Task{ID: 11, ConversationID: 2, Kind: "k", Status: "running", After: []Id{}, Owns: []Id{}}); err != nil {
			return nil, err
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Commit(kernelInvoker(), func(tx *Tx) (any, error) {
		return nil, tx.NewConversation(&Conversation{ID: 3, Owner: &owner2})
	}); err != nil {
		t.Fatal(err)
	}
	_ = owner3

	subtree := sess.Subtree(1)
	if !subtree[1] || !subtree[2] || !subtree[3] {
		t.Fatalf("subtree = %v", subtree)
	}
	subtree = sess.Subtree(2)
	if subtree[1] || !subtree[2] || !subtree[3] {
		t.Fatalf("subtree(2) = %v", subtree)
	}
	// Ancestors walk root-first: 3 -> [1, 2] (conversation chain from the
	// root down to the owner of 3).
	ancestors := sess.Ancestors(3)
	if len(ancestors) != 2 || ancestors[0] != 1 || ancestors[1] != 2 {
		t.Fatalf("ancestors = %v", ancestors)
	}
}

func TestInvocationTokenRevokeOnce(t *testing.T) {
	revoked := 0
	token := NewInvocationToken(func() { revoked++ })
	if !token.Alive() {
		t.Fatal("token born dead")
	}
	token.Revoke()
	token.Revoke()
	if revoked != 1 {
		t.Fatalf("revoked = %d", revoked)
	}
	if token.Alive() {
		t.Fatal("token alive after revoke")
	}
}
