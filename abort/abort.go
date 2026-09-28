// Package abort provides AbortSignal/AbortController equivalents for the
// WHATWG AbortController semantics the TypeScript sources rely on.
//
// Mapping contract (TS -> Go):
//   - `AbortSignal` (platform object)   -> *abort.Signal (nil == undefined signal)
//   - `AbortController`                 -> *abort.Controller
//   - `controller.abort()`              -> ctrl.Abort() with default reason
//     &abort.Error{Message: "This operation was aborted"} (Node behavior)
//   - `controller.abort(reason)`        -> ctrl.AbortReason(reason)
//   - `AbortSignal.any([a, b])`         -> abort.Any(a, b)
//   - `signal.addEventListener("abort", fn, {once:true})` -> sig.OnAbort(fn)
//
// A nil *Signal is valid and behaves as a signal that never aborts, mirroring
// TS call sites that accept `signal?: AbortSignal | undefined`.
package abort

import (
	"context"
	"fmt"
	"sync"
)

// DefaultAbortMessage is the message Node.js uses when abort() is called
// without a reason.
const DefaultAbortMessage = "This operation was aborted"

// Error mirrors a DOMException with name "AbortError". JS DOMException
// stringifies to just its message.
type Error struct {
	Message string
}

func (e *Error) Error() string { return e.Message }

// Name mirrors DOMException.name.
func (e *Error) Name() string { return "AbortError" }

// NewAbortError returns an *Error with the given message.
func NewAbortError(message string) *Error { return &Error{Message: message} }

type listenerEntry struct {
	id int64
	fn func()
}

// Signal is the Go equivalent of a WHATWG AbortSignal.
type Signal struct {
	mu             sync.Mutex
	aborted        bool
	reason         any
	listeners      []listenerEntry
	nextListenerID int64
	done           chan struct{}
}

// Aborted reports whether the signal has aborted. Nil signals never abort.
func (s *Signal) Aborted() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aborted
}

// Reason returns the abort reason (any value, as in JS). Returns nil if the
// signal has not aborted.
func (s *Signal) Reason() any {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// ReasonError returns the abort reason coerced to an error: the reason itself
// when it is an error, otherwise an *Error carrying its string form.
func (s *Signal) ReasonError() error {
	if s == nil {
		return nil
	}
	r := s.Reason()
	if r == nil {
		return nil
	}
	if err, ok := r.(error); ok {
		return err
	}
	return &Error{Message: fmt.Sprint(r)}
}

// Done returns a channel that is closed exactly once, when the signal aborts.
// The nil signal returns a channel that is never closed; it is still safe to
// select on.
func (s *Signal) Done() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.done
}

// OnAbort registers fn to run when the signal aborts and returns a remove
// function. Listeners run synchronously in registration order inside Abort().
// As in JS, a listener registered on an already-aborted signal never runs.
// The returned remove function is idempotent.
func (s *Signal) OnAbort(fn func()) (remove func()) {
	if s == nil {
		return func() {}
	}
	s.mu.Lock()
	s.nextListenerID++
	id := s.nextListenerID
	s.listeners = append(s.listeners, listenerEntry{id: id, fn: fn})
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, l := range s.listeners {
			if l.id == id {
				s.listeners = append(s.listeners[:i], s.listeners[i+1:]...)
				return
			}
		}
	}
}

// ThrowIfAborted mirrors signal.throwIfAborted(): returns the reason as an
// error when aborted, nil otherwise.
func (s *Signal) ThrowIfAborted() error {
	if !s.Aborted() {
		return nil
	}
	return s.ReasonError()
}

// Controller is the Go equivalent of a WHATWG AbortController.
type Controller struct {
	signal *Signal
}

// NewController creates a controller whose signal is not aborted.
func NewController() *Controller {
	return &Controller{signal: &Signal{done: make(chan struct{})}}
}

// Signal returns the controller's signal.
func (c *Controller) Signal() *Signal { return c.signal }

// Abort aborts the signal with the default reason (matching controller.abort()
// in Node).
func (c *Controller) Abort() { c.AbortReason(NewAbortError(DefaultAbortMessage)) }

// AbortReason aborts the signal with an explicit reason, matching
// controller.abort(reason).
func (c *Controller) AbortReason(reason any) {
	s := c.signal
	s.mu.Lock()
	if s.aborted {
		s.mu.Unlock()
		return
	}
	s.aborted = true
	s.reason = reason
	listeners := s.listeners
	s.listeners = nil
	close(s.done)
	s.mu.Unlock()
	for _, l := range listeners {
		l.fn()
	}
}

// NewAbortedSignal returns a signal that is already aborted with reason.
func NewAbortedSignal(reason any) *Signal {
	c := NewController()
	c.AbortReason(reason)
	return c.Signal()
}

// Any returns a signal that aborts when any of the given signals aborts, with
// the reason of the first source to abort (matching AbortSignal.any). Nil
// signals are ignored. Any() with no effective sources never aborts.
func Any(signals ...*Signal) *Signal {
	live := make([]*Signal, 0, len(signals))
	for _, s := range signals {
		if s != nil {
			live = append(live, s)
		}
	}
	for _, s := range live {
		if s.Aborted() {
			return NewAbortedSignal(s.Reason())
		}
	}
	derived := &Signal{done: make(chan struct{})}
	derivedCtrl := NewController()
	derivedCtrl.signal = derived
	for _, src := range live {
		src.OnAbort(func() {
			derivedCtrl.AbortReason(src.Reason())
		})
	}
	return derived
}

// FromGoContext returns a signal that aborts with the context's error when the
// context is done. A nil context yields a never-aborting signal.
func FromGoContext(ctx context.Context) *Signal {
	if ctx == nil {
		return nil
	}
	sig := &Signal{done: make(chan struct{})}
	ctrl := NewController()
	ctrl.signal = sig
	if ctx.Err() != nil {
		ctrl.AbortReason(ctx.Err())
		return sig
	}
	go func() {
		<-ctx.Done()
		ctrl.AbortReason(ctx.Err())
	}()
	return sig
}

// ToGoContext returns a context.Context that is canceled when the signal
// aborts (or when the returned cancel function is called). A nil signal yields
// a context derived only from parent.
func ToGoContext(parent context.Context, s *Signal) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if s == nil {
		return ctx, cancel
	}
	remove := s.OnAbort(func() { cancel() })
	return ctx, func() {
		remove()
		cancel()
	}
}
