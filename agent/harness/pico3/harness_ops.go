package pico3

// harness_ops.go ports harness.ts's conversation-facing operations:
// conversation lookup, conversation creation with an optional initial
// input, entry/task reads, input abort with the cross-conversation guard,
// and durable task marking.

import (
	"fmt"

	"github.com/gladmo/openagent/jsonx"
)

// ConversationHandle mirrors the TS handle surface over one conversation.
type ConversationHandle2 struct {
	harness *Harness
	ID      Id
}

// Conversation reads one conversation and returns its handle (nil when
// absent).
func (h *Harness) Conversation(id Id) (*ConversationHandle2, error) {
	conversation, err := h.session.Storage.Conversation(id)
	if err != nil || conversation == nil {
		return nil, err
	}
	return &ConversationHandle2{harness: h, ID: conversation.ID}, nil
}

// CreateConversationInput is the initial input payload.
type CreateConversationInput struct {
	Content *jsonx.Obj
}

// CreateConversation commits the conversation write plus an optional
// initial input in ONE transaction.
func (h *Harness) CreateConversation(spec ConversationSpec, input *CreateConversationInput) (Id, error) {
	var created Id
	_, err := h.session.Commit(Invoker{Type: "kernel"}, func(tx *Tx) (any, error) {
		conversation := &Conversation{}
		conversation.ID = h.session.Storage.MintID()
		if spec.Parent != nil {
			parent := *spec.Parent
			conversation.Parent = &parent
		}
		if spec.Owner != nil {
			owner := *spec.Owner
			conversation.Owner = &owner
		}
		if spec.Sections != nil {
			conversation.Sections = append([]SectionSeedRef{}, spec.Sections...)
		}
		if err := tx.NewConversation(conversation); err != nil {
			return nil, err
		}
		created = conversation.ID
		if input != nil {
			inputRecord := &Input{
				ID:             h.session.Storage.MintID(),
				ConversationID: conversation.ID,
				Status:         "queued",
			}
			if err := tx.NewInput(inputRecord); err != nil {
				return nil, err
			}
		}
		return conversation.ID, nil
	})
	if err != nil {
		return 0, err
	}
	return created, nil
}

// Entries scans entries through the session storage.
func (h *Harness) Entries(scan EntryScan) ([]*Entry, error) {
	return h.session.Storage.ScanEntries(scan)
}

// GetTask reads one task through the session storage.
func (h *Harness) GetTask(id Id) (*Task, error) {
	return h.session.Storage.Task(id)
}

// AbortInputResult mirrors the TS union.
type AbortInputResult string

const (
	InputAborted       AbortInputResult = "aborted"
	InputAlreadyPlaced AbortInputResult = "already_placed"
	InputNotFound      AbortInputResult = "not_found"
)

// AbortInput aborts a queued input; a conversationId guard rejects inputs
// outside that conversation.
func (h *Harness) AbortInput(id Id, conversationID *Id) (AbortInputResult, error) {
	if conversationID != nil {
		input, err := h.session.Storage.Input(id)
		if err != nil {
			return "", err
		}
		if input != nil && input.ConversationID != *conversationID {
			return "", &Forbidden{Message: fmt.Sprintf("input %d is outside conversation %d", id, *conversationID)}
		}
	}
	return h.abortInputRecord(id)
}

func (h *Harness) abortInputRecord(id Id) (AbortInputResult, error) {
	var result AbortInputResult
	_, err := h.session.Commit(Invoker{Type: "kernel"}, func(tx *Tx) (any, error) {
		input, err := tx.storage.Input(id)
		if err != nil || input == nil {
			result = InputNotFound
			return result, err
		}
		switch input.Status {
		case "placed", "done", "unanswered":
			result = InputAlreadyPlaced
			return result, nil
		}
		reason := "aborted"
		patched := *input
		patched.Status = "unanswered"
		patched.Reason = &reason
		if err := tx.NewInput(&patched); err != nil {
			return nil, err
		}
		result = InputAborted
		return result, nil
	})
	return result, err
}

// MarkTaskResult mirrors the TS union.
type MarkTaskResult string

const (
	TaskMarked   MarkTaskResult = "marked"
	TaskTerminal MarkTaskResult = "terminal"
)

// MarkTask durably marks a task for abort without signalling its
// invocation; the scheduler aborts it when it next drains.
func (h *Harness) MarkTask(id Id) (MarkTaskResult, error) {
	var result MarkTaskResult
	_, err := h.session.Commit(Invoker{Type: "kernel"}, func(tx *Tx) (any, error) {
		task, err := tx.Task(id)
		if err != nil {
			return nil, err
		}
		if task == nil {
			return nil, fmt.Errorf("task %d not found", id)
		}
		if task.Status == "terminal" {
			result = TaskTerminal
			return result, nil
		}
		marked := *task
		marked.Abort = true
		if err := tx.SetTask(&marked); err != nil {
			return nil, err
		}
		result = TaskMarked
		return result, nil
	})
	return result, err
}
