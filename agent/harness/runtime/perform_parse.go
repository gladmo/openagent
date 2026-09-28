package runtime

import (
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// jsonxParse2 renders an ai frame through its JSON form.
var jsonxParse2 = func(frame ai.AssistantMessageFrame) *jsonx.Obj {
	encoded := jsonx.Stringify(frame)
	parsed, err := jsonx.Parse(encoded)
	if err != nil {
		return nil
	}
	if obj, ok := parsed.(*jsonx.Obj); ok {
		return obj
	}
	return nil
}
