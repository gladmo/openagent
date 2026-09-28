package chord

import (
	"math"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

// Port of pi/packages/chord/test/json.test.ts (representative cases).

func TestCopyJsonStrictness(t *testing.T) {
	// Valid tree copies without aliasing.
	value, err := jsonx.Parse(`{"a":[1,{"b":null}],"c":"x","d":true}`)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := CopyJson(value, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonx.Equal(value, copied) {
		t.Fatalf("copy = %s", jsonx.Stringify(copied))
	}
	inner := value.(*jsonx.Obj).MustGet("a").([]any)[1]
	copiedInner := copied.(*jsonx.Obj).MustGet("a").([]any)[1]
	if inner == copiedInner {
		t.Fatal("copy aliases the original")
	}

	// Non-finite numbers are rejected.
	if _, err := CopyJson([]any{inf()}, nil); err == nil {
		t.Fatal("non-finite accepted")
	}
	if _, err := CopyJson(inf(), nil); err == nil {
		t.Fatal("non-finite scalar accepted")
	}
	// Cycles are rejected.
	a := jsonx.NewObj()
	b := jsonx.NewObj()
	a.Set("b", b)
	b.Set("a", a)
	if _, err := CopyJson(a, nil); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestIsJsonValue(t *testing.T) {
	if !IsJsonValue(nil) || !IsJsonValue(true) || !IsJsonValue("x") || !IsJsonValue(1.5) {
		t.Fatal("primitives rejected")
	}
	if !IsJsonValue([]any{1.0, "two", nil}) {
		t.Fatal("array rejected")
	}
	if !IsJsonValue(jsonx.ObjFrom("k", "v")) {
		t.Fatal("object rejected")
	}
	if IsJsonValue(inf()) {
		t.Fatal("non-finite accepted")
	}
	if IsJsonValue(func() {}) {
		t.Fatal("function accepted")
	}
	cyclic := jsonx.NewObj()
	cyclic.Set("self", cyclic)
	if IsJsonValue(cyclic) {
		t.Fatal("cycle accepted")
	}
}

func inf() float64 { return math.Inf(1) }
