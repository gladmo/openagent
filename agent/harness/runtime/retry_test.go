package runtime

// Ports of drive/retry.ts behaviors.

import (
	"math"
	"testing"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/ai"
)

func TestRetryNotBefore(t *testing.T) {
	policy := ai.RetryPolicy{Enabled: true, MaxRetries: 5, BaseDelayMs: 1000}
	// Attempt 1 backoff: base * 2^0 = 1000ms.
	notBefore := RetryNotBefore(policy, 1, 10000)
	if notBefore != 11000 {
		t.Fatalf("notBefore = %v", notBefore)
	}
	// Clamp to MAX_SAFE_INTEGER when the sum overflows.
	huge := RetryNotBefore(policy, 1, 9.007199254740991e15)
	if huge != 9007199254740991 {
		t.Fatalf("huge = %v", huge)
	}
	_ = math.MaxInt64
}

func TestWaitUntilElapses(t *testing.T) {
	notBefore := float64(time.Now().UnixMilli()) + 120
	start := time.Now()
	if err := WaitUntil(notBefore, nil); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("returned early")
	}
}

func TestWaitUntilAlreadyDue(t *testing.T) {
	past := float64(time.Now().UnixMilli()) - 1000
	if err := WaitUntil(past, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWaitUntilAborts(t *testing.T) {
	controller := abort.NewController()
	notBefore := float64(time.Now().UnixMilli()) + 5000
	go func() {
		time.Sleep(80 * time.Millisecond)
		controller.Abort()
	}()
	start := time.Now()
	err := WaitUntil(notBefore, controller.Signal())
	if err == nil {
		t.Fatal("abort ignored")
	}
	if time.Since(start) > time.Second {
		t.Fatal("abort not prompt")
	}
}

func TestWaitUntilPreAborted(t *testing.T) {
	controller := abort.NewController()
	controller.Abort()
	if err := WaitUntil(float64(time.Now().UnixMilli())+10000, controller.Signal()); err == nil {
		t.Fatal("pre-aborted ignored")
	}
}
