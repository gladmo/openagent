// Package trajectory records an agent run as an engineering object: an
// append-only log of typed records that can be collected live, assembled
// into derived views, stored as JSONL, queried, replayed through the faux
// provider, exported (stdout NDJSON / JSONL / Markdown), and extended with
// custom record kinds.
//
// The design follows the DeepSeek Harness session-event model: one envelope
// per record with a type discriminant, a dense monotonic seq, an epoch-ms
// time, a JSON payload, and an ignorable marker that lets foreign readers
// skip extension kinds they do not understand. Unlike the harness session
// (the durable conversation tree in agent/harness/session), the trajectory
// is a full-fidelity observation plane: it keeps failed attempts, retry
// chains, per-chunk stream timings, and inputs that never enter the
// conversation.
package trajectory

// trajectory/record.go: the record envelope, the core kind catalog, and the
// JSON codec with exact-key validation.

import (
	"fmt"

	"github.com/gladmo/openagent/jsonx"
)

// Record is one immutable trajectory entry. Seq is assigned at commit (dense
// from 1); TimeMs is stamped at commit (Unix milliseconds). Turn counts from
// 1 within the trajectory (0 = session-scoped record); Step counts from 1
// within the turn (0 = turn-scoped record). A step covers one model call
// plus the tool executions it requested.
type Record struct {
	Type      string
	Seq       int64
	TimeMs    float64
	Turn      int
	Step      int
	Data      any
	Ignorable bool
}

// Core record kinds. The set is closed here; callers add kinds through
// Trajectory.RegisterKind (those are written with ignorable=true).
const (
	KindSessionStart = "session/start"
	KindSessionEnd   = "session/end"
	KindError        = "error"

	KindTurnStart = "turn/start"
	KindTurnEnd   = "turn/end"

	KindStepStart = "step/start"
	KindStepEnd   = "step/end"

	KindRequestHeader    = "request/header"
	KindModelInput       = "model/input"
	KindAssistantMessage = "assistant/message"
	KindAssistantAttempt = "assistant/attempt"

	KindUserMessage   = "user/message"
	KindSystemMessage = "system/message"
	KindAgentMessage  = "agent/message" // custom harness roles (ignorable)

	KindToolCall   = "tool/call"
	KindToolUpdate = "tool/update"
	KindToolResult = "tool/result"

	KindLlmRetry        = "llm/retry"
	KindLlmRetryStarted = "llm/retry-started"

	KindSubagentStart = "subagent/start"
	KindSubagentEnd   = "subagent/end"
)

// Scope constrains where a kind may appear in the turn/step coordinate
// system.
type Scope string

const (
	ScopeSession Scope = "session" // Turn==0, Step==0
	ScopeTurn    Scope = "turn"    // Turn>0, Step==0
	ScopeStep    Scope = "step"    // Turn>0, Step>0
	ScopeAny     Scope = "any"     // no constraint
)

type kindRule struct {
	scope Scope
	// core marks kinds declared in this package; core kinds are always
	// registered and never written with ignorable=true by the recorder.
	core bool
	// required marks an extension kind whose loss breaks reconstruction
	// for every consumer of the instance; other extension kinds are
	// written with ignorable=true.
	required bool
}

// coreKinds is the closed v1 catalog. Payload shapes are owned by the
// constructors in recorder.go and the module page docs/modules/trajectory.md.
var coreKinds = map[string]kindRule{
	KindSessionStart: {scope: ScopeSession, core: true},
	KindSessionEnd:   {scope: ScopeSession, core: true},
	KindError:        {scope: ScopeAny, core: true},

	KindTurnStart: {scope: ScopeTurn, core: true},
	KindTurnEnd:   {scope: ScopeTurn, core: true},

	KindStepStart: {scope: ScopeStep, core: true},
	KindStepEnd:   {scope: ScopeStep, core: true},

	KindRequestHeader:    {scope: ScopeStep, core: true},
	KindModelInput:       {scope: ScopeStep, core: true},
	KindAssistantMessage: {scope: ScopeStep, core: true},
	KindAssistantAttempt: {scope: ScopeStep, core: true},

	KindUserMessage:   {scope: ScopeTurn, core: true},
	KindSystemMessage: {scope: ScopeTurn, core: true},
	KindAgentMessage:  {scope: ScopeTurn, core: true},

	KindToolCall:   {scope: ScopeStep, core: true},
	KindToolUpdate: {scope: ScopeStep, core: true},
	KindToolResult: {scope: ScopeStep, core: true},

	KindLlmRetry:        {scope: ScopeAny, core: true},
	KindLlmRetryStarted: {scope: ScopeAny, core: true},

	KindSubagentStart: {scope: ScopeAny, core: true},
	KindSubagentEnd:   {scope: ScopeAny, core: true},
}

// Envelope key order for the JSON codec.
const (
	keyType      = "type"
	keySeq       = "seq"
	keyTime      = "time"
	keyTurn      = "turn"
	keyStep      = "step"
	keyData      = "data"
	keyIgnorable = "ignorable"
)

// envelopeKeys is the exact key set a persisted record may carry.
var envelopeKeys = map[string]bool{
	keyType: true, keySeq: true, keyTime: true, keyTurn: true,
	keyStep: true, keyData: true, keyIgnorable: true,
}

// validate checks envelope consistency against the kind rule. The payload
// shape is owned by the constructors; JSON-safety of Data is checked by the
// caller (Trajectory.Append).
func (r *Record) validate(rule kindRule) error {
	if r.Step > 0 && r.Turn <= 0 {
		return fmt.Errorf("trajectory: record %q carries step %d without a turn", r.Type, r.Step)
	}
	switch rule.scope {
	case ScopeSession:
		if r.Turn != 0 || r.Step != 0 {
			return fmt.Errorf("trajectory: record %q is session-scoped but carries turn %d step %d", r.Type, r.Turn, r.Step)
		}
	case ScopeTurn:
		if r.Turn <= 0 || r.Step != 0 {
			return fmt.Errorf("trajectory: record %q is turn-scoped and needs turn>0 step=0, got turn %d step %d", r.Type, r.Turn, r.Step)
		}
	case ScopeStep:
		if r.Turn <= 0 || r.Step <= 0 {
			return fmt.Errorf("trajectory: record %q is step-scoped and needs turn>0 step>0, got turn %d step %d", r.Type, r.Turn, r.Step)
		}
	case ScopeAny:
	}
	return nil
}

// ToJSON renders the envelope with fixed key order through jsonx (JS number
// formatting). Step is omitted when zero, ignorable when false.
func (r *Record) ToJSON() *jsonx.Obj {
	o := jsonx.NewObj()
	o.Set(keyType, r.Type)
	o.Set(keySeq, float64(r.Seq))
	o.Set(keyTime, r.TimeMs)
	o.Set(keyTurn, float64(r.Turn))
	if r.Step != 0 {
		o.Set(keyStep, float64(r.Step))
	}
	o.Set(keyData, r.Data)
	if r.Ignorable {
		o.Set(keyIgnorable, true)
	}
	return o
}

// RecordFromJSON decodes one envelope value with exact-key validation: an
// unknown key is rejected rather than silently dropped, mirroring the
// persistence-boundary strictness of the reference model.
func RecordFromJSON(v any) (*Record, error) {
	obj, ok := v.(*jsonx.Obj)
	if !ok {
		return nil, fmt.Errorf("trajectory: record is not a JSON object")
	}
	for _, k := range obj.Keys() {
		if !envelopeKeys[k] {
			return nil, fmt.Errorf("trajectory: record has unknown key %q", k)
		}
	}
	typeValue, ok := obj.Get(keyType)
	if !ok {
		return nil, fmt.Errorf("trajectory: record is missing %q", keyType)
	}
	recordType, ok := typeValue.(string)
	if !ok || recordType == "" {
		return nil, fmt.Errorf("trajectory: record %q is not a non-empty string", keyType)
	}
	rec := &Record{Type: recordType}
	if seqValue, ok := obj.Get(keySeq); ok {
		f, ok := jsonx.ToFloat(seqValue)
		if !ok || f != float64(int64(f)) {
			return nil, fmt.Errorf("trajectory: record %q has non-integer seq", recordType)
		}
		rec.Seq = int64(f)
	}
	if timeValue, ok := obj.Get(keyTime); ok {
		f, ok := jsonx.ToFloat(timeValue)
		if !ok {
			return nil, fmt.Errorf("trajectory: record %q has non-number time", recordType)
		}
		rec.TimeMs = f
	}
	if turnValue, ok := obj.Get(keyTurn); ok {
		f, ok := jsonx.ToFloat(turnValue)
		if !ok || f != float64(int(f)) {
			return nil, fmt.Errorf("trajectory: record %q has non-integer turn", recordType)
		}
		rec.Turn = int(f)
	}
	if stepValue, ok := obj.Get(keyStep); ok {
		f, ok := jsonx.ToFloat(stepValue)
		if !ok || f != float64(int(f)) {
			return nil, fmt.Errorf("trajectory: record %q has non-integer step", recordType)
		}
		rec.Step = int(f)
	}
	data, hasData := obj.Get(keyData)
	if !hasData {
		return nil, fmt.Errorf("trajectory: record %q is missing %q", recordType, keyData)
	}
	rec.Data = data
	if ignorable, ok := obj.Get(keyIgnorable); ok {
		b, ok := ignorable.(bool)
		if !ok {
			return nil, fmt.Errorf("trajectory: record %q has non-boolean ignorable", recordType)
		}
		rec.Ignorable = b
	}
	return rec, nil
}

// String renders the record as one JSON line (no trailing newline).
func (r *Record) String() string { return jsonx.Stringify(r.ToJSON()) }
