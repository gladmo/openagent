package ai

// Port of pi-ai telemetry.ts (2ec55d808): the AI telemetry schema and the
// request observer that wraps Models streams without provider changes.
//
// Go mapping notes:
//   - The TS WeakMap instrument cache becomes a sync.Map keyed by the
//     metrics adapter (adapters are shared singletons or pointers).
//   - UTF-16 `.length` accounting becomes unicode/utf16.RuneCountInString.
//   - The lazyStream observer seam becomes forwardObservedStream, which
//     forwards the provider stream through the observer into an outer
//     stream.

import (
	"sync"
	"time"
	"unicode/utf16"

	"github.com/gladmo/openagent/telemetry"
)

// utf16Len counts UTF-16 code units (the TS string .length).
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

var aiTelemetryProviderAttr = telemetry.TelemetryStartAttributeDefinition{
	TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
		Description: "Provider identifier.",
		Type:        telemetry.AttrString,
	},
	Required: true,
}

var aiTelemetryModelAttr = telemetry.TelemetryStartAttributeDefinition{
	TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
		Description: "Requested model identifier.",
		Type:        telemetry.AttrString,
	},
	Required: true,
}

var aiTelemetryStopAttr = telemetry.TelemetryAttributeDefinition{
	Description: "Normalized stop reason.",
	Type:        telemetry.AttrString,
	Values:      []string{StopPending, StopStop, StopLength, StopToolUse, StopError, StopAborted, StopDeferred},
}

func aiTelemetryRequestSpan(description string) telemetry.TelemetrySpanDefinition {
	return telemetry.TelemetrySpanDefinition{
		Description: description,
		Parents:     telemetry.TelemetryParentDefinition{Kind: "any"},
		StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
			"provider": aiTelemetryProviderAttr,
			"api": {
				TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
					Description: "API identifier.",
					Type:        telemetry.AttrString,
				},
				Required: true,
			},
			"model": aiTelemetryModelAttr,
			"reasoning": {
				TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
					Description: "Scalar requested reasoning level.",
					Type:        telemetry.AttrString,
				},
				Required: false,
			},
		},
		EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{
			"stop":              aiTelemetryStopAttr,
			"usage.input":       {Description: "Uncached input tokens.", Type: telemetry.AttrNumber},
			"usage.output":      {Description: "Output tokens.", Type: telemetry.AttrNumber},
			"usage.cache_read":  {Description: "Cache-read tokens.", Type: telemetry.AttrNumber},
			"usage.cache_write": {Description: "Cache-write tokens.", Type: telemetry.AttrNumber},
			"cost.total":        {Description: "Total request cost in USD.", Type: telemetry.AttrNumber},
			"response.id":       {Description: "Provider response identifier.", Type: telemetry.AttrString, Cardinality: "high"},
		},
		Events: map[string]telemetry.TelemetryEventDefinition{
			"pi.ai.first_chunk": {Description: "First normalized stream event delivered.", Attributes: map[string]telemetry.TelemetryEventAttributeDefinition{}},
			"pi.ai.block": {
				Description: "A completed content block; content is never recorded.",
				Attributes: map[string]telemetry.TelemetryEventAttributeDefinition{
					"type": {
						TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
							Description: "Block kind.",
							Type:        telemetry.AttrString,
							Values:      []string{"text", "thinking", "tool_call"},
						},
						Required: true,
					},
					"index": {
						TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
							Description: "Content block index.",
							Type:        telemetry.AttrNumber,
						},
						Required: true,
					},
					"chars": {
						TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
							Description: "UTF-16 character count.",
							Type:        telemetry.AttrNumber,
						},
						Required: true,
					},
					"tool": {
						TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
							Description: "Tool name, for tool-call blocks only.",
							Type:        telemetry.AttrString,
						},
						Required: false,
					},
				},
			},
		},
		Status: telemetry.TelemetrySpanStatusDefinition{
			Default:   "ok",
			ErrorWhen: "Stop reason is error: provider_error with at most 500 error-message characters. Aborted stays ok.",
		},
	}
}

// AiTelemetrySchema is the pi-owned request metadata schema with bounded
// error status, events, and low-cardinality metrics.
var AiTelemetrySchema = telemetry.DefineTelemetrySchema(telemetry.TelemetrySchemaDefinition{
	Version: 1,
	Spans: map[string]telemetry.TelemetrySpanDefinition{
		"pi.ai.request": aiTelemetryRequestSpan("One logical model request, including authentication and provider setup."),
		"pi.ai.poll":    aiTelemetryRequestSpan("One deferred-response poll, not the deferred request's lifetime."),
	},
	Metrics: map[string]telemetry.TelemetryMetricDefinition{
		"pi.ai.request.duration": {
			Kind:        "histogram",
			Description: "Logical request or poll duration.",
			Unit:        "ms",
			Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{
				"provider": {TelemetryAttributeDefinition: aiTelemetryProviderAttr.TelemetryAttributeDefinition, Required: true},
				"model":    {TelemetryAttributeDefinition: aiTelemetryModelAttr.TelemetryAttributeDefinition, Required: true},
				"stop":     {TelemetryAttributeDefinition: aiTelemetryStopAttr, Required: true},
			},
		},
		"pi.ai.first_chunk.duration": {
			Kind:        "histogram",
			Description: "Time to the first normalized stream event.",
			Unit:        "ms",
			Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{
				"provider": {TelemetryAttributeDefinition: aiTelemetryProviderAttr.TelemetryAttributeDefinition, Required: true},
				"model":    {TelemetryAttributeDefinition: aiTelemetryModelAttr.TelemetryAttributeDefinition, Required: true},
			},
		},
		"pi.ai.tokens": {
			Kind:        "counter",
			Description: "Reported model token usage; pending and deferred polls are excluded.",
			Unit:        "token",
			Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{
				"provider": {TelemetryAttributeDefinition: aiTelemetryProviderAttr.TelemetryAttributeDefinition, Required: true},
				"model":    {TelemetryAttributeDefinition: aiTelemetryModelAttr.TelemetryAttributeDefinition, Required: true},
				"kind": {
					TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
						Description: "Token usage category.",
						Type:        telemetry.AttrString,
						Values:      []string{"input", "output", "cache_read", "cache_write"},
					},
					Required: true,
				},
			},
		},
		"pi.ai.cost": {
			Kind:        "counter",
			Description: "Reported model cost; pending and deferred polls are excluded.",
			Unit:        "USD",
			Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{
				"provider": {TelemetryAttributeDefinition: aiTelemetryProviderAttr.TelemetryAttributeDefinition, Required: true},
				"model":    {TelemetryAttributeDefinition: aiTelemetryModelAttr.TelemetryAttributeDefinition, Required: true},
			},
		},
	},
})

type aiInstruments struct {
	requestDuration    telemetry.TelemetryHistogram
	firstChunkDuration telemetry.TelemetryHistogram
	tokens             telemetry.TelemetryCounter
	cost               telemetry.TelemetryCounter
}

var aiInstrumentCache sync.Map // telemetry.TelemetryMetrics -> aiInstruments

func aiInstrumentsFor(adapter telemetry.TelemetryMetrics) aiInstruments {
	if cached, ok := aiInstrumentCache.Load(adapter); ok {
		return cached.(aiInstruments)
	}
	metrics := telemetry.CreateTypedMetrics(adapter, AiTelemetrySchema)
	instruments := aiInstruments{
		requestDuration:    metrics.Histogram("pi.ai.request.duration"),
		firstChunkDuration: metrics.Histogram("pi.ai.first_chunk.duration"),
		tokens:             metrics.Counter("pi.ai.tokens"),
		cost:               metrics.Counter("pi.ai.cost"),
	}
	actual, loaded := aiInstrumentCache.LoadOrStore(adapter, instruments)
	if loaded {
		return actual.(aiInstruments)
	}
	return instruments
}

// modelRequestObserver mirrors observeModelRequest's observer: it records
// the request span, block events, and usage/cost metrics.
type modelRequestObserver struct {
	attributes  telemetry.SpanAttributes
	adapter     telemetry.TelemetryMetrics
	instruments aiInstruments
	started     time.Time
	poll        bool
	spanReady   chan telemetry.TelemetrySpan
	finished    chan struct{}
	span        telemetry.TelemetrySpan
	first       bool
	toolChars   map[int]int
	toolCharsMu sync.Mutex
}

// newModelRequestObserver starts the request span on the parent and returns
// the observer for the stream forwarding path. The span settles when
// onResult runs.
func newModelRequestObserver(requestedModel *Model, parent telemetry.TelemetrySpan, reasoning string, poll bool) *modelRequestObserver {
	attributes := telemetry.SpanAttributes{"provider": requestedModel.Provider, "model": requestedModel.ID}
	if parent == nil {
		parent = telemetry.NOOP_TELEMETRY_CONTEXT
	}
	adapter := parent.Metrics()
	observer := &modelRequestObserver{
		attributes:  attributes,
		adapter:     adapter,
		instruments: aiInstrumentsFor(adapter),
		started:     time.Now(),
		poll:        poll,
		spanReady:   make(chan telemetry.TelemetrySpan, 1),
		finished:    make(chan struct{}),
		first:       true,
		toolChars:   map[int]int{},
	}
	startAttributes := telemetry.SpanAttributes{
		"provider": requestedModel.Provider,
		"api":      requestedModel.API,
		"model":    requestedModel.ID,
	}
	if reasoning != "" {
		startAttributes["reasoning"] = reasoning
	}
	finished := observer.finished
	name := "pi.ai.request"
	if poll {
		name = "pi.ai.poll"
	}
	go func() {
		_, _ = telemetry.StartSpan(parent, telemetry.SpanOptions{Name: name, Attributes: startAttributes}, func(span telemetry.TelemetrySpan) (any, error) {
			observer.spanReady <- span
			<-finished
			return nil, nil
		})
	}()
	return observer
}

func (o *modelRequestObserver) activeSpan() telemetry.TelemetrySpan {
	if o.span == nil {
		o.span = <-o.spanReady
	}
	return o.span
}

func (o *modelRequestObserver) onEvent(event AssistantMessageEvent) {
	span := o.activeSpan()
	if o.first {
		o.first = false
		span.AddEvent("pi.ai.first_chunk", nil)
		o.instruments.firstChunkDuration.Record(float64(time.Since(o.started))/float64(time.Millisecond), o.attributes, span)
	}
	switch typed := event.(type) {
	case *EventToolCallDelta:
		o.toolCharsMu.Lock()
		o.toolChars[typed.ContentIndex] += utf16Len(typed.Delta)
		o.toolCharsMu.Unlock()
	case *EventTextEnd:
		span.AddEvent("pi.ai.block", telemetry.SpanAttributes{
			"type":  "text",
			"index": float64(typed.ContentIndex),
			"chars": float64(utf16Len(typed.Content)),
		})
	case *EventThinkingEnd:
		span.AddEvent("pi.ai.block", telemetry.SpanAttributes{
			"type":  "thinking",
			"index": float64(typed.ContentIndex),
			"chars": float64(utf16Len(typed.Content)),
		})
	case *EventToolCallEnd:
		o.toolCharsMu.Lock()
		chars := o.toolChars[typed.ContentIndex]
		delete(o.toolChars, typed.ContentIndex)
		o.toolCharsMu.Unlock()
		span.AddEvent("pi.ai.block", telemetry.SpanAttributes{
			"type":  "tool_call",
			"index": float64(typed.ContentIndex),
			"chars": float64(chars),
			"tool":  typed.ToolCall.Name,
		})
	}
}

func (o *modelRequestObserver) onResult(message *AssistantMessage) {
	span := o.activeSpan()
	if message != nil {
		span.SetAttributes(telemetry.SpanAttributes{
			"stop":              message.StopReason,
			"usage.input":       message.Usage.Input,
			"usage.output":      message.Usage.Output,
			"usage.cache_read":  message.Usage.CacheRead,
			"usage.cache_write": message.Usage.CacheWrite,
			"cost.total":        message.Usage.Cost.Total,
			"response.id":       derefOrEmpty(message.ResponseID),
		})
		if message.StopReason == StopError {
			bounded := ""
			if message.ErrorMessage != nil {
				bounded = truncateRunes(*message.ErrorMessage, 500)
			}
			span.SetStatus(telemetry.StatusError("provider_error", bounded))
		}
		o.instruments.requestDuration.Record(float64(time.Since(o.started))/float64(time.Millisecond), telemetry.SpanAttributes{
			"provider": o.attributes["provider"],
			"model":    o.attributes["model"],
			"stop":     message.StopReason,
		}, span)
		if !o.poll || (message.StopReason != StopPending && message.StopReason != StopDeferred) {
			o.instruments.TokensAdd(o.attributes, message)
			o.instruments.cost.Add(message.Usage.Cost.Total, o.attributes)
		}
	}
	close(o.finished)
}

// TokensAdd records the four token-usage counters.
func (i aiInstruments) TokensAdd(attributes telemetry.SpanAttributes, message *AssistantMessage) {
	add := func(value float64, kind string) {
		i.tokens.Add(value, telemetry.SpanAttributes{
			"provider": attributes["provider"],
			"model":    attributes["model"],
			"kind":     kind,
		})
	}
	add(message.Usage.Input, "input")
	add(message.Usage.Output, "output")
	add(message.Usage.CacheRead, "cache_read")
	add(message.Usage.CacheWrite, "cache_write")
}

func derefOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func truncateRunes(value string, limit int) string {
	if utf16Len(value) <= limit {
		return value
	}
	runes := []rune(value)
	// UTF-16 code units: astral runes count as two; walk until the budget
	// is spent so the cut matches TS slice semantics.
	units := 0
	for i, r := range runes {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > limit {
			return string(runes[:i])
		}
		units += width
	}
	return string(runes)
}

// forwardObservedStream forwards the provider stream through the observer
// into a fresh outer stream (the lazyStream forwarding path).
func forwardObservedStream(observer *modelRequestObserver, inner *AssistantMessageEventStream) *AssistantMessageEventStream {
	outer := NewAssistantMessageEventStream()
	go func() {
		var result *AssistantMessage
		for {
			event, ok := inner.Next()
			if !ok {
				break
			}
			observer.onEvent(event)
			outer.Push(event)
			switch typed := event.(type) {
			case *EventDone:
				result = typed.Message
			case *EventError:
				result = typed.Error
			}
		}
		if result == nil && inner.Done() {
			result = inner.Result()
		}
		observer.onResult(result)
		if result != nil {
			outer.End(result)
		} else {
			outer.End()
		}
	}()
	return outer
}

// observeStream wraps a provider stream with the request observer.
func observeStream(model *Model, parent telemetry.TelemetrySpan, reasoning string, poll bool, inner *AssistantMessageEventStream) *AssistantMessageEventStream {
	return forwardObservedStream(newModelRequestObserver(model, parent, reasoning, poll), inner)
}

// observeErrorStream wraps a setup-failure stream with the observer so the
// span records the failure (the TS lazyStream catch path).
func observeErrorStream(model *Model, parent telemetry.TelemetrySpan, reasoning string, poll bool, err error) *AssistantMessageEventStream {
	return forwardObservedStream(newModelRequestObserver(model, parent, reasoning, poll), errorStream(err))
}
