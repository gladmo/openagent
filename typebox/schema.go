// Package typebox is a port of the npm "typebox" package (v1.3.27) subset
// used by pi: schema builders, JSON-Schema serialization with TypeBox key
// order, value conversion (Value.Convert), and validation (Compile/Check/
// Errors) with en_US localized error messages.
//
// TypeBox represents schemas as plain JSON-Schema objects carrying hidden
// markers: `~kind` (e.g. "Object", "String") and `~optional`. The markers are
// non-enumerable in JS and therefore never serialize; Go models them as
// unexported fields. A schema decoded from raw JSON has Kind "" (no marker),
// exactly like a hand-written JSON Schema object.
package typebox

import (
	"encoding/json"

	"github.com/gladmo/openagent/jsonx"
)

// Schema is a TypeBox/JSON-Schema value.
type Schema struct {
	// Hidden markers (never serialized, mirroring non-enumerable JS props).
	kind     string
	optional bool

	// JSON-Schema keywords, in TypeBox emission order.
	Type                 any        `json:"-"` // string or []string
	Required             []string   `json:"-"`
	Properties           *jsonx.Obj `json:"-"` // name -> *Schema
	Items                any        `json:"-"` // *Schema or []*Schema
	AdditionalProperties any        `json:"-"` // bool or *Schema
	AnyOf                []*Schema  `json:"-"`
	AllOf                []*Schema  `json:"-"`
	OneOf                []*Schema  `json:"-"`
	Enum                 []any      `json:"-"`
	Const                any        `json:"-"`
	MinLength            *float64   `json:"-"`
	MaxLength            *float64   `json:"-"`
	Pattern              *string    `json:"-"`
	Minimum              *float64   `json:"-"`
	Maximum              *float64   `json:"-"`
	ExclusiveMinimum     *float64   `json:"-"`
	ExclusiveMaximum     *float64   `json:"-"`

	// Extra holds option keys (description, title, ...) in insertion order.
	Extra *jsonx.Obj `json:"-"`
}

// Kind returns the TypeBox kind marker ("" for raw JSON-Schema values).
func (s *Schema) Kind() string { return s.kind }

// IsOptional reports whether the schema carries the ~optional marker.
func (s *Schema) IsOptional() bool { return s.optional }

// Property is a name/schema pair for Object construction.
type Property struct {
	Name   string
	Schema *Schema
}

// Prop builds a Property.
func Prop(name string, s *Schema) *Property { return &Property{Name: name, Schema: s} }

// Option carries TypeBox options (description, etc.).
type Option func(o *jsonx.Obj)

// Description sets the description option.
func Description(d string) Option {
	return func(o *jsonx.Obj) { o.Set("description", d) }
}

// NumberOption sets a numeric option such as minimum or maxLength.
func NumberOption(name string, v float64) Option {
	return func(o *jsonx.Obj) { o.Set(name, v) }
}

// StringOption sets a string option such as pattern.
func StringOption(name, v string) Option {
	return func(o *jsonx.Obj) { o.Set(name, v) }
}

func applyExtras(s *Schema, options []Option) {
	for _, opt := range options {
		if s.Extra == nil {
			s.Extra = jsonx.NewObj()
		}
		opt(s.Extra)
	}
}

// Object creates an Object schema. Required is auto-computed from
// non-optional properties in declaration order, exactly like typebox.
func Object(props []*Property, options ...Option) *Schema {
	s := &Schema{kind: "Object", Type: "object", Properties: jsonx.NewObj()}
	for _, p := range props {
		s.Properties.Set(p.Name, p.Schema)
		if !p.Schema.IsOptional() {
			s.Required = append(s.Required, p.Name)
		}
	}
	applyExtras(s, options)
	return s
}

// String creates a String schema.
func String(options ...Option) *Schema {
	s := &Schema{kind: "String", Type: "string"}
	applyExtras(s, options)
	return s
}

// Number creates a Number schema.
func Number(options ...Option) *Schema {
	s := &Schema{kind: "Number", Type: "number"}
	applyExtras(s, options)
	return s
}

// Integer creates an Integer schema (typebox uses a number pattern; pi tools
// treat it as JSON-Schema "integer" for validation).
func Integer(options ...Option) *Schema {
	s := &Schema{kind: "Integer", Type: "integer"}
	applyExtras(s, options)
	return s
}

// Boolean creates a Boolean schema.
func Boolean(options ...Option) *Schema {
	s := &Schema{kind: "Boolean", Type: "boolean"}
	applyExtras(s, options)
	return s
}

// Null creates a Null schema.
func Null(options ...Option) *Schema {
	s := &Schema{kind: "Null", Type: "null"}
	applyExtras(s, options)
	return s
}

// Array creates an Array schema over an items schema.
func Array(items *Schema, options ...Option) *Schema {
	s := &Schema{kind: "Array", Type: "array", Items: items}
	applyExtras(s, options)
	return s
}

// Optional marks a schema optional (hidden ~optional marker on a clone).
func Optional(s *Schema) *Schema {
	c := s.cloneShallow()
	c.optional = true
	return c
}

// Union creates an anyOf schema (Type.Union emits {anyOf:[...]} with no
// type field).
func Union(schemas []*Schema, options ...Option) *Schema {
	s := &Schema{kind: "Union", AnyOf: schemas}
	applyExtras(s, options)
	return s
}

// Literal creates a const schema (Type.Literal).
func Literal(value any, options ...Option) *Schema {
	s := &Schema{kind: "Literal", Const: value}
	applyExtras(s, options)
	return s
}

// Enum creates an enum schema (Type.Enum).
func Enum(values []any, options ...Option) *Schema {
	s := &Schema{kind: "Enum", Enum: values}
	applyExtras(s, options)
	return s
}

// Any creates a permissive schema (Type.Any).
func Any(options ...Option) *Schema {
	s := &Schema{kind: "Any"}
	applyExtras(s, options)
	return s
}

// Record creates a record schema (Type.Record): object with
// additionalProperties.
func Record(value *Schema, options ...Option) *Schema {
	s := &Schema{kind: "Record", Type: "object", AdditionalProperties: value}
	applyExtras(s, options)
	return s
}

func (s *Schema) cloneShallow() *Schema {
	c := *s
	return &c
}

// MarshalJSON serializes in exact TypeBox key order.
func (s *Schema) MarshalJSON() ([]byte, error) {
	return []byte(s.Serialize()), nil
}

// Serialize returns the JSON text TypeBox would emit (JSON.stringify of the
// schema object with hidden markers removed).
func (s *Schema) Serialize() string { return jsonx.Stringify(s.toValue()) }

func (s *Schema) toValue() *jsonx.Obj {
	o := jsonx.NewObj()
	if s.Type != nil {
		o.Set("type", s.Type)
	}
	if s.Required != nil && len(s.Required) > 0 {
		req := make([]any, len(s.Required))
		for i, r := range s.Required {
			req[i] = r
		}
		o.Set("required", req)
	}
	if s.Properties != nil {
		props := jsonx.NewObj()
		for _, k := range s.Properties.Keys() {
			ps := s.Properties.MustGet(k).(*Schema)
			props.Set(k, ps.toValue())
		}
		o.Set("properties", props)
	}
	if s.Items != nil {
		o.Set("items", itemsValue(s.Items))
	}
	if s.AdditionalProperties != nil {
		o.Set("additionalProperties", schemaOrBoolValue(s.AdditionalProperties))
	}
	if s.AnyOf != nil {
		o.Set("anyOf", schemaListValue(s.AnyOf))
	}
	if s.AllOf != nil {
		o.Set("allOf", schemaListValue(s.AllOf))
	}
	if s.OneOf != nil {
		o.Set("oneOf", schemaListValue(s.OneOf))
	}
	if s.Enum != nil {
		enum := make([]any, len(s.Enum))
		copy(enum, s.Enum)
		o.Set("enum", enum)
	}
	if s.Const != nil {
		o.Set("const", s.Const)
	}
	if s.MinLength != nil {
		o.Set("minLength", *s.MinLength)
	}
	if s.MaxLength != nil {
		o.Set("maxLength", *s.MaxLength)
	}
	if s.Pattern != nil {
		o.Set("pattern", *s.Pattern)
	}
	if s.Minimum != nil {
		o.Set("minimum", *s.Minimum)
	}
	if s.Maximum != nil {
		o.Set("maximum", *s.Maximum)
	}
	if s.ExclusiveMinimum != nil {
		o.Set("exclusiveMinimum", *s.ExclusiveMinimum)
	}
	if s.ExclusiveMaximum != nil {
		o.Set("exclusiveMaximum", *s.ExclusiveMaximum)
	}
	if s.Extra != nil {
		for _, k := range s.Extra.Keys() {
			o.Set(k, s.Extra.MustGet(k))
		}
	}
	return o
}

func itemsValue(items any) any {
	switch t := items.(type) {
	case *Schema:
		return t.toValue()
	case []*Schema:
		out := make([]any, len(t))
		for i, sub := range t {
			out[i] = sub.toValue()
		}
		return out
	default:
		return jsonx.Normalize(items)
	}
}

func schemaOrBoolValue(v any) any {
	if s, ok := v.(*Schema); ok {
		return s.toValue()
	}
	return v
}

func schemaListValue(list []*Schema) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s.toValue()
	}
	return out
}

// UnmarshalJSON decodes a raw JSON-Schema object into a Schema with no kind
// marker. Unknown keywords are preserved in Extra for round-tripping.
func (s *Schema) UnmarshalJSON(data []byte) error {
	v, err := jsonx.ParseBytes(data)
	if err != nil {
		return err
	}
	obj, ok := v.(*jsonx.Obj)
	if !ok {
		return &jsonConvError{msg: "typebox: schema must be a JSON object"}
	}
	*s = *fromJSON(obj)
	return nil
}

type jsonConvError struct{ msg string }

func (e *jsonConvError) Error() string { return e.msg }

func fromJSON(obj *jsonx.Obj) *Schema {
	s := &Schema{}
	known := map[string]bool{}
	if v, ok := obj.Get("type"); ok {
		s.Type = v
		known["type"] = true
	}
	if v, ok := obj.Get("required"); ok {
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				if str, ok := e.(string); ok {
					s.Required = append(s.Required, str)
				}
			}
		}
		known["required"] = true
	}
	if v, ok := obj.Get("properties"); ok {
		if props, ok := v.(*jsonx.Obj); ok {
			s.Properties = jsonx.NewObj()
			for _, k := range props.Keys() {
				if sub := valueToSchema(props.MustGet(k)); sub != nil {
					s.Properties.Set(k, sub)
				}
			}
		}
		known["properties"] = true
	}
	if v, ok := obj.Get("items"); ok {
		s.Items = valueToItems(v)
		known["items"] = true
	}
	if v, ok := obj.Get("additionalProperties"); ok {
		if b, ok := v.(bool); ok {
			s.AdditionalProperties = b
		} else if sub := valueToSchema(v); sub != nil {
			s.AdditionalProperties = sub
		}
		known["additionalProperties"] = true
	}
	for _, kw := range []struct {
		name string
		dst  *[]*Schema
	}{
		{"anyOf", &s.AnyOf},
		{"allOf", &s.AllOf},
		{"oneOf", &s.OneOf},
	} {
		if v, ok := obj.Get(kw.name); ok {
			if arr, ok := v.([]any); ok {
				for _, e := range arr {
					if sub := valueToSchema(e); sub != nil {
						*kw.dst = append(*kw.dst, sub)
					}
				}
			}
			known[kw.name] = true
		}
	}
	if v, ok := obj.Get("enum"); ok {
		if arr, ok := v.([]any); ok {
			s.Enum = append([]any{}, arr...)
		}
		known["enum"] = true
	}
	if v, ok := obj.Get("const"); ok {
		s.Const = v
		known["const"] = true
	}
	for _, kw := range []struct {
		name  string
		dst   **float64
		isStr bool
		str   **string
	}{
		{"minLength", &s.MinLength, false, nil},
		{"maxLength", &s.MaxLength, false, nil},
		{"minimum", &s.Minimum, false, nil},
		{"maximum", &s.Maximum, false, nil},
		{"exclusiveMinimum", &s.ExclusiveMinimum, false, nil},
		{"exclusiveMaximum", &s.ExclusiveMaximum, false, nil},
		{"pattern", nil, true, &s.Pattern},
	} {
		if v, ok := obj.Get(kw.name); ok {
			if kw.isStr {
				if str, ok := v.(string); ok {
					*kw.str = &str
				}
			} else if f, ok := jsonx.ToFloat(v); ok {
				*kw.dst = &f
			}
			known[kw.name] = true
		}
	}
	// Preserve remaining keys for round-trips.
	for _, k := range obj.Keys() {
		if !known[k] {
			if s.Extra == nil {
				s.Extra = jsonx.NewObj()
			}
			s.Extra.Set(k, obj.MustGet(k))
		}
	}
	return s
}

func valueToItems(v any) any {
	if arr, ok := v.([]any); ok {
		out := make([]*Schema, 0, len(arr))
		for _, e := range arr {
			if sub := valueToSchema(e); sub != nil {
				out = append(out, sub)
			}
		}
		return out
	}
	return valueToSchema(v)
}

func valueToSchema(v any) *Schema {
	if obj, ok := v.(*jsonx.Obj); ok {
		return fromJSON(obj)
	}
	return nil
}

// SchemaFromJSON converts a decoded JSON value (jsonx model) into a raw
// Schema. Returns nil for non-objects.
func SchemaFromJSON(v any) *Schema {
	if obj, ok := v.(*jsonx.Obj); ok {
		return fromJSON(obj)
	}
	return nil
}

// JSON returns the schema as a jsonx object value.
func (s *Schema) JSON() *jsonx.Obj { return s.toValue() }

var _ json.Marshaler = (*Schema)(nil)
var _ json.Unmarshaler = (*Schema)(nil)
