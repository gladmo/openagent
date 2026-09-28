// trajectory/recorder.go: the collector. Attach subscribes one Agent
// listener and wraps the agent's StreamFn; together the two seams observe
// everything the loop does without touching its behavior. Dispose restores
// both registrations.
//
// Event-to-record mapping:
//
//	agent run            Prompt/ContinueFrom → AgentStart … AgentEnd
//	turn/start → turn/start                    (turn counter spans runs)
//	user/system/custom message_end → user/message | system/message | agent/message
//	StreamFn invocation → step/open + request/header + model/input
//	assistant message_end → assistant/message (with timed stream)
//	errored StreamFn outcome → assistant/attempt
//	tool_execution_start/update/end → tool/call | tool/update | tool/result
//	turn/end → step/end (if open) + turn/end
//
// What the loop does NOT surface cannot be recorded: see the module page's
// "Not recorded" section (provider-internal HTTP retries, steer vs followUp
// distinction, tool-termination decisions).
package trajectory

import (
	"fmt"
	"sync"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// InputFidelity controls how much of the normalized model input is captured
// per step.
type InputFidelity string

const (
	// InputFull records every input message (default).
	InputFull InputFidelity = "full"
	// InputSummary records per-message role/block/preview summaries.
	InputSummary InputFidelity = "summary"
	// InputOff records no model input.
	InputOff InputFidelity = "off"
)

// DefaultMaxInlineImageBytes caps base64 image data recorded inline; larger
// images keep mime and byte length with dataOmitted=true.
const DefaultMaxInlineImageBytes = 262144

// RecorderOptions configures Attach. The zero value (plus a Trajectory)
// captures full model input, timed streams, and capped inline images.
type RecorderOptions struct {
	// Trajectory is the log to append to. Required.
	Trajectory *Trajectory
	// Now stamps observation times (Unix ms); defaults to the wall clock.
	Now func() float64
	// ModelInput selects model-input fidelity (empty = InputFull).
	ModelInput InputFidelity
	// DisableStreamDeltas turns off per-chunk stream timing; assistant
	// messages then carry their content only.
	DisableStreamDeltas bool
	// IncludeToolUpdates records partial tool execution updates.
	IncludeToolUpdates bool
	// MaxInlineImageBytes caps inline base64 image data (0 = default).
	MaxInlineImageBytes int
}

// Recorder collects one agent's run into a Trajectory.
type Recorder struct {
	traj *Trajectory
	a    *agent.Agent
	opts RecorderOptions
	now  func() float64

	mu sync.Mutex

	turn int
	step int // 0 = no open step

	stepStart     float64
	stepUsage     ai.Usage
	stepUsageSeen bool
	stepStop      string             // stop reason of the step's assistant message
	pendingTools  map[string]float64 // callId → start time

	invocation *invocationState

	runPromptSeen bool
	retryCounter  int

	unsubscribe     func()
	origStreamFn    agent.StreamFn
	streamFnPatched bool
	disposed        bool
}

// invocationState tracks one StreamFn invocation (one model attempt).
type invocationState struct {
	start   float64
	stream  *StreamAccumulator
	model   *ai.Model
	context *ai.TranscriptContext
	options *ai.SimpleStreamOptions
}

// Attach wires a recorder onto the agent: it appends session/start,
// subscribes to the agent's lifecycle events, and wraps the agent's
// StreamFn. The returned disposer restores both registrations and appends
// session/end. Attach between runs, not while one is streaming.
func Attach(a *agent.Agent, opts RecorderOptions) (*Recorder, func(), error) {
	if opts.Trajectory == nil {
		return nil, nil, fmt.Errorf("trajectory: RecorderOptions.Trajectory is required")
	}
	if opts.ModelInput == "" {
		opts.ModelInput = InputFull
	}
	if opts.MaxInlineImageBytes <= 0 {
		opts.MaxInlineImageBytes = DefaultMaxInlineImageBytes
	}
	now := opts.Now
	if now == nil {
		now = func() float64 { return float64(unixMilliNow()) }
	}
	rec := &Recorder{
		traj:         opts.Trajectory,
		a:            a,
		opts:         opts,
		now:          now,
		pendingTools: map[string]float64{},
	}

	model := a.Model()
	toolNames := make([]any, 0, len(a.Tools()))
	for _, tool := range a.Tools() {
		toolNames = append(toolNames, tool.Name)
	}
	if _, err := rec.append(Record{
		Type: KindSessionStart,
		Data: jsonx.ObjFrom(
			"sessionId", rec.traj.ID(),
			"model", modelJSON(model),
			"thinkingLevel", a.ThinkingLevel(),
			"tools", toolNames,
			"parentTrajectoryId", nilSafeString(rec.traj.ParentID()),
		),
	}); err != nil {
		return nil, nil, err
	}

	rec.unsubscribe = a.Subscribe(rec.onAgentEvent)
	if a.StreamFunction != nil {
		rec.origStreamFn = a.StreamFunction
		rec.streamFnPatched = true
		a.StreamFunction = rec.wrapStream(rec.origStreamFn)
	}
	return rec, rec.dispose, nil
}

func (r *Recorder) dispose() {
	r.mu.Lock()
	if r.disposed {
		r.mu.Unlock()
		return
	}
	r.disposed = true
	r.mu.Unlock()

	if r.unsubscribe != nil {
		r.unsubscribe()
	}
	if r.streamFnPatched {
		r.a.StreamFunction = r.origStreamFn
	}
	r.closeOpenStep()
	usage, _ := SumUsage(r.traj.Snapshot())
	_, _ = r.append(Record{
		Type: KindSessionEnd,
		Data: jsonx.ObjFrom(
			"reason", "disposed",
			"turns", float64(r.turnCount()),
			"usage", usageToJSONValue(usage),
		),
	})
}

func (r *Recorder) turnCount() int {
	turns := 0
	for _, rec := range r.traj.Snapshot() {
		if rec.Type == KindTurnStart {
			turns++
		}
	}
	return turns
}

// append validates and commits through the trajectory; recorder-internal
// failures propagate (a broken observation contract is a defect, not a
// runtime condition).
func (r *Recorder) append(rec Record) (*Record, error) {
	return r.traj.Append(rec)
}

// ---------------------------------------------------------------------------
// Agent event listener
// ---------------------------------------------------------------------------

func (r *Recorder) onAgentEvent(event agent.AgentEvent, _ *abort.Signal) {
	switch e := event.(type) {
	case *agent.EventAgentStart:
		r.runPromptSeen = false
	case *agent.EventTurnStart:
		r.beginTurn()
	case *agent.EventMessageStart:
		if msg := e.Message; msg != nil && msg.Role() == "assistant" {
			r.ensureStep(nil)
		}
	case *agent.EventMessageEnd:
		r.onMessageEnd(e.Message)
	case *agent.EventToolExecutionStart:
		r.onToolStart(e.ToolCallID, e.ToolName, e.Args)
	case *agent.EventToolExecutionUpdate:
		r.onToolUpdate(e.ToolCallID, e.ToolName, e.PartialResult)
	case *agent.EventToolExecutionEnd:
		r.onToolEnd(e.ToolCallID, e.ToolName, e.Result, e.IsError)
	case *agent.EventTurnEnd:
		r.endTurn(e.Message)
	case *agent.EventAgentEnd:
		r.closeOpenStep()
	}
}

func (r *Recorder) beginTurn() {
	r.closeOpenStep()
	r.mu.Lock()
	r.turn++
	turn := r.turn
	r.mu.Unlock()
	_, _ = r.append(Record{Type: KindTurnStart, Turn: turn, Data: jsonx.ObjFrom("turn", float64(turn))})
}

func (r *Recorder) onMessageEnd(message agent.AgentMessage) {
	if message == nil {
		return
	}
	switch m := message.(type) {
	case *ai.AssistantMessage:
		r.onAssistantEnd(m)
		return
	case *ai.ToolResultMessage:
		// tool/result records own tool-result content.
		return
	}
	// Non-assistant, non-tool messages enter at turn scope.
	r.closeOpenStep()
	r.mu.Lock()
	turn := r.turn
	r.mu.Unlock()
	switch msg := message.(type) {
	case *ai.UserMessage:
		source := "injection"
		if !r.runPromptSeen {
			r.runPromptSeen = true
			source = "prompt"
		}
		_, _ = r.append(Record{
			Type: KindUserMessage, Turn: turn,
			Data: jsonx.ObjFrom("source", source, "message", r.messageJSON(msg)),
		})
	case *ai.SystemMessage:
		_, _ = r.append(Record{
			Type: KindSystemMessage, Turn: turn,
			Data: jsonx.ObjFrom("message", r.messageJSON(msg)),
		})
	default:
		role := message.Role()
		payload := jsonx.NewObj()
		if custom, ok := message.(*agent.CustomAgentMessage); ok && custom != nil {
			payload.Set("role", custom.Role_)
			payload.Set("timestamp", custom.TimestampMs)
			if custom.Fields != nil {
				payload.Set("fields", custom.Fields)
			}
		}
		_, _ = r.append(Record{
			Type: KindAgentMessage, Turn: turn, Ignorable: true,
			Data: jsonx.ObjFrom("role", role, "message", payload),
		})
	}
}

func (r *Recorder) onAssistantEnd(m *ai.AssistantMessage) {
	r.mu.Lock()
	turn, step := r.turn, r.step
	inv := r.invocation
	r.mu.Unlock()
	if step == 0 {
		// Defensive: a message_end without a started step cannot happen in
		// the current loop; drop rather than emit an invalid record.
		return
	}
	data := jsonx.NewObj()
	data.Set("message", r.messageJSON(m))
	if inv != nil {
		if !r.opts.DisableStreamDeltas && inv.stream != nil {
			data.Set("stream", inv.stream.Snapshot())
		}
		data.Set("durationMs", r.now()-inv.start)
		if first, ok := inv.stream.FirstTime(); ok {
			data.Set("ttftMs", first-inv.start)
		}
		r.mu.Lock()
		r.invocation = nil
		r.mu.Unlock()
	}
	data.Set("usage", usageToJSONValue(m.Usage))
	if m.StopReason == ai.StopAborted {
		data.Set("interrupted", true)
	}
	data.Set("stopReason", m.StopReason)
	if m.ErrorMessage != nil {
		data.Set("error", jsonx.ObjFrom("code", "error", "message", *m.ErrorMessage))
	}
	_, _ = r.append(Record{Type: KindAssistantMessage, Turn: turn, Step: step, Data: data})

	r.mu.Lock()
	r.stepUsage = addUsage(r.stepUsage, m.Usage)
	r.stepUsageSeen = true
	r.stepStop = m.StopReason
	r.mu.Unlock()
}

func (r *Recorder) onToolStart(callID, name string, args any) {
	r.ensureStep(nil)
	r.mu.Lock()
	turn, step := r.turn, r.step
	r.pendingTools[callID] = r.now()
	r.mu.Unlock()
	argsJSON := "{}"
	if obj, ok := args.(*jsonx.Obj); ok && obj != nil {
		argsJSON = jsonx.Stringify(obj)
	}
	_, _ = r.append(Record{
		Type: KindToolCall, Turn: turn, Step: step,
		Data: jsonx.ObjFrom("callId", callID, "name", name, "arguments", argsJSON),
	})
}

func (r *Recorder) onToolUpdate(callID, name string, partial any) {
	if !r.opts.IncludeToolUpdates {
		return
	}
	r.mu.Lock()
	turn, step := r.turn, r.step
	r.mu.Unlock()
	if step == 0 {
		return
	}
	var payload any = partial
	if !isJSONValue(partial) {
		payload = fmt.Sprint(partial)
	}
	_, _ = r.append(Record{
		Type: KindToolUpdate, Turn: turn, Step: step, Ignorable: true,
		Data: jsonx.ObjFrom("callId", callID, "name", name, "partial", payload),
	})
}

func (r *Recorder) onToolEnd(callID, name string, result *agent.AgentToolResult, isError bool) {
	r.mu.Lock()
	turn, step := r.turn, r.step
	started, hadStart := r.pendingTools[callID]
	delete(r.pendingTools, callID)
	r.mu.Unlock()
	duration := 0.0
	if hadStart {
		duration = r.now() - started
	}
	data := jsonx.NewObj()
	data.Set("callId", callID)
	data.Set("name", name)
	if result != nil {
		message := &ai.ToolResultMessage{
			ToolCallID:  callID,
			ToolName:    name,
			Content:     result.Content,
			Details:     result.Details,
			Usage:       result.Usage,
			IsError:     isError,
			TimestampMs: r.now(),
		}
		data.Set("message", r.messageJSON(message))
		if result.Usage != nil {
			data.Set("usage", usageToJSONValue(*result.Usage))
			r.mu.Lock()
			r.stepUsage = addUsage(r.stepUsage, *result.Usage)
			r.mu.Unlock()
		}
	}
	data.Set("isError", isError)
	data.Set("durationMs", duration)
	_, _ = r.append(Record{Type: KindToolResult, Turn: turn, Step: step, Data: data})
}

func (r *Recorder) endTurn(message *ai.AssistantMessage) {
	r.closeOpenStep()
	r.mu.Lock()
	turn := r.turn
	r.mu.Unlock()
	reason := "completed"
	if message != nil {
		reason = turnEndReason(message.StopReason)
	}
	_, _ = r.append(Record{
		Type: KindTurnEnd, Turn: turn,
		Data: jsonx.ObjFrom("turn", float64(turn), "reason", reason),
	})
}

// ---------------------------------------------------------------------------
// Step lifecycle
// ---------------------------------------------------------------------------

// ensureStep opens the current turn's step if none is open. invocation may
// be nil when the step opens from a message event rather than a model call.
func (r *Recorder) ensureStep(inv *invocationState) {
	r.mu.Lock()
	if r.step != 0 {
		if inv != nil && r.invocation == nil {
			r.invocation = inv
		}
		r.mu.Unlock()
		return
	}
	if r.turn == 0 {
		// Defensive: model activity before any turn/start cannot happen in
		// the current loop; do not emit an invalid step-scoped record.
		r.mu.Unlock()
		return
	}
	r.step = 1
	turn, step := r.turn, r.step
	r.stepStart = r.now()
	r.stepUsage = ai.Usage{}
	r.stepUsageSeen = false
	r.stepStop = ""
	r.invocation = inv
	r.mu.Unlock()

	data := jsonx.NewObj()
	if inv != nil {
		data.Set("model", modelJSON(inv.model))
		level := "off"
		if inv.options != nil && inv.options.Reasoning != nil {
			level = *inv.options.Reasoning
		}
		data.Set("thinkingLevel", level)
	}
	_, _ = r.append(Record{Type: KindStepStart, Turn: turn, Step: step, Data: data})
}

func (r *Recorder) closeOpenStep() {
	r.mu.Lock()
	if r.step == 0 {
		r.mu.Unlock()
		return
	}
	turn, step := r.turn, r.step
	duration := r.now() - r.stepStart
	usage, seen := r.stepUsage, r.stepUsageSeen
	stop := r.stepStop
	r.step = 0
	r.invocation = nil
	r.mu.Unlock()

	data := jsonx.ObjFrom(
		"durationMs", duration,
		"stopReason", stop,
	)
	if seen {
		data.Set("usage", usageToJSONValue(usage))
	}
	_, _ = r.append(Record{Type: KindStepEnd, Turn: turn, Step: step, Data: data})
}

// ---------------------------------------------------------------------------
// StreamFn wrapper
// ---------------------------------------------------------------------------

// wrapStream observes the model call: the normalized input (request/header
// + model/input), per-chunk timings, and the attempt outcome. The wrapped
// stream preserves the original's events exactly.
func (r *Recorder) wrapStream(original agent.StreamFn) agent.StreamFn {
	return func(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		inv := r.beginInvocation(model, context, options)
		out := ai.NewAssistantMessageEventStream()
		orig := original(model, context, options)
		go func() {
			var outcome *ai.AssistantMessage
			var outcomeTerminal bool
			for {
				event, ok := orig.Next()
				if !ok {
					break
				}
				if inv.stream != nil {
					inv.stream.Push(r.now(), event)
				}
				switch e := event.(type) {
				case *ai.EventDone:
					outcome, outcomeTerminal = e.Message, true
				case *ai.EventError:
					outcome, outcomeTerminal = e.Error, true
				}
				out.Push(event)
			}
			if !outcomeTerminal && outcome == nil {
				out.End()
			}
			r.endInvocation(inv, outcome)
		}()
		return out
	}
}

func (r *Recorder) beginInvocation(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *invocationState {
	inv := &invocationState{
		start:   r.now(),
		stream:  &StreamAccumulator{},
		model:   model,
		context: context,
		options: options,
	}
	r.ensureStep(inv)
	r.mu.Lock()
	turn, step := r.turn, r.step
	r.mu.Unlock()
	if step == 0 {
		return inv
	}

	// request/header: the logical request head. Tools come from the agent's
	// current toolset — the loop declares them through the system message,
	// not the stream options.
	tools := make([]any, 0, len(r.a.Tools()))
	for _, tool := range r.a.Tools() {
		entry := jsonx.NewObj()
		entry.Set("name", tool.Name)
		if tool.Parameters != nil {
			entry.Set("parameters", tool.Parameters.JSON())
		}
		tools = append(tools, entry)
	}
	systemLen := 0.0
	messageCount := 0
	if context != nil {
		messageCount = len(context.Messages)
		for _, message := range context.Messages {
			if sys, ok := message.(*ai.SystemMessage); ok {
				systemLen = float64(len(ai.GetSystemMessageText(sys)))
			}
		}
	}
	thinkingLevel := "off"
	if options != nil && options.Reasoning != nil {
		thinkingLevel = *options.Reasoning
	}
	_, _ = r.append(Record{
		Type: KindRequestHeader, Turn: turn, Step: step,
		Data: jsonx.ObjFrom(
			"model", modelJSON(model),
			"thinkingLevel", thinkingLevel,
			"tools", tools,
			"messageCount", float64(messageCount),
			"systemPromptLength", systemLen,
		),
	})

	switch r.opts.ModelInput {
	case InputOff:
	case InputSummary:
		summaries := make([]any, 0, messageCount)
		if context != nil {
			for _, message := range context.Messages {
				summaries = append(summaries, summarizeMessage(message))
			}
		}
		_, _ = r.append(Record{
			Type: KindModelInput, Turn: turn, Step: step,
			Data: jsonx.ObjFrom("fidelity", string(InputSummary), "messages", summaries),
		})
	default:
		messages := make([]any, 0, messageCount)
		if context != nil {
			for _, message := range context.Messages {
				messages = append(messages, r.messageJSON(message))
			}
		}
		_, _ = r.append(Record{
			Type: KindModelInput, Turn: turn, Step: step,
			Data: jsonx.ObjFrom("fidelity", string(InputFull), "messages", messages),
		})
	}
	return inv
}

func (r *Recorder) endInvocation(inv *invocationState, outcome *ai.AssistantMessage) {
	if outcome == nil {
		return
	}
	if outcome.StopReason != ai.StopError {
		// Successful and aborted outcomes surface through the loop's
		// message events; only a failed attempt needs its own record.
		return
	}
	r.mu.Lock()
	turn, step := r.turn, r.step
	isCurrent := r.invocation == inv
	if isCurrent {
		r.invocation = nil
	}
	r.mu.Unlock()
	if step == 0 {
		// The step already closed (for example an abort raced the failure);
		// the attempt has no live step to attach to.
		return
	}
	data := jsonx.NewObj()
	if !r.opts.DisableStreamDeltas && inv.stream != nil {
		data.Set("stream", inv.stream.Snapshot())
	}
	data.Set("usage", usageToJSONValue(outcome.Usage))
	errorMessage := ""
	if outcome.ErrorMessage != nil {
		errorMessage = *outcome.ErrorMessage
	}
	data.Set("error", jsonx.ObjFrom("code", "error", "message", errorMessage))
	data.Set("durationMs", r.now()-inv.start)
	_, _ = r.append(Record{Type: KindAssistantAttempt, Turn: turn, Step: step, Data: data})
}

// ---------------------------------------------------------------------------
// Retry capture
// ---------------------------------------------------------------------------

// RetryCallbacks returns an ai.RetryCallbacks implementation that records
// llm/retry and llm/retry-started. Wire it where the caller applies
// ai.RetryAssistantCall around the agent's stream function — the loop does
// not wire retries itself, so without this hand-off retries are invisible.
func (r *Recorder) RetryCallbacks() ai.RetryCallbacks {
	return &recorderRetryCallbacks{r: r}
}

type recorderRetryCallbacks struct{ r *Recorder }

func (c *recorderRetryCallbacks) OnRetryScheduled(attempt int, maxAttempts int, delayMs float64, errorMessage string) {
	r := c.r
	r.mu.Lock()
	r.retryCounter++
	retryID := fmt.Sprintf("%s:retry-%d", r.traj.ID(), r.retryCounter)
	turn, step := r.turn, r.step
	r.mu.Unlock()
	data := jsonx.ObjFrom(
		"retryId", retryID,
		"retry", float64(attempt),
		"maxRetries", float64(maxAttempts),
		"delayMs", delayMs,
		"failure", jsonx.ObjFrom("code", "error", "message", errorMessage),
	)
	if step > 0 {
		data.Set("step", float64(step))
	}
	_, _ = r.append(Record{Type: KindLlmRetry, Turn: turn, Step: step, Data: data})
}

func (c *recorderRetryCallbacks) OnRetryAttemptStart() {
	r := c.r
	r.mu.Lock()
	turn, step := r.turn, r.step
	last := r.retryCounter
	r.mu.Unlock()
	if last == 0 {
		return
	}
	retryID := fmt.Sprintf("%s:retry-%d", r.traj.ID(), last)
	data := jsonx.ObjFrom("retryId", retryID)
	if step > 0 {
		data.Set("step", float64(step))
	}
	_, _ = r.append(Record{Type: KindLlmRetryStarted, Turn: turn, Step: step, Data: data})
}

func (c *recorderRetryCallbacks) OnRetryFinished(bool, int, string, bool) {}

// ---------------------------------------------------------------------------
// Payload helpers
// ---------------------------------------------------------------------------

// messageJSON serializes one agent message with the ai codec; custom roles
// fall back to their fields; image data over the cap is omitted in place.
func (r *Recorder) messageJSON(message ai.Message) *jsonx.Obj {
	var obj *jsonx.Obj
	switch m := message.(type) {
	case *ai.SystemMessage, *ai.UserMessage, *ai.AssistantMessage, *ai.ToolResultMessage:
		obj = ai.MessageToJSON(m)
	default:
		return nil
	}
	return capImageJSON(obj, r.opts.MaxInlineImageBytes)
}

func modelJSON(model *ai.Model) *jsonx.Obj {
	if model == nil {
		return jsonx.ObjFrom("provider", "", "id", "", "api", "")
	}
	return jsonx.ObjFrom("provider", model.Provider, "id", model.ID, "api", model.API)
}

func turnEndReason(stopReason string) string {
	switch stopReason {
	case ai.StopAborted:
		return "aborted"
	case ai.StopError:
		return "error"
	case ai.StopLength:
		return "length"
	case ai.StopDeferred:
		return "deferred"
	default:
		return "completed"
	}
}

// summarizeMessage builds the InputSummary form of one input message.
func summarizeMessage(message ai.Message) any {
	o := jsonx.NewObj()
	o.Set("role", message.Role())
	preview := messageText(message)
	if len(preview) > 200 {
		preview = preview[:200]
	}
	o.Set("preview", preview)
	blocks := 0
	switch m := message.(type) {
	case *ai.SystemMessage:
		if !m.Content.IsText {
			blocks = len(m.Content.Blocks)
		}
	case *ai.UserMessage:
		if !m.Content.IsText {
			blocks = len(m.Content.Blocks)
		}
	case *ai.AssistantMessage:
		blocks = len(m.Content)
	case *ai.ToolResultMessage:
		blocks = len(m.Content)
	}
	o.Set("blocks", float64(blocks))
	return o
}

// messageText extracts the text of one message (single-block preview use).
func messageText(message ai.Message) string {
	switch m := message.(type) {
	case *ai.SystemMessage:
		return ai.ContentText(m.Content, "")
	case *ai.UserMessage:
		return ai.ContentText(m.Content, "")
	case *ai.AssistantMessage:
		var out string
		for _, block := range m.Content {
			if text, ok := block.(ai.TextContent); ok {
				out += text.Text
			}
		}
		return out
	case *ai.ToolResultMessage:
		var out string
		for _, block := range m.Content {
			if text, ok := block.(ai.TextContent); ok {
				out += text.Text
			}
		}
		return out
	}
	return ""
}

// capImageJSON walks a serialized value and truncates over-cap base64 image
// data in place: {type:"image", data, mimeType} becomes
// {type:"image", data:"", mimeType, dataBytes, dataOmitted:true}.
func capImageJSON(v any, maxBytes int) *jsonx.Obj {
	obj, ok := v.(*jsonx.Obj)
	if !ok {
		return jsonx.NewObj()
	}
	walkAndCap(obj, maxBytes)
	return obj
}

func walkAndCap(v any, maxBytes int) {
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			walkAndCap(item, maxBytes)
		}
	case *jsonx.Obj:
		if typeValue, ok := t.Get("type"); ok && typeValue == "image" {
			if dataValue, ok := t.Get("data"); ok {
				if data, ok := dataValue.(string); ok && len(data) > maxBytes {
					t.Set("dataBytes", float64(len(data)))
					t.Set("dataOmitted", true)
					t.Set("data", "")
				}
			}
			return
		}
		for _, entry := range t.Entries() {
			walkAndCap(entry[1], maxBytes)
		}
	}
}

// usageToJSONValue renders an ai.Usage as a JSON object.
func usageToJSONValue(u ai.Usage) any {
	o := jsonx.NewObj()
	o.Set("input", u.Input)
	o.Set("output", u.Output)
	o.Set("cacheRead", u.CacheRead)
	o.Set("cacheWrite", u.CacheWrite)
	if u.CacheWrite1h != nil {
		o.Set("cacheWrite1h", *u.CacheWrite1h)
	}
	if u.Reasoning != nil {
		o.Set("reasoning", *u.Reasoning)
	}
	o.Set("totalTokens", u.TotalTokens)
	cost := jsonx.NewObj()
	cost.Set("total", u.Cost.Total)
	o.Set("cost", cost)
	return o
}

func addUsage(a, b ai.Usage) ai.Usage {
	a.Input += b.Input
	a.Output += b.Output
	a.CacheRead += b.CacheRead
	a.CacheWrite += b.CacheWrite
	if b.CacheWrite1h != nil {
		v := *b.CacheWrite1h
		if a.CacheWrite1h != nil {
			v += *a.CacheWrite1h
		}
		a.CacheWrite1h = &v
	}
	if b.Reasoning != nil {
		v := *b.Reasoning
		if a.Reasoning != nil {
			v += *a.Reasoning
		}
		a.Reasoning = &v
	}
	a.TotalTokens += b.TotalTokens
	a.Cost.Input += b.Cost.Input
	a.Cost.Output += b.Cost.Output
	a.Cost.CacheRead += b.Cost.CacheRead
	a.Cost.CacheWrite += b.Cost.CacheWrite
	a.Cost.Total += b.Cost.Total
	return a
}

func nilSafeString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func isJSONValue(v any) bool {
	switch v.(type) {
	case nil, bool, float64, string, []any, *jsonx.Obj:
		return true
	}
	return false
}
