package codingagent

// Port of modes/interactive/components/first-time-setup.ts: the
// first-run dialog with the theme step (system-appearance detection +
// live preview) and the analytics opt-in step.

import (
	"fmt"
	"strings"

	"github.com/gladmo/openagent/tui"
)

// FirstTimeSetupResult is the dialog outcome.
type FirstTimeSetupResult struct {
	Theme          string // "dark" | "light"
	ShareAnalytics bool
}

// FirstTimeSetupOptions configure the dialog.
type FirstTimeSetupOptions struct {
	DetectedTheme  string
	OnThemePreview func(themeName string)
	OnSubmit       func(result FirstTimeSetupResult)
	OnCancel       func()
}

type firstTimeSetupOption struct {
	value string
	label string
}

var setupThemeOptions = []firstTimeSetupOption{
	{value: "dark", label: "Dark"},
	{value: "light", label: "Light"},
}

var setupAnalyticsLabels = []struct {
	value bool
	label string
}{
	{value: true, label: "Share anonymous usage data"},
	{value: false, label: "Don't share"},
}

var setupLogoLines = []string{"██████", "██  ██", "████  ██", "██    ██"}

// FirstTimeSetupComponent renders the first-run dialog.
type FirstTimeSetupComponent struct {
	*tui.VStack

	step           string // "theme" | "analytics"
	themeIndex     int
	analyticsIndex int
	options        FirstTimeSetupOptions
}

// NewFirstTimeSetupComponent builds the dialog (theme step selected from
// the detected appearance).
func NewFirstTimeSetupComponent(options FirstTimeSetupOptions) *FirstTimeSetupComponent {
	component := &FirstTimeSetupComponent{
		VStack:  tui.NewVStack(0),
		step:    "theme",
		options: options,
	}
	for i, option := range setupThemeOptions {
		if option.value == options.DetectedTheme {
			component.themeIndex = i
			break
		}
	}
	component.update()
	return component
}

// Step reports the current step (tests).
func (c *FirstTimeSetupComponent) Step() string { return c.step }

// ThemeIndex reports the highlighted theme (tests).
func (c *FirstTimeSetupComponent) ThemeIndex() int { return c.themeIndex }

// AnalyticsIndex reports the highlighted analytics choice (tests).
func (c *FirstTimeSetupComponent) AnalyticsIndex() int { return c.analyticsIndex }

// update rebuilds the whole dialog so theme previews recolor every line.
func (c *FirstTimeSetupComponent) update() {
	c.VStack = tui.NewVStack(0)

	c.VStack.AddChild(tui.NewText(strings.Join(setupLogoLines, "\n"), 1, 0, nil), nil)
	c.VStack.AddChild(tui.NewSpacer(1), nil)
	c.VStack.AddChild(tui.NewText(fmt.Sprintf("Welcome to %s, the minimal coding agent.", AppName()), 1, 0, nil), nil)
	c.VStack.AddChild(tui.NewSpacer(1), nil)

	if c.step == "theme" {
		c.VStack.AddChild(tui.NewText("Pick a theme.", 1, 0, nil), nil)
		c.VStack.AddChild(tui.NewText("Detected system appearance: "+c.options.DetectedTheme, 1, 0, nil), nil)
		c.VStack.AddChild(tui.NewSpacer(1), nil)
		labels := make([]string, len(setupThemeOptions))
		for i, option := range setupThemeOptions {
			labels[i] = option.label
		}
		c.addOptionList(labels, c.themeIndex)
	} else {
		c.VStack.AddChild(tui.NewText("Opt-in to anonymous usage data sharing?", 1, 0, nil), nil)
		explanation := strings.Join([]string{
			"Opting in stores a tracking identifier in settings.json and enables anonymous",
			"usage analytics. This helps us to better debug, reproduce, and resolve issues",
			"and bugs within " + AppName() + ". You can observe what is shared using /privacy and make",
			"changes anytime in settings.json.",
		}, "\n")
		c.VStack.AddChild(tui.NewText(explanation, 1, 0, nil), nil)
		c.VStack.AddChild(tui.NewSpacer(1), nil)
		labels := make([]string, len(setupAnalyticsLabels))
		for i, option := range setupAnalyticsLabels {
			labels[i] = option.label
		}
		c.addOptionList(labels, c.analyticsIndex)
	}

	c.VStack.AddChild(tui.NewSpacer(1), nil)
	action := "finish"
	if c.step == "theme" {
		action = "continue"
	}
	c.VStack.AddChild(tui.NewText("↑↓ navigate  enter to "+action+"  esc skip setup", 1, 0, nil), nil)
	c.VStack.AddChild(tui.NewSpacer(1), nil)
}

// addOptionList renders one option row per label with the cursor marker.
func (c *FirstTimeSetupComponent) addOptionList(labels []string, selectedIndex int) {
	for i, label := range labels {
		prefix := "  "
		if i == selectedIndex {
			prefix = "→ "
		}
		c.VStack.AddChild(tui.NewText(prefix+label, 1, 0, nil), nil)
	}
}

// moveSelection clamps the cursor and previews theme changes.
func (c *FirstTimeSetupComponent) moveSelection(delta int) {
	if c.step == "theme" {
		next := c.themeIndex + delta
		if next < 0 {
			next = 0
		}
		if next > len(setupThemeOptions)-1 {
			next = len(setupThemeOptions) - 1
		}
		if next != c.themeIndex {
			c.themeIndex = next
			if c.options.OnThemePreview != nil {
				c.options.OnThemePreview(setupThemeOptions[c.themeIndex].value)
			}
		}
	} else {
		next := c.analyticsIndex + delta
		if next < 0 {
			next = 0
		}
		if next > len(setupAnalyticsLabels)-1 {
			next = len(setupAnalyticsLabels) - 1
		}
		c.analyticsIndex = next
	}
	c.update()
}

// HandleInput drives navigation and step transitions.
func (c *FirstTimeSetupComponent) HandleInput(key string) {
	switch key {
	case "up", "k":
		c.moveSelection(-1)
	case "down", "j":
		c.moveSelection(1)
	case "enter", "\n":
		if c.step == "theme" {
			c.step = "analytics"
			c.update()
			return
		}
		if c.options.OnSubmit != nil {
			c.options.OnSubmit(FirstTimeSetupResult{
				Theme:          setupThemeOptions[c.themeIndex].value,
				ShareAnalytics: setupAnalyticsLabels[c.analyticsIndex].value,
			})
		}
	case "escape", "ctrl+c":
		if c.options.OnCancel != nil {
			c.options.OnCancel()
		}
	}
}
