package agent

// Pins the ported search/index.ts contract shapes: field names, optionality,
// and the optional-method capability split.

import (
	"encoding/json"
	"testing"
)

func TestSearchQueryJSONShape(t *testing.T) {
	zero, err := json.Marshal(SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if string(zero) != `{"text":""}` {
		t.Fatalf("zero value: got %s", zero)
	}
	limit := 10.0
	full, err := json.Marshal(SearchQuery{Text: "parser bug", Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if string(full) != `{"text":"parser bug","limit":10}` {
		t.Fatalf("full value: got %s", full)
	}
}

func TestSessionSearchHitJSONShape(t *testing.T) {
	// Optional score/top stay absent on the wire.
	minimal, err := json.Marshal(SessionSearchHit{SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(minimal) != `{"sessionId":"s1"}` {
		t.Fatalf("minimal: got %s", minimal)
	}
	score := 0.75
	snippet := "best match"
	full, err := json.Marshal(SessionSearchHit{
		SessionID: "s1",
		Score:     &score,
		Top:       &SessionSearchTopHit{EntryID: "e9", Snippet: &snippet, Timestamp: 1234.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"sessionId":"s1","score":0.75,"top":{"entryId":"e9","snippet":"best match","timestamp":1234.5}}`
	if string(full) != want {
		t.Fatalf("full: got %s", full)
	}
	// Top without snippet keeps the optional snippet absent.
	topOnly, err := json.Marshal(SessionSearchHit{Top: &SessionSearchTopHit{EntryID: "e9", Timestamp: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if string(topOnly) != `{"sessionId":"","top":{"entryId":"e9","timestamp":1}}` {
		t.Fatalf("top without snippet: got %s", topOnly)
	}
}

func TestEntrySearchHitJSONShape(t *testing.T) {
	minimal, err := json.Marshal(EntrySearchHit{SessionID: "s1", EntryID: "e2", Timestamp: 99})
	if err != nil {
		t.Fatal(err)
	}
	if string(minimal) != `{"sessionId":"s1","entryId":"e2","timestamp":99}` {
		t.Fatalf("minimal: got %s", minimal)
	}
	score := 1.5
	snippet := "hit"
	full, err := json.Marshal(EntrySearchHit{
		SessionID: "s1", EntryID: "e2", Timestamp: 99, Snippet: &snippet, Score: &score,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(full) != `{"sessionId":"s1","entryId":"e2","timestamp":99,"snippet":"hit","score":1.5}` {
		t.Fatalf("full: got %s", full)
	}
}

type baseOnlySearchService struct{}

func (baseOnlySearchService) SearchSessions(query SearchQuery) ([]SessionSearchHit, error) {
	return nil, nil
}
func (baseOnlySearchService) Sync() error         { return nil }
func (baseOnlySearchService) Notify(string)       {}
func (baseOnlySearchService) Remove(string) error { return nil }
func (baseOnlySearchService) Close() error        { return nil }

type entryCapableSearchService struct{ baseOnlySearchService }

func (entryCapableSearchService) SearchEntries(query SearchQuery) ([]EntrySearchHit, error) {
	return nil, nil
}

func TestEntrySearchCapabilitySplit(t *testing.T) {
	var base SessionSearchService = baseOnlySearchService{}
	if _, ok := base.(EntrySearchCapable); ok {
		t.Fatal("base-only service must not satisfy EntrySearchCapable")
	}
	var full SessionSearchService = entryCapableSearchService{}
	capable, ok := full.(EntrySearchCapable)
	if !ok {
		t.Fatal("entry-capable service must upgrade to EntrySearchCapable")
	}
	if _, err := capable.SearchEntries(SearchQuery{Text: "x"}); err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}
}
