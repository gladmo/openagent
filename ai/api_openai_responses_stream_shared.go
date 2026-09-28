package ai

// api_openai_responses_stream_shared.go ports processResponsesStream from
// api/openai-responses-shared.ts: the shared Responses-API SSE event pump.

import (
	"io"
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// responsesStreamOptions mirrors OpenAIResponsesStreamOptions.
type responsesStreamOptions struct {
	OnProviderStreamEvent      func(any, *Model)
	ServiceTier                *string
	GrammarToolInputProperties map[string]string
	ResolveServiceTier         func(responseServiceTier, requestServiceTier *string) *string
	ApplyServiceTierPricing    func(usage *Usage, serviceTier *string)
}

type responsesSlot struct {
	kind           string // "thinking" | "text" | "toolCall"
	contentIndex   int
	toolCall       *ToolCall
	partialJSON    string
	hasPartialJSON bool
	customProperty string
	jsonBuffer     *GrammarToolInputJSONBuffer
}

func responsesCustomToolCallInput(slot *responsesSlot) string {
	if slot.customProperty == "" || slot.toolCall.Arguments == nil {
		return ""
	}
	if value, present := slot.toolCall.Arguments.Get(slot.customProperty); present {
		if s, isString := value.(string); isString {
			return s
		}
	}
	return ""
}

func responsesAppendCustomToolCallInput(slot *responsesSlot, nextInput string, close bool) (string, bool) {
	if slot.customProperty == "" {
		return "", false
	}
	delta, err := AppendGrammarToolInputJSONDelta(slot.jsonBuffer, slot.customProperty, nextInput, close)
	if err != nil {
		panic(err)
	}
	slot.toolCall.Arguments = jsonx.ObjFrom(slot.customProperty, nextInput)
	return delta, delta != ""
}

// responsesEventSource yields parsed Responses stream events; io.EOF ends.
type responsesEventSource interface {
	Next() (*jsonx.Obj, error)
}

// sseResponsesEvents adapts an SSEStream into a responsesEventSource (data
// JSON objects; [DONE] and non-object payloads skipped).
type sseResponsesEvents struct {
	stream *SSEStream
}

func (s *sseResponsesEvents) Next() (*jsonx.Obj, error) {
	for {
		event, err := s.stream.Next()
		if err != nil {
			return nil, err
		}
		if event.Data == SSEDone {
			continue
		}
		parsed, parseErr := ParseJSONWithRepair(event.Data)
		if parseErr != nil {
			continue
		}
		obj, isObj := JxObj(parsed)
		if !isObj {
			continue
		}
		return obj, nil
	}
}

// processResponsesStream pumps the event source into pi events. Returns
// an error mirroring the TS throws.
func processResponsesStream(
	source responsesEventSource,
	output *AssistantMessage,
	stream *AssistantMessageEventStream,
	model *Model,
	options *responsesStreamOptions,
) error {
	sawTerminalResponseEvent := false
	outputSlots := map[int]*responsesSlot{}
	reasoningBlocksByID := map[string]int{}

	applyMessagePhaseStopReason := func(item *jsonx.Obj) {
		if itemType, _ := JxString(item, "type"); itemType == "message" {
			if phase, has := JxString(item, "phase"); has && phase == "final_answer" {
				output.StopReason = StopStop
			}
		}
	}

	getSlot := func(outputIndex int, kind string) *responsesSlot {
		slot, ok := outputSlots[outputIndex]
		if !ok || slot.kind != kind {
			return nil
		}
		return slot
	}

	pushToolCallDelta := func(slot *responsesSlot, delta string, has bool) {
		if !has {
			return
		}
		stream.Push(&EventToolCallDelta{ContentIndex: slot.contentIndex, Delta: delta, Partial: output})
	}

	createSlot := func(outputIndex int, item *jsonx.Obj) *responsesSlot {
		itemType, _ := JxString(item, "type")
		switch itemType {
		case "reasoning":
			output.Content = append(output.Content, ThinkingContent{Thinking: ""})
			slot := &responsesSlot{kind: "thinking", contentIndex: len(output.Content) - 1}
			outputSlots[outputIndex] = slot
			stream.Push(&EventThinkingStart{ContentIndex: slot.contentIndex, Partial: output})
			return slot
		case "message":
			applyMessagePhaseStopReason(item)
			output.Content = append(output.Content, TextContent{Text: ""})
			slot := &responsesSlot{kind: "text", contentIndex: len(output.Content) - 1}
			outputSlots[outputIndex] = slot
			stream.Push(&EventTextStart{ContentIndex: slot.contentIndex, Partial: output})
			return slot
		case "function_call":
			callID, _ := JxString(item, "call_id")
			itemID, _ := JxString(item, "id")
			name, _ := JxString(item, "name")
			arguments, _ := JxString(item, "arguments")
			call := &ToolCall{ID: callID + "|" + itemID, Name: name, Arguments: jsonx.NewObj()}
			if namespace, has := JxString(item, "namespace"); has {
				call.Namespace = &namespace
			}
			output.Content = append(output.Content, call)
			slot := &responsesSlot{
				kind: "toolCall", contentIndex: len(output.Content) - 1,
				toolCall: call, partialJSON: arguments, hasPartialJSON: true,
			}
			outputSlots[outputIndex] = slot
			stream.Push(&EventToolCallStart{ContentIndex: slot.contentIndex, Partial: output})
			return slot
		case "custom_tool_call":
			name, _ := JxString(item, "name")
			callID, _ := JxString(item, "call_id")
			itemID, _ := JxString(item, "id")
			input, _ := JxString(item, "input")
			inputProperty := "input"
			if options != nil && options.GrammarToolInputProperties != nil {
				if property, inMap := options.GrammarToolInputProperties[name]; inMap {
					inputProperty = property
				}
			}
			call := &ToolCall{ID: callID + "|" + itemID, Name: name, Arguments: jsonx.ObjFrom(inputProperty, input)}
			if namespace, has := JxString(item, "namespace"); has {
				call.Namespace = &namespace
			}
			output.Content = append(output.Content, call)
			slot := &responsesSlot{
				kind: "toolCall", contentIndex: len(output.Content) - 1,
				toolCall: call, customProperty: inputProperty, jsonBuffer: &GrammarToolInputJSONBuffer{},
			}
			outputSlots[outputIndex] = slot
			stream.Push(&EventToolCallStart{ContentIndex: slot.contentIndex, Partial: output})
			return slot
		}
		return nil
	}

	getOrCreateSlot := func(outputIndex int, item *jsonx.Obj) *responsesSlot {
		if slot, ok := outputSlots[outputIndex]; ok {
			return slot
		}
		return createSlot(outputIndex, item)
	}

	// Azure can omit encrypted_content from output_item.done and provide it
	// only in the terminal response; backfill persisted signatures.
	backfillReasoningSignatures := func(responseOutput []any) {
		for _, entry := range responseOutput {
			item, ok := JxObj(entry)
			if !ok {
				continue
			}
			if itemType, _ := JxString(item, "type"); itemType != "reasoning" {
				continue
			}
			encryptedContent, has := JxString(item, "encrypted_content")
			if !has || encryptedContent == "" {
				continue
			}
			id, _ := JxString(item, "id")
			contentIndex, known := reasoningBlocksByID[id]
			if !known {
				continue
			}
			updateThinkingBlock(output, contentIndex, func(t *ThinkingContent) {
				if t.ThinkingSignature == nil {
					return
				}
				storedItem, err := ParseJSONWithRepair(*t.ThinkingSignature)
				if err != nil {
					return
				}
				storedObj, isObj := JxObj(storedItem)
				if !isObj {
					return
				}
				if _, has := JxString(storedObj, "encrypted_content"); has {
					return
				}
				storedObj.Set("encrypted_content", encryptedContent)
				t.ThinkingSignature = strPtrOf(jsonx.Stringify(storedObj))
			})
		}
	}

	finalizeResponse := func(response *jsonx.Obj) {
		sawTerminalResponseEvent = true
		if responseOutput, ok := JxList(response, "output"); ok {
			backfillReasoningSignatures(responseOutput)
		}
		if id, has := JxString(response, "id"); has && id != "" {
			output.ResponseID = &id
		}
		if usage, ok := JxObjectField(response, "usage"); ok {
			cachedTokens := 0.0
			cacheWriteTokens := 0.0
			if details, has := JxObjectField(usage, "input_tokens_details"); has {
				if value, present := JxFloat(details, "cached_tokens"); present {
					cachedTokens = value
				}
				if value, present := JxFloat(details, "cache_write_tokens"); present {
					cacheWriteTokens = value
				}
			}
			inputTokens := jxf(usage, "input_tokens") - cachedTokens - cacheWriteTokens
			if inputTokens < 0 {
				inputTokens = 0
			}
			reasoning := 0.0
			if details, has := JxObjectField(usage, "output_tokens_details"); has {
				if value, present := JxFloat(details, "reasoning_tokens"); present {
					reasoning = value
				}
			}
			output.Usage = Usage{
				Input:       inputTokens,
				Output:      jxf(usage, "output_tokens"),
				CacheRead:   cachedTokens,
				CacheWrite:  cacheWriteTokens,
				Reasoning:   &reasoning,
				TotalTokens: jxf(usage, "total_tokens"),
				Cost:        UsageCost{},
			}
		}
		CalculateCost(model, &output.Usage)
		if options != nil && options.ApplyServiceTierPricing != nil {
			var serviceTier *string
			if options.ResolveServiceTier != nil {
				responseTier := stringPtrOrNilOf(response, "service_tier")
				serviceTier = options.ResolveServiceTier(responseTier, options.ServiceTier)
			} else {
				serviceTier = stringPtrOrNilOf(response, "service_tier")
				if serviceTier == nil {
					serviceTier = options.ServiceTier
				}
			}
			options.ApplyServiceTierPricing(&output.Usage, serviceTier)
		}
		status, _ := JxString(response, "status")
		incompleteReason := ""
		if details, ok := JxObjectField(response, "incomplete_details"); ok {
			if reason, isString := JxString(details, "reason"); isString {
				incompleteReason = reason
			}
		}
		rawStopReason := status
		if incompleteReason != "" {
			rawStopReason = status + "." + incompleteReason
		}
		output.RawStopReason = &rawStopReason
		stopReason, errorMessage, err := mapResponsesStopReason(status, incompleteReason)
		if err != nil {
			panic(err)
		}
		output.StopReason = stopReason
		output.ErrorMessage = errorMessage
		hasToolCall := false
		for _, block := range output.Content {
			if _, isCall := block.(*ToolCall); isCall {
				hasToolCall = true
				break
			}
		}
		if hasToolCall && output.StopReason == StopStop {
			output.StopReason = StopToolUse
		}
	}

	for {
		eventObj, err := source.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return &csError{msg: err.Error()}
		}
		if eventObj == nil {
			continue
		}
		if options != nil && options.OnProviderStreamEvent != nil {
			options.OnProviderStreamEvent(eventObj, model)
		}
		eventType, _ := JxString(eventObj, "type")

		switch eventType {
		case "response.created":
			if response, ok := JxObjectField(eventObj, "response"); ok {
				if id, has := JxString(response, "id"); has {
					output.ResponseID = &id
				}
			}

		case "response.output_item.added":
			if item, ok := JxObjectField(eventObj, "item"); ok {
				createSlot(int(jxf(eventObj, "output_index")), item)
			}

		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if slot := getSlot(int(jxf(eventObj, "output_index")), "thinking"); slot != nil {
				delta, _ := JxString(eventObj, "delta")
				updateThinkingBlock(output, slot.contentIndex, func(t *ThinkingContent) { t.Thinking += delta })
				stream.Push(&EventThinkingDelta{ContentIndex: slot.contentIndex, Delta: delta, Partial: output})
			}

		case "response.reasoning_summary_part.done":
			if slot := getSlot(int(jxf(eventObj, "output_index")), "thinking"); slot != nil {
				updateThinkingBlock(output, slot.contentIndex, func(t *ThinkingContent) { t.Thinking += "\n\n" })
				stream.Push(&EventThinkingDelta{ContentIndex: slot.contentIndex, Delta: "\n\n", Partial: output})
			}

		case "response.output_text.delta", "response.refusal.delta":
			if slot := getSlot(int(jxf(eventObj, "output_index")), "text"); slot != nil {
				delta, _ := JxString(eventObj, "delta")
				updateTextBlock(output, slot.contentIndex, func(t *TextContent) { t.Text += delta })
				stream.Push(&EventTextDelta{ContentIndex: slot.contentIndex, Delta: delta, Partial: output})
			}

		case "response.function_call_arguments.delta":
			if slot := getSlot(int(jxf(eventObj, "output_index")), "toolCall"); slot != nil && slot.hasPartialJSON {
				delta, _ := JxString(eventObj, "delta")
				slot.partialJSON += delta
				slot.toolCall.Arguments = ParseStreamingJSONObject(slot.partialJSON)
				pushToolCallDelta(slot, delta, true)
			}

		case "response.function_call_arguments.done":
			if slot := getSlot(int(jxf(eventObj, "output_index")), "toolCall"); slot != nil && slot.hasPartialJSON {
				previousPartialJSON := slot.partialJSON
				arguments, _ := JxString(eventObj, "arguments")
				slot.partialJSON = arguments
				slot.toolCall.Arguments = ParseStreamingJSONObject(slot.partialJSON)
				if strings.HasPrefix(arguments, previousPartialJSON) {
					delta := arguments[len(previousPartialJSON):]
					if len(delta) > 0 {
						pushToolCallDelta(slot, delta, true)
					}
				}
			}

		case "response.custom_tool_call_input.delta":
			if slot := getSlot(int(jxf(eventObj, "output_index")), "toolCall"); slot != nil && slot.customProperty != "" {
				delta, _ := JxString(eventObj, "delta")
				emitted, has := responsesAppendCustomToolCallInput(slot, responsesCustomToolCallInput(slot)+delta, false)
				pushToolCallDelta(slot, emitted, has)
			}

		case "response.custom_tool_call_input.done":
			if slot := getSlot(int(jxf(eventObj, "output_index")), "toolCall"); slot != nil && slot.customProperty != "" {
				input, _ := JxString(eventObj, "input")
				emitted, has := responsesAppendCustomToolCallInput(slot, input, true)
				pushToolCallDelta(slot, emitted, has)
			}

		case "response.output_item.done":
			item, _ := JxObjectField(eventObj, "item")
			if item == nil {
				continue
			}
			applyMessagePhaseStopReason(item)
			slot := getOrCreateSlot(int(jxf(eventObj, "output_index")), item)
			if slot == nil {
				continue
			}
			itemType, _ := JxString(item, "type")
			switch {
			case itemType == "reasoning" && slot.kind == "thinking":
				summaryText := joinResponsesTexts(item, "summary", "text")
				contentText := joinResponsesTexts(item, "content", "text")
				updateThinkingBlock(output, slot.contentIndex, func(t *ThinkingContent) {
					if summaryText != "" {
						t.Thinking = summaryText
					} else if contentText != "" {
						t.Thinking = contentText
					}
					t.ThinkingSignature = strPtrOf(jsonx.Stringify(item))
				})
				if id, has := JxString(item, "id"); has {
					reasoningBlocksByID[id] = slot.contentIndex
				}
				var finalContent string
				if summaryText != "" {
					finalContent = summaryText
				} else if contentText != "" {
					finalContent = contentText
				} else if thinking, ok := output.Content[slot.contentIndex].(ThinkingContent); ok {
					finalContent = thinking.Thinking
				}
				stream.Push(&EventThinkingEnd{ContentIndex: slot.contentIndex, Content: finalContent, Partial: output})
				delete(outputSlots, int(jxf(eventObj, "output_index")))

			case itemType == "message" && slot.kind == "text":
				text := ""
				if content, ok := JxList(item, "content"); ok {
					for _, entry := range content {
						if part, isObj := JxObj(entry); isObj {
							if partType, _ := JxString(part, "type"); partType == "output_text" {
								partText, _ := JxString(part, "text")
								text += partText
							} else {
								refusal, _ := JxString(part, "refusal")
								text += refusal
							}
						}
					}
				}
				itemID, _ := JxString(item, "id")
				phase := ""
				if p, has := JxString(item, "phase"); has {
					phase = p
				}
				updateTextBlock(output, slot.contentIndex, func(t *TextContent) {
					t.Text = text
					signature := encodeTextSignatureV1(itemID, phase)
					t.TextSignature = &signature
				})
				stream.Push(&EventTextEnd{ContentIndex: slot.contentIndex, Content: text, Partial: output})
				delete(outputSlots, int(jxf(eventObj, "output_index")))

			case itemType == "function_call" && slot.kind == "toolCall" && slot.hasPartialJSON:
				arguments, hasArguments := JxString(item, "arguments")
				source := "{}"
				if hasArguments && arguments != "" {
					source = arguments
				} else if slot.partialJSON != "" {
					source = slot.partialJSON
				}
				slot.toolCall.Arguments = ParseStreamingJSONObject(source)
				if namespace, has := JxString(item, "namespace"); has {
					slot.toolCall.Namespace = &namespace
				}
				slot.hasPartialJSON = false
				stream.Push(&EventToolCallEnd{ContentIndex: slot.contentIndex, ToolCall: slot.toolCall, Partial: output})
				delete(outputSlots, int(jxf(eventObj, "output_index")))

			case itemType == "custom_tool_call" && slot.kind == "toolCall" && slot.customProperty != "":
				input, hasInput := JxString(item, "input")
				if !hasInput {
					input = responsesCustomToolCallInput(slot)
				}
				emitted, has := responsesAppendCustomToolCallInput(slot, input, true)
				pushToolCallDelta(slot, emitted, has)
				if namespace, has := JxString(item, "namespace"); has {
					slot.toolCall.Namespace = &namespace
				}
				slot.customProperty = ""
				stream.Push(&EventToolCallEnd{ContentIndex: slot.contentIndex, ToolCall: slot.toolCall, Partial: output})
				delete(outputSlots, int(jxf(eventObj, "output_index")))
			}

		case "response.completed", "response.incomplete":
			response, _ := JxObjectField(eventObj, "response")
			if response != nil {
				finalizeResponse(response)
			}

		case "error":
			code := ""
			if value, has := JxString(eventObj, "code"); has {
				code = value
			}
			message := ""
			if value, has := JxString(eventObj, "message"); has {
				message = value
			}
			return &csError{msg: "Error Code " + code + ": " + message}

		case "response.failed":
			sawTerminalResponseEvent = true
			response, _ := JxObjectField(eventObj, "response")
			if response != nil {
				if status, has := JxString(response, "status"); has {
					output.RawStopReason = &status
				}
			}
			msg := "Unknown error (no error details in response)"
			if response != nil {
				if errorObj, has := JxObjectField(response, "error"); has {
					code := "unknown"
					if value, has := JxString(errorObj, "code"); has && value != "" {
						code = value
					}
					message := "no message"
					if value, has := JxString(errorObj, "message"); has && value != "" {
						message = value
					}
					msg = code + ": " + message
				} else if details, has := JxObjectField(response, "incomplete_details"); has {
					if reason, isString := JxString(details, "reason"); isString {
						msg = "incomplete: " + reason
					}
				}
			}
			return &csError{msg: msg}
		}
	}

	if !sawTerminalResponseEvent {
		return &csError{msg: "OpenAI Responses stream ended before a terminal response event"}
	}
	return nil
}

func joinResponsesTexts(item *jsonx.Obj, listKey, textKey string) string {
	list, ok := JxList(item, listKey)
	if !ok {
		return ""
	}
	var parts []string
	for _, entry := range list {
		if obj, isObj := JxObj(entry); isObj {
			if text, has := JxString(obj, textKey); has {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func strPtrOf(value string) *string { return &value }

func stringPtrOrNilOf(obj *jsonx.Obj, key string) *string {
	if value, has := JxString(obj, key); has && value != "" {
		return &value
	}
	return nil
}

// mapResponsesStopReason ports mapStopReason; unknown statuses error.
func mapResponsesStopReason(status string, incompleteReason string) (string, *string, error) {
	if status == "" {
		return StopStop, nil, nil
	}
	switch status {
	case "completed":
		return StopStop, nil, nil
	case "incomplete":
		if incompleteReason == "max_output_tokens" {
			return StopLength, nil, nil
		}
		if incompleteReason != "" {
			message := "Response incomplete: " + incompleteReason
			return StopError, &message, nil
		}
		message := "Response incomplete without a provider reason"
		return StopError, &message, nil
	case "failed", "cancelled":
		return StopError, nil, nil
	case "in_progress", "queued":
		return StopStop, nil, nil
	default:
		return "", nil, &csError{msg: "Unhandled stop reason: " + status}
	}
}
