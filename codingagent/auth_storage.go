package codingagent

// Port of core/auth-storage.ts (reduced), core/models-store.ts, and
// core/runtime-credentials.ts: auth.json credential storage, JSON-backed
// model catalog storage, and the runtime API-key overlay. proper-lockfile
// maps to an in-process mutex plus an O_EXCL lock file (documented
// deviation: single-host locking).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/ai"
)

// =============================================================================
// auth-storage.ts
// =============================================================================

// AuthStorage is a CredentialStore backed by auth.json.
type AuthStorage struct {
	mu   sync.Mutex
	path string
}

// NewAuthStorage opens (creating when needed) the auth store.
func NewAuthStorage(authPath string, _ any) *AuthStorage {
	path := authPath
	if path == "" {
		path = GetAuthPath()
	}
	path = NormalizePathDefault(path)
	return &AuthStorage{path: path}
}

func (s *AuthStorage) load() map[string]ai.Credential {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return map[string]ai.Credential{}
	}
	var parsed map[string]ai.Credential
	if err := json.Unmarshal([]byte(StripBom(string(data))), &parsed); err != nil {
		return map[string]ai.Credential{}
	}
	if parsed == nil {
		return map[string]ai.Credential{}
	}
	return parsed
}

func (s *AuthStorage) save(data map[string]ai.Credential) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(encoded, '\n'), 0o600)
}

// Read implements CredentialStore.
func (s *AuthStorage) Read(providerID string) (*ai.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if credential, ok := s.load()[providerID]; ok {
		cloned := credential
		return &cloned, nil
	}
	return nil, nil
}

// List implements CredentialStore.
func (s *AuthStorage) List() ([]ai.CredentialInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.load()
	out := make([]ai.CredentialInfo, 0, len(data))
	for providerID, credential := range data {
		out = append(out, ai.CredentialInfo{ProviderID: providerID, Type: credential.Type})
	}
	return out, nil
}

// Modify implements CredentialStore.
func (s *AuthStorage) Modify(providerID string, fn func(*ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.load()
	var current *ai.Credential
	if credential, ok := data[providerID]; ok {
		cloned := credential
		current = &cloned
	}
	next, err := fn(current)
	if err != nil {
		return current, err
	}
	if next != nil {
		data[providerID] = *next
	} else {
		delete(data, providerID)
	}
	if err := s.save(data); err != nil {
		return current, err
	}
	if next == nil {
		return nil, nil
	}
	stored := *next
	return &stored, nil
}

// Delete implements CredentialStore.
func (s *AuthStorage) Delete(providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.load()
	delete(data, providerID)
	return s.save(data)
}

func errAborted() error { return &abortError{"Aborted"} }

type abortError struct{ message string }

func (e *abortError) Error() string { return e.message }

// =============================================================================
// models-store.ts
// =============================================================================

// InMemoryCodingAgentModelsStore is the in-memory ModelsStore.
type InMemoryCodingAgentModelsStore struct {
	mu      sync.Mutex
	entries map[string]ai.ModelsStoreEntry
}

// NewInMemoryCodingAgentModelsStore creates an empty store.
func NewInMemoryCodingAgentModelsStore() *InMemoryCodingAgentModelsStore {
	return &InMemoryCodingAgentModelsStore{entries: map[string]ai.ModelsStoreEntry{}}
}

// Read implements ModelsStore.
func (s *InMemoryCodingAgentModelsStore) Read(providerID string, signal *abort.Signal) (*ai.ModelsStoreEntry, error) {
	if signal != nil && signal.Aborted() {
		return nil, errAborted()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.entries[providerID]; ok {
		cloned := entry
		return &cloned, nil
	}
	return nil, nil
}

// Write implements ModelsStore.
func (s *InMemoryCodingAgentModelsStore) Write(providerID string, entry ai.ModelsStoreEntry, signal *abort.Signal) error {
	if signal != nil && signal.Aborted() {
		return errAborted()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[providerID] = entry
	return nil
}

// Delete implements ModelsStore.
func (s *InMemoryCodingAgentModelsStore) Delete(providerID string, signal *abort.Signal) error {
	if signal != nil && signal.Aborted() {
		return errAborted()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, providerID)
	return nil
}

// FileModelsStore is the JSON-backed catalog store.
type FileModelsStore struct {
	mu   sync.Mutex
	path string
}

// NewFileModelsStore opens (creating when needed) the store.
func NewFileModelsStore(path string) *FileModelsStore {
	if path == "" {
		path = filepath.Join(GetAgentDir(), "models-store.json")
	}
	return &FileModelsStore{path: NormalizePathDefault(path)}
}

func (s *FileModelsStore) load() map[string]ai.ModelsStoreEntry {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return map[string]ai.ModelsStoreEntry{}
	}
	var parsed map[string]ai.ModelsStoreEntry
	if err := json.Unmarshal([]byte(StripBom(string(data))), &parsed); err != nil {
		return map[string]ai.ModelsStoreEntry{}
	}
	if parsed == nil {
		return map[string]ai.ModelsStoreEntry{}
	}
	return parsed
}

func (s *FileModelsStore) save(data map[string]ai.ModelsStoreEntry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(encoded, '\n'), 0o644)
}

// Read implements ModelsStore.
func (s *FileModelsStore) Read(providerID string, signal *abort.Signal) (*ai.ModelsStoreEntry, error) {
	if signal != nil && signal.Aborted() {
		return nil, errAborted()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.load()[providerID]; ok {
		cloned := entry
		return &cloned, nil
	}
	return nil, nil
}

// Write implements ModelsStore.
func (s *FileModelsStore) Write(providerID string, entry ai.ModelsStoreEntry, signal *abort.Signal) error {
	if signal != nil && signal.Aborted() {
		return errAborted()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.load()
	data[providerID] = entry
	return s.save(data)
}

// Delete implements ModelsStore.
func (s *FileModelsStore) Delete(providerID string, signal *abort.Signal) error {
	if signal != nil && signal.Aborted() {
		return errAborted()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.load()
	delete(data, providerID)
	return s.save(data)
}

// =============================================================================
// runtime-credentials.ts
// =============================================================================

// RuntimeCredentials overlays non-persistent runtime API keys.
type RuntimeCredentials struct {
	mu        sync.Mutex
	store     ai.CredentialStore
	overrides map[string]string
}

// NewRuntimeCredentials wraps a store.
func NewRuntimeCredentials(store ai.CredentialStore) *RuntimeCredentials {
	return &RuntimeCredentials{store: store, overrides: map[string]string{}}
}

// SetRuntimeApiKey installs a runtime override.
func (r *RuntimeCredentials) SetRuntimeApiKey(providerID, apiKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.overrides[providerID] = apiKey
}

// RemoveRuntimeApiKey drops a runtime override.
func (r *RuntimeCredentials) RemoveRuntimeApiKey(providerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.overrides, providerID)
}

// HasRuntimeApiKey reports an override's presence.
func (r *RuntimeCredentials) HasRuntimeApiKey(providerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.overrides[providerID]
	return ok
}

// Read implements CredentialStore.
func (r *RuntimeCredentials) Read(providerID string) (*ai.Credential, error) {
	r.mu.Lock()
	override, hasOverride := r.overrides[providerID]
	r.mu.Unlock()
	if hasOverride {
		key := override
		return &ai.Credential{Type: ai.CredentialTypeAPIKey, APIKey: &key}, nil
	}
	return r.store.Read(providerID)
}

// List implements CredentialStore.
func (r *RuntimeCredentials) List() ([]ai.CredentialInfo, error) {
	entries, err := r.store.List()
	if err != nil {
		return nil, err
	}
	byProvider := map[string]ai.CredentialInfo{}
	for _, entry := range entries {
		byProvider[entry.ProviderID] = entry
	}
	r.mu.Lock()
	for providerID := range r.overrides {
		byProvider[providerID] = ai.CredentialInfo{ProviderID: providerID, Type: ai.CredentialTypeAPIKey}
	}
	r.mu.Unlock()
	out := make([]ai.CredentialInfo, 0, len(byProvider))
	for _, entry := range byProvider {
		out = append(out, entry)
	}
	return out, nil
}

// Modify implements CredentialStore.
func (r *RuntimeCredentials) Modify(providerID string, fn func(*ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	return r.store.Modify(providerID, fn)
}

// Delete implements CredentialStore.
func (r *RuntimeCredentials) Delete(providerID string) error {
	if err := r.store.Delete(providerID); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.overrides, providerID)
	r.mu.Unlock()
	return nil
}

// envLookup is the runtime env accessor seam.
func envLookup(name string) string { return os.Getenv(name) }

var _ = strings.TrimSpace
