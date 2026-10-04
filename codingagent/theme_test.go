package codingagent

// Tests for the theme core (ported from theme.test.ts surfaces).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/tui"
)

func TestNewThemeTokens(t *testing.T) {
	fg := map[string]any{
		"accent": "#ff0000",
		"border": "#333333",
		"text":   "#eeeeee",
		"muted":  "#888888",
	}
	bg := map[string]any{
		"selectedBg": "#222222",
	}
	theme := NewTheme(fg, bg, tui.TerminalColorModeTrueColor, "test", "", "", false)

	if theme.Name != "test" {
		t.Fatalf("name = %q", theme.Name)
	}

	// Fallbacks fill: scrollbarTrack <- muted, scrollbarThumb <- text.
	if theme.Fg("accent") == "" || theme.Fg("scrollbarTrack") == "" || theme.Fg("scrollbarThumb") == "" {
		t.Fatal("fallback tokens missing")
	}
	// searchMatchText <- text, searchMatchBg <- selectedBg.
	if theme.Fg("searchMatchText") == "" || theme.Bg("searchMatchBg") == "" {
		t.Fatal("search fallbacks missing")
	}

	// Colors resolve to hex.
	colors := theme.Colors()
	if colors["accent"] != "#ff0000" {
		t.Fatalf("accent = %q", colors["accent"])
	}
}

func TestNewThemeTerminalDefault(t *testing.T) {
	fg := map[string]any{"accent": "", "text": "#ffffff"}
	theme := NewTheme(fg, map[string]any{}, tui.TerminalColorModeTrueColor, "t", "", "", false)

	// "" maps to the default-fg sequence and no concrete color.
	if theme.Fg("accent") != "\x1b[39m" {
		t.Fatalf("default fg = %q", theme.Fg("accent"))
	}
	if _, has := theme.Colors()["accent"]; has {
		t.Fatal("default token has a concrete color")
	}
	if theme.Fg("text") == "\x1b[39m" {
		t.Fatal("text collapsed to default")
	}
}

func TestNewThemeAppearance(t *testing.T) {
	// Bright colors on dark backgrounds -> dark theme.
	bright := NewTheme(
		map[string]any{"text": "#ffffff", "accent": "#ff5555"},
		map[string]any{"selectedBg": "#111111"},
		tui.TerminalColorModeTrueColor, "bright", "", "", false,
	)
	if bright.Appearance() != ThemeAppearance("dark") {
		t.Fatalf("bright = %q", bright.Appearance())
	}

	// Declared appearance wins.
	light := NewTheme(
		map[string]any{"text": "#ffffff"},
		map[string]any{},
		tui.TerminalColorModeTrueColor, "light", "", ThemeAppearance("light"), true,
	)
	if !light.IsLight() {
		t.Fatal("declared light not honored")
	}
}

func TestThemeJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my-theme.json")
	os.WriteFile(path, []byte(`{
		"name": "Custom",
		"appearance": "dark",
		"colors": {
			"accent": "#00ff00",
			"selectedBg": "#000044"
		}
	}`), 0o644)

	theme, err := LoadThemeFromPath(path, tui.TerminalColorModeTrueColor)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if theme.Name != "Custom" {
		t.Fatalf("name = %q", theme.Name)
	}
	if theme.Appearance() != ThemeAppearance("dark") {
		t.Fatalf("appearance = %q", theme.Appearance())
	}
	if theme.Colors()["accent"] != "#00ff00" {
		t.Fatalf("accent = %q", theme.Colors()["accent"])
	}
	if theme.Bg("selectedBg") == "\x1b[49m" {
		t.Fatal("selectedBg collapsed to default")
	}
}

func TestThemeJSONInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte(`{nope`), 0o644)
	if _, err := LoadThemeFromPath(path, tui.TerminalColorModeTrueColor); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestThemeJSONNameFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fallback-name.json")
	os.WriteFile(path, []byte(`{"colors": {"text": "#fff"}}`), 0o644)
	theme, err := LoadThemeFromPath(path, tui.TerminalColorModeTrueColor)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if theme.Name != "fallback-name" {
		t.Fatalf("name = %q", theme.Name)
	}
}

func TestThemeRegistry(t *testing.T) {
	builtin := NewTheme(
		map[string]any{"text": "#fff"},
		map[string]any{},
		tui.TerminalColorModeTrueColor, "builtin-one", "", "", false,
	)
	SetRegisteredThemes([]*Theme{builtin})

	if names := GetAvailableThemes(); len(names) != 1 || names[0] != "builtin-one" {
		t.Fatalf("names = %v", names)
	}
	if GetRegisteredTheme("builtin-one") == nil {
		t.Fatal("registered lookup failed")
	}
	if GetRegisteredTheme("missing") != nil {
		t.Fatal("missing returned a theme")
	}
}

func TestThemeActivation(t *testing.T) {
	builtin := NewTheme(
		map[string]any{"text": "#fff"},
		map[string]any{},
		tui.TerminalColorModeTrueColor, "active-me", "", "", false,
	)
	SetRegisteredThemes([]*Theme{builtin})

	changed := 0
	OnThemeChange(func() { changed++ })

	ok, errMsg := SetTheme("active-me", tui.TerminalColorModeTrueColor)
	if !ok || errMsg != "" {
		t.Fatalf("set = %v %q", ok, errMsg)
	}
	if ActiveTheme() == nil || ActiveTheme().Name != "active-me" {
		t.Fatal("active theme wrong")
	}
	if changed != 1 {
		t.Fatalf("changed = %d", changed)
	}

	// Unknown theme fails.
	ok, errMsg = SetTheme("no-such-theme", tui.TerminalColorModeTrueColor)
	if ok || !strings.Contains(errMsg, "Theme not found") {
		t.Fatalf("missing = %v %q", ok, errMsg)
	}
	if changed != 1 {
		t.Fatalf("changed after failure = %d", changed)
	}
}

func TestParseAutoThemeSetting(t *testing.T) {
	light, dark, ok := ParseAutoThemeSetting("light-theme/dark-theme")
	if !ok || light != "light-theme" || dark != "dark-theme" {
		t.Fatalf("auto = %v %v %v", light, dark, ok)
	}

	// Invalid forms.
	for _, invalid := range []string{"", "single", "a/b/c", "/dark", "light/"} {
		if _, _, ok := ParseAutoThemeSetting(invalid); ok {
			t.Errorf("%q accepted", invalid)
		}
	}
}

func TestResolveThemeSetting(t *testing.T) {
	if got := ResolveThemeSetting("lighty/darky", TerminalTheme("light")); got != "lighty" {
		t.Fatalf("light = %q", got)
	}
	if got := ResolveThemeSetting("lighty/darky", TerminalTheme("dark")); got != "darky" {
		t.Fatalf("dark = %q", got)
	}
	if got := ResolveThemeSetting("plain-theme", TerminalTheme("dark")); got != "plain-theme" {
		t.Fatalf("plain = %q", got)
	}
	if got := ResolveThemeSetting("", TerminalTheme("dark")); got != "" {
		t.Fatalf("empty = %q", got)
	}
}

func TestGetThemeForRgbColor(t *testing.T) {
	if got := GetThemeForRgbColor(rgbFromValues(230, 230, 230)); got != TerminalTheme("light") {
		t.Fatalf("bright = %q", got)
	}
	if got := GetThemeForRgbColor(rgbFromValues(15, 15, 15)); got != TerminalTheme("dark") {
		t.Fatalf("dark = %q", got)
	}
}

func TestDetectTerminalBackgroundFromEnv(t *testing.T) {
	// bg=0 (black) -> dark; bg=15 (white) -> light (luminance based).
	t.Setenv("COLORFGBG", "15;0")
	if got := DetectTerminalBackgroundFromEnv(); got.Theme != TerminalTheme("dark") || got.Source != "COLORFGBG" {
		t.Fatalf("COLORFGBG = %+v", got)
	}

	t.Setenv("COLORFGBG", "0;15")
	if got := DetectTerminalBackgroundFromEnv(); got.Theme != TerminalTheme("light") {
		t.Fatalf("COLORFGBG light = %+v", got)
	}

	// Missing hint falls back to dark.
	t.Setenv("COLORFGBG", "")
	if got := DetectTerminalBackgroundFromEnv(); got.Theme != TerminalTheme("dark") || got.Source != "fallback" {
		t.Fatalf("fallback = %+v", got)
	}
}

func rgbFromValues(r, g, b float64) tui.RgbColor {
	return tui.RgbColor{R: r, G: g, B: b}
}
