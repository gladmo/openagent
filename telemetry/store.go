package telemetry

// Port of the pi-telemetry stored-span store contract (d9025a5f5): read
// settled traces without depending on a storage backend.

// StoredSpanEvent is a settled event snapshot; times are relative to span
// start.
type StoredSpanEvent struct {
	Name       string         `json:"name"`
	TimeMs     float64        `json:"timeMs"`
	Attributes SpanAttributes `json:"attributes"`
}

// StoredSpanLink is a settled link snapshot.
type StoredSpanLink struct {
	TraceID    string         `json:"traceId"`
	SpanID     string         `json:"spanId"`
	Attributes SpanAttributes `json:"attributes,omitempty"`
}

// StoredSpan is a settled span snapshot, independent of the recording
// adapter.
type StoredSpan struct {
	ID         string            `json:"id"`
	TraceID    string            `json:"traceId"`
	ParentID   string            `json:"parentId,omitempty"`
	Name       string            `json:"name"`
	StartedMs  int64             `json:"startedMs"`
	EndedMs    int64             `json:"endedMs"`
	Attributes SpanAttributes    `json:"attributes"`
	Events     []StoredSpanEvent `json:"events"`
	Links      []StoredSpanLink  `json:"links"`
	Status     SpanStatus        `json:"status"`
}

// StoredTrace groups settled spans by trace.
type StoredTrace struct {
	TraceID string       `json:"traceId"`
	Spans   []StoredSpan `json:"spans"`
}

// TraceQuery filters stored traces. Name, time bounds, and the cursor apply
// to the earliest stored span of each trace (span ID breaking timestamp
// ties); attribute equalities must all match one span, which may be anywhere
// in the trace.
type TraceQuery struct {
	Name       string         `json:"name,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"` // string|number|boolean values
	Errors     bool           `json:"errors,omitempty"`
	StartMin   int64          `json:"startMin,omitempty"`
	StartMax   int64          `json:"startMax,omitempty"`
	Limit      int            `json:"limit,omitempty"` // positive page size; default 20 (non-positive falls back)
	// Cursor is an opaque continuation from the previous page; retain the
	// same filters. Unparseable or malformed cursors are ignored (the first
	// page is returned) rather than rejected.
	Cursor string `json:"cursor,omitempty"`
}

// MetricQuery filters stored metrics by interval start time.
type MetricQuery struct {
	Name       string         `json:"name"`
	StartMin   int64          `json:"startMin,omitempty"`
	StartMax   int64          `json:"startMax,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// StoredMetric aggregates measurements for one interval and attribute set.
type StoredMetric struct {
	IntervalMs int64          `json:"intervalMs"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"` // "counter" | "upDownCounter" | "histogram"
	Unit       string         `json:"unit,omitempty"`
	Attributes SpanAttributes `json:"attributes"`
	Count      int            `json:"count"`
	Sum        float64        `json:"sum"`
	Min        float64        `json:"min"`
	Max        float64        `json:"max"`
}

// FindTracesResult is one page of stored traces; an empty cursor means the
// page is final.
type FindTracesResult struct {
	Traces []StoredTrace `json:"traces"`
	Cursor string        `json:"cursor,omitempty"`
}

// TelemetryStore reads settled traces without depending on a storage
// backend.
type TelemetryStore interface {
	FindTraces(query TraceQuery) FindTracesResult
	GetTrace(traceID string) (StoredTrace, bool)
	FindMetrics(query MetricQuery) []StoredMetric
}
