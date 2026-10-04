package tui

import (
	"strings"
	"testing"
)

// Ported from pi/packages/tui/test/input.test.ts.

func newTestInput() *Input { return NewInput(InputOptions{}) }

func inputLine(input *Input, width int) string {
	lines := input.Render(width)
	return lines[0]
}

func TestInputSubmitsBackslash(t *testing.T) {
	input := newTestInput()
	submitted := ""
	input.OnSubmit = func(value string) { submitted = value }

	for _, ch := range []string{"h", "e", "l", "l", "o", "\\"} {
		input.HandleInput(ch)
	}
	input.HandleInput("\r")

	if submitted != "hello\\" {
		t.Fatalf("submitted = %q", submitted)
	}
}

func TestInputInsertsBackslash(t *testing.T) {
	input := newTestInput()
	input.HandleInput("\\")
	input.HandleInput("x")
	if got := input.GetValue(); got != "\\x" {
		t.Fatalf("value = %q", got)
	}
}

func TestInputPlaceholder(t *testing.T) {
	emptyPrompt := ""
	input := NewInput(InputOptions{
		Prompt:           &emptyPrompt,
		Placeholder:      "Find transcript",
		PlaceholderStyle: func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
	})
	input.SetFocused(true)

	empty := inputLine(input, 20)
	if !strings.Contains(empty, "\x1b[2m") {
		t.Fatalf("missing dim style: %q", empty)
	}
	if strings.TrimRight(StripTerminalSequences(empty), " ") != "Find transcript" {
		t.Fatalf("placeholder = %q", empty)
	}

	input.HandleInput("n")
	populated := inputLine(input, 20)
	if strings.TrimRight(StripTerminalSequences(populated), " ") != "n" {
		t.Fatalf("populated = %q", populated)
	}
}

func TestInputWideTextNoOverflow(t *testing.T) {
	width := 93
	cases := []string{
		"가나다라마바사아자차카타파하 한글 텍스트가 터미널 너비를 초과하면 크래시가 발생합니다 이것은 재현용 테스트입니다",
		"これはテスト文章です。日本語のテキストが正しく表示されるかどうかを確認するためのサンプルテキストです。あいうえお",
		"这是一段测试文本，用于验证中文字符在终端中的显示宽度是否被正确计算，如果不正确就会导致用户界面崩溃的问题",
		"ＡＢＣＤＥＦＧＨＩＪＫＬＭＮＯＰＱＲＳＴＵＶＷＸＹＺ０１２３４５６７８９ａｂｃｄｅｆｇｈｉｊｋｌｍ",
	}
	moves := []struct {
		label string
		move  func(input *Input)
	}{
		{"start", func(input *Input) {}},
		{"middle", func(input *Input) {
			for i := 0; i < 10; i++ {
				input.HandleInput("\x1b[C")
			}
		}},
		{"end", func(input *Input) { input.HandleInput("\x05") }},
	}

	for _, text := range cases {
		for _, tc := range moves {
			input := newTestInput()
			input.SetValue(text)
			input.SetFocused(true)
			tc.move(input)

			line := inputLine(input, width)
			if w := VisibleWidth(line); w > width {
				t.Errorf("%s/%s overflowed: %d > %d", text[:8], tc.label, w, width)
			}
		}
	}
}

func TestInputScrollKeepsCursorVisible(t *testing.T) {
	input := newTestInput()
	width := 20
	input.SetValue("가나다라마바사아자차카타파하")
	input.SetFocused(true)
	input.HandleInput("\x01")
	for i := 0; i < 5; i++ {
		input.HandleInput("\x1b[C")
	}

	line := inputLine(input, width)
	if w := VisibleWidth(line); w > width {
		t.Fatalf("overflow: %d (%q)", w, line)
	}
}

func TestInputKillRingBasics(t *testing.T) {
	// Ctrl+W deletes; Ctrl+Y yanks.
	input := newTestInput()
	input.SetValue("foo bar baz")
	input.HandleInput("\x05") // Ctrl+E
	input.HandleInput("\x17") // Ctrl+W deletes "baz"
	if got := input.GetValue(); got != "foo bar " {
		t.Fatalf("after W: %q", got)
	}
	input.HandleInput("\x01") // Ctrl+A
	input.HandleInput("\x19") // Ctrl+Y
	if got := input.GetValue(); got != "bazfoo bar " {
		t.Fatalf("after Y: %q", got)
	}
}

func TestInputCtrlWPunctuation(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"foo.bar", "foo."},
		{"foo:bar", "foo:"},
	} {
		input := newTestInput()
		input.SetValue(tc.value)
		input.HandleInput("\x05")
		input.HandleInput("\x17")
		if got := input.GetValue(); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestInputCtrlWUnicodeBoundaries(t *testing.T) {
	// "你好世界。你好，世界" word-segments per-char under the default
	// (non-ICU-dictionary) rules, so Ctrl+W deletes one CJK char at a time;
	// the punctuation run deletes as one unit. Documented deviation from
	// the reference's ICU dictionary merges (see word_segmenter.go).
	input := newTestInput()
	input.SetValue("你好世界。你好，世界")
	input.HandleInput("\x05")
	want := []string{
		"你好世界。你好，世", // 界
		"你好世界。你好，",  // 世
		"你好世界。你好",   // ，
		"你好世界。你",    // 好
		"你好世界。",     // 你
		"你好世界",      // 。
		"你好世",       // 界
		"你好",        // 世
		"你",         // 好
		"",          // 你
	}
	for _, w := range want {
		input.HandleInput("\x17")
		if got := input.GetValue(); got != w {
			t.Fatalf("got %q, want %q", got, w)
		}
	}
}

func TestInputCtrlUAndCtrlK(t *testing.T) {
	input := newTestInput()
	input.SetValue("hello world")
	input.HandleInput("\x01")
	for i := 0; i < 6; i++ {
		input.HandleInput("\x1b[C")
	}
	input.HandleInput("\x15") // Ctrl+U deletes "hello "
	if got := input.GetValue(); got != "world" {
		t.Fatalf("after U: %q", got)
	}
	input.HandleInput("\x19")
	if got := input.GetValue(); got != "hello world" {
		t.Fatalf("after yank: %q", got)
	}

	input2 := newTestInput()
	input2.SetValue("hello world")
	input2.HandleInput("\x01")
	input2.HandleInput("\x0b") // Ctrl+K
	if got := input2.GetValue(); got != "" {
		t.Fatalf("after K: %q", got)
	}
	input2.HandleInput("\x19")
	if got := input2.GetValue(); got != "hello world" {
		t.Fatalf("after yank: %q", got)
	}
}

func TestInputYankEmptyRing(t *testing.T) {
	input := newTestInput()
	input.SetValue("test")
	input.HandleInput("\x05")
	input.HandleInput("\x19")
	if got := input.GetValue(); got != "test" {
		t.Fatalf("got %q", got)
	}
}

func TestInputYankPopCycles(t *testing.T) {
	input := newTestInput()
	for _, word := range []string{"first", "second", "third"} {
		input.SetValue(word)
		input.HandleInput("\x05")
		input.HandleInput("\x17")
	}
	if got := input.GetValue(); got != "" {
		t.Fatalf("after kills: %q", got)
	}

	input.HandleInput("\x19")
	if got := input.GetValue(); got != "third" {
		t.Fatalf("yank: %q", got)
	}
	input.HandleInput("\x1by")
	if got := input.GetValue(); got != "second" {
		t.Fatalf("pop 1: %q", got)
	}
	input.HandleInput("\x1by")
	if got := input.GetValue(); got != "first" {
		t.Fatalf("pop 2: %q", got)
	}
	input.HandleInput("\x1by")
	if got := input.GetValue(); got != "third" {
		t.Fatalf("pop 3 wraps: %q", got)
	}
}

func TestInputYankPopGuards(t *testing.T) {
	// Not preceded by yank.
	input := newTestInput()
	input.SetValue("test")
	input.HandleInput("\x05")
	input.HandleInput("\x17")
	input.SetValue("other")
	input.HandleInput("\x05")
	input.HandleInput("x")
	if got := input.GetValue(); got != "otherx" {
		t.Fatalf("typed: %q", got)
	}
	input.HandleInput("\x1by")
	if got := input.GetValue(); got != "otherx" {
		t.Fatalf("pop without yank: %q", got)
	}

	// Single-entry ring.
	input2 := newTestInput()
	input2.SetValue("only")
	input2.HandleInput("\x05")
	input2.HandleInput("\x17")
	input2.HandleInput("\x19")
	if got := input2.GetValue(); got != "only" {
		t.Fatalf("yank: %q", got)
	}
	input2.HandleInput("\x1by")
	if got := input2.GetValue(); got != "only" {
		t.Fatalf("pop single: %q", got)
	}
}

func TestInputKillAccumulation(t *testing.T) {
	input := newTestInput()
	input.SetValue("one two three")
	input.HandleInput("\x05")
	input.HandleInput("\x17")
	input.HandleInput("\x17")
	input.HandleInput("\x17")
	if got := input.GetValue(); got != "" {
		t.Fatalf("after kills: %q", got)
	}
	input.HandleInput("\x19")
	if got := input.GetValue(); got != "one two three" {
		t.Fatalf("accumulated yank: %q", got)
	}
}

func TestInputNonDeleteBreaksAccumulation(t *testing.T) {
	input := newTestInput()
	input.SetValue("foo bar baz")
	input.HandleInput("\x05")
	input.HandleInput("\x17")
	if got := input.GetValue(); got != "foo bar " {
		t.Fatalf("after W: %q", got)
	}
	input.HandleInput("x")
	if got := input.GetValue(); got != "foo bar x" {
		t.Fatalf("typed: %q", got)
	}
	input.HandleInput("\x17")
	if got := input.GetValue(); got != "foo bar " {
		t.Fatalf("second W: %q", got)
	}
	input.HandleInput("\x19")
	if got := input.GetValue(); got != "foo bar x" {
		t.Fatalf("yank most recent: %q", got)
	}
	input.HandleInput("\x1by")
	if got := input.GetValue(); got != "foo bar baz" {
		t.Fatalf("pop to baz: %q", got)
	}
}

func TestInputAltDDeletesForward(t *testing.T) {
	input := newTestInput()
	input.SetValue("foo bar baz")
	input.HandleInput("\x01")  // start
	input.HandleInput("\x1bd") // Alt+D deletes "foo"
	if got := input.GetValue(); got != " bar baz" {
		t.Fatalf("after Alt+D: %q", got)
	}
	input.HandleInput("\x19")
	if got := input.GetValue(); got != "foo bar baz" {
		t.Fatalf("yank: %q", got)
	}
}

func TestInputUndo(t *testing.T) {
	// Empty stack: no-op.
	input := newTestInput()
	input.SetValue("test")
	input.HandleInput("\x1f") // Ctrl+- (undo)
	if got := input.GetValue(); got != "test" {
		t.Fatalf("empty undo: %q", got)
	}

	// Coalescing: word chars one undo unit, spaces separately.
	input2 := newTestInput()
	input2.HandleInput("h")
	input2.HandleInput("i")
	input2.HandleInput(" ")
	input2.HandleInput("a")
	if got := input2.GetValue(); got != "hi a" {
		t.Fatalf("typed: %q", got)
	}
	// Space pushes its own group; "a" coalesces onto it, so one undo
	// removes " a" (TS: space always breaks the word group).
	input2.HandleInput("\x1f")
	if got := input2.GetValue(); got != "hi" {
		t.Fatalf("undo space group: %q", got)
	}
	input2.HandleInput("\x1f")
	if got := input2.GetValue(); got != "" {
		t.Fatalf("undo coalesced words: %q", got)
	}
}
