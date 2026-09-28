// Package context ports @gladmo/chord/context: an immutable
// singly-linked value chain carrying an optional abort signal.
//
// The chain is ported 1:1 rather than mapped onto context.Context because
// WithoutAbortSignal must be able to MASK a parent signal (store nil at a
// child level), which Go contexts cannot express.
package context

import (
	"github.com/gladmo/openagent/abort"
)

type keyToken struct{ description string }

// ContextKey is a typed key; identity is the token pointer, mirroring the TS
// symbol token. The phantom type parameter carries the TS ContextKey<T>
// typing at compile time.
type ContextKey[T any] struct{ token *keyToken }

// ContextKeyMarker lets heterogeneous keys share one lookup signature; the
// token pointer is the identity, mirroring the TS symbol comparison.
type ContextKeyMarker interface{ keyToken() *keyToken }

func (k ContextKey[T]) keyToken() *keyToken { return k.token }

// Description returns the key description (the TS symbol description).
func (k ContextKey[T]) Description() string {
	if k.token == nil {
		return ""
	}
	return k.token.description
}

// CreateContextKey creates a fresh key with the given description.
func CreateContextKey[T any](description string) ContextKey[T] {
	return ContextKey[T]{token: &keyToken{description: description}}
}

// Context is the immutable value-chain interface.
type Context interface {
	// Value returns the value stored under key (any ContextKey), or nil.
	Value(key ContextKeyMarker) any
	// AbortSignal returns the context's abort signal, or nil.
	AbortSignal() *abort.Signal
	String() string
}

var abortSignalKey = CreateContextKey[*abort.Signal]("chord.abortSignal")

type emptyContext struct{ name string }

func (c *emptyContext) Value(ContextKeyMarker) any { return nil }
func (c *emptyContext) AbortSignal() *abort.Signal {
	return nil // the shared key never exists on an empty context
}
func (c *emptyContext) String() string { return c.name }

type contextValue struct {
	parent Context
	key    ContextKeyMarker
	value  any
}

func (c *contextValue) Value(key ContextKeyMarker) any {
	if sameKey(c.key, key) {
		return c.value
	}
	return c.parent.Value(key)
}

func (c *contextValue) AbortSignal() *abort.Signal {
	if v, ok := c.Value(abortSignalKey).(*abort.Signal); ok {
		return v
	}
	return nil
}

func sameKey(a, b ContextKeyMarker) bool {
	return a.keyToken() == b.keyToken()
}

func (c *contextValue) String() string {
	desc := "anonymous"
	if describer, ok := c.key.(interface{ Description() string }); ok {
		if d := describer.Description(); d != "" {
			desc = d
		}
	}
	return c.parent.String() + ".WithValue(" + desc + ")"
}

// BackgroundContext mirrors BACKGROUND_CONTEXT.
var BackgroundContext Context = &emptyContext{name: "[Context BACKGROUND_CONTEXT]"}

// TodoContext mirrors TODO_CONTEXT.
var TodoContext Context = &emptyContext{name: "[Context TODO_CONTEXT]"}

// Value is the typed lookup helper standing in for the TS generic method
// value<T>(key: ContextKey<T>).
func Value[T any](ctx Context, key ContextKey[T]) (T, bool) {
	v, ok := ctx.Value(key).(T)
	return v, ok
}

// WithContextValue derives a context containing one additional or replaced
// value.
func WithContextValue[T any](key ContextKey[T], value T, parent Context) Context {
	return &contextValue{parent: parent, key: key, value: value}
}

// WithAbortSignal derives a context cancelled by either the parent signal or
// the supplied signal. The parent context remains unchanged.
func WithAbortSignal(signal *abort.Signal, ctx Context) Context {
	parentSignal := ctx.AbortSignal()
	combined := signal
	if parentSignal != nil {
		combined = abort.Any(parentSignal, signal)
	}
	return WithContextValue(abortSignalKey, combined, ctx)
}

// WithoutAbortSignal derives a context retaining all values except caller
// cancellation. Intended for mandatory cleanup only. The nil value stored at
// the abort key masks any parent signal.
func WithoutAbortSignal(ctx Context) Context {
	return WithContextValue(abortSignalKey, (*abort.Signal)(nil), ctx)
}

// CancelContext pairs a derived context with its cancel function.
type CancelContext struct {
	Context Context
	Cancel  func(reason ...any)
}

// WithCancel derives an independently cancellable child context.
func WithCancel(ctx Context) CancelContext {
	controller := abort.NewController()
	return CancelContext{
		Context: WithAbortSignal(controller.Signal(), ctx),
		Cancel: func(reason ...any) {
			if len(reason) > 0 {
				controller.AbortReason(reason[0])
			} else {
				controller.Abort()
			}
		},
	}
}

// AwaitWithContext observes an operation until it settles or the invocation
// is cancelled. Cancellation fails only this waiter; it does not cancel the
// underlying operation. wait runs on the caller's goroutine; pass a function
// that blocks on an already-started operation.
func AwaitWithContext[T any](ctx Context, wait func() (T, error)) (T, error) {
	var zero T
	signal := ctx.AbortSignal()
	if signal == nil {
		return wait()
	}
	if signal.Aborted() {
		return zero, abortError(signal)
	}
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		v, err := wait()
		done <- outcome{v, err}
	}()
	select {
	case r := <-done:
		return r.value, r.err
	case <-signal.Done():
		return zero, abortError(signal)
	}
}

func abortError(signal *abort.Signal) error {
	if err := signal.ReasonError(); err != nil {
		return err
	}
	return abort.NewAbortError("The operation was aborted")
}
