package kinds

// frames.go ports harness/pico3/kinds/frames.ts: applying one encoded
// frame to the tracked output. Same switch as pi-ai's
// reduceAssistantMessageFrames; no buffering, no second copy.

import (
	"github.com/gladmo/openagent/jsonx"
)

// ApplyFrame applies one frame in place to the output object's message.
// The output carries {"message": {...}} with the content array inside.
func ApplyFrame(output *jsonx.Obj, frame *jsonx.Obj) error {
	frameType := frameString(frame, "type")
	if frameType == "start" {
		partial, _ := frame.Get("partial")
		if partialObj, ok := partial.(*jsonx.Obj); ok {
			output.Set("message", cloneJSON(partialObj))
		}
		return nil
	}
	messageValue, ok := output.Get("message")
	if !ok || messageValue == nil {
		return &frameOrderError{frameType: frameType}
	}
	message, ok := messageValue.(*jsonx.Obj)
	if !ok {
		return &frameOrderError{frameType: frameType}
	}
	contentValue, _ := message.Get("content")
	content, ok := contentValue.([]any)
	if !ok {
		content = []any{}
	}
	contentIndex := int(frameNumber(frame, "contentIndex"))
	for len(content) <= contentIndex {
		content = append(content, nil)
	}

	switch frameType {
	case "text_start":
		if contentFrame, ok := frame.Get("content"); ok {
			content[contentIndex] = cloneJSON(contentFrame)
		}
	case "text_delta":
		block := contentBlock(content, contentIndex)
		block.Set("text", frameString(block, "text")+frameString(frame, "delta"))
	case "text_end":
		block := contentBlock(content, contentIndex)
		block.Set("text", frameString(frame, "content"))
		block.Delete("textSignature")
		if v, ok := frame.Get("textSignature"); ok && v != nil {
			block.Set("textSignature", v)
		}
	case "thinking_start":
		if contentFrame, ok := frame.Get("content"); ok {
			content[contentIndex] = cloneJSON(contentFrame)
		}
	case "thinking_delta":
		block := contentBlock(content, contentIndex)
		block.Set("thinking", frameString(block, "thinking")+frameString(frame, "delta"))
	case "thinking_end":
		block := contentBlock(content, contentIndex)
		block.Set("thinking", frameString(frame, "content"))
		block.Delete("thinkingSignature")
		block.Delete("redacted")
		if v, ok := frame.Get("thinkingSignature"); ok && v != nil {
			block.Set("thinkingSignature", v)
		}
		if v, ok := frame.Get("redacted"); ok && v != nil {
			block.Set("redacted", v)
		}
	case "toolcall_start":
		if toolCall, ok := frame.Get("toolCall"); ok {
			content[contentIndex] = cloneJSON(toolCall)
		}
	case "toolcall_checkpoint":
		block := contentBlock(content, contentIndex)
		block.Set("arguments", safeParse(frameString(frame, "json")))
	case "toolcall_delta":
		// Arguments materialise at checkpoint/end; deltas only feed the
		// encoder's own json buffer.
	case "toolcall_end":
		block := contentBlock(content, contentIndex)
		block.Set("id", frameString(frame, "id"))
		block.Set("name", frameString(frame, "name"))
		if arguments, ok := frame.Get("arguments"); ok {
			block.Set("arguments", cloneJSON(arguments))
		}
		if v, ok := frame.Get("thoughtSignature"); ok && v != nil {
			block.Set("thoughtSignature", v)
		}
		if v, ok := frame.Get("namespace"); ok && v != nil {
			block.Set("namespace", v)
		}
	}
	message.Set("content", content)
	return nil
}

type frameOrderError struct{ frameType string }

func (e *frameOrderError) Error() string {
	return e.frameType + " before start"
}

// contentBlock returns the block at index as a mutable object, replacing
// non-objects with fresh ones.
func contentBlock(content []any, index int) *jsonx.Obj {
	if block, ok := content[index].(*jsonx.Obj); ok {
		return block
	}
	fresh := jsonx.NewObj()
	content[index] = fresh
	return fresh
}

func frameString(obj *jsonx.Obj, key string) string {
	if v, ok := obj.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func frameNumber(obj *jsonx.Obj, key string) float64 {
	if v, ok := obj.Get(key); ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

func safeParse(json string) any {
	parsed, err := jsonxParseHelper(json)
	if err != nil {
		return jsonx.NewObj()
	}
	return parsed
}

func cloneJSON(v any) any {
	if obj, ok := v.(*jsonx.Obj); ok {
		out := jsonx.NewObj()
		for _, key := range obj.Keys() {
			value, _ := obj.Get(key)
			out.Set(key, value)
		}
		return out
	}
	return v
}
