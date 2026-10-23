package codingagent

// Tests for compaction utilities and cut points (ported from
// compaction.test.ts surfaces).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func assistantMessage(text string) *ai.AssistantMessage {
	return &ai.AssistantMessage{Content: []ai.ContentBlock{ai.TextContent{Text: text}}}
}

func userMessageOf(text string) *ai.UserMessage {
	return &ai.UserMessage{Content: ai.StringContent(text)}
}

func TestExtractFileOpsFromMessage(t *testing.T) {
	fileOps := CreateFileOps()

	args := func(path string) *jsonx.Obj {
		return objFrom(map[string]any{"path": path})
	}
	assistant := &ai.AssistantMessage{
		Content: []ai.ContentBlock{
			&ai.ToolCall{Name: "read", Arguments: args("/a.txt")},
			&ai.ToolCall{Name: "write", Arguments: args("/b.txt")},
			&ai.ToolCall{Name: "edit", Arguments: args("/c.txt")},
			&ai.ToolCall{Name: "grep", Arguments: args("/d.txt")},
			&ai.ToolCall{Name: "read", Arguments: objFrom(map[string]any{})},
		},
	}
	ExtractFileOpsFromMessage(assistant, fileOps)

	if !fileOps.Read["/a.txt"] || len(fileOps.Read) != 1 {
		t.Fatalf("read = %v", fileOps.Read)
	}
	if !fileOps.Written["/b.txt"] {
		t.Fatalf("written = %v", fileOps.Written)
	}
	if !fileOps.Edited["/c.txt"] {
		t.Fatalf("edited = %v", fileOps.Edited)
	}

	// Non-assistant messages are ignored.
	ExtractFileOpsFromMessage(userMessageOf("hi"), fileOps)
	if len(fileOps.Read) != 1 {
		t.Fatal("user message mutated file ops")
	}
}

func TestComputeFileLists(t *testing.T) {
	fileOps := CreateFileOps()
	fileOps.Read["/only-read"] = true
	fileOps.Read["/both"] = true
	fileOps.Written["/both"] = true
	fileOps.Edited["/changed"] = true

	readFiles, modifiedFiles := ComputeFileLists(fileOps)
	if len(readFiles) != 1 || readFiles[0] != "/only-read" {
		t.Fatalf("read = %v", readFiles)
	}
	if len(modifiedFiles) != 2 || modifiedFiles[0] != "/both" || modifiedFiles[1] != "/changed" {
		t.Fatalf("modified = %v", modifiedFiles)
	}
}

func TestFormatFileOperations(t *testing.T) {
	if got := FormatFileOperations(nil, nil); got != "" {
		t.Fatalf("empty = %q", got)
	}
	got := FormatFileOperations([]string{"/a"}, []string{"/b"})
	if !strings.Contains(got, "<read-files>\n/a\n</read-files>") {
		t.Fatalf("read section = %q", got)
	}
	if !strings.Contains(got, "<modified-files>\n/b\n</modified-files>") {
		t.Fatalf("modified section = %q", got)
	}
}

func TestSerializeConversation(t *testing.T) {
	messages := []ai.Message{
		userMessageOf("hello"),
		assistantMessage("hi there"),
		&ai.ToolResultMessage{Content: []ai.ContentBlock{ai.TextContent{Text: "tool output"}}},
	}
	serialized := SerializeConversation(messages)
	if !strings.Contains(serialized, "[User]: hello") {
		t.Fatalf("user = %q", serialized)
	}
	if !strings.Contains(serialized, "[Assistant]: hi there") {
		t.Fatalf("assistant = %q", serialized)
	}
	if !strings.Contains(serialized, "[Tool result]: tool output") {
		t.Fatalf("tool = %q", serialized)
	}

	// Tool calls render; thinking blocks are omitted from summaries
	// (#10893).
	assistant := &ai.AssistantMessage{
		Content: []ai.ContentBlock{
			ai.ThinkingContent{Thinking: "pondering"},
			&ai.ToolCall{Name: "read", Arguments: objFrom(map[string]any{"path": "/x"})},
			ai.TextContent{Text: "response"},
		},
	}
	serialized = SerializeConversation([]ai.Message{assistant})
	if strings.Contains(serialized, "pondering") {
		t.Fatalf("thinking leaked: %q", serialized)
	}
	if !strings.Contains(serialized, "[Assistant tool calls]: read(path=") {
		t.Fatalf("tool call = %q", serialized)
	}
	if !strings.Contains(serialized, "[Assistant]: response") {
		t.Fatalf("text = %q", serialized)
	}
}

func TestSerializeConversationToolResultTruncation(t *testing.T) {
	long := strings.Repeat("x", ToolResultMaxChars+500)
	messages := []ai.Message{
		&ai.ToolResultMessage{Content: []ai.ContentBlock{ai.TextContent{Text: long}}},
	}
	serialized := SerializeConversation(messages)
	if !strings.Contains(serialized, "more characters truncated]") {
		t.Fatalf("truncation marker = %q", serialized[len(serialized)-100:])
	}
	if len(serialized) >= len(long) {
		t.Fatalf("not truncated: %d", len(serialized))
	}
}

func TestEstimateTokens(t *testing.T) {
	// ~4 chars per token.
	if got := EstimateTokens(userMessageOf("12345678")); got != 2 {
		t.Fatalf("user = %d", got)
	}
	if got := EstimateTokens(assistantMessage("1234")); got != 1 {
		t.Fatalf("assistant = %d", got)
	}
	if got := EstimateTokens(&BashExecutionMessage{Command: "ab", Output: "cd"}); got != 1 {
		t.Fatalf("bash = %d", got)
	}
	if got := EstimateTokens(&BranchSummaryMessage{Summary: "12345678"}); got != 2 {
		t.Fatalf("branch = %d", got)
	}
	if got := EstimateTokens(&CompactionSummaryMessage{Summary: "1234"}); got != 1 {
		t.Fatalf("compaction = %d", got)
	}

	// Tool calls count name + args.
	assistant := &ai.AssistantMessage{
		Content: []ai.ContentBlock{
			&ai.ToolCall{Name: "abcd", Arguments: objFrom(map[string]any{"x": "y"})},
		},
	}
	if got := EstimateTokens(assistant); got < 2 {
		t.Fatalf("tool call = %d", got)
	}
}

func TestCalculateContextTokens(t *testing.T) {
	usage := ai.Usage{Input: 100, Output: 50, CacheRead: 10, CacheWrite: 5}
	if got := CalculateContextTokens(usage); got != 165 {
		t.Fatalf("components = %d", got)
	}
	usage.TotalTokens = 999
	if got := CalculateContextTokens(usage); got != 999 {
		t.Fatalf("native = %d", got)
	}
}

func TestGetLastAssistantUsage(t *testing.T) {
	entries := []SessionEntry{
		&SessionMessageEntry{
			SessionEntryBase: SessionEntryBase{Type: EntryTypeMessage, ID: "1", Timestamp: "2024-01-01T00:00:00Z"},
			Message:          rawJSON(`{"role":"assistant","content":[],"usage":{"input":10,"output":5}}`),
		},
		&SessionMessageEntry{
			SessionEntryBase: SessionEntryBase{Type: EntryTypeMessage, ID: "2", Timestamp: "2024-01-01T00:00:01Z"},
			Message:          rawJSON(`{"role":"user","content":"hi"}`),
		},
	}
	if usage := GetLastAssistantUsage(entries); usage == nil || usage.Input != 10 {
		t.Fatalf("usage = %+v", usage)
	}

	// Aborted assistant messages are skipped.
	entries = append(entries, &SessionMessageEntry{
		SessionEntryBase: SessionEntryBase{Type: EntryTypeMessage, ID: "3", Timestamp: "2024-01-01T00:00:02Z"},
		Message:          rawJSON(`{"role":"assistant","content":[],"stopReason":"aborted","usage":{"input":999}}`),
	})
	if usage := GetLastAssistantUsage(entries); usage == nil || usage.Input != 10 {
		t.Fatalf("aborted skipped = %+v", usage)
	}

	// No assistant usage.
	empty := []SessionEntry{
		&SessionMessageEntry{
			SessionEntryBase: SessionEntryBase{Type: EntryTypeMessage, ID: "1"},
			Message:          rawJSON(`{"role":"user","content":"x"}`),
		},
	}
	if usage := GetLastAssistantUsage(empty); usage != nil {
		t.Fatalf("none = %+v", usage)
	}
}

func TestEstimateContextTokens(t *testing.T) {
	// No usage: full estimation.
	messages := []agentAgentMessage{userMessageOf("1234")}
	estimate := EstimateContextTokens(toAgentMessages(messages))
	if estimate.Tokens != 1 || estimate.LastUsageIndex != -1 || estimate.UsageTokens != 0 {
		t.Fatalf("estimate = %+v", estimate)
	}

	// With usage: usage tokens + trailing estimates.
	withUsage := []agentAgentMessage{
		userMessageOf("hi"),
		assistantWithUsage(100, 50),
		userMessageOf("12345678"),
	}
	estimate = EstimateContextTokens(toAgentMessages(withUsage))
	if estimate.UsageTokens != 150 {
		t.Fatalf("usage = %d", estimate.UsageTokens)
	}
	if estimate.TrailingTokens != 2 {
		t.Fatalf("trailing = %d", estimate.TrailingTokens)
	}
	if estimate.Tokens != 152 {
		t.Fatalf("tokens = %d", estimate.Tokens)
	}
	if estimate.LastUsageIndex != 1 {
		t.Fatalf("index = %d", estimate.LastUsageIndex)
	}
}

func TestFindCutPoint(t *testing.T) {
	// Build entries: user, assistant, toolResult, user, assistant.
	manager := newTestSessionManager(t, false)
	var entries []SessionEntry
	for i, text := range []string{"q1", "a1", "q2", "a2", "q3"} {
		if i%2 == 0 {
			entries = append(entries, managerEntry(t, manager, userMessageOf(strings.Repeat(text, 200))))
		} else {
			entries = append(entries, managerEntry(t, manager, assistantMessage(strings.Repeat(text, 200))))
		}
	}

	// Large budget keeps everything: cut at the first entry.
	result := FindCutPoint(entries, 0, len(entries), 1000000)
	if result.FirstKeptEntryIndex != 0 {
		t.Fatalf("large budget = %+v", result)
	}
	if result.IsSplitTurn || result.TurnStartIndex != -1 {
		t.Fatalf("split = %+v", result)
	}

	// Tiny budget cuts near the end.
	result = FindCutPoint(entries, 0, len(entries), 10)
	if result.FirstKeptEntryIndex < 3 {
		t.Fatalf("tiny budget = %+v", result)
	}
}

// objFrom builds a jsonx object.
func objFrom(values map[string]any) *jsonx.Obj {
	obj := jsonx.NewObj()
	for key, value := range values {
		obj.Set(key, value)
	}
	return obj
}

// rawJSON marshals for message entries.
func rawJSON(text string) json.RawMessage { return json.RawMessage(text) }

// managerEntry appends a message and returns its entry.
func managerEntry(t *testing.T, manager *SessionManager, message interface{}) SessionEntry {
	t.Helper()
	raw := marshalAgentMessageWithRole(message)
	entry := &SessionMessageEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeMessage, ID: manager.generateID(), Timestamp: manager.nowISO(),
		},
		Message: json.RawMessage(raw),
	}
	manager.appendEntry(entry)
	return entry
}

// agentAgentMessage is shorthand for test message lists.
type agentAgentMessage interface{ Role() string }

// toAgentMessages converts to the agent message slice type.
func toAgentMessages(messages []agentAgentMessage) []agent.AgentMessage {
	out := make([]agent.AgentMessage, len(messages))
	for i, message := range messages {
		out[i] = message.(agent.AgentMessage)
	}
	return out
}

// assistantWithUsage builds an assistant message carrying usage.
func assistantWithUsage(input, output float64) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Content: []ai.ContentBlock{ai.TextContent{Text: "done"}},
		Usage:   ai.Usage{Input: input, Output: output},
	}
}

// jsonMarshalImpl avoids importing encoding/json twice in the branch tests.
func jsonMarshalImpl(value any) ([]byte, error) {
	return json.Marshal(value)
}
