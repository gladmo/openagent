package codingagent

// Content-block accessors that accept both the value and pointer forms.
// Providers and decoders emit text/thinking blocks as values (see
// ai/api_*_stream.go) while some construction paths use pointers; display
// code must render either form.

import "github.com/gladmo/openagent/ai"

// blockText returns a text block's text.
func blockText(block ai.ContentBlock) (string, bool) {
	switch typed := block.(type) {
	case ai.TextContent:
		return typed.Text, true
	case *ai.TextContent:
		return typed.Text, true
	}
	return "", false
}

// blockThinking returns a thinking block's text.
func blockThinking(block ai.ContentBlock) (string, bool) {
	switch typed := block.(type) {
	case ai.ThinkingContent:
		return typed.Thinking, true
	case *ai.ThinkingContent:
		return typed.Thinking, true
	}
	return "", false
}

// blockImage reports an image block's mime type.
func blockImage(block ai.ContentBlock) (string, bool) {
	switch typed := block.(type) {
	case ai.ImageContent:
		return typed.MimeType, true
	case *ai.ImageContent:
		return typed.MimeType, true
	}
	return "", false
}
