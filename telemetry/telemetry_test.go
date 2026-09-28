package telemetry

import (
	"encoding/json"
	"errors"
	"testing"
)

// Port of pi/packages/telemetry/test/telemetry.test.ts (runtime subset; the
// compile-time Proxy/type-level assertions have no Go equivalent).

func TestDefineTelemetrySchemaPreservesDefinition(t *testing.T) {
	definition := TelemetrySchemaDefinition{
		Version: 1,
		Spans: map[string]TelemetrySpanDefinition{
			"operation": {
				Description: "Test operation",
				Parents:     TelemetryParentDefinition{Kind: "any"},
				StartAttributes: map[string]TelemetryStartAttributeDefinition{
					"kind": {TelemetryAttributeDefinition{Type: "string", Values: []string{"read", "write"}, Description: "Kind"}, true},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{},
				Events: map[string]TelemetryEventDefinition{
					"result": {
						Description: "Result",
						Attributes: map[string]TelemetryEventAttributeDefinition{
							"outcome": {TelemetryAttributeDefinition{Type: "string", Values: []string{"ok", "error"}, Description: "Outcome"}, true},
						},
					},
				},
				Status: TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The operation fails"},
			},
		},
	}
	schema := DefineTelemetrySchema(definition)
	if schema.Version != 1 || len(schema.Spans) != 1 {
		t.Fatalf("schema = %+v", schema)
	}
	if _, err := json.Marshal(schema); err != nil {
		t.Fatalf("schema not serializable: %v", err)
	}
}

func TestTypedSpanStarterCombinesVocabularies(t *testing.T) {
	operationSchema := TelemetrySchemaDefinition{
		Version: 1,
		Spans: map[string]TelemetrySpanDefinition{
			"operation": {
				Description: "Operation",
				Parents:     TelemetryParentDefinition{Kind: "root_or_external"},
				StartAttributes: map[string]TelemetryStartAttributeDefinition{
					"kind": {TelemetryAttributeDefinition{Type: "string", Values: []string{"read", "write"}, Description: "Kind"}, true},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{},
				Status:        TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The operation fails"},
			},
		},
	}
	requestSchema := TelemetrySchemaDefinition{
		Version: 3,
		Spans: map[string]TelemetrySpanDefinition{
			"request": {
				Description: "Request",
				Parents:     TelemetryParentDefinition{Kind: "spans", Spans: []string{"operation"}},
				StartAttributes: map[string]TelemetryStartAttributeDefinition{
					"provider": {TelemetryAttributeDefinition{Type: "string", Description: "Provider"}, true},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{
					"response": {Type: "string", Description: "Response kind"},
				},
				Status: TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The request fails"},
			},
		},
	}
	telemetryContext := NewInMemoryTelemetryContext()
	startSpan := CreateTypedSpanStarter(telemetryContext, operationSchema, requestSchema)

	result, err := StartTypedSpan(startSpan, "operation", SpanAttributes{"kind": "read"},
		func(_ TelemetrySpan, startChildSpan TypedSpanStarter) (int, error) {
			v, err := StartTypedSpan(startChildSpan, "request", SpanAttributes{"provider": "example"},
				func(requestSpan TelemetrySpan, _ TypedSpanStarter) (int, error) {
					requestSpan.SetAttributes(SpanAttributes{"response": "cached"})
					return 42, nil
				})
			return v, err
		})
	if err != nil {
		t.Fatal(err)
	}
	if result != 42 {
		t.Fatalf("result = %v", result)
	}
	spans := telemetryContext.GetSpans()
	var operationSpan, requestSpan *RecordedTelemetrySpan
	for i := range spans {
		switch spans[i].Name {
		case "operation":
			operationSpan = &spans[i]
		case "request":
			requestSpan = &spans[i]
		}
	}
	if operationSpan == nil || requestSpan == nil {
		t.Fatalf("spans missing: %+v", spans)
	}
	if operationSpan.ParentID != nil {
		t.Fatalf("operation parentId = %v", *operationSpan.ParentID)
	}
	if requestSpan.ParentID == nil || *requestSpan.ParentID != operationSpan.ID {
		t.Fatalf("request parentId = %v", requestSpan.ParentID)
	}

	// Synchronous callback error propagates with identity.
	syncErr := errors.New("sync")
	_, err = StartTypedSpan(startSpan, "operation", SpanAttributes{"kind": "write"},
		func(TelemetrySpan, TypedSpanStarter) (any, error) { return nil, syncErr })
	if !errors.Is(err, syncErr) {
		t.Fatalf("sync error = %v", err)
	}

	_, err = StartTypedSpan(startSpan, "request", SpanAttributes{"provider": "example"},
		func(TelemetrySpan, TypedSpanStarter) (any, error) { return nil, errors.New("async") })
	if err == nil || err.Error() != "async" {
		t.Fatalf("async error = %v", err)
	}
}

func TestNoopContextAdmitsSynchronously(t *testing.T) {
	admitted := false
	var childSpan TelemetrySpan
	result, err := StartSpan(NOOP_TELEMETRY_CONTEXT, SpanOptions{Name: "first"}, func(span TelemetrySpan) (int, error) {
		admitted = true
		child, err := StartSpan(span, SpanOptions{Name: "child"}, func(childSpan TelemetrySpan) (TelemetrySpan, error) {
			return childSpan, nil
		})
		if err != nil {
			return 0, err
		}
		childSpan = child
		return 42, nil
	})
	if !admitted {
		t.Fatal("callback not admitted synchronously")
	}
	if err != nil || result != 42 {
		t.Fatalf("result = %v, err = %v", result, err)
	}
	// The child span is the same inert singleton.
	if childSpan != TelemetrySpan(NOOP_TELEMETRY_CONTEXT) {
		t.Fatal("child span is not the shared noop span")
	}
}

func TestNoopPreservesRejectionValues(t *testing.T) {
	syncErr := errors.New("sync")
	_, err := StartSpan(NOOP_TELEMETRY_CONTEXT, SpanOptions{Name: "sync"}, func(TelemetrySpan) (any, error) {
		return nil, syncErr
	})
	if !errors.Is(err, syncErr) {
		t.Fatalf("err = %v", err)
	}
}

func TestNoopIgnoresPayloads(t *testing.T) {
	_, err := StartSpan(NOOP_TELEMETRY_CONTEXT, SpanOptions{Name: "operation", Attributes: SpanAttributes{"secret": "prompt content"}},
		func(span TelemetrySpan) (any, error) {
			span.AddEvent("event", SpanAttributes{"secret": "content"})
			span.SetAttributes(SpanAttributes{"secret": "content"})
			span.SetStatus(StatusOK())
			return nil, nil
		})
	if err != nil {
		t.Fatal(err)
	}
}
