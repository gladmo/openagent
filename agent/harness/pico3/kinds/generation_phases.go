package kinds

// generation_phases.go ports harness/pico3/kinds/generation.ts's phase
// model and pure decisions: the checkpoint shape (prepared/requesting/
// retrying/deferred carrying the immutable Prep), the failure completions,
// the display-assistant entry shape, and the config defaults.

import (
	"github.com/gladmo/openagent/agent/harness/pico3"
	"github.com/gladmo/openagent/jsonx"
)

// GenerationKindName identifies the kind.
const GenerationKindName = "pi.generation"

// Generation phases.
const (
	GenPhasePrepared   = "prepared"
	GenPhaseRequesting = "requesting"
	GenPhaseRetrying   = "retrying"
	GenPhaseDeferred   = "deferred"
)

// GenerationConfigDefaults mirrors generationConfig.
func GenerationConfigDefaults() map[string]any {
	return map[string]any{
		"thinkingLevel": "off",
		"selectedTools": []any{},
		"profile":       "default",
		"retry":         jsonx.ObjFrom("enabled", true, "maxRetries", float64(3), "baseDelayMs", float64(2000), "maxAgentDelayMs", float64(60000)),
	}
}

// GenerationPrepFields reads the Prep slice of a checkpoint.
type GenerationPrepFields struct {
	Cutoff          int64
	System          *int64
	Model           *jsonx.Obj
	ThinkingLevel   string
	Tools           []string
	RetryEnabled    bool
	MaxRetries      int
	BaseDelayMS     float64
	MaxAgentDelayMS *float64
	Attempt         float64
}

// GenerationCheckpointFields reads the full checkpoint.
type GenerationCheckpointFields struct {
	Phase     string
	Prep      GenerationPrepFields
	UntilMS   float64
	LastError string
	// Deferred fields.
	Handle *jsonx.Obj
	PollAt float64
}

// ParseGenerationCheckpoint reads one checkpoint.
func ParseGenerationCheckpoint(checkpoint pico3.Checkpoint) GenerationCheckpointFields {
	fields := GenerationCheckpointFields{}
	if checkpoint == nil {
		return fields
	}
	if v, ok := checkpoint.Get("phase"); ok {
		if s, ok := v.(string); ok {
			fields.Phase = s
		}
	}
	prep := &fields.Prep
	if v, ok := checkpoint.Get("cutoff"); ok {
		if f, ok := v.(float64); ok {
			prep.Cutoff = int64(f)
		}
	}
	if v, ok := checkpoint.Get("system"); ok {
		if f, ok := v.(float64); ok {
			id := int64(f)
			prep.System = &id
		}
	}
	if v, ok := checkpoint.Get("model"); ok {
		if obj, ok := v.(*jsonx.Obj); ok {
			prep.Model = obj
		}
	}
	if v, ok := checkpoint.Get("thinkingLevel"); ok {
		if s, ok := v.(string); ok {
			prep.ThinkingLevel = s
		}
	}
	if v, ok := checkpoint.Get("tools"); ok {
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					prep.Tools = append(prep.Tools, s)
				}
			}
		}
	}
	if v, ok := checkpoint.Get("retry"); ok {
		if obj, ok := v.(*jsonx.Obj); ok {
			if v, ok := obj.Get("enabled"); ok {
				prep.RetryEnabled = v == true
			}
			if v, ok := obj.Get("maxRetries"); ok {
				if f, ok := v.(float64); ok {
					prep.MaxRetries = int(f)
				}
			}
			if v, ok := obj.Get("baseDelayMs"); ok {
				if f, ok := v.(float64); ok {
					prep.BaseDelayMS = f
				}
			}
			if v, ok := obj.Get("maxAgentDelayMs"); ok {
				if f, ok := v.(float64); ok {
					maxDelay := f
					prep.MaxAgentDelayMS = &maxDelay
				}
			}
		}
	}
	if v, ok := checkpoint.Get("attempt"); ok {
		if f, ok := v.(float64); ok {
			prep.Attempt = f
		}
	}
	if v, ok := checkpoint.Get("untilMs"); ok {
		if f, ok := v.(float64); ok {
			fields.UntilMS = f
		}
	}
	if v, ok := checkpoint.Get("lastError"); ok {
		if s, ok := v.(string); ok {
			fields.LastError = s
		}
	}
	if v, ok := checkpoint.Get("handle"); ok {
		if obj, ok := v.(*jsonx.Obj); ok {
			fields.Handle = obj
		}
	}
	if v, ok := checkpoint.Get("pollAt"); ok {
		if f, ok := v.(float64); ok {
			fields.PollAt = f
		}
	}
	return fields
}

// GenerationFailedCompletion builds the failed completion.
func GenerationFailedCompletion(reason, detail string, assistant *int64) *jsonx.Obj {
	failure := jsonx.ObjFrom("reason", reason, "detail", detail)
	if assistant != nil {
		failure.Set("assistant", float64(*assistant))
	}
	return jsonx.ObjFrom("status", "failed", "failure", failure)
}

// DisplayAssistantData builds the display-only assistant entry data:
// attempt + display message + reason (error | aborted). Display-only
// entries never carry `model`.
func DisplayAssistantData(attempt float64, display *jsonx.Obj, reason string) *jsonx.Obj {
	return jsonx.ObjFrom("attempt", attempt, "display", display, "reason", reason)
}

// IsDisplayOnly reports whether an entry is a display-only assistant
// (pi.assistant with data.display and no model).
func IsDisplayOnly(entry *pico3.Entry) bool {
	if entry == nil || entry.Kind != "pi.assistant" || entry.Data == nil {
		return false
	}
	if _, hasModel := entry.Data.Get("display"); !hasModel {
		return false
	}
	return len(entry.Model) == 0
}
