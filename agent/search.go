package agent

// search.go ports search/index.ts: the abstract session search contracts.

import "github.com/gladmo/openagent/ai"

// SearchQuery mirrors the TS interface.
type SearchQuery struct {
	Text     string   `json:"text"`
	Limit    *float64 `json:"limit,omitempty"`
	Provider *string  `json:"provider,omitempty"`
	Model    *string  `json:"model,omitempty"`
}

// SessionSearchHit mirrors the TS interface.
type SessionSearchHit struct {
	SessionID string  `json:"sessionId"`
	Score     float64 `json:"score"`
}

// EntrySearchHit mirrors the TS interface.
type EntrySearchHit struct {
	SessionID string                `json:"sessionId"`
	EntryID   string                `json:"entryId"`
	Timestamp float64               `json:"timestamp"`
	Message   *ai.ToolResultMessage `json:"message,omitempty"`
	Snippet   string                `json:"snippet"`
	Score     float64               `json:"score"`
}

// SessionSearchService mirrors the TS interface (abstract; no
// implementation ships in this package).
type SessionSearchService interface {
	SearchSessions(query SearchQuery) ([]SessionSearchHit, error)
	SearchEntries(query SearchQuery) ([]EntrySearchHit, error)
}
