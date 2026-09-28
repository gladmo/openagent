package harness

// hooks.go ports harness/hooks.ts: the ordered hook registry with
// per-hook aggregation semantics, plus the stream-options patch helpers.
// Hook events/results are JSON-shaped (jsonx) values; typed accessors
// arrive with the runtime port.

import (
	"sync"

	chordcontext "github.com/gladmo/openagent/chord/context"
	"github.com/gladmo/openagent/jsonx"
)

// HookName values.
const (
	HookBeforeRun        = "before_run"
	HookBeforeDrive      = "before_drive"
	HookBeforeRunEnd     = "before_run_end"
	HookTransformContext = "transform_context"
	HookBeforeRequest    = "before_request"
	HookBeforePayload    = "before_payload"
	HookAfterResponse    = "after_response"
	HookBeforeTool       = "before_tool"
	HookAfterTool        = "after_tool"
	HookBeforeCompaction = "before_compaction"
	HookBeforeNavigation = "before_navigation"
)

// HookHandler receives the event payload (jsonx object) and context; the
// return is the (possibly nil) result payload.
type HookHandler func(event *jsonx.Obj, ctx Context) (*jsonx.Obj, error)

// HookErrorReporter mirrors the TS type.
type HookErrorReporter func(err error, hook string, lane string, ctx Context)

// Hooks is the registry contract.
type Hooks interface {
	On(name string, handler HookHandler, id ...string) (unsubscribe func())
	Has(name string) bool
	Close(err error)
}

type hookRegistration struct {
	id      string
	handler HookHandler
}

// HookRegistry mirrors the TS class.
type HookRegistry struct {
	mu            sync.Mutex
	registrations map[string][]hookRegistration
	reportError   HookErrorReporter
	closedError   error
}

// NewHookRegistry builds a registry with an error reporter.
func NewHookRegistry(reportError HookErrorReporter) *HookRegistry {
	return &HookRegistry{registrations: map[string][]hookRegistration{}, reportError: reportError}
}

// On registers an ordered handler.
func (r *HookRegistry) On(name string, handler HookHandler, id ...string) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closedError != nil {
		panic(r.closedError)
	}
	registration := hookRegistration{handler: handler}
	if len(id) > 0 {
		registration.id = id[0]
	}
	r.registrations[name] = append(r.registrations[name], registration)
	removed := false
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if removed {
			return
		}
		removed = true
		list := r.registrations[name]
		for i := range list {
			if &list[i] == &registration {
				r.registrations[name] = append(list[:i], list[i+1:]...)
				return
			}
		}
	}
}

// Has reports whether any handler is registered.
func (r *HookRegistry) Has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.registrations[name]) != 0
}

// Close freezes the registry.
func (r *HookRegistry) Close(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closedError == nil {
		r.closedError = err
	}
}

func (r *HookRegistry) registrationsFor(name string) []hookRegistration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]hookRegistration{}, r.registrations[name]...)
}

// Run invokes one aggregate by hook name (RunWithGate's admitted body; the
// gate itself arrives with the execution port).
func (r *HookRegistry) Run(name string, event *jsonx.Obj, ctx Context) (*jsonx.Obj, error) {
	if err := r.closedErr(); err != nil {
		return nil, err
	}
	return r.aggregate(name, event, ctx)
}

func (r *HookRegistry) closedErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closedError
}

func (r *HookRegistry) aggregate(name string, event *jsonx.Obj, ctx Context) (*jsonx.Obj, error) {
	switch name {
	case HookBeforeRun:
		return r.beforeRun(event, ctx), nil
	case HookBeforeDrive:
		return nil, r.invokeAllFailClosed(name, event, ctx)
	case HookBeforeRunEnd:
		var followUp any
		_ = followUp
		var result *jsonx.Obj
		r.invokeAll(name, event, func(value *jsonx.Obj) {
			if value != nil {
				if fu, ok := value.Get("followUp"); ok && fu != nil {
					result = jsonx.ObjFrom("followUp", fu)
				}
			}
		}, ctx)
		return result, nil
	case HookTransformContext:
		return r.transformContext(event, ctx), nil
	case HookBeforeRequest:
		return r.beforeRequest(event, ctx), nil
	case HookBeforePayload:
		return r.beforePayload(event, ctx), nil
	case HookAfterResponse:
		return r.afterResponse(event, ctx), nil
	case HookBeforeTool:
		return r.beforeTool(event, ctx), nil
	case HookAfterTool:
		return r.afterTool(event, ctx), nil
	case HookBeforeCompaction:
		return r.firstStructural(name, event, "compaction", ctx), nil
	case HookBeforeNavigation:
		return r.firstStructural(name, event, "summary", ctx), nil
	default:
		return nil, nil
	}
}

func eventLaneOf(event *jsonx.Obj) string {
	if lane, ok := event.Get("lane"); ok {
		if s, ok := lane.(string); ok {
			return s
		}
	}
	return ""
}

func (r *HookRegistry) report(err error, hook string, event *jsonx.Obj, ctx Context) {
	if r.reportError != nil {
		r.reportError(err, hook, eventLaneOf(event), ctx)
	}
}

// beforeRun accumulates injected messages across handlers.
func (r *HookRegistry) beforeRun(event *jsonx.Obj, ctx Context) *jsonx.Obj {
	promptList, _ := event.Get("prompt")
	prompt, _ := promptList.([]any)
	var injected []any
	for _, registration := range r.registrationsFor(HookBeforeRun) {
		current := event.Clone()
		current.Set("prompt", append([]any{}, prompt...))
		result, err := registration.handler(current, ctx)
		if err != nil {
			r.report(err, HookBeforeRun, event, ctx)
			continue
		}
		if messages, ok := result.Get("messages"); ok && messages != nil {
			if list, ok := messages.([]any); ok {
				injected = append(injected, list...)
				prompt = append(append([]any{}, prompt...), list...)
			}
		}
	}
	if len(injected) == 0 {
		return nil
	}
	return jsonx.ObjFrom("messages", injected)
}

// beforeTool: last args wins; first block breaks (throw => block).
func (r *HookRegistry) beforeTool(event *jsonx.Obj, ctx Context) *jsonx.Obj {
	argsValue, hasArgs := event.Get("args")
	args := argsValue
	var block *jsonx.Obj
	for _, registration := range r.registrationsFor(HookBeforeTool) {
		current := event.Clone()
		if hasArgs {
			current.Set("args", args)
		}
		result, err := r.invokeToolRegistration(HookBeforeTool, registration, current, ctx)
		if err != nil {
			r.report(err, HookBeforeTool, event, ctx)
			block = jsonx.ObjFrom("reason", err.Error())
			break
		}
		if nextArgs, ok := result.Get("args"); ok && nextArgs != nil {
			args = nextArgs
		}
		if blockValue, ok := result.Get("block"); ok && blockValue != nil {
			if blockObj, ok := blockValue.(*jsonx.Obj); ok {
				block = blockObj
				break
			}
		}
	}
	out := jsonx.NewObj()
	if hasArgs && args != argsValue {
		out.Set("args", args)
	}
	if block != nil {
		out.Set("block", block)
	}
	if out.Len() == 0 {
		return nil
	}
	return out
}

// transformContext folds messages and systemPrompt.
func (r *HookRegistry) transformContext(event *jsonx.Obj, ctx Context) *jsonx.Obj {
	messagesValue, _ := event.Get("messages")
	systemPromptValue, _ := event.Get("systemPrompt")
	messages, systemPrompt := messagesValue, systemPromptValue
	for _, registration := range r.registrationsFor(HookTransformContext) {
		current := event.Clone()
		current.Set("messages", messages)
		current.Set("systemPrompt", systemPrompt)
		result, err := registration.handler(current, ctx)
		if err != nil {
			r.report(err, HookTransformContext, event, ctx)
			continue
		}
		if next, ok := result.Get("messages"); ok && next != nil {
			messages = next
		}
		if next, ok := result.Get("systemPrompt"); ok && next != nil {
			systemPrompt = next
		}
	}
	return jsonx.ObjFrom("messages", messages, "systemPrompt", systemPrompt)
}

// beforeRequest merges stream-options patches; returns the cumulative
// patch when anything changed.
func (r *HookRegistry) beforeRequest(event *jsonx.Obj, ctx Context) *jsonx.Obj {
	baseValue, _ := event.Get("streamOptions")
	baseObj, _ := baseValue.(*jsonx.Obj)
	base := baseObj
	if base == nil {
		base = jsonx.NewObj()
	}
	options := *streamOptionsFromJSON(base)
	changed := false
	for _, registration := range r.registrationsFor(HookBeforeRequest) {
		current := event.Clone()
		current.Set("streamOptions", streamOptionsToJSON(&options))
		result, err := registration.handler(current, ctx)
		if err != nil {
			r.report(err, HookBeforeRequest, event, ctx)
			continue
		}
		if patchValue, ok := result.Get("streamOptions"); ok {
			if patchObj, ok := patchValue.(*jsonx.Obj); ok {
				patch := streamOptionsPatchFromJSON(patchObj)
				options = ApplyStreamOptionsPatch(options, patch)
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	original := *streamOptionsFromJSON(base)
	return jsonx.ObjFrom("streamOptions", streamOptionsPatchToJSON(CreateStreamOptionsPatch(original, options)))
}

func (r *HookRegistry) beforePayload(event *jsonx.Obj, ctx Context) *jsonx.Obj {
	payloadValue, _ := event.Get("payload")
	payload := payloadValue
	for _, registration := range r.registrationsFor(HookBeforePayload) {
		current := event.Clone()
		current.Set("payload", payload)
		result, err := registration.handler(current, ctx)
		if err != nil {
			r.report(err, HookBeforePayload, event, ctx)
			continue
		}
		if next, ok := result.Get("payload"); ok && next != nil {
			payload = next
		}
	}
	return jsonx.ObjFrom("payload", payload)
}

func (r *HookRegistry) afterResponse(event *jsonx.Obj, ctx Context) *jsonx.Obj {
	messageValue, _ := event.Get("message")
	message := messageValue
	for _, registration := range r.registrationsFor(HookAfterResponse) {
		current := event.Clone()
		current.Set("message", message)
		result, err := registration.handler(current, ctx)
		if err != nil {
			r.report(err, HookAfterResponse, event, ctx)
			continue
		}
		if next, ok := result.Get("message"); ok && next != nil {
			message = next
		}
	}
	return jsonx.ObjFrom("message", message)
}

// afterTool: field-wise overlay from every handler.
func (r *HookRegistry) afterTool(event *jsonx.Obj, ctx Context) *jsonx.Obj {
	current := jsonx.NewObj()
	for _, key := range []string{"content", "details", "isError", "usage"} {
		if v, ok := event.Get(key); ok {
			current.Set(key, v)
		}
	}
	aggregate := jsonx.NewObj()
	for _, registration := range r.registrationsFor(HookAfterTool) {
		invocation := event.Clone()
		for _, key := range current.Keys() {
			invocation.Set(key, current.MustGet(key))
		}
		result, err := r.invokeToolRegistration(HookAfterTool, registration, invocation, ctx)
		if err != nil {
			r.report(err, HookAfterTool, event, ctx)
			continue
		}
		for _, key := range []string{"content", "details", "isError", "usage", "terminate"} {
			if v, ok := result.Get(key); ok && v != nil {
				aggregate.Set(key, v)
			}
		}
		for _, key := range []string{"content", "details", "isError", "usage"} {
			if v, ok := result.Get(key); ok && v != nil {
				current.Set(key, v)
			}
		}
	}
	if aggregate.Len() == 0 {
		return nil
	}
	return aggregate
}

// firstStructural: first decline-or-result wins; decline+field is an error.
func (r *HookRegistry) firstStructural(name string, event *jsonx.Obj, resultField string, ctx Context) *jsonx.Obj {
	for _, registration := range r.registrationsFor(name) {
		result, err := registration.handler(event, ctx)
		if err != nil {
			r.report(err, name, event, ctx)
			continue
		}
		if result == nil || result.Len() == 0 {
			continue
		}
		decline, hasDecline := result.Get("decline")
		field, hasField := result.Get(resultField)
		isDecline := hasDecline && decline == true
		hasResultField := hasField && field != nil
		if isDecline && hasResultField {
			r.report(errBothDeclineAndField(name, resultField), name, event, ctx)
			continue
		}
		if isDecline || hasResultField {
			return result
		}
	}
	return nil
}

func errBothDeclineAndField(name, field string) error {
	return ToError(name + " hook cannot return both decline and " + field)
}

// invokeToolRegistration wraps tool hooks in a pi.harness.hook span.
func (r *HookRegistry) invokeToolRegistration(name string, registration hookRegistration, event *jsonx.Obj, ctx Context) (result *jsonx.Obj, err error) {
	attributes := map[string]any{
		"pi.lane.name": eventLaneOf(event),
		"pi.hook.name": name,
	}
	if runID, ok := event.Get("runId"); ok && runID != nil {
		attributes["pi.operation.id"] = runID
	}
	if registration.id != "" {
		attributes["pi.hook.registration_id"] = registration.id
	}
	spanErr := error(nil)
	_, spanErr2 := StartHarnessSpan(HarnessSpanHook, attributes, ctx, func(span telemetrySpanAlias, spanCtx Context) (*jsonx.Obj, error) {
		result, err = registration.handler(event, spanCtx)
		if err != nil {
			span.SetAttributes(map[string]any{"pi.hook.outcome": "failed"})
			span.SetStatus(spanStatusError())
			return nil, err
		}
		blocked := false
		if name == HookBeforeTool && result != nil {
			if block, ok := result.Get("block"); ok && block != nil {
				blocked = true
			}
		}
		outcome := "completed"
		if blocked {
			outcome = "blocked"
		}
		span.SetAttributes(map[string]any{"pi.hook.outcome": outcome})
		return nil, nil
	})
	_ = spanErr
	_ = spanErr2
	return result, err
}

func (r *HookRegistry) invokeAllFailClosed(name string, event *jsonx.Obj, ctx Context) error {
	for _, registration := range r.registrationsFor(name) {
		if _, err := registration.handler(event, ctx); err != nil {
			r.report(err, name, event, ctx)
			return err
		}
	}
	return nil
}

func (r *HookRegistry) invokeAll(name string, event *jsonx.Obj, apply func(*jsonx.Obj), ctx Context) {
	for _, registration := range r.registrationsFor(name) {
		result, err := registration.handler(event, ctx)
		if err != nil {
			r.report(err, name, event, ctx)
			continue
		}
		apply(result)
	}
}

// ---------------------------------------------------------------------------
// Stream options patch helpers
// ---------------------------------------------------------------------------

// ApplyStreamOptionsPatch mirrors applyStreamOptionsPatch.
func ApplyStreamOptionsPatch(base AgentHarnessStreamOptions, patch AgentHarnessStreamOptionsPatch) AgentHarnessStreamOptions {
	next := base
	if patch.HasTransport {
		next.Transport = patch.Transport
	}
	if patch.HasTimeoutMs {
		next.TimeoutMs = patch.TimeoutMs
	}
	if patch.HasMaxRetries {
		next.MaxRetries = patch.MaxRetries
	}
	if patch.HasMaxRetryDelayMs {
		next.MaxRetryDelayMs = patch.MaxRetryDelayMs
	}
	if patch.HasCacheRetention {
		next.CacheRetention = patch.CacheRetention
	}
	if patch.HasDeferred {
		next.Deferred = patch.Deferred
	}
	if patch.HasHeaders {
		if patch.ClearHeaders {
			next.Headers = nil
		} else {
			headers := map[string]string{}
			for k, v := range next.Headers {
				headers[k] = v
			}
			for k, v := range patch.Headers {
				if v == nil {
					delete(headers, k)
				} else {
					headers[k] = *v
				}
			}
			next.Headers = headers
		}
	}
	if patch.HasMetadata {
		if patch.ClearMetadata {
			next.Metadata = nil
		} else {
			metadata := map[string]any{}
			for k, v := range next.Metadata {
				metadata[k] = v
			}
			for k, v := range patch.Metadata {
				if v == nil {
					delete(metadata, k)
				} else {
					metadata[k] = v
				}
			}
			next.Metadata = metadata
		}
	}
	return next
}

// CreateStreamOptionsPatch mirrors createStreamOptionsPatch (diff).
func CreateStreamOptionsPatch(base, value AgentHarnessStreamOptions) AgentHarnessStreamOptionsPatch {
	patch := AgentHarnessStreamOptionsPatch{}
	if base.Transport != value.Transport {
		patch.Transport, patch.HasTransport = value.Transport, true
	}
	if floatPtrEqual(base.TimeoutMs, value.TimeoutMs) != true {
		patch.TimeoutMs, patch.HasTimeoutMs = value.TimeoutMs, true
	}
	if floatPtrEqual(base.MaxRetries, value.MaxRetries) != true {
		patch.MaxRetries, patch.HasMaxRetries = value.MaxRetries, true
	}
	if floatPtrEqual(base.MaxRetryDelayMs, value.MaxRetryDelayMs) != true {
		patch.MaxRetryDelayMs, patch.HasMaxRetryDelayMs = value.MaxRetryDelayMs, true
	}
	if stringPtrEqual(base.CacheRetention, value.CacheRetention) != true {
		patch.CacheRetention, patch.HasCacheRetention = value.CacheRetention, true
	}
	if base.Deferred != value.Deferred {
		patch.Deferred, patch.HasDeferred = value.Deferred, true
	}
	if !headersEqual(base.Headers, value.Headers) {
		if value.Headers == nil {
			patch.Headers, patch.HasHeaders, patch.ClearHeaders = nil, true, true
		} else {
			headers := map[string]*string{}
			for k := range base.Headers {
				if _, exists := value.Headers[k]; !exists {
					headers[k] = nil
				}
			}
			for k, v := range value.Headers {
				if base.Headers == nil || base.Headers[k] != v {
					s := v
					headers[k] = &s
				}
			}
			patch.Headers, patch.HasHeaders = headers, true
		}
	}
	if !metadataEqual(base.Metadata, value.Metadata) {
		if value.Metadata == nil {
			patch.Metadata, patch.HasMetadata, patch.ClearMetadata = nil, true, true
		} else {
			metadata := map[string]any{}
			for k := range base.Metadata {
				if _, exists := value.Metadata[k]; !exists {
					metadata[k] = nil
				}
			}
			for k, v := range value.Metadata {
				if existing, exists := base.Metadata[k]; !exists || existing != v {
					metadata[k] = v
				}
			}
			patch.Metadata, patch.HasMetadata = metadata, true
		}
	}
	return patch
}

func floatPtrEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func headersEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func metadataEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok || !jsonxEqualAny(v, bv) {
			return false
		}
	}
	return true
}

func jsonxEqualAny(a, b any) bool {
	return a == b
}

// ---------------------------------------------------------------------------
// JSON codecs for stream options (used by hooks and future persistence)
// ---------------------------------------------------------------------------

func streamOptionsToJSON(o *AgentHarnessStreamOptions) *jsonx.Obj {
	obj := jsonx.NewObj()
	if o.Transport != nil {
		obj.Set("transport", *o.Transport)
	}
	if o.TimeoutMs != nil {
		obj.Set("timeoutMs", *o.TimeoutMs)
	}
	if o.MaxRetries != nil {
		obj.Set("maxRetries", *o.MaxRetries)
	}
	if o.MaxRetryDelayMs != nil {
		obj.Set("maxRetryDelayMs", *o.MaxRetryDelayMs)
	}
	if len(o.Headers) > 0 {
		headers := jsonx.NewObj()
		for k, v := range o.Headers {
			headers.Set(k, v)
		}
		obj.Set("headers", headers)
	}
	if len(o.Metadata) > 0 {
		metadata := jsonx.NewObj()
		for k, v := range o.Metadata {
			metadata.Set(k, v)
		}
		obj.Set("metadata", metadata)
	}
	if o.CacheRetention != nil {
		obj.Set("cacheRetention", *o.CacheRetention)
	}
	if o.Deferred != nil {
		obj.Set("deferred", o.Deferred)
	}
	return obj
}

func streamOptionsFromJSON(obj *jsonx.Obj) *AgentHarnessStreamOptions {
	if obj == nil {
		return &AgentHarnessStreamOptions{}
	}
	o := &AgentHarnessStreamOptions{}
	if v, ok := obj.Get("transport"); ok {
		if s, ok := v.(string); ok {
			o.Transport = &s
		}
	}
	if v, ok := obj.Get("timeoutMs"); ok {
		if f, ok := v.(float64); ok {
			o.TimeoutMs = &f
		}
	}
	if v, ok := obj.Get("maxRetries"); ok {
		if f, ok := v.(float64); ok {
			o.MaxRetries = &f
		}
	}
	if v, ok := obj.Get("maxRetryDelayMs"); ok {
		if f, ok := v.(float64); ok {
			o.MaxRetryDelayMs = &f
		}
	}
	if v, ok := obj.Get("headers"); ok {
		if h, ok := v.(*jsonx.Obj); ok {
			o.Headers = map[string]string{}
			for _, k := range h.Keys() {
				if s, ok := h.MustGet(k).(string); ok {
					o.Headers[k] = s
				}
			}
		}
	}
	if v, ok := obj.Get("metadata"); ok {
		if m, ok := v.(*jsonx.Obj); ok {
			o.Metadata = map[string]any{}
			for _, k := range m.Keys() {
				o.Metadata[k] = m.MustGet(k)
			}
		}
	}
	if v, ok := obj.Get("cacheRetention"); ok {
		if s, ok := v.(string); ok {
			o.CacheRetention = &s
		}
	}
	if v, ok := obj.Get("deferred"); ok {
		o.Deferred = v
	}
	return o
}

func streamOptionsPatchToJSON(p AgentHarnessStreamOptionsPatch) *jsonx.Obj {
	obj := jsonx.NewObj()
	if p.HasTransport {
		if p.Transport != nil {
			obj.Set("transport", *p.Transport)
		} else {
			obj.Set("transport", nil)
		}
	}
	if p.HasTimeoutMs {
		obj.Set("timeoutMs", floatPtrOrNil(p.TimeoutMs))
	}
	if p.HasMaxRetries {
		obj.Set("maxRetries", floatPtrOrNil(p.MaxRetries))
	}
	if p.HasMaxRetryDelayMs {
		obj.Set("maxRetryDelayMs", floatPtrOrNil(p.MaxRetryDelayMs))
	}
	if p.HasHeaders {
		if p.ClearHeaders {
			obj.Set("headers", nil)
		} else {
			headers := jsonx.NewObj()
			for k, v := range p.Headers {
				if v == nil {
					headers.Set(k, nil)
				} else {
					headers.Set(k, *v)
				}
			}
			obj.Set("headers", headers)
		}
	}
	if p.HasMetadata {
		if p.ClearMetadata {
			obj.Set("metadata", nil)
		} else {
			metadata := jsonx.NewObj()
			for k, v := range p.Metadata {
				metadata.Set(k, v)
			}
			obj.Set("metadata", metadata)
		}
	}
	if p.HasCacheRetention {
		if p.CacheRetention != nil {
			obj.Set("cacheRetention", *p.CacheRetention)
		} else {
			obj.Set("cacheRetention", nil)
		}
	}
	if p.HasDeferred {
		if p.Deferred != nil {
			obj.Set("deferred", p.Deferred)
		} else {
			obj.Set("deferred", nil)
		}
	}
	return obj
}

func floatPtrOrNil(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func streamOptionsPatchFromJSON(obj *jsonx.Obj) AgentHarnessStreamOptionsPatch {
	p := AgentHarnessStreamOptionsPatch{}
	if v, ok := obj.Get("transport"); ok {
		p.HasTransport = true
		if s, ok := v.(string); ok {
			p.Transport = &s
		}
	}
	if v, ok := obj.Get("timeoutMs"); ok {
		p.HasTimeoutMs = true
		if f, ok := v.(float64); ok {
			p.TimeoutMs = &f
		}
	}
	if v, ok := obj.Get("maxRetries"); ok {
		p.HasMaxRetries = true
		if f, ok := v.(float64); ok {
			p.MaxRetries = &f
		}
	}
	if v, ok := obj.Get("maxRetryDelayMs"); ok {
		p.HasMaxRetryDelayMs = true
		if f, ok := v.(float64); ok {
			p.MaxRetryDelayMs = &f
		}
	}
	if v, ok := obj.Get("headers"); ok {
		p.HasHeaders = true
		if v == nil {
			p.ClearHeaders = true
		} else if h, ok := v.(*jsonx.Obj); ok {
			p.Headers = map[string]*string{}
			for _, k := range h.Keys() {
				value := h.MustGet(k)
				if value == nil {
					p.Headers[k] = nil
				} else if s, ok := value.(string); ok {
					copied := s
					p.Headers[k] = &copied
				}
			}
		}
	}
	if v, ok := obj.Get("metadata"); ok {
		p.HasMetadata = true
		if v == nil {
			p.ClearMetadata = true
		} else if m, ok := v.(*jsonx.Obj); ok {
			p.Metadata = map[string]any{}
			for _, k := range m.Keys() {
				p.Metadata[k] = m.MustGet(k)
			}
		}
	}
	if v, ok := obj.Get("cacheRetention"); ok {
		p.HasCacheRetention = true
		if s, ok := v.(string); ok {
			p.CacheRetention = &s
		}
	}
	if v, ok := obj.Get("deferred"); ok {
		p.HasDeferred = true
		p.Deferred = v
	}
	return p
}

// aliases to avoid import cycles in this file's helper signatures
type abortSignalAlias = abortSignalType
type telemetrySpanAlias = telemetrySpanType

// keep chordcontext referenced for the future gate port
var _ = chordcontext.BackgroundContext
