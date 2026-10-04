package codingagent

// Tests for the compaction summarization half (adapted from
// compaction.test.ts summarization surfaces).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

func TestSummarizationPromptConstants(t *testing.T) {
	for _, expected := range []string{
		"## Goal", "## Constraints & Preferences", "### Done", "### In Progress",
		"### Blocked", "## Key Decisions", "## Next Steps", "## Critical Context",
	} {
		if !strings.Contains(SummarizationPrompt, expected) {
			t.Errorf("initial prompt missing %q", expected)
		}
	}
	if !strings.Contains(SummarizationPrompt, "Preserve exact file paths, function names, and error messages") {
		t.Error("initial prompt missing preservation rule")
	}
	if !strings.Contains(UpdateSummarizationInstructions, "PRESERVE all existing information") {
		t.Error("update instructions missing preserve rule")
	}
	if !strings.Contains(UpdateSummarizationPrompt, "<previous-summary>") {
		t.Error("update prompt missing tag reference")
	}
	if !strings.Contains(TurnPrefixSummarizationPrompt, "earlier context") {
		t.Error("turn prefix prompt text changed")
	}
}

func TestGetSummarizationFailure(t *testing.T) {
	// Normal stops persist.
	if got := GetSummarizationFailure(&ai.AssistantMessage{StopReason: "stop"}, "Compaction"); got != "" {
		t.Fatalf("stop = %q", got)
	}
	if got := GetSummarizationFailure(&ai.AssistantMessage{StopReason: "toolUse"}, "Compaction"); got != "" {
		t.Fatalf("toolUse = %q", got)
	}

	// Error stop carries the provider message.
	message := "rate limited"
	got := GetSummarizationFailure(&ai.AssistantMessage{StopReason: "error", ErrorMessage: &message}, "Compaction")
	if !strings.Contains(got, "Compaction failed: rate limited") {
		t.Fatalf("error = %q", got)
	}

	// Error stop without a message falls back.
	got = GetSummarizationFailure(&ai.AssistantMessage{StopReason: "error"}, "Branch summary")
	if !strings.Contains(got, "Branch summary failed: Unknown error") {
		t.Fatalf("unknown = %q", got)
	}

	// Length stop must not become a checkpoint.
	got = GetSummarizationFailure(&ai.AssistantMessage{StopReason: "length"}, "Compaction")
	if !strings.Contains(got, "token cap") || !strings.Contains(got, "incomplete") {
		t.Fatalf("length = %q", got)
	}
}

func TestCreateSummarizationOptions(t *testing.T) {
	reasoning := &ai.Model{Provider: "p", ID: "m", Reasoning: true}
	plain := &ai.Model{Provider: "p", ID: "m"}

	// Reasoning model + level passes through.
	options := CreateSummarizationOptions(reasoning, 4096, "key", nil, nil, nil, "high", "sess")
	if options.MaxTokens != 4096 || options.ApiKey != "key" || options.SessionID != "sess" {
		t.Fatalf("options = %+v", options)
	}
	if options.ThinkingLevel != "high" {
		t.Fatalf("thinking = %q", options.ThinkingLevel)
	}

	// "off" and empty levels do not set reasoning.
	if got := CreateSummarizationOptions(reasoning, 1, "", nil, nil, nil, "off", "").ThinkingLevel; got != "" {
		t.Fatalf("off = %q", got)
	}
	if got := CreateSummarizationOptions(reasoning, 1, "", nil, nil, nil, "", "").ThinkingLevel; got != "" {
		t.Fatalf("empty = %q", got)
	}

	// Non-reasoning models ignore the level.
	if got := CreateSummarizationOptions(plain, 1, "", nil, nil, nil, "high", "").ThinkingLevel; got != "" {
		t.Fatalf("plain = %q", got)
	}
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
	// Branch summaries have no caller session: a fresh routing id lands.
	if _, err := CompleteSummarization(context.Background(), &ai.Model{}, nil, nil, streamFn, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
	if seenSession == "" {
		t.Fatal("no session id assigned")
	}
	if seenSession == "routing-1" {
		t.Fatal("id leaked from another call")
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
	if response == nil || response.StopReason != "error" {
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
		return &ai.AssistantMessage{
			StopReason: "stop",
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "## Goal\nship it"}},
			Usage:      ai.Usage{Input: 10, Output: 5},
		}, nil
	}

	result, err := GenerateSummary(nil, &ai.Model{MaxTokens: 8192}, &GenerateSummaryOptions{ReserveTokens: 1000}, streamFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(result.Text, "## Goal") {
		t.Fatalf("text = %q", result.Text)
	}
	if result.Usage.Input != 10 || result.Usage.Output != 5 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	// maxTokens = min(0.8*reserve, model max) = 800.
	if maxTokensSeen != 800 {
		t.Fatalf("max tokens = %d", maxTokensSeen)
	}
	// Initial prompt: no previous-summary tag.
	if strings.Contains(promptSeen, "<previous-summary>") {
		t.Fatalf("update prompt used = %q", promptSeen)
	}
	if !strings.Contains(promptSeen, "structured context checkpoint summary") {
		t.Fatalf("initial prompt missing = %q", promptSeen)
	}
	if !strings.Contains(promptSeen, "<conversation>") {
		t.Fatalf("conversation missing = %q", promptSeen)
	}
}

func TestGenerateSummaryUpdateAndCustomFocus(t *testing.T) {
	var promptSeen string
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		if user, ok := messages[1].(*ai.UserMessage); ok {
			promptSeen = user.Content.Text
		}
		return &ai.AssistantMessage{StopReason: "stop", Content: []ai.ContentBlock{&ai.TextContent{Text: "merged"}}}, nil
	}

	result, err := GenerateSummary(nil, &ai.Model{}, &GenerateSummaryOptions{
		ReserveTokens:      100,
		PreviousSummary:    "old summary text",
		CustomInstructions: "focus on tests",
	}, streamFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result.Text != "merged" {
		t.Fatalf("text = %q", result.Text)
	}
	if !strings.Contains(promptSeen, "<previous-summary>\nold summary text\n</previous-summary>") {
		t.Fatalf("previous tag = %q", promptSeen)
	}
	if !strings.Contains(promptSeen, "Additional focus: focus on tests") {
		t.Fatalf("custom focus = %q", promptSeen)
	}
	if !strings.Contains(promptSeen, "PRESERVE all existing information") {
		t.Fatalf("update prompt = %q", promptSeen)
	}
}

func TestGenerateSummaryModelTokenCap(t *testing.T) {
	var maxTokensSeen int
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		maxTokensSeen = options.MaxTokens
		return &ai.AssistantMessage{StopReason: "stop"}, nil
	}
	// Reserve 100000 -> 80000, but the model caps at 4096.
	if _, err := GenerateSummary(nil, &ai.Model{MaxTokens: 4096}, &GenerateSummaryOptions{ReserveTokens: 100000}, streamFn); err != nil {
		t.Fatalf("err = %v", err)
	}
	if maxTokensSeen != 4096 {
		t.Fatalf("max tokens = %d", maxTokensSeen)
	}
}

func TestGenerateSummaryRejectsLengthStop(t *testing.T) {
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{StopReason: "length"}, nil
	}
	_, err := GenerateSummary(nil, &ai.Model{}, &GenerateSummaryOptions{ReserveTokens: 100}, streamFn)
	if err == nil || !strings.Contains(err.Error(), "token cap") {
		t.Fatalf("err = %v", err)
	}
}

func TestGenerateSummaryRejectsToolCalls(t *testing.T) {
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{
			StopReason: "stop",
			Content:    []ai.ContentBlock{&ai.ToolCall{ID: "t", Name: "bash"}},
		}, nil
	}
	_, err := GenerateSummary(nil, &ai.Model{}, &GenerateSummaryOptions{ReserveTokens: 100}, streamFn)
	if err == nil || !strings.Contains(err.Error(), "attempted to call a tool") {
		t.Fatalf("err = %v", err)
	}
}

func branchSummaryEntry(role, text string) SessionEntry {
	message := map[string]any{"role": role, "content": text}
	encoded, _ := json.Marshal(message)
	return &SessionMessageEntry{
		SessionEntryBase: SessionEntryBase{ID: "e-" + text, Type: EntryTypeMessage},
		Message:          json.RawMessage(encoded),
	}
}

func TestGenerateBranchSummaryEmpty(t *testing.T) {
	result := GenerateBranchSummary(nil, &ai.Model{}, nil, nil)
	if result == nil {
		t.Fatal("nil result")
	}
	if result.Summary != "No content to summarize" {
		t.Fatalf("summary = %q", result.Summary)
	}
}

func TestGenerateBranchSummarySuccess(t *testing.T) {
	var promptSeen string
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		if user, ok := messages[1].(*ai.UserMessage); ok {
			promptSeen = user.Content.Text
		}
		return &ai.AssistantMessage{
			StopReason: "stop",
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "## Goal\nfix the bug"}},
			Usage:      ai.Usage{Input: 20, Output: 8},
		}, nil
	}

	entries := []SessionEntry{
		branchSummaryEntry("user", "please fix the parser"),
		branchSummaryEntry("assistant", "working on it"),
	}
	result := GenerateBranchSummary(entries, &ai.Model{ContextWindow: 128000}, nil, streamFn)
	if result.Error != "" {
		t.Fatalf("error = %q", result.Error)
	}
	if !strings.Contains(result.Summary, "The user explored a different conversation branch") {
		t.Fatalf("preamble missing: %q", result.Summary)
	}
	if !strings.Contains(result.Summary, "fix the bug") {
		t.Fatalf("summary = %q", result.Summary)
	}
	if result.Usage == nil {
		t.Fatal("usage missing")
	}
	// Branch prompt (not the compaction one) drove the request.
	if !strings.Contains(promptSeen, "structured summary of this conversation branch") {
		t.Fatalf("prompt = %q", promptSeen)
	}
	if strings.Contains(promptSeen, "Critical Context") {
		t.Fatalf("compaction prompt leaked: %q", promptSeen)
	}
}

func TestGenerateBranchSummaryCustomInstructions(t *testing.T) {
	var promptSeen string
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		if user, ok := messages[1].(*ai.UserMessage); ok {
			promptSeen = user.Content.Text
		}
		return &ai.AssistantMessage{StopReason: "stop", Content: []ai.ContentBlock{&ai.TextContent{Text: "ok"}}}, nil
	}
	entries := []SessionEntry{branchSummaryEntry("user", "hi")}

	// Appended focus keeps the stock format.
	GenerateBranchSummary(entries, &ai.Model{}, &GenerateBranchSummaryOptions{CustomInstructions: "focus on errors"}, streamFn)
	if !strings.Contains(promptSeen, "Additional focus: focus on errors") {
		t.Fatalf("appended = %q", promptSeen)
	}
	if !strings.Contains(promptSeen, "structured summary of this conversation branch") {
		t.Fatalf("stock lost = %q", promptSeen)
	}

	// Replace instructions swaps the prompt entirely.
	GenerateBranchSummary(entries, &ai.Model{}, &GenerateBranchSummaryOptions{
		CustomInstructions:  "only list files",
		ReplaceInstructions: true,
	}, streamFn)
	if !strings.Contains(promptSeen, "only list files") {
		t.Fatalf("replaced = %q", promptSeen)
	}
	if strings.Contains(promptSeen, "structured summary of this conversation branch") {
		t.Fatalf("stock survived replacement = %q", promptSeen)
	}
}

func TestGenerateBranchSummaryAborted(t *testing.T) {
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{StopReason: "aborted"}, nil
	}
	entries := []SessionEntry{branchSummaryEntry("user", "hi")}
	result := GenerateBranchSummary(entries, &ai.Model{}, nil, streamFn)
	if !result.Aborted {
		t.Fatalf("result = %+v", result)
	}
	if result.Error != "" {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestGenerateBranchSummaryErrors(t *testing.T) {
	entries := []SessionEntry{branchSummaryEntry("user", "hi")}

	// Length stop.
	lengthFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{StopReason: "length"}, nil
	}
	if result := GenerateBranchSummary(entries, &ai.Model{}, nil, lengthFn); !strings.Contains(result.Error, "token cap") {
		t.Fatalf("length = %+v", result)
	}

	// Tool call.
	toolFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{StopReason: "stop", Content: []ai.ContentBlock{&ai.ToolCall{ID: "t", Name: "read"}}}, nil
	}
	if result := GenerateBranchSummary(entries, &ai.Model{}, nil, toolFn); !strings.Contains(result.Error, "attempted to call a tool") {
		t.Fatalf("tool = %+v", result)
	}

	// Transport failure.
	failFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return nil, errors.New("socket closed")
	}
	if result := GenerateBranchSummary(entries, &ai.Model{}, nil, failFn); result.Error == "" {
		t.Fatalf("transport = %+v", result)
	}
}

func TestGenerateBranchSummaryTokenBudget(t *testing.T) {
	// A large context window fits every entry; a tiny one trims.
	var seenConversation string
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		if user, ok := messages[1].(*ai.UserMessage); ok {
			seenConversation = user.Content.Text
		}
		return &ai.AssistantMessage{StopReason: "stop", Content: []ai.ContentBlock{&ai.TextContent{Text: "x"}}}, nil
	}
	entries := []SessionEntry{
		branchSummaryEntry("user", strings.Repeat("long content ", 200)),
		branchSummaryEntry("user", "tail marker"),
	}

	GenerateBranchSummary(entries, &ai.Model{ContextWindow: 128000}, nil, streamFn)
	if !strings.Contains(seenConversation, "tail marker") {
		t.Fatalf("large budget missing tail: %q", seenConversation)
	}

	GenerateBranchSummary(entries, &ai.Model{ContextWindow: 17000}, nil, streamFn)
	if !strings.Contains(seenConversation, "tail marker") {
		t.Fatalf("trimmed budget must keep the tail: %q", seenConversation)
	}
	if strings.Contains(seenConversation, "long content long content long content long content long content long content") {
		t.Fatalf("budget not enforced: %q", seenConversation)
	}
}

func TestGenerateTurnPrefixSummary(t *testing.T) {
	var promptSeen string
	var maxTokensSeen int
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		if user, ok := messages[1].(*ai.UserMessage); ok {
			promptSeen = user.Content.Text
		}
		maxTokensSeen = options.MaxTokens
		return &ai.AssistantMessage{
			StopReason: "stop",
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "prefix checkpoint"}},
			Usage:      ai.Usage{Input: 7, Output: 3},
		}, nil
	}

	result, err := GenerateTurnPrefixSummary([]agent.AgentMessage{
		&ai.UserMessage{Content: ai.StringContent("do the thing")},
	}, &ai.Model{}, 10000, nil, streamFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result.Text != "prefix checkpoint" {
		t.Fatalf("text = %q", result.Text)
	}
	if result.Usage.Input != 7 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	// Budget is half the reserve.
	if maxTokensSeen != 5000 {
		t.Fatalf("max tokens = %d", maxTokensSeen)
	}
	if !strings.Contains(promptSeen, "# Conversation") || !strings.Contains(promptSeen, "# Instructions") {
		t.Fatalf("prompt = %q", promptSeen)
	}
	if !strings.Contains(promptSeen, "## Original Request") {
		t.Fatalf("format = %q", promptSeen)
	}
}

func TestGenerateTurnPrefixSummaryErrors(t *testing.T) {
	lengthFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{StopReason: "length"}, nil
	}
	if _, err := GenerateTurnPrefixSummary(nil, &ai.Model{}, 100, nil, lengthFn); err == nil || !strings.Contains(err.Error(), "Turn prefix summarization") {
		t.Fatalf("length = %v", err)
	}

	toolFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{StopReason: "stop", Content: []ai.ContentBlock{&ai.ToolCall{ID: "t", Name: "bash"}}}, nil
	}
	if _, err := GenerateTurnPrefixSummary(nil, &ai.Model{}, 100, nil, toolFn); err == nil || !strings.Contains(err.Error(), "attempted to call a tool") {
		t.Fatalf("tool = %v", err)
	}
}

func TestCombineUsage(t *testing.T) {
	one := ai.Usage{Input: 10, Output: 5, CacheRead: 2, CacheWrite: 1, TotalTokens: 18}
	two := ai.Usage{Input: 4, Output: 3, CacheRead: 1, CacheWrite: 0, TotalTokens: 8}
	combined := CombineUsage(one, two)
	if combined.Input != 14 || combined.Output != 8 || combined.CacheRead != 3 || combined.TotalTokens != 26 {
		t.Fatalf("combined = %+v", combined)
	}
	// Optional fields appear only when either side reported them.
	if combined.CacheWrite1h != nil || combined.Reasoning != nil {
		t.Fatalf("optional leaked = %+v", combined)
	}
	one.CacheWrite1h = usageFloatPtr(4)
	two.Reasoning = usageFloatPtr(3)
	combined = CombineUsage(one, two)
	if combined.CacheWrite1h == nil || *combined.CacheWrite1h != 4 {
		t.Fatalf("1h = %+v", combined.CacheWrite1h)
	}
	if combined.Reasoning == nil || *combined.Reasoning != 3 {
		t.Fatalf("reasoning = %+v", combined.Reasoning)
	}
}

func TestCompactRequiresPreparation(t *testing.T) {
	if _, err := Compact(nil, &ai.Model{}, nil, nil); err == nil {
		t.Fatal("nil preparation accepted")
	}
	// A missing first-kept id is the migration error.
	_, err := Compact(&CompactionPreparation{}, &ai.Model{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "session may need migration") {
		t.Fatalf("err = %v", err)
	}
}

func TestCompactHistoryOnly(t *testing.T) {
	var prompts []string
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		if user, ok := messages[1].(*ai.UserMessage); ok {
			prompts = append(prompts, user.Content.Text)
		}
		return &ai.AssistantMessage{
			StopReason: "stop",
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "history summary"}},
			Usage:      ai.Usage{Input: 100, Output: 20},
		}, nil
	}

	outcome, err := Compact(&CompactionPreparation{
		FirstKeptEntryID:    "keep-1",
		MessagesToSummarize: []agent.AgentMessage{&ai.UserMessage{Content: ai.StringContent("old turn")}},
		TokensBefore:        5000,
		Settings:            CompactionSettings{ReserveTokens: 1000, KeepRecentTokens: 100},
	}, &ai.Model{}, nil, streamFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(outcome.Summary, "history summary") {
		t.Fatalf("summary = %q", outcome.Summary)
	}
	if strings.Contains(outcome.Summary, "Turn Context") {
		t.Fatalf("split marker leaked: %q", outcome.Summary)
	}
	if outcome.FirstKeptEntryID != "keep-1" || outcome.TokensBefore != 5000 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.Usage.Input != 100 {
		t.Fatalf("usage = %+v", outcome.Usage)
	}
	if len(prompts) != 1 {
		t.Fatalf("prompts = %d", len(prompts))
	}
}

func TestCompactSplitTurnMergesSummaries(t *testing.T) {
	call := 0
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		call++
		text := "history part"
		if call > 1 {
			text = "prefix part"
		}
		return &ai.AssistantMessage{
			StopReason: "stop",
			Content:    []ai.ContentBlock{&ai.TextContent{Text: text}},
			Usage:      ai.Usage{Input: 10, Output: 2},
		}, nil
	}

	outcome, err := Compact(&CompactionPreparation{
		FirstKeptEntryID:    "keep-2",
		MessagesToSummarize: []agent.AgentMessage{&ai.UserMessage{Content: ai.StringContent("earlier")}},
		TurnPrefixMessages:  []agent.AgentMessage{&ai.UserMessage{Content: ai.StringContent("current turn head")}},
		IsSplitTurn:         true,
		Settings:            CompactionSettings{ReserveTokens: 1000},
	}, &ai.Model{}, nil, streamFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(outcome.Summary, "history part") || !strings.Contains(outcome.Summary, "prefix part") {
		t.Fatalf("summary = %q", outcome.Summary)
	}
	if !strings.Contains(outcome.Summary, "**Turn Context (split turn):**") {
		t.Fatalf("separator missing: %q", outcome.Summary)
	}
	// Usage combines both calls.
	if outcome.Usage.Input != 20 || outcome.Usage.Output != 4 {
		t.Fatalf("usage = %+v", outcome.Usage)
	}
}

func TestCompactSplitTurnWithoutHistory(t *testing.T) {
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{
			StopReason: "stop",
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "prefix only"}},
			Usage:      ai.Usage{Input: 3},
		}, nil
	}
	outcome, err := Compact(&CompactionPreparation{
		FirstKeptEntryID:   "keep-3",
		TurnPrefixMessages: []agent.AgentMessage{&ai.UserMessage{Content: ai.StringContent("head")}},
		IsSplitTurn:        true,
		PreviousSummary:    "prior summary",
		Settings:           CompactionSettings{ReserveTokens: 100},
	}, &ai.Model{}, nil, streamFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(outcome.Summary, "prior summary") {
		t.Fatalf("previous summary lost: %q", outcome.Summary)
	}
	if !strings.Contains(outcome.Summary, "prefix only") {
		t.Fatalf("prefix lost: %q", outcome.Summary)
	}
	if outcome.Usage.Input != 3 {
		t.Fatalf("usage = %+v", outcome.Usage)
	}
}

func TestCompactAppendsFileOperations(t *testing.T) {
	streamFn := func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{
			StopReason: "stop",
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "summary"}},
		}, nil
	}
	fileOps := CreateFileOps()
	fileOps.Read["/read/one.go"] = true
	fileOps.Written["/modified/two.go"] = true

	outcome, err := Compact(&CompactionPreparation{
		FirstKeptEntryID:    "keep-4",
		MessagesToSummarize: []agent.AgentMessage{&ai.UserMessage{Content: ai.StringContent("x")}},
		FileOps:             fileOps,
		Settings:            CompactionSettings{ReserveTokens: 100},
	}, &ai.Model{}, nil, streamFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(outcome.ReadFiles) != 1 || outcome.ReadFiles[0] != "/read/one.go" {
		t.Fatalf("read files = %v", outcome.ReadFiles)
	}
	if len(outcome.ModifiedFiles) != 1 || outcome.ModifiedFiles[0] != "/modified/two.go" {
		t.Fatalf("modified files = %v", outcome.ModifiedFiles)
	}
	if !strings.Contains(outcome.Summary, "/read/one.go") || !strings.Contains(outcome.Summary, "/modified/two.go") {
		t.Fatalf("summary files = %q", outcome.Summary)
	}
}

// --- PrepareCompaction -----------------------------------------------------

func compactionSessionEntry(id, role, text string) SessionEntry {
	message := map[string]any{"role": role, "content": text}
	encoded, _ := json.Marshal(message)
	return &SessionMessageEntry{
		SessionEntryBase: SessionEntryBase{ID: id, Type: EntryTypeMessage, Timestamp: "2024-01-01T00:00:00Z"},
		Message:          json.RawMessage(encoded),
	}
}

func TestShouldCompact(t *testing.T) {
	settings := CompactionSettings{Enabled: true, ReserveTokens: 1000}
	if ShouldCompact(500, 10000, settings) {
		t.Fatal("below threshold compacted")
	}
	if !ShouldCompact(9500, 10000, settings) {
		t.Fatal("above threshold skipped")
	}
	// Disabled never compacts.
	disabled := CompactionSettings{Enabled: false, ReserveTokens: 1000}
	if ShouldCompact(99999, 10000, disabled) {
		t.Fatal("disabled compacted")
	}
}

func TestPrepareCompactionEndsInCompaction(t *testing.T) {
	entries := []SessionEntry{
		compactionSessionEntry("a", "user", "hi"),
		&CompactionEntry{SessionEntryBase: SessionEntryBase{ID: "c", Type: EntryTypeCompaction}, Summary: "s"},
	}
	if got := PrepareCompaction(entries, DefaultCompactionSettings); got != nil {
		t.Fatalf("preparation = %+v", got)
	}
}

// chainedEntries builds a parent-linked path (what PrepareCompaction
// consumes from the session manager).
func chainedEntries(count int, text string) []SessionEntry {
	var entries []SessionEntry
	parent := ""
	for i := 0; i < count; i++ {
		id := "entry-" + itoaBash(i)
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		entry := compactionSessionEntry(id, role, text)
		if parent != "" {
			base := entry.base()
			parentCopy := parent
			base.ParentID = &parentCopy
		}
		entries = append(entries, entry)
		parent = id
	}
	return entries
}

func TestPrepareCompactionPlansCut(t *testing.T) {
	entries := chainedEntries(40, strings.Repeat("content ", 50))
	var latestID string
	for _, entry := range entries {
		latestID = entry.base().ID
	}

	settings := CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 200}
	preparation := PrepareCompaction(entries, settings)
	if preparation == nil {
		t.Fatal("no preparation")
	}
	if preparation.FirstKeptEntryID == "" {
		t.Fatal("no first kept id")
	}
	if preparation.FirstKeptEntryID == latestID && len(preparation.MessagesToSummarize) == 0 {
		t.Fatal("nothing to summarize")
	}
	if len(preparation.MessagesToSummarize) == 0 {
		t.Fatal("no history messages")
	}
	if preparation.TokensBefore <= 0 {
		t.Fatalf("tokens before = %d", preparation.TokensBefore)
	}
	// The kept entry must be one of the path entries.
	found := false
	for _, entry := range entries {
		if entry.base().ID == preparation.FirstKeptEntryID {
			found = true
		}
	}
	if !found {
		t.Fatalf("first kept id %q not in path", preparation.FirstKeptEntryID)
	}
}

func TestPrepareCompactionHonorsPreviousSummary(t *testing.T) {
	head := chainedEntries(6, strings.Repeat("content ", 50))
	tail := chainedEntries(34, strings.Repeat("content ", 50))

	// A compaction entry mid-path contributes its summary as the restore.
	headID := head[len(head)-1].base().ID
	tailParent := headID
	for i, entry := range tail {
		base := entry.base()
		if i == 0 {
			base.ParentID = &tailParent
		}
		base.ID = "tail-" + itoaBash(i)
		if i > 0 {
			prev := "tail-" + itoaBash(i-1)
			base.ParentID = &prev
		}
	}
	var withCompaction []SessionEntry
	withCompaction = append(withCompaction, head...)
	compactParent := headID
	withCompaction = append(withCompaction, &CompactionEntry{
		SessionEntryBase: SessionEntryBase{ID: "compact-1", Type: EntryTypeCompaction, ParentID: &compactParent},
		Summary:          "previous checkpoint",
	})
	tailFirst := tail[0].base()
	compactID := "compact-1"
	tailFirst.ParentID = &compactID
	withCompaction = append(withCompaction, tail...)

	settings := CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 200}
	preparation := PrepareCompaction(withCompaction, settings)
	if preparation == nil {
		t.Fatal("no preparation")
	}
	if preparation.PreviousSummary == "" {
		t.Fatal("previous summary not carried")
	}
}

func TestPrepareCompactionNothingToDo(t *testing.T) {
	// A single short entry: the cut keeps everything, so nothing is
	// compressible.
	entries := []SessionEntry{compactionSessionEntry("only", "user", "hi")}
	if got := PrepareCompaction(entries, DefaultCompactionSettings); got != nil && len(got.MessagesToSummarize) == 0 && len(got.TurnPrefixMessages) == 0 {
		t.Fatalf("preparation = %+v", got)
	}
}

func TestEstimateProjectedContextTokensFallsBackToEstimate(t *testing.T) {
	// No provider usage present: the estimate sums the messages.
	projection := &SessionProjection{
		Entries: []ProjectedSessionEntry{
			{
				SourceEntry: compactionSessionEntry("a", "user", strings.Repeat("word ", 100)),
				Messages:    []interface{}{&ai.UserMessage{Content: ai.StringContent(strings.Repeat("word ", 100))}},
			},
		},
	}
	estimate := EstimateProjectedContextTokens(projection, nil)
	if estimate.Tokens <= 0 {
		t.Fatalf("estimate = %+v", estimate)
	}
	if estimate.LastUsageIndex != -1 {
		t.Fatalf("last usage = %d", estimate.LastUsageIndex)
	}
}

func TestEstimateProjectedContextTokensNilProjection(t *testing.T) {
	estimate := EstimateProjectedContextTokens(nil, nil)
	if estimate.Tokens != 0 || estimate.LastUsageIndex != -1 {
		t.Fatalf("estimate = %+v", estimate)
	}
}
