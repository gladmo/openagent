package runtime

// generation_perform.go ports performGeneration's stream consumption:
// relaying provider events into the pending-frame progress channel,
// encoding frames through the ai frame encoder, and settling on the done
// or error event.

import (
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// PerformHooks carries the observer seams.
type PerformHooks struct {
	// OnFrame receives each encoded frame (the progress write).
	OnFrame func(frame *jsonx.Obj)
	// OnStart observes the message_start.
	OnStart func(message *jsonx.Obj)
	// OnUpdate observes message_update events.
	OnUpdate func(message *jsonx.Obj, event any)
	// OnEnd observes message_end.
	OnEnd func(message *jsonx.Obj)
}

// PerformGeneration consumes the provider stream and returns the settled
// message in its JSON form.
func PerformGeneration(
	registry *ModelsRegistry,
	prepared *PreparedGeneration,
	thinkingLevel string,
	hooks *PerformHooks,
	options *ai.StreamOptions,
) (*jsonx.Obj, error) {
	messages := make([]ai.Message, 0, len(prepared.Messages))
	for _, message := range prepared.Messages {
		decoded, err := ai.MessageFromJSON(message)
		if err != nil || decoded == nil {
			// Fall back to a raw user message wrapper for undecodable
			// entries; the provider normalizes upstream.
			messages = append(messages, &ai.UserMessage{Content: ai.StringContent(jsonx.Stringify(message)), TimestampMs: 0})
			continue
		}
		messages = append(messages, decoded)
	}
	events, err := registry.Stream(prepared.Model, StreamRequest{
		Messages:      messages,
		ThinkingLevel: thinkingLevel,
	}, options)
	if err != nil {
		return nil, err
	}
	encoder := ai.NewAssistantMessageFrameEncoder()
	for event := range events {
		if event.Err != nil {
			// Terminal error event: the message rides the error.
			if done := errorEventMessage(event.Err); done != nil {
				return assistantToJSONObj(done), nil
			}
			return nil, event.Err
		}
		if event.Done != nil {
			return assistantToJSONObj(event.Done.Message), nil
		}
		if event.Frame != nil {
			relayFrame(encoder, event.Frame, hooks)
		}
	}
	// The stream closed without a terminal event; that is an interrupted
	// generation — the recovery path handles it upstream.
	return nil, nil
}

// relayFrame encodes one event frame and forwards it through the hooks.
func relayFrame(encoder *ai.AssistantMessageFrameEncoder, event *ai.AssistantMessageEvent, hooks *PerformHooks) {
	frame := encoder.Encode(*event)
	partial := eventPartial(event)
	partialJSON := assistantToJSONObj(partial)
	switch (*event).EventType() {
	case "start":
		if hooks != nil && hooks.OnStart != nil {
			hooks.OnStart(partialJSON)
		}
	case "message_update", "text_delta", "thinking_delta", "toolcall_delta":
		if hooks != nil && hooks.OnUpdate != nil {
			hooks.OnUpdate(partialJSON, *event)
		}
	default:
		// text_end / thinking_end / toolcall_end etc.
		if (*event).EventType() == "done" {
			if hooks != nil && hooks.OnEnd != nil {
				hooks.OnEnd(partialJSON)
			}
		}
	}
	if frame != nil && hooks != nil && hooks.OnFrame != nil {
		if encoded := jsonxParse2(frame); encoded != nil {
			hooks.OnFrame(encoded)
		}
	}
}

func eventPartial(event *ai.AssistantMessageEvent) *ai.AssistantMessage {
	switch t := (*event).(type) {
	case *ai.EventStart:
		return t.Partial
	case *ai.EventTextStart:
		return t.Partial
	case *ai.EventTextDelta:
		return t.Partial
	case *ai.EventTextEnd:
		return t.Partial
	case *ai.EventDone:
		return t.Message
	}
	return nil
}

func errorEventMessage(err error) *ai.AssistantMessage {
	if streamErr, ok := err.(*providerStreamError); ok {
		return streamErr.message
	}
	return nil
}

// assistantToJSONObj renders one assistant message through its JSON form.
func assistantToJSONObj(message *ai.AssistantMessage) *jsonx.Obj {
	if message == nil {
		return jsonx.ObjFrom("role", "assistant", "content", []any{}, "stopReason", "error", "errorMessage", "stream closed without a terminal event")
	}
	// Prefer the message codec; fall back to field-wise rendering.
	if encoded := ai.MessageToJSON(message); encoded != nil {
		return encoded
	}
	obj := jsonx.NewObj()
	obj.Set("role", "assistant")
	obj.Set("api", message.API)
	obj.Set("provider", message.Provider)
	obj.Set("model", message.Model)
	obj.Set("stopReason", message.StopReason)
	if message.ErrorMessage != nil {
		obj.Set("errorMessage", *message.ErrorMessage)
	}
	if message.Deferred != nil {
		obj.Set("deferred", message.Deferred)
	}
	obj.Set("content", contentBlocksToJSON(message.Content))
	return obj
}

func contentBlocksToJSON(content []ai.ContentBlock) []any {
	out := make([]any, 0, len(content))
	for _, block := range content {
		out = append(out, jsonx.Stringify(block))
	}
	return out
}
