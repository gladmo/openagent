package ai

// api_anthropic_messages.go ports api/anthropic-messages.ts: the Anthropic
// Messages streaming client. The TS implementation rides on @anthropic-ai/sdk
// 0.124.0; this port speaks the same wire protocol directly (POST
// {baseURL}/v1/messages?beta=true with x-api-key/Bearer auth, SSE events,
// the SDK's error message composition, and its retry policy via
// RetryProviderRequest).

import (
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// Anthropic effort levels (adaptive thinking).
const (
	AnthropicEffortLow    = "low"
	AnthropicEffortMedium = "medium"
	AnthropicEffortHigh   = "high"
	AnthropicEffortXHigh  = "xhigh"
	AnthropicEffortMax    = "max"
)

// Anthropic thinking display modes.
const (
	AnthropicThinkingDisplaySummarized = "summarized"
	AnthropicThinkingDisplayOmitted    = "omitted"
)

// Beta feature identifiers.
const (
	anthropicFineGrainedToolStreamingBeta = "fine-grained-tool-streaming-2025-05-14"
	anthropicInterleavedThinkingBeta      = "interleaved-thinking-2025-05-14"
	anthropicServerSideFallbackBeta       = "server-side-fallback-2026-07-01"
	anthropicMidConvoOutputConfigBeta     = "mid-conversation-output-config-2026-07-01"
	anthropicThinkingBindingControlsBeta  = "thinking-binding-controls-2026-08-01"
	anthropicMidConvoToolChangesBeta      = "mid-conversation-tool-changes-2026-07-01"
	anthropicClaudeCodeBeta               = "claude-code-20250219"
	anthropicOAuthBeta                    = "oauth-2025-04-20"
)

// Claude Code stealth-mode constants.
const claudeCodeVersion = "2.1.280"

var claudeCodeTools = []string{
	"Read", "Write", "Edit", "Bash", "Grep", "Glob", "AskUserQuestion",
	"EnterPlanMode", "ExitPlanMode", "KillShell", "NotebookEdit", "Skill",
	"Task", "TaskOutput", "TodoWrite", "WebFetch", "WebSearch",
}

func claudeCodeToolLookup() map[string]string {
	lookup := make(map[string]string, len(claudeCodeTools))
	for _, tool := range claudeCodeTools {
		lookup[strings.ToLower(tool)] = tool
	}
	return lookup
}

// ToClaudeCodeName converts a tool name to Claude Code canonical casing
// (case-insensitive match).
func ToClaudeCodeName(name string) string {
	if canonical, ok := claudeCodeToolLookup()[strings.ToLower(name)]; ok {
		return canonical
	}
	return name
}

// FromClaudeCodeName maps a Claude Code tool name back to the declared tool's
// spelling.
func FromClaudeCodeName(name string, tools []Tool) string {
	if len(tools) > 0 {
		lower := strings.ToLower(name)
		for _, tool := range tools {
			if strings.ToLower(tool.Name) == lower {
				return tool.Name
			}
		}
	}
	return name
}

// AnthropicToolChoice forces a specific tool.
type AnthropicToolChoice struct {
	Name string
}

// AnthropicOptions mirrors the TS api-specific stream options.
type AnthropicOptions struct {
	StreamOptions
	ThinkingEnabled      *bool
	ThinkingBudgetTokens *float64
	Effort               *string
	ThinkingDisplay      *string // "summarized" | "omitted"
	InterleavedThinking  *bool
	// ToolChoice: nil = omitted; otherwise "auto"|"any"|"none" or a forced
	// tool.
	ToolChoice     *string
	ToolChoiceTool *AnthropicToolChoice
}

// anthropicCompat is the resolved compat view (getAnthropicCompat).
type anthropicCompat struct {
	SupportsEagerToolInputStreaming bool
	SupportsLongCacheRetention      bool
	SendSessionAffinityHeaders      bool
	SessionAffinityFormat           string // "" | "openrouter"
	SupportsCacheControlOnTools     bool
	SupportsTemperature             bool
	AllowEmptySignature             bool
	SupportsStrictTools             bool
	SupportsMidConvoSystemMessages  bool
	SupportsMidConvoToolChanges     bool
	SupportsMidConvoEffort          bool
	ForceAdaptiveThinking           bool
}

type anthropicFallbackModel struct {
	Provider string
	Model    string
	Cost     *ModelCost
}

func anthropicAllowedFallbackModels(model *Model) []anthropicFallbackModel {
	compat, ok := JxCompatObj(model)
	if !ok {
		return nil
	}
	list, ok := JxList(compat, "allowedFallbackModels")
	if !ok {
		return nil
	}
	var out []anthropicFallbackModel
	for _, entry := range list {
		obj, isObj := JxObj(entry)
		if !isObj {
			continue
		}
		fallback := anthropicFallbackModel{}
		fallback.Provider, _ = JxString(obj, "provider")
		fallback.Model, _ = JxString(obj, "model")
		if costObj, hasCost := JxObjectField(obj, "cost"); hasCost {
			cost := &ModelCost{}
			cost.Input, _ = JxFloat(costObj, "input")
			cost.Output, _ = JxFloat(costObj, "output")
			cost.CacheRead, _ = JxFloat(costObj, "cacheRead")
			cost.CacheWrite, _ = JxFloat(costObj, "cacheWrite")
			fallback.Cost = cost
		}
		out = append(out, fallback)
	}
	return out
}

func anthropicSupportsMidConvoEffort(model *Model) bool {
	return JxCompatBool(model, "supportsMidConvoEffort", false)
}

func anthropicForceAdaptiveThinking(model *Model) bool {
	return JxCompatBool(model, "forceAdaptiveThinking", false)
}

func getAnthropicCompat(model *Model) anthropicCompat {
	isOpenRouter := model.Provider == "openrouter" || strings.Contains(model.BaseURL, "openrouter.ai")
	compat := anthropicCompat{
		SupportsEagerToolInputStreaming: JxCompatBool(model, "supportsEagerToolInputStreaming", true),
		SupportsLongCacheRetention:      JxCompatBool(model, "supportsLongCacheRetention", true),
		SupportsCacheControlOnTools:     JxCompatBool(model, "supportsCacheControlOnTools", true),
		SupportsTemperature:             JxCompatBool(model, "supportsTemperature", true),
		AllowEmptySignature:             JxCompatBool(model, "allowEmptySignature", false),
		SupportsStrictTools:             JxCompatBool(model, "supportsStrictTools", false),
		SupportsMidConvoSystemMessages:  JxCompatBool(model, "supportsMidConvoSystemMessages", false),
		SupportsMidConvoToolChanges:     JxCompatBool(model, "supportsMidConvoToolChanges", false),
		SupportsMidConvoEffort:          anthropicSupportsMidConvoEffort(model),
		ForceAdaptiveThinking:           anthropicForceAdaptiveThinking(model),
	}
	compat.SendSessionAffinityHeaders = JxCompatBool(model, "sendSessionAffinityHeaders", isOpenRouter)
	if format, ok := func() (string, bool) {
		if obj, has := JxCompatObj(model); has {
			return JxString(obj, "sessionAffinityFormat")
		}
		return "", false
	}(); ok {
		compat.SessionAffinityFormat = format
	} else if isOpenRouter {
		compat.SessionAffinityFormat = "openrouter"
	}
	return compat
}

// anthropicThinkingLevelMapValue reads model.thinkingLevelMap[level]:
// (value, present). present=false maps to TS undefined.
func anthropicThinkingLevelMapValue(model *Model, level string) (string, bool, bool) {
	if model.ThinkingLevelMap == nil {
		return "", false, false
	}
	mapped, present := model.ThinkingLevelMap[level]
	if !present || mapped == nil {
		if present && mapped == nil {
			return "", true, true // null
		}
		return "", false, false
	}
	return *mapped, true, false
}

// ---------------------------------------------------------------------------
// Client / request construction
// ---------------------------------------------------------------------------

// mergeHTTPHeaderRecords merges records case-insensitively; later sources win.
func mergeHTTPHeaderRecords(sources ...map[string]string) map[string]string {
	merged := map[string]string{}
	lowerKeys := map[string]string{}
	for _, source := range sources {
		for name, value := range source {
			lower := strings.ToLower(name)
			if existing, ok := lowerKeys[lower]; ok {
				delete(merged, existing)
			}
			merged[name] = value
			lowerKeys[lower] = name
		}
	}
	return merged
}

func providerHeadersToRecord(headers ProviderHeaders) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := map[string]string{}
	for name, value := range headers {
		if value != nil {
			out[name] = *value
		}
	}
	return out
}

func hasAuthHeader(headers map[string]string, name string) bool {
	expected := strings.ToLower(name)
	for key, value := range headers {
		if strings.ToLower(key) == expected && len(strings.TrimSpace(value)) > 0 {
			return true
		}
	}
	return false
}

func anthropicAssertRequestAuth(provider string, apiKey string, headers map[string]string) error {
	if apiKey != "" {
		return nil
	}
	if hasAuthHeader(headers, "authorization") || hasAuthHeader(headers, "x-api-key") || hasAuthHeader(headers, "cf-aig-authorization") {
		return nil
	}
	return &csError{msg: "No API key for provider: " + provider}
}

func isOAuthToken(apiKey string) bool {
	return strings.Contains(apiKey, "sk-ant-oat")
}

// anthropicClientConfig is the resolved per-request transport config
// (createClient): base URL, auth style, and merged default headers.
type anthropicClientConfig struct {
	BaseURL      string
	XAPIKey      string // "" = omit
	BearerToken  string // "" = omit
	Headers      map[string]string
	IsOAuthToken bool
}

func createAnthropicClient(model *Model, apiKey string, optionsHeaders ProviderHeaders, dynamicHeaders map[string]string, sessionID string) anthropicClientConfig {
	piHeaders := map[string]string{
		"User-Agent": GetPiUserAgent(),
		"accept":     "application/json",
		"anthropic-dangerous-direct-browser-access": "true",
	}
	optionsRecord := providerHeadersToRecord(optionsHeaders)

	// Copilot: Bearer auth.
	if model.Provider == "github-copilot" {
		headers := mergeHTTPHeaderRecords(piHeaders, model.Headers, dynamicHeaders, optionsRecord)
		return anthropicClientConfig{
			BaseURL:     model.BaseURL,
			BearerToken: apiKey,
			Headers:     headers,
		}
	}

	// OAuth: Bearer auth, Claude Code identity headers.
	if apiKey != "" && isOAuthToken(apiKey) {
		headers := mergeHTTPHeaderRecords(
			piHeaders,
			map[string]string{
				"user-agent": "claude-cli/" + claudeCodeVersion,
				"x-app":      "cli",
			},
			model.Headers,
			optionsRecord,
		)
		return anthropicClientConfig{
			BaseURL:      model.BaseURL,
			BearerToken:  apiKey,
			Headers:      headers,
			IsOAuthToken: true,
		}
	}

	// API key or header-owned auth.
	compat := getAnthropicCompat(model)
	extra := map[string]string{}
	if sessionID != "" && compat.SendSessionAffinityHeaders {
		header := "x-session-affinity"
		if compat.SessionAffinityFormat == "openrouter" {
			header = "x-session-id"
		}
		extra[header] = sessionID
	}
	headers := mergeHTTPHeaderRecords(piHeaders, extra, model.Headers, optionsRecord)
	return anthropicClientConfig{
		BaseURL: model.BaseURL,
		XAPIKey: apiKey,
		Headers: headers,
	}
}

// buildAnthropicRequestHeaders composes the final wire headers (SDK order:
// platform defaults, auth, client default headers, per-request headers).
func buildAnthropicRequestHeaders(client anthropicClientConfig) map[string]string {
	headers := map[string]string{
		"anthropic-version": "2023-06-01",
	}
	if client.XAPIKey != "" {
		headers["x-api-key"] = client.XAPIKey
	}
	if client.BearerToken != "" {
		headers["Authorization"] = "Bearer " + client.BearerToken
	}
	for name, value := range client.Headers {
		headers[name] = value
	}
	return headers
}

// ---------------------------------------------------------------------------
// Message conversion
// ---------------------------------------------------------------------------

// normalizeToolCallId forces ids into Anthropic's ^[a-zA-Z0-9_-]+$ (max 64).
func normalizeToolCallId(id string) string {
	return normalizeAnthropicToolCallId(id)
}

func normalizeAnthropicToolCallId(id string) string {
	var builder strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	normalized := builder.String()
	if len(normalized) > 64 {
		return normalized[:64]
	}
	return normalized
}

// convertContentBlocks converts text/image blocks to the Anthropic content
// shape: a plain string when no images, otherwise a block array.
func convertContentBlocks(content []ContentBlock) any {
	hasImages := false
	for _, block := range content {
		if block.ContentType() == "image" {
			hasImages = true
			break
		}
	}
	if !hasImages {
		texts := make([]string, 0, len(content))
		for _, block := range content {
			if text, ok := block.(TextContent); ok {
				texts = append(texts, text.Text)
			}
		}
		return SanitizeSurrogates(strings.Join(texts, "\n"))
	}

	var blocks []any
	for _, block := range content {
		if text, ok := block.(TextContent); ok {
			blocks = append(blocks, jsonx.ObjFrom(
				"type", "text",
				"text", SanitizeSurrogates(text.Text),
			))
			continue
		}
		if image, ok := block.(ImageContent); ok {
			blocks = append(blocks, jsonx.ObjFrom(
				"type", "image",
				"source", jsonx.ObjFrom(
					"type", "base64",
					"media_type", image.MimeType,
					"data", image.Data,
				),
			))
		}
	}
	hasText := false
	for _, block := range blocks {
		if obj, ok := JxObj(block); ok {
			if t, _ := JxString(obj, "type"); t == "text" {
				hasText = true
				break
			}
		}
	}
	if !hasText {
		blocks = append([]any{jsonx.ObjFrom("type", "text", "text", "(see attached image)")}, blocks...)
	}
	return blocks
}

func convertToolResultMessage(msg *ToolResultMessage) *jsonx.Obj {
	return jsonx.ObjFrom(
		"type", "tool_result",
		"tool_use_id", msg.ToolCallID,
		"content", convertContentBlocks(msg.Content),
		"is_error", msg.IsError,
	)
}

type convertedAnthropicMessages struct {
	messages        []*jsonx.Obj
	assistantLevels map[int]string // params index -> effort
}

func isAnthropicEffort(value string) bool {
	switch value {
	case AnthropicEffortLow, AnthropicEffortMedium, AnthropicEffortHigh, AnthropicEffortXHigh, AnthropicEffortMax:
		return true
	}
	return false
}

func convertAnthropicMessages(
	transformedMessages []Message,
	isOAuthToken bool,
	cacheControl *jsonx.Obj,
	allowEmptySignature bool,
	managedProvider string,
	nativeToolChanges bool,
) *convertedAnthropicMessages {
	params := []*jsonx.Obj{}
	assistantLevels := map[int]string{}
	var pendingSystemMessages []*jsonx.Obj

	flushPendingSystemMessages := func() {
		params = append(params, pendingSystemMessages...)
		pendingSystemMessages = nil
	}

	appendCacheControl := func(block *jsonx.Obj) {
		if cacheControl != nil {
			block.Set("cache_control", cacheControl)
		}
	}

	for i := 0; i < len(transformedMessages); i++ {
		msg := transformedMessages[i]

		switch t := msg.(type) {
		case *SystemMessage:
			text := RenderSystemMessageUpdate(t)
			var blocks []any
			if len(text) > 0 {
				block := jsonx.ObjFrom("type", "text", "text", SanitizeSurrogates(text))
				blocks = append(blocks, block)
			}
			if nativeToolChanges {
				for _, ref := range t.ToolsRemoved {
					name := ref.Name
					if isOAuthToken {
						name = ToClaudeCodeName(name)
					}
					blocks = append(blocks, jsonx.ObjFrom(
						"type", "tool_removal",
						"tool", jsonx.ObjFrom("type", "tool_reference", "name", name),
					))
				}
				for _, tool := range t.ToolsAdded {
					name := tool.Name
					if isOAuthToken {
						name = ToClaudeCodeName(name)
					}
					blocks = append(blocks, jsonx.ObjFrom(
						"type", "tool_addition",
						"tool", jsonx.ObjFrom("type", "tool_reference", "name", name),
					))
				}
			}
			if len(blocks) > 0 {
				pendingSystemMessages = append(pendingSystemMessages, jsonx.ObjFrom("role", "system", "content", blocks))
			}

		case *UserMessage:
			if t.Content.IsText {
				if len(strings.TrimSpace(t.Content.Text)) > 0 {
					params = append(params, jsonx.ObjFrom(
						"role", "user",
						"content", SanitizeSurrogates(t.Content.Text),
					))
				}
			} else {
				var blocks []any
				for _, block := range t.Content.Blocks {
					if text, ok := block.(TextContent); ok {
						blocks = append(blocks, jsonx.ObjFrom(
							"type", "text",
							"text", SanitizeSurrogates(text.Text),
						))
					} else if image, ok := block.(ImageContent); ok {
						blocks = append(blocks, jsonx.ObjFrom(
							"type", "image",
							"source", jsonx.ObjFrom(
								"type", "base64",
								"media_type", image.MimeType,
								"data", image.Data,
							),
						))
					}
				}
				filtered := blocks[:0]
				for _, block := range blocks {
					if obj, ok := JxObj(block); ok {
						if t, _ := JxString(obj, "type"); t == "text" {
							text, _ := JxString(obj, "text")
							if len(strings.TrimSpace(text)) == 0 {
								continue
							}
						}
					}
					filtered = append(filtered, block)
				}
				if len(filtered) == 0 {
					continue
				}
				params = append(params, jsonx.ObjFrom("role", "user", "content", filtered))
			}

		case *AssistantMessage:
			flushPendingSystemMessages()
			var blocks []any
			for _, block := range t.Content {
				switch b := block.(type) {
				case TextContent:
					if len(strings.TrimSpace(b.Text)) == 0 {
						continue
					}
					blocks = append(blocks, jsonx.ObjFrom(
						"type", "text",
						"text", SanitizeSurrogates(b.Text),
					))
				case ThinkingContent:
					if b.Redacted != nil && *b.Redacted {
						data := ""
						if b.ThinkingSignature != nil {
							data = *b.ThinkingSignature
						}
						blocks = append(blocks, jsonx.ObjFrom("type", "redacted_thinking", "data", data))
						continue
					}
					thinkingSignature := ""
					if b.ThinkingSignature != nil {
						thinkingSignature = *b.ThinkingSignature
					}
					hasSignature := len(strings.TrimSpace(thinkingSignature)) > 0
					if len(strings.TrimSpace(b.Thinking)) == 0 && !hasSignature {
						continue
					}
					if !hasSignature {
						if allowEmptySignature {
							blocks = append(blocks, jsonx.ObjFrom(
								"type", "thinking",
								"thinking", SanitizeSurrogates(b.Thinking),
								"signature", "",
							))
						} else {
							blocks = append(blocks, jsonx.ObjFrom(
								"type", "text",
								"text", SanitizeSurrogates(b.Thinking),
							))
						}
					} else {
						blocks = append(blocks, jsonx.ObjFrom(
							"type", "thinking",
							"thinking", SanitizeSurrogates(b.Thinking),
							"signature", thinkingSignature,
						))
					}
				case *ToolCall:
					toolName := b.Name
					if isOAuthToken {
						toolName = ToClaudeCodeName(toolName)
					}
					input := any(jsonx.NewObj())
					if b.Arguments != nil {
						input = b.Arguments
					}
					blocks = append(blocks, jsonx.ObjFrom(
						"type", "tool_use",
						"id", b.ID,
						"name", toolName,
						"input", input,
					))
				}
			}
			if len(blocks) == 0 {
				continue
			}
			messageIndex := len(params)
			params = append(params, jsonx.ObjFrom("role", "assistant", "content", blocks))
			if managedProvider != "" && t.API == "anthropic-messages" && t.Provider == managedProvider &&
				t.ProviderThinkingLevel != nil && isAnthropicEffort(*t.ProviderThinkingLevel) {
				assistantLevels[messageIndex] = *t.ProviderThinkingLevel
			}

		case *ToolResultMessage:
			// Collect consecutive toolResult messages into one user turn.
			var toolResults []any
			j := i
			for j < len(transformedMessages) {
				if result, ok := transformedMessages[j].(*ToolResultMessage); ok {
					toolResults = append(toolResults, convertToolResultMessage(result))
					j++
				} else {
					break
				}
			}
			i = j - 1
			params = append(params, jsonx.ObjFrom("role", "user", "content", toolResults))
		}
	}

	flushPendingSystemMessages()

	// Add cache_control to the last user or system message.
	if cacheControl != nil && len(params) > 0 {
		lastMessage := params[len(params)-1]
		role, _ := JxString(lastMessage, "role")
		if role == "user" || role == "system" {
			contentValue, hasContent := lastMessage.Get("content")
			if hasContent {
				if contentList, isList := contentValue.([]any); isList && len(contentList) > 0 {
					if lastBlock, isObj := JxObj(contentList[len(contentList)-1]); isObj {
						blockType, _ := JxString(lastBlock, "type")
						switch blockType {
						case "text", "image", "tool_result", "tool_addition", "tool_removal":
							appendCacheControl(lastBlock)
						}
					}
				} else if text, isString := contentValue.(string); isString {
					block := jsonx.ObjFrom("type", "text", "text", text)
					appendCacheControl(block)
					lastMessage.Set("content", []any{block})
				}
			}
		}
	}

	return &convertedAnthropicMessages{messages: params, assistantLevels: assistantLevels}
}

func insertThinkingLevelMessages(converted *convertedAnthropicMessages, activeEffort string) []any {
	messages := []any{}
	for index := 0; index < len(converted.messages); index++ {
		if historicalEffort, ok := converted.assistantLevels[index]; ok {
			messages = append(messages, jsonx.ObjFrom(
				"role", "system",
				"content", []any{},
				"output_config", jsonx.ObjFrom("effort", historicalEffort),
			))
		}
		messages = append(messages, converted.messages[index])
	}
	messages = append(messages, jsonx.ObjFrom(
		"role", "system",
		"content", []any{},
		"output_config", jsonx.ObjFrom("effort", activeEffort),
	))
	return messages
}

// ---------------------------------------------------------------------------
// Tools / beta features / params
// ---------------------------------------------------------------------------

// deferredToolPlaceholder mirrors DEFERRED_TOOL_PLACEHOLDER.
func deferredToolPlaceholder() *jsonx.Obj {
	return jsonx.ObjFrom(
		"name", "__pi_deferred_placeholder__",
		"description", "Reserved placeholder. Never available. Never call this.",
		"input_schema", jsonx.ObjFrom("type", "object", "properties", jsonx.NewObj(), "required", []any{}),
		"defer_loading", true,
	)
}

func shouldUseFineGrainedToolStreamingBeta(model *Model, context *TranscriptContext) bool {
	return len(GetCurrentTools(context.Messages)) > 0 && !getAnthropicCompat(model).SupportsEagerToolInputStreaming
}

func shouldUseServerSideFallbackBeta(model *Model) bool {
	return len(anthropicAllowedFallbackModels(model)) > 0
}

func getBetaFeatures(model *Model, context *TranscriptContext, isOAuthToken bool, nativeToolChanges bool, options *AnthropicOptions) []any {
	// configuredFeatures: undefined (absent) | null (removed) | string list.
	configuredFeatures := (*string)(nil)
	configuredIsNull := false
	sources := []ProviderHeaders{modelHeadersToProviderHeaders(model), nil}
	if options != nil {
		sources[1] = options.Headers
	}
	for _, headers := range sources {
		for name, value := range headers {
			if strings.ToLower(name) == "anthropic-beta" {
				if value == nil {
					configuredIsNull = true
					configuredFeatures = nil
				} else {
					v := *value
					configuredFeatures = &v
					configuredIsNull = false
				}
			}
		}
	}
	if configuredIsNull {
		return nil
	}
	if configuredFeatures != nil {
		seen := map[string]bool{}
		var features []any
		for _, feature := range strings.Split(*configuredFeatures, ",") {
			trimmed := strings.TrimSpace(feature)
			if trimmed == "" || seen[trimmed] {
				continue
			}
			seen[trimmed] = true
			features = append(features, trimmed)
		}
		return features
	}

	seen := map[string]bool{}
	add := func(feature string) {
		if !seen[feature] {
			seen[feature] = true
		}
	}
	if isOAuthToken {
		add(anthropicClaudeCodeBeta)
		add(anthropicOAuthBeta)
	}
	if shouldUseFineGrainedToolStreamingBeta(model, context) {
		add(anthropicFineGrainedToolStreamingBeta)
	}
	interleaved := true
	if options != nil && options.InterleavedThinking != nil {
		interleaved = *options.InterleavedThinking
	}
	if model.Reasoning && options != nil && options.ThinkingEnabled != nil && *options.ThinkingEnabled &&
		interleaved && !anthropicForceAdaptiveThinking(model) {
		add(anthropicInterleavedThinkingBeta)
	}
	if shouldUseServerSideFallbackBeta(model) {
		add(anthropicServerSideFallbackBeta)
	}
	if anthropicSupportsMidConvoEffort(model) {
		add(anthropicMidConvoOutputConfigBeta)
		add(anthropicThinkingBindingControlsBeta)
	}
	if nativeToolChanges {
		add(anthropicMidConvoToolChangesBeta)
	}
	features := make([]any, 0, len(seen))
	// Preserve TS insertion order deterministically.
	for _, feature := range []string{
		anthropicClaudeCodeBeta, anthropicOAuthBeta, anthropicFineGrainedToolStreamingBeta,
		anthropicInterleavedThinkingBeta, anthropicServerSideFallbackBeta,
		anthropicMidConvoOutputConfigBeta, anthropicThinkingBindingControlsBeta,
		anthropicMidConvoToolChangesBeta,
	} {
		if seen[feature] {
			features = append(features, feature)
		}
	}
	return features
}

func modelHeadersToProviderHeaders(model *Model) ProviderHeaders {
	if len(model.Headers) == 0 {
		return nil
	}
	out := ProviderHeaders{}
	for name, value := range model.Headers {
		v := value
		out[name] = &v
	}
	return out
}

func convertAnthropicTools(tools []Tool, isOAuthToken bool, supportsEagerToolInputStreaming bool, supportsStrictTools bool, cacheControl *jsonx.Obj) []any {
	out := make([]any, 0, len(tools))
	for index, tool := range tools {
		strict, err := ResolveJSONSchemaStrictSampling(&tool, supportsStrictTools)
		if err != nil {
			panic(err) // TS throws out of buildParams; surfaced by the pump
		}
		parameters, err := GetJSONSchemaToolParameters(&tool, strict)
		if err != nil {
			panic(err)
		}
		var properties any = jsonx.NewObj()
		var required []any
		if schemaObj, ok := JxObj(parameters); ok {
			if props, has := JxObjectField(schemaObj, "properties"); has {
				properties = props
			}
			if req, has := JxList(schemaObj, "required"); has {
				required = req
			}
		}
		legacyInputSchema := jsonx.ObjFrom(
			"type", "object",
			"properties", properties,
			"required", required,
		)
		var inputSchema any = legacyInputSchema
		if strict {
			merged := parameters.(*jsonx.Obj).ShallowClone()
			for _, key := range legacyInputSchema.Keys() {
				value, _ := legacyInputSchema.Get(key)
				merged.Set(key, value)
			}
			inputSchema = merged
		}

		name := tool.Name
		if isOAuthToken {
			name = ToClaudeCodeName(name)
		}
		toolParam := jsonx.NewObj()
		toolParam.Set("name", name)
		toolParam.Set("description", tool.Description)
		if supportsEagerToolInputStreaming {
			toolParam.Set("eager_input_streaming", true)
		}
		if strict {
			toolParam.Set("strict", true)
		}
		toolParam.Set("input_schema", inputSchema)
		if cacheControl != nil && index == len(tools)-1 {
			toolParam.Set("cache_control", cacheControl)
		}
		out = append(out, toolParam)
	}
	return out
}

// getAnthropicCacheControl ports resolveCacheRetention/getCacheControl.
func getAnthropicCacheControl(model *Model, cacheRetention *string, env ProviderEnv) *jsonx.Obj {
	retention := CacheRetentionShort
	if cacheRetention != nil {
		retention = *cacheRetention
	} else if GetProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		retention = CacheRetentionLong
	}
	if retention == CacheRetentionNone {
		return nil
	}
	if retention == CacheRetentionLong && getAnthropicCompat(model).SupportsLongCacheRetention {
		return jsonx.ObjFrom("type", "ephemeral", "ttl", "1h")
	}
	return jsonx.ObjFrom("type", "ephemeral")
}

func buildAnthropicParams(model *Model, context *TranscriptContext, isOAuthToken bool, options *AnthropicOptions) *jsonx.Obj {
	var cacheRetention *string
	var env ProviderEnv
	if options != nil {
		cacheRetention = options.CacheRetention
		env = options.Env
	}
	cacheControl := getAnthropicCacheControl(model, cacheRetention, env)
	compat := getAnthropicCompat(model)
	initialSystemMessage := GetInitialSystemMessage(context.Messages)
	initialSystemText := ""
	if initialSystemMessage != nil {
		initialSystemText = GetSystemMessageText(initialSystemMessage)
	}
	transformedMessages := TransformMessages(context.Messages, model, func(id string, _ *Model, _ *AssistantMessage) string {
		return normalizeAnthropicToolCallId(id)
	})
	conversationMessages := transformedMessages
	if initialSystemMessage != nil {
		conversationMessages = transformedMessages[1:]
	}
	initialTools := []Tool{}
	if initialSystemMessage != nil {
		initialTools = initialSystemMessage.ToolsAdded
	}
	nativeToolChanges := compat.SupportsMidConvoSystemMessages &&
		compat.SupportsMidConvoToolChanges &&
		len(initialTools) > 0 &&
		!HasToolRedefinitions(context.Messages)
	managedProvider := ""
	if anthropicSupportsMidConvoEffort(model) {
		managedProvider = model.Provider
	}
	converted := convertAnthropicMessages(conversationMessages, isOAuthToken, cacheControl, compat.AllowEmptySignature, managedProvider, nativeToolChanges)

	activeEffort := AnthropicEffortHigh
	if options != nil && options.Effort != nil {
		activeEffort = *options.Effort
	}
	betaFeatures := getBetaFeatures(model, context, isOAuthToken, nativeToolChanges, options)

	params := jsonx.NewObj()
	params.Set("model", model.ID)
	if anthropicSupportsMidConvoEffort(model) {
		params.Set("messages", insertThinkingLevelMessages(converted, activeEffort))
	} else {
		messages := make([]any, 0, len(converted.messages))
		for _, message := range converted.messages {
			messages = append(messages, message)
		}
		params.Set("messages", messages)
	}
	maxTokens := model.MaxTokens
	if options != nil && options.MaxTokens != nil {
		maxTokens = *options.MaxTokens
	}
	params.Set("max_tokens", maxTokens)
	params.Set("stream", true)
	if len(betaFeatures) > 0 {
		params.Set("betas", betaFeatures)
	}

	// For OAuth tokens, include the Claude Code identity.
	if isOAuthToken {
		system := []any{}
		identity := jsonx.ObjFrom("type", "text", "text", "You are Claude Code, Anthropic's official CLI for Claude.")
		if cacheControl != nil {
			identity.Set("cache_control", cacheControl)
		}
		system = append(system, identity)
		if initialSystemText != "" {
			promptBlock := jsonx.ObjFrom("type", "text", "text", SanitizeSurrogates(initialSystemText))
			if cacheControl != nil {
				promptBlock.Set("cache_control", cacheControl)
			}
			system = append(system, promptBlock)
		}
		params.Set("system", system)
	} else if initialSystemText != "" {
		promptBlock := jsonx.ObjFrom("type", "text", "text", SanitizeSurrogates(initialSystemText))
		if cacheControl != nil {
			promptBlock.Set("cache_control", cacheControl)
		}
		params.Set("system", []any{promptBlock})
	}

	// Temperature is incompatible with extended thinking and unsupported on
	// newer Opus models.
	if options != nil && options.Temperature != nil && !thinkingEnabled(options) && !anthropicSupportsMidConvoEffort(model) && compat.SupportsTemperature {
		params.Set("temperature", *options.Temperature)
	}

	toolCacheControl := cacheControl
	if !compat.SupportsCacheControlOnTools {
		toolCacheControl = nil
	}
	if nativeToolChanges {
		initialNames := map[string]bool{}
		for _, tool := range initialTools {
			initialNames[tool.Name] = true
		}
		var laterTools []Tool
		for _, tool := range GetDeclaredTools(context.Messages) {
			if !initialNames[tool.Name] {
				laterTools = append(laterTools, tool)
			}
		}
		tools := append(
			convertAnthropicTools(initialTools, isOAuthToken, compat.SupportsEagerToolInputStreaming, compat.SupportsStrictTools, toolCacheControl),
			deferredToolPlaceholder(),
		)
		for _, convertedTool := range convertAnthropicTools(laterTools, isOAuthToken, compat.SupportsEagerToolInputStreaming, compat.SupportsStrictTools, nil) {
			if obj, ok := JxObj(convertedTool); ok {
				obj.Set("defer_loading", true)
			}
			tools = append(tools, convertedTool)
		}
		params.Set("tools", tools)
	} else {
		tools := GetCurrentTools(context.Messages)
		if len(tools) > 0 {
			params.Set("tools", convertAnthropicTools(tools, isOAuthToken, compat.SupportsEagerToolInputStreaming, compat.SupportsStrictTools, toolCacheControl))
		}
	}

	// Thinking configuration.
	if anthropicSupportsMidConvoEffort(model) {
		display := AnthropicThinkingDisplaySummarized
		if options != nil && options.ThinkingDisplay != nil {
			display = *options.ThinkingDisplay
		}
		params.Set("thinking", jsonx.ObjFrom(
			"type", "adaptive",
			"display", display,
			"block_binding", jsonx.ObjFrom("prefix_mismatch_behavior", "drop_block"),
		))
		params.Set("output_config", jsonx.ObjFrom("effort", AnthropicEffortHigh))
	} else if model.Reasoning {
		if thinkingEnabled(options) {
			display := AnthropicThinkingDisplaySummarized
			if options != nil && options.ThinkingDisplay != nil {
				display = *options.ThinkingDisplay
			}
			if anthropicForceAdaptiveThinking(model) {
				params.Set("thinking", jsonx.ObjFrom("type", "adaptive", "display", display))
				if options != nil && options.Effort != nil {
					params.Set("output_config", jsonx.ObjFrom("effort", *options.Effort))
				}
			} else {
				budget := 1024.0
				if options != nil && options.ThinkingBudgetTokens != nil {
					budget = *options.ThinkingBudgetTokens
				}
				if budget == 0 {
					budget = 1024.0
				}
				params.Set("thinking", jsonx.ObjFrom(
					"type", "enabled",
					"budget_tokens", budget,
					"display", display,
				))
			}
		} else if options != nil && options.ThinkingEnabled != nil && !*options.ThinkingEnabled {
			_, present, isNull := anthropicThinkingLevelMapValue(model, ThinkingOff)
			if !(present && isNull) {
				params.Set("thinking", jsonx.ObjFrom("type", "disabled"))
			}
		}
	}

	if options != nil && options.Metadata != nil {
		if userID, ok := options.Metadata["user_id"].(string); ok {
			params.Set("metadata", jsonx.ObjFrom("user_id", userID))
		}
	}

	if options != nil {
		if options.ToolChoice != nil {
			params.Set("tool_choice", jsonx.ObjFrom("type", *options.ToolChoice))
		} else if options.ToolChoiceTool != nil {
			params.Set("tool_choice", jsonx.ObjFrom("type", "tool", "name", options.ToolChoiceTool.Name))
		}
	}

	if fallbacks := anthropicAllowedFallbackModels(model); len(fallbacks) > 0 {
		var fallbackParams []any
		for _, fallback := range fallbacks {
			fallbackParams = append(fallbackParams, jsonx.ObjFrom("model", fallback.Model))
		}
		params.Set("fallbacks", fallbackParams)
	}

	return params
}

func thinkingEnabled(options *AnthropicOptions) bool {
	return options != nil && options.ThinkingEnabled != nil && *options.ThinkingEnabled
}
