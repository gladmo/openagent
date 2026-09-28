package runtime

// Ports of the lane models-surface behaviors.

import (
	"testing"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func fauxRegistry(t *testing.T) *ModelsRegistry {
	t.Helper()
	models := ai.CreateModels()
	handle := ai.FauxProvider(ai.RegisterFauxProviderOptions{})
	models.SetProvider(handle.Provider)
	return NewModelsRegistry(models)
}

func TestModelsRegistryResolve(t *testing.T) {
	registry := fauxRegistry(t)
	identity := jsonx.ObjFrom("provider", ai.FauxDefaultProvider, "modelId", ai.FauxDefaultModelID)
	model := registry.Resolve(identity)
	if model == nil {
		t.Fatal("faux model unresolvable")
	}
	if model.Provider != ai.FauxDefaultProvider || model.ID != ai.FauxDefaultModelID {
		t.Fatalf("model = %+v", model)
	}
	// Unknown identity -> nil.
	if registry.Resolve(jsonx.ObjFrom("provider", "nope", "modelId", "x")) != nil {
		t.Fatal("unknown identity resolved")
	}
	// Nil registry / nil identity are nil-safe.
	var nilRegistry *ModelsRegistry
	if nilRegistry.Resolve(identity) != nil {
		t.Fatal("nil registry resolved")
	}
	if registry.Resolve(nil) != nil {
		t.Fatal("nil identity resolved")
	}
}

func TestContextAndMaxTokensOf(t *testing.T) {
	if ContextWindowOf(nil) != 0 || MaxTokensOf(nil) != 0 {
		t.Fatal("nil model helpers")
	}
	model := &ai.Model{ContextWindow: 128000, MaxTokens: 16384}
	if ContextWindowOf(model) != 128000 || MaxTokensOf(model) != 16384 {
		t.Fatal("helpers wrong")
	}
}

func TestStreamRelaysEvents(t *testing.T) {
	registry := fauxRegistry(t)
	model := registry.Resolve(jsonx.ObjFrom("provider", ai.FauxDefaultProvider, "modelId", ai.FauxDefaultModelID))
	if model == nil {
		t.SkipNow()
	}
	options := &ai.StreamOptions{}
	events, err := registry.Stream(model, StreamRequest{
		Messages:      []ai.Message{&ai.UserMessage{Content: ai.StringContent("hi"), TimestampMs: 1}},
		ThinkingLevel: "off",
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	var done *ai.EventDone
	frames := 0
	var streamErr error
	for event := range events {
		if event.Err != nil {
			streamErr = event.Err
			continue
		}
		if event.Done != nil {
			done = event.Done
		} else if event.Frame != nil {
			frames++
		}
	}
	// The stream must close with EITHER a done event or a terminal error.
	if done == nil && streamErr == nil {
		t.Fatal("stream closed without a terminal event")
	}
	_ = frames
}

func TestFetchDeferredRequiresHandle(t *testing.T) {
	registry := fauxRegistry(t)
	if _, err := registry.FetchDeferred(nil, nil, nil); err == nil {
		t.Fatal("nil fetch accepted")
	}
	if err := registry.CancelDeferred(nil, nil, nil); err == nil {
		t.Fatal("nil cancel accepted")
	}
}
