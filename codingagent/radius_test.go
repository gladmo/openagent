package codingagent

// Tests for the Radius gateway origin (adapted from radius.test.ts).

import (
	"testing"
)

func TestNormalizeRadiusGatewayUrl(t *testing.T) {
	cases := map[string]string{
		"https://gateway.radius.dev":   "https://gateway.radius.dev",
		"https://gateway.radius.dev/":  "https://gateway.radius.dev",
		"https://gateway.radius.dev//": "https://gateway.radius.dev",
		"gateway.staging.radius.dev":   "https://gateway.staging.radius.dev",
		"http://localhost:8080/":       "http://localhost:8080",
		"":                             DefaultRadiusGateway,
		"   ":                          DefaultRadiusGateway,
	}
	for input, want := range cases {
		if got := NormalizeRadiusGatewayUrl(input); got != want {
			t.Errorf("NormalizeRadiusGatewayUrl(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGetRadiusGatewayUrlDefault(t *testing.T) {
	t.Setenv(EnvRadiusGateway, "")
	if got := GetRadiusGatewayUrl(); got != DefaultRadiusGateway {
		t.Fatalf("default = %q", got)
	}
}

func TestGetRadiusGatewayUrlOverride(t *testing.T) {
	t.Setenv(EnvRadiusGateway, "https://custom.radius.host/")
	if got := GetRadiusGatewayUrl(); got != "https://custom.radius.host" {
		t.Fatalf("override = %q", got)
	}
	t.Setenv(EnvRadiusGateway, "internal.radius.svc:9090")
	if got := GetRadiusGatewayUrl(); got != "https://internal.radius.svc:9090" {
		t.Fatalf("schemeless = %q", got)
	}
}
