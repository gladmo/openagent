package telemetry

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Ports of pi-telemetry test/store.test.ts and test/store-conformance.ts
// against InMemoryTelemetryContext. The TS suite mocks Date.now /
// performance.now; these tests swap the package clock seams the same way.

type mockClock struct {
	wall    time.Time
	elapsed time.Duration
}

func (m *mockClock) install(t *testing.T) {
	t.Helper()
	previousWall, previousSince := wallNow, monotonicSince
	wallNow = func() time.Time { return m.wall }
	monotonicSince = func(time.Time) time.Duration { return m.elapsed }
	t.Cleanup(func() { wallNow, monotonicSince = previousWall, previousSince })
}

func newStoreFixture() *InMemoryTelemetryContext { return NewInMemoryTelemetryContext() }

func TestStoreRecordsBoundariesAndRelativeEventTimes(t *testing.T) {
	clock := &mockClock{wall: time.UnixMilli(1000)}
	clock.install(t)
	telemetry := newStoreFixture()
	var id string
	_, err := telemetry.StartSpan(SpanOptions{Name: "timed"}, func(span TelemetrySpan) (any, error) {
		carrier := telemetry.Inject(span)
		if carrier == nil {
			t.Fatal("expected carrier")
		}
		id = carrierID(carrier)
		clock.elapsed = 10 * time.Millisecond
		span.AddEvent("event", SpanAttributes{"count": float64(1)})
		clock.elapsed = 25 * time.Millisecond
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	trace, ok := telemetry.GetTrace(id)
	if !ok {
		t.Fatalf("missing trace %s", id)
	}
	if len(trace.Spans) != 1 {
		t.Fatalf("spans = %+v", trace.Spans)
	}
	span := trace.Spans[0]
	if span.ID != id || span.TraceID != id || span.ParentID != "" || span.Name != "timed" {
		t.Fatalf("span = %+v", span)
	}
	if span.StartedMs != 1000 || span.EndedMs != 1025 {
		t.Fatalf("boundaries = %d..%d", span.StartedMs, span.EndedMs)
	}
	if len(span.Events) != 1 || span.Events[0].Name != "event" || span.Events[0].TimeMs != 10 {
		t.Fatalf("events = %+v", span.Events)
	}
	if span.Events[0].Attributes["count"] != float64(1) {
		t.Fatalf("event attributes = %+v", span.Events[0].Attributes)
	}
	// Normalized snapshots keep event times out of GetSpans.
	if len(telemetry.GetSpans()[0].Events) != 1 || telemetry.GetSpans()[0].Events[0].Name != "event" {
		t.Fatalf("snapshots = %+v", telemetry.GetSpans())
	}
}

func TestStoreMapsRawMeasurementsToCountOneRows(t *testing.T) {
	clock := &mockClock{wall: time.UnixMilli(20_001)}
	clock.install(t)
	telemetry := newStoreFixture()
	calls := telemetry.Counter("calls", nil)
	calls.Add(2, nil)
	calls.Add(3, nil)
	rows := telemetry.FindMetrics(MetricQuery{Name: "calls"})
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	for i, want := range []float64{2, 3} {
		if rows[i].IntervalMs != 20_000 || rows[i].Count != 1 || rows[i].Sum != want || rows[i].Min != want || rows[i].Max != want {
			t.Fatalf("row[%d] = %+v", i, rows[i])
		}
	}
	measurements := telemetry.GetMetrics()
	if len(measurements) != 2 {
		t.Fatalf("measurements = %+v", measurements)
	}
	for i, want := range []float64{2, 3} {
		m := measurements[i]
		if m.Kind != "counter" || m.Name != "calls" || m.Value != want || m.Context != nil {
			t.Fatalf("measurement[%d] = %+v", i, m)
		}
	}
}

func TestStoreDetachesLinkAttributesAndErrorDetails(t *testing.T) {
	telemetry := newStoreFixture()
	var target TelemetrySpan
	_, err := telemetry.StartSpan(SpanOptions{Name: "target"}, func(span TelemetrySpan) (any, error) {
		target = span
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = telemetry.StartSpan(SpanOptions{
		Name:  "source",
		Links: []SpanLink{{Context: target, Attributes: SpanAttributes{"tags": []string{"original"}}}},
	}, func(span TelemetrySpan) (any, error) {
		span.SetStatus(StatusError("Expected", "original"))
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	trace := telemetry.FindTraces(TraceQuery{Name: "source"}).Traces[0]
	span := trace.Spans[0]
	span.Links[0].Attributes["tags"] = []string{"changed"}
	if span.Status.Error != nil {
		span.Status.Error.Message = "changed"
	}
	fresh, _ := telemetry.GetTrace(trace.TraceID)
	freshSpan := fresh.Spans[0]
	tags, _ := freshSpan.Links[0].Attributes["tags"].([]string)
	if len(tags) != 1 || tags[0] != "original" {
		t.Fatalf("link attributes = %+v", freshSpan.Links[0].Attributes)
	}
	if freshSpan.Status.Error == nil || freshSpan.Status.Error.Message != "original" {
		t.Fatalf("status = %+v", freshSpan.Status)
	}
}

func TestStoreReturnsOnlySettledTracesWithCompleteSnapshots(t *testing.T) {
	clock := &mockClock{wall: time.UnixMilli(20_000)}
	clock.install(t)
	telemetry := newStoreFixture()
	gate := make(chan struct{})
	activeDone := make(chan error, 1)
	go func() {
		_, err := telemetry.StartSpan(SpanOptions{Name: "root", Attributes: SpanAttributes{"tags": []string{"original"}}}, func(root TelemetrySpan) (any, error) {
			root.AddEvent("event", SpanAttributes{"tags": []string{"original"}})
			<-gate
			_, err := root.StartSpan(SpanOptions{Name: "child"}, func(TelemetrySpan) (any, error) {
				return nil, nil
			})
			return nil, err
		})
		activeDone <- err
	}()
	// Wait until the root span is admitted and its event recorded.
	for {
		spans := telemetry.GetSpans()
		if len(spans) == 1 && len(spans[0].Events) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if traces := telemetry.FindTraces(TraceQuery{}).Traces; len(traces) != 0 {
		t.Fatalf("unsettled traces = %+v", traces)
	}
	if _, ok := telemetry.GetTrace("missing"); ok {
		t.Fatal("missing trace resolved")
	}
	close(gate)
	if err := <-activeDone; err != nil {
		t.Fatal(err)
	}
	traces := telemetry.FindTraces(TraceQuery{}).Traces
	if len(traces) != 1 {
		t.Fatalf("traces = %+v", traces)
	}
	trace := traces[0]
	var root, child *StoredSpan
	for i := range trace.Spans {
		switch trace.Spans[i].Name {
		case "root":
			root = &trace.Spans[i]
		case "child":
			child = &trace.Spans[i]
		}
	}
	if root == nil || child == nil {
		t.Fatalf("trace = %+v", trace)
	}
	idPattern := regexp.MustCompile(`^[0-9a-f-]{36}:[1-9][0-9]*$`)
	if !idPattern.MatchString(root.ID) {
		t.Fatalf("root id = %q", root.ID)
	}
	if root.TraceID != root.ID || root.ParentID != "" {
		t.Fatalf("root = %+v", root)
	}
	if child.ParentID != root.ID || child.TraceID != root.TraceID {
		t.Fatalf("child = %+v", child)
	}
	if root.StartedMs != 20_000 || root.EndedMs < root.StartedMs {
		t.Fatalf("boundaries = %d..%d", root.StartedMs, root.EndedMs)
	}
	if root.Events[0].TimeMs < 0 {
		t.Fatalf("event time = %f", root.Events[0].TimeMs)
	}
	// Mutating results must not affect stored state.
	root.Attributes["tags"] = []string{"changed"}
	root.Events[0].Attributes["tags"] = []string{"changed"}
	fresh, _ := telemetry.GetTrace(trace.TraceID)
	for i := range fresh.Spans {
		if fresh.Spans[i].ID == root.ID {
			tags, _ := fresh.Spans[i].Attributes["tags"].([]string)
			eventTags, _ := fresh.Spans[i].Events[0].Attributes["tags"].([]string)
			if len(tags) != 1 || tags[0] != "original" || len(eventTags) != 1 || eventTags[0] != "original" {
				t.Fatalf("mutated through snapshot: %+v", fresh.Spans[i])
			}
		}
	}
}

func TestStoreFiltersByNameStartErrorsAndAttributes(t *testing.T) {
	clock := &mockClock{wall: time.UnixMilli(10_000)}
	clock.install(t)
	telemetry := newStoreFixture()
	mustStart(t, telemetry, SpanOptions{Name: "old"}, func(root TelemetrySpan) error {
		clock.wall = time.UnixMilli(30_000)
		if err := startChild(root, SpanOptions{Name: "recent-child", Attributes: SpanAttributes{"left": float64(1)}}); err != nil {
			return err
		}
		return startChild(root, SpanOptions{Name: "split", Attributes: SpanAttributes{"right": float64(2)}})
	})
	clock.wall = time.UnixMilli(20_000)
	mustStart(t, telemetry, SpanOptions{Name: "wanted"}, func(root TelemetrySpan) error {
		clock.wall = time.UnixMilli(20_001)
		return startChild(root, SpanOptions{
			Name:       "failure",
			Attributes: SpanAttributes{"left": float64(1), "right": float64(2), "valid": true, `odd."key`: "value"},
		}, func(span TelemetrySpan) {
			span.SetStatus(StatusErrorWithoutDetails())
		})
	})
	clock.wall = time.UnixMilli(40_000)
	mustStart(t, telemetry, SpanOptions{Name: "new", Attributes: SpanAttributes{"valid": float64(1)}}, func(TelemetrySpan) error {
		return nil
	})
	names := func(query TraceQuery) []string {
		traces := telemetry.FindTraces(query).Traces
		out := []string{}
		for _, trace := range traces {
			out = append(out, trace.Spans[0].Name)
		}
		return out
	}
	assertNames := func(want []string, query TraceQuery) {
		t.Helper()
		got := names(query)
		if len(got) != len(want) {
			t.Fatalf("query %+v = %v, want %v", query, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("query %+v = %v, want %v", query, got, want)
			}
		}
	}
	assertNames([]string{"wanted"}, TraceQuery{
		Name:       "wanted",
		Attributes: map[string]any{"left": float64(1), "right": float64(2)},
		Errors:     true,
		StartMin:   20_000,
		StartMax:   20_000,
	})
	assertNames([]string{}, TraceQuery{Name: "failure"})
	assertNames([]string{"wanted"}, TraceQuery{Attributes: map[string]any{"left": float64(1), "right": float64(2)}})
	assertNames([]string{"wanted"}, TraceQuery{Attributes: map[string]any{"valid": true, `odd."key`: "value"}})
	assertNames([]string{"new"}, TraceQuery{Attributes: map[string]any{"valid": float64(1)}})
	assertNames([]string{"wanted"}, TraceQuery{Errors: true})
	assertNames([]string{"new", "wanted"}, TraceQuery{StartMin: 20_000})
	assertNames([]string{"old"}, TraceQuery{StartMax: 10_000})
}

func TestStorePagesCompleteTracesWhenStartsTie(t *testing.T) {
	clock := &mockClock{wall: time.UnixMilli(20_000)}
	clock.install(t)
	telemetry := newStoreFixture()
	for index := 0; index < 25; index++ {
		mustStart(t, telemetry, SpanOptions{
			Name:       "root",
			Attributes: SpanAttributes{"index": float64(index)},
		}, func(root TelemetrySpan) error {
			clock.wall = time.UnixMilli(20_001)
			return startChild(root, SpanOptions{Name: "child"})
		})
		clock.wall = time.UnixMilli(20_000)
	}
	first := telemetry.FindTraces(TraceQuery{})
	if len(first.Traces) != 20 || first.Cursor == "" {
		t.Fatalf("first page = %d traces, cursor %q", len(first.Traces), first.Cursor)
	}
	rest := telemetry.FindTraces(TraceQuery{Cursor: first.Cursor})
	if len(rest.Traces) != 5 || rest.Cursor != "" {
		t.Fatalf("second page = %d traces, cursor %q", len(rest.Traces), rest.Cursor)
	}
	all := telemetry.FindTraces(TraceQuery{Limit: 100}).Traces
	if len(all) != 25 {
		t.Fatalf("all = %d traces", len(all))
	}
	for i := range all {
		if len(all[i].Spans) != 2 {
			t.Fatalf("trace %d spans = %+v", i, all[i].Spans)
		}
	}
	var ids []string
	cursor := ""
	for {
		page := telemetry.FindTraces(TraceQuery{Name: "root", Limit: 3, Cursor: cursor})
		for _, trace := range page.Traces {
			ids = append(ids, trace.TraceID)
		}
		if page.Cursor == "" {
			break
		}
		cursor = page.Cursor
	}
	if len(ids) != len(all) {
		t.Fatalf("paged ids = %d, want %d", len(ids), len(all))
	}
	for i := range ids {
		if ids[i] != all[i].TraceID {
			t.Fatalf("paged id[%d] = %s, want %s", i, ids[i], all[i].TraceID)
		}
	}
}

func TestStoreInheritsRemoteTraceIDsAndCrossTraceLinks(t *testing.T) {
	clock := &mockClock{wall: time.UnixMilli(20_000)}
	clock.install(t)
	sender := newStoreFixture()
	receiver := newStoreFixture()
	var carrier TelemetryCarrier
	_, err := sender.StartSpan(SpanOptions{Name: "sender"}, func(span TelemetrySpan) (any, error) {
		carrier = sender.Inject(span)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sent := sender.FindTraces(TraceQuery{}).Traces[0]
	remote := receiver.Extract(copyTestCarrierValue(carrier))
	if _, err := remote.StartSpan(SpanOptions{Name: "receiver"}, func(TelemetrySpan) (any, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = receiver.StartSpan(SpanOptions{
		Name:  "linked",
		Links: []SpanLink{{Context: remote, Attributes: SpanAttributes{"reason": "background"}}},
	}, func(TelemetrySpan) (any, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	continued, ok := receiver.GetTrace(sent.TraceID)
	if !ok || len(continued.Spans) != 1 {
		t.Fatalf("continued = %+v", continued)
	}
	receiverSpan := continued.Spans[0]
	if receiverSpan.TraceID != sent.TraceID || receiverSpan.ParentID != sent.Spans[0].ID || receiverSpan.Name != "receiver" {
		t.Fatalf("receiver span = %+v", receiverSpan)
	}
	if receiverSpan.ID == sent.Spans[0].ID {
		t.Fatal("receiver reused the sender span id")
	}
	linked := receiver.FindTraces(TraceQuery{Name: "linked"}).Traces[0]
	if linked.TraceID == sent.TraceID {
		t.Fatal("linked trace joined the sender trace")
	}
	if len(linked.Spans[0].Links) != 1 {
		t.Fatalf("links = %+v", linked.Spans[0].Links)
	}
	link := linked.Spans[0].Links[0]
	if link.TraceID != sent.TraceID || link.SpanID != sent.Spans[0].ID || link.Attributes["reason"] != "background" {
		t.Fatalf("link = %+v", link)
	}
	// Incomplete and non-string carriers fall back to recorded roots.
	fallbacks := 0
	for key := range carrier {
		incomplete := copyTestCarrierValue(carrier)
		delete(incomplete, key)
		if _, err := receiver.Extract(incomplete).StartSpan(SpanOptions{Name: "fallback"}, func(TelemetrySpan) (any, error) {
			return nil, nil
		}); err != nil {
			t.Fatal(err)
		}
		fallbacks++
		// Non-string identity values cannot be expressed in TelemetryCarrier
		// (map[string]string); the equivalent TS case feeds numbers. The
		// string-typed carrier makes every present key valid, so only the
		// missing-key half applies.
	}
	traces := receiver.FindTraces(TraceQuery{Name: "fallback"}).Traces
	if len(traces) != fallbacks {
		t.Fatalf("fallbacks = %d, want %d", len(traces), fallbacks)
	}
	for _, trace := range traces {
		if trace.Spans[0].ParentID != "" || trace.TraceID != trace.Spans[0].ID {
			t.Fatalf("fallback = %+v", trace)
		}
	}
}

func TestStoreAcceptsExternalStringIdentities(t *testing.T) {
	telemetry := newStoreFixture()
	var carrier TelemetryCarrier
	_, err := telemetry.StartSpan(SpanOptions{Name: "source"}, func(span TelemetrySpan) (any, error) {
		carrier = telemetry.Inject(span)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	external := TelemetryCarrier{}
	for key := range carrier {
		external[key] = "external-" + key
	}
	parent := telemetry.Extract(external)
	if injected := telemetry.Inject(parent); !carriersEqualValue(injected, external) {
		t.Fatalf("inject(parent) = %+v, want %+v", injected, external)
	}
	if _, err := parent.StartSpan(SpanOptions{Name: "continued"}, func(TelemetrySpan) (any, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	trace := telemetry.FindTraces(TraceQuery{Name: "continued"}).Traces[0]
	if !strings.HasPrefix(trace.TraceID, "external-") {
		t.Fatalf("traceId = %q", trace.TraceID)
	}
	if !strings.HasPrefix(trace.Spans[0].ParentID, "external-") {
		t.Fatalf("parentId = %q", trace.Spans[0].ParentID)
	}
}

func TestStoreQueriesMetricNamesBoundsAndTypedAttributes(t *testing.T) {
	clock := &mockClock{wall: time.UnixMilli(20_001)}
	clock.install(t)
	telemetry := newStoreFixture()
	counter := telemetry.Counter("hits", &InstrumentOptions{Unit: "By"})
	counter.Add(2, SpanAttributes{"method": "read", "valid": true})
	counter.Add(3, SpanAttributes{"valid": true, "method": "read"})
	counter.Add(11, SpanAttributes{"method": "write", "valid": float64(1)})
	telemetry.Counter("other", nil).Add(100, SpanAttributes{"method": "read", "valid": true})
	clock.wall = time.UnixMilli(30_001)
	counter.Add(7, SpanAttributes{"method": "read", "valid": true})
	query := MetricQuery{
		Name:       "hits",
		StartMin:   20_000,
		StartMax:   20_000,
		Attributes: map[string]any{"method": "read", "valid": true},
	}
	rows := telemetry.FindMetrics(query)
	count, sum := 0, float64(0)
	min, max := rows[0].Min, rows[0].Min
	for _, row := range rows {
		count += row.Count
		sum += row.Sum
		if row.Min < min {
			min = row.Min
		}
		if row.Max > max {
			max = row.Max
		}
		if row.Kind != "counter" || row.Unit != "By" || row.IntervalMs != 20_000 {
			t.Fatalf("row = %+v", row)
		}
	}
	if count != 2 || sum != 5 || min != 2 || max != 3 {
		t.Fatalf("aggregate = count %d sum %f min %f max %f", count, sum, min, max)
	}
	rows[0].Attributes["method"] = "changed"
	if telemetry.FindMetrics(query)[0].Attributes["method"] != "read" {
		t.Fatal("rows not detached")
	}
	if rows := telemetry.FindMetrics(MetricQuery{Name: "missing"}); len(rows) != 0 {
		t.Fatalf("missing rows = %+v", rows)
	}
	sum = 0
	for _, row := range telemetry.FindMetrics(MetricQuery{Name: "hits", Attributes: map[string]any{"valid": float64(1)}}) {
		sum += row.Sum
	}
	if sum != 11 {
		t.Fatalf("typed mismatch: sum = %f", sum)
	}
	sum = 0
	for _, row := range telemetry.FindMetrics(MetricQuery{Name: "hits", StartMin: 30_000}) {
		sum += row.Sum
	}
	if sum != 7 {
		t.Fatalf("later rows sum = %f", sum)
	}
}

func TestStoreCursorEncodesStartedMsAndTraceID(t *testing.T) {
	telemetry := newStoreFixture()
	mustStart(t, telemetry, SpanOptions{Name: "root"}, func(TelemetrySpan) error { return nil })
	page := telemetry.FindTraces(TraceQuery{})
	if page.Cursor != "" {
		t.Fatalf("cursor = %q", page.Cursor)
	}
	var decoded []any
	if err := json.Unmarshal([]byte(page.Cursor), &decoded); err == nil {
		t.Fatalf("empty cursor decoded to %v", decoded)
	}
}

func mustStart(t *testing.T, ctx TelemetryContext, options SpanOptions, fn func(TelemetrySpan) error) {
	t.Helper()
	_, err := ctx.StartSpan(options, func(span TelemetrySpan) (any, error) {
		return nil, fn(span)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func startChild(parent TelemetrySpan, options SpanOptions, callbacks ...func(TelemetrySpan)) error {
	_, err := parent.StartSpan(options, func(span TelemetrySpan) (any, error) {
		for _, callback := range callbacks {
			callback(span)
		}
		return nil, nil
	})
	return err
}

func copyTestCarrierValue(carrier TelemetryCarrier) TelemetryCarrier {
	out := TelemetryCarrier{}
	for key, value := range carrier {
		out[key] = value
	}
	return out
}

func carriersEqualValue(left, right TelemetryCarrier) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
