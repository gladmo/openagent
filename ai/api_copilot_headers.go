package ai

// api_copilot_headers.go ports api/github-copilot-headers.ts. These headers
// apply only to the github-copilot provider; other providers never build
// them.

// InferCopilotInitiator reports whether the request is user- or
// agent-initiated (follow-up after assistant/tool messages).
func InferCopilotInitiator(messages []Message) string {
	if len(messages) == 0 {
		return "user"
	}
	last := messages[len(messages)-1]
	if last.Role() != "user" {
		return "agent"
	}
	return "user"
}

// HasCopilotVisionInput reports whether any message carries an image.
func HasCopilotVisionInput(messages []Message) bool {
	for _, msg := range messages {
		switch t := msg.(type) {
		case *UserMessage:
			if !t.Content.IsText {
				for _, block := range t.Content.Blocks {
					if block.ContentType() == "image" {
						return true
					}
				}
			}
		case *ToolResultMessage:
			for _, block := range t.Content {
				if block.ContentType() == "image" {
					return true
				}
			}
		}
	}
	return false
}

// BuildCopilotDynamicHeaders builds the copilot-specific dynamic headers.
func BuildCopilotDynamicHeaders(messages []Message, hasImages bool) map[string]string {
	headers := map[string]string{
		"X-Initiator":   InferCopilotInitiator(messages),
		"Openai-Intent": "conversation-edits",
	}
	if hasImages {
		headers["Copilot-Vision-Request"] = "true"
	}
	return headers
}
