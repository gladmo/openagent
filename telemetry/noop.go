package telemetry

// noopTelemetrySpan is the single inert span doubling as the shared context,
// mirroring NOOP_TELEMETRY_CONTEXT.
type noopTelemetrySpan struct{}

// NOOP_TELEMETRY_CONTEXT is the shared telemetry context used when an
// application does not provide one. It implements both TelemetryContext and
// TelemetrySpan, like the frozen TS object.
var NOOP_TELEMETRY_CONTEXT TelemetrySpan = noopTelemetrySpan{}

// StartSpan admits the callback synchronously and returns its result. A
// callback panic propagates after the callback returns (mirroring the TS
// sync-throw -> rejected-promise conversion is unnecessary here because Go
// panics already bypass the normal return path).
func (noopTelemetrySpan) StartSpan(options SpanOptions, callback func(TelemetrySpan) (any, error)) (any, error) {
	return callback(NOOP_TELEMETRY_CONTEXT)
}

func (noopTelemetrySpan) AddEvent(name string, attributes SpanAttributes) {}
func (noopTelemetrySpan) SetAttributes(attributes SpanAttributes)         {}
func (noopTelemetrySpan) SetStatus(status SpanStatus)                     {}
