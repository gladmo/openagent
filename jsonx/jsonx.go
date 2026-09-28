// Package jsonx is a JavaScript-compatible JSON value model and codec.
//
// The TypeScript sources rely on platform JSON semantics that encoding/json
// does not provide:
//   - object key insertion order (JSON.stringify preserves it; the assistant
//     frame encoder compares JSON.stringify snapshots for equality)
//   - JS number-to-string formatting (shortest round-trip, fixed notation for
//     1e-7 <= |x| < 1e21, exponent form otherwise, NaN/Infinity -> null)
//   - structuredClone deep copies of JSON-shaped values
//
// Value is the Go equivalent of a JS value that has been restricted to JSON:
//
//	nil          <-> null / undefined (absent)
//	bool         <-> boolean
//	float64      <-> number
//	int / int64  <-> number (convenience; normalized to float64 on Clone)
//	string       <-> string
//	[]any        <-> array
//	*Obj         <-> object (insertion-ordered)
//
// json.RawMessage is accepted by Marshal wherever a pre-encoded JSON value is
// needed.
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// Obj is an insertion-ordered string-keyed map, mirroring a JS object.
type Obj struct {
	keys []string
	m    map[string]any
}

// NewObj returns an empty ordered object.
func NewObj() *Obj { return &Obj{m: map[string]any{}} }

// ObjFrom builds an ordered object from alternating key/value pairs.
func ObjFrom(kv ...any) *Obj {
	o := NewObj()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// Set inserts or replaces key with value, preserving the original insertion
// position when the key already exists (JS assignment semantics).
func (o *Obj) Set(key string, value any) {
	if _, ok := o.m[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.m[key] = value
}

// Get returns the value for key and whether it is present.
func (o *Obj) Get(key string) (any, bool) {
	v, ok := o.m[key]
	return v, ok
}

// MustGet returns the value for key or nil.
func (o *Obj) MustGet(key string) any {
	return o.m[key]
}

// Has reports whether key is present (even with a nil value).
func (o *Obj) Has(key string) bool {
	_, ok := o.m[key]
	return ok
}

// Delete removes key, no-op when absent.
func (o *Obj) Delete(key string) {
	if _, ok := o.m[key]; !ok {
		return
	}
	delete(o.m, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			return
		}
	}
}

// Len returns the number of entries.
func (o *Obj) Len() int { return len(o.keys) }

// Keys returns the keys in insertion order.
func (o *Obj) Keys() []string {
	out := make([]string, len(o.keys))
	copy(out, o.keys)
	return out
}

// Entries returns key/value pairs in insertion order.
func (o *Obj) Entries() [][2]any {
	out := make([][2]any, 0, len(o.keys))
	for _, k := range o.keys {
		out = append(out, [2]any{k, o.m[k]})
	}
	return out
}

// ShallowClone returns a copy of the object whose values still reference the
// original children (one-level copy).
func (o *Obj) ShallowClone() *Obj {
	c := &Obj{keys: make([]string, len(o.keys)), m: make(map[string]any, len(o.m))}
	copy(c.keys, o.keys)
	for k, v := range o.m {
		c.m[k] = v
	}
	return c
}

// Clone returns a deep copy.
func (o *Obj) Clone() *Obj {
	c := NewObj()
	for _, k := range o.keys {
		c.Set(k, Clone(o.m[k]))
	}
	return c
}

// Equal reports deep equality (numbers compared as float64).
func (o *Obj) Equal(other *Obj) bool {
	if other == nil {
		return false
	}
	if len(o.keys) != len(other.keys) {
		return false
	}
	for _, k := range o.keys {
		v2, ok := other.m[k]
		if !ok || !Equal(o.m[k], v2) {
			return false
		}
	}
	return true
}

// Clone deep-copies a JSON-shaped value. Following structuredClone of a value
// that only contains JSON data, ints are normalized to float64 and nil slices
// become empty arrays only when they were created as []any(nil) by Go code;
// JSON round-trips are stable.
func Clone(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case bool:
		return t
	case string:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	case float64:
		return t
	case json.Number:
		f, _ := t.Float64()
		return f
	case json.RawMessage:
		var out any
		if err := json.Unmarshal(t, &out); err != nil {
			return nil
		}
		return normalize(&out)
	case *Obj:
		return t.Clone()
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = Clone(e)
		}
		return out
	default:
		// Structs and Go maps marshal through encoding/json then normalize.
		b, err := json.Marshal(t)
		if err != nil {
			return nil
		}
		var out any
		if err := json.Unmarshal(b, &out); err != nil {
			return nil
		}
		return normalize(&out)
	}
}

func normalize(p *any) any {
	switch t := (*p).(type) {
	case map[string]any:
		o := NewObj()
		// encoding/json unmarshals maps with sorted keys; ordering is
		// canonicalized, which is deterministic.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			v := t[k]
			o.Set(k, normalize(&v))
		}
		return o
	case []any:
		for i := range t {
			t[i] = normalize(&t[i])
		}
		return t
	case json.Number:
		f, _ := t.Float64()
		return f
	default:
		return *p
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Equal reports deep JSON equality.
func Equal(a, b any) bool {
	a, b = Clone(a), Clone(b)
	return equalValues(a, b)
}

func equalValues(a, b any) bool {
	switch at := a.(type) {
	case nil:
		return b == nil
	case bool:
		bt, ok := b.(bool)
		return ok && at == bt
	case string:
		bt, ok := b.(string)
		return ok && at == bt
	case float64:
		bt, ok := b.(float64)
		return ok && at == bt
	case *Obj:
		return at.Equal(toObj(b))
	case []any:
		bt, ok := b.([]any)
		if !ok || len(at) != len(bt) {
			return false
		}
		for i := range at {
			if !equalValues(at[i], bt[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func toObj(v any) *Obj {
	if o, ok := v.(*Obj); ok {
		return o
	}
	return nil
}

// Parse decodes JSON text exactly like JSON.parse: any JSON value, trailing
// garbage rejected. Objects come back as *Obj (order preserved), numbers as
// float64.
func Parse(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	v, err := parseTokenValue(dec)
	if err != nil {
		return nil, err
	}
	if err := ensureTrailingConsumed(dec); err != nil {
		return nil, err
	}
	return v, nil
}

func parseTokenValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return tokenValue(dec, tok)
}

func tokenValue(dec *json.Decoder, tok json.Token) (any, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := NewObj()
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("invalid object key")
				}
				val, err := parseTokenValue(dec)
				if err != nil {
					return nil, err
				}
				obj.Set(key, val)
			}
			if _, err := dec.Token(); err != nil { // consume '}'
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				val, err := parseTokenValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil { // consume ']'
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return nil, err
		}
		return f, nil
	default:
		return tok, nil // nil, bool, string
	}
}

func ensureTrailingConsumed(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if err == nil {
		return fmt.Errorf("Unexpected non-whitespace character after JSON")
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("Unexpected non-whitespace character after JSON")
	}
	return nil
}

// ParseBytes is Parse over a byte slice.
func ParseBytes(b []byte) (any, error) { return Parse(string(b)) }

// Stringify encodes v with JSON.stringify semantics. nil numbers (NaN,
// +/-Inf) encode as null. Strings are escaped per JSON.stringify (which for
// the standard JSON repertoire matches encoding/json except that JS escapes
// lone surrogates as \uXXXX escape sequences of the surrogate code units).
func Stringify(v any) string {
	var sb strings.Builder
	writeValue(&sb, v)
	return sb.String()
}

func writeValue(sb *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		if t {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case string:
		writeString(sb, t)
	case int:
		sb.WriteString(FormatNumber(float64(t)))
	case int64:
		sb.WriteString(FormatNumber(float64(t)))
	case uint64:
		sb.WriteString(FormatNumber(float64(t)))
	case float64:
		sb.WriteString(FormatNumber(t))
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			sb.WriteString("null")
			return
		}
		sb.WriteString(FormatNumber(f))
	case *Obj:
		sb.WriteByte('{')
		for i, k := range t.keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeString(sb, k)
			sb.WriteByte(':')
			writeValue(sb, t.m[k])
		}
		sb.WriteByte('}')
	case []any:
		sb.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeValue(sb, e)
		}
		sb.WriteByte(']')
	case []string:
		sb.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeString(sb, e)
		}
		sb.WriteByte(']')
	case json.RawMessage:
		sb.Write(bytes.TrimLeft(t, " \t\r\n"))
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			sb.WriteByte('[')
			for i := 0; i < rv.Len(); i++ {
				if i > 0 {
					sb.WriteByte(',')
				}
				writeValue(sb, rv.Index(i).Interface())
			}
			sb.WriteByte(']')
		case reflect.Map:
			// Sort keys for deterministic output (mirrors normalize()).
			keys := make([]string, 0, rv.Len())
			for _, k := range rv.MapKeys() {
				keys = append(keys, fmt.Sprint(k.Interface()))
			}
			sortStrings(keys)
			sb.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					sb.WriteByte(',')
				}
				writeString(sb, k)
				sb.WriteByte(':')
				writeValue(sb, rv.MapIndex(reflect.ValueOf(k)).Interface())
			}
			sb.WriteByte('}')
		case reflect.Ptr, reflect.Interface:
			if rv.IsNil() {
				sb.WriteString("null")
			} else {
				writeValue(sb, rv.Elem().Interface())
			}
		default:
			b, err := json.Marshal(t)
			if err != nil {
				sb.WriteString("null")
				return
			}
			sb.Write(b)
		}
	}
}

// FormatNumber formats a float64 exactly like JavaScript Number -> string:
// shortest round-trip digits, fixed notation when the decimal exponent n
// (value = d.ddd × 10^n) satisfies -6 <= n <= 20, exponential otherwise.
func FormatNumber(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null"
	}
	if f == 0 {
		return "0" // -0 stringifies as "0" in JS
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	parts := strings.SplitN(s, "e", 2)
	mantissa, exp := parts[0], parts[1]
	e, _ := strconv.Atoi(exp)
	if e >= -6 && e <= 20 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	sign := "+"
	if e < 0 {
		sign = "-"
		e = -e
	}
	return mantissa + "e" + sign + strconv.Itoa(e)
}

// writeString escapes a string like JSON.stringify: control characters as
// \b \t \n \f \r or \u00XX, quote and backslash escaped, all other characters
// literal (including invalid UTF-8 handling through replacement).
func writeString(sb *strings.Builder, s string) {
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\b':
			sb.WriteString("\\b")
		case '\t':
			sb.WriteString("\\t")
		case '\n':
			sb.WriteString("\\n")
		case '\f':
			sb.WriteString("\\f")
		case '\r':
			sb.WriteString("\\r")
		default:
			if r < 0x20 {
				fmt.Fprintf(sb, "\\u%04x", r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
}

// IsNumber reports whether v is a numeric value in the jsonx model.
func IsNumber(v any) bool {
	switch v.(type) {
	case float64, int, int64, uint64, json.Number:
		return true
	}
	return false
}

// ToFloat coerces a jsonx numeric value to float64.
func ToFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	}
	return 0, false
}

// Normalize converts any Go-side value (structs, maps, RawMessage) into the
// jsonx value model.
func Normalize(v any) any { return Clone(v) }

// MustParseString parses s and panics on error (test helper).
func MustParseString(s string) any {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}
