package ai

// env_api_keys.go ports env-api-keys.ts: the provider -> API-key env-var
// mapping used for env discovery and status. The Vertex ADC file probing is
// google-vertex specific and out of this provider subset; it is omitted.

// Anthropic auth env vars (AUTH_TOKEN participates in discovery but requests
// must carry it as Authorization: Bearer).
const (
	EnvAnthropicAuthToken  = "ANTHROPIC_AUTH_TOKEN"
	EnvAnthropicOAuthToken = "ANTHROPIC_OAUTH_TOKEN"
	EnvAnthropicAPIKey     = "ANTHROPIC_API_KEY"
)

// apiKeyEnvVars maps provider ids to their API-key env var. Mirrors
// getApiKeyEnvVars' envMap; github-copilot's special case is kept.
var apiKeyEnvVars = map[string][]string{
	"github-copilot": {"COPILOT_GITHUB_TOKEN"},
	// anthropic handled separately (see APIKeyEnvVars).
	"ant-ling":                   {"ANT_LING_API_KEY"},
	"qwen-token-plan":            {"QWEN_TOKEN_PLAN_API_KEY"},
	"qwen-token-plan-cn":         {"QWEN_TOKEN_PLAN_CN_API_KEY"},
	"qwen-token-plan-individual": {"QWEN_TOKEN_PLAN_API_KEY"},
	"openai":                     {"OPENAI_API_KEY"},
	"azure-openai-responses":     {"AZURE_OPENAI_API_KEY"},
	"nvidia":                     {"NVIDIA_API_KEY"},
	"deepseek":                   {"DEEPSEEK_API_KEY"},
	"google":                     {"GEMINI_API_KEY"},
	"google-vertex":              {"GOOGLE_CLOUD_API_KEY"},
	"groq":                       {"GROQ_API_KEY"},
	"cerebras":                   {"CEREBRAS_API_KEY"},
	"xai":                        {"XAI_API_KEY"},
	"typesafe":                   {"TYPESAFE_API_KEY"},
	"radius":                     {"RADIUS_API_KEY"},
	"openrouter":                 {"OPENROUTER_API_KEY"},
	"vercel-ai-gateway":          {"AI_GATEWAY_API_KEY"},
	"zai":                        {"ZAI_API_KEY"},
	"zai-coding-cn":              {"ZAI_CODING_CN_API_KEY"},
	"mistral":                    {"MISTRAL_API_KEY"},
	"minimax":                    {"MINIMAX_API_KEY"},
	"minimax-cn":                 {"MINIMAX_CN_API_KEY"},
	"moonshotai":                 {"MOONSHOT_API_KEY"},
	"moonshotai-cn":              {"MOONSHOT_API_KEY"},
	"huggingface":                {"HF_TOKEN"},
	"fireworks":                  {"FIREWORKS_API_KEY"},
	"together":                   {"TOGETHER_API_KEY"},
	"baseten":                    {"BASETEN_API_KEY"},
	"opencode":                   {"OPENCODE_API_KEY"},
	"opencode-go":                {"OPENCODE_API_KEY"},
	"kimi-coding":                {"KIMI_API_KEY"},
	"meta":                       {"META_API_KEY"},
	"cloudflare-workers-ai":      {"CLOUDFLARE_API_KEY"},
	"cloudflare-ai-gateway":      {"CLOUDFLARE_API_KEY"},
	"xiaomi":                     {"XIAOMI_API_KEY"},
	"xiaomi-token-plan-cn":       {"XIAOMI_TOKEN_PLAN_CN_API_KEY"},
	"xiaomi-token-plan-ams":      {"XIAOMI_TOKEN_PLAN_AMS_API_KEY"},
	"xiaomi-token-plan-sgp":      {"XIAOMI_TOKEN_PLAN_SGP_API_KEY"},
}

// APIKeyEnvVars returns the env vars that can provide an API key for a
// provider, in resolution order.
func APIKeyEnvVars(provider string) []string {
	if provider == "anthropic" {
		return []string{EnvAnthropicAuthToken, EnvAnthropicOAuthToken, EnvAnthropicAPIKey}
	}
	if vars, ok := apiKeyEnvVars[provider]; ok {
		return vars
	}
	return nil
}

// FindEnvKeys returns the configured env vars that can provide an API key.
func FindEnvKeys(provider string, env ProviderEnv) []string {
	var found []string
	for _, name := range APIKeyEnvVars(provider) {
		if GetProviderEnvValue(name, env) != "" {
			found = append(found, name)
		}
	}
	return found
}

// GetEnvAPIKey returns the ambient API key for a provider from known env
// vars. ANTHROPIC_AUTH_TOKEN is skipped (it must travel as a Bearer header).
func GetEnvAPIKey(provider string, env ProviderEnv) string {
	envKeys := FindEnvKeys(provider, env)
	if len(envKeys) == 0 {
		return ""
	}
	if provider == "anthropic" {
		for _, key := range envKeys {
			if key != EnvAnthropicAuthToken {
				return GetProviderEnvValue(key, env)
			}
		}
		return ""
	}
	return GetProviderEnvValue(envKeys[0], env)
}
