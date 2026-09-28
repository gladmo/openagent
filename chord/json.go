// Package chord ports the @gladmo/chord closure that pi/packages/agent
// depends on: the JsonValue model (root package), the context chain
// (chord/context), and the delta operation algebra (chord/delta).
//
// Only the closure is ported; services/facets/node land in the pico3 phase.
package chord

import (
	"reflect"

	"github.com/gladmo/openagent/jsonx"
)

// JsonValue is the strict JSON value tree: nil, bool, float64, string,
// []any (dense arrays) or *jsonx.Obj (plain objects, insertion-ordered).
type JsonValue = any

// CopyJsonOptions mirrors the TS options.
type CopyJsonOptions struct {
	// OmitUndefinedProperties is a no-op in Go: the jsonx value model has no
	// undefined-valued properties (absence is the only representation).
	OmitUndefinedProperties bool
}

// CopyJson copies a value into an alias-free strict-JSON tree owned by the
// caller. Non-finite numbers, non-JSON values and cycles are errors, like the
// TS implementation's TypeError throws.
func CopyJson(value any, options *CopyJsonOptions) (JsonValue, error) {
	return copyJson(value, map[any]bool{}, options)
}

func copyJson(value any, ancestors map[any]bool, options *CopyJsonOptions) (JsonValue, error) {
	switch t := value.(type) {
	case nil:
		return nil, nil
	case bool:
		return t, nil
	case string:
		return t, nil
	case float64:
		if !isFinite(t) {
			return nil, &StrictJSONError{Message: "Value contains a non-finite number and is not strict JSON"}
		}
		return t, nil
	case int:
		return float64(t), nil
	case []any:
		if ancestors[identityKey(t)] {
			return nil, &StrictJSONError{Message: "Value contains cycles and is not strict JSON"}
		}
		ancestors[identityKey(t)] = true
		defer delete(ancestors, identityKey(t))
		out := make([]any, len(t))
		for i, e := range t {
			copied, err := copyJson(e, ancestors, options)
			if err != nil {
				return nil, err
			}
			out[i] = copied
		}
		return out, nil
	case *jsonx.Obj:
		if ancestors[identityKey(t)] {
			return nil, &StrictJSONError{Message: "Value contains cycles and is not strict JSON"}
		}
		ancestors[identityKey(t)] = true
		defer delete(ancestors, identityKey(t))
		out := jsonx.NewObj()
		for _, k := range t.Keys() {
			copied, err := copyJson(t.MustGet(k), ancestors, options)
			if err != nil {
				return nil, err
			}
			out.Set(k, copied)
		}
		return out, nil
	default:
		return nil, &StrictJSONError{Message: "Value contains a non-JSON value; expected strict JSON"}
	}
}

// sliceKey identifies an array by its backing-array pointer and length.
// jsonx-built trees always allocate fresh backing arrays, so this is a sound
// identity for cycle detection; only hand-constructed sub-slice aliases could
// confuse it, which the strict-JSON contract already excludes.
type sliceKey struct {
	ptr uintptr
	len int
}

// identityKey returns a comparable identity key for containers: objects by
// pointer, arrays by backing pointer + length.
func identityKey(v any) any {
	switch t := v.(type) {
	case *jsonx.Obj:
		return t
	case []any:
		if len(t) == 0 {
			return nil
		}
		return sliceKey{ptr: reflect.ValueOf(t).Pointer(), len: len(t)}
	default:
		return nil
	}
}

func isFinite(f float64) bool {
	return !(f != f || f > 1.7976931348623157e308 || f < -1.7976931348623157e308)
}

// StrictJSONError mirrors the TypeError throws of the TS implementation.
type StrictJSONError struct{ Message string }

func (e *StrictJSONError) Error() string { return e.Message }

// IsJsonValue reports whether the value is finite strict JSON with plain
// objects/arrays and no cycles.
func IsJsonValue(value any) bool {
	return checkJson(value, map[any]bool{})
}

func checkJson(value any, ancestors map[any]bool) bool {
	switch t := value.(type) {
	case nil:
		return true
	case bool:
		return true
	case string:
		return true
	case float64:
		return isFinite(t)
	case int:
		return true
	case []any:
		key := identityKey(t)
		if key != nil && ancestors[key] {
			return false
		}
		if key != nil {
			ancestors[key] = true
			defer delete(ancestors, key)
		}
		for _, e := range t {
			if !checkJson(e, ancestors) {
				return false
			}
		}
		return true
	case *jsonx.Obj:
		if ancestors[t] {
			return false
		}
		ancestors[t] = true
		defer delete(ancestors, t)
		for _, k := range t.Keys() {
			if !checkJson(t.MustGet(k), ancestors) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
