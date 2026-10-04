package codingagent

// Tests for the footer data surface and interactive components (adapted
// from footer.test.ts and countdown-timer.test.ts).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSanitizeStatusText(t *testing.T) {
	if got := SanitizeStatusText("line1\nline2\ttabbed\r\ncrlf"); got != "line1 line2 tabbed crlf" {
		t.Fatalf("sanitized = %q", got)
	}
	if got := SanitizeStatusText("  multiple   spaces  "); got != "multiple spaces" {
		t.Fatalf("spaces = %q", got)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[float64]string{
		0: "0", 999: "999",
		1000: "1.0k", 9999: "10.0k",
		10000: "10k", 999999: "1000k",
		1000000: "1.0M", 9999999: "10.0M",
		10000000: "10M",
	}
	for input, want := range cases {
		if got := FormatTokens(input); got != want {
			t.Errorf("FormatTokens(%v) = %q, want %q", input, got, want)
		}
	}
}

func TestFormatCwdForFooter(t *testing.T) {
	if got := FormatCwdForFooter("/home/user/project", "/home/user"); got != "~/project" {
		t.Fatalf("inside = %q", got)
	}
	if got := FormatCwdForFooter("/home/user", "/home/user"); got != "~" {
		t.Fatalf("home itself = %q", got)
	}
	if got := FormatCwdForFooter("/opt/other", "/home/user"); got != "/opt/other" {
		t.Fatalf("outside = %q", got)
	}
	if got := FormatCwdForFooter("/any/path", ""); got != "/any/path" {
		t.Fatalf("no home = %q", got)
	}
}

type stubFooterData struct {
	branch         string
	subscriptionID string
	allSubscribed  bool
}

func (s *stubFooterData) GetGitBranch() string { return s.branch }
func (s *stubFooterData) IsUsingSubscription(providerID string) bool {
	return s.allSubscribed || providerID == s.subscriptionID
}

func appendAssistantUsage(t *testing.T, manager *SessionManager, input, output, cacheRead, cacheWrite float64) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"role": "assistant",
		"Usage": map[string]any{
			"Input":      input,
			"Output":     output,
			"CacheRead":  cacheRead,
			"CacheWrite": cacheWrite,
			"Cost": map[string]any{
				"Input":      input * 3 / 1e6,
				"Output":     output * 15 / 1e6,
				"CacheRead":  cacheRead * 0.3 / 1e6,
				"CacheWrite": cacheWrite * 3.75 / 1e6,
			},
		},
	})
	manager.AppendMessage(json.RawMessage(raw))
}

func TestFooterComputeUsageTotals(t *testing.T) {
	session := newTestSession(t)
	appendAssistantUsage(t, session.SessionManager, 100, 200, 5000, 1000)
	appendAssistantUsage(t, session.SessionManager, 50, 80, 3000, 0)

	footer := NewFooter(session, &stubFooterData{})
	totals := footer.ComputeUsageTotals()
	if totals.Input != 150 {
		t.Fatalf("input = %v", totals.Input)
	}
	if totals.Output != 280 {
		t.Fatalf("output = %v", totals.Output)
	}
	if totals.CacheRead != 8000 || totals.CacheWrite != 1000 {
		t.Fatalf("cache = %v/%v", totals.CacheRead, totals.CacheWrite)
	}
	if totals.Cost <= 0 {
		t.Fatalf("cost = %v", totals.Cost)
	}
	if !footer.hasCacheHitRate {
		t.Fatal("no cache hit rate")
	}
	// Last entry: 3000/(50+3000+0) = 98.36%
	if footer.latestCacheHit < 98 || footer.latestCacheHit > 99 {
		t.Fatalf("hit rate = %v", footer.latestCacheHit)
	}
}

func TestFooterRenderLines(t *testing.T) {
	session := newTestSession(t)
	appendAssistantUsage(t, session.SessionManager, 1200, 3400, 8000, 2000)

	footer := NewFooter(session, &stubFooterData{branch: "main"})
	lines := footer.RenderLines(120)
	if len(lines) != 2 {
		t.Fatalf("lines = %v", lines)
	}
	if !strings.Contains(lines[0], "(main)") {
		t.Fatalf("pwd = %q", lines[0])
	}
	if !strings.Contains(lines[1], "↑1.2k") || !strings.Contains(lines[1], "↓3.4k") {
		t.Fatalf("stats = %q", lines[1])
	}
	if !strings.Contains(lines[1], "R8.0k") || !strings.Contains(lines[1], "W2.0k") {
		t.Fatalf("cache = %q", lines[1])
	}
	if !strings.Contains(lines[1], "$") {
		t.Fatalf("cost = %q", lines[1])
	}

	// Session name appends.
	session.SessionManager.AppendSessionInfo("my-session")
	lines = footer.RenderLines(120)
	if !strings.Contains(lines[0], "• my-session") {
		t.Fatalf("named = %q", lines[0])
	}
}

func TestFooterRenderLinesSubscription(t *testing.T) {
	session := newTestSession(t)
	footer := NewFooter(session, &stubFooterData{allSubscribed: true})
	lines := footer.RenderLines(120)
	// No usage yet, but the subscription marker shows the cost slot.
	if !strings.Contains(lines[1], "(sub)") {
		t.Fatalf("stats = %q", lines[1])
	}
}

func TestCountdownTimer(t *testing.T) {
	ticks := make(chan int, 10)
	expired := make(chan struct{}, 1)

	timer := NewCountdownTimer(2500, nil, func(seconds int) {
		ticks <- seconds
	}, func() { expired <- struct{}{} })
	defer timer.Dispose()

	// Initial synchronous tick.
	select {
	case got := <-ticks:
		if got != 3 {
			t.Fatalf("initial = %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no initial tick")
	}

	select {
	case <-expired:
	case <-time.After(5 * time.Second):
		t.Fatal("never expired")
	}
}

func TestCountdownTimerDispose(t *testing.T) {
	expired := make(chan struct{}, 1)
	timer := NewCountdownTimer(1500, nil, func(int) {}, func() { expired <- struct{}{} })
	timer.Dispose()
	select {
	case <-expired:
		t.Fatal("expired after dispose")
	case <-time.After(300 * time.Millisecond):
	}
	// Idempotent.
	timer.Dispose()
}

func TestDynamicBorder(t *testing.T) {
	border := NewDynamicBorder(
		func(s string) string { return "[i]" + s },
		func(s string) string { return "[a]" + s },
	)
	if got := border.Apply("x"); got != "[i]x" {
		t.Fatalf("inactive = %q", got)
	}
	border.SetActive(true)
	if got := border.Apply("x"); got != "[a]x" {
		t.Fatalf("active = %q", got)
	}
}
