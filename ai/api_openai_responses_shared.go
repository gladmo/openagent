package ai

// api_openai_responses_shared.go ports api/openai-responses-shared.ts:
// Responses-API message/tool conversion plus the shared stream-event pump
// used by the openai-responses, azure-openai-responses, and codex clients.

import (
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

func encodeTextSignatureV1(id string, phase string) string {
	payload := jsonx.ObjFrom("v", 1, "id", id)
	if phase != "" {
		payload.Set("phase", phase)
	}
	return jsonx.Stringify(payload)
}

type textSignature struct {
	ID       string
	Phase    string
	hasPhase bool
}

func parseTextSignature(signature string) *textSignature {
	if signature == "" {
		return nil
	}
	if strings.HasPrefix(signature, "{") {
		if parsed, err := ParseJSONWithRepair(signature); err == nil {
			if obj, ok := JxObj(parsed); ok {
				version, hasV := JxFloat(obj, "v")
				id, hasID := JxString(obj, "id")
				if hasV && version == 1 && hasID {
					result := &textSignature{ID: id}
					if phase, has := JxString(obj, "phase"); has && (phase == "commentary" || phase == "final_answer") {
						result.Phase = phase
						result.hasPhase = true
					}
					return result
				}
			}
		}
	}
	return &textSignature{ID: signature}
}

// convertResponsesToolResultOutput ports convertToolResultOutput: text or a
// text+image content array for vision models.
func convertResponsesToolResultOutput(model *Model, content []ContentBlock) any {
	var textParts []string
	var images []ImageContent
	for _, block := range content {
		if text, ok := block.(TextContent); ok {
			textParts = append(textParts, text.Text)
		} else if image, ok := block.(ImageContent); ok {
			images = append(images, image)
		}
	}
	textResult := strings.Join(textParts, "\n")
	hasText := len(textResult) > 0

	if len(images) == 0 || !modelSupportsImages(model) {
		if hasText {
			return SanitizeSurrogates(textResult)
		}
		if len(images) > 0 {
			return "(see attached image)"
		}
		return "(no tool output)"
	}

	var output []any
	if hasText {
		output = append(output, jsonx.ObjFrom("type", "input_text", "text", SanitizeSurrogates(textResult)))
	}
	for _, image := range images {
		output = append(output, jsonx.ObjFrom(
			"type", "input_image",
			"detail", "auto",
			"image_url", "data:"+image.MimeType+";base64,"+image.Data,
		))
	}
	return output
}

// ConvertResponsesMessagesOptions mirrors the TS interface.
type ConvertResponsesMessagesOptions struct {
	IncludeSystemPrompt            bool
	HasIncludeSystemPrompt         bool
	GrammarToolInputProperties     map[string]string
	SupportsMidConvoSystemMessages bool
	SupportsAdditionalTools        bool
	SupportsToolSearch             bool
	ToolOptions                    *ConvertResponsesToolsOptions
}

// ConvertResponsesToolsOptions mirrors the TS interface.
type ConvertResponsesToolsOptions struct {
	Strict                     *bool
	SupportsStrictMode         bool
	SupportsOpenAIGrammarTools bool
	ToolSearchResult           bool
}

func normalizeResponsesIDPart(part string) string {
	var sanitized strings.Builder
	for _, r := range part {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sanitized.WriteRune(r)
		} else {
			sanitized.WriteByte('_')
		}
	}
	normalized := sanitized.String()
	if len(normalized) > 64 {
		normalized = normalized[:64]
	}
	return strings.TrimRight(normalized, "_")
}

func buildForeignResponsesItemID(itemID string) string {
	normalized := "fc_" + ShortHash(itemID)
	if len(normalized) > 64 {
		return normalized[:64]
	}
	return normalized
}

// convertResponsesMessages ports convertResponsesMessages. Returns the
// Response input items; an error surfaces JSON.parse failures on thinking
// signatures (TS throws out of the stream pump).
func convertResponsesMessages(
	model *Model,
	context *TranscriptContext,
	allowedToolCallProviders map[string]bool,
	options *ConvertResponsesMessagesOptions,
) ([]any, error) {
	opts := &ConvertResponsesMessagesOptions{}
	if options != nil {
		opts = options
	}
	normalizedContext := ResolveTranscript(context, opts.SupportsMidConvoSystemMessages)
	messages := []any{}

	normalizeToolCallID := func(id string, source *AssistantMessage) string {
		if !allowedToolCallProviders[model.Provider] {
			return normalizeResponsesIDPart(id)
		}
		if !strings.Contains(id, "|") {
			return normalizeResponsesIDPart(id)
		}
		parts := strings.SplitN(id, "|", 2)
		callID, itemID := parts[0], parts[1]
		normalizedCallID := normalizeResponsesIDPart(callID)
		isForeignToolCall := source.Provider != model.Provider || source.API != model.API
		normalizedItemID := normalizeResponsesIDPart(itemID)
		if isForeignToolCall {
			normalizedItemID = buildForeignResponsesItemID(itemID)
		}
		if !strings.HasPrefix(normalizedItemID, "fc_") {
			normalizedItemID = normalizeResponsesIDPart("fc_" + normalizedItemID)
		}
		return normalizedCallID + "|" + normalizedItemID
	}

	transformedMessages := TransformMessages(normalizedContext.Messages, model, func(id string, _ *Model, source *AssistantMessage) string {
		return normalizeToolCallID(id, source)
	})
	transcriptTools := ResolveTranscriptTools(
		normalizedContext.Messages,
		opts.SupportsAdditionalTools || opts.SupportsToolSearch,
	)
	appendSystemToolAdditions := func(message *SystemMessage, seed string) {
		tools := []Tool{}
		if transcriptTools.AnchorsAdditions {
			tools = message.ToolsAdded
		}
		if len(tools) == 0 {
			return
		}
		if opts.SupportsAdditionalTools {
			messages = append(messages, jsonx.ObjFrom(
				"type", "additional_tools",
				"role", "developer",
				"tools", convertResponsesTools(tools, opts.ToolOptions),
			))
			return
		}
		if !opts.SupportsToolSearch {
			return
		}
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, tool.Name)
		}
		callID := "pi_tool_load_" + ShortHash(seed+":"+strings.Join(names, ","))
		messages = append(messages, jsonx.ObjFrom(
			"type", "tool_search_call",
			"call_id", callID,
			"execution", "client",
			"status", "completed",
			"arguments", jsonx.ObjFrom("query", strings.Join(names, " "), "limit", float64(len(names))),
		))
		messages = append(messages, jsonx.ObjFrom(
			"type", "tool_search_output",
			"call_id", callID,
			"execution", "client",
			"status", "completed",
			"tools", convertResponsesTools(tools, withToolSearchResult(opts.ToolOptions)),
		))
	}

	includeInitialSystemMessage := true
	if opts.HasIncludeSystemPrompt {
		includeInitialSystemMessage = opts.IncludeSystemPrompt
	}
	supportsDeveloperRole := true
	if compat, ok := JxCompatObj(model); ok {
		if value, present := JxBool(compat, "supportsDeveloperRole"); present {
			supportsDeveloperRole = value
		}
	}
	instructionRole := "system"
	if model.Reasoning && supportsDeveloperRole {
		instructionRole = "developer"
	}

	msgIndex := 0
	sourceIndex := 0
	for _, msg := range transformedMessages {
		isLeadingSystemMessage := sourceIndex == 0 && msg.Role() == "system"
		sourceIndex++
		switch t := msg.(type) {
		case *SystemMessage:
			if !isLeadingSystemMessage {
				appendSystemToolAdditions(t, "system:"+itoaStatus(msgIndex))
			}
			if !isLeadingSystemMessage || includeInitialSystemMessage {
				text := RenderSystemMessageUpdate(t)
				if isLeadingSystemMessage {
					text = GetSystemMessageText(t)
				}
				if len(text) > 0 {
					messages = append(messages, jsonx.ObjFrom("role", instructionRole, "content", SanitizeSurrogates(text)))
				}
			}

		case *UserMessage:
			if t.Content.IsText {
				messages = append(messages, jsonx.ObjFrom(
					"role", "user",
					"content", []any{jsonx.ObjFrom("type", "input_text", "text", SanitizeSurrogates(t.Content.Text))},
				))
			} else {
				var content []any
				for _, item := range t.Content.Blocks {
					if text, ok := item.(TextContent); ok {
						content = append(content, jsonx.ObjFrom("type", "input_text", "text", SanitizeSurrogates(text.Text)))
					} else if image, ok := item.(ImageContent); ok {
						content = append(content, jsonx.ObjFrom(
							"type", "input_image",
							"detail", "auto",
							"image_url", "data:"+image.MimeType+";base64,"+image.Data,
						))
					}
				}
				if len(content) == 0 {
					if !isLeadingSystemMessage {
						msgIndex++
					}
					continue
				}
				messages = append(messages, jsonx.ObjFrom("role", "user", "content", content))
			}

		case *AssistantMessage:
			var output []any
			isSameProviderAndAPI := t.Provider == model.Provider && t.API == model.API
			isSameModel := isSameProviderAndAPI && t.Model == model.ID
			isDifferentModel := isSameProviderAndAPI && t.Model != model.ID
			textBlockIndex := 0

			for _, block := range t.Content {
				switch b := block.(type) {
				case ThinkingContent:
					if b.ThinkingSignature != nil {
						reasoningItem, err := ParseJSONWithRepair(*b.ThinkingSignature)
						if err != nil {
							return nil, err
						}
						output = append(output, reasoningItem)
					}
				case TextContent:
					parsedSignature := (*textSignature)(nil)
					if b.TextSignature != nil {
						parsedSignature = parseTextSignature(*b.TextSignature)
					}
					fallbackMessageID := "msg_pi_" + itoaStatus(msgIndex)
					if textBlockIndex > 0 {
						fallbackMessageID = "msg_pi_" + itoaStatus(msgIndex) + "_" + itoaStatus(textBlockIndex)
					}
					textBlockIndex++
					msgID := fallbackMessageID
					if parsedSignature != nil && parsedSignature.ID != "" {
						msgID = parsedSignature.ID
						if len(msgID) > 64 {
							msgID = "msg_" + ShortHash(msgID)
						}
					}
					message := jsonx.ObjFrom(
						"type", "message",
						"role", "assistant",
						"content", []any{jsonx.ObjFrom("type", "output_text", "text", SanitizeSurrogates(b.Text), "annotations", []any{})},
						"status", "completed",
						"id", msgID,
					)
					if parsedSignature != nil && parsedSignature.hasPhase {
						message.Set("phase", parsedSignature.Phase)
					}
					output = append(output, message)
				case *ToolCall:
					callID := b.ID
					itemID := ""
					if idx := strings.Index(b.ID, "|"); idx >= 0 {
						callID = b.ID[:idx]
						itemID = b.ID[idx+1:]
					}
					customInputProperty, hasCustom := "", false
					if opts.GrammarToolInputProperties != nil {
						property, inMap := opts.GrammarToolInputProperties[b.Name]
						customInputProperty, hasCustom = property, inMap
					}
					omitItemID := false
					if (isDifferentModel && strings.HasPrefix(itemID, "fc_")) ||
						(!hasCustom && !strings.HasPrefix(itemID, "fc_")) {
						omitItemID = true
					}

					if hasCustom {
						input := ""
						if b.Arguments != nil {
							if value, present := b.Arguments.Get(customInputProperty); present {
								if s, isString := value.(string); isString {
									input = s
								}
							}
						}
						item := jsonx.ObjFrom(
							"type", "custom_tool_call",
							"call_id", callID,
							"name", b.Name,
							"input", SanitizeSurrogates(input),
						)
						if !omitItemID {
							item.Set("id", itemID)
						}
						if isSameModel && b.Namespace != nil {
							item.Set("namespace", *b.Namespace)
						}
						output = append(output, item)
					} else {
						arguments := "{}"
						if b.Arguments != nil {
							arguments = jsonx.Stringify(b.Arguments)
						}
						item := jsonx.ObjFrom(
							"type", "function_call",
							"call_id", callID,
							"name", b.Name,
							"arguments", arguments,
						)
						if !omitItemID {
							item.Set("id", itemID)
						}
						if isSameModel && b.Namespace != nil {
							item.Set("namespace", *b.Namespace)
						}
						output = append(output, item)
					}
				}
			}
			if len(output) == 0 {
				if !isLeadingSystemMessage {
					msgIndex++
				}
				continue
			}
			messages = append(messages, output...)

		case *ToolResultMessage:
			callID := t.ToolCallID
			if idx := strings.Index(t.ToolCallID, "|"); idx >= 0 {
				callID = t.ToolCallID[:idx]
			}
			outputValue := convertResponsesToolResultOutput(model, t.Content)
			itemType := "function_call_output"
			if opts.GrammarToolInputProperties != nil {
				if _, inMap := opts.GrammarToolInputProperties[t.ToolName]; inMap {
					itemType = "custom_tool_call_output"
				}
			}
			messages = append(messages, jsonx.ObjFrom(
				"type", itemType,
				"call_id", callID,
				"output", outputValue,
			))
		}
		if !isLeadingSystemMessage {
			msgIndex++
		}
	}

	return messages, nil
}

func withToolSearchResult(options *ConvertResponsesToolsOptions) *ConvertResponsesToolsOptions {
	resolved := &ConvertResponsesToolsOptions{}
	if options != nil {
		*resolved = *options
	}
	resolved.ToolSearchResult = true
	return resolved
}

// convertResponsesTools ports convertResponsesTools.
func convertResponsesTools(tools []Tool, options *ConvertResponsesToolsOptions) []any {
	defaultStrict := false
	supportsStrictMode := true
	supportsOpenAIGrammarTools := false
	if options != nil {
		if options.Strict != nil {
			defaultStrict = *options.Strict
		}
		supportsStrictMode = options.SupportsStrictMode
		supportsOpenAIGrammarTools = options.SupportsOpenAIGrammarTools
	}

	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		grammar, err := ResolveGrammarConstrainedSampling(&tool, supportsOpenAIGrammarTools)
		if err != nil {
			panic(err)
		}
		if grammar != nil {
			custom := jsonx.ObjFrom(
				"type", "custom",
				"name", tool.Name,
				"description", tool.Description,
				"format", jsonx.ObjFrom(
					"type", "grammar",
					"syntax", grammar.Format,
					"definition", grammar.Definition,
				),
			)
			if options != nil && options.ToolSearchResult {
				custom.Set("defer_loading", true)
			}
			out = append(out, custom)
			continue
		}

		constrainedStrict, err := ResolveJSONSchemaStrictSampling(&tool, supportsStrictMode)
		if err != nil {
			panic(err)
		}
		strict := constrainedStrict || defaultStrict
		parameters, err := GetJSONSchemaToolParameters(&tool, strict)
		if err != nil {
			panic(err)
		}
		var parametersValue any = jsonx.NewObj()
		if obj, ok := JxObj(parameters); ok {
			parametersValue = obj
		}
		functionTool := jsonx.NewObj()
		functionTool.Set("type", "function")
		functionTool.Set("name", tool.Name)
		functionTool.Set("description", tool.Description)
		functionTool.Set("parameters", parametersValue)
		if options != nil && options.ToolSearchResult {
			functionTool.Set("defer_loading", true)
		}
		if supportsStrictMode {
			functionTool.Set("strict", strict)
		}
		out = append(out, functionTool)
	}
	return out
}
