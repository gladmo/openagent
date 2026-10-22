// Package telemetrytesting ports @gladmo/pi-telemetry/testing: the
// runner-independent conformance cases for telemetry adapter implementations.
//
// The TS cases rely on Proxy-based "unreadable payload" objects and undefined
// rejection values; those sub-cases have no Go equivalent and are noted in
// PORTING.md. All behavioral cases are ported.
package telemetrytesting

import (
	"errors"
	"maps"
	"strconv"
	"testing"

	"github.com/gladmo/openagent/telemetry"
)

// TelemetryAdapterFixture mirrors the TS interface (asyncDispose becomes
// Dispose). Propagation, Metrics, and GetMetrics are optional capabilities;
// nil means the adapter does not provide them.
type TelemetryAdapterFixture interface {
	Context() telemetry.TelemetryContext
	GetSpans() ([]telemetry.RecordedTelemetrySpan, error)
	Dispose() error
}

// OptionalFixtureCapabilities carries the optional fixture surfaces; cases
// skip themselves when a capability is nil (TS `fixture.metrics ?? ...`).
type OptionalFixtureCapabilities interface {
	Propagation() telemetry.TelemetryPropagation // nil when unsupported
	Metrics() telemetry.TelemetryMetrics         // nil when unsupported
	GetMetrics() ([]telemetry.RecordedTelemetryMeasurement, error)
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

// carriersEqual compares two propagation carriers by value (TS
// deepStrictEqual on plain objects).
func carriersEqual(left, right telemetry.TelemetryCarrier) bool {
	return maps.Equal(left, right)
}

// copyTestCarrier clones a carrier for a cross-adapter wire hop (TS
// JSON.parse(JSON.stringify(carrier))).
func copyTestCarrier(carrier telemetry.TelemetryCarrier) telemetry.TelemetryCarrier {
	out := telemetry.TelemetryCarrier{}
	maps.Copy(out, carrier)
	return out
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
		createCase("capabilities", "carries adapter capabilities through contexts and spans",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				caps, ok := fixture.(OptionalFixtureCapabilities)
				if !ok {
					return
				}
				context := fixture.Context()
				metrics := caps.Metrics()
				if metrics == nil {
					metrics = context.Metrics()
				}
				propagation := caps.Propagation()
				if propagation == nil {
					propagation = context.Propagation()
				}
				assertCapabilities := func(ctx telemetry.TelemetryContext) {
					if metrics != nil && ctx.Metrics() != metrics {
						t.Fatalf("metrics capability not carried: %v", ctx.Metrics())
					}
					if propagation != nil && ctx.Propagation() != propagation {
						t.Fatalf("propagation capability not carried: %v", ctx.Propagation())
					}
				}
				assertCapabilities(context)
				var settled telemetry.TelemetrySpan
				_, err := telemetry.StartSpan(context, telemetry.SpanOptions{Name: "capabilities"}, func(span telemetry.TelemetrySpan) (any, error) {
					settled = span
					assertCapabilities(span)
					_, err := telemetry.StartSpan(span, telemetry.SpanOptions{Name: "capabilities.child"}, func(child telemetry.TelemetrySpan) (any, error) {
						assertCapabilities(child)
						return nil, nil
					})
					if err != nil {
						return nil, err
					}
					if propagation != nil {
						parent := propagation.Extract(propagation.Inject(span))
						assertCapabilities(parent)
						_, err := telemetry.StartSpan(parent, telemetry.SpanOptions{Name: "capabilities.extracted"}, func(child telemetry.TelemetrySpan) (any, error) {
							assertCapabilities(child)
							return nil, nil
						})
						if err != nil {
							return nil, err
						}
					}
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if settled == nil {
					t.Fatal("expected settled span")
				}
				_, err = telemetry.StartSpan(settled, telemetry.SpanOptions{Name: "capabilities.inert"}, func(inert telemetry.TelemetrySpan) (any, error) {
					assertCapabilities(inert)
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}),

		createCase("metrics", "records ordered measurements and optional histogram exemplars",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				caps, ok := fixture.(OptionalFixtureCapabilities)
				if !ok || caps.Metrics() == nil {
					return
				}
				metrics := caps.Metrics()
				metrics.Counter("bytes", &telemetry.InstrumentOptions{Description: "Encoded bytes", Unit: "By"}).
					Add(12, telemetry.SpanAttributes{"direction": "out"})
				active := metrics.UpDownCounter("active", nil)
				active.Add(1, nil)
				active.Add(-1, nil)
				histogram := metrics.Histogram("duration", &telemetry.HistogramOptions{Unit: "ms", Boundaries: []float64{1, 10}})
				_, err := telemetry.StartSpan(fixture.Context(), telemetry.SpanOptions{Name: "measured"}, func(span telemetry.TelemetrySpan) (any, error) {
					histogram.Record(5, telemetry.SpanAttributes{"method": "read"}, span)
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				measurements, err := caps.GetMetrics()
				if err != nil {
					t.Fatal(err)
				}
				want := [][3]any{
					{"counter", "bytes", float64(12)},
					{"upDownCounter", "active", float64(1)},
					{"upDownCounter", "active", float64(-1)},
					{"histogram", "duration", float64(5)},
				}
				if len(measurements) != len(want) {
					t.Fatalf("measurements = %+v", measurements)
				}
				for i, entry := range want {
					if measurements[i].Kind != entry[0] || measurements[i].Name != entry[1] || measurements[i].Value != entry[2] {
						t.Fatalf("measurement[%d] = %+v, want %v", i, measurements[i], entry)
					}
				}
				if measurements[0].Attributes["direction"] != "out" {
					t.Fatalf("attributes = %+v", measurements[0].Attributes)
				}
				if measurements[0].Description != "Encoded bytes" || measurements[0].Unit != "By" {
					t.Fatalf("metadata = %+v", measurements[0])
				}
				if len(measurements[3].Boundaries) != 2 || measurements[3].Boundaries[0] != 1 || measurements[3].Boundaries[1] != 10 {
					t.Fatalf("boundaries = %+v", measurements[3].Boundaries)
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				if !carriersEqual(measurements[3].Context, findSpan(t, spans, "measured").Context) {
					t.Fatalf("exemplar = %+v", measurements[3].Context)
				}
			}),

		createCase("metrics", "copies instrument metadata and measurement attributes",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				caps, ok := fixture.(OptionalFixtureCapabilities)
				if !ok || caps.Metrics() == nil {
					return
				}
				boundaries := []float64{1, 10}
				options := &telemetry.HistogramOptions{Description: "Original", Boundaries: boundaries}
				histogram := caps.Metrics().Histogram("snapshot", options)
				boundaries[1] = 100
				options.Description = "Changed"
				attributes := telemetry.SpanAttributes{"tags": []string{"original"}}
				histogram.Record(-1, attributes, nil)
				attributes["tags"] = []string{"changed"}
				measurements, err := caps.GetMetrics()
				if err != nil {
					t.Fatal(err)
				}
				if len(measurements) != 1 {
					t.Fatalf("measurements = %+v", measurements)
				}
				tags, _ := measurements[0].Attributes["tags"].([]string)
				if len(tags) != 1 || tags[0] != "original" {
					t.Fatalf("attributes = %+v", measurements[0].Attributes)
				}
				if len(measurements[0].Boundaries) != 2 || measurements[0].Boundaries[0] != 1 || measurements[0].Boundaries[1] != 10 {
					t.Fatalf("boundaries = %+v", measurements[0].Boundaries)
				}
				if measurements[0].Description != "Original" {
					t.Fatalf("description = %q", measurements[0].Description)
				}
				if measurements[0].Context != nil {
					t.Fatalf("context = %+v", measurements[0].Context)
				}
			}),

		createCase("links", "records initial and late links without changing parents",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				var target telemetry.TelemetrySpan
				_, err := telemetry.StartSpan(fixture.Context(), telemetry.SpanOptions{Name: "target"}, func(span telemetry.TelemetrySpan) (any, error) {
					target = span
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if target == nil {
					t.Fatal("expected target span")
				}
				attributes := telemetry.SpanAttributes{"reason": []string{"initial"}}
				_, err = telemetry.StartSpan(fixture.Context(), telemetry.SpanOptions{
					Name:       "linked",
					Attributes: nil,
					Links:      []telemetry.SpanLink{{Context: target, Attributes: attributes}},
				}, func(span telemetry.TelemetrySpan) (any, error) {
					attributes["reason"] = []string{"mutated"}
					span.AddLink(telemetry.SpanLink{Context: target, Attributes: telemetry.SpanAttributes{"reason": "late"}})
					span.AddLink(telemetry.SpanLink{Context: telemetry.NOOP_TELEMETRY_CONTEXT})
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				linked := findSpan(t, spans, "linked")
				if linked.ParentID != nil || linked.ParentContext != nil {
					t.Fatalf("link changed parentage: %+v", linked)
				}
				if len(linked.Links) != 2 {
					t.Fatalf("links = %+v", linked.Links)
				}
				targetSpan := findSpan(t, spans, "target")
				if !carriersEqual(linked.Links[0].Context, targetSpan.Context) || !carriersEqual(linked.Links[1].Context, targetSpan.Context) {
					t.Fatalf("link carriers = %+v", linked.Links)
				}
				if reason, _ := linked.Links[0].Attributes["reason"].([]string); len(reason) != 1 || reason[0] != "initial" {
					t.Fatalf("initial link attributes = %+v", linked.Links[0].Attributes)
				}
				if linked.Links[1].Attributes["reason"] != "late" {
					t.Fatalf("late link attributes = %+v", linked.Links[1].Attributes)
				}
			}),

		createCase("links", "ignores links added after source settlement",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				var source telemetry.TelemetrySpan
				_, err := telemetry.StartSpan(fixture.Context(), telemetry.SpanOptions{Name: "link-source"}, func(span telemetry.TelemetrySpan) (any, error) {
					source = span
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if source == nil {
					t.Fatal("expected source span")
				}
				source.AddLink(telemetry.SpanLink{Context: source})
				spans, err := fixture.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				if links := findSpan(t, spans, "link-source").Links; len(links) != 0 {
					t.Fatalf("links = %+v", links)
				}
			}),

		createCase("propagation", "roundtrips parent identity through JSON without retaining the carrier",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				caps, ok := fixture.(OptionalFixtureCapabilities)
				if !ok || caps.Propagation() == nil {
					return
				}
				propagation := caps.Propagation()
				_, err := telemetry.StartSpan(fixture.Context(), telemetry.SpanOptions{Name: "sender"}, func(span telemetry.TelemetrySpan) (any, error) {
					carrier := propagation.Inject(span)
					if carrier == nil {
						t.Fatal("expected carrier")
					}
					wire := telemetry.TelemetryCarrier{}
					for key, value := range carrier {
						wire[key] = value
					}
					parent := propagation.Extract(wire)
					for key := range wire {
						wire[key] = "mutated"
					}
					_, err := telemetry.StartSpan(parent, telemetry.SpanOptions{Name: "receiver"}, func(telemetry.TelemetrySpan) (any, error) {
						return 42, nil
					})
					if err != nil {
						return nil, err
					}
					if reinjected := propagation.Inject(span); !carriersEqual(reinjected, carrier) {
						t.Fatalf("carrier mutated: %+v", reinjected)
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
				sender := findSpan(t, spans, "sender")
				receiver := findSpan(t, spans, "receiver")
				if !carriersEqual(receiver.ParentContext, sender.Context) {
					t.Fatalf("parent context = %+v, want %+v", receiver.ParentContext, sender.Context)
				}
			}),

		createCase("propagation", "absent and unknown carriers produce recorded roots",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				caps, ok := fixture.(OptionalFixtureCapabilities)
				if !ok || caps.Propagation() == nil {
					return
				}
				propagation := caps.Propagation()
				if carrier := propagation.Inject(telemetry.NOOP_TELEMETRY_CONTEXT); carrier != nil {
					t.Fatalf("NOOP inject = %+v", carrier)
				}
				carriers := []telemetry.TelemetryCarrier{nil, {}, {"unknown": "format"}}
				for index, carrier := range carriers {
					context := propagation.Extract(carrier)
					result, err := telemetry.StartSpan(context, telemetry.SpanOptions{Name: "fallback-" + strconv.Itoa(index)}, func(telemetry.TelemetrySpan) (any, error) {
						return 42, nil
					})
					if err != nil || result != 42 {
						t.Fatalf("fallback-%d: result=%v err=%v", index, result, err)
					}
				}
				spans, err := fixture.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				for _, span := range spans {
					if span.ParentContext != nil {
						t.Fatalf("span %s has parent %+v", span.Name, span.ParentContext)
					}
				}
			}),

		createCase("propagation", "propagates across fresh adapters and multiple hops",
			func(t *testing.T, fixture TelemetryAdapterFixture) {
				caps, ok := fixture.(OptionalFixtureCapabilities)
				if !ok || caps.Propagation() == nil {
					return
				}
				propagation := caps.Propagation()
				newCaps := func() (TelemetryAdapterFixture, OptionalFixtureCapabilities) {
					built, err := factory()
					if err != nil {
						t.Fatalf("fixture: %v", err)
					}
					capabilities, ok := built.(OptionalFixtureCapabilities)
					if !ok || capabilities.Propagation() == nil {
						t.Fatal("Expected propagation on each fixture")
					}
					return built, capabilities
				}
				receiver, receiverCaps := newCaps()
				defer func() { _ = receiver.Dispose() }()
				worker, workerCaps := newCaps()
				defer func() { _ = worker.Dispose() }()

				var senderCarrier telemetry.TelemetryCarrier
				_, err := telemetry.StartSpan(fixture.Context(), telemetry.SpanOptions{Name: "sender"}, func(span telemetry.TelemetrySpan) (any, error) {
					senderCarrier = propagation.Inject(span)
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if senderCarrier == nil {
					t.Fatal("expected sender carrier")
				}
				var receiverCarrier telemetry.TelemetryCarrier
				_, err = telemetry.StartSpan(receiverCaps.Propagation().Extract(copyTestCarrier(senderCarrier)), telemetry.SpanOptions{Name: "receiver"}, func(span telemetry.TelemetrySpan) (any, error) {
					receiverCarrier = receiverCaps.Propagation().Inject(span)
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if receiverCarrier == nil {
					t.Fatal("expected receiver carrier")
				}
				_, err = telemetry.StartSpan(workerCaps.Propagation().Extract(copyTestCarrier(receiverCarrier)), telemetry.SpanOptions{Name: "worker"}, func(telemetry.TelemetrySpan) (any, error) {
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				receiverSpans, err := receiver.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				if parent := findSpan(t, receiverSpans, "receiver").ParentContext; !carriersEqual(parent, senderCarrier) {
					t.Fatalf("receiver parent = %+v, want %+v", parent, senderCarrier)
				}
				workerSpans, err := worker.GetSpans()
				if err != nil {
					t.Fatal(err)
				}
				if parent := findSpan(t, workerSpans, "worker").ParentContext; !carriersEqual(parent, receiverCarrier) {
					t.Fatalf("worker parent = %+v, want %+v", parent, receiverCarrier)
				}
			}),

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
