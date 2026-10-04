package codingagent

// Port of modes/interactive/components/branch-summary-message.ts and
// compaction-summary-message.ts: boxed summary messages with
// collapsed/expanded states sharing the custom-message background.

import (
	"fmt"

	"github.com/gladmo/openagent/tui"
)

// SummaryMessageComponent renders one boxed summary (branch or
// compaction) with collapse/expand.
type SummaryMessageComponent struct {
	*tui.Box

	expanded    bool
	label       string // "[branch]" | "[compaction]"
	summary     string
	subtitle    string // collapsed hint text
	tokensField string // compaction token count ("" for branch)
}

// NewBranchSummaryMessageComponent builds the branch variant.
func NewBranchSummaryMessageComponent(summary string) *SummaryMessageComponent {
	return newSummaryMessage("[branch]", summary, "Branch summary (")
}

// NewCompactionSummaryMessageComponent builds the compaction variant
// (token count in the collapsed hint and expanded header).
func NewCompactionSummaryMessageComponent(summary string, tokensBefore int) *SummaryMessageComponent {
	tokenStr := formatLocaleInt(tokensBefore)
	component := newSummaryMessage("[compaction]", summary,
		fmt.Sprintf("Compacted from %s tokens (", tokenStr))
	component.tokensField = tokenStr
	component.updateDisplay()
	return component
}

func newSummaryMessage(label, summary, subtitle string) *SummaryMessageComponent {
	component := &SummaryMessageComponent{
		Box:      tui.NewBox(1, 1, func(str string) string { return str }),
		label:    label,
		summary:  summary,
		subtitle: subtitle,
	}
	component.updateDisplay()
	return component
}

// SetExpanded toggles the state.
func (c *SummaryMessageComponent) SetExpanded(expanded bool) {
	if c.expanded != expanded {
		c.expanded = expanded
		c.updateDisplay()
	}
}

// Toggle flips the state (click seam).
func (c *SummaryMessageComponent) Toggle() { c.SetExpanded(!c.expanded) }

// Expanded reports the state (tests).
func (c *SummaryMessageComponent) Expanded() bool { return c.expanded }

func (c *SummaryMessageComponent) updateDisplay() {
	c.Box = tui.NewBox(1, 1, func(str string) string { return str })

	content := tui.NewVStack(0)
	content.AddChild(tui.NewText("\x1b[1m"+c.label+"\x1b[22m", 0, 0, nil), nil)
	content.AddChild(tui.NewSpacer(1), nil)

	if c.expanded {
		header := "**Branch Summary**\n\n"
		if c.label == "[compaction]" {
			header = fmt.Sprintf("**Compacted from %s tokens**\n\n", c.tokensField)
		}
		markdown := tui.NewMarkdown(header+c.summary, 0, 0, tui.MarkdownTheme{}, nil, nil)
		content.AddChild(markdown, nil)
	} else {
		hint := c.subtitle + KeyTextOf("app.tools.expand") + " to expand)"
		content.AddChild(tui.NewText(hint, 0, 0, nil), nil)
	}

	c.Box.AddChild(content)
}

// formatLocaleInt renders thousands-separated counts.
func formatLocaleInt(value int) string {
	if value < 0 {
		return "-" + formatLocaleInt(-value)
	}
	digits := fmt.Sprintf("%d", value)
	var groups []string
	for len(digits) > 3 {
		groups = append([]string{digits[len(digits)-3:]}, groups...)
		digits = digits[:len(digits)-3]
	}
	groups = append([]string{digits}, groups...)
	out := ""
	for i, group := range groups {
		if i > 0 {
			out += ","
		}
		out += group
	}
	return out
}
