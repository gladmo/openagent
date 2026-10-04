package tui

import (
	"regexp"
	"sort"
	"strings"
)

// Port of fuzzy.ts: matches when all query characters appear in order (not
// necessarily consecutive); lower score = better match.

// FuzzyMatch is a fuzzy match result; lower Score is better.
type FuzzyMatch struct {
	Matches bool
	Score   float64
}

var wordBoundaryChar = regexp.MustCompile(`[\s\-_./:]`)

var alphaNumericPattern = regexp.MustCompile(`^([a-z]+)([0-9]+)$`)
var numericAlphaPattern = regexp.MustCompile(`^([0-9]+)([a-z]+)$`)

// FuzzyMatchQuery reports how well text matches query.
func FuzzyMatchQuery(query, text string) FuzzyMatch {
	queryLower := strings.ToLower(query)
	textLower := strings.ToLower(text)
	textRunes := []rune(textLower)

	matchQuery := func(normalizedQuery string) FuzzyMatch {
		if len(normalizedQuery) == 0 {
			return FuzzyMatch{Matches: true, Score: 0}
		}
		queryRunes := []rune(normalizedQuery)
		if len(queryRunes) > len(textRunes) {
			return FuzzyMatch{Matches: false, Score: 0}
		}

		queryIndex := 0
		score := 0.0
		lastMatchIndex := -1
		consecutiveMatches := 0

		for queryIndex < len(queryRunes) {
			i := indexFrom(textRunes, queryRunes[queryIndex], lastMatchIndex+1)
			if i == -1 {
				break
			}

			isWordBoundary := i == 0 || wordBoundaryChar.MatchString(string(textRunes[i-1]))

			// Reward consecutive matches
			if lastMatchIndex == i-1 {
				consecutiveMatches++
				score -= float64(consecutiveMatches) * 5
			} else {
				consecutiveMatches = 0
				// Penalize gaps
				if lastMatchIndex >= 0 {
					score += float64(i-lastMatchIndex-1) * 2
				}
			}

			// Reward word boundary matches
			if isWordBoundary {
				score -= 10
			}

			// Slight penalty for later matches
			score += float64(i) * 0.1

			lastMatchIndex = i
			queryIndex++
		}

		if queryIndex < len(queryRunes) {
			return FuzzyMatch{Matches: false, Score: 0}
		}

		if normalizedQuery == textLower {
			score -= 100
		}

		return FuzzyMatch{Matches: true, Score: score}
	}

	primaryMatch := matchQuery(queryLower)
	if primaryMatch.Matches {
		return primaryMatch
	}

	swappedQuery := ""
	if m := alphaNumericPattern.FindStringSubmatch(queryLower); m != nil {
		swappedQuery = m[2] + m[1]
	} else if m := numericAlphaPattern.FindStringSubmatch(queryLower); m != nil {
		swappedQuery = m[2] + m[1]
	}

	if swappedQuery == "" {
		return primaryMatch
	}

	swappedMatch := matchQuery(swappedQuery)
	if !swappedMatch.Matches {
		return primaryMatch
	}

	return FuzzyMatch{Matches: true, Score: swappedMatch.Score + 5}
}

func indexFrom(runes []rune, target rune, from int) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

// FuzzyFilter filters and sorts items by fuzzy match quality (best first).
// Whitespace- and slash-separated tokens must all match.
func FuzzyFilter[T any](items []T, query string, getText func(T) string) []T {
	if strings.TrimSpace(query) == "" {
		return items
	}

	var tokens []string
	for _, token := range regexp.MustCompile(`[\s/]+`).Split(strings.TrimSpace(query), -1) {
		if len(token) > 0 {
			tokens = append(tokens, token)
		}
	}

	if len(tokens) == 0 {
		return items
	}

	type scored struct {
		item  T
		score float64
	}
	var results []scored

	for _, item := range items {
		text := getText(item)
		totalScore := 0.0
		allMatch := true

		for _, token := range tokens {
			match := FuzzyMatchQuery(token, text)
			if match.Matches {
				totalScore += match.Score
			} else {
				allMatch = false
				break
			}
		}

		if allMatch {
			results = append(results, scored{item: item, score: totalScore})
		}
	}

	sort.SliceStable(results, func(a, b int) bool {
		return results[a].score < results[b].score
	})
	out := make([]T, len(results))
	for i, r := range results {
		out[i] = r.item
	}
	return out
}
