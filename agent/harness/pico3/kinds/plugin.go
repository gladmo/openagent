package kinds

// plugin.go ports harness/pico3/kinds/plugin.ts: the pi.plugin kind —
// dispatch an input to a registered plugin handler through the task API;
// a missing handler fails missing_handler, a throw fails threw (unless the
// context aborted, which re-raises).

import (
	"github.com/gladmo/openagent/jsonx"
)

// PluginKindName identifies the kind.
const PluginKindName = "pi.plugin"

// PluginInflight mirrors kind.inflight.
var PluginInflight = []string{"started"}

// PluginInputFields reads the input payload.
type PluginInputFields struct {
	Handler string
	Input   any
}

// ParsePluginInput reads the input object.
func ParsePluginInput(input any) PluginInputFields {
	fields := PluginInputFields{}
	obj, ok := input.(*jsonx.Obj)
	if !ok {
		return fields
	}
	if v, ok := obj.Get("handler"); ok {
		if s, ok := v.(string); ok {
			fields.Handler = s
		}
	}
	if v, ok := obj.Get("input"); ok {
		fields.Input = v
	}
	return fields
}

// PluginHandlerFn is the registered handler surface.
type PluginHandlerFn func(input any) (any, error)

// PluginFailedCompletion builds the failed completion.
func PluginFailedCompletion(reason, detail string) *jsonx.Obj {
	return jsonx.ObjFrom(
		"status", "failed",
		"failure", jsonx.ObjFrom("reason", reason, "detail", detail),
	)
}

// PluginCompletedCompletion builds the completed completion (result is a
// strict JSON round trip of the handler's return).
func PluginCompletedCompletion(result any) (*jsonx.Obj, error) {
	encoded := jsonx.Stringify(result)
	parsed, err := jsonx.Parse(encoded)
	if err != nil {
		return nil, err
	}
	return jsonx.ObjFrom("status", "completed", "result", parsed), nil
}

// RunPlugin mirrors run(): missing handler fails; a throw fails threw
// unless aborted (re-raise); success completes through strict JSON.
func RunPlugin(
	input PluginInputFields,
	handlers map[string]PluginHandlerFn,
	aborted func() bool,
) (*jsonx.Obj, error) {
	handler, ok := handlers[input.Handler]
	if !ok {
		return PluginFailedCompletion("missing_handler", input.Handler), nil
	}
	result, err := handler(input.Input)
	if err != nil {
		if aborted != nil && aborted() {
			return nil, err
		}
		return PluginFailedCompletion("threw", err.Error()), nil
	}
	completion, completeErr := PluginCompletedCompletion(result)
	if completeErr != nil {
		return PluginFailedCompletion("threw", completeErr.Error()), nil
	}
	return completion, nil
}
