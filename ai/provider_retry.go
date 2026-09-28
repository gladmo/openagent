package ai

// provider_retry.go ports utils/provider-retry.ts: it reproduces the retry
// behavior used by the OpenAI and Anthropic SDKs (x-should-retry,
// retry-after-ms / retry-after, exponential backoff with jitter) while making
// the backoff sleep interruptible by the request abort signal.

import (
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/gladmo/openagent/abort"
)

// DefaultMaxRetryDelayMs caps server-requested retry delays.
const DefaultMaxRetryDelayMs = 60_000

// ProviderHTTPError is the error carrier for provider HTTP failures. It
// plays the role of the SDK error objects in TS (status + headers + body).
type ProviderHTTPError struct {
	Status  int
	Headers map[string]string
	Body    string
	Message string
}

// Error implements error. The raw status is the fallback message; providers
// compose display strings via NormalizeProviderError/FormatProviderError.
func (e *ProviderHTTPError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "provider error: " + strconv.Itoa(e.Status)
}

func (e *ProviderHTTPError) header(name string) string {
	if e.Headers == nil {
		return ""
	}
	for key, value := range e.Headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// IsRetryableProviderError mirrors the pinned OpenAI/Anthropic SDK retry
// policy.
func IsRetryableProviderError(err error) bool {
	providerErr, ok := err.(*ProviderHTTPError)
	if !ok {
		return false
	}
	shouldRetry := providerErr.header("x-should-retry")
	if shouldRetry == "true" {
		return true
	}
	if shouldRetry == "false" {
		return false
	}
	if providerErr.Status == 0 {
		return true
	}
	return providerErr.Status == 408 || providerErr.Status == 409 || providerErr.Status == 429 || providerErr.Status >= 500
}

// ErrRetryDelayTooLarge reports a server-requested delay above the cap; TS
// throws this synchronously out of retryProviderRequest.
type ErrRetryDelayTooLarge struct{ Message string }

// Error implements error.
func (e *ErrRetryDelayTooLarge) Error() string { return e.Message }

func validateServerRetryDelayMs(delayMs, maxRetryDelayMs float64, providerErrorMessage string) (float64, error) {
	if maxRetryDelayMs > 0 && delayMs > maxRetryDelayMs {
		return 0, &ErrRetryDelayTooLarge{
			Message: "Server requested " + strconv.Itoa(int(math.Ceil(delayMs/1000))) +
				"s retry delay (max: " + strconv.Itoa(int(math.Ceil(maxRetryDelayMs/1000))) +
				"s). " + providerErrorMessage,
		}
	}
	return delayMs, nil
}

// GetRetryDelayMs computes the backoff for a failed attempt. The error is
// non-nil when the server-requested delay exceeds the cap.
func GetRetryDelayMs(err error, retryIndex int, maxRetryDelayMs float64) (float64, error) {
	providerErr, ok := err.(*ProviderHTTPError)
	if !ok {
		providerErr = &ProviderHTTPError{}
	}
	if retryAfterMs := providerErr.header("retry-after-ms"); retryAfterMs != "" {
		if value, parseErr := strconv.ParseFloat(retryAfterMs, 64); parseErr == nil {
			return validateServerRetryDelayMs(value, maxRetryDelayMs, providerErr.Error())
		}
	}
	if retryAfter := providerErr.header("retry-after"); retryAfter != "" {
		seconds, parseErr := strconv.ParseFloat(retryAfter, 64)
		var delayMs float64
		if parseErr != nil {
			if parsed, ok := parseHTTPDate(retryAfter); ok {
				delayMs = parsed - float64(time.Now().UnixMilli())
			}
		} else {
			delayMs = seconds * 1000
		}
		return validateServerRetryDelayMs(delayMs, maxRetryDelayMs, providerErr.Error())
	}
	exponentialDelay := math.Min(0.5*math.Pow(2, float64(retryIndex)), 8) * 1000
	return exponentialDelay * (1 - rand.Float64()*0.25), nil
}

func parseHTTPDate(value string) (float64, bool) {
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC822, time.RFC822Z, time.ANSIC} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return float64(parsed.UnixMilli()), true
		}
	}
	return 0, false
}

// ProviderRetryOptions mirrors ProviderRetryOptions.
type ProviderRetryOptions struct {
	MaxRetries      *float64
	MaxRetryDelayMs *float64
	Signal          *abort.Signal
}

// AbortableSleep sleeps for ms milliseconds, returning false when aborted.
func AbortableSleep(ms float64, signal *abort.Signal) bool {
	if ms <= 0 {
		ms = 0
	}
	if signal != nil && signal.Aborted() {
		return false
	}
	timer := time.NewTimer(time.Duration(ms * float64(time.Millisecond)))
	defer timer.Stop()
	if signal == nil {
		<-timer.C
		return true
	}
	select {
	case <-timer.C:
		return true
	case <-signal.Done():
		return false
	}
}

// RetryProviderRequest runs request with SDK-style retries. A
// server-requested delay above MaxRetryDelayMs fails immediately (60 seconds
// by default); set it to zero to disable the limit.
func RetryProviderRequest(request func() error, options *ProviderRetryOptions) error {
	maxRetries := 0.0
	maxRetryDelayMs := float64(DefaultMaxRetryDelayMs)
	var signal *abort.Signal
	if options != nil {
		if options.MaxRetries != nil {
			maxRetries = *options.MaxRetries
		}
		if options.MaxRetryDelayMs != nil {
			maxRetryDelayMs = *options.MaxRetryDelayMs
		}
		signal = options.Signal
	}
	retriesRemaining := maxRetries

	for {
		err := request()
		if err == nil {
			return nil
		}
		if signal != nil && signal.Aborted() {
			return abort.NewAbortError("Request aborted")
		}
		if retriesRemaining <= 0 || !IsRetryableProviderError(err) {
			return err
		}
		retryIndex := maxRetries - retriesRemaining
		retriesRemaining--
		delayMs, delayErr := GetRetryDelayMs(err, int(retryIndex), maxRetryDelayMs)
		if delayErr != nil {
			return delayErr
		}
		if !AbortableSleep(delayMs, signal) {
			return abort.NewAbortError("Request aborted")
		}
	}
}
