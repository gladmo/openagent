package telemetry

import "sync"

// RecordedTelemetryEvent mirrors the TS interface.
type RecordedTelemetryEvent struct {
	Name       string         `json:"name"`
	Attributes SpanAttributes `json:"attributes"`
}

// RecordedTelemetrySpan mirrors the TS interface. EndSequence is a pointer so
// that absent stays distinct from 0 (TS omits the key when undefined).
type RecordedTelemetrySpan struct {
	ID          int                      `json:"id"`
	ParentID    *int                     `json:"parentId"`
	Name        string                   `json:"name"`
	Attributes  SpanAttributes           `json:"attributes"`
	Events      []RecordedTelemetryEvent `json:"events"`
	Status      SpanStatus               `json:"status"`
	Settled     bool                     `json:"settled"`
	EndSequence *int                     `json:"endSequence,omitempty"`
}

type mutableRecordedTelemetrySpan struct {
	id             int
	parentID       *int
	name           string
	attributes     SpanAttributes
	events         []RecordedTelemetryEvent
	status         SpanStatus
	explicitStatus bool
	settled        bool
	endSequence    *int
}

type inMemoryTelemetryState struct {
	mu              sync.Mutex
	spans           []*mutableRecordedTelemetrySpan
	nextSpanID      int
	nextEndSequence int
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
	copyMap := SpanAttributes{}
	for name, value := range attributes {
		if value == nil {
			continue
		}
		copyMap[name] = copyAttributeValue(value)
	}
	return copyMap
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
	seq := s.nextEndSequence
	s.nextEndSequence++
	span.endSequence = &seq
}

func (s *inMemoryTelemetryState) createSpan(parent *mutableRecordedTelemetrySpan, options SpanOptions) *mutableRecordedTelemetrySpan {
	span := &mutableRecordedTelemetrySpan{
		id:         s.nextSpanID,
		parentID:   nil,
		name:       options.Name,
		attributes: copyAttributes(options.Attributes),
		events:     []RecordedTelemetryEvent{},
		status:     StatusOK(),
	}
	s.nextSpanID++
	if parent != nil {
		id := parent.id
		span.parentID = &id
	}
	return span
}

// inMemorySpan is the TelemetrySpan view over a recorded span.
type inMemorySpan struct {
	state *inMemoryTelemetryState
	span  *mutableRecordedTelemetrySpan
}

func (i *inMemorySpan) StartSpan(options SpanOptions, callback func(TelemetrySpan) (any, error)) (any, error) {
	return startInMemorySpan(i.state, i.span, options, callback)
}

func (i *inMemorySpan) AddEvent(name string, attributes SpanAttributes) {
	i.state.mu.Lock()
	defer i.state.mu.Unlock()
	if i.span.settled {
		return
	}
	i.span.events = append(i.span.events, RecordedTelemetryEvent{
		Name:       name,
		Attributes: copyAttributes(attributes),
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

func startInMemorySpan(
	state *inMemoryTelemetryState,
	parent *mutableRecordedTelemetrySpan,
	options SpanOptions,
	callback func(TelemetrySpan) (any, error),
) (result any, err error) {
	if parent != nil {
		state.mu.Lock()
		parentSettled := parent.settled
		state.mu.Unlock()
		if parentSettled {
			return NOOP_TELEMETRY_CONTEXT.StartSpan(options, callback)
		}
	}
	state.mu.Lock()
	recordedSpan := state.createSpan(parent, options)
	state.spans = append(state.spans, recordedSpan)
	state.mu.Unlock()

	span := &inMemorySpan{state: state, span: recordedSpan}

	panicked := true
	defer func() {
		if panicked {
			// Callback panicked: settle as failed and let the panic continue
			// (TS settles then converts the throw to a rejection).
			if r := recover(); r != nil {
				state.settleSpan(recordedSpan, true, r)
				panic(r)
			}
		}
	}()
	result, err = callback(span)
	panicked = false
	state.settleSpan(recordedSpan, err != nil, err)
	return result, err
}

// InMemoryTelemetryContext is the backend-neutral reference implementation
// that records spans in process memory.
type InMemoryTelemetryContext struct {
	state inMemoryTelemetryState
}

// NewInMemoryTelemetryContext creates a fresh recording scope.
func NewInMemoryTelemetryContext() *InMemoryTelemetryContext {
	ctx := &InMemoryTelemetryContext{}
	ctx.state.nextSpanID = 1
	ctx.state.nextEndSequence = 1
	return ctx
}

// StartSpan records a span; see package docs for the callback contract.
func (c *InMemoryTelemetryContext) StartSpan(options SpanOptions, callback func(TelemetrySpan) (any, error)) (any, error) {
	return startInMemorySpan(&c.state, nil, options, callback)
}

// GetSpans returns detached snapshots in span-start order.
func (c *InMemoryTelemetryContext) GetSpans() []RecordedTelemetrySpan {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	out := make([]RecordedTelemetrySpan, 0, len(c.state.spans))
	for _, span := range c.state.spans {
		snapshot := RecordedTelemetrySpan{
			ID:          span.id,
			ParentID:    span.parentID,
			Name:        span.name,
			Attributes:  copyAttributes(span.attributes),
			Events:      make([]RecordedTelemetryEvent, len(span.events)),
			Status:      copyStatus(span.status),
			Settled:     span.settled,
			EndSequence: span.endSequence,
		}
		for i, event := range span.events {
			snapshot.Events[i] = RecordedTelemetryEvent{
				Name:       event.Name,
				Attributes: copyAttributes(event.Attributes),
			}
		}
		out = append(out, snapshot)
	}
	return out
}
