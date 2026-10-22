package telemetry

import (
	"crypto/rand"
	"encoding/json"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Port of pi-telemetry memory.ts. Go mapping notes:
//   - The TS module-global WeakMap of context carriers becomes a per-instance
//     table guarded by the state mutex (no weak references in Go). Carriers
//     are therefore visible only to the adapter that recorded the span or
//     extracted the parent; foreign-span inject/link resolves as absent.
//   - performance.now() becomes time.Since over a monotonic start; Date.now()
//     becomes time.Now().UnixMilli.
//   - Span end timestamps truncate to whole milliseconds.

// RecordedTelemetryEvent is the settled event snapshot; times live on stored
// spans.
type RecordedTelemetryEvent struct {
	Name       string         `json:"name"`
	Attributes SpanAttributes `json:"attributes"`
}

// RecordedTelemetryLink snapshots a link's carrier and attributes.
type RecordedTelemetryLink struct {
	Context    TelemetryCarrier `json:"context"`
	Attributes SpanAttributes   `json:"attributes"`
}

// RecordedTelemetrySpan mirrors the TS interface. ParentContext is nil for
// roots and parents owned by another recorder; EndSequence is a pointer so
// that absent stays distinct from 0.
type RecordedTelemetrySpan struct {
	ID            int                      `json:"id"`
	ParentID      *int                     `json:"parentId"`
	Context       TelemetryCarrier         `json:"context"`
	ParentContext TelemetryCarrier         `json:"parentContext"`
	Links         []RecordedTelemetryLink  `json:"links"`
	Name          string                   `json:"name"`
	Attributes    SpanAttributes           `json:"attributes"`
	Events        []RecordedTelemetryEvent `json:"events"`
	Status        SpanStatus               `json:"status"`
	Settled       bool                     `json:"settled"`
	EndSequence   *int                     `json:"endSequence,omitempty"`
}

// RecordedTelemetryMeasurement is one recorded metric datapoint; Context is
// the exemplar span's carrier, nil when recorded without one.
type RecordedTelemetryMeasurement struct {
	Kind        string           `json:"kind"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Unit        string           `json:"unit,omitempty"`
	Boundaries  []float64        `json:"boundaries,omitempty"`
	Value       float64          `json:"value"`
	Attributes  SpanAttributes   `json:"attributes"`
	Context     TelemetryCarrier `json:"context"`
}

type mutableRecordedTelemetryEvent struct {
	timeMs     float64
	name       string
	attributes SpanAttributes
}

type mutableRecordedTelemetrySpan struct {
	startedMs      int64
	clockStart     time.Time
	endedMs        int64
	id             int
	parentID       *int
	context        TelemetryCarrier
	parentContext  TelemetryCarrier
	links          []RecordedTelemetryLink
	name           string
	attributes     SpanAttributes
	events         []mutableRecordedTelemetryEvent
	status         SpanStatus
	explicitStatus bool
	settled        bool
	endSequence    *int
}

// inMemoryParent couples a carrier with its optional local span id and
// recorded span (absent for extracted parents).
type inMemoryParent struct {
	context TelemetryCarrier
	localID *int
	span    *mutableRecordedTelemetrySpan
}

type timestampedMeasurement struct {
	intervalMs  int64
	measurement RecordedTelemetryMeasurement
}

type inMemoryTelemetryState struct {
	mu              sync.Mutex
	metrics         TelemetryMetrics
	propagation     TelemetryPropagation
	scope           string
	spans           []*mutableRecordedTelemetrySpan
	measurements    []timestampedMeasurement
	carriers        map[TelemetryContext]TelemetryCarrier
	nextSpanID      int
	nextEndSequence int
}

// Clock seams: tests substitute deterministic clocks (the TS suite mocks
// Date.now / performance.now from outside).
var (
	wallNow        = func() time.Time { return time.Now() }
	monotonicSince = func(t time.Time) time.Duration { return time.Since(t) }
)

func carrierID(carrier TelemetryCarrier) string {
	return carrier["pi-memory-scope"] + ":" + carrier["pi-memory-span"]
}

func randomScopeID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never fails on supported platforms; fall back to a
		// time-derived scope rather than panicking in telemetry setup.
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	const hex = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hex[v>>4], hex[v&0x0f])
	}
	return string(out)
}

func copyAttributeValue(value AttributeValue) AttributeValue {
	switch t := value.(type) {
	case []string:
		out := make([]string, len(t))
		copy(out, t)
		return out
	case []float64:
		out := make([]float64, len(t))
		copy(out, t)
		return out
	case []bool:
		out := make([]bool, len(t))
		copy(out, t)
		return out
	case []any:
		out := make([]any, len(t))
		copy(out, t)
		return out
	default:
		return t
	}
}

func copyAttributes(attributes SpanAttributes) SpanAttributes {
	if attributes == nil {
		return SpanAttributes{}
	}
	copyMap := SpanAttributes{}
	for name, value := range attributes {
		if value == nil {
			continue
		}
		copyMap[name] = copyAttributeValue(value)
	}
	return copyMap
}

func copyCarrier(carrier TelemetryCarrier) TelemetryCarrier {
	if carrier == nil {
		return nil
	}
	out := TelemetryCarrier{}
	for k, v := range carrier {
		out[k] = v
	}
	return out
}

func mergeAttributes(current, attributes SpanAttributes) SpanAttributes {
	merged := copyAttributes(current)
	for name, value := range attributes {
		if value == nil {
			continue
		}
		merged[name] = copyAttributeValue(value)
	}
	return merged
}

func copyStatus(status SpanStatus) SpanStatus {
	out := SpanStatus{Status: status.Status}
	if status.Error != nil {
		out.Error = &SpanError{Name: status.Error.Name, Message: status.Error.Message}
	}
	return out
}

// automaticErrorStatus derives the span status from a failed callback. JS
// inspects instanceof Error; every Go error carries a message, so the status
// always includes details (name = error type, message = Error()).
func automaticErrorStatus(err any) SpanStatus {
	if err, ok := err.(error); ok {
		name := "Error"
		if n, ok := err.(interface{ Name() string }); ok {
			name = n.Name()
		}
		return StatusError(name, err.Error())
	}
	return StatusErrorWithoutDetails()
}

// copyLinkLocked snapshots a link when the referenced context is known to
// this adapter; callers must hold the state mutex.
func (s *inMemoryTelemetryState) copyLinkLocked(link SpanLink) (RecordedTelemetryLink, bool) {
	context, ok := s.carriers[link.Context]
	if !ok {
		return RecordedTelemetryLink{}, false
	}
	return RecordedTelemetryLink{Context: copyCarrier(context), Attributes: copyAttributes(link.Attributes)}, true
}

func (s *inMemoryTelemetryState) createSpan(parent *inMemoryParent, options SpanOptions) *mutableRecordedTelemetrySpan {
	id := s.nextSpanID
	s.nextSpanID++
	trace := s.scope + ":" + strconv.Itoa(id)
	var parentContext TelemetryCarrier
	var parentID *int
	if parent != nil {
		parentContext = copyCarrier(parent.context)
		if parent.traceID() != "" {
			trace = parent.traceID()
		}
		if parent.localID != nil {
			local := *parent.localID
			parentID = &local
		}
	}
	span := &mutableRecordedTelemetrySpan{
		startedMs:     wallNow().UnixMilli(),
		clockStart:    wallNow(),
		id:            id,
		parentID:      parentID,
		context:       TelemetryCarrier{"pi-memory-scope": s.scope, "pi-memory-span": strconv.Itoa(id), "pi-memory-trace": trace},
		parentContext: parentContext,
		name:          options.Name,
		attributes:    copyAttributes(options.Attributes),
		events:        []mutableRecordedTelemetryEvent{},
		status:        StatusOK(),
	}
	for _, link := range options.Links {
		if copy, ok := s.copyLinkLocked(link); ok {
			span.links = append(span.links, copy)
		}
	}
	return span
}

// traceID returns the carrier's trace identifier ("pi-memory-trace").
func (p *inMemoryParent) traceID() string {
	if p == nil || p.context == nil {
		return ""
	}
	return p.context["pi-memory-trace"]
}

func (s *inMemoryTelemetryState) settleSpan(span *mutableRecordedTelemetrySpan, failed bool, err any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if span.settled {
		return
	}
	if failed && !span.explicitStatus {
		span.status = automaticErrorStatus(err)
	}
	span.settled = true
	span.endedMs = span.startedMs + monotonicSince(span.clockStart).Milliseconds()
	seq := s.nextEndSequence
	s.nextEndSequence++
	span.endSequence = &seq
}

// inMemorySpan is the TelemetrySpan view over a recorded span.
type inMemorySpan struct {
	state *inMemoryTelemetryState
	span  *mutableRecordedTelemetrySpan
}

func (i *inMemorySpan) StartSpan(options SpanOptions, callback func(TelemetrySpan) (any, error)) (any, error) {
	return i.state.startInMemorySpan(&inMemoryParent{context: i.span.context, localID: &i.span.id, span: i.span}, options, callback)
}

func (i *inMemorySpan) Metrics() TelemetryMetrics { return i.state.metrics }

func (i *inMemorySpan) Propagation() TelemetryPropagation { return i.state.propagation }

func (i *inMemorySpan) AddLink(link SpanLink) {
	i.state.mu.Lock()
	defer i.state.mu.Unlock()
	if i.span.settled {
		return
	}
	if copy, ok := i.state.copyLinkLocked(link); ok {
		i.span.links = append(i.span.links, copy)
	}
}

func (i *inMemorySpan) AddEvent(name string, attributes SpanAttributes) {
	i.state.mu.Lock()
	defer i.state.mu.Unlock()
	if i.span.settled {
		return
	}
	i.span.events = append(i.span.events, mutableRecordedTelemetryEvent{
		timeMs:     float64(monotonicSince(i.span.clockStart)) / float64(time.Millisecond),
		name:       name,
		attributes: copyAttributes(attributes),
	})
}

func (i *inMemorySpan) SetAttributes(attributes SpanAttributes) {
	i.state.mu.Lock()
	defer i.state.mu.Unlock()
	if i.span.settled {
		return
	}
	i.span.attributes = mergeAttributes(i.span.attributes, attributes)
}

func (i *inMemorySpan) SetStatus(status SpanStatus) {
	i.state.mu.Lock()
	defer i.state.mu.Unlock()
	if i.span.settled {
		return
	}
	i.span.status = copyStatus(status)
	i.span.explicitStatus = true
}

// inertSpan keeps adapter capabilities alive under a settled parent without
// recording. It is never registered in the carrier table.
type inertSpan struct {
	state *inMemoryTelemetryState
}

func (i *inertSpan) StartSpan(options SpanOptions, callback func(TelemetrySpan) (any, error)) (any, error) {
	return NOOP_TELEMETRY_CONTEXT.StartSpan(options, func(noop TelemetrySpan) (any, error) {
		return callback(&inertSpan{state: i.state})
	})
}

func (i *inertSpan) Metrics() TelemetryMetrics                       { return i.state.metrics }
func (i *inertSpan) Propagation() TelemetryPropagation               { return i.state.propagation }
func (i *inertSpan) AddLink(link SpanLink)                           {}
func (i *inertSpan) AddEvent(name string, attributes SpanAttributes) {}
func (i *inertSpan) SetAttributes(attributes SpanAttributes)         {}
func (i *inertSpan) SetStatus(status SpanStatus)                     {}

func (s *inMemoryTelemetryState) startInertSpan(options SpanOptions, callback func(TelemetrySpan) (any, error)) (any, error) {
	return NOOP_TELEMETRY_CONTEXT.StartSpan(options, func(TelemetrySpan) (any, error) {
		return callback(&inertSpan{state: s})
	})
}

func (s *inMemoryTelemetryState) startInMemorySpan(
	parent *inMemoryParent,
	options SpanOptions,
	callback func(TelemetrySpan) (any, error),
) (result any, err error) {
	if parent != nil && parent.span != nil {
		s.mu.Lock()
		parentSettled := parent.span.settled
		s.mu.Unlock()
		if parentSettled {
			return s.startInertSpan(options, callback)
		}
	}
	s.mu.Lock()
	recordedSpan := s.createSpan(parent, options)
	s.spans = append(s.spans, recordedSpan)
	s.mu.Unlock()

	span := &inMemorySpan{state: s, span: recordedSpan}
	s.mu.Lock()
	s.carriers[span] = copyCarrier(recordedSpan.context)
	s.mu.Unlock()

	panicked := true
	defer func() {
		if panicked {
			// Callback panicked: settle as failed and let the panic continue
			// (TS settles then converts the throw to a rejection).
			if r := recover(); r != nil {
				s.settleSpan(recordedSpan, true, r)
				panic(r)
			}
		}
	}()
	result, err = callback(span)
	panicked = false
	s.settleSpan(recordedSpan, err != nil, err)
	return result, err
}

// recordMeasurement appends a datapoint to the measurement log.
func (s *inMemoryTelemetryState) recordMeasurement(
	kind, name string,
	description, unit string,
	boundaries []float64,
	value float64,
	attributes SpanAttributes,
	context TelemetryContext,
) {
	var carrier TelemetryCarrier
	if context != nil {
		s.mu.Lock()
		if c, ok := s.carriers[context]; ok {
			carrier = copyCarrier(c)
		}
		s.mu.Unlock()
	}
	interval := (wallNow().UnixMilli() / 10_000) * 10_000
	s.mu.Lock()
	defer s.mu.Unlock()
	s.measurements = append(s.measurements, timestampedMeasurement{
		intervalMs: interval,
		measurement: RecordedTelemetryMeasurement{
			Kind:        kind,
			Name:        name,
			Description: description,
			Unit:        unit,
			Boundaries:  boundaries,
			Value:       value,
			Attributes:  copyAttributes(attributes),
			Context:     carrier,
		},
	})
}

// InMemoryTelemetryContext is the backend-neutral reference implementation
// that records spans in process memory. Create a fresh instance to isolate
// tests or independent recording scopes.
type InMemoryTelemetryContext struct {
	state inMemoryTelemetryState
}

// NewInMemoryTelemetryContext creates a fresh recording scope.
func NewInMemoryTelemetryContext() *InMemoryTelemetryContext {
	ctx := &InMemoryTelemetryContext{}
	ctx.state.metrics = ctx
	ctx.state.propagation = ctx
	ctx.state.scope = randomScopeID()
	ctx.state.carriers = map[TelemetryContext]TelemetryCarrier{}
	ctx.state.nextSpanID = 1
	ctx.state.nextEndSequence = 1
	return ctx
}

// StartSpan records a span; see package docs for the callback contract.
func (c *InMemoryTelemetryContext) StartSpan(options SpanOptions, callback func(TelemetrySpan) (any, error)) (any, error) {
	return c.state.startInMemorySpan(nil, options, callback)
}

// Metrics returns the context's own instruments capability.
func (c *InMemoryTelemetryContext) Metrics() TelemetryMetrics { return c }

// Propagation returns the context's own propagation capability.
func (c *InMemoryTelemetryContext) Propagation() TelemetryPropagation { return c }

// Inject returns a detached carrier for a known span or extracted parent,
// including settled spans; nil when the context is unknown.
func (c *InMemoryTelemetryContext) Inject(context TelemetryContext) TelemetryCarrier {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	if carrier, ok := c.state.carriers[context]; ok {
		return copyCarrier(carrier)
	}
	return nil
}

// Extract resolves a carrier to a parent context. Missing or non-string
// carrier identities fall back to this recorder's root context.
func (c *InMemoryTelemetryContext) Extract(carrier TelemetryCarrier) TelemetryContext {
	scope, okScope := carrier["pi-memory-scope"]
	span, okSpan := carrier["pi-memory-span"]
	traceID, okTrace := carrier["pi-memory-trace"]
	if !okScope || !okSpan || !okTrace {
		return c
	}
	context := TelemetryCarrier{"pi-memory-scope": scope, "pi-memory-span": span, "pi-memory-trace": traceID}
	var localID *int
	if scope == c.state.scope {
		if parsed, err := strconv.Atoi(span); err == nil {
			localID = &parsed
		}
	}
	parent := &extractedParent{state: &c.state, context: context, localID: localID}
	c.state.mu.Lock()
	c.state.carriers[parent] = copyCarrier(context)
	c.state.mu.Unlock()
	return parent
}

// extractedParent is a TelemetryContext that starts children under a carrier
// resolved from propagation data.
type extractedParent struct {
	state   *inMemoryTelemetryState
	context TelemetryCarrier
	localID *int
}

func (p *extractedParent) StartSpan(options SpanOptions, callback func(TelemetrySpan) (any, error)) (any, error) {
	return p.state.startInMemorySpan(&inMemoryParent{context: p.context, localID: p.localID}, options, callback)
}

func (p *extractedParent) Metrics() TelemetryMetrics         { return p.state.metrics }
func (p *extractedParent) Propagation() TelemetryPropagation { return p.state.propagation }

// Counter records counter increments; instrument metadata is captured at
// creation.
func (c *InMemoryTelemetryContext) Counter(name string, options *InstrumentOptions) TelemetryCounter {
	description, unit := instrumentMetadata(options)
	return measurementFunc(func(value float64, attributes SpanAttributes) {
		c.state.recordMeasurement(kindCounter, name, description, unit, nil, value, attributes, nil)
	})
}

// UpDownCounter records up-down counter increments; instrument metadata is
// captured at creation.
func (c *InMemoryTelemetryContext) UpDownCounter(name string, options *InstrumentOptions) TelemetryUpDownCounter {
	description, unit := instrumentMetadata(options)
	return measurementFunc(func(value float64, attributes SpanAttributes) {
		c.state.recordMeasurement(kindUpDownCounter, name, description, unit, nil, value, attributes, nil)
	})
}

// Histogram records histogram measurements with optional exemplar context;
// instrument metadata and boundaries are captured at creation.
func (c *InMemoryTelemetryContext) Histogram(name string, options *HistogramOptions) TelemetryHistogram {
	description, unit := instrumentMetadata(options.instrument())
	boundaries := copyBoundaries(options)
	return exemplarFunc(func(value float64, attributes SpanAttributes, context TelemetryContext) {
		c.state.recordMeasurement(kindHistogram, name, description, unit, boundaries, value, attributes, context)
	})
}

const (
	kindCounter       = "counter"
	kindUpDownCounter = "upDownCounter"
	kindHistogram     = "histogram"
)

func instrumentMetadata(options *InstrumentOptions) (description, unit string) {
	if options == nil {
		return "", ""
	}
	return options.Description, options.Unit
}

func (o *HistogramOptions) instrument() *InstrumentOptions {
	if o == nil {
		return nil
	}
	return &InstrumentOptions{Description: o.Description, Unit: o.Unit}
}

func copyBoundaries(options *HistogramOptions) []float64 {
	if options == nil || options.Boundaries == nil {
		return nil
	}
	out := make([]float64, len(options.Boundaries))
	copy(out, options.Boundaries)
	return out
}

type measurementFunc func(value float64, attributes SpanAttributes)

func (f measurementFunc) Add(value float64, attributes SpanAttributes) { f(value, attributes) }

type exemplarFunc func(value float64, attributes SpanAttributes, context TelemetryContext)

func (f exemplarFunc) Record(value float64, attributes SpanAttributes, context TelemetryContext) {
	f(value, attributes, context)
}

// storedSpans returns settled spans as StoredSpan values sorted by start
// time, then span ID.
func (c *InMemoryTelemetryContext) storedSpans() []StoredSpan {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	out := make([]StoredSpan, 0, len(c.state.spans))
	for _, span := range c.state.spans {
		if !span.settled {
			continue
		}
		stored := StoredSpan{
			ID:         carrierID(span.context),
			TraceID:    span.context["pi-memory-trace"],
			Name:       span.name,
			StartedMs:  span.startedMs,
			EndedMs:    span.endedMs,
			Attributes: copyAttributes(span.attributes),
			Events:     make([]StoredSpanEvent, 0, len(span.events)),
			Links:      make([]StoredSpanLink, 0, len(span.links)),
			Status:     copyStatus(span.status),
		}
		if span.parentContext != nil {
			stored.ParentID = carrierID(span.parentContext)
		}
		for _, event := range span.events {
			stored.Events = append(stored.Events, StoredSpanEvent{
				Name:       event.name,
				TimeMs:     event.timeMs,
				Attributes: copyAttributes(event.attributes),
			})
		}
		for _, link := range span.links {
			stored.Links = append(stored.Links, StoredSpanLink{
				TraceID:    link.Context["pi-memory-trace"],
				SpanID:     carrierID(link.Context),
				Attributes: copyAttributes(link.Attributes),
			})
		}
		out = append(out, stored)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartedMs != out[j].StartedMs {
			return out[i].StartedMs < out[j].StartedMs
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// GetTrace returns the stored trace for traceID.
func (c *InMemoryTelemetryContext) GetTrace(traceID string) (StoredTrace, bool) {
	spans := []StoredSpan{}
	for _, span := range c.storedSpans() {
		if span.TraceID == traceID {
			spans = append(spans, span)
		}
	}
	if len(spans) == 0 {
		return StoredTrace{}, false
	}
	return StoredTrace{TraceID: traceID, Spans: spans}, true
}

// FindTraces returns one page of stored traces, newest first.
func (c *InMemoryTelemetryContext) FindTraces(query TraceQuery) FindTracesResult {
	limit := query.Limit
	if limit == 0 {
		limit = 20
	}
	var afterMs float64
	var afterTraceID string
	haveCursor := false
	if query.Cursor != "" {
		var decoded []any
		if err := json.Unmarshal([]byte(query.Cursor), &decoded); err == nil && len(decoded) == 2 {
			if ms, ok := decoded[0].(float64); ok {
				afterMs = ms
			}
			if id, ok := decoded[1].(string); ok {
				afterTraceID = id
			}
			haveCursor = true
		}
	}
	type groupedTrace struct {
		traceID string
		spans   []StoredSpan
	}
	grouped := map[string]*groupedTrace{}
	order := []string{}
	for _, span := range c.storedSpans() {
		trace, ok := grouped[span.TraceID]
		if !ok {
			trace = &groupedTrace{traceID: span.TraceID}
			grouped[span.TraceID] = trace
			order = append(order, span.TraceID)
		}
		trace.spans = append(trace.spans, span)
	}
	matches := []*groupedTrace{}
	for _, traceID := range order {
		trace := grouped[traceID]
		first := trace.spans[0]
		matchesQuery := true
		if query.Name != "" && first.Name != query.Name {
			matchesQuery = false
		}
		if matchesQuery && (query.StartMin != 0 || query.StartMax != 0) {
			if query.StartMin != 0 && first.StartedMs < query.StartMin {
				matchesQuery = false
			}
			if matchesQuery && query.StartMax != 0 && first.StartedMs > query.StartMax {
				matchesQuery = false
			}
		}
		if matchesQuery && haveCursor && !(float64(first.StartedMs) < afterMs || (float64(first.StartedMs) == afterMs && trace.traceID > afterTraceID)) {
			matchesQuery = false
		}
		if matchesQuery && query.Errors {
			hasError := false
			for _, span := range trace.spans {
				if span.Status.Status == "error" {
					hasError = true
					break
				}
			}
			matchesQuery = hasError
		}
		if matchesQuery && len(query.Attributes) > 0 {
			matched := false
			for _, span := range trace.spans {
				allEqual := true
				for key, value := range query.Attributes {
					if span.Attributes[key] != value {
						allEqual = false
						break
					}
				}
				if allEqual {
					matched = true
					break
				}
			}
			matchesQuery = matched
		}
		if matchesQuery {
			matches = append(matches, trace)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i].spans[0].StartedMs, matches[j].spans[0].StartedMs
		if a != b {
			return a > b
		}
		return matches[i].traceID < matches[j].traceID
	})
	result := FindTracesResult{Traces: []StoredTrace{}}
	if len(matches) > limit {
		matches = matches[:limit]
		last := matches[len(matches)-1]
		cursor, _ := json.Marshal([]any{last.spans[0].StartedMs, last.traceID})
		result.Cursor = string(cursor)
	}
	for _, trace := range matches {
		result.Traces = append(result.Traces, StoredTrace{TraceID: trace.traceID, Spans: trace.spans})
	}
	return result
}

// FindMetrics reads raw measurements as count-one rows, without changing
// GetMetrics snapshots.
func (c *InMemoryTelemetryContext) FindMetrics(query MetricQuery) []StoredMetric {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	out := []StoredMetric{}
	for _, entry := range c.state.measurements {
		measurement := entry.measurement
		if measurement.Name != query.Name {
			continue
		}
		if query.StartMin != 0 && entry.intervalMs < query.StartMin {
			continue
		}
		if query.StartMax != 0 && entry.intervalMs > query.StartMax {
			continue
		}
		matched := true
		for key, value := range query.Attributes {
			if measurement.Attributes[key] != value {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		out = append(out, StoredMetric{
			IntervalMs: entry.intervalMs,
			Name:       measurement.Name,
			Kind:       measurement.Kind,
			Unit:       measurement.Unit,
			Attributes: copyAttributes(measurement.Attributes),
			Count:      1,
			Sum:        measurement.Value,
			Min:        measurement.Value,
			Max:        measurement.Value,
		})
	}
	return out
}

// GetMetrics returns detached measurements in recording order, without
// aggregation.
func (c *InMemoryTelemetryContext) GetMetrics() []RecordedTelemetryMeasurement {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	out := make([]RecordedTelemetryMeasurement, 0, len(c.state.measurements))
	for _, entry := range c.state.measurements {
		measurement := entry.measurement
		snapshot := RecordedTelemetryMeasurement{
			Kind:        measurement.Kind,
			Name:        measurement.Name,
			Description: measurement.Description,
			Unit:        measurement.Unit,
			Boundaries:  measurement.Boundaries,
			Value:       measurement.Value,
			Attributes:  copyAttributes(measurement.Attributes),
			Context:     copyCarrier(measurement.Context),
		}
		out = append(out, snapshot)
	}
	return out
}

// GetSpans returns detached snapshots in span-start order.
func (c *InMemoryTelemetryContext) GetSpans() []RecordedTelemetrySpan {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	out := make([]RecordedTelemetrySpan, 0, len(c.state.spans))
	for _, span := range c.state.spans {
		snapshot := RecordedTelemetrySpan{
			ID:            span.id,
			ParentID:      span.parentID,
			Context:       copyCarrier(span.context),
			ParentContext: copyCarrier(span.parentContext),
			Links:         make([]RecordedTelemetryLink, len(span.links)),
			Name:          span.name,
			Attributes:    copyAttributes(span.attributes),
			Events:        make([]RecordedTelemetryEvent, len(span.events)),
			Status:        copyStatus(span.status),
			Settled:       span.settled,
			EndSequence:   span.endSequence,
		}
		for i, link := range span.links {
			snapshot.Links[i] = RecordedTelemetryLink{
				Context:    copyCarrier(link.Context),
				Attributes: copyAttributes(link.Attributes),
			}
		}
		for i, event := range span.events {
			snapshot.Events[i] = RecordedTelemetryEvent{
				Name:       event.name,
				Attributes: copyAttributes(event.attributes),
			}
		}
		out = append(out, snapshot)
	}
	return out
}
