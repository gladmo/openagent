package runtime

// retry.go ports harness/runtime/drive/retry.ts: retry backoff scheduling
// with abort-aware waiting.

import (
	"fmt"
	"math"
	"time"

	"github.com/gladmo/openagent/ai"
)

// RetryNotBefore mirrors retryNotBefore: now + retryDelayMs, clamped to
// MAX_SAFE_INTEGER.
func RetryNotBefore(policy ai.RetryPolicy, attempt int64, now float64) float64 {
	sum := now + ai.RetryDelayMs(policy, int(attempt))
	if sum > 9007199254740991 || sum != math.Floor(sum) {
		return 9007199254740991
	}
	return sum
}

// WaitUntil blocks until notBefore (Unix ms) elapses or the abort signal
// fires. Returns the abort reason on abort.
func WaitUntil(notBefore float64, signal *SignalAlias) error {
	if signal != nil && signal.Aborted() {
		return abortReasonOf(signal)
	}
	for {
		remaining := notBefore - float64(time.Now().UnixMilli())
		if remaining <= 0 {
			return nil
		}
		if signal != nil {
			// Poll in small slices so an abort lands promptly; the TS uses
			// an event listener + capped timer, the Go port polls at the
			// same granularity as its timer cap.
			slice := remaining
			if slice > 250 {
				slice = 250
			}
			aborted := waitForOrAbort(time.Duration(slice)*time.Millisecond, signal)
			if aborted {
				return abortReasonOf(signal)
			}
			continue
		}
		time.Sleep(time.Duration(remaining) * time.Millisecond)
		return nil
	}
}

func waitForOrAbort(d time.Duration, signal *SignalAlias) bool {
	if signal == nil {
		time.Sleep(d)
		return false
	}
	done := make(chan struct{})
	remove := signal.OnAbort(func() { close(done) })
	defer remove()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func abortReasonOf(signal *SignalAlias) error {
	return fmt.Errorf("aborted")
}
