package codingagent

// Port of the ExpandableText helper defined in
// modes/interactive/interactive-mode.ts: a text component that swaps
// between collapsed and expanded content.

import (
	"github.com/gladmo/openagent/tui"
)

// ExpandableText renders collapsed or expanded text, re-resolving the
// content from the getters on every toggle.
type ExpandableText struct {
	getCollapsed func() string
	getExpanded  func() string
	text         *tui.Text
}

// NewExpandableText builds the component; expanded selects the initial
// content.
func NewExpandableText(getCollapsed func() string, getExpanded func() string, expanded bool, paddingX, paddingY int) *ExpandableText {
	component := &ExpandableText{
		getCollapsed: getCollapsed,
		getExpanded:  getExpanded,
		text:         tui.NewText("", paddingX, paddingY, nil),
	}
	component.SetExpanded(expanded)
	return component
}

// SetExpanded swaps the rendered content.
func (c *ExpandableText) SetExpanded(expanded bool) {
	if expanded {
		c.text.SetText(c.getExpanded())
		return
	}
	c.text.SetText(c.getCollapsed())
}

// Render implements tui.Component.
func (c *ExpandableText) Render(width int) []string { return c.text.Render(width) }

// Invalidate implements tui.Component.
func (c *ExpandableText) Invalidate() { c.text.Invalidate() }
