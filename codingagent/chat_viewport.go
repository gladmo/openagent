package codingagent

// Port of modes/interactive/chat-viewport.ts: the shared fullscreen
// transcript and fixed input-dock layout.

import (
	"github.com/gladmo/openagent/tui"
)

// ChatViewportOptions configure the fullscreen chat layout. Document,
// Status, Editor, and Footer are required; the pending-message and widget
// slots stay optional (the port has no components for them yet).
type ChatViewportOptions struct {
	Document        tui.Component
	PendingMessages tui.Component
	Status          tui.Component
	Editor          tui.Component
	Footer          tui.Component
	WidgetsAbove    tui.Component
	WidgetsBelow    tui.Component

	Scrollbar           tui.ScrollViewScrollbar
	ScrollbarTrackStyle func(text string) string
	ScrollbarThumbStyle func(text string) string
}

// ChatViewport is the transcript scroll view plus the layout root that
// pins the dock to the bottom.
type ChatViewport struct {
	Root       tui.Component
	Transcript *tui.ScrollView
}

// CreateChatViewport builds the shared fullscreen transcript and fixed
// input-dock layout: the document scrolls in a follow-end primary scroll
// view, everything else stacks in a fixed dock below it.
func CreateChatViewport(options ChatViewportOptions) ChatViewport {
	scrollbar := options.Scrollbar
	if scrollbar == "" {
		// The reference defaults the fullscreen scrollbar to "auto".
		scrollbar = tui.ScrollbarAuto
	}
	transcript := tui.NewScrollView(options.Document, tui.ScrollViewOptions{
		Follow:              "end",
		Primary:             true,
		Overscroll:          "chain",
		Scrollbar:           scrollbar,
		ScrollbarTrackStyle: options.ScrollbarTrackStyle,
		ScrollbarThumbStyle: options.ScrollbarThumbStyle,
	})

	zero := 0
	one := 1
	three := 3

	dock := tui.NewVStack(0)
	addDockChild := func(component tui.Component, minSize int) {
		if component == nil {
			return
		}
		dock.AddChild(component, &tui.StackEntryOptions{
			Shrink:  &one,
			MinSize: &minSize,
		})
	}
	addDockChild(options.PendingMessages, zero)
	addDockChild(options.Status, zero)
	addDockChild(options.WidgetsAbove, zero)
	addDockChild(options.Editor, three)
	addDockChild(options.WidgetsBelow, zero)
	addDockChild(options.Footer, zero)

	root := tui.NewVStack(0)
	root.AddChild(transcript, &tui.StackEntryOptions{
		Basis:   &tui.StackBasis{Auto: false, Value: 0},
		Grow:    &one,
		Shrink:  &one,
		MinSize: &one,
	})
	root.AddChild(dock, &tui.StackEntryOptions{
		Basis:   &tui.StackBasis{Auto: true},
		Grow:    &zero,
		Shrink:  &one,
		MinSize: &one,
	})

	return ChatViewport{Root: root, Transcript: transcript}
}
