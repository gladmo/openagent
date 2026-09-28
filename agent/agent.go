package agent

// agent.go ports agent.ts: the stateful Agent wrapper over the low-level
// loop.

import (
	"fmt"
	"sync"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/ai"
)

func defaultConvertToLlm(messages []AgentMessage) []ai.Message {
	out := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		switch m := message.(type) {
		case *ai.SystemMessage, *ai.UserMessage, *ai.AssistantMessage, *ai.ToolResultMessage:
			out = append(out, m.(ai.Message))
		}
	}
	return out
}

var emptyUsage = ai.Usage{}

// DefaultModel mirrors DEFAULT_MODEL.
func DefaultModel() *ai.Model {
	return &ai.Model{ID: "unknown", Name: "unknown", API: "unknown", Provider: "unknown"}
}

// AgentInitialState mirrors the TS type.
type AgentInitialState struct {
	SystemPrompt  *string
	Model         *ai.Model
	ThinkingLevel *string
	Tools         []*AgentTool
	Messages      []AgentMessage
}

// AgentOptions mirrors the TS interface. StreamFn is required.
type AgentOptions struct {
	InitialState               *AgentInitialState
	ConvertToLlm               func(messages []AgentMessage) []ai.Message
	TransformContext           func(messages []AgentMessage, signal *abort.Signal) []AgentMessage
	StreamFn                   StreamFn
	GetAPIKey                  func(provider string) (string, bool)
	OnPayload                  func(payload any, model *ai.Model) any
	OnResponse                 func(response ai.ProviderResponse, model *ai.Model)
	OnProviderStreamEvent      func(data any, model *ai.Model)
	BeforeToolCall             func(context *BeforeToolCallContext, signal *abort.Signal) (*BeforeToolCallResult, error)
	AfterToolCall              func(context *AfterToolCallContext, signal *abort.Signal) (*AfterToolCallResult, error)
	FinishTurn                 FinishTurn
	PrepareRequest             PrepareRequest
	PrepareNextTurn            func(signal *abort.Signal) (*AgentLoopTurnUpdate, error)
	PrepareNextTurnWithContext func(context *AgentTurnContext, signal *abort.Signal) (*AgentLoopTurnUpdate, error)
	SteeringMode               *string
	FollowUpMode               *string
	SessionID                  *string
	ThinkingBudgets            map[string]float64
	Transport                  *string
	MaxRetryDelayMs            *float64
	ToolExecution              *string
}

type pendingMessageQueue struct {
	mu       sync.Mutex
	messages []AgentMessage
	mode     string
}

func newPendingMessageQueue(mode string) *pendingMessageQueue {
	return &pendingMessageQueue{mode: mode}
}

func (q *pendingMessageQueue) enqueue(message AgentMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(q.messages, message)
}

func (q *pendingMessageQueue) hasItems() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages) > 0
}

func (q *pendingMessageQueue) peek() []AgentMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.mode == QueueAll {
		return append([]AgentMessage{}, q.messages...)
	}
	if len(q.messages) > 0 {
		return []AgentMessage{q.messages[0]}
	}
	return nil
}

func (q *pendingMessageQueue) drain() []AgentMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	drained := q.peekLocked()
	q.messages = append([]AgentMessage{}, q.messages[len(drained):]...)
	return drained
}

func (q *pendingMessageQueue) peekLocked() []AgentMessage {
	if q.mode == QueueAll {
		return append([]AgentMessage{}, q.messages...)
	}
	if len(q.messages) > 0 {
		return []AgentMessage{q.messages[0]}
	}
	return nil
}

func (q *pendingMessageQueue) clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = nil
}

type activeRun struct {
	done            chan struct{}
	abortController *abort.Controller
}

// Agent is the stateful wrapper around the low-level agent loop.
type Agent struct {
	mu sync.Mutex
	// emitMu serializes event dispatch (state reduction + listener
	// invocation). Parallel tool goroutines emit concurrently; listeners
	// are promised sequential, subscription-order invocation.
	emitMu sync.Mutex

	// State (guarded).
	tools            []*AgentTool
	messages         []AgentMessage
	model            *ai.Model
	thinkingLevel    string
	isStreaming      bool
	streamingMessage AgentMessage
	hasStreaming     bool
	pendingToolCalls map[string]bool
	errorMessage     *string

	listeners     map[*agentListener]bool
	listenerOrder []*agentListener
	steeringQueue *pendingMessageQueue
	followUpQueue *pendingMessageQueue
	active        *activeRun

	// Configuration (public fields mirroring the TS class).
	ConvertToLlm               func(messages []AgentMessage) []ai.Message
	TransformContext           func(messages []AgentMessage, signal *abort.Signal) []AgentMessage
	StreamFunction             StreamFn
	GetAPIKey                  func(provider string) (string, bool)
	OnPayload                  func(payload any, model *ai.Model) any
	OnResponse                 func(response ai.ProviderResponse, model *ai.Model)
	OnProviderStreamEvent      func(data any, model *ai.Model)
	BeforeToolCall             func(context *BeforeToolCallContext, signal *abort.Signal) (*BeforeToolCallResult, error)
	AfterToolCall              func(context *AfterToolCallContext, signal *abort.Signal) (*AfterToolCallResult, error)
	FinishTurn                 FinishTurn
	PrepareRequest             PrepareRequest
	PrepareNextTurn            func(signal *abort.Signal) (*AgentLoopTurnUpdate, error)
	PrepareNextTurnWithContext func(context *AgentTurnContext, signal *abort.Signal) (*AgentLoopTurnUpdate, error)
	SessionID                  *string
	ThinkingBudgets            map[string]float64
	Transport                  string
	MaxRetryDelayMs            *float64
	ToolExecution              string
}

type agentListener func(event AgentEvent, signal *abort.Signal)

// AgentListener is the subscribe callback type.
type AgentListener = agentListener

// NewAgent constructs an Agent.
func NewAgent(options AgentOptions) *Agent {
	agent := &Agent{
		pendingToolCalls: map[string]bool{},
		listeners:        map[*agentListener]bool{},
		thinkingLevel:    ThinkingOff,
		Transport:        ai.TransportAuto,
		ToolExecution:    ToolExecutionParallel,
	}
	initial := options.InitialState
	if initial != nil {
		agent.tools = append([]*AgentTool{}, initial.Tools...)
		agent.messages = append([]AgentMessage{}, initial.Messages...)
		if initial.Model != nil {
			agent.model = initial.Model
		}
		if initial.ThinkingLevel != nil {
			agent.thinkingLevel = *initial.ThinkingLevel
		}
	}
	if agent.model == nil {
		agent.model = DefaultModel()
	}
	declarations := make([]ai.Tool, 0, len(agent.tools))
	for _, tool := range agent.tools {
		declarations = append(declarations, ai.ToToolDeclaration(tool.ToTool()))
	}
	var systemPrompt *string
	if initial != nil {
		systemPrompt = initial.SystemPrompt
	}
	initialMessage := ai.CreateInitialSystemMessage(systemPrompt, declarations)
	if len(agent.messages) == 0 || agent.messages[0].Role() != "system" {
		if initialMessage != nil {
			agent.messages = append([]AgentMessage{initialMessage}, agent.messages...)
		}
	}

	agent.ConvertToLlm = options.ConvertToLlm
	if agent.ConvertToLlm == nil {
		agent.ConvertToLlm = defaultConvertToLlm
	}
	agent.TransformContext = options.TransformContext
	agent.StreamFunction = options.StreamFn
	agent.GetAPIKey = options.GetAPIKey
	agent.OnPayload = options.OnPayload
	agent.OnResponse = options.OnResponse
	agent.OnProviderStreamEvent = options.OnProviderStreamEvent
	agent.BeforeToolCall = options.BeforeToolCall
	agent.AfterToolCall = options.AfterToolCall
	agent.FinishTurn = options.FinishTurn
	agent.PrepareRequest = options.PrepareRequest
	agent.PrepareNextTurn = options.PrepareNextTurn
	agent.PrepareNextTurnWithContext = options.PrepareNextTurnWithContext
	steeringMode := QueueOneAtATime
	if options.SteeringMode != nil {
		steeringMode = *options.SteeringMode
	}
	followUpMode := QueueOneAtATime
	if options.FollowUpMode != nil {
		followUpMode = *options.FollowUpMode
	}
	agent.steeringQueue = newPendingMessageQueue(steeringMode)
	agent.followUpQueue = newPendingMessageQueue(followUpMode)
	agent.SessionID = options.SessionID
	agent.ThinkingBudgets = options.ThinkingBudgets
	if options.Transport != nil {
		agent.Transport = *options.Transport
	}
	agent.MaxRetryDelayMs = options.MaxRetryDelayMs
	if options.ToolExecution != nil {
		agent.ToolExecution = *options.ToolExecution
	}
	return agent
}

// Subscribe registers a lifecycle listener invoked sequentially in
// subscription order; the returned function unsubscribes.
func (a *Agent) Subscribe(listener AgentListener) (unsubscribe func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := &listener
	a.listeners[key] = true
	a.listenerOrder = append(a.listenerOrder, key)
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.listeners[key] {
			delete(a.listeners, key)
			for i, k := range a.listenerOrder {
				if k == key {
					a.listenerOrder = append(a.listenerOrder[:i], a.listenerOrder[i+1:]...)
					break
				}
			}
		}
	}
}

// SystemPrompt replays the current system prompt from the transcript.
func (a *Agent) SystemPrompt() string {
	a.mu.Lock()
	messages := a.messages
	a.mu.Unlock()
	return ai.GetCurrentSystemPrompt(agentMessagesToAI(messages))
}

// Model returns the active model.
func (a *Agent) Model() *ai.Model {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.model
}

// SetModel sets the active model.
func (a *Agent) SetModel(model *ai.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
}

// ThinkingLevel returns the requested reasoning level.
func (a *Agent) ThinkingLevel() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.thinkingLevel
}

// SetThinkingLevel sets the requested reasoning level.
func (a *Agent) SetThinkingLevel(level string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinkingLevel = level
}

// Tools returns the executable tools (top-level copy).
func (a *Agent) Tools() []*AgentTool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*AgentTool{}, a.tools...)
}

// SetTools replaces the executable tools (copies the top-level array).
func (a *Agent) SetTools(tools []*AgentTool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools = append([]*AgentTool{}, tools...)
}

// Messages returns the transcript (top-level copy).
func (a *Agent) Messages() []AgentMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AgentMessage{}, a.messages...)
}

// SetMessages replaces the transcript (copies the top-level array).
func (a *Agent) SetMessages(messages []AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append([]AgentMessage{}, messages...)
}

// IsStreaming is true while processing a prompt or continuation.
func (a *Agent) IsStreaming() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.isStreaming
}

// StreamingMessage returns the partial assistant message, if any.
func (a *Agent) StreamingMessage() (AgentMessage, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.streamingMessage, a.hasStreaming
}

// PendingToolCalls returns the executing tool call ids.
func (a *Agent) PendingToolCalls() map[string]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]bool, len(a.pendingToolCalls))
	for k, v := range a.pendingToolCalls {
		out[k] = v
	}
	return out
}

// ErrorMessage returns the last run error message, if any.
func (a *Agent) ErrorMessage() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.errorMessage == nil {
		return "", false
	}
	return *a.errorMessage, true
}

// SetSteeringMode controls how queued steering messages drain.
func (a *Agent) SetSteeringMode(mode string) {
	a.steeringQueue.mu.Lock()
	a.steeringQueue.mode = mode
	a.steeringQueue.mu.Unlock()
}

// SteeringMode returns the steering drain mode.
func (a *Agent) SteeringMode() string {
	a.steeringQueue.mu.Lock()
	defer a.steeringQueue.mu.Unlock()
	return a.steeringQueue.mode
}

// SetFollowUpMode controls how queued follow-up messages drain.
func (a *Agent) SetFollowUpMode(mode string) {
	a.followUpQueue.mu.Lock()
	a.followUpQueue.mode = mode
	a.followUpQueue.mu.Unlock()
}

// FollowUpMode returns the follow-up drain mode.
func (a *Agent) FollowUpMode() string {
	a.followUpQueue.mu.Lock()
	defer a.followUpQueue.mu.Unlock()
	return a.followUpQueue.mode
}

// Steer queues a message injected after the current assistant turn.
func (a *Agent) Steer(message AgentMessage) { a.steeringQueue.enqueue(message) }

// FollowUp queues a message to run only after the agent would stop.
func (a *Agent) FollowUp(message AgentMessage) { a.followUpQueue.enqueue(message) }

// ClearSteeringQueue removes all queued steering messages.
func (a *Agent) ClearSteeringQueue() { a.steeringQueue.clear() }

// ClearFollowUpQueue removes all queued follow-up messages.
func (a *Agent) ClearFollowUpQueue() { a.followUpQueue.clear() }

// ClearAllQueues removes every queued message.
func (a *Agent) ClearAllQueues() { a.ClearSteeringQueue(); a.ClearFollowUpQueue() }

// HasQueuedMessages reports whether either queue has items.
func (a *Agent) HasQueuedMessages() bool {
	return a.steeringQueue.hasItems() || a.followUpQueue.hasItems()
}

// PeekQueuedMessages previews the next-turn messages without consuming.
func (a *Agent) PeekQueuedMessages() []AgentMessage {
	if steering := a.steeringQueue.peek(); len(steering) > 0 {
		return steering
	}
	return a.followUpQueue.peek()
}

// Signal returns the active run's abort signal, or nil.
func (a *Agent) Signal() *abort.Signal {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		return nil
	}
	return a.active.abortController.Signal()
}

// Abort aborts the current run, if active.
func (a *Agent) Abort() {
	a.mu.Lock()
	active := a.active
	a.mu.Unlock()
	if active != nil {
		active.abortController.Abort()
	}
}

// WaitForIdle resolves when the current run and its listeners finish.
func (a *Agent) WaitForIdle() {
	a.mu.Lock()
	active := a.active
	a.mu.Unlock()
	if active != nil {
		<-active.done
	}
}

// Reset clears conversation state and queues while retaining the replayed
// prompt/tool baseline.
func (a *Agent) Reset() error {
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		return fmt.Errorf("Agent is already processing. Wait for completion before resetting.")
	}
	baseline := ai.GetCurrentSystemMessage(agentMessagesToAI(a.messages))
	if baseline != nil {
		a.messages = []AgentMessage{baseline}
	} else {
		a.messages = nil
	}
	a.isStreaming = false
	a.streamingMessage, a.hasStreaming = nil, false
	a.pendingToolCalls = map[string]bool{}
	a.errorMessage = nil
	a.mu.Unlock()
	a.ClearFollowUpQueue()
	a.ClearSteeringQueue()
	return nil
}

// Prompt overloads: text+images, one message, or a batch.

// PromptText starts a new prompt from text with optional images.
func (a *Agent) PromptText(input string, images ...ai.ImageContent) error {
	return a.Prompt(a.normalizePromptInput(input, images))
}

// Prompt starts a new prompt from one message or a batch.
func (a *Agent) Prompt(input ...AgentMessage) error {
	messages := input
	if a.hasActiveRun() {
		return fmt.Errorf("Agent is already processing a prompt. Use steer() or followUp() to queue messages, or wait for completion.")
	}
	return a.runPromptMessages(messages, false)
}

// ContinueFrom continues from the current transcript.
func (a *Agent) ContinueFrom() error {
	if a.hasActiveRun() {
		return fmt.Errorf("Agent is already processing. Wait for completion before continuing.")
	}
	a.mu.Lock()
	lastIndex := len(a.messages) - 1
	allSystem := true
	for _, message := range a.messages {
		if message.Role() != "system" {
			allSystem = false
			break
		}
	}
	var lastMessage AgentMessage
	if lastIndex >= 0 {
		lastMessage = a.messages[lastIndex]
	}
	a.mu.Unlock()

	if lastMessage == nil || allSystem {
		return fmt.Errorf("No messages to continue from")
	}

	if lastMessage.Role() == "assistant" {
		queuedSteering := a.steeringQueue.drain()
		if len(queuedSteering) > 0 {
			return a.runPromptMessages(queuedSteering, true)
		}
		queuedFollowUps := a.followUpQueue.drain()
		if len(queuedFollowUps) > 0 {
			return a.runPromptMessages(queuedFollowUps, false)
		}
		return fmt.Errorf("Cannot continue from message role: assistant")
	}

	return a.runContinuation()
}

func (a *Agent) hasActiveRun() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active != nil
}

func (a *Agent) normalizePromptInput(input string, images []ai.ImageContent) AgentMessage {
	blocks := []ai.ContentBlock{ai.TextContent{Text: input}}
	blocks = append(blocks, toImageBlocks(images)...)
	return &ai.UserMessage{Content: ai.BlocksContent(blocks...), TimestampMs: float64(time.Now().UnixMilli())}
}

func toImageBlocks(images []ai.ImageContent) []ai.ContentBlock {
	out := make([]ai.ContentBlock, 0, len(images))
	for _, image := range images {
		out = append(out, image)
	}
	return out
}

func (a *Agent) runPromptMessages(messages []AgentMessage, skipInitialSteeringPoll bool) error {
	return a.runWithLifecycle(func(signal *abort.Signal) error {
		_, err := RunAgentLoop(messages, a.createContextSnapshot(), a.createLoopConfig(skipInitialSteeringPoll), a.processEvents, signal, a.StreamFunction)
		return err
	})
}

func (a *Agent) runContinuation() error {
	return a.runWithLifecycle(func(signal *abort.Signal) error {
		_, err := RunAgentLoopContinue(a.createContextSnapshot(), a.createLoopConfig(false), a.processEvents, signal, a.StreamFunction)
		return err
	})
}

func (a *Agent) createContextSnapshot() *AgentContext {
	a.mu.Lock()
	defer a.mu.Unlock()
	return &AgentContext{
		Messages: append([]AgentMessage{}, a.messages...),
		Tools:    append([]*AgentTool{}, a.tools...),
	}
}

func (a *Agent) createLoopConfig(skipInitialSteeringPoll bool) *AgentLoopConfig {
	a.mu.Lock()
	model := a.model
	thinkingLevel := a.thinkingLevel
	a.mu.Unlock()
	reasoning := normalizeReasoning(&thinkingLevel)
	toolExecution := a.ToolExecution

	config := &AgentLoopConfig{
		Model:            model,
		Reasoning:        reasoning,
		ConvertToLlm:     a.ConvertToLlm,
		TransformContext: a.TransformContext,
		GetAPIKey:        a.GetAPIKey,
		FinishTurn:       a.FinishTurn,
		PrepareRequest:   a.PrepareRequest,
		BeforeToolCall:   a.BeforeToolCall,
		AfterToolCall:    a.AfterToolCall,
		ToolExecution:    &toolExecution,
	}
	config.SessionID = a.SessionID
	config.OnPayload = a.OnPayload
	config.OnResponse = a.OnResponse
	config.OnProviderStreamEvent = a.OnProviderStreamEvent
	transport := a.Transport
	config.Transport = &transport
	config.ThinkingBudgets = a.ThinkingBudgets
	config.MaxRetryDelayMs = a.MaxRetryDelayMs
	if a.PrepareNextTurnWithContext != nil || a.PrepareNextTurn != nil {
		config.PrepareNextTurn = func(context *AgentTurnContext) (*AgentLoopTurnUpdate, error) {
			if a.PrepareNextTurnWithContext != nil {
				return a.PrepareNextTurnWithContext(context, a.Signal())
			}
			return a.PrepareNextTurn(a.Signal())
		}
	}
	skip := skipInitialSteeringPoll
	config.GetSteeringMessages = func() []AgentMessage {
		if skip {
			skip = false
			return nil
		}
		return a.steeringQueue.drain()
	}
	config.GetFollowUpMessages = func() []AgentMessage {
		return a.followUpQueue.drain()
	}
	return config
}

func (a *Agent) runWithLifecycle(executor func(signal *abort.Signal) error) error {
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		return fmt.Errorf("Agent is already processing.")
	}
	abortController := abort.NewController()
	run := &activeRun{done: make(chan struct{}), abortController: abortController}
	a.active = run
	a.isStreaming = true
	a.streamingMessage, a.hasStreaming = nil, false
	a.errorMessage = nil
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.isStreaming = false
		a.streamingMessage, a.hasStreaming = nil, false
		a.pendingToolCalls = map[string]bool{}
		if a.active == run {
			close(run.done)
			a.active = nil
		}
		a.mu.Unlock()
	}()

	if err := executor(abortController.Signal()); err != nil {
		a.handleRunFailure(err, abortController.Signal().Aborted())
	}
	return nil
}

func (a *Agent) handleRunFailure(err error, aborted bool) {
	stopReason := ai.StopError
	if aborted {
		stopReason = ai.StopAborted
	}
	message := err.Error()
	failureMessage := &ai.AssistantMessage{
		Content:      []ai.ContentBlock{ai.TextContent{Text: ""}},
		API:          a.Model().API,
		Provider:     a.Model().Provider,
		Model:        a.Model().ID,
		Usage:        emptyUsage,
		StopReason:   stopReason,
		ErrorMessage: &message,
		TimestampMs:  float64(time.Now().UnixMilli()),
	}
	a.processEvents(&EventMessageStart{Message: failureMessage})
	a.processEvents(&EventMessageEnd{Message: failureMessage})
	a.processEvents(&EventTurnEnd{Message: failureMessage, ToolResults: []*ai.ToolResultMessage{}})
	a.processEvents(&EventAgentEnd{Messages: []AgentMessage{failureMessage}})
}

// processEvents reduces internal state for a loop event, then invokes
// listeners sequentially in subscription order. Listener failures propagate
// (TS awaits would reject the loop). The whole dispatch is serialized:
// parallel tool executions emit from multiple goroutines.
func (a *Agent) processEvents(event AgentEvent) {
	a.emitMu.Lock()
	defer a.emitMu.Unlock()
	a.mu.Lock()
	switch e := event.(type) {
	case *EventMessageStart:
		a.streamingMessage, a.hasStreaming = e.Message, true
	case *EventMessageUpdate:
		a.streamingMessage, a.hasStreaming = e.Message, true
	case *EventMessageEnd:
		a.streamingMessage, a.hasStreaming = nil, false
		a.messages = append(a.messages, e.Message)
	case *EventToolExecutionStart:
		pending := make(map[string]bool, len(a.pendingToolCalls)+1)
		for k, v := range a.pendingToolCalls {
			pending[k] = v
		}
		pending[e.ToolCallID] = true
		a.pendingToolCalls = pending
	case *EventToolExecutionEnd:
		pending := make(map[string]bool, len(a.pendingToolCalls))
		for k, v := range a.pendingToolCalls {
			pending[k] = v
		}
		delete(pending, e.ToolCallID)
		a.pendingToolCalls = pending
	case *EventTurnEnd:
		if e.Message.ErrorMessage != nil {
			message := *e.Message.ErrorMessage
			a.errorMessage = &message
		}
	case *EventAgentEnd:
		a.streamingMessage, a.hasStreaming = nil, false
	}
	signal := (*abort.Signal)(nil)
	if a.active != nil {
		signal = a.active.abortController.Signal()
	}
	listeners := append([]*agentListener{}, a.listenerOrder...)
	a.mu.Unlock()

	if signal == nil {
		panic("Agent listener invoked outside active run")
	}
	for _, listener := range listeners {
		(*listener)(event, signal)
	}
}
