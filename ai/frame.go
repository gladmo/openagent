package ai

// frame.go ports utils/assistant-message-frame.ts.

import (
	"fmt"

	"github.com/gladmo/openagent/jsonx"
)

// AssistantMessageFrame is the compact, replayable progress union.
type AssistantMessageFrame interface{ FrameType() string }

// FrameStart is {type:"start", partial}.
type FrameStart struct{ Partial *AssistantMessage }

// FrameType implements AssistantMessageFrame.
func (*FrameStart) FrameType() string { return "start" }

// FrameTextStart is {type:"text_start", contentIndex, content}.
type FrameTextStart struct {
	ContentIndex int
	Content      TextContent
}

// FrameType implements AssistantMessageFrame.
func (*FrameTextStart) FrameType() string { return "text_start" }

// FrameTextDelta is {type:"text_delta", contentIndex, delta}.
type FrameTextDelta struct {
	ContentIndex int
	Delta        string
}

// FrameType implements AssistantMessageFrame.
func (*FrameTextDelta) FrameType() string { return "text_delta" }

// FrameTextEnd is {type:"text_end", contentIndex, content, textSignature?}.
type FrameTextEnd struct {
	ContentIndex  int
	Content       string
	TextSignature *string
}

// FrameType implements AssistantMessageFrame.
func (*FrameTextEnd) FrameType() string { return "text_end" }

// FrameThinkingStart is {type:"thinking_start", contentIndex, content}.
type FrameThinkingStart struct {
	ContentIndex int
	Content      ThinkingContent
}

// FrameType implements AssistantMessageFrame.
func (*FrameThinkingStart) FrameType() string { return "thinking_start" }

// FrameThinkingDelta is {type:"thinking_delta", contentIndex, delta}.
type FrameThinkingDelta struct {
	ContentIndex int
	Delta        string
}

// FrameType implements AssistantMessageFrame.
func (*FrameThinkingDelta) FrameType() string { return "thinking_delta" }

// FrameThinkingEnd is {type:"thinking_end", contentIndex, content,
// thinkingSignature?, redacted?}.
type FrameThinkingEnd struct {
	ContentIndex      int
	Content           string
	ThinkingSignature *string
	Redacted          *bool
}

// FrameType implements AssistantMessageFrame.
func (*FrameThinkingEnd) FrameType() string { return "thinking_end" }

// FrameToolCallStart is {type:"toolcall_start", contentIndex, toolCall}.
type FrameToolCallStart struct {
	ContentIndex int
	ToolCall     *ToolCall
}

// FrameType implements AssistantMessageFrame.
func (*FrameToolCallStart) FrameType() string { return "toolcall_start" }

// FrameToolCallCheckpoint is {type:"toolcall_checkpoint", contentIndex, json}.
type FrameToolCallCheckpoint struct {
	ContentIndex int
	JSON         string
}

// FrameType implements AssistantMessageFrame.
func (*FrameToolCallCheckpoint) FrameType() string { return "toolcall_checkpoint" }

// FrameToolCallDelta is {type:"toolcall_delta", contentIndex, delta}.
type FrameToolCallDelta struct {
	ContentIndex int
	Delta        string
}

// FrameType implements AssistantMessageFrame.
func (*FrameToolCallDelta) FrameType() string { return "toolcall_delta" }

// FrameToolCallEnd is {type:"toolcall_end", ...}.
type FrameToolCallEnd struct {
	ContentIndex     int
	ID               string
	Name             string
	Arguments        *jsonx.Obj
	ThoughtSignature *string
	Namespace        *string
}

// FrameType implements AssistantMessageFrame.
func (*FrameToolCallEnd) FrameType() string { return "toolcall_end" }

// FrameToJSON serializes a frame with TS field order.
func FrameToJSON(f AssistantMessageFrame) *jsonx.Obj {
	o := jsonx.NewObj()
	switch t := f.(type) {
	case *FrameStart:
		o.Set("type", "start")
		o.Set("partial", MessageToJSON(t.Partial))
	case *FrameTextStart:
		o.Set("type", "text_start")
		o.Set("contentIndex", float64(t.ContentIndex))
		o.Set("content", blockToJSON(t.Content))
	case *FrameTextDelta:
		o.Set("type", "text_delta")
		o.Set("contentIndex", float64(t.ContentIndex))
		o.Set("delta", t.Delta)
	case *FrameTextEnd:
		o.Set("type", "text_end")
		o.Set("contentIndex", float64(t.ContentIndex))
		o.Set("content", t.Content)
		if t.TextSignature != nil {
			o.Set("textSignature", *t.TextSignature)
		}
	case *FrameThinkingStart:
		o.Set("type", "thinking_start")
		o.Set("contentIndex", float64(t.ContentIndex))
		o.Set("content", blockToJSON(t.Content))
	case *FrameThinkingDelta:
		o.Set("type", "thinking_delta")
		o.Set("contentIndex", float64(t.ContentIndex))
		o.Set("delta", t.Delta)
	case *FrameThinkingEnd:
		o.Set("type", "thinking_end")
		o.Set("contentIndex", float64(t.ContentIndex))
		o.Set("content", t.Content)
		if t.ThinkingSignature != nil {
			o.Set("thinkingSignature", *t.ThinkingSignature)
		}
		if t.Redacted != nil {
			o.Set("redacted", *t.Redacted)
		}
	case *FrameToolCallStart:
		o.Set("type", "toolcall_start")
		o.Set("contentIndex", float64(t.ContentIndex))
		o.Set("toolCall", blockToJSON(t.ToolCall))
	case *FrameToolCallCheckpoint:
		o.Set("type", "toolcall_checkpoint")
		o.Set("contentIndex", float64(t.ContentIndex))
		o.Set("json", t.JSON)
	case *FrameToolCallDelta:
		o.Set("type", "toolcall_delta")
		o.Set("contentIndex", float64(t.ContentIndex))
		o.Set("delta", t.Delta)
	case *FrameToolCallEnd:
		o.Set("type", "toolcall_end")
		o.Set("contentIndex", float64(t.ContentIndex))
		o.Set("id", t.ID)
		o.Set("name", t.Name)
		args := t.Arguments
		if args == nil {
			args = jsonx.NewObj()
		}
		o.Set("arguments", args)
		if t.ThoughtSignature != nil {
			o.Set("thoughtSignature", *t.ThoughtSignature)
		}
		if t.Namespace != nil {
			o.Set("namespace", *t.Namespace)
		}
	}
	return o
}

// FrameFromJSON decodes a frame from its jsonx object form.
func FrameFromJSON(v any) (AssistantMessageFrame, error) {
	obj, ok := v.(*jsonx.Obj)
	if !ok {
		return nil, fmt.Errorf("frame is not an object")
	}
	typ := stringField(obj, "type")
	contentIndex := int(floatField(obj, "contentIndex"))
	switch typ {
	case "start":
		if partial, err := MessageFromJSON(obj.MustGet("partial")); err != nil {
			return nil, err
		} else {
			return &FrameStart{Partial: partial.(*AssistantMessage)}, nil
		}
	case "text_start":
		if b, ok := blockFromJSON(obj.MustGet("content")); ok {
			return &FrameTextStart{ContentIndex: contentIndex, Content: b.(TextContent)}, nil
		}
		return nil, fmt.Errorf("text_start frame content")
	case "text_delta":
		return &FrameTextDelta{ContentIndex: contentIndex, Delta: stringField(obj, "delta")}, nil
	case "text_end":
		f := &FrameTextEnd{ContentIndex: contentIndex, Content: stringField(obj, "content")}
		if sig, ok := obj.Get("textSignature"); ok {
			f.TextSignature = strPtr(sig)
		}
		return f, nil
	case "thinking_start":
		if b, ok := blockFromJSON(obj.MustGet("content")); ok {
			return &FrameThinkingStart{ContentIndex: contentIndex, Content: b.(ThinkingContent)}, nil
		}
		return nil, fmt.Errorf("thinking_start frame content")
	case "thinking_delta":
		return &FrameThinkingDelta{ContentIndex: contentIndex, Delta: stringField(obj, "delta")}, nil
	case "thinking_end":
		f := &FrameThinkingEnd{ContentIndex: contentIndex, Content: stringField(obj, "content")}
		if sig, ok := obj.Get("thinkingSignature"); ok {
			f.ThinkingSignature = strPtr(sig)
		}
		if red, ok := obj.Get("redacted"); ok {
			if b, ok := red.(bool); ok {
				f.Redacted = &b
			}
		}
		return f, nil
	case "toolcall_start":
		if b, ok := blockFromJSON(obj.MustGet("toolCall")); ok {
			return &FrameToolCallStart{ContentIndex: contentIndex, ToolCall: b.(*ToolCall)}, nil
		}
		return nil, fmt.Errorf("toolcall_start frame toolCall")
	case "toolcall_checkpoint":
		return &FrameToolCallCheckpoint{ContentIndex: contentIndex, JSON: stringField(obj, "json")}, nil
	case "toolcall_delta":
		return &FrameToolCallDelta{ContentIndex: contentIndex, Delta: stringField(obj, "delta")}, nil
	case "toolcall_end":
		f := &FrameToolCallEnd{ContentIndex: contentIndex, ID: stringField(obj, "id"), Name: stringField(obj, "name")}
		if args, ok := obj.Get("arguments"); ok {
			if argsObj, ok := args.(*jsonx.Obj); ok {
				f.Arguments = argsObj
			}
		}
		if sig, ok := obj.Get("thoughtSignature"); ok {
			f.ThoughtSignature = strPtr(sig)
		}
		if ns, ok := obj.Get("namespace"); ok {
			f.Namespace = strPtr(ns)
		}
		return f, nil
	default:
		return nil, fmt.Errorf("unknown frame type: %s", typ)
	}
}

// ---------------------------------------------------------------------------
// Encoder
// ---------------------------------------------------------------------------

type encoderBlockState struct {
	kind string // "text" | "thinking" | "toolCall"
	// text/thinking
	coveredChars int
	deltaChars   int
	// toolCall
	caughtUp          bool
	catchupJSON       string
	snapshotArguments string
}

// AssistantMessageFrameEncoder encodes one assistant stream.
type AssistantMessageFrameEncoder struct {
	started  bool
	terminal bool
	blocks   map[int]*encoderBlockState
}

// NewAssistantMessageFrameEncoder creates an encoder.
func NewAssistantMessageFrameEncoder() *AssistantMessageFrameEncoder {
	return &AssistantMessageFrameEncoder{blocks: map[int]*encoderBlockState{}}
}

var emptyParsedToolArguments = serializedArguments(jsonx.NewObj())

func serializedArguments(argumentsValue *jsonx.Obj) string {
	return jsonStringify(argumentsValue)
}

func cloneStartMessage(message *AssistantMessage) *AssistantMessage {
	return &AssistantMessage{
		Content:               []ContentBlock{},
		API:                   message.API,
		Provider:              message.Provider,
		Model:                 message.Model,
		ResponseModel:         message.ResponseModel,
		ResponseID:            message.ResponseID,
		ProviderThinkingLevel: message.ProviderThinkingLevel,
		Diagnostics:           append([]AssistantMessageDiagnostic{}, message.Diagnostics...),
		Usage:                 *usageClone(&message.Usage),
		StopReason:            StopPending,
		TimestampMs:           message.TimestampMs,
	}
}

func usageClone(u *Usage) *Usage {
	clone := *u
	return &clone
}

func assertContentIndex(contentIndex int) {
	if contentIndex < 0 {
		panic(fmt.Sprintf("Invalid assistant message frame contentIndex: %d", contentIndex))
	}
}

func eventBlock(event AssistantMessageEvent, contentIndex int) ContentBlock {
	assertContentIndex(contentIndex)
	partial := eventPartial(event)
	if partial == nil || contentIndex >= len(partial.Content) {
		panic(fmt.Sprintf("%s event has no content block at index %d", event.EventType(), contentIndex))
	}
	return partial.Content[contentIndex]
}

func eventPartial(event AssistantMessageEvent) *AssistantMessage {
	switch t := event.(type) {
	case *EventStart:
		return t.Partial
	case *EventTextStart:
		return t.Partial
	case *EventTextDelta:
		return t.Partial
	case *EventTextEnd:
		return t.Partial
	case *EventThinkingStart:
		return t.Partial
	case *EventThinkingDelta:
		return t.Partial
	case *EventThinkingEnd:
		return t.Partial
	case *EventToolCallStart:
		return t.Partial
	case *EventToolCallDelta:
		return t.Partial
	case *EventToolCallEnd:
		return t.Partial
	default:
		return nil
	}
}

func eventContentIndex(event AssistantMessageEvent) int {
	switch t := event.(type) {
	case *EventTextStart:
		return t.ContentIndex
	case *EventTextDelta:
		return t.ContentIndex
	case *EventTextEnd:
		return t.ContentIndex
	case *EventThinkingStart:
		return t.ContentIndex
	case *EventThinkingDelta:
		return t.ContentIndex
	case *EventThinkingEnd:
		return t.ContentIndex
	case *EventToolCallStart:
		return t.ContentIndex
	case *EventToolCallDelta:
		return t.ContentIndex
	case *EventToolCallEnd:
		return t.ContentIndex
	default:
		return -1
	}
}

// isJSONPrefix reports whether snapshot is a JSON-prefix of current.
func isJSONPrefix(snapshot, current any) bool {
	if s, ok := snapshot.(string); ok {
		c, isStr := current.(string)
		return isStr && len(c) >= len(s) && c[:len(s)] == s
	}
	if sArr, ok := snapshot.([]any); ok {
		cArr, isArr := current.([]any)
		if !isArr || len(sArr) > len(cArr) {
			return false
		}
		for i := range sArr {
			if !isJSONPrefix(sArr[i], cArr[i]) {
				return false
			}
		}
		return true
	}
	sObj, sIsObj := snapshot.(*jsonx.Obj)
	if !sIsObj {
		// Object.is semantics: strict equality.
		return jsonxStrictEqual(snapshot, current)
	}
	cObj, cIsObj := current.(*jsonx.Obj)
	if !cIsObj {
		return false
	}
	for _, key := range sObj.Keys() {
		if !cObj.Has(key) {
			return false
		}
		if !isJSONPrefix(sObj.MustGet(key), cObj.MustGet(key)) {
			return false
		}
	}
	return true
}

func jsonxStrictEqual(a, b any) bool {
	switch a.(type) {
	case *jsonx.Obj, []any:
		return a == b // reference identity for containers, like Object.is
	}
	if af, ok := jsonx.ToFloat(a); ok {
		if bf, ok2 := jsonx.ToFloat(b); ok2 {
			return af == bf
		}
	}
	return a == b
}

// Encode converts one event into a frame (nil when nothing to persist).
func (e *AssistantMessageFrameEncoder) Encode(event AssistantMessageEvent) AssistantMessageFrame {
	if e.terminal {
		panic(fmt.Sprintf("Assistant message event %s follows a terminal event", event.EventType()))
	}
	switch t := event.(type) {
	case *EventStart:
		if e.started {
			panic("Assistant message stream contains more than one start event")
		}
		e.started = true
		return &FrameStart{Partial: cloneStartMessage(t.Partial)}
	case *EventDone:
		if !e.started {
			panic("Assistant message done event appears before start")
		}
		e.terminal = true
		return nil
	case *EventError:
		e.terminal = true
		return nil
	}
	if !e.started {
		panic(fmt.Sprintf("Assistant message %s event appears before start", event.EventType()))
	}

	switch t := event.(type) {
	case *EventTextStart:
		content := eventBlock(event, t.ContentIndex)
		text, ok := content.(TextContent)
		if !ok {
			panic(fmt.Sprintf("text_start event points to %s block at index %d", content.ContentType(), t.ContentIndex))
		}
		e.startBlock(t.ContentIndex, &encoderBlockState{kind: "text", coveredChars: len([]rune(text.Text))})
		return &FrameTextStart{ContentIndex: t.ContentIndex, Content: cloneTextContent(text)}
	case *EventTextDelta:
		return e.encodeTextDelta(t.ContentIndex, t.Delta, "text")
	case *EventTextEnd:
		content := eventBlock(event, t.ContentIndex)
		text, ok := content.(TextContent)
		if !ok {
			panic(fmt.Sprintf("text_end event points to %s block at index %d", content.ContentType(), t.ContentIndex))
		}
		e.endBlock(t.ContentIndex, "text")
		return &FrameTextEnd{ContentIndex: t.ContentIndex, Content: t.Content, TextSignature: text.TextSignature}
	case *EventThinkingStart:
		content := eventBlock(event, t.ContentIndex)
		thinking, ok := content.(ThinkingContent)
		if !ok {
			panic(fmt.Sprintf("thinking_start event points to %s block at index %d", content.ContentType(), t.ContentIndex))
		}
		e.startBlock(t.ContentIndex, &encoderBlockState{kind: "thinking", coveredChars: len([]rune(thinking.Thinking))})
		return &FrameThinkingStart{ContentIndex: t.ContentIndex, Content: cloneThinkingContent(thinking)}
	case *EventThinkingDelta:
		return e.encodeTextDelta(t.ContentIndex, t.Delta, "thinking")
	case *EventThinkingEnd:
		content := eventBlock(event, t.ContentIndex)
		thinking, ok := content.(ThinkingContent)
		if !ok {
			panic(fmt.Sprintf("thinking_end event points to %s block at index %d", content.ContentType(), t.ContentIndex))
		}
		e.endBlock(t.ContentIndex, "thinking")
		return &FrameThinkingEnd{
			ContentIndex:      t.ContentIndex,
			Content:           t.Content,
			ThinkingSignature: thinking.ThinkingSignature,
			Redacted:          thinking.Redacted,
		}
	case *EventToolCallStart:
		content := eventBlock(event, t.ContentIndex)
		call, ok := content.(*ToolCall)
		if !ok {
			panic(fmt.Sprintf("toolcall_start event points to %s block at index %d", content.ContentType(), t.ContentIndex))
		}
		snapshotArguments := serializedArguments(call.Arguments)
		caughtUp := snapshotArguments == emptyParsedToolArguments
		snapshot := ""
		if !caughtUp {
			snapshot = snapshotArguments
		}
		e.startBlock(t.ContentIndex, &encoderBlockState{
			kind: "toolCall", caughtUp: caughtUp, snapshotArguments: snapshot,
		})
		return &FrameToolCallStart{ContentIndex: t.ContentIndex, ToolCall: cloneToolCall(call)}
	case *EventToolCallDelta:
		state := e.block(t.ContentIndex, "toolCall")
		if state.caughtUp {
			if len(t.Delta) == 0 {
				return nil
			}
			return &FrameToolCallDelta{ContentIndex: t.ContentIndex, Delta: t.Delta}
		}
		state.catchupJSON += t.Delta
		argumentsValue := ParseStreamingJSONObject(state.catchupJSON)
		if serializedArguments(argumentsValue) != state.snapshotArguments {
			snapshotArguments := ParseStreamingJSONObject(state.snapshotArguments)
			if !isJSONPrefix(snapshotArguments, argumentsValue) {
				return nil
			}
		}
		state.caughtUp = true
		state.snapshotArguments = ""
		json := state.catchupJSON
		state.catchupJSON = ""
		if len(json) == 0 {
			return nil
		}
		return &FrameToolCallCheckpoint{ContentIndex: t.ContentIndex, JSON: json}
	case *EventToolCallEnd:
		content := eventBlock(event, t.ContentIndex)
		if _, ok := content.(*ToolCall); !ok {
			panic(fmt.Sprintf("toolcall_end event points to %s block at index %d", content.ContentType(), t.ContentIndex))
		}
		e.endBlock(t.ContentIndex, "toolCall")
		args := t.ToolCall.Arguments
		if args != nil {
			args = args.Clone()
		}
		return &FrameToolCallEnd{
			ContentIndex:     t.ContentIndex,
			ID:               t.ToolCall.ID,
			Name:             t.ToolCall.Name,
			Arguments:        args,
			ThoughtSignature: t.ToolCall.ThoughtSignature,
			Namespace:        t.ToolCall.Namespace,
		}
	default:
		return nil
	}
}

func cloneTextContent(content TextContent) TextContent {
	return TextContent{Text: content.Text, TextSignature: content.TextSignature}
}

func cloneThinkingContent(content ThinkingContent) ThinkingContent {
	return ThinkingContent{Thinking: content.Thinking, ThinkingSignature: content.ThinkingSignature, Redacted: content.Redacted}
}

func cloneToolCall(call *ToolCall) *ToolCall {
	return &ToolCall{
		ID:               call.ID,
		Name:             call.Name,
		Arguments:        call.Arguments.Clone(),
		ThoughtSignature: call.ThoughtSignature,
		Namespace:        call.Namespace,
	}
}

func (e *AssistantMessageFrameEncoder) startBlock(contentIndex int, state *encoderBlockState) {
	assertContentIndex(contentIndex)
	if _, exists := e.blocks[contentIndex]; exists {
		panic(fmt.Sprintf("Assistant message block %d starts more than once", contentIndex))
	}
	e.blocks[contentIndex] = state
}

func (e *AssistantMessageFrameEncoder) block(contentIndex int, kind string) *encoderBlockState {
	assertContentIndex(contentIndex)
	state, exists := e.blocks[contentIndex]
	if !exists {
		panic(fmt.Sprintf("Assistant message %s block %d has not started", kind, contentIndex))
	}
	if state.kind != kind {
		panic(fmt.Sprintf("Assistant message block %d is %s, not %s", contentIndex, state.kind, kind))
	}
	return state
}

func (e *AssistantMessageFrameEncoder) endBlock(contentIndex int, kind string) {
	e.block(contentIndex, kind)
	delete(e.blocks, contentIndex)
}

func (e *AssistantMessageFrameEncoder) encodeTextDelta(contentIndex int, delta, kind string) AssistantMessageFrame {
	state := e.block(contentIndex, kind)
	deltaStart := state.deltaChars
	state.deltaChars += len(delta)
	covered := state.coveredChars - deltaStart
	if covered < 0 {
		covered = 0
	}
	if covered >= len(delta) {
		return nil
	}
	uncovered := delta
	if covered != 0 {
		uncovered = delta[covered:]
	}
	if kind == "text" {
		return &FrameTextDelta{ContentIndex: contentIndex, Delta: uncovered}
	}
	return &FrameThinkingDelta{ContentIndex: contentIndex, Delta: uncovered}
}

// ---------------------------------------------------------------------------
// Reducer
// ---------------------------------------------------------------------------

type reducerBlockState struct {
	kind  string
	ended bool
	json  string
}

// ReduceAssistantMessageFrames replays compact frames without mutating them.
// Returns nil when there is no start frame.
func ReduceAssistantMessageFrames(frames []AssistantMessageFrame) *AssistantMessage {
	var message *AssistantMessage
	var frameBeforeStart string
	states := map[int]*reducerBlockState{}
	stateOrder := []int{}

	for _, frame := range frames {
		if t, ok := frame.(*FrameStart); ok {
			if message != nil {
				panic("Assistant message frame sequence contains more than one start frame")
			}
			if frameBeforeStart != "" {
				panic(fmt.Sprintf("%s frame appears before the start frame", frameBeforeStart))
			}
			message = cloneStartMessageForReduce(t.Partial)
			continue
		}
		if message == nil {
			if frameBeforeStart == "" {
				frameBeforeStart = frame.FrameType()
			}
			continue
		}

		switch t := frame.(type) {
		case *FrameTextStart:
			appendReducerBlock(message, states, &stateOrder, t.ContentIndex, t.Content, &reducerBlockState{kind: "text"})
		case *FrameTextDelta:
			block := activeReducerBlock(message, states, t.ContentIndex, "text", t.FrameType())
			text := block.(TextContent)
			text.Text += t.Delta
			message.Content[t.ContentIndex] = text
		case *FrameTextEnd:
			block, state := activeReducerBlockPair(message, states, t.ContentIndex, "text", t.FrameType())
			text := block.(TextContent)
			text.Text = t.Content
			text.TextSignature = t.TextSignature
			message.Content[t.ContentIndex] = text
			state.ended = true
		case *FrameThinkingStart:
			appendReducerBlock(message, states, &stateOrder, t.ContentIndex, t.Content, &reducerBlockState{kind: "thinking"})
		case *FrameThinkingDelta:
			block := activeReducerBlock(message, states, t.ContentIndex, "thinking", t.FrameType())
			thinking := block.(ThinkingContent)
			thinking.Thinking += t.Delta
			message.Content[t.ContentIndex] = thinking
		case *FrameThinkingEnd:
			block, state := activeReducerBlockPair(message, states, t.ContentIndex, "thinking", t.FrameType())
			thinking := block.(ThinkingContent)
			thinking.Thinking = t.Content
			thinking.ThinkingSignature = t.ThinkingSignature
			thinking.Redacted = t.Redacted
			message.Content[t.ContentIndex] = thinking
			state.ended = true
		case *FrameToolCallStart:
			appendReducerBlock(message, states, &stateOrder, t.ContentIndex, cloneToolCall(t.ToolCall), &reducerBlockState{kind: "toolCall"})
		case *FrameToolCallCheckpoint:
			block, state := activeReducerBlockPair(message, states, t.ContentIndex, "toolCall", t.FrameType())
			call := block.(*ToolCall)
			state.json = t.JSON
			call.Arguments = ParseStreamingJSONObject(t.JSON)
			message.Content[t.ContentIndex] = call
		case *FrameToolCallDelta:
			block, state := activeReducerBlockPair(message, states, t.ContentIndex, "toolCall", t.FrameType())
			_ = block
			state.json += t.Delta
		case *FrameToolCallEnd:
			block, state := activeReducerBlockPair(message, states, t.ContentIndex, "toolCall", t.FrameType())
			call := block.(*ToolCall)
			call.ID = t.ID
			call.Name = t.Name
			args := t.Arguments
			if args != nil {
				args = args.Clone()
			} else {
				args = jsonx.NewObj()
			}
			call.Arguments = args
			call.ThoughtSignature = t.ThoughtSignature
			call.Namespace = t.Namespace
			message.Content[t.ContentIndex] = call
			state.ended = true
		}
	}

	if message == nil {
		return nil
	}
	for _, contentIndex := range stateOrder {
		state := states[contentIndex]
		if state == nil || state.kind != "toolCall" || state.ended || len(state.json) == 0 {
			continue
		}
		if contentIndex >= len(message.Content) {
			continue
		}
		if call, ok := message.Content[contentIndex].(*ToolCall); ok {
			call.Arguments = ParseStreamingJSONObject(state.json)
		}
	}
	return message
}

func cloneStartMessageForReduce(message *AssistantMessage) *AssistantMessage {
	clone := cloneStartMessage(message)
	clone.StopReason = message.StopReason
	clone.Usage = *usageClone(&message.Usage)
	clone.Diagnostics = append([]AssistantMessageDiagnostic{}, message.Diagnostics...)
	return clone
}

func appendReducerBlock(message *AssistantMessage, states map[int]*reducerBlockState, order *[]int, contentIndex int, block ContentBlock, state *reducerBlockState) {
	assertContentIndex(contentIndex)
	if contentIndex != len(message.Content) {
		reason := "would leave a gap"
		if contentIndex < len(message.Content) {
			reason = "already exists"
		}
		panic(fmt.Sprintf("Cannot start assistant message block at index %d: %s", contentIndex, reason))
	}
	message.Content = append(message.Content, block)
	states[contentIndex] = state
	*order = append(*order, contentIndex)
}

func activeReducerBlock(message *AssistantMessage, states map[int]*reducerBlockState, contentIndex int, expectedKind, frameType string) ContentBlock {
	block, _ := activeReducerBlockPair(message, states, contentIndex, expectedKind, frameType)
	return block
}

func activeReducerBlockPair(message *AssistantMessage, states map[int]*reducerBlockState, contentIndex int, expectedKind, frameType string) (ContentBlock, *reducerBlockState) {
	assertContentIndex(contentIndex)
	state := states[contentIndex]
	if contentIndex >= len(message.Content) {
		panic(fmt.Sprintf("%s frame has no started block at index %d", frameType, contentIndex))
	}
	block := message.Content[contentIndex]
	if state == nil {
		panic(fmt.Sprintf("%s frame has no started block at index %d", frameType, contentIndex))
	}
	if state.kind != expectedKind || block.ContentType() != expectedKind {
		panic(fmt.Sprintf("%s frame expected %s block at index %d, found %s", frameType, expectedKind, contentIndex, block.ContentType()))
	}
	if state.ended {
		panic(fmt.Sprintf("%s frame follows the end of block at index %d", frameType, contentIndex))
	}
	return block, state
}
