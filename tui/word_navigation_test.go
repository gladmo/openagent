package tui

import "testing"

// Ported from pi/packages/tui/test/word-navigation.test.ts. The "CJK mixed"
// cases assert the per-character CJK segmentation of the default (non-ICU-
// dictionary) rules; the reference's ICU dictionary merges are a documented
// deviation (word_segmenter.go).

func TestFindWordBackward(t *testing.T) {
	cases := []struct {
		text   string
		cursor int
		want   int
	}{
		{"hello world", 11, 6},
		{"hello world", 6, 0},
		{"foo.bar", 7, 4},
		{"foo.bar", 4, 3},
		{"foo.bar", 3, 0},
		{"foo:bar", 7, 4},
		{"foo:bar", 4, 3},
		{"foo:bar", 3, 0},
		{"path/to/file", 12, 8},
		{"path/to/file", 8, 7},
		// "/to" is one word-like segment with "/" as punctuation boundary
		{"path/to/file", 7, 5},
		{"path/to/file", 5, 4},
		{"path/to/file", 4, 0},
		// CJK chars each segment individually under the default rules.
		{"你好世界 test", 9, 5},
		{"你好世界 test", 5, 3},
		{"你好世界 test", 4, 3},
		{"你好世界 test", 3, 2},
		{"你好世界 test", 2, 1},
		{"你好世界 test", 1, 0},
		{"  hello  ", 9, 2},
		{"  hello  ", 2, 0},
		{"foo...bar", 9, 6},
		{"foo...bar", 6, 3},
		{"foo...bar", 3, 0},
		{"hello", 0, 0},
	}
	for _, tc := range cases {
		if got := FindWordBackward(tc.text, tc.cursor, nil); got != tc.want {
			t.Errorf("FindWordBackward(%q, %d) = %d, want %d", tc.text, tc.cursor, got, tc.want)
		}
	}
}

func TestFindWordForward(t *testing.T) {
	cases := []struct {
		text   string
		cursor int
		want   int
	}{
		{"hello world", 0, 5},
		{"hello world", 5, 11},
		{"foo.bar", 0, 3},
		{"foo.bar", 3, 4},
		{"foo.bar", 4, 7},
		{"foo:bar", 0, 3},
		{"foo:bar", 3, 4},
		{"foo:bar", 4, 7},
		{"path/to/file", 0, 4},
		{"path/to/file", 4, 5},
		{"path/to/file", 5, 7},
		{"path/to/file", 7, 8},
		{"path/to/file", 8, 12},
		{"  hello  ", 0, 7},
		{"  hello  ", 7, 9},
		{"foo...bar", 0, 3},
		{"foo...bar", 3, 6},
		{"foo...bar", 6, 9},
		{"hello", 5, 5},
	}
	for _, tc := range cases {
		if got := FindWordForward(tc.text, tc.cursor, nil); got != tc.want {
			t.Errorf("FindWordForward(%q, %d) = %d, want %d", tc.text, tc.cursor, got, tc.want)
		}
	}
}

func TestFindWordForwardCjkWalksToEnd(t *testing.T) {
	text := "你好世界 test"
	pos := 0
	for pos < len([]rune(text)) {
		next := FindWordForward(text, pos, nil)
		if next == pos {
			break
		}
		pos = next
	}
	if pos != len([]rune(text)) {
		t.Fatalf("walk ended at %d, want %d", pos, len([]rune(text)))
	}
}

func TestWordNavigationAtomicSegments(t *testing.T) {
	marker := "[paste #1 +5 lines]"
	text := "hello " + marker + " world"

	segmentMap := map[string][]WordSegment{
		text: {
			{Segment: "hello", IsWordLike: true},
			{Segment: " ", IsWordLike: false},
			{Segment: marker, IsWordLike: true},
			{Segment: " ", IsWordLike: false},
			{Segment: "world", IsWordLike: true},
		},
		text[:26]: { // "hello [paste #1 +5 lines] "
			{Segment: "hello", IsWordLike: true},
			{Segment: " ", IsWordLike: false},
			{Segment: marker, IsWordLike: true},
			{Segment: " ", IsWordLike: false},
		},
		text[6:]: { // "[paste #1 +5 lines] world"
			{Segment: marker, IsWordLike: true},
			{Segment: " ", IsWordLike: false},
			{Segment: "world", IsWordLike: true},
		},
	}

	opts := &WordNavigationOptions{
		Segment: func(input string) []WordSegment {
			return segmentMap[input]
		},
		IsAtomicSegment: func(s string) bool { return s == marker },
	}

	if got := FindWordBackward(text, len([]rune(text)), opts); got != 26 {
		t.Fatalf("backward from end = %d, want 26", got)
	}
	if got := FindWordBackward(text, 26, opts); got != 6 {
		t.Fatalf("backward from 26 = %d, want 6", got)
	}
	if got := FindWordForward(text, 6, opts); got != 6+len(marker) {
		t.Fatalf("forward from 6 = %d, want %d", got, 6+len(marker))
	}
}

func TestKillRingBehavior(t *testing.T) {
	var ring KillRing

	ring.Push("first", KillRingPushOptions{})
	ring.Push("second", KillRingPushOptions{})
	if got, ok := ring.Peek(); !ok || got != "second" {
		t.Fatalf("peek = %q ok=%v", got, ok)
	}

	ring.Push("a", KillRingPushOptions{Accumulate: true, Prepend: false})
	if got, _ := ring.Peek(); got != "seconda" {
		t.Fatalf("append accumulate = %q", got)
	}
	ring.Push("b", KillRingPushOptions{Accumulate: true, Prepend: true})
	if got, _ := ring.Peek(); got != "bseconda" {
		t.Fatalf("prepend accumulate = %q", got)
	}
	ring.Push("", KillRingPushOptions{})
	if ring.Len() != 2 {
		t.Fatalf("empty push changed ring: %d", ring.Len())
	}

	ring.Rotate()
	if got, _ := ring.Peek(); got != "first" {
		t.Fatalf("rotate = %q", got)
	}
	ring.Rotate()
	if got, _ := ring.Peek(); got != "bseconda" {
		t.Fatalf("rotate twice = %q", got)
	}
}

func TestUndoStackBehavior(t *testing.T) {
	cloneStrings := func(s []string) []string { return append([]string(nil), s...) }
	stack := NewUndoStack(cloneStrings)

	base := []string{"a"}
	stack.Push(base)
	base[0] = "mutated"
	popped, ok := stack.Pop()
	if !ok || len(popped) != 1 || popped[0] != "a" {
		t.Fatalf("clone-on-push broken: %v", popped)
	}
	if _, ok := stack.Pop(); ok {
		t.Fatal("pop on empty returned a value")
	}

	stack.Push([]string{"x"})
	stack.Push([]string{"y"})
	stack.Clear()
	if stack.Len() != 0 {
		t.Fatalf("clear broken: %d", stack.Len())
	}
}
