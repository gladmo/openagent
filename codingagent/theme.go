package codingagent

// Port of modes/interactive/theme/theme.ts (core): the Theme token model,
// theme construction with precomputed ANSI sequences, JSON theme loading,
// theme-name resolution (including light/dark auto pairs), and the
// registry with change notification.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/gladmo/openagent/tui"
)

// ThemeColor names every foreground token.
var ThemeColorTokens = []string{
	"accent", "border", "borderAccent", "borderMuted",
	"success", "error", "warning", "muted", "dim", "text",
	"thinkingText",
	"scrollbarTrack", "scrollbarThumb", "searchMatchText",
	"userMessageText", "customMessageText", "customMessageLabel",
	"toolTitle", "toolOutput",
	"mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdCodeBlock", "mdCodeBlockBorder",
	"mdQuote", "mdQuoteBorder", "mdHr", "mdListBullet",
	"toolDiffAdded", "toolDiffRemoved", "toolDiffContext",
	"syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxVariable",
	"syntaxString", "syntaxNumber", "syntaxType", "syntaxOperator", "syntaxPunctuation",
	"thinkingOff", "thinkingMinimal", "thinkingLow", "thinkingMedium",
	"thinkingHigh", "thinkingXhigh", "thinkingMax",
	"bashMode",
}

// ThemeBg names every background token.
var ThemeBgTokens = []string{
	"selectedBg", "searchMatchBg", "userMessageBg", "customMessageBg",
	"toolPendingBg", "toolSuccessBg", "toolErrorBg",
}

// ThemeAppearance is light | dark.
type ThemeAppearance string

// TerminalTheme is the detected terminal appearance.
type TerminalTheme = ThemeAppearance

// Theme is one constructed theme.
type Theme struct {
	Name       string
	SourcePath string
	SourceInfo SourceInfo
	Mode       tui.TerminalColorMode

	fgAnsi map[string]string
	bgAnsi map[string]string

	concreteColors          map[string]tui.Color
	defaultForegroundTokens []string
	defaultBackgroundTokens []string

	ownAppearance ThemeAppearance
	hasAppearance bool
}

// themeValue accepts string or number color specs.
type themeValue struct {
	value any
}

// NewTheme builds a theme from token color maps, precomputing the escape
// sequences ("" means terminal default for the slot).
func NewTheme(fgColors, bgColors map[string]any, mode tui.TerminalColorMode, name, sourcePath string, appearance ThemeAppearance, hasAppearance bool) *Theme {
	theme := &Theme{
		Name:           name,
		SourcePath:     sourcePath,
		Mode:           mode,
		fgAnsi:         map[string]string{},
		bgAnsi:         map[string]string{},
		concreteColors: map[string]tui.Color{},
	}

	// Fill the fallback chain.
	withFallback := func(colors map[string]any) map[string]any {
		filled := map[string]any{}
		for key, value := range colors {
			filled[key] = value
		}
		if _, ok := filled["scrollbarTrack"]; !ok {
			filled["scrollbarTrack"] = colors["muted"]
		}
		if _, ok := filled["scrollbarThumb"]; !ok {
			filled["scrollbarThumb"] = colors["text"]
		}
		if _, ok := filled["thinkingMax"]; !ok {
			filled["thinkingMax"] = colors["thinkingXhigh"]
		}
		if _, ok := filled["searchMatchText"]; !ok {
			filled["searchMatchText"] = colors["text"]
		}
		return filled
	}
	withBgFallback := func(colors map[string]any) map[string]any {
		filled := map[string]any{}
		for key, value := range colors {
			filled[key] = value
		}
		if _, ok := filled["searchMatchBg"]; !ok {
			filled["searchMatchBg"] = colors["selectedBg"]
		}
		return filled
	}

	addToken := func(token string, value any, isBackground bool) {
		if text, ok := value.(string); ok && text == "" {
			if isBackground {
				theme.defaultBackgroundTokens = append(theme.defaultBackgroundTokens, token)
				theme.bgAnsi[token] = "\x1b[49m"
			} else {
				theme.defaultForegroundTokens = append(theme.defaultForegroundTokens, token)
				theme.fgAnsi[token] = "\x1b[39m"
			}
			return
		}
		var spec string
		switch typed := value.(type) {
		case string:
			spec = typed
		case float64:
			spec = fmt.Sprintf("%d", int(typed))
		default:
			spec = "#000000"
		}
		color := tui.ParseColor(spec)
		theme.concreteColors[token] = color
		if isBackground {
			theme.bgAnsi[token] = tui.BackgroundAnsi(color, mode)
		} else {
			theme.fgAnsi[token] = tui.ForegroundAnsi(color, mode)
		}
	}

	for token, value := range withFallback(fgColors) {
		addToken(token, value, false)
	}
	for token, value := range withBgFallback(bgColors) {
		addToken(token, value, true)
	}

	theme.ownAppearance = appearance
	theme.hasAppearance = hasAppearance
	if !hasAppearance {
		theme.ownAppearance = detectAppearance(theme.concreteColors, ThemeColorTokens, ThemeBgTokens)
	}
	return theme
}

// detectAppearance infers light/dark from the concrete colors' luminance.
func detectAppearance(colors map[string]tui.Color, fgTokens, bgTokens []string) ThemeAppearance {
	var samples []tui.RgbColor
	for _, token := range append(append([]string(nil), fgTokens...), bgTokens...) {
		if color, ok := colors[token]; ok {
			samples = append(samples, tui.ColorToRgb(color))
		}
	}
	if len(samples) == 0 {
		return ThemeAppearance("dark")
	}
	sum := 0.0
	for _, rgb := range samples {
		sum += 0.2126*rgb.R + 0.7152*rgb.G + 0.0722*rgb.B
	}
	average := sum / float64(len(samples))
	if average > 0.5 {
		return ThemeAppearance("dark") // bright colors on a dark background
	}
	return ThemeAppearance("light")
}

// Fg returns the precomputed foreground sequence for a token.
func (theme *Theme) Fg(token string) string {
	if sequence, ok := theme.fgAnsi[token]; ok {
		return sequence
	}
	return "\x1b[39m"
}

// Bg returns the precomputed background sequence for a token.
func (theme *Theme) Bg(token string) string {
	if sequence, ok := theme.bgAnsi[token]; ok {
		return sequence
	}
	return "\x1b[49m"
}

// StyleText renders text with a token's foreground.
func (theme *Theme) StyleText(token, text string) string {
	return theme.Fg(token) + text + "\x1b[39m"
}

// Appearance reports the theme's designed background.
func (theme *Theme) Appearance() ThemeAppearance { return theme.ownAppearance }

// Colors resolves concrete colors (terminal-default tokens included).
func (theme *Theme) Colors() map[string]string {
	out := map[string]string{}
	for token, color := range theme.concreteColors {
		out[token] = tui.ColorToHex(color)
	}
	return out
}

// IsLight reports a light-designed theme.
func (theme *Theme) IsLight() bool { return theme.ownAppearance == ThemeAppearance("light") }

// ============================================================================
// JSON themes
// ============================================================================

// ThemeJSON is the on-disk theme document.
type ThemeJSON struct {
	Name       string         `json:"name"`
	Appearance string         `json:"appearance"`
	Colors     map[string]any `json:"colors"`
	Styles     map[string]any `json:"styles"`
}

// ParseThemeJSONContent parses and validates one theme document.
func ParseThemeJSONContent(path, content string) (*ThemeJSON, error) {
	var themeJSON ThemeJSON
	if err := json.Unmarshal([]byte(StripBom(content)), &themeJSON); err != nil {
		return nil, fmt.Errorf("Invalid theme JSON in %s: %s", path, err.Error())
	}
	if themeJSON.Colors == nil {
		themeJSON.Colors = map[string]any{}
	}
	return &themeJSON, nil
}

// CreateTheme builds a Theme from a parsed document.
func CreateTheme(themeJSON *ThemeJSON, mode tui.TerminalColorMode, sourcePath string) (*Theme, error) {
	colors := themeJSON.Colors
	fgColors := map[string]any{}
	bgColors := map[string]any{}
	for token, value := range colors {
		if containsString(ThemeBgTokens, token) {
			bgColors[token] = value
			continue
		}
		fgColors[token] = value
	}

	name := themeJSON.Name
	if name == "" && sourcePath != "" {
		name = strings.TrimSuffix(filepath.Base(sourcePath), ".json")
	}

	appearance := ThemeAppearance("")
	hasAppearance := false
	switch themeJSON.Appearance {
	case "light":
		appearance = ThemeAppearance("light")
		hasAppearance = true
	case "dark":
		appearance = ThemeAppearance("dark")
		hasAppearance = true
	}

	return NewTheme(fgColors, bgColors, mode, name, sourcePath, appearance, hasAppearance), nil
}

// LoadThemeFromPath reads and constructs a theme file.
func LoadThemeFromPath(themePath string, mode tui.TerminalColorMode) (*Theme, error) {
	content, err := os.ReadFile(themePath)
	if err != nil {
		return nil, err
	}
	themeJSON, err := ParseThemeJSONContent(themePath, string(content))
	if err != nil {
		return nil, err
	}
	return CreateTheme(themeJSON, mode, themePath)
}

// ============================================================================
// Registry + resolution
// ============================================================================

var (
	themeMu              sync.Mutex
	registeredThemes     = map[string]*Theme{}
	themeChangeCallbacks []func()
	activeTheme          *Theme
)

// SetRegisteredThemes installs the built-in themes.
func SetRegisteredThemes(themes []*Theme) {
	themeMu.Lock()
	defer themeMu.Unlock()
	registeredThemes = map[string]*Theme{}
	for _, theme := range themes {
		registeredThemes[theme.Name] = theme
	}
}

// GetRegisteredTheme returns one built-in theme.
func GetRegisteredTheme(name string) *Theme {
	themeMu.Lock()
	defer themeMu.Unlock()
	return registeredThemes[name]
}

// GetAvailableThemes lists built-in theme names.
func GetAvailableThemes() []string {
	themeMu.Lock()
	defer themeMu.Unlock()
	names := make([]string, 0, len(registeredThemes))
	for name := range registeredThemes {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// ThemeInfo pairs a name with its file path.
type ThemeInfo struct {
	Name string
	Path string
}

// GetAvailableThemesWithPaths lists built-ins plus discovered files.
func GetAvailableThemesWithPaths() []ThemeInfo {
	themeMu.Lock()
	defer themeMu.Unlock()
	var infos []ThemeInfo
	for name, theme := range registeredThemes {
		infos = append(infos, ThemeInfo{Name: name, Path: theme.SourcePath})
	}
	// Custom themes dir.
	if entries, err := os.ReadDir(GetCustomThemesDir()); err == nil {
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".json") {
				name := strings.TrimSuffix(entry.Name(), ".json")
				infos = append(infos, ThemeInfo{Name: name, Path: filepath.Join(GetCustomThemesDir(), entry.Name())})
			}
		}
	}
	sortStrings(nil)
	return infos
}

// GetThemeByName resolves built-ins then files; nil when missing.
func GetThemeByName(name string, mode tui.TerminalColorMode) *Theme {
	if registered := GetRegisteredTheme(name); registered != nil {
		return registered
	}
	for _, info := range GetAvailableThemesWithPaths() {
		if info.Name == name {
			if theme, err := LoadThemeFromPath(info.Path, mode); err == nil {
				return theme
			}
			return nil
		}
	}
	return nil
}

var autoThemePattern = regexp.MustCompile(`^[^/]+/[^/]+$`)

// ParseAutoThemeSetting splits "light/dark" pairs.
func ParseAutoThemeSetting(themeSetting string) (lightTheme, darkTheme string, ok bool) {
	if themeSetting == "" {
		return "", "", false
	}
	if !autoThemePattern.MatchString(themeSetting) {
		return "", "", false
	}
	parts := strings.SplitN(themeSetting, "/", 2)
	light := strings.TrimSpace(parts[0])
	dark := strings.TrimSpace(parts[1])
	if light == "" || dark == "" {
		return "", "", false
	}
	return light, dark, true
}

// ResolveThemeSetting resolves a setting against the terminal theme;
// invalid paths resolve to nothing.
func ResolveThemeSetting(themeSetting string, terminalTheme TerminalTheme) string {
	if light, dark, ok := ParseAutoThemeSetting(themeSetting); ok {
		if terminalTheme == TerminalTheme("light") {
			return light
		}
		return dark
	}
	if strings.Contains(themeSetting, "/") {
		return ""
	}
	return themeSetting
}

// rgbLuminance computes the WCAG relative luminance from 0-255 channels.
func rgbLuminance(r, g, b float64) float64 {
	toLinear := func(channel float64) float64 {
		value := channel / 255
		if value <= 0.03928 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*toLinear(r) + 0.7152*toLinear(g) + 0.0722*toLinear(b)
}

// GetThemeForRgbColor classifies a background color.
func GetThemeForRgbColor(rgb tui.RgbColor) TerminalTheme {
	if rgbLuminance(rgb.R, rgb.G, rgb.B) >= 0.5 {
		return TerminalTheme("light")
	}
	return TerminalTheme("dark")
}

// ansiColorLuminance luminance of an ANSI-256 index.
func ansiColorLuminance(index int) float64 {
	rgb := tui.ColorToRgb(tui.IndexedColor{Index: index})
	return rgbLuminance(rgb.R, rgb.G, rgb.B)
}

// colorFgBgBackgroundIndex parses COLORFGBG's background index (last valid
// 0-255 field, scanning backwards).
func colorFgBgBackgroundIndex(colorfgbg string) (int, bool) {
	parts := strings.Split(colorfgbg, ";")
	for i := len(parts) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(parts[i])
		index := 0
		valid := trimmed != ""
		for _, c := range trimmed {
			if c < '0' || c > '9' {
				valid = false
				break
			}
			index = index*10 + int(c-'0')
		}
		if valid && index >= 0 && index <= 255 {
			return index, true
		}
	}
	return 0, false
}

// DetectTerminalBackgroundFromEnv guesses from COLORFGBG.
func DetectTerminalBackgroundFromEnv() TerminalThemeDetection {
	if colorfgbg := os.Getenv("COLORFGBG"); colorfgbg != "" {
		if bg, ok := colorFgBgBackgroundIndex(colorfgbg); ok {
			theme := TerminalTheme("dark")
			if ansiColorLuminance(bg) >= 0.5 {
				theme = TerminalTheme("light")
			}
			return TerminalThemeDetection{Theme: theme, Source: "COLORFGBG"}
		}
	}
	return TerminalThemeDetection{Theme: TerminalTheme("dark"), Source: "fallback"}
}

// TerminalThemeDetection is the detection outcome.
type TerminalThemeDetection struct {
	Theme  TerminalTheme
	Source string
}

// SetTheme activates a theme by name.
func SetTheme(name string, mode tui.TerminalColorMode) (bool, string) {
	theme := GetThemeByName(name, mode)
	if theme == nil {
		return false, fmt.Sprintf("Theme not found: %s", name)
	}
	themeMu.Lock()
	activeTheme = theme
	var callbacks []func()
	callbacks = append(callbacks, themeChangeCallbacks...)
	themeMu.Unlock()
	for _, callback := range callbacks {
		if callback != nil {
			callback()
		}
	}
	return true, ""
}

// ActiveTheme returns the current theme (nil before init).
func ActiveTheme() *Theme {
	themeMu.Lock()
	defer themeMu.Unlock()
	return activeTheme
}

// SetThemeInstance installs a constructed theme.
func SetThemeInstance(theme *Theme) {
	themeMu.Lock()
	activeTheme = theme
	themeMu.Unlock()
}

// OnThemeChange registers a change callback.
func OnThemeChange(callback func()) {
	themeMu.Lock()
	defer themeMu.Unlock()
	themeChangeCallbacks = append(themeChangeCallbacks, callback)
}

// GetDefaultTheme returns the default theme name.
func GetDefaultTheme() string { return "default" }

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}
