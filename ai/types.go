// Package ai ports the @gladmo/pi-ai closure that pi/packages/agent
// depends on: the message model, streaming, models registry, faux provider,
// and the utility subset.
//
// Numbers are float64 (JS doubles) throughout, including timestamps (Unix
// milliseconds). Optional TS fields are pointers; JSON conversion functions
// (MessageToJSON/MessageFromJSON, content/event codecs) reproduce the TS
// field order and omit undefined fields.
package ai

import (
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

// ---------------------------------------------------------------------------
// Content blocks
// ---------------------------------------------------------------------------

// ContentBlock is a typed message content block (text, thinking, image,
// toolCall).
type ContentBlock interface{ ContentType() string }

// TextContent is a "text" block.
type TextContent struct {
	Text          string
	TextSignature *string
}

// ContentType implements ContentBlock.
func (TextContent) ContentType() string { return "text" }

// ThinkingContent is a "thinking" block.
type ThinkingContent struct {
	Thinking          string
	ThinkingSignature *string
	Redacted          *bool
}

// ContentType implements ContentBlock.
func (ThinkingContent) ContentType() string { return "thinking" }

// ImageContent is an "image" block.
type ImageContent struct {
	Data     string // base64
	MimeType string
}

// ContentType implements ContentBlock.
func (ImageContent) ContentType() string { return "image" }

// ToolCall is a "toolCall" block.
type ToolCall struct {
	ID               string
	Name             string
	Arguments        *jsonx.Obj // JsonObject
	ThoughtSignature *string
	Namespace        *string
}

// ContentType implements ContentBlock.
func (*ToolCall) ContentType() string { return "toolCall" }

// ---------------------------------------------------------------------------
// Usage / stop reasons / deferred
// ---------------------------------------------------------------------------

// Usage mirrors the TS interface. CacheWrite1h/Reasoning are pointers so
// absent stays distinct from 0.
type Usage struct {
	Input        float64
	Output       float64
	CacheRead    float64
	CacheWrite   float64
	CacheWrite1h *float64
	Reasoning    *float64
	TotalTokens  float64
	Cost         UsageCost
}

// UsageCost mirrors the nested cost object.
type UsageCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Total      float64
}

// StopReason values.
const (
	StopPending  = "pending"
	StopStop     = "stop"
	StopLength   = "length"
	StopToolUse  = "toolUse"
	StopError    = "error"
	StopAborted  = "aborted"
	StopDeferred = "deferred"
)

// DeferredHandle mirrors the TS interface.
type DeferredHandle struct {
	Provider    string
	ModelID     string
	API         string
	ID          string
	ExpiresAt   *float64
	PollAfterMs *float64
	Data        any // JsonValue
}

// DiagnosticErrorInfo mirrors utils/diagnostics.ts.
type DiagnosticErrorInfo struct {
	Name    *string
	Message string
	Stack   *string
	Code    any // string | number
}

// AssistantMessageDiagnostic mirrors utils/diagnostics.ts.
type AssistantMessageDiagnostic struct {
	Type      string
	Timestamp float64
	Error     *DiagnosticErrorInfo
	Details   *jsonx.Obj
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// Message is the union of system/user/assistant/toolResult messages.
type Message interface{ Role() string }

// Content is `string | blocks`: string passes through; block slices marshal
// as arrays.
type Content struct {
	Text   string
	Blocks []ContentBlock
	IsText bool
}

// StringContent wraps a plain string.
func StringContent(s string) Content { return Content{Text: s, IsText: true} }

// BlocksContent wraps a block slice.
func BlocksContent(blocks ...ContentBlock) Content {
	return Content{Blocks: blocks}
}

// Tool mirrors the TS Tool interface.
type Tool struct {
	Name                string
	Description         string
	Parameters          *typebox.Schema
	ConstrainedSampling any // false | ConstrainedSamplingConfig
}

// ToolReference mirrors the TS interface.
type ToolReference struct{ Name string }

// SystemMessage mirrors the TS interface.
type SystemMessage struct {
	Content      Content            // string | TextContent[]
	Sections     map[string]*string // name -> text or null (removal)
	SectionOrder []string           // insertion order of section names
	HasSections  bool
	ToolsAdded   []Tool
	ToolsRemoved []ToolReference
	TimestampMs  float64
}

// Role implements Message.
func (*SystemMessage) Role() string { return "system" }

// UserMessage mirrors the TS interface.
type UserMessage struct {
	Content     Content // string | (TextContent|ImageContent)[]
	TimestampMs float64
}

// Role implements Message.
func (*UserMessage) Role() string { return "user" }

// AssistantMessage mirrors the TS interface.
type AssistantMessage struct {
	Content               []ContentBlock // text | thinking | toolCall
	API                   string
	Provider              string
	Model                 string
	ResponseModel         *string
	ResponseID            *string
	ProviderThinkingLevel *string
	Diagnostics           []AssistantMessageDiagnostic
	Usage                 Usage
	StopReason            string
	Deferred              *DeferredHandle
	ErrorMessage          *string
	RawStopReason         *string
	EndTurn               *bool
	TimestampMs           float64
}

// Role implements Message.
func (*AssistantMessage) Role() string { return "assistant" }

// ToolResultMessage mirrors the TS interface.
type ToolResultMessage struct {
	ToolCallID  string
	ToolName    string
	Content     []ContentBlock // text | image
	Details     any            // JsonValue; nil = absent
	Usage       *Usage
	IsError     bool
	TimestampMs float64
}

// Role implements Message.
func (*ToolResultMessage) Role() string { return "toolResult" }

// ---------------------------------------------------------------------------
// Request contexts
// ---------------------------------------------------------------------------

// Context is the un-normalized request context.
type Context struct {
	SystemPrompt *string
	Messages     []Message
	Tools        []Tool
}

// TranscriptContext is the normalized request context (only
// NormalizeContext produces it).
type TranscriptContext struct{ Messages []Message }

// NewTranscriptContext constructs a TranscriptContext (standing in for the TS
// brand).
func NewTranscriptContext(messages []Message) *TranscriptContext {
	return &TranscriptContext{Messages: messages}
}

// ---------------------------------------------------------------------------
// Assistant message events
// ---------------------------------------------------------------------------

// AssistantMessageEvent is the stream protocol event union. Partial is the
// shared live response-so-far accumulator, not a snapshot.
type AssistantMessageEvent interface{ EventType() string }

// EventStart is {type:"start"}.
type EventStart struct{ Partial *AssistantMessage }

// EventType implements AssistantMessageEvent.
func (*EventStart) EventType() string { return "start" }

// EventTextStart is {type:"text_start", contentIndex}.
type EventTextStart struct {
	ContentIndex int
	Partial      *AssistantMessage
}

// EventType implements AssistantMessageEvent.
func (*EventTextStart) EventType() string { return "text_start" }

// EventTextDelta is {type:"text_delta", contentIndex, delta}.
type EventTextDelta struct {
	ContentIndex int
	Delta        string
	Partial      *AssistantMessage
}

// EventType implements AssistantMessageEvent.
func (*EventTextDelta) EventType() string { return "text_delta" }

// EventTextEnd is {type:"text_end", contentIndex, content}.
type EventTextEnd struct {
	ContentIndex int
	Content      string
	Partial      *AssistantMessage
}

// EventType implements AssistantMessageEvent.
func (*EventTextEnd) EventType() string { return "text_end" }

// EventThinkingStart is {type:"thinking_start", contentIndex}.
type EventThinkingStart struct {
	ContentIndex int
	Partial      *AssistantMessage
}

// EventType implements AssistantMessageEvent.
func (*EventThinkingStart) EventType() string { return "thinking_start" }

// EventThinkingDelta is {type:"thinking_delta", contentIndex, delta}.
type EventThinkingDelta struct {
	ContentIndex int
	Delta        string
	Partial      *AssistantMessage
}

// EventType implements AssistantMessageEvent.
func (*EventThinkingDelta) EventType() string { return "thinking_delta" }

// EventThinkingEnd is {type:"thinking_end", contentIndex, content}.
type EventThinkingEnd struct {
	ContentIndex int
	Content      string
	Partial      *AssistantMessage
}

// EventType implements AssistantMessageEvent.
func (*EventThinkingEnd) EventType() string { return "thinking_end" }

// EventToolCallStart is {type:"toolcall_start", contentIndex}.
type EventToolCallStart struct {
	ContentIndex int
	Partial      *AssistantMessage
}

// EventType implements AssistantMessageEvent.
func (*EventToolCallStart) EventType() string { return "toolcall_start" }

// EventToolCallDelta is {type:"toolcall_delta", contentIndex, delta}.
type EventToolCallDelta struct {
	ContentIndex int
	Delta        string
	Partial      *AssistantMessage
}

// EventType implements AssistantMessageEvent.
func (*EventToolCallDelta) EventType() string { return "toolcall_delta" }

// EventToolCallEnd is {type:"toolcall_end", contentIndex, toolCall}.
type EventToolCallEnd struct {
	ContentIndex int
	ToolCall     *ToolCall
	Partial      *AssistantMessage
}

// EventType implements AssistantMessageEvent.
func (*EventToolCallEnd) EventType() string { return "toolcall_end" }

// EventDone is {type:"done", reason, message}.
type EventDone struct {
	Reason  string // stop | length | toolUse | deferred
	Message *AssistantMessage
}

// EventType implements AssistantMessageEvent.
func (*EventDone) EventType() string { return "done" }

// EventError is {type:"error", reason, error}.
type EventError struct {
	Reason string // aborted | error
	Error  *AssistantMessage
}

// EventType implements AssistantMessageEvent.
func (*EventError) EventType() string { return "error" }

// ---------------------------------------------------------------------------
// Model catalog types (subset used by agent + faux)
// ---------------------------------------------------------------------------

// ThinkingLevel values (pi-neutral).
const (
	ThinkingMinimal = "minimal"
	ThinkingLow     = "low"
	ThinkingMedium  = "medium"
	ThinkingHigh    = "high"
	ThinkingXHigh   = "xhigh"
	ThinkingMax     = "max"
	ThinkingOff     = "off"
)

// ThinkingLevelMap maps pi thinking levels to provider values (nil marks
// unsupported).
type ThinkingLevelMap map[string]*string

// ModelCostRates is $/million tokens.
type ModelCostRates struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// ModelCostTier is a request-wide pricing tier.
type ModelCostTier struct {
	ModelCostRates
	InputTokensAbove float64
}

// ModelCost adds tiered pricing.
type ModelCost struct {
	ModelCostRates
	Tiers []ModelCostTier
}

// HasTiers reports whether tiered pricing is present.
func (c ModelCost) HasTiers() bool { return len(c.Tiers) > 0 }

// ModelPromptCache mirrors the TS prompt-cache lifetimes (seconds).
type ModelPromptCache struct {
	Short float64
	Long  float64
}

// Model mirrors the TS chat Model interface (type field omitted in TS chat
// entries; Compat/PromptCache/InputLimits are jsonx values from the catalog).
type Model struct {
	ID               string
	Name             string
	API              string
	Provider         string
	BaseURL          string
	Input            []string // "text" | "image"
	Cost             ModelCost
	Reasoning        bool
	ThinkingLevelMap ThinkingLevelMap
	ContextWindow    float64
	MaxTokens        float64
	Headers          map[string]string

	// Type is "chat" (zero value), "image", or "classifier".
	Type string
	// SamplingParams are default sampling parameters merged under request
	// options (openai-completions buildParams).
	SamplingParams map[string]any
	// Compat is the provider-specific compat metadata (a decoded jsonx
	// object; read through the per-API accessor helpers).
	Compat any
	// PromptCache carries cache lifetimes when annotated.
	PromptCache *ModelPromptCache
	// InputLimits is the decoded inputLimits jsonx object.
	InputLimits any
}

// ToolChoice values.
const (
	ToolChoiceAuto = "auto"
	ToolChoiceNone = "none"
)
