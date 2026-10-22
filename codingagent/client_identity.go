package codingagent

// Port of core/client-identity.ts (adc5fa9f8 + 04a93bad8): how pi's requests
// identify it to providers.

import (
	"os"

	"github.com/gladmo/openagent/ai"
)

// installTelemetryEnv is the env override read by isInstallTelemetryEnabled.
const installTelemetryEnv = "PI_TELEMETRY"

// GetClientIdentity returns how pi's requests identify it to providers: a
// detailed User-Agent with pi's version, and attribution to pi as a CLI
// agent while install telemetry is enabled.
func GetClientIdentity(settingsManager *SettingsManager) ai.ClientIdentity {
	identity := ai.ClientIdentity{
		UserAgent: ai.DetailedUserAgent(),
		Version:   Version(),
	}
	env, envSet := os.LookupEnv(installTelemetryEnv)
	if IsInstallTelemetryEnabled(settingsManager, env, envSet) {
		attribution := ai.PiAttribution
		identity.Attribution = &attribution
	} else {
		identity.AttributionOff = true
	}
	return identity
}
