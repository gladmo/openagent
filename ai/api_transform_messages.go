package ai

// api_transform_messages.go ports api/transform-messages.ts: cross-provider
// transcript normalization — unsupported-image downgrade, thinking-block
// handling, tool-call id normalization, and synthetic results for orphaned
// tool calls.

const nonVisionUserImagePlaceholder = "(image omitted: model does not support images)"
const nonVisionToolImagePlaceholder = "(tool image omitted: model does not support images)"

func modelSupportsImages(model *Model) bool {
	for _, input := range model.Input {
		if input == "image" {
			return true
		}
	}
	return false
}

func replaceImagesWithPlaceholder(blocks []ContentBlock, placeholder string) []ContentBlock {
	result := []ContentBlock{}
	previousWasPlaceholder := false
	for _, block := range blocks {
		if block.ContentType() == "image" {
			if !previousWasPlaceholder {
				result = append(result, TextContent{Text: placeholder})
			}
			previousWasPlaceholder = true
			continue
		}
		result = append(result, block)
		if text, ok := block.(TextContent); ok {
			previousWasPlaceholder = text.Text == placeholder
		} else {
			previousWasPlaceholder = false
		}
	}
	return result
}

func downgradeUnsupportedImages(messages []Message, model *Model) []Message {
	if modelSupportsImages(model) {
		return messages
	}
	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		switch t := msg.(type) {
		case *UserMessage:
			if !t.Content.IsText && t.Content.Blocks != nil {
				copyMsg := *t
				copyMsg.Content = BlocksContent(replaceImagesWithPlaceholder(t.Content.Blocks, nonVisionUserImagePlaceholder)...)
				out = append(out, &copyMsg)
				continue
			}
			out = append(out, msg)
		case *ToolResultMessage:
			if t.Content != nil {
				copyMsg := *t
				copyMsg.Content = replaceImagesWithPlaceholder(t.Content, nonVisionToolImagePlaceholder)
				out = append(out, &copyMsg)
				continue
			}
			out = append(out, msg)
		default:
			out = append(out, msg)
		}
	}
	return out
}

// TransformMessages normalizes a transcript for a target model.
// NormalizeToolCallID may be nil.
func TransformMessages(messages []Message, model *Model, normalizeToolCallID func(id string, model *Model, source *AssistantMessage) string) []Message {
	toolCallIDMap := map[string]string{}
	imageAware := downgradeUnsupportedImages(messages, model)

	// First pass: transform assistant/toolResult messages.
	transformed := make([]Message, 0, len(imageAware))
	for _, msg := range imageAware {
		switch t := msg.(type) {
		case *ToolResultMessage:
			if normalizedID, ok := toolCallIDMap[t.ToolCallID]; ok && normalizedID != t.ToolCallID {
				copyMsg := *t
				copyMsg.ToolCallID = normalizedID
				transformed = append(transformed, &copyMsg)
				continue
			}
			transformed = append(transformed, msg)

		case *AssistantMessage:
			isSameModel := t.Provider == model.Provider && t.API == model.API && t.Model == model.ID
			var content []ContentBlock
			for _, block := range t.Content {
				switch b := block.(type) {
				case ThinkingContent:
					// Thinking from another model is dropped. Signatures and
					// encrypted reasoning are only valid for the model that
					// produced them, and sending readable thinking as plain
					// text makes the new model treat it as part of the
					// visible reply.
					if !isSameModel {
						continue
					}
					// Keep redacted and signed blocks even if the thinking
					// text is empty (OpenAI encrypted reasoning); they are
					// needed for replay.
					if (b.Redacted != nil && *b.Redacted) || b.ThinkingSignature != nil {
						content = append(content, b)
						continue
					}
					if trimWhitespace(b.Thinking) == "" {
						continue
					}
					content = append(content, b)

				case TextContent:
					if isSameModel {
						content = append(content, b)
						continue
					}
					content = append(content, TextContent{Text: b.Text})

				case *ToolCall:
					normalized := b
					if !isSameModel && b.ThoughtSignature != nil {
						copyCall := *b
						copyCall.ThoughtSignature = nil
						normalized = &copyCall
					}
					if !isSameModel && normalizeToolCallID != nil {
						normalizedID := normalizeToolCallID(b.ID, model, t)
						if normalizedID != b.ID {
							toolCallIDMap[b.ID] = normalizedID
							if normalized == b {
								copyCall := *b
								normalized = &copyCall
							}
							normalized.ID = normalizedID
						}
					}
					content = append(content, normalized)

				default:
					content = append(content, block)
				}
			}
			copyMsg := *t
			copyMsg.Content = content
			transformed = append(transformed, &copyMsg)

		default:
			transformed = append(transformed, msg)
		}
	}

	// Second pass: insert synthetic tool results for orphaned tool calls.
	result := []Message{}
	var pendingToolCalls []*ToolCall
	existingToolResultIDs := map[string]bool{}
	var heldSystemMessages []Message

	closePendingToolCalls := func() {
		if len(pendingToolCalls) > 0 {
			for _, call := range pendingToolCalls {
				if !existingToolResultIDs[call.ID] {
					result = append(result, &ToolResultMessage{
						ToolCallID:  call.ID,
						ToolName:    call.Name,
						Content:     []ContentBlock{TextContent{Text: "No result provided"}},
						IsError:     true,
						TimestampMs: nowMs(),
					})
				}
			}
			pendingToolCalls = nil
			existingToolResultIDs = map[string]bool{}
		}
		result = append(result, heldSystemMessages...)
		heldSystemMessages = nil
	}

	for _, msg := range transformed {
		switch t := msg.(type) {
		case *AssistantMessage:
			// Insert synthetic results for the previous assistant's orphaned
			// calls, then skip errored/aborted turns entirely.
			closePendingToolCalls()
			if t.StopReason == StopError || t.StopReason == StopAborted {
				continue
			}
			pendingToolCalls = nil
			for _, block := range t.Content {
				if call, ok := block.(*ToolCall); ok {
					pendingToolCalls = append(pendingToolCalls, call)
				}
			}
			if len(pendingToolCalls) > 0 {
				existingToolResultIDs = map[string]bool{}
			}
			result = append(result, msg)

		case *ToolResultMessage:
			existingToolResultIDs[t.ToolCallID] = true
			result = append(result, msg)

		case *SystemMessage:
			if len(pendingToolCalls) > 0 {
				heldSystemMessages = append(heldSystemMessages, msg)
			} else {
				result = append(result, msg)
			}

		case *UserMessage:
			// A new user turn interrupts the tool flow.
			closePendingToolCalls()
			result = append(result, msg)

		default:
			result = append(result, msg)
		}
	}

	closePendingToolCalls()
	return result
}

func trimWhitespace(s string) string {
	return trimSpace(s)
}

// SplitErrorToolResult is an error tool result split into a text-only
// result and the images to send after the tool results.
type SplitErrorToolResult struct {
	// Content is the result content with each run of images replaced by a
	// note.
	Content []TextContent
	// Caption names the tool call, sent before Images.
	Caption string
	Images  []ImageContent
}

const errorToolImagePlaceholder = "(image attached after the tool results)"

// SplitErrorToolResultImages splits the images out of an error tool result,
// for APIs that reject images in error results (Anthropic, and Claude on
// Bedrock). Send the returned content as the tool result and the caption and
// images after all tool results of the turn, in the same user message.
// Returns false for results that need no split (#10794).
func SplitErrorToolResultImages(msg *ToolResultMessage) (*SplitErrorToolResult, bool) {
	if !msg.IsError {
		return nil, false
	}
	images := []ImageContent{}
	for _, block := range msg.Content {
		if image, ok := block.(ImageContent); ok {
			images = append(images, image)
		}
	}
	if len(images) == 0 {
		return nil, false
	}
	return &SplitErrorToolResult{
		Content: replaceErrorImagesWithPlaceholder(msg.Content, errorToolImagePlaceholder),
		Caption: "Images from the error result of tool call " + msg.ToolCallID + " (" + msg.ToolName + "):",
		Images:  images,
	}, true
}

// replaceErrorImagesWithPlaceholder narrows the placeholder walk to text
// blocks: error tool results may only carry text after the split.
func replaceErrorImagesWithPlaceholder(content []ContentBlock, placeholder string) []TextContent {
	result := []TextContent{}
	previousWasPlaceholder := false
	for _, block := range content {
		if _, isImage := block.(ImageContent); isImage {
			if !previousWasPlaceholder {
				result = append(result, TextContent{Text: placeholder})
			}
			previousWasPlaceholder = true
			continue
		}
		if text, ok := block.(TextContent); ok {
			result = append(result, text)
			previousWasPlaceholder = text.Text == placeholder
		}
	}
	return result
}
