package ai

// api_simple_options.go ports api/simple-options.ts: context-aware
// max-tokens clamping and the shared base-options construction used by every
// streamSimple implementation, plus the thinking-budget helpers.

// ContextSafetyTokens are always reserved when fitting maxTokens into the
// context window.
const ContextSafetyTokens = 4096

// MinMaxTokens is the floor for clamped max-token values.
const MinMaxTokens = 1

// ClampMaxTokensToContext caps maxTokens to the model context window minus
// the estimated context and a safety margin.
func ClampMaxTokensToContext(model *Model, context *TranscriptContext, maxTokens float64) float64 {
	if model.ContextWindow <= 0 {
		return maxF(MinMaxTokens, maxTokens)
	}
	available := model.ContextWindow - EstimateContextTokens(context.Messages).Tokens - ContextSafetyTokens
	return minF(maxTokens, maxF(MinMaxTokens, available))
}

// BuildBaseOptions projects SimpleStreamOptions into the StreamOptions the
// API-level stream functions receive.
func BuildBaseOptions(model *Model, context *TranscriptContext, options *SimpleStreamOptions, apiKey string) *StreamOptions {
	base := &StreamOptions{}
	if options != nil {
		base = &options.StreamOptions
	}
	maxTokens := model.MaxTokens
	if options != nil && options.MaxTokens != nil {
		maxTokens = *options.MaxTokens
	}
	resolved := &StreamOptions{
		Signal:                    base.Signal,
		TelemetryContext:          base.TelemetryContext,
		APIKey:                    base.APIKey,
		Fetch:                     base.Fetch,
		Env:                       base.Env,
		OnPayload:                 base.OnPayload,
		OnResponse:                base.OnResponse,
		Headers:                   base.Headers,
		TimeoutMs:                 base.TimeoutMs,
		MaxRetries:                base.MaxRetries,
		MaxRetryDelayMs:           base.MaxRetryDelayMs,
		OnProviderStreamEvent:     base.OnProviderStreamEvent,
		Temperature:               base.Temperature,
		SamplingParams:            base.SamplingParams,
		MaxTokens:                 floatPtr(ClampMaxTokensToContext(model, context, maxTokens)),
		Transport:                 base.Transport,
		CacheRetention:            base.CacheRetention,
		SessionID:                 base.SessionID,
		WebsocketConnectTimeoutMs: base.WebsocketConnectTimeoutMs,
		Metadata:                  base.Metadata,
	}
	if apiKey != "" {
		resolved.APIKey = &apiKey
	}
	return resolved
}

// MinAnswerTokens are always left for the answer when a thinking budget
// shares the response ceiling.
const MinAnswerTokens = 1024

// DefaultThinkingBudgets maps pi reasoning levels to token budgets.
func DefaultThinkingBudgets() map[string]float64 {
	return map[string]float64{
		ThinkingMinimal: 1024,
		ThinkingLow:     2048,
		ThinkingMedium:  8192,
		ThinkingHigh:    16384,
	}
}

// ClampReasoning clamps xhigh/max down to high.
func ClampReasoning(effort *string) *string {
	if effort == nil {
		return nil
	}
	if *effort == ThinkingXHigh || *effort == ThinkingMax {
		high := ThinkingHigh
		return &high
	}
	return effort
}

// ThinkingBudgetForLevel resolves the token budget for a reasoning level.
func ThinkingBudgetForLevel(reasoningLevel string, customBudgets map[string]float64) float64 {
	budgets := DefaultThinkingBudgets()
	for key, value := range customBudgets {
		budgets[key] = value
	}
	clamped := ClampReasoning(&reasoningLevel)
	return budgets[*clamped]
}

// ClampThinkingBudgetToAnswerRoom caps a thinking budget so at least
// MinAnswerTokens remain under a shared response ceiling.
func ClampThinkingBudgetToAnswerRoom(thinkingBudget, ceiling float64) float64 {
	return minF(thinkingBudget, maxF(0, ceiling-MinAnswerTokens))
}

// AdjustMaxTokensForThinking fits a thinking budget inside the response
// ceiling. BaseMaxTokens nil means no explicit caller cap.
func AdjustMaxTokensForThinking(baseMaxTokens *float64, modelMaxTokens float64, reasoningLevel string, customBudgets map[string]float64) (maxTokens float64, thinkingBudget float64) {
	thinkingBudget = ThinkingBudgetForLevel(reasoningLevel, customBudgets)
	if baseMaxTokens == nil {
		maxTokens = modelMaxTokens
	} else {
		maxTokens = minF(*baseMaxTokens+thinkingBudget, modelMaxTokens)
	}
	if maxTokens <= thinkingBudget {
		thinkingBudget = ClampThinkingBudgetToAnswerRoom(thinkingBudget, maxTokens)
	}
	return maxTokens, thinkingBudget
}

func floatPtr(value float64) *float64 { return &value }

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
