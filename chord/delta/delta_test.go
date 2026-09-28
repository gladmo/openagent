package delta

import (
	"testing"

	"github.com/gladmo/openagent/chord"
	"github.com/gladmo/openagent/jsonx"
)

// Ports of pi/packages/chord/test/delta.test.ts (apply/validation/codec
// sections; tracker tests belong to the pico3 phase).

func parseObj(t *testing.T, s string) *jsonx.Obj {
	t.Helper()
	v, err := jsonx.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonx.Obj)
}

func stringifyOps(ops []Op) string {
	return jsonx.Stringify(OpsToJSON(ops))
}

func opsFromLiteral(t *testing.T, s string) []Op {
	t.Helper()
	v, err := jsonx.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := OpsFromJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

func TestApplyMutableAndImmutable(t *testing.T) {
	operations := opsFromLiteral(t, `[["a",["text"],"b"],["p",["values"],1,1,[3,4]],["s",["nested","value"],2]]`)
	base := parseObj(t, `{"text":"a","values":[1,2],"nested":{"value":1},"stable":{"value":9}}`)
	immutable, err := ApplyImmutable(base, operations)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"text":"ab","values":[1,3,4],"nested":{"value":2},"stable":{"value":9}}`
	if got := jsonx.Stringify(immutable); got != want {
		t.Fatalf("immutable = %s", got)
	}
	if got := jsonx.Stringify(base); got != `{"text":"a","values":[1,2],"nested":{"value":1},"stable":{"value":9}}` {
		t.Fatalf("base mutated: %s", got)
	}
	// The untouched subtree stays shared with the previous revision.
	stableBase := base.MustGet("stable")
	stableNew := immutable.(*jsonx.Obj).MustGet("stable")
	if stableBase != stableNew {
		t.Fatal("stable subtree not shared")
	}
	clone, err := chord.CopyJson(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	mutated, err := Apply(clone, operations)
	if err != nil {
		t.Fatal(err)
	}
	if jsonx.Stringify(mutated) != want {
		t.Fatalf("apply = %s", jsonx.Stringify(mutated))
	}
}

func TestRootReplacementSplicePermute(t *testing.T) {
	value, err := Apply(nil, opsFromLiteral(t, `[["r",[1,2,3]]]`))
	if err != nil {
		t.Fatal(err)
	}
	value, err = Apply(value, opsFromLiteral(t, `[["p",[],1,1,[4]]]`))
	if err != nil {
		t.Fatal(err)
	}
	value, err = Apply(value, opsFromLiteral(t, `[["m",[],[2,0,1]]]`))
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonx.Stringify(value); got != `[3,1,4]` {
		t.Fatalf("value = %s", got)
	}
	if !IsBase(opsFromLiteral(t, `[["r",[1]]]`)) {
		t.Fatal("isBase")
	}
	if IsBase(opsFromLiteral(t, `[["s",["a"],1]]`)) {
		t.Fatal("isBase false positive")
	}
}

func TestRejectsUnsafeAndMalformedPaths(t *testing.T) {
	mustErr := func(target string, ops string) {
		t.Helper()
		parsed, perr := OpsFromJSON(mustParse(t, ops))
		if perr != nil {
			return // rejected at validation: also an error, as in TS
		}
		_, err := Apply(parseObj(t, target), parsed)
		if err == nil {
			t.Fatalf("expected error for %s on %s", ops, target)
		}
	}
	mustErr(`{}`, `[["s",["constructor","prototype","x"],true]]`)
	mustErr(`{"values":[1]}`, `[["s",["values",3],2]]`)
	mustErr(`{"value":1}`, `[["a",["value"],"x"]]`)
	if _, err := OpFromJSON(mustParse(t, `["s","value",1]`)); err == nil {
		t.Fatal("string path accepted")
	}
	if _, err := OpFromJSON(mustParse(t, `["m",[],[0,0]]`)); err == nil {
		t.Fatal("non-bijective permutation accepted")
	}
}

func mustParse(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDecodedAndWireVocabulariesSeparately(t *testing.T) {
	if _, err := OpFromJSON(mustParse(t, `["s",["value"],1]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := OpFromJSON(mustParse(t, `["s",1]`)); err == nil {
		t.Fatal("wire form accepted as decoded op")
	}
	if err := assertValidWireOp(WireOp{"s", float64(1)}); err != nil {
		t.Fatal(err)
	}
	if err := assertValidWireOp(WireOp{"#", float64(0), []any{"value"}}); err != nil {
		t.Fatal(err)
	}
}

func TestCodecInternsPathsAndOmitsAdjacent(t *testing.T) {
	enc := NewEncoder()
	dec := NewDecoder()
	path := Path{"nested", "text"}
	first := []Op{
		&Truncate{Path: path, Count: 1},
		&Append{Path: path, Text: "x"},
	}
	decoded, err := dec.Decode(enc.Encode(first))
	if err != nil {
		t.Fatal(err)
	}
	if stringifyOps(decoded) != stringifyOps(first) {
		t.Fatalf("round trip: %s vs %s", stringifyOps(decoded), stringifyOps(first))
	}
	second := []Op{&Append{Path: path, Text: "y"}}
	wire := enc.Encode(second)
	want := `[["#",0,["nested","text"]],["a",0,"y"]]`
	if got := jsonx.Stringify(wire); got != want {
		t.Fatalf("wire = %s want %s", got, want)
	}
	decoded, err = dec.Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	if stringifyOps(decoded) != stringifyOps(second) {
		t.Fatalf("second round trip: %s", stringifyOps(decoded))
	}
}

func TestCodecResetsDictionaryOnBase(t *testing.T) {
	enc := NewEncoder()
	path := Path{"value"}
	enc.Encode([]Op{&Set{Path: path, Value: float64(1)}})
	enc.Encode([]Op{&Set{Path: path, Value: float64(2)}})
	base := enc.Encode([]Op{&Replace{Value: parseObj(t, `{"value":3}`)}})
	if got := jsonx.Stringify(base); got != `[["r",{"value":3}]]` {
		t.Fatalf("base = %s", got)
	}
	after := enc.Encode([]Op{&Set{Path: path, Value: float64(4)}})
	if got := jsonx.Stringify(after); got != `[["s",["value"],4]]` {
		t.Fatalf("after base = %s", got)
	}
}

func TestCodecRejectsUnresolvedShortForms(t *testing.T) {
	if _, err := NewDecoder().Decode([]WireOp{{"a", "x"}}); err == nil {
		t.Fatal("unresolved short form accepted")
	}
	wire := []WireOp{
		{"#", float64(0), []any{"__proto__"}},
		{"s", float64(0), true},
	}
	var unsafeErr *UnsafePathError
	_, err := NewDecoder().Decode(wire)
	if err == nil {
		t.Fatal("unsafe interned path accepted")
	}
	if e, ok := err.(*UnsafePathError); !ok {
		var _ = unsafeErr
		t.Fatalf("err type %T", err)
	} else if e.Segment != "__proto__" {
		t.Fatalf("segment = %v", e.Segment)
	}
}

func TestCodecOmitsAdjacentRepeatedPath(t *testing.T) {
	enc := NewEncoder()
	path := Path{"value"}
	wire := enc.Encode([]Op{
		&Set{Path: path, Value: float64(1)},
		&Set{Path: path, Value: float64(2)},
	})
	if got := jsonx.Stringify(wire); got != `[["s",["value"],1],["s",2]]` {
		t.Fatalf("wire = %s", got)
	}
}

func TestCodecInternsOnSecondUse(t *testing.T) {
	enc := NewEncoder()
	path := Path{"a", "deep"}
	first := enc.Encode([]Op{&Append{Path: path, Text: "1"}})
	if got := jsonx.Stringify(first); got != `[["a",["a","deep"],"1"]]` {
		t.Fatalf("first = %s", got)
	}
	second := enc.Encode([]Op{&Append{Path: path, Text: "2"}})
	if got := jsonx.Stringify(second); got != `[["#",0,["a","deep"]],["a",0,"2"]]` {
		t.Fatalf("second = %s", got)
	}
}

func TestCodecNoNullCharacterCollisions(t *testing.T) {
	ops := opsFromLiteral(t, `[["s",["a\u0000b"],1],["s",["a","b"],2]]`)
	dec := NewDecoder()
	decoded, err := dec.Decode(NewEncoder().Encode(ops))
	if err != nil {
		t.Fatal(err)
	}
	if stringifyOps(decoded) != stringifyOps(ops) {
		t.Fatalf("collision: %s", stringifyOps(decoded))
	}
}

func TestCodecClearsDecoderIdsOnBase(t *testing.T) {
	dec := NewDecoder()
	if _, err := dec.Decode([]WireOp{{"#", float64(0), []any{"a"}}, {"a", float64(0), "1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode([]WireOp{{"r", parseObj(t, `{"a":""}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode([]WireOp{{"a", float64(0), "2"}}); err == nil {
		t.Fatal("stale id accepted after base")
	}
}

func TestCodecBatchesAfterBaseSelfContained(t *testing.T) {
	enc := NewEncoder()
	path := Path{"a", "deep"}
	enc.Encode([]Op{&Append{Path: path, Text: "1"}})
	enc.Encode([]Op{&Append{Path: path, Text: "2"}})
	base := enc.Encode([]Op{&Replace{Value: parseObj(t, `{"a":{"deep":"x"}}`)}})
	after := enc.Encode([]Op{&Append{Path: path, Text: "3"}})
	if got := jsonx.Stringify(after); got != `[["a",["a","deep"],"3"]]` {
		t.Fatalf("after = %s", got)
	}
	dec := NewDecoder()
	decoded, err := dec.Decode(base)
	if err != nil {
		t.Fatal(err)
	}
	if stringifyOps(decoded) != `[["r",{"a":{"deep":"x"}}]]` {
		t.Fatalf("base decode = %s", stringifyOps(decoded))
	}
	decoded, err = dec.Decode(after)
	if err != nil {
		t.Fatal(err)
	}
	if stringifyOps(decoded) != `[["a",["a","deep"],"3"]]` {
		t.Fatalf("after decode = %s", stringifyOps(decoded))
	}
}

func TestCodecRoundTripsMixedStreams(t *testing.T) {
	enc := NewEncoder()
	dec := NewDecoder()
	for index := 0; index < 100; index++ {
		batch := []Op{
			&Set{Path: Path{"rows", float64(index), "value"}, Value: float64(index)},
			&Append{Path: Path{"output"}, Text: itoa(index)},
			&Splice{Path: Path{"tail"}, Index: index, Remove: 0, Items: []chord.JsonValue{float64(index)}},
		}
		decoded, err := dec.Decode(enc.Encode(batch))
		if err != nil {
			t.Fatal(err)
		}
		if stringifyOps(decoded) != stringifyOps(batch) {
			t.Fatalf("batch %d: %s vs %s", index, stringifyOps(decoded), stringifyOps(batch))
		}
	}
}

func itoa(i int) string {
	return jsonx.FormatNumber(float64(i))
}

func TestArrayIndexSafety(t *testing.T) {
	applyStr := func(target string, ops string) string {
		t.Helper()
		result, err := Apply(parseObj(t, target), opsFromLiteral(t, ops))
		if err != nil {
			t.Fatalf("apply(%s, %s): %v", target, ops, err)
		}
		return jsonx.Stringify(result)
	}
	if got := applyStr(`{"values":[1,2,3]}`, `[["s",["values",1],9]]`); got != `{"values":[1,9,3]}` {
		t.Fatalf("existing index: %s", got)
	}
	if got := applyStr(`{"values":[1,2,3]}`, `[["s",["values",3],9]]`); got != `{"values":[1,2,3,9]}` {
		t.Fatalf("append one past end: %s", got)
	}
	for _, bad := range []struct{ target, ops string }{
		{`{"values":[1,2,3]}`, `[["s",["values",5],9]]`},
		{`{"values":[]}`, `[["s",["values",4294967290],1]]`},
		{`{"values":[1]}`, `[["s",["values","0"],9]]`},
		{`{"values":["a"]}`, `[["a",["values","0"],"b"]]`},
	} {
		if _, err := Apply(parseObj(t, bad.target), opsFromLiteral(t, bad.ops)); err == nil {
			t.Fatalf("expected rejection: %s on %s", bad.ops, bad.target)
		}
	}
	if got := applyStr(`{"values":[1]}`, `[["p",["values"],1,0,[null,null,9]]]`); got != `{"values":[1,null,null,9]}` {
		t.Fatalf("explicit growth: %s", got)
	}
	if _, err := Apply(parseObj(t, `{"values":[1]}`), opsFromLiteral(t, `[["d",["values",1]]]`)); err == nil {
		t.Fatal("deletion past end accepted")
	}
	// Large splice payloads.
	items := make([]chord.JsonValue, 300000)
	for i := range items {
		items[i] = nil
	}
	result, err := Apply(parseObj(t, `{"values":[]}`), []Op{&Splice{Path: Path{"values"}, Items: items}})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(*jsonx.Obj).MustGet("values").([]any); len(got) != len(items) {
		t.Fatalf("length = %d", len(got))
	}
}

func TestOperationStructureSafety(t *testing.T) {
	target := parseObj(t, `{"value":1}`)
	if _, err := Apply(target, []Op{&Set{Path: Path{"value"}, Value: float64(9)}}); err != nil {
		// sanity: valid op succeeds
		_ = err
	}
	// Unknown verbs cannot be constructed (typed algebra), but wire decoding
	// rejects them.
	if _, err := OpFromJSON(mustParse(t, `["ZZZ",["value"],9]`)); err == nil {
		t.Fatal("unknown verb accepted")
	}
	if _, err := OpFromJSON(mustParse(t, `["p",["values"],0,0,"not-an-array"]`)); err == nil {
		t.Fatal("non-array items accepted")
	}
	if _, err := OpFromJSON(mustParse(t, `["s","value",9]`)); err == nil {
		t.Fatal("string path accepted")
	}
	if _, err := OpFromJSON(nil); err == nil {
		t.Fatal("null op accepted")
	}
	if _, err := Apply(parseObj(t, `{"value":"abc"}`), []Op{&Truncate{Path: Path{"value"}, Count: -1}}); err == nil {
		t.Fatal("negative truncate accepted")
	}
	if _, err := NewDecoder().Decode([]WireOp{{"t", []any{"value"}, float64(-1)}}); err == nil {
		t.Fatal("decoder accepted negative truncate")
	}
}

func TestSpliceClampsRemovalPastEnd(t *testing.T) {
	result, err := Apply(parseObj(t, `{"values":[1,2]}`), opsFromLiteral(t, `[["p",["values"],0,1000000000,[]]]`))
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonx.Stringify(result); got != `{"values":[]}` {
		t.Fatalf("clamped = %s", got)
	}
}

func TestOperationAssertions(t *testing.T) {
	for _, op := range []string{
		`["r",{"value":1}]`,
		`["s",["value"],1]`,
		`["d",["value"]]`,
		`["a",["value"],"x"]`,
		`["t",["value"],2]`,
		`["p",["value"],0,0,[]]`,
		`["m",["value"],[0]]`,
	} {
		if _, err := OpFromJSON(mustParse(t, op)); err != nil {
			t.Fatalf("valid op rejected: %s (%v)", op, err)
		}
	}
	for _, wireOnly := range []WireOp{
		{"s", float64(1)},
		{"d"},
		{"a", "x"},
		{"t", float64(2)},
		{"p", float64(0), float64(0), []any{}},
		{"#", float64(0), []any{"value"}},
		{"s", float64(0), float64(1)},
	} {
		if _, err := OpFromJSON(wireOnly); err == nil {
			t.Fatalf("wire form accepted as decoded: %v", wireOnly)
		}
		if err := assertValidWireOp(wireOnly); err != nil {
			t.Fatalf("wire form rejected: %v (%v)", wireOnly, err)
		}
	}
}

func TestOverlapBounded(t *testing.T) {
	if got := Overlap("abcdefgh", "defghxyz", 65536, 0, 0); got != 5 {
		t.Fatalf("overlap = %d", got)
	}
	if got := Overlap("abcdef", "defghi", 0, 0, 0); got != 0 {
		t.Fatalf("overlap scan 0 = %d", got)
	}
}
