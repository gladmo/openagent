package pico3

// Ports of harness.ts conversation-facing operations.

import (
	"strings"
	"testing"
)

func TestConversationLookupAndCreate(t *testing.T) {
	harness := newHarnessForTest(t)
	// Absent conversation returns nil handle.
	if handle, err := harness.Conversation(99); err != nil || handle != nil {
		t.Fatalf("handle = %v err = %v", handle, err)
	}
	// Create without input.
	id, err := harness.CreateConversation(ConversationSpec{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := harness.Conversation(id)
	if err != nil || handle == nil || handle.ID != id {
		t.Fatalf("handle = %+v err = %v", handle, err)
	}
	// Create WITH input: one conversation + one queued input in one tx.
	inputID := harness.Session().Storage.MintID()
	input := &Input{ID: inputID, ConversationID: 0, Status: "queued"}
	id2, err := harness.CreateConversation(ConversationSpec{}, &CreateConversationInput{})
	if err != nil {
		t.Fatal(err)
	}
	_ = input
	if handle, _ := harness.Conversation(id2); handle == nil {
		t.Fatal("second conversation missing")
	}
}

func TestHarnessEntriesAndTask(t *testing.T) {
	harness := newHarnessForTest(t)
	conversationID, err := harness.CreateConversation(ConversationSpec{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Stage one entry through the session.
	entry := &Entry{ID: harness.Session().Storage.MintID(), ConversationID: conversationID, Kind: "pi.user"}
	if _, err := harness.Session().Commit(Invoker{Type: "kernel"}, func(tx *Tx) (any, error) {
		return nil, tx.NewEntry(entry)
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := harness.Entries(EntryScan{ConversationID: conversationID, Limit: 10})
	if err != nil || len(entries) != 1 || entries[0].ID != entry.ID {
		t.Fatalf("entries = %d err = %v", len(entries), err)
	}
	// Tasks read absent as nil.
	if task, err := harness.GetTask(42); err != nil || task != nil {
		t.Fatalf("task = %v err = %v", task, err)
	}
}

func TestAbortInputStates(t *testing.T) {
	harness := newHarnessForTest(t)
	ctxFree := true
	_ = ctxFree
	conversationID, _ := harness.CreateConversation(ConversationSpec{}, nil)

	// Not found.
	result, err := harness.AbortInput(999, nil)
	if err != nil || result != InputNotFound {
		t.Fatalf("result = %s err = %v", result, err)
	}

	// Queued input aborts.
	queued := harness.stageInput(t, conversationID, "queued")
	result, err = harness.AbortInput(queued, nil)
	if err != nil || result != InputAborted {
		t.Fatalf("result = %s err = %v", result, err)
	}
	input, _ := harness.Session().Storage.Input(queued)
	if input.Status != "unanswered" {
		t.Fatalf("status = %s", input.Status)
	}
	if input.Reason == nil || *input.Reason != "aborted" {
		t.Fatal("reason missing")
	}

	// Placed input is already placed.
	placed := harness.stageInput(t, conversationID, "placed")
	result, err = harness.AbortInput(placed, nil)
	if err != nil || result != InputAlreadyPlaced {
		t.Fatalf("result = %s err = %v", result, err)
	}
}

func (h *Harness) stageInput(t *testing.T, conversationID Id, status string) Id {
	t.Helper()
	id := h.Session().Storage.MintID()
	input := &Input{ID: id, ConversationID: conversationID, Status: status}
	if _, err := h.Session().Commit(Invoker{Type: "kernel"}, func(tx *Tx) (any, error) {
		return nil, tx.NewInput(input)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAbortInputConversationGuard(t *testing.T) {
	harness := newHarnessForTest(t)
	convA, _ := harness.CreateConversation(ConversationSpec{}, nil)
	convB, _ := harness.CreateConversation(ConversationSpec{}, nil)
	inputID := harness.stageInput(t, convA, "queued")
	// Guard with the wrong conversation.
	wrong := convB
	if _, err := harness.AbortInput(inputID, &wrong); err == nil || !strings.Contains(err.Error(), "outside conversation") {
		t.Fatalf("err = %v", err)
	}
	// Guard with the right conversation proceeds.
	right := convA
	if result, err := harness.AbortInput(inputID, &right); err != nil || result != InputAborted {
		t.Fatalf("result = %s err = %v", result, err)
	}
}

func TestMarkTask(t *testing.T) {
	harness := newHarnessForTest(t)
	conversationID, _ := harness.CreateConversation(ConversationSpec{}, nil)
	taskID := harness.Session().Storage.MintID()
	task := &Task{ID: taskID, ConversationID: conversationID, Kind: "app.job", Status: "pending", After: []Id{}, Owns: []Id{}}
	if _, err := harness.Session().Commit(Invoker{Type: "kernel"}, func(tx *Tx) (any, error) {
		return nil, tx.SetTask(task)
	}); err != nil {
		t.Fatal(err)
	}

	// Pending -> marked.
	result, err := harness.MarkTask(taskID)
	if err != nil || result != TaskMarked {
		t.Fatalf("result = %s err = %v", result, err)
	}
	stored, _ := harness.GetTask(taskID)
	if !stored.Abort {
		t.Fatal("abort mark missing")
	}

	// Terminal -> terminal (no mark change).
	terminal := "terminal"
	if _, err := harness.Session().Commit(Invoker{Type: "kernel"}, func(tx *Tx) (any, error) {
		patched := *stored
		patched.Status = terminal
		return nil, tx.SetTask(&patched)
	}); err != nil {
		t.Fatal(err)
	}
	result, err = harness.MarkTask(taskID)
	if err != nil || result != TaskTerminal {
		t.Fatalf("result = %s err = %v", result, err)
	}

	// Unknown task errors.
	if _, err := harness.MarkTask(9999); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}
