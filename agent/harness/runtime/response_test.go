package runtime

// Ports of drive/response.ts classification helpers.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func TestUUIDv7Timestamp(t *testing.T) {
	// 0x018f6a1e = 267305902 -> slice(0,8)+slice(9,13) of a v7 id.
	id := "018f6a1e-1234-7xxx-xxxx-xxxxxxxxxxxx"
	ts, err := UUIDv7Timestamp(id)
	if err != nil {
		t.Fatal(err)
	}
	// 0x018f6a1e1234 in ms.
	expected := float64(0x018f6a1e1234)
	if ts != expected {
		t.Fatalf("ts = %v expected = %v", ts, expected)
	}
	// Malformed ids reject.
	if _, err := UUIDv7Timestamp("short"); err == nil {
		t.Fatal("short id accepted")
	}
	if _, err := UUIDv7Timestamp("zzzzzzzz-zzzz-7xxx-xxxx-xxxxxxxxxxxx"); err == nil {
		t.Fatal("non-hex accepted")
	}
}

func TestProviderError(t *testing.T) {
	// errorMessage wins.
	message := jsonx.ObjFrom("errorMessage", "custom boom", "stopReason", "error")
	err := ProviderError("assistant", message)
	if err.Code != "assistant_error" || err.Message != "custom boom" {
		t.Fatalf("err = %+v", err)
	}
	// Fallback text carries the source label + stop reason.
	message = jsonx.ObjFrom("stopReason", "length")
	err = ProviderError("deferred", message)
	if err.Message != "Deferred request ended with length" {
		t.Fatalf("message = %q", err.Message)
	}
	err = ProviderError("assistant", message)
	if !strings.Contains(err.Message, "Assistant request ended with") {
		t.Fatalf("message = %q", err.Message)
	}
}

func TestNormalizeErrorAndAborted(t *testing.T) {
	message := jsonx.ObjFrom("content", []any{}, "stopReason", "length")
	// Error: forced stop + message, content preserved.
	normalized := NormalizeError(message, "too long")
	if normalized.MustGet("stopReason") != "error" || normalized.MustGet("errorMessage") != "too long" {
		t.Fatalf("normalized = %v", normalized)
	}
	if normalized.MustGet("content") == nil {
		t.Fatal("content lost")
	}
	if message.MustGet("stopReason") != "length" {
		t.Fatal("source mutated")
	}
	// Aborted without an existing message: fallback text.
	normalized = NormalizeAborted("assistant", message)
	if normalized.MustGet("stopReason") != "aborted" || normalized.MustGet("errorMessage") != "Assistant request was cancelled" {
		t.Fatalf("normalized = %v", normalized)
	}
	// Aborted with an existing message: preserved.
	withMessage := jsonx.ObjFrom("errorMessage", "kept", "stopReason", "pending")
	normalized = NormalizeAborted("deferred", withMessage)
	if normalized.MustGet("errorMessage") != "kept" {
		t.Fatal("existing message dropped")
	}
}

func TestDeferredHandleIsValid(t *testing.T) {
	build := func(stop string, handle *jsonx.Obj, api string) *jsonx.Obj {
		message := jsonx.ObjFrom("role", "assistant", "stopReason", stop, "api", api)
		if handle != nil {
			message.Set("deferred", handle)
		}
		return message
	}
	handle := jsonx.ObjFrom("id", "d1", "provider", "p", "modelId", "m", "api", "openai")
	// Fully matching.
	if !DeferredHandleIsValid(build("deferred", handle, "openai"), "p", "m") {
		t.Fatal("valid handle rejected")
	}
	// Wrong stop reason.
	if DeferredHandleIsValid(build("stop", handle, "openai"), "p", "m") {
		t.Fatal("non-deferred accepted")
	}
	// Missing handle.
	if DeferredHandleIsValid(build("deferred", nil, "openai"), "p", "m") {
		t.Fatal("missing handle accepted")
	}
	// Empty id.
	empty := jsonx.ObjFrom("id", "", "provider", "p", "modelId", "m", "api", "openai")
	if DeferredHandleIsValid(build("deferred", empty, "openai"), "p", "m") {
		t.Fatal("empty id accepted")
	}
	// Wrong provider/model.
	if DeferredHandleIsValid(build("deferred", handle, "openai"), "other", "m") {
		t.Fatal("provider mismatch accepted")
	}
	if DeferredHandleIsValid(build("deferred", handle, "openai"), "p", "other") {
		t.Fatal("model mismatch accepted")
	}
	// api mismatch between handle and message.
	if DeferredHandleIsValid(build("deferred", handle, "anthropic"), "p", "m") {
		t.Fatal("api mismatch accepted")
	}
}

func TestTurnIDOf(t *testing.T) {
	if id := TurnIDOf(session.AtAssistantEffectPending, "step-1", 0); id != "step-1" {
		t.Fatalf("assistant turn = %q", id)
	}
	if id := TurnIDOf(session.AtDeferredEffectPending, "step-1", 3); id != "step-1:poll:3" {
		t.Fatalf("deferred turn = %q", id)
	}
}

func TestResponseSource(t *testing.T) {
	if ResponseSource(session.AtAssistantEffectPending) != "assistant" {
		t.Fatal("assistant label")
	}
	if ResponseSource(session.AtDeferredEffectPending) != "deferred" {
		t.Fatal("deferred label")
	}
}
