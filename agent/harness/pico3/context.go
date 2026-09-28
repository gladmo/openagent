package pico3

// context.go ports harness/pico3/context.ts: deriveContext (pico §1.3
// verbatim) and tool-result reordering with fork-cut synthesis.

import (
	"github.com/gladmo/openagent/jsonx"
)

// ContextView mirrors the TS interface.
type ContextView struct {
	Head     *Entry
	Entries  []*Entry
	Messages []any // stored messages (JSON values)
}

// DeriveContext implements pico §1.3:
//
//	H       = newest fork-visible entry at or before T with a head
//	from    = H ? H.head : transcript start
//	range   = fork-visible entries from `from` through T
//	edits   = per target, newest edit in range wins
//	entries = H ? [H, ...range without any head entries] : range
//	model   = concat(entries.map(e => edits[e.id] ? apply : e.model)), then reorder
//
// Display-only entries (no model) contribute nothing. at == nil means the
// newest tip.
func DeriveContext(storage Storage, conversationID Id, at *Id, ctx ContextAlias) (*ContextView, error) {
	headScan := EntryScan{ConversationID: conversationID, WithHead: true, Limit: 1}
	if at != nil {
		before := *at + 1
		headScan.Before = &before
	}
	headPage, err := storage.ScanEntries(headScan)
	if err != nil {
		return nil, err
	}
	var head *Entry
	if len(headPage) > 0 {
		head = headPage[0]
	}
	var from *Id
	if head != nil {
		from = head.Head
	}

	// Walk newest-first until we pass `from` (or exhaust).
	var rangeEntries []*Entry
	beforeSet := at != nil
	beforeValue := Id(0)
	if at != nil {
		beforeValue = *at + 1
	}
	for {
		scan := EntryScan{ConversationID: conversationID, Limit: 256}
		if beforeSet {
			b := beforeValue
			scan.Before = &b
		}
		page, err := storage.ScanEntries(scan)
		if err != nil {
			return nil, err
		}
		done := len(page) < 256
		for _, entry := range page {
			if from != nil && entry.ID < *from {
				done = true
				break
			}
			rangeEntries = append(rangeEntries, entry)
		}
		if done {
			break
		}
		beforeValue = page[len(page)-1].ID
		beforeSet = true
	}
	// Reverse to oldest-first.
	for i, j := 0, len(rangeEntries)-1; i < j; i, j = i+1, j-1 {
		rangeEntries[i], rangeEntries[j] = rangeEntries[j], rangeEntries[i]
	}

	// Per-target edits; later wins by iteration order.
	edits := map[Id]*ContextEdit{}
	for _, entry := range rangeEntries {
		for i := range entry.Edits {
			editCopy := entry.Edits[i]
			edits[editCopy.Target] = &editCopy
		}
	}

	entries := rangeEntries
	if head != nil {
		entries = []*Entry{head}
		for _, entry := range rangeEntries {
			if entry.Head == nil {
				entries = append(entries, entry)
			}
		}
	}

	var messages []any
	for _, entry := range entries {
		edit := edits[entry.ID]
		if edit != nil && edit.Action == "omit" {
			continue
		}
		if edit != nil && edit.Action == "replace" {
			for _, message := range edit.Messages {
				messages = append(messages, message)
			}
			continue
		}
		if entry.Model != nil {
			messages = append(messages, entry.Model...)
		}
	}
	return &ContextView{Head: head, Entries: entries, Messages: ReorderToolResults(messages)}, nil
}

// ReorderToolResults puts tool results back in call order after an
// assistant message, synthesising a missing one after a fork cut.
func ReorderToolResults(messages []any) []any {
	out := []any{}
	for i := 0; i < len(messages); i++ {
		message := messages[i]
		out = append(out, message)
		messageObj, ok := message.(*jsonx.Obj)
		if !ok || stringOfAny(messageObj, "role") != "assistant" {
			continue
		}
		calls := toolCallsOf(messageObj)
		if len(calls) == 0 {
			continue
		}
		results := map[string]*jsonx.Obj{}
		j := i + 1
		for j < len(messages) {
			resultObj, ok := messages[j].(*jsonx.Obj)
			if !ok || stringOfAny(resultObj, "role") != "toolResult" {
				break
			}
			results[stringOfAny(resultObj, "toolCallId")] = resultObj
			j++
		}
		for _, call := range calls {
			callID := stringOfAny(call, "id")
			if result, ok := results[callID]; ok {
				out = append(out, result)
			} else {
				out = append(out, synthesizedResult(call, messageObj))
			}
		}
		i = j - 1
	}
	return out
}

func toolCallsOf(message *jsonx.Obj) []*jsonx.Obj {
	contentValue, ok := message.Get("content")
	if !ok {
		return nil
	}
	content, ok := contentValue.([]any)
	if !ok {
		return nil
	}
	var calls []*jsonx.Obj
	for _, item := range content {
		if obj, ok := item.(*jsonx.Obj); ok {
			if t, _ := obj.Get("type"); t == "toolCall" {
				calls = append(calls, obj)
			}
		}
	}
	return calls
}

func synthesizedResult(call, assistant *jsonx.Obj) *jsonx.Obj {
	result := jsonx.NewObj()
	result.Set("role", "toolResult")
	result.Set("toolCallId", stringOfAny(call, "id"))
	result.Set("toolName", stringOfAny(call, "name"))
	text := jsonx.NewObj()
	text.Set("type", "text")
	text.Set("text", "Tool result unavailable: history ends before this call completed.")
	result.Set("content", []any{text})
	result.Set("isError", true)
	details := jsonx.NewObj()
	details.Set("reason", "missing_after_fork")
	result.Set("details", details)
	if ts, ok := assistant.Get("timestamp"); ok {
		result.Set("timestamp", ts)
	}
	return result
}

func stringOfAny(obj *jsonx.Obj, key string) string {
	if v, ok := obj.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
