package ai

// api_openai_completions_stream.go ports the streaming half of
// api/openai-completions.ts: Stream/StreamSimple and the SSE chunk loop. See
// api_openai_completions.go for the request-building half.

import (
	"io"
	"strings"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

// OpenAICompletionsAPI implements ProviderStreams for "openai-completions".
type OpenAICompletionsAPI struct{}

// OpenAICompletionsApi returns the shared API instance.
func OpenAICompletionsApi() *OpenAICompletionsAPI { return &OpenAICompletionsAPI{} }

// Stream implements ProviderStreams with plain options.
func (a *OpenAICompletionsAPI) Stream(model *Model, context *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream {
	completionsOptions := &OpenAICompletionsOptions{}
	if options != nil {
		completionsOptions.StreamOptions = *options
	}
	return a.StreamCompletions(model, context, completionsOptions)
}

// StreamSimple implements ProviderStreams.
func (a *OpenAICompletionsAPI) StreamSimple(model *Model, context *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
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
	completionsOptions := &OpenAICompletionsOptions{
		StreamOptions:   *base,
		ToolChoice:      plainOptions.ToolChoice,
		ThinkingBudgets: plainOptions.ThinkingBudgets,
	}
	if plainOptions.Reasoning != nil && *plainOptions.Reasoning != "" {
		clamped := ClampThinkingLevel(model, *plainOptions.Reasoning)
		if clamped != ThinkingOff {
			completionsOptions.ReasoningEffort = &clamped
		}
	}
	return a.StreamCompletions(model, context, completionsOptions)
}

// completionsToolCallState is the streaming scratch for one tool call block.
type completionsToolCallState struct {
	call           *ToolCall
	contentIndex   int
	partialArgs    string
	hasPartialArgs bool
	customProperty string
	jsonBuffer     *GrammarToolInputJSONBuffer
	streamIndex    int
	hasStreamIndex bool
}

// StreamCompletions ports the api-specific stream function.
func (a *OpenAICompletionsAPI) StreamCompletions(model *Model, context *TranscriptContext, options *OpenAICompletionsOptions) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStream()
	compat := getOpenAICompletionsCompat(model)
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

		// reasoning_details replay metadata lives in memory while streaming
		// and serializes into the signature when the block finalizes.
		streamedReasoningDetails := []any(nil)
		applyStreamedReasoningDetails := func(index int) {
			if streamedReasoningDetails != nil {
				updateThinkingBlock(output, index, func(t *ThinkingContent) {
					signature := jsonx.Stringify(streamedReasoningDetails)
					t.ThinkingSignature = &signature
				})
			}
		}

		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = errorFromPanic(r)
				}
			}()
			return a.run(model, normalizedContext, output, stream, options, compat, &streamedReasoningDetails, applyStreamedReasoningDetails)
		}()

		if err != nil {
			for index, block := range output.Content {
				if _, isThinking := block.(ThinkingContent); isThinking {
					applyStreamedReasoningDetails(index)
				}
			}
			output.StopReason = StopError
			if options != nil && options.Signal != nil && options.Signal.Aborted() {
				output.StopReason = StopAborted
			}
			message := FormatProviderError(NormalizeProviderError(err), "")
			if httpErr, ok := err.(*ProviderHTTPError); ok {
				if raw := rawMetadataOf(httpErr); raw != "" && !strings.Contains(message, raw) {
					message += "\n" + raw
				}
			}
			output.ErrorMessage = &message
			stream.Push(&EventError{Reason: output.StopReason, Error: output})
			stream.End()
		}
	}()

	return stream
}

func updateThinkingBlock(output *AssistantMessage, index int, fn func(*ThinkingContent)) {
	if index < 0 || index >= len(output.Content) {
		return
	}
	if thinking, ok := output.Content[index].(ThinkingContent); ok {
		fn(&thinking)
		output.Content[index] = thinking
	}
}

func rawMetadataOf(httpErr *ProviderHTTPError) string {
	parsed, err := ParseJSONWithRepair(httpErr.Body)
	if err != nil {
		return ""
	}
	obj, isObj := JxObj(parsed)
	if !isObj {
		return ""
	}
	errorObj, has := JxObjectField(obj, "error")
	if !has {
		return ""
	}
	metadata, has := JxObjectField(errorObj, "metadata")
	if !has {
		return ""
	}
	raw, has := metadata.Get("raw")
	if !has || raw == nil {
		return ""
	}
	if s, isString := raw.(string); isString {
		return s
	}
	return jsonx.Stringify(raw)
}

func (a *OpenAICompletionsAPI) run(
	model *Model,
	normalizedContext *TranscriptContext,
	output *AssistantMessage,
	stream *AssistantMessageEventStream,
	options *OpenAICompletionsOptions,
	compat openAICompletionsCompat,
	streamedReasoningDetails *[]any,
	applyStreamedReasoningDetails func(int),
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

	grammarToolInputProperties := CreateGrammarToolInputProperties(
		GetDeclaredTools(normalizedContext.Messages),
		compat.SupportsOpenAIGrammarTools,
	)
	cacheRetention := CacheRetentionShort
	if options != nil && options.CacheRetention != nil && *options.CacheRetention != "" {
		cacheRetention = *options.CacheRetention
	} else if options != nil && GetProviderEnvValue("PI_CACHE_RETENTION", options.Env) == "long" {
		cacheRetention = CacheRetentionLong
	}
	sessionID := ""
	if options != nil && options.SessionID != nil && cacheRetention != CacheRetentionNone {
		sessionID = *options.SessionID
	}
	headers := createOpenAICompletionsClient(model, normalizedContext, apiKey, optionsHeaders, sessionID, compat)

	params := buildOpenAICompletionsParams(model, normalizedContext, options, compat, cacheRetention, grammarToolInputProperties)
	if options != nil && options.OnPayload != nil {
		if next := options.OnPayload(params, model); next != nil {
			if obj, ok := JxObj(next); ok {
				params = obj
			}
		}
	}
	// The TS SDK enforces stream on the streaming create call.
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

	url := strings.TrimSuffix(model.BaseURL, "/") + "/chat/completions"
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

	// Block scratch state.
	textIndex := -1
	thinkingIndex := -1
	hasFinishReason := false
	toolCallsByStreamIndex := map[int]*completionsToolCallState{}
	toolCallsByID := map[string]*completionsToolCallState{}

	pushContent := func(block ContentBlock) int {
		output.Content = append(output.Content, block)
		return len(output.Content) - 1
	}

	ensureTextBlock := func() int {
		if textIndex == -1 {
			textIndex = pushContent(TextContent{Text: ""})
			stream.Push(&EventTextStart{ContentIndex: textIndex, Partial: output})
		}
		return textIndex
	}
	ensureThinkingBlock := func(signature string) int {
		if thinkingIndex == -1 {
			thinkingIndex = pushContent(ThinkingContent{Thinking: "", ThinkingSignature: &signature})
			stream.Push(&EventThinkingStart{ContentIndex: thinkingIndex, Partial: output})
		}
		return thinkingIndex
	}

	ensureToolCallBlock := func(toolCall *jsonx.Obj) *completionsToolCallState {
		streamIndex, hasStreamIndex := -1, false
		if value, present := JxFloat(toolCall, "index"); present {
			streamIndex, hasStreamIndex = int(value), true
		}
		function, _ := JxObjectField(toolCall, "function")
		custom, _ := JxObjectField(toolCall, "custom")
		name := ""
		if function != nil {
			name, _ = JxString(function, "name")
		}
		if custom != nil {
			name, _ = JxString(custom, "name")
		}
		id, _ := JxString(toolCall, "id")

		state := (*completionsToolCallState)(nil)
		if hasStreamIndex {
			state = toolCallsByStreamIndex[streamIndex]
		}
		if state == nil && id != "" {
			state = toolCallsByID[id]
		}
		if state == nil {
			_, hasFunction := toolCall.Get("function")
			customInputProperty := ""
			if _, has := toolCall.Get("custom"); has && !hasFunction {
				if property, inMap := grammarToolInputProperties[name]; inMap {
					customInputProperty = property
				} else {
					customInputProperty = "input"
				}
			}
			call := &ToolCall{ID: id, Name: name, Arguments: jsonx.NewObj()}
			if customInputProperty != "" {
				call.Arguments = jsonx.ObjFrom(customInputProperty, "")
			}
			contentIndex := pushContent(call)
			state = &completionsToolCallState{
				call:           call,
				contentIndex:   contentIndex,
				customProperty: customInputProperty,
				jsonBuffer:     &GrammarToolInputJSONBuffer{},
			}
			if customInputProperty == "" {
				state.hasPartialArgs = true
			}
			if hasStreamIndex {
				state.streamIndex, state.hasStreamIndex = streamIndex, true
				toolCallsByStreamIndex[streamIndex] = state
			}
			if id != "" {
				toolCallsByID[id] = state
			}
			stream.Push(&EventToolCallStart{ContentIndex: contentIndex, Partial: output})
		}
		if hasStreamIndex && !state.hasStreamIndex {
			state.streamIndex, state.hasStreamIndex = streamIndex, true
			toolCallsByStreamIndex[streamIndex] = state
		}
		if id != "" {
			toolCallsByID[id] = state
		}
		if state.call.Name == "" && name != "" {
			state.call.Name = name
		}
		if custom != nil {
			_, hasFunction := toolCall.Get("function")
			if !hasFunction && state.customProperty == "" {
				property := "input"
				if mapped, inMap := grammarToolInputProperties[state.call.Name]; inMap {
					property = mapped
				}
				state.customProperty = property
				state.call.Arguments = jsonx.ObjFrom(property, "")
				state.jsonBuffer = &GrammarToolInputJSONBuffer{}
				state.hasPartialArgs = false
			}
		}
		return state
	}

	getCustomToolCallInput := func(state *completionsToolCallState) string {
		if state.customProperty == "" {
			return ""
		}
		if value, present := state.call.Arguments.Get(state.customProperty); present {
			if s, isString := value.(string); isString {
				return s
			}
		}
		return ""
	}
	appendCustomToolCallInput := func(state *completionsToolCallState, nextInput string, close bool) (string, bool) {
		if state.customProperty == "" {
			return "", false
		}
		delta, err := AppendGrammarToolInputJSONDelta(state.jsonBuffer, state.customProperty, nextInput, close)
		if err != nil {
			panic(err)
		}
		state.call.Arguments = jsonx.ObjFrom(state.customProperty, nextInput)
		return delta, delta != ""
	}

	finishBlock := func(index int) {
		if index < 0 || index >= len(output.Content) {
			return
		}
		switch block := output.Content[index].(type) {
		case TextContent:
			stream.Push(&EventTextEnd{ContentIndex: index, Content: block.Text, Partial: output})
		case ThinkingContent:
			applyStreamedReasoningDetails(index)
			if thinking, ok := output.Content[index].(ThinkingContent); ok {
				stream.Push(&EventThinkingEnd{ContentIndex: index, Content: thinking.Thinking, Partial: output})
			}
		case *ToolCall:
			var state *completionsToolCallState
			for _, candidate := range toolCallsByID {
				if candidate.call == block {
					state = candidate
					break
				}
			}
			if state == nil {
				for _, candidate := range toolCallsByStreamIndex {
					if candidate.call == block {
						state = candidate
						break
					}
				}
			}
			if state != nil && state.customProperty != "" {
				if delta, has := appendCustomToolCallInput(state, getCustomToolCallInput(state), true); has {
					stream.Push(&EventToolCallDelta{ContentIndex: index, Delta: delta, Partial: output})
				}
			} else if state != nil {
				state.call.Arguments = ParseStreamingJSONObject(state.partialArgs)
			}
			stream.Push(&EventToolCallEnd{ContentIndex: index, ToolCall: block, Partial: output})
		}
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
		if event.Data == SSEDone {
			continue
		}

		parsed, parseErr := ParseJSONWithRepair(event.Data)
		if parseErr != nil {
			continue
		}
		chunk, isObj := JxObj(parsed)
		if !isObj {
			continue
		}
		if options != nil && options.OnProviderStreamEvent != nil {
			options.OnProviderStreamEvent(chunk, model)
		}

		if output.ResponseID == nil {
			if id, has := JxString(chunk, "id"); has && id != "" {
				output.ResponseID = &id
			}
		}
		if output.ResponseModel == nil {
			if chunkModel, has := JxString(chunk, "model"); has && len(chunkModel) > 0 && chunkModel != model.ID {
				output.ResponseModel = &chunkModel
			}
		}
		if usage, ok := JxObjectField(chunk, "usage"); ok {
			output.Usage = parseChunkUsage(usage, model)
		}

		choices, hasChoices := JxList(chunk, "choices")
		if !hasChoices || len(choices) == 0 {
			continue
		}
		choice, isObj := JxObj(choices[0])
		if !isObj {
			continue
		}

		if !hasUsage(chunk) {
			if usage, ok := JxObjectField(choice, "usage"); ok {
				output.Usage = parseChunkUsage(usage, model)
			}
		}

		if finishReason, has := JxString(choice, "finish_reason"); has && finishReason != "" {
			output.RawStopReason = &finishReason
			stopReason, errorMessage := mapOpenAIStopReason(finishReason)
			output.StopReason = stopReason
			if errorMessage != nil {
				output.ErrorMessage = errorMessage
			}
			hasFinishReason = true
		}

		delta, hasDelta := JxObjectField(choice, "delta")
		if hasDelta {
			if content, has := JxString(delta, "content"); has && len(content) > 0 {
				index := ensureTextBlock()
				updateTextBlock(output, index, func(t *TextContent) { t.Text += content })
				stream.Push(&EventTextDelta{ContentIndex: index, Delta: content, Partial: output})
			}

			// Reasoning arrives in reasoning_content (llama.cpp), reasoning,
			// or reasoning_text. First non-empty field wins.
			reasoningFields := []string{"reasoning_content", "reasoning", "reasoning_text"}
			foundReasoningField := ""
			var reasoningDelta string
			for _, field := range reasoningFields {
				if value, has := JxString(delta, field); has && len(value) > 0 {
					foundReasoningField = field
					reasoningDelta = value
					break
				}
			}
			if foundReasoningField != "" {
				thinkingSignature := foundReasoningField
				if model.Provider == "opencode-go" && foundReasoningField == "reasoning" {
					thinkingSignature = "reasoning_content"
				}
				index := ensureThinkingBlock(thinkingSignature)
				updateThinkingBlock(output, index, func(t *ThinkingContent) { t.Thinking += reasoningDelta })
				stream.Push(&EventThinkingDelta{ContentIndex: index, Delta: reasoningDelta, Partial: output})
			}

			if toolCallDeltas, ok := JxList(delta, "tool_calls"); ok {
				for _, entry := range toolCallDeltas {
					toolCall, isObj := JxObj(entry)
					if !isObj {
						continue
					}
					state := ensureToolCallBlock(toolCall)
					if id, has := JxString(toolCall, "id"); has && id != "" && state.call.ID == "" {
						state.call.ID = id
						toolCallsByID[id] = state
					}
					function, _ := JxObjectField(toolCall, "function")
					custom, _ := JxObjectField(toolCall, "custom")
					name := ""
					if function != nil {
						name, _ = JxString(function, "name")
					}
					if custom != nil {
						name, _ = JxString(custom, "name")
					}
					if state.call.Name == "" && name != "" {
						state.call.Name = name
					}

					deltaValue := ""
					if function != nil {
						if arguments, has := JxString(function, "arguments"); has && arguments != "" {
							deltaValue = arguments
							state.partialArgs += arguments
							state.hasPartialArgs = true
							state.call.Arguments = ParseStreamingJSONObject(state.partialArgs)
						}
					} else if custom != nil {
						if input, has := JxString(custom, "input"); has && input != "" {
							nextInput := getCustomToolCallInput(state) + input
							if appended, has := appendCustomToolCallInput(state, nextInput, false); has {
								deltaValue = appended
							}
						}
					}
					stream.Push(&EventToolCallDelta{ContentIndex: state.contentIndex, Delta: deltaValue, Partial: output})
				}
			}

			if reasoningDetails, ok := JxList(delta, "reasoning_details"); ok {
				for _, detail := range reasoningDetails {
					if !isOpenAIReasoningDetail(detail) {
						continue
					}
					ensureThinkingBlock("")
					*streamedReasoningDetails = appendOpenAIReasoningDetail(*streamedReasoningDetails, detail)
				}
			}
		}
	}

	for index := range output.Content {
		finishBlock(index)
	}
	if options != nil && options.Signal != nil && options.Signal.Aborted() {
		return abort.NewAbortError("Request was aborted")
	}
	if output.StopReason == StopAborted {
		return abort.NewAbortError("Request was aborted")
	}
	if !hasFinishReason && !compat.SupportsFinishReason {
		hasToolCallBlock := false
		for _, block := range output.Content {
			if _, isCall := block.(*ToolCall); isCall {
				hasToolCallBlock = true
				break
			}
		}
		if hasToolCallBlock {
			output.StopReason = StopToolUse
		} else {
			output.StopReason = StopStop
		}
	}
	if output.StopReason == StopError {
		message := "Provider returned an error stop reason"
		if output.ErrorMessage != nil {
			message = *output.ErrorMessage
		}
		return &csError{msg: message}
	}
	if (compat.SupportsFinishReason && !hasFinishReason) || output.StopReason == StopPending {
		return &csError{msg: "Stream ended without finish_reason"}
	}

	stream.Push(&EventDone{Reason: output.StopReason, Message: output})
	stream.End(output)
	return nil
}

func hasUsage(chunk *jsonx.Obj) bool {
	usage, ok := JxObjectField(chunk, "usage")
	return ok && usage != nil
}
