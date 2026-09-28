package kinds

// collapse.go ports harness/pico3/kinds/collapse.ts: the pi.collapse kind
// core — the checkpoint model, failure completions, the chooseThrough
// cut-point search, and the prepared-summary commit decision. The provider
// streaming side (summarizeNow's model call) rides on the runtime's model
// surface and lands with the generation port.

import (
	"github.com/gladmo/openagent/agent/harness/pico3"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// Collapse phases.
const (
	CollapsePhaseSummarizing = "summarizing"
	CollapsePhaseRetrying    = "retrying"
	CollapsePhasePrepared    = "prepared"
)

// CollapseInflight mirrors kind.inflight.
var CollapseInflight = []string{CollapsePhaseSummarizing}

// CollapseConfigDefaults mirrors collapseConfig.
var CollapseConfigDefaults = map[string]float64{
	"threshold":  0,
	"keepRecent": 20000,
}

// CollapseInputFields reads the input payload.
type CollapseInputFields struct {
	Reason          string // threshold | manual | overflow
	Through         int64
	Instructions    string
	HasInstructions bool
}

// ParseCollapseInput reads the input object.
func ParseCollapseInput(input any) CollapseInputFields {
	fields := CollapseInputFields{}
	obj, ok := input.(*jsonx.Obj)
	if !ok {
		return fields
	}
	if v, ok := obj.Get("reason"); ok {
		if s, ok := v.(string); ok {
			fields.Reason = s
		}
	}
	if v, ok := obj.Get("through"); ok {
		if f, ok := v.(float64); ok {
			fields.Through = int64(f)
		}
	}
	if v, ok := obj.Get("instructions"); ok {
		if s, ok := v.(string); ok {
			fields.Instructions = s
			fields.HasInstructions = true
		}
	}
	return fields
}

// CollapseBaseFields reads the checkpoint base.
type CollapseBaseFields struct {
	ExpectedHead    *int64
	HasExpectedHead bool
	Instructions    string
	HasInstructions bool
	Model           *jsonx.Obj
	ThinkingLevel   string
	Retry           ai.RetryPolicy
	Attempt         float64
}

// ParseCollapseCheckpoint reads a checkpoint.
func ParseCollapseCheckpoint(checkpoint pico3.Checkpoint) (phase string, base CollapseBaseFields, untilMS float64, lastError string, summary string) {
	if checkpoint == nil {
		return "", base, 0, "", ""
	}
	if v, ok := checkpoint.Get("phase"); ok {
		if s, ok := v.(string); ok {
			phase = s
		}
	}
	if v, ok := checkpoint.Get("expectedHead"); ok {
		if f, ok := v.(float64); ok {
			id := int64(f)
			base.ExpectedHead = &id
			base.HasExpectedHead = true
		}
	}
	if v, ok := checkpoint.Get("instructions"); ok {
		if s, ok := v.(string); ok {
			base.Instructions = s
			base.HasInstructions = true
		}
	}
	if v, ok := checkpoint.Get("model"); ok {
		if obj, ok := v.(*jsonx.Obj); ok {
			base.Model = obj
		}
	}
	if v, ok := checkpoint.Get("thinkingLevel"); ok {
		if s, ok := v.(string); ok {
			base.ThinkingLevel = s
		}
	}
	if v, ok := checkpoint.Get("retry"); ok {
		if obj, ok := v.(*jsonx.Obj); ok {
			base.Retry = ai.RetryPolicy{
				Enabled:     objMustBool(obj, "enabled"),
				MaxRetries:  int(objMustNumber(obj, "maxRetries")),
				BaseDelayMs: objMustNumber(obj, "baseDelayMs"),
			}
			if v, ok := obj.Get("maxAgentDelayMs"); ok {
				if f, ok := v.(float64); ok {
					maxDelay := f
					base.Retry.MaxAgentDelayMs = &maxDelay
				}
			}
		}
	}
	if v, ok := checkpoint.Get("attempt"); ok {
		if f, ok := v.(float64); ok {
			base.Attempt = f
		}
	}
	if v, ok := checkpoint.Get("untilMs"); ok {
		if f, ok := v.(float64); ok {
			untilMS = f
		}
	}
	if v, ok := checkpoint.Get("lastError"); ok {
		if s, ok := v.(string); ok {
			lastError = s
		}
	}
	if v, ok := checkpoint.Get("summary"); ok {
		if s, ok := v.(string); ok {
			summary = s
		}
	}
	return phase, base, untilMS, lastError, summary
}

func objMustBool(obj *jsonx.Obj, key string) bool {
	if v, ok := obj.Get(key); ok {
		return v == true
	}
	return false
}

func objMustNumber(obj *jsonx.Obj, key string) float64 {
	if v, ok := obj.Get(key); ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

// CollapseFailedCompletion builds the failed completion.
func CollapseFailedCompletion(reason, detail string) *jsonx.Obj {
	return jsonx.ObjFrom(
		"status", "failed",
		"failure", jsonx.ObjFrom("reason", reason, "detail", detail),
	)
}

// HeadMoved compares the expected head against the observed one.
func HeadMoved(expected *int64, hasExpected bool, observed *int64) bool {
	if !hasExpected {
		return observed != nil
	}
	if expected == nil || observed == nil {
		return expected != observed
	}
	return *expected != *observed
}

// PreparedCommitDecision evaluates the prepared phase's done decision:
// stale when the head moved; otherwise the summary entry write with head =
// the first retained entry after `through`, or "self".
type PreparedCommitDecision struct {
	Stale   bool
	Summary string
	Through int64
	// HeadID is the retained head entry id (0 = "self").
	HeadID    int64
	HeadSelf  bool
	Timestamp float64
}

// EvaluatePrepared mirrors the prepared done closure's decision inputs.
func EvaluatePrepared(expectedHead *int64, hasExpected bool, observedHead *int64, entries []*pico3.Entry, through int64) *PreparedCommitDecision {
	if HeadMoved(expectedHead, hasExpected, observedHead) {
		return &PreparedCommitDecision{Stale: true}
	}
	decision := &PreparedCommitDecision{Through: through, HeadSelf: true}
	for _, entry := range entries {
		if entry.ID > through {
			decision.HeadID = entry.ID
			decision.HeadSelf = false
			break
		}
	}
	return decision
}

// ChooseThrough mirrors chooseThrough: group entries into exchanges
// (assistant-led, extended by tool results), retain from the newest while
// under keepRecent, and return the cut entry: undefined when everything
// fits; the previous exchange's last when only the newest remains; else
// the boundary exchange's last.
func ChooseThrough(entries []*pico3.Entry, keepRecent float64, estimate func(messages []any) float64) (int64, bool) {
	type exchange struct {
		last   int64
		tokens float64
	}
	var exchanges []exchange
	var open *exchange
	for _, entry := range entries {
		role := entryRole(entry)
		tokens := estimate(entryModelSlice(entry))
		if role == "assistant" {
			exchanges = append(exchanges, exchange{last: entry.ID, tokens: tokens})
			open = &exchanges[len(exchanges)-1]
		} else if role == "toolResult" && open != nil {
			open.last = entry.ID
			open.tokens += tokens
		} else {
			open = nil
			exchanges = append(exchanges, exchange{last: entry.ID, tokens: tokens})
		}
	}
	retained := 0.0
	index := len(exchanges) - 1
	for index >= 0 && retained+exchanges[index].tokens <= keepRecent {
		retained += exchanges[index].tokens
		index--
	}
	if index < 0 {
		return 0, false
	}
	if index == len(exchanges)-1 {
		if index-1 < 0 {
			return 0, false
		}
		return exchanges[index-1].last, true
	}
	return exchanges[index].last, true
}

func entryRole(entry *pico3.Entry) string {
	if len(entry.Model) == 0 {
		return ""
	}
	first, ok := entry.Model[0].(*jsonx.Obj)
	if !ok {
		return ""
	}
	if v, ok := first.Get("role"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func entryModelSlice(entry *pico3.Entry) []any {
	out := make([]any, 0, len(entry.Model))
	out = append(out, entry.Model...)
	return out
}

// AfterFailureDecision mirrors afterFailure: retry or fail per the retry
// policy's decision.
type AfterFailureDecision struct {
	Fail     bool
	Reason   string
	UntilMS  float64
	HasRetry bool
}

// RetryDecisionFn computes the retry decision for the attempt.
type RetryDecisionFn func(base CollapseBaseFields, now float64) (fail bool, reason string, untilMS float64, retry bool)

// AfterFailure applies the retry decision to a failure detail.
func AfterFailure(base CollapseBaseFields, detail string, now float64, decide RetryDecisionFn) *AfterFailureDecision {
	fail, reason, untilMS, retry := decide(base, now)
	if fail {
		return &AfterFailureDecision{Fail: true, Reason: reason}
	}
	return &AfterFailureDecision{UntilMS: untilMS, HasRetry: retry}
}
