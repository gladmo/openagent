package codingagent

// Tests for branch summarization collection and preparation (ported from
// branch-summarization.test.ts surfaces).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
)

func branchTestManager(t *testing.T) *SessionManager {
	t.Helper()
	return newTestSessionManager(t, false)
}

func TestCollectEntriesForBranchSummary(t *testing.T) {
	manager := branchTestManager(t)

	// Tree: root -> a -> b (old path), root -> c (target path).
	root := manager.AppendMessage(userMessageOf("root"))
	a := manager.AppendMessage(userMessageOf("a"))
	b := manager.AppendMessage(userMessageOf("b"))
	manager.Branch(root)
	c := manager.AppendMessage(userMessageOf("c"))
	_ = a

	result := CollectEntriesForBranchSummary(
		manager.GetBranch, func(id string) SessionEntry { return manager.GetEntry(id) },
		b, c,
	)
	if len(result.Entries) != 2 {
		t.Fatalf("entries = %d", len(result.Entries))
	}
	if result.CommonAncestorID != root {
		t.Fatalf("ancestor = %q", result.CommonAncestorID)
	}
	// Chronological order: a before b.
	if result.Entries[0].base().ID != a || result.Entries[1].base().ID != b {
		t.Fatalf("order = %v, %v", result.Entries[0].base().ID, result.Entries[1].base().ID)
	}

	// Same position: nothing to summarize.
	result = CollectEntriesForBranchSummary(
		manager.GetBranch, func(id string) SessionEntry { return manager.GetEntry(id) },
		b, b,
	)
	if len(result.Entries) != 0 || result.CommonAncestorID != b {
		t.Fatalf("same = %+v", result)
	}

	// No old leaf.
	result = CollectEntriesForBranchSummary(
		manager.GetBranch, func(id string) SessionEntry { return manager.GetEntry(id) },
		"", c,
	)
	if len(result.Entries) != 0 || result.CommonAncestorID != "" {
		t.Fatalf("no old = %+v", result)
	}
}

func TestCollectEntriesIncludesCompaction(t *testing.T) {
	manager := branchTestManager(t)

	root := manager.AppendMessage(userMessageOf("root"))
	manager.AppendMessage(userMessageOf("middle"))
	firstKept := manager.AppendMessage(userMessageOf("kept"))
	manager.AppendCompaction("summary", &firstKept, 100, nil, false, nil)
	afterCompaction := manager.AppendMessage(userMessageOf("after"))
	manager.Branch(root)
	target := manager.AppendMessage(userMessageOf("target"))

	// Compaction boundaries are included.
	result := CollectEntriesForBranchSummary(
		manager.GetBranch, func(id string) SessionEntry { return manager.GetEntry(id) },
		afterCompaction, target,
	)
	compactionSeen := false
	for _, entry := range result.Entries {
		if _, ok := entry.(*CompactionEntry); ok {
			compactionSeen = true
		}
	}
	if !compactionSeen {
		t.Fatalf("compaction missing from %d entries", len(result.Entries))
	}
}

func TestPrepareBranchEntries(t *testing.T) {
	manager := branchTestManager(t)

	var entries []SessionEntry
	entries = append(entries, managerEntryOf(t, manager, userMessageOf(strings.Repeat("u", 400))))
	entries = append(entries, managerEntryOf(t, manager, assistantMessage(strings.Repeat("a", 400))))
	entries = append(entries, managerEntryOf(t, manager, userMessageOf(strings.Repeat("m", 400))))

	// Unlimited budget keeps everything in chronological order.
	preparation := PrepareBranchEntries(entries, 0)
	if len(preparation.Messages) != 3 {
		t.Fatalf("messages = %d", len(preparation.Messages))
	}
	if preparation.TotalTokens != 300 { // 400 chars each / 4
		t.Fatalf("tokens = %d", preparation.TotalTokens)
	}

	// Tight budget keeps only the newest.
	preparation = PrepareBranchEntries(entries, 100)
	if len(preparation.Messages) > 1 {
		t.Fatalf("tight = %d messages", len(preparation.Messages))
	}
}

func TestPrepareBranchEntriesSkipsToolResults(t *testing.T) {
	manager := branchTestManager(t)

	toolResult := &ai.ToolResultMessage{Content: []ai.ContentBlock{ai.TextContent{Text: "result"}}}
	entries := []SessionEntry{managerEntryOf(t, manager, toolResult)}

	preparation := PrepareBranchEntries(entries, 0)
	if len(preparation.Messages) != 0 {
		t.Fatalf("tool result leaked = %d", len(preparation.Messages))
	}
}

func TestPrepareBranchEntriesSummaryOverBudget(t *testing.T) {
	manager := branchTestManager(t)

	// A compaction entry exceeds the budget but fits under the 90% rule.
	bigEntry := managerEntryOf(t, manager, userMessageOf(strings.Repeat("x", 4000)))
	firstKept := bigEntry.base().ID
	manager.appendEntry(bigEntry.(*SessionMessageEntry))

	compactionEntry := &CompactionEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeCompaction, ID: manager.generateID(),
			ParentID: &firstKept, Timestamp: manager.nowISO(),
		},
		Summary:          strings.Repeat("s", 400),
		FirstKeptEntryID: firstKept,
		TokensBefore:     100,
	}
	manager.appendEntry(compactionEntry)

	entries := manager.GetEntries()
	preparation := PrepareBranchEntries(entries, 200)
	// The 100-token summary fits under 90% of 200 even with 0 prior tokens.
	found := false
	for _, message := range preparation.Messages {
		if _, ok := message.(*CompactionSummaryMessage); ok {
			found = true
		}
	}
	if !found {
		t.Fatalf("summary dropped: %+v", preparation.Messages)
	}
}

func TestPrepareBranchEntriesNestedFileOps(t *testing.T) {
	manager := branchTestManager(t)

	// A prior branch summary carries file details.
	root := manager.AppendMessage(userMessageOf("root"))
	details, _ := jsonMarshal(BranchSummaryDetails{
		ReadFiles:     []string{"/prior-read"},
		ModifiedFiles: []string{"/prior-modified"},
	})
	summaryEntry := &BranchSummaryEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeBranchSummary, ID: "bs", ParentID: &root, Timestamp: manager.nowISO(),
		},
		FromID:   root,
		Summary:  "prior branch",
		Details:  details,
		FromHook: false,
	}
	manager.appendEntry(summaryEntry)

	// New assistant message with tool calls.
	assistant := &ai.AssistantMessage{
		Content: []ai.ContentBlock{
			&ai.ToolCall{Name: "read", Arguments: objFrom(map[string]any{"path": "/fresh"})},
		},
	}
	managerEntryOf(t, manager, assistant)

	preparation := PrepareBranchEntries(manager.GetEntries(), 0)
	if !preparation.FileOps.Read["/prior-read"] {
		t.Fatal("prior read missing")
	}
	if !preparation.FileOps.Edited["/prior-modified"] {
		t.Fatal("prior modified missing")
	}
	if !preparation.FileOps.Read["/fresh"] {
		t.Fatal("fresh read missing")
	}
}

func jsonMarshal(value any) (marshaled []byte, err error) {
	return jsonMarshalImpl(value)
}

func TestBranchSummaryConstants(t *testing.T) {
	if !strings.HasPrefix(BranchSummaryPreamble, "The user explored") {
		t.Fatalf("preamble = %q", BranchSummaryPreamble)
	}
	if !strings.Contains(BranchSummaryPrompt, "## Goal") || !strings.Contains(BranchSummaryPrompt, "## Next Steps") {
		t.Fatalf("prompt = %q", BranchSummaryPrompt)
	}
}

func managerEntryOf(t *testing.T, manager *SessionManager, message interface{}) SessionEntry {
	t.Helper()
	return managerEntry(t, manager, message)
}
