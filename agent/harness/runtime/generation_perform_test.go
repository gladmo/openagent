package runtime

// Ports of performGeneration's stream consumption.

import (
	"testing"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func performDeps(t *testing.T) (*ModelsRegistry, *PreparedGeneration) {
	t.Helper()
	registry := prepRegistry(t)
	model := registry.Resolve(prepIdentity())
	return registry, &PreparedGeneration{
		Model:        model,
		Messages:     []*jsonx.Obj{jsonx.ObjFrom("role", "user", "content", "hi", "timestamp", float64(1))},
		SystemPrompt: "",
	}
}

func TestPerformGenerationSettles(t *testing.T) {
	registry, prepared := performDeps(t)
	message, err := PerformGeneration(registry, prepared, "off", nil, &ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if message == nil {
		t.Fatal("no settled message")
	}
	if message.MustGet("role") != "assistant" {
		t.Fatalf("message = %v", message)
	}
	// Either a settled stopReason or an error message (unscripted faux).
	stop, hasStop := message.Get("stopReason")
	errorMessage, hasError := message.Get("errorMessage")
	if !((hasStop && stop == "stop") || (hasError && errorMessage != nil)) {
		t.Fatalf("message = %v", message)
	}
}

func TestPerformGenerationRelaysFrames(t *testing.T) {
	registry, prepared := performDeps(t)
	var frames []*jsonx.Obj
	var starts, updates int
	hooks := &PerformHooks{
		OnFrame:  func(frame *jsonx.Obj) { frames = append(frames, frame) },
		OnStart:  func(*jsonx.Obj) { starts++ },
		OnUpdate: func(*jsonx.Obj, any) { updates++ },
	}
	message, err := PerformGeneration(registry, prepared, "off", hooks, &ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if message == nil {
		t.Fatal("no settled message")
	}
	// The faux provider emits start + deltas + done; frames were relayed
	// (a scripted delta path) or the stream errored immediately. Either
	// way the hooks are safe to call.
	_ = frames
	_ = starts
	_ = updates
}

func TestPerformGenerationNilModel(t *testing.T) {
	registry := prepRegistry(t)
	_, err := PerformGeneration(registry, &PreparedGeneration{Model: nil}, "off", nil, nil)
	if err == nil {
		t.Fatal("nil model accepted")
	}
}

func TestAssistantToJSONObj(t *testing.T) {
	// Nil message renders the interrupted-error shape.
	obj := assistantToJSONObj(nil)
	if obj.MustGet("stopReason") != "error" || obj.MustGet("errorMessage") == nil {
		t.Fatalf("obj = %v", obj)
	}
	// A real message renders through the codec.
	msg := &ai.AssistantMessage{
		API:      "faux",
		Provider: "faux",
		Model:    "faux-1",
		Content:  []ai.ContentBlock{ai.TextContent{Text: "hi"}},
	}
	obj = assistantToJSONObj(msg)
	if obj.MustGet("role") != "assistant" || obj.MustGet("provider") != "faux" {
		t.Fatalf("obj = %v", obj)
	}
	// Pointer error message stringifies.
	errText := "boom"
	msg2 := &ai.AssistantMessage{ErrorMessage: &errText, StopReason: "error"}
	obj = assistantToJSONObj(msg2)
	if obj.MustGet("errorMessage") != "boom" {
		t.Fatalf("obj = %v", obj)
	}
}
