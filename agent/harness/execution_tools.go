package harness

// execution_tools.go ports harness/execution/tools.ts.

import (
	"fmt"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// PreparedToolCall: tool exists and prepared arguments passed validation.
type PreparedToolCall struct {
	ToolCall *ai.ToolCall
	Tool     *AgentHarnessTool
	Args     *jsonx.Obj
}

// ImmediateToolOutcome: synthetic result without crossing the tool-effect
// boundary.
type ImmediateToolOutcome struct {
	ToolCall  *ai.ToolCall
	Result    *agent.AgentToolResult
	Terminate bool
}

// BeforeToolDecision: aggregated before-tool hook decision.
type BeforeToolDecision struct {
	Args  *jsonx.Obj
	Block *struct {
		Reason    string
		Terminate bool
	}
}

// ClearedToolCall: prepared call cleared for intent publication/execution.
type ClearedToolCall struct {
	ToolCall *ai.ToolCall
	Tool     *AgentHarnessTool
	Args     *jsonx.Obj
}

// ExecutedToolCall: raw phase-two output before after-tool patching.
type ExecutedToolCall struct {
	Result  *agent.AgentToolResult
	IsError bool
}

// AfterToolPatch: aggregated after-tool patch.
type AfterToolPatch struct {
	Content    []ai.ContentBlock
	HasContent bool
	Details    any
	HasDetails bool
	IsError    *bool
	Usage      *ai.Usage
	Terminate  *bool
}

// FinalizedToolCall: final output ready for the durable tool-result
// message.
type FinalizedToolCall struct {
	ToolCall  *ai.ToolCall
	Result    *agent.AgentToolResult
	IsError   bool
	Terminate bool
}

func executionErrorToolResult(message string) *agent.AgentToolResult {
	return &agent.AgentToolResult{
		Content: []ai.ContentBlock{ai.TextContent{Text: message}},
	}
}

func executionImmediateError(toolCall *ai.ToolCall, message string, terminate bool) *ImmediateToolOutcome {
	return &ImmediateToolOutcome{
		ToolCall:  toolCall,
		Result:    executionErrorToolResult(message),
		Terminate: terminate,
	}
}

// PrepareToolCall resolves a tool, applies deterministic argument
// preparation, and validates the result.
func PrepareToolCall(call *ai.ToolCall, tools []*AgentHarnessTool) (*PreparedToolCall, *ImmediateToolOutcome) {
	var tool *AgentHarnessTool
	for _, candidate := range tools {
		if candidate.Name == call.Name {
			tool = candidate
			break
		}
	}
	if tool == nil {
		return nil, executionImmediateError(call, fmt.Sprintf("Tool %s is unavailable", jsonx.Stringify(call.Name)), false)
	}

	preparedArguments := any(call.Arguments)
	if tool.PrepareArguments != nil {
		preparedArguments = tool.PrepareArguments(call.Arguments)
	}
	preparedCall := call
	if obj, ok := preparedArguments.(*jsonx.Obj); ok && obj != call.Arguments {
		clone := *call
		clone.Arguments = obj
		preparedCall = &clone
	}
	args, err := ai.ValidateToolArguments(&ai.Tool{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}, preparedCall)
	if err != nil {
		return nil, executionImmediateError(call, err.Error(), false)
	}
	return &PreparedToolCall{ToolCall: call, Tool: tool, Args: args.(*jsonx.Obj)}, nil
}

// ApplyBeforeToolDecision applies an explicit hook decision; replacement
// arguments are revalidated.
func ApplyBeforeToolDecision(prepared *PreparedToolCall, decision *BeforeToolDecision) (*ClearedToolCall, *ImmediateToolOutcome) {
	if decision != nil && decision.Block != nil {
		return nil, executionImmediateError(prepared.ToolCall, decision.Block.Reason, decision.Block.Terminate)
	}
	if decision == nil || decision.Args == nil {
		return &ClearedToolCall{ToolCall: prepared.ToolCall, Tool: prepared.Tool, Args: prepared.Args}, nil
	}
	clone := *prepared.ToolCall
	clone.Arguments = decision.Args
	args, err := ai.ValidateToolArguments(&ai.Tool{Name: prepared.Tool.Name, Description: prepared.Tool.Description, Parameters: prepared.Tool.Parameters}, &clone)
	if err != nil {
		return nil, executionImmediateError(prepared.ToolCall, err.Error(), false)
	}
	return &ClearedToolCall{ToolCall: prepared.ToolCall, Tool: prepared.Tool, Args: args.(*jsonx.Obj)}, nil
}

// ExecuteToolCall executes one cleared external tool effect. A tool that
// panics (Go's throw) converts to an isErrored result, mirroring the TS
// conversion of tool throws to error output; AbortRequested keeps unwinding
// to the caller (mirroring the TS gate.admit throw). Panics with
// AbortRequested when the gate rejects admission.
func ExecuteToolCall(
	call *ClearedToolCall,
	gate Gate,
	onUpdate AgentHarnessToolUpdateCallback,
	toolContext any,
	invocation AgentHarnessToolInvocation,
	ctx Context,
) (executed *ExecutedToolCall, admitted bool) {
	if onUpdate == nil {
		onUpdate = func(*agent.AgentToolResult, *AgentHarnessToolUpdateOptions) {}
	}
	result := func() (outcome *ExecutedToolCall) {
		gate.Admit(func() {
			admittedContext := WithAbortSignal(gate.Signal(), ctx)
			if err := admittedContext.AbortSignal().ThrowIfAborted(); err != nil {
				panic(err)
			}
			acceptingUpdates := true
			defer func() { acceptingUpdates = false }()
			defer func() {
				if r := recover(); r != nil {
					if _, isAbort := r.(*AbortRequested); isAbort {
						panic(r)
					}
					outcome = &ExecutedToolCall{Result: executionErrorToolResult(ToError(r).Error()), IsError: true}
				}
			}()
			result, err := call.Tool.Execute(
				call.ToolCall.ID,
				call.Args,
				func(partial *agent.AgentToolResult, options *AgentHarnessToolUpdateOptions) {
					if acceptingUpdates {
						onUpdate(partial, options)
					}
				},
				toolContext,
				invocation,
				admittedContext,
			)
			if err != nil {
				outcome = &ExecutedToolCall{Result: executionErrorToolResult(err.Error()), IsError: true}
				return
			}
			outcome = &ExecutedToolCall{Result: result, IsError: false}
		})
		return
	}()
	return result, true
}

// FinalizeToolCall applies an after-tool patch field by field.
func FinalizeToolCall(call *ClearedToolCall, executed *ExecutedToolCall, patch *AfterToolPatch) *FinalizedToolCall {
	result := executed.Result
	if patch != nil {
		merged := *result
		if patch.HasContent {
			merged.Content = patch.Content
		}
		if patch.HasDetails {
			merged.Details = patch.Details
		}
		if patch.Usage != nil {
			merged.Usage = patch.Usage
		}
		if patch.Terminate != nil {
			merged.Terminate = patch.Terminate
		}
		result = &merged
	}
	isError := executed.IsError
	if patch != nil && patch.IsError != nil {
		isError = *patch.IsError
	}
	return &FinalizedToolCall{
		ToolCall:  call.ToolCall,
		Result:    result,
		IsError:   isError,
		Terminate: result.Terminate != nil && *result.Terminate,
	}
}

// ToolResultFromMessage reconstructs the canonical tool result represented
// by a staged transcript message.
func ToolResultFromMessage(message *ai.ToolResultMessage, terminate bool) *agent.AgentToolResult {
	result := &agent.AgentToolResult{
		Content: message.Content,
		Details: message.Details,
		Usage:   message.Usage,
	}
	if terminate {
		t := true
		result.Terminate = &t
	}
	return result
}

// CreateToolResultMessage converts finalized tool output to the
// provider-facing transcript message.
func CreateToolResultMessage(call *FinalizedToolCall) *ai.ToolResultMessage {
	content := call.Result.Content
	if content == nil {
		content = []ai.ContentBlock{}
	}
	msg := &ai.ToolResultMessage{
		ToolCallID:  call.ToolCall.ID,
		ToolName:    call.ToolCall.Name,
		Content:     content,
		Details:     call.Result.Details,
		Usage:       call.Result.Usage,
		IsError:     call.IsError,
		TimestampMs: float64(time.Now().UnixMilli()),
	}
	return msg
}

// keep abort referenced for gate signal handling parity
var _ = abort.NewController
