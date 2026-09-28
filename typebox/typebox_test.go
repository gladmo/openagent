package typebox

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func TestObjectSerializationOrder(t *testing.T) {
	s := Object([]*Property{
		Prop("path", String(Description("Path to the file"))),
		Prop("offset", Optional(Number(Description("Line number")))),
		Prop("limit", Optional(Number(Description("Max lines")))),
	})
	want := `{"type":"object","required":["path"],"properties":{"path":{"type":"string","description":"Path to the file"},"offset":{"type":"number","description":"Line number"},"limit":{"type":"number","description":"Max lines"}}}`
	if got := s.Serialize(); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestNestedSchemas(t *testing.T) {
	s := Object([]*Property{
		Prop("edits", Array(Object([]*Property{
			Prop("oldText", String()),
			Prop("newText", String()),
		}))),
	})
	want := `{"type":"object","required":["edits"],"properties":{"edits":{"type":"array","items":{"type":"object","required":["oldText","newText"],"properties":{"oldText":{"type":"string"},"newText":{"type":"string"}}}}}}`
	if got := s.Serialize(); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestCheck(t *testing.T) {
	s := Object([]*Property{
		Prop("path", String()),
		Prop("n", Number()),
		Prop("items", Optional(Array(String()))),
	})
	ok := func(v string) bool {
		pv, err := jsonx.Parse(v)
		if err != nil {
			t.Fatal(err)
		}
		return Compile(s).Check(pv)
	}
	if !ok(`{"path":"a","n":1}`) {
		t.Fatal("valid rejected")
	}
	if !ok(`{"path":"a","n":1,"items":["x"]}`) {
		t.Fatal("valid with optional rejected")
	}
	if ok(`{"n":1}`) {
		t.Fatal("missing required accepted")
	}
	if ok(`{"path":5,"n":1}`) {
		t.Fatal("wrong type accepted")
	}
	if ok(`{"path":"a","n":1,"items":[1]}`) {
		t.Fatal("bad item accepted")
	}
	if ok(`{"path":"a","n":"1"}`) {
		t.Fatal("string number accepted")
	}
}

func TestErrorsShape(t *testing.T) {
	s := Object([]*Property{
		Prop("path", String()),
		Prop("nested", Object([]*Property{Prop("x", Number())})),
	})
	v, _ := jsonx.Parse(`{"nested":{"x":"bad"},"extra":1}`)
	errs := Compile(s).Errors(v)
	if len(errs) != 2 {
		for _, e := range errs {
			t.Logf("err: %s %s %s", e.Keyword, e.InstancePath, e.Message)
		}
		t.Fatalf("want 2 errors, got %d", len(errs))
	}
	// required is emitted before properties in engine order.
	if errs[0].Keyword != "required" || errs[0].InstancePath != "" {
		t.Fatalf("first = %+v", errs[0])
	}
	if errs[0].Message != "must have required properties path" {
		t.Fatalf("message = %q", errs[0].Message)
	}
	if errs[1].Keyword != "type" || errs[1].InstancePath != "/nested/x" || errs[1].Message != "must be number" {
		t.Fatalf("second = %+v", errs[1])
	}
}

func TestErrorPathFormatting(t *testing.T) {
	// Mirrors ai/utils/validation.ts formatValidationPath: instancePath
	// /edits/0/oldText -> edits.0.oldText; required -> first property.
	s := Object([]*Property{
		Prop("edits", Array(Object([]*Property{Prop("oldText", String())}))),
	})
	v, _ := jsonx.Parse(`{"edits":[{"oldText":3}]}`)
	errs := Compile(s).Errors(v)
	if len(errs) != 1 || errs[0].InstancePath != "/edits/0/oldText" {
		t.Fatalf("errs = %+v", errs)
	}
}

func TestConvertNumber(t *testing.T) {
	s := Object([]*Property{Prop("n", Number()), Prop("s", String()), Prop("b", Boolean())})
	v, _ := jsonx.Parse(`{"n":"5","s":7,"b":"true"}`)
	out := Convert(s, v).(*jsonx.Obj)
	if f, _ := jsonx.ToFloat(out.MustGet("n")); f != 5 {
		t.Fatalf("n = %v", out.MustGet("n"))
	}
	if out.MustGet("s") != "7" {
		t.Fatalf("s = %v", out.MustGet("s"))
	}
	if b, _ := out.MustGet("b").(bool); !b {
		t.Fatalf("b = %v", out.MustGet("b"))
	}
}

func TestConvertNaN(t *testing.T) {
	if got := Convert(Number(), "abc"); !isNaN(got.(float64)) {
		t.Fatalf("abc -> %v, want NaN (JS Number('abc') === NaN is a number)", got)
	}
	if got := Convert(Number(), nil); got.(float64) != 0 {
		t.Fatalf("null -> %v", got)
	}
	if got := Convert(Number(), true); got.(float64) != 1 {
		t.Fatalf("true -> %v", got)
	}
}

func isNaN(f float64) bool { return f != f }

func TestConvertRawSchemaNoop(t *testing.T) {
	raw, err := jsonx.Parse(`{"type":"object","properties":{"n":{"type":"number"}},"required":["n"]}`)
	if err != nil {
		t.Fatal(err)
	}
	s := SchemaFromJSON(raw)
	if s.Kind() != "" {
		t.Fatalf("raw schema kind = %q", s.Kind())
	}
	v, _ := jsonx.Parse(`{"n":"5"}`)
	out := Convert(s, v)
	if !jsonx.Equal(out, v) {
		t.Fatalf("raw schema converted: %s", jsonx.Stringify(out))
	}
	if Compile(s).Check(v) {
		t.Fatal("raw schema check should fail for string n")
	}
}

func TestRoundTripUnmarshal(t *testing.T) {
	s := Object([]*Property{
		Prop("a", String(Description("x"))),
		Prop("b", Optional(Integer())),
	})
	serialized := s.Serialize()
	var back Schema
	if err := jsonUnmarshal(&back, serialized); err != nil {
		t.Fatal(err)
	}
	if back.Serialize() != serialized {
		t.Fatalf("round trip: %s", back.Serialize())
	}
	if back.IsOptional() {
		t.Fatal("raw optional marker")
	}
}

func jsonUnmarshal(s *Schema, data string) error {
	return s.UnmarshalJSON([]byte(data))
}

func TestIntegerCheck(t *testing.T) {
	s := Object([]*Property{Prop("n", Integer())})
	v, _ := jsonx.Parse(`{"n":1.5}`)
	if Compile(s).Check(v) {
		t.Fatal("1.5 accepted as integer")
	}
	v2, _ := jsonx.Parse(`{"n":2.0}`)
	if !Compile(s).Check(v2) {
		t.Fatal("2.0 rejected as integer")
	}
}
