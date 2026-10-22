package telemetry

// noopTelemetrySpan is the single inert span doubling as the shared context,
// mirroring NOOP_TELEMETRY_CONTEXT.
type noopTelemetrySpan struct{}

// noopCounter and friends never inspect or retain metadata, measurements, or
// contexts.
type noopCounter struct{}

func (noopCounter) Add(value float64, attributes SpanAttributes) {}

type noopHistogram struct{}

func (noopHistogram) Record(value float64, attributes SpanAttributes, context TelemetryContext) {}

// noopMetrics is the shared instruments capability.
type noopMetrics struct{}

// Counter returns the shared no-op counter.
func (noopMetrics) Counter(name string, options *InstrumentOptions) TelemetryCounter {
	return noopCounter{}
}

// UpDownCounter returns the shared no-op up-down counter.
func (noopMetrics) UpDownCounter(name string, options *InstrumentOptions) TelemetryUpDownCounter {
	return noopCounter{}
}

// Histogram returns the shared no-op histogram.
func (noopMetrics) Histogram(name string, options *HistogramOptions) TelemetryHistogram {
	return noopHistogram{}
}

// NOOP_TELEMETRY_METRICS is the shared instruments capability that never
// inspects or retains metadata, measurements, or contexts.
var NOOP_TELEMETRY_METRICS TelemetryMetrics = noopMetrics{}

// noopPropagation is the shared propagation capability.
type noopPropagation struct{}

// Inject always reports an unknown context.
func (noopPropagation) Inject(context TelemetryContext) TelemetryCarrier { return nil }

// Extract always resolves to the root NOOP context.
func (noopPropagation) Extract(carrier TelemetryCarrier) TelemetryContext {
	return NOOP_TELEMETRY_CONTEXT
}

// NOOP_TELEMETRY_PROPAGATION is the shared propagation capability that
// neither inspects nor retains its inputs.
var NOOP_TELEMETRY_PROPAGATION TelemetryPropagation = noopPropagation{}

// NOOP_TELEMETRY_CONTEXT is the shared telemetry context used when an
// application does not provide one. It implements TelemetrySpan, like the
// frozen TS object, and carries both NOOP capabilities.
var NOOP_TELEMETRY_CONTEXT TelemetrySpan = noopTelemetrySpan{}

// Metrics returns the shared NOOP instruments.
func (noopTelemetrySpan) Metrics() TelemetryMetrics { return NOOP_TELEMETRY_METRICS }

// Propagation returns the shared NOOP propagation.
func (noopTelemetrySpan) Propagation() TelemetryPropagation { return NOOP_TELEMETRY_PROPAGATION }

// StartSpan admits the callback synchronously and returns its result. A
// callback panic propagates after the callback returns (mirroring the TS
// sync-throw -> rejected-promise conversion is unnecessary here because Go
// panics already bypass the normal return path).
func (noopTelemetrySpan) StartSpan(options SpanOptions, callback func(TelemetrySpan) (any, error)) (any, error) {
	return callback(NOOP_TELEMETRY_CONTEXT)
}

func (noopTelemetrySpan) AddLink(link SpanLink)                           {}
func (noopTelemetrySpan) AddEvent(name string, attributes SpanAttributes) {}
func (noopTelemetrySpan) SetAttributes(attributes SpanAttributes)         {}
func (noopTelemetrySpan) SetStatus(status SpanStatus)                     {}
