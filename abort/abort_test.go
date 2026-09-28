package abort

import (
	"context"
	"testing"
	"time"
)

func TestControllerAbortDefaultReason(t *testing.T) {
	c := NewController()
	s := c.Signal()
	if s.Aborted() {
		t.Fatal("fresh signal aborted")
	}
	if err := s.ThrowIfAborted(); err != nil {
		t.Fatalf("ThrowIfAborted on live signal: %v", err)
	}
	c.Abort()
	if !s.Aborted() {
		t.Fatal("signal not aborted")
	}
	err := s.ReasonError()
	ae, ok := err.(*Error)
	if !ok {
		t.Fatalf("reason type %T", err)
	}
	if ae.Message != DefaultAbortMessage || ae.Name() != "AbortError" {
		t.Fatalf("reason = %+v", ae)
	}
	if got := s.ThrowIfAborted(); got != err {
		t.Fatalf("ThrowIfAborted = %v", got)
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("done not closed after abort")
	}
}

func TestAbortIdempotent(t *testing.T) {
	c := NewController()
	c.AbortReason("first")
	c.AbortReason("second")
	if r := c.Signal().Reason(); r != "first" {
		t.Fatalf("reason overwritten: %v", r)
	}
}

func TestListenerOrderAndRemoval(t *testing.T) {
	c := NewController()
	var order []int
	rm := c.Signal().OnAbort(func() { order = append(order, 1) })
	c.Signal().OnAbort(func() { order = append(order, 2) })
	rm()
	c.Abort()
	if len(order) != 1 || order[0] != 2 {
		t.Fatalf("order = %v", order)
	}
	// Listener added post-abort never fires.
	fired := false
	c.Signal().OnAbort(func() { fired = true })
	if fired {
		t.Fatal("post-abort listener fired")
	}
}

func TestNilSignal(t *testing.T) {
	var s *Signal
	if s.Aborted() || s.Done() != nil {
		t.Fatal("nil signal misbehaves")
	}
	if err := s.ThrowIfAborted(); err != nil {
		t.Fatal(err)
	}
	if r := s.Reason(); r != nil {
		t.Fatal(r)
	}
	s.OnAbort(func() { t.Fatal("nil signal listener fired") })
	Any(s, nil) // must not panic
}

func TestAnyPreAborted(t *testing.T) {
	a := NewAbortedSignal("boom")
	derived := Any(a, nil)
	if !derived.Aborted() || derived.Reason() != "boom" {
		t.Fatalf("derived = %+v", derived)
	}
}

func TestAnyPropagation(t *testing.T) {
	c1 := NewController()
	c2 := NewController()
	derived := Any(c1.Signal(), c2.Signal())
	if derived.Aborted() {
		t.Fatal("derived aborted early")
	}
	c2.AbortReason(NewAbortError("c2"))
	if !derived.Aborted() {
		t.Fatal("derived not aborted")
	}
	if err := derived.ReasonError(); err == nil || err.Error() != "c2" {
		t.Fatalf("reason = %v", derived.ReasonError())
	}
	// Late abort of the other source does not change the derived reason.
	c1.AbortReason("c1")
	if derived.ReasonError().Error() != "c2" {
		t.Fatal("reason changed")
	}
}

func TestFromGoContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sig := FromGoContext(ctx)
	if sig.Aborted() {
		t.Fatal("pre canceled")
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for !sig.Aborted() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !sig.Aborted() {
		t.Fatal("signal not aborted after ctx cancel")
	}
	if err := sig.ReasonError(); err != context.Canceled {
		t.Fatalf("reason = %v", err)
	}
}

func TestToGoContext(t *testing.T) {
	c := NewController()
	ctx, cancel := ToGoContext(context.Background(), c.Signal())
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatal("ctx done early")
	default:
	}
	c.Abort()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("ctx not canceled on abort")
	}
}
