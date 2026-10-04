package codingagent

// Port of core/http-dispatcher.ts: HTTP idle-timeout parsing/formatting
// and proxy application. Go's net/http transport configuration replaces
// the undici dispatcher installation (documented mapping: DefaultTransport
// tuning is owned by the ai http seam).

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// DefaultHTTPIdleTimeoutMS is the idle timeout default (5 minutes).
const DefaultHTTPIdleTimeoutMS = 300_000

// HttpIdleTimeoutChoice is one labeled timeout option.
type HttpIdleTimeoutChoice struct {
	Label     string
	TimeoutMs int
}

// HTTPIdleTimeoutChoices lists the settings menu options.
var HTTPIdleTimeoutChoices = []HttpIdleTimeoutChoice{
	{Label: "30 sec", TimeoutMs: 30_000},
	{Label: "1 min", TimeoutMs: 60_000},
	{Label: "2 min", TimeoutMs: 120_000},
	{Label: "5 min", TimeoutMs: 300_000},
	{Label: "disabled", TimeoutMs: 0},
}

// ParseHttpIdleTimeoutMs parses "disabled", numeric strings, or numbers
// (nil = invalid).
func ParseHttpIdleTimeoutMs(value any) (int, bool) {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if strings.EqualFold(trimmed, "disabled") {
			return 0, true
		}
		if trimmed == "" {
			return 0, false
		}
		number, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, false
		}
		return ParseHttpIdleTimeoutMs(number)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < 0 {
			return 0, false
		}
		return int(math.Floor(typed)), true
	case int:
		if typed < 0 {
			return 0, false
		}
		return typed, true
	}
	return 0, false
}

// FormatHTTPIdleTimeoutMs renders the settings label.
func FormatHTTPIdleTimeoutMs(timeoutMs int) string {
	for _, choice := range HTTPIdleTimeoutChoices {
		if choice.TimeoutMs == timeoutMs {
			return choice.Label
		}
	}
	return fmt.Sprintf("%d sec", timeoutMs/1000)
}

// ApplyHTTPProxySettings seeds HTTP_PROXY/HTTPS_PROXY when unset.
func ApplyHTTPProxySettings(httpProxy string) {
	proxy := strings.TrimSpace(httpProxy)
	if proxy == "" {
		return
	}
	if osGetenv("HTTP_PROXY") == "" {
		osSetenv("HTTP_PROXY", proxy)
	}
	if osGetenv("HTTPS_PROXY") == "" {
		osSetenv("HTTPS_PROXY", proxy)
	}
}

// ConfigureHTTPDispatcher validates and applies the timeout. Go's
// net/http transport tuning is owned by the ai http seam; this validates
// and records the setting for the transport constructor.
func ConfigureHTTPDispatcher(timeoutMs int) error {
	normalized, ok := ParseHttpIdleTimeoutMs(float64(timeoutMs))
	if !ok {
		return fmt.Errorf("Invalid HTTP idle timeout: %s", strconv.Itoa(timeoutMs))
	}
	httpIdleTimeoutMs = normalized
	return nil
}

// httpIdleTimeoutMs is the applied timeout (read by the transport seam).
var httpIdleTimeoutMs = DefaultHTTPIdleTimeoutMS

// AppliedHTTPIdleTimeoutMs reports the configured timeout.
func AppliedHTTPIdleTimeoutMs() int { return httpIdleTimeoutMs }

func osSetenv(key, value string) { os.Setenv(key, value) }
