package kinds

// tool.go ports harness/pico3/kinds/tool.ts: the pi.tool kind's decision
// core — argument validation against the declaration schema, the
// before-tool chain with identity preservation, the started-phase replay
// guards, and the synthetic error results.

import (
	"fmt"
	"strings"

	"github.com/gladmo/openagent/agent/harness/pico3"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

// ToolKindName identifies the kind.
const ToolKindName = "pi.tool"

// ToolInflight mirrors kind.inflight.
var ToolInflight = []string{"started"}

// StoredToolCall is the JSON-shaped tool call.
type StoredToolCall = *jsonx.Obj

// ToolInputFields reads the tool input payload.
type ToolInputFields struct {
	Assistant int64
	Call      StoredToolCall
	Offered   []string
	Index     int64
}

// ParseToolInput reads the input object.
func ParseToolInput(input any) ToolInputFields {
	fields := ToolInputFields{}
	obj, ok := input.(*jsonx.Obj)
	if !ok {
		return fields
	}
	if v, ok := obj.Get("assistant"); ok {
		if f, ok := v.(float64); ok {
			fields.Assistant = int64(f)
		}
	}
	if v, ok := obj.Get("call"); ok {
		if callObj, ok := v.(*jsonx.Obj); ok {
			fields.Call = callObj
		}
	}
	if v, ok := obj.Get("offered"); ok {
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					fields.Offered = append(fields.Offered, s)
				}
			}
		}
	}
	if v, ok := obj.Get("index"); ok {
		if f, ok := v.(float64); ok {
			fields.Index = int64(f)
		}
	}
	return fields
}

// ToolCheckpointFields reads the started checkpoint.
type ToolCheckpointFields struct {
	Phase  string
	Replay string
	Call   StoredToolCall
}

// ParseToolCheckpoint reads the checkpoint.
func ParseToolCheckpoint(checkpoint pico3.Checkpoint) ToolCheckpointFields {
	fields := ToolCheckpointFields{}
	if checkpoint == nil {
		return fields
	}
	if v, ok := checkpoint.Get("phase"); ok {
		if s, ok := v.(string); ok {
			fields.Phase = s
		}
	}
	if v, ok := checkpoint.Get("replay"); ok {
		if s, ok := v.(string); ok {
			fields.Replay = s
		}
	}
	if v, ok := checkpoint.Get("call"); ok {
		if callObj, ok := v.(*jsonx.Obj); ok {
			fields.Call = callObj
		}
	}
	return fields
}

// SyntheticResult builds the error tool result.
func SyntheticResult(text, code string) *jsonx.Obj {
	return jsonx.ObjFrom(
		"content", []any{jsonx.ObjFrom("type", "text", "text", text)},
		"isError", true,
		"diagnostics", []any{jsonx.ObjFrom("severity", "error", "message", text, "code", code)},
	)
}

// InvalidArguments validates call arguments against the declaration's
// schema and formats the errors like the TS.
func InvalidArguments(declaration *pico3.ToolDeclaration, call StoredToolCall) string {
	if declaration == nil || declaration.Parameters == nil || call == nil {
		return ""
	}
	argsValue, ok := call.Get("arguments")
	if !ok {
		argsValue = nil
	}
	errors := typebox.Compile(declaration.Parameters).Errors(argsValue)
	if len(errors) == 0 {
		return ""
	}
	parts := make([]string, 0, len(errors))
	for _, err := range errors {
		instancePath := err.InstancePath
		if instancePath == "" {
			instancePath = "/"
		}
		parts = append(parts, fmt.Sprintf("%s: %s", instancePath, err.Message))
	}
	return strings.Join(parts, "; ")
}

// CallString reads a string field off the call.
func CallString(call StoredToolCall, key string) string {
	if call == nil {
		return ""
	}
	if v, ok := call.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// SameIdentity compares two calls by id/name/namespace.
func SameIdentity(a, b StoredToolCall) bool {
	return CallString(a, "id") == CallString(b, "id") &&
		CallString(a, "name") == CallString(b, "name") &&
		CallString(a, "namespace") == CallString(b, "namespace")
}

// ToolDecision captures the initial/started phase's terminal decisions.
type ToolDecision struct {
	// Done closes the task with a synthetic result (nil = proceed).
	Done *jsonx.Obj
	// Code is the synthetic diagnostic code for Done.
	Code string
	// Checkpoint is the started checkpoint to write before the effect.
	WriteCheckpoint bool
	Replay          string
	Call            StoredToolCall
}

// EvaluateToolInitial mirrors kind.initial's decision sequence: offered
// check, registration, validation, before-tool chain (injectable), final
// validation, then the durable started write.
func EvaluateToolInitial(
	input ToolInputFields,
	declaration *pico3.ToolDeclaration,
	beforeTool func(call StoredToolCall) (blocked bool, blockReason string, replacement StoredToolCall, err error),
) (*ToolDecision, error) {
	callName := CallString(input.Call, "name")
	if !containsString(input.Offered, callName) {
		return &ToolDecision{
			Done: SyntheticResult(fmt.Sprintf("tool %s was not offered", callName), "not_offered"),
			Code: "not_offered",
		}, nil
	}
	if declaration == nil {
		return &ToolDecision{
			Done: SyntheticResult(fmt.Sprintf("tool %s is not registered", callName), "missing_tool"),
			Code: "missing_tool",
		}, nil
	}
	if bad := InvalidArguments(declaration, input.Call); bad != "" {
		return &ToolDecision{
			Done: SyntheticResult("invalid arguments: "+bad, "invalid_arguments"),
			Code: "invalid_arguments",
		}, nil
	}
	blocked, blockReason, replacement, err := beforeTool(input.Call)
	if err != nil {
		return nil, err
	}
	if blocked {
		return &ToolDecision{
			Done: SyntheticResult("blocked: "+blockReason, "blocked"),
			Code: "blocked",
		}, nil
	}
	if !SameIdentity(input.Call, replacement) {
		return &ToolDecision{
			Done: SyntheticResult("blocked: call identity changed", "blocked"),
			Code: "blocked",
		}, nil
	}
	if bad := InvalidArguments(declaration, replacement); bad != "" {
		return &ToolDecision{
			Done: SyntheticResult("invalid arguments after hook: "+bad, "invalid_arguments"),
			Code: "invalid_arguments",
		}, nil
	}
	replay := declaration.Replay
	if replay == "" {
		replay = "unsafe"
	}
	return &ToolDecision{
		WriteCheckpoint: true,
		Replay:          replay,
		Call:            replacement,
	}, nil
}

// EvaluateToolStarted mirrors the started phase's replay guards:
// unavailable after restart, interrupted unless both checkpoints and the
// declaration are safe-replay, interrupted when arguments no longer
// validate, else invoke.
func EvaluateToolStarted(
	checkpoint ToolCheckpointFields,
	declaration *pico3.ToolDeclaration,
) (*ToolDecision, error) {
	callName := CallString(checkpoint.Call, "name")
	if declaration == nil {
		return &ToolDecision{
			Done: SyntheticResult(fmt.Sprintf("tool %s unavailable after restart", callName), "unavailable"),
			Code: "unavailable",
		}, nil
	}
	declarationReplay := declaration.Replay
	if declarationReplay == "" {
		declarationReplay = "unsafe"
	}
	if checkpoint.Replay != "safe" || declarationReplay != "safe" {
		return &ToolDecision{
			Done: SyntheticResult(fmt.Sprintf("tool %s was interrupted", callName), "interrupted"),
			Code: "interrupted",
		}, nil
	}
	if bad := InvalidArguments(declaration, checkpoint.Call); bad != "" {
		return &ToolDecision{
			Done: SyntheticResult(fmt.Sprintf("tool %s was interrupted; arguments no longer validate", callName), "interrupted"),
			Code: "interrupted",
		}, nil
	}
	return &ToolDecision{
		WriteCheckpoint: false,
		Replay:          checkpoint.Replay,
		Call:            checkpoint.Call,
	}, nil
}

// AbortSlotPatch mirrors the abort closure's slot patch: status aborted,
// entry recorded, waitingOn cleared.
func AbortSlotPatch(slot *jsonx.Obj, entryID int64) {
	if slot == nil {
		return
	}
	slot.Set("status", "aborted")
	slot.Set("entry", float64(entryID))
	slot.Delete("waitingOn")
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
