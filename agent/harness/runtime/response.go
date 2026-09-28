package runtime

// response.go ports harness/runtime/drive/response.ts's pure
// classification helpers: uuidv7 timestamp parsing, provider error
// shaping, abort/error normalization, deferred-handle validation, and the
// turn-id derivation.

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// UUIDv7Timestamp mirrors uuidV7Timestamp: the first 12 hex digits (8 +
// 4 around the dash) decode to the Unix millisecond timestamp.
func UUIDv7Timestamp(id string) (float64, error) {
	if len(id) < 23 {
		return 0, &session.SessionInvariantError{Message: fmt.Sprintf("Invalid reserved UUIDv7 %s", id)}
	}
	raw := id[0:8] + id[9:13]
	value, err := strconv.ParseInt(raw, 16, 64)
	if err != nil {
		return 0, &session.SessionInvariantError{Message: fmt.Sprintf("Invalid reserved UUIDv7 %s", id)}
	}
	return float64(value), nil
}

// ProviderError mirrors providerError: the message's errorMessage or the
// fallback "X request ended with <stopReason>".
func ProviderError(source string, message *jsonx.Obj) *session.OperationError {
	errorMessage, _ := message.Get("errorMessage")
	if s, ok := errorMessage.(string); ok && s != "" {
		return &session.OperationError{Code: "assistant_error", Message: s}
	}
	stopReason, _ := message.Get("stopReason")
	label := "Deferred"
	if source == "assistant" {
		label = "Assistant"
	}
	return &session.OperationError{Code: "assistant_error", Message: fmt.Sprintf("%s request ended with %v", label, stopReason)}
}

// NormalizeError mirrors normalizeError: force error stop + message.
func NormalizeError(message *jsonx.Obj, errorMessage string) *jsonx.Obj {
	normalized := jsonx.NewObj()
	for _, key := range message.Keys() {
		value, _ := message.Get(key)
		normalized.Set(key, value)
	}
	normalized.Set("stopReason", "error")
	normalized.Set("errorMessage", errorMessage)
	return normalized
}

// NormalizeAborted mirrors normalizeAborted: force aborted stop + message
// with the fallback.
func NormalizeAborted(source string, message *jsonx.Obj) *jsonx.Obj {
	normalized := jsonx.NewObj()
	for _, key := range message.Keys() {
		value, _ := message.Get(key)
		normalized.Set(key, value)
	}
	normalized.Set("stopReason", "aborted")
	if existing, ok := message.Get("errorMessage"); ok && existing != nil {
		normalized.Set("errorMessage", existing)
	} else {
		label := "Deferred"
		if source == "assistant" {
			label = "Assistant"
		}
		normalized.Set("errorMessage", label+" request was cancelled")
	}
	return normalized
}

// DeferredHandleIsValid mirrors deferredHandleIsValid: deferred stop +
// non-empty handle id + provider/model/api match against the generation
// identity and the message's api.
func DeferredHandleIsValid(message *jsonx.Obj, provider, modelID string) bool {
	stopReason, _ := message.Get("stopReason")
	if stopReason != "deferred" {
		return false
	}
	handleValue, ok := message.Get("deferred")
	if !ok || handleValue == nil {
		return false
	}
	handle, ok := handleValue.(*jsonx.Obj)
	if !ok {
		return false
	}
	id, _ := handle.Get("id")
	idStr, _ := id.(string)
	if idStr == "" {
		return false
	}
	handleProvider, _ := handle.Get("provider")
	handleModel, _ := handle.Get("modelId")
	if handleProvider != provider || handleModel != modelID {
		return false
	}
	handleAPI, _ := handle.Get("api")
	messageAPI, _ := message.Get("api")
	return handleAPI == messageAPI
}

// TurnIDOf mirrors the turn-id derivation: assistant effect-pending uses
// the generation stepId; deferred polls use stepId:poll:N.
func TurnIDOf(at, stepID string, poll int64) string {
	if at == session.AtAssistantEffectPending {
		return stepID
	}
	return fmt.Sprintf("%s:poll:%d", stepID, poll)
}

// ResponseSource labels the response origin.
func ResponseSource(at string) string {
	if at == session.AtAssistantEffectPending {
		return "assistant"
	}
	return "deferred"
}

// keep hex + strings referenced for the decoder.
var (
	_ = hex.EncodeToString
	_ = strings.TrimSpace
)
