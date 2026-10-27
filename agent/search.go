package agent

// search.go ports search/index.ts: the abstract session search contracts.

// SearchQuery mirrors the TS interface.
type SearchQuery struct {
	Text  string   `json:"text"`
	Limit *float64 `json:"limit,omitempty"`
}

// SessionSearchTopHit is the best-match entry surfaced on a
// SessionSearchHit, for display.
type SessionSearchTopHit struct {
	EntryID   string  `json:"entryId"`
	Snippet   *string `json:"snippet,omitempty"`
	Timestamp float64 `json:"timestamp"`
}

// SessionSearchHit mirrors the TS interface.
type SessionSearchHit struct {
	SessionID string               `json:"sessionId"`
	Score     *float64             `json:"score,omitempty"`
	Top       *SessionSearchTopHit `json:"top,omitempty"`
}

// EntrySearchHit mirrors the TS interface.
type EntrySearchHit struct {
	SessionID string   `json:"sessionId"`
	EntryID   string   `json:"entryId"`
	Timestamp float64  `json:"timestamp"`
	Snippet   *string  `json:"snippet,omitempty"`
	Score     *float64 `json:"score,omitempty"`
}

// SessionSearchService mirrors the TS interface (abstract; no
// implementation ships in this package).
type SessionSearchService interface {
	SearchSessions(query SearchQuery) ([]SessionSearchHit, error)
	// Sync enumerates sessions and catches up all indexing cursors.
	Sync() error
	// Notify is a freshness hint triggering a debounced pull of one session.
	Notify(sessionID string)
	// Remove drops the session's index state.
	Remove(sessionID string) error
	Close() error
}

// EntrySearchCapable marks the optional searchEntries method via interface
// upgrade, mirroring the TS optional method.
type EntrySearchCapable interface {
	SessionSearchService
	SearchEntries(query SearchQuery) ([]EntrySearchHit, error)
}
