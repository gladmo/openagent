// Package agent ports the pi-agent-core root package: the low-level agent
// loop, the stateful Agent wrapper, the SSE proxy stream function, and the
// search service contracts.
package agent

import (
	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

// StreamFn mirrors the TS type: streams a normalized transcript. The loop
// passes a normalized transcript; the system prompt and tool declarations
// live in the transcript's system messages. Must not throw: failures are
// encoded in the returned stream.
type StreamFn func(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream

// ToolExecutionMode values.
const (
	ToolExecutionSequential = "sequential"
	ToolExecutionParallel   = "parallel"
)

// QueueMode values.
const (
	QueueAll        = "all"
	QueueOneAtATime = "one-at-a-time"
)

// AgentToolCall is the tool-call content block of an assistant message.
type AgentToolCall = ai.ToolCall

// BeforeToolCallResult mirrors the TS interface.
type BeforeToolCallResult struct {
	Block     *bool
	Reason    string
	Terminate *bool
}

// AfterToolCallResult mirrors the TS interface. Nil pointer fields keep the
// original executed values (field-by-field overlay, no deep merge).
type AfterToolCallResult struct {
	Content    []ai.ContentBlock
	HasContent bool
	Details    any
	HasDetails bool
	IsError    *bool
	Usage      *ai.Usage
	Terminate  *bool
}

// BeforeToolCallContext mirrors the TS interface.
type BeforeToolCallContext struct {
	AssistantMessage *ai.AssistantMessage
	ToolCall         *AgentToolCall
	Args             any
	Context          *AgentContext
}

// AfterToolCallContext mirrors the TS interface.
type AfterToolCallContext struct {
	AssistantMessage *ai.AssistantMessage
	ToolCall         *AgentToolCall
	Args             any
	Result           *AgentToolResult
	IsError          bool
	Context          *AgentContext
}

// AgentTurnContext mirrors the TS interface.
type AgentTurnContext struct {
	Message     *ai.AssistantMessage
	ToolResults []*ai.ToolResultMessage
	Context     *AgentContext
	NewMessages []AgentMessage
}

// AgentTurnDecision mirrors the TS union; zero value preserves normal
// scheduling.
type AgentTurnDecision struct {
	Action string // "" | "continue" | "end"
}

// FinishTurn mirrors the TS callback.
type FinishTurn func(turn *AgentTurnContext, signal *abort.Signal) (AgentTurnDecision, error)

// AgentLoopTurnUpdate mirrors the TS interface.
type AgentLoopTurnUpdate struct {
	Context       *AgentContext
	HasContext    bool
	Messages      []AgentMessage
	Model         *ai.Model
	ThinkingLevel *string
}

// PrepareRequestContext mirrors the TS interface.
type PrepareRequestContext struct {
	Context       *AgentContext
	Model         *ai.Model
	ThinkingLevel string
}

// AgentRequestUpdate mirrors the TS type (turn update without messages).
type AgentRequestUpdate struct {
	Context       *AgentContext
	Model         *ai.Model
	ThinkingLevel *string
}

// PrepareRequest mirrors the TS callback.
type PrepareRequest func(request *PrepareRequestContext, signal *abort.Signal) (*AgentRequestUpdate, error)

// PrepareNextTurn mirrors the TS callback.
type PrepareNextTurn func(context *AgentTurnContext) (*AgentLoopTurnUpdate, error)

// ThinkingLevel values (agent-level).
const (
	ThinkingOff     = "off"
	ThinkingMinimal = "minimal"
	ThinkingLow     = "low"
	ThinkingMedium  = "medium"
	ThinkingHigh    = "high"
	ThinkingXHigh   = "xhigh"
	ThinkingMax     = "max"
)

// CustomAgentMessage is the extension point standing in for the TS
// CustomAgentMessages declaration merge. The harness registers its custom
// roles (bashExecution, custom, branchSummary, compactionSummary) through
// this type.
type CustomAgentMessage struct {
	Role_       string     `json:"role"`
	TimestampMs float64    `json:"timestamp"`
	Fields      *jsonx.Obj `json:"-"`
}

// Role implements AgentMessage.
func (c *CustomAgentMessage) Role() string { return c.Role_ }

// AgentMessage is ai.Message | custom messages.
type AgentMessage interface{ Role() string }

// AgentToolResult mirrors the TS interface. Details is arbitrary JSON.
type AgentToolResult struct {
	Content   []ai.ContentBlock
	Details   any
	Usage     *ai.Usage
	Terminate *bool
}

// AgentToolUpdateCallback streams partial execution updates.
type AgentToolUpdateCallback func(partialResult *AgentToolResult)

// AgentTool mirrors the TS interface (Tool + agent runtime fields).
type AgentTool struct {
	Name        string
	Description string
	Parameters  *typebox.Schema
	// Label is a human-readable label for UI display.
	Label string
	// PrepareArguments is an optional compatibility shim before validation.
	PrepareArguments func(args any) any
	// Execute runs the tool; it returns an error instead of encoding errors
	// in content.
	Execute func(toolCallID string, params any, signal *abort.Signal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error)
	// Replay is "never" | "safe" (nil = default).
	Replay *string
	// ExecutionMode is an optional per-tool override.
	ExecutionMode *string
}

// ToTool converts to the ai.Tool declaration shape.
func (t *AgentTool) ToTool() ai.Tool {
	return ai.Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters}
}

// AgentContext mirrors the TS interface.
type AgentContext struct {
	Messages []AgentMessage
	Tools    []*AgentTool
}

// AgentEvent is the 11-variant event union.
type AgentEvent interface{ EventType() string }

// EventAgentStart is {type:"agent_start"}.
type EventAgentStart struct{}

// EventType implements AgentEvent.
func (*EventAgentStart) EventType() string { return "agent_start" }

// EventAgentEnd is {type:"agent_end", messages}.
type EventAgentEnd struct{ Messages []AgentMessage }

// EventType implements AgentEvent.
func (*EventAgentEnd) EventType() string { return "agent_end" }

// EventTurnStart is {type:"turn_start"}.
type EventTurnStart struct{}

// EventType implements AgentEvent.
func (*EventTurnStart) EventType() string { return "turn_start" }

// EventTurnEnd is {type:"turn_end", message, toolResults}.
type EventTurnEnd struct {
	Message     *ai.AssistantMessage
	ToolResults []*ai.ToolResultMessage
}

// EventType implements AgentEvent.
func (*EventTurnEnd) EventType() string { return "turn_end" }

// EventMessageStart is {type:"message_start", message}.
type EventMessageStart struct{ Message AgentMessage }

// EventType implements AgentEvent.
func (*EventMessageStart) EventType() string { return "message_start" }

// EventMessageUpdate is {type:"message_update", message, assistantMessageEvent}.
type EventMessageUpdate struct {
	Message               *ai.AssistantMessage
	AssistantMessageEvent ai.AssistantMessageEvent
}

// EventType implements AgentEvent.
func (*EventMessageUpdate) EventType() string { return "message_update" }

// EventMessageEnd is {type:"message_end", message}.
type EventMessageEnd struct{ Message AgentMessage }

// EventType implements AgentEvent.
func (*EventMessageEnd) EventType() string { return "message_end" }

// EventToolExecutionStart is {type:"tool_execution_start", toolCallId,
// toolName, args}.
type EventToolExecutionStart struct {
	ToolCallID string
	ToolName   string
	Args       any
}

// EventType implements AgentEvent.
func (*EventToolExecutionStart) EventType() string { return "tool_execution_start" }

// EventToolExecutionUpdate is {type:"tool_execution_update", toolCallId,
// toolName, args, partialResult}.
type EventToolExecutionUpdate struct {
	ToolCallID    string
	ToolName      string
	Args          any
	PartialResult any
}

// EventType implements AgentEvent.
func (*EventToolExecutionUpdate) EventType() string { return "tool_execution_update" }

// EventToolExecutionEnd is {type:"tool_execution_end", toolCallId, toolName,
// result, isError}.
type EventToolExecutionEnd struct {
	ToolCallID string
	ToolName   string
	Result     *AgentToolResult
	IsError    bool
}

// EventType implements AgentEvent.
func (*EventToolExecutionEnd) EventType() string { return "tool_execution_end" }

// AgentLoopConfig mirrors the TS interface (SimpleStreamOptions + model +
// hooks). Hooks are optional (nil).
type AgentLoopConfig struct {
	ai.SimpleStreamOptions

	Model *ai.Model
	// Reasoning is the requested thinking level; nil means "off".
	Reasoning *string

	// ConvertToLlm converts AgentMessage[] to LLM-compatible Message[] before
	// each LLM call. Required. Must not fail.
	ConvertToLlm func(messages []AgentMessage) []ai.Message

	// TransformContext is an optional AgentMessage-level transform before
	// ConvertToLlm. Must not fail.
	TransformContext func(messages []AgentMessage, signal *abort.Signal) []AgentMessage

	// GetAPIKey resolves an API key dynamically for each call.
	GetAPIKey func(provider string) (string, bool)

	FinishTurn          FinishTurn
	PrepareRequest      PrepareRequest
	PrepareNextTurn     PrepareNextTurn
	GetSteeringMessages func() []AgentMessage
	GetFollowUpMessages func() []AgentMessage

	// ToolExecution is "sequential" or "parallel" (default).
	ToolExecution *string

	BeforeToolCall func(context *BeforeToolCallContext, signal *abort.Signal) (*BeforeToolCallResult, error)
	AfterToolCall  func(context *AfterToolCallContext, signal *abort.Signal) (*AfterToolCallResult, error)
}
