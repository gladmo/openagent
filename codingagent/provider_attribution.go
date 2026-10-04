package codingagent

// Port of core/provider-attribution.ts: provider attribution headers
// (OpenRouter/NVIDIA NIM/Cloudflare attribution + OpenCode session
// routing) merged over caller-supplied header sources.

import (
	"net/url"
)

const (
	openrouterHost          = "openrouter.ai"
	nvidiaNimHost           = "integrate.api.nvidia.com"
	cloudflareAPIHost       = "api.cloudflare.com"
	cloudflareAIGatewayHost = "gateway.ai.cloudflare.com"
	opencodeHost            = "opencode.ai"
)

// AttributionModel is the model surface attribution reads.
type AttributionModel struct {
	Provider string
	BaseURL  string
}

func matchesHost(baseURL, expectedHost string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return parsed.Hostname() == expectedHost
}

func isOpenRouterModel(model *AttributionModel) bool {
	return model.Provider == "openrouter" || urlContains(model.BaseURL, openrouterHost)
}

func urlContains(baseURL, fragment string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == fragment || len(host) > len(fragment) && host[len(host)-len(fragment)-1:] == "."+fragment
}

func isNvidiaNimModel(model *AttributionModel) bool {
	return model.Provider == "nvidia" || matchesHost(model.BaseURL, nvidiaNimHost)
}

func isCloudflareModel(model *AttributionModel) bool {
	return model.Provider == "cloudflare-workers-ai" ||
		model.Provider == "cloudflare-ai-gateway" ||
		matchesHost(model.BaseURL, cloudflareAPIHost) ||
		matchesHost(model.BaseURL, cloudflareAIGatewayHost)
}

// getDefaultAttributionHeaders renders the install-telemetry-gated
// attribution set.
func getDefaultAttributionHeaders(model *AttributionModel, telemetryEnabled bool) map[string]string {
	if !telemetryEnabled {
		return nil
	}
	if isOpenRouterModel(model) {
		return map[string]string{
			"HTTP-Referer":            "https://pi.dev",
			"X-OpenRouter-Title":      "pi",
			"X-OpenRouter-Categories": "cli-agent",
		}
	}
	if isNvidiaNimModel(model) {
		return map[string]string{"X-BILLING-INVOKE-ORIGIN": "Pi"}
	}
	if isCloudflareModel(model) {
		return map[string]string{"User-Agent": "pi-coding-agent"}
	}
	return nil
}

// getSessionHeaders renders the OpenCode session routing set.
func getSessionHeaders(model *AttributionModel, sessionID string) map[string]string {
	if sessionID == "" {
		return nil
	}
	if model.Provider != "opencode" && model.Provider != "opencode-go" && !matchesHost(model.BaseURL, opencodeHost) {
		return nil
	}
	return map[string]string{
		"x-opencode-session": sessionID,
		"x-opencode-client":  "pi",
	}
}

// MergeProviderAttributionHeaders merges session + attribution headers
// under caller-supplied sources (nil when empty).
func MergeProviderAttributionHeaders(model *AttributionModel, telemetryEnabled bool, sessionID string, headerSources ...map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range getSessionHeaders(model, sessionID) {
		merged[key] = value
	}
	for key, value := range getDefaultAttributionHeaders(model, telemetryEnabled) {
		merged[key] = value
	}
	for _, headers := range headerSources {
		for key, value := range headers {
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}
