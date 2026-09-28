package harness

// telemetry.go ports harness/telemetry.ts: the AI and harness telemetry
// schemas and the typed span starters.

import (
	"github.com/gladmo/openagent/telemetry"
)

// AiSpanNames/attribute keys mirror the schema surface the runtime uses.
const (
	AiSpanRequest = "pi.ai.request"
)

// aiTelemetrySchema mirrors AI_TELEMETRY_SCHEMA.
func aiTelemetrySchema() telemetry.TelemetrySchemaDefinition {
	return telemetry.TelemetrySchemaDefinition{
		Version: 1,
		Spans: map[string]telemetry.TelemetrySpanDefinition{
			AiSpanRequest: {
				Description: "One logical request to an AI provider",
				Parents:     telemetry.TelemetryParentDefinition{Kind: "any"},
				StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
					"pi.ai.operation": {
						TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
							Type: "string", Values: []any{"stream", "fetch_deferred", "cancel_deferred", "generate_images"},
							Description: "Logical provider operation",
						},
						Required: true,
					},
					"pi.ai.provider": {
						TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{Type: "string", Description: "Selected provider id"},
						Required:                     true,
					},
					"pi.ai.model": {
						TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{Type: "string", Description: "Requested model id"},
						Required:                     true,
					},
					"pi.ai.api": {
						TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{Type: "string", Description: "Provider API id"},
						Required:                     true,
					},
					"pi.ai.streaming": {
						TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{Type: "boolean", Description: "Whether this operation returns a stream"},
						Required:                     true,
					},
					"pi.ai.deferred": {
						TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{Type: "boolean", Description: "Whether the operation requests or participates in deferred execution"},
					},
				},
				EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{
					"pi.ai.response.model":                {Type: "string", Description: "Concrete response model"},
					"pi.ai.response.id":                   {Type: "string", Cardinality: "high", Description: "Provider response id"},
					"pi.ai.response.stop_reason":          {Type: "string", Values: []any{"stop", "length", "tool_use", "error", "aborted", "deferred"}, Description: "Normalized terminal response reason"},
					"pi.ai.http.status_code":              {Type: "number", Description: "Final HTTP status"},
					"pi.ai.usage.input_tokens":            {Type: "number", Description: "Reported input tokens"},
					"pi.ai.usage.output_tokens":           {Type: "number", Description: "Reported output tokens"},
					"pi.ai.usage.cache_read_tokens":       {Type: "number", Description: "Reported cache-read tokens"},
					"pi.ai.usage.cache_write_tokens":      {Type: "number", Description: "Reported cache-write tokens"},
					"pi.ai.usage.reasoning_tokens":        {Type: "number", Description: "Reported reasoning tokens"},
					"pi.ai.usage.total_tokens":            {Type: "number", Description: "Reported total tokens"},
					"pi.ai.usage.cost":                    {Type: "number", Description: "Reported total cost"},
					"pi.ai.stream.chunk_count":            {Type: "number", Description: "Streamed update chunk count"},
					"pi.ai.stream.time_to_first_chunk_ms": {Type: "number", Description: "Elapsed milliseconds to first update chunk"},
					"pi.ai.error.type":                    {Type: "string", Cardinality: "low", Description: "Provider or transport error class"},
				},
				Status: telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The operation throws or returns an error result"},
			},
		},
	}
}

// Harness span names.
const (
	HarnessSpanRun          = "pi.harness.run"
	HarnessSpanCompaction   = "pi.harness.compaction"
	HarnessSpanNavigation   = "pi.harness.navigation"
	HarnessSpanCheckpoint   = "pi.harness.checkpoint"
	HarnessSpanTurn         = "pi.harness.turn"
	HarnessSpanStep         = "pi.harness.step"
	HarnessSpanTool         = "pi.harness.tool"
	HarnessSpanHook         = "pi.harness.hook"
	HarnessSpanSleep        = "pi.harness.sleep"
	HarnessSpanEventHandler = "pi.harness.event_handler"
	SessionSpanWrite        = "pi.session.write"
)

func strDef(description string) telemetry.TelemetryAttributeDefinition {
	return telemetry.TelemetryAttributeDefinition{Type: "string", Description: description}
}

func numDef(description string) telemetry.TelemetryAttributeDefinition {
	return telemetry.TelemetryAttributeDefinition{Type: "number", Description: description}
}

func boolDef(description string) telemetry.TelemetryAttributeDefinition {
	return telemetry.TelemetryAttributeDefinition{Type: "boolean", Description: description}
}

func highCardinality(def telemetry.TelemetryAttributeDefinition) telemetry.TelemetryAttributeDefinition {
	def.Cardinality = "high"
	return def
}

func required(def telemetry.TelemetryAttributeDefinition) telemetry.TelemetryStartAttributeDefinition {
	return telemetry.TelemetryStartAttributeDefinition{TelemetryAttributeDefinition: def, Required: true}
}

func operationStartAttributes(kind string, kindValues []any) map[string]telemetry.TelemetryStartAttributeDefinition {
	return map[string]telemetry.TelemetryStartAttributeDefinition{
		"pi.session.id":         required(highCardinality(strDef("Session id"))),
		"pi.lane.name":          required(highCardinality(strDef("Lane name"))),
		"pi.operation.id":       required(highCardinality(strDef("Durable operation id"))),
		"pi.operation.recovery": required(boolDef("Whether this invocation resumes durable work")),
		"pi.operation.kind": {
			TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{Type: "string", Values: kindValues, Description: kind + " operation kind"},
			Required:                     true,
		},
	}
}

func operationErrorEndAttributes() map[string]telemetry.TelemetryAttributeDefinition {
	return map[string]telemetry.TelemetryAttributeDefinition{
		"pi.error.code": {Type: "string", Cardinality: "low", Description: "Stable operation error code"},
		"pi.error.type": {Type: "string", Cardinality: "low", Description: "Low-cardinality operation error class"},
	}
}

// harnessTelemetrySchema mirrors HARNESS_TELEMETRY_SCHEMA.
func harnessTelemetrySchema() telemetry.TelemetrySchemaDefinition {
	turnParents := telemetry.TelemetryParentDefinition{Kind: "spans", Spans: []string{HarnessSpanRun}}
	stepParents := telemetry.TelemetryParentDefinition{Kind: "spans", Spans: []string{HarnessSpanTurn, HarnessSpanCheckpoint, HarnessSpanCompaction, HarnessSpanNavigation}}
	sleepParents := telemetry.TelemetryParentDefinition{Kind: "spans", Spans: []string{HarnessSpanRun, HarnessSpanCompaction, HarnessSpanNavigation, HarnessSpanTurn, HarnessSpanCheckpoint}}
	toolParents := telemetry.TelemetryParentDefinition{Kind: "spans", Spans: []string{HarnessSpanTurn, HarnessSpanRun}}
	root := telemetry.TelemetryParentDefinition{Kind: "root_or_external"}
	anyParent := telemetry.TelemetryParentDefinition{Kind: "any"}
	spans := map[string]telemetry.TelemetrySpanDefinition{
		HarnessSpanRun: {
			Description: "One admitted in-process run invocation", Parents: root,
			StartAttributes: operationStartAttributes("Run", []any{"run"}),
			EndAttributes: func() map[string]telemetry.TelemetryAttributeDefinition {
				attrs := operationErrorEndAttributes()
				attrs["pi.operation.outcome"] = telemetry.TelemetryAttributeDefinition{Type: "string", Values: []any{"completed", "aborted", "failed", "suspended"}, Description: "Run invocation outcome"}
				return attrs
			}(),
			Status: telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The run fails or throws"},
		},
		HarnessSpanCompaction: {
			Description: "One admitted in-process manual compaction invocation", Parents: root,
			StartAttributes: operationStartAttributes("Compaction", []any{"compaction"}),
			EndAttributes: func() map[string]telemetry.TelemetryAttributeDefinition {
				attrs := operationErrorEndAttributes()
				attrs["pi.operation.outcome"] = telemetry.TelemetryAttributeDefinition{Type: "string", Values: []any{"completed", "declined", "aborted", "failed"}, Description: "Compaction invocation outcome"}
				return attrs
			}(),
			Status: telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The compaction fails or throws"},
		},
		HarnessSpanNavigation: {
			Description: "One admitted in-process navigation invocation", Parents: root,
			StartAttributes: operationStartAttributes("Navigation", []any{"navigation"}),
			EndAttributes: func() map[string]telemetry.TelemetryAttributeDefinition {
				attrs := operationErrorEndAttributes()
				attrs["pi.operation.outcome"] = telemetry.TelemetryAttributeDefinition{Type: "string", Values: []any{"completed", "declined", "aborted", "failed"}, Description: "Navigation invocation outcome"}
				return attrs
			}(),
			Status: telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The navigation fails or throws"},
		},
		HarnessSpanCheckpoint: {
			Description: "One run checkpoint",
			Parents:     turnParents,
			StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
				"pi.lane.name":    required(highCardinality(strDef("Lane name"))),
				"pi.operation.id": required(highCardinality(strDef("Durable operation id"))),
				"pi.checkpoint.kind": {
					TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{Type: "string", Values: []any{"normal", "abort_reconcile"}, Description: "Checkpoint purpose"},
					Required:                     true,
				},
			},
			EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{},
			Status:        telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "Checkpoint work throws"},
		},
		HarnessSpanTurn: {
			Description: "One assistant response and its tool batch",
			Parents:     turnParents,
			StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
				"pi.lane.name":    required(highCardinality(strDef("Lane name"))),
				"pi.operation.id": required(highCardinality(strDef("Durable operation id"))),
				"pi.turn.id":      required(highCardinality(strDef("Invocation-local turn id"))),
			},
			EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{
				"pi.turn.stop_reason": {Type: "string", Values: []any{"stop", "length", "tool_use", "error", "aborted", "deferred"}, Description: "Terminal response reason"},
			},
			Status: telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The turn fails or throws"},
		},
		HarnessSpanStep: {
			Description: "One durable retry attempt",
			Parents:     stepParents,
			StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
				"pi.lane.name":         required(highCardinality(strDef("Lane name"))),
				"pi.operation.id":      required(highCardinality(strDef("Durable operation id"))),
				"pi.step.kind":         required(strDef("Retryable step kind")),
				"pi.step.attempt":      required(numDef("One-based durable attempt number")),
				"pi.compaction.reason": requiredDef(strDef("Compaction trigger"), true),
			},
			EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{
				"pi.step.outcome": {Type: "string", Values: []any{"completed", "aborted", "failed"}, Description: "Attempt outcome"},
			},
			Status: telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The attempt fails or throws"},
		},
		HarnessSpanTool: {
			Description: "One raw phase-2 tool execution",
			Parents:     toolParents,
			StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
				"pi.lane.name":     required(highCardinality(strDef("Lane name"))),
				"pi.operation.id":  required(highCardinality(strDef("Durable operation id"))),
				"pi.turn.id":       required(highCardinality(strDef("Invocation-local live turn id"))),
				"pi.tool.name":     required(highCardinality(strDef("Tool name"))),
				"pi.tool.call_id":  required(highCardinality(strDef("Tool call id"))),
				"pi.tool.replay":   required(strDef("Declared replay policy")),
				"pi.tool.recovery": required(boolDef("Whether this is recovery execution")),
			},
			EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{
				"pi.tool.is_error": boolDef("Whether raw phase-2 execution returned an error"),
			},
			Status: telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The tool execution throws"},
		},
		HarnessSpanHook: {
			Description: "One registered hook handler invocation",
			Parents:     anyParent,
			StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
				"pi.lane.name":            required(highCardinality(strDef("Lane name"))),
				"pi.operation.id":         requiredDef(highCardinality(strDef("Durable operation id when accepted")), false),
				"pi.hook.name":            required(strDef("Hook name")),
				"pi.hook.registration_id": requiredDef(highCardinality(strDef("Optional hook registration metadata")), false),
			},
			EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{
				"pi.hook.outcome": {Type: "string", Values: []any{"completed", "blocked", "failed"}, Description: "Handler outcome"},
			},
			Status: telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The hook handler throws"},
		},
		HarnessSpanSleep: {
			Description: "One bounded retry backoff sleep",
			Parents:     sleepParents,
			StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
				"pi.lane.name":      required(highCardinality(strDef("Lane name"))),
				"pi.operation.id":   required(highCardinality(strDef("Durable operation id"))),
				"pi.retry.attempt":  required(numDef("One-based retry attempt")),
				"pi.retry.delay_ms": required(numDef("Computed backoff delay")),
			},
			EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{},
			Status:        telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The sleep throws"},
		},
		HarnessSpanEventHandler: {
			Description: "One event handler invocation",
			Parents:     anyParent,
			StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
				"pi.event.type": required(strDef("Delivered event type")),
			},
			EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{
				"pi.event.handler_error": {Type: "string", Cardinality: "low", Description: "Handler error text"},
			},
			Status: telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The handler throws"},
		},
		SessionSpanWrite: {
			Description: "One session storage commit",
			Parents:     anyParent,
			StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
				"pi.session.id":  required(highCardinality(strDef("Session id"))),
				"pi.write.count": required(numDef("Number of committed writes")),
			},
			EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{},
			Status:        telemetry.TelemetrySpanStatusDefinition{Default: "ok", ErrorWhen: "The commit throws"},
		},
	}
	return telemetry.TelemetrySchemaDefinition{Version: 1, Spans: spans}
}

func requiredDef(def telemetry.TelemetryAttributeDefinition, req bool) telemetry.TelemetryStartAttributeDefinition {
	return telemetry.TelemetryStartAttributeDefinition{TelemetryAttributeDefinition: def, Required: req}
}

// AITelemetrySchema / HarnessTelemetrySchema / AgentTelemetrySchemas are
// the exported mirrors (materialized once).
var (
	AITelemetrySchema      = aiTelemetrySchema()
	HarnessTelemetrySchema = harnessTelemetrySchema()
	AgentTelemetrySchemas  = []telemetry.TelemetrySchemaDefinition{AITelemetrySchema, HarnessTelemetrySchema}
)

// StartAiSpan mirrors startAiSpan: starts a pi.ai.request span under the
// context's telemetry parent and passes a context with the span attached.
func StartAiSpan[T any](
	name string,
	attributes telemetry.SpanAttributes,
	ctx Context,
	callback func(span telemetry.TelemetrySpan, ctx Context) (T, error),
) (T, error) {
	return telemetry.StartSpan(GetTelemetryContext(ctx), telemetry.SpanOptions{Name: name, Attributes: attributes}, func(span telemetry.TelemetrySpan) (T, error) {
		return callback(span, WithTelemetryContext(span, ctx))
	})
}

// StartHarnessSpan mirrors startHarnessSpan.
func StartHarnessSpan[T any](
	name string,
	attributes telemetry.SpanAttributes,
	ctx Context,
	callback func(span telemetry.TelemetrySpan, ctx Context) (T, error),
) (T, error) {
	return telemetry.StartSpan(GetTelemetryContext(ctx), telemetry.SpanOptions{Name: name, Attributes: attributes}, func(span telemetry.TelemetrySpan) (T, error) {
		return callback(span, WithTelemetryContext(span, ctx))
	})
}
