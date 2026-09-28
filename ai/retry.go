package ai

import (
	"math"
	"regexp"
	"strings"

	"github.com/gladmo/openagent/abort"
)

// retry.go ports utils/retry.ts.

func buildProviderErrorPattern(patterns []string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)` + strings.Join(patterns, "|"))
}

var nonRetryableProviderLimitErrorPattern = buildProviderErrorPattern([]string{
	"GoUsageLimitError",
	"FreeUsageLimitError",
	"Monthly usage limit reached",
	"available balance",
	"insufficient_quota",
	"out of budget",
	"quota exceeded",
	"billing",
})

var retryableProviderErrorPattern = buildProviderErrorPattern([]string{
	"overloaded",
	"currently experiencing high demand",
	"rate.?limit",
	"too many requests",
	"429",
	"500",
	"502",
	"503",
	"504",
	"520",
	"524",
	"service.?unavailable",
	"server.?error",
	"internal.?error",
	"provider.?returned.?error",
	"exceeded request buffer limit while retrying upstream",
	"network.?error",
	"connection.?error",
	"connection.?refused",
	"connection.?lost",
	"other side closed",
	"fetch failed",
	"getaddrinfo",
	"ENOTFOUND",
	"EAI_AGAIN",
	"upstream.?connect",
	"reset before headers",
	"socket hang up",
	"socket connection was closed",
	"timed? out",
	"timeout",
	"terminated",
	"websocket.?closed",
	"websocket.?error",
	"ended without",
	"stream ended before message_stop",
	"stream ended before a terminal response event",
	"http2 request did not get a response",
	"retry delay",
	"you can retry your request",
	"try your request again",
	"please retry your request",
	"ResourceExhausted",
})

// RetryPolicy mirrors the TS interface.
type RetryPolicy struct {
	Enabled         bool
	MaxRetries      int
	BaseDelayMs     float64
	MaxAgentDelayMs *float64
}

// DefaultMaxAgentRetryDelayMs mirrors DEFAULT_MAX_AGENT_RETRY_DELAY_MS.
const DefaultMaxAgentRetryDelayMs = 60000.0

// RetryDelayMs computes `baseDelayMs * 2^(attempt-1)` capped by
// maxAgentDelayMs (default 60s).
func RetryDelayMs(policy RetryPolicy, attempt int) float64 {
	delay := policy.BaseDelayMs * math.Pow(2, math.Max(0, float64(attempt-1)))
	if delay > maxSafeInteger {
		delay = maxSafeInteger
	}
	cap := DefaultMaxAgentRetryDelayMs
	if policy.MaxAgentDelayMs != nil {
		cap = *policy.MaxAgentDelayMs
	}
	return math.Min(delay, cap)
}

const maxSafeInteger = float64(9007199254740991)

// RetryCallbacks mirrors the TS interface (callbacks may block; TS awaits
// them sequentially).
type RetryCallbacks interface {
	OnRetryScheduled(attempt int, maxAttempts int, delayMs float64, errorMessage string)
	OnRetryAttemptStart()
	OnRetryFinished(success bool, attempt int, finalError string, hasError bool)
}

// RetrySleepFunc sleeps for ms, returning false when the signal aborted the
// wait (RetrySleepAbortError in TS).
type sleepFunc func(ms float64, signal *abort.Signal) bool

// RetryAssistantCall runs a single assistant-producing call with bounded
// retry on transient errors. produce is invoked per attempt (blocking); it
// returns the produced message. When policy is nil or disabled the first
// response is returned unchanged.
func RetryAssistantCall(
	produce func() *AssistantMessage,
	policy *RetryPolicy,
	signal *abort.Signal,
	callbacks RetryCallbacks,
) *AssistantMessage {
	return retryAssistantCallWithSleep(produce, policy, signal, callbacks, defaultRetrySleep)
}

func retryAssistantCallWithSleep(
	produce func() *AssistantMessage,
	policy *RetryPolicy,
	signal *abort.Signal,
	callbacks RetryCallbacks,
	sleep sleepFunc,
) *AssistantMessage {
	maxAttempts := 0
	if policy != nil && policy.Enabled {
		maxAttempts = policy.MaxRetries
	}
	attempt := 0
	lastRetryAttempt := 0
	lastRetryMessage := ""
	hadRetry := false
	for {
		response := produce()

		if response.StopReason == StopAborted {
			if hadRetry && callbacks != nil {
				callbacks.OnRetryFinished(false, lastRetryAttempt, "", false)
			}
			return response
		}
		if response.StopReason != StopError {
			if hadRetry && callbacks != nil {
				callbacks.OnRetryFinished(true, lastRetryAttempt, "", false)
			}
			return response
		}
		if attempt >= maxAttempts || !IsRetryableAssistantError(response) {
			if hadRetry && callbacks != nil {
				callbacks.OnRetryFinished(false, lastRetryAttempt, response.ErrorMessageText(), response.ErrorMessage != nil)
			}
			return response
		}
		attempt++
		hadRetry = true
		lastRetryAttempt = attempt
		lastRetryMessage = response.ErrorMessageText()
		if lastRetryMessage == "" {
			lastRetryMessage = "Unknown error"
		}
		delayMs := RetryDelayMs(*policy, attempt)
		if callbacks != nil {
			callbacks.OnRetryScheduled(attempt, maxAttempts, delayMs, lastRetryMessage)
		}
		if !sleep(delayMs, signal) {
			// Aborted during backoff: normalize to an aborted message with
			// errorMessage stripped.
			aborted := *response
			aborted.StopReason = StopAborted
			aborted.ErrorMessage = nil
			if callbacks != nil {
				callbacks.OnRetryFinished(false, attempt, lastRetryMessage, true)
			}
			return &aborted
		}
		if callbacks != nil {
			callbacks.OnRetryAttemptStart()
		}
	}
}

func defaultRetrySleep(ms float64, signal *abort.Signal) bool {
	if signal.Aborted() {
		return false
	}
	done := make(chan struct{})
	timer := timeAfterFunc(ms, func() { close(done) })
	remove := signal.OnAbort(func() {
		if timer.Stop() {
			close(done)
		}
	})
	defer remove()
	<-done
	return !signal.Aborted()
}

// IsRetryableAssistantError classifies whether a failed assistant message
// looks like a transient provider or transport error.
func IsRetryableAssistantError(message *AssistantMessage) bool {
	if message.StopReason != StopError || message.ErrorMessage == nil {
		return false
	}
	errorMessage := *message.ErrorMessage
	if nonRetryableProviderLimitErrorPattern.MatchString(errorMessage) {
		return false
	}
	return retryableProviderErrorPattern.MatchString(errorMessage)
}

// ErrorMessageText returns the error message or "".
func (m *AssistantMessage) ErrorMessageText() string {
	if m.ErrorMessage == nil {
		return ""
	}
	return *m.ErrorMessage
}
