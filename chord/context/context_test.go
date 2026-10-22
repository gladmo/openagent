package context

import (
	"errors"
	"testing"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/telemetry"
)

// Port of pi/packages/chord/test/context.test.ts.

func TestDistinctEmptyRootContexts(t *testing.T) {
	key := CreateContextKey[string]("value")
	if TodoContext == BackgroundContext {
		t.Fatal("TODO == BACKGROUND")
	}
	if TodoContext.AbortSignal() != nil {
		t.Fatal("TODO has a signal")
	}
	if v, ok := Value(TodoContext, key); ok || v != "" {
		t.Fatal("TODO has a value")
	}
	if BackgroundContext.String() != "[Context BACKGROUND_CONTEXT]" {
		t.Fatalf("%s", BackgroundContext.String())
	}
	if TodoContext.String() != "[Context TODO_CONTEXT]" {
		t.Fatalf("%s", TodoContext.String())
	}
}

func TestLayersTypedValuesWithoutModifyingParents(t *testing.T) {
	firstKey := CreateContextKey[string]("first")
	secondKey := CreateContextKey[int]("second")
	first := WithContextValue(firstKey, "one", BackgroundContext)
	second := WithContextValue(secondKey, 2, first)
	replaced := WithContextValue(firstKey, "updated", second)

	if _, ok := Value(BackgroundContext, firstKey); ok {
		t.Fatal("background inherited value")
	}
	if v, _ := Value(first, firstKey); v != "one" {
		t.Fatalf("first = %q", v)
	}
	if _, ok := Value(first, secondKey); ok {
		t.Fatal("first leaked second")
	}
	if v, _ := Value(second, firstKey); v != "one" {
		t.Fatal("second lost first")
	}
	if v, _ := Value(second, secondKey); v != 2 {
		t.Fatal("second lost its own value")
	}
	if v, _ := Value(replaced, firstKey); v != "updated" {
		t.Fatal("replacement failed")
	}
	if v, _ := Value(second, firstKey); v != "one" {
		t.Fatal("parent modified by child")
	}
	want := "[Context BACKGROUND_CONTEXT].WithValue(first).WithValue(second).WithValue(first)"
	if replaced.String() != want {
		t.Fatalf("String() = %s", replaced.String())
	}
}

func TestInheritsParentCancellationAndIsolatesChild(t *testing.T) {
	parentController := abort.NewController()
	parent := WithAbortSignal(parentController.Signal(), BackgroundContext)
	child := WithCancel(parent)
	sibling := WithCancel(parent)
	childListenerCalls := 0
	child.Context.AbortSignal().OnAbort(func() { childListenerCalls++ })

	child.Cancel("child")
	if !child.Context.AbortSignal().Aborted() {
		t.Fatal("child not aborted")
	}
	if r := child.Context.AbortSignal().Reason(); r != "child" {
		t.Fatalf("child reason = %v", r)
	}
	if sibling.Context.AbortSignal().Aborted() {
		t.Fatal("sibling aborted by child cancel")
	}
	if parent.AbortSignal().Aborted() {
		t.Fatal("parent aborted by child cancel")
	}
	if childListenerCalls != 1 {
		t.Fatalf("listener calls = %d", childListenerCalls)
	}

	parentController.AbortReason("parent")
	if !sibling.Context.AbortSignal().Aborted() {
		t.Fatal("sibling not aborted by parent")
	}
	if r := sibling.Context.AbortSignal().Reason(); r != "parent" {
		t.Fatalf("sibling reason = %v", r)
	}
}

func TestMasksCallerCancellationForCleanup(t *testing.T) {
	controller := abort.NewController()
	key := CreateContextKey[string]("value")
	context := WithContextValue(key, "preserved", WithAbortSignal(controller.Signal(), BackgroundContext))
	cleanup := WithoutAbortSignal(context)

	controller.Abort()
	if !context.AbortSignal().Aborted() {
		t.Fatal("context not aborted")
	}
	if cleanup.AbortSignal() != nil {
		t.Fatal("cleanup signal not masked")
	}
	if v, _ := Value(cleanup, key); v != "preserved" {
		t.Fatal("cleanup lost value")
	}
}

func TestStopsWaitingWhenCancelled(t *testing.T) {
	controller := abort.NewController()
	context := WithAbortSignal(controller.Signal(), BackgroundContext)
	workDone := make(chan struct{})
	waiting := make(chan error, 1)
	go func() {
		_, err := AwaitWithContext(context, func() (string, error) {
			<-workDone
			return "completed later", nil
		})
		waiting <- err
	}()
	cancellation := errors.New("cancelled")
	controller.AbortReason(cancellation)
	select {
	case err := <-waiting:
		if !errors.Is(err, cancellation) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not released by cancellation")
	}
	// The underlying operation continues and settles later.
	close(workDone)
	if v, err := AwaitWithContext(BackgroundContext, func() (string, error) {
		return "completed", nil
	}); err != nil || v != "completed" {
		t.Fatalf("background wait = %q, %v", v, err)
	}
}

func TestDerivesTelemetryIndependentlyFromValuesAndCancellation(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	other := telemetry.NewInMemoryTelemetryContext()
	controller := abort.NewController()
	_, err := recorder.StartSpan(telemetry.SpanOptions{Name: "caller"}, func(parent telemetry.TelemetrySpan) (any, error) {
		return other.StartSpan(telemetry.SpanOptions{Name: "other"}, func(otherSpan telemetry.TelemetrySpan) (any, error) {
			key := CreateContextKey[string]("principal")
			base := WithContextValue(key, "owner", WithAbortSignal(controller.Signal(), BackgroundContext))
			ctx := WithTelemetry(parent, base)
			replaced := WithTelemetry(otherSpan, ctx)
			if base.Telemetry() != telemetry.NOOP_TELEMETRY_CONTEXT {
				t.Fatal("base telemetry is not NOOP")
			}
			if TodoContext.Telemetry() != telemetry.NOOP_TELEMETRY_CONTEXT {
				t.Fatal("TODO telemetry is not NOOP")
			}
			if ctx.Telemetry() != parent {
				t.Fatal("context telemetry not derived")
			}
			if replaced.Telemetry() != otherSpan {
				t.Fatal("replaced telemetry not derived")
			}
			if v, _ := Value(replaced, key); v != "owner" {
				t.Fatalf("value = %q", v)
			}
			if replaced.AbortSignal() != base.AbortSignal() {
				t.Fatal("telemetry changed cancellation")
			}
			if WithoutAbortSignal(ctx).Telemetry() != parent {
				t.Fatal("cleanup context lost telemetry")
			}
			return traceOuter(t, ctx, key)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	spans := recorder.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("spans = %+v", spans)
	}
	caller, outer, inner := spans[0], spans[1], spans[2]
	if outer.ParentID == nil || *outer.ParentID != caller.ID {
		t.Fatalf("outer parent = %+v", outer)
	}
	if inner.ParentID == nil || *inner.ParentID != outer.ID {
		t.Fatalf("inner parent = %+v", inner)
	}
	if len(inner.Links) != 1 || !carriersEqual(inner.Links[0].Context, outer.Context) {
		t.Fatalf("links = %+v", inner.Links)
	}
	controller.Abort()
	if !ctxSignalAborted(t, WithAbortSignal(controller.Signal(), BackgroundContext)) {
		t.Fatal("context not aborted")
	}
}

func traceOuter(t *testing.T, ctx Context, key ContextKey[string]) (any, error) {
	t.Helper()
	return TraceIn(ctx, telemetry.SpanOptions{Name: "outer"}, func(child Context) (any, error) {
		if v, _ := Value(child, key); v != "owner" {
			t.Fatalf("child value = %q", v)
		}
		if child.AbortSignal() != ctx.AbortSignal() {
			t.Fatal("child changed cancellation")
		}
		child.Telemetry().AddEvent("started", nil)
		return TraceIn(child, telemetry.SpanOptions{Name: "inner"}, func(nested Context) (any, error) {
			nested.Telemetry().AddLink(telemetry.SpanLink{Context: child.Telemetry()})
			return 42, nil
		})
	})
}

func ctxSignalAborted(t *testing.T, ctx Context) bool {
	t.Helper()
	signal := ctx.AbortSignal()
	return signal != nil && signal.Aborted()
}

func TestIsolatesConcurrentTraceInBranches(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	_, err := recorder.StartSpan(telemetry.SpanOptions{Name: "caller"}, func(parent telemetry.TelemetrySpan) (any, error) {
		return traceBranches(t, WithTelemetry(parent, BackgroundContext))
	})
	if err != nil {
		t.Fatal(err)
	}
	spans := recorder.GetSpans()
	for _, name := range []string{"first", "second"} {
		var parent *telemetry.RecordedTelemetrySpan
		for i := range spans {
			if spans[i].Name == name {
				parent = &spans[i]
			}
		}
		if parent == nil {
			t.Fatalf("missing %s span", name)
		}
		found := false
		for i := range spans {
			if spans[i].Name == name+".child" {
				found = spans[i].ParentID != nil && *spans[i].ParentID == parent.ID
			}
		}
		if !found {
			t.Fatalf("child of %s not recorded: %+v", name, spans)
		}
	}
}

func traceBranches(t *testing.T, ctx Context) (any, error) {
	t.Helper()
	release := make(chan struct{})
	firstDone := make(chan struct{}, 1)
	go func() {
		_, _ = TraceIn(ctx, telemetry.SpanOptions{Name: "first"}, func(child Context) (any, error) {
			<-release
			return TraceIn(child, telemetry.SpanOptions{Name: "first.child"}, func(Context) (any, error) {
				return "first", nil
			})
		})
		firstDone <- struct{}{}
	}()
	second, err := TraceIn(ctx, telemetry.SpanOptions{Name: "second"}, func(child Context) (any, error) {
		result, err := TraceIn(child, telemetry.SpanOptions{Name: "second.child"}, func(Context) (any, error) {
			return "second", nil
		})
		close(release)
		return result, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if second != "second" {
		t.Fatalf("second = %v", second)
	}
	<-firstDone
	return nil, nil
}

func carriersEqual(left, right telemetry.TelemetryCarrier) bool {
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
