package codingagent

// Port of modes/interactive/components/session-selector-search.ts:
// query parsing (regex re: mode, quoted phrases, unclosed-quote
// fallback), session matching (regex index scoring, phrase indexOf,
// fuzzy tokens), and filter/sort with name gating.

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gladmo/openagent/tui"
)

// SessionSearchTokenKind distinguishes fuzzy from phrase tokens.
type SessionSearchTokenKind string

const (
	TokenFuzzy  SessionSearchTokenKind = "fuzzy"
	TokenPhrase SessionSearchTokenKind = "phrase"
)

// SessionSearchToken is one parsed query token.
type SessionSearchToken struct {
	Kind  SessionSearchTokenKind
	Value string
}

// ParsedSearchQuery is the parsed form.
type ParsedSearchQuery struct {
	Mode   string // "tokens" | "regex"
	Tokens []SessionSearchToken
	Regex  *regexp.Regexp
	// Error: parsing failed; the query is non-matching.
	Error string
}

// SessionSearchInfo is the session surface search reads.
type SessionSearchInfo struct {
	ID              string
	Name            string
	AllMessagesText string
	Cwd             string
	Modified        time.Time
}

// MatchResult reports one session's match (score lower = better).
type MatchResult struct {
	Matches bool
	Score   float64
}

var sessionWhitespacePattern = regexp.MustCompile(`\s+`)

func normalizeWhitespaceLower(text string) string {
	lowered := strings.ToLower(text)
	return strings.TrimSpace(sessionWhitespacePattern.ReplaceAllString(lowered, " "))
}

func getSessionSearchText(session SessionSearchInfo) string {
	return session.ID + " " + session.Name + " " + session.AllMessagesText + " " + session.Cwd
}

// HasSessionName reports a non-blank name.
func HasSessionName(session SessionSearchInfo) bool {
	return strings.TrimSpace(session.Name) != ""
}

func matchesNameFilter(session SessionSearchInfo, filter string) bool {
	if filter == "all" {
		return true
	}
	return HasSessionName(session)
}

// ParseSearchQuery parses re: regex or token queries with quote support.
func ParseSearchQuery(query string) ParsedSearchQuery {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return ParsedSearchQuery{Mode: "tokens"}
	}

	if strings.HasPrefix(trimmed, "re:") {
		pattern := strings.TrimSpace(trimmed[3:])
		if pattern == "" {
			return ParsedSearchQuery{Mode: "regex", Error: "Empty regex"}
		}
		compiled, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return ParsedSearchQuery{Mode: "regex", Error: err.Error()}
		}
		return ParsedSearchQuery{Mode: "regex", Regex: compiled}
	}

	var tokens []SessionSearchToken
	buf := ""
	inQuote := false

	flush := func(kind SessionSearchTokenKind) {
		value := strings.TrimSpace(buf)
		buf = ""
		if value == "" {
			return
		}
		tokens = append(tokens, SessionSearchToken{Kind: kind, Value: value})
	}

	for _, ch := range trimmed {
		if ch == '"' {
			if inQuote {
				flush(TokenPhrase)
				inQuote = false
			} else {
				flush(TokenFuzzy)
				inQuote = true
			}
			continue
		}
		if !inQuote && (ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r') {
			flush(TokenFuzzy)
			continue
		}
		buf += string(ch)
	}

	// Unclosed quotes: plain whitespace tokenization.
	if inQuote {
		var fallback []SessionSearchToken
		for _, part := range strings.Fields(trimmed) {
			if strings.TrimSpace(part) != "" {
				fallback = append(fallback, SessionSearchToken{Kind: TokenFuzzy, Value: strings.TrimSpace(part)})
			}
		}
		return ParsedSearchQuery{Mode: "tokens", Tokens: fallback}
	}

	flush(TokenFuzzy)
	return ParsedSearchQuery{Mode: "tokens", Tokens: tokens}
}

// MatchSession scores one session against a parsed query.
func MatchSession(session SessionSearchInfo, parsed ParsedSearchQuery) MatchResult {
	text := getSessionSearchText(session)

	if parsed.Mode == "regex" {
		if parsed.Regex == nil {
			return MatchResult{}
		}
		location := parsed.Regex.FindStringIndex(text)
		if location == nil {
			return MatchResult{}
		}
		return MatchResult{Matches: true, Score: float64(location[0]) * 0.1}
	}

	if len(parsed.Tokens) == 0 {
		return MatchResult{Matches: true}
	}

	totalScore := 0.0
	normalizedText := (*string)(nil)
	for _, token := range parsed.Tokens {
		if token.Kind == TokenPhrase {
			if normalizedText == nil {
				value := normalizeWhitespaceLower(text)
				normalizedText = &value
			}
			phrase := normalizeWhitespaceLower(token.Value)
			if phrase == "" {
				continue
			}
			index := strings.Index(*normalizedText, phrase)
			if index < 0 {
				return MatchResult{}
			}
			totalScore += float64(index) * 0.1
			continue
		}
		match := tui.FuzzyMatchQuery(token.Value, text)
		if !match.Matches {
			return MatchResult{}
		}
		totalScore += match.Score
	}
	return MatchResult{Matches: true, Score: totalScore}
}

// FilterAndSortSessions filters by query and orders by mode (recent
// keeps incoming order; relevance sorts by score with modified-desc
// tie-breaks).
func FilterAndSortSessions(sessions []SessionSearchInfo, query, sortMode, nameFilter string) []SessionSearchInfo {
	var nameFiltered []SessionSearchInfo
	for _, session := range sessions {
		if matchesNameFilter(session, nameFilter) {
			nameFiltered = append(nameFiltered, session)
		}
	}
	if strings.TrimSpace(query) == "" {
		return nameFiltered
	}

	parsed := ParseSearchQuery(query)
	if parsed.Error != "" {
		return nil
	}

	if sortMode == "recent" {
		var filtered []SessionSearchInfo
		for _, session := range nameFiltered {
			if MatchSession(session, parsed).Matches {
				filtered = append(filtered, session)
			}
		}
		return filtered
	}

	type scoredSession struct {
		session SessionSearchInfo
		score   float64
	}
	var scored []scoredSession
	for _, session := range nameFiltered {
		result := MatchSession(session, parsed)
		if !result.Matches {
			continue
		}
		scored = append(scored, scoredSession{session: session, score: result.Score})
	}
	sort.SliceStable(scored, func(a, b int) bool {
		if scored[a].score != scored[b].score {
			return scored[a].score < scored[b].score
		}
		return scored[a].session.Modified.After(scored[b].session.Modified)
	})
	out := make([]SessionSearchInfo, 0, len(scored))
	for _, entry := range scored {
		out = append(out, entry.session)
	}
	return out
}
