package codingagent

// Golden test for NormalizeNFKC against Node ICU
// (String.normalize("NFKC")): 1,533 samples spanning compatibility
// decompositions, Hangul, canonical compositions, and blocked marks.

import (
	"encoding/json"
	"os"
	"testing"
)

func TestNormalizeNFKCGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/nfkc-golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var samples []json.RawMessage
	if err := json.Unmarshal(data, &samples); err != nil {
		t.Fatalf("parse golden: %v", err)
	}

	failures := 0
	for _, raw := range samples {
		var pair []json.RawMessage
		if err := json.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
			t.Fatalf("parse pair %s: %v", raw, err)
		}

		var want []rune
		if err := json.Unmarshal(pair[1], &want); err != nil {
			t.Fatalf("parse runes: %v", err)
		}

		var input string
		if err := json.Unmarshal(pair[0], &input); err == nil {
			if got := []rune(NormalizeNFKC(input)); !runeSlicesEqual(got, want) {
				failures++
				if failures <= 8 {
					t.Errorf("NFKC(%q) = %X, want %X", input, got, want)
				}
			}
			continue
		}
		var codepoint float64
		if err := json.Unmarshal(pair[0], &codepoint); err != nil {
			t.Fatalf("parse input: %v", err)
		}
		if got := []rune(NormalizeNFKC(string(rune(codepoint)))); !runeSlicesEqual(got, want) {
			failures++
			if failures <= 8 {
				t.Errorf("NFKC(U+%04X) = %X, want %X", int(codepoint), got, want)
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d/%d golden mismatches", failures, len(samples))
	}
}
