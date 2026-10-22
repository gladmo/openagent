package telemetry

// Port of pi-telemetry metrics.ts: the optional adapter metrics capability,
// typed metric definitions, and the schema-bound typed metrics factory.

// InstrumentOptions mirrors the TS interface (nil pointer = absent options).
type InstrumentOptions struct {
	Description string `json:"description,omitempty"`
	Unit        string `json:"unit,omitempty"`
}

// HistogramOptions extends InstrumentOptions with bucket hints; callers
// supply finite, strictly increasing values.
type HistogramOptions struct {
	Description string    `json:"description,omitempty"`
	Unit        string    `json:"unit,omitempty"`
	Boundaries  []float64 `json:"boundaries,omitempty"`
}

// TelemetryCounter records nonnegative increments.
type TelemetryCounter interface {
	Add(value float64, attributes SpanAttributes)
}

// TelemetryUpDownCounter records increments and decrements.
type TelemetryUpDownCounter interface {
	Add(value float64, attributes SpanAttributes)
}

// TelemetryHistogram records measurements; context is an optional exemplar
// span.
type TelemetryHistogram interface {
	Record(value float64, attributes SpanAttributes, context TelemetryContext)
}

// TelemetryMetrics is the optional adapter capability with synchronous
// factories and recording methods. Typed inputs are trusted; callers supply
// finite measurements and nonnegative counter increments.
type TelemetryMetrics interface {
	Counter(name string, options *InstrumentOptions) TelemetryCounter
	UpDownCounter(name string, options *InstrumentOptions) TelemetryUpDownCounter
	Histogram(name string, options *HistogramOptions) TelemetryHistogram
}

// TelemetryMetricAttributeDefinition is a metric attribute definition
// (a start-attribute definition; cardinality rides on the base metadata).
type TelemetryMetricAttributeDefinition struct {
	TelemetryAttributeDefinition
	Required bool `json:"required"`
}

// TelemetryMetricDefinition mirrors the TS discriminated union as one struct:
// Boundaries is only meaningful for kind "histogram".
type TelemetryMetricDefinition struct {
	Description string                                        `json:"description"`
	Unit        string                                        `json:"unit,omitempty"`
	Kind        string                                        `json:"kind"` // "counter" | "upDownCounter" | "histogram"
	Boundaries  []float64                                     `json:"boundaries,omitempty"`
	Attributes  map[string]TelemetryMetricAttributeDefinition `json:"attributes"`
}

// TypedTelemetryMetrics binds instrument names, kinds, and metadata to one or
// more schemas; unknown names pass nil options.
type TypedTelemetryMetrics struct {
	metrics     TelemetryMetrics
	definitions map[string]TelemetryMetricDefinition
}

func (t TypedTelemetryMetrics) instrumentOptions(definition TelemetryMetricDefinition, ok bool) *InstrumentOptions {
	if !ok {
		return nil
	}
	return &InstrumentOptions{Description: definition.Description, Unit: definition.Unit}
}

// Counter returns the schema-bound counter for name.
func (t TypedTelemetryMetrics) Counter(name string) TelemetryCounter {
	definition, ok := t.definitions[name]
	return t.metrics.Counter(name, t.instrumentOptions(definition, ok))
}

// UpDownCounter returns the schema-bound up-down counter for name.
func (t TypedTelemetryMetrics) UpDownCounter(name string) TelemetryUpDownCounter {
	definition, ok := t.definitions[name]
	return t.metrics.UpDownCounter(name, t.instrumentOptions(definition, ok))
}

// Histogram returns the schema-bound histogram for name.
func (t TypedTelemetryMetrics) Histogram(name string) TelemetryHistogram {
	definition, ok := t.definitions[name]
	if !ok {
		return t.metrics.Histogram(name, nil)
	}
	return t.metrics.Histogram(name, &HistogramOptions{
		Description: definition.Description,
		Unit:        definition.Unit,
		Boundaries:  definition.Boundaries,
	})
}

// CreateTypedMetrics binds instrument names, kinds, metadata, and exact
// measurement attributes to one or more schemas. Later schemas win on
// duplicate names, mirroring the TS Map insertion order.
func CreateTypedMetrics(metrics TelemetryMetrics, schemas ...TelemetrySchemaDefinition) TypedTelemetryMetrics {
	definitions := map[string]TelemetryMetricDefinition{}
	for _, schema := range schemas {
		for name, definition := range schema.Metrics {
			definitions[name] = definition
		}
	}
	return TypedTelemetryMetrics{metrics: metrics, definitions: definitions}
}
