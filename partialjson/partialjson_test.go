package partialjson

import (
	"math"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func stringify(v any) string { return jsonx.Stringify(v) }

func TestCompleteJSON(t *testing.T) {
	cases := []struct{ in, out string }{
		{`{"a":1,"b":[1,2,3],"c":"x","d":null,"e":true}`, `{"a":1,"b":[1,2,3],"c":"x","d":null,"e":true}`},
		{`"plain"`, `"plain"`},
		{`123.5`, `123.5`},
		{`[1,2]`, `[1,2]`},
	}
	for _, c := range cases {
		v, err := Parse(c.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.in, err)
		}
		if got := stringify(v); got != c.out {
			t.Errorf("Parse(%q) = %s want %s", c.in, got, c.out)
		}
	}
}

func TestPartialObjectAndArray(t *testing.T) {
	v, err := Parse(`{"a": 1, "b": 2`)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringify(v); got != `{"a":1,"b":2}` {
		t.Fatalf("got %s", got)
	}

	v, err = ParseAllow(`[{"a": 1, "b": 2}, {"a": 3,`, AllowARR)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringify(v); got != `[{"a":1,"b":2}]` {
		t.Fatalf("README example 1: got %s", got)
	}

	v, err = ParseAllow(`["complete string", "incompl`, ^AllowSTR)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringify(v); got != `["complete string"]` {
		t.Fatalf("README example 2: got %s", got)
	}
}

func TestPartialString(t *testing.T) {
	v, err := Parse(`{"a":"hello wor`)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringify(v); got != `{"a":"hello wor"}` {
		t.Fatalf("got %s", got)
	}
	// Trailing backslash inside partial string.
	v, err = Parse(`{"a":"abc\`)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringify(v); got != `{"a":"abc"}` {
		t.Fatalf("escaped cut: got %s", got)
	}
}

func TestPartialSpecials(t *testing.T) {
	if v, _ := Parse(`nu`); v != nil {
		t.Fatalf("nu = %v", v)
	}
	if v, _ := Parse(`tr`); v != true {
		t.Fatalf("tr = %v", v)
	}
	if v, _ := Parse(`fa`); v != false {
		t.Fatalf("fa = %v", v)
	}
	if v, _ := Parse(`Na`); !math.IsNaN(v.(float64)) {
		t.Fatalf("Na = %v", v)
	}
	if v, _ := Parse(`Inf`); !math.IsInf(v.(float64), 1) {
		t.Fatalf("Inf = %v", v)
	}
	if v, _ := Parse(`-Inf`); !math.IsInf(v.(float64), -1) {
		t.Fatalf("-Inf = %v", v)
	}
	if v, _ := Parse(`-Infinity`); !math.IsInf(v.(float64), -1) {
		t.Fatalf("-Infinity = %v", v)
	}
}

func TestPartialNumber(t *testing.T) {
	v, err := Parse(`{"a": 1e`)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringify(v); got != `{"a":1}` {
		t.Fatalf("got %s", got)
	}
	v, err = ParseAllow(`{"a": 1.`, AllowOBJ)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringify(v); got != `{}` {
		t.Fatalf("NUM disallowed: got %s", got)
	}
}

func TestDisallowKind(t *testing.T) {
	if _, err := ParseAllow(`{"a": 1`, 0); err == nil {
		t.Fatal("expected PartialError")
	}
	_, err := ParseAllow(`{"a": 1`, 0)
	if _, ok := err.(*PartialError); !ok {
		t.Fatalf("err type %T", err)
	}
}

func TestMalformed(t *testing.T) {
	_, err := Parse(`xyz`)
	if _, ok := err.(*MalformedError); !ok {
		t.Fatalf("err type %T (%v)", err, err)
	}
	_, err = Parse(`-`)
	if _, ok := err.(*MalformedError); !ok {
		t.Fatalf("'-' err type %T", err)
	}
	_, err = Parse(`   `)
	if err == nil {
		t.Fatal("empty accepted")
	}
}

func TestNestedPartial(t *testing.T) {
	v, err := Parse(`{"outer":{"inner":[1,2,{"deep":"valu`)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringify(v); got != `{"outer":{"inner":[1,2,{"deep":"valu"}]}}` {
		t.Fatalf("got %s", got)
	}
}

func TestUnterminatedWithDisallow(t *testing.T) {
	// STR disallowed but OBJ still allowed: the enclosing object is returned
	// without the partial string value (matches npm partial-json).
	v, err := ParseAllow(`{"a":"abc`, ^AllowSTR)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := stringify(v); got != `{}` {
		t.Fatalf("got %s", got)
	}
}
