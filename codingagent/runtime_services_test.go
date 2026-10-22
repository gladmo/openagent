package codingagent

// Tests for client identity, telemetry gating, and diagnostics dedup
// (adapted from their reference tests).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
)

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

func TestGetClientIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvAgentDirName(), dir)
	settings := SettingsManagerCreate(dir, dir, nil)

	// Default: pi attribution while install telemetry is enabled.
	identity := GetClientIdentity(settings)
	if identity.UserAgent == nil || identity.UserAgent.Kind != ai.UserAgentDetailed {
		t.Fatalf("userAgent = %+v", identity.UserAgent)
	}
	if identity.Version != Version() {
		t.Fatalf("version = %q", identity.Version)
	}
	if identity.Attribution == nil || identity.Attribution.Name != "pi" || identity.AttributionOff {
		t.Fatalf("attribution = %+v", identity.Attribution)
	}

	// Telemetry off: attribution explicitly disabled.
	t.Setenv("PI_TELEMETRY", "0")
	identity = GetClientIdentity(settings)
	if identity.Attribution != nil || !identity.AttributionOff {
		t.Fatalf("attribution = %+v off=%v", identity.Attribution, identity.AttributionOff)
	}
}
