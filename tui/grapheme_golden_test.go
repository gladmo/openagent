package tui

import (
	"strings"
	"testing"
)

// Golden cases generated from Node's Intl.Segmenter (granularity grapheme).
// Regenerate with /tmp/gen-grapheme-golden.mjs (node >= 22, full ICU).
type graphemeGoldenCase struct {
	input string
	want  string
}

var graphemeGoldenCases = []graphemeGoldenCase{
	{"", ""},
	{"a", "a"},
	{"ab", "a\x00b"},
	{"a b", "a\x00 \x00b"},
	{"hello world", "h\x00e\x00l\x00l\x00o\x00 \x00w\x00o\x00r\x00l\x00d"},
	{"g\u0308", "g\u0308"},
	{"g\u0308", "g\u0308"},
	{"\uac01", "\uac01"},
	{"\u1100\u1161\u11a8", "\u1100\u1161\u11a8"},
	{"\uac01", "\uac01"},
	{"\x0d\x0a", "\x0d\x0a"},
	{"a\x0d\x0ab", "a\x00\x0d\x0a\x00b"},
	{"a\x0ab", "a\x00\x0a\x00b"},
	{"a\x0db", "a\x00\x0d\x00b"},
	{"\u0e01\u0e33", "\u0e01\u0e33"},
	{"\u0e40", "\u0e40"},
	{"\u0937\u093f", "\u0937\u093f"},
	{"\u0915\u094d\u0937\u093f", "\u0915\u094d\u0937\u093f"},
	{"\u0915\u094d\u0937", "\u0915\u094d\u0937"},
	{"\u0915\u0937", "\u0915\x00\u0937"},
	{"\u0924\u094d\u0924", "\u0924\u094d\u0924"},
	{"\u0915\u094d\u0930\u092e", "\u0915\u094d\u0930\x00\u092e"},
	{"\U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466", "\U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466"},
	{"\U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466x", "\U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466\x00x"},
	{"\U0001f44d\U0001f3fd", "\U0001f44d\U0001f3fd"},
	{"\U0001f1fa\U0001f1f8", "\U0001f1fa\U0001f1f8"},
	{"\U0001f1fa\U0001f1f8\U0001f1eb\U0001f1f7", "\U0001f1fa\U0001f1f8\x00\U0001f1eb\U0001f1f7"},
	{"\U0001f1e6\U0001f1e8\U0001f1e6\U0001f1e9", "\U0001f1e6\U0001f1e8\x00\U0001f1e6\U0001f1e9"},
	{"\U0001f3f3\ufe0f\u200d\U0001f308", "\U0001f3f3\ufe0f\u200d\U0001f308"},
	{"\U0001f3f4\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f", "\U0001f3f4\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f"},
	{"#\ufe0f\u20e3", "#\ufe0f\u20e3"},
	{"1\ufe0f\u20e32\ufe0f\u20e3", "1\ufe0f\u20e3\x002\ufe0f\u20e3"},
	{"e\u0301\u0302", "e\u0301\u0302"},
	{"a\u200db", "a\u200d\x00b"},
	{"\u200d", "\u200d"},
	{"a\u200d\u200db", "a\u200d\u200d\x00b"},
	{"\U0001f44d\u200d\U0001f44d", "\U0001f44d\u200d\U0001f44d"},
	{"\u270c\U0001f3fd", "\u270c\U0001f3fd"},
	{"\u2764\ufe0f", "\u2764\ufe0f"},
	{"\u2764", "\u2764"},
	{"\u26a1", "\u26a1"},
	{"\u1100", "\u1100"},
	{"\u1100\u1161", "\u1100\u1161"},
	{"\u1100\u1161\u11a8", "\u1100\u1161\u11a8"},
	{"\uac00\ub098\ub2e4", "\uac00\x00\ub098\x00\ub2e4"},
	{"\u4e2d\u6587\u5b57", "\u4e2d\x00\u6587\x00\u5b57"},
	{"\u65e5\u672c\u8a9e\u30c6\u30b9\u30c8", "\u65e5\x00\u672c\x00\u8a9e\x00\u30c6\x00\u30b9\x00\u30c8"},
	{"\ud55c\uad6d\uc5b4", "\ud55c\x00\uad6d\x00\uc5b4"},
	{"\u00e1b\u0301", "\u00e1\x00b\u0301"},
	{"1.\u00e1", "1\x00.\x00\u00e1"},
	{"x\u00ady", "x\x00\u00ad\x00y"},
	{"\u0645\u0631\u062d\u0628\u0627", "\u0645\x00\u0631\x00\u062d\x00\u0628\x00\u0627"},
	{"\u0e19\u0e49\u0e33", "\u0e19\u0e49\u0e33"},
	{"\u0e19\u0e4d\u0e32", "\u0e19\u0e4d\x00\u0e32"},
	{"\U0001f389\U0001f38a", "\U0001f389\x00\U0001f38a"},
	{"\U0001f9d1\u200d\U0001f680", "\U0001f9d1\u200d\U0001f680"},
	{"\U0001f469\U0001f3fb\u200d\U0001f4bb", "\U0001f469\U0001f3fb\u200d\U0001f4bb"},
	{"\U0001f3c3\U0001f3fe\u200d\u2640\ufe0f", "\U0001f3c3\U0001f3fe\u200d\u2640\ufe0f"},
	{"A\u04acBF", "A\x00\u04ac\x00B\x00F"},
	{"\u1820\u1880", "\u1820\x00\u1880"},
	{"\u1820\u180b", "\u1820\u180b"},
	{"a\u0300\u0316\u0334", "a\u0300\u0316\u0334"},
	{"\u05d7\u05b7\u05d9", "\u05d7\u05b7\x00\u05d9"},
	{"\U0001f600 \u00e9", "\U0001f600\x00 \x00\u00e9"},
	{"\U0001f1e6\U0001f1e6", "\U0001f1e6\U0001f1e6"},
	{"\U0001f1e6\U0001f1e7\U0001f1e6", "\U0001f1e6\U0001f1e7\x00\U0001f1e6"},
	{"\U0001f1e6\U0001f1e7\U0001f1e6\U0001f1e7", "\U0001f1e6\U0001f1e7\x00\U0001f1e6\U0001f1e7"},
	{"\U0001f469\u200d\U0001f469\u200d\U0001f466", "\U0001f469\u200d\U0001f469\u200d\U0001f466"},
	{"a\x09b", "a\x00\x09\x00b"},
	{"\x09\x09", "\x09\x00\x09"},
	{" \u00a0 x", " \x00\u00a0\x00 \x00x"},
	{"\U0001f1e6\u200d\U0001f1e6", "\U0001f1e6\u200d\x00\U0001f1e6"},
	{"\u0915\u094d\u0915\u094d\u0915", "\u0915\u094d\u0915\u094d\u0915"},
	{"\u0b15\u0b4d\u0b15", "\u0b15\u0b4d\u0b15"},
	{"\u0c15\u0c4d\u0c15", "\u0c15\u0c4d\u0c15"},
	{"\u0d15\u0d4d\u0d15", "\u0d15\u0d4d\u0d15"},
	{"\u0995\u09cd\u0995", "\u0995\u09cd\u0995"},
	{"\u0a95\u0acd\u0a95", "\u0a95\u0acd\u0a95"},
	{"\u0b15\u0b4d\u0b15\u200d\u0b15", "\u0b15\u0b4d\u0b15\u200d\x00\u0b15"},
}

func TestSegmentGraphemesMatchesIntlSegmenter(t *testing.T) {
	for _, tc := range graphemeGoldenCases {
		got := strings.Join(segmentGraphemes(tc.input), "\x00")
		if got != tc.want {
			t.Errorf("segmentGraphemes(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
