package delta

import (
	"fmt"

	"github.com/gladmo/openagent/jsonx"
)

// WireOp is a wire-form op tuple (jsonx model): positional arrays with
// path refs (ids), short forms and "#" definitions.
type WireOp = []any

// Encoder mirrors the TS Encoder: ONE PAIR PER INDEPENDENT STATE STREAM.
type Encoder interface {
	Encode(ops []Op) []WireOp
}

type encoderState struct {
	seen     map[string]bool
	ids      map[string]int
	nextID   int
	previous string // last path key in the current batch
}

// NewEncoder creates an encoder.
func NewEncoder() Encoder {
	return &encoderState{seen: map[string]bool{}, ids: map[string]int{}}
}

func pathKey(path Path) string { return jsonx.Stringify(pathToJSON(path)) }

// Encode interns paths on SECOND use and omits paths that repeat the previous
// op's path within a batch.
func (e *encoderState) Encode(ops []Op) []WireOp {
	e.previous = ""
	out := []WireOp{}
	for _, op := range ops {
		if replace, ok := op.(*Replace); ok {
			out = append(out, WireOp{"r", replace.Value})
			// A base batch is a RECOVERY POINT: everything after it must be
			// self-contained.
			e.seen = map[string]bool{}
			e.ids = map[string]int{}
			e.nextID = 0
			e.previous = ""
			continue
		}
		path := opPathOf(op)
		key := pathKey(path)

		// Same path as the previous op: drop the ref entirely.
		if key == e.previous && key != "" {
			switch t := op.(type) {
			case *Set:
				out = append(out, WireOp{"s", t.Value})
			case *Delete:
				out = append(out, WireOp{"d"})
			case *Append:
				out = append(out, WireOp{"a", t.Text})
			case *Truncate:
				out = append(out, WireOp{"t", float64(t.Count)})
			case *Splice:
				out = append(out, WireOp{"p", float64(t.Index), float64(t.Remove), t.Items})
			case *Permute:
				out = append(out, permWire(t.Perm))
			}
			continue
		}

		var ref any = pathToJSON(path)
		if existing, ok := e.ids[key]; ok {
			ref = float64(existing)
		} else if e.seen[key] {
			id := e.nextID
			e.nextID++
			e.ids[key] = id
			out = append(out, WireOp{"#", float64(id), pathToJSON(path)}) // second use: define, then reference
			ref = float64(id)
		} else {
			e.seen[key] = true // first use: inline
		}

		switch t := op.(type) {
		case *Set:
			out = append(out, WireOp{"s", ref, t.Value})
		case *Delete:
			out = append(out, WireOp{"d", ref})
		case *Append:
			out = append(out, WireOp{"a", ref, t.Text})
		case *Truncate:
			out = append(out, WireOp{"t", ref, float64(t.Count)})
		case *Splice:
			out = append(out, WireOp{"p", ref, float64(t.Index), float64(t.Remove), t.Items})
		case *Permute:
			wire := append(WireOp{"m", ref}, permWire(t.Perm)[1:]...)
			out = append(out, wire)
		}
		e.previous = key
	}
	return out
}

func permWire(perm []int) WireOp {
	out := WireOp{"m"}
	for _, p := range perm {
		out = append(out, float64(p))
	}
	return out
}

func opPathOf(op Op) Path {
	switch t := op.(type) {
	case *Set:
		return t.Path
	case *Delete:
		return t.Path
	case *Append:
		return t.Path
	case *Truncate:
		return t.Path
	case *Splice:
		return t.Path
	case *Permute:
		return t.Path
	default:
		return Path{}
	}
}

// Decoder mirrors the TS Decoder: observes exactly the batches encoded by its
// matching encoder, beginning with that state's base.
type Decoder interface {
	Decode(wire []WireOp) ([]Op, error)
}

type decoderState struct {
	paths map[int]Path
}

// NewDecoder creates a decoder.
func NewDecoder() Decoder { return &decoderState{paths: map[int]Path{}} }

func (d *decoderState) Decode(wire []WireOp) ([]Op, error) {
	var previous Path
	out := []Op{}
	for _, op := range wire {
		if err := assertValidWireOp(op); err != nil {
			return nil, err
		}
		verb, _ := op[0].(string)
		if verb == "#" {
			id, _ := jsonx.ToFloat(op[1])
			path, err := wirePath(op[2])
			if err != nil {
				return nil, err
			}
			d.paths[int(id)] = path
			continue
		}
		if verb == "r" {
			out = append(out, &Replace{Value: op[1]})
			d.paths = map[int]Path{}
			previous = nil
			continue
		}

		// Arity tells whether a ref is present: the short forms omit it.
		short := (verb == "d" && len(op) == 1) ||
			(verb != "d" && verb != "p" && verb != "r" && len(op) == 2) ||
			(verb == "p" && len(op) == 4)

		var path Path
		if short {
			if previous == nil {
				return nil, &PathError{Path: Path{}}
			}
			path = previous
		} else {
			ref := op[1]
			if id, ok := jsonx.ToFloat(ref); ok {
				resolved, exists := d.paths[int(id)]
				if !exists {
					return nil, &PathError{Path: id}
				}
				path = resolved
			} else {
				p, err := wirePath(ref)
				if err != nil {
					return nil, err
				}
				path = p
			}
			previous = path
		}

		if verb != "p" && verb != "m" && len(path) == 0 {
			return nil, &PathError{Path: path}
		}
		switch verb {
		case "s":
			var value any
			if short {
				value = op[1]
			} else {
				value = op[2]
			}
			out = append(out, &Set{Path: path, Value: value})
		case "d":
			out = append(out, &Delete{Path: path})
		case "a":
			var text string
			if short {
				text, _ = op[1].(string)
			} else {
				text, _ = op[2].(string)
			}
			out = append(out, &Append{Path: path, Text: text})
		case "t":
			var count float64
			if short {
				count, _ = jsonx.ToFloat(op[1])
			} else {
				count, _ = jsonx.ToFloat(op[2])
			}
			out = append(out, &Truncate{Path: path, Count: int(count)})
		case "p":
			var i, r float64
			var items []any
			if short {
				i, _ = jsonx.ToFloat(op[1])
				r, _ = jsonx.ToFloat(op[2])
				items, _ = op[3].([]any)
			} else {
				i, _ = jsonx.ToFloat(op[2])
				r, _ = jsonx.ToFloat(op[3])
				items, _ = op[4].([]any)
			}
			out = append(out, &Splice{Path: path, Index: int(i), Remove: int(r), Items: items})
		case "m":
			permWire := op[len(op)-1].([]any)
			perm := make([]int, len(permWire))
			for idx, e := range permWire {
				f, _ := jsonx.ToFloat(e)
				perm[idx] = int(f)
			}
			out = append(out, &Permute{Path: path, Perm: perm})
		}
	}
	return out, nil
}

func wirePath(v any) (Path, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("path is not an array")
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

// assertValidWireOp validates a wire tuple like the TS assertValidWireOp.
func assertValidWireOp(op WireOp) error {
	if len(op) == 0 {
		return fmt.Errorf("op is not a tuple")
	}
	verb, _ := op[0].(string)
	okRef := func(r any) error {
		if id, ok := jsonx.ToFloat(r); ok {
			if id != float64(int(id)) || id < 0 {
				return fmt.Errorf("bad path id")
			}
			return nil
		}
		_, err := wirePath(r)
		return err
	}
	isInt := func(v any, msg string) error {
		f, ok := jsonx.ToFloat(v)
		if !ok || f != float64(int(f)) || f < 0 {
			return fmt.Errorf("%s", msg)
		}
		return nil
	}
	switch verb {
	case "r":
		if len(op) != 2 {
			return fmt.Errorf("r arity")
		}
	case "s":
		if len(op) == 3 {
			if err := okRef(op[1]); err != nil {
				return err
			}
		} else if len(op) != 2 {
			return fmt.Errorf("s arity")
		}
	case "d":
		if len(op) == 2 {
			if err := okRef(op[1]); err != nil {
				return err
			}
		} else if len(op) != 1 {
			return fmt.Errorf("d arity")
		}
	case "a":
		switch len(op) {
		case 3:
			if err := okRef(op[1]); err != nil {
				return err
			}
			if _, ok := op[2].(string); !ok {
				return fmt.Errorf("a value")
			}
		case 2:
			if _, ok := op[1].(string); !ok {
				return fmt.Errorf("a value")
			}
		default:
			return fmt.Errorf("a arity")
		}
	case "t":
		switch len(op) {
		case 3:
			if err := okRef(op[1]); err != nil {
				return err
			}
			if err := isInt(op[2], "t count"); err != nil {
				return err
			}
		case 2:
			if err := isInt(op[1], "t count"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("t arity")
		}
	case "p":
		var i, r, items any
		switch len(op) {
		case 5:
			if err := okRef(op[1]); err != nil {
				return err
			}
			i, r, items = op[2], op[3], op[4]
		case 4:
			i, r, items = op[1], op[2], op[3]
		default:
			return fmt.Errorf("p arity")
		}
		if err := isInt(i, "p index"); err != nil {
			return err
		}
		if err := isInt(r, "p remove"); err != nil {
			return err
		}
		if _, ok := items.([]any); !ok {
			return fmt.Errorf("p items")
		}
	case "m":
		if len(op) == 3 {
			if err := okRef(op[1]); err != nil {
				return err
			}
		} else if len(op) != 2 {
			return fmt.Errorf("m arity")
		}
		if err := assertPermutation(op[len(op)-1]); err != nil {
			return err
		}
	case "#":
		if len(op) != 3 {
			return fmt.Errorf("# shape")
		}
		if err := isInt(op[1], "# shape"); err != nil {
			return fmt.Errorf("# shape")
		}
		if _, err := wirePath(op[2]); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown op verb: %v", op[0])
	}
	return nil
}
