// Package telemetry is a 1:1 port of @gladmo/pi-telemetry: the
// vendor-neutral telemetry contract, typed schema definitions, the shared
// no-op context, and the in-memory reference recorder.
//
// Go mapping notes:
//   - `startSpan<T>(options, callback): Promise<T>` becomes
//     StartSpan[T](ctx, options, fn) with fn func(TelemetrySpan) (T, error).
//     A TS callback that returns a Promise keeps the span open until it
//     settles; in Go the callback blocks until its work is done, which
//     preserves the settlement ordering guarantees.
//   - Rejection-value identity: the exact error returned by the callback is
//     returned to the caller (TS `rejects.toBe(value)`).
//   - A callback panic settles the span as failed and re-panics (the TS code
//     converts sync throws to rejections; Go propagates the panic).
//   - TS type-level machinery (Infer*/Exact*/SchemaTelemetrySpan/
//     TypedSpanStarter generic overloads) has no runtime artifact and is not
//     ported; CreateTypedSpanStarter keeps the runtime behavior.
package telemetry

// AttributeValue is string | number | boolean | string[] | number[] | bool[].
// Numbers are float64 (JS doubles).
type AttributeValue = any

// SpanAttributes maps names to attribute values. A nil/absent entry mirrors a
// TS `undefined` value, which recorders skip.
type SpanAttributes = map[string]AttributeValue

// SpanError carries the name/message pair of an errored span status.
type SpanError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// SpanStatus is {status:"ok"} | {status:"error"; error?{name,message}}.
type SpanStatus struct {
	Status string     `json:"status"`
	Error  *SpanError `json:"error,omitempty"`
}

// Equal reports value equality with another status.
func (s SpanStatus) Equal(other SpanStatus) bool {
	if s.Status != other.Status {
		return false
	}
	if (s.Error == nil) != (other.Error == nil) {
		return false
	}
	if s.Error != nil {
		return *s.Error == *other.Error
	}
	return true
}

// StatusOK returns the ok status.
func StatusOK() SpanStatus { return SpanStatus{Status: "ok"} }

// StatusError returns an error status with details.
func StatusError(name, message string) SpanStatus {
	return SpanStatus{Status: "error", Error: &SpanError{Name: name, Message: message}}
}

// StatusErrorWithoutDetails returns an error status without details.
func StatusErrorWithoutDetails() SpanStatus { return SpanStatus{Status: "error"} }

// SpanOptions mirrors the TS interface.
type SpanOptions struct {
	Name       string         `json:"name"`
	Attributes SpanAttributes `json:"attributes,omitempty"`
}

// TelemetryContext starts spans whose lifetime is the callback lifetime.
type TelemetryContext interface {
	StartSpan(options SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error)
}

// TelemetrySpan is a context that also records events, attributes and status.
type TelemetrySpan interface {
	TelemetryContext
	AddEvent(name string, attributes SpanAttributes)
	SetAttributes(attributes SpanAttributes)
	SetStatus(status SpanStatus)
}

// StartSpan is the generic helper over TelemetryContext.StartSpan, standing
// in for the TS generic method.
func StartSpan[T any](ctx TelemetryContext, options SpanOptions, fn func(TelemetrySpan) (T, error)) (T, error) {
	var zero T
	result, err := ctx.StartSpan(options, func(span TelemetrySpan) (any, error) {
		v, err := fn(span)
		return v, err
	})
	if err != nil {
		return zero, err
	}
	if result == nil {
		return zero, nil
	}
	v, ok := result.(T)
	if !ok {
		return zero, ErrResultType
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Schema definitions (runtime-serializable data only)
// ---------------------------------------------------------------------------

// TelemetryAttributeType enumerates attribute types.
type TelemetryAttributeType = string

const (
	AttrString   TelemetryAttributeType = "string"
	AttrNumber   TelemetryAttributeType = "number"
	AttrBoolean  TelemetryAttributeType = "boolean"
	AttrStringA  TelemetryAttributeType = "string[]"
	AttrNumberA  TelemetryAttributeType = "number[]"
	AttrBooleanA TelemetryAttributeType = "boolean[]"
)

// TelemetryAttributeMetadata mirrors the TS interface.
type TelemetryAttributeMetadata struct {
	Description string `json:"description"`
	Sensitive   bool   `json:"sensitive,omitempty"`
	Cardinality string `json:"cardinality,omitempty"` // "low" | "high"
}

// TelemetryAttributeDefinition mirrors the TS discriminated union as one
// struct: scalar types use Values/Examples, array types use
// ElementValues/Examples.
type TelemetryAttributeDefinition struct {
	Description   string `json:"description"`
	Sensitive     bool   `json:"sensitive,omitempty"`
	Cardinality   string `json:"cardinality,omitempty"`
	Type          string `json:"type"`
	Values        any    `json:"values,omitempty"`
	Examples      any    `json:"examples,omitempty"`
	ElementValues any    `json:"elementValues,omitempty"`
}

// TelemetryStartAttributeDefinition adds the required flag.
type TelemetryStartAttributeDefinition struct {
	TelemetryAttributeDefinition
	Required bool `json:"required"`
}

// TelemetryEventAttributeDefinition adds the required flag.
type TelemetryEventAttributeDefinition struct {
	TelemetryAttributeDefinition
	Required bool `json:"required"`
}

// TelemetryEventDefinition mirrors the TS interface.
type TelemetryEventDefinition struct {
	Description string                                       `json:"description"`
	Attributes  map[string]TelemetryEventAttributeDefinition `json:"attributes"`
}

// TelemetryParentDefinition is kind "any" | "root_or_external" | "spans".
type TelemetryParentDefinition struct {
	Kind  string   `json:"kind"`
	Spans []string `json:"spans,omitempty"`
}

// TelemetrySpanDefinition mirrors the TS interface.
type TelemetrySpanDefinition struct {
	Description     string                                       `json:"description"`
	Parents         TelemetryParentDefinition                    `json:"parents"`
	StartAttributes map[string]TelemetryStartAttributeDefinition `json:"startAttributes"`
	EndAttributes   map[string]TelemetryAttributeDefinition      `json:"endAttributes"`
	Events          map[string]TelemetryEventDefinition          `json:"events,omitempty"`
	Status          TelemetrySpanStatusDefinition                `json:"status"`
}

// TelemetrySpanStatusDefinition is {default:"ok"; errorWhen:string}.
type TelemetrySpanStatusDefinition struct {
	Default   string `json:"default"`
	ErrorWhen string `json:"errorWhen"`
}

// TelemetrySchemaDefinition mirrors the TS interface.
type TelemetrySchemaDefinition struct {
	Version int                                `json:"version"`
	Spans   map[string]TelemetrySpanDefinition `json:"spans"`
}

// DefineTelemetrySchema is the typed identity helper; the runtime value is
// returned unchanged.
func DefineTelemetrySchema(schema TelemetrySchemaDefinition) TelemetrySchemaDefinition {
	return schema
}

// ---------------------------------------------------------------------------
// Typed span starter
// ---------------------------------------------------------------------------

// TypedSpanStarter mirrors the runtime shape of createTypedSpanStarter's
// result: schema values are inference-only in TS and unused at runtime.
type TypedSpanStarter func(
	name string,
	attributes SpanAttributes,
	callback func(span TelemetrySpan, startChildSpan TypedSpanStarter) (any, error),
) (any, error)

// StartTypedSpan is the generic helper over TypedSpanStarter.
func StartTypedSpan[T any](
	starter TypedSpanStarter,
	name string,
	attributes SpanAttributes,
	fn func(span TelemetrySpan, startChildSpan TypedSpanStarter) (T, error),
) (T, error) {
	var zero T
	result, err := starter(name, attributes, func(span TelemetrySpan, child TypedSpanStarter) (any, error) {
		v, err := fn(span, child)
		return v, err
	})
	if err != nil {
		return zero, err
	}
	if result == nil {
		return zero, nil
	}
	v, ok := result.(T)
	if !ok {
		return zero, ErrResultType
	}
	return v, nil
}

// CreateTypedSpanStarter binds an explicit parent context to the combined
// span vocabulary of one or more schemas. Schemas are accepted for parity but
// carry no runtime behavior (mirroring the TS implementation).
func CreateTypedSpanStarter(telemetryContext TelemetryContext, _schemas ...TelemetrySchemaDefinition) TypedSpanStarter {
	return bindTypedSpanStarter(telemetryContext)
}

func bindTypedSpanStarter(telemetryContext TelemetryContext) TypedSpanStarter {
	return func(name string, attributes SpanAttributes, callback func(TelemetrySpan, TypedSpanStarter) (any, error)) (any, error) {
		return telemetryContext.StartSpan(
			SpanOptions{Name: name, Attributes: attributes},
			func(span TelemetrySpan) (any, error) {
				return callback(span, bindTypedSpanStarter(span))
			},
		)
	}
}

// ErrResultType is returned when a StartSpan generic helper receives a result
// of an unexpected type (cannot happen for well-typed callbacks).
var ErrResultType = &ResultTypeError{}

// ResultTypeError marks a StartSpan result type mismatch.
type ResultTypeError struct{}

func (e *ResultTypeError) Error() string { return "telemetry: span callback result type mismatch" }
