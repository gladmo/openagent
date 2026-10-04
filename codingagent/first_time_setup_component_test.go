package codingagent

// Tests for the first-time setup dialog (adapted from
// first-time-setup.test.ts).

import (
	"strings"
	"testing"
)

func TestFirstTimeSetupInitialStep(t *testing.T) {
	component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{DetectedTheme: "dark"})
	if component.Step() != "theme" {
		t.Fatalf("step = %q", component.Step())
	}
	if component.ThemeIndex() != 0 {
		t.Fatalf("theme index = %d", component.ThemeIndex())
	}

	lines := component.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Welcome to "+AppName()) {
		t.Fatalf("welcome = %q", joined)
	}
	if !strings.Contains(joined, "Pick a theme.") {
		t.Fatalf("prompt = %q", joined)
	}
	if !strings.Contains(joined, "Detected system appearance: dark") {
		t.Fatalf("detection = %q", joined)
	}
	if !strings.Contains(joined, "→ Dark") {
		t.Fatalf("cursor = %q", joined)
	}
	if !strings.Contains(joined, "esc skip setup") {
		t.Fatalf("hint = %q", joined)
	}
}

func TestFirstTimeSetupDetectedLight(t *testing.T) {
	component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{DetectedTheme: "light"})
	if component.ThemeIndex() != 1 {
		t.Fatalf("theme index = %d", component.ThemeIndex())
	}
	lines := component.Render(80)
	if !strings.Contains(strings.Join(lines, "\n"), "→ Light") {
		t.Fatalf("cursor = %v", lines)
	}
}

func TestFirstTimeSetupUnknownDetectedTheme(t *testing.T) {
	// An unknown detection falls back to the first option.
	component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{DetectedTheme: "solarized"})
	if component.ThemeIndex() != 0 {
		t.Fatalf("theme index = %d", component.ThemeIndex())
	}
}

func TestFirstTimeSetupThemePreview(t *testing.T) {
	previews := []string{}
	component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
		DetectedTheme:  "light",
		OnThemePreview: func(themeName string) { previews = append(previews, themeName) },
	})
	// Moving up from light selects dark and previews it.
	component.HandleInput("up")
	if component.ThemeIndex() != 0 {
		t.Fatalf("index = %d", component.ThemeIndex())
	}
	if len(previews) != 1 || previews[0] != "dark" {
		t.Fatalf("previews = %v", previews)
	}
	// Clamped at the top: no further preview.
	component.HandleInput("up")
	if len(previews) != 1 {
		t.Fatalf("previews = %v", previews)
	}
	// Down returns to light.
	component.HandleInput("down")
	if len(previews) != 2 || previews[1] != "light" {
		t.Fatalf("previews = %v", previews)
	}
}

func TestFirstTimeSetupVimKeys(t *testing.T) {
	component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{DetectedTheme: "dark"})
	component.HandleInput("j")
	if component.ThemeIndex() != 1 {
		t.Fatalf("j = %d", component.ThemeIndex())
	}
	component.HandleInput("k")
	if component.ThemeIndex() != 0 {
		t.Fatalf("k = %d", component.ThemeIndex())
	}
}

func TestFirstTimeSetupAdvancesToAnalytics(t *testing.T) {
	component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{DetectedTheme: "dark"})
	component.HandleInput("enter")
	if component.Step() != "analytics" {
		t.Fatalf("step = %q", component.Step())
	}
	lines := component.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Opt-in to anonymous usage data sharing?") {
		t.Fatalf("prompt = %q", joined)
	}
	if !strings.Contains(joined, "→ Share anonymous usage data") {
		t.Fatalf("cursor = %q", joined)
	}
	if !strings.Contains(joined, "finish") {
		t.Fatalf("action hint = %q", joined)
	}
	if !strings.Contains(joined, "settings.json") {
		t.Fatalf("explanation = %q", joined)
	}
}

func TestFirstTimeSetupSubmit(t *testing.T) {
	var result *FirstTimeSetupResult
	component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
		DetectedTheme: "dark",
		OnSubmit:      func(r FirstTimeSetupResult) { result = &r },
	})
	component.HandleInput("enter") // theme -> analytics
	component.HandleInput("down")  // share -> don't share
	component.HandleInput("enter") // submit

	if result == nil {
		t.Fatal("no result")
	}
	if result.Theme != "dark" {
		t.Fatalf("theme = %q", result.Theme)
	}
	if result.ShareAnalytics {
		t.Fatal("analytics not declined")
	}
}

func TestFirstTimeSetupSubmitAcceptsAnalytics(t *testing.T) {
	var result *FirstTimeSetupResult
	component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
		DetectedTheme: "light",
		OnSubmit:      func(r FirstTimeSetupResult) { result = &r },
	})
	component.HandleInput("enter") // theme -> analytics (light selected)
	component.HandleInput("enter") // submit with share

	if result == nil {
		t.Fatal("no result")
	}
	if result.Theme != "light" || !result.ShareAnalytics {
		t.Fatalf("result = %+v", result)
	}
}

func TestFirstTimeSetupAnalyticsClamp(t *testing.T) {
	component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{DetectedTheme: "dark"})
	component.HandleInput("enter")
	component.HandleInput("down")
	component.HandleInput("down")
	component.HandleInput("down")
	if component.AnalyticsIndex() != 1 {
		t.Fatalf("index = %d", component.AnalyticsIndex())
	}
	component.HandleInput("up")
	component.HandleInput("up")
	if component.AnalyticsIndex() != 0 {
		t.Fatalf("index = %d", component.AnalyticsIndex())
	}
}

func TestFirstTimeSetupCancel(t *testing.T) {
	cancelled := false
	component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
		DetectedTheme: "dark",
		OnCancel:      func() { cancelled = true },
	})
	component.HandleInput("escape")
	if !cancelled {
		t.Fatal("escape did not cancel")
	}

	component2 := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
		DetectedTheme: "dark",
		OnCancel:      func() { cancelled = true },
	})
	cancelled = false
	component2.HandleInput("ctrl+c")
	if !cancelled {
		t.Fatal("ctrl+c did not cancel")
	}
}
