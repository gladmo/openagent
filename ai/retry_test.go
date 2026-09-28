package ai

// Ports of pi/packages/ai/test/retry.test.ts.

import (
	"testing"

	"github.com/gladmo/openagent/abort"
)

func retryTestMsg(text string, stopReason string, errorMessage *string) *AssistantMessage {
	msg := FauxAssistantMessage(text)
	msg.StopReason = stopReason
	msg.ErrorMessage = errorMessage
	return msg
}

func errPtr(s string) *string { return &s }

func TestIsRetryableExplicitProviderGuidance(t *testing.T) {
	openAIExplicit := "An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID req_******** in your message."
	bedrockExplicit := `{"message":"The system encountered an unexpected error during processing. Try your request again."}`
	nvidia := "ResourceExhausted: Worker local total request limit reached (288/48)"
	for _, msg := range []string{openAIExplicit, bedrockExplicit, nvidia} {
		if !IsRetryableAssistantError(retryTestMsg("", StopError, errPtr(msg))) {
			t.Fatalf("expected retryable: %s", msg)
		}
	}
}

func TestIsRetryableTransportWording(t *testing.T) {
	messages := []string{
		"The socket connection was closed unexpectedly. For more information, pass `verbose: true` in the second argument to fetch()",
		"Error: exceeded request buffer limit while retrying upstream",
		"The pending stream has been canceled (caused by: getaddrinfo ENOTFOUND bedrock-runtime.us-east-1.amazonaws.com)",
		"connect ENOTFOUND api.example.com",
		"EAI_AGAIN api.example.com",
		"getaddrinfo failed for api.example.com",
		"OpenAI Responses stream ended before a terminal response event",
		"The system is currently experiencing high demand and cannot process your request. Your request exceeds the maximum usage size allowed during peak load. For improved capacity reliability, consider switching to Provisioned Throughput.",
	}
	for _, msg := range messages {
		if !IsRetryableAssistantError(retryTestMsg("", StopError, errPtr(msg))) {
			t.Fatalf("expected retryable: %s", msg)
		}
	}
}

func TestIsRetryableNonRetryableLimits(t *testing.T) {
	if IsRetryableAssistantError(retryTestMsg("", StopError, errPtr("429 quota exceeded"))) {
		t.Fatal("quota exceeded should not be retryable")
	}
}

func TestIsRetryableClassification(t *testing.T) {
	if !IsRetryableAssistantError(retryTestMsg("", StopError, errPtr("overloaded_error"))) {
		t.Fatal("overloaded should be retryable")
	}
	for _, code := range []string{"520", "524"} {
		if !IsRetryableAssistantError(retryTestMsg("", StopError, errPtr(code+" status code (no body)"))) {
			t.Fatalf("%s should be retryable", code)
		}
	}
	if IsRetryableAssistantError(FauxAssistantMessage("not an error")) {
		t.Fatal("non-error message classified retryable")
	}
}

func TestRetryDelayMsCap(t *testing.T) {
	if got := RetryDelayMs(RetryPolicy{BaseDelayMs: 2000}, 6); got != 60000 {
		t.Fatalf("default cap: %v", got)
	}
	cap := 5000.0
	if got := RetryDelayMs(RetryPolicy{BaseDelayMs: 2000, MaxAgentDelayMs: &cap}, 5); got != 5000 {
		t.Fatalf("custom cap: %v", got)
	}
	zero := 0.0
	if got := RetryDelayMs(RetryPolicy{BaseDelayMs: 2000, MaxAgentDelayMs: &zero}, 5); got != 0 {
		t.Fatalf("zero cap: %v", got)
	}
}

type retryCallbacksRecorder struct {
	scheduled    []int
	attemptStart int
	finished     []finishedRecord
}

type finishedRecord struct {
	success  bool
	attempt  int
	errorMsg string
	hasError bool
}

func (r *retryCallbacksRecorder) OnRetryScheduled(attempt int, maxAttempts int, delayMs float64, errorMessage string) {
	r.scheduled = append(r.scheduled, attempt)
}
func (r *retryCallbacksRecorder) OnRetryAttemptStart() { r.attemptStart++ }
func (r *retryCallbacksRecorder) OnRetryFinished(success bool, attempt int, finalError string, hasError bool) {
	r.finished = append(r.finished, finishedRecord{success, attempt, finalError, hasError})
}

func TestRetryReturnsSuccessImmediately(t *testing.T) {
	calls := 0
	policy := RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}
	res := RetryAssistantCall(func() *AssistantMessage {
		calls++
		return FauxAssistantMessage("ok")
	}, &policy, nil, nil)
	if res.Content[0].(TextContent).Text != "ok" {
		t.Fatal("content")
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestRetryDoesNotRetryAborted(t *testing.T) {
	calls := 0
	cb := &retryCallbacksRecorder{}
	policy := RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}
	res := RetryAssistantCall(func() *AssistantMessage {
		calls++
		return retryTestMsg("", StopAborted, nil)
	}, &policy, nil, cb)
	if res.StopReason != StopAborted || calls != 1 || len(cb.scheduled) != 0 {
		t.Fatalf("res=%s calls=%d scheduled=%v", res.StopReason, calls, cb.scheduled)
	}
}

func TestRetryDoesNotRetryNonRetryable(t *testing.T) {
	calls := 0
	cb := &retryCallbacksRecorder{}
	policy := RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}
	res := RetryAssistantCall(func() *AssistantMessage {
		calls++
		return retryTestMsg("", StopError, errPtr("insufficient_quota"))
	}, &policy, nil, cb)
	if res.StopReason != StopError || calls != 1 || len(cb.scheduled) != 0 || len(cb.finished) != 0 {
		t.Fatalf("calls=%d scheduled=%v finished=%v", calls, cb.scheduled, cb.finished)
	}
}

func TestRetryRetriesUntilMax(t *testing.T) {
	calls := 0
	cb := &retryCallbacksRecorder{}
	policy := RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}
	res := RetryAssistantCall(func() *AssistantMessage {
		calls++
		return retryTestMsg("", StopError, errPtr("terminated"))
	}, &policy, nil, cb)
	if res.StopReason != StopError || calls != 4 || len(cb.scheduled) != 3 {
		t.Fatalf("calls=%d scheduled=%d", calls, len(cb.scheduled))
	}
	if len(cb.finished) != 1 {
		t.Fatalf("finished = %+v", cb.finished)
	}
	f := cb.finished[0]
	if f.success || f.attempt != 3 || f.errorMsg != "terminated" || !f.hasError {
		t.Fatalf("finished = %+v", f)
	}
}

func TestRetryReportsCappedDelays(t *testing.T) {
	n := 0
	cap := 15.0
	policy := RetryPolicy{Enabled: true, MaxRetries: 4, BaseDelayMs: 10, MaxAgentDelayMs: &cap}
	var delays []float64
	cb := &funcCallbacks{
		scheduled: func(attempt int, maxAttempts int, delayMs float64, errorMessage string) {
			delays = append(delays, delayMs)
		},
	}
	RetryAssistantCall(func() *AssistantMessage {
		n++
		if n < 5 {
			return retryTestMsg("", StopError, errPtr("terminated"))
		}
		return FauxAssistantMessage("recovered")
	}, &policy, nil, cb)
	if len(delays) != 4 || delays[0] != 10 || delays[1] != 15 || delays[2] != 15 || delays[3] != 15 {
		t.Fatalf("delays = %v", delays)
	}
}

type funcCallbacks struct {
	scheduled func(attempt int, maxAttempts int, delayMs float64, errorMessage string)
}

func (f *funcCallbacks) OnRetryScheduled(attempt int, maxAttempts int, delayMs float64, errorMessage string) {
	if f.scheduled != nil {
		f.scheduled(attempt, maxAttempts, delayMs, errorMessage)
	}
}
func (f *funcCallbacks) OnRetryAttemptStart()                    {}
func (f *funcCallbacks) OnRetryFinished(bool, int, string, bool) {}

func TestRetryStopsOnSuccess(t *testing.T) {
	n := 0
	cb := &retryCallbacksRecorder{}
	policy := RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}
	res := RetryAssistantCall(func() *AssistantMessage {
		n++
		if n < 3 {
			return retryTestMsg("", StopError, errPtr("terminated"))
		}
		return FauxAssistantMessage("recovered")
	}, &policy, nil, cb)
	if res.Content[0].(TextContent).Text != "recovered" || n != 3 {
		t.Fatalf("n=%d", n)
	}
	if len(cb.finished) != 1 || !cb.finished[0].success || cb.finished[0].attempt != 2 {
		t.Fatalf("finished = %+v", cb.finished)
	}
}

func TestRetryAbortedRetriedCallUnsuccessful(t *testing.T) {
	n := 0
	cb := &retryCallbacksRecorder{}
	policy := RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}
	res := RetryAssistantCall(func() *AssistantMessage {
		n++
		if n == 1 {
			return retryTestMsg("", StopError, errPtr("terminated"))
		}
		return retryTestMsg("", StopAborted, nil)
	}, &policy, nil, cb)
	if res.StopReason != StopAborted || n != 2 {
		t.Fatalf("res=%s n=%d", res.StopReason, n)
	}
	if len(cb.finished) != 1 || cb.finished[0].success || cb.finished[0].attempt != 1 {
		t.Fatalf("finished = %+v", cb.finished)
	}
}

func TestRetryDisabledPolicy(t *testing.T) {
	calls := 0
	cb := &retryCallbacksRecorder{}
	policy := RetryPolicy{Enabled: false, MaxRetries: 3, BaseDelayMs: 0}
	res := RetryAssistantCall(func() *AssistantMessage {
		calls++
		return retryTestMsg("", StopError, errPtr("terminated"))
	}, &policy, nil, cb)
	if res.StopReason != StopError || calls != 1 || len(cb.scheduled) != 0 || len(cb.finished) != 0 {
		t.Fatal("disabled policy retried")
	}
}

func TestRetryAttemptStartOrdering(t *testing.T) {
	var events []string
	n := 0
	policy := RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}
	cb := &orderingCallbacks{events: &events}
	res := RetryAssistantCall(func() *AssistantMessage {
		*cb.events = append(*cb.events, "produce")
		n++
		if n < 3 {
			return retryTestMsg("", StopError, errPtr("terminated"))
		}
		return FauxAssistantMessage("recovered")
	}, &policy, nil, cb)
	if res.Content[0].(TextContent).Text != "recovered" {
		t.Fatal("content")
	}
	want := []string{"produce", "retry:1", "attempt-start", "produce", "retry:2", "attempt-start", "produce"}
	if len(events) != len(want) {
		t.Fatalf("events = %v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v", events)
		}
	}
}

type orderingCallbacks struct{ events *[]string }

func (o *orderingCallbacks) OnRetryScheduled(attempt int, maxAttempts int, delayMs float64, errorMessage string) {
	*o.events = append(*o.events, "retry:"+itoaRetry(attempt))
}
func (o *orderingCallbacks) OnRetryAttemptStart()                    { *o.events = append(*o.events, "attempt-start") }
func (o *orderingCallbacks) OnRetryFinished(bool, int, string, bool) {}

func itoaRetry(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

func TestRetryAbortDuringBackoff(t *testing.T) {
	controller := abort.NewController()
	calls := 0
	cb := &retryCallbacksRecorder{}
	policy := RetryPolicy{Enabled: true, MaxRetries: 5, BaseDelayMs: 10000}
	done := make(chan *AssistantMessage, 1)
	go func() {
		done <- retryAssistantCallWithSleep(func() *AssistantMessage {
			calls++
			return retryTestMsg("", StopError, errPtr("terminated"))
		}, &policy, controller.Signal(), cb, func(ms float64, signal *abort.Signal) bool {
			// Abort arrives while sleeping.
			controller.Abort()
			return !signal.Aborted()
		})
	}()
	res := <-done
	if res.StopReason != StopAborted {
		t.Fatalf("stopReason = %s", res.StopReason)
	}
	if res.ErrorMessage != nil {
		t.Fatalf("errorMessage = %v", *res.ErrorMessage)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	if len(cb.finished) != 1 || cb.finished[0].success || cb.finished[0].attempt != 1 || cb.finished[0].errorMsg != "terminated" {
		t.Fatalf("finished = %+v", cb.finished)
	}
}
