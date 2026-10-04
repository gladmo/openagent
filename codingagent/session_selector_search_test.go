package codingagent

// Tests for session selector search (adapted from
// session-selector-search.test.ts).

import (
	"strings"
	"testing"
	"time"
)

func searchSession(id, name, text string, modified time.Time) SessionSearchInfo {
	return SessionSearchInfo{ID: id, Name: name, AllMessagesText: text, Cwd: "/w", Modified: modified}
}

func TestParseSearchQueryEmpty(t *testing.T) {
	parsed := ParseSearchQuery("")
	if parsed.Mode != "tokens" || len(parsed.Tokens) != 0 || parsed.Regex != nil {
		t.Fatalf("parsed = %+v", parsed)
	}
	parsed = ParseSearchQuery("   ")
	if len(parsed.Tokens) != 0 {
		t.Fatalf("whitespace = %+v", parsed)
	}
}

func TestParseSearchQueryTokens(t *testing.T) {
	parsed := ParseSearchQuery("foo bar")
	if len(parsed.Tokens) != 2 {
		t.Fatalf("tokens = %+v", parsed.Tokens)
	}
	for _, token := range parsed.Tokens {
		if token.Kind != TokenFuzzy {
			t.Fatalf("kind = %v", token.Kind)
		}
	}
}

func TestParseSearchQueryPhrases(t *testing.T) {
	parsed := ParseSearchQuery(`foo "node cve" bar`)
	if len(parsed.Tokens) != 3 {
		t.Fatalf("tokens = %+v", parsed.Tokens)
	}
	if parsed.Tokens[1].Kind != TokenPhrase || parsed.Tokens[1].Value != "node cve" {
		t.Fatalf("phrase = %+v", parsed.Tokens[1])
	}
	if parsed.Tokens[0].Value != "foo" || parsed.Tokens[2].Value != "bar" {
		t.Fatalf("fuzzy = %+v", parsed.Tokens)
	}
}

func TestParseSearchQueryUnclosedQuote(t *testing.T) {
	parsed := ParseSearchQuery(`foo "unclosed`)
	if len(parsed.Tokens) != 2 {
		t.Fatalf("fallback tokens = %+v", parsed.Tokens)
	}
	for _, token := range parsed.Tokens {
		if token.Kind != TokenFuzzy {
			t.Fatalf("kind = %v", token.Kind)
		}
	}
	if parsed.Tokens[1].Value != "\"unclosed" {
		t.Fatalf("value = %q", parsed.Tokens[1].Value)
	}
}

func TestParseSearchQueryRegex(t *testing.T) {
	parsed := ParseSearchQuery("re:abc\\d+")
	if parsed.Mode != "regex" || parsed.Regex == nil || parsed.Error != "" {
		t.Fatalf("parsed = %+v", parsed)
	}
	if !parsed.Regex.MatchString("abc123") {
		t.Fatal("regex does not match")
	}

	if parsed := ParseSearchQuery("re:"); parsed.Error != "Empty regex" {
		t.Fatalf("empty = %+v", parsed)
	}
	if parsed := ParseSearchQuery("re:[unclosed"); parsed.Error == "" {
		t.Fatalf("invalid accepted = %+v", parsed)
	}
}

func TestMatchSessionEmptyQuery(t *testing.T) {
	session := searchSession("s1", "", "anything", time.Now())
	result := MatchSession(session, ParseSearchQuery(""))
	if !result.Matches {
		t.Fatal("empty query did not match")
	}
}

func TestMatchSessionFuzzyTokens(t *testing.T) {
	session := searchSession("s1", "my session", "fixed the parser bug", time.Now())
	if !MatchSession(session, ParseSearchQuery("parser")).Matches {
		t.Fatal("single token failed")
	}
	if !MatchSession(session, ParseSearchQuery("prs bug")).Matches {
		t.Fatal("fuzzy subsequence failed")
	}
	if MatchSession(session, ParseSearchQuery("zzz")).Matches {
		t.Fatal("non-matching token matched")
	}
	if MatchSession(session, ParseSearchQuery("parser zzz")).Matches {
		t.Fatal("one-of-two matched")
	}
}

func TestMatchSessionPhrase(t *testing.T) {
	session := searchSession("s1", "", "investigate node cve 2024", time.Now())
	if !MatchSession(session, ParseSearchQuery(`"node cve"`)).Matches {
		t.Fatal("phrase failed")
	}
	if MatchSession(session, ParseSearchQuery(`"cve node"`)).Matches {
		t.Fatal("reversed phrase matched")
	}
}

func TestMatchSessionRegex(t *testing.T) {
	session := searchSession("s1", "", "error 500 in handler", time.Now())
	if !MatchSession(session, ParseSearchQuery("re:error \\d+")).Matches {
		t.Fatal("regex failed")
	}
	if MatchSession(session, ParseSearchQuery("re:zzz")).Matches {
		t.Fatal("non-matching regex matched")
	}
	if MatchSession(session, ParseSearchQuery("re:[bad")).Matches {
		t.Fatal("invalid regex matched")
	}
}

func TestFilterAndSortSessionsRecent(t *testing.T) {
	sessions := []SessionSearchInfo{
		searchSession("s1", "", "alpha content", time.Now()),
		searchSession("s2", "", "beta content", time.Now()),
		searchSession("s3", "", "alpha other", time.Now()),
	}
	filtered := FilterAndSortSessions(sessions, "alpha", "recent", "all")
	if len(filtered) != 2 {
		t.Fatalf("filtered = %d", len(filtered))
	}
	// Recent keeps incoming order.
	if filtered[0].ID != "s1" || filtered[1].ID != "s3" {
		t.Fatalf("order = %v,%v", filtered[0].ID, filtered[1].ID)
	}
}

func TestFilterAndSortSessionsRelevance(t *testing.T) {
	base := time.Now()
	sessions := []SessionSearchInfo{
		searchSession("far", "", "prefix prefix prefix target", base.Add(-time.Hour)),
		searchSession("near", "", "target suffix", base),
	}
	filtered := FilterAndSortSessions(sessions, "target", "relevance", "all")
	if len(filtered) != 2 {
		t.Fatalf("filtered = %d", len(filtered))
	}
	// Earlier occurrence scores lower (better) -> near first.
	if filtered[0].ID != "near" {
		t.Fatalf("order = %s, %s", filtered[0].ID, filtered[1].ID)
	}
}

func TestFilterAndSortSessionsNameFilter(t *testing.T) {
	sessions := []SessionSearchInfo{
		searchSession("s1", "", "x", time.Now()),
		searchSession("s2", "named", "x", time.Now()),
	}
	if got := FilterAndSortSessions(sessions, "", "recent", "all"); len(got) != 2 {
		t.Fatalf("all = %d", len(got))
	}
	if got := FilterAndSortSessions(sessions, "", "recent", "named"); len(got) != 1 || got[0].ID != "s2" {
		t.Fatalf("named = %+v", got)
	}
}

func TestFilterAndSortSessionsInvalidRegex(t *testing.T) {
	sessions := []SessionSearchInfo{searchSession("s1", "", "x", time.Now())}
	if got := FilterAndSortSessions(sessions, "re:[bad", "relevance", "all"); len(got) != 0 {
		t.Fatalf("invalid = %+v", got)
	}
}

func TestNormalizeWhitespaceLower(t *testing.T) {
	if got := normalizeWhitespaceLower("  Hello   World  "); got != "hello world" {
		t.Fatalf("normalized = %q", got)
	}
	_ = strings.ToLower
}
