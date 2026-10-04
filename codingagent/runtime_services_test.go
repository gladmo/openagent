package codingagent

// Tests for provider attribution, telemetry gating, and diagnostics
// dedup (adapted from their reference tests).

import (
	"strings"
	"testing"
)

func TestMergeProviderAttributionHeadersOpenRouter(t *testing.T) {
	model := &AttributionModel{Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1"}
	headers := MergeProviderAttributionHeaders(model, true, "", nil)
	if headers == nil {
		t.Fatal("nil headers")
	}
	if headers["HTTP-Referer"] != "https://pi.dev" {
		t.Fatalf("referer = %q", headers["HTTP-Referer"])
	}
	if headers["X-OpenRouter-Title"] != "pi" || headers["X-OpenRouter-Categories"] != "cli-agent" {
		t.Fatalf("headers = %v", headers)
	}

	// Telemetry off: nil.
	if headers := MergeProviderAttributionHeaders(model, false, "", nil); headers != nil {
		t.Fatalf("telemetry off = %v", headers)
	}

	// Non-attributed provider: nil.
	other := &AttributionModel{Provider: "anthropic", BaseURL: "https://api.anthropic.com"}
	if headers := MergeProviderAttributionHeaders(other, true, "", nil); headers != nil {
		t.Fatalf("unattributed = %v", headers)
	}
}

func TestMergeProviderAttributionHeadersHostMatching(t *testing.T) {
	// NVIDIA NIM by host.
	nim := &AttributionModel{Provider: "custom", BaseURL: "https://integrate.api.nvidia.com/v1"}
	headers := MergeProviderAttributionHeaders(nim, true, "", nil)
	if headers == nil || headers["X-BILLING-INVOKE-ORIGIN"] != "Pi" {
		t.Fatalf("nim = %v", headers)
	}

	// Cloudflare by provider id.
	cf := &AttributionModel{Provider: "cloudflare-workers-ai", BaseURL: "https://api.cloudflare.com/v1"}
	headers = MergeProviderAttributionHeaders(cf, true, "", nil)
	if headers == nil || headers["User-Agent"] != "pi-coding-agent" {
		t.Fatalf("cloudflare = %v", headers)
	}

	// Cloudflare by gateway host.
	gateway := &AttributionModel{Provider: "custom", BaseURL: "https://gateway.ai.cloudflare.com/v1"}
	headers = MergeProviderAttributionHeaders(gateway, true, "", nil)
	if headers == nil {
		t.Fatal("gateway nil")
	}
}

func TestMergeProviderAttributionHeadersOpenCodeSession(t *testing.T) {
	opencode := &AttributionModel{Provider: "opencode", BaseURL: "https://opencode.ai"}
	headers := MergeProviderAttributionHeaders(opencode, false, "sess-123", nil)
	if headers == nil {
		t.Fatal("nil session headers")
	}
	if headers["x-opencode-session"] != "sess-123" || headers["x-opencode-client"] != "pi" {
		t.Fatalf("headers = %v", headers)
	}

	// Other providers get nothing without attribution.
	other := &AttributionModel{Provider: "anthropic", BaseURL: "https://api.anthropic.com"}
	if headers := MergeProviderAttributionHeaders(other, false, "sess-123", nil); headers != nil {
		t.Fatalf("session leaked = %v", headers)
	}

	// Caller sources take precedence.
	caller := map[string]string{"x-opencode-session": "override"}
	headers = MergeProviderAttributionHeaders(opencode, false, "sess-123", caller)
	if headers["x-opencode-session"] != "override" {
		t.Fatalf("override = %v", headers)
	}
}

func TestIsInstallTelemetryEnabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvAgentDirName(), dir)
	settings := SettingsManagerCreate(dir, dir, nil)

	// Default on.
	if !IsInstallTelemetryEnabled(settings, "", false) {
		t.Fatal("default off")
	}

	// Env override wins both ways.
	if IsInstallTelemetryEnabled(settings, "0", true) {
		t.Fatal("0 enabled")
	}
	if IsInstallTelemetryEnabled(settings, "false", true) {
		t.Fatal("false enabled")
	}
	if !IsInstallTelemetryEnabled(settings, "1", true) {
		t.Fatal("1 disabled")
	}
	if !IsInstallTelemetryEnabled(settings, "YES", true) {
		t.Fatal("YES disabled")
	}

	// Setting flip.
	settings.SetEnableInstallTelemetry(false)
	if IsInstallTelemetryEnabled(settings, "", false) {
		t.Fatal("setting off lost")
	}
}

func TestCollectSettingsDiagnostics(t *testing.T) {
	errors := []SettingsDrainedError{
		{Scope: "global", Path: "/g.json", Error: "bad json"},
		{Scope: "project", Path: "", Error: "missing field"},
	}
	diagnostics := CollectSettingsDiagnostics(errors)
	if len(diagnostics) != 2 {
		t.Fatalf("count = %d", len(diagnostics))
	}
	if !strings.Contains(diagnostics[0].Message, "Invalid settings file /g.json: bad json") {
		t.Fatalf("pathed = %q", diagnostics[0].Message)
	}
	if !strings.Contains(diagnostics[1].Message, "Invalid project settings: missing field") {
		t.Fatalf("scope = %q", diagnostics[1].Message)
	}
	if diagnostics[0].Type != "warning" {
		t.Fatalf("type = %q", diagnostics[0].Type)
	}
}

func TestDeduplicateDiagnostics(t *testing.T) {
	diagnostics := []CliDiagnostic{
		{Type: "warning", Message: "same"},
		{Type: "error", Message: "same"},
		{Type: "warning", Message: "same"},
		{Type: "warning", Message: "other"},
	}
	deduped := DeduplicateDiagnostics(diagnostics)
	if len(deduped) != 3 {
		t.Fatalf("count = %d", len(deduped))
	}
}
