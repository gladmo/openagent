// trajectory/export.go: sinks and exporters — live stdout NDJSON (the
// default tap), the JSONL file sink, and post-hoc Markdown / JSON / JSONL
// exports.
package trajectory

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/gladmo/openagent/jsonx"
)

// Default bounding for the stdout projection, mirroring the reference
// headless stream: per-string cap 8 KiB, per-line cap 32 KiB (newline
// included). Every cut sets truncated:true.
const (
	DefaultMaxStringBytes = 8 * 1024
	DefaultMaxLineBytes   = 32 * 1024
)

// Sink receives committed records. Implementations must be safe for use
// from the publishing goroutine only (the trajectory serializes delivery).
type Sink interface {
	Write(rec *Record) error
	Flush() error
	Close() error
}

// StdoutSink writes one bounded JSON line per record to w. It is the
// default trajectory tap.
type StdoutSink struct {
	W              io.Writer
	MaxStringBytes int
	MaxLineBytes   int
}

// NewStdoutSink creates the default tap over w with default bounds.
func NewStdoutSink(w io.Writer) *StdoutSink {
	return &StdoutSink{W: w, MaxStringBytes: DefaultMaxStringBytes, MaxLineBytes: DefaultMaxLineBytes}
}

// Write emits the bounded record line.
func (s *StdoutSink) Write(rec *Record) error {
	_, err := fmt.Fprintln(s.W, BoundRecordLine(rec, s.MaxStringBytes, s.MaxLineBytes))
	return err
}

// Flush is a no-op (unbuffered writes).
func (s *StdoutSink) Flush() error { return nil }

// Close is a no-op; the writer stays owned by the caller.
func (s *StdoutSink) Close() error { return nil }

// FileSink streams committed records into a FileStore.
type FileSink struct {
	Store *FileStore
}

// NewFileSink creates the trajectory file at path with the given header.
func NewFileSink(path string, header FileHeader) (*FileSink, error) {
	store, err := NewFileStore(path, header)
	if err != nil {
		return nil, err
	}
	return &FileSink{Store: store}, nil
}

// Write appends one record line (buffered until Flush).
func (s *FileSink) Write(rec *Record) error { return s.Store.Write(rec) }

// Flush flushes and syncs the file.
func (s *FileSink) Flush() error { return s.Store.Flush() }

// Close flushes and closes the file; idempotent.
func (s *FileSink) Close() error { return s.Store.Close() }

// MultiSink fans out to several sinks. The first write error is returned
// and later sinks in the list still receive the record.
type MultiSink []Sink

// Write delivers the record to every sink.
func (m MultiSink) Write(rec *Record) error {
	var firstErr error
	for _, sink := range m {
		if err := sink.Write(rec); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Flush flushes every sink, returning the first error.
func (m MultiSink) Flush() error {
	var firstErr error
	for _, sink := range m {
		if err := sink.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Close closes every sink, returning the first error.
func (m MultiSink) Close() error {
	var firstErr error
	for _, sink := range m {
		if err := sink.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// PipeSubscribe wires a sink onto a trajectory for live records only and
// returns the disposer. Delivery errors are swallowed (the sink is an
// observer; implement a custom Sink to surface failures). Attach sinks
// before the first Append, or use PipeSubscribeAfter to catch up.
func PipeSubscribe(t *Trajectory, sink Sink) func() {
	return t.Subscribe(func(rec *Record) {
		if err := sink.Write(rec); err != nil {
			return
		}
		_ = sink.Flush()
	})
}

// PipeSubscribeAfter wires a sink whose delivery starts with the first
// committed record after afterSeq (0 replays the whole log) and then
// follows live commits atomically — the right wiring for a sink attached
// to a trajectory that already has records.
func PipeSubscribeAfter(t *Trajectory, sink Sink, afterSeq int64) func() {
	return t.SubscribeAfter(afterSeq, func(rec *Record) {
		if err := sink.Write(rec); err != nil {
			return
		}
		_ = sink.Flush()
	})
}

// ---------------------------------------------------------------------------
// Bounded line projection
// ---------------------------------------------------------------------------

// BoundRecordLine renders one record as a JSON line under both caps: every
// string (object keys included) is cut at maxStringBytes, and the full line
// at maxLineBytes. Over-limit lines degrade to scalars, then to
// {type,truncated}; every cut adds truncated:true.
func BoundRecordLine(rec *Record, maxStringBytes, maxLineBytes int) string {
	bounded := boundValue(rec.ToJSON(), maxStringBytes)
	line := jsonx.Stringify(bounded)
	if len(line)+1 <= maxLineBytes {
		return line
	}
	scalars := jsonx.NewObj()
	if boundedObj, ok := bounded.(*jsonx.Obj); ok {
		for _, entry := range boundedObj.Entries() {
			switch entry[1].(type) {
			case nil, bool, float64, string:
				scalars.Set(entry[0].(string), entry[1])
			}
		}
	}
	scalars.Set("truncated", true)
	short := jsonx.Stringify(scalars)
	if len(short)+1 <= maxLineBytes {
		return short
	}
	return jsonx.Stringify(jsonx.ObjFrom("type", rec.Type, "truncated", true))
}

// boundValue caps every string in a JSON value (object keys included).
// Returns a rebuilt value; the marker is added by the record-level wrapper.
func boundValue(v any, maxBytes int) any {
	switch t := v.(type) {
	case string:
		if len(t) <= maxBytes {
			return t
		}
		return truncateUTF8(t, maxBytes) + "…"
	case []any:
		out := make([]any, 0, len(t))
		for _, item := range t {
			out = append(out, boundValue(item, maxBytes))
		}
		return out
	case *jsonx.Obj:
		out := jsonx.NewObj()
		for _, entry := range t.Entries() {
			key := entry[0].(string)
			if len(key) > maxBytes {
				key = truncateUTF8(key, maxBytes) + "…"
			}
			out.Set(key, boundValue(entry[1], maxBytes))
		}
		return out
	default:
		return v
	}
}

// truncateUTF8 cuts a UTF-8 string to at most maxBytes on a rune boundary,
// dropping a split trailing character.
func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := s[:maxBytes]
	for len(cut) > 0 && !utf8.RuneStart(cut[len(cut)-1]) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// ---------------------------------------------------------------------------
// Post-hoc exports
// ---------------------------------------------------------------------------

// ExportJSONL writes header + records as canonical JSONL (the storage
// format).
func ExportJSONL(w io.Writer, header FileHeader, records []Record) error {
	if header.FormatVersion == 0 {
		header.FormatVersion = TrajectoryFormatVersion
	}
	if header.Kind == "" {
		header.Kind = "trajectory"
	}
	var sb strings.Builder
	sb.WriteString(jsonx.Stringify(headerJSONObject(header)))
	sb.WriteString("\n")
	for i := range records {
		sb.WriteString(records[i].String())
		sb.WriteString("\n")
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

// ExportJSON writes the whole trajectory as one JSON document.
func ExportJSON(w io.Writer, header FileHeader, records []Record) error {
	doc := jsonx.NewObj()
	doc.Set("header", headerJSONObject(header))
	arr := make([]any, 0, len(records))
	for i := range records {
		arr = append(arr, records[i].ToJSON())
	}
	doc.Set("records", arr)
	_, err := io.WriteString(w, jsonx.Stringify(doc))
	return err
}

func headerJSONObject(header FileHeader) any {
	value := jsonx.NewObj()
	value.Set("v", float64(header.FormatVersion))
	value.Set("kind", header.Kind)
	value.Set("id", header.ID)
	value.Set("createdAt", header.CreatedAt)
	if header.Model != "" {
		value.Set("model", header.Model)
	}
	if header.Provider != "" {
		value.Set("provider", header.Provider)
	}
	if header.ParentTrajectory != "" {
		value.Set("parentTrajectoryId", header.ParentTrajectory)
	}
	return value
}

// ExportMarkdown renders a human-readable ledger: session summary, then
// per-turn sections with steps, tool calls, errors, durations, and usage.
// Strings are capped at DefaultMaxStringBytes.
func ExportMarkdown(w io.Writer, header FileHeader, records []Record) error {
	var sb strings.Builder
	summary := UsageSummaryFrom(records)
	sb.WriteString("# Trajectory ")
	sb.WriteString(header.ID)
	sb.WriteString("\n\n")
	if header.Provider != "" || header.Model != "" {
		sb.WriteString("- model: ")
		sb.WriteString(header.Provider)
		sb.WriteString("/")
		sb.WriteString(header.Model)
		sb.WriteString("\n")
	}
	if header.ParentTrajectory != "" {
		sb.WriteString("- parent: " + header.ParentTrajectory + "\n")
	}
	sb.WriteString(fmt.Sprintf("- turns: %d · steps: %d", summary.Turns, summary.Steps))
	if summary.HasUsage {
		sb.WriteString(fmt.Sprintf(" · tokens: in %.0f / out %.0f / total %.0f",
			summary.Usage.Input, summary.Usage.Output, summary.Usage.TotalTokens))
		if summary.Usage.Cost.Total > 0 {
			sb.WriteString(fmt.Sprintf(" · cost: %.6f", summary.Usage.Cost.Total))
		}
	}
	sb.WriteString("\n\n")

	currentTurn, currentStep := 0, 0
	for i := range records {
		rec := &records[i]
		if rec.Turn != currentTurn {
			currentTurn = rec.Turn
			currentStep = 0
			if currentTurn > 0 {
				sb.WriteString(fmt.Sprintf("## Turn %d\n\n", currentTurn))
			}
		}
		switch rec.Type {
		case KindTurnStart:
			// header written on turn change
		case KindUserMessage:
			sb.WriteString(fmt.Sprintf("- **user** (%s): %s\n", stringField(rec.Data, "source"), messagePreview(rec, 400)))
		case KindSystemMessage:
			sb.WriteString("- **system** updated\n")
		case KindStepStart:
			currentStep = rec.Step
			sb.WriteString(fmt.Sprintf("### Step %d\n", currentStep))
			sb.WriteString(stepHeaderLine(rec))
		case KindRequestHeader:
			sb.WriteString(fmt.Sprintf("- **request**: %s · %d messages\n",
				modelLine(rec.Data), intField(rec.Data, "messageCount")))
		case KindAssistantMessage:
			sb.WriteString(fmt.Sprintf("- **assistant**: %s\n", messagePreview(rec, 400)))
			if thinking := thinkingPreview(rec, 200); thinking != "" {
				sb.WriteString(fmt.Sprintf("  - *thinking*: %s\n", thinking))
			}
			if obj, ok := rec.Data.(*jsonx.Obj); ok {
				if d := durationOf(obj); d > 0 {
					sb.WriteString(fmt.Sprintf("  - %.0f ms", d))
					if ttft := floatOf(obj, "ttftMs"); ttft != nil {
						sb.WriteString(fmt.Sprintf(" · ttft %.0f ms", *ttft))
					}
					sb.WriteString("\n")
				}
			}
			if rec.Data != nil {
				if obj, ok := rec.Data.(*jsonx.Obj); ok {
					if v, ok := obj.Get("error"); ok {
						sb.WriteString(fmt.Sprintf("  - **error**: %s\n", ExtractText(v)))
					}
				}
			}
		case KindAssistantAttempt:
			sb.WriteString("- **attempt failed** (retry material)\n")
		case KindToolCall:
			sb.WriteString(fmt.Sprintf("- **tool** `%s`(%s)\n", stringField(rec.Data, "name"), stringField(rec.Data, "arguments")))
		case KindToolResult:
			status := "ok"
			if boolField(rec.Data, "isError") {
				status = "error"
			}
			duration := ""
			if obj, ok := rec.Data.(*jsonx.Obj); ok {
				if d := durationOf(obj); d > 0 {
					duration = fmt.Sprintf(" · %.0f ms", d)
				}
			}
			sb.WriteString(fmt.Sprintf("  - result [%s]%s: %s\n", status, duration, toolResultPreview(rec, 400)))
		case KindLlmRetry:
			sb.WriteString(fmt.Sprintf("- **retry**: attempt %s in %sms — %s\n",
				stringField(rec.Data, "retry"), stringField(rec.Data, "delayMs"), retryMessage(rec.Data)))
		case KindStepEnd:
			usage := ""
			if u, ok := usageFromRecord(rec); ok {
				usage = fmt.Sprintf(" · tokens in %.0f / out %.0f", u.Input, u.Output)
			}
			sb.WriteString(fmt.Sprintf("- *step end*%s\n", usage))
		case KindTurnEnd:
			sb.WriteString(fmt.Sprintf("- *turn end*: %s\n\n", stringField(rec.Data, "reason")))
		case KindError:
			sb.WriteString(fmt.Sprintf("- **error**: %s\n", ExtractText(rec.Data)))
		}
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

func stepHeaderLine(rec *Record) string {
	if obj, ok := rec.Data.(*jsonx.Obj); ok {
		if modelValue, ok := obj.Get("model"); ok {
			if model, ok := modelValue.(*jsonx.Obj); ok {
				provider, _ := model.Get("provider")
				id, _ := model.Get("id")
				providerStr, _ := provider.(string)
				idStr, _ := id.(string)
				if providerStr != "" || idStr != "" {
					return fmt.Sprintf("  %s/%s\n", providerStr, idStr)
				}
			}
		}
	}
	return ""
}

func modelLine(data any) string {
	obj, ok := data.(*jsonx.Obj)
	if !ok {
		return "?"
	}
	modelValue, ok := obj.Get("model")
	if !ok {
		return "?"
	}
	model, ok := modelValue.(*jsonx.Obj)
	if !ok {
		return "?"
	}
	provider, _ := model.Get("provider")
	id, _ := model.Get("id")
	providerStr, _ := provider.(string)
	idStr, _ := id.(string)
	return providerStr + "/" + idStr
}

func stringField(data any, key string) string {
	obj, ok := data.(*jsonx.Obj)
	if !ok {
		return ""
	}
	v, ok := obj.Get(key)
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		runes := []rune(t)
		if len(runes) > 200 {
			return string(runes[:200]) + "…"
		}
		return t
	case float64:
		return jsonx.FormatNumber(t)
	default:
		return jsonx.Stringify(v)
	}
}

func intField(data any, key string) int {
	obj, ok := data.(*jsonx.Obj)
	if !ok {
		return 0
	}
	if v, ok := obj.Get(key); ok {
		if f, ok := jsonx.ToFloat(v); ok {
			return int(f)
		}
	}
	return 0
}

func boolField(data any, key string) bool {
	obj, ok := data.(*jsonx.Obj)
	if !ok {
		return false
	}
	if v, ok := obj.Get(key); ok {
		b, _ := v.(bool)
		return b
	}
	return false
}

func retryMessage(data any) string {
	obj, ok := data.(*jsonx.Obj)
	if !ok {
		return ""
	}
	failureValue, ok := obj.Get("failure")
	if !ok {
		return ""
	}
	failure, ok := failureValue.(*jsonx.Obj)
	if !ok {
		return ""
	}
	return stringField(failure, "message")
}

// thinkingPreview extracts the thinking block text of an assistant message
// record.
func thinkingPreview(rec *Record, limit int) string {
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
	arr, ok := contentValue.([]any)
	if !ok {
		return ""
	}
	var text string
	for _, item := range arr {
		block, ok := item.(*jsonx.Obj)
		if !ok {
			continue
		}
		if typeValue, _ := block.Get("type"); typeValue == "thinking" {
			if t, ok := block.Get("thinking"); ok {
				if s, ok := t.(string); ok {
					text += s
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

// toolResultPreview extracts the text blocks of a tool result record.
func toolResultPreview(rec *Record, limit int) string {
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
