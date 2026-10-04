package tui

import (
	"reflect"
	"testing"
)

// Ported from the search cases of pi/packages/tui/test/tui-alt-screen.test.ts
// (the overlay component render tests land with the Input component batch).

func TestSearchNormalizedTextAcrossRows(t *testing.T) {
	matches := FindAltScreenSearchMatches([]string{"alpha QUICK", "brown fox"}, "quick brown")
	want := []AltScreenSearchMatch{{
		Segments: []AltScreenSearchSegment{
			{Row: 0, StartCol: 6, EndCol: 11},
			{Row: 1, StartCol: 0, EndCol: 5},
		},
	}}
	if !reflect.DeepEqual(matches, want) {
		t.Fatalf("matches = %+v", matches)
	}
}

func TestSearchMapsAsciiAndUnicodeToColumns(t *testing.T) {
	matches := FindAltScreenSearchMatches([]string{"\x1b[31mfoo  bar\x1b[0m", "A界🙂éZ"}, "oo   bar\nA界🙂é")
	want := []AltScreenSearchMatch{{
		Segments: []AltScreenSearchSegment{
			{Row: 0, StartCol: 1, EndCol: 3},
			{Row: 0, StartCol: 5, EndCol: 8},
			{Row: 1, StartCol: 0, EndCol: 6},
		},
	}}
	if !reflect.DeepEqual(matches, want) {
		t.Fatalf("matches = %+v", matches)
	}
}

func TestSearchIndexReusesMatches(t *testing.T) {
	var index AltScreenSearchIndex
	initial := index.Search([]string{"alpha needle", "omega"}, "needle")
	if !initial.Changed || len(initial.Matches) != 1 {
		t.Fatalf("initial = %+v", initial)
	}

	cached := index.Search([]string{"alpha needle", "omega"}, "needle")
	if cached.Changed {
		t.Fatal("unchanged input reported changed")
	}
	if !reflect.DeepEqual(cached.Matches, initial.Matches) {
		t.Fatalf("cached matches differ: %+v", cached.Matches)
	}

	changedQuery := index.Search([]string{"alpha needle", "omega"}, "omega")
	if !changedQuery.Changed {
		t.Fatal("query change not detected")
	}
	wantSegments := []AltScreenSearchSegment{{Row: 1, StartCol: 0, EndCol: 5}}
	if !reflect.DeepEqual(changedQuery.Matches[0].Segments, wantSegments) {
		t.Fatalf("segments = %+v", changedQuery.Matches[0].Segments)
	}

	changedLines := index.Search([]string{"alpha needle", "no match"}, "omega")
	if !changedLines.Changed {
		t.Fatal("line change not detected")
	}
	if len(changedLines.Matches) != 0 {
		t.Fatalf("matches = %+v", changedLines.Matches)
	}
}

func TestAltScreenFlashContainer(t *testing.T) {
	rendered := make(chan int, 4)
	container := NewAltScreenFlashContainer(func() { rendered <- 1 })

	container.Flash("saved", 1000)
	lines := container.Render(20)
	if len(lines) != 1 {
		t.Fatalf("lines = %q", lines)
	}
	if lines[0] != "\x1b[7m saved \x1b[27m" {
		t.Fatalf("line = %q", lines[0])
	}

	// Truncation to width with empty ellipsis.
	container.Flash("a much longer flash message here", 1000)
	all := container.Render(10)
	if VisibleWidth(all[1]) > 10 {
		t.Fatalf("flash not truncated: %q", all[1])
	}

	container.Dispose()
	if got := container.Render(20); len(got) != 0 {
		t.Fatalf("after dispose = %q", got)
	}
}
