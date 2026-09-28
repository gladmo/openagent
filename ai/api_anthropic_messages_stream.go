package ai

// api_anthropic_messages_stream.go ports the streaming half of
// api/anthropic-messages.ts (stream/streamSimple, the SSE event loop, stop
// reason mapping, and error composition). See api_anthropic_messages.go for
// the request-building half.

import (
	"io"
	"strings"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

// AnthropicMessagesAPI implements ProviderStreams for "anthropic-messages".
type AnthropicMessagesAPI struct{}

// AnthropicMessagesApi returns the shared API instance.
func AnthropicMessagesApi() *AnthropicMessagesAPI { return &AnthropicMessagesAPI{} }

// Stream implements ProviderStreams with plain options (no API-specific
// fields; thinking is omitted unless set).
func (a *AnthropicMessagesAPI) Stream(model *Model, context *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream {
	anthropicOptions := &AnthropicOptions{}
	if options != nil {
		anthropicOptions.StreamOptions = *options
	}
	return a.StreamAnthropic(model, context, anthropicOptions)
}

// StreamSimple implements ProviderStreams.
func (a *AnthropicMessagesAPI) StreamSimple(model *Model, context *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
	plainOptions := options
	if plainOptions == nil {
		plainOptions = &SimpleStreamOptions{}
	}
	apiKey := ""
	if plainOptions.APIKey != nil {
		apiKey = *plainOptions.APIKey
	}

	anthropicAssertRequestAuth(model.Provider, apiKey, providerHeadersToRecord(plainOptions.Headers))

	base := BuildBaseOptions(model, context, plainOptions, apiKey)
	anthropicBase := &AnthropicOptions{StreamOptions: *base}
	anthropicBase.ToolChoice = plainOptions.ToolChoice

	if plainOptions.Reasoning == nil || *plainOptions.Reasoning == "" {
		disabled := false
		anthropicBase.ThinkingEnabled = &disabled
		return a.StreamAnthropic(model, context, anthropicBase)
	}

	// Adaptive thinking models use an effort level; older models a budget.
	if anthropicForceAdaptiveThinking(model) {
		enabled := true
		effort := mapThinkingLevelToEffort(model, *plainOptions.Reasoning)
		anthropicBase.ThinkingEnabled = &enabled
		anthropicBase.Effort = &effort
		return a.StreamAnthropic(model, context, anthropicBase)
	}

	maxTokensCap := model.MaxTokens
	if plainOptions.MaxTokens != nil {
		maxTokensCap = *plainOptions.MaxTokens
	}
	adjustedMaxTokens, thinkingBudget := AdjustMaxTokensForThinking(
		base.MaxTokens,
		maxTokensCap,
		*plainOptions.Reasoning,
		plainOptions.ThinkingBudgets,
	)
	maxTokens := ClampMaxTokensToContext(model, context, adjustedMaxTokens)

	enabled := true
	anthropicBase.MaxTokens = &maxTokens
	anthropicBase.ThinkingEnabled = &enabled
	budget := minF(thinkingBudget, maxF(0, maxTokens-1024))
	anthropicBase.ThinkingBudgetTokens = &budget
	return a.StreamAnthropic(model, context, anthropicBase)
}

// mapThinkingLevelToEffort maps a pi thinking level to an Anthropic effort.
func mapThinkingLevelToEffort(model *Model, level string) string {
	if mapped, present, isNull := anthropicThinkingLevelMapValue(model, level); present && !isNull {
		return mapped
	}
	switch level {
	case ThinkingMinimal, ThinkingLow:
		return AnthropicEffortLow
	case ThinkingMedium:
		return AnthropicEffortMedium
	default:
		return AnthropicEffortHigh
	}
}

// anthropicBlockState tracks the streaming scratch state of one content
// block (the TS code stores index/partialJson on the block itself). Value
// blocks are mutated through updateText/updateThinking which copy out of
// output.Content, mutate, and write back.
type anthropicBlockState struct {
	anthropicIndex int
	kind           string // "text" | "thinking" | "toolCall"
	contentIndex   int    // position in output.Content
	thinking       *ThinkingContent
	toolCall       *ToolCall
	partialJSON    string
}

func updateTextBlock(output *AssistantMessage, index int, fn func(*TextContent)) {
	if index < 0 || index >= len(output.Content) {
		return
	}
	if text, ok := output.Content[index].(TextContent); ok {
		fn(&text)
		output.Content[index] = text
	}
}

// StreamAnthropic ports the api-specific stream function.
func (a *AnthropicMessagesAPI) StreamAnthropic(model *Model, context *TranscriptContext, options *AnthropicOptions) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStream()
	compat := getAnthropicCompat(model)
	normalizedContext := ResolveTranscript(context, compat.SupportsMidConvoSystemMessages)
	currentTools := GetCurrentTools(normalizedContext.Messages)

	go func() {
		providerThinkingLevel := ""
		if anthropicSupportsMidConvoEffort(model) {
			providerThinkingLevel = AnthropicEffortHigh
			if options != nil && options.Effort != nil {
				providerThinkingLevel = *options.Effort
			}
		}
		output := &AssistantMessage{
			Content:     []ContentBlock{},
			API:         model.API,
			Provider:    model.Provider,
			Model:       model.ID,
			Usage:       zeroUsage(),
			StopReason:  StopPending,
			TimestampMs: nowMs(),
		}
		if providerThinkingLevel != "" {
			level := providerThinkingLevel
			output.ProviderThinkingLevel = &level
		}

		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = errorFromPanic(r)
				}
			}()
			return a.run(model, normalizedContext, currentTools, output, stream, options)
		}()

		if err != nil {
			output.StopReason = StopError
			if options != nil && options.Signal != nil && options.Signal.Aborted() {
				output.StopReason = StopAborted
			}
			message := err.Error()
			output.ErrorMessage = &message
			stream.Push(&EventError{Reason: output.StopReason, Error: output})
			stream.End()
		}
	}()

	return stream
}

func zeroUsage() Usage {
	return Usage{Cost: UsageCost{}}
}

// run executes the request/event loop; the caller converts the returned
// error into an error event.
func (a *AnthropicMessagesAPI) run(
	model *Model,
	normalizedContext *TranscriptContext,
	currentTools []Tool,
	output *AssistantMessage,
	stream *AssistantMessageEventStream,
	options *AnthropicOptions,
) error {
	var apiKey string
	var optionsHeaders ProviderHeaders
	if options != nil {
		if options.APIKey != nil {
			apiKey = *options.APIKey
		}
		optionsHeaders = options.Headers
	}

	cacheRetention := CacheRetentionShort
	if options != nil && options.CacheRetention != nil && *options.CacheRetention != "" {
		cacheRetention = *options.CacheRetention
	}
	sessionID := ""
	if options != nil && options.SessionID != nil {
		sessionID = *options.SessionID
		if cacheRetention == CacheRetentionNone {
			sessionID = ""
		}
	}

	// Copilot's gateway routes on the initiator/intent/vision headers for
	// every transport, this one included; the other transports wire the
	// same builder.
	var dynamicHeaders map[string]string
	if model.Provider == "github-copilot" {
		dynamicHeaders = BuildCopilotDynamicHeaders(normalizedContext.Messages, HasCopilotVisionInput(normalizedContext.Messages))
	}
	client := createAnthropicClient(model, apiKey, optionsHeaders, dynamicHeaders, sessionID)
	if err := anthropicAssertRequestAuth(model.Provider, apiKey, buildAnthropicRequestHeaders(client)); err != nil {
		return err
	}
	isOAuth := client.IsOAuthToken

	params := buildAnthropicParams(model, normalizedContext, isOAuth, options)
	if options != nil && options.OnPayload != nil {
		if next := options.OnPayload(params, model); next != nil {
			if obj, ok := JxObj(next); ok {
				obj.Set("stream", true)
				params = obj
			}
		}
	}

	requestHeaders := buildAnthropicRequestHeaders(client)
	payload := []byte(jsonx.Stringify(params))

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

	sse, err := postAnthropicMessages(client, requestHeaders, payload, &ProviderRetryOptions{
		MaxRetries:      maxRetries,
		MaxRetryDelayMs: maxRetryDelayMs,
		Signal:          signal,
	}, timeoutMs, fetch)
	if err != nil {
		return anthropicTransportError(err, options)
	}
	defer sse.Stream.Close()
	if options != nil && options.OnResponse != nil {
		options.OnResponse(ProviderResponse{Status: sse.Status, Headers: sse.Headers}, model)
	}
	stream.Push(&EventStart{Partial: output})

	usageModel := model
	var inputTransformations []any

	var blocks []*anthropicBlockState
	findBlock := func(index int) *anthropicBlockState {
		for _, state := range blocks {
			if state.anthropicIndex == index {
				return state
			}
		}
		return nil
	}

	sawMessageStart := false
	sawMessageEnd := false
	anthropicEventNames := map[string]bool{
		"message_start": true, "message_delta": true, "message_stop": true,
		"content_block_start": true, "content_block_delta": true, "content_block_stop": true,
	}

	pushBlock := func(state *anthropicBlockState) {
		output.Content = append(output.Content, nil)
		state.contentIndex = len(output.Content) - 1
		blocks = append(blocks, state)
	}

	for {
		event, err := sse.Stream.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			if options != nil && options.Signal != nil && options.Signal.Aborted() {
				return abort.NewAbortError("Request was aborted")
			}
			return &csError{msg: err.Error()}
		}
		if options != nil && options.Signal != nil && options.Signal.Aborted() {
			return abort.NewAbortError("Request was aborted")
		}

		if event.Event == "error" {
			return &csError{msg: event.Data}
		}
		if !anthropicEventNames[event.Event] {
			continue
		}

		parsed, parseErr := ParseJSONWithRepair(event.Data)
		if parseErr != nil {
			return &csError{msg: "Could not parse Anthropic SSE event " + event.Event + ": " + parseErr.Error() + "; data=" + event.Data}
		}
		eventObj, isObj := JxObj(parsed)
		if !isObj {
			return &csError{msg: "Could not parse Anthropic SSE event " + event.Event + ": expected object; data=" + event.Data}
		}
		eventType, _ := JxString(eventObj, "type")

		if options != nil && options.OnProviderStreamEvent != nil {
			options.OnProviderStreamEvent(eventObj, model)
		}

		switch eventType {
		case "message_start":
			sawMessageStart = true
			if message, ok := JxObjectField(eventObj, "message"); ok {
				if id, has := JxString(message, "id"); has {
					output.ResponseID = &id
				}
				if transformations, ok := JxList(message, "input_transformations"); ok {
					inputTransformations = transformations
				}
				if responseModel, has := JxString(message, "model"); has && responseModel != model.ID {
					output.ResponseModel = &responseModel
					for _, fallback := range anthropicAllowedFallbackModels(model) {
						if fallback.Provider == model.Provider && fallback.Model == responseModel && fallback.Cost != nil {
							usageModel = &Model{ID: responseModel, Provider: model.Provider, API: model.API, BaseURL: model.BaseURL, Cost: *fallback.Cost}
							break
						}
					}
				}
				usage, _ := JxObjectField(message, "usage")
				output.Usage.Input = jxf(usage, "input_tokens")
				output.Usage.Output = jxf(usage, "output_tokens")
				output.Usage.CacheRead = jxf(usage, "cache_read_input_tokens")
				output.Usage.CacheWrite = jxf(usage, "cache_creation_input_tokens")
				cacheWrite1h := 0.0
				if usage != nil {
					if creation, ok := JxObjectField(usage, "cache_creation"); ok {
						cacheWrite1h = jxf(creation, "ephemeral_1h_input_tokens")
					}
				}
				output.Usage.CacheWrite1h = &cacheWrite1h
				output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
				CalculateCost(usageModel, &output.Usage)
			}

		case "content_block_start":
			index := int(jxf(eventObj, "index"))
			block, _ := JxObjectField(eventObj, "content_block")
			blockType, _ := JxString(block, "type")
			switch blockType {
			case "fallback":
				if len(output.Content) > 0 {
					return &csError{msg: "Anthropic performed an unsupported mid-output model fallback"}
				}
			case "text":
				text, _ := JxString(block, "text")
				state := &anthropicBlockState{anthropicIndex: index, kind: "text"}
				pushBlock(state)
				output.Content[state.contentIndex] = TextContent{Text: text}
				stream.Push(&EventTextStart{ContentIndex: state.contentIndex, Partial: output})
			case "thinking":
				thinking, _ := JxString(block, "thinking")
				signature, _ := JxString(block, "signature")
				state := &anthropicBlockState{anthropicIndex: index, kind: "thinking", thinking: &ThinkingContent{Thinking: thinking, ThinkingSignature: &signature}}
				pushBlock(state)
				output.Content[state.contentIndex] = *state.thinking
				stream.Push(&EventThinkingStart{ContentIndex: state.contentIndex, Partial: output})
			case "redacted_thinking":
				data, _ := JxString(block, "data")
				redacted := true
				state := &anthropicBlockState{anthropicIndex: index, kind: "thinking", thinking: &ThinkingContent{Thinking: "[Reasoning redacted]", ThinkingSignature: &data, Redacted: &redacted}}
				pushBlock(state)
				output.Content[state.contentIndex] = *state.thinking
				stream.Push(&EventThinkingStart{ContentIndex: state.contentIndex, Partial: output})
			case "tool_use":
				id, _ := JxString(block, "id")
				name, _ := JxString(block, "name")
				if isOAuth {
					name = FromClaudeCodeName(name, currentTools)
				}
				state := &anthropicBlockState{anthropicIndex: index, kind: "toolCall", toolCall: &ToolCall{ID: id, Name: name, Arguments: jsonx.NewObj()}}
				pushBlock(state)
				output.Content[state.contentIndex] = state.toolCall
				stream.Push(&EventToolCallStart{ContentIndex: state.contentIndex, Partial: output})
			}

		case "content_block_delta":
			index := int(jxf(eventObj, "index"))
			delta, _ := JxObjectField(eventObj, "delta")
			deltaType, _ := JxString(delta, "type")
			state := findBlock(index)
			if state == nil {
				continue
			}
			// A delta whose type does not match the block it indexes is
			// skipped, mirroring the TS tolerance for gateways that emit
			// mismatched deltas instead of failing the stream.
			switch deltaType {
			case "text_delta":
				if state.kind != "text" {
					continue
				}
				text, _ := JxString(delta, "text")
				updateTextBlock(output, state.contentIndex, func(t *TextContent) { t.Text += text })
				stream.Push(&EventTextDelta{ContentIndex: state.contentIndex, Delta: text, Partial: output})
			case "thinking_delta":
				if state.kind != "thinking" {
					continue
				}
				thinking, _ := JxString(delta, "thinking")
				state.thinking.Thinking += thinking
				output.Content[state.contentIndex] = *state.thinking
				stream.Push(&EventThinkingDelta{ContentIndex: state.contentIndex, Delta: thinking, Partial: output})
			case "input_json_delta":
				if state.kind != "toolCall" {
					continue
				}
				partialJSON, _ := JxString(delta, "partial_json")
				state.partialJSON += partialJSON
				state.toolCall.Arguments = ParseStreamingJSONObject(state.partialJSON)
				stream.Push(&EventToolCallDelta{ContentIndex: state.contentIndex, Delta: partialJSON, Partial: output})
			case "signature_delta":
				if state.kind != "thinking" {
					continue
				}
				signature, _ := JxString(delta, "signature")
				if state.thinking.ThinkingSignature == nil {
					state.thinking.ThinkingSignature = &signature
				} else {
					*state.thinking.ThinkingSignature += signature
				}
				output.Content[state.contentIndex] = *state.thinking
			}

		case "content_block_stop":
			index := int(jxf(eventObj, "index"))
			state := findBlock(index)
			if state == nil {
				continue
			}
			switch state.kind {
			case "text":
				var finalText TextContent
				if text, ok := output.Content[state.contentIndex].(TextContent); ok {
					finalText = text
				}
				stream.Push(&EventTextEnd{ContentIndex: state.contentIndex, Content: finalText.Text, Partial: output})
			case "thinking":
				stream.Push(&EventThinkingEnd{ContentIndex: state.contentIndex, Content: state.thinking.Thinking, Partial: output})
			case "toolCall":
				state.toolCall.Arguments = ParseStreamingJSONObject(state.partialJSON)
				stream.Push(&EventToolCallEnd{ContentIndex: state.contentIndex, ToolCall: state.toolCall, Partial: output})
			}

		case "message_delta":
			if transformations, ok := JxList(eventObj, "input_transformations"); ok {
				inputTransformations = transformations
			}
			delta, _ := JxObjectField(eventObj, "delta")
			if delta != nil {
				if reason, has := JxString(delta, "stop_reason"); has && reason != "" {
					output.RawStopReason = &reason
					stopDetails, _ := JxObjectField(delta, "stop_details")
					stopReason, errorMessage, err := mapAnthropicStopReason(reason, stopDetails)
					if err != nil {
						return err
					}
					output.StopReason = stopReason
					if errorMessage != nil {
						output.ErrorMessage = errorMessage
					}
				}
			}
			if usage, ok := JxObjectField(eventObj, "usage"); ok {
				if value, present := JxFloat(usage, "input_tokens"); present {
					output.Usage.Input = value
				}
				if value, present := JxFloat(usage, "output_tokens"); present {
					output.Usage.Output = value
				}
				if value, present := JxFloat(usage, "cache_read_input_tokens"); present {
					output.Usage.CacheRead = value
				}
				if value, present := JxFloat(usage, "cache_creation_input_tokens"); present {
					output.Usage.CacheWrite = value
				}
				if creation, ok := JxObjectField(usage, "cache_creation"); ok {
					if value, present := JxFloat(creation, "ephemeral_1h_input_tokens"); present {
						output.Usage.CacheWrite1h = &value
					}
				}
				if details, ok := JxObjectField(usage, "output_tokens_details"); ok {
					if value, present := JxFloat(details, "thinking_tokens"); present {
						output.Usage.Reasoning = &value
					}
				}
			}
			output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
			CalculateCost(usageModel, &output.Usage)

		case "message_stop":
			sawMessageEnd = true
		}
	}

	if sawMessageStart && !sawMessageEnd {
		return &csError{msg: "Anthropic stream ended before message_stop"}
	}
	if options != nil && options.Signal != nil && options.Signal.Aborted() {
		return abort.NewAbortError("Request was aborted")
	}
	if output.StopReason == StopPending {
		return &csError{msg: "Anthropic stream ended without a stop reason"}
	}
	if output.StopReason == StopAborted || output.StopReason == StopError {
		message := "An unknown error occurred"
		if output.ErrorMessage != nil {
			message = *output.ErrorMessage
		}
		return &csError{msg: message}
	}
	if len(inputTransformations) > 0 {
		var transformationObjs []any
		for _, transformation := range inputTransformations {
			if obj, ok := JxObj(transformation); ok {
				entry := jsonx.NewObj()
				if t, has := JxString(obj, "type"); has {
					entry.Set("type", t)
				}
				if p, has := JxString(obj, "path"); has {
					entry.Set("path", p)
				}
				if r, has := JxString(obj, "reason"); has {
					entry.Set("reason", r)
				}
				transformationObjs = append(transformationObjs, entry)
			}
		}
		AppendAssistantMessageDiagnostic(output, CreateAssistantMessageDiagnostic("anthropic_input_transformations", nil, jsonx.ObjFrom("transformations", transformationObjs)))
	}

	stream.Push(&EventDone{Reason: output.StopReason, Message: output})
	stream.End(output)
	return nil
}

func jxf(obj *jsonx.Obj, key string) float64 {
	value, _ := JxFloat(obj, key)
	return value
}

func postAnthropicMessages(client anthropicClientConfig, headers map[string]string, payload []byte, retryOptions *ProviderRetryOptions, timeoutMs *float64, fetch FetchFunction) (*SSEStreamResponse, error) {
	var signal *abort.Signal
	if retryOptions != nil {
		signal = retryOptions.Signal
	}
	url := strings.TrimSuffix(client.BaseURL, "/") + "/v1/messages?beta=true"

	var response *SSEStreamResponse
	request := func() error {
		fetchOptions := &StreamOptions{Signal: signal, TimeoutMs: timeoutMs, Fetch: fetch}
		sse, err := PostJSONStream(url, headers, payload, fetchOptions)
		if err != nil {
			return WrapStainlessHTTPError(err)
		}
		response = sse
		return nil
	}
	if err := RetryProviderRequest(request, retryOptions); err != nil {
		return nil, err
	}
	return response, nil
}

func itoaStatus(status int) string {
	if status == 0 {
		return "0"
	}
	negative := status < 0
	if negative {
		status = -status
	}
	digits := []byte{}
	for status > 0 {
		digits = append([]byte{byte('0' + status%10)}, digits...)
		status /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

func anthropicTransportError(err error, options *AnthropicOptions) error {
	if options != nil && options.Signal != nil && options.Signal.Aborted() {
		return abort.NewAbortError("Request was aborted")
	}
	return err
}

// mapAnthropicStopReason ports mapStopReason; unknown reasons error like TS.
func mapAnthropicStopReason(reason string, stopDetails *jsonx.Obj) (string, *string, error) {
	switch reason {
	case "end_turn":
		return StopStop, nil, nil
	case "max_tokens":
		return StopLength, nil, nil
	case "tool_use":
		return StopToolUse, nil, nil
	case "refusal":
		explanation := ""
		if stopDetails != nil {
			if value, has := JxString(stopDetails, "explanation"); has {
				explanation = value
			}
		}
		if explanation == "" {
			explanation = "The model refused to complete the request"
		}
		return StopError, &explanation, nil
	case "pause_turn":
		return StopStop, nil, nil
	case "stop_sequence":
		return StopStop, nil, nil
	case "sensitive":
		message := "Provider stopped with: sensitive"
		return StopError, &message, nil
	default:
		return "", nil, &csError{msg: "Unhandled stop reason: " + reason}
	}
}
