package runtime

// Ports of prepareGeneration decision sequence.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func prepRegistry(t *testing.T) *ModelsRegistry {
	t.Helper()
	models := ai.CreateModels()
	handle := ai.FauxProvider(ai.RegisterFauxProviderOptions{})
	models.SetProvider(handle.Provider)
	return NewModelsRegistry(models)
}

func prepIdentity() *jsonx.Obj {
	return jsonx.ObjFrom("provider", ai.FauxDefaultProvider, "modelId", ai.FauxDefaultModelID)
}

func noopBeforeRequest() (*jsonx.Obj, error) { return nil, nil }

func TestPrepareGenerationModelUnavailable(t *testing.T) {
	registry := prepRegistry(t)
	prep, err := PrepareGeneration(registry, jsonx.ObjFrom("provider", "ghost", "modelId", "x"), nil, nil,
		func() ([]*jsonx.Obj, bool, error) { return nil, false, nil },
		func() (string, error) { return "", nil },
		noopBeforeRequest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Kind != GenerationPrepFailure || prep.Error.Code != "model_unavailable" {
		t.Fatalf("prep = %+v", prep)
	}
	if !strings.Contains(prep.Error.Message, "model is unavailable") {
		t.Fatalf("message = %q", prep.Error.Message)
	}
}

func TestPrepareGenerationMissingTools(t *testing.T) {
	registry := prepRegistry(t)
	available := []ToolDescriptor{{Name: "bash"}, {Name: "read"}}
	prep, err := PrepareGeneration(registry, prepIdentity(), []string{"bash", "ghost", "read"}, available,
		func() ([]*jsonx.Obj, bool, error) { return nil, false, nil },
		func() (string, error) { return "", nil },
		noopBeforeRequest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Kind != GenerationPrepFailure || prep.Error.Code != "configured_tools_unavailable" {
		t.Fatalf("prep = %+v", prep)
	}
	details := prep.Error.Details.(*jsonx.Obj)
	tools := details.MustGet("tools").([]any)
	if len(tools) != 1 || tools[0] != "ghost" {
		t.Fatalf("missing = %v", tools)
	}
}

func TestPrepareGenerationReady(t *testing.T) {
	registry := prepRegistry(t)
	available := []ToolDescriptor{
		{Name: "bash", Description: "Run a shell command", Parameters: jsonx.ObjFrom("type", "object")},
		{Name: "read", Description: "Read a file"},
	}
	messages := []*jsonx.Obj{jsonx.ObjFrom("role", "user", "content", "hi")}
	base := jsonx.ObjFrom("timeoutMs", float64(30000))
	prep, err := PrepareGeneration(registry, prepIdentity(), []string{"bash", "read"}, available,
		func() ([]*jsonx.Obj, bool, error) { return messages, false, nil },
		func() (string, error) { return "You are pi.", nil },
		noopBeforeRequest, base)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Kind != GenerationPrepReady {
		t.Fatalf("prep = %+v", prep)
	}
	ready := prep.Prepared
	if ready.Model == nil || ready.Model.ID != ai.FauxDefaultModelID {
		t.Fatal("model lost")
	}
	if len(ready.Messages) != 1 || ready.SystemPrompt != "You are pi." {
		t.Fatalf("ready = %+v", ready)
	}
	if len(ready.Tools) != 2 || ready.Tools[0].MustGet("name") != "bash" {
		t.Fatalf("tools = %v", ready.Tools)
	}
	if ready.Tools[0].MustGet("description") != "Run a shell command" || ready.Tools[0].MustGet("parameters") == nil {
		t.Fatal("tool fields")
	}
	if ready.Tools[1].MustGet("parameters") != nil {
		// read has no parameters; the key is omitted.
		t.Fatal("parameters leaked for parameterless tool")
	}
	if ready.StreamOptions.MustGet("timeoutMs") != float64(30000) {
		t.Fatal("stream options lost")
	}
}

func TestPrepareGenerationCancelled(t *testing.T) {
	registry := prepRegistry(t)
	prep, err := PrepareGeneration(registry, prepIdentity(), nil, nil,
		func() ([]*jsonx.Obj, bool, error) { return nil, true, nil },
		func() (string, error) { return "", nil },
		noopBeforeRequest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Kind != GenerationPrepCancelled {
		t.Fatalf("prep = %+v", prep)
	}
}

func TestApplyStreamOptionsPatchJSON(t *testing.T) {
	base := jsonx.ObjFrom("timeoutMs", float64(30000), "maxRetries", float64(2))
	patch := jsonx.ObjFrom("timeoutMs", float64(60000), "maxRetries", nil, "temperature", float64(0.7))
	merged := applyStreamOptionsPatchJSON(base, patch)
	if merged.MustGet("timeoutMs") != float64(60000) {
		t.Fatal("patch value not applied")
	}
	if _, has := merged.Get("maxRetries"); has {
		t.Fatal("null patch did not clear")
	}
	if merged.MustGet("temperature") != float64(0.7) {
		t.Fatal("new key not added")
	}
	// Nil base returns the patch.
	if applyStreamOptionsPatchJSON(nil, patch) != patch {
		t.Fatal("nil base")
	}
}
