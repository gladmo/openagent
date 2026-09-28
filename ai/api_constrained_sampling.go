package ai

// api_constrained_sampling.go ports api/constrained-sampling.ts: strict
// JSON-schema conversion for constrained sampling and grammar-tool helpers.

import (
	"encoding/json"

	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

// GrammarVariants mirrors the TS grammar variants.
type GrammarVariants struct {
	OpenAILark  string
	OpenAIRegex string
}

// ConstrainedSamplingConfig mirrors the TS union (Type discriminates).
type ConstrainedSamplingConfig struct {
	Type     string // "json_schema" | "grammar"
	Strict   string // "prefer" | "require" (json_schema)
	Variants GrammarVariants
}

// ParseConstrainedSampling decodes Tool.ConstrainedSampling (a jsonx value or
// a *ConstrainedSamplingConfig) into the config; nil when absent/false.
func ParseConstrainedSampling(value any) *ConstrainedSamplingConfig {
	switch t := value.(type) {
	case nil:
		return nil
	case bool:
		return nil
	case *ConstrainedSamplingConfig:
		return t
	case ConstrainedSamplingConfig:
		return &t
	}
	obj, ok := value.(*jsonx.Obj)
	if !ok {
		return nil
	}
	config := &ConstrainedSamplingConfig{}
	if typeValue, ok := obj.Get("type"); ok {
		if s, ok := typeValue.(string); ok {
			config.Type = s
		}
	}
	if strictValue, ok := obj.Get("strict"); ok {
		if s, ok := strictValue.(string); ok {
			config.Strict = s
		}
	}
	if variantsValue, ok := obj.Get("variants"); ok {
		if variants, ok := variantsValue.(*jsonx.Obj); ok {
			if v, ok := variants.Get("openai_lark"); ok {
				if s, ok := v.(string); ok {
					config.Variants.OpenAILark = s
				}
			}
			if v, ok := variants.Get("openai_regex"); ok {
				if s, ok := v.(string); ok {
					config.Variants.OpenAIRegex = s
				}
			}
		}
	}
	return config
}

// csError is a plain message error (stands in for JS `new Error(msg)`).
type csError struct{ msg string }

func (e *csError) Error() string { return e.msg }

// unsupportedStrictJSONSchemaError marks schemas outside the strict subset.
type unsupportedStrictJSONSchemaError struct{ msg string }

func (e *unsupportedStrictJSONSchemaError) Error() string { return e.msg }

var unsupportedStrictSchemaKeys = []string{
	"$ref", "$defs", "definitions", "allOf", "oneOf", "patternProperties",
	"dependentSchemas", "dependencies", "unevaluatedProperties", "propertyNames",
	"contains", "prefixItems", "not", "if", "then", "else",
}

func asSchemaObj(value any) (*jsonx.Obj, bool) {
	obj, ok := value.(*jsonx.Obj)
	return obj, ok
}

func isStructuredSchema(schema any) bool {
	obj, ok := asSchemaObj(schema)
	if !ok {
		return false
	}
	types := schemaTypeList(obj)
	for _, t := range types {
		if t == "object" || t == "array" {
			return true
		}
	}
	_, hasProperties := obj.Get("properties")
	_, hasItems := obj.Get("items")
	return hasProperties || hasItems
}

func schemaTypeList(obj *jsonx.Obj) []string {
	typeValue, ok := obj.Get("type")
	if !ok {
		return nil
	}
	switch t := typeValue.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, entry := range t {
			if s, ok := entry.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func schemaAllowsNull(schema any) bool {
	obj, ok := asSchemaObj(schema)
	if !ok {
		return false
	}
	types := schemaTypeList(obj)
	if len(types) == 1 && types[0] == "null" {
		return true
	}
	for _, t := range types {
		if t == "null" {
			return true
		}
	}
	if constValue, ok := obj.Get("const"); ok && constValue == nil {
		return true
	}
	if enumValue, ok := obj.Get("enum"); ok {
		if list, ok := enumValue.([]any); ok {
			for _, entry := range list {
				if entry == nil {
					return true
				}
			}
		}
	}
	if anyOfValue, ok := obj.Get("anyOf"); ok {
		if list, ok := anyOfValue.([]any); ok {
			for _, variant := range list {
				if schemaAllowsNull(variant) {
					return true
				}
			}
		}
	}
	return false
}

func makeJSONSchemaNodeStrict(schema any) error {
	obj, ok := asSchemaObj(schema)
	if !ok {
		return &unsupportedStrictJSONSchemaError{msg: "boolean schemas are unsupported"}
	}
	for _, key := range unsupportedStrictSchemaKeys {
		if _, present := obj.Get(key); present {
			return &unsupportedStrictJSONSchemaError{msg: key + " schemas are unsupported"}
		}
	}

	if anyOfValue, ok := obj.Get("anyOf"); ok {
		variants, isList := anyOfValue.([]any)
		if !isList || len(variants) == 0 {
			return &unsupportedStrictJSONSchemaError{msg: "anyOf must contain at least one schema"}
		}
		for _, variant := range variants {
			if isStructuredSchema(variant) {
				return &unsupportedStrictJSONSchemaError{msg: "object and array unions are unsupported"}
			}
			if err := makeJSONSchemaNodeStrict(variant); err != nil {
				return err
			}
		}
	}

	if itemsValue, ok := obj.Get("items"); ok {
		if _, isList := itemsValue.([]any); isList {
			return &unsupportedStrictJSONSchemaError{msg: "tuple schemas are unsupported"}
		}
		if err := makeJSONSchemaNodeStrict(itemsValue); err != nil {
			return err
		}
	}

	types := schemaTypeList(obj)
	isObjectSchema := len(types) == 1 && types[0] == "object"
	if _, hasProperties := obj.Get("properties"); hasProperties && !isObjectSchema {
		return &unsupportedStrictJSONSchemaError{msg: "properties require type object"}
	}
	if !isObjectSchema {
		return nil
	}
	if additionalValue, ok := obj.Get("additionalProperties"); ok && additionalValue != false {
		return &unsupportedStrictJSONSchemaError{msg: "schema-valued or true additionalProperties is unsupported"}
	}

	properties, _ := obj.Get("properties")
	propertiesObj, propertiesIsObj := asSchemaObj(properties)
	var propertyNames []string
	if propertiesIsObj {
		propertyNames = propertiesObj.Keys()
	}
	requiredSet := map[string]bool{}
	if requiredValue, ok := obj.Get("required"); ok {
		list, isList := requiredValue.([]any)
		if !isList {
			return &unsupportedStrictJSONSchemaError{msg: "object required must be a string array"}
		}
		for _, entry := range list {
			name, isString := entry.(string)
			if !isString {
				return &unsupportedStrictJSONSchemaError{msg: "object required must be a string array"}
			}
			requiredSet[name] = true
		}
	}
	for name := range requiredSet {
		known := false
		for _, candidate := range propertyNames {
			if candidate == name {
				known = true
				break
			}
		}
		if !known {
			return &unsupportedStrictJSONSchemaError{msg: "required contains an unknown property"}
		}
	}
	for _, key := range propertyNames {
		property, _ := propertiesObj.Get(key)
		if err := makeJSONSchemaNodeStrict(property); err != nil {
			return err
		}
		if !requiredSet[key] && !schemaAllowsNull(property) {
			propertiesObj.Set(key, jsonx.ObjFrom("anyOf", []any{property, jsonx.ObjFrom("type", "null")}))
		}
	}
	obj.Set("required", stringListValue(propertyNames))
	obj.Set("additionalProperties", false)
	return nil
}

func stringListValue(names []string) []any {
	out := make([]any, 0, len(names))
	for _, name := range names {
		out = append(out, name)
	}
	return out
}

// SchemaToJSON converts a typebox schema to a jsonx value tree.
func SchemaToJSON(schema *typebox.Schema) (any, error) {
	if schema == nil {
		return nil, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	return jsonx.ParseBytes(data)
}

// MakeStrictJSONSchema converts a tool schema to the strict subset expected
// by provider constrained sampling.
func MakeStrictJSONSchema(schema *typebox.Schema) (*jsonx.Obj, error) {
	cloned, err := SchemaToJSON(schema)
	if err != nil {
		return nil, err
	}
	root, ok := asSchemaObj(cloned)
	if !ok {
		return nil, &unsupportedStrictJSONSchemaError{msg: "root schema must have type object"}
	}
	if err := makeJSONSchemaNodeStrict(root); err != nil {
		return nil, err
	}
	if types := schemaTypeList(root); len(types) != 1 || types[0] != "object" {
		return nil, &unsupportedStrictJSONSchemaError{msg: "root schema must have type object"}
	}
	return root, nil
}

// GetJSONSchemaToolParameters returns the tool parameters, upgraded to the
// strict subset when strict is true.
func GetJSONSchemaToolParameters(tool *Tool, strict bool) (any, error) {
	if !strict {
		return SchemaToJSON(tool.Parameters)
	}
	strictSchema, err := MakeStrictJSONSchema(tool.Parameters)
	if err != nil {
		return nil, err
	}
	return strictSchema, nil
}

// GrammarConstrainedSampling mirrors the TS interface.
type GrammarConstrainedSampling struct {
	Format        string // "lark" | "regex"
	Definition    string
	InputProperty string
}

// GrammarToolInputJSONBuffer mirrors the TS interface.
type GrammarToolInputJSONBuffer struct {
	Input   string
	Started bool
	Closed  bool
}

// GetGrammarToolInput extracts the grammar input property from arguments.
func GetGrammarToolInput(toolName string, arguments *jsonx.Obj, inputProperty string) (string, error) {
	input, ok := arguments.Get(inputProperty)
	if !ok {
		return "", &csError{msg: "Grammar tool call \"" + toolName + "\" requires argument \"" + inputProperty + "\" to be a string."}
	}
	s, isString := input.(string)
	if !isString {
		return "", &csError{msg: "Grammar tool call \"" + toolName + "\" requires argument \"" + inputProperty + "\" to be a string."}
	}
	return s, nil
}

// AppendGrammarToolInputJSONDelta emits the JSON delta stream for a grammar
// tool's input property. Empty string return = nothing to emit.
func AppendGrammarToolInputJSONDelta(buffer *GrammarToolInputJSONBuffer, inputProperty string, nextInput string, close bool) (string, error) {
	if buffer.Closed {
		if close && nextInput == buffer.Input {
			return "", nil
		}
		return "", &csError{msg: "grammar tool input for property \"" + inputProperty + "\" changed after it was closed"}
	}
	if len(nextInput) < len(buffer.Input) || nextInput[:len(buffer.Input)] != buffer.Input {
		return "", &csError{msg: "grammar tool input for property \"" + inputProperty + "\" changed non-monotonically"}
	}

	inputDelta := nextInput[len(buffer.Input):]
	if !close && inputDelta == "" {
		return "", nil
	}

	delta := ""
	if !buffer.Started {
		delta += "{" + jsonString(inputProperty) + ":\""
		buffer.Started = true
	}
	delta += trimJSONQuotes(jsonString(inputDelta))
	buffer.Input = nextInput

	if close {
		delta += "\"}"
		buffer.Closed = true
	}
	return delta, nil
}

func jsonString(value string) string { return jsonx.Stringify(value) }

func trimJSONQuotes(serialized string) string {
	if len(serialized) >= 2 && serialized[0] == '"' && serialized[len(serialized)-1] == '"' {
		return serialized[1 : len(serialized)-1]
	}
	return serialized
}

func inferGrammarInputProperty(tool *Tool) (string, error) {
	schemaValue, err := SchemaToJSON(tool.Parameters)
	if err != nil {
		return "", err
	}
	schema, ok := asSchemaObj(schemaValue)
	if !ok {
		return "", &csError{msg: "grammar constrained sampling requires an object parameter schema"}
	}
	if types := schemaTypeList(schema); len(types) != 1 || types[0] != "object" {
		return "", &csError{msg: "grammar constrained sampling requires an object parameter schema"}
	}
	requiredValue, _ := schema.Get("required")
	required, ok := requiredValue.([]any)
	if !ok || len(required) != 1 {
		return "", &csError{msg: "grammar constrained sampling requires exactly one required string property"}
	}
	inputProperty, isString := required[0].(string)
	if !isString {
		return "", &csError{msg: "grammar constrained sampling requires exactly one required string property"}
	}
	propertiesValue, _ := schema.Get("properties")
	properties, ok := asSchemaObj(propertiesValue)
	if !ok || !properties.Has(inputProperty) {
		return "", &csError{msg: "grammar constrained sampling requires a properties entry for " + inputProperty}
	}
	propertyValue, _ := properties.Get(inputProperty)
	property, ok := asSchemaObj(propertyValue)
	if !ok {
		return "", &csError{msg: "grammar constrained sampling requires a properties entry for " + inputProperty}
	}
	if types := schemaTypeList(property); len(types) != 1 || types[0] != "string" {
		return "", &csError{msg: "grammar constrained sampling property " + inputProperty + " must have type string"}
	}
	return inputProperty, nil
}

// ResolveJSONSchemaStrictSampling resolves the strict-tools decision for a
// tool. Nil = no strict sampling.
func ResolveJSONSchemaStrictSampling(tool *Tool, supportsStrictMode bool) (bool, error) {
	config := ParseConstrainedSampling(tool.ConstrainedSampling)
	if config == nil || config.Type != "json_schema" {
		return false, nil
	}
	if supportsStrictMode {
		if _, err := MakeStrictJSONSchema(tool.Parameters); err == nil {
			return true, nil
		} else if _, unsupported := err.(*unsupportedStrictJSONSchemaError); !unsupported {
			return false, err
		} else if config.Strict != "require" {
			return false, nil
		} else {
			return false, &csError{msg: "Tool \"" + tool.Name + "\" requires JSON-schema constrained sampling, but " + err.Error() + "."}
		}
	}
	if config.Strict == "require" {
		return false, &csError{msg: "Tool \"" + tool.Name + "\" requires JSON-schema constrained sampling, but strict tools are unsupported."}
	}
	return false, nil
}

// ResolveGrammarConstrainedSampling resolves grammar sampling for a tool.
func ResolveGrammarConstrainedSampling(tool *Tool, supportsOpenAIGrammarTools bool) (*GrammarConstrainedSampling, error) {
	config := ParseConstrainedSampling(tool.ConstrainedSampling)
	if config == nil || config.Type != "grammar" {
		return nil, nil
	}
	if !supportsOpenAIGrammarTools {
		return nil, nil
	}
	larkDefinition := config.Variants.OpenAILark
	regexDefinition := config.Variants.OpenAIRegex
	hasLark := len(larkDefinition) > 0 && len(trimSpace(larkDefinition)) > 0
	hasRegex := len(regexDefinition) > 0 && len(trimSpace(regexDefinition)) > 0
	if !hasLark && !hasRegex {
		return nil, &csError{msg: "Tool \"" + tool.Name + "\" cannot use grammar constrained sampling: no supported grammar variant was provided."}
	}
	grammar := &GrammarConstrainedSampling{}
	if hasLark {
		grammar.Format = "lark"
		grammar.Definition = larkDefinition
	} else {
		grammar.Format = "regex"
		grammar.Definition = regexDefinition
	}
	inputProperty, err := inferGrammarInputProperty(tool)
	if err != nil {
		if fe, ok := err.(*fauxError); ok {
			return nil, &csError{msg: "Tool \"" + tool.Name + "\" cannot use grammar constrained sampling: " + fe.msg + "."}
		}
		return nil, err
	}
	grammar.InputProperty = inputProperty
	return grammar, nil
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// CreateGrammarToolInputProperties maps grammar tool names to their input
// property.
func CreateGrammarToolInputProperties(tools []Tool, supportsOpenAIGrammarTools bool) map[string]string {
	properties := map[string]string{}
	for i := range tools {
		grammar, err := ResolveGrammarConstrainedSampling(&tools[i], supportsOpenAIGrammarTools)
		if err != nil {
			continue
		}
		if grammar != nil {
			properties[tools[i].Name] = grammar.InputProperty
		}
	}
	return properties
}
