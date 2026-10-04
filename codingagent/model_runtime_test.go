package codingagent

// Tests for the model runtime, registry facade, auth storage, models
// store, and runtime credentials (ported from their reference tests).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gladmo/openagent/ai"
)

func newTestRuntime(t *testing.T, modelsJSON string) *ModelRuntime {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(EnvAgentDirName(), dir)

	modelsPath := ""
	if modelsJSON != "" {
		modelsPath = filepath.Join(dir, "models.json")
		os.WriteFile(modelsPath, []byte(modelsJSON), 0o644)
	} else {
		// No models.json: an explicit empty path avoids the default file.
		modelsPath = filepath.Join(dir, "absent-models.json")
	}

	runtime, err := NewModelRuntime(&CreateModelRuntimeOptions{
		AuthPath:      filepath.Join(dir, "auth.json"),
		ModelsPath:    modelsPath,
		HasModelsPath: true,
	})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	return runtime
}

func TestModelRuntimeBuiltinModels(t *testing.T) {
	runtime := newTestRuntime(t, "")
	models := runtime.GetModels()
	if len(models) == 0 {
		t.Fatal("no builtin models")
	}
	// The ported builtin catalog registers known providers.
	providers := map[string]bool{}
	for _, model := range models {
		providers[model.Provider] = true
	}
	for _, expected := range []string{"anthropic", "openai", "deepseek"} {
		if !providers[expected] {
			t.Errorf("provider %s missing", expected)
		}
	}
	if runtime.GetError() != "" {
		t.Fatalf("error = %s", runtime.GetError())
	}
}

func TestModelRuntimeFindModel(t *testing.T) {
	runtime := newTestRuntime(t, "")
	models := runtime.GetModels()
	if len(models) == 0 {
		t.Fatal("no models")
	}
	first := models[0]
	if found := runtime.GetModel(first.Provider, first.ID); found == nil {
		t.Fatalf("find %s/%s failed", first.Provider, first.ID)
	}
	if found := runtime.GetModel("nope", "nope"); found != nil {
		t.Fatalf("unexpected find = %+v", found)
	}
}

func TestModelRuntimeModelsJsonCustomProvider(t *testing.T) {
	runtime := newTestRuntime(t, `{
		"providers": {
			"my-gateway": {
				"name": "My Gateway",
				"baseUrl": "https://gw.example.com/v1",
				"apiKey": "static-key",
				"api": "openai-completions",
				"models": [{"id": "custom-1", "contextWindow": 64000}]
			}
		}
	}`)

	models := runtime.GetModels()
	found := false
	for _, model := range models {
		if model.Provider == "my-gateway" && model.ID == "custom-1" {
			found = true
			if model.ContextWindow != 64000 {
				t.Fatalf("window = %v", model.ContextWindow)
			}
			if model.BaseURL != "https://gw.example.com/v1" {
				t.Fatalf("url = %q", model.BaseURL)
			}
		}
	}
	if !found {
		t.Fatal("custom model missing")
	}

	// Composition errors stay empty.
	for id, message := range runtime.GetCompositionErrors() {
		t.Errorf("composition error %s: %s", id, message)
	}

	// Refresh marks the static-key provider configured.
	if err := runtime.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !runtime.HasConfiguredAuth("my-gateway") {
		t.Fatal("gateway not configured")
	}
	available := runtime.GetAvailableSnapshot()
	availableProviders := map[string]bool{}
	for _, model := range available {
		availableProviders[model.Provider] = true
	}
	if !availableProviders["my-gateway"] {
		t.Fatal("gateway model not available")
	}
}

func TestModelRuntimeCompositionError(t *testing.T) {
	// apiKey alone configures auth without models (no error); a fully
	// empty entry is the structural error.
	runtime := newTestRuntime(t, `{
		"providers": {
			"broken-provider": {}
		}
	}`)
	// Empty provider config (no baseUrl/models) is a composition error, but
	// builtins stay available.
	errors := runtime.GetCompositionErrors()
	if len(errors) == 0 {
		t.Fatal("expected composition error")
	}
	for id, message := range errors {
		if id == "broken-provider" && !strings.Contains(message, `must specify "baseUrl"`) {
			t.Errorf("error %s: %s", id, message)
		}
	}
	if len(runtime.GetModels()) == 0 {
		t.Fatal("builtins lost")
	}
}

func TestModelRuntimeRegisterExtension(t *testing.T) {
	runtime := newTestRuntime(t, "")

	hasKey := true
	if err := runtime.RegisterProvider("ext-provider", &ProviderConfigInput{
		APIKey: "ext-key", HasAPIKey: hasKey,
		BaseURL: "https://ext.example.com/v1",
		API:     "openai-completions",
		Models: []ProviderModelConfig{
			{ProviderModelConfigBase: ProviderModelConfigBase{ID: "ext-model", Name: "Ext Model", API: "openai-completions", Input: []string{"text"}}},
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	found := false
	for _, model := range runtime.GetModels() {
		if model.Provider == "ext-provider" && model.ID == "ext-model" {
			found = true
		}
	}
	if !found {
		t.Fatal("extension model missing")
	}
	if ids := runtime.GetRegisteredProviderIDs(); len(ids) == 0 {
		t.Fatal("no registrations")
	}
	if config := runtime.GetRegisteredProviderConfig("ext-provider"); config == nil {
		t.Fatal("config missing")
	}

	runtime.UnregisterProvider("ext-provider")
	for _, model := range runtime.GetModels() {
		if model.Provider == "ext-provider" {
			t.Fatal("extension survived unregister")
		}
	}
}

func TestModelRegistryFacade(t *testing.T) {
	runtime := newTestRuntime(t, "")
	registry := NewModelRegistry(runtime)

	models := registry.GetAll()
	if len(models) == 0 {
		t.Fatal("no models")
	}
	first := models[0]
	if found := registry.Find(first.Provider, first.ID); found == nil {
		t.Fatal("find failed")
	}
	if registry.GetProviderDisplayName(first.Provider) == "" {
		t.Fatal("display name empty")
	}
	if registry.GetError() != "" {
		t.Fatalf("error = %q", registry.GetError())
	}
	if registry.Runtime() != runtime {
		t.Fatal("runtime mismatch")
	}
	status := registry.GetProviderAuthStatus("anthropic")
	if status.Source == "" && !status.Configured {
		// Status shape exists; unconfigured builtin is valid.
		t.Log("anthropic unconfigured (expected without a key)")
	}
}

func TestAuthStorageRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	storage := NewAuthStorage(path, nil)

	key := "sk-stored"
	if _, err := storage.Modify("anthropic", func(current *ai.Credential) (*ai.Credential, error) {
		return &ai.Credential{Type: ai.CredentialTypeAPIKey, APIKey: &key}, nil
	}); err != nil {
		t.Fatalf("modify: %v", err)
	}

	credential, err := storage.Read("anthropic")
	if err != nil || credential == nil || credential.APIKey == nil || *credential.APIKey != "sk-stored" {
		t.Fatalf("read = %+v err=%v", credential, err)
	}

	infos, err := storage.List()
	if err != nil || len(infos) != 1 || infos[0].ProviderID != "anthropic" {
		t.Fatalf("list = %+v err=%v", infos, err)
	}

	// File perms are 0600.
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perms = %v", info.Mode().Perm())
	}

	// Delete removes.
	if err := storage.Delete("anthropic"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if credential, _ := storage.Read("anthropic"); credential != nil {
		t.Fatal("credential survived delete")
	}
}

func TestRuntimeCredentialsOverlay(t *testing.T) {
	storage := ai.NewInMemoryCredentialStore()
	credentials := NewRuntimeCredentials(storage)

	// No override: passthrough read.
	if credential, err := credentials.Read("p"); err != nil || credential != nil {
		t.Fatalf("read = %+v err=%v", credential, err)
	}

	// Override installs a runtime key.
	credentials.SetRuntimeApiKey("p", "runtime-key")
	if !credentials.HasRuntimeApiKey("p") {
		t.Fatal("override not detected")
	}
	credential, err := credentials.Read("p")
	if err != nil || credential == nil || credential.APIKey == nil || *credential.APIKey != "runtime-key" {
		t.Fatalf("override read = %+v err=%v", credential, err)
	}

	// List includes the override.
	infos, err := credentials.List()
	if err != nil || len(infos) != 1 || infos[0].Type != ai.CredentialTypeAPIKey {
		t.Fatalf("list = %+v err=%v", infos, err)
	}

	// Remove drops it.
	credentials.RemoveRuntimeApiKey("p")
	if credentials.HasRuntimeApiKey("p") {
		t.Fatal("override survived removal")
	}
	if credential, _ := credentials.Read("p"); credential != nil {
		t.Fatal("override leaked after removal")
	}
}

func TestFileModelsStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models-store.json")
	store := NewFileModelsStore(path)

	entry := ai.ModelsStoreEntry{Models: []ai.Model{{ID: "m", Provider: "p"}}}
	if err := store.Write("p", entry, nil); err != nil {
		t.Fatalf("write: %v", err)
	}
	read, err := store.Read("p", nil)
	if err != nil || read == nil || len(read.Models) != 1 || read.Models[0].ID != "m" {
		t.Fatalf("read = %+v err=%v", read, err)
	}

	// A new handle sees the same file.
	second := NewFileModelsStore(path)
	if read, _ := second.Read("p", nil); read == nil {
		t.Fatal("second handle empty")
	}

	if err := store.Delete("p", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if read, _ := store.Read("p", nil); read != nil {
		t.Fatal("entry survived delete")
	}
}

func TestAuthStorageConcurrentModify(t *testing.T) {
	dir := t.TempDir()
	storage := NewAuthStorage(filepath.Join(dir, "auth.json"), nil)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "key-" + string(rune('a'+n))
			_, _ = storage.Modify("provider-"+string(rune('a'+n%3)), func(current *ai.Credential) (*ai.Credential, error) {
				return &ai.Credential{Type: ai.CredentialTypeAPIKey, APIKey: &key}, nil
			})
		}(i)
	}
	wg.Wait()

	infos, err := storage.List()
	if err != nil || len(infos) != 3 {
		t.Fatalf("list = %d err=%v", len(infos), err)
	}
	var data map[string]json.RawMessage
	raw, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	json.Unmarshal(raw, &data)
	if len(data) != 3 {
		t.Fatalf("file = %d entries", len(data))
	}
}

// TestSetRuntimeApiKeyFeedsAvailabilityAndAuth pins the --api-key path: the
// override must mark the provider configured AND be visible to the
// request-side auth resolution.
func TestSetRuntimeApiKeyFeedsAvailabilityAndAuth(t *testing.T) {
	runtime := newTestRuntime(t, "")

	// zai resolves from ZAI_API_KEY / stored credentials only; without one
	// it is not configured.
	if err := runtime.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if runtime.HasConfiguredAuth("zai") {
		t.Fatal("zai configured without any key")
	}

	runtime.SetRuntimeApiKey("zai", "runtime-zai-key")
	if !runtime.HasRuntimeApiKey("zai") {
		t.Fatal("override not recorded")
	}
	if runtime.HasConfiguredAuth("zai") {
		t.Fatal("pre-refresh snapshot should still be stale")
	}
	if err := runtime.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !runtime.HasConfiguredAuth("zai") {
		t.Fatal("override did not mark the provider configured")
	}

	// The request path resolves the same override.
	auth, ok := runtime.GetAuth("zai")
	if !ok || auth == nil || auth.Auth.APIKey == nil || *auth.Auth.APIKey != "runtime-zai-key" {
		t.Fatalf("GetAuth = %+v ok=%v", auth, ok)
	}
}
