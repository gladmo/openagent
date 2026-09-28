package ai

// faux.go ports the builders from providers/faux.ts (createFauxCore and
// fauxProvider land with the models registry port).

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gladmo/openagent/jsonx"
)

// Faux defaults.
const (
	FauxDefaultAPI           = "faux"
	FauxDefaultProvider      = "faux"
	FauxDefaultModelID       = "faux-1"
	FauxDefaultModelName     = "Faux Model"
	FauxDefaultBaseURL       = "http://localhost:0"
	FauxDefaultContextWindow = 128000
	FauxDefaultMaxTokens     = 16384
)

// FauxContentBlock is text | thinking | toolCall.
type FauxContentBlock = ContentBlock

// FauxText builds a text block.
func FauxText(text string) TextContent { return TextContent{Text: text} }

// FauxThinking builds a thinking block.
func FauxThinking(thinking string) ThinkingContent { return ThinkingContent{Thinking: thinking} }

// FauxToolCall builds a tool call with a random id (or an explicit one).
func FauxToolCall(name string, arguments *jsonx.Obj, id ...string) *ToolCall {
	call := &ToolCall{ID: randomID("tool"), Name: name, Arguments: arguments}
	if len(id) > 0 && id[0] != "" {
		call.ID = id[0]
	}
	return call
}

func randomID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// FauxAssistantMessageOptions mirror the TS options.
type FauxAssistantMessageOptions struct {
	StopReason   *string
	Deferred     *DeferredHandle
	ErrorMessage *string
	ResponseID   *string
	Timestamp    *float64
}

// FauxAssistantMessage builds an AssistantMessage shaped like the faux
// provider's responses. Content is a string (single text block), one block,
// or a block list.
func FauxAssistantMessage(content any, options ...FauxAssistantMessageOptions) *AssistantMessage {
	var blocks []ContentBlock
	switch c := content.(type) {
	case string:
		blocks = []ContentBlock{FauxText(c)}
	case TextContent:
		blocks = []ContentBlock{c}
	case ThinkingContent:
		blocks = []ContentBlock{c}
	case *ToolCall:
		blocks = []ContentBlock{c}
	case []ContentBlock:
		blocks = c
	case nil:
		blocks = []ContentBlock{FauxText("")}
	default:
		blocks = []ContentBlock{FauxText("")}
	}
	opts := FauxAssistantMessageOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	msg := &AssistantMessage{
		Content:     blocks,
		API:         FauxDefaultAPI,
		Provider:    FauxDefaultProvider,
		Model:       FauxDefaultModelID,
		Usage:       Usage{},
		StopReason:  StopStop,
		TimestampMs: nowMs(),
	}
	if opts.StopReason != nil {
		msg.StopReason = *opts.StopReason
	}
	msg.Deferred = opts.Deferred
	msg.ErrorMessage = opts.ErrorMessage
	msg.ResponseID = opts.ResponseID
	if opts.Timestamp != nil {
		msg.TimestampMs = *opts.Timestamp
	}
	return msg
}

// FauxModelDefinition mirrors the TS interface.
type FauxModelDefinition struct {
	ID            string
	Name          string
	Reasoning     bool
	Input         []string
	ContextWindow float64
	MaxTokens     float64
}

// FauxModel builds the chat Model for a definition with default faux
// identity. Use FauxModelWithIdentity when the provider/api are customized.
func FauxModel(def FauxModelDefinition) *Model {
	return FauxModelWithIdentity(def, FauxDefaultAPI, FauxDefaultProvider)
}

// FauxModelWithIdentity builds the model with an explicit api/provider.
func FauxModelWithIdentity(def FauxModelDefinition, api, provider string) *Model {
	name := def.Name
	if name == "" {
		name = FauxDefaultModelName
	}
	input := def.Input
	if input == nil {
		input = []string{"text"}
	}
	contextWindow := def.ContextWindow
	if contextWindow == 0 {
		contextWindow = FauxDefaultContextWindow
	}
	maxTokens := def.MaxTokens
	if maxTokens == 0 {
		maxTokens = FauxDefaultMaxTokens
	}
	return &Model{
		ID:            def.ID,
		Name:          name,
		API:           api,
		Provider:      provider,
		BaseURL:       FauxDefaultBaseURL,
		Input:         input,
		Reasoning:     def.Reasoning,
		ContextWindow: contextWindow,
		MaxTokens:     maxTokens,
	}
}
