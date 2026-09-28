package ai

// api_transform_messages_test.go ports the transform-messages essentials:
// thinking/text handling for cross-model turns, tool-call id normalization,
// orphaned tool calls, and synthetic tool results.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func transformFixture() (*Model, []Message) {
	model := &Model{ID: "m1", API: "anthropic-messages", Provider: "p1", Input: []string{"text"}}
	messages := []Message{
		&UserMessage{Content: StringContent("Hello"), TimestampMs: 1},
		&AssistantMessage{
			Content: []ContentBlock{
				ThinkingContent{Thinking: "same model thinking", ThinkingSignature: strPtrOf("sig")},
				ThinkingContent{Thinking: "", ThinkingSignature: strPtrOf("sig2")},
				ThinkingContent{Thinking: "cross model thinking", ThinkingSignature: strPtrOf("sig3")},
				TextContent{Text: "answer", TextSignature: strPtrOf("tsig")},
				&ToolCall{ID: "bad|id with spaces++", Name: "tool", Arguments: jsonx.NewObj(), ThoughtSignature: strPtrOf("thought")},
			},
			API: "anthropic-messages", Provider: "p1", Model: "m2",
			StopReason: StopToolUse, TimestampMs: 2,
		},
		&UserMessage{Content: StringContent("next"), TimestampMs: 3},
		&AssistantMessage{
			Content: []ContentBlock{TextContent{Text: "aborted mid-turn"}},
			API:     "anthropic-messages", Provider: "p1", Model: "m1",
			StopReason: StopAborted, TimestampMs: 4,
		},
		&UserMessage{Content: StringContent("after"), TimestampMs: 5},
		&AssistantMessage{
			Content: []ContentBlock{&ToolCall{ID: "orphan", Name: "lonely", Arguments: jsonx.NewObj()}},
			API:     "anthropic-messages", Provider: "p1", Model: "m1",
			StopReason: StopToolUse, TimestampMs: 6,
		},
	}
	return model, messages
}

func TestTransformMessagesCrossModelHandling(t *testing.T) {
	model, messages := transformFixture()
	transformed := TransformMessages(messages, model, func(id string, _ *Model, _ *AssistantMessage) string {
		return normalizeAnthropicToolCallId(id)
	})

	// user, assistant(m2: no thinking sigs, text without sig, normalized tool id),
	// synthetic result for its orphaned call, user("next"), the aborted turn is
	// dropped entirely, user("after"), assistant(orphan), synthetic result.
	roles := []string{}
	for _, msg := range transformed {
		roles = append(roles, msg.Role())
	}
	want := "user,assistant,toolResult,user,user,assistant,toolResult"
	if strings.Join(roles, ",") != want {
		t.Fatalf("unexpected roles: %v (got %d messages)", roles, len(transformed))
	}

	assistant := transformed[1].(*AssistantMessage)
	// Cross-model: both thinking blocks become plain text (empty one dropped),
	// then the original text, then the normalized tool call.
	if len(assistant.Content) != 4 {
		t.Fatalf("expected text+text+text+toolCall, got %d blocks", len(assistant.Content))
	}
	first, isText := assistant.Content[0].(TextContent)
	if !isText || first.Text != "same model thinking" {
		t.Fatalf("expected thinking converted to text, got %#v", assistant.Content[0])
	}
	second, isText := assistant.Content[1].(TextContent)
	if !isText || second.Text != "cross model thinking" {
		t.Fatalf("unexpected second text: %#v", assistant.Content[1])
	}
	third, isText := assistant.Content[2].(TextContent)
	if !isText || third.Text != "answer" || third.TextSignature != nil {
		t.Fatalf("cross-model text must lose its signature: %#v", assistant.Content[2])
	}
	// Tool call id normalized and thoughtSignature dropped.
	call, ok := assistant.Content[3].(*ToolCall)
	if !ok {
		t.Fatalf("expected tool call, got %#v", assistant.Content[3])
	}
	if call.ID != "bad_id_with_spaces__" {
		t.Fatalf("unexpected normalized id: %q", call.ID)
	}
	if call.ThoughtSignature != nil {
		t.Fatal("cross-model tool call must lose thoughtSignature")
	}
}

func TestTransformMessagesSyntheticToolResults(t *testing.T) {
	model, messages := transformFixture()
	transformed := TransformMessages(messages, model, nil)

	// The orphaned "lonely" tool call gets a synthetic error result.
	last := transformed[len(transformed)-1]
	result, ok := last.(*ToolResultMessage)
	if !ok {
		t.Fatalf("expected trailing synthetic toolResult, got %s", last.Role())
	}
	if result.ToolCallID != "orphan" || result.ToolName != "lonely" || !result.IsError {
		t.Fatalf("unexpected synthetic result: %+v", result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected placeholder content, got %d blocks", len(result.Content))
	}
	if text, ok := result.Content[0].(TextContent); !ok || text.Text != "No result provided" {
		t.Fatalf("unexpected placeholder: %#v", result.Content[0])
	}
}

func TestTransformMessagesSameModelKeepsSignatures(t *testing.T) {
	model := &Model{ID: "m1", API: "anthropic-messages", Provider: "p1", Input: []string{"text"}}
	messages := []Message{
		&UserMessage{Content: StringContent("hi"), TimestampMs: 1},
		&AssistantMessage{
			Content: []ContentBlock{
				ThinkingContent{Thinking: "t", ThinkingSignature: strPtrOf("sig")},
				TextContent{Text: "a", TextSignature: strPtrOf("tsig")},
			},
			API: "anthropic-messages", Provider: "p1", Model: "m1",
			StopReason: StopStop, TimestampMs: 2,
		},
	}
	transformed := TransformMessages(messages, model, nil)
	assistant := transformed[1].(*AssistantMessage)
	thinking := assistant.Content[0].(ThinkingContent)
	if thinking.ThinkingSignature == nil || *thinking.ThinkingSignature != "sig" {
		t.Fatal("same-model thinking keeps its signature")
	}
	text := assistant.Content[1].(TextContent)
	if text.TextSignature == nil || *text.TextSignature != "tsig" {
		t.Fatal("same-model text keeps its signature")
	}
}

func TestTransformMessagesImageDowngrade(t *testing.T) {
	model := &Model{ID: "m1", API: "openai-completions", Provider: "p1", Input: []string{"text"}}
	messages := []Message{
		&UserMessage{
			Content: BlocksContent(
				TextContent{Text: "look"},
				ImageContent{Data: "abc", MimeType: "image/png"},
			),
			TimestampMs: 1,
		},
	}
	transformed := TransformMessages(messages, model, nil)
	user := transformed[0].(*UserMessage)
	if user.Content.IsText {
		t.Fatal("expected blocks content")
	}
	if len(user.Content.Blocks) != 2 {
		t.Fatalf("expected image replaced by placeholder, got %d blocks", len(user.Content.Blocks))
	}
	placeholder, ok := user.Content.Blocks[1].(TextContent)
	if !ok || placeholder.Text != nonVisionUserImagePlaceholder {
		t.Fatalf("expected placeholder, got %#v", user.Content.Blocks[1])
	}
}
