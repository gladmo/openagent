package harness

// execution_assistant.go ports harness/execution/assistant.ts.

import (
	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/telemetry"
)

// AssistantResponseMetadata captures HTTP response metadata before the
// provider response body is consumed.
type AssistantResponseMetadata struct {
	Status  *int
	Headers map[string]string
}

// AssistantStreamObserver is the process-local lifecycle observer for one
// assistant stream.
type AssistantStreamObserver interface {
	Start(message *ai.AssistantMessage, event *ai.EventStart, ctx Context)
	Update(message *ai.AssistantMessage, event ai.AssistantMessageEvent, ctx Context)
	End(message *ai.AssistantMessage, ctx Context)
}

// HarnessAssistantStreamConfig holds the executable inputs for one
// already-approved assistant provider request.
type HarnessAssistantStreamConfig struct {
	Model            *ai.Model
	SystemPrompt     string
	Tools            []ai.Tool
	ThinkingLevel    string
	StreamOptions    AgentHarnessStreamOptions
	TransformContext func(requestContext struct {
		Messages     []agent.AgentMessage
		SystemPrompt string
	}, ctx Context) (struct {
		Messages     []agent.AgentMessage
		SystemPrompt string
	}, error)
	ToProviderMessages func(messages []agent.AgentMessage, ctx Context) []ai.Message
	BeforePayload      func(payload any, model *ai.Model, ctx Context) any
	AfterResponse      func(message *ai.AssistantMessage, metadata AssistantResponseMetadata, ctx Context) (*ai.AssistantMessage, error)
	Request            func(aiContext ai.Context, options ai.SimpleStreamOptions, ctx Context) *ai.AssistantMessageEventStream
	Observer           AssistantStreamObserver
}

func createRequestOptions(
	config *HarnessAssistantStreamConfig,
	captureMetadata func(AssistantResponseMetadata),
	ctx Context,
) ai.SimpleStreamOptions {
	options := config.StreamOptions
	simple := ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			Transport:        options.Transport,
			TimeoutMs:        options.TimeoutMs,
			MaxRetries:       options.MaxRetries,
			MaxRetryDelayMs:  options.MaxRetryDelayMs,
			Headers:          headerMapToProviderHeaders(options.Headers),
			Metadata:         options.Metadata,
			CacheRetention:   options.CacheRetention,
			Signal:           ctx.AbortSignal(),
			TelemetryContext: GetTelemetryContext(ctx),
		},
		Deferred: options.Deferred,
	}
	if config.ThinkingLevel != agent.ThinkingOff {
		level := config.ThinkingLevel
		simple.Reasoning = &level
	}
	if config.BeforePayload != nil {
		before := config.BeforePayload
		simple.OnPayload = func(payload any, model *ai.Model) any {
			return before(payload, model, ctx)
		}
	}
	simple.OnResponse = func(response ai.ProviderResponse, _ *ai.Model) {
		status := response.Status
		captureMetadata(AssistantResponseMetadata{Status: &status, Headers: response.Headers})
	}
	return simple
}

func headerMapToProviderHeaders(headers map[string]string) ai.ProviderHeaders {
	if headers == nil {
		return nil
	}
	out := ai.ProviderHeaders{}
	for k, v := range headers {
		value := v
		out[k] = &value
	}
	return out
}

func isUpdateEvent(event ai.AssistantMessageEvent) bool {
	t := event.EventType()
	return t != "start" && t != "done" && t != "error"
}

func eventPartialMessage(event ai.AssistantMessageEvent) *ai.AssistantMessage {
	switch t := event.(type) {
	case *ai.EventStart:
		return t.Partial
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

// ConsumeAssistantStream mirrors consumeAssistantStream: enforces exactly
// one start, updates after start, done-after-start; applies afterResponse
// (AbortRequested waits the cancellation then keeps the settled message);
// ends the observer.
func ConsumeAssistantStream(
	stream *ai.AssistantMessageEventStream,
	observer AssistantStreamObserver,
	afterResponse func(message *ai.AssistantMessage, ctx Context) (*ai.AssistantMessage, error),
	ctx Context,
) (*ai.AssistantMessage, error) {
	started := false
	for {
		event, ok := stream.Next()
		if !ok {
			break
		}
		switch e := event.(type) {
		case *ai.EventStart:
			if started {
				return nil, ToError("Assistant message stream emitted more than one start event")
			}
			started = true
			observer.Start(shallowCopyAssistant(e.Partial), e, ctx)
		default:
			if isUpdateEvent(event) {
				if !started {
					return nil, ToError("Assistant message stream emitted " + event.EventType() + " before start")
				}
				if partial := eventPartialMessage(event); partial != nil {
					observer.Update(shallowCopyAssistant(partial), event, ctx)
				}
			} else if done, isDone := e.(*ai.EventDone); isDone && !started {
				_ = done
				return nil, ToError("Assistant message stream emitted done before start")
			}
		}
	}

	settled := stream.Result()
	finalMessage := settled
	if afterResponse != nil {
		patched, err := afterResponse(settled, ctx)
		if err != nil {
			if abortRequested, ok := err.(*AbortRequested); ok {
				<-abortRequested.Cancellation
			} else {
				return nil, err
			}
		} else {
			finalMessage = patched
		}
	}
	observer.End(finalMessage, ctx)
	return finalMessage, nil
}

// StreamHarnessAssistant streams one assistant response without mutating
// the caller's message list.
func StreamHarnessAssistant(
	messages []agent.AgentMessage,
	config *HarnessAssistantStreamConfig,
	ctx Context,
) (*ai.AssistantMessage, error) {
	requestContext := struct {
		Messages     []agent.AgentMessage
		SystemPrompt string
	}{append([]agent.AgentMessage{}, messages...), config.SystemPrompt}
	if config.TransformContext != nil {
		transformed, err := config.TransformContext(requestContext, ctx)
		if err != nil {
			return nil, err
		}
		requestContext = transformed
	}

	providerMessages := config.ToProviderMessages(requestContext.Messages, ctx)
	aiContext := ai.Context{
		SystemPrompt: &requestContext.SystemPrompt,
		Messages:     providerMessages,
		Tools:        config.Tools,
	}

	var metadata AssistantResponseMetadata
	hasMetadata := false
	stream := config.Request(aiContext, createRequestOptions(config, func(next AssistantResponseMetadata) {
		metadata = next
		hasMetadata = true
	}, ctx), ctx)

	var afterResponse func(message *ai.AssistantMessage, ctx Context) (*ai.AssistantMessage, error)
	if config.AfterResponse != nil {
		hook := config.AfterResponse
		capturedMeta := metadata
		capturedHas := hasMetadata
		afterResponse = func(message *ai.AssistantMessage, afterCtx Context) (*ai.AssistantMessage, error) {
			_ = capturedHas
			return hook(message, capturedMeta, afterCtx)
		}
	}
	return ConsumeAssistantStream(stream, config.Observer, afterResponse, ctx)
}

// keep abort/telemetry references for parity with the TS import surface.
var (
	_ = abort.NewController
	_ telemetry.TelemetryContext
)
