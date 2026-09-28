package abort

// any_race_test.go pins the derived-signal atomicity: an abort racing Any()
// registration always lands, an already-aborted source fires immediately,
// and anyWithDispose removes its registrations.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAnyAlreadyAbortedSourceFires(t *testing.T) {
	source := NewController()
	source.AbortReason("gone")
	derived := Any(source.Signal())
	if !derived.Aborted() {
		t.Fatal("derived did not abort for an already-aborted source")
	}
}

// The derived signal must observe every source abort, no matter how the
// abort interleaves with the registration loop. Run under -race.
func TestAnyNeverLosesAbort(t *testing.T) {
	const iterations = 2000
	var missed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		source := NewController()
		wg.Add(2)
		go func() {
			defer wg.Done()
			derived := Any(source.Signal())
			<-derived.Done()
			if !derived.Aborted() {
				missed.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			source.Abort()
		}()
	}
	wg.Wait()
	if got := missed.Load(); got != 0 {
		t.Fatalf("%d/%d derived signals never aborted", got, iterations)
	}
}

func TestAnyWithDisposeRemovesRegistrations(t *testing.T) {
	source := NewController()
	_, dispose := AnyWithDispose(source.Signal())
	dispose()
	// Idempotent.
	dispose()
	// After disposal the derived signal no longer reacts; the source has
	// no leftover listeners (observed via abort running cleanly with none).
	other := NewController()
	derived2, dispose2 := AnyWithDispose(source.Signal(), other.Signal())
	dispose2()
	source.Abort()
	if derived2.Aborted() {
		t.Fatal("disposed derived signal still aborted")
	}
}

func TestOnAbortAfterAbortDoesNotRetain(t *testing.T) {
	source := NewController()
	source.Abort()
	ran := false
	remove := source.Signal().OnAbort(func() { ran = true })
	remove()
	if ran {
		t.Fatal("listener ran on an already-aborted signal")
	}
}

func TestToGoContextWithAbortedSignalCancels(t *testing.T) {
	source := NewController()
	source.Abort()
	ctx, cancel := ToGoContext(context.Background(), source.Signal())
	defer cancel()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("context not canceled for an already-aborted signal")
	}
}
