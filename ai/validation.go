package ai

// validation.go ports utils/validation.ts. TypeBox 1.3.27 stores the kind in
// a hidden "~kind" string property and has no Symbol.for("TypeBox.Kind"), so
// the raw JSON-Schema coercion pass ALWAYS runs in the shipped code; this
// port replicates that behavior unconditionally.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

// ValidateToolCall finds a tool by name and validates the call arguments.
func ValidateToolCall(tools []Tool, toolCall *ToolCall) (any, error) {
	var tool *Tool
	for i := range tools {
		if tools[i].Name == toolCall.Name {
			tool = &tools[i]
			break
		}
	}
	if tool == nil {
		return nil, fmt.Errorf("Tool %q not found", toolCall.Name)
	}
	return ValidateToolArguments(tool, toolCall)
}

// ValidateToolArguments validates tool call arguments against the tool's
// schema, applying optional-null normalization, TypeBox conversion, and the
// raw JSON-Schema coercion pass.
func ValidateToolArguments(tool *Tool, toolCall *ToolCall) (any, error) {
	args := jsonx.Clone(toolCallArguments(toolCall))
	schema := tool.Parameters
	if schema == nil {
		schema = typebox.Object(nil)
	}
	normalizeOptionalNulls(args, schema.JSON())
	typebox.Convert(schema, args)

	validator := typebox.Compile(schema)
	coerced := coerceWithJSONSchema(args, schema.JSON())
	if coerced != args {
		// Mirror the in-place object overlay semantics.
		if obj, ok := args.(*jsonx.Obj); ok {
			if coercedObj, ok := coerced.(*jsonx.Obj); ok {
				for _, key := range obj.Keys() {
					obj.Delete(key)
				}
				for _, key := range coercedObj.Keys() {
					obj.Set(key, coercedObj.MustGet(key))
				}
			}
		} else {
			if validator.Check(coerced) {
				return coerced, nil
			}
		}
	}
	if validator.Check(args) {
		return args, nil
	}

	var errorLines []string
	for _, err := range validator.Errors(args) {
		errorLines = append(errorLines, fmt.Sprintf("  - %s: %s", formatValidationPath(err), err.Message))
	}
	joined := strings.Join(errorLines, "\n")
	if joined == "" {
		joined = "Unknown validation error"
	}
	errorMessage := fmt.Sprintf("Validation failed for tool %q:\n%s\n\nReceived arguments:\n%s",
		toolCall.Name, joined, prettyJSON(toolCallArguments(toolCall)))
	return nil, fmt.Errorf("%s", errorMessage)
}

func toolCallArguments(toolCall *ToolCall) *jsonx.Obj {
	if toolCall.Arguments == nil {
		return jsonx.NewObj()
	}
	return toolCall.Arguments
}

func formatValidationPath(err *typebox.Error) string {
	if err.Keyword == "required" {
		requiredProperties, _ := err.Params.Get("requiredProperties")
		if arr, ok := requiredProperties.([]any); ok && len(arr) > 0 {
			basePath := strings.TrimPrefix(err.InstancePath, "/")
			basePath = strings.ReplaceAll(basePath, "/", ".")
			if basePath != "" {
				return basePath + "." + fmt.Sprint(arr[0])
			}
			return fmt.Sprint(arr[0])
		}
	}
	path := strings.TrimPrefix(err.InstancePath, "/")
	path = strings.ReplaceAll(path, "/", ".")
	if path == "" {
		return "root"
	}
	return path
}

func prettyJSON(v any) string {
	// JSON.stringify(v, null, 2)
	return prettyJSONIndent(v, 0)
}

func prettyJSONIndent(v any, depth int) string {
	switch t := v.(type) {
	case *jsonx.Obj:
		if t.Len() == 0 {
			return "{}"
		}
		var sb strings.Builder
		sb.WriteString("{\n")
		for i, key := range t.Keys() {
			if i > 0 {
				sb.WriteString(",\n")
			}
			sb.WriteString(strings.Repeat("  ", depth+1))
			sb.WriteString(jsonx.Stringify(key))
			sb.WriteString(": ")
			sb.WriteString(prettyJSONIndent(t.MustGet(key), depth+1))
		}
		sb.WriteString("\n")
		sb.WriteString(strings.Repeat("  ", depth))
		sb.WriteString("}")
		return sb.String()
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		var sb strings.Builder
		sb.WriteString("[\n")
		for i, e := range t {
			if i > 0 {
				sb.WriteString(",\n")
			}
			sb.WriteString(strings.Repeat("  ", depth+1))
			sb.WriteString(prettyJSONIndent(e, depth+1))
		}
		sb.WriteString("\n")
		sb.WriteString(strings.Repeat("  ", depth))
		sb.WriteString("]")
		return sb.String()
	default:
		return jsonx.Stringify(v)
	}
}

// ---------------------------------------------------------------------------
// Raw JSON-Schema coercion (ported from validation.ts)
// ---------------------------------------------------------------------------

func schemaTypes(schema *jsonx.Obj) []string {
	if t, ok := schema.Get("type"); ok {
		if s, ok := t.(string); ok {
			return []string{s}
		}
		if arr, ok := t.([]any); ok {
			out := make([]string, 0, len(arr))
			for _, e := range arr {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

func matchesJSONType(value any, typ string) bool {
	switch typ {
	case "number":
		_, ok := jsonx.ToFloat(value)
		return ok
	case "integer":
		f, ok := jsonx.ToFloat(value)
		return ok && f == float64(int64(f))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "null":
		return value == nil
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(*jsonx.Obj)
		return ok
	default:
		return false
	}
}

func subSchemaValidator(schema *jsonx.Obj) *typebox.Validator {
	raw := &typebox.Schema{}
	if err := raw.UnmarshalJSON([]byte(jsonx.Stringify(schema))); err != nil {
		return nil
	}
	return typebox.Compile(raw)
}

func coercePrimitiveByType(value any, typ string) any {
	switch typ {
	case "number", "integer":
		if value == nil {
			return float64(0)
		}
		if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
			if parsed, ok := jsParseNumber(s); ok {
				return parsed
			}
		}
		if b, ok := value.(bool); ok {
			if b {
				return float64(1)
			}
			return float64(0)
		}
		return value
	case "boolean":
		if value == nil {
			return false
		}
		if s, ok := value.(string); ok {
			if s == "true" {
				return true
			}
			if s == "false" {
				return false
			}
		}
		if f, ok := jsonx.ToFloat(value); ok {
			if f == 1 {
				return true
			}
			if f == 0 {
				return false
			}
		}
		return value
	case "string":
		if value == nil {
			return ""
		}
		if f, ok := jsonx.ToFloat(value); ok {
			return jsonx.FormatNumber(f)
		}
		if b, ok := value.(bool); ok {
			if b {
				return "true"
			}
			return "false"
		}
		return value
	case "null":
		if value == "" || value == float64(0) || value == false {
			return nil
		}
		return value
	default:
		return value
	}
}

func jsParseNumber(s string) (float64, bool) {
	trimmed := strings.TrimSpace(s)
	switch strings.ToLower(trimmed) {
	case "infinity", "+infinity":
		return infPos, true
	case "-infinity":
		return infNeg, true
	}
	if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return f, true
	}
	// JS Number() also accepts hex/octal/binary integer literals.
	if u, err := strconv.ParseUint(trimmed, 0, 64); err == nil {
		return float64(u), true
	}
	return 0, false
}

var (
	infPos = posInf()
	infNeg = negInf()
)

func applySchemaObjectCoercion(value *jsonx.Obj, schema *jsonx.Obj) {
	properties, _ := schema.Get("properties")
	props, _ := properties.(*jsonx.Obj)
	definedKeys := map[string]bool{}
	if props != nil {
		for _, key := range props.Keys() {
			definedKeys[key] = true
			if !value.Has(key) {
				continue
			}
			value.Set(key, coerceWithJSONSchema(value.MustGet(key), props.MustGet(key).(*jsonx.Obj)))
		}
	}
	if additional, ok := schema.Get("additionalProperties"); ok {
		if addObj, ok := additional.(*jsonx.Obj); ok {
			for _, key := range value.Keys() {
				if definedKeys[key] {
					continue
				}
				value.Set(key, coerceWithJSONSchema(value.MustGet(key), addObj))
			}
		}
	}
}

func applySchemaArrayCoercion(value []any, schema *jsonx.Obj) {
	items, _ := schema.Get("items")
	if itemArr, ok := items.([]any); ok {
		for index := 0; index < len(value); index++ {
			if index >= len(itemArr) {
				continue
			}
			if itemSchema, ok := itemArr[index].(*jsonx.Obj); ok {
				value[index] = coerceWithJSONSchema(value[index], itemSchema)
			}
		}
		return
	}
	if itemSchema, ok := items.(*jsonx.Obj); ok {
		for index := 0; index < len(value); index++ {
			value[index] = coerceWithJSONSchema(value[index], itemSchema)
		}
	}
}

func coerceWithUnionSchema(value any, schemas []any) any {
	for _, s := range schemas {
		schema, ok := s.(*jsonx.Obj)
		if !ok {
			continue
		}
		if v := subSchemaValidator(schema); v != nil && v.Check(value) {
			return value
		}
	}
	for _, s := range schemas {
		schema, ok := s.(*jsonx.Obj)
		if !ok {
			continue
		}
		candidate := coerceWithJSONSchema(jsonx.Clone(value), schema)
		if v := subSchemaValidator(schema); v != nil && v.Check(candidate) {
			return candidate
		}
	}
	return value
}

func coerceWithJSONSchema(value any, schema *jsonx.Obj) any {
	nextValue := value
	if allOf, ok := schema.Get("allOf"); ok {
		if arr, ok := allOf.([]any); ok {
			for _, nested := range arr {
				if n, ok := nested.(*jsonx.Obj); ok {
					nextValue = coerceWithJSONSchema(nextValue, n)
				}
			}
		}
	}
	if anyOf, ok := schema.Get("anyOf"); ok {
		if arr, ok := anyOf.([]any); ok {
			nextValue = coerceWithUnionSchema(nextValue, arr)
		}
	}
	if oneOf, ok := schema.Get("oneOf"); ok {
		if arr, ok := oneOf.([]any); ok {
			nextValue = coerceWithUnionSchema(nextValue, arr)
		}
	}

	types := schemaTypes(schema)
	matchesUnionMember := false
	if len(types) > 1 {
		for _, typ := range types {
			if matchesJSONType(nextValue, typ) {
				matchesUnionMember = true
				break
			}
		}
	}
	if len(types) > 0 && !matchesUnionMember {
		for _, typ := range types {
			candidate := coercePrimitiveByType(nextValue, typ)
			if !jsonx.Equal(candidate, nextValue) {
				nextValue = candidate
				break
			}
		}
	}

	containsObject := false
	for _, typ := range types {
		if typ == "object" {
			containsObject = true
		}
	}
	if containsObject {
		if obj, ok := nextValue.(*jsonx.Obj); ok {
			applySchemaObjectCoercion(obj, schema)
		}
	}

	containsArray := false
	for _, typ := range types {
		if typ == "array" {
			containsArray = true
		}
	}
	if containsArray {
		if arr, ok := nextValue.([]any); ok {
			applySchemaArrayCoercion(arr, schema)
		}
	}
	return nextValue
}

func normalizeOptionalNulls(value any, schema *jsonx.Obj) {
	switch v := value.(type) {
	case []any:
		items, _ := schema.Get("items")
		if itemArr, ok := items.([]any); ok {
			for index := 0; index < len(v); index++ {
				if index < len(itemArr) {
					if itemSchema, ok := itemArr[index].(*jsonx.Obj); ok {
						normalizeOptionalNulls(v[index], itemSchema)
					}
				}
			}
		} else if itemSchema, ok := items.(*jsonx.Obj); ok {
			for _, item := range v {
				normalizeOptionalNulls(item, itemSchema)
			}
		}
		return
	case *jsonx.Obj:
	default:
		return
	}
	obj := value.(*jsonx.Obj)
	properties, _ := schema.Get("properties")
	props, ok := properties.(*jsonx.Obj)
	if !ok {
		return
	}
	required := map[string]bool{}
	if req, ok := schema.Get("required"); ok {
		if arr, ok := req.([]any); ok {
			for _, e := range arr {
				if s, ok := e.(string); ok {
					required[s] = true
				}
			}
		}
	}
	for _, key := range props.Keys() {
		if !obj.Has(key) {
			continue
		}
		propSchema, ok := props.MustGet(key).(*jsonx.Obj)
		if !ok {
			continue
		}
		isNull := obj.MustGet(key) == nil
		hasStringRef := false
		if ref, ok := propSchema.Get("$ref"); ok {
			_, hasStringRef = ref.(string)
		}
		if isNull && !required[key] && !hasStringRef {
			if v := subSchemaValidator(propSchema); v != nil && !v.Check(nil) {
				obj.Delete(key)
				continue
			}
		}
		normalizeOptionalNulls(obj.MustGet(key), propSchema)
	}
}
