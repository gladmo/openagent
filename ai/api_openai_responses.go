package ai

// api_openai_responses.go ports api/openai-responses.ts: the OpenAI
// Responses streaming client (POST {baseURL}/responses, typed SSE events).

import (
	"strings"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

var openAIToolCallProviders = map[string]bool{"openai": true, "openai-codex": true, "opencode": true}

// OpenAI Responses rejects max_output_tokens below 16.
const openAIResponsesMinOutputTokens = 16

// openAIResponsesCompat is Required<OpenAIResponsesCompat>.
type openAIResponsesCompat struct {
	SupportsDeveloperRole           bool
	SupportsMidConvoSystemMessages  bool
	SessionAffinityFormat           string
	SupportsLongCacheRetention      bool
	SupportsStrictMode              bool
	SupportsOpenAIGrammarTools      bool
	SupportsAdditionalTools         bool
	SupportsToolSearch              bool
	SupportsExplicitPromptCacheMode bool
	SupportsMaxOutputTokens         bool
}

func detectResponsesSessionAffinityFormat(model *Model) string {
	if model.Provider == "openrouter" || strings.Contains(model.BaseURL, "openrouter.ai") {
		return "openrouter"
	}
	return "openai"
}

func getOpenAIResponsesCompat(model *Model) openAIResponsesCompat {
	compat := openAIResponsesCompat{
		SupportsDeveloperRole:           JxCompatBool(model, "supportsDeveloperRole", true),
		SupportsMidConvoSystemMessages:  JxCompatBool(model, "supportsMidConvoSystemMessages", false),
		SessionAffinityFormat:           detectResponsesSessionAffinityFormat(model),
		SupportsLongCacheRetention:      JxCompatBool(model, "supportsLongCacheRetention", true),
		SupportsStrictMode:              JxCompatBool(model, "supportsStrictMode", false),
		SupportsOpenAIGrammarTools:      JxCompatBool(model, "supportsOpenAIGrammarTools", false),
		SupportsAdditionalTools:         JxCompatBool(model, "supportsAdditionalTools", false),
		SupportsToolSearch:              JxCompatBool(model, "supportsToolSearch", false),
		SupportsExplicitPromptCacheMode: JxCompatBool(model, "supportsExplicitPromptCacheMode", false),
		SupportsMaxOutputTokens:         JxCompatBool(model, "supportsMaxOutputTokens", true),
	}
	if format := compatString(model, "sessionAffinityFormat"); format != "" {
		compat.SessionAffinityFormat = format
	}
	return compat
}

func compatString(model *Model, key string) string {
	if compat, ok := JxCompatObj(model); ok {
		if value, present := JxString(compat, key); present {
			return value
		}
	}
	return ""
}

func getResponsesPromptCacheRetention(compat openAIResponsesCompat, cacheRetention string) string {
	if cacheRetention == CacheRetentionLong && compat.SupportsLongCacheRetention && !compat.SupportsExplicitPromptCacheMode {
		return "24h"
	}
	return ""
}

func getResponsesPromptCacheOptions(compat openAIResponsesCompat, cacheRetention string) *jsonx.Obj {
	if !compat.SupportsExplicitPromptCacheMode {
		return nil
	}
	if cacheRetention == CacheRetentionNone {
		return jsonx.ObjFrom("mode", "explicit")
	}
	if cacheRetention == CacheRetentionLong && compat.SupportsLongCacheRetention {
		return jsonx.ObjFrom("ttl", "30m")
	}
	return nil
}

// OpenAIResponsesOptions mirrors the TS api-specific options.
type OpenAIResponsesOptions struct {
	StreamOptions
	ReasoningEffort  *string // minimal|low|medium|high|xhigh|max
	ReasoningSummary *string // "auto" | "detailed" | "concise"
	ServiceTier      *string
	ToolChoice       *string
	ToolChoiceTool   *string
}

// OpenAIResponsesAPI implements ProviderStreams for "openai-responses".
type OpenAIResponsesAPI struct{}

// OpenAIResponsesApi returns the shared API instance.
func OpenAIResponsesApi() *OpenAIResponsesAPI { return &OpenAIResponsesAPI{} }

// Stream implements ProviderStreams with plain options.
func (a *OpenAIResponsesAPI) Stream(model *Model, context *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream {
	responsesOptions := &OpenAIResponsesOptions{}
	if options != nil {
		responsesOptions.StreamOptions = *options
	}
	return a.StreamResponses(model, context, responsesOptions)
}

// StreamSimple implements ProviderStreams.
func (a *OpenAIResponsesAPI) StreamSimple(model *Model, context *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
	plainOptions := options
	if plainOptions == nil {
		plainOptions = &SimpleStreamOptions{}
	}
	apiKey := ""
	if plainOptions.APIKey != nil {
		apiKey = *plainOptions.APIKey
	}
	if _, err := getClientAPIKey(model.Provider, apiKey, plainOptions.Headers); err != nil {
		return errorStream(err)
	}

	base := BuildBaseOptions(model, context, plainOptions, apiKey)
	responsesOptions := &OpenAIResponsesOptions{
		StreamOptions: *base,
		ToolChoice:    plainOptions.ToolChoice,
	}
	if plainOptions.Reasoning != nil && *plainOptions.Reasoning != "" {
		clamped := ClampThinkingLevel(model, *plainOptions.Reasoning)
		if clamped != ThinkingOff {
			responsesOptions.ReasoningEffort = &clamped
		}
	}
	return a.StreamResponses(model, context, responsesOptions)
}

// StreamResponses ports the api-specific stream function.
func (a *OpenAIResponsesAPI) StreamResponses(model *Model, context *TranscriptContext, options *OpenAIResponsesOptions) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStream()
	compat := getOpenAIResponsesCompat(model)
	normalizedContext := ResolveTranscript(context, compat.SupportsMidConvoSystemMessages)

	go func() {
		output := &AssistantMessage{
			Content:     []ContentBlock{},
			API:         model.API,
			Provider:    model.Provider,
			Model:       model.ID,
			Usage:       zeroUsage(),
			StopReason:  StopPending,
			TimestampMs: nowMs(),
		}

		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = errorFromPanic(r)
				}
			}()
			return a.run(model, normalizedContext, output, stream, options, compat)
		}()

		if err != nil {
			output.StopReason = StopError
			if options != nil && options.Signal != nil && options.Signal.Aborted() {
				output.StopReason = StopAborted
			}
			prefix := model.Provider + " API error"
			if model.Provider == "openai" {
				prefix = "OpenAI API error"
			}
			message := FormatProviderError(NormalizeProviderError(err), prefix)
			output.ErrorMessage = &message
			stream.Push(&EventError{Reason: output.StopReason, Error: output})
			stream.End()
		}
	}()

	return stream
}

func (a *OpenAIResponsesAPI) run(
	model *Model,
	normalizedContext *TranscriptContext,
	output *AssistantMessage,
	stream *AssistantMessageEventStream,
	options *OpenAIResponsesOptions,
	compat openAIResponsesCompat,
) error {
	var apiKey string
	var optionsHeaders ProviderHeaders
	if options != nil {
		if options.APIKey != nil {
			apiKey = *options.APIKey
		}
		optionsHeaders = options.Headers
	}
	apiKey, err := getClientAPIKey(model.Provider, apiKey, optionsHeaders)
	if err != nil {
		return err
	}

	cacheRetention := CacheRetentionShort
	var env ProviderEnv
	if options != nil {
		env = options.Env
		if options.CacheRetention != nil && *options.CacheRetention != "" {
			cacheRetention = *options.CacheRetention
		} else if GetProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
			cacheRetention = CacheRetentionLong
		}
	}
	sessionID := ""
	if options != nil && options.SessionID != nil && cacheRetention != CacheRetentionNone {
		sessionID = *options.SessionID
	}
	grammarToolInputProperties := CreateGrammarToolInputProperties(
		GetDeclaredTools(normalizedContext.Messages),
		compat.SupportsOpenAIGrammarTools,
	)
	headers := createResponsesClient(model, normalizedContext, apiKey, optionsHeaders, sessionID, compat)

	params := buildResponsesParams(model, normalizedContext, options, compat, grammarToolInputProperties, cacheRetention)
	if options != nil && options.OnPayload != nil {
		if next := options.OnPayload(params, model); next != nil {
			if obj, ok := JxObj(next); ok {
				params = obj
			}
		}
	}
	params.Set("stream", true)

	var signal *abort.Signal
	var maxRetries, maxRetryDelayMs, timeoutMs *float64
	var fetch FetchFunction
	if options != nil {
		signal = options.Signal
		maxRetries = options.MaxRetries
		maxRetryDelayMs = options.MaxRetryDelayMs
		timeoutMs = options.TimeoutMs
		fetch = options.Fetch
	}

	url := strings.TrimSuffix(model.BaseURL, "/") + "/responses"
	payload := []byte(jsonx.Stringify(params))

	var sse *SSEStreamResponse
	request := func() error {
		fetchOptions := &StreamOptions{Signal: signal, TimeoutMs: timeoutMs, Fetch: fetch}
		response, err := PostJSONStream(url, headers, payload, fetchOptions)
		if err != nil {
			return WrapStainlessHTTPError(err)
		}
		sse = response
		return nil
	}
	if err := RetryProviderRequest(request, &ProviderRetryOptions{
		MaxRetries:      maxRetries,
		MaxRetryDelayMs: maxRetryDelayMs,
		Signal:          signal,
	}); err != nil {
		return err
	}
	defer sse.Stream.Close()
	if options != nil && options.OnResponse != nil {
		options.OnResponse(ProviderResponse{Status: sse.Status, Headers: sse.Headers}, model)
	}
	stream.Push(&EventStart{Partial: output})

	streamOptions := &responsesStreamOptions{
		GrammarToolInputProperties: grammarToolInputProperties,
	}
	if options != nil {
		streamOptions.OnProviderStreamEvent = options.OnProviderStreamEvent
		streamOptions.ServiceTier = options.ServiceTier
		streamOptions.ApplyServiceTierPricing = func(usage *Usage, serviceTier *string) {
			applyResponsesServiceTierPricing(usage, serviceTier, model)
		}
	}

	if err := processResponsesStream(&sseResponsesEvents{stream: sse.Stream}, output, stream, model, streamOptions); err != nil {
		return err
	}

	if options != nil && options.Signal != nil && options.Signal.Aborted() {
		return abort.NewAbortError("Request was aborted")
	}
	if output.StopReason == StopPending {
		return &csError{msg: "OpenAI Responses stream ended without a stop reason"}
	}
	if output.StopReason == StopAborted || output.StopReason == StopError {
		message := "An unknown error occurred"
		if output.ErrorMessage != nil {
			message = *output.ErrorMessage
		}
		return &csError{msg: message}
	}

	stream.Push(&EventDone{Reason: output.StopReason, Message: output})
	stream.End(output)
	return nil
}

func createResponsesClient(model *Model, context *TranscriptContext, apiKey string, optionsHeaders ProviderHeaders, sessionID string, compat openAIResponsesCompat) map[string]string {
	headers := map[string]string{"User-Agent": GetPiUserAgent()}
	for name, value := range model.Headers {
		headers[name] = value
	}
	if model.Provider == "github-copilot" {
		for name, value := range BuildCopilotDynamicHeaders(context.Messages, HasCopilotVisionInput(context.Messages)) {
			headers[name] = value
		}
	}
	if sessionID != "" {
		if compat.SessionAffinityFormat == "openrouter" {
			headers["x-session-id"] = sessionID
		} else {
			if compat.SessionAffinityFormat == "openai" {
				headers["session_id"] = sessionID
			}
			headers["x-client-request-id"] = sessionID
		}
	}
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

func buildResponsesParams(
	model *Model,
	context *TranscriptContext,
	options *OpenAIResponsesOptions,
	compat openAIResponsesCompat,
	grammarToolInputProperties map[string]string,
	cacheRetention string,
) *jsonx.Obj {
	transcriptTools := ResolveTranscriptTools(
		context.Messages,
		compat.SupportsAdditionalTools || compat.SupportsToolSearch,
	)
	messages, err := convertResponsesMessages(model, context, openAIToolCallProviders, &ConvertResponsesMessagesOptions{
		GrammarToolInputProperties:     grammarToolInputProperties,
		SupportsMidConvoSystemMessages: compat.SupportsMidConvoSystemMessages,
		SupportsAdditionalTools:        compat.SupportsAdditionalTools,
		SupportsToolSearch:             compat.SupportsToolSearch,
		ToolOptions: &ConvertResponsesToolsOptions{
			SupportsStrictMode:         compat.SupportsStrictMode,
			SupportsOpenAIGrammarTools: compat.SupportsOpenAIGrammarTools,
		},
	})
	if err != nil {
		panic(err)
	}

	var sessionID string
	if options != nil && options.SessionID != nil {
		sessionID = *options.SessionID
	}

	params := jsonx.NewObj()
	params.Set("model", model.ID)
	params.Set("input", messages)
	params.Set("stream", true)
	if cacheRetention != CacheRetentionNone {
		if key, ok := ClampOpenAIPromptCacheKey(sessionID); ok {
			params.Set("prompt_cache_key", key)
		}
	}
	if retention := getResponsesPromptCacheRetention(compat, cacheRetention); retention != "" {
		params.Set("prompt_cache_retention", retention)
	}
	if cacheOptions := getResponsesPromptCacheOptions(compat, cacheRetention); cacheOptions != nil {
		params.Set("prompt_cache_options", cacheOptions)
	}
	params.Set("store", false)

	if options != nil && options.MaxTokens != nil && compat.SupportsMaxOutputTokens {
		maxOutputTokens := *options.MaxTokens
		if maxOutputTokens < openAIResponsesMinOutputTokens {
			maxOutputTokens = openAIResponsesMinOutputTokens
		}
		params.Set("max_output_tokens", maxOutputTokens)
	}

	if options != nil && options.Temperature != nil {
		params.Set("temperature", *options.Temperature)
	}
	if options != nil && options.ServiceTier != nil {
		params.Set("service_tier", *options.ServiceTier)
	}

	if len(transcriptTools.RequestTools) > 0 {
		params.Set("tools", convertResponsesTools(transcriptTools.RequestTools, &ConvertResponsesToolsOptions{
			SupportsStrictMode:         compat.SupportsStrictMode,
			SupportsOpenAIGrammarTools: compat.SupportsOpenAIGrammarTools,
		}))
	}

	if options != nil {
		if options.ToolChoice != nil {
			params.Set("tool_choice", jsonx.ObjFrom("type", *options.ToolChoice))
		} else if options.ToolChoiceTool != nil {
			params.Set("tool_choice", jsonx.ObjFrom("type", "function", "name", *options.ToolChoiceTool))
		}
	}

	if model.Reasoning {
		reasoningEffort := ""
		hasReasoningSummary := false
		if options != nil {
			if options.ReasoningEffort != nil {
				reasoningEffort = *options.ReasoningEffort
			}
			hasReasoningSummary = options.ReasoningSummary != nil
		}
		if reasoningEffort != "" || hasReasoningSummary {
			effort := "medium"
			if reasoningEffort != "" {
				if mapped, defined, isNull := thinkingLevelMapLookup(model, reasoningEffort); defined && !isNull {
					effort = mapped
				} else {
					effort = reasoningEffort
				}
			}
			summary := "auto"
			if options != nil && options.ReasoningSummary != nil && *options.ReasoningSummary != "" {
				summary = *options.ReasoningSummary
			}
			params.Set("reasoning", jsonx.ObjFrom("effort", effort, "summary", summary))
			params.Set("include", []any{"reasoning.encrypted_content"})
		} else if model.Provider != "github-copilot" {
			_, _, offNull := thinkingLevelMapLookup(model, ThinkingOff)
			if !offNull {
				effort := "none"
				if mapped, defined, isNull := thinkingLevelMapLookup(model, ThinkingOff); defined && !isNull {
					effort = mapped
				}
				params.Set("reasoning", jsonx.ObjFrom("effort", effort))
			}
		}
		if model.Provider == "xai" {
			params.Set("include", []any{"reasoning.encrypted_content"})
		}
	}

	// Last so custom keys override the named request fields.
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

func getServiceTierCostMultiplier(model *Model, serviceTier *string) float64 {
	if serviceTier == nil {
		return 1
	}
	switch *serviceTier {
	case "flex":
		return 0.5
	case "priority":
		if model.ID == "gpt-5.5" {
			return 2.5
		}
		return 2
	default:
		return 1
	}
}

func applyResponsesServiceTierPricing(usage *Usage, serviceTier *string, model *Model) {
	multiplier := getServiceTierCostMultiplier(model, serviceTier)
	if multiplier == 1 {
		return
	}
	usage.Cost.Input *= multiplier
	usage.Cost.Output *= multiplier
	usage.Cost.CacheRead *= multiplier
	usage.Cost.CacheWrite *= multiplier
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
}
