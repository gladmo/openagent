// Package telemetrytesting ports @gladmo/pi-telemetry/testing: the
// runner-independent conformance cases for telemetry adapter implementations.
//
// The TS cases rely on Proxy-based "unreadable payload" objects and undefined
// rejection values; those sub-cases have no Go equivalent and are noted in
// PORTING.md. All behavioral cases are ported.
package telemetrytesting

import (
	"errors"
	"testing"

	"github.com/gladmo/openagent/telemetry"
)

// TelemetryAdapterFixture mirrors the TS interface (asyncDispose becomes
// Dispose).
type TelemetryAdapterFixture interface {
	Context() telemetry.TelemetryContext
	GetSpans() ([]telemetry.RecordedTelemetrySpan, error)
	Dispose() error
}

// TelemetryAdapterFixtureFactory mirrors the TS type.
type TelemetryAdapterFixtureFactory func() (TelemetryAdapterFixture, error)

// TelemetryAdapterConformanceCase mirrors the TS interface.
type TelemetryAdapterConformanceCase struct {
	Group string
	Name  string
	Run   func(t *testing.T)
}

func findSpan(t *testing.T, spans []telemetry.RecordedTelemetrySpan, name string) telemetry.RecordedTelemetrySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("Expected recorded span %s", name)
	return telemetry.RecordedTelemetrySpan{}
}

// CreateTelemetryAdapterConformance creates the conformance cases for a
// fixture factory.
func CreateTelemetryAdapterConformance(factory TelemetryAdapterFixtureFactory) []TelemetryAdapterConformanceCase {
	createCase := func(group, name string, test func(t *testing.T, fixture TelemetryAdapterFixture)) TelemetryAdapterConformanceCase {
		return TelemetryAdapterConformanceCase{
			Group: group,
			Name:  name,
			Run: func(t *testing.T) {
				fixture, err := factory()
				if err != nil {
					t.Fatalf("fixture: %v", err)
				}
				defer func() {
					if err := fixture.Dispose(); err != nil {
						t.Fatalf("dispose: %v", err)
					}
				}()
				test(t, fixture)
			},
		}
	}
	return []TelemetryAdapterConformanceCase{
		createCase("callback lifecycle", "admits once synchronously and preserves the result",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				admitted := false
				calls := 0
				expected := &struct{ Value int }{42}
				result, err := fixture.Context().StartSpan(telemetry.SpanOptions{Name: "success"}, func(telemetry.TelemetrySpan) (any, error) {
					admitted = true
					calls++
					return expected, nil
				})
				if !admitted || calls != 1 {
					t.Fatalf("admitted=%v calls=%d", admitted, calls)
				}
				if err != nil || result != any(expected) {
					t.Fatalf("identity mismatch: result=%v err=%v", result, err)
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				span := findSpan(t, spans, "success")
				if !span.Status.Equal(telemetry.StatusOK()) {
					t.Fatalf("status = %+v", span.Status)
				}
				if !span.Settled {
					t.Fatal("not settled")
				}
			}),

		createCase("callback lifecycle", "preserves rejection values",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				syncError := errors.New("sync")
				_, err := fixture.Context().StartSpan(telemetry.SpanOptions{Name: "sync-error"}, func(telemetry.TelemetrySpan) (any, error) {
					return nil, syncError
				})
				if !errors.Is(err, syncError) {
					t.Fatalf("sync err = %v", err)
				}
				asyncError := errors.New("async")
				_, err = fixture.Context().StartSpan(telemetry.SpanOptions{Name: "async-error"}, func(telemetry.TelemetrySpan) (any, error) {
					return nil, asyncError
				})
				if !errors.Is(err, asyncError) {
					t.Fatalf("async err = %v", err)
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"sync-error", "async-error"} {
					if status := findSpan(t, spans, name).Status; status.Status != "error" {
						t.Fatalf("%s status = %+v", name, status)
					}
				}
			}),

		createCase("status", "uses last explicit status without automatic overwrite",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				_, _ = fixture.Context().StartSpan(telemetry.SpanOptions{Name: "last-status"}, func(span telemetry.TelemetrySpan) (any, error) {
					span.SetStatus(telemetry.StatusError("Expected", "first"))
					span.SetStatus(telemetry.StatusOK())
					return nil, nil
				})
				thrown := errors.New("after explicit status")
				_, _ = fixture.Context().StartSpan(telemetry.SpanOptions{Name: "explicit-before-throw"}, func(span telemetry.TelemetrySpan) (any, error) {
					span.SetStatus(telemetry.StatusOK())
					return nil, thrown
				})
				rejected := errors.New("after async explicit status")
				_, _ = fixture.Context().StartSpan(telemetry.SpanOptions{Name: "explicit-before-rejection"}, func(span telemetry.TelemetrySpan) (any, error) {
					span.SetStatus(telemetry.StatusError("Expected", "async failure"))
					return nil, rejected
				})
				_, _ = fixture.Context().StartSpan(telemetry.SpanOptions{Name: "expected-failure"}, func(span telemetry.TelemetrySpan) (any, error) {
					span.SetStatus(telemetry.StatusError("Expected", "returned failure"))
					return map[string]any{"ok": false}, nil
				})
				spans, err := fixture.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				if status := findSpan(t, spans, "last-status").Status; !status.Equal(telemetry.StatusOK()) {
					t.Fatalf("last-status = %+v", status)
				}
				if status := findSpan(t, spans, "explicit-before-throw").Status; !status.Equal(telemetry.StatusOK()) {
					t.Fatalf("explicit-before-throw = %+v", status)
				}
				if status := findSpan(t, spans, "explicit-before-rejection").Status; !status.Equal(telemetry.StatusError("Expected", "async failure")) {
					t.Fatalf("explicit-before-rejection = %+v", status)
				}
				if status := findSpan(t, spans, "expected-failure").Status; !status.Equal(telemetry.StatusError("Expected", "returned failure")) {
					t.Fatalf("expected-failure = %+v", status)
				}
			}),

		createCase("recording", "merges attributes and records ordered events",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				_, err := fixture.Context().StartSpan(telemetry.SpanOptions{
					Name:       "recording",
					Attributes: telemetry.SpanAttributes{"start": "value", "overwrite": "start"},
				}, func(span telemetry.TelemetrySpan) (any, error) {
					span.SetAttributes(telemetry.SpanAttributes{"count": float64(1), "overwrite": "middle"})
					span.SetAttributes(telemetry.SpanAttributes{"overwrite": "end"})
					span.AddEvent("first", telemetry.SpanAttributes{"index": float64(1)})
					span.AddEvent("second", telemetry.SpanAttributes{"index": float64(2)})
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				span := findSpan(t, spans, "recording")
				wantAttrs := telemetry.SpanAttributes{"start": "value", "overwrite": "end", "count": float64(1)}
				if len(span.Attributes) != len(wantAttrs) {
					t.Fatalf("attributes = %+v", span.Attributes)
				}
				for k, v := range wantAttrs {
					if span.Attributes[k] != v {
						t.Fatalf("attributes = %+v", span.Attributes)
					}
				}
				if len(span.Events) != 2 || span.Events[0].Name != "first" || span.Events[1].Name != "second" {
					t.Fatalf("events = %+v", span.Events)
				}
				if span.Events[0].Attributes["index"] != float64(1) || span.Events[1].Attributes["index"] != float64(2) {
					t.Fatalf("event attributes = %+v", span.Events)
				}
			}),

		createCase("recording", "makes calls after settlement inert",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				var settledSpan telemetry.TelemetrySpan
				_, err := fixture.Context().StartSpan(telemetry.SpanOptions{
					Name:       "settled",
					Attributes: telemetry.SpanAttributes{"value": "initial"},
				}, func(span telemetry.TelemetrySpan) (any, error) {
					settledSpan = span
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				settledSpan.SetAttributes(telemetry.SpanAttributes{"value": "late"})
				settledSpan.AddEvent("late", telemetry.SpanAttributes{"value": true})
				settledSpan.SetStatus(telemetry.StatusErrorWithoutDetails())
				childAdmitted := false
				childResult, err := settledSpan.StartSpan(telemetry.SpanOptions{Name: "late-child"}, func(telemetry.TelemetrySpan) (any, error) {
					childAdmitted = true
					return 7, nil
				})
				if !childAdmitted || err != nil || childResult != 7 {
					t.Fatalf("child admitted=%v result=%v err=%v", childAdmitted, childResult, err)
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				if len(spans) != 1 {
					t.Fatalf("spans = %+v", spans)
				}
				if spans[0].Attributes["value"] != "initial" || len(spans[0].Events) != 0 || !spans[0].Status.Equal(telemetry.StatusOK()) {
					t.Fatalf("span = %+v", spans[0])
				}
			}),

		createCase("parentage", "records nested and concurrent child relationships",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				releaseFirst := make(chan struct{})
				firstDone := make(chan error, 1)
				_, err := fixture.Context().StartSpan(telemetry.SpanOptions{Name: "parent"}, func(parent telemetry.TelemetrySpan) (any, error) {
					go func() {
						_, err := parent.StartSpan(telemetry.SpanOptions{Name: "first-child"}, func(telemetry.TelemetrySpan) (any, error) {
							<-releaseFirst
							return nil, nil
						})
						firstDone <- err
					}()
					second, err := telemetry.StartSpan(parent, telemetry.SpanOptions{Name: "second-child"}, func(telemetry.TelemetrySpan) (string, error) {
						return "done", nil
					})
					if err != nil {
						return nil, err
					}
					if second != "done" {
						t.Fatalf("second = %v", second)
					}
					close(releaseFirst)
					if err := <-firstDone; err != nil {
						return nil, err
					}
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				parent := findSpan(t, spans, "parent")
				first := findSpan(t, spans, "first-child")
				second := findSpan(t, spans, "second-child")
				if parent.ParentID != nil {
					t.Fatal("parent has a parent")
				}
				if first.ParentID == nil || *first.ParentID != parent.ID {
					t.Fatalf("first parentId = %v", first.ParentID)
				}
				if second.ParentID == nil || *second.ParentID != parent.ID {
					t.Fatalf("second parentId = %v", second.ParentID)
				}
				if second.EndSequence == nil || first.EndSequence == nil || parent.EndSequence == nil {
					t.Fatal("missing end sequences")
				}
				if !(*second.EndSequence < *first.EndSequence && *first.EndSequence < *parent.EndSequence) {
					t.Fatalf("settlement order: second=%v first=%v parent=%v", *second.EndSequence, *first.EndSequence, *parent.EndSequence)
				}
			}),
	}
}
