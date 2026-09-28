package harness

// utils_usage.go ports harness/utils/usage.ts, plus the harness Context
// helpers (context.ts) and config validators (config.ts).

import (
	"fmt"

	"github.com/gladmo/openagent/ai"
	chordcontext "github.com/gladmo/openagent/chord/context"
	"github.com/gladmo/openagent/telemetry"
)

// EmptyUsage mirrors emptyUsage().
func EmptyUsage() ai.Usage { return ai.Usage{} }

// AddUsage mirrors addUsage(): optional fields stay absent when both sides
// are absent.
func AddUsage(left, right ai.Usage) ai.Usage {
	out := ai.Usage{
		Input:       left.Input + right.Input,
		Output:      left.Output + right.Output,
		CacheRead:   left.CacheRead + right.CacheRead,
		CacheWrite:  left.CacheWrite + right.CacheWrite,
		TotalTokens: left.TotalTokens + right.TotalTokens,
		Cost: ai.UsageCost{
			Input:      left.Cost.Input + right.Cost.Input,
			Output:     left.Cost.Output + right.Cost.Output,
			CacheRead:  left.Cost.CacheRead + right.Cost.CacheRead,
			CacheWrite: left.Cost.CacheWrite + right.Cost.CacheWrite,
			Total:      left.Cost.Total + right.Cost.Total,
		},
	}
	if left.CacheWrite1h != nil || right.CacheWrite1h != nil {
		sum := derefFloat(left.CacheWrite1h) + derefFloat(right.CacheWrite1h)
		out.CacheWrite1h = &sum
	}
	if left.Reasoning != nil || right.Reasoning != nil {
		sum := derefFloat(left.Reasoning) + derefFloat(right.Reasoning)
		out.Reasoning = &sum
	}
	return out
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// ---------------------------------------------------------------------------
// context.ts re-exports + telemetry-context key
// ---------------------------------------------------------------------------

// Context aliases the chord Context.
type Context = chordcontext.Context

// BackgroundContext / TodoContext re-exports.
var (
	BackgroundContext  = chordcontext.BackgroundContext
	TodoContext        = chordcontext.TodoContext
	WithAbortSignal    = chordcontext.WithAbortSignal
	WithoutAbortSignal = chordcontext.WithoutAbortSignal
	WithCancel         = chordcontext.WithCancel
)

var telemetryContextKey = chordcontext.CreateContextKey[telemetry.TelemetryContext]("pi.harness.telemetryContext")

// GetTelemetryContext reads the harness telemetry parent context.
func GetTelemetryContext(ctx Context) telemetry.TelemetryContext {
	if v, ok := chordcontext.Value(ctx, telemetryContextKey); ok && v != nil {
		return v
	}
	return telemetry.NOOP_TELEMETRY_CONTEXT
}

// WithTelemetryContext derives a context carrying a telemetry parent.
func WithTelemetryContext(tel telemetry.TelemetryContext, parent Context) Context {
	return chordcontext.WithContextValue(telemetryContextKey, tel, parent)
}

// ---------------------------------------------------------------------------
// config.ts
// ---------------------------------------------------------------------------

// DefaultRetryPolicy mirrors DEFAULT_RETRY_POLICY.
func DefaultRetryPolicy() ai.RetryPolicy {
	baseDelay := 1000.0
	return ai.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: baseDelay}
}

// ValidateToolNames throws (panics) on duplicates, mirroring validateToolNames.
func ValidateToolNames(names []string) error {
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return fmt.Errorf("Duplicate tool name: %s", name)
		}
		seen[name] = true
	}
	return nil
}

// ValidateRetryPolicy mirrors validateRetryPolicy.
func ValidateRetryPolicy(policy ai.RetryPolicy) error {
	if policy.MaxRetries < 0 {
		return fmt.Errorf("maxRetries must be >= 0")
	}
	if policy.BaseDelayMs < 0 {
		return fmt.Errorf("baseDelayMs must be >= 0")
	}
	if policy.MaxAgentDelayMs != nil && *policy.MaxAgentDelayMs < 0 {
		return fmt.Errorf("maxAgentDelayMs must be >= 0")
	}
	return nil
}

// CompactionSettings mirror the TS CompactionSettings subset used by config
// validation.
type CompactionSettings struct {
	Enabled          bool
	ReserveTokens    float64
	KeepRecentTokens float64
}

// ValidateCompactionSettings mirrors validateCompactionSettings.
func ValidateCompactionSettings(settings CompactionSettings) error {
	if settings.ReserveTokens < 0 {
		return fmt.Errorf("reserveTokens must be >= 0")
	}
	if settings.KeepRecentTokens < 0 {
		return fmt.Errorf("keepRecentTokens must be >= 0")
	}
	return nil
}
