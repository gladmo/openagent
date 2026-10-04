package minimatch

import "testing"

func TestBasicWildcards(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"*", "file", true},
		{"*", "dir/file", false}, // * stays within a segment
		{"*.ts", "a.ts", true},
		{"*.ts", "a.js", false},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"**", "anything/at/all", true},
		{"**/*.ts", "src/deep/a.ts", true},
		{"**/*.ts", "a.ts", true},
		{"src/**", "src/x/y", true},
		{"src/**", "other/x", false},
		{"provider/*", "provider/model-1", true},
		{"provider/*", "provider/ns/model", false},
		{"[abc].txt", "b.txt", true},
		{"[abc].txt", "d.txt", false},
		{"[!abc].txt", "d.txt", true},
		{"[!abc].txt", "a.txt", false},
		{"plain", "plain", true},
		{"plain", "other", false},
		{"a.b", "axb", false}, // dot is literal
		{"*sonnet*", "claude-sonnet-4-5", true},
	}
	for _, tc := range cases {
		if got := MinimatchMatch(tc.path, tc.pattern); got != tc.want {
			t.Errorf("Minimatch(%q, %q) = %v, want %v", tc.path, tc.pattern, got, tc.want)
		}
	}
}

func TestNoCase(t *testing.T) {
	if !MinimatchMatch("MODEL-X", "model-*", Options{NoCase: true}) {
		t.Fatal("nocase match failed")
	}
	if MinimatchMatch("MODEL-X", "model-*") {
		t.Fatal("case-sensitive match should fail")
	}
}

func TestQuotingMetacharacters(t *testing.T) {
	if !MinimatchMatch("a+b", "a+b") {
		t.Fatal("literal + should match")
	}
	if MinimatchMatch("aab", "a+b") {
		t.Fatal("+ must be literal, not regex +")
	}
	if !MinimatchMatch("a(b)", "a(b)") {
		t.Fatal("literal parens should match")
	}
}
