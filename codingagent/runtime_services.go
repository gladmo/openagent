package codingagent

// Port of core/telemetry.ts, core/settings-diagnostics.ts, and
// core/runtime-credentials.ts: install-telemetry gating, settings-error
// diagnostics with dedup, and the runtime API-key overlay store.

import (
	"fmt"
	"strings"
)

// isTruthyEnvFlag parses "1"/"true"/"yes".
func isTruthyEnvFlag(value string) bool {
	if value == "" {
		return false
	}
	return value == "1" || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes")
}

// IsInstallTelemetryEnabled gates attribution: the env override wins,
// else the setting.
func IsInstallTelemetryEnabled(settingsManager *SettingsManager, telemetryEnv string, envSet bool) bool {
	if envSet {
		return isTruthyEnvFlag(telemetryEnv)
	}
	return settingsManager.GetEnableInstallTelemetry()
}

// ============================================================================
// settings-diagnostics.ts
// ============================================================================

// SettingsDrainedError is one drained settings-load error.
type SettingsDrainedError struct {
	Scope string
	Path  string
	Error string
}

// CollectSettingsDiagnostics converts drained settings errors to
// warnings.
func CollectSettingsDiagnostics(errors []SettingsDrainedError) []CliDiagnostic {
	diagnostics := make([]CliDiagnostic, 0, len(errors))
	for _, entry := range errors {
		message := ""
		if entry.Path != "" {
			message = fmt.Sprintf("Invalid settings file %s: %s", entry.Path, entry.Error)
		} else {
			message = fmt.Sprintf("Invalid %s settings: %s", entry.Scope, entry.Error)
		}
		diagnostics = append(diagnostics, CliDiagnostic{
			Type:    "warning",
			Message: message,
		})
	}
	return diagnostics
}

// DeduplicateDiagnostics removes duplicate type/message pairs keeping
// first occurrences (startup and runtime managers report the same file
// error).
func DeduplicateDiagnostics(diagnostics []CliDiagnostic) []CliDiagnostic {
	seen := map[string]bool{}
	var out []CliDiagnostic
	for _, diagnostic := range diagnostics {
		key := diagnostic.Type + "\x00" + diagnostic.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, diagnostic)
	}
	return out
}
