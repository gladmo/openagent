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

// Retry patterns synced wholesale to pi-ai utils/retry.ts @ bd3c9b823.
// RE2 has no lookbehind/lookahead: the standalone status-code pattern consumes
// its boundary characters instead (#11057).
var nonRetryableProviderLimitErrorPattern = buildProviderErrorPattern([]string{
	// OpenCode Go/free-tier limits returned as 429 JSON error types by OpenCode's
	// Zen API. These are subscription/account limits, not transient throttles.
	"GoUsageLimitError",
	"FreeUsageLimitError",

	// OpenCode Go subscription-limit text asks users to enable available-balance
	// usage after rolling/weekly/monthly limits are reached.
	"Monthly usage limit reached",
	"available balance",

	// Generic quota/budget/billing exhaustion. `insufficient_quota` is OpenAI's
	// quota/billing error code; the other strings cover common gateway wording.
	"insufficient_quota",
	"out of budget",
	"quota exceeded",
	"billing",

	// Sign in with ChatGPT: the subscription's shared usage limit, which resets
	// after hours rather than seconds.
	"subscription_sharing_usage_limit_exceeded",
	// Sign in with ChatGPT on a ChatGPT Business plan: a usage limit set by the
	// workspace admin. Only the admin can raise it.
	"subscription_sharing_workplace_individual_usage_limit_exceeded",
})

var retryableProviderErrorPattern = buildProviderErrorPattern([]string{
	// Generic provider load, HTTP status, and server-side transient failures.
	"overloaded",
	"server_busy",
	"servers are currently busy",
	"currently experiencing high demand",
	"model is at capacity",
	"rate.?limit",
	"too many requests",
	// Status codes must not be part of a longer word or number, e.g. a request id or byte count (#11057).
	"(?:^|[^a-z0-9])(?:429|500|502|503|504|520|524)(?:$|[^a-z0-9])",
	// Also matches "The service is temporarily unavailable" (#10965).
	"service.?(?:is.?)?(?:temporarily.?)?unavailable",
	"server.?error",
	"internal.?error",

	// Wrapper/provider text for transient upstream failures, including OpenRouter
	// "Provider returned error" responses (#2264) and gateway wording such as
	// "The upstream provider returned an error" (#10842).
	"provider.?returned.?(?:an.?)?error",
	"exceeded request buffer limit while retrying upstream",
	// Gateway failure relaying the upstream response stream, sent as an OpenAI
	// Responses `response.failed` event without an error code (#10920).
	"unable to forward upstream",
	// Other gateway upstream failures relayed as `response.failed` error codes (#10920).
	"upstream.?request.?failed",
	"upstream_stream_read_error",
	"stream was interrupted",
	"http/?2 stream failed",

	// Network, proxy, and fetch transport failures. This includes OpenAI Codex
	// raw-fetch failures such as "upstream connect", "connection refused", and
	// "reset before headers" (#733), plus OpenRouter connection drops (#3317).
	"network.?error",
	"connection.?error",
	"connection.?refused",
	"connection.?lost",
	"other side closed",
	"fetch failed",
	// node-fetch system errors, e.g. the Google ADC OAuth token refresh used by
	// google-vertex. The reason is often empty (#10996).
	"request to \\S+ failed, reason:",
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

	// WebSocket transports can report close/error text instead of HTTP/fetch text.
	"websocket.?closed",
	"websocket.?error",

	// Premature stream endings from SDKs and transports. Anthropic can throw
	// "stream ended without ..." and "Anthropic stream ended before message_stop"
	// (#4433); Bedrock/Smithy can throw an HTTP/2 no-response error (#3594).
	"ended without",
	"stream ended before message_stop",
	"stream ended before a terminal response event",
	"http2 request did not get a response",
	// Codex CLI mid-stream disconnect, relayed by proxies that wrap the Codex CLI,
	// e.g. "stream disconnected before completion: stream closed before response.completed" (#11057).
	"stream disconnected before completion",
	// Node ERR_HTTP2_STREAM_CANCEL: the HTTP/2 session died before the request was
	// sent, e.g. after the Bedrock SDK's 5-minute session timeout (#10379).
	"pending stream has been canceled",

	// Provider-requested retry delay cap failures should flow through the outer
	// retry policy so callers can surface/abort the backoff (#1123).
	"retry delay",

	// Explicit retry guidance emitted mid-stream by OpenAI Responses and Bedrock
	// stream exceptions (#6019).
	"you can retry your request",
	"try your request again",
	"please retry your request",

	// gRPC based providers (e.g. NVIDIA NIM)
	"ResourceExhausted",

	// Sign in with ChatGPT: usage or user data temporarily unavailable. Usage
	// failures can arrive mid-stream without an HTTP 503 in the message.
	"subscription_sharing_usage_unavailable",
	"subscription_sharing_user_unavailable",
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
