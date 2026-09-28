package harness

// Ports of events.ts delivery semantics (adapted from harness watch tests)
// and telemetry schema tests.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/telemetry"
)

func eventOf(typ string, pairs ...any) HarnessEvent {
	event := jsonx.NewObj()
	event.Set("type", typ)
	for i := 0; i+1 < len(pairs); i += 2 {
		event.Set(pairs[i].(string), pairs[i+1])
	}
	return event
}

func TestEventBusDeliveryAndClone(t *testing.T) {
	bus := NewHarnessEventBus()
	var received []HarnessEvent
	unsub := bus.On("run_start", func(event HarnessEvent, _ Context) {
		// Mutate the received clone; the source must stay intact.
		event.Set("mutated", true)
		received = append(received, event)
	})
	source := eventOf("run_start", "lane", "main")
	bus.Emit(source, BackgroundContext)
	if len(received) != 1 {
		t.Fatalf("received = %d", len(received))
	}
	if _, mutated := source.Get("mutated"); mutated {
		t.Fatal("recipient mutation leaked into the source event")
	}
	unsub()
	// Unsubscribed listeners get nothing; unsubscribe is idempotent.
	unsub()
	bus.Emit(eventOf("run_start"), BackgroundContext)
	if len(received) != 1 {
		t.Fatalf("received after unsubscribe = %d", len(received))
	}
}

func TestEventBusHandlerErrorIsolation(t *testing.T) {
	bus := NewHarnessEventBus()
	var handlerErrors []HarnessEvent
	bus.On("handler_error", func(event HarnessEvent, _ Context) {
		handlerErrors = append(handlerErrors, event)
	})
	bus.On("run_start", func(event HarnessEvent, _ Context) {
		panic("listener exploded")
	})
	lane := "left"
	// Must not propagate the panic.
	bus.Emit(eventOf("run_start", "lane", lane), BackgroundContext)
	if len(handlerErrors) != 1 {
		t.Fatalf("handlerErrors = %d", len(handlerErrors))
	}
	errEvent := handlerErrors[0]
	if EventType(errEvent) != "handler_error" {
		t.Fatalf("type = %s", EventType(errEvent))
	}
	if msg, _ := errEvent.Get("error"); msg != "listener exploded" {
		t.Fatalf("error = %v", msg)
	}
	if got, _ := EventLane(errEvent); got != "left" {
		t.Fatalf("lane = %q", got)
	}
	// handler_error listener failures never re-enter.
	bus.On("handler_error", func(HarnessEvent, Context) {
		panic("double fault")
	})
	bus.Emit(eventOf("run_start"), BackgroundContext)
}

func TestEventBusBatchContiguous(t *testing.T) {
	bus := NewHarnessEventBus()
	var order []string
	bus.On("a", func(e HarnessEvent, _ Context) { order = append(order, "a") })
	// A listener registered mid-batch (by an earlier event in the same
	// batch) must not receive later events in that batch.
	bus.On("first", func(e HarnessEvent, _ Context) {
		order = append(order, "first")
		bus.On("second", func(HarnessEvent, Context) {
			order = append(order, "second:late")
		})
	})
	bus.On("second", func(HarnessEvent, Context) { order = append(order, "second") })
	bus.EmitBatch([]HarnessEvent{eventOf("first"), eventOf("second")}, BackgroundContext)
	want := "first,second"
	if strings.Join(order, ",") != want {
		t.Fatalf("order = %v", order)
	}
}

func TestEventBusClose(t *testing.T) {
	bus := NewHarnessEventBus()
	closeErr := ToError("closed")
	bus.Close(closeErr)
	// Further emits are no-ops; On panics.
	bus.Emit(eventOf("run_start"), BackgroundContext)
	defer func() {
		if recover() == nil {
			t.Fatal("On after close did not panic")
		}
	}()
	bus.On("run_start", func(HarnessEvent, Context) {})
}

func newTestWatcher() *bufferedEventWatcher {
	w := &bufferedEventWatcher{state: "buffering"}
	w.onError = func(error, HarnessEvent, Context) {}
	return w
}

func TestBufferedWatcherLifecycle(t *testing.T) {
	w := newTestWatcher()
	var got []string
	w.Push(eventOf("a"), BackgroundContext)
	w.Push(eventOf("b"), BackgroundContext)
	// Buffered until Start.
	w.Start(func(e HarnessEvent, _ Context) { got = append(got, EventType(e)) })
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("got = %v", got)
	}
	// After start, pushes go straight to the listener.
	w.Push(eventOf("c"), BackgroundContext)
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("got = %v", got)
	}
	w.Unsubscribe()
	w.Push(eventOf("d"), BackgroundContext)
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("after unsubscribe got = %v", got)
	}
}

func TestBufferedWatcherStartOnce(t *testing.T) {
	w := newTestWatcher()
	w.Start(func(HarnessEvent, Context) {})
	defer func() {
		if recover() == nil {
			t.Fatal("second start accepted")
		}
	}()
	w.Start(func(HarnessEvent, Context) {})
}

func TestBufferedWatcherResnapshotDropHold(t *testing.T) {
	w := newTestWatcher()
	var got []string
	w.Start(func(e HarnessEvent, _ Context) { got = append(got, EventType(e)) })
	// Resnapshot: events arriving before the boundary drop; after it, they
	// hold and replay after the snapshot swap.
	w.resnapshotCallback = func(ctx Context) (any, error) {
		w.Push(eventOf("before-boundary"), ctx) // dropping phase
		w.MarkResnapshotBoundary()
		w.Push(eventOf("after-boundary"), ctx) // holding phase
		return "new-snapshot", nil
	}
	snapshot, err := w.Resnapshot(BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot != "new-snapshot" {
		t.Fatalf("snapshot = %v", snapshot)
	}
	// before-boundary dropped, after-boundary replayed.
	if strings.Join(got, ",") != "after-boundary" {
		t.Fatalf("got = %v", got)
	}
	// New events flow again.
	w.Push(eventOf("fresh"), BackgroundContext)
	if strings.Join(got, ",") != "after-boundary,fresh" {
		t.Fatalf("got = %v", got)
	}
}

func TestTelemetrySchemas(t *testing.T) {
	if AITelemetrySchema.Version != 1 {
		t.Fatal("ai schema version")
	}
	if _, ok := AITelemetrySchema.Spans[AiSpanRequest]; !ok {
		t.Fatal("missing pi.ai.request span")
	}
	if _, ok := HarnessTelemetrySchema.Spans[HarnessSpanRun]; !ok {
		t.Fatal("missing pi.harness.run span")
	}
	if _, ok := HarnessTelemetrySchema.Spans[SessionSpanWrite]; !ok {
		t.Fatal("missing pi.session.write span")
	}
	if len(AgentTelemetrySchemas) != 2 {
		t.Fatal("AGENT_TELEMETRY_SCHEMAS length")
	}
	// Schemas are JSON-serializable.
	if _, err := jsonxStringifyForTest(AgentTelemetrySchemas); err != nil {
		t.Fatal(err)
	}
	// Required start attributes are marked.
	if !HarnessTelemetrySchema.Spans[HarnessSpanRun].StartAttributes["pi.session.id"].Required {
		t.Fatal("pi.session.id not required")
	}
}

func jsonxStringifyForTest(v any) (string, error) {
	data, err := jsonMarshalForTest(v)
	return string(data), err
}

func TestStartAiSpanBindsContext(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	ctx := WithTelemetryContext(recorder, BackgroundContext)
	result, err := StartAiSpan(AiSpanRequest, telemetry.SpanAttributes{
		"pi.ai.operation": "stream",
		"pi.ai.provider":  "faux",
		"pi.ai.model":     "faux-1",
		"pi.ai.api":       "faux",
		"pi.ai.streaming": true,
	}, ctx, func(span telemetry.TelemetrySpan, ctx Context) (string, error) {
		// The derived context carries the span as telemetry parent.
		if GetTelemetryContext(ctx) == telemetry.NOOP_TELEMETRY_CONTEXT {
			t.Fatal("span context did not propagate")
		}
		return "done", nil
	})
	if err != nil || result != "done" {
		t.Fatalf("result = %v err = %v", result, err)
	}
	spans := recorder.GetSpans()
	if len(spans) != 1 || spans[0].Name != AiSpanRequest {
		t.Fatalf("spans = %+v", spans)
	}
}
