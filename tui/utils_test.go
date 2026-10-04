package tui

import (
	"strings"
	"testing"
)

// Ported from pi/packages/tui/test/truncate-to-width.test.ts, wrap-ansi.test.ts,
// and the pure-function subset of tab-width.test.ts.

func repeat(s string, n int) string { return strings.Repeat(s, n) }

func TestTruncateToWidthLargeUnicodeInput(t *testing.T) {
	text := repeat("🙂界", 100_000)
	truncated := TruncateToWidth(text, 40, "…", false)
	if VisibleWidth(truncated) > 40 {
		t.Fatalf("width %d > 40", VisibleWidth(truncated))
	}
	if !strings.HasSuffix(truncated, "…\x1b[0m") {
		t.Fatalf("suffix = %q", truncated[len(truncated)-10:])
	}
}

func TestTruncateToWidthPreservesAnsiStyling(t *testing.T) {
	text := "\x1b[31m" + repeat("hello ", 1000) + "\x1b[0m"
	truncated := TruncateToWidth(text, 20, "…", false)
	if VisibleWidth(truncated) > 20 {
		t.Fatalf("width %d > 20", VisibleWidth(truncated))
	}
	if !strings.Contains(truncated, "\x1b[31m") {
		t.Fatal("missing red")
	}
	if !strings.HasSuffix(truncated, "\x1b[0m…\x1b[0m") {
		t.Fatalf("suffix = %q", truncated)
	}
}

func TestTruncateToWidthClosesBelOsc8Link(t *testing.T) {
	open := "\x1b]8;;https://example.com\x07"
	closeCode := "\x1b]8;;\x07"
	text := open + "some-longer-label-here" + closeCode
	want := open + "some-longer-" + closeCode + "\x1b[0m...\x1b[0m"
	if got := TruncateToWidth(text, 15, "...", false); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestTruncateToWidthMalformedAnsiPrefix(t *testing.T) {
	text := "abc\x1bnot-ansi " + repeat("🙂", 1000)
	truncated := TruncateToWidth(text, 20, "…", false)
	if VisibleWidth(truncated) > 20 {
		t.Fatalf("width %d > 20", VisibleWidth(truncated))
	}
}

func TestTruncateToWidthClipsWideEllipsis(t *testing.T) {
	if got := TruncateToWidth("abcdef", 1, "🙂", false); got != "" {
		t.Fatalf("width 1: got %q", got)
	}
	if got := TruncateToWidth("abcdef", 2, "🙂", false); got != "\x1b[0m🙂\x1b[0m" {
		t.Fatalf("width 2: got %q", got)
	}
}

func TestTruncateToWidthReturnsOriginalWhenItFits(t *testing.T) {
	if got := TruncateToWidth("a", 2, "🙂", false); got != "a" {
		t.Fatalf("got %q", got)
	}
	if got := TruncateToWidth("界", 2, "🙂", false); got != "界" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateToWidthPads(t *testing.T) {
	truncated := TruncateToWidth("🙂界🙂界🙂界", 8, "…", true)
	if w := VisibleWidth(truncated); w != 8 {
		t.Fatalf("width = %d, want 8 (%q)", w, truncated)
	}
}

func TestTruncateToWidthTrailingResetWithoutEllipsis(t *testing.T) {
	truncated := TruncateToWidth("\x1b[31m"+repeat("hello", 100), 10, "", false)
	if VisibleWidth(truncated) > 10 {
		t.Fatalf("width %d > 10", VisibleWidth(truncated))
	}
	if !strings.HasSuffix(truncated, "\x1b[0m") {
		t.Fatalf("suffix = %q", truncated)
	}
}

func TestTruncateToWidthKeepsContiguousPrefix(t *testing.T) {
	got := TruncateToWidth("🙂\t界 \x1b_abc\x07", 7, "…", true)
	if want := "🙂\t\x1b[0m…\x1b[0m "; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestVisibleWidthTabsAndAnsi(t *testing.T) {
	if got := VisibleWidth("\t\x1b[31m界\x1b[0m"); got != 5 {
		t.Fatalf("got %d, want 5", got)
	}
}

func TestVisibleWidthIndicConjunctSpacing(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"र्क", 2},
		{"नेटवर्क", 5},
		{"सर्वाधिकार सुरक्षित। ऑर्डर पर क्लिक करें", 33},
		{"র্ক", 2},
		{"ર્ક", 2},
		{"ର୍କ", 2},
		{"ర్క", 2},
		{"ര്‍ക", 2},
	}
	for _, tc := range cases {
		if got := VisibleWidth(tc.text); got != tc.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

func TestVisibleWidthOrdinaryCombiningMarksZeroWidth(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"e\u0301", 1},
		{"čřžůú", 5},
		{"שָׁ", 1},
		{"بّ", 1},
		{"རྐ", 1},
		{"ᜠ᜴", 1},
		{"가〮", 2},
		{"가〯", 2},
	}
	for _, tc := range cases {
		if got := VisibleWidth(tc.text); got != tc.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

func TestVisibleWidthCjkJapanese(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"网络", 4},
		{"ネットワーク", 12},
		{"が", 2},
		{"か\u3099", 2},
	}
	for _, tc := range cases {
		if got := VisibleWidth(tc.text); got != tc.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

func TestVisibleWidthMyanmarMarks(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"ကာ", 2},
		{"ကေ", 2},
		{"က်", 2},
		{"ကျ", 2},
		{"ကြ", 2},
		{"ကဳ", 2},
		{"ကဴ", 2},
		{"ကဵ", 2},
		{"ကး", 2},
		{"ကို", 1},
		{"က္", 1},
	}
	for _, tc := range cases {
		if got := VisibleWidth(tc.text); got != tc.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

func TestVisibleWidthThaiLaoAm(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"ำ", 1},
		{"ຳ", 1},
		{"กำ", 2},
		{"ກຳ", 2},
	}
	for _, tc := range cases {
		if got := VisibleWidth(tc.text); got != tc.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

func TestNormalizeTerminalOutputThaiLaoAm(t *testing.T) {
	if got := NormalizeTerminalOutput("ำ"); got != "ํา" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeTerminalOutput("ຳ"); got != "ໍາ" {
		t.Fatalf("got %q", got)
	}
	if VisibleWidth(NormalizeTerminalOutput("ำabc")) != VisibleWidth("ำabc") {
		t.Fatal("width changed for Thai AM")
	}
	if VisibleWidth(NormalizeTerminalOutput("ຳabc")) != VisibleWidth("ຳabc") {
		t.Fatal("width changed for Lao AM")
	}
}

func TestVisibleWidthOscAndRegionalIndicators(t *testing.T) {
	if got := VisibleWidth("\x1b]133;A\x07hello\x1b]133;B\x07"); got != 5 {
		t.Fatalf("OSC BEL: got %d", got)
	}
	if got := VisibleWidth("\x1b]133;A\x1b\\hello\x1b]133;B\x1b\\"); got != 5 {
		t.Fatalf("OSC ST: got %d", got)
	}
	if got := VisibleWidth("🇨"); got != 2 {
		t.Fatalf("isolated RI: got %d", got)
	}
	if got := VisibleWidth("🇨🇳"); got != 2 {
		t.Fatalf("RI pair: got %d", got)
	}
}

func TestWrapTextWithAnsiUnderline(t *testing.T) {
	underlineOn := "\x1b[4m"
	underlineOff := "\x1b[24m"
	url := "https://example.com/very/long/path/that/will/wrap"
	text := "read this thread " + underlineOn + url + underlineOff

	wrapped := WrapTextWithAnsi(text, 40)
	if wrapped[0] != "read this thread" {
		t.Fatalf("first line = %q", wrapped[0])
	}
	if !strings.HasPrefix(wrapped[1], underlineOn) {
		t.Fatalf("second line = %q", wrapped[1])
	}
	if !strings.Contains(wrapped[1], "https://") {
		t.Fatalf("second line = %q", wrapped[1])
	}

	textWithUnderlinedTrailingSpace := underlineOn + "underlined text here " + underlineOff + "more"
	wrapped = WrapTextWithAnsi(textWithUnderlinedTrailingSpace, 18)
	if strings.Contains(wrapped[0], " "+underlineOff) {
		t.Fatalf("whitespace before reset: %q", wrapped[0])
	}

	url = "https://example.com/very/long/path/that/will/definitely/wrap"
	text = "prefix " + underlineOn + url + underlineOff + " suffix"
	wrapped = WrapTextWithAnsi(text, 30)
	for i := 1; i < len(wrapped)-1; i++ {
		line := wrapped[i]
		if strings.Contains(line, underlineOn) {
			if !strings.HasSuffix(line, underlineOff) {
				t.Fatalf("line %d does not end with underline-off: %q", i, line)
			}
			if strings.HasSuffix(line, "\x1b[0m") {
				t.Fatalf("line %d ends with full reset: %q", i, line)
			}
		}
	}
}

func TestWrapTextWithAnsiBackgroundPreservation(t *testing.T) {
	bgBlue := "\x1b[44m"
	reset := "\x1b[0m"
	text := bgBlue + "hello world this is blue background text" + reset
	wrapped := WrapTextWithAnsi(text, 15)
	for _, line := range wrapped {
		if !strings.Contains(line, bgBlue) {
			t.Fatalf("line missing background: %q", line)
		}
	}
	for i := 0; i < len(wrapped)-1; i++ {
		if strings.HasSuffix(wrapped[i], reset) {
			t.Fatalf("middle line ends with full reset: %q", wrapped[i])
		}
	}
}

func TestWrapTextWithAnsiUnderlineInsideBackground(t *testing.T) {
	underlineOn := "\x1b[4m"
	underlineOff := "\x1b[24m"
	reset := "\x1b[0m"
	text := "\x1b[41mprefix " + underlineOn + "UNDERLINED_CONTENT_THAT_WRAPS" + underlineOff + " suffix" + reset

	wrapped := WrapTextWithAnsi(text, 20)
	for _, line := range wrapped {
		hasBgColor := strings.Contains(line, "[41m") || strings.Contains(line, ";41m") || strings.Contains(line, "[41;")
		if !hasBgColor {
			t.Fatalf("line missing background: %q", line)
		}
	}
}

func TestWrapTextWithAnsiLineEndings(t *testing.T) {
	wrapped := WrapTextWithAnsi("first\nsecond\r\nthird\rfourth", 80)
	want := []string{"first", "second", "third", "fourth"}
	if len(wrapped) != len(want) {
		t.Fatalf("got %q", wrapped)
	}
	for i := range want {
		if wrapped[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, wrapped[i], want[i])
		}
	}

	red := "\x1b[31m"
	reset := "\x1b[0m"
	wrapped = WrapTextWithAnsi(red+"first\r\nsecond\rthird"+reset, 80)
	want = []string{red + "first", red + "second", red + "third" + reset}
	for i := range want {
		if wrapped[i] != want[i] {
			t.Fatalf("ansi line %d = %q, want %q", i, wrapped[i], want[i])
		}
	}
}

func TestWrapTextWithAnsiPlainAndCjk(t *testing.T) {
	wrapped := WrapTextWithAnsi("hello world this is a test", 10)
	if len(wrapped) <= 1 {
		t.Fatalf("not wrapped: %q", wrapped)
	}
	for _, line := range wrapped {
		if VisibleWidth(line) > 10 {
			t.Fatalf("line too wide: %q", line)
		}
	}

	text := "This is an example 中文汉字测试段落内容中文汉字测试段落内容."
	wrapped = WrapTextWithAnsi(text, 40)
	want := []string{"This is an example 中文汉字测试段落内容", "中文汉字测试段落内容."}
	if len(wrapped) != 2 || wrapped[0] != want[0] || wrapped[1] != want[1] {
		t.Fatalf("got %q", wrapped)
	}

	red := "\x1b[31m"
	reset := "\x1b[0m"
	wrapped = WrapTextWithAnsi(red+text+reset, 40)
	if len(wrapped) != 2 {
		t.Fatalf("got %q", wrapped)
	}
	if wrapped[0] != red+"This is an example 中文汉字测试段落内容" {
		t.Fatalf("line 0 = %q", wrapped[0])
	}
	if wrapped[1] != red+"中文汉字测试段落内容."+reset {
		t.Fatalf("line 1 = %q", wrapped[1])
	}
}

func TestWrapTextWithAnsiTrailingWhitespaceAndColorContinuation(t *testing.T) {
	wrapped := WrapTextWithAnsi("  ", 1)
	if VisibleWidth(wrapped[0]) > 1 {
		t.Fatalf("width = %d", VisibleWidth(wrapped[0]))
	}

	red := "\x1b[31m"
	reset := "\x1b[0m"
	text := red + "hello world this is red" + reset
	wrapped = WrapTextWithAnsi(text, 10)
	for i := 1; i < len(wrapped); i++ {
		if !strings.HasPrefix(wrapped[i], red) {
			t.Fatalf("line %d missing red prefix: %q", i, wrapped[i])
		}
	}
	for i := 0; i < len(wrapped)-1; i++ {
		if strings.HasSuffix(wrapped[i], reset) {
			t.Fatalf("middle line ends with reset: %q", wrapped[i])
		}
	}
}

func TestWrapTextWithAnsiOsc8Hyperlinks(t *testing.T) {
	url := "https://example.com"
	input := "\x1b]8;;" + url + "\x1b\\0123456789\x1b]8;;\x1b\\"
	lines := WrapTextWithAnsi(input, 6)

	oscOpen := "\x1b]8;;" + url + "\x1b\\"
	for _, line := range lines {
		stripped := stripOsc8ForTest(line)
		if strings.TrimSpace(stripped) != "" && !strings.Contains(line, oscOpen) {
			t.Fatalf("line %q has visible text but no OSC 8 re-open", line)
		}
	}
	for i := 0; i < len(lines)-1; i++ {
		if strings.Contains(lines[i], oscOpen) && !strings.HasSuffix(lines[i], "\x1b]8;;\x1b\\") {
			t.Fatalf("non-final line %q does not close hyperlink", lines[i])
		}
	}

	belURL := "https://example.com/oauth/" + repeat("a", 32)
	input = "\x1b]8;;" + belURL + "\x07" + belURL + "\x1b]8;;\x07"
	lines = WrapTextWithAnsi(input, 20)
	if len(lines) <= 1 {
		t.Fatalf("not wrapped: %q", lines)
	}
	for _, line := range lines {
		if !strings.Contains(line, "\x1b]8;;"+belURL+"\x07") {
			t.Fatalf("line %q does not reopen with BEL", line)
		}
		if strings.Contains(line, "\x1b]8;;"+belURL+"\x1b\\") {
			t.Fatalf("line %q reopens with ST", line)
		}
	}
	for _, line := range lines[:len(lines)-1] {
		if !strings.HasSuffix(line, "\x1b]8;;\x07") {
			t.Fatalf("line %q does not close with BEL", line)
		}
	}

	input = "before \x1b]8;;" + url + "\x1b\\link\x1b]8;;\x1b\\ after"
	lines = WrapTextWithAnsi(input, 80)
	if len(lines) != 1 {
		t.Fatalf("expected single line, got %q", lines)
	}
	if got := strings.Count(lines[0], "\x1b]8;;https:"); got != 1 {
		t.Fatalf("open count = %d", got)
	}
	if got := strings.Count(lines[0], "\x1b]8;;\x1b\\"); got != 1 {
		t.Fatalf("close count = %d", got)
	}
}

func stripOsc8ForTest(line string) string {
	runes := []rune(line)
	var out []rune
	i := 0
	for i < len(runes) {
		if code, n := extractAnsiCodeAt(runes, i); n > 0 {
			if strings.HasPrefix(code, "\x1b]8;;") {
				i += n
				continue
			}
			i += n
			continue
		}
		out = append(out, runes[i])
		i++
	}
	return string(out)
}

func TestTabWidthSliceHelpers(t *testing.T) {
	text := "out 192M\t.pi/skill-tests/results-ha"
	slice := SliceWithWidth(text, 0, 10, true)
	if slice.text != "out 192M" || slice.width != 8 {
		t.Fatalf("slice = %+v", slice)
	}
	if VisibleWidth(slice.text) != slice.width {
		t.Fatalf("width mismatch: %d vs %d", VisibleWidth(slice.text), slice.width)
	}

	segments := ExtractSegments(text, 10, 13, 10, true)
	if segments.Before != "out 192M" || segments.BeforeWidth != 8 {
		t.Fatalf("segments = %+v", segments)
	}
	if VisibleWidth(segments.Before) != segments.BeforeWidth {
		t.Fatalf("before width mismatch")
	}

	tabFits := ExtractSegments(text, 11, 13, 10, true)
	if tabFits.Before != "out 192M\t" || tabFits.BeforeWidth != 11 {
		t.Fatalf("tabFits = %+v", tabFits)
	}
}

func TestNormalizeTerminalOutputKeepsControlSequenceTabs(t *testing.T) {
	controlSequences := []string{
		"\x1b]8;;https://example.test/a\tb\x07",
		"\x1b]0;window\ttitle\x1b\\",
		"\x1b_payload\tdata\x1b\\",
	}
	for _, cs := range controlSequences {
		want := cs + "label   text"
		if got := NormalizeTerminalOutput(cs + "label\ttext"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}
