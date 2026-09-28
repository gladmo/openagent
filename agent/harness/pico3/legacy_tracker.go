package pico3

// legacy_tracker.go ports harness/pico3/legacy-tracker.ts: the flush-based
// document tracker compatibility surface. The TS tracker rides on chord's
// proxy-based change tracking; the Go port diffs the working copy against
// the base (top-level key diff -> Set/Delete ops; nested values are set
// whole) and always emits a Replace base op first, matching rebase().

import (
	chorddelta "github.com/gladmo/openagent/chord/delta"
	"github.com/gladmo/openagent/jsonx"
)

// Tracker is the flush-based document compatibility surface.
type Tracker struct {
	base      *jsonx.Obj
	working   *jsonx.Obj
	hasChange bool
	forceBase bool
}

// Track builds a tracker over an initial object state.
func Track(initial *jsonx.Obj) *Tracker {
	base := cloneJSONObj(initial)
	working := cloneJSONObj(initial)
	return &Tracker{base: base, working: working, forceBase: true}
}

// State returns the mutable working copy.
func (t *Tracker) State() *jsonx.Obj {
	if !t.hasChange {
		t.hasChange = true
	}
	return t.working
}

// Target returns the committed base value.
func (t *Tracker) Target() *jsonx.Obj { return t.base }

// Dirty reports whether the next flush will produce ops.
func (t *Tracker) Dirty() bool {
	return t.forceBase || t.hasChange
}

// Flush adopts the change and returns the ops to persist. Pico3 adopts
// before storage; a storage failure faults the owning Session.
func (t *Tracker) Flush() []chorddelta.Op {
	var prepared []chorddelta.Op
	if t.hasChange {
		prepared = diffObjects(t.base, t.working)
		t.base = cloneJSONObj(t.working)
		t.hasChange = false
	}
	if t.forceBase {
		t.forceBase = false
		return []chorddelta.Op{&chorddelta.Replace{Value: cloneJSONObj(t.base)}}
	}
	return prepared
}

// Rebase forces the next flush to emit a base op.
func (t *Tracker) Rebase() { t.forceBase = true }

// diffObjects produces Set/Delete ops for changed top-level keys.
func diffObjects(base, working *jsonx.Obj) []chorddelta.Op {
	var ops []chorddelta.Op
	for _, key := range working.Keys() {
		nextValue, _ := working.Get(key)
		prevValue, had := base.Get(key)
		if had && jsonValuesEqual(prevValue, nextValue) {
			continue
		}
		ops = append(ops, &chorddelta.Set{Path: chorddelta.Path{key}, Value: nextValue})
	}
	for _, key := range base.Keys() {
		if _, still := working.Get(key); !still {
			ops = append(ops, &chorddelta.Delete{Path: chorddelta.Path{key}})
		}
	}
	return ops
}

func jsonValuesEqual(a, b any) bool {
	return jsonx.Stringify(a) == jsonx.Stringify(b)
}

func cloneJSONObj(obj *jsonx.Obj) *jsonx.Obj {
	if obj == nil {
		return jsonx.NewObj()
	}
	cloned := jsonx.NewObj()
	for _, key := range obj.Keys() {
		value, _ := obj.Get(key)
		cloned.Set(key, value)
	}
	return cloned
}
