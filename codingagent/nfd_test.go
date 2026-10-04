package codingagent

// Golden test for NormalizeNFD against Node ICU (String.normalize("NFD")),
// the runtime the TypeScript reference uses.

import (
	"encoding/json"
	"os"
	"testing"
)

func TestNormalizeNFDGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/nfd-golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var samples []json.RawMessage
	if err := json.Unmarshal(data, &samples); err != nil {
		t.Fatalf("parse golden: %v", err)
	}

	for _, raw := range samples {
		var pair []json.RawMessage
		if err := json.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
			t.Fatalf("parse pair %s: %v", raw, err)
		}

		var want []rune
		if err := json.Unmarshal(pair[1], &want); err != nil {
			// Tolerate a raw numeric codepoint sample.
			var single float64
			if err2 := json.Unmarshal(pair[0], &single); err2 == nil {
				continue
			}
			t.Fatalf("parse runes: %v", err)
		}

		var input string
		if err := json.Unmarshal(pair[0], &input); err == nil {
			if got := []rune(NormalizeNFD(input)); !runeSlicesEqual(got, want) {
				t.Errorf("NFD(%q) = %X, want %X", input, got, want)
			}
			continue
		}
		var codepoint float64
		if err := json.Unmarshal(pair[0], &codepoint); err != nil {
			t.Fatalf("parse input: %v", err)
		}
		if got := []rune(NormalizeNFD(string(rune(codepoint)))); !runeSlicesEqual(got, want) {
			t.Errorf("NFD(U+%04X) = %X, want %X", int(codepoint), got, want)
		}
	}
}

func runeSlicesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNormalizeNFDIdentity(t *testing.T) {
	for _, s := range []string{"", "abc", "日本語", "hello world", "\n\t"} {
		if got := NormalizeNFD(s); got != s {
			t.Errorf("NFD(%q) = %q", s, got)
		}
	}
}

func TestTryVariants(t *testing.T) {
	// AM/PM narrow no-break space.
	got := tryMacOSScreenshotPath("Screen Shot 2024-01-01 at 10.00.00 AM.png")
	want := "Screen Shot 2024-01-01 at 10.00.00 AM.png"
	if got != want {
		t.Fatalf("ampm = %q, want %q", got, want)
	}
	// Curly quote.
	if got := tryCurlyQuoteVariant("Capture d'écran.png"); got != "Capture d’écran.png" {
		t.Fatalf("curly = %q", got)
	}
	// NFD composes to multi-rune.
	if got := NormalizeNFD("é"); []rune(got)[0] != 'e' || len([]rune(got)) != 2 {
		t.Fatalf("nfd = %q", got)
	}
}

func TestResolveReadPathVariants(t *testing.T) {
	dir := t.TempDir()

	// Exact name resolves directly.
	exact := dir + "/readme.md"
	os.WriteFile(exact, []byte("x"), 0o644)
	if got := ResolveReadPath(dir+"/readme.md", dir); got != exact {
		t.Fatalf("exact = %q", got)
	}

	// Curly-quote on-disk variant found from straight-apostrophe input.
	curly := dir + "/Capture d’écran.png"
	os.WriteFile(curly, []byte("x"), 0o644)
	if got := ResolveReadPath(dir+"/Capture d'écran.png", dir); got != curly {
		t.Fatalf("curly resolve = %q", got)
	}

	// Missing file: raw resolution returned.
	if got := ResolveReadPath(dir+"/missing.txt", dir); got != dir+"/missing.txt" {
		t.Fatalf("missing = %q", got)
	}
}
