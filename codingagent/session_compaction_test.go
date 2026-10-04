package codingagent

// Tests for session-level compaction execution (agent-session compact
// path) and its /compact command wiring.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// summaryStreamFn returns a fixed summary for every summarization call.
func summaryStreamFn(text string, usage ai.Usage) SummarizationStreamFn {
	return func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{
			StopReason: "stop",
			Content:    []ai.ContentBlock{&ai.TextContent{Text: text}},
			Usage:      usage,
		}, nil
	}
}

func TestCompactSessionNothingToDo(t *testing.T) {
	session := newTestSession(t)
	result, err := session.CompactSession(nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result == nil || result.Executed {
		t.Fatalf("result = %+v", result)
	}
}

func TestCompactSessionExecutes(t *testing.T) {
	session := newTestSession(t)
	// A long path so the cut point leaves history to summarize.
	for i := 0; i < 40; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		session.SessionManager.AppendMessage(map[string]any{
			"role":    role,
			"content": strings.Repeat("content ", 50),
		})
	}
	session.SetSummarizationStreamFn(summaryStreamFn("checkpoint summary", ai.Usage{Input: 30, Output: 10}))

	result, err := session.CompactSession(&SessionCompactionOptions{
		Settings: CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 200},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !result.Executed {
		t.Fatalf("not executed: %+v", result)
	}
	if !strings.Contains(result.Summary, "checkpoint summary") {
		t.Fatalf("summary = %q", result.Summary)
	}
	if result.Usage.Input != 30 {
		t.Fatalf("usage = %+v", result.Usage)
	}

	// The checkpoint landed in the session as a compaction entry.
	entries := session.SessionManager.GetEntries()
	found := false
	for _, entry := range entries {
		if compaction, ok := entry.(*CompactionEntry); ok {
			found = true
			if !strings.Contains(compaction.Summary, "checkpoint summary") {
				t.Fatalf("stored summary = %q", compaction.Summary)
			}
			if compaction.FirstKeptEntryID != result.FirstKeptEntryID {
				t.Fatalf("first kept = %q want %q", compaction.FirstKeptEntryID, result.FirstKeptEntryID)
			}
		}
	}
	if !found {
		t.Fatal("no compaction entry appended")
	}
}

func TestCompactSessionReportsFailure(t *testing.T) {
	session := newTestSession(t)
	for i := 0; i < 40; i++ {
		session.SessionManager.AppendMessage(map[string]any{
			"role":    "user",
			"content": strings.Repeat("content ", 50),
		})
	}
	session.SetSummarizationStreamFn(func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return nil, errors.New("summarizer down")
	})

	_, err := session.CompactSession(&SessionCompactionOptions{
		Settings: CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 200},
	})
	if err == nil || !strings.Contains(err.Error(), "summarizer down") {
		t.Fatalf("err = %v", err)
	}
}

func TestShouldAutoCompact(t *testing.T) {
	session := newTestSession(t)
	settings := CompactionSettings{Enabled: true, ReserveTokens: 1000}
	// An empty transcript is far below the threshold.
	if session.ShouldAutoCompact(settings) {
		t.Fatal("empty session wants compaction")
	}

	// A huge transcript crosses it.
	session.Agent.SetMessages([]agent.AgentMessage{
		&ai.UserMessage{Content: ai.StringContent(strings.Repeat("x", 600000))},
	})
	if !session.ShouldAutoCompact(settings) {
		t.Fatal("large transcript below threshold")
	}
}

func TestAutoCompactIfNeededNoop(t *testing.T) {
	session := newTestSession(t)
	result, err := session.AutoCompactIfNeeded(CompactionSettings{Enabled: true, ReserveTokens: 1000}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result == nil || result.Executed {
		t.Fatalf("result = %+v", result)
	}
}

func TestInteractiveCompactCommand(t *testing.T) {
	session := newTestSession(t)
	for i := 0; i < 40; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		session.SessionManager.AppendMessage(map[string]any{
			"role":    role,
			"content": strings.Repeat("content ", 50),
		})
	}
	session.SetSummarizationStreamFn(summaryStreamFn("interactive checkpoint", ai.Usage{Input: 5}))

	mode := NewInteractiveMode(InteractiveModeOptions{Session: session, TuiMode: "regular"})
	lines := &[]string{}
	mode.StatusSink = func(line string) { *lines = append(*lines, line) }

	// /compact uses the default settings (20k kept tokens), so the path
	// must exceed that for a cut point to exist.
	for i := 0; i < 60; i++ {
		session.SessionManager.AppendMessage(map[string]any{
			"role":    "user",
			"content": strings.Repeat("padding ", 400),
		})
	}

	mode.handleEditorSubmit("/compact")

	joined := strings.Join(*lines, "|")
	if !strings.Contains(joined, "Compacting...") {
		t.Fatalf("statuses = %q", joined)
	}
	if !strings.Contains(joined, "Compacted") {
		t.Fatalf("outcome = %q", joined)
	}
	if strings.Contains(joined, "error:") {
		t.Fatalf("error = %q", joined)
	}
}

func TestInteractiveCompactCommandUnavailable(t *testing.T) {
	session := newTestSession(t)
	mode := NewInteractiveMode(InteractiveModeOptions{Session: session, TuiMode: "regular"})
	lines := &[]string{}
	mode.StatusSink = func(line string) { *lines = append(*lines, line) }

	// /compact uses the default settings (20k kept tokens), so the path
	// must exceed that for a cut point to exist.
	for i := 0; i < 60; i++ {
		session.SessionManager.AppendMessage(map[string]any{
			"role":    "user",
			"content": strings.Repeat("padding ", 400),
		})
	}

	mode.handleEditorSubmit("/compact")
	joined := strings.Join(*lines, "|")
	if !strings.Contains(joined, "no summarization model") {
		t.Fatalf("statuses = %q", joined)
	}
}

func TestInteractiveCompactCommandNothingToDo(t *testing.T) {
	session := newTestSession(t)
	session.SetSummarizationStreamFn(summaryStreamFn("x", ai.Usage{}))
	mode := NewInteractiveMode(InteractiveModeOptions{Session: session, TuiMode: "regular"})
	lines := &[]string{}
	mode.StatusSink = func(line string) { *lines = append(*lines, line) }

	mode.handleEditorSubmit("/compact")
	if joined := strings.Join(*lines, "|"); !strings.Contains(joined, "Nothing to compact") {
		t.Fatalf("statuses = %q", joined)
	}
}
