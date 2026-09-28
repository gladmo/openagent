package ai

// provider_retry_test.go ports provider-retry.test.ts (fake timers become
// short delays) plus SSE-reader parsing tests for http_sse.go.

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/abort"
)

func providerHTTPError(status int, headers map[string]string) *ProviderHTTPError {
	message := "Provider error"
	if status != 0 {
		message = "Provider error: " + itoaTest(status)
	}
	return &ProviderHTTPError{Status: status, Headers: headers, Message: message}
}

func itoaTest(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}

func TestRetryProviderRequestRetriesRetryableErrors(t *testing.T) {
	calls := 0
	err := RetryProviderRequest(func() error {
		calls++
		if calls == 1 {
			return providerHTTPError(429, map[string]string{"retry-after-ms": "1"})
		}
		return nil
	}, &ProviderRetryOptions{MaxRetries: float64Ptr(1)})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestRetryProviderRequestHonorsXShouldRetryFalse(t *testing.T) {
	calls := 0
	err := RetryProviderRequest(func() error {
		calls++
		return providerHTTPError(429, map[string]string{"x-should-retry": "false"})
	}, &ProviderRetryOptions{MaxRetries: float64Ptr(2)})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryProviderRequestRejectsTooLargeDelay(t *testing.T) {
	calls := 0
	err := RetryProviderRequest(func() error {
		calls++
		return providerHTTPError(429, map[string]string{"retry-after": "277403"})
	}, &ProviderRetryOptions{MaxRetries: float64Ptr(1), MaxRetryDelayMs: float64Ptr(1000)})
	if err == nil {
		t.Fatal("expected error")
	}
	want := "Server requested 277403s retry delay (max: 1s)"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected %q in %q", want, err.Error())
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryProviderRequestAllowsDisablingCap(t *testing.T) {
	calls := 0
	err := RetryProviderRequest(func() error {
		calls++
		if calls == 1 {
			return providerHTTPError(429, map[string]string{"retry-after": "0.01"})
		}
		return nil
	}, &ProviderRetryOptions{MaxRetries: float64Ptr(1), MaxRetryDelayMs: float64Ptr(0)})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestRetryProviderRequestAbortsDuringDelay(t *testing.T) {
	controller := abort.NewController()
	calls := 0
	time.AfterFunc(5*time.Millisecond, controller.Abort)
	err := RetryProviderRequest(func() error {
		calls++
		return providerHTTPError(429, map[string]string{"retry-after": "277403"})
	}, &ProviderRetryOptions{MaxRetries: float64Ptr(2), MaxRetryDelayMs: float64Ptr(0), Signal: controller.Signal()})
	if err == nil {
		t.Fatal("expected abort error")
	}
	if _, isAbort := err.(*abort.Error); !isAbort {
		t.Fatalf("expected AbortError, got %T: %v", err, err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestIsRetryableProviderError(t *testing.T) {
	cases := []struct {
		status  int
		headers map[string]string
		want    bool
	}{
		{408, nil, true},
		{409, nil, true},
		{429, nil, true},
		{500, nil, true},
		{503, nil, true},
		{400, nil, false},
		{401, nil, false},
		{429, map[string]string{"x-should-retry": "true"}, true},
		{500, map[string]string{"x-should-retry": "false"}, false},
		{400, map[string]string{"x-should-retry": "true"}, true},
	}
	for _, tc := range cases {
		if got := IsRetryableProviderError(providerHTTPError(tc.status, tc.headers)); got != tc.want {
			t.Errorf("status=%d headers=%v: got %v want %v", tc.status, tc.headers, got, tc.want)
		}
	}
	if !IsRetryableProviderError(&ProviderHTTPError{Status: 0, Headers: nil}) {
		t.Error("status-less network errors should be retryable")
	}
}

func float64Ptr(v float64) *float64 { return &v }

// ---------------------------------------------------------------------------
// SSE reader
// ---------------------------------------------------------------------------

func TestSSEReaderParsesEvents(t *testing.T) {
	body := "event: message_start\n" +
		"data: {\"type\":\"message_start\"}\n" +
		"\n" +
		": keep-alive comment\n" +
		"\n" +
		"data: {\"type\":\"content_block_delta\"}\n" +
		"\n" +
		"data: [DONE]\n" +
		"\n"
	reader := NewSSEStream(strings.NewReader(body))

	first, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if first.Event != "message_start" || first.Data != `{"type":"message_start"}` {
		t.Fatalf("unexpected first event: %+v", first)
	}

	second, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if second.Event != "" || second.Data != `{"type":"content_block_delta"}` {
		t.Fatalf("unexpected second event: %+v", second)
	}

	third, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if third.Data != "[DONE]" {
		t.Fatalf("unexpected third event: %+v", third)
	}

	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestSSEReaderCRLFAndMultiData(t *testing.T) {
	body := "event: delta\r\n" +
		"data: line1\r\n" +
		"data: line2\r\n" +
		"\r\n"
	reader := NewSSEStream(strings.NewReader(body))
	event, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if event.Event != "delta" || event.Data != "line1\nline2" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestSSEReaderNoTrailingBlankLine(t *testing.T) {
	reader := NewSSEStream(strings.NewReader("data: x\n"))
	event, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if event.Data != "x" {
		t.Fatalf("unexpected data: %q", event.Data)
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}
