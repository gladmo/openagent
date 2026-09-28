// trajectory/stream.go: lossless compact capture of one model stream with
// per-chunk timing. Text, thinking (chain-of-thought), and tool-call deltas
// pack into runs `{type, time0, index, dt[], texts[]|args[]}` where member i
// arrived at time0+Σdt[0..i); every other event is a raw `{type:"chunk",
// time, event}` record. A run only groups consecutive deltas of the same
// kind and content block — any other event between two deltas closes the run
// — so replay expands the exact event sequence in arrival order.
package trajectory

import (
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// Stream record discriminants (the "type" field inside a record).
const (
	streamTextChunks     = "text-chunks"
	streamThinkingChunks = "thinking-chunks"
	streamToolCallChunks = "toolcall-chunks"
	streamRawChunk       = "chunk"
)

// runAccumulator is the mutable form of one packed delta run.
type runAccumulator struct {
	kind     string // streamTextChunks | streamThinkingChunks | streamToolCallChunks
	index    int    // content block index
	time0    float64
	dt       []float64
	texts    []string // text/thinking runs
	args     []string // tool-call runs
	id       string   // tool-call identity, stamped at toolcall_end
	name     string
	hasName  bool
	lastTime float64
}

// StreamAccumulator compacts one model attempt incrementally without
// retaining a second raw-chunk list. It is not safe for concurrent use; the
// recorder serializes access.
type StreamAccumulator struct {
	// entries alternate between *runAccumulator and raw jsonx values in
	// arrival order; the tail entry is the only run that can grow.
	entries []any
	// firstTime records when the first event was observed (time-to-first-
	// token when read against the attempt start time).
	firstTime float64
	hasFirst  bool
}

// Push observes one provider stream event at timeMs. The caller owns the
// clock.
func (a *StreamAccumulator) Push(timeMs float64, event ai.AssistantMessageEvent) {
	if !a.hasFirst {
		a.firstTime = timeMs
		a.hasFirst = true
	}
	switch e := event.(type) {
	case *ai.EventTextDelta:
		a.pushDelta(streamTextChunks, e.ContentIndex, timeMs, e.Delta)
	case *ai.EventThinkingDelta:
		a.pushDelta(streamThinkingChunks, e.ContentIndex, timeMs, e.Delta)
	case *ai.EventToolCallDelta:
		a.pushDelta(streamToolCallChunks, e.ContentIndex, timeMs, e.Delta)
	case *ai.EventToolCallEnd:
		// The tool-call identity becomes known only at end; stamp the open
		// run for this block index.
		if run := a.openRun(streamToolCallChunks, e.ContentIndex); run != nil {
			run.id = e.ToolCall.ID
			run.name = e.ToolCall.Name
			run.hasName = true
		}
		a.pushRaw(timeMs, event)
	default:
		a.pushRaw(timeMs, event)
	}
}

func (a *StreamAccumulator) pushDelta(kind string, index int, timeMs float64, delta string) {
	run := a.openRun(kind, index)
	if run == nil {
		run = &runAccumulator{kind: kind, index: index, time0: timeMs, lastTime: timeMs}
		a.entries = append(a.entries, run)
	} else {
		run.dt = append(run.dt, timeMs-run.lastTime)
		run.lastTime = timeMs
	}
	if kind == streamToolCallChunks {
		run.args = append(run.args, delta)
	} else {
		run.texts = append(run.texts, delta)
	}
}

// openRun returns the tail run when it is still growing: same kind, same
// content block, and nothing else observed since its last member.
func (a *StreamAccumulator) openRun(kind string, index int) *runAccumulator {
	if len(a.entries) == 0 {
		return nil
	}
	run, ok := a.entries[len(a.entries)-1].(*runAccumulator)
	if !ok || run.kind != kind || run.index != index {
		return nil
	}
	return run
}

func (a *StreamAccumulator) pushRaw(timeMs float64, event ai.AssistantMessageEvent) {
	a.entries = append(a.entries, rawChunkJSON(timeMs, event))
}

// Snapshot returns the compact records as JSON values in arrival order. The
// accumulator keeps working after a snapshot.
func (a *StreamAccumulator) Snapshot() []any {
	out := make([]any, 0, len(a.entries))
	for _, entry := range a.entries {
		if run, ok := entry.(*runAccumulator); ok {
			out = append(out, run.toJSON())
			continue
		}
		out = append(out, entry)
	}
	return out
}

// FirstTime reports when the first event was observed.
func (a *StreamAccumulator) FirstTime() (float64, bool) { return a.firstTime, a.hasFirst }

// Count reports how many events were observed.
func (a *StreamAccumulator) Count() int {
	n := 0
	for _, entry := range a.entries {
		switch e := entry.(type) {
		case *runAccumulator:
			n += len(e.texts) + len(e.args)
		default:
			n++
		}
	}
	return n
}

func (r *runAccumulator) toJSON() any {
	o := jsonx.NewObj()
	o.Set("type", r.kind)
	o.Set("time0", r.time0)
	o.Set("index", float64(r.index))
	dt := make([]any, 0, len(r.dt))
	for _, v := range r.dt {
		dt = append(dt, v)
	}
	o.Set("dt", dt)
	if r.kind == streamToolCallChunks {
		args := make([]any, 0, len(r.args))
		for _, v := range r.args {
			args = append(args, v)
		}
		o.Set("args", args)
		if r.id != "" {
			o.Set("id", r.id)
		}
		if r.hasName && r.name != "" {
			o.Set("name", r.name)
		}
	} else {
		texts := make([]any, 0, len(r.texts))
		for _, v := range r.texts {
			texts = append(texts, v)
		}
		o.Set("texts", texts)
	}
	return o
}

// rawChunkJSON renders a non-delta event as a raw chunk record: the event
// discriminant plus its content index; deltas carry the payloads.
func rawChunkJSON(timeMs float64, event ai.AssistantMessageEvent) any {
	o := jsonx.NewObj()
	o.Set("type", streamRawChunk)
	o.Set("time", timeMs)
	o.Set("event", event.EventType())
	switch e := event.(type) {
	case *ai.EventTextStart:
		o.Set("contentIndex", float64(e.ContentIndex))
	case *ai.EventTextEnd:
		o.Set("contentIndex", float64(e.ContentIndex))
	case *ai.EventThinkingStart:
		o.Set("contentIndex", float64(e.ContentIndex))
	case *ai.EventThinkingEnd:
		o.Set("contentIndex", float64(e.ContentIndex))
	case *ai.EventToolCallStart:
		o.Set("contentIndex", float64(e.ContentIndex))
	case *ai.EventToolCallEnd:
		o.Set("contentIndex", float64(e.ContentIndex))
	case *ai.EventDone:
		o.Set("reason", e.Reason)
	case *ai.EventError:
		o.Set("reason", e.Reason)
	}
	return o
}

// StreamDelta is one reconstructed delta from ExpandStreamRecords.
type StreamDelta struct {
	Kind  string // text-chunks | thinking-chunks | toolcall-chunks
	Index int
	Text  string
}

// ExpandStreamRecords reconstructs the delta sequence (kind, index, text)
// from compact records in arrival order. Non-delta events are skipped; the
// sequence is what re-streaming replay consumes.
func ExpandStreamRecords(records []any) []StreamDelta {
	var out []StreamDelta
	for _, record := range records {
		obj, ok := record.(*jsonx.Obj)
		if !ok {
			continue
		}
		typeValue, _ := obj.Get("type")
		kind, _ := typeValue.(string)
		if kind != streamTextChunks && kind != streamThinkingChunks && kind != streamToolCallChunks {
			continue
		}
		index := intFromObj(obj, "index")
		for _, text := range streamMembers(obj, kind) {
			out = append(out, StreamDelta{Kind: kind, Index: index, Text: text})
		}
	}
	return out
}

func intFromObj(obj *jsonx.Obj, key string) int {
	if v, ok := obj.Get(key); ok {
		if f, ok := jsonx.ToFloat(v); ok {
			return int(f)
		}
	}
	return 0
}

func streamMembers(obj *jsonx.Obj, kind string) []string {
	key := "texts"
	if kind == streamToolCallChunks {
		key = "args"
	}
	v, ok := obj.Get(key)
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
