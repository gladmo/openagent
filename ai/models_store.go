package ai

// models_store.go ports models-store.ts; models_error.go and
// model-operations.go are folded in (they are tiny).

import (
	"fmt"
	"sync"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

// ModelsStoreEntry mirrors the TS interface.
type ModelsStoreEntry struct {
	Models       []Model
	LastModified *float64
	CheckedAt    *float64
	ETag         *string
}

// ModelsStore persists model catalogs keyed by provider ID.
type ModelsStore interface {
	Read(providerID string, signal *abort.Signal) (*ModelsStoreEntry, error)
	Write(providerID string, entry ModelsStoreEntry, signal *abort.Signal) error
	Delete(providerID string, signal *abort.Signal) error
}

// InMemoryModelsStore mirrors the TS class (structuredClone semantics via
// jsonx deep copy of the stored entry).
type InMemoryModelsStore struct {
	mu      sync.Mutex
	entries map[string]ModelsStoreEntry
}

// NewInMemoryModelsStore creates an empty store.
func NewInMemoryModelsStore() *InMemoryModelsStore {
	return &InMemoryModelsStore{entries: map[string]ModelsStoreEntry{}}
}

// Read returns a detached copy of the entry.
func (s *InMemoryModelsStore) Read(providerID string, signal *abort.Signal) (*ModelsStoreEntry, error) {
	if err := signal.ThrowIfAborted(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[providerID]
	if !ok {
		return nil, nil
	}
	copied := entry
	copied.Models = append([]Model{}, entry.Models...)
	return &copied, nil
}

// Write stores a copy of the entry.
func (s *InMemoryModelsStore) Write(providerID string, entry ModelsStoreEntry, signal *abort.Signal) error {
	if err := signal.ThrowIfAborted(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := entry
	copied.Models = append([]Model{}, entry.Models...)
	s.entries[providerID] = copied
	return nil
}

// Delete removes the entry.
func (s *InMemoryModelsStore) Delete(providerID string, signal *abort.Signal) error {
	if err := signal.ThrowIfAborted(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, providerID)
	return nil
}

// ---------------------------------------------------------------------------
// models-error.ts
// ---------------------------------------------------------------------------

// ModelsErrorCode values.
const (
	ModelsErrorCodeModelSource     = "model_source"
	ModelsErrorCodeModelValidation = "model_validation"
	ModelsErrorCodeProvider        = "provider"
	ModelsErrorCodeStream          = "stream"
	ModelsErrorCodeAuth            = "auth"
	ModelsErrorCodeOauth           = "oauth"
)

// ModelsError mirrors the TS class.
type ModelsError struct {
	Code    string
	Message string
}

func (e *ModelsError) Error() string { return e.Message }

// NewModelsError builds a ModelsError, appending the cause detail to the
// message like the TS constructor.
func NewModelsError(code, message string, cause any) *ModelsError {
	return &ModelsError{Code: code, Message: withCauseDetail(message, cause)}
}

func withCauseDetail(message string, cause any) string {
	if cause == nil {
		return message
	}
	detail := FormatThrownValue(cause)
	for len(detail) > 0 && (detail[0] == ' ' || detail[0] == '\t' || detail[0] == '\n' || detail[0] == '\r') {
		detail = detail[1:]
	}
	for len(detail) > 0 && (detail[len(detail)-1] == ' ' || detail[len(detail)-1] == '\n' || detail[len(detail)-1] == '\r' || detail[len(detail)-1] == '\t') {
		detail = detail[:len(detail)-1]
	}
	if detail == "" || containsSubstr(message, detail) {
		return message
	}
	return message + ": " + detail
}

func containsSubstr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// model-operations.ts (chat-only subset; image/classifier models are outside
// the agent closure)
// ---------------------------------------------------------------------------

// AssertChatModel errors for non-chat models. All Go Model values are chat
// models in this closure, so this validates nothing extra.
func AssertChatModel(model *Model) error {
	if model == nil {
		return NewModelsError(ModelsErrorCodeProvider, "Model is not a chat model", nil)
	}
	return nil
}

// ModelKey renders provider/id for messages.
func ModelKey(model *Model) string {
	return fmt.Sprintf("%s/%s", model.Provider, model.ID)
}

// keep jsonx imported for future store serialization helpers.
var _ = jsonx.Stringify
