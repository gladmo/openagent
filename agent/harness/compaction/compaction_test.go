package compaction

import (
	"strings"
	"testing"

	oa "github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func userMessage(text string) *ai.UserMessage {
	return &ai.UserMessage{Content: ai.StringContent(text)}
}

func assistantMessage(text string) *ai.AssistantMessage {
	return &ai.AssistantMessage{Content: []ai.ContentBlock{ai.TextContent{Text: text}}}
}

// pathEntry builds one non-compaction entry carrying a single message.
func pathEntry(id, role, text string) PathEntry {
	var message oa.AgentMessage
	switch role {
	case "user":
		message = userMessage(text)
	case "assistant":
		message = assistantMessage(text)
	case "toolResult":
		message = &ai.ToolResultMessage{Content: []ai.ContentBlock{ai.TextContent{Text: text}}}
	case "system":
		message = &ai.SystemMessage{Content: ai.StringContent(text)}
	}
	return PathEntry{ID: id, Messages: []oa.AgentMessage{message}}
}

func TestShouldCompact(t *testing.T) {
	settings := Settings{Enabled: true, ReserveTokens: 1000}
	if ShouldCompact(5000, 16000, settings) {
		t.Fatal("below threshold compacted")
	}
	if !ShouldCompact(15100, 16000, settings) {
		t.Fatal("above threshold not compacted")
	}
	settings.Enabled = false
	if ShouldCompact(15999, 16000, settings) {
		t.Fatal("disabled settings compacted")
	}
}

func TestEstimateTokens(t *testing.T) {
	// chars/4: 8 chars -> 2 tokens.
	if got := EstimateTokens(userMessage("12345678")); got != 2 {
		t.Fatalf("user tokens = %d", got)
	}
	thinking := &ai.AssistantMessage{Content: []ai.ContentBlock{
		ai.ThinkingContent{Thinking: "1234"},
		ai.TextContent{Text: "5678"},
		&ai.ToolCall{Name: "read", Arguments: jsonx.ObjFrom("path", "/a")},
	}}
	// thinking 4 + text 4 + name 4 + args encoded 14 = 26 chars -> 7 tokens.
	if got := EstimateTokens(thinking); got != 7 {
		t.Fatalf("assistant tokens = %d", got)
	}
	result := &ai.ToolResultMessage{Content: []ai.ContentBlock{ai.TextContent{Text: "12345678"}}}
	if got := EstimateTokens(result); got != 2 {
		t.Fatalf("toolResult tokens = %d", got)
	}
	system := &ai.SystemMessage{Content: ai.StringContent("1234")}
	if got := EstimateTokens(system); got != 1 {
		t.Fatalf("system tokens = %d", got)
	}
}

func TestCalculateContextTokens(t *testing.T) {
	if got := CalculateContextTokens(ai.Usage{TotalTokens: 12}); got != 12 {
		t.Fatalf("total = %d", got)
	}
	if got := CalculateContextTokens(ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}); got != 10 {
		t.Fatalf("components = %d", got)
	}
}

func TestEstimateContextTokens(t *testing.T) {
	withUsage := &ai.AssistantMessage{
		Content:    []ai.ContentBlock{ai.TextContent{Text: "a"}},
		Usage:      ai.Usage{TotalTokens: 100},
		StopReason: "stop",
	}
	trailing := userMessage("12345678") // 2 tokens
	estimate := EstimateContextTokens([]oa.AgentMessage{userMessage("seed"), withUsage, trailing})
	if estimate.Tokens != 102 || estimate.UsageTokens != 100 || estimate.TrailingTokens != 2 {
		t.Fatalf("estimate = %+v", estimate)
	}
	if estimate.LastUsageIndex != 1 {
		t.Fatalf("usage index = %d", estimate.LastUsageIndex)
	}

	// No usage anywhere: full char-based estimation.
	noUsage := EstimateContextTokens([]oa.AgentMessage{userMessage("1234"), assistantMessage("5678")})
	if noUsage.Tokens != 2 || noUsage.LastUsageIndex != -1 {
		t.Fatalf("no-usage estimate = %+v", noUsage)
	}

	// Aborted and errored assistants carry no usable usage.
	aborted := &ai.AssistantMessage{Content: []ai.ContentBlock{}, Usage: ai.Usage{TotalTokens: 50}, StopReason: ai.StopAborted}
	errored := &ai.AssistantMessage{Content: []ai.ContentBlock{}, Usage: ai.Usage{TotalTokens: 50}, StopReason: ai.StopError}
	if got := EstimateContextTokens([]oa.AgentMessage{aborted, errored}); got.UsageTokens != 0 {
		t.Fatalf("aborted usage counted: %+v", got)
	}
}

func TestFindCutPointBudget(t *testing.T) {
	// Three turns of 10 tokens each; the keep-recent budget of 30 fills at
	// the third turn's user entry, so the cut lands there and everything
	// before it is compressible.
	entries := []PathEntry{
		pathEntry("e1", "user", "0123456789012345678901234567890123456789"), // 10 tokens
		pathEntry("e2", "assistant", "0123456789012345678901234567890123456789"),
		pathEntry("e3", "user", "0123456789012345678901234567890123456789"),
		pathEntry("e4", "assistant", "0123456789012345678901234567890123456789"),
		pathEntry("e5", "user", "0123456789012345678901234567890123456789"),
		pathEntry("e6", "assistant", "0123456789"),
	}
	cut := FindCutPoint(entries, 0, len(entries), 30)
	if cut.FirstKeptEntryIndex != 2 {
		t.Fatalf("cut = %+v", cut)
	}
	if cut.IsSplitTurn || cut.TurnStartIndex != -1 {
		t.Fatalf("split = %+v", cut)
	}
}

func TestFindCutPointSplitTurn(t *testing.T) {
	// The budget cuts between an assistant message and its turn's user
	// start: a split turn.
	entries := []PathEntry{
		pathEntry("e1", "user", "0123456789012345678901234567890123456789"),
		pathEntry("e2", "assistant", "0123456789012345678901234567890123456789"),
		pathEntry("e3", "user", "0123456789012345678901234567890123456789"),
		pathEntry("e4", "assistant", "0123456789012345678901234567890123456789"),
		pathEntry("e5", "toolResult", "0123456789012345678901234567890123456789"),
	}
	// Budget keeps only the tool result: the cut point walks back to the
	// latest valid cut entry (e4, assistant) which does not start a turn.
	cut := FindCutPoint(entries, 0, len(entries), 12)
	if !cut.IsSplitTurn {
		t.Fatalf("cut = %+v", cut)
	}
	if cut.TurnStartIndex != 2 {
		t.Fatalf("turn start = %d", cut.TurnStartIndex)
	}
}

func TestFindCutPointAdjustsOverMetadata(t *testing.T) {
	// A tiny keep-recent budget cuts at the last user entry; the metadata
	// entry before it contributes no context and is pulled into the kept
	// suffix. The adjusted cut lands on the metadata entry itself, which
	// starts no turn, so the cut reports a split turn back to e1.
	entries := []PathEntry{
		pathEntry("e1", "user", "0123456789012345678901234567890123456789"),
		pathEntry("e2", "assistant", "0123456789012345678901234567890123456789"),
		{ID: "meta"},
		pathEntry("e4", "user", "0123456789012345678901234567890123456789"),
	}
	cut := FindCutPoint(entries, 0, len(entries), 5)
	if cut.FirstKeptEntryIndex != 2 {
		t.Fatalf("cut = %+v", cut)
	}
	if !cut.IsSplitTurn || cut.TurnStartIndex != 0 {
		t.Fatalf("split = %+v", cut)
	}
}

func TestPrepareCompaction(t *testing.T) {
	entries := []PathEntry{
		pathEntry("e1", "user", "0123456789012345678901234567890123456789"),
		pathEntry("e2", "assistant", "0123456789012345678901234567890123456789"),
		pathEntry("e3", "user", "0123456789012345678901234567890123456789"),
		pathEntry("e4", "assistant", "0123456789012345678901234567890123456789"),
	}
	preparation := PrepareCompaction(entries, Settings{Enabled: true, KeepRecentTokens: 20})
	if preparation == nil {
		t.Fatal("nothing prepared")
	}
	if preparation.FirstKeptEntryID != "e3" {
		t.Fatalf("first kept = %q", preparation.FirstKeptEntryID)
	}
	if len(preparation.MessagesToSummarize) != 2 {
		t.Fatalf("summarize messages = %d", len(preparation.MessagesToSummarize))
	}
	if preparation.PreviousSummary != "" {
		t.Fatalf("previous summary = %q", preparation.PreviousSummary)
	}

	// Path already ends in a compaction with contribution: nothing to do.
	compacted := append(entries, PathEntry{ID: "c1", IsCompaction: true, Summary: "s",
		Messages: []oa.AgentMessage{userMessage(FramedSummary("s"))}})
	if PrepareCompaction(compacted, Settings{Enabled: true, KeepRecentTokens: 1}) != nil {
		t.Fatal("compaction after compaction prepared")
	}
}

func TestPrepareCompactionHonorsPreviousCheckpoint(t *testing.T) {
	framed := userMessage(FramedSummary("first checkpoint"))
	entries := []PathEntry{
		{ID: "c1", IsCompaction: true, Summary: "first checkpoint", Messages: []oa.AgentMessage{framed}},
		pathEntry("e2", "user", "0123456789012345678901234567890123456789"),
		pathEntry("e3", "assistant", "0123456789012345678901234567890123456789"),
		pathEntry("e4", "user", "0123456789012345678901234567890123456789"),
		pathEntry("e5", "assistant", "0123456789012345678901234567890123456789"),
	}
	preparation := PrepareCompaction(entries, Settings{Enabled: true, KeepRecentTokens: 20})
	if preparation == nil {
		t.Fatal("nothing prepared")
	}
	if preparation.PreviousSummary != "first checkpoint" {
		t.Fatalf("previous = %q", preparation.PreviousSummary)
	}
	// Only the post-checkpoint history summarizes.
	if len(preparation.MessagesToSummarize) != 2 {
		t.Fatalf("messages = %d", len(preparation.MessagesToSummarize))
	}
}

func TestPrepareCompactionNothingCompressible(t *testing.T) {
	// A single user entry: the cut point is the turn's own user entry, so
	// nothing falls before it.
	entries := []PathEntry{
		pathEntry("e1", "user", "hello"),
	}
	if PrepareCompaction(entries, Settings{Enabled: true, KeepRecentTokens: 1}) != nil {
		t.Fatal("single entry prepared")
	}
}

func TestFileOperations(t *testing.T) {
	fileOps := CreateFileOps()
	ExtractFileOpsFromMessage(&ai.AssistantMessage{Content: []ai.ContentBlock{
		&ai.ToolCall{Name: "read", Arguments: jsonx.ObjFrom("path", "/a")},
		&ai.ToolCall{Name: "edit", Arguments: jsonx.ObjFrom("path", "/b")},
		&ai.ToolCall{Name: "other", Arguments: jsonx.ObjFrom("path", "/c")},
	}}, fileOps)
	read, modified := ComputeFileLists(fileOps)
	if len(read) != 1 || read[0] != "/a" {
		t.Fatalf("read = %v", read)
	}
	if len(modified) != 1 || modified[0] != "/b" {
		t.Fatalf("modified = %v", modified)
	}
	if got := FormatFileOperations(read, modified); !strings.Contains(got, "<read-files>") || !strings.Contains(got, "<modified-files>") {
		t.Fatalf("format = %q", got)
	}
	if got := FormatFileOperations(nil, nil); got != "" {
		t.Fatalf("empty format = %q", got)
	}
}

func TestSerializeConversation(t *testing.T) {
	messages := []ai.Message{
		userMessage("hello"),
		&ai.AssistantMessage{Content: []ai.ContentBlock{
			ai.ThinkingContent{Thinking: "hmm"},
			ai.TextContent{Text: "hi"},
			&ai.ToolCall{Name: "read", Arguments: jsonx.ObjFrom("path", "/a")},
		}},
		&ai.ToolResultMessage{Content: []ai.ContentBlock{ai.TextContent{Text: "0123456789"}}},
	}
	text := SerializeConversation(messages)
	for _, expected := range []string{"[User]: hello", "[Assistant thinking]: hmm", "[Assistant]: hi", "[Assistant tool calls]: read(path=\"/a\")", "[Tool result]: 0123456789"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in %q", expected, text)
		}
	}

	long := &ai.ToolResultMessage{Content: []ai.ContentBlock{ai.TextContent{Text: string(make([]byte, ToolResultMaxChars+10))}}}
	truncated := SerializeConversation([]ai.Message{long})
	if !strings.Contains(truncated, "more characters truncated]") {
		t.Fatal("no truncation marker")
	}
}
