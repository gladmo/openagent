package ai

// api_openai_completions.go ports api/openai-completions.ts (request-building
// half): compat resolution, message/tool conversion, and params assembly.
// The TS implementation rides on the openai SDK 6.40.0; this port POSTs
// {baseURL}/chat/completions directly with Bearer auth and SSE chunks.

import (
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// OpenAI prompt cache key cap (openai-prompt-cache.ts).
const OpenAIPromptCacheKeyMaxLength = 64

// ClampOpenAIPromptCacheKey truncates a prompt cache key by code points.
func ClampOpenAIPromptCacheKey(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	chars := []rune(key)
	if len(chars) <= OpenAIPromptCacheKeyMaxLength {
		return key, true
	}
	return string(chars[:OpenAIPromptCacheKeyMaxLength]), true
}

// OpenAICompletionsOptions mirrors the TS api-specific options.
type OpenAICompletionsOptions struct {
	StreamOptions
	// ToolChoice: nil = omitted; "auto"/"none"/"required" or "tool:<name>".
	ToolChoice      *string
	ToolChoiceTool  *string
	ReasoningEffort *string // minimal|low|medium|high|xhigh|max
	ThinkingBudgets map[string]float64
}

// OpenAICompatCacheControl is the anthropic-format cache_control value.
type openAICompatCacheControl = *jsonx.Obj

// openAICompletionsCompat is ResolvedOpenAICompletionsCompat.
type openAICompletionsCompat struct {
	SupportsStore                               bool
	SupportsDeveloperRole                       bool
	SupportsReasoningEffort                     bool
	SupportsUsageInStreaming                    bool
	SupportsFinishReason                        bool
	MaxTokensField                              string // "max_tokens" | "max_completion_tokens"
	RequiresToolResultName                      bool
	RequiresAssistantAfterToolResult            bool
	RequiresThinkingAsText                      bool
	RequiresReasoningContentOnAssistantMessages bool
	ThinkingFormat                              string
	OpenRouterRouting                           *jsonx.Obj
	VercelGatewayRouting                        *jsonx.Obj
	ChatTemplateKwargs                          *jsonx.Obj
	ChatTemplateArgs                            *jsonx.Obj
	ZaiToolStream                               bool
	SupportsThinkingTokenBudget                 bool
	ThinkingTokenBudgetField                    string
	SupportsStrictMode                          bool
	SupportsOpenAIGrammarTools                  bool
	SupportsMidConvoSystemMessages              bool
	SupportsMidConvoToolAdditions               bool
	CacheControlFormat                          string
	SendSessionAffinityHeaders                  bool
	SessionAffinityFormat                       string
	SupportsLongCacheRetention                  bool
	VllmPriority                                *float64
}

func detectOpenAICompletionsCompat(model *Model) openAICompletionsCompat {
	provider := model.Provider
	baseURL := model.BaseURL

	isZai := provider == "zai" || provider == "zai-coding-cn" ||
		strings.Contains(baseURL, "api.z.ai") || strings.Contains(baseURL, "open.bigmodel.cn")
	isTogether := provider == "together" || strings.Contains(baseURL, "api.together.ai") || strings.Contains(baseURL, "api.together.xyz")
	isMoonshot := provider == "moonshotai" || provider == "moonshotai-cn" || strings.Contains(baseURL, "api.moonshot.")
	isOpenRouter := provider == "openrouter" || strings.Contains(baseURL, "openrouter.ai")
	isCloudflareWorkersAI := provider == "cloudflare-workers-ai" || strings.Contains(baseURL, "api.cloudflare.com")
	isCloudflareAiGateway := provider == "cloudflare-ai-gateway" || strings.Contains(baseURL, "gateway.ai.cloudflare.com")
	isNvidia := provider == "nvidia" || strings.Contains(baseURL, "integrate.api.nvidia.com")
	isAntLing := provider == "ant-ling" || strings.Contains(baseURL, "api.ant-ling.com")
	isCerebras := provider == "cerebras" || strings.Contains(baseURL, "cerebras.ai")
	isDeepSeek := provider == "deepseek" || strings.Contains(strings.ToLower(baseURL), "deepseek.com")

	isNonStandard := isNvidia || isCerebras || provider == "xai" || strings.Contains(baseURL, "api.x.ai") ||
		isTogether || strings.Contains(baseURL, "chutes.ai") || isDeepSeek || isZai || isMoonshot ||
		provider == "opencode" || strings.Contains(baseURL, "opencode.ai") ||
		isCloudflareWorkersAI || isCloudflareAiGateway || isAntLing

	useMaxTokens := strings.Contains(baseURL, "chutes.ai") || isDeepSeek || isMoonshot ||
		isCloudflareAiGateway || isTogether || isNvidia || isAntLing || isZai

	sessionAffinityFormat := "openai"
	if isOpenRouter {
		sessionAffinityFormat = "openrouter"
	}

	isGrok := provider == "xai" || strings.Contains(baseURL, "api.x.ai")
	isOpenRouterDeveloperRoleModel := isOpenRouter && (strings.HasPrefix(model.ID, "anthropic/") || strings.HasPrefix(model.ID, "openai/"))
	cacheControlFormat := ""
	if provider == "openrouter" && strings.HasPrefix(model.ID, "anthropic/") {
		cacheControlFormat = "anthropic"
	}

	thinkingFormat := "openai"
	switch {
	case isDeepSeek:
		thinkingFormat = "deepseek"
	case isZai:
		thinkingFormat = "zai"
	case isTogether:
		thinkingFormat = "together"
	case isAntLing:
		thinkingFormat = "ant-ling"
	case isOpenRouter:
		thinkingFormat = "openrouter"
	}

	maxTokensField := "max_completion_tokens"
	if useMaxTokens {
		maxTokensField = "max_tokens"
	}

	return openAICompletionsCompat{
		SupportsStore:                               !isNonStandard,
		SupportsDeveloperRole:                       isOpenRouterDeveloperRoleModel || (!isNonStandard && !isOpenRouter),
		SupportsReasoningEffort:                     !isGrok && !isZai && !isMoonshot && !isTogether && !isCloudflareAiGateway && !isNvidia && !isAntLing,
		SupportsUsageInStreaming:                    true,
		SupportsFinishReason:                        true,
		MaxTokensField:                              maxTokensField,
		RequiresReasoningContentOnAssistantMessages: isDeepSeek,
		ThinkingFormat:                              thinkingFormat,
		OpenRouterRouting:                           jsonx.NewObj(),
		VercelGatewayRouting:                        jsonx.NewObj(),
		ChatTemplateKwargs:                          jsonx.NewObj(),
		ChatTemplateArgs:                            jsonx.NewObj(),
		ZaiToolStream:                               false,
		SupportsStrictMode:                          false,
		SupportsOpenAIGrammarTools:                  false,
		SupportsMidConvoSystemMessages:              false,
		SupportsMidConvoToolAdditions:               false,
		CacheControlFormat:                          cacheControlFormat,
		SendSessionAffinityHeaders:                  isOpenRouter,
		SessionAffinityFormat:                       sessionAffinityFormat,
		SupportsLongCacheRetention:                  !(isTogether || isCloudflareWorkersAI || isCloudflareAiGateway || isNvidia || isAntLing),
	}
}

// getOpenAICompletionsCompat merges detected compat with explicit
// model.compat overrides.
func getOpenAICompletionsCompat(model *Model) openAICompletionsCompat {
	detected := detectOpenAICompletionsCompat(model)
	compatObj, hasCompat := JxCompatObj(model)
	if !hasCompat {
		return detected
	}

	overrideString := func(key string, fallback string) string {
		if value, present := JxString(compatObj, key); present && value != "" {
			return value
		}
		return fallback
	}
	overrideBool := func(key string, fallback bool) bool {
		if value, present := JxBool(compatObj, key); present {
			return value
		}
		return fallback
	}
	overrideObj := func(key string, fallback *jsonx.Obj) *jsonx.Obj {
		if value, present := JxObjectField(compatObj, key); present {
			return value
		}
		return fallback
	}
	overrideFloat := func(key string, fallback *float64) *float64 {
		if value, present := JxFloat(compatObj, key); present {
			return &value
		}
		return fallback
	}

	sessionAffinityFormat := overrideString("sessionAffinityFormat", detected.SessionAffinityFormat)

	resolved := detected
	resolved.SupportsStore = overrideBool("supportsStore", detected.SupportsStore)
	resolved.SupportsDeveloperRole = overrideBool("supportsDeveloperRole", detected.SupportsDeveloperRole)
	resolved.SupportsReasoningEffort = overrideBool("supportsReasoningEffort", detected.SupportsReasoningEffort)
	resolved.SupportsUsageInStreaming = overrideBool("supportsUsageInStreaming", detected.SupportsUsageInStreaming)
	resolved.SupportsFinishReason = overrideBool("supportsFinishReason", detected.SupportsFinishReason)
	resolved.MaxTokensField = overrideString("maxTokensField", detected.MaxTokensField)
	resolved.RequiresToolResultName = overrideBool("requiresToolResultName", detected.RequiresToolResultName)
	resolved.RequiresAssistantAfterToolResult = overrideBool("requiresAssistantAfterToolResult", detected.RequiresAssistantAfterToolResult)
	resolved.RequiresThinkingAsText = overrideBool("requiresThinkingAsText", detected.RequiresThinkingAsText)
	resolved.RequiresReasoningContentOnAssistantMessages = overrideBool("requiresReasoningContentOnAssistantMessages", detected.RequiresReasoningContentOnAssistantMessages)
	resolved.ThinkingFormat = overrideString("thinkingFormat", detected.ThinkingFormat)
	resolved.OpenRouterRouting = overrideObj("openRouterRouting", jsonx.NewObj())
	resolved.VercelGatewayRouting = overrideObj("vercelGatewayRouting", detected.VercelGatewayRouting)
	resolved.ChatTemplateKwargs = overrideObj("chatTemplateKwargs", detected.ChatTemplateKwargs)
	resolved.ChatTemplateArgs = overrideObj("chatTemplateArgs", detected.ChatTemplateArgs)
	resolved.ZaiToolStream = overrideBool("zaiToolStream", detected.ZaiToolStream)
	resolved.SupportsThinkingTokenBudget = overrideBool("supportsThinkingTokenBudget", detected.SupportsThinkingTokenBudget)
	resolved.ThinkingTokenBudgetField = overrideString("thinkingTokenBudgetField", detected.ThinkingTokenBudgetField)
	resolved.SupportsStrictMode = overrideBool("supportsStrictMode", detected.SupportsStrictMode)
	resolved.SupportsOpenAIGrammarTools = overrideBool("supportsOpenAIGrammarTools", detected.SupportsOpenAIGrammarTools)
	resolved.SupportsMidConvoSystemMessages = overrideBool("supportsMidConvoSystemMessages", detected.SupportsMidConvoSystemMessages)
	resolved.SupportsMidConvoToolAdditions = overrideBool("supportsMidConvoToolAdditions", detected.SupportsMidConvoToolAdditions)
	resolved.CacheControlFormat = overrideString("cacheControlFormat", detected.CacheControlFormat)
	resolved.SendSessionAffinityHeaders = overrideBool("sendSessionAffinityHeaders", detected.SendSessionAffinityHeaders)
	resolved.SessionAffinityFormat = sessionAffinityFormat
	resolved.SupportsLongCacheRetention = overrideBool("supportsLongCacheRetention", detected.SupportsLongCacheRetention)
	resolved.VllmPriority = overrideFloat("vllmPriority", detected.VllmPriority)
	return resolved
}

// ---------------------------------------------------------------------------
// Auth / client helpers
// ---------------------------------------------------------------------------

func openAIHasAuthHeader(headers ProviderHeaders, name string) bool {
	if len(headers) == 0 {
		return false
	}
	expected := strings.ToLower(name)
	for key, value := range headers {
		if strings.ToLower(key) == expected && value != nil && len(strings.TrimSpace(*value)) > 0 {
			return true
		}
	}
	return false
}

func getClientAPIKey(provider string, apiKey string, headers ProviderHeaders) (string, error) {
	if apiKey != "" {
		return apiKey, nil
	}
	if openAIHasAuthHeader(headers, "authorization") || openAIHasAuthHeader(headers, "cf-aig-authorization") {
		return "unused", nil
	}
	return "", &csError{msg: "No API key for provider: " + provider}
}

func hasToolHistory(messages []Message) bool {
	for _, msg := range messages {
		if result, ok := msg.(*ToolResultMessage); ok {
			_ = result
			return true
		}
		if assistant, ok := msg.(*AssistantMessage); ok {
			for _, block := range assistant.Content {
				if _, isCall := block.(*ToolCall); isCall {
					return true
				}
			}
		}
	}
	return false
}

// createOpenAICompletionsClient resolves the request headers (createClient).
func createOpenAICompletionsClient(model *Model, context *TranscriptContext, apiKey string, optionsHeaders ProviderHeaders, sessionID string, compat openAICompletionsCompat) map[string]string {
	headers := map[string]string{"User-Agent": GetPiUserAgent()}
	for name, value := range model.Headers {
		headers[name] = value
	}
	if model.Provider == "github-copilot" {
		for name, value := range BuildCopilotDynamicHeaders(context.Messages, HasCopilotVisionInput(context.Messages)) {
			headers[name] = value
		}
	}
	if sessionID != "" && compat.SendSessionAffinityHeaders {
		if compat.SessionAffinityFormat == "openrouter" {
			headers["x-session-id"] = sessionID
		} else {
			if compat.SessionAffinityFormat == "openai" {
				headers["session_id"] = sessionID
			}
			headers["x-client-request-id"] = sessionID
			headers["x-session-affinity"] = sessionID
		}
	}
	// Options headers merge last so they can override defaults.
	for name, value := range optionsHeaders {
		if value != nil {
			headers[name] = *value
		}
	}
	if apiKey != "" && apiKey != "unused" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	return headers
}

// ---------------------------------------------------------------------------
// Reasoning details replay
// ---------------------------------------------------------------------------

func isOpenAIReasoningDetail(detail any) bool {
	obj, ok := JxObj(detail)
	if !ok {
		return false
	}
	detailType, _ := JxString(obj, "type")
	if id, present := obj.Get("id"); present && id != nil {
		if _, isString := id.(string); !isString {
			return false
		}
	}
	if format, present := obj.Get("format"); present {
		if _, isString := format.(string); !isString {
			return false
		}
	}
	if index, present := obj.Get("index"); present {
		if _, isNumber := jsonx.ToFloat(index); !isNumber {
			return false
		}
	}
	switch detailType {
	case "reasoning.summary":
		_, has := JxString(obj, "summary")
		return has
	case "reasoning.encrypted":
		_, has := JxString(obj, "data")
		return has
	case "reasoning.text":
		if _, has := JxString(obj, "text"); !has {
			return false
		}
		if signature, present := obj.Get("signature"); present && signature != nil {
			if _, isString := signature.(string); !isString {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func parseOpenAIReasoningDetails(signature string) []any {
	if signature == "" {
		return nil
	}
	parsed, err := ParseJSONWithRepair(signature)
	if err != nil {
		return nil
	}
	list, ok := parsed.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	for _, detail := range list {
		if !isOpenAIReasoningDetail(detail) {
			return nil
		}
	}
	return list
}

func parseLegacyEncryptedReasoningDetail(signature string) *jsonx.Obj {
	if signature == "" {
		return nil
	}
	parsed, err := ParseJSONWithRepair(signature)
	if err != nil {
		return nil
	}
	obj, ok := JxObj(parsed)
	if !ok || !isOpenAIReasoningDetail(obj) {
		return nil
	}
	if detailType, _ := JxString(obj, "type"); detailType != "reasoning.encrypted" {
		return nil
	}
	id, hasID := JxString(obj, "id")
	data, hasData := JxString(obj, "data")
	if !hasID || len(id) == 0 || !hasData || len(data) == 0 {
		return nil
	}
	return obj
}

// appendOpenAIReasoningDetail merges consecutive text/summary deltas.
func appendOpenAIReasoningDetail(details []any, detail any) []any {
	detailObj, ok := JxObj(detail)
	if !ok {
		return append(details, detail)
	}
	detailType, _ := JxString(detailObj, "type")
	if len(details) > 0 {
		if lastObj, ok := JxObj(details[len(details)-1]); ok {
			lastType, _ := JxString(lastObj, "type")
			switch {
			case detailType == "reasoning.text" && lastType == "reasoning.text":
				lastText, _ := JxString(lastObj, "text")
				detailText, _ := JxString(detailObj, "text")
				lastObj.Set("text", lastText+detailText)
				if _, has := JxString(lastObj, "signature"); !has {
					if signature, hasSig := JxString(detailObj, "signature"); hasSig {
						lastObj.Set("signature", signature)
					}
				}
				fillMissingReasoningDetailFields(lastObj, detailObj)
				return details
			case detailType == "reasoning.summary" && lastType == "reasoning.summary":
				lastSummary, _ := JxString(lastObj, "summary")
				detailSummary, _ := JxString(detailObj, "summary")
				lastObj.Set("summary", lastSummary+detailSummary)
				fillMissingReasoningDetailFields(lastObj, detailObj)
				return details
			}
		}
	}
	cloned := jsonx.NewObj()
	for _, key := range detailObj.Keys() {
		value, _ := detailObj.Get(key)
		cloned.Set(key, value)
	}
	return append(details, cloned)
}

func fillMissingReasoningDetailFields(target, source *jsonx.Obj) {
	if _, has := target.Get("id"); !has {
		if id, hasID := source.Get("id"); hasID {
			target.Set("id", id)
		}
	}
	if _, has := target.Get("format"); !has {
		if format, hasFormat := source.Get("format"); hasFormat {
			target.Set("format", format)
		}
	}
	if _, has := target.Get("index"); !has {
		if index, hasIndex := source.Get("index"); hasIndex {
			target.Set("index", index)
		}
	}
}

// ---------------------------------------------------------------------------
// Message / tool conversion
// ---------------------------------------------------------------------------

func normalizeOpenAIToolCallID(id string, provider string) string {
	if strings.Contains(id, "|") {
		separatorIndex := strings.Index(id, "|")
		callID := sanitizeIDChars(id[:separatorIndex])
		itemID := sanitizeIDChars(id[separatorIndex+1:])
		combinedID := callID
		if len(itemID) > 0 {
			combinedID = callID + "_" + itemID
		}
		if len(combinedID) <= 40 {
			return combinedID
		}
		hash := ShortHash(id)[:8]
		prefix := callID
		if len(prefix) > 40-len(hash)-1 {
			prefix = prefix[:maxInt(1, 40-len(hash)-1)]
		}
		return prefix + "_" + hash
	}
	if provider == "openai" && len(id) > 40 {
		return id[:40]
	}
	return id
}

func sanitizeIDChars(id string) string {
	var builder strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ConvertOpenAICompletionsMessagesOptions mirrors ConvertCompletionsMessagesOptions.
type ConvertOpenAICompletionsMessagesOptions struct {
	GrammarToolInputProperties map[string]string
}

// convertOpenAICompletionsMessages ports convertMessages.
func convertOpenAICompletionsMessages(
	model *Model,
	context *TranscriptContext,
	compat openAICompletionsCompat,
	options *ConvertOpenAICompletionsMessagesOptions,
) []*jsonx.Obj {
	normalizedContext := ResolveTranscript(context, compat.SupportsMidConvoSystemMessages)
	params := []*jsonx.Obj{}
	grammarProperties := map[string]string{}
	if options != nil {
		grammarProperties = options.GrammarToolInputProperties
	}

	normalize := func(id string) string { return normalizeOpenAIToolCallID(id, model.Provider) }
	transformedMessages := TransformMessages(normalizedContext.Messages, model, func(id string, _ *Model, _ *AssistantMessage) string {
		return normalize(id)
	})
	transcriptTools := ResolveTranscriptTools(
		normalizedContext.Messages,
		compat.SupportsMidConvoSystemMessages && compat.SupportsMidConvoToolAdditions,
	)
	instructionRole := "system"
	if model.Reasoning && compat.SupportsDeveloperRole {
		instructionRole = "developer"
	}

	lastRole := ""
	for i := 0; i < len(transformedMessages); i++ {
		msg := transformedMessages[i]
		if compat.RequiresAssistantAfterToolResult && lastRole == "toolResult" {
			if user, isUser := msg.(*UserMessage); isUser {
				params = append(params, jsonx.ObjFrom(
					"role", "assistant",
					"content", "I have processed the tool results.",
				))
				_ = user
			}
		}

		switch t := msg.(type) {
		case *SystemMessage:
			addedTools := []Tool{}
			if i > 0 && transcriptTools.AnchorsAdditions {
				addedTools = t.ToolsAdded
			}
			if len(addedTools) > 0 {
				params = append(params, jsonx.ObjFrom(
					"role", "system",
					"tools", convertOpenAICompletionsTools(addedTools, compat),
				))
			}
			text := RenderSystemMessageUpdate(t)
			if i == 0 {
				text = GetSystemMessageText(t)
			}
			if len(text) > 0 {
				params = append(params, jsonx.ObjFrom(
					"role", instructionRole,
					"content", SanitizeSurrogates(text),
				))
			}

		case *UserMessage:
			if t.Content.IsText {
				params = append(params, jsonx.ObjFrom(
					"role", "user",
					"content", SanitizeSurrogates(t.Content.Text),
				))
			} else {
				var content []any
				for _, item := range t.Content.Blocks {
					if text, ok := item.(TextContent); ok {
						if len(text.Text) == 0 {
							continue
						}
						content = append(content, jsonx.ObjFrom(
							"type", "text",
							"text", SanitizeSurrogates(text.Text),
						))
					} else if image, ok := item.(ImageContent); ok {
						content = append(content, jsonx.ObjFrom(
							"type", "image_url",
							"image_url", jsonx.ObjFrom("url", "data:"+image.MimeType+";base64,"+image.Data),
						))
					}
				}
				if len(content) == 0 {
					lastRole = msg.Role()
					continue
				}
				params = append(params, jsonx.ObjFrom("role", "user", "content", content))
			}

		case *AssistantMessage:
			assistantMsg := jsonx.NewObj()
			assistantMsg.Set("role", "assistant")
			assistantMsg.Set("content", nil)
			if compat.RequiresAssistantAfterToolResult {
				assistantMsg.Set("content", "")
			}

			var assistantTextParts []string
			for _, block := range t.Content {
				if text, ok := block.(TextContent); ok && len(strings.TrimSpace(text.Text)) > 0 {
					assistantTextParts = append(assistantTextParts, SanitizeSurrogates(text.Text))
				}
			}
			assistantText := strings.Join(assistantTextParts, "")

			var thinkingBlocks []ThinkingContent
			var toolCalls []*ToolCall
			for _, block := range t.Content {
				switch b := block.(type) {
				case ThinkingContent:
					thinkingBlocks = append(thinkingBlocks, b)
				case *ToolCall:
					toolCalls = append(toolCalls, b)
				}
			}

			signedReasoningDetails := []any(nil)
			for _, block := range thinkingBlocks {
				signature := ""
				if block.ThinkingSignature != nil {
					signature = *block.ThinkingSignature
				}
				if details := parseOpenAIReasoningDetails(signature); details != nil {
					signedReasoningDetails = details
					break
				}
			}
			var legacyReasoningDetails []any
			for _, call := range toolCalls {
				signature := ""
				if call.ThoughtSignature != nil {
					signature = *call.ThoughtSignature
				}
				if detail := parseLegacyEncryptedReasoningDetail(signature); detail != nil {
					legacyReasoningDetails = append(legacyReasoningDetails, detail)
				}
			}
			preservedReasoningDetails := signedReasoningDetails
			if preservedReasoningDetails == nil && len(legacyReasoningDetails) > 0 {
				preservedReasoningDetails = legacyReasoningDetails
			}

			var nonEmptyThinkingBlocks []ThinkingContent
			for _, block := range thinkingBlocks {
				if len(strings.TrimSpace(block.Thinking)) > 0 {
					nonEmptyThinkingBlocks = append(nonEmptyThinkingBlocks, block)
				}
			}
			if len(nonEmptyThinkingBlocks) > 0 {
				if compat.RequiresThinkingAsText {
					var thinkingTexts []string
					for _, block := range nonEmptyThinkingBlocks {
						thinkingTexts = append(thinkingTexts, SanitizeSurrogates(block.Thinking))
					}
					var textParts []any
					textParts = append(textParts, jsonx.ObjFrom("type", "text", "text", strings.Join(thinkingTexts, "\n\n")))
					for _, part := range assistantTextParts {
						textParts = append(textParts, jsonx.ObjFrom("type", "text", "text", part))
					}
					assistantMsg.Set("content", textParts)
				} else {
					if len(assistantText) > 0 {
						assistantMsg.Set("content", assistantText)
					}
					if preservedReasoningDetails == nil {
						signature := ""
						if nonEmptyThinkingBlocks[0].ThinkingSignature != nil {
							signature = *nonEmptyThinkingBlocks[0].ThinkingSignature
						}
						if model.Provider == "opencode-go" && signature == "reasoning" {
							signature = "reasoning_content"
						}
						if isOpenAICompletionsReasoningField(signature) {
							var thinkingTexts []string
							for _, block := range nonEmptyThinkingBlocks {
								thinkingTexts = append(thinkingTexts, block.Thinking)
							}
							assistantMsg.Set(signature, strings.Join(thinkingTexts, "\n"))
						}
					}
				}
			} else if len(assistantText) > 0 {
				assistantMsg.Set("content", assistantText)
			}

			if len(toolCalls) > 0 {
				var toolCallParams []any
				for _, call := range toolCalls {
					if customInputProperty, has := grammarProperties[call.Name]; has {
						input := ""
						if call.Arguments != nil {
							if value, present := call.Arguments.Get(customInputProperty); present {
								if s, isString := value.(string); isString {
									input = s
								}
							}
						}
						toolCallParams = append(toolCallParams, jsonx.ObjFrom(
							"id", call.ID,
							"type", "custom",
							"custom", jsonx.ObjFrom("name", call.Name, "input", SanitizeSurrogates(input)),
						))
						continue
					}
					arguments := "{}"
					if call.Arguments != nil {
						arguments = jsonx.Stringify(call.Arguments)
					}
					toolCallParams = append(toolCallParams, jsonx.ObjFrom(
						"id", call.ID,
						"type", "function",
						"function", jsonx.ObjFrom("name", call.Name, "arguments", arguments),
					))
				}
				assistantMsg.Set("tool_calls", toolCallParams)
			}
			if preservedReasoningDetails != nil {
				assistantMsg.Set("reasoning_details", preservedReasoningDetails)
			}
			if compat.RequiresReasoningContentOnAssistantMessages && model.Reasoning {
				if _, has := assistantMsg.Get("reasoning_content"); !has {
					assistantMsg.Set("reasoning_content", "")
				}
			}
			// Skip assistant messages with no content and no tool calls.
			contentValue, hasContent := assistantMsg.Get("content")
			hasContentValue := hasContent && contentValue != nil
			if hasContentValue {
				if text, isString := contentValue.(string); isString {
					hasContentValue = len(text) > 0
				} else if list, isList := contentValue.([]any); isList {
					hasContentValue = len(list) > 0
				}
			}
			_, hasToolCalls := assistantMsg.Get("tool_calls")
			if !hasContentValue && !hasToolCalls {
				lastRole = msg.Role()
				continue
			}
			params = append(params, assistantMsg)

		case *ToolResultMessage:
			var imageBlocks []any
			j := i
			for ; j < len(transformedMessages); j++ {
				toolMsg, ok := transformedMessages[j].(*ToolResultMessage)
				if !ok {
					break
				}
				var textParts []string
				hasImages := false
				for _, block := range toolMsg.Content {
					if text, isText := block.(TextContent); isText {
						textParts = append(textParts, text.Text)
					} else if block.ContentType() == "image" {
						hasImages = true
					}
				}
				textResult := strings.Join(textParts, "\n")
				toolResultText := "(no tool output)"
				if len(textResult) > 0 {
					toolResultText = textResult
				} else if hasImages {
					toolResultText = "(see attached image)"
				}
				toolResultMsg := jsonx.ObjFrom(
					"role", "tool",
					"content", SanitizeSurrogates(toolResultText),
					"tool_call_id", toolMsg.ToolCallID,
				)
				if compat.RequiresToolResultName && toolMsg.ToolName != "" {
					toolResultMsg.Set("name", toolMsg.ToolName)
				}
				params = append(params, toolResultMsg)

				if hasImages && modelSupportsImages(model) {
					for _, block := range toolMsg.Content {
						if image, isImage := block.(ImageContent); isImage {
							imageBlocks = append(imageBlocks, jsonx.ObjFrom(
								"type", "image_url",
								"image_url", jsonx.ObjFrom("url", "data:"+image.MimeType+";base64,"+image.Data),
							))
						}
					}
				}
			}
			i = j - 1

			if len(imageBlocks) > 0 {
				if compat.RequiresAssistantAfterToolResult {
					params = append(params, jsonx.ObjFrom(
						"role", "assistant",
						"content", "I have processed the tool results.",
					))
				}
				content := []any{jsonx.ObjFrom("type", "text", "text", "Attached image(s) from tool result:")}
				content = append(content, imageBlocks...)
				params = append(params, jsonx.ObjFrom("role", "user", "content", content))
				lastRole = "user"
			} else {
				lastRole = "toolResult"
			}
			continue
		}

		lastRole = msg.Role()
	}

	return params
}

func isOpenAICompletionsReasoningField(field string) bool {
	switch field {
	case "reasoning", "reasoning_content", "reasoning_text":
		return true
	}
	return false
}

func convertOpenAICompletionsTools(tools []Tool, compat openAICompletionsCompat) []any {
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		grammar, err := ResolveGrammarConstrainedSampling(&tool, compat.SupportsOpenAIGrammarTools)
		if err != nil {
			panic(err)
		}
		if grammar != nil {
			out = append(out, jsonx.ObjFrom(
				"type", "custom",
				"custom", jsonx.ObjFrom(
					"name", tool.Name,
					"description", tool.Description,
					"format", jsonx.ObjFrom(
						"type", "grammar",
						"grammar", jsonx.ObjFrom("syntax", grammar.Format, "definition", grammar.Definition),
					),
				),
			))
			continue
		}
		strict, err := ResolveJSONSchemaStrictSampling(&tool, compat.SupportsStrictMode)
		if err != nil {
			panic(err)
		}
		parameters, err := GetJSONSchemaToolParameters(&tool, strict)
		if err != nil {
			panic(err)
		}
		var parametersValue any = jsonx.NewObj()
		if obj, ok := JxObj(parameters); ok {
			parametersValue = obj
		}
		function := jsonx.NewObj()
		function.Set("name", tool.Name)
		function.Set("description", tool.Description)
		function.Set("parameters", parametersValue)
		if compat.SupportsStrictMode {
			function.Set("strict", strict)
		}
		out = append(out, jsonx.ObjFrom("type", "function", "function", function))
	}
	return out
}

// ---------------------------------------------------------------------------
// Params assembly
// ---------------------------------------------------------------------------

func getCompatCacheControl(compat openAICompletionsCompat, cacheRetention string) openAICompatCacheControl {
	if compat.CacheControlFormat != "anthropic" || cacheRetention == CacheRetentionNone {
		return nil
	}
	if cacheRetention == CacheRetentionLong && compat.SupportsLongCacheRetention {
		return jsonx.ObjFrom("type", "ephemeral", "ttl", "1h")
	}
	return jsonx.ObjFrom("type", "ephemeral")
}

func applyAnthropicCacheControl(messages []*jsonx.Obj, tools []any, cacheControl openAICompatCacheControl) {
	addCacheControlToSystemPrompt(messages, cacheControl)
	addCacheControlToLastTool(tools, cacheControl)
	addCacheControlToLastConversationMessage(messages, cacheControl)
}

func addCacheControlToSystemPrompt(messages []*jsonx.Obj, cacheControl openAICompatCacheControl) {
	for _, message := range messages {
		if role, _ := JxString(message, "role"); role == "system" || role == "developer" {
			addCacheControlToTextContent(message, cacheControl)
			return
		}
	}
}

func addCacheControlToLastConversationMessage(messages []*jsonx.Obj, cacheControl openAICompatCacheControl) {
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := JxString(messages[i], "role")
		if role == "user" || role == "assistant" || role == "tool" {
			if addCacheControlToTextContent(messages[i], cacheControl) {
				return
			}
		}
	}
}

func addCacheControlToLastTool(tools []any, cacheControl openAICompatCacheControl) {
	if len(tools) == 0 {
		return
	}
	if tool, ok := JxObj(tools[len(tools)-1]); ok {
		tool.Set("cache_control", cacheControl)
	}
}

func addCacheControlToTextContent(message *jsonx.Obj, cacheControl openAICompatCacheControl) bool {
	contentValue, has := message.Get("content")
	if !has || contentValue == nil {
		return false
	}
	if text, isString := contentValue.(string); isString {
		if len(text) == 0 {
			return false
		}
		message.Set("content", []any{jsonx.ObjFrom("type", "text", "text", text, "cache_control", cacheControl)})
		return true
	}
	content, isList := contentValue.([]any)
	if !isList {
		return false
	}
	for i := len(content) - 1; i >= 0; i-- {
		if part, ok := JxObj(content[i]); ok {
			if partType, _ := JxString(part, "type"); partType == "text" {
				part.Set("cache_control", cacheControl)
				return true
			}
		}
	}
	return false
}

func resolveThinkingTokenBudgetField(compat openAICompletionsCompat) string {
	if compat.ThinkingTokenBudgetField != "" {
		return compat.ThinkingTokenBudgetField
	}
	if compat.SupportsThinkingTokenBudget {
		return "thinking_token_budget"
	}
	return ""
}

func resolveClampedThinkingBudget(model *Model, options *OpenAICompletionsOptions, params *jsonx.Obj) (float64, bool) {
	if options == nil || options.ReasoningEffort == nil || !model.Reasoning {
		return 0, false
	}
	ceiling := model.MaxTokens
	if value, has := JxFloat(params, "max_tokens"); has {
		ceiling = value
	} else if value, has := JxFloat(params, "max_completion_tokens"); has {
		ceiling = value
	}
	budget := ClampThinkingBudgetToAnswerRoom(
		ThinkingBudgetForLevel(*options.ReasoningEffort, options.ThinkingBudgets),
		ceiling,
	)
	if budget > 0 {
		return budget, true
	}
	return 0, false
}

// thinkingLevelMapLookup returns (mapped, defined, isNull).
func thinkingLevelMapLookup(model *Model, level string) (string, bool, bool) {
	if model.ThinkingLevelMap == nil {
		return "", false, false
	}
	mapped, present := model.ThinkingLevelMap[level]
	if !present {
		return "", false, false
	}
	if mapped == nil {
		return "", true, true
	}
	return *mapped, true, false
}

func buildChatTemplateValues(model *Model, options *OpenAICompletionsOptions, values *jsonx.Obj, thinkingBudget *float64) *jsonx.Obj {
	if values == nil {
		return nil
	}
	resolved := jsonx.NewObj()
	for _, key := range values.Keys() {
		value, _ := values.Get(key)
		if resolvedValue, ok := resolveChatTemplateKwargValue(model, options, value, thinkingBudget); ok {
			resolved.Set(key, resolvedValue)
		}
	}
	if resolved.Len() == 0 {
		return nil
	}
	return resolved
}

func resolveChatTemplateKwargValue(model *Model, options *OpenAICompletionsOptions, value any, thinkingBudget *float64) (any, bool) {
	valueObj, isObj := JxObj(value)
	if !isObj {
		return value, true
	}
	var reasoningEffort string
	if options != nil && options.ReasoningEffort != nil {
		reasoningEffort = *options.ReasoningEffort
	}
	if omitWhenOff, has := JxBool(valueObj, "omitWhenOff"); has && omitWhenOff && reasoningEffort == "" {
		return nil, false
	}
	if variable, has := JxString(valueObj, "$var"); has {
		switch variable {
		case "thinking.enabled":
			return reasoningEffort != "", true
		case "thinking.budget":
			if thinkingBudget != nil {
				return *thinkingBudget, true
			}
			return nil, false
		}
	}
	level := reasoningEffort
	if level == "" {
		level = ThinkingOff
	}
	mapped, defined, isNull := thinkingLevelMapLookup(model, level)
	if !defined {
		return reasoningEffort, reasoningEffort != ""
	}
	if isNull {
		return nil, false
	}
	return mapped, true
}

// buildOpenAICompletionsParams ports buildParams.
func buildOpenAICompletionsParams(
	model *Model,
	context *TranscriptContext,
	options *OpenAICompletionsOptions,
	compat openAICompletionsCompat,
	cacheRetention string,
	grammarToolInputProperties map[string]string,
) *jsonx.Obj {
	transcriptTools := ResolveTranscriptTools(
		context.Messages,
		compat.SupportsMidConvoSystemMessages && compat.SupportsMidConvoToolAdditions,
	)
	messages := convertOpenAICompletionsMessages(model, context, compat, &ConvertOpenAICompletionsMessagesOptions{
		GrammarToolInputProperties: grammarToolInputProperties,
	})
	cacheControl := getCompatCacheControl(compat, cacheRetention)

	var sessionID string
	if options != nil && options.SessionID != nil {
		sessionID = *options.SessionID
	}

	params := jsonx.NewObj()
	params.Set("model", model.ID)
	messageValues := make([]any, 0, len(messages))
	for _, message := range messages {
		messageValues = append(messageValues, message)
	}
	params.Set("messages", messageValues)
	params.Set("stream", true)
	if (strings.Contains(model.BaseURL, "api.openai.com") && cacheRetention != CacheRetentionNone) ||
		(cacheRetention == CacheRetentionLong && compat.SupportsLongCacheRetention) {
		if key, ok := ClampOpenAIPromptCacheKey(sessionID); ok {
			params.Set("prompt_cache_key", key)
		}
	}
	if cacheRetention == CacheRetentionLong && compat.SupportsLongCacheRetention {
		params.Set("prompt_cache_retention", "24h")
	}

	if compat.SupportsUsageInStreaming {
		params.Set("stream_options", jsonx.ObjFrom("include_usage", true))
	}
	if compat.SupportsStore {
		params.Set("store", false)
	}

	if options != nil && options.MaxTokens != nil {
		if compat.MaxTokensField == "max_tokens" {
			params.Set("max_tokens", *options.MaxTokens)
		} else {
			params.Set("max_completion_tokens", *options.MaxTokens)
		}
	}

	if options != nil && options.Temperature != nil {
		params.Set("temperature", *options.Temperature)
	}

	var tools []any
	if len(transcriptTools.RequestTools) > 0 {
		tools = convertOpenAICompletionsTools(transcriptTools.RequestTools, compat)
		params.Set("tools", tools)
		if compat.ZaiToolStream {
			params.Set("tool_stream", true)
		}
	} else if hasToolHistory(context.Messages) {
		params.Set("tools", []any{})
	}

	if cacheControl != nil {
		applyAnthropicCacheControl(messages, tools, cacheControl)
	}

	if options != nil {
		if options.ToolChoice != nil {
			params.Set("tool_choice", jsonx.ObjFrom("type", *options.ToolChoice))
		} else if options.ToolChoiceTool != nil {
			params.Set("tool_choice", jsonx.ObjFrom("type", "tool", "function", jsonx.ObjFrom("name", *options.ToolChoiceTool)))
		}
	}

	if compat.VllmPriority != nil {
		params.Set("priority", *compat.VllmPriority)
	}

	thinkingTokenBudgetField := resolveThinkingTokenBudgetField(compat)
	thinkingBudget, hasThinkingBudget := resolveClampedThinkingBudget(model, options, params)

	var reasoningEffort string
	if options != nil && options.ReasoningEffort != nil {
		reasoningEffort = *options.ReasoningEffort
	}

	mappedEffort := func(level string) (string, bool) {
		mapped, defined, isNull := thinkingLevelMapLookup(model, level)
		if !defined {
			return level, true
		}
		if isNull {
			return "", false
		}
		return mapped, true
	}
	offValue := func() (string, bool) {
		mapped, defined, isNull := thinkingLevelMapLookup(model, ThinkingOff)
		if !defined {
			return "", false
		}
		if isNull {
			return "", false
		}
		return mapped, true
	}

	switch {
	case compat.ThinkingFormat == "zai" && model.Reasoning:
		if reasoningEffort != "" {
			params.Set("thinking", jsonx.ObjFrom("type", "enabled", "clear_thinking", false))
		} else {
			params.Set("thinking", jsonx.ObjFrom("type", "disabled"))
		}
		if reasoningEffort != "" && compat.SupportsReasoningEffort {
			if effort, ok := mappedEffort(reasoningEffort); ok {
				params.Set("reasoning_effort", effort)
			}
		}
	case compat.ThinkingFormat == "qwen" && model.Reasoning:
		params.Set("enable_thinking", reasoningEffort != "")
		if reasoningEffort != "" && compat.SupportsReasoningEffort {
			if effort, ok := mappedEffort(reasoningEffort); ok {
				params.Set("reasoning_effort", effort)
			}
		}
	case compat.ThinkingFormat == "qwen-chat-template" && model.Reasoning:
		params.Set("chat_template_kwargs", jsonx.ObjFrom("enable_thinking", reasoningEffort != "", "preserve_thinking", true))
	case compat.ThinkingFormat == "chat-template" && model.Reasoning:
		if kwargs := buildChatTemplateValues(model, options, compat.ChatTemplateKwargs, budgetPtr(thinkingBudget, hasThinkingBudget)); kwargs != nil {
			params.Set("chat_template_kwargs", kwargs)
		}
	case compat.ThinkingFormat == "baseten" && model.Reasoning:
		if args := buildChatTemplateValues(model, options, compat.ChatTemplateArgs, budgetPtr(thinkingBudget, hasThinkingBudget)); args != nil {
			params.Set("chat_template_args", args)
		}
		if compat.SupportsReasoningEffort {
			if reasoningEffort != "" {
				if effort, ok := mappedEffort(reasoningEffort); ok {
					params.Set("reasoning_effort", effort)
				}
			} else if effort, ok := offValue(); ok {
				params.Set("reasoning_effort", effort)
			}
		}
	case compat.ThinkingFormat == "deepseek" && model.Reasoning:
		if reasoningEffort != "" {
			params.Set("thinking", jsonx.ObjFrom("type", "enabled"))
		} else {
			_, _, offNull := thinkingLevelMapLookup(model, ThinkingOff)
			if !offNull {
				params.Set("thinking", jsonx.ObjFrom("type", "disabled"))
			}
		}
		if reasoningEffort != "" && compat.SupportsReasoningEffort {
			if effort, ok := mappedEffort(reasoningEffort); ok {
				params.Set("reasoning_effort", effort)
			}
		}
	case compat.ThinkingFormat == "openrouter" && model.Reasoning:
		if reasoningEffort != "" {
			if effort, ok := mappedEffort(reasoningEffort); ok {
				params.Set("reasoning", jsonx.ObjFrom("effort", effort))
			}
		} else {
			_, _, offNull := thinkingLevelMapLookup(model, ThinkingOff)
			if !offNull {
				effort := "none"
				if mapped, ok := offValue(); ok {
					effort = mapped
				}
				params.Set("reasoning", jsonx.ObjFrom("effort", effort))
			}
		}
	case compat.ThinkingFormat == "ant-ling" && model.Reasoning && reasoningEffort != "":
		if mapped, defined, isNull := thinkingLevelMapLookup(model, reasoningEffort); defined && !isNull {
			params.Set("reasoning", jsonx.ObjFrom("effort", mapped))
		}
	case compat.ThinkingFormat == "together" && model.Reasoning:
		params.Set("reasoning", jsonx.ObjFrom("enabled", reasoningEffort != ""))
		if reasoningEffort != "" && compat.SupportsReasoningEffort {
			if effort, ok := mappedEffort(reasoningEffort); ok {
				params.Set("reasoning_effort", effort)
			}
		}
	case compat.ThinkingFormat == "string-thinking" && model.Reasoning:
		if reasoningEffort != "" {
			if effort, ok := mappedEffort(reasoningEffort); ok {
				params.Set("thinking", effort)
			}
		} else {
			_, _, offNull := thinkingLevelMapLookup(model, ThinkingOff)
			if !offNull {
				effort := "none"
				if mapped, ok := offValue(); ok {
					effort = mapped
				}
				params.Set("thinking", effort)
			}
		}
	case reasoningEffort != "" && model.Reasoning && compat.SupportsReasoningEffort:
		if effort, ok := mappedEffort(reasoningEffort); ok {
			params.Set("reasoning_effort", effort)
		}
	case reasoningEffort == "" && model.Reasoning && compat.SupportsReasoningEffort:
		if effort, ok := offValue(); ok {
			params.Set("reasoning_effort", effort)
		}
	}

	if thinkingTokenBudgetField != "" && hasThinkingBudget {
		params.Set(thinkingTokenBudgetField, thinkingBudget)
	}

	if compat.OpenRouterRouting != nil && compat.OpenRouterRouting.Len() > 0 {
		params.Set("provider", compat.OpenRouterRouting)
	}

	if compat.VercelGatewayRouting != nil {
		gateway := jsonx.NewObj()
		if only, ok := JxList(compat.VercelGatewayRouting, "only"); ok {
			gateway.Set("only", only)
		}
		if order, ok := JxList(compat.VercelGatewayRouting, "order"); ok {
			gateway.Set("order", order)
		}
		if gateway.Len() > 0 {
			params.Set("providerOptions", jsonx.ObjFrom("gateway", gateway))
		}
	}

	// Last so custom keys override named request fields; per-request keys
	// override model defaults.
	for name, value := range model.SamplingParams {
		params.Set(name, value)
	}
	if options != nil {
		for name, value := range options.SamplingParams {
			params.Set(name, value)
		}
	}

	return params
}

func budgetPtr(budget float64, ok bool) *float64 {
	if !ok {
		return nil
	}
	return &budget
}

// ---------------------------------------------------------------------------
// Usage / stop reasons
// ---------------------------------------------------------------------------

func parseChunkUsage(rawUsage *jsonx.Obj, model *Model) Usage {
	promptTokens := jxf(rawUsage, "prompt_tokens")
	cacheReadTokens := 0.0
	if details, ok := JxObjectField(rawUsage, "prompt_tokens_details"); ok {
		if value, present := JxFloat(details, "cached_tokens"); present {
			cacheReadTokens = value
		}
	}
	if cacheReadTokens == 0 {
		if value, present := JxFloat(rawUsage, "prompt_cache_hit_tokens"); present {
			cacheReadTokens = value
		}
	}
	if cacheReadTokens == 0 {
		if value, present := JxFloat(rawUsage, "cached_tokens"); present {
			cacheReadTokens = value
		}
	}
	cacheWriteTokens := 0.0
	if details, ok := JxObjectField(rawUsage, "prompt_tokens_details"); ok {
		if value, present := JxFloat(details, "cache_write_tokens"); present {
			cacheWriteTokens = value
		}
	}

	input := promptTokens - cacheReadTokens - cacheWriteTokens
	if input < 0 {
		input = 0
	}
	outputTokens := jxf(rawUsage, "completion_tokens")
	reasoning := 0.0
	if details, ok := JxObjectField(rawUsage, "completion_tokens_details"); ok {
		if value, present := JxFloat(details, "reasoning_tokens"); present {
			reasoning = value
		}
	}
	usage := Usage{
		Input:       input,
		Output:      outputTokens,
		CacheRead:   cacheReadTokens,
		CacheWrite:  cacheWriteTokens,
		Reasoning:   &reasoning,
		TotalTokens: input + outputTokens + cacheReadTokens + cacheWriteTokens,
		Cost:        UsageCost{},
	}
	CalculateCost(model, &usage)
	return usage
}

// mapOpenAIStopReason ports mapStopReason.
func mapOpenAIStopReason(reason string) (string, *string) {
	switch reason {
	case "":
		return StopStop, nil // null finish_reason
	case "stop", "end":
		return StopStop, nil
	case "length":
		return StopLength, nil
	case "function_call", "tool_calls":
		return StopToolUse, nil
	case "content_filter":
		message := "Provider finish_reason: content_filter"
		return StopError, &message
	case "network_error":
		message := "Provider finish_reason: network_error"
		return StopError, &message
	default:
		message := "Provider finish_reason: " + reason
		return StopError, &message
	}
}
