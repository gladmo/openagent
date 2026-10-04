package tui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// IndexedColor selects an entry from the terminal's 256-color palette.
type IndexedColor struct {
	Index int
}

// RgbColorValue is a literal sRGB color.
type RgbColorValue struct {
	R float64
	G float64
	B float64
}

// OklchColorValue is a color in the OKLCH perceptual space.
type OklchColorValue struct {
	L float64
	C float64
	H float64
}

// Color is a concrete color. Every color can be converted to sRGB, so color
// math never fails. The closed union is discriminated by the Go type.
type Color interface {
	colorKind() string
}

func (IndexedColor) colorKind() string    { return "indexed" }
func (RgbColorValue) colorKind() string   { return "rgb" }
func (OklchColorValue) colorKind() string { return "oklch" }

// TerminalColorMode selects the ANSI color encoding for styled output.
type TerminalColorMode string

const (
	TerminalColorMode256       TerminalColorMode = "256color"
	TerminalColorModeTrueColor TerminalColorMode = "truecolor"
)

// ColorMixSpace selects the interpolation space for MixColors.
type ColorMixSpace string

const (
	ColorMixSpaceOklch ColorMixSpace = "oklch"
	ColorMixSpaceSrgb  ColorMixSpace = "srgb"
)

// OklchChannels is OKLCH as three plain channels.
type OklchChannels struct {
	L float64
	C float64
	H float64
}

// TextAttributes are the SGR attribute flags.
type TextAttributes struct {
	Bold          bool
	Dim           bool
	Italic        bool
	Underline     bool
	Inverse       bool
	Strikethrough bool
}

// TextStyle adds foreground/background colors to TextAttributes.
type TextStyle struct {
	TextAttributes
	Fg Color
	Bg Color
}

func requireFinite(value float64, name string) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		panic(fmt.Sprintf("%s must be finite", name))
	}
}

// IndexedColorAt builds an indexed color, rejecting out-of-range indexes.
// (Named At because the type keeps the TS name IndexedColor.)
func IndexedColorAt(index int) IndexedColor {
	if index < 0 || index > 255 {
		panic(fmt.Sprintf("ANSI color index must be an integer from 0 to 255: %s", formatJSNumber(float64(index))))
	}
	return IndexedColor{Index: index}
}

// RgbColorAt builds an RGB color with channels in 0..255 (fractions allowed).
func RgbColorAt(r, g, b float64) RgbColorValue {
	for _, ch := range []struct {
		name  string
		value float64
	}{{"r", r}, {"g", g}, {"b", b}} {
		requireFinite(ch.value, ch.name)
		if ch.value < 0 || ch.value > 255 {
			panic(fmt.Sprintf("%s must be between 0 and 255: %s", ch.name, formatJSNumber(ch.value)))
		}
	}
	return RgbColorValue{R: r, G: g, B: b}
}

// OklchColorAt builds an OKLCH color; hue is normalized into [0, 360).
func OklchColorAt(l, c, h float64) OklchColorValue {
	requireFinite(l, "l")
	requireFinite(c, "c")
	requireFinite(h, "h")
	if l < 0 || l > 1 {
		panic(fmt.Sprintf("l must be between 0 and 1: %s", formatJSNumber(l)))
	}
	if c < 0 {
		panic(fmt.Sprintf("c must not be negative: %s", formatJSNumber(c)))
	}
	return OklchColorValue{L: l, C: c, H: jsMod(h, 360)}
}

const numberPattern = `[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?`

var hexColorPattern = regexp.MustCompile(`(?i)^#([\da-f]{3}|[\da-f]{6})$`)
var oklchPattern = regexp.MustCompile(`(?i)^oklch\(\s*(` + numberPattern + `)(%)?\s+(` + numberPattern + `)\s+(` + numberPattern + `)(?:deg)?\s*\)$`)

// ParseIndexedOrNamedColor mirrors parseColor(value: string | number): a
// number selects an indexed color, a string parses as hex or oklch().
func ParseIndexedOrNamedColor(value any) Color {
	switch v := value.(type) {
	case int:
		return IndexedColorAt(v)
	case float64:
		return IndexedColorAt(int(v))
	case string:
		return ParseColor(v)
	}
	panic(fmt.Sprintf("Invalid color value: %v", value))
}

// ParseColor parses "#abc", "#aabbcc", and "oklch(L C H)" (percent or 0..1
// lightness). Panics with "Invalid color value: ..." otherwise, like the
// reference.
func ParseColor(value string) Color {
	if hex := hexColorPattern.FindStringSubmatch(value); hex != nil {
		digits := hex[1]
		if len(digits) == 3 {
			digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
		}
		r, _ := strconv.ParseInt(digits[0:2], 16, 64)
		g, _ := strconv.ParseInt(digits[2:4], 16, 64)
		b, _ := strconv.ParseInt(digits[4:6], 16, 64)
		return RgbColorAt(float64(r), float64(g), float64(b))
	}

	if oklch := oklchPattern.FindStringSubmatch(value); oklch != nil {
		lightness, _ := strconv.ParseFloat(oklch[1], 64)
		divisor := 1.0
		if oklch[2] == "%" {
			divisor = 100
		}
		c, _ := strconv.ParseFloat(oklch[3], 64)
		h, _ := strconv.ParseFloat(oklch[4], 64)
		return OklchColorAt(lightness/divisor, c, h)
	}

	panic(fmt.Sprintf("Invalid color value: %s", value))
}

var basicColors = []RgbColor{
	{R: 0, G: 0, B: 0},
	{R: 128, G: 0, B: 0},
	{R: 0, G: 128, B: 0},
	{R: 128, G: 128, B: 0},
	{R: 0, G: 0, B: 128},
	{R: 128, G: 0, B: 128},
	{R: 0, G: 128, B: 128},
	{R: 192, G: 192, B: 192},
	{R: 128, G: 128, B: 128},
	{R: 255, G: 0, B: 0},
	{R: 0, G: 255, B: 0},
	{R: 255, G: 255, B: 0},
	{R: 0, G: 0, B: 255},
	{R: 255, G: 0, B: 255},
	{R: 0, G: 255, B: 255},
	{R: 255, G: 255, B: 255},
}

var cubeValues = [6]float64{0, 95, 135, 175, 215, 255}

func grayValues() [24]float64 {
	var values [24]float64
	for i := 0; i < 24; i++ {
		values[i] = float64(8 + i*10)
	}
	return values
}

func indexedToRgb(index int) RgbColor {
	if index < 16 {
		return basicColors[index]
	}
	if index < 232 {
		cubeIndex := index - 16
		return RgbColor{
			R: cubeValues[cubeIndex/36],
			G: cubeValues[(cubeIndex%36)/6],
			B: cubeValues[cubeIndex%6],
		}
	}
	gray := float64(8 + (index-232)*10)
	return RgbColor{R: gray, G: gray, B: gray}
}

func srgbToLinear(channel float64) float64 {
	if channel <= 0.04045 {
		return channel / 12.92
	}
	return jsPow((channel+0.055)/1.055, 2.4)
}

func linearToSrgb(channel float64) float64 {
	if channel <= 0.0031308 {
		return channel * 12.92
	}
	return 1.055*jsPow(channel, 1/2.4) - 0.055
}

type oklabChannels struct {
	l float64
	a float64
	b float64
}

type linearRgbChannels struct {
	r float64
	g float64
	b float64
}

func rgbToOklab(rgb RgbColor) oklabChannels {
	linearR := srgbToLinear(rgb.R / 255)
	linearG := srgbToLinear(rgb.G / 255)
	linearB := srgbToLinear(rgb.B / 255)
	l := math.Cbrt(0.4122214708*linearR + 0.5363325363*linearG + 0.0514459929*linearB)
	m := math.Cbrt(0.2119034982*linearR + 0.6806995451*linearG + 0.1073969566*linearB)
	s := math.Cbrt(0.0883024619*linearR + 0.2817188376*linearG + 0.6299787005*linearB)
	return oklabChannels{
		l: 0.2104542553*l + 0.793617785*m - 0.0040720468*s,
		a: 1.9779984951*l - 2.428592205*m + 0.4505937099*s,
		b: 0.0259040371*l + 0.7827717662*m - 0.808675766*s,
	}
}

func oklabToLinearRgb(ch oklabChannels) linearRgbChannels {
	lRoot := ch.l + 0.3963377774*ch.a + 0.2158037573*ch.b
	mRoot := ch.l - 0.1055613458*ch.a - 0.0638541728*ch.b
	sRoot := ch.l - 0.0894841775*ch.a - 1.291485548*ch.b
	lValue := jsPow(lRoot, 3)
	mValue := jsPow(mRoot, 3)
	sValue := jsPow(sRoot, 3)
	return linearRgbChannels{
		r: 4.0767416621*lValue - 3.3077115913*mValue + 0.2309699292*sValue,
		g: -1.2684380046*lValue + 2.6097574011*mValue - 0.3413193965*sValue,
		b: -0.0041960863*lValue - 0.7034186147*mValue + 1.707614701*sValue,
	}
}

func isInSrgbGamut(ch linearRgbChannels) bool {
	const epsilon = 1e-7
	return ch.r >= -epsilon && ch.r <= 1+epsilon && ch.g >= -epsilon && ch.g <= 1+epsilon && ch.b >= -epsilon && ch.b <= 1+epsilon
}

func linearRgbToChannels(ch linearRgbChannels) RgbColor {
	return RgbColor{
		R: jsRound(math.Max(0, math.Min(1, linearToSrgb(ch.r))) * 255),
		G: jsRound(math.Max(0, math.Min(1, linearToSrgb(ch.g))) * 255),
		B: jsRound(math.Max(0, math.Min(1, linearToSrgb(ch.b))) * 255),
	}
}

func oklchToRgb(ch OklchChannels) RgbColor {
	// Gamut mapping keeps the hue fixed, so its direction is computed once and
	// scaled by chroma.
	radians := (ch.H * math.Pi) / 180
	cos := math.Cos(radians)
	sin := math.Sin(radians)
	atChroma := func(chroma float64) linearRgbChannels {
		return oklabToLinearRgb(oklabChannels{l: ch.L, a: chroma * cos, b: chroma * sin})
	}

	direct := atChroma(ch.C)
	if isInSrgbGamut(direct) {
		return linearRgbToChannels(direct)
	}

	// Reduce chroma until the color fits. The achromatic color is always in
	// gamut, so it is the fallback when no bisection step fits, e.g.
	// oklch(100% 0.3 150) must map to white.
	linear := atChroma(0)
	low := 0.0
	high := ch.C
	for index := 0; index < 20; index++ {
		chroma := (low + high) / 2
		candidate := atChroma(chroma)
		if isInSrgbGamut(candidate) {
			low = chroma
			linear = candidate
		} else {
			high = chroma
		}
	}
	return linearRgbToChannels(linear)
}

// ColorToRgb converts any color to sRGB channels.
func ColorToRgb(color Color) RgbColor {
	switch c := color.(type) {
	case IndexedColor:
		return indexedToRgb(c.Index)
	case RgbColorValue:
		return RgbColor{R: c.R, G: c.G, B: c.B}
	case OklchColorValue:
		return oklchToRgb(OklchChannels{L: c.L, C: c.C, H: c.H})
	}
	panic("unreachable color kind")
}

// ColorToOklch converts any color to OKLCH channels.
func ColorToOklch(color Color) OklchChannels {
	if c, ok := color.(OklchColorValue); ok {
		return OklchChannels{L: c.L, C: c.C, H: c.H}
	}
	lab := rgbToOklab(ColorToRgb(color))
	return OklchChannels{
		L: lab.l,
		C: math.Hypot(lab.a, lab.b),
		H: jsMod(math.Atan2(lab.b, lab.a)*180/math.Pi+360, 360),
	}
}

// ColorToHex renders the sRGB hex form, e.g. "#ab01cd".
func ColorToHex(color Color) string {
	rgb := ColorToRgb(color)
	return "#" + padStart(hexChannel(rgb.R)) + padStart(hexChannel(rgb.G)) + padStart(hexChannel(rgb.B))
}

func hexChannel(value float64) string {
	return strconv.FormatInt(int64(jsRound(value)), 16)
}

func padStart(s string) string {
	for len(s) < 2 {
		s = "0" + s
	}
	return s
}

// MixColors interpolates between two colors; amount stays within 0..1.
func MixColors(first, second Color, amount float64, space ColorMixSpace) Color {
	requireFinite(amount, "amount")
	if amount < 0 || amount > 1 {
		panic(fmt.Sprintf("amount must be between 0 and 1: %s", formatJSNumber(amount)))
	}

	if space == ColorMixSpaceSrgb {
		a := ColorToRgb(first)
		b := ColorToRgb(second)
		return RgbColorAt(a.R+(b.R-a.R)*amount, a.G+(b.G-a.G)*amount, a.B+(b.B-a.B)*amount)
	}
	if space == "" {
		space = ColorMixSpaceOklch
	}

	a := ColorToOklch(first)
	b := ColorToOklch(second)
	firstHue := a.H
	if a.C < 1e-7 {
		firstHue = b.H
	}
	secondHue := b.H
	if b.C < 1e-7 {
		secondHue = firstHue
	}
	hueDelta := jsMod(secondHue-firstHue+540, 360) - 180
	return OklchColorAt(a.L+(b.L-a.L)*amount, a.C+(b.C-a.C)*amount, firstHue+hueDelta*amount)
}

func findClosest(values []float64, target float64) int {
	closestIndex := 0
	closestDistance := math.Inf(1)
	for index := 0; index < len(values); index++ {
		distance := math.Abs(target - values[index])
		if distance < closestDistance {
			closestIndex = index
			closestDistance = distance
		}
	}
	return closestIndex
}

func colorDistance(first, second RgbColor) float64 {
	dr := first.R - second.R
	dg := first.G - second.G
	db := first.B - second.B
	return dr*dr*0.299 + dg*dg*0.587 + db*db*0.114
}

func rgbToAnsi256(color RgbColor) int {
	rIndex := findClosest(cubeValues[:], color.R)
	gIndex := findClosest(cubeValues[:], color.G)
	bIndex := findClosest(cubeValues[:], color.B)
	cubeColor := RgbColor{R: cubeValues[rIndex], G: cubeValues[gIndex], B: cubeValues[bIndex]}
	cubeIndex := 16 + 36*rIndex + 6*gIndex + bIndex

	gray := jsRound(0.299*color.R + 0.587*color.G + 0.114*color.B)
	grays := grayValues()
	grayOffset := findClosest(grays[:], gray)
	grayValue := grays[grayOffset]
	spread := math.Max(color.R, math.Max(color.G, color.B)) - math.Min(color.R, math.Min(color.G, color.B))
	if spread < 10 && colorDistance(color, RgbColor{R: grayValue, G: grayValue, B: grayValue}) < colorDistance(color, cubeColor) {
		return 232 + grayOffset
	}
	return cubeIndex
}

func colorAnsi(color Color, mode TerminalColorMode, background bool) string {
	if c, ok := color.(IndexedColor); ok {
		return fmt.Sprintf("\x1b[%d;5;%dm", ansiGround(background), c.Index)
	}

	rgb := ColorToRgb(color)
	if mode == TerminalColorModeTrueColor {
		return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", ansiGround(background),
			int(jsRound(rgb.R)), int(jsRound(rgb.G)), int(jsRound(rgb.B)))
	}
	return fmt.Sprintf("\x1b[%d;5;%dm", ansiGround(background), rgbToAnsi256(rgb))
}

func ansiGround(background bool) int {
	if background {
		return 48
	}
	return 38
}

// ForegroundAnsi renders the SGR escape opening a foreground color.
func ForegroundAnsi(color Color, mode TerminalColorMode) string {
	return colorAnsi(color, mode, false)
}

// BackgroundAnsi renders the SGR escape opening a background color.
func BackgroundAnsi(color Color, mode TerminalColorMode) string {
	return colorAnsi(color, mode, true)
}

// StyleText styles text with colors and attributes for the terminal mode.
func StyleText(text string, options TextStyle, mode TerminalColorMode) string {
	var fgAnsi, bgAnsi string
	if options.Fg != nil {
		fgAnsi = ForegroundAnsi(options.Fg, mode)
	}
	if options.Bg != nil {
		bgAnsi = BackgroundAnsi(options.Bg, mode)
	}
	return StyleTextWithAnsi(text, fgAnsi, bgAnsi, options.TextAttributes)
}

// StyleTextWithAnsi styles text with precomputed color escape sequences, e.g.
// cached theme colors; colors in options are ignored.
func StyleTextWithAnsi(text, fgAnsi, bgAnsi string, options TextAttributes) string {
	// Resets are prepended so they close in reverse order of the opening
	// sequences.
	prefix := ""
	suffix := ""
	if fgAnsi != "" {
		prefix += fgAnsi
		suffix = "\x1b[39m"
	}
	if bgAnsi != "" {
		prefix += bgAnsi
		suffix = "\x1b[49m" + suffix
	}
	if options.Bold {
		prefix += "\x1b[1m"
	}
	if options.Dim {
		prefix += "\x1b[2m"
	}
	if options.Bold || options.Dim {
		suffix = "\x1b[22m" + suffix
	}
	if options.Italic {
		prefix += "\x1b[3m"
		suffix = "\x1b[23m" + suffix
	}
	if options.Underline {
		prefix += "\x1b[4m"
		suffix = "\x1b[24m" + suffix
	}
	if options.Inverse {
		prefix += "\x1b[7m"
		suffix = "\x1b[27m" + suffix
	}
	if options.Strikethrough {
		prefix += "\x1b[9m"
		suffix = "\x1b[29m" + suffix
	}
	return prefix + text + suffix
}
