package typebox

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// Convert mirrors typebox Value.Convert: walks typed schemas and converts
// values that are string-encodable into their target types. Raw schemas
// (Kind == "") are left untouched, exactly like the JS implementation, whose
// dispatch keys off the hidden ~kind markers.
func Convert(schema *Schema, value any) any {
	if schema == nil {
		return value
	}
	switch schema.kind {
	case "Number":
		if v, ok := tryNumber(value); ok {
			return v
		}
		return value
	case "Integer":
		if v, ok := tryNumber(value); ok {
			return math.Trunc(v.(float64))
		}
		return value
	case "String":
		if v, ok := tryString(value); ok {
			return v
		}
		return value
	case "Boolean":
		if v, ok := tryBoolean(value); ok {
			return v
		}
		return value
	case "Object":
		if obj, ok := value.(*jsonx.Obj); ok {
			if schema.Properties != nil {
				for _, key := range schema.Properties.Keys() {
					if !obj.Has(key) {
						continue
					}
					prop := schema.Properties.MustGet(key).(*Schema)
					obj.Set(key, Convert(prop, obj.MustGet(key)))
				}
			}
			if sub, ok := schema.AdditionalProperties.(*Schema); ok {
				for _, key := range obj.Keys() {
					if schema.Properties != nil && schema.Properties.Has(key) {
						continue
					}
					obj.Set(key, Convert(sub, obj.MustGet(key)))
				}
			}
		}
		return value
	case "Array":
		if items, ok := schema.Items.(*Schema); ok {
			if arr, ok := value.([]any); ok {
				for i := range arr {
					arr[i] = Convert(items, arr[i])
				}
			}
		}
		return value
	case "Union":
		for _, sub := range schema.AnyOf {
			if checkSchema(sub, value) {
				return value
			}
		}
		for _, sub := range schema.AnyOf {
			candidate := Convert(sub, jsonx.Clone(value))
			if checkSchema(schema, candidate) {
				return candidate
			}
		}
		return value
	default:
		return value
	}
}

// tryNumber ports partial-json-adjacent semantics of typebox's TryNumber:
// boolean -> 1/0, number -> itself, null -> 0, string -> Number(string)
// (NaN counts as a number, matching JS), undefined -> 0.
func tryNumber(value any) (any, bool) {
	switch t := value.(type) {
	case bool:
		if t {
			return float64(1), true
		}
		return float64(0), true
	case float64:
		return t, true
	case int:
		return float64(t), true
	case nil:
		return float64(0), true
	case string:
		return jsNumber(t), true
	default:
		return value, false
	}
}

// jsNumber implements JS Number(str): trims whitespace, "" -> 0,
// "Infinity"/"-Infinity"/"+Infinity", hex/octal/binary prefixes, decimal
// float otherwise. Returns NaN when unparseable.
func jsNumber(s string) float64 {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0
	}
	switch strings.ToLower(trimmed) {
	case "infinity", "+infinity":
		return math.Inf(1)
	case "-infinity":
		return math.Inf(-1)
	case "true":
		return 1
	case "false":
		return 0
	}
	if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return f
	}
	// JS accepts hex (0x), octal (0o) and binary (0b) integer literals.
	neg := false
	body := trimmed
	if strings.HasPrefix(body, "-") || strings.HasPrefix(body, "+") {
		neg = body[0] == '-'
		body = body[1:]
	}
	if len(body) > 2 {
		var base int
		switch body[:2] {
		case "0x", "0X":
			base = 16
		case "0o", "0O":
			base = 8
		case "0b", "0B":
			base = 2
		}
		if base != 0 {
			if u, err := strconv.ParseUint(body[2:], base, 64); err == nil {
				f := float64(u)
				if neg {
					f = -f
				}
				return f
			}
		}
	}
	return math.NaN()
}

// tryString ports TryString: number -> Number.toString, boolean -> "true"/
// "false", null -> "null", undefined -> "".
func tryString(value any) (any, bool) {
	switch t := value.(type) {
	case float64:
		return jsonx.FormatNumber(t), true
	case int:
		return jsonx.FormatNumber(float64(t)), true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	case nil:
		return "null", true
	case string:
		return t, true
	default:
		return value, false
	}
}

// tryBoolean ports TryBoolean: number 0/1, string "true"/"false" (any case
// for the words) or "0"/"1".
func tryBoolean(value any) (any, bool) {
	switch t := value.(type) {
	case float64:
		if t == 0 {
			return false, true
		}
		if t == 1 {
			return true, true
		}
		return value, false
	case string:
		switch strings.ToLower(t) {
		case "true":
			return true, true
		case "false":
			return false, true
		case "0":
			return false, true
		case "1":
			return true, true
		}
		return value, false
	default:
		return value, false
	}
}

var patternCache = map[string]*regexp.Regexp{}

func patternMatches(pattern, s string) bool {
	re, ok := patternCache[pattern]
	if !ok {
		compiled, compileErr := regexp.Compile(pattern)
		if compileErr != nil {
			// A broken pattern never matches, mirroring JS RegExp throws that
			// typebox surfaces as check failures.
			re = nil
		} else {
			re = compiled
		}
		patternCache[pattern] = re
	}
	if re == nil {
		return false
	}
	return re.MatchString(s)
}
