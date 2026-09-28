package pico3

// Ports of the ConversationHandle surface.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func handleHarness(t *testing.T) (*Harness, *ConversationHandle) {
	t.Helper()
	harness, err := OpenHarness(NewMemoryStorage(), HarnessOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = harness.Session().Close() })
	conversationID, err := harness.CreateConversation(ConversationSpec{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return harness, harness.HandleFor(conversationID)
}

func TestHostAndKernelCommits(t *testing.T) {
	harness, handle := handleHarness(t)
	// Kernel commits run through the kernel invoker.
	value, err := handle.KernelCommit(func(tx *Tx) (any, error) {
		return "kernel-value", nil
	})
	if err != nil || value != "kernel-value" {
		t.Fatalf("value = %v err = %v", value, err)
	}
	// Host commits run through the host invoker.
	value, err = handle.HostCommit(func(tx *Tx) (any, error) {
		return "host-value", nil
	})
	if err != nil || value != "host-value" {
		t.Fatalf("value = %v err = %v", value, err)
	}
	_ = harness
}

func TestConfigSetValidation(t *testing.T) {
	patch := jsonx.ObjFrom("thinkingLevel", "high")
	if err := ConfigSet(patch); err != nil {
		t.Fatal(err)
	}
	// An explicitly-null value carries the guidance.
	bad := jsonx.NewObj()
	bad.Set("model", nil)
	err := ConfigSet(bad)
	if err == nil || !strings.Contains(err.Error(), "use config.reset()") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigGet(t *testing.T) {
	harness, handle := handleHarness(t)
	defaults := CollectDefaults(CoreConfigDeclarations())
	rewindable := NewRewindableState()
	rewindable.Set("thinkingLevel", "high")
	sticky := NewStickyState()
	config := handle.ConfigGet(defaults, rewindable, sticky)
	if config.MustGet("thinkingLevel") != "high" {
		t.Fatalf("config = %v", config)
	}
	if config.MustGet("steeringMode") != "all" {
		t.Fatal("sticky default lost")
	}
	_ = harness
}

func TestSendStagesInput(t *testing.T) {
	harness, handle := handleHarness(t)
	inputID, err := handle.Send("req-1", jsonx.ObjFrom("text", "hi"))
	if err != nil {
		t.Fatal(err)
	}
	if inputID == 0 {
		t.Fatal("no input id")
	}
	input, err := harness.Session().Storage.Input(inputID)
	if err != nil || input == nil {
		t.Fatalf("input = %v err = %v", input, err)
	}
	if input.ConversationID != handle.ConversationID || input.Status != "queued" {
		t.Fatalf("input = %+v", input)
	}
	if input.RequestID == nil || *input.RequestID != "req-1" {
		t.Fatal("request id")
	}
	// Request-keyed lookup works.
	found, _ := harness.Session().Storage.InputByRequest(handle.ConversationID, "req-1")
	if found == nil || found.ID != inputID {
		t.Fatal("request lookup")
	}
}

func TestWriteAppends(t *testing.T) {
	_, handle := handleHarness(t)
	entryID := handle.harness.Session().Storage.MintID()
	entry := &Entry{ID: entryID, Kind: "pi.user", Model: []any{jsonx.ObjFrom("role", "user", "content", "hi")}}
	if err := handle.Write(entry); err != nil {
		t.Fatal(err)
	}
	found, err := handle.harness.Session().Storage.Entries([]Id{entryID})
	if err != nil || len(found) != 1 {
		t.Fatalf("found = %d err = %v", len(found), err)
	}
	if found[entryID].ConversationID != handle.ConversationID {
		t.Fatal("conversation id not attached")
	}
}

func TestDocumentReads(t *testing.T) {
	_, handle := handleHarness(t)
	rewindable, err := handle.Rewindable()
	if err != nil {
		t.Fatal(err)
	}
	// Empty docs fold to empty objects.
	_ = rewindable
	sticky, err := handle.Sticky()
	if err != nil {
		t.Fatal(err)
	}
	_ = sticky
	// Context over an empty conversation is empty.
	view, err := handle.Context()
	if err != nil {
		t.Fatal(err)
	}
	if view.Head != nil || len(view.Entries) != 0 {
		t.Fatalf("view = %+v", view)
	}
}

func TestManualCollapseThrough(t *testing.T) {
	estimate := func(messages []any) float64 {
		total := 0.0
		for _, message := range messages {
			if obj, ok := message.(*jsonx.Obj); ok {
				if v, ok := obj.Get("tokens"); ok {
					if f, ok := v.(float64); ok {
						total += f
					}
				}
			}
		}
		return total
	}
	entry := func(id int64, role string, tokens float64) *Entry {
		return &Entry{ID: id, Model: []any{jsonx.ObjFrom("role", role, "tokens", tokens)}}
	}
	entries := []*Entry{
		entry(1, "user", 100),
		entry(2, "assistant", 200),
		entry(3, "user", 100),
		entry(4, "assistant", 200),
	}
	// Everything fits: nothing to collapse.
	if _, err := ManualCollapseThrough(entries, 10000, estimate); err == nil || !strings.Contains(err.Error(), "nothing to collapse") {
		t.Fatalf("err = %v", err)
	}
	// Retain only the last exchange: cut at entry 3.
	through, err := ManualCollapseThrough(entries, 200, estimate)
	if err != nil || through != 3 {
		t.Fatalf("through = %d err = %v", through, err)
	}
}
