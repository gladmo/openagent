package agent

// proxy.go ports proxy.ts: the SSE proxy stream function for apps that
// route LLM calls through a server.

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"strings"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// ProxyStreamOptions mirrors the TS interface: the serializable stream
// option subset plus proxy auth and URL.
type ProxyStreamOptions struct {
	// Serializable subset (ProxySerializableStreamOptions).
	Temperature     *float64
	SamplingParams  map[string]any
	MaxTokens       *float64
	Reasoning       *string
	CacheRetention  *string
	SessionID       *string
	Headers         ai.ProviderHeaders
	Metadata        map[string]any
	Transport       *string
	ThinkingBudgets map[string]float64
	MaxRetryDelayMs *float64

	// Local abort signal for the proxy request.
	Signal *abort.Signal
	// Auth token for the proxy server.
	AuthToken string
	// Proxy server URL (e.g., "https://genai.example.com").
	ProxyURL string
}

// buildProxyRequestOptions serializes only the whitelisted options.
func buildProxyRequestOptions(options *ProxyStreamOptions) *jsonx.Obj {
	o := jsonx.NewObj()
	if options.Temperature != nil {
		o.Set("temperature", *options.Temperature)
	}
	if options.SamplingParams != nil {
		params := jsonx.NewObj()
		for k, v := range options.SamplingParams {
			params.Set(k, jsonx.Clone(v))
		}
		o.Set("samplingParams", params)
	}
	if options.MaxTokens != nil {
		o.Set("maxTokens", *options.MaxTokens)
	}
	if options.Reasoning != nil {
		o.Set("reasoning", *options.Reasoning)
	}
	if options.CacheRetention != nil {
		o.Set("cacheRetention", *options.CacheRetention)
	}
	if options.SessionID != nil {
		o.Set("sessionId", *options.SessionID)
	}
	if options.Headers != nil {
		headers := jsonx.NewObj()
		for k, v := range options.Headers {
			if v != nil {
				headers.Set(k, *v)
			} else {
				headers.Set(k, nil)
			}
		}
		o.Set("headers", headers)
	}
	if options.Metadata != nil {
		metadata := jsonx.NewObj()
		for k, v := range options.Metadata {
			metadata.Set(k, jsonx.Clone(v))
		}
		o.Set("metadata", metadata)
	}
	if options.Transport != nil {
		o.Set("transport", *options.Transport)
	}
	if options.ThinkingBudgets != nil {
		budgets := jsonx.NewObj()
		for k, v := range options.ThinkingBudgets {
			budgets.Set(k, v)
		}
		o.Set("thinkingBudgets", budgets)
	}
	if options.MaxRetryDelayMs != nil {
		o.Set("maxRetryDelayMs", *options.MaxRetryDelayMs)
	}
	return o
}

// modelToJSON serializes the model for the request body.
func modelToJSON(model *ai.Model) *jsonx.Obj {
	o := jsonx.NewObj()
	o.Set("id", model.ID)
	o.Set("name", model.Name)
	o.Set("api", model.API)
	o.Set("provider", model.Provider)
	o.Set("baseUrl", model.BaseURL)
	inputs := make([]any, 0, len(model.Input))
	for _, input := range model.Input {
		inputs = append(inputs, input)
	}
	o.Set("input", inputs)
	cost := jsonx.NewObj()
	cost.Set("input", model.Cost.Input)
	cost.Set("output", model.Cost.Output)
	cost.Set("cacheRead", model.Cost.CacheRead)
	cost.Set("cacheWrite", model.Cost.CacheWrite)
	o.Set("cost", cost)
	o.Set("reasoning", model.Reasoning)
	o.Set("contextWindow", model.ContextWindow)
	o.Set("maxTokens", model.MaxTokens)
	return o
}

// contextToJSON serializes the transcript context.
func contextToJSON(context *ai.TranscriptContext) *jsonx.Obj {
	o := jsonx.NewObj()
	messages := make([]any, 0, len(context.Messages))
	for _, message := range context.Messages {
		messages = append(messages, ai.MessageToJSON(message))
	}
	o.Set("messages", messages)
	return o
}

// StreamProxy mirrors streamProxy: posts the request to
// {proxyUrl}/api/stream, consumes the SSE `data: {...}` lines, rebuilds the
// partial assistant message client-side, and pushes full provider events.
func StreamProxy(
	model *ai.Model,
	context *ai.TranscriptContext,
	options *ProxyStreamOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()

	go func() {
		partial := &ai.AssistantMessage{
			Content:    []ai.ContentBlock{},
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			Usage:      ai.Usage{},
			StopReason: ai.StopPending,
		}
		partial.TimestampMs = float64(nowUnixMilli())

		fail := func(errorMessage string) {
			reason := ai.StopError
			if options.Signal.Aborted() {
				reason = ai.StopAborted
			}
			partial.StopReason = reason
			partial.ErrorMessage = &errorMessage
			stream.Push(&ai.EventError{Reason: reason, Error: partial})
			stream.End()
		}

		body := jsonx.NewObj()
		body.Set("model", modelToJSON(model))
		body.Set("context", contextToJSON(context))
		body.Set("options", buildProxyRequestOptions(options))

		request, err := http.NewRequest(http.MethodPost, options.ProxyURL+"/api/stream", bytes.NewReader([]byte(jsonx.Stringify(body))))
		if err != nil {
			fail(err.Error())
			return
		}
		request.Header.Set("Authorization", "Bearer "+options.AuthToken)
		request.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		if ctx, cancel := abort.ToGoContext(request.Context(), options.Signal); ctx != nil {
			request = request.WithContext(ctx)
			defer cancel()
		}

		response, err := client.Do(request)
		if err != nil {
			fail(err.Error())
			return
		}
		defer response.Body.Close()

		if response.StatusCode < 200 || response.StatusCode >= 300 {
			errorMessage := fmt.Sprintf("Proxy error: %d %s", response.StatusCode, response.Status)
			var parsed struct {
				Error string `json:"error"`
			}
			if err := decodeJSONBody(response, &parsed); err == nil && parsed.Error != "" {
				errorMessage = "Proxy error: " + parsed.Error
			}
			fail(errorMessage)
			return
		}

		sawTerminalEvent := false
		processLine := func(line string) {
			if !strings.HasPrefix(line, "data: ") {
				return
			}
			data := strings.TrimSpace(line[6:])
			if data == "" {
				return
			}
			value, err := jsonx.Parse(data)
			if err != nil {
				return
			}
			event, err := processProxyEvent(value, partial)
			if err != nil {
				panic(err)
			}
			if event != nil {
				if event.EventType() == "done" || event.EventType() == "error" {
					sawTerminalEvent = true
				}
				stream.Push(event)
			}
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					if err, ok := r.(error); ok {
						fail(err.Error())
						return
					}
					fail(fmt.Sprint(r))
				}
			}()
			scanner := bufio.NewScanner(response.Body)
			scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
			for scanner.Scan() {
				if options.Signal.Aborted() {
					fail("Request aborted by user")
					return
				}
				processLine(scanner.Text())
			}
			// A clean EOF without done/error means the server dropped the
			// response mid-stream: surface it as an error.
			if !sawTerminalEvent && !stream.Done() {
				partial.StopReason = ai.StopError
				message := "Connection closed by proxy server before the response completed"
				partial.ErrorMessage = &message
				stream.Push(&ai.EventError{Reason: ai.StopError, Error: partial})
			}
			stream.End()
		}()
	}()
	return stream
}

// processProxyEvent converts one wire event into a full provider event,
// updating the shared partial.
func processProxyEvent(value any, partial *ai.AssistantMessage) (ai.AssistantMessageEvent, error) {
	obj, ok := value.(*jsonx.Obj)
	if !ok {
		return nil, fmt.Errorf("proxy event is not an object")
	}
	typ, _ := obj.Get("type")
	contentIndex := proxyContentIndex(obj)
	switch typ {
	case "start":
		return &ai.EventStart{Partial: partial}, nil
	case "text_start":
		ensureContentIndex(partial, contentIndex, ai.TextContent{})
		return &ai.EventTextStart{ContentIndex: contentIndex, Partial: partial}, nil
	case "text_delta":
		if content, ok := contentAt(partial, contentIndex).(ai.TextContent); ok {
			content.Text += proxyString(obj, "delta")
			partial.Content[contentIndex] = content
			return &ai.EventTextDelta{ContentIndex: contentIndex, Delta: proxyString(obj, "delta"), Partial: partial}, nil
		}
		return nil, fmt.Errorf("Received text_delta for non-text content")
	case "text_end":
		if content, ok := contentAt(partial, contentIndex).(ai.TextContent); ok {
			if sig, has := obj.Get("contentSignature"); has {
				if s, ok := sig.(string); ok {
					content.TextSignature = &s
				}
			}
			partial.Content[contentIndex] = content
			return &ai.EventTextEnd{ContentIndex: contentIndex, Content: content.Text, Partial: partial}, nil
		}
		return nil, fmt.Errorf("Received text_end for non-text content")
	case "thinking_start":
		ensureContentIndex(partial, contentIndex, ai.ThinkingContent{})
		return &ai.EventThinkingStart{ContentIndex: contentIndex, Partial: partial}, nil
	case "thinking_delta":
		if content, ok := contentAt(partial, contentIndex).(ai.ThinkingContent); ok {
			content.Thinking += proxyString(obj, "delta")
			partial.Content[contentIndex] = content
			return &ai.EventThinkingDelta{ContentIndex: contentIndex, Delta: proxyString(obj, "delta"), Partial: partial}, nil
		}
		return nil, fmt.Errorf("Received thinking_delta for non-thinking content")
	case "thinking_end":
		if content, ok := contentAt(partial, contentIndex).(ai.ThinkingContent); ok {
			if sig, has := obj.Get("contentSignature"); has {
				if s, ok := sig.(string); ok {
					content.ThinkingSignature = &s
				}
			}
			partial.Content[contentIndex] = content
			return &ai.EventThinkingEnd{ContentIndex: contentIndex, Content: content.Thinking, Partial: partial}, nil
		}
		return nil, fmt.Errorf("Received thinking_end for non-thinking content")
	case "toolcall_start":
		call := &ai.ToolCall{
			ID:        proxyString(obj, "id"),
			Name:      proxyString(obj, "toolName"),
			Arguments: jsonx.NewObj(),
		}
		ensureContentIndex(partial, contentIndex, call)
		return &ai.EventToolCallStart{ContentIndex: contentIndex, Partial: partial}, nil
	case "toolcall_delta":
		if call, ok := contentAt(partial, contentIndex).(*ai.ToolCall); ok {
			delta := proxyString(obj, "delta")
			partialJSON := proxyPartialJSON(call) + delta
			setProxyPartialJSON(call, partialJSON)
			call.Arguments = ai.ParseStreamingJSONObject(partialJSON)
			partial.Content[contentIndex] = call
			return &ai.EventToolCallDelta{ContentIndex: contentIndex, Delta: delta, Partial: partial}, nil
		}
		return nil, fmt.Errorf("Received toolcall_delta for non-toolCall content")
	case "toolcall_end":
		if call, ok := contentAt(partial, contentIndex).(*ai.ToolCall); ok {
			if toolCallValue, has := obj.Get("toolCall"); has {
				if toolCallObj, ok := toolCallValue.(*jsonx.Obj); ok {
					call.ID = proxyString(toolCallObj, "id")
					call.Name = proxyString(toolCallObj, "name")
					if args, has := toolCallObj.Get("arguments"); has {
						if argsObj, ok := args.(*jsonx.Obj); ok {
							call.Arguments = argsObj
						}
					}
					if sig, has := toolCallObj.Get("thoughtSignature"); has {
						call.ThoughtSignature = stringPtrOrNil(sig)
					}
					if ns, has := toolCallObj.Get("namespace"); has {
						call.Namespace = stringPtrOrNil(ns)
					}
				}
			}
			clearProxyPartialJSON(call)
			return &ai.EventToolCallEnd{ContentIndex: contentIndex, ToolCall: call, Partial: partial}, nil
		}
		return nil, nil
	case "done":
		partial.StopReason = proxyString(obj, "reason")
		partial.Usage = proxyUsage(obj)
		if level, has := obj.Get("providerThinkingLevel"); has {
			if s, ok := level.(string); ok {
				partial.ProviderThinkingLevel = &s
			}
		}
		return &ai.EventDone{Reason: partial.StopReason, Message: partial}, nil
	case "error":
		partial.StopReason = proxyString(obj, "reason")
		if msg, has := obj.Get("errorMessage"); has {
			partial.ErrorMessage = stringPtrOrNil(msg)
		}
		partial.Usage = proxyUsage(obj)
		if level, has := obj.Get("providerThinkingLevel"); has {
			if s, ok := level.(string); ok {
				partial.ProviderThinkingLevel = &s
			}
		}
		return &ai.EventError{Reason: partial.StopReason, Error: partial}, nil
	default:
		return nil, nil
	}
}

// The TS tracks the streamed tool-call JSON on a hidden partialJson field;
// Go carries it in the arguments obj under a reserved key instead.
const proxyPartialJSONKey = "\x00partialJson"

func proxyPartialJSON(call *ai.ToolCall) string {
	if v, ok := call.Arguments.Get(proxyPartialJSONKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func setProxyPartialJSON(call *ai.ToolCall, partialJSON string) {
	call.Arguments.Set(proxyPartialJSONKey, partialJSON)
}

func clearProxyPartialJSON(call *ai.ToolCall) {
	call.Arguments.Delete(proxyPartialJSONKey)
}

func proxyContentIndex(obj *jsonx.Obj) int {
	if v, ok := obj.Get("contentIndex"); ok {
		if f, ok := jsonx.ToFloat(v); ok {
			return int(f)
		}
	}
	return 0
}

func proxyString(obj *jsonx.Obj, key string) string {
	if v, ok := obj.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func stringPtrOrNil(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func proxyUsage(obj *jsonx.Obj) ai.Usage {
	usage := ai.Usage{}
	if v, ok := obj.Get("usage"); ok {
		if u, ok := v.(*jsonx.Obj); ok {
			if f, ok := jsonx.ToFloat(u.MustGet("input")); ok {
				usage.Input = f
			}
			if f, ok := jsonx.ToFloat(u.MustGet("output")); ok {
				usage.Output = f
			}
			if f, ok := jsonx.ToFloat(u.MustGet("cacheRead")); ok {
				usage.CacheRead = f
			}
			if f, ok := jsonx.ToFloat(u.MustGet("cacheWrite")); ok {
				usage.CacheWrite = f
			}
			if v, ok := u.Get("cacheWrite1h"); ok {
				if f, ok := jsonx.ToFloat(v); ok {
					usage.CacheWrite1h = &f
				}
			}
			if v, ok := u.Get("reasoning"); ok {
				if f, ok := jsonx.ToFloat(v); ok {
					usage.Reasoning = &f
				}
			}
			if f, ok := jsonx.ToFloat(u.MustGet("totalTokens")); ok {
				usage.TotalTokens = f
			}
			if c, ok := u.Get("cost"); ok {
				if co, ok := c.(*jsonx.Obj); ok {
					if f, ok := jsonx.ToFloat(co.MustGet("input")); ok {
						usage.Cost.Input = f
					}
					if f, ok := jsonx.ToFloat(co.MustGet("output")); ok {
						usage.Cost.Output = f
					}
					if f, ok := jsonx.ToFloat(co.MustGet("cacheRead")); ok {
						usage.Cost.CacheRead = f
					}
					if f, ok := jsonx.ToFloat(co.MustGet("cacheWrite")); ok {
						usage.Cost.CacheWrite = f
					}
					if f, ok := jsonx.ToFloat(co.MustGet("total")); ok {
						usage.Cost.Total = f
					}
				}
			}
		}
	}
	return usage
}

func ensureContentIndex(partial *ai.AssistantMessage, index int, block ai.ContentBlock) {
	for len(partial.Content) <= index {
		partial.Content = append(partial.Content, nil)
	}
	partial.Content[index] = block
}

func contentAt(partial *ai.AssistantMessage, index int) ai.ContentBlock {
	if index < 0 || index >= len(partial.Content) {
		return nil
	}
	return partial.Content[index]
}

func decodeJSONBody(response *http.Response, out any) error {
	return jsonDecode(response, out)
}

func nowUnixMilli() int64 { return timeNowUnixMilli() }
