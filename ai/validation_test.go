package ai

// Ports of pi/packages/ai/test/validation.test.ts.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

func plainSchemaTool(t *testing.T, schemaJSON string, value any) (*Tool, *ToolCall) {
	t.Helper()
	parsed := jsonx.MustParseString(schemaJSON).(*jsonx.Obj)
	// A schema JSON that already carries top-level "properties" is a full
	// parameters object; otherwise wrap it as the "value" property.
	if _, hasProps := parsed.Get("properties"); !hasProps {
		obj := jsonx.NewObj()
		obj.Set("type", "object")
		props := jsonx.NewObj()
		props.Set("value", parsed)
		obj.Set("properties", props)
		obj.Set("required", []any{"value"})
		parsed = obj
	}
	schema := typebox.SchemaFromJSON(parsed)
	return &Tool{Name: "echo", Description: "Echo tool", Parameters: schema},
		&ToolCall{ID: "tool-1", Name: "echo", Arguments: jsonx.ObjFrom("value", value)}
}

func TestValidateCoercesPlainSchemas(t *testing.T) {
	cases := []struct {
		schema   string
		input    any
		expected string
	}{
		{`{"type":"number"}`, "42", `{"value":42}`},
		{`{"type":"number"}`, true, `{"value":1}`},
		{`{"type":"number"}`, nil, `{"value":0}`},
		{`{"type":"integer"}`, "42", `{"value":42}`},
		{`{"type":"boolean"}`, "true", `{"value":true}`},
		{`{"type":"boolean"}`, "false", `{"value":false}`},
		{`{"type":"boolean"}`, float64(1), `{"value":true}`},
		{`{"type":"boolean"}`, float64(0), `{"value":false}`},
		{`{"type":"string"}`, nil, `{"value":""}`},
		{`{"type":"string"}`, true, `{"value":"true"}`},
		{`{"type":"null"}`, "", `{"value":null}`},
		{`{"type":"null"}`, float64(0), `{"value":null}`},
		{`{"type":"null"}`, false, `{"value":null}`},
		{`{"type":["number","string"]}`, "1", `{"value":"1"}`},
		{`{"type":["boolean","number"]}`, "1", `{"value":1}`},
	}
	for _, tc := range cases {
		tool, toolCall := plainSchemaTool(t, tc.schema, tc.input)
		result, err := ValidateToolArguments(tool, toolCall)
		if err != nil {
			t.Fatalf("schema %s input %v: %v", tc.schema, tc.input, err)
		}
		if got := jsonStringify(result); got != tc.expected {
			t.Fatalf("schema %s input %v: got %s want %s", tc.schema, tc.input, got, tc.expected)
		}
	}
}

func TestValidateNullAsOmissionForOptional(t *testing.T) {
	tool := &Tool{
		Name: "echo", Description: "Echo tool",
		Parameters: typebox.Object([]*typebox.Property{
			typebox.Prop("path", typebox.String()),
			typebox.Prop("offset", typebox.Optional(typebox.Number())),
			typebox.Prop("nullable", typebox.Optional(typebox.Union([]*typebox.Schema{typebox.String(), typebox.Null()}))),
			typebox.Prop("metadata", typebox.Object([]*typebox.Property{
				typebox.Prop("enabled", typebox.Optional(typebox.Boolean())),
			})),
		}),
	}
	toolCall := &ToolCall{
		ID: "tool-1", Name: "echo",
		Arguments: jsonx.MustParseString(`{"path":"file.txt","offset":null,"nullable":null,"metadata":{"enabled":null}}`).(*jsonx.Obj),
	}
	result, err := ValidateToolArguments(tool, toolCall)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonStringify(result); got != `{"path":"file.txt","nullable":null,"metadata":{}}` {
		t.Fatalf("got %s", got)
	}
}

func TestValidatePreservesNullsWithRefSchema(t *testing.T) {
	tool, toolCall := plainSchemaTool(t,
		`{"type":"object","properties":{"value":{"$ref":"#/$defs/value"}},"$defs":{"value":{"anyOf":[{"type":"number"},{"type":"null"}]}}}`,
		nil)
	result, err := ValidateToolArguments(tool, toolCall)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonStringify(result); got != `{"value":null}` {
		t.Fatalf("got %s", got)
	}
}

func TestValidatePreservesMatchingUnionArm(t *testing.T) {
	tool := &Tool{
		Name: "echo", Description: "Echo tool",
		Parameters: typebox.Object([]*typebox.Property{
			typebox.Prop("value", typebox.Union([]*typebox.Schema{typebox.Number(), typebox.Null()})),
		}),
	}
	toolCall := &ToolCall{ID: "tool-1", Name: "echo", Arguments: jsonx.ObjFrom("value", nil)}
	result, err := ValidateToolArguments(tool, toolCall)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonStringify(result); got != `{"value":null}` {
		t.Fatalf("got %s", got)
	}
}

func TestValidatePreservesMatchingOneOfArm(t *testing.T) {
	tool, toolCall := plainSchemaTool(t, `{"oneOf":[{"type":"number"},{"type":"null"}]}`, nil)
	result, err := ValidateToolArguments(tool, toolCall)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonStringify(result); got != `{"value":null}` {
		t.Fatalf("got %s", got)
	}
}

func TestValidateCoercesNonMatchingNullableUnion(t *testing.T) {
	tool, toolCall := plainSchemaTool(t, `{"anyOf":[{"type":"number"},{"type":"null"}]}`, "42")
	result, err := ValidateToolArguments(tool, toolCall)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonStringify(result); got != `{"value":42}` {
		t.Fatalf("got %s", got)
	}
}

func TestValidateNullableArrayWithItems(t *testing.T) {
	tool, toolCall := plainSchemaTool(t, `{"type":["array","null"],"items":{"type":"string"}}`, nil)
	// The generated validator must accept the raw arguments.
	if !typebox.Compile(tool.Parameters).Check(toolCall.Arguments) {
		t.Fatal("compiled check rejected nullable array null")
	}
	result, err := ValidateToolArguments(tool, toolCall)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonStringify(result); got != `{"value":null}` {
		t.Fatalf("got %s", got)
	}
}

func TestValidateRejectsInvalidCoercions(t *testing.T) {
	failing := []struct {
		schema string
		input  any
	}{
		{`{"type":"boolean"}`, "1"},
		{`{"type":"boolean"}`, "0"},
		{`{"type":"null"}`, "null"},
		{`{"type":"integer"}`, "42.1"},
	}
	for _, tc := range failing {
		tool, toolCall := plainSchemaTool(t, tc.schema, tc.input)
		_, err := ValidateToolArguments(tool, toolCall)
		if err == nil || !strings.HasPrefix(err.Error(), "Validation failed") {
			t.Fatalf("schema %s input %v: err = %v", tc.schema, tc.input, err)
		}
	}
}

func TestValidateErrorMessageFormat(t *testing.T) {
	tool := &Tool{
		Name: "read", Description: "Read",
		Parameters: typebox.Object([]*typebox.Property{
			typebox.Prop("path", typebox.String()),
		}),
	}
	toolCall := &ToolCall{ID: "t", Name: "read", Arguments: jsonx.ObjFrom("path", []any{"nested"})}
	_, err := ValidateToolArguments(tool, toolCall)
	if err == nil {
		t.Fatal("expected error")
	}
	want := "Validation failed for tool \"read\":\n  - path: must be string\n\nReceived arguments:\n{\n  \"path\": [\n    \"nested\"\n  ]\n}"
	if err.Error() != want {
		t.Fatalf("error =\n%s\nwant =\n%s", err.Error(), want)
	}
}
