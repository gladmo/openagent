// Package delta ports @gladmo/chord/delta/index.ts: the positional
// tuple operation algebra over strict JSON trees.
//
// Op representation: TS tuples are the wire/disk form; in Go each verb is a
// concrete struct implementing the Op interface, and OpFromJSON/OpToJSON (and
// MarshalJSON/UnmarshalJSON) convert to/from the positional tuples with
// identical validation rules (assertValidOp).
package delta

import (
	"fmt"

	"github.com/gladmo/openagent/chord"
	"github.com/gladmo/openagent/jsonx"
)

// Seg is a path segment: string (object key) or number (array index).
type Seg = any

// Path is a segment list.
type Path = []Seg

// Op is one decoded operation; tuples are the wire form.
type Op interface {
	opVerb() string
}

// Replace ("r", value) replaces the whole value; the only root-capable value
// op. The payload is adopted, not copied.
type Replace struct{ Value chord.JsonValue }

// Set ("s", path, value).
type Set struct {
	Path  Path
	Value chord.JsonValue
}

// Delete ("d", path).
type Delete struct{ Path Path }

// Append ("a", path, text) appends to a string.
type Append struct {
	Path Path
	Text string
}

// Truncate ("t", path, count) slices a string from count.
type Truncate struct {
	Path  Path
	Count int
}

// Splice ("p", path, index, removeCount, items).
type Splice struct {
	Path   Path
	Index  int
	Remove int
	Items  []chord.JsonValue
}

// Permute ("m", path, perm): new[i] = old[perm[i]].
type Permute struct {
	Path Path
	Perm []int
}

func (*Replace) opVerb() string  { return "r" }
func (*Set) opVerb() string      { return "s" }
func (*Delete) opVerb() string   { return "d" }
func (*Append) opVerb() string   { return "a" }
func (*Truncate) opVerb() string { return "t" }
func (*Splice) opVerb() string   { return "p" }
func (*Permute) opVerb() string  { return "m" }

// IsReplace mirrors isReplace.
func IsReplace(op Op) bool { return op.opVerb() == "r" }

// IsBase reports that a batch begins with a replacement (a recovery point).
func IsBase(ops []Op) bool { return len(ops) > 0 && ops[0].opVerb() == "r" }

// OpToJSON renders an op as its positional tuple (jsonx model).
func OpToJSON(op Op) any {
	switch t := op.(type) {
	case *Replace:
		return []any{"r", t.Value}
	case *Set:
		return []any{"s", pathToJSON(t.Path), t.Value}
	case *Delete:
		return []any{"d", pathToJSON(t.Path)}
	case *Append:
		return []any{"a", pathToJSON(t.Path), t.Text}
	case *Truncate:
		return []any{"t", pathToJSON(t.Path), float64(t.Count)}
	case *Splice:
		items := make([]any, len(t.Items))
		for i, item := range t.Items {
			items[i] = item
		}
		return []any{"p", pathToJSON(t.Path), float64(t.Index), float64(t.Remove), items}
	case *Permute:
		perm := make([]any, len(t.Perm))
		for i, p := range t.Perm {
			perm[i] = float64(p)
		}
		return []any{"m", pathToJSON(t.Path), perm}
	default:
		return nil
	}
}

func pathToJSON(path Path) any {
	out := make([]any, len(path))
	for i, seg := range path {
		if f, ok := seg.(float64); ok {
			out[i] = f
		} else if n, ok := seg.(int); ok {
			out[i] = float64(n)
		} else {
			out[i] = seg
		}
	}
	return out
}

// OpFromJSON converts a decoded tuple (jsonx model) into an Op, validating
// exactly like assertValidOp.
func OpFromJSON(v any) (Op, error) {
	tuple, ok := v.([]any)
	if !ok || len(tuple) == 0 {
		return nil, fmt.Errorf("op is not a tuple")
	}
	verb, _ := tuple[0].(string)
	switch verb {
	case "r":
		if len(tuple) != 2 {
			return nil, fmt.Errorf("r arity")
		}
		return &Replace{Value: tuple[1]}, nil
	case "s":
		if len(tuple) != 3 {
			return nil, fmt.Errorf("s arity")
		}
		path, err := assertPathArg(tuple[1], true)
		if err != nil {
			return nil, err
		}
		return &Set{Path: path, Value: tuple[2]}, nil
	case "d":
		if len(tuple) != 2 {
			return nil, fmt.Errorf("d arity")
		}
		path, err := assertPathArg(tuple[1], true)
		if err != nil {
			return nil, err
		}
		return &Delete{Path: path}, nil
	case "a":
		if len(tuple) != 3 {
			return nil, fmt.Errorf("a shape")
		}
		text, ok := tuple[2].(string)
		if !ok {
			return nil, fmt.Errorf("a shape")
		}
		path, err := assertPathArg(tuple[1], true)
		if err != nil {
			return nil, err
		}
		return &Append{Path: path, Text: text}, nil
	case "t":
		if len(tuple) != 3 {
			return nil, fmt.Errorf("t shape")
		}
		count, ok := jsonx.ToFloat(tuple[2])
		if !ok || count != float64(int(count)) || count < 0 {
			return nil, fmt.Errorf("t shape")
		}
		path, err := assertPathArg(tuple[1], true)
		if err != nil {
			return nil, err
		}
		return &Truncate{Path: path, Count: int(count)}, nil
	case "p":
		if len(tuple) != 5 {
			return nil, fmt.Errorf("p arity")
		}
		path, err := assertPathArg(tuple[1], false)
		if err != nil {
			return nil, err
		}
		index, ok := jsonx.ToFloat(tuple[2])
		if !ok || index != float64(int(index)) || index < 0 {
			return nil, fmt.Errorf("p index")
		}
		remove, ok := jsonx.ToFloat(tuple[3])
		if !ok || remove != float64(int(remove)) || remove < 0 {
			return nil, fmt.Errorf("p remove")
		}
		items, ok := tuple[4].([]any)
		if !ok {
			return nil, fmt.Errorf("p items")
		}
		return &Splice{Path: path, Index: int(index), Remove: int(remove), Items: items}, nil
	case "m":
		if len(tuple) != 3 {
			return nil, fmt.Errorf("m arity")
		}
		path, err := assertPathArg(tuple[1], false)
		if err != nil {
			return nil, err
		}
		if err := assertPermutation(tuple[2]); err != nil {
			return nil, err
		}
		arr := tuple[2].([]any)
		perm := make([]int, len(arr))
		for i, e := range arr {
			f, _ := jsonx.ToFloat(e)
			perm[i] = int(f)
		}
		return &Permute{Path: path, Perm: perm}, nil
	default:
		return nil, fmt.Errorf("unknown op verb: %v", tuple[0])
	}
}

// OpsFromJSON converts a list of tuples.
func OpsFromJSON(list any) ([]Op, error) {
	arr, ok := list.([]any)
	if !ok {
		return nil, fmt.Errorf("ops is not an array")
	}
	out := make([]Op, 0, len(arr))
	for _, v := range arr {
		op, err := OpFromJSON(v)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, nil
}

// OpsToJSON renders a list of tuples.
func OpsToJSON(ops []Op) []any {
	out := make([]any, len(ops))
	for i, op := range ops {
		out[i] = OpToJSON(op)
	}
	return out
}

func assertPathArg(p any, nonEmpty bool) (Path, error) {
	arr, ok := p.([]any)
	if !ok {
		return nil, fmt.Errorf("path is not an array")
	}
	if nonEmpty && len(arr) == 0 {
		return nil, fmt.Errorf("path is empty")
	}
	path := make(Path, len(arr))
	for i, seg := range arr {
		switch t := seg.(type) {
		case string:
			path[i] = t
		case float64:
			path[i] = t
		case int:
			path[i] = float64(t)
		default:
			return nil, &UnsafePathError{Segment: seg}
		}
	}
	if err := AssertSafePath(path); err != nil {
		return nil, err
	}
	return path, nil
}

func assertPermutation(value any) error {
	arr, ok := value.([]any)
	if !ok {
		return fmt.Errorf("m permutation is not an array")
	}
	seen := make([]bool, len(arr))
	for _, e := range arr {
		f, ok := jsonx.ToFloat(e)
		if !ok || f != float64(int(f)) || f < 0 || f >= float64(len(arr)) || seen[int(f)] {
			return fmt.Errorf("m permutation is not a bijection")
		}
		seen[int(f)] = true
	}
	return nil
}

// ReservedSegments are the segments that reach the prototype chain in JS;
// kept identical for wire compatibility.
var ReservedSegments = map[string]bool{
	"__proto__":   true,
	"constructor": true,
	"prototype":   true,
}

// UnsafePathError mirrors the TS error.
type UnsafePathError struct{ Segment Seg }

func (e *UnsafePathError) Error() string {
	return fmt.Sprintf("unsafe path segment: %v", e.Segment)
}

// PathError mirrors the TS error.
type PathError struct{ Path any }

func (e *PathError) Error() string {
	return fmt.Sprintf("unresolvable path: %s", jsonx.Stringify(normalizePathForString(e.Path)))
}

func normalizePathForString(p any) any {
	if path, ok := p.(Path); ok {
		return pathToJSON(path)
	}
	return p
}

// AssertSafePath rejects reserved string segments and non-nonnegative-integer
// numeric segments.
func AssertSafePath(path Path) error {
	for _, seg := range path {
		switch t := seg.(type) {
		case string:
			if ReservedSegments[t] {
				return &UnsafePathError{Segment: t}
			}
		case float64:
			if t != float64(int(t)) || t < 0 {
				return &UnsafePathError{Segment: t}
			}
		case int:
			if t < 0 {
				return &UnsafePathError{Segment: t}
			}
		default:
			return &UnsafePathError{Segment: seg}
		}
	}
	return nil
}

// ToFloatOr is a small helper used by apply.go.
func toFloatOr(seg Seg, fallback float64) float64 {
	if f, ok := seg.(float64); ok {
		return f
	}
	return fallback
}
