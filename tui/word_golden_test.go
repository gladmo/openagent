package tui

import (
	"strings"
	"testing"
)

// Golden cases generated from Node's Intl.Segmenter (granularity word);
// dictionary-dependent CJK-ideograph/Hiragana cases are excluded (ICU CLDR
// dictionary tailoring is a documented deviation; see word_segmenter.go).
// Regenerate with /tmp/gen-word-golden.mjs (node >= 22, full ICU).

type wordGoldenCase struct {
	input string
	want  string
}

var wordGoldenCases = []wordGoldenCase{
	{"hello world", "hello:1\x00 :0\x00world:1"},
	{"Hello, World!", "Hello:1\x00,:0\x00 :0\x00World:1\x00!:0"},
	{"can't", "can't:1"},
	{"3.2", "3.2:1"},
	{"1,456.789", "1,456.789:1"},
	{"snake_case_name", "snake_case_name:1"},
	{"kebab-case-name", "kebab:1\x00-:0\x00case:1\x00-:0\x00name:1"},
	{"e.g.", "e.g:1\x00.:0"},
	{"example.com", "example.com:1"},
	{"foo_bar", "foo_bar:1"},
	{"_leading", "_leading:1"},
	{"trailing_", "trailing_:1"},
	{"AIK:are", "AIK:are:1"},
	{"c:a", "c:a:1"},
	{"l'objectif", "l'objectif:1"},
	{"50%", "50:1\x00%:0"},
	{"$100", "$:0\x00100:1"},
	{"a/b/c", "a:1\x00/:0\x00b:1\x00/:0\x00c:1"},
	{"x  y", "x:1\x00  :0\x00y:1"},
	{"  ", "  :0"},
	{"\x09\x09", "\x09:0\x00\x09:0"},
	{"\ud55c\uad6d\uc5b4 \ub2e8\uc5b4", "\ud55c\uad6d\uc5b4:1\x00 :0\x00\ub2e8\uc5b4:1"},
	{"\u05e9\u05b8\u05c1\u05dc\u05d5\u05b9\u05dd", "\u05e9\u05b8\u05c1\u05dc\u05d5\u05b9\u05dd:1"},
	{"\u05f4quote\u05f4", "\u05f4:0\x00quote:1\x00\u05f4:0"},
	{"\u05f3geresh\u05f3", "\u05f3geresh\u05f3:1"},
	{"The quick (brown) fox", "The:1\x00 :0\x00quick:1\x00 :0\x00(:0\x00brown:1\x00):0\x00 :0\x00fox:1"},
	{"out-of-the-box", "out:1\x00-:0\x00of:1\x00-:0\x00the:1\x00-:0\x00box:1"},
	{"mixed123abc", "mixed123abc:1"},
	{"123abc456", "123abc456:1"},
	{"\U0001f1fa\U0001f1f8 flag", "\U0001f1fa\U0001f1f8:0\x00 :0\x00flag:1"},
	{"\U0001f468\u200d\U0001f469\u200d\U0001f467 family", "\U0001f468\u200d\U0001f469\u200d\U0001f467:0\x00 :0\x00family:1"},
	{"\U0001f44d\U0001f3fd thumbs", "\U0001f44d\U0001f3fd:0\x00 :0\x00thumbs:1"},
	{"#hashtag", "#:0\x00hashtag:1"},
	{"@mention", "@:0\x00mention:1"},
	{"e\u0301tude", "e\u0301tude:1"},
	{"\u0301leading", "\u0301:0\x00leading:1"},
	{"trailing\u0301", "trailing\u0301:1"},
	{"a\u200db", "a\u200db:1"},
	{"va\u0301lue 42", "va\u0301lue:1\x00 :0\x0042:1"},
	{"x=y=z", "x:1\x00=:0\x00y:1\x00=:0\x00z:1"},
	{"path/to/file.ts", "path:1\x00/:0\x00to:1\x00/:0\x00file.ts:1"},
	{"C:\\Users\\mo", "C:1\x00::0\x00\\:0\x00Users:1\x00\\:0\x00mo:1"},
	{"one\x0d\x0atwo\x0dinthree", "one:1\x00\x0d\x0a:0\x00two:1\x00\x0d:0\x00inthree:1"},
	{"nl\x0btest", "nl:1\x00\x0b:0\x00test:1"},
	{"\u0085nel", "\u0085:0\x00nel:1"},
	{"\u2028ls\u2029ps", "\u2028:0\x00ls:1\x00\u2029:0\x00ps:1"},
	{"a b", "a:1\x00 :0\x00b:1"},
	{"a\u00a0b", "a:1\x00\u00a0:0\x00b:1"},
	{"3a", "3a:1"},
	{"a3", "a3:1"},
	{"a3b", "a3b:1"},
	{"32.3 feet", "32.3:1\x00 :0\x00feet:1"},
	{"word\u00adsoft", "word\u00adsoft:1"},
	{"\u0644\u0627 _ \u0645\u0631", "\u0644\u0627:1\x00 :0\x00_:0\x00 :0\x00\u0645\u0631:1"},
	{"1999-12-31", "1999:1\x00-:0\x0012:1\x00-:0\x0031:1"},
	{"2024.01.02", "2024.01.02:1"},
	{"v1.2.3", "v1.2.3:1"},
	{"N/A", "N:1\x00/:0\x00A:1"},
	{"co-op", "co:1\x00-:0\x00op:1"},
}

func TestSegmentWordsMatchesIntlSegmenter(t *testing.T) {
	for _, tc := range wordGoldenCases {
		parts := segmentWords(tc.input)
		got := make([]string, len(parts))
		for i, p := range parts {
			flag := ":0"
			if p.IsWordLike {
				flag = ":1"
			}
			got[i] = p.Segment + flag
		}
		joined := strings.Join(got, "\x00")
		if joined != tc.want {
			t.Errorf("segmentWords(%q) =\n%q\nwant\n%q", tc.input, joined, tc.want)
		}
	}
}
