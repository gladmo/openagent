package codingagent

// Tests for chat_viewport.go — the ported fullscreen transcript and fixed
// input-dock layout (modes/interactive/chat-viewport.ts).

import (
	"testing"

	"github.com/gladmo/openagent/tui"
)

type fixedLinesComponent struct{ lines []string }

func (c *fixedLinesComponent) Render(width int) []string { return c.lines }
func (c *fixedLinesComponent) Invalidate()               {}

func TestCreateChatViewportLayout(t *testing.T) {
	document := &fixedLinesComponent{lines: []string{"chat"}}
	status := &fixedLinesComponent{lines: []string{"status"}}
	editor := &fixedLinesComponent{lines: []string{"editor"}}
	footer := &fixedLinesComponent{lines: []string{"footer"}}

	viewport := CreateChatViewport(ChatViewportOptions{
		Document: document,
		Status:   status,
		Editor:   editor,
		Footer:   footer,
	})

	if viewport.Transcript == nil {
		t.Fatal("no transcript scroll view")
	}
	if !viewport.Transcript.Primary() {
		t.Fatal("transcript must be the primary scroll view")
	}
	if !viewport.Transcript.IsFollowingEnd() {
		t.Fatal("transcript must follow the end")
	}

	root, ok := viewport.Root.(*tui.VStack)
	if !ok {
		t.Fatalf("root = %T, want VStack", viewport.Root)
	}
	if len(root.Entries) != 2 {
		t.Fatalf("root entries = %d, want transcript + dock", len(root.Entries))
	}
	if root.Entries[0].Component != tui.Component(viewport.Transcript) {
		t.Fatal("root must lead with the transcript")
	}
	transcriptEntry := root.Entries[0]
	if transcriptEntry.Grow == nil || *transcriptEntry.Grow != 1 {
		t.Fatalf("transcript grow = %v, want 1", transcriptEntry.Grow)
	}
	if transcriptEntry.MinSize == nil || *transcriptEntry.MinSize != 1 {
		t.Fatalf("transcript minSize = %v, want 1", transcriptEntry.MinSize)
	}
	dockEntry := root.Entries[1]
	if dockEntry.Basis == nil || !dockEntry.Basis.Auto {
		t.Fatalf("dock basis = %+v, want auto", dockEntry.Basis)
	}
	if dockEntry.Grow == nil || *dockEntry.Grow != 0 {
		t.Fatalf("dock grow = %v, want 0", dockEntry.Grow)
	}

	dock, ok := dockEntry.Component.(*tui.VStack)
	if !ok {
		t.Fatalf("dock = %T, want VStack", dockEntry.Component)
	}
	want := []tui.Component{status, editor, footer}
	if len(dock.Entries) != len(want) {
		t.Fatalf("dock entries = %d, want %d", len(dock.Entries), len(want))
	}
	for i, component := range want {
		if dock.Entries[i].Component != component {
			t.Errorf("dock entry %d = %T, want %T", i, dock.Entries[i].Component, component)
		}
	}
	// The editor keeps a three-line minimum, like the reference dock.
	if dock.Entries[1].MinSize == nil || *dock.Entries[1].MinSize != 3 {
		t.Fatalf("editor minSize = %v, want 3", dock.Entries[1].MinSize)
	}
}

func TestCreateChatViewportOptionalSlots(t *testing.T) {
	// Pending messages and widget slots are absent in this port; the dock
	// still holds status, editor, and footer in order.
	status := &fixedLinesComponent{lines: []string{"status"}}
	editor := &fixedLinesComponent{lines: []string{"editor"}}
	footer := &fixedLinesComponent{lines: []string{"footer"}}

	viewport := CreateChatViewport(ChatViewportOptions{
		Document: &fixedLinesComponent{lines: []string{"chat"}},
		Status:   status,
		Editor:   editor,
		Footer:   footer,
	})
	dock := viewport.Root.(*tui.VStack).Entries[1].Component.(*tui.VStack)
	if len(dock.Entries) != 3 {
		t.Fatalf("dock entries = %d, want 3", len(dock.Entries))
	}
	if dock.Entries[0].Component != tui.Component(status) ||
		dock.Entries[2].Component != tui.Component(footer) {
		t.Fatal("dock order must be status, editor, footer")
	}

	// Empty scrollbar option defaults to the reference's "auto".
	if viewport.Transcript.Primary() != true {
		t.Fatal("transcript primary flag lost")
	}
}
