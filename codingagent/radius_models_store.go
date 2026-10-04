package codingagent

// Port of core/radius.ts: the Radius gateway origin with override. The
// models-store port lives in auth_storage.go.

import "strings"

// RadiusProviderID names the Radius provider.
const RadiusProviderID = "radius"

// EnvRadiusGateway is the gateway override env var.
const EnvRadiusGateway = "PI_RADIUS_GATEWAY"

// DefaultRadiusGateway is the production gateway.
const DefaultRadiusGateway = "https://gateway.radius.dev"

// NormalizeRadiusGatewayUrl canonicalizes the origin.
func NormalizeRadiusGatewayUrl(rawURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if trimmed == "" {
		return DefaultRadiusGateway
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	return trimmed
}

// GetRadiusGatewayUrl resolves the gateway origin (env override honored).
func GetRadiusGatewayUrl() string {
	return NormalizeRadiusGatewayUrl(envDefault(osGetenv(EnvRadiusGateway), DefaultRadiusGateway))
}

func envDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
