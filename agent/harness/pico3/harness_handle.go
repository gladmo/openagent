package pico3

// harness_handle.go ports harness/pico3/harness.ts's ConversationHandle:
// the host/kernel commit wrappers, the config facade (get/set/reset),
// send/write, document reads, context derivation, and the manual
// collapse decision.

import (
	"github.com/gladmo/openagent/jsonx"
)

// ConversationHandle mirrors the TS surface over one conversation.
type ConversationHandle struct {
	harness        *Harness
	ConversationID Id
}

// HandleFor wraps one conversation.
func (h *Harness) HandleFor(conversationID Id) *ConversationHandle {
	return &ConversationHandle{harness: h, ConversationID: conversationID}
}

// HostCommit runs one host-invoker transaction.
func (c *ConversationHandle) HostCommit(fn func(tx *Tx) (any, error)) (any, error) {
	return c.harness.commitValue(Invoker{Type: "host", ConversationID: c.ConversationID}, fn)
}

// KernelCommit runs one kernel-invoker transaction.
func (c *ConversationHandle) KernelCommit(fn func(tx *Tx) (any, error)) (any, error) {
	return c.harness.commitValue(Invoker{Type: "kernel", ConversationID: c.ConversationID}, fn)
}

// commitValue is the harness's commit->value helper.
func (h *Harness) commitValue(invoker Invoker, fn func(tx *Tx) (any, error)) (any, error) {
	result, err := h.session.Commit(invoker, fn)
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

// ConfigGet reads every routed config key's effective value.
func (c *ConversationHandle) ConfigGet(defaults *Defaults, rewindable, sticky *jsonx.Obj) *jsonx.Obj {
	return ProjectConfig(defaults, rewindable, sticky)
}

// ConfigSet validates a patch: undefined values are rejected with the
// "use config.reset()" guidance.
func ConfigSet(patch *jsonx.Obj) error {
	for _, key := range patch.Keys() {
		value, _ := patch.Get(key)
		if value == nil {
			return &configSetError{key: key}
		}
	}
	return nil
}

type configSetError struct{ key string }

func (e *configSetError) Error() string {
	return "config.set(" + e.key + "): use config.reset()"
}

// Send stages one input and returns its handle.
func (c *ConversationHandle) Send(requestID string, content *jsonx.Obj) (int64, error) {
	value, err := c.KernelCommit(func(tx *Tx) (any, error) {
		inputID := c.harness.Session().Storage.MintID()
		input := &Input{
			ID:             inputID,
			ConversationID: c.ConversationID,
			Status:         "queued",
		}
		if requestID != "" {
			id := requestID
			input.RequestID = &id
		}
		if err := tx.NewInput(input); err != nil {
			return nil, err
		}
		return inputID, nil
	})
	if err != nil {
		return 0, err
	}
	return value.(int64), nil
}

// Write appends one entry to the conversation through a host commit.
func (c *ConversationHandle) Write(entry *Entry) error {
	_, err := c.HostCommit(func(tx *Tx) (any, error) {
		entry.ConversationID = c.ConversationID
		return nil, tx.NewEntry(entry)
	})
	return err
}

// Rewindable reads the rewindable document.
func (c *ConversationHandle) Rewindable() (JsonObject, error) {
	return c.harness.Session().Storage.Doc(DocRef{Doc: "rewindable", ConversationID: c.ConversationID})
}

// Sticky reads the sticky document.
func (c *ConversationHandle) Sticky() (JsonObject, error) {
	return c.harness.Session().Storage.Doc(DocRef{Doc: "sticky", ConversationID: c.ConversationID})
}

// Context derives the model context at the newest tip.
func (c *ConversationHandle) Context() (*ContextView, error) {
	return DeriveContext(c.harness.Session().Storage, c.ConversationID, nil, nil)
}

// ManualCollapseThrough mirrors the manual collapse decision: the
// chooseThrough cut at the keepRecent budget; "nothing to collapse" when
// everything fits.
func ManualCollapseThrough(entries []*Entry, keepRecent float64, estimate func(messages []any) float64) (int64, error) {
	through, ok := kindsChooseThrough(entries, keepRecent, estimate)
	if !ok {
		return 0, &nothingToCollapseError{}
	}
	return through, nil
}

type nothingToCollapseError struct{}

func (e *nothingToCollapseError) Error() string { return "nothing to collapse" }

// kindsChooseThrough adapts the kinds package's ChooseThrough over
// pico3 entries.
func kindsChooseThrough(entries []*Entry, keepRecent float64, estimate func(messages []any) float64) (int64, bool) {
	return chooseThroughImpl(entries, keepRecent, estimate)
}
