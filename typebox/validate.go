package typebox

import (
	"fmt"
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// Error is a localized validation error, mirroring typebox's
// TLocalizedValidationError.
type Error struct {
	Keyword      string
	SchemaPath   string
	InstancePath string
	Params       *jsonx.Obj
	Message      string
}

func (e *Error) Error() string { return e.Message }

// message renders the en_US locale table for the supported keywords.
func (e *Error) message() string {
	p := func(name string) any {
		if e.Params == nil {
			return nil
		}
		return e.Params.MustGet(name)
	}
	switch e.Keyword {
	case "additionalProperties":
		return "must not have additional properties"
	case "anyOf":
		return "must match a schema in anyOf"
	case "boolean":
		return "schema is false"
	case "const":
		return "must be equal to constant"
	case "enum":
		return "must be equal to one of the allowed values"
	case "exclusiveMaximum":
		return fmt.Sprintf("must be %v %v", p("comparison"), p("limit"))
	case "exclusiveMinimum":
		return fmt.Sprintf("must be %v %v", p("comparison"), p("limit"))
	case "maxLength":
		return fmt.Sprintf("must not have more than %v characters", p("limit"))
	case "maximum":
		return fmt.Sprintf("must be %v %v", p("comparison"), p("limit"))
	case "minLength":
		return fmt.Sprintf("must not have fewer than %v characters", p("limit"))
	case "minimum":
		return fmt.Sprintf("must be %v %v", p("comparison"), p("limit"))
	case "oneOf":
		return "must match exactly one schema in oneOf"
	case "pattern":
		return fmt.Sprintf("must match pattern %q", p("pattern"))
	case "required":
		return fmt.Sprintf("must have required properties %s", strings.Join(stringSlice(p("requiredProperties")), ", "))
	case "type":
		if t, ok := p("type").(string); ok {
			return fmt.Sprintf("must be %s", t)
		}
		types := stringSlice(p("type"))
		return fmt.Sprintf("must be either %s", strings.Join(types, " or "))
	default:
		return "an unknown validation error occurred"
	}
}

func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, fmt.Sprint(e))
	}
	return out
}

// Validator mirrors the return type of typebox Compile().
type Validator struct{ schema *Schema }

// Compile builds a validator for the schema.
func Compile(schema *Schema) *Validator { return &Validator{schema: schema} }

// Check reports whether value satisfies the schema.
func (v *Validator) Check(value any) bool {
	return checkSchema(v.schema, jsonx.Clone(value))
}

// Errors returns all validation errors for value, in typebox engine
// evaluation order.
func (v *Validator) Errors(value any) []*Error {
	ctx := &errorContext{}
	value = jsonx.Clone(value)
	valid := errorSchema(ctx, "#", "", v.schema, value)
	if valid {
		return nil
	}
	out := make([]*Error, 0, len(ctx.errors))
	for i := range ctx.errors {
		e := &ctx.errors[i]
		e.Message = e.message()
		out = append(out, e)
	}
	return out
}

type errorContext struct{ errors []Error }

func (c *errorContext) addError(keyword, schemaPath, instancePath string, params *jsonx.Obj) bool {
	c.errors = append(c.errors, Error{
		Keyword:      keyword,
		SchemaPath:   schemaPath,
		InstancePath: instancePath,
		Params:       params,
	})
	return false
}

// ---------------------------------------------------------------------------
// Check
// ---------------------------------------------------------------------------

func checkSchema(s *Schema, value any) bool {
	if s == nil {
		return true
	}
	if s.Type != nil && !checkType(s.Type, value) {
		return false
	}
	if obj, ok := value.(*jsonx.Obj); ok {
		for _, key := range s.Required {
			if !obj.Has(key) {
				return false
			}
		}
		if !checkAdditionalProperties(s, obj) {
			return false
		}
		if s.Properties != nil {
			for _, key := range s.Properties.Keys() {
				if !obj.Has(key) {
					continue
				}
				if !checkSchema(s.Properties.MustGet(key).(*Schema), obj.MustGet(key)) {
					return false
				}
			}
		}
	}
	if arr, ok := value.([]any); ok {
		if !checkItems(s, arr) {
			return false
		}
	}
	if str, ok := value.(string); ok {
		if !checkStringConstraints(s, str) {
			return false
		}
	}
	if f, ok := jsonx.ToFloat(value); ok {
		if !checkNumberConstraints(s, f) {
			return false
		}
	}
	if s.Const != nil && !jsonx.Equal(s.Const, value) {
		return false
	}
	if s.Enum != nil && !enumMatches(s.Enum, value) {
		return false
	}
	for _, sub := range s.AllOf {
		if !checkSchema(sub, value) {
			return false
		}
	}
	if s.AnyOf != nil && len(s.AnyOf) > 0 {
		any := false
		for _, sub := range s.AnyOf {
			if checkSchema(sub, value) {
				any = true
				break
			}
		}
		if !any {
			return false
		}
	}
	if s.OneOf != nil && len(s.OneOf) > 0 {
		count := 0
		for _, sub := range s.OneOf {
			if checkSchema(sub, value) {
				count++
			}
		}
		if count != 1 {
			return false
		}
	}
	return true
}

func checkType(typ any, value any) bool {
	match := func(t string) bool {
		switch t {
		case "object":
			_, ok := value.(*jsonx.Obj)
			return ok
		case "array":
			_, ok := value.([]any)
			return ok
		case "boolean":
			_, ok := value.(bool)
			return ok
		case "integer":
			f, ok := jsonx.ToFloat(value)
			return ok && !floatIsNonInteger(f)
		case "number":
			_, ok := jsonx.ToFloat(value)
			return ok
		case "null":
			return value == nil
		case "string":
			_, ok := value.(string)
			return ok
		default:
			return true
		}
	}
	if t, ok := typ.(string); ok {
		return match(t)
	}
	for _, t := range stringSlice(typ) {
		if match(t) {
			return true
		}
	}
	return false
}

func floatIsNonInteger(f float64) bool {
	return f != float64(int64(f))
}

func checkAdditionalProperties(s *Schema, obj *jsonx.Obj) bool {
	if s.AdditionalProperties == nil {
		return true
	}
	if b, ok := s.AdditionalProperties.(bool); ok {
		if !b {
			if s.Properties == nil {
				return obj.Len() == 0
			}
			for _, k := range obj.Keys() {
				if !s.Properties.Has(k) {
					return false
				}
			}
		}
		return true
	}
	if sub, ok := s.AdditionalProperties.(*Schema); ok {
		for _, k := range obj.Keys() {
			if s.Properties != nil && s.Properties.Has(k) {
				continue
			}
			if !checkSchema(sub, obj.MustGet(k)) {
				return false
			}
		}
	}
	return true
}

func checkItems(s *Schema, arr []any) bool {
	switch items := s.Items.(type) {
	case *Schema:
		for _, e := range arr {
			if !checkSchema(items, e) {
				return false
			}
		}
	case []*Schema:
		for i, sub := range items {
			if i >= len(arr) {
				break
			}
			if !checkSchema(sub, arr[i]) {
				return false
			}
		}
	}
	return true
}

func checkStringConstraints(s *Schema, str string) bool {
	if s.MinLength != nil && float64(len([]rune(str))) < *s.MinLength {
		return false
	}
	if s.MaxLength != nil && float64(len([]rune(str))) > *s.MaxLength {
		return false
	}
	if s.Pattern != nil && !patternMatches(*s.Pattern, str) {
		return false
	}
	return true
}

func checkNumberConstraints(s *Schema, f float64) bool {
	if s.Minimum != nil && f < *s.Minimum {
		return false
	}
	if s.Maximum != nil && f > *s.Maximum {
		return false
	}
	if s.ExclusiveMinimum != nil && f <= *s.ExclusiveMinimum {
		return false
	}
	if s.ExclusiveMaximum != nil && f >= *s.ExclusiveMaximum {
		return false
	}
	return true
}

func enumMatches(enum []any, value any) bool {
	for _, e := range enum {
		if jsonx.Equal(e, value) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

func errorSchema(ctx *errorContext, schemaPath, instancePath string, s *Schema, value any) bool {
	if s == nil {
		return true
	}
	result := true
	// type
	if s.Type != nil && !checkType(s.Type, value) {
		params := jsonx.NewObj()
		params.Set("type", s.Type)
		result = ctx.addError("type", schemaPath, instancePath, params) && result
	}
	// object-keyed keywords, only for object values
	if obj, ok := value.(*jsonx.Obj); ok {
		result = errorObjectKeywords(ctx, schemaPath, instancePath, s, obj) && result
	}
	if arr, ok := value.([]any); ok {
		result = errorArrayKeywords(ctx, schemaPath, instancePath, s, arr) && result
	}
	if str, ok := value.(string); ok {
		result = errorStringKeywords(ctx, schemaPath, instancePath, s, str) && result
	}
	if f, ok := jsonx.ToFloat(value); ok {
		result = errorNumberKeywords(ctx, schemaPath, instancePath, s, f) && result
	}
	result = errorCombinatorKeywords(ctx, schemaPath, instancePath, s, value) && result
	return result
}

func errorObjectKeywords(ctx *errorContext, schemaPath, instancePath string, s *Schema, obj *jsonx.Obj) bool {
	result := true
	// required
	if s.Required != nil {
		missing := []any{}
		for _, key := range s.Required {
			if !obj.Has(key) {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			params := jsonx.NewObj()
			params.Set("requiredProperties", missing)
			result = ctx.addError("required", schemaPath+"/required", instancePath, params) && result
		}
	}
	// additionalProperties
	if s.AdditionalProperties != nil {
		if b, ok := s.AdditionalProperties.(bool); ok && !b {
			var extra []any
			for _, k := range obj.Keys() {
				if s.Properties == nil || !s.Properties.Has(k) {
					extra = append(extra, k)
				}
			}
			if len(extra) > 0 {
				params := jsonx.NewObj()
				params.Set("additionalProperties", extra)
				result = ctx.addError("additionalProperties", schemaPath+"/additionalProperties", instancePath, params) && result
			}
		} else if sub, ok := s.AdditionalProperties.(*Schema); ok {
			for _, k := range obj.Keys() {
				if s.Properties != nil && s.Properties.Has(k) {
					continue
				}
				result = errorSchema(ctx, schemaPath+"/additionalProperties", instancePath+"/"+k, sub, obj.MustGet(k)) && result
			}
		}
	}
	// properties
	if s.Properties != nil {
		for _, key := range s.Properties.Keys() {
			if !obj.Has(key) {
				continue
			}
			sub := s.Properties.MustGet(key).(*Schema)
			result = errorSchema(ctx, schemaPath+"/properties/"+key, instancePath+"/"+key, sub, obj.MustGet(key)) && result
		}
	}
	return result
}

func errorArrayKeywords(ctx *errorContext, schemaPath, instancePath string, s *Schema, arr []any) bool {
	result := true
	switch items := s.Items.(type) {
	case *Schema:
		for i, e := range arr {
			result = errorSchema(ctx, schemaPath+"/items", fmt.Sprintf("%s/%d", instancePath, i), items, e) && result
		}
	case []*Schema:
		for i, sub := range items {
			if i >= len(arr) {
				break
			}
			result = errorSchema(ctx, fmt.Sprintf("%s/items/%d", schemaPath, i), fmt.Sprintf("%s/%d", instancePath, i), sub, arr[i]) && result
		}
	}
	return result
}

func errorStringKeywords(ctx *errorContext, schemaPath, instancePath string, s *Schema, str string) bool {
	result := true
	if s.MaxLength != nil && float64(len([]rune(str))) > *s.MaxLength {
		params := jsonx.NewObj()
		params.Set("limit", *s.MaxLength)
		result = ctx.addError("maxLength", schemaPath+"/maxLength", instancePath, params) && result
	}
	if s.MinLength != nil && float64(len([]rune(str))) < *s.MinLength {
		params := jsonx.NewObj()
		params.Set("limit", *s.MinLength)
		result = ctx.addError("minLength", schemaPath+"/minLength", instancePath, params) && result
	}
	if s.Pattern != nil && !patternMatches(*s.Pattern, str) {
		params := jsonx.NewObj()
		params.Set("pattern", *s.Pattern)
		result = ctx.addError("pattern", schemaPath+"/pattern", instancePath, params) && result
	}
	return result
}

func errorNumberKeywords(ctx *errorContext, schemaPath, instancePath string, s *Schema, f float64) bool {
	result := true
	emit := func(keyword, comparison string, limit *float64, ok bool) {
		if ok {
			params := jsonx.NewObj()
			params.Set("comparison", comparison)
			params.Set("limit", *limit)
			result = ctx.addError(keyword, schemaPath+"/"+keyword, instancePath, params) && result
		}
	}
	emit("exclusiveMaximum", "<", s.ExclusiveMaximum, s.ExclusiveMaximum != nil && f >= *s.ExclusiveMaximum)
	emit("exclusiveMinimum", ">", s.ExclusiveMinimum, s.ExclusiveMinimum != nil && f <= *s.ExclusiveMinimum)
	emit("maximum", "<=", s.Maximum, s.Maximum != nil && f > *s.Maximum)
	emit("minimum", ">=", s.Minimum, s.Minimum != nil && f < *s.Minimum)
	return result
}

func errorCombinatorKeywords(ctx *errorContext, schemaPath, instancePath string, s *Schema, value any) bool {
	result := true
	if s.Const != nil && !jsonx.Equal(s.Const, value) {
		params := jsonx.NewObj()
		params.Set("allowedValue", s.Const)
		result = ctx.addError("const", schemaPath+"/const", instancePath, params) && result
	}
	if s.Enum != nil && !enumMatches(s.Enum, value) {
		params := jsonx.NewObj()
		params.Set("allowedValues", s.Enum)
		result = ctx.addError("enum", schemaPath+"/enum", instancePath, params) && result
	}
	for _, sub := range s.AllOf {
		result = errorSchema(ctx, schemaPath+"/allOf", instancePath, sub, value) && result
	}
	if len(s.AnyOf) > 0 {
		matched := false
		for _, sub := range s.AnyOf {
			if checkSchema(sub, value) {
				matched = true
				break
			}
		}
		if !matched {
			for _, sub := range s.AnyOf {
				result = errorSchema(ctx, schemaPath+"/anyOf", instancePath, sub, value) && result
			}
			result = ctx.addError("anyOf", schemaPath+"/anyOf", instancePath, nil) && result
		}
	}
	if len(s.OneOf) > 0 {
		count := 0
		for _, sub := range s.OneOf {
			if checkSchema(sub, value) {
				count++
			}
		}
		if count != 1 {
			for _, sub := range s.OneOf {
				result = errorSchema(ctx, schemaPath+"/oneOf", instancePath, sub, value) && result
			}
			result = ctx.addError("oneOf", schemaPath+"/oneOf", instancePath, nil) && result
		}
	}
	return result
}
