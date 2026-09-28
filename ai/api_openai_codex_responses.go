package ai

// api_openai_codex_responses.go ports api/openai-codex-responses.ts (SSE
// transport): codex identity headers, /codex/responses URL resolution, the
// codex retry policy with ChatGPT usage-limit friendliness, event mapping
// (response.done/end_turn), and the shared Responses stream pump.
//
// Scope deviation (PORTING.md): the WebSocket transport (connection cache,
// continuation state, connection-limit retry) is not ported; `transport`
// "auto"/"websocket-cached" fall back to SSE. The zstd request-body
// compression is also omitted (the backend accepts uncompressed JSON).

import (
	"encoding/base64"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

const (
	defaultCodexBaseURL         = "https://chatgpt.com/backend-api"
	codexJWTClaimPath           = "https://api.openai.com/auth"
	codexDefaultMaxRetries      = 0
	codexBaseDelayMs            = 1000
	codexDefaultMaxRetryDelayMs = 60_000
)

var codexToolCallProviders = map[string]bool{"openai": true, "openai-codex": true, "opencode": true}

var codexResponseStatuses = map[string]bool{
	"completed": true, "incomplete": true, "failed": true,
	"cancelled": true, "queued": true, "in_progress": true,
}

// OpenAICodexResponsesOptions mirrors the TS api-specific options.
type OpenAICodexResponsesOptions struct {
	StreamOptions
	ReasoningEffort  *string // none|minimal|low|medium|high|xhigh|max
	ReasoningSummary *string // auto|concise|detailed|off|on
	ServiceTier      *string
	TextVerbosity    *string // low|medium|high
	ToolChoice       *string // auto|none|required
}

// CodexAPIError carries a provider error code (CodexApiError).
type CodexAPIError struct {
	Message string
	Code    string
	Payload any
}

func (e *CodexAPIError) Error() string { return e.Message }

// CodexProtocolError marks a wire-protocol failure.
type CodexProtocolError struct {
	Message string
	Payload any
}

func (e *CodexProtocolError) Error() string { return e.Message }

// extractCodexAccountID ports extractAccountId: the ChatGPT account id lives
// in the JWT payload under the auth claim path.
func extractCodexAccountID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		if payload, err := decodeJWTPayload(parts[1]); err == nil {
			if auth, ok := JxObjectField(payload, codexJWTClaimPath); ok {
				if accountID, has := JxString(auth, "chatgpt_account_id"); has && accountID != "" {
					return accountID, nil
				}
			}
		}
	}
	return "", &csError{msg: "Failed to extract accountId from token"}
}

func decodeJWTPayload(segment string) (*jsonx.Obj, error) {
	// JWTs use base64url; accept both padding styles.
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(segment, "="))
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(padBase64(segment))
		if err != nil {
			return nil, err
		}
	}
	payload, err := jsonx.ParseBytes(decoded)
	if err != nil {
		return nil, err
	}
	obj, ok := JxObj(payload)
	if !ok {
		return nil, &csError{msg: "Invalid token payload"}
	}
	return obj, nil
}

func padBase64(segment string) string {
	padded := segment
	if padding := len(segment) % 4; padding != 0 {
		padded += strings.Repeat("=", 4-padding)
	}
	return padded
}

func buildBaseCodexHeaders(initHeaders map[string]string, additionalHeaders ProviderHeaders, accountID, token string) map[string]string {
	headers := map[string]string{}
	for name, value := range initHeaders {
		headers[name] = value
	}
	lowerKeys := map[string]string{}
	for name := range headers {
		lowerKeys[strings.ToLower(name)] = name
	}
	for name, value := range additionalHeaders {
		if value == nil {
			if existing, ok := lowerKeys[strings.ToLower(name)]; ok {
				delete(headers, existing)
				delete(lowerKeys, strings.ToLower(name))
			}
			continue
		}
		if existing, ok := lowerKeys[strings.ToLower(name)]; ok {
			delete(headers, existing)
		}
		headers[name] = *value
		lowerKeys[strings.ToLower(name)] = name
	}
	headers["Authorization"] = "Bearer " + token
	headers["chatgpt-account-id"] = accountID
	headers["originator"] = "pi"
	headers["User-Agent"] = GetPiUserAgent()
	return headers
}

func buildCodexSSEHeaders(initHeaders map[string]string, additionalHeaders ProviderHeaders, accountID, token, sessionID string) map[string]string {
	headers := buildBaseCodexHeaders(initHeaders, additionalHeaders, accountID, token)
	headers["OpenAI-Beta"] = "responses=experimental"
	headers["accept"] = "text/event-stream"
	headers["content-type"] = "application/json"
	if sessionID != "" {
		headers["session-id"] = sessionID
		headers["x-client-request-id"] = sessionID
	}
	return headers
}

// resolveCodexURL ports resolveCodexUrl.
func resolveCodexURL(baseURL string) string {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		raw = defaultCodexBaseURL
	}
	normalized := strings.TrimRight(raw, "/")
	if strings.HasSuffix(normalized, "/codex/responses") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/codex") {
		return normalized + "/responses"
	}
	return normalized + "/codex/responses"
}

// ---------------------------------------------------------------------------
// Retry helpers
// ---------------------------------------------------------------------------

var codexTerminalRateLimitPattern = regexp.MustCompile(`GoUsageLimitError|FreeUsageLimitError|Monthly usage limit reached|available balance|insufficient_quota|out of budget|quota exceeded|billing`)

func isTerminalRateLimitError(errorText string) bool {
	return codexTerminalRateLimitPattern.MatchString(errorText)
}

var codexRetryableTextPattern = regexp.MustCompile(`rate.?limit|overloaded|service.?unavailable|upstream.?connect|connection.?refused`)

func isCodexRetryableError(status int, errorText string) bool {
	if status == 429 && isTerminalRateLimitError(errorText) {
		return false
	}
	if status == 429 || status == 500 || status == 502 || status == 503 || status == 504 {
		return true
	}
	return codexRetryableTextPattern.MatchString(errorText)
}

func getCodexRetryAfterDelayMs(headers map[string]string) (float64, bool) {
	if value := headerGet(headers, "retry-after-ms"); value != "" {
		if millis, err := strconv.ParseFloat(value, 64); err == nil {
			return math.Max(0, millis), true
		}
	}
	retryAfter := headerGet(headers, "retry-after")
	if retryAfter == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseFloat(retryAfter, 64); err == nil {
		return math.Max(0, seconds*1000), true
	}
	if date, ok := parseHTTPDate(retryAfter); ok {
		return math.Max(0, date-float64(time.Now().UnixMilli())), true
	}
	return 0, false
}

func headerGet(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// CodexRetryDelayExceededError mirrors RetryDelayExceededError.
type CodexRetryDelayExceededError struct{ Message string }

func (e *CodexRetryDelayExceededError) Error() string { return e.Message }

func validateCodexRetryDelayMs(delayMs float64, options *StreamOptions) (float64, error) {
	maxRetryDelayMs := float64(codexDefaultMaxRetryDelayMs)
	if options != nil && options.MaxRetryDelayMs != nil {
		maxRetryDelayMs = *options.MaxRetryDelayMs
	}
	if maxRetryDelayMs > 0 && delayMs > maxRetryDelayMs {
		return 0, &CodexRetryDelayExceededError{
			Message: "Server requested " + strconv.Itoa(int(math.Ceil(delayMs/1000))) +
				"s retry delay (max: " + strconv.Itoa(int(math.Ceil(maxRetryDelayMs/1000))) + "s)",
		}
	}
	return delayMs, nil
}

// parseCodexErrorResponse ports parseErrorResponse (friendly usage limits).
func parseCodexErrorResponse(status int, raw string) (message string, friendlyMessage string) {
	message = raw
	friendlyMessage = ""
	if parsed, err := ParseJSONWithRepair(raw); err == nil {
		if obj, isObj := JxObj(parsed); isObj {
			if errObj, has := JxObjectField(obj, "error"); has {
				code, _ := JxString(errObj, "code")
				if code == "" {
					code, _ = JxString(errObj, "type")
				}
				usageLimited := regexp.MustCompile(`usage_limit_reached|usage_not_included|rate_limit_exceeded`).MatchString(code)
				if usageLimited || status == 429 {
					plan := ""
					if planType, has := JxString(errObj, "plan_type"); has && planType != "" {
						plan = " (" + strings.ToLower(planType) + " plan)"
					}
					when := ""
					if resetsAt, has := JxFloat(errObj, "resets_at"); has {
						mins := math.Max(0, math.Round((resetsAt*1000-float64(time.Now().UnixMilli()))/60000))
						when = " Try again in ~" + strconv.Itoa(int(mins)) + " min."
					}
					friendlyMessage = strings.TrimSpace("You have hit your ChatGPT usage limit" + plan + "." + when)
				}
				if errMessage, has := JxString(errObj, "message"); has && errMessage != "" {
					message = errMessage
				} else if friendlyMessage != "" {
					message = friendlyMessage
				}
			}
		}
	}
	return message, friendlyMessage
}

// ---------------------------------------------------------------------------
// Request body
// ---------------------------------------------------------------------------

func buildCodexRequestBody(
	model *Model,
	context *TranscriptContext,
	options *OpenAICodexResponsesOptions,
	cacheSessionID string,
	grammarToolInputProperties map[string]string,
) *jsonx.Obj {
	supportsStrictMode := JxCompatBool(model, "supportsStrictMode", true)
	supportsOpenAIGrammarTools := JxCompatBool(model, "supportsOpenAIGrammarTools", false)
	supportsAdditionalTools := JxCompatBool(model, "supportsAdditionalTools", false)
	supportsToolSearch := JxCompatBool(model, "supportsToolSearch", false)
	transcriptTools := ResolveTranscriptTools(context.Messages, supportsAdditionalTools || supportsToolSearch)
	messages, err := convertResponsesMessages(model, context, codexToolCallProviders, &ConvertResponsesMessagesOptions{
		IncludeSystemPrompt:            false,
		HasIncludeSystemPrompt:         true,
		GrammarToolInputProperties:     grammarToolInputProperties,
		SupportsMidConvoSystemMessages: JxCompatBool(model, "supportsMidConvoSystemMessages", false),
		SupportsAdditionalTools:        supportsAdditionalTools,
		SupportsToolSearch:             supportsToolSearch,
		ToolOptions: &ConvertResponsesToolsOptions{
			Strict:                     nil,
			SupportsStrictMode:         supportsStrictMode,
			SupportsOpenAIGrammarTools: supportsOpenAIGrammarTools,
		},
	})
	if err != nil {
		panic(err)
	}

	initialSystemMessage := GetInitialSystemMessage(context.Messages)
	instructions := ""
	if initialSystemMessage != nil {
		instructions = GetSystemMessageText(initialSystemMessage)
	}
	if instructions == "" {
		instructions = "You are a helpful assistant."
	}
	verbosity := "low"
	if options != nil && options.TextVerbosity != nil && *options.TextVerbosity != "" {
		verbosity = *options.TextVerbosity
	}
	toolChoice := "auto"
	if options != nil && options.ToolChoice != nil {
		toolChoice = *options.ToolChoice
	}

	body := jsonx.NewObj()
	body.Set("model", model.ID)
	body.Set("store", false)
	body.Set("stream", true)
	body.Set("instructions", instructions)
	body.Set("input", messages)
	body.Set("text", jsonx.ObjFrom("verbosity", verbosity))
	body.Set("include", []any{"reasoning.encrypted_content"})
	if cacheSessionID != "" {
		body.Set("prompt_cache_key", cacheSessionID)
	}
	body.Set("tool_choice", toolChoice)
	body.Set("parallel_tool_calls", true)

	if options != nil && options.Temperature != nil {
		body.Set("temperature", *options.Temperature)
	}
	if options != nil && options.ServiceTier != nil {
		body.Set("service_tier", *options.ServiceTier)
	}

	if len(transcriptTools.RequestTools) > 0 {
		body.Set("tools", convertResponsesTools(transcriptTools.RequestTools, &ConvertResponsesToolsOptions{
			Strict:                     nil,
			SupportsStrictMode:         supportsStrictMode,
			SupportsOpenAIGrammarTools: supportsOpenAIGrammarTools,
		}))
	}

	if options != nil && options.ReasoningEffort != nil {
		effort := *options.ReasoningEffort
		if effort == "none" {
			mapped, defined, isNull := thinkingLevelMapLookup(model, ThinkingOff)
			switch {
			case !defined:
				effort = "none"
			case isNull:
				effort = "" // null effort: skip reasoning entirely
			default:
				effort = mapped
			}
		} else {
			mapped, defined, isNull := thinkingLevelMapLookup(model, effort)
			if defined && !isNull {
				effort = mapped
			}
			if isNull {
				effort = ""
			}
		}
		if effort != "" {
			summary := "auto"
			if options.ReasoningSummary != nil && *options.ReasoningSummary != "" {
				summary = *options.ReasoningSummary
			}
			body.Set("reasoning", jsonx.ObjFrom("effort", effort, "summary", summary))
		}
	} else if model.Reasoning {
		_, _, offNull := thinkingLevelMapLookup(model, ThinkingOff)
		if !offNull {
			effort := "none"
			if mapped, defined, isNull := thinkingLevelMapLookup(model, ThinkingOff); defined && !isNull {
				effort = mapped
			}
			body.Set("reasoning", jsonx.ObjFrom("effort", effort))
		}
	}

	// Per-request sampling keys override named fields last.
	for name, value := range model.SamplingParams {
		body.Set(name, value)
	}
	if options != nil {
		for name, value := range options.SamplingParams {
			body.Set(name, value)
		}
	}

	return body
}

func resolveCodexServiceTier(responseServiceTier, requestServiceTier *string) *string {
	if responseServiceTier != nil && *responseServiceTier == "default" &&
		(requestServiceTier != nil && (*requestServiceTier == "flex" || *requestServiceTier == "priority")) {
		return requestServiceTier
	}
	if responseServiceTier != nil {
		return responseServiceTier
	}
	return requestServiceTier
}

// ---------------------------------------------------------------------------
// API implementation
// ---------------------------------------------------------------------------

// OpenAICodexResponsesAPI implements ProviderStreams for
// "openai-codex-responses" over the SSE transport.
type OpenAICodexResponsesAPI struct{}

// OpenAICodexResponsesApi returns the shared API instance.
func OpenAICodexResponsesApi() *OpenAICodexResponsesAPI { return &OpenAICodexResponsesAPI{} }

// Stream implements ProviderStreams with plain options.
func (a *OpenAICodexResponsesAPI) Stream(model *Model, context *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream {
	codexOptions := &OpenAICodexResponsesOptions{}
	if options != nil {
		codexOptions.StreamOptions = *options
	}
	return a.StreamCodex(model, context, codexOptions)
}

// StreamSimple implements ProviderStreams.
func (a *OpenAICodexResponsesAPI) StreamSimple(model *Model, context *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
	plainOptions := options
	if plainOptions == nil {
		plainOptions = &SimpleStreamOptions{}
	}
	if plainOptions.APIKey == nil || *plainOptions.APIKey == "" {
		return errorStream(&csError{msg: "No API key for provider: " + model.Provider})
	}

	base := BuildBaseOptions(model, context, plainOptions, *plainOptions.APIKey)
	codexOptions := &OpenAICodexResponsesOptions{
		StreamOptions: *base,
		ToolChoice:    plainOptions.ToolChoice,
	}
	if plainOptions.Reasoning != nil && *plainOptions.Reasoning != "" {
		clamped := ClampThinkingLevel(model, *plainOptions.Reasoning)
		if clamped != ThinkingOff {
			codexOptions.ReasoningEffort = &clamped
		}
	}
	return a.StreamCodex(model, context, codexOptions)
}

// StreamCodex ports the api-specific stream function.
func (a *OpenAICodexResponsesAPI) StreamCodex(model *Model, context *TranscriptContext, options *OpenAICodexResponsesOptions) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStream()
	normalizedContext := ResolveTranscript(context, JxCompatBool(model, "supportsMidConvoSystemMessages", false))

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
			return a.run(model, normalizedContext, output, stream, options)
		}()

		if err != nil {
			output.StopReason = StopError
			if options != nil && options.Signal != nil && options.Signal.Aborted() {
				output.StopReason = StopAborted
			}
			message := FormatProviderError(NormalizeProviderError(err), "")
			output.ErrorMessage = &message
			stream.Push(&EventError{Reason: output.StopReason, Error: output})
			stream.End()
		}
	}()

	return stream
}

func (a *OpenAICodexResponsesAPI) run(
	model *Model,
	normalizedContext *TranscriptContext,
	output *AssistantMessage,
	stream *AssistantMessageEventStream,
	options *OpenAICodexResponsesOptions,
) error {
	var apiKey string
	var optionsHeaders ProviderHeaders
	if options != nil {
		if options.APIKey != nil {
			apiKey = *options.APIKey
		}
		optionsHeaders = options.Headers
	}
	if apiKey == "" {
		return &csError{msg: "No API key for provider: " + model.Provider}
	}

	accountID, err := extractCodexAccountID(apiKey)
	if err != nil {
		return err
	}
	grammarToolInputProperties := CreateGrammarToolInputProperties(
		GetDeclaredTools(normalizedContext.Messages),
		JxCompatBool(model, "supportsOpenAIGrammarTools", false),
	)

	cacheSessionID := ""
	if options != nil && options.SessionID != nil {
		if options.CacheRetention == nil || *options.CacheRetention != CacheRetentionNone {
			if key, ok := ClampOpenAIPromptCacheKey(*options.SessionID); ok {
				cacheSessionID = key
			}
		}
	}

	body := buildCodexRequestBody(model, normalizedContext, options, cacheSessionID, grammarToolInputProperties)
	if options != nil && options.OnPayload != nil {
		if next := options.OnPayload(body, model); next != nil {
			if obj, ok := JxObj(next); ok {
				body = obj
			}
		}
	}

	sseHeaders := buildCodexSSEHeaders(model.Headers, optionsHeaders, accountID, apiKey, cacheSessionID)
	bodyJSON := jsonx.Stringify(body)

	var signal *abort.Signal
	var fetch FetchFunction
	maxRetries := float64(codexDefaultMaxRetries)
	var timeoutMs *float64
	if options != nil {
		signal = options.Signal
		fetch = options.Fetch
		timeoutMs = options.TimeoutMs
		if options.MaxRetries != nil {
			maxRetries = *options.MaxRetries
		}
	}
	url := resolveCodexURL(model.BaseURL)

	var response *FetchResponse
	var lastError error
	attempts := int(maxRetries) + 1
	for attempt := 0; attempt < attempts; attempt++ {
		if signal != nil && signal.Aborted() {
			return abort.NewAbortError("Request was aborted")
		}

		headersTimeoutController := abort.NewController()
		var headerTimer *time.Timer
		if timeoutMs != nil && *timeoutMs > 0 {
			headerTimer = time.AfterFunc(time.Duration(*timeoutMs*float64(time.Millisecond)), headersTimeoutController.Abort)
		}
		combined, disposeCombined := abort.AnyWithDispose(signal, headersTimeoutController.Signal())
		fetched, fetchErr := fetchOrDefault(fetch, FetchRequest{
			URL:     url,
			Method:  http.MethodPost,
			Headers: sseHeaders,
			Body:    []byte(bodyJSON),
			Signal:  combined,
		})
		if headerTimer != nil {
			headerTimer.Stop()
		}
		// Every attempt derives from the caller's long-lived signal;
		// without disposal the registrations accumulate for the session.
		disposeCombined()
		if fetchErr != nil {
			if headersTimeoutController.Signal().Aborted() && (signal == nil || !signal.Aborted()) {
				return &csError{msg: "Codex SSE response headers timed out after " + strconv.FormatFloat(*timeoutMs, 'f', -1, 64) + "ms"}
			}
			if isAbortError(fetchErr) {
				return abort.NewAbortError("Request was aborted")
			}
			lastError = fetchErr
			if attempt < attempts-1 && !strings.Contains(fetchErr.Error(), "usage limit") {
				if !AbortableSleep(float64(codexBaseDelayMs)*math.Pow(2, float64(attempt)), signal) {
					return abort.NewAbortError("Request was aborted")
				}
				continue
			}
			return lastError
		}
		response = &fetched

		if options != nil && options.OnResponse != nil {
			options.OnResponse(ProviderResponse{Status: response.Status, Headers: response.Headers}, model)
		}

		if response.Status >= 200 && response.Status < 300 {
			break
		}

		bodyBytes, _ := io.ReadAll(response.Body)
		if closer, ok := response.Body.(io.Closer); ok {
			closer.Close()
		}
		errorText := string(bodyBytes)
		if attempt < attempts-1 && isCodexRetryableError(response.Status, errorText) {
			delayMs := float64(codexBaseDelayMs) * math.Pow(2, float64(attempt))
			if retryAfter, ok := getCodexRetryAfterDelayMs(response.Headers); ok {
				streamOptions := optionsStreamOf(options)
				validated, err := validateCodexRetryDelayMs(retryAfter, &streamOptions)
				if err != nil {
					return err
				}
				delayMs = validated
			}
			if !AbortableSleep(delayMs, signal) {
				return abort.NewAbortError("Request was aborted")
			}
			continue
		}

		message, friendly := parseCodexErrorResponse(response.Status, errorText)
		if friendly != "" {
			return &csError{msg: friendly}
		}
		return &csError{msg: message}
	}

	if response == nil || response.Status < 200 || response.Status >= 300 {
		if lastError != nil {
			return lastError
		}
		return &csError{msg: "Failed after retries"}
	}
	if response.Body == nil {
		return &csError{msg: "No response body"}
	}

	stream.Push(&EventStart{Partial: output})
	sse := NewSSEStream(response.Body)
	defer sse.Close()

	mapped := &codexEventMapper{
		output:     output,
		model:      model,
		options:    options,
		terminated: false,
	}
	streamOptions := &responsesStreamOptions{
		GrammarToolInputProperties: grammarToolInputProperties,
		ResolveServiceTier:         resolveCodexServiceTier,
	}
	if options != nil {
		streamOptions.ServiceTier = options.ServiceTier
		streamOptions.ApplyServiceTierPricing = func(usage *Usage, serviceTier *string) {
			applyResponsesServiceTierPricing(usage, serviceTier, model)
		}
	}

	// mapCodexEvents fires the provider-event callback itself; the shared
	// pump therefore receives no callback.
	source := &codexResponsesEvents{stream: sse, mapper: mapped, signal: signal}
	if err := processResponsesStream(source, output, stream, model, streamOptions); err != nil {
		return err
	}

	if signal != nil && signal.Aborted() {
		return abort.NewAbortError("Request was aborted")
	}
	if output.StopReason == StopPending {
		return &csError{msg: "Codex stream ended without a stop reason"}
	}
	if output.StopReason == StopError || output.StopReason == StopAborted {
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

func optionsStreamOf(options *OpenAICodexResponsesOptions) StreamOptions {
	if options == nil {
		return StreamOptions{}
	}
	return options.StreamOptions
}

func isAbortError(err error) bool {
	if _, ok := err.(*abort.Error); ok {
		return true
	}
	return strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "Request aborted")
}

func fetchOrDefault(fetch FetchFunction, request FetchRequest) (FetchResponse, error) {
	if fetch != nil {
		return fetch(request)
	}
	return DefaultFetch(request)
}

// codexEventMapper ports mapCodexEvents state.
type codexEventMapper struct {
	output     *AssistantMessage
	model      *Model
	options    *OpenAICodexResponsesOptions
	terminated bool
}

// mapEvent processes one raw SSE data event. Returns the event to forward
// (nil = skip), whether the mapped stream is done, and an error.
func (m *codexEventMapper) mapEvent(raw string) (*jsonx.Obj, bool, error) {
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return nil, false, &CodexProtocolError{Message: "Invalid Codex SSE JSON: " + FormatThrownValue(err), Payload: raw}
	}
	event, isObj := JxObj(parsed)
	if !isObj {
		return nil, false, nil
	}

	if m.options != nil && m.options.OnProviderStreamEvent != nil {
		m.options.OnProviderStreamEvent(event, m.model)
	}

	eventType, hasType := JxString(event, "type")
	if !hasType || eventType == "" {
		return nil, false, nil
	}

	if eventType == "error" {
		code, message := extractCodexEventError(event)
		display := message
		if display == "" {
			display = code
		}
		if display == "" {
			display = jsonx.Stringify(event)
		}
		return nil, false, &CodexAPIError{Message: "Codex error: " + display, Code: code, Payload: event}
	}

	if eventType == "response.failed" {
		response, _ := JxObjectField(event, "response")
		code := ""
		message := ""
		if response != nil {
			if errorObj, has := JxObjectField(response, "error"); has {
				code, _ = JxString(errorObj, "code")
				message, _ = JxString(errorObj, "message")
			}
		}
		display := message
		if display == "" {
			display = "Codex response failed"
		}
		return nil, false, &CodexAPIError{Message: display, Code: code, Payload: event}
	}

	if eventType == "response.done" || eventType == "response.completed" || eventType == "response.incomplete" {
		response, _ := JxObjectField(event, "response")
		if response != nil {
			if endTurn, has := JxBool(response, "end_turn"); has {
				output := m.output
				endTurnValue := endTurn
				output.EndTurn = &endTurnValue
			}
			normalizedResponse := response.ShallowClone()
			if status, has := JxString(response, "status"); has {
				if !codexResponseStatuses[status] {
					normalizedResponse.Delete("status")
				}
			}
			mapped := jsonx.NewObj()
			for _, key := range event.Keys() {
				if key == "type" || key == "response" {
					continue
				}
				value, _ := event.Get(key)
				mapped.Set(key, value)
			}
			mapped.Set("type", "response.completed")
			mapped.Set("response", normalizedResponse)
			return mapped, true, nil
		}
		mapped := jsonx.NewObj()
		for _, key := range event.Keys() {
			value, _ := event.Get(key)
			mapped.Set(key, value)
		}
		mapped.Set("type", "response.completed")
		return mapped, true, nil
	}

	return event, false, nil
}

func extractCodexEventError(event *jsonx.Obj) (string, string) {
	code := ""
	message := ""
	if value, has := JxString(event, "code"); has {
		code = value
	}
	if value, has := JxString(event, "message"); has {
		message = value
	}
	if nested, ok := JxObjectField(event, "error"); ok {
		if code == "" {
			code, _ = JxString(nested, "code")
		}
		if message == "" {
			message, _ = JxString(nested, "message")
		}
	}
	return code, message
}

// codexResponsesEvents adapts the raw SSE stream through mapCodexEvents:
// callbacks fire here, error/response.failed become errors, and the first
// terminal response event (response.done/completed/incomplete) is normalized
// to response.completed before ending the stream.
type codexResponsesEvents struct {
	stream *SSEStream
	mapper *codexEventMapper
	signal *abort.Signal
}

func (s *codexResponsesEvents) Next() (*jsonx.Obj, error) {
	if s.mapper.terminated {
		return nil, io.EOF
	}
	for {
		if s.signal != nil && s.signal.Aborted() {
			return nil, &csError{msg: "Request was aborted"}
		}
		event, err := s.stream.Next()
		if err != nil {
			return nil, err
		}
		if event.Data == SSEDone {
			continue
		}
		mapped, mappedDone, mapErr := s.mapper.mapEvent(event.Data)
		if mapErr != nil {
			return nil, mapErr
		}
		if mapped != nil {
			if mappedDone {
				s.mapper.terminated = true
			}
			return mapped, nil
		}
		if mappedDone {
			s.mapper.terminated = true
			return nil, io.EOF
		}
	}
}
