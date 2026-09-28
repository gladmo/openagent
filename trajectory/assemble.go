// trajectory/assemble.go: pure folds that assemble derived views from
// records — the conversation transcript, the turn outline, usage totals,
// and parent/child combination.
package trajectory

import (
	"sort"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// Transcript rebuilds the model-visible conversation from the message
// records (user/message, system/message, assistant/message). tool/result
// content is reachable through tool/result records; the transcript mirrors
// what a replay would feed back into a fresh agent's initial state.
func Transcript(records []Record) []ai.Message {
	var out []ai.Message
	for i := range records {
		rec := &records[i]
		switch rec.Type {
		case KindUserMessage, KindSystemMessage, KindAssistantMessage:
			obj, ok := rec.Data.(*jsonx.Obj)
			if !ok {
				continue
			}
			messageValue, ok := obj.Get("message")
			if !ok {
				continue
			}
			message, err := ai.MessageFromJSON(messageValue)
			if err != nil || message == nil {
				continue
			}
			out = append(out, message)
		}
	}
	return out
}

// TurnOutline summarizes each turn: its steps, tool calls, wall duration,
// and token totals.
type TurnOutline struct {
	Turn       int
	Steps      int
	ToolCalls  int
	Errors     int
	DurationMs float64
	Usage      ai.Usage
	HasUsage   bool
	Prompt     string
	Response   string
}

// Outline folds the record stream into per-turn summaries. Prompt previews
// the turn's first user message; Response previews the turn's last
// assistant text (each capped at 120 runes).
func Outline(records []Record) []TurnOutline {
	byTurn := map[int]*TurnOutline{}
	var order []int
	for i := range records {
		rec := &records[i]
		entry, ok := byTurn[rec.Turn]
		if !ok && rec.Turn > 0 {
			entry = &TurnOutline{Turn: rec.Turn}
			byTurn[rec.Turn] = entry
			order = append(order, rec.Turn)
		}
		if entry == nil {
			continue
		}
		switch rec.Type {
		case KindStepStart:
			entry.Steps++
		case KindToolCall:
			entry.ToolCalls++
		case KindError, KindAssistantAttempt:
			entry.Errors++
		case KindTurnEnd:
			if obj, ok := rec.Data.(*jsonx.Obj); ok {
				entry.DurationMs = durationOf(obj)
			}
		case KindUserMessage:
			if entry.Prompt == "" {
				entry.Prompt = messagePreview(rec, 120)
			}
		case KindAssistantMessage:
			if text := messagePreview(rec, 120); text != "" {
				entry.Response = text
			}
		case KindStepEnd:
			if usage, ok := usageFromRecord(rec); ok {
				entry.Usage = addUsage(entry.Usage, usage)
				entry.HasUsage = true
			}
		}
	}
	sort.Ints(order)
	out := make([]TurnOutline, 0, len(order))
	for _, turn := range order {
		out = append(out, *byTurn[turn])
	}
	return out
}

// UsageSummary aggregates token accounting across a whole trajectory.
type UsageSummary struct {
	Usage    ai.Usage
	HasUsage bool
	Turns    int
	Steps    int
}

// SumUsage totals step usage (model calls plus tool-reported usage). The
// second result is false when no usage was observed at all.
func SumUsage(records []Record) (ai.Usage, bool) {
	summary := UsageSummaryFrom(records)
	return summary.Usage, summary.HasUsage
}

// UsageSummaryFrom folds usage and counting records into one summary.
func UsageSummaryFrom(records []Record) UsageSummary {
	summary := UsageSummary{}
	for i := range records {
		rec := &records[i]
		switch rec.Type {
		case KindStepStart:
			summary.Steps++
		case KindTurnStart:
			summary.Turns++
		case KindStepEnd:
			if usage, ok := usageFromRecord(rec); ok {
				summary.Usage = addUsage(summary.Usage, usage)
				summary.HasUsage = true
			}
		}
	}
	return summary
}

// Combine merges trajectories by commit time (seq renumbered dense in the
// merged order; the earliest record's ignorable markers survive). Use it to
// assemble a parent with its nested child trajectories into one view.
func Combine(primary *Trajectory, others ...*Trajectory) []Record {
	type stamped struct {
		rec  Record
		ord  int
		time float64
	}
	var all []stamped
	collect := func(t *Trajectory, ord int) {
		if t == nil {
			return
		}
		for _, rec := range t.Snapshot() {
			all = append(all, stamped{rec: rec, ord: ord, time: rec.TimeMs})
		}
	}
	collect(primary, 0)
	for i, t := range others {
		collect(t, i+1)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].time != all[j].time {
			return all[i].time < all[j].time
		}
		return all[i].ord < all[j].ord
	})
	out := make([]Record, 0, len(all))
	for i := range all {
		rec := all[i].rec
		rec.Seq = int64(i) + 1
		out = append(out, rec)
	}
	return out
}

// ---------------------------------------------------------------------------
// Payload accessors shared by folds and exporters
// ---------------------------------------------------------------------------

func durationOf(obj *jsonx.Obj) float64 {
	if v, ok := obj.Get("durationMs"); ok {
		if f, ok := jsonx.ToFloat(v); ok {
			return f
		}
	}
	return 0
}

func usageFromRecord(rec *Record) (ai.Usage, bool) {
	obj, ok := rec.Data.(*jsonx.Obj)
	if !ok {
		return ai.Usage{}, false
	}
	value, ok := obj.Get("usage")
	if !ok {
		return ai.Usage{}, false
	}
	usageObj, ok := value.(*jsonx.Obj)
	if !ok {
		return ai.Usage{}, false
	}
	usage := ai.Usage{}
	if f := floatOf(usageObj, "input"); f != nil {
		usage.Input = *f
	}
	if f := floatOf(usageObj, "output"); f != nil {
		usage.Output = *f
	}
	if f := floatOf(usageObj, "cacheRead"); f != nil {
		usage.CacheRead = *f
	}
	if f := floatOf(usageObj, "cacheWrite"); f != nil {
		usage.CacheWrite = *f
	}
	if f := floatOf(usageObj, "cacheWrite1h"); f != nil {
		usage.CacheWrite1h = f
	}
	if f := floatOf(usageObj, "reasoning"); f != nil {
		usage.Reasoning = f
	}
	if f := floatOf(usageObj, "totalTokens"); f != nil {
		usage.TotalTokens = *f
	}
	if costValue, ok := usageObj.Get("cost"); ok {
		if cost, ok := costValue.(*jsonx.Obj); ok {
			if f := floatOf(cost, "total"); f != nil {
				usage.Cost.Total = *f
			}
		}
	}
	return usage, true
}

func floatOf(obj *jsonx.Obj, key string) *float64 {
	v, ok := obj.Get(key)
	if !ok {
		return nil
	}
	f, ok := jsonx.ToFloat(v)
	if !ok {
		return nil
	}
	return &f
}

// messagePreview extracts a bounded preview of a message record's content:
// string content directly, block content as the joined text blocks
// (thinking excluded). It reads only the serialized payload, so it works on
// records from memory or from a stored file.
func messagePreview(rec *Record, limit int) string {
	obj, ok := rec.Data.(*jsonx.Obj)
	if !ok {
		return ""
	}
	messageValue, ok := obj.Get("message")
	if !ok {
		return ""
	}
	message, ok := messageValue.(*jsonx.Obj)
	if !ok {
		return ""
	}
	contentValue, ok := message.Get("content")
	if !ok {
		return ""
	}
	var text string
	if s, ok := contentValue.(string); ok {
		text = s
	} else if arr, ok := contentValue.([]any); ok {
		for _, item := range arr {
			block, ok := item.(*jsonx.Obj)
			if !ok {
				continue
			}
			if typeValue, _ := block.Get("type"); typeValue == "text" {
				if t, ok := block.Get("text"); ok {
					if s, ok := t.(string); ok {
						text += s
					}
				}
			}
		}
	}
	runes := []rune(text)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}
