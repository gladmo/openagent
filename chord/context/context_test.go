package context

import (
	"errors"
	"testing"
	"time"

	"github.com/gladmo/openagent/abort"
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
