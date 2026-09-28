package jsonx

import (
	"math"
	"testing"
)

func mustParse(t *testing.T, s string) any {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return v
}

func TestParsePreservesKeyOrder(t *testing.T) {
	v := mustParse(t, `{"zeta":1,"alpha":2,"mid":{"b":1,"a":2}}`)
	o := v.(*Obj)
	got := o.Keys()
	if len(got) != 3 || got[0] != "zeta" || got[1] != "alpha" || got[2] != "mid" {
		t.Fatalf("keys = %v", got)
	}
	inner := o.MustGet("mid").(*Obj)
	if k := inner.Keys(); k[0] != "b" || k[1] != "a" {
		t.Fatalf("inner keys = %v", k)
	}
	if Stringify(v) != `{"zeta":1,"alpha":2,"mid":{"b":1,"a":2}}` {
		t.Fatalf("round trip = %s", Stringify(v))
	}
}

func TestParseRejectsTrailing(t *testing.T) {
	if _, err := Parse(`{"a":1} x`); err == nil {
		t.Fatal("trailing garbage accepted")
	}
	if _, err := Parse(`01`); err == nil {
		t.Fatal("leading zero accepted")
	}
	if _, err := Parse(``); err == nil {
		t.Fatal("empty accepted")
	}
}

func TestParseScalars(t *testing.T) {
	cases := []struct{ in, out string }{
		{"null", "null"},
		{"true", "true"},
		{"false", "false"},
		{"-1.5", "-1.5"},
		{`"hi\nthere"`, `"hi\nthere"`},
		{"[]", "[]"},
		{"[1,2]", "[1,2]"},
	}
	for _, c := range cases {
		if got := Stringify(mustParse(t, c.in)); got != c.out {
			t.Errorf("Parse(%q).Stringify = %s want %s", c.in, got, c.out)
		}
	}
}

func TestFormatNumberJS(t *testing.T) {
	cases := []struct {
		f    float64
		want string
	}{
		{0, "0"},
		{-0, "0"},
		{1, "1"},
		{-1, "-1"},
		{1.5, "1.5"},
		{0.1, "0.1"},
		{0.000001, "0.000001"},
		{1e-7, "1e-7"},
		{1.5e-8, "1.5e-8"},
		{1e20, "100000000000000000000"},
		{1e21, "1e+21"},
		{1.5e21, "1.5e+21"},
		{2.5e-8, "2.5e-8"},
		{math.MaxFloat64, "1.7976931348623157e+308"},
		{5e-324, "5e-324"},
		{9007199254740992, "9007199254740992"},
		{123456789012345680000, "123456789012345680000"},
		{0.3333333333333333, "0.3333333333333333"},
		{math.NaN(), "null"},
		{math.Inf(1), "null"},
		{math.Inf(-1), "null"},
	}
	for _, c := range cases {
		if got := FormatNumber(c.f); got != c.want {
			t.Errorf("FormatNumber(%v) = %q want %q", c.f, got, c.want)
		}
	}
}

func TestStringifyEscapes(t *testing.T) {
	o := ObjFrom("a", "\x00\x1f\b\t\n\f\r\"\\", "k", "é中\U0001F600")
	want := `{"a":"\u0000\u001f\b\t\n\f\r\"\\","k":"é中😀"}`
	if got := Stringify(o); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestCloneDeep(t *testing.T) {
	v := mustParse(t, `{"a":[1,{"b":2}],"c":{"d":null}}`)
	c := Clone(v)
	(Clone(c).(*Obj)).Set("a", []any{"mutated"})
	if Stringify(v) != `{"a":[1,{"b":2}],"c":{"d":null}}` {
		t.Fatalf("original mutated: %s", Stringify(v))
	}
	if !Equal(v, mustParse(t, `{"c":{"d":null},"a":[1,{"b":2}]}`)) {
		t.Fatal("Equal failed across key order")
	}
}

func TestObjSemantics(t *testing.T) {
	o := NewObj()
	o.Set("b", float64(1))
	o.Set("a", float64(2))
	o.Set("b", float64(3)) // replace in place
	if got := o.Keys(); len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("keys %v", got)
	}
	if o.MustGet("b").(float64) != 3 {
		t.Fatal("replace failed")
	}
	o.Delete("b")
	if o.Has("b") || o.Len() != 1 {
		t.Fatal("delete failed")
	}
	o.Delete("missing") // no-op
	if o.Len() != 1 {
		t.Fatal("no-op delete changed len")
	}
}
