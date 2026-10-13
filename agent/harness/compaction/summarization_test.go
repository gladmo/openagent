package compaction

import (
	"context"
	"errors"
	"strings"
	"testing"

	oa "github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

func stopText(text string) *ai.AssistantMessage {
	return &ai.AssistantMessage{StopReason: "stop", Content: []ai.ContentBlock{ai.TextContent{Text: text}}}
}

func TestCompleteSummarizationSuccess(t *testing.T) {
	var seenOptions *SummarizationOptions
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		seenOptions = options
		return &ai.AssistantMessage{StopReason: "stop"}, nil
	}

	response, err := CompleteSummarization(context.Background(), &ai.Model{}, nil, &SummarizationOptions{SessionID: "routing-1"}, streamFn, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if response == nil || response.StopReason != "stop" {
		t.Fatalf("response = %+v", response)
	}
	// Cache writes are disabled and routing is preserved.
	if seenOptions.CacheRetention != "none" {
		t.Fatalf("cache = %q", seenOptions.CacheRetention)
	}
	if seenOptions.SessionID != "routing-1" {
		t.Fatalf("session = %q", seenOptions.SessionID)
	}
}

func TestCompleteSummarizationFreshSessionID(t *testing.T) {
	var seenSession string
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		seenSession = options.SessionID
		return &ai.AssistantMessage{StopReason: "stop"}, nil
	}
	if _, err := CompleteSummarization(context.Background(), &ai.Model{}, nil, nil, streamFn, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
	if seenSession == "" {
		t.Fatal("no session id assigned")
	}
}

func TestCompleteSummarizationRetriesTransient(t *testing.T) {
	attempts := 0
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("socket hang up")
		}
		return &ai.AssistantMessage{StopReason: "stop"}, nil
	}
	policy := &ai.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}
	response, err := CompleteSummarization(context.Background(), &ai.Model{}, nil, nil, streamFn, policy)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if response == nil || response.StopReason != "stop" {
		t.Fatalf("response = %+v", response)
	}
	if attempts < 3 {
		t.Fatalf("attempts = %d", attempts)
	}
}

func TestCompleteSummarizationDeterministicError(t *testing.T) {
	attempts := 0
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		attempts++
		return nil, errors.New("invalid api key")
	}
	policy := &ai.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}
	response, err := CompleteSummarization(context.Background(), &ai.Model{}, nil, nil, streamFn, policy)
	if err == nil {
		t.Fatal("no error surfaced")
	}
	if response == nil || response.StopReason != ai.StopError {
		t.Fatalf("response = %+v", response)
	}
	if attempts != 1 {
		t.Fatalf("deterministic error retried: %d attempts", attempts)
	}
}

func TestCompleteSummarizationRequiresStreamFn(t *testing.T) {
	if _, err := CompleteSummarization(context.Background(), &ai.Model{}, nil, nil, nil, nil); err == nil {
		t.Fatal("nil stream fn accepted")
	}
}

func TestBuildSummarizationContext(t *testing.T) {
	messages := BuildSummarizationContext("<conversation>\nhi\n</conversation>")
	if len(messages) != 2 {
		t.Fatalf("messages = %d", len(messages))
	}
	system, ok := messages[0].(*ai.SystemMessage)
	if !ok {
		t.Fatalf("system type = %T", messages[0])
	}
	if !strings.Contains(system.Content.Text, "Do NOT continue the conversation") {
		t.Fatalf("system = %q", system.Content.Text)
	}
	user, ok := messages[1].(*ai.UserMessage)
	if !ok {
		t.Fatalf("user type = %T", messages[1])
	}
	if !strings.Contains(user.Content.Text, "<conversation>") {
		t.Fatalf("prompt = %q", user.Content.Text)
	}
}

func TestGenerateSummaryInitial(t *testing.T) {
	var promptSeen string
	var maxTokensSeen int
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		if user, ok := messages[1].(*ai.UserMessage); ok {
			promptSeen = user.Content.Text
		}
		maxTokensSeen = options.MaxTokens
		return stopText("## Goal\nship it"), nil
	}

	result, err := GenerateSummary(
		[]oa.AgentMessage{userMessage("fix the bug"), assistantMessage("done")},
		&ai.Model{},
		&GenerateSummaryOptions{ReserveTokens: 1000},
		streamFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result.Text != "## Goal\nship it" {
		t.Fatalf("text = %q", result.Text)
	}
	if maxTokensSeen != 800 {
		t.Fatalf("maxTokens = %d", maxTokensSeen)
	}
	if !strings.Contains(promptSeen, "<conversation>") || !strings.Contains(promptSeen, "## Goal") {
		t.Fatalf("prompt = %q", promptSeen)
	}
	if !strings.Contains(promptSeen, "[User]: fix the bug") {
		t.Fatalf("conversation missing: %q", promptSeen)
	}
}

func TestGenerateSummaryUpdateAndCustomFocus(t *testing.T) {
	var promptSeen string
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		if user, ok := messages[1].(*ai.UserMessage); ok {
			promptSeen = user.Content.Text
		}
		return stopText("updated"), nil
	}

	_, err := GenerateSummary(
		[]oa.AgentMessage{userMessage("more work")},
		&ai.Model{},
		&GenerateSummaryOptions{ReserveTokens: 100, PreviousSummary: "old summary", CustomInstructions: "focus on tests"},
		streamFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(promptSeen, "<previous-summary>\nold summary\n</previous-summary>") {
		t.Fatalf("previous summary missing: %q", promptSeen)
	}
	if !strings.Contains(promptSeen, "Update the existing structured summary") {
		t.Fatalf("update prompt missing: %q", promptSeen)
	}
	if !strings.Contains(promptSeen, "Additional focus: focus on tests") {
		t.Fatalf("custom focus missing: %q", promptSeen)
	}
}

func TestGenerateSummaryModelTokenCap(t *testing.T) {
	var maxTokensSeen int
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		maxTokensSeen = options.MaxTokens
		return stopText("s"), nil
	}
	_, err := GenerateSummary(nil, &ai.Model{MaxTokens: 100}, &GenerateSummaryOptions{ReserveTokens: 1000}, streamFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if maxTokensSeen != 100 {
		t.Fatalf("maxTokens = %d", maxTokensSeen)
	}
}

func TestGenerateSummaryRejectsLengthStop(t *testing.T) {
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{StopReason: ai.StopLength, Content: []ai.ContentBlock{ai.TextContent{Text: "partial"}}}, nil
	}
	if _, err := GenerateSummary(nil, nil, &GenerateSummaryOptions{ReserveTokens: 100}, streamFn); err == nil {
		t.Fatal("length stop accepted")
	}
}

func TestGenerateSummaryRejectsToolCalls(t *testing.T) {
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{StopReason: "stop", Content: []ai.ContentBlock{&ai.ToolCall{ID: "1", Name: "read"}}}, nil
	}
	if _, err := GenerateSummary(nil, nil, &GenerateSummaryOptions{ReserveTokens: 100}, streamFn); err == nil {
		t.Fatal("tool call accepted")
	}
}

func TestCompactPlain(t *testing.T) {
	preparation := &Preparation{
		FirstKeptEntryID:    "e3",
		MessagesToSummarize: []oa.AgentMessage{userMessage("q"), assistantMessage("a")},
		TokensBefore:        500,
		PreviousSummary:     "",
		FileOps:             CreateFileOps(),
		Settings:            Settings{Enabled: true, ReserveTokens: 1000},
	}
	outcome, err := Compact(preparation, &ai.Model{}, &GenerateSummaryOptions{}, func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return stopText("checkpoint text"), nil
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if outcome.Summary != "checkpoint text" || outcome.FirstKeptEntryID != "e3" || outcome.TokensBefore != 500 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestCompactSplitTurn(t *testing.T) {
	preparation := &Preparation{
		FirstKeptEntryID:    "e4",
		MessagesToSummarize: []oa.AgentMessage{userMessage("q1"), assistantMessage("a1")},
		TurnPrefixMessages:  []oa.AgentMessage{userMessage("q2"), assistantMessage("a2")},
		IsSplitTurn:         true,
		TokensBefore:        900,
		PreviousSummary:     "",
		FileOps:             CreateFileOps(),
		Settings:            Settings{Enabled: true, ReserveTokens: 1000},
	}
	summaryCount := 0
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		summaryCount++
		return stopText("segment " + strings.Repeat("x", summaryCount)), nil
	}
	outcome, err := Compact(preparation, &ai.Model{}, &GenerateSummaryOptions{}, streamFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if summaryCount != 2 {
		t.Fatalf("summary calls = %d", summaryCount)
	}
	if !strings.Contains(outcome.Summary, TurnContextSeparator) {
		t.Fatalf("separator missing: %q", outcome.Summary)
	}
	if !strings.HasPrefix(outcome.Summary, "segment x") {
		t.Fatalf("history summary missing: %q", outcome.Summary)
	}
}

func TestCompactRequiresPreparation(t *testing.T) {
	if _, err := Compact(nil, nil, nil, nil); err == nil {
		t.Fatal("nil preparation accepted")
	}
	if _, err := Compact(&Preparation{}, nil, nil, nil); err == nil {
		t.Fatal("missing first-kept id accepted")
	}
}

func TestFramedSummary(t *testing.T) {
	framed := FramedSummary("body")
	if !strings.HasPrefix(framed, CompactionSummaryPrefix) || !strings.HasSuffix(framed, CompactionSummarySuffix) {
		t.Fatalf("framed = %q", framed)
	}
	if !strings.Contains(framed, "body") {
		t.Fatalf("body missing: %q", framed)
	}
}
