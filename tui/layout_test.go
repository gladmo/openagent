package tui

import (
	"strings"
	"testing"
	"time"
)

// Ported from pi/packages/tui/test/layout.test.ts.

func visibleLines(lines []string) []string {
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = strings.TrimRight(StripTerminalSequences(line), " ")
	}
	return result
}

func staticFuncComponent(lines []string) Component {
	return &linesComponent{lines: lines}
}

func entryBasis(value int) *StackBasis { return &StackBasis{Value: value} }

func autoBasis() *StackBasis { return &StackBasis{Auto: true} }

func intPtr(v int) *int { return &v }

func TestLayoutVStackGrowSpace(t *testing.T) {
	root := NewVStack(0)
	root.AddChild(NewText("top", 0, 0, nil), &StackEntryOptions{Basis: entryBasis(1), Shrink: intPtr(0)})
	root.AddChild(NewText("body", 0, 0, nil), &StackEntryOptions{Basis: entryBasis(0), Grow: intPtr(1)})

	frame := RenderLayoutFrame(root, 10, 4, func() {})
	if len(frame.Root.Children) != 2 {
		t.Fatalf("children = %d", len(frame.Root.Children))
	}
	if frame.Root.Children[0].Rect.Height != 1 || frame.Root.Children[1].Rect.Height != 3 {
		t.Fatalf("heights = [%d, %d]", frame.Root.Children[0].Rect.Height, frame.Root.Children[1].Rect.Height)
	}
	want := []string{"top", "body", "", ""}
	got := visibleLines(frame.Lines)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLayoutNoScrollRenderDuringStackMeasurement(t *testing.T) {
	renderCount := 0
	transcript := NewScrollView(staticFuncComponent(nil), ScrollViewOptions{})
	transcript.Child = &countingComponent{lines: []string{"one", "two", "three"}, count: &renderCount}
	transcript.Children = []Component{transcript.Child}
	root := NewVStack(0)
	root.AddChild(transcript, &StackEntryOptions{Basis: entryBasis(0), Grow: intPtr(1)})
	root.AddChild(NewText("dock", 0, 0, nil), &StackEntryOptions{Basis: autoBasis()})

	RenderLayoutFrame(root, 10, 3, func() {})
	if renderCount != 1 {
		t.Fatalf("renderCount = %d, want 1", renderCount)
	}
}

type countingComponent struct {
	lines []string
	count *int
}

func (c *countingComponent) Render(width int) []string {
	*c.count++
	return c.lines
}
func (c *countingComponent) Invalidate() {}

func TestLayoutPaintsClippedRowsFromLargeScrollContent(t *testing.T) {
	lineCount := 1_000_000_000
	lines := make([]string, lineCount)
	lines[lineCount-4] = "before"
	lines[lineCount-3] = "visible 1"
	lines[lineCount-2] = "visible 2"
	lines[lineCount-1] = "visible 3"
	transcript := NewScrollView(staticFuncComponent(lines), ScrollViewOptions{Follow: "end"})

	frame := RenderLayoutFrame(transcript, 10, 3, func() {})
	got := visibleLines(frame.Lines)
	want := []string{"visible 1", "visible 2", "visible 3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLayoutShrinksToMinimumSizes(t *testing.T) {
	root := NewVStack(0)
	root.AddChild(NewText("a1\na2\na3", 0, 0, nil), &StackEntryOptions{Shrink: intPtr(1), MinSize: intPtr(1)})
	root.AddChild(NewText("b1\nb2\nb3", 0, 0, nil), &StackEntryOptions{Shrink: intPtr(0)})

	frame := RenderLayoutFrame(root, 10, 4, func() {})
	if frame.Root.Children[0].Rect.Height != 1 || frame.Root.Children[1].Rect.Height != 3 {
		t.Fatalf("heights = [%d, %d]",
			frame.Root.Children[0].Rect.Height, frame.Root.Children[1].Rect.Height)
	}
	want := []string{"a1", "b1", "b2", "b3"}
	got := visibleLines(frame.Lines)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLayoutNestedMinimumSizes(t *testing.T) {
	dock := NewVStack(0)
	dock.AddChild(NewText("top1\ntop2\ntop3", 0, 0, nil), nil)
	dock.AddChild(NewText("selector", 0, 0, nil), &StackEntryOptions{MinSize: intPtr(3)})
	dock.AddChild(NewText("below", 0, 0, nil), nil)
	dock.AddChild(NewText("footer", 0, 0, nil), &StackEntryOptions{MinSize: intPtr(1)})

	root := NewVStack(0)
	root.AddChild(NewText("body", 0, 0, nil), &StackEntryOptions{Basis: entryBasis(0), Grow: intPtr(1), MinSize: intPtr(1)})
	root.AddChild(dock, &StackEntryOptions{Basis: autoBasis(), MinSize: intPtr(1)})

	frame := RenderLayoutFrame(root, 10, 9, func() {})
	want := []string{"body", "top1", "top2", "top3", "selector", "", "", "below", "footer"}
	got := visibleLines(frame.Lines)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLayoutOmitsGapsAroundInvisibleEntries(t *testing.T) {
	stack := NewVStack(1)
	stack.AddChild(NewText("one", 0, 0, nil), nil)
	stack.AddChild(NewText("hidden", 0, 0, nil), &StackEntryOptions{
		Visible: func(viewport LayoutViewport) bool { return false },
	})
	stack.AddChild(NewText("two", 0, 0, nil), nil)

	got := visibleLines(stack.Render(10))
	want := []string{"one", "", "two"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLayoutCropsKittyImagesAtBoundary(t *testing.T) {
	imageID := 124
	columns, rows, id := 2, 3, imageID
	moveCursor := false
	imageLine := EncodeKitty("AAAA", EncodeKittyOptions{Columns: &columns, Rows: &rows, ImageID: &id, MoveCursor: &moveCursor})
	RegisterKittyImageMetadata(KittyImageMetadata{ImageID: imageID, Columns: 2, Rows: 3, WidthPx: 100, HeightPx: 100})
	transcript := NewScrollView(staticFuncComponent([]string{"one", "two", imageLine, "", ""}), ScrollViewOptions{})

	root := NewVStack(0)
	root.AddChild(transcript, &StackEntryOptions{Basis: entryBasis(0), Grow: intPtr(1)})
	root.AddChild(NewText("dock", 0, 0, nil), nil)

	frame := RenderLayoutFrame(root, 20, 4, func() {})
	if !strings.Contains(frame.Lines[2], "y=0,h=34,r=1") {
		t.Fatalf("cropped image line = %q", frame.Lines[2])
	}
}

func TestLayoutHorizontalChildren(t *testing.T) {
	root := NewHStack(0)
	root.AddChild(NewText("left", 0, 0, nil), &StackEntryOptions{Basis: entryBasis(6), Shrink: intPtr(0)})
	root.AddChild(NewText("right", 0, 0, nil), &StackEntryOptions{Basis: entryBasis(6), Shrink: intPtr(0)})

	frame := RenderLayoutFrame(root, 12, 1, func() {})
	got := visibleLines(frame.Lines)
	if got[0] != "left  right" {
		t.Fatalf("line = %q", got[0])
	}
}

func TestLayoutZeroWidthChildrenNotPainted(t *testing.T) {
	root := NewHStack(0)
	root.AddChild(NewText("hidden", 0, 0, nil), &StackEntryOptions{Basis: entryBasis(0), Shrink: intPtr(0)})
	root.AddChild(NewText("shown", 0, 0, nil), &StackEntryOptions{Basis: entryBasis(0), Grow: intPtr(1)})

	frame := RenderLayoutFrame(root, 5, 1, func() {})
	got := visibleLines(frame.Lines)
	if got[0] != "shown" {
		t.Fatalf("line = %q", got[0])
	}
}

func TestLayoutFollowEndAndUnusedDelta(t *testing.T) {
	scrollView := NewScrollView(NewText("1\n2\n3\n4\n5\n6", 0, 0, nil), ScrollViewOptions{Follow: "end", Primary: true})
	RenderLayoutFrame(scrollView, 10, 3, func() {})
	if scrollView.ScrollTop() != 3 || !scrollView.IsFollowingEnd() {
		t.Fatalf("scrollTop = %d following = %v", scrollView.ScrollTop(), scrollView.IsFollowingEnd())
	}

	if got := scrollView.ScrollBy(-2); got != 0 {
		t.Fatalf("unused delta = %d", got)
	}
	if scrollView.ScrollTop() != 1 || scrollView.IsFollowingEnd() {
		t.Fatalf("after -2: scrollTop = %d following = %v", scrollView.ScrollTop(), scrollView.IsFollowingEnd())
	}
	if got := scrollView.ScrollBy(-3); got != -2 {
		t.Fatalf("unused delta = %d, want -2", got)
	}
	if scrollView.ScrollTop() != 0 {
		t.Fatalf("clamped scrollTop = %d", scrollView.ScrollTop())
	}
	if got := scrollView.ScrollBy(10); got != 7 {
		t.Fatalf("unused delta = %d, want 7", got)
	}
	if scrollView.ScrollTop() != 3 || !scrollView.IsFollowingEnd() {
		t.Fatal("did not return to follow-end")
	}
}

func TestLayoutScrollbarAlwaysReservesColumn(t *testing.T) {
	content := NewText("1\n2", 0, 0, nil)
	view := NewScrollView(content, ScrollViewOptions{Scrollbar: ScrollbarAlways})
	frame := RenderLayoutFrame(view, 6, 4, func() {})
	if len(frame.Root.Children) != 1 || frame.Root.Children[0].Rect.Width != 5 {
		t.Fatalf("content width = %+v", frame.Root.Children[0].Rect)
	}
	for _, line := range visibleLines(frame.Lines) {
		if !strings.HasSuffix(line, "┃") {
			t.Fatalf("line missing thumb: %q", line)
		}
	}
}

func TestLayoutScrollbarThumbHeights(t *testing.T) {
	thumbHeightFor := func(contentHeight int) int {
		lines := make([]string, contentHeight)
		for i := range lines {
			lines[i] = "x"
		}
		view := NewScrollView(NewText(strings.Join(lines, "\n"), 0, 0, nil), ScrollViewOptions{Scrollbar: ScrollbarAuto})
		RenderLayoutFrame(view, 6, 20, func() {})
		view.ScrollBy(1)
		frame := RenderLayoutFrame(view, 6, 20, func() {})
		count := 0
		for _, line := range frame.Lines {
			if strings.HasSuffix(StripTerminalSequences(line), "┃") {
				count++
			}
		}
		return count
	}
	cases := []struct {
		contentHeight int
		want          int
	}{
		{21, 19},
		{40, 10},
		{100, 4},
		{400, 2},
	}
	for _, tc := range cases {
		if got := thumbHeightFor(tc.contentHeight); got != tc.want {
			t.Errorf("thumbHeightFor(%d) = %d, want %d", tc.contentHeight, got, tc.want)
		}
	}
}

func TestLayoutTransientScrollbarHides(t *testing.T) {
	source := []string{"abcde1", "abcde2", "abcde3", "abcde4", "abcde5", "abcde6", "abcde7", "abcde8"}
	content := NewText(strings.Join(source, "\n"), 0, 0, nil)
	view := NewScrollView(content, ScrollViewOptions{
		Scrollbar:            ScrollbarAuto,
		ScrollbarHideDelayMs: 10,
	})
	render := func() []string { return RenderLayoutFrame(view, 6, 4, func() {}).Lines }
	visible := func(lines []string) []string {
		out := make([]string, len(lines))
		for i, line := range lines {
			out[i] = StripTerminalSequences(line)
		}
		return out
	}

	lines := render()
	for i := 0; i < 4; i++ {
		if visible(lines)[i] != source[i] {
			t.Fatalf("initial view = %q", visible(lines))
		}
	}

	view.ScrollBy(2)
	lines = render()
	want := []string{"abcde│", "abcde┃", "abcde┃", "abcde│"}
	for i := range want {
		if visible(lines)[i] != want[i] {
			t.Fatalf("scrolled view[%d] = %q, want %q", i, visible(lines)[i], want[i])
		}
	}

	view.SetScrollbarActive(false)
	time.Sleep(30 * time.Millisecond)
	lines = render()
	for i := 0; i < 4; i++ {
		if visible(lines)[i] != source[i+2] {
			t.Fatalf("hidden scrollbar view[%d] = %q", i, visible(lines)[i])
		}
	}
}

func TestLayoutFollowEndGrowsWithoutScrollbar(t *testing.T) {
	source := []string{"abcd界", "abcde2", "abcde3", "abcde4", "abcde5", "abcde6", "abcde7", "abcde8"}
	content := NewText(strings.Join(source, "\n"), 0, 0, nil)
	view := NewScrollView(content, ScrollViewOptions{Follow: "end", Scrollbar: ScrollbarAuto})
	RenderLayoutFrame(view, 6, 4, func() {})
	if view.ScrollTop() != 4 {
		t.Fatalf("scrollTop = %d", view.ScrollTop())
	}
	content.SetText(strings.Join(source, "\n") + "\nabcde9")
	frame := RenderLayoutFrame(view, 6, 4, func() {})
	if view.ScrollTop() != 5 {
		t.Fatalf("grown scrollTop = %d", view.ScrollTop())
	}
	for _, line := range frame.Lines {
		plain := StripTerminalSequences(line)
		if strings.Contains(plain, "│") || strings.Contains(plain, "┃") {
			t.Fatalf("scrollbar visible while following end: %q", plain)
		}
	}
}
