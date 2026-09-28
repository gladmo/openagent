package pico3

import "github.com/gladmo/openagent/jsonx"

// chooseThroughImpl mirrors the kinds/collapse.ts chooseThrough over
// pico3 entries: exchange grouping by the first model message's role,
// retention from the newest while under keepRecent, cut at the boundary.
func chooseThroughImpl(entries []*Entry, keepRecent float64, estimate func(messages []any) float64) (int64, bool) {
	type exchange struct {
		last   int64
		tokens float64
	}
	var exchanges []exchange
	var open *exchange
	for _, entry := range entries {
		role := entryModelRole(entry)
		messages := make([]any, 0, len(entry.Model))
		messages = append(messages, entry.Model...)
		tokens := estimate(messages)
		if role == "assistant" {
			exchanges = append(exchanges, exchange{last: entry.ID, tokens: tokens})
			open = &exchanges[len(exchanges)-1]
		} else if role == "toolResult" && open != nil {
			open.last = entry.ID
			open.tokens += tokens
		} else {
			open = nil
			exchanges = append(exchanges, exchange{last: entry.ID, tokens: tokens})
		}
	}
	retained := 0.0
	index := len(exchanges) - 1
	for index >= 0 && retained+exchanges[index].tokens <= keepRecent {
		retained += exchanges[index].tokens
		index--
	}
	if index < 0 {
		return 0, false
	}
	if index == len(exchanges)-1 {
		if index-1 < 0 {
			return 0, false
		}
		return exchanges[index-1].last, true
	}
	return exchanges[index].last, true
}

func entryModelRole(entry *Entry) string {
	if len(entry.Model) == 0 {
		return ""
	}
	first, ok := entry.Model[0].(*jsonx.Obj)
	if !ok {
		return ""
	}
	if v, ok := first.Get("role"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
