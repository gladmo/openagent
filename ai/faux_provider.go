package ai

// faux_provider.go ports createFauxCore/fauxProvider from providers/faux.ts.

import (
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/jsonx"
)

const (
	fauxDefaultMinTokenSize = 3
	fauxDefaultMaxTokenSize = 5
)

// FauxProviderState mirrors the TS interface.
type FauxProviderState struct {
	mu                 sync.Mutex
	callCount          int
	deferredFetchCount int
	cancelledDeferred  []*DeferredHandle
}

// CallCount returns the number of stream calls.
func (s *FauxProviderState) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.callCount
}

// DeferredFetchCount returns the number of deferred fetch calls.
func (s *FauxProviderState) DeferredFetchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deferredFetchCount
}

// CancelledDeferred returns the cancelled handles.
func (s *FauxProviderState) CancelledDeferred() []*DeferredHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*DeferredHandle{}, s.cancelledDeferred...)
}

// FauxResponseFactory mirrors the TS factory step.
type FauxResponseFactory func(context *TranscriptContext, options *SimpleStreamOptions, state *FauxProviderState, model *Model) *AssistantMessage

// FauxResponseStep is either a message or a factory.
type FauxResponseStep struct {
	Message *AssistantMessage
	Factory FauxResponseFactory
}

// FauxStep wraps a message.
func FauxStep(message *AssistantMessage) FauxResponseStep { return FauxResponseStep{Message: message} }

// FauxStepFn wraps a factory.
func FauxStepFn(factory FauxResponseFactory) FauxResponseStep {
	return FauxResponseStep{Factory: factory}
}

// RegisterFauxProviderOptions mirrors the TS options.
type RegisterFauxProviderOptions struct {
	API             string
	Provider        string
	Models          []FauxModelDefinition
	Deferred        *FauxDeferredOptions
	TokensPerSecond float64
	TokenSizeMin    int
	TokenSizeMax    int
}

// FauxDeferredOptions mirrors options.deferred.
type FauxDeferredOptions struct {
	PendingFetches int
	PollAfterMs    *float64
}

type fauxDeferredEntry struct {
	handle         *DeferredHandle
	step           FauxResponseStep
	context        *TranscriptContext
	options        *SimpleStreamOptions
	model          *Model
	pendingFetches int
	cancelled      bool
	final          *AssistantMessage
}

// FauxCore mirrors createFauxCore's return.
type FauxCore struct {
	api       string
	provider  string
	minTokens int
	maxTokens int

	mu                sync.Mutex
	pendingResponses  []FauxResponseStep
	deferredResponses map[string]*fauxDeferredEntry
	promptCache       map[string]string

	State           *FauxProviderState
	tokensPerSecond float64
	Models          []*Model

	deferredPendingFetches *int
	deferredPollAfterMs    *float64
}

// API returns the faux API id.
func (c *FauxCore) API() string { return c.api }

// ProviderID returns the faux provider id.
func (c *FauxCore) ProviderID() string { return c.provider }

func fauxEstimateTokens(text string) float64 {
	return ceilDivFloat(float64(len(text)), 4)
}

func ceilDivFloat(v, unit float64) float64 {
	if unit == 0 {
		return 0
	}
	n := v / unit
	if n != float64(int64(n)) {
		return float64(int64(n)) + 1
	}
	return n
}

func fauxRandomID(prefix string) string {
	return prefix + ":" + jsonx.FormatNumber(float64(time.Now().UnixMilli())) + ":" + randomHex(6)
}

func randomHex(n int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	for i := range b {
		b[i] = digits[rand.Intn(36)]
	}
	return string(b)
}

func fauxContentToText(content Content) string {
	if content.IsText {
		return content.Text
	}
	parts := make([]string, 0, len(content.Blocks))
	for _, block := range content.Blocks {
		switch b := block.(type) {
		case TextContent:
			parts = append(parts, b.Text)
		case ImageContent:
			parts = append(parts, "[image:"+b.MimeType+":"+jsonx.FormatNumber(float64(len(b.Data)))+"]")
		}
	}
	return strings.Join(parts, "\n")
}

func fauxAssistantContentToText(content []ContentBlock) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		switch b := block.(type) {
		case TextContent:
			parts = append(parts, b.Text)
		case ThinkingContent:
			parts = append(parts, b.Thinking)
		case *ToolCall:
			parts = append(parts, b.Name+":"+jsonStringify(b.Arguments))
		}
	}
	return strings.Join(parts, "\n")
}

func fauxMessageToText(message Message) string {
	switch m := message.(type) {
	case *SystemMessage:
		parts := []string{GetSystemMessageText(m)}
		for _, tool := range m.ToolsRemoved {
			parts = append(parts, "tool-:"+jsonStringify(jsonx.ObjFrom("name", tool.Name)))
		}
		for _, tool := range m.ToolsAdded {
			parts = append(parts, "tool+:"+jsonStringify(ToolToJSON(tool)))
		}
		filtered := parts[:0]
		for _, part := range parts {
			if len(part) > 0 {
				filtered = append(filtered, part)
			}
		}
		return strings.Join(filtered, "\n")
	case *UserMessage:
		return fauxContentToText(m.Content)
	case *AssistantMessage:
		return fauxAssistantContentToText(m.Content)
	case *ToolResultMessage:
		parts := []string{m.ToolName}
		for _, block := range m.Content {
			parts = append(parts, fauxContentToText(BlocksContent(block)))
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func fauxSerializeContext(context *TranscriptContext) string {
	parts := make([]string, 0, len(context.Messages))
	for _, message := range context.Messages {
		parts = append(parts, message.Role()+":"+fauxMessageToText(message))
	}
	return strings.Join(parts, "\n\n")
}

func fauxCommonPrefixLength(a, b string) int {
	length := len(a)
	if len(b) < length {
		length = len(b)
	}
	index := 0
	for index < length && a[index] == b[index] {
		index++
	}
	return index
}

func fauxWithUsageEstimate(message *AssistantMessage, context *TranscriptContext, options *SimpleStreamOptions, promptCache map[string]string, mu *sync.Mutex) *AssistantMessage {
	promptText := fauxSerializeContext(context)
	promptTokens := fauxEstimateTokens(promptText)
	outputTokens := fauxEstimateTokens(fauxAssistantContentToText(message.Content))
	input := promptTokens
	cacheRead := 0.0
	cacheWrite := 0.0

	sessionID := ""
	if options != nil && options.SessionID != nil {
		sessionID = *options.SessionID
	}
	cacheRetention := ""
	if options != nil && options.CacheRetention != nil {
		cacheRetention = *options.CacheRetention
	}
	if sessionID != "" && cacheRetention != CacheRetentionNone {
		mu.Lock()
		previousPrompt, exists := promptCache[sessionID]
		promptCache[sessionID] = promptText
		mu.Unlock()
		if exists {
			cachedChars := fauxCommonPrefixLength(previousPrompt, promptText)
			cacheRead = fauxEstimateTokens(previousPrompt[:cachedChars])
			cacheWrite = fauxEstimateTokens(promptText[cachedChars:])
			input = promptTokens - cacheRead
			if input < 0 {
				input = 0
			}
		} else {
			cacheWrite = promptTokens
		}
	}

	clone := *message
	clone.Usage = Usage{
		Input:       input,
		Output:      outputTokens,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: input + outputTokens + cacheRead + cacheWrite,
	}
	return &clone
}

func fauxSplitStringByTokenSize(text string, minTokenSize, maxTokenSize int) []string {
	var chunks []string
	index := 0
	for index < len(text) {
		tokenSize := minTokenSize + rand.Intn(maxTokenSize-minTokenSize+1)
		charSize := tokenSize * 4
		if charSize < 1 {
			charSize = 1
		}
		end := index + charSize
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, text[index:end])
		index = end
	}
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	return chunks
}

func fauxCloneMessage(message *AssistantMessage, api, provider, modelID string) *AssistantMessage {
	decoded, err := MessageFromJSON(MessageToJSON(message))
	if err != nil {
		clone := *message
		clone.API, clone.Provider, clone.Model = api, provider, modelID
		return &clone
	}
	clone := decoded.(*AssistantMessage)
	clone.API, clone.Provider, clone.Model = api, provider, modelID
	if clone.TimestampMs == 0 {
		clone.TimestampMs = nowMs()
	}
	if clone.StopReason == "" {
		clone.StopReason = StopStop
	}
	return clone
}

func fauxCreateDeferredMessage(model *Model, handle *DeferredHandle) *AssistantMessage {
	return &AssistantMessage{
		Content:     []ContentBlock{},
		API:         model.API,
		Provider:    model.Provider,
		Model:       model.ID,
		Usage:       Usage{},
		StopReason:  StopDeferred,
		Deferred:    handle,
		TimestampMs: nowMs(),
	}
}

func fauxCreateErrorMessage(err error, api, provider, modelID string) *AssistantMessage {
	message := err.Error()
	return &AssistantMessage{
		Content:      []ContentBlock{},
		API:          api,
		Provider:     provider,
		Model:        modelID,
		Usage:        Usage{},
		StopReason:   StopError,
		ErrorMessage: &message,
		TimestampMs:  nowMs(),
	}
}

func fauxCreateAbortedMessage(partial *AssistantMessage) *AssistantMessage {
	aborted := *partial
	aborted.StopReason = StopAborted
	message := "Request was aborted"
	aborted.ErrorMessage = &message
	aborted.TimestampMs = nowMs()
	return &aborted
}

// shallowCopyPartial mirrors the TS `{...partial}` shallow copy.
func shallowCopyPartial(partial *AssistantMessage) *AssistantMessage {
	clone := *partial
	clone.Content = append([]ContentBlock{}, partial.Content...)
	return &clone
}

// streamWithDeltas pushes the full event protocol for a message.
func (c *FauxCore) streamWithDeltas(
	stream *AssistantMessageEventStream,
	message *AssistantMessage,
	signal *abort.Signal,
) error {
	partial := shallowCopyPartial(message)
	partial.Content = []ContentBlock{}
	partial.StopReason = StopPending

	failAborted := func() {
		aborted := fauxCreateAbortedMessage(partial)
		stream.Push(&EventError{Reason: StopAborted, Error: aborted})
		stream.End(aborted)
	}

	if signal.Aborted() {
		failAborted()
		return nil
	}
	stream.Push(&EventStart{Partial: shallowCopyPartial(partial)})

	for index := 0; index < len(message.Content); index++ {
		if signal.Aborted() {
			failAborted()
			return nil
		}
		block := message.Content[index]
		switch b := block.(type) {
		case ThinkingContent:
			partial.Content = append(partial.Content, ThinkingContent{})
			stream.Push(&EventThinkingStart{ContentIndex: index, Partial: shallowCopyPartial(partial)})
			for _, chunk := range fauxSplitStringByTokenSize(b.Thinking, c.minTokens, c.maxTokens) {
				if signal.Aborted() {
					failAborted()
					return nil
				}
				current := partial.Content[index].(ThinkingContent)
				current.Thinking += chunk
				partial.Content[index] = current
				stream.Push(&EventThinkingDelta{ContentIndex: index, Delta: chunk, Partial: shallowCopyPartial(partial)})
			}
			stream.Push(&EventThinkingEnd{ContentIndex: index, Content: b.Thinking, Partial: shallowCopyPartial(partial)})
		case TextContent:
			partial.Content = append(partial.Content, TextContent{})
			stream.Push(&EventTextStart{ContentIndex: index, Partial: shallowCopyPartial(partial)})
			for _, chunk := range fauxSplitStringByTokenSize(b.Text, c.minTokens, c.maxTokens) {
				if signal.Aborted() {
					failAborted()
					return nil
				}
				current := partial.Content[index].(TextContent)
				current.Text += chunk
				partial.Content[index] = current
				stream.Push(&EventTextDelta{ContentIndex: index, Delta: chunk, Partial: shallowCopyPartial(partial)})
			}
			stream.Push(&EventTextEnd{ContentIndex: index, Content: b.Text, Partial: shallowCopyPartial(partial)})
		case *ToolCall:
			partial.Content = append(partial.Content, &ToolCall{ID: b.ID, Name: b.Name, Arguments: jsonx.NewObj()})
			stream.Push(&EventToolCallStart{ContentIndex: index, Partial: shallowCopyPartial(partial)})
			for _, chunk := range fauxSplitStringByTokenSize(jsonStringify(b.Arguments), c.minTokens, c.maxTokens) {
				if signal.Aborted() {
					failAborted()
					return nil
				}
				stream.Push(&EventToolCallDelta{ContentIndex: index, Delta: chunk, Partial: shallowCopyPartial(partial)})
			}
			if call, ok := partial.Content[index].(*ToolCall); ok {
				call.Arguments = b.Arguments
			}
			stream.Push(&EventToolCallEnd{ContentIndex: index, ToolCall: b, Partial: shallowCopyPartial(partial)})
		}
	}

	if message.StopReason == StopPending {
		return errFauxNoStopReason
	}
	if message.StopReason == StopError || message.StopReason == StopAborted {
		stream.Push(&EventError{Reason: message.StopReason, Error: message})
		stream.End(message)
		return nil
	}
	stream.Push(&EventDone{Reason: message.StopReason, Message: message})
	stream.End(message)
	return nil
}

var errFauxNoStopReason = &fauxError{"Faux response ended without a stop reason"}

type fauxError struct{ msg string }

func (e *fauxError) Error() string { return e.msg }

// CreateFauxCore mirrors createFauxCore.
func CreateFauxCore(options RegisterFauxProviderOptions) *FauxCore {
	api := options.API
	if api == "" {
		api = fauxRandomID(FauxDefaultAPI)
	}
	provider := options.Provider
	if provider == "" {
		provider = FauxDefaultProvider
	}
	minTokens := options.TokenSizeMin
	if minTokens == 0 {
		minTokens = fauxDefaultMinTokenSize
	}
	maxTokens := options.TokenSizeMax
	if maxTokens == 0 {
		maxTokens = fauxDefaultMaxTokenSize
	}
	if minTokens > maxTokens {
		minTokens = maxTokens
	}
	if maxTokens < minTokens {
		maxTokens = minTokens
	}

	definitions := options.Models
	if len(definitions) == 0 {
		definitions = []FauxModelDefinition{{
			ID:            FauxDefaultModelID,
			Name:          FauxDefaultModelName,
			Input:         []string{"text", "image"},
			ContextWindow: FauxDefaultContextWindow,
			MaxTokens:     FauxDefaultMaxTokens,
		}}
	}
	models := make([]*Model, 0, len(definitions))
	for _, def := range definitions {
		models = append(models, FauxModelWithIdentity(def, api, provider))
	}
	core := &FauxCore{
		api:               api,
		provider:          provider,
		minTokens:         minTokens,
		maxTokens:         maxTokens,
		deferredResponses: map[string]*fauxDeferredEntry{},
		promptCache:       map[string]string{},
		State:             &FauxProviderState{},
		tokensPerSecond:   options.TokensPerSecond,
		Models:            models,
	}
	return core
}

// GetModel returns the first model or one by id.
func (c *FauxCore) GetModel(modelID ...string) *Model {
	if len(modelID) == 0 || modelID[0] == "" {
		return c.Models[0]
	}
	for _, model := range c.Models {
		if model.ID == modelID[0] {
			return model
		}
	}
	return nil
}

// SetResponses replaces the queued responses.
func (c *FauxCore) SetResponses(responses []FauxResponseStep) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingResponses = append([]FauxResponseStep{}, responses...)
}

// AppendResponses appends to the queue.
func (c *FauxCore) AppendResponses(responses []FauxResponseStep) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingResponses = append(c.pendingResponses, responses...)
}

// GetPendingResponseCount returns the queue length.
func (c *FauxCore) GetPendingResponseCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pendingResponses)
}

func (c *FauxCore) popResponse() (FauxResponseStep, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pendingResponses) == 0 {
		return FauxResponseStep{}, false
	}
	step := c.pendingResponses[0]
	c.pendingResponses = c.pendingResponses[1:]
	return step, true
}

func (c *FauxCore) resolveResponse(step FauxResponseStep, context *TranscriptContext, options *SimpleStreamOptions, model *Model) *AssistantMessage {
	var resolved *AssistantMessage
	if step.Factory != nil {
		resolved = step.Factory(context, options, c.State, model)
	} else {
		resolved = step.Message
	}
	return fauxWithUsageEstimate(fauxCloneMessage(resolved, c.api, c.provider, model.ID), context, options, c.promptCache, &c.mu)
}

// Stream mirrors the faux stream function: pops the next step
// synchronously, then fills the returned stream asynchronously.
func (c *FauxCore) Stream(requestModel *Model, context *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
	outer := NewAssistantMessageEventStream()
	step, hasStep := c.popResponse()
	c.State.mu.Lock()
	c.State.callCount++
	c.State.mu.Unlock()

	var signal *abort.Signal
	if options != nil {
		signal = options.Signal
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				message := fauxCreateErrorMessage(errorFromPanic(r), c.api, c.provider, requestModel.ID)
				outer.Push(&EventError{Reason: StopError, Error: message})
				outer.End(message)
			}
		}()
		if options != nil && options.OnResponse != nil {
			options.OnResponse(ProviderResponse{Status: 200, Headers: map[string]string{}}, requestModel)
		}
		if !hasStep {
			message := fauxCreateErrorMessage(errorFromString("No more faux responses queued"), c.api, c.provider, requestModel.ID)
			message = fauxWithUsageEstimate(message, context, options, c.promptCache, &c.mu)
			outer.Push(&EventError{Reason: StopError, Error: message})
			outer.End(message)
			return
		}
		if options != nil && fauxDeferredTruthy(options.Deferred) {
			pollAfterMs := (*float64)(nil)
			if c.deferredPollAfterMs != nil {
				pollAfterMs = c.deferredPollAfterMs
			}
			handle := &DeferredHandle{
				Provider:    requestModel.Provider,
				ModelID:     requestModel.ID,
				API:         requestModel.API,
				ID:          fauxRandomID("deferred"),
				PollAfterMs: pollAfterMs,
			}
			pending := 0
			if c.deferredPendingFetches != nil {
				pending = *c.deferredPendingFetches
			}
			c.mu.Lock()
			c.deferredResponses[handle.ID] = &fauxDeferredEntry{
				handle: handle, step: step, context: context, options: options,
				model: requestModel, pendingFetches: pending,
			}
			c.mu.Unlock()
			c.pushDeltasOrError(outer, fauxCreateDeferredMessage(requestModel, handle), signal, requestModel)
			return
		}
		message := c.resolveResponse(step, context, options, requestModel)
		c.pushDeltasOrError(outer, message, signal, requestModel)
	}()
	return outer
}

func errorFromString(msg string) error { return &fauxError{msg} }

func errorFromPanic(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return errorFromString(stringifyPanicValue(r))
}

func stringifyPanicValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case error:
		return t.Error()
	default:
		return jsonx.Stringify(v)
	}
}

// FetchDeferred mirrors the faux deferred fetch.
func (c *FauxCore) FetchDeferred(requestModel *Model, handle *DeferredHandle, fetchOptions *DeferredFetchOptions) *AssistantMessageEventStream {
	outer := NewAssistantMessageEventStream()
	c.State.mu.Lock()
	c.State.deferredFetchCount++
	c.State.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				message := fauxCreateErrorMessage(errorFromPanic(r), c.api, c.provider, requestModel.ID)
				outer.Push(&EventError{Reason: StopError, Error: message})
				outer.End(message)
			}
		}()
		var signal *abort.Signal
		if fetchOptions != nil {
			signal = fetchOptions.Signal
			if fetchOptions.OnResponse != nil {
				fetchOptions.OnResponse(ProviderResponse{Status: 200, Headers: map[string]string{}}, requestModel)
			}
		}
		c.mu.Lock()
		entry, exists := c.deferredResponses[handle.ID]
		c.mu.Unlock()
		if !exists || entry.handle.Provider != handle.Provider || entry.handle.ModelID != handle.ModelID || entry.handle.API != handle.API {
			panic(errorFromString("Unknown faux deferred response: " + handle.ID))
		}
		if entry.cancelled {
			panic(errorFromString("Faux deferred response was cancelled: " + handle.ID))
		}
		if entry.pendingFetches > 0 {
			entry.pendingFetches--
			c.pushDeltasOrError(outer, fauxCreateDeferredMessage(requestModel, entry.handle), signal, requestModel)
			return
		}
		if entry.final == nil {
			submissionOptions := *entry.options
			submissionOptions.Deferred = nil
			submissionOptions.Signal = nil
			submissionOptions.OnResponse = nil
			func() {
				defer func() {
					if r := recover(); r != nil {
						entry.final = fauxCreateErrorMessage(errorFromPanic(r), c.api, c.provider, entry.model.ID)
					}
				}()
				entry.final = c.resolveResponse(entry.step, entry.context, &submissionOptions, entry.model)
			}()
		}
		c.pushDeltasOrError(outer, entry.final, signal, requestModel)
	}()
	return outer
}

// pushDeltasOrError streams the message, converting a streaming failure
// (e.g. a response without a terminal stop reason) into an error event, like
// the TS throw path.
func (c *FauxCore) pushDeltasOrError(outer *AssistantMessageEventStream, message *AssistantMessage, signal *abort.Signal, requestModel *Model) {
	if err := c.streamWithDeltas(outer, message, signal); err != nil {
		errorMessage := fauxCreateErrorMessage(err, c.api, c.provider, requestModel.ID)
		outer.Push(&EventError{Reason: StopError, Error: errorMessage})
		outer.End(errorMessage)
	}
}

// CancelDeferred mirrors the faux cancel.
func (c *FauxCore) CancelDeferred(_ *Model, handle *DeferredHandle, cancelOptions *DeferredCancelOptions) error {
	cancelled, _ := jsonx.Parse(jsonx.Stringify(deferredToJSON(handle)))
	var cancelledHandle *DeferredHandle
	if obj, ok := cancelled.(*jsonx.Obj); ok {
		if m, err := assistantMessageDeferredFromJSON(obj); err == nil {
			cancelledHandle = m
		}
	}
	if cancelledHandle == nil {
		cancelledHandle = handle
	}
	c.State.mu.Lock()
	c.State.cancelledDeferred = append(c.State.cancelledDeferred, cancelledHandle)
	c.State.mu.Unlock()
	c.mu.Lock()
	if entry, ok := c.deferredResponses[handle.ID]; ok {
		entry.cancelled = true
	}
	c.mu.Unlock()
	if cancelOptions != nil && cancelOptions.OnResponse != nil {
		cancelOptions.OnResponse(ProviderResponse{Status: 200, Headers: map[string]string{}}, nil)
	}
	return nil
}

func assistantMessageDeferredFromJSON(obj *jsonx.Obj) (*DeferredHandle, error) {
	handle := &DeferredHandle{
		Provider: stringField(obj, "provider"),
		ModelID:  stringField(obj, "modelId"),
		API:      stringField(obj, "api"),
		ID:       stringField(obj, "id"),
	}
	return handle, nil
}

// fauxProviderInstance adapts FauxCore to the ai.Provider interface.
type fauxProviderInstance struct {
	core *FauxCore
}

// FauxProviderHandle mirrors the TS handle.
type FauxProviderHandle struct {
	Provider Provider
	Core     *FauxCore
	API      string
	Models   []*Model
	State    *FauxProviderState
}

// GetModel resolves the faux models.
func (h *FauxProviderHandle) GetModel(modelID ...string) *Model { return h.Core.GetModel(modelID...) }

// SetResponses replaces the queue.
func (h *FauxProviderHandle) SetResponses(responses []FauxResponseStep) {
	h.Core.SetResponses(responses)
}

// AppendResponses appends to the queue.
func (h *FauxProviderHandle) AppendResponses(responses []FauxResponseStep) {
	h.Core.AppendResponses(responses)
}

// GetPendingResponseCount returns the queue length.
func (h *FauxProviderHandle) GetPendingResponseCount() int { return h.Core.GetPendingResponseCount() }

func (p *fauxProviderInstance) ID() string   { return p.core.provider }
func (p *fauxProviderInstance) Name() string { return p.core.provider }
func (p *fauxProviderInstance) Auth() ProviderAuth {
	return AlwaysConfiguredAuth("Faux")
}
func (p *fauxProviderInstance) GetModels() []*Model { return p.core.Models }
func (p *fauxProviderInstance) Stream(model *Model, context *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream {
	simple := SimpleStreamOptions{}
	if options != nil {
		simple.StreamOptions = *options
	}
	return p.core.Stream(model, context, &simple)
}
func (p *fauxProviderInstance) StreamSimple(model *Model, context *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
	return p.core.Stream(model, context, options)
}
func (p *fauxProviderInstance) FetchDeferred(model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream {
	return p.core.FetchDeferred(model, handle, options)
}
func (p *fauxProviderInstance) CancelDeferred(model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error {
	return p.core.CancelDeferred(model, handle, options)
}

// FauxProvider builds the faux provider handle for tests.
func FauxProvider(options RegisterFauxProviderOptions) *FauxProviderHandle {
	core := CreateFauxCore(options)
	if options.Deferred != nil {
		core.deferredPendingFetches = &options.Deferred.PendingFetches
		core.deferredPollAfterMs = options.Deferred.PollAfterMs
	}
	provider := &fauxProviderInstance{core: core}
	return &FauxProviderHandle{
		Provider: provider,
		Core:     core,
		API:      core.api,
		Models:   core.Models,
		State:    core.State,
	}
}

func fauxDeferredTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	default:
		return true
	}
}
