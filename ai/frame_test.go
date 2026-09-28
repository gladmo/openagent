package ai

// Ports of pi/packages/ai/test/assistant-message-frame.test.ts (the pure
// encoder/reducer subset; provider stream round-trip tests are out of the
// closure).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func seedMessage() *AssistantMessage {
	return &AssistantMessage{
		Content:     []ContentBlock{},
		API:         "test-api",
		Provider:    "test-provider",
		Model:       "test-model",
		Usage:       Usage{},
		StopReason:  StopPending,
		TimestampMs: 1,
	}
}

func mustFrame(t *testing.T, encoder *AssistantMessageFrameEncoder, event AssistantMessageEvent) AssistantMessageFrame {
	t.Helper()
	converted := encoder.Encode(event)
	if converted == nil {
		t.Fatalf("Expected %s event to produce a frame", event.EventType())
	}
	return converted
}

func TestFrameAuthoritativeTextEnd(t *testing.T) {
	partial := seedMessage()
	encoder := NewAssistantMessageFrameEncoder()
	frames := []AssistantMessageFrame{mustFrame(t, encoder, &EventStart{Partial: partial})}
	partial.Content = append(partial.Content, TextContent{Text: "Hello "})
	frames = append(frames, mustFrame(t, encoder, &EventTextStart{ContentIndex: 0, Partial: partial}))
	sig := "sig-text"
	partial.Content[0] = TextContent{Text: "Hello world", TextSignature: &sig}
	frames = append(frames,
		mustFrame(t, encoder, &EventTextDelta{ContentIndex: 0, Delta: "incorrect", Partial: partial}),
		mustFrame(t, encoder, &EventTextEnd{ContentIndex: 0, Content: "Hello world", Partial: partial}),
	)
	last := frames[len(frames)-1].(*FrameTextEnd)
	if last.Content != "Hello world" || last.TextSignature == nil || *last.TextSignature != "sig-text" {
		t.Fatalf("last frame = %+v", last)
	}
	reduced := ReduceAssistantMessageFrames(frames)
	text := reduced.Content[0].(TextContent)
	if text.Text != "Hello world" || text.TextSignature == nil || *text.TextSignature != "sig-text" {
		t.Fatalf("reduced = %+v", text)
	}
}

func TestFramePreservesThinkingLevel(t *testing.T) {
	partial := seedMessage()
	high := "high"
	partial.ProviderThinkingLevel = &high
	encoder := NewAssistantMessageFrameEncoder()
	start := mustFrame(t, encoder, &EventStart{Partial: partial}).(*FrameStart)
	if start.Partial.ProviderThinkingLevel == nil || *start.Partial.ProviderThinkingLevel != "high" {
		t.Fatal("providerThinkingLevel lost")
	}
	if reduced := ReduceAssistantMessageFrames([]AssistantMessageFrame{start}); reduced.ProviderThinkingLevel == nil || *reduced.ProviderThinkingLevel != "high" {
		t.Fatal("reducer lost providerThinkingLevel")
	}
}

func TestFrameThinkingMetadataAndRedaction(t *testing.T) {
	partial := seedMessage()
	encoder := NewAssistantMessageFrameEncoder()
	frames := []AssistantMessageFrame{mustFrame(t, encoder, &EventStart{Partial: partial})}
	sigStart, redacted := "encrypted-start", true
	partial.Content = append(partial.Content, ThinkingContent{Thinking: "[redacted]", ThinkingSignature: &sigStart, Redacted: &redacted})
	frames = append(frames, mustFrame(t, encoder, &EventThinkingStart{ContentIndex: 0, Partial: partial}))
	sigFinal := "encrypted-final"
	partial.Content[0] = ThinkingContent{Thinking: "[redacted]", ThinkingSignature: &sigFinal, Redacted: &redacted}
	frames = append(frames, mustFrame(t, encoder, &EventThinkingEnd{ContentIndex: 0, Content: "[redacted]", Partial: partial}))
	last := frames[len(frames)-1].(*FrameThinkingEnd)
	if last.ThinkingSignature == nil || *last.ThinkingSignature != "encrypted-final" || last.Redacted == nil || !*last.Redacted {
		t.Fatalf("last = %+v", last)
	}
	thinking := ReduceAssistantMessageFrames(frames).Content[0].(ThinkingContent)
	if thinking.ThinkingSignature == nil || *thinking.ThinkingSignature != "encrypted-final" || thinking.Redacted == nil || !*thinking.Redacted {
		t.Fatalf("reduced = %+v", thinking)
	}
}

func TestFrameUnfinishedToolJSONThenAuthoritativeEnd(t *testing.T) {
	initialFrames := []AssistantMessageFrame{
		&FrameStart{Partial: seedMessage()},
		&FrameToolCallStart{ContentIndex: 0, ToolCall: &ToolCall{ID: "initial-id", Name: "write", Arguments: jsonx.NewObj()}},
		&FrameToolCallDelta{ContentIndex: 0, Delta: `{"path":"READ`},
	}
	reduced := ReduceAssistantMessageFrames(initialFrames)
	call := reduced.Content[0].(*ToolCall)
	if path, _ := call.Arguments.Get("path"); path != "READ" {
		t.Fatalf("initial args = %s", jsonx.Stringify(call.Arguments))
	}
	completeFrames := append(initialFrames,
		&FrameToolCallDelta{ContentIndex: 0, Delta: `ME.md","lines":[1,2]}`},
		&FrameToolCallEnd{
			ContentIndex: 0, ID: "final-id", Name: "write_file",
			Arguments:        jsonx.ObjFrom("path", "final.md", "lines", []any{float64(3)}),
			ThoughtSignature: strp("thought"), Namespace: strp("files"),
		},
	)
	final := ReduceAssistantMessageFrames(completeFrames).Content[0].(*ToolCall)
	if final.ID != "final-id" || final.Name != "write_file" ||
		jsonx.Stringify(final.Arguments) != `{"path":"final.md","lines":[3]}` ||
		final.ThoughtSignature == nil || *final.ThoughtSignature != "thought" ||
		final.Namespace == nil || *final.Namespace != "files" {
		t.Fatalf("final = %+v args=%s", final, jsonx.Stringify(final.Arguments))
	}
}

func TestFrameReconcilesQueuedTextAgainstLivePartial(t *testing.T) {
	partial := seedMessage()
	events := []AssistantMessageEvent{&EventStart{Partial: partial}}
	text := TextContent{Text: ""}
	partial.Content = append(partial.Content, text)
	events = append(events, &EventTextStart{ContentIndex: 0, Partial: partial})
	for _, delta := range []string{"Hel", "lo", " ", "world"} {
		text.Text += delta
		events = append(events, &EventTextDelta{ContentIndex: 0, Delta: delta, Partial: partial})
	}
	// Encoding happens after all events accumulated (shared partial fully
	// advanced), which is exactly what the covered-chars offsets handle.
	partial.Content[0] = text
	encoder := NewAssistantMessageFrameEncoder()
	var frames []AssistantMessageFrame
	for _, event := range events {
		if encoded := encoder.Encode(event); encoded != nil {
			frames = append(frames, encoded)
		}
	}
	types := frameTypes(frames)
	if strings.Join(types, ",") != "start,text_start" {
		t.Fatalf("types = %v", types)
	}
	if start := frames[0].(*FrameStart); len(start.Partial.Content) != 0 || start.Partial.StopReason != StopPending {
		t.Fatalf("start frame partial = %+v", start.Partial)
	}
	reduced := ReduceAssistantMessageFrames(frames)
	if got := reduced.Content[0].(TextContent).Text; got != "Hello world" {
		t.Fatalf("reduced text = %q", got)
	}
}

func frameTypes(frames []AssistantMessageFrame) []string {
	out := make([]string, len(frames))
	for i, f := range frames {
		out[i] = f.FrameType()
	}
	return out
}

func TestFrameTrimsCoveredPrefixInsideDelta(t *testing.T) {
	partial := seedMessage()
	encoder := NewAssistantMessageFrameEncoder()
	frames := []AssistantMessageFrame{mustFrame(t, encoder, &EventStart{Partial: partial})}
	text := TextContent{Text: "Hel"}
	partial.Content = append(partial.Content, text)
	frames = append(frames, mustFrame(t, encoder, &EventTextStart{ContentIndex: 0, Partial: partial}))
	if encoded := encoder.Encode(&EventTextDelta{ContentIndex: 0, Delta: "He", Partial: partial}); encoded != nil {
		t.Fatal("covered delta produced a frame")
	}
	remainder := encoder.Encode(&EventTextDelta{ContentIndex: 0, Delta: "llo", Partial: partial})
	if remainder == nil {
		t.Fatal("expected uncovered delta frame")
	}
	frames = append(frames, remainder)
	if d := remainder.(*FrameTextDelta); d.Delta != "lo" {
		t.Fatalf("delta = %q", d.Delta)
	}
	if got := ReduceAssistantMessageFrames(frames).Content[0].(TextContent).Text; got != "Hello" {
		t.Fatalf("reduced = %q", got)
	}
}

func TestFrameCheckpointsQueuedToolJSON(t *testing.T) {
	partial := seedMessage()
	toolCall := &ToolCall{ID: "call", Name: "write", Arguments: jsonx.NewObj()}
	events := []AssistantMessageEvent{&EventStart{Partial: partial}}
	partial.Content = append(partial.Content, toolCall)
	events = append(events, &EventToolCallStart{ContentIndex: 0, Partial: partial})
	toolCall.Arguments = jsonx.ObjFrom("path", "README.md")
	events = append(events,
		&EventToolCallDelta{ContentIndex: 0, Delta: `{"path":"READ`, Partial: partial},
		&EventToolCallDelta{ContentIndex: 0, Delta: `ME.md"}`, Partial: partial},
	)
	encoder := NewAssistantMessageFrameEncoder()
	var frames []AssistantMessageFrame
	for _, event := range events {
		if encoded := encoder.Encode(event); encoded != nil {
			frames = append(frames, encoded)
		}
	}
	if types := strings.Join(frameTypes(frames), ","); types != "start,toolcall_start,toolcall_checkpoint" {
		t.Fatalf("types = %s", types)
	}
	last := frames[len(frames)-1].(*FrameToolCallCheckpoint)
	if last.JSON != `{"path":"README.md"}` {
		t.Fatalf("checkpoint json = %s", last.JSON)
	}
	reduced := ReduceAssistantMessageFrames(frames)
	call := reduced.Content[0].(*ToolCall)
	if jsonx.Stringify(call.Arguments) != `{"path":"README.md"}` {
		t.Fatalf("reduced args = %s", jsonx.Stringify(call.Arguments))
	}
}

func TestFrameResumesLegacyGrammarFromInitialArguments(t *testing.T) {
	partial := seedMessage()
	encoder := NewAssistantMessageFrameEncoder()
	frames := []AssistantMessageFrame{mustFrame(t, encoder, &EventStart{Partial: partial})}
	toolCall := &ToolCall{ID: "call", Name: "bash", Arguments: jsonx.ObjFrom("input", "a")}
	partial.Content = append(partial.Content, toolCall)
	frames = append(frames, mustFrame(t, encoder, &EventToolCallStart{ContentIndex: 0, Partial: partial}))
	toolCall.Arguments = jsonx.ObjFrom("input", "ab")
	frames = append(frames, mustFrame(t, encoder, &EventToolCallDelta{ContentIndex: 0, Delta: `{"input":"ab`, Partial: partial}))
	toolCall.Arguments = jsonx.ObjFrom("input", "abc")
	frames = append(frames, mustFrame(t, encoder, &EventToolCallDelta{ContentIndex: 0, Delta: `c"}`, Partial: partial}))

	rest := frames[2:]
	if len(rest) != 2 {
		t.Fatalf("frames = %v", frameTypes(frames))
	}
	if cp, ok := rest[0].(*FrameToolCallCheckpoint); !ok || cp.JSON != `{"input":"ab` {
		t.Fatalf("first = %+v", rest[0])
	}
	if d, ok := rest[1].(*FrameToolCallDelta); !ok || d.Delta != `c"}` {
		t.Fatalf("second = %+v", rest[1])
	}
	reduced := ReduceAssistantMessageFrames(frames)
	call := reduced.Content[0].(*ToolCall)
	if jsonx.Stringify(call.Arguments) != `{"input":"abc"}` {
		t.Fatalf("reduced = %s", jsonx.Stringify(call.Arguments))
	}
}

func TestFrameStreamsCompactFromEmptyStart(t *testing.T) {
	partial := seedMessage()
	encoder := NewAssistantMessageFrameEncoder()
	frames := []AssistantMessageFrame{mustFrame(t, encoder, &EventStart{Partial: partial})}
	toolCall := &ToolCall{ID: "call", Name: "bash", Arguments: jsonx.NewObj()}
	partial.Content = append(partial.Content, toolCall)
	frames = append(frames, mustFrame(t, encoder, &EventToolCallStart{ContentIndex: 0, Partial: partial}))
	toolCall.Arguments = jsonx.ObjFrom("command", "ls -la /tmp")
	frames = append(frames, mustFrame(t, encoder, &EventToolCallDelta{ContentIndex: 0, Delta: `{"command":"ls -la /tmp"}`, Partial: partial}))
	last, ok := frames[len(frames)-1].(*FrameToolCallDelta)
	if !ok || last.Delta != `{"command":"ls -la /tmp"}` {
		t.Fatalf("last = %+v", frames[len(frames)-1])
	}
	reduced := ReduceAssistantMessageFrames(frames)
	call := reduced.Content[0].(*ToolCall)
	if cmd, _ := call.Arguments.Get("command"); cmd != "ls -la /tmp" {
		t.Fatalf("args = %s", jsonx.Stringify(call.Arguments))
	}
}

func TestFrameAcceptsPreGenerationErrorRejectsUpdatesBeforeStart(t *testing.T) {
	// Pre-generation error is terminal.
	encoder := NewAssistantMessageFrameEncoder()
	if f := encoder.Encode(&EventError{Reason: StopError, Error: seedMessage()}); f != nil {
		t.Fatal("error produced a frame")
	}
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s did not panic", name)
			}
		}()
		fn()
	}
	partial := seedMessage()
	partial.Content = append(partial.Content, TextContent{Text: "x"})
	mustPanic("text before start", func() {
		NewAssistantMessageFrameEncoder().Encode(&EventTextStart{ContentIndex: 0, Partial: partial})
	})
	mustPanic("done before start", func() {
		NewAssistantMessageFrameEncoder().Encode(&EventDone{Reason: StopStop, Message: partial})
	})
}

func TestFrameTerminalExcluded(t *testing.T) {
	partial := seedMessage()
	encoder := NewAssistantMessageFrameEncoder()
	frames := []AssistantMessageFrame{mustFrame(t, encoder, &EventStart{Partial: partial})}
	if f := encoder.Encode(&EventDone{Reason: StopStop, Message: partial}); f != nil {
		t.Fatal("done produced a frame")
	}
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s did not panic", name)
			}
		}()
		fn()
	}
	mustPanic("event after terminal", func() {
		encoder.Encode(&EventTextStart{ContentIndex: 0, Partial: partial})
	})
	if reduced := ReduceAssistantMessageFrames(frames); reduced.StopReason != StopPending {
		t.Fatalf("reduced stopReason = %s", reduced.StopReason)
	}
}

func TestFrameNoStartReturnsNil(t *testing.T) {
	if ReduceAssistantMessageFrames([]AssistantMessageFrame{
		&FrameTextDelta{ContentIndex: 0, Delta: "x"},
	}) != nil {
		t.Fatal("expected nil")
	}
}

func TestFrameRejectsInvalidSequences(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s did not panic", name)
			}
		}()
		fn()
	}
	// Frame before start recorded, then start arrives: rejects.
	mustPanic("frame before start", func() {
		ReduceAssistantMessageFrames([]AssistantMessageFrame{
			&FrameTextDelta{ContentIndex: 0, Delta: "x"},
			&FrameStart{Partial: seedMessage()},
		})
	})
	// Duplicate start.
	mustPanic("duplicate start", func() {
		ReduceAssistantMessageFrames([]AssistantMessageFrame{
			&FrameStart{Partial: seedMessage()},
			&FrameStart{Partial: seedMessage()},
		})
	})
	// Duplicate end.
	partial := seedMessage()
	encoder := NewAssistantMessageFrameEncoder()
	var frames []AssistantMessageFrame
	frames = append(frames, mustFrame(t, encoder, &EventStart{Partial: partial}))
	partial.Content = append(partial.Content, TextContent{Text: "x"})
	frames = append(frames, mustFrame(t, encoder, &EventTextStart{ContentIndex: 0, Partial: partial}))
	frames = append(frames, mustFrame(t, encoder, &EventTextEnd{ContentIndex: 0, Content: "x", Partial: partial}))
	mustPanic("duplicate end", func() {
		ReduceAssistantMessageFrames(append(frames, &FrameTextEnd{ContentIndex: 0, Content: "y"}))
	})
	// Index gap.
	mustPanic("index gap", func() {
		ReduceAssistantMessageFrames([]AssistantMessageFrame{
			&FrameStart{Partial: seedMessage()},
			&FrameTextStart{ContentIndex: 2, Content: TextContent{Text: "x"}},
		})
	})
}

func TestFrameWhitelistsPublicBlockFields(t *testing.T) {
	partial := seedMessage()
	encoder := NewAssistantMessageFrameEncoder()
	frames := []AssistantMessageFrame{mustFrame(t, encoder, &EventStart{Partial: partial})}
	sig := "s"
	call := &ToolCall{ID: "i", Name: "n", Arguments: jsonx.ObjFrom("a", float64(1)), ThoughtSignature: &sig, Namespace: &sig}
	partial.Content = append(partial.Content, call)
	frames = append(frames, mustFrame(t, encoder, &EventToolCallStart{ContentIndex: 0, Partial: partial}))
	start := frames[1].(*FrameToolCallStart)
	encoded := jsonStringify(blockToJSON(start.ToolCall))
	want := `{"type":"toolCall","id":"i","name":"n","arguments":{"a":1},"thoughtSignature":"s","namespace":"s"}`
	if encoded != want {
		t.Fatalf("encoded = %s", encoded)
	}
	// Reduction is pure: source frames unchanged.
	partialBefore := jsonStringify(blockToJSON(call))
	ReduceAssistantMessageFrames(frames)
	if jsonStringify(blockToJSON(call)) != partialBefore {
		t.Fatal("source frames mutated")
	}
}

func TestFrameInterleavedStreamsByContentIndex(t *testing.T) {
	partial := seedMessage()
	encoder := NewAssistantMessageFrameEncoder()
	frames := []AssistantMessageFrame{mustFrame(t, encoder, &EventStart{Partial: partial})}
	partial.Content = append(partial.Content, TextContent{Text: ""}, ThinkingContent{Thinking: ""})
	frames = append(frames, mustFrame(t, encoder, &EventTextStart{ContentIndex: 0, Partial: partial}))
	frames = append(frames, mustFrame(t, encoder, &EventThinkingStart{ContentIndex: 1, Partial: partial}))
	frames = append(frames, mustFrame(t, encoder, &EventTextDelta{ContentIndex: 0, Delta: "a", Partial: partial}))
	frames = append(frames, mustFrame(t, encoder, &EventThinkingDelta{ContentIndex: 1, Delta: "b", Partial: partial}))
	frames = append(frames, mustFrame(t, encoder, &EventTextEnd{ContentIndex: 0, Content: "a", Partial: partial}))
	frames = append(frames, mustFrame(t, encoder, &EventThinkingEnd{ContentIndex: 1, Content: "b", Partial: partial}))
	reduced := ReduceAssistantMessageFrames(frames)
	if len(reduced.Content) != 2 {
		t.Fatalf("content = %d", len(reduced.Content))
	}
	if text := reduced.Content[0].(TextContent).Text; text != "a" {
		t.Fatalf("text = %q", text)
	}
	if thinking := reduced.Content[1].(ThinkingContent).Thinking; thinking != "b" {
		t.Fatalf("thinking = %q", thinking)
	}
}
