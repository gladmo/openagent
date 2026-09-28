package delta

import (
	"fmt"
	"strings"

	"github.com/gladmo/openagent/chord"
	"github.com/gladmo/openagent/jsonx"
)

// Overlap mirrors the TS overlap(): longest suffix of a that is a prefix of b,
// probing with a long head first, then one byte, with bounded candidates.
// Operates on bytes (the TS version operates on UTF-16 code units; callers in
// pi use it over captured output text).
func Overlap(a, b string, scan int, probe int, maxCandidates int) int {
	if len(a) == 0 || len(b) == 0 || scan == 0 {
		return 0
	}
	probe, maxCandidates = withDefaults(probe, maxCandidates)
	tail := a
	if len(a) > scan {
		tail = a[len(a)-scan:]
	}
	for _, h := range []int{minInt(probe, len(b)), 1} {
		head := b[:h]
		tried := 0
		for k := indexFrom(tail, head, 0); k != -1; k = indexFrom(tail, head, k+1) {
			tried++
			if tried > maxCandidates {
				break
			}
			n := len(tail) - k
			if n <= len(b) && tail[k:] == b[:n] {
				return n
			}
		}
		if h == 1 {
			break
		}
	}
	return 0
}

func indexFrom(s, sub string, from int) int {
	if from >= len(s) {
		if from == len(s) && sub == "" {
			return len(s)
		}
		return -1
	}
	idx := strings.Index(s[from:], sub)
	if idx < 0 {
		return -1
	}
	return from + idx
}

func withDefaults(probe, maxCandidates int) (int, int) {
	if probe <= 0 {
		probe = 64
	}
	if maxCandidates <= 0 {
		maxCandidates = 8
	}
	return probe, maxCandidates
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Chain resolution
// ---------------------------------------------------------------------------

// nodeChain resolves a path to a node plus a writeback function that replaces
// the node's slot with a new value and returns the resulting root. Objects on
// the chain mutate in place; array header changes propagate up through the
// writeback closures. Mutating chain arrays in place is only valid when they
// are owned copies (ApplyImmutable guarantees this along the op path; Apply
// callers own the whole tree).
type nodeChain struct {
	node      chord.JsonValue
	writeback func(chord.JsonValue) chord.JsonValue
}

func resolveChain(root chord.JsonValue, path Path, fullPath Path) (nodeChain, error) {
	current := nodeChain{node: root, writeback: func(v chord.JsonValue) chord.JsonValue { return v }}
	for _, seg := range path {
		switch n := current.node.(type) {
		case *jsonx.Obj:
			key, ok := seg.(string)
			if !ok {
				return nodeChain{}, &UnsafePathError{Segment: seg}
			}
			child, exists := n.Get(key)
			if !exists {
				return nodeChain{}, &PathError{Path: fullPath}
			}
			parent := n
			parentWriteback := current.writeback
			current = nodeChain{
				node: child,
				writeback: func(v chord.JsonValue) chord.JsonValue {
					parent.Set(key, v)
					return parentWriteback(parent)
				},
			}
		case []any:
			idx, ok := jsonx.ToFloat(seg)
			if !ok || idx != float64(int(idx)) || idx < 0 {
				return nodeChain{}, &UnsafePathError{Segment: seg}
			}
			i := int(idx)
			if i >= len(n) {
				return nodeChain{}, &PathError{Path: fullPath}
			}
			slice := n
			parentWriteback := current.writeback
			current = nodeChain{
				node: slice[i],
				writeback: func(v chord.JsonValue) chord.JsonValue {
					slice[i] = v
					return parentWriteback(slice)
				},
			}
		default:
			return nodeChain{}, &PathError{Path: fullPath}
		}
	}
	return current, nil
}

// ---------------------------------------------------------------------------
// Applier
// ---------------------------------------------------------------------------

// Apply applies decoded ops to a value and returns the resulting root.
// Like the TS apply, the target tree may be mutated in place; callers that
// need the previous revision untouched must use ApplyImmutable.
func Apply(target chord.JsonValue, ops []Op) (chord.JsonValue, error) {
	root := target
	var err error
	for _, op := range ops {
		root, err = applyOne(root, op)
		if err != nil {
			return nil, err
		}
	}
	return root, nil
}

func applyOne(root chord.JsonValue, op Op) (chord.JsonValue, error) {
	if err := validateOpStruct(op); err != nil {
		return nil, err
	}
	switch t := op.(type) {
	case *Replace:
		// Adopted, not copied; the consumer owns the batch it was handed.
		return t.Value, nil
	case *Splice:
		if err := AssertSafePath(t.Path); err != nil {
			return nil, err
		}
		chain, err := resolveChain(root, t.Path, t.Path)
		if err != nil {
			return nil, err
		}
		arr, ok := chain.node.([]any)
		if !ok {
			return nil, &PathError{Path: t.Path}
		}
		updated, err := spliceArray(arr, t.Index, t.Remove, t.Items)
		if err != nil {
			return nil, err
		}
		return chain.writeback(updated), nil
	case *Permute:
		if err := AssertSafePath(t.Path); err != nil {
			return nil, err
		}
		chain, err := resolveChain(root, t.Path, t.Path)
		if err != nil {
			return nil, err
		}
		arr, ok := chain.node.([]any)
		if !ok || len(arr) != len(t.Perm) {
			return nil, &PathError{Path: t.Path}
		}
		previous := make([]any, len(arr))
		copy(previous, arr)
		for i, p := range t.Perm {
			arr[i] = previous[p]
		}
		return root, nil
	case *Set:
		if err := AssertSafePath(t.Path); err != nil {
			return nil, err
		}
		if err := checkParentIndex(root, t.Path); err != nil {
			return nil, err
		}
		parentChain, err := resolveChain(root, t.Path[:len(t.Path)-1], t.Path)
		if err != nil {
			return nil, err
		}
		return writeAtKey(parentChain, t.Path[len(t.Path)-1], t.Value, t.Path)
	case *Delete:
		if err := AssertSafePath(t.Path); err != nil {
			return nil, err
		}
		parentChain, err := resolveChain(root, t.Path[:len(t.Path)-1], t.Path)
		if err != nil {
			return nil, err
		}
		key := t.Path[len(t.Path)-1]
		switch p := parentChain.node.(type) {
		case []any:
			idx, ok := jsonx.ToFloat(key)
			if !ok || idx != float64(int(idx)) {
				return nil, &UnsafePathError{Segment: key}
			}
			i := int(idx)
			if i >= len(p) {
				return nil, &PathError{Path: t.Path}
			}
			updated, err := spliceArray(p, i, 1, nil)
			if err != nil {
				return nil, err
			}
			return parentChain.writeback(updated), nil
		case *jsonx.Obj:
			s, ok := key.(string)
			if !ok {
				return nil, &UnsafePathError{Segment: key}
			}
			p.Delete(s)
			return root, nil
		default:
			return nil, &PathError{Path: t.Path}
		}
	case *Append:
		if err := AssertSafePath(t.Path); err != nil {
			return nil, err
		}
		if err := checkParentIndex(root, t.Path); err != nil {
			return nil, err
		}
		current, err := readStringAt(root, t.Path)
		if err != nil {
			return nil, err
		}
		parentChain, err := resolveChain(root, t.Path[:len(t.Path)-1], t.Path)
		if err != nil {
			return nil, err
		}
		return writeAtKey(parentChain, t.Path[len(t.Path)-1], current+t.Text, t.Path)
	case *Truncate:
		if err := AssertSafePath(t.Path); err != nil {
			return nil, err
		}
		if err := checkParentIndex(root, t.Path); err != nil {
			return nil, err
		}
		current, err := readStringAt(root, t.Path)
		if err != nil {
			return nil, err
		}
		count := t.Count
		if count > len(current) {
			count = len(current)
		}
		parentChain, err := resolveChain(root, t.Path[:len(t.Path)-1], t.Path)
		if err != nil {
			return nil, err
		}
		return writeAtKey(parentChain, t.Path[len(t.Path)-1], current[count:], t.Path)
	}
	return nil, &PathError{Path: Path{}}
}

// writeAtKey writes value under key into the chain's node (object create or
// replace; array index within range or append-one-past-end).
func writeAtKey(parentChain nodeChain, key Seg, value chord.JsonValue, fullPath Path) (chord.JsonValue, error) {
	switch p := parentChain.node.(type) {
	case *jsonx.Obj:
		s, ok := key.(string)
		if !ok {
			return nil, &UnsafePathError{Segment: key}
		}
		p.Set(s, value)
		return parentChain.writeback(p), nil
	case []any:
		idx, ok := jsonx.ToFloat(key)
		if !ok || idx != float64(int(idx)) || idx < 0 {
			return nil, &UnsafePathError{Segment: key}
		}
		i := int(idx)
		if i > len(p) {
			return nil, &UnsafePathError{Segment: idx}
		}
		if i == len(p) {
			grown := make([]any, len(p), len(p)+1)
			copy(grown, p)
			grown = append(grown, value)
			return parentChain.writeback(grown), nil
		}
		out := make([]any, len(p))
		copy(out, p)
		out[i] = value
		return parentChain.writeback(out), nil
	default:
		return nil, &PathError{Path: fullPath}
	}
}

// checkParentIndex enforces the "existing element or exactly one past the
// end" rule for array writes (assertIndexInRange).
func checkParentIndex(root chord.JsonValue, path Path) error {
	if len(path) == 0 {
		return nil
	}
	parent, err := resolveNodeValue(root, path[:len(path)-1])
	if err != nil {
		return err
	}
	if arr, ok := parent.([]any); ok {
		key := path[len(path)-1]
		idx, ok := jsonx.ToFloat(key)
		if !ok || idx != float64(int(idx)) {
			return &UnsafePathError{Segment: key}
		}
		if int(idx) > len(arr) {
			return &UnsafePathError{Segment: idx}
		}
	}
	return nil
}

func spliceArray(arr []any, index, remove int, items []chord.JsonValue) ([]any, error) {
	if index > len(arr) {
		return nil, &UnsafePathError{Segment: float64(index)}
	}
	if remove > len(arr)-index {
		remove = len(arr) - index
	}
	out := make([]any, 0, len(arr)-remove+len(items))
	out = append(out, arr[:index]...)
	out = append(out, items...)
	out = append(out, arr[index+remove:]...)
	return out, nil
}

func readStringAt(root chord.JsonValue, path Path) (string, error) {
	node, err := resolveNodeValue(root, path[:len(path)-1])
	if err != nil {
		return "", err
	}
	key := path[len(path)-1]
	var current any
	has := false
	switch p := node.(type) {
	case *jsonx.Obj:
		if s, ok := key.(string); ok {
			current, has = p.Get(s)
		} else {
			return "", &UnsafePathError{Segment: key}
		}
	case []any:
		idx, ok := jsonx.ToFloat(key)
		if !ok || idx != float64(int(idx)) {
			return "", &UnsafePathError{Segment: key}
		}
		i := int(idx)
		if i < len(p) {
			current, has = p[i], true
		}
	default:
		return "", &PathError{Path: path}
	}
	if !has {
		current = nil
	}
	s, ok := current.(string)
	if !ok {
		return "", &PathError{Path: path}
	}
	return s, nil
}

func resolveNodeValue(root chord.JsonValue, path Path) (chord.JsonValue, error) {
	node := root
	for _, seg := range path {
		switch n := node.(type) {
		case *jsonx.Obj:
			key, ok := seg.(string)
			if !ok {
				return nil, &UnsafePathError{Segment: seg}
			}
			child, exists := n.Get(key)
			if !exists {
				return nil, &PathError{Path: path}
			}
			node = child
		case []any:
			idx, ok := jsonx.ToFloat(seg)
			if !ok || idx != float64(int(idx)) || idx < 0 {
				return nil, &UnsafePathError{Segment: seg}
			}
			i := int(idx)
			if i >= len(n) {
				return nil, &PathError{Path: path}
			}
			node = n[i]
		default:
			return nil, &PathError{Path: path}
		}
	}
	return node, nil
}

// ---------------------------------------------------------------------------
// Immutable replay
// ---------------------------------------------------------------------------

// ApplyImmutable applies one decoded operation batch without mutating the
// previous immutable value.
func ApplyImmutable(target chord.JsonValue, ops []Op) (chord.JsonValue, error) {
	return ApplyImmutableBatches(target, [][]Op{ops})
}

// ApplyImmutableBatches applies decoded operation batches as one
// final-result-only replay. The "owned" memo shares object containers already
// copied within the same replay (mirroring the TS WeakSet); arrays are
// re-copied, which is value-identical.
func ApplyImmutableBatches(target chord.JsonValue, batches [][]Op) (chord.JsonValue, error) {
	root := target
	owned := map[any]bool{}
	for _, ops := range batches {
		for _, op := range ops {
			if replace, ok := op.(*Replace); ok {
				root = replace.Value
				continue
			}
			var err error
			root, err = copyContainers(root, opCopyPath(op), owned)
			if err != nil {
				return nil, err
			}
			root, err = applyOne(root, op)
			if err != nil {
				return nil, err
			}
		}
	}
	return root, nil
}

func opCopyPath(op Op) Path {
	switch t := op.(type) {
	case *Splice:
		return t.Path
	case *Permute:
		return t.Path
	case *Set:
		return t.Path[:len(t.Path)-1]
	case *Delete:
		return t.Path[:len(t.Path)-1]
	case *Append:
		return t.Path[:len(t.Path)-1]
	case *Truncate:
		return t.Path[:len(t.Path)-1]
	default:
		return Path{}
	}
}

// copyContainers shallow-copies the containers along path so that the apply
// step can mutate them safely. Untouched subtrees stay shared by reference
// with the previous revision.
func copyContainers(node chord.JsonValue, path Path, owned map[any]bool) (chord.JsonValue, error) {
	var self chord.JsonValue
	switch n := node.(type) {
	case *jsonx.Obj:
		if owned[n] {
			self = n
		} else {
			cloned := n.ShallowClone()
			owned[cloned] = true
			self = cloned
		}
	case []any:
		out := make([]any, len(n))
		copy(out, n)
		self = out
	default:
		return nil, &PathError{Path: path}
	}
	if len(path) == 0 {
		return self, nil
	}
	seg := path[0]
	var child chord.JsonValue
	has := false
	switch d := self.(type) {
	case *jsonx.Obj:
		key, ok := seg.(string)
		if !ok {
			return nil, &UnsafePathError{Segment: seg}
		}
		child, has = d.Get(key)
	case []any:
		idx, ok := jsonx.ToFloat(seg)
		if !ok || idx != float64(int(idx)) || idx < 0 {
			return nil, &UnsafePathError{Segment: seg}
		}
		i := int(idx)
		if i < len(d) {
			child, has = d[i], true
		}
	}
	if !has {
		return nil, &PathError{Path: path}
	}
	switch child.(type) {
	case *jsonx.Obj, []any:
	default:
		return nil, &PathError{Path: path}
	}
	copiedChild, err := copyContainers(child, path[1:], owned)
	if err != nil {
		return nil, err
	}
	switch d := self.(type) {
	case *jsonx.Obj:
		d.Set(seg.(string), copiedChild)
	case []any:
		d[int(toFloatOr(seg, 0))] = copiedChild
	}
	return self, nil
}

// validateOpStruct re-checks the payload invariants assertValidOp enforces on
// decoded tuples, for ops constructed directly in Go.
func validateOpStruct(op Op) error {
	switch t := op.(type) {
	case *Truncate:
		if t.Count < 0 {
			return fmt.Errorf("t shape")
		}
	case *Splice:
		if t.Index < 0 {
			return fmt.Errorf("p index")
		}
		if t.Remove < 0 {
			return fmt.Errorf("p remove")
		}
	case *Permute:
		seen := make([]bool, len(t.Perm))
		for _, index := range t.Perm {
			if index < 0 || index >= len(t.Perm) || seen[index] {
				return fmt.Errorf("m permutation is not a bijection")
			}
			seen[index] = true
		}
	}
	return nil
}
