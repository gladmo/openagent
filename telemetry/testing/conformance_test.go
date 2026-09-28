package telemetrytesting

import (
	"testing"

	"github.com/gladmo/openagent/telemetry"
)

type inMemoryFixture struct {
	context *telemetry.InMemoryTelemetryContext
}

func (f *inMemoryFixture) Context() telemetry.TelemetryContext { return f.context }
func (f *inMemoryFixture) GetSpans() ([]telemetry.RecordedTelemetrySpan, error) {
	return f.context.GetSpans(), nil
}
func (f *inMemoryFixture) Dispose() error { return nil }

// Port of conformance.test.ts: run every case against InMemoryTelemetryContext.
func TestInMemoryConformance(t *testing.T) {
	conformance := CreateTelemetryAdapterConformance(func() (TelemetryAdapterFixture, error) {
		return &inMemoryFixture{context: telemetry.NewInMemoryTelemetryContext()}, nil
	})
	for _, testCase := range conformance {
		testCase := testCase
		t.Run(testCase.Group+"/"+testCase.Name, func(t *testing.T) {
			testCase.Run(t)
		})
	}
}

// Port of the detached-snapshot test from conformance.test.ts.
func TestInMemoryReturnsDetachedSnapshots(t *testing.T) {
	context := telemetry.NewInMemoryTelemetryContext()
	var openSettled bool
	var openEndSequence *int
	_, err := telemetry.StartSpan(context, telemetry.SpanOptions{
		Name:       "snapshot",
		Attributes: telemetry.SpanAttributes{"tags": []string{"initial"}},
	}, func(span telemetry.TelemetrySpan) (any, error) {
		span.AddEvent("event", telemetry.SpanAttributes{"value": float64(1)})
		spans := context.GetSpans()
		openSettled = spans[0].Settled
		openEndSequence = spans[0].EndSequence
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if openSettled {
		t.Fatal("open span reported settled")
	}
	if openEndSequence != nil {
		t.Fatal("open span has endSequence")
	}
	first := context.GetSpans()[0]
	if !first.Settled || first.EndSequence == nil || *first.EndSequence != 1 {
		t.Fatalf("first = %+v", first)
	}
	// Mutating the snapshot must not affect recording state.
	first.Attributes["tags"] = []string{"mutated"}
	first.Events[0].Attributes["value"] = float64(2)
	second := context.GetSpans()[0]
	tags, _ := second.Attributes["tags"].([]string)
	if len(tags) != 1 || tags[0] != "initial" {
		t.Fatalf("attributes mutated through snapshot: %+v", second.Attributes)
	}
	if second.Events[0].Attributes["value"] != float64(1) {
		t.Fatalf("event attributes mutated: %+v", second.Events[0])
	}
}
