package ai

// providers_all.go ports the provider definitions from
// pi/packages/ai/src/providers/{anthropic,openai,openai-codex,deepseek,zai,
// zai-coding-cn,openrouter}.ts plus the all.ts registry subset. Models come
// from the embedded catalog snapshot (catalog/data); auth is env-key only
// (OAuth login flows are out of scope — see auth_provider.go).

// AnthropicProvider ports providers/anthropic.ts. The OAuth slot (Claude
// Pro/Max) is advertised but not implemented in this port.
func AnthropicProvider() Provider {
	return NewProvider(CreateProviderOptions{
		ID:      "anthropic",
		Name:    "Anthropic",
		BaseURL: "https://api.anthropic.com",
		Auth: ProviderAuth{
			APIKey: AnthropicAPIKeyAuth(),
			OAuth: &ProviderOAuthAuth{
				Name:           "Anthropic (Claude Pro/Max)",
				IsSubscription: true,
			},
		},
		Models: GetCatalogChatModels("anthropic"),
		API:    AnthropicMessagesApi(),
	})
}

// OpenAIProvider ports providers/openai.ts.
func OpenAIProvider() Provider {
	return NewProvider(CreateProviderOptions{
		ID:      "openai",
		Name:    "OpenAI",
		BaseURL: "https://api.openai.com/v1",
		Auth: ProviderAuth{
			APIKey: EnvAPIKeyAuth("OpenAI API key", "OPENAI_API_KEY"),
		},
		Models: GetCatalogChatModels("openai"),
		API:    OpenAIResponsesApi(),
	})
}

// OpenAICodexProvider ports providers/openai-codex.ts. pi authenticates
// Codex exclusively via ChatGPT OAuth; this port takes the ChatGPT token
// from OPENAI_CODEX_API_KEY instead (documented deviation).
func OpenAICodexProvider() Provider {
	return NewProvider(CreateProviderOptions{
		ID:      "openai-codex",
		Name:    "OpenAI Codex",
		BaseURL: "https://chatgpt.com/backend-api",
		Auth: ProviderAuth{
			APIKey: EnvAPIKeyAuth("OpenAI Codex API key", "OPENAI_CODEX_API_KEY"),
			OAuth: &ProviderOAuthAuth{
				Name:           "OpenAI (ChatGPT Plus/Pro)",
				IsSubscription: true,
			},
		},
		Models: GetCatalogChatModels("openai-codex"),
		API:    OpenAICodexResponsesApi(),
	})
}

// DeepSeekProvider ports providers/deepseek.ts.
func DeepSeekProvider() Provider {
	return NewProvider(CreateProviderOptions{
		ID:      "deepseek",
		Name:    "DeepSeek",
		BaseURL: "https://api.deepseek.com",
		Auth: ProviderAuth{
			APIKey: EnvAPIKeyAuth("DeepSeek API key", "DEEPSEEK_API_KEY"),
		},
		Models: GetCatalogChatModels("deepseek"),
		API:    OpenAICompletionsApi(),
	})
}

// ZaiProvider ports providers/zai.ts.
func ZaiProvider() Provider {
	return NewProvider(CreateProviderOptions{
		ID:      "zai",
		Name:    "Z.AI",
		BaseURL: "https://api.z.ai/api/coding/paas/v4",
		Auth: ProviderAuth{
			APIKey: EnvAPIKeyAuth("Z.AI API key", "ZAI_API_KEY"),
		},
		Models: GetCatalogChatModels("zai"),
		API:    OpenAICompletionsApi(),
	})
}

// ZaiCodingCnProvider ports providers/zai-coding-cn.ts.
func ZaiCodingCnProvider() Provider {
	return NewProvider(CreateProviderOptions{
		ID:      "zai-coding-cn",
		Name:    "Z.AI Coding CN",
		BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4",
		Auth: ProviderAuth{
			APIKey: EnvAPIKeyAuth("Z.AI Coding CN API key", "ZAI_CODING_CN_API_KEY"),
		},
		Models: GetCatalogChatModels("zai-coding-cn"),
		API:    OpenAICompletionsApi(),
	})
}

// OpenRouterProvider ports providers/openrouter.ts (chat models only; the
// images/classifier slots are out of scope). anthropic/* models speak
// anthropic-messages, everything else openai-completions.
func OpenRouterProvider() Provider {
	return NewProvider(CreateProviderOptions{
		ID:      "openrouter",
		Name:    "OpenRouter",
		BaseURL: "https://openrouter.ai/api/v1",
		Auth: ProviderAuth{
			APIKey: EnvAPIKeyAuth("OpenRouter API key", "OPENROUTER_API_KEY"),
			OAuth: &ProviderOAuthAuth{
				Name:       "OpenRouter OAuth",
				LoginLabel: "Sign in with OpenRouter",
			},
		},
		Models: chatModelsByAPI(GetCatalogChatModels("openrouter"), "anthropic-messages", "openai-completions"),
		APIByModel: map[string]ProviderStreams{
			"anthropic-messages": AnthropicMessagesApi(),
			"openai-completions": OpenAICompletionsApi(),
		},
	})
}

// chatModelsByAPI filters a provider's chat models down to the implemented
// APIs (unimplemented APIs would fail dispatch).
func chatModelsByAPI(models []*Model, apis ...string) []*Model {
	allowed := map[string]bool{}
	for _, api := range apis {
		allowed[api] = true
	}
	var out []*Model
	for _, model := range models {
		if allowed[model.API] {
			out = append(out, model)
		}
	}
	return out
}

// BuiltinProviders returns the ported built-in providers, freshly
// constructed (all.ts builtinProviders subset).
func BuiltinProviders() []Provider {
	return []Provider{
		AnthropicProvider(),
		DeepSeekProvider(),
		OpenAIProvider(),
		OpenAICodexProvider(),
		OpenRouterProvider(),
		ZaiProvider(),
		ZaiCodingCnProvider(),
	}
}

// BuiltinModels builds a Models collection with every ported built-in
// provider registered (all.ts builtinModels subset).
func BuiltinModels(options ...CreateModelsOptions) MutableModels {
	models := CreateModels(options...)
	for _, provider := range BuiltinProviders() {
		models.SetProvider(provider)
	}
	return models
}
