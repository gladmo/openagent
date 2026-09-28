package agent

// agent_loop.go ports agent-loop.ts: the low-level prompt -> tool -> prompt
// loop working with AgentMessage throughout, transforming to ai.Message only
// at the LLM boundary.

import (
	"fmt"
	"sync"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// AgentEventSink receives loop events sequentially. TS awaits sinks; Go
// sinks may block and are invoked in order (except parallel tool-execution
// end events, which may interleave exactly as TS Promise.all does).
type AgentEventSink func(event AgentEvent)

// AgentLoop starts an agent loop with a new prompt message, returning an
// event stream whose result is the run's new messages.
func AgentLoop(
	prompts []AgentMessage,
	context *AgentContext,
	config *AgentLoopConfig,
	signal *abort.Signal,
	streamFn StreamFn,
) *ai.EventStream[AgentEvent, []AgentMessage] {
	stream := ai.NewEventStream(func(event AgentEvent) bool {
		return event.EventType() == "agent_end"
	}, func(event AgentEvent) []AgentMessage {
		if end, ok := event.(*EventAgentEnd); ok {
			return end.Messages
		}
		return nil
	})
	go func() {
		messages, err := RunAgentLoop(prompts, context, config, func(event AgentEvent) {
			stream.Push(event)
		}, signal, streamFn)
		if err != nil {
			// runAgentLoop's emit contract: failures are encoded by the
			// stream function; an error here is a loop bug. Surface as an
			// agent_end with what we have (TS would reject the promise).
			stream.End(messages)
			return
		}
		stream.End(messages)
	}()
	return stream
}

// AgentLoopContinue continues an agent loop from the current context without
// adding a new message.
func AgentLoopContinue(
	context *AgentContext,
	config *AgentLoopConfig,
	signal *abort.Signal,
	streamFn StreamFn,
) (*ai.EventStream[AgentEvent, []AgentMessage], error) {
	if err := validateContinueContext(context); err != nil {
		return nil, err
	}
	stream := ai.NewEventStream(func(event AgentEvent) bool {
		return event.EventType() == "agent_end"
	}, func(event AgentEvent) []AgentMessage {
		if end, ok := event.(*EventAgentEnd); ok {
			return end.Messages
		}
		return nil
	})
	go func() {
		messages, _ := RunAgentLoopContinue(context, config, func(event AgentEvent) {
			stream.Push(event)
		}, signal, streamFn)
		stream.End(messages)
	}()
	return stream, nil
}

func validateContinueContext(context *AgentContext) error {
	if len(context.Messages) == 0 {
		return errorsNew("Cannot continue: no messages in context")
	}
	last := context.Messages[len(context.Messages)-1]
	if last.Role() == "assistant" {
		return errorsNew("Cannot continue from message role: assistant")
	}
	return nil
}

// RunAgentLoop runs the loop for a new prompt, emitting events to sink and
// returning the run's new messages.
func RunAgentLoop(
	prompts []AgentMessage,
	context *AgentContext,
	config *AgentLoopConfig,
	emit AgentEventSink,
	signal *abort.Signal,
	streamFn StreamFn,
) ([]AgentMessage, error) {
	resolved, err := resolveStreamFn(streamFn)
	if err != nil {
		return nil, err
	}
	streamFn = resolved
	initialMessages := declareToolChanges(context, prompts)
	newMessages := append([]AgentMessage{}, initialMessages...)
	currentContext := &AgentContext{
		Messages: append(append([]AgentMessage{}, context.Messages...), initialMessages...),
		Tools:    context.Tools,
	}

	emit(&EventAgentStart{})
	emit(&EventTurnStart{})
	for _, message := range initialMessages {
		emit(&EventMessageStart{Message: message})
		emit(&EventMessageEnd{Message: message})
	}

	newMessages, loopErr := runLoop(currentContext, newMessages, config, signal, emit, streamFn)
	if loopErr != nil {
		return newMessages, loopErr
	}
	return newMessages, nil
}

// resolveStreamFn applies the process-global default when the caller
// omitted streamFn, failing loud before the first model call.
func resolveStreamFn(streamFn StreamFn) (StreamFn, error) {
	if streamFn != nil {
		return streamFn, nil
	}
	return GetDefaultStreamFn()
}

// RunAgentLoopContinue runs the loop from the existing context.
func RunAgentLoopContinue(
	context *AgentContext,
	config *AgentLoopConfig,
	emit AgentEventSink,
	signal *abort.Signal,
	streamFn StreamFn,
) ([]AgentMessage, error) {
	if err := validateContinueContext(context); err != nil {
		return nil, err
	}
	resolved, err := resolveStreamFn(streamFn)
	if err != nil {
		return nil, err
	}
	streamFn = resolved
	// The caller's backing array is private to the caller: the loop appends
	// and replaces entries in place (TS copies the array).
	newMessages := []AgentMessage{}
	currentContext := &AgentContext{Messages: append([]AgentMessage{}, context.Messages...), Tools: context.Tools}

	emit(&EventAgentStart{})
	emit(&EventTurnStart{})

	newMessages, loopErr := runLoop(currentContext, newMessages, config, signal, emit, streamFn)
	if loopErr != nil {
		return newMessages, loopErr
	}
	return newMessages, nil
}

func errorsNew(msg string) error { return fmt.Errorf("%s", msg) }

// runLoop is the main loop logic shared by both entry points.
func runLoop(
	initialContext *AgentContext,
	newMessages []AgentMessage,
	initialConfig *AgentLoopConfig,
	signal *abort.Signal,
	emit AgentEventSink,
	streamFunction StreamFn,
) ([]AgentMessage, error) {
	currentContext := initialContext
	config := initialConfig
	var lastCompletedTurn *AgentTurnContext
	explicitContinuation := false
	// Check for steering messages at start (user may have typed while waiting).
	pendingMessages := getSteeringMessages(config)

	for {
		hasMoreToolCalls := true

		for hasMoreToolCalls || len(pendingMessages) > 0 {
			preparedMessages := []AgentMessage{}
			if lastCompletedTurn != nil {
				if config.PrepareNextTurn != nil {
					nextTurnSnapshot, err := config.PrepareNextTurn(lastCompletedTurn)
					if err != nil {
						return newMessages, err
					}
					if nextTurnSnapshot != nil {
						if nextTurnSnapshot.Context != nil {
							currentContext = nextTurnSnapshot.Context
						}
						preparedMessages = nextTurnSnapshot.Messages
						if nextTurnSnapshot.Model != nil {
							config.Model = nextTurnSnapshot.Model
						}
						if nextTurnSnapshot.ThinkingLevel != nil {
							config.Reasoning = normalizeReasoning(nextTurnSnapshot.ThinkingLevel)
						}
					}
				}
				// Preparation can be long-running. Pick up steering queued
				// while it ran; only poll again when the earlier poll was
				// empty (one-at-a-time fairness).
				if len(pendingMessages) == 0 {
					pendingMessages = getSteeringMessages(config)
				}
				emit(&EventTurnStart{})
			}

			// Process prepared and queued messages before the next response.
			combined := append(append([]AgentMessage{}, preparedMessages...), pendingMessages...)
			for _, message := range declareToolChanges(currentContext, combined) {
				emit(&EventMessageStart{Message: message})
				emit(&EventMessageEnd{Message: message})
				currentContext.Messages = append(currentContext.Messages, message)
				newMessages = append(newMessages, message)
			}
			pendingMessages = []AgentMessage{}

			if config.PrepareRequest != nil {
				thinkingLevel := ThinkingOff
				if config.Reasoning != nil {
					thinkingLevel = *config.Reasoning
				}
				requestUpdate, err := config.PrepareRequest(&PrepareRequestContext{
					Context: currentContext, Model: config.Model, ThinkingLevel: thinkingLevel,
				}, signal)
				if err != nil {
					return newMessages, err
				}
				if requestUpdate != nil {
					if requestUpdate.Context != nil {
						currentContext = requestUpdate.Context
					}
					if requestUpdate.Model != nil {
						config.Model = requestUpdate.Model
					}
					if requestUpdate.ThinkingLevel != nil {
						config.Reasoning = normalizeReasoning(requestUpdate.ThinkingLevel)
					}
				}
			}

			// Stream assistant response.
			message, err := streamAssistantResponse(currentContext, config, signal, emit, streamFunction)
			if err != nil {
				return newMessages, err
			}
			newMessages = append(newMessages, message)

			if message.StopReason == ai.StopError || message.StopReason == ai.StopAborted {
				lastCompletedTurn = &AgentTurnContext{
					Message: message, ToolResults: []*ai.ToolResultMessage{},
					Context: currentContext, NewMessages: newMessages,
				}
				if config.FinishTurn != nil {
					if _, err := config.FinishTurn(lastCompletedTurn, signal); err != nil {
						return newMessages, err
					}
				}
				emit(&EventTurnEnd{Message: message, ToolResults: []*ai.ToolResultMessage{}})
				emit(&EventAgentEnd{Messages: newMessages})
				return newMessages, nil
			}

			// Check for tool calls.
			var toolCalls []*ai.ToolCall
			for _, block := range message.Content {
				if call, ok := block.(*ai.ToolCall); ok {
					toolCalls = append(toolCalls, call)
				}
			}

			toolResults := []*ai.ToolResultMessage{}
			hasMoreToolCalls = false
			if len(toolCalls) > 0 {
				// A "length" stop means arguments may be truncated: fail all
				// calls instead of executing them.
				var executed *executedToolCallBatch
				if message.StopReason == ai.StopLength {
					executed = failToolCallsFromTruncatedMessage(toolCalls, emit)
				} else {
					executed = executeToolCalls(currentContext, message, config, signal, emit)
				}
				toolResults = append(toolResults, executed.messages...)
				hasMoreToolCalls = !executed.terminate

				for _, result := range toolResults {
					currentContext.Messages = append(currentContext.Messages, result)
					newMessages = append(newMessages, result)
				}
			}

			lastCompletedTurn = &AgentTurnContext{
				Message: message, ToolResults: toolResults,
				Context: currentContext, NewMessages: newMessages,
			}
			var decision AgentTurnDecision
			if config.FinishTurn != nil {
				d, err := config.FinishTurn(lastCompletedTurn, signal)
				if err != nil {
					return newMessages, err
				}
				decision = d
			}
			emit(&EventTurnEnd{Message: message, ToolResults: toolResults})

			if decision.Action == "end" {
				emit(&EventAgentEnd{Messages: newMessages})
				return newMessages, nil
			}

			explicitContinuation = decision.Action == "continue"
			pendingMessages = getSteeringMessages(config)
			if hasMoreToolCalls || len(pendingMessages) > 0 {
				explicitContinuation = false
			}
		}

		// Agent would stop here. Check for follow-up messages.
		followUpMessages := getFollowUpMessages(config)
		if len(followUpMessages) > 0 {
			explicitContinuation = false
			pendingMessages = followUpMessages
			continue
		}

		// Fulfill an explicit continuation with one context-only turn.
		if explicitContinuation {
			explicitContinuation = false
			continue
		}
		break
	}

	emit(&EventAgentEnd{Messages: newMessages})
	return newMessages, nil
}

// normalizeReasoning maps "off" to nil.
func normalizeReasoning(level *string) *string {
	if level == nil || *level == ThinkingOff {
		return nil
	}
	return level
}

// declareToolChanges declares tool loadout changes to the model.
func declareToolChanges(context *AgentContext, pendingMessages []AgentMessage) []AgentMessage {
	systemIndex := -1
	for i := len(pendingMessages) - 1; i >= 0; i-- {
		if pendingMessages[i].Role() == "system" {
			systemIndex = i
			break
		}
	}
	var pending *ai.SystemMessage
	if systemIndex >= 0 {
		if sm, ok := pendingMessages[systemIndex].(*ai.SystemMessage); ok {
			pending = sm
		}
	}
	var baseline []AgentMessage
	if pending != nil {
		baseline = append([]AgentMessage{}, pendingMessages...)
		baseline[systemIndex] = withToolChanges(pending, noChanges())
	} else {
		baseline = pendingMessages
	}
	declared := ai.GetCurrentTools(agentMessagesToAI(append(append([]AgentMessage{}, context.Messages...), baseline...)))
	executable := make([]ai.Tool, 0, len(context.Tools))
	for _, tool := range context.Tools {
		executable = append(executable, ai.ToToolDeclaration(tool.ToTool()))
	}
	changes := ai.GetToolStateChanges(declared, executable)
	unchanged := len(changes.ToolsAdded) == 0 && len(changes.ToolsRemoved) == 0

	if pending != nil {
		if unchanged && len(pending.ToolsAdded) == 0 && len(pending.ToolsRemoved) == 0 {
			return pendingMessages
		}
		out := append([]AgentMessage{}, baseline...)
		out[systemIndex] = withToolChanges(pending, changes)
		return out
	}
	if unchanged {
		return pendingMessages
	}
	now := float64(time.Now().UnixMilli())
	update := withToolChanges(&ai.SystemMessage{Content: ai.StringContent(""), TimestampMs: now}, changes)
	insertIndex := -1
	for i, message := range pendingMessages {
		if message.Role() != "system" {
			insertIndex = i
			break
		}
	}
	index := len(pendingMessages)
	if insertIndex != -1 {
		index = insertIndex
	}
	out := make([]AgentMessage, 0, len(pendingMessages)+1)
	out = append(out, pendingMessages[:index]...)
	out = append(out, update)
	out = append(out, pendingMessages[index:]...)
	return out
}

func noChanges() ai.ToolStateChanges {
	return ai.ToolStateChanges{ToolsAdded: []ai.Tool{}, ToolsRemoved: []ai.ToolReference{}}
}

// withToolChanges copies a system message with its tool fields replaced;
// empty lists omit the fields.
func withToolChanges(message *ai.SystemMessage, changes ai.ToolStateChanges) *ai.SystemMessage {
	clone := &ai.SystemMessage{
		Content:      message.Content,
		Sections:     message.Sections,
		SectionOrder: message.SectionOrder,
		HasSections:  message.HasSections,
		TimestampMs:  message.TimestampMs,
	}
	if len(changes.ToolsAdded) > 0 {
		clone.ToolsAdded = changes.ToolsAdded
	}
	if len(changes.ToolsRemoved) > 0 {
		clone.ToolsRemoved = changes.ToolsRemoved
	}
	return clone
}

func agentMessagesToAI(messages []AgentMessage) []ai.Message {
	out := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		if m, ok := message.(ai.Message); ok {
			out = append(out, m)
		}
	}
	return out
}

// streamAssistantResponse streams one assistant response, transforming
// AgentMessage[] to ai.Message[] at the boundary.
func streamAssistantResponse(
	context *AgentContext,
	config *AgentLoopConfig,
	signal *abort.Signal,
	emit AgentEventSink,
	streamFunction StreamFn,
) (*ai.AssistantMessage, error) {
	messages := context.Messages
	if config.TransformContext != nil {
		messages = config.TransformContext(messages, signal)
	}

	llmMessages := config.ConvertToLlm(messages)
	llmContext := ai.NormalizeContext(ai.Context{Messages: llmMessages})

	// Resolve API key (important for expiring tokens).
	options := config.SimpleStreamOptions
	resolved := ""
	hasKey := false
	if config.GetAPIKey != nil {
		if key, ok := config.GetAPIKey(config.Model.Provider); ok {
			resolved, hasKey = key, true
		}
	}
	if !hasKey && options.APIKey != nil {
		resolved, hasKey = *options.APIKey, true
	}
	if hasKey {
		options.APIKey = &resolved
	} else {
		options.APIKey = nil
	}
	options.Signal = signal

	response := streamFunction(config.Model, llmContext, &options)

	var partialMessage *ai.AssistantMessage
	addedPartial := false

	for {
		event, ok := response.Next()
		if !ok {
			break
		}
		switch e := event.(type) {
		case *ai.EventStart:
			partialMessage = e.Partial
			context.Messages = append(context.Messages, partialMessage)
			addedPartial = true
			emit(&EventMessageStart{Message: shallowCopyAssistant(partialMessage)})
		case *ai.EventTextStart, *ai.EventTextDelta, *ai.EventTextEnd,
			*ai.EventThinkingStart, *ai.EventThinkingDelta, *ai.EventThinkingEnd,
			*ai.EventToolCallStart, *ai.EventToolCallDelta, *ai.EventToolCallEnd:
			if partialMessage != nil {
				partialMessage = eventPartial(e)
				context.Messages[len(context.Messages)-1] = partialMessage
				emit(&EventMessageUpdate{
					Message:               shallowCopyAssistant(partialMessage),
					AssistantMessageEvent: e,
				})
			}
		case *ai.EventDone, *ai.EventError:
			finalMessage := response.Result()
			if addedPartial {
				context.Messages[len(context.Messages)-1] = finalMessage
			} else {
				context.Messages = append(context.Messages, finalMessage)
			}
			if !addedPartial {
				emit(&EventMessageStart{Message: finalMessage})
			}
			emit(&EventMessageEnd{Message: finalMessage})
			return finalMessage, nil
		}
	}

	finalMessage := response.Result()
	if addedPartial {
		context.Messages[len(context.Messages)-1] = finalMessage
	} else {
		context.Messages = append(context.Messages, finalMessage)
		emit(&EventMessageStart{Message: finalMessage})
	}
	emit(&EventMessageEnd{Message: finalMessage})
	return finalMessage, nil
}

func eventPartial(event ai.AssistantMessageEvent) *ai.AssistantMessage {
	switch t := event.(type) {
	case *ai.EventTextStart:
		return t.Partial
	case *ai.EventTextDelta:
		return t.Partial
	case *ai.EventTextEnd:
		return t.Partial
	case *ai.EventThinkingStart:
		return t.Partial
	case *ai.EventThinkingDelta:
		return t.Partial
	case *ai.EventThinkingEnd:
		return t.Partial
	case *ai.EventToolCallStart:
		return t.Partial
	case *ai.EventToolCallDelta:
		return t.Partial
	case *ai.EventToolCallEnd:
		return t.Partial
	default:
		return nil
	}
}

func shallowCopyAssistant(message *ai.AssistantMessage) *ai.AssistantMessage {
	clone := *message
	clone.Content = append([]ai.ContentBlock{}, message.Content...)
	return &clone
}

type executedToolCallBatch struct {
	messages  []*ai.ToolResultMessage
	terminate bool
}

type finalizedToolCallOutcome struct {
	toolCall *ai.ToolCall
	result   *AgentToolResult
	isError  bool
}

type preparedToolCall struct {
	tool     *AgentTool
	toolCall *ai.ToolCall
	args     any
}

// failToolCallsFromTruncatedMessage fails every tool call from a truncated
// message so the model can re-issue them.
func failToolCallsFromTruncatedMessage(toolCalls []*ai.ToolCall, emit AgentEventSink) *executedToolCallBatch {
	messages := []*ai.ToolResultMessage{}
	for _, toolCall := range toolCalls {
		emit(&EventToolExecutionStart{ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: toolCall.Arguments})
		finalized := finalizedToolCallOutcome{
			toolCall: toolCall,
			result: createErrorToolResult(fmt.Sprintf(
				"Tool call %q was not executed: the response hit the output token limit, so its arguments may be truncated. Re-issue the tool call with complete arguments.",
				toolCall.Name)),
			isError: true,
		}
		emitToolExecutionEnd(finalized, emit)
		toolResultMessage := createToolResultMessage(finalized)
		emitToolResultMessage(toolResultMessage, emit)
		messages = append(messages, toolResultMessage)
	}
	return &executedToolCallBatch{messages: messages, terminate: false}
}

// executeToolCalls runs the batch sequentially or in parallel.
func executeToolCalls(
	currentContext *AgentContext,
	assistantMessage *ai.AssistantMessage,
	config *AgentLoopConfig,
	signal *abort.Signal,
	emit AgentEventSink,
) *executedToolCallBatch {
	var toolCalls []*ai.ToolCall
	for _, block := range assistantMessage.Content {
		if call, ok := block.(*ai.ToolCall); ok {
			toolCalls = append(toolCalls, call)
		}
	}
	hasSequentialToolCall := false
	for _, tc := range toolCalls {
		for _, tool := range currentContext.Tools {
			if tool.Name == tc.Name && tool.ExecutionMode != nil && *tool.ExecutionMode == ToolExecutionSequential {
				hasSequentialToolCall = true
			}
		}
	}
	if (config.ToolExecution != nil && *config.ToolExecution == ToolExecutionSequential) || hasSequentialToolCall {
		return executeToolCallsSequential(currentContext, assistantMessage, toolCalls, config, signal, emit)
	}
	return executeToolCallsParallel(currentContext, assistantMessage, toolCalls, config, signal, emit)
}

func executeToolCallsSequential(
	currentContext *AgentContext,
	assistantMessage *ai.AssistantMessage,
	toolCalls []*ai.ToolCall,
	config *AgentLoopConfig,
	signal *abort.Signal,
	emit AgentEventSink,
) *executedToolCallBatch {
	finalizedCalls := []finalizedToolCallOutcome{}
	messages := []*ai.ToolResultMessage{}

	for _, toolCall := range toolCalls {
		emit(&EventToolExecutionStart{ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: toolCall.Arguments})

		preparation := prepareToolCall(currentContext, assistantMessage, toolCall, config, signal)
		var finalized finalizedToolCallOutcome
		if preparation.immediate != nil {
			finalized = finalizedToolCallOutcome{toolCall: toolCall, result: preparation.immediate.result, isError: preparation.immediate.isError}
		} else {
			executed := executePreparedToolCall(preparation.prepared, signal, emit)
			finalized = finalizeExecutedToolCall(currentContext, assistantMessage, preparation.prepared, executed, config, signal)
		}

		emitToolExecutionEnd(finalized, emit)
		toolResultMessage := createToolResultMessage(finalized)
		emitToolResultMessage(toolResultMessage, emit)
		finalizedCalls = append(finalizedCalls, finalized)
		messages = append(messages, toolResultMessage)

		if signal.Aborted() {
			break
		}
	}

	return &executedToolCallBatch{messages: messages, terminate: shouldTerminateToolBatch(finalizedCalls)}
}

type parallelEntry struct {
	outcome finalizedToolCallOutcome
	lazy    func() finalizedToolCallOutcome
	isLazy  bool
}

func executeToolCallsParallel(
	currentContext *AgentContext,
	assistantMessage *ai.AssistantMessage,
	toolCalls []*ai.ToolCall,
	config *AgentLoopConfig,
	signal *abort.Signal,
	emit AgentEventSink,
) *executedToolCallBatch {
	entries := []*parallelEntry{}

	for _, toolCall := range toolCalls {
		emit(&EventToolExecutionStart{ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: toolCall.Arguments})

		preparation := prepareToolCall(currentContext, assistantMessage, toolCall, config, signal)
		if preparation.immediate != nil {
			finalized := finalizedToolCallOutcome{toolCall: toolCall, result: preparation.immediate.result, isError: preparation.immediate.isError}
			emitToolExecutionEnd(finalized, emit)
			entries = append(entries, &parallelEntry{outcome: finalized})
			if signal.Aborted() {
				break
			}
			continue
		}

		prepared := preparation.prepared
		entries = append(entries, &parallelEntry{isLazy: true, lazy: func() finalizedToolCallOutcome {
			if signal.Aborted() {
				aborted := finalizedToolCallOutcome{
					toolCall: toolCall,
					result:   createErrorToolResult("Operation aborted"),
					isError:  true,
				}
				emitToolExecutionEnd(aborted, emit)
				return aborted
			}
			executed := executePreparedToolCall(prepared, signal, emit)
			finalized := finalizeExecutedToolCall(currentContext, assistantMessage, prepared, executed, config, signal)
			emitToolExecutionEnd(finalized, emit)
			return finalized
		}})
		if signal.Aborted() {
			break
		}
	}

	// Run lazy thunks concurrently (Promise.all); tool_execution_end fires in
	// completion order, tool-result messages later in source order.
	var wg sync.WaitGroup
	for _, entry := range entries {
		if entry.isLazy {
			wg.Add(1)
			go func(entry *parallelEntry) {
				defer wg.Done()
				entry.outcome = entry.lazy()
				entry.isLazy = false
			}(entry)
		}
	}
	wg.Wait()

	orderedFinalizedCalls := make([]finalizedToolCallOutcome, 0, len(entries))
	for _, entry := range entries {
		orderedFinalizedCalls = append(orderedFinalizedCalls, entry.outcome)
	}
	messages := []*ai.ToolResultMessage{}
	for _, finalized := range orderedFinalizedCalls {
		toolResultMessage := createToolResultMessage(finalized)
		emitToolResultMessage(toolResultMessage, emit)
		messages = append(messages, toolResultMessage)
	}

	return &executedToolCallBatch{messages: messages, terminate: shouldTerminateToolBatch(orderedFinalizedCalls)}
}

type preparationResult struct {
	prepared  *preparedToolCall
	immediate *immediateOutcome
}

type immediateOutcome struct {
	result  *AgentToolResult
	isError bool
}

func shouldTerminateToolBatch(finalizedCalls []finalizedToolCallOutcome) bool {
	if len(finalizedCalls) == 0 {
		return false
	}
	for _, finalized := range finalizedCalls {
		if finalized.result.Terminate == nil || !*finalized.result.Terminate {
			return false
		}
	}
	return true
}

func prepareToolCallArguments(tool *AgentTool, toolCall *ai.ToolCall) *ai.ToolCall {
	if tool.PrepareArguments == nil {
		return toolCall
	}
	preparedArguments := tool.PrepareArguments(toolCall.Arguments)
	if obj, ok := preparedArguments.(*jsonx.Obj); ok && obj == toolCall.Arguments {
		return toolCall
	}
	clone := *toolCall
	if obj, ok := preparedArguments.(*jsonx.Obj); ok {
		clone.Arguments = obj
	} else {
		clone.Arguments = jsonx.NewObj()
	}
	return &clone
}

func prepareToolCall(
	currentContext *AgentContext,
	assistantMessage *ai.AssistantMessage,
	toolCall *ai.ToolCall,
	config *AgentLoopConfig,
	signal *abort.Signal,
) preparationResult {
	var tool *AgentTool
	for _, candidate := range currentContext.Tools {
		if candidate.Name == toolCall.Name {
			tool = candidate
			break
		}
	}
	if tool == nil {
		return preparationResult{immediate: &immediateOutcome{
			result:  createErrorToolResult(fmt.Sprintf("Tool %s not found", toolCall.Name)),
			isError: true,
		}}
	}

	preparedCall := prepareToolCallArguments(tool, toolCall)
	toolForValidation := ai.Tool{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}
	validatedArgs, err := ai.ValidateToolArguments(&toolForValidation, preparedCall)
	if err != nil {
		return preparationResult{immediate: &immediateOutcome{result: createErrorToolResult(err.Error()), isError: true}}
	}
	if config.BeforeToolCall != nil {
		beforeResult, err := config.BeforeToolCall(&BeforeToolCallContext{
			AssistantMessage: assistantMessage,
			ToolCall:         toolCall,
			Args:             validatedArgs,
			Context:          currentContext,
		}, signal)
		if signal.Aborted() {
			return preparationResult{immediate: &immediateOutcome{result: createErrorToolResult("Operation aborted"), isError: true}}
		}
		if err != nil {
			return preparationResult{immediate: &immediateOutcome{result: createErrorToolResult(err.Error()), isError: true}}
		}
		if beforeResult != nil && beforeResult.Block != nil && *beforeResult.Block {
			reason := "Tool execution was blocked"
			if beforeResult.Reason != "" {
				reason = beforeResult.Reason
			}
			result := createErrorToolResult(reason)
			if beforeResult.Terminate != nil && *beforeResult.Terminate {
				terminate := true
				result.Terminate = &terminate
			}
			return preparationResult{immediate: &immediateOutcome{result: result, isError: true}}
		}
	}
	if signal.Aborted() {
		return preparationResult{immediate: &immediateOutcome{result: createErrorToolResult("Operation aborted"), isError: true}}
	}
	return preparationResult{prepared: &preparedToolCall{tool: tool, toolCall: toolCall, args: validatedArgs}}
}

func executePreparedToolCall(
	prepared *preparedToolCall,
	signal *abort.Signal,
	emit AgentEventSink,
) *immediateOutcome {
	result, err := prepared.tool.Execute(prepared.toolCall.ID, prepared.args, signal, func(partialResult *AgentToolResult) {
		emit(&EventToolExecutionUpdate{
			ToolCallID:    prepared.toolCall.ID,
			ToolName:      prepared.toolCall.Name,
			Args:          prepared.toolCall.Arguments,
			PartialResult: partialResult,
		})
	})
	if err != nil {
		return &immediateOutcome{result: createErrorToolResult(err.Error()), isError: true}
	}
	if result == nil {
		// A tool returning (nil, nil) would nil-deref the loop's
		// finalization; convert it to an error result like a throw.
		return &immediateOutcome{result: createErrorToolResult("Tool " + prepared.toolCall.Name + " returned no result"), isError: true}
	}
	return &immediateOutcome{result: result, isError: false}
}

func finalizeExecutedToolCall(
	currentContext *AgentContext,
	assistantMessage *ai.AssistantMessage,
	prepared *preparedToolCall,
	executed *immediateOutcome,
	config *AgentLoopConfig,
	signal *abort.Signal,
) finalizedToolCallOutcome {
	result := executed.result
	isError := executed.isError

	if config.AfterToolCall != nil {
		afterResult, err := config.AfterToolCall(&AfterToolCallContext{
			AssistantMessage: assistantMessage,
			ToolCall:         prepared.toolCall,
			Args:             prepared.args,
			Result:           result,
			IsError:          isError,
			Context:          currentContext,
		}, signal)
		if err != nil {
			result = createErrorToolResult(err.Error())
			isError = true
		} else if afterResult != nil {
			if afterResult.HasContent {
				result.Content = afterResult.Content
			}
			if afterResult.HasDetails {
				result.Details = afterResult.Details
			}
			if afterResult.Usage != nil {
				result.Usage = afterResult.Usage
			}
			if afterResult.Terminate != nil {
				result.Terminate = afterResult.Terminate
			}
			if afterResult.IsError != nil {
				isError = *afterResult.IsError
			}
		}
	}

	return finalizedToolCallOutcome{toolCall: prepared.toolCall, result: result, isError: isError}
}

func createErrorToolResult(message string) *AgentToolResult {
	return &AgentToolResult{
		Content: []ai.ContentBlock{ai.TextContent{Text: message}},
		Details: jsonx.NewObj(),
	}
}

func emitToolExecutionEnd(finalized finalizedToolCallOutcome, emit AgentEventSink) {
	emit(&EventToolExecutionEnd{
		ToolCallID: finalized.toolCall.ID,
		ToolName:   finalized.toolCall.Name,
		Result:     finalized.result,
		IsError:    finalized.isError,
	})
}

func createToolResultMessage(finalized finalizedToolCallOutcome) *ai.ToolResultMessage {
	content := finalized.result.Content
	if content == nil {
		content = []ai.ContentBlock{}
	}
	details := finalized.result.Details
	if details == nil {
		details = jsonx.NewObj()
	}
	return &ai.ToolResultMessage{
		ToolCallID:  finalized.toolCall.ID,
		ToolName:    finalized.toolCall.Name,
		Content:     content,
		Details:     details,
		Usage:       finalized.result.Usage,
		IsError:     finalized.isError,
		TimestampMs: float64(time.Now().UnixMilli()),
	}
}

func emitToolResultMessage(toolResultMessage *ai.ToolResultMessage, emit AgentEventSink) {
	emit(&EventMessageStart{Message: toolResultMessage})
	emit(&EventMessageEnd{Message: toolResultMessage})
}

func getSteeringMessages(config *AgentLoopConfig) []AgentMessage {
	if config.GetSteeringMessages == nil {
		return nil
	}
	return config.GetSteeringMessages()
}

func getFollowUpMessages(config *AgentLoopConfig) []AgentMessage {
	if config.GetFollowUpMessages == nil {
		return nil
	}
	return config.GetFollowUpMessages()
}
