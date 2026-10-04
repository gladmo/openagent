package codingagent

// Port of the startup header built in
// modes/interactive/interactive-mode.ts: the app/version logo, the compact
// or expanded keybinding hints, and the onboarding lines. The hint lines
// come from the registered app actions (interactive_keys.go), so the header
// only advertises keys that actually work.

import (
	"fmt"
)

// StartableHeaderText returns the collapsed and expanded header content for
// the given hint lines.
func StartupHeaderText(version string, compactHints string, expandedHints string) (collapsed string, expanded string) {
	logo := fmt.Sprintf("%s v%s", AppName(), version)
	onboarding := "Pi can explain its own features and look up its docs. Ask it how to use or extend Pi."
	compactOnboarding := "Press ctrl+o to show full startup help and loaded resources."

	collapsed = logo + "\n" + compactHints + "\n" + compactOnboarding + "\n\n" + onboarding
	expanded = logo + "\n" + expandedHints + "\n\n" + onboarding
	return collapsed, expanded
}

// NewStartupHeader builds the built-in startup header; when the startup is
// silenced it returns an empty component, like the reference's Text("").
func NewStartupHeader(version string, expanded bool, silenced bool, compactHints string, expandedHints string) *ExpandableText {
	if silenced {
		return NewExpandableText(func() string { return "" }, func() string { return "" }, false, 0, 0)
	}
	collapsed, expandedText := StartupHeaderText(version, compactHints, expandedHints)
	return NewExpandableText(func() string { return collapsed }, func() string { return expandedText }, expanded, 1, 0)
}
