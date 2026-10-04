package codingagent

// Tests for bug-report redaction and bundle assembly (adapted from
// bug-report.test.ts surfaces).

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsSensitiveKey(t *testing.T) {
	for _, sensitive := range []string{
		"api_key", "apiKey", "API_KEY", "secret", "my-secret",
		"auth_token", "password", "PASSWD", "credential",
		"authorization", "cookie", "session-token", "apikey",
	} {
		if !isSensitiveKey(sensitive) {
			t.Errorf("%q not detected", sensitive)
		}
	}
	for _, safe := range []string{
		"model", "provider", "theme", "maxTokens", "context_window",
		"tokenizer", "cookiejar-not", "tokencount",
	} {
		if isSensitiveKey(safe) {
			t.Errorf("%q misdetected", safe)
		}
	}
}

func TestRedactUrl(t *testing.T) {
	// Credentials stripped.
	if got := RedactUrl("https://user:pass@example.com/path"); strings.Contains(got, "user:pass") {
		t.Fatalf("credentials = %q", got)
	}
	// Sensitive query params redacted.
	got := RedactUrl("https://example.com/path?api_key=secret123&model=x")
	if strings.Contains(got, "secret123") {
		t.Fatalf("api key = %q", got)
	}
	if !strings.Contains(got, "model=x") {
		t.Fatalf("safe param lost: %q", got)
	}
	// Clean URL unchanged.
	clean := "https://example.com/path?a=1"
	if got := RedactUrl(clean); got != clean {
		t.Fatalf("clean = %q", got)
	}
	// Non-URL passes through.
	if got := RedactUrl("not a url"); got != "not a url" {
		t.Fatalf("non-url = %q", got)
	}
}

func TestRedactJsonValue(t *testing.T) {
	value := map[string]any{
		"api_key": "secret",
		"nested":  map[string]any{"authToken": "tok"},
		"url":     "https://k:v@x.io/p?token=t",
		"safe":    "kept",
		"list":    []any{"item"},
	}
	redacted := RedactJsonValue(value).(map[string]any)
	if redacted["api_key"] != "<redacted>" {
		t.Fatalf("api_key = %v", redacted["api_key"])
	}
	nested := redacted["nested"].(map[string]any)
	if nested["authToken"] != "<redacted>" {
		t.Fatalf("nested token = %v", nested["authToken"])
	}
	if url := redacted["url"].(string); strings.Contains(url, "k:v") || strings.Contains(url, "t=token") {
		t.Fatalf("url = %q", url)
	}
	if redacted["safe"] != "kept" {
		t.Fatalf("safe = %v", redacted["safe"])
	}
	list := redacted["list"].([]any)
	if list[0] != "item" {
		t.Fatalf("list = %v", list)
	}
}

func TestRedactSettings(t *testing.T) {
	settings := Settings{
		"trackingId": "track-me",
		"apiKey":     "k",
		"theme":      "dark",
	}
	redacted := RedactSettings(settings)
	if _, has := redacted["trackingId"]; has {
		t.Fatal("tracking id survived")
	}
	if redacted["apiKey"] != "<redacted>" {
		t.Fatalf("key = %v", redacted["apiKey"])
	}
	if redacted["theme"] != "dark" {
		t.Fatalf("theme = %v", redacted["theme"])
	}
}

func TestCollectBugReportEnvironment(t *testing.T) {
	env := CollectBugReportEnvironment()
	if env.Version == "" || env.UserAgent == "" {
		t.Fatalf("env = %+v", env)
	}
	if env.Runtime == "" || env.Platform == "" || env.Arch == "" {
		t.Fatalf("runtime = %+v", env)
	}
	// PI_* env names collected without values.
	for _, name := range env.PiEnvironmentVariables {
		if !strings.HasPrefix(name, "PI_") {
			t.Fatalf("non-PI name = %q", name)
		}
		if strings.Contains(name, "=") {
			t.Fatalf("value leaked = %q", name)
		}
	}
}

func TestBugReportFiles(t *testing.T) {
	// Minimal bundle: report + diagnostics only.
	bundle := &BugReportBundle{
		Metadata:    map[string]any{"version": "1.0"},
		Diagnostics: map[string]any{"ok": true},
	}
	files := BugReportFiles(bundle)
	if len(files) != 2 {
		t.Fatalf("files = %d", len(files))
	}
	if files[0].Name != "report.json" || files[1].Name != "diagnostics.json" {
		t.Fatalf("names = %v %v", files[0].Name, files[1].Name)
	}
	var metadata map[string]any
	json.Unmarshal([]byte(files[0].Data), &metadata)
	if metadata["version"] != "1.0" {
		t.Fatalf("metadata = %v", metadata)
	}

	// Full bundle adds session + summary.
	bundle.SessionJsonl = "{}\n"
	bundle.Summary = "summary text"
	files = BugReportFiles(bundle)
	if len(files) != 4 {
		t.Fatalf("full files = %d", len(files))
	}
	if files[2].Name != "session.jsonl" || files[3].Name != "summary.md" {
		t.Fatalf("names = %v", files)
	}
	if !strings.HasSuffix(files[3].Data, "\n") {
		t.Fatalf("summary newline = %q", files[3].Data)
	}
	// Summary with trailing newline stays unmodified.
	bundle.Summary = "already\n"
	files = BugReportFiles(bundle)
	if strings.Count(files[3].Data, "\n") != 1 {
		t.Fatalf("double newline = %q", files[3].Data)
	}
}

func TestBugReportArchiveFileName(t *testing.T) {
	if got := BugReportArchiveFileName("abc123"); got != "pi-bug-report-abc123.zip" {
		t.Fatalf("name = %q", got)
	}
}
