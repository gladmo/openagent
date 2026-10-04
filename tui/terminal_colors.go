// Package tui is a Go 1:1 port of pi/packages/tui v0.87.1 (commit ff72faba2):
// a terminal UI library with differential rendering. Files mirror the
// TypeScript sources by basename (colors.ts -> colors.go); identifiers follow
// the repo-wide TS->Go mapping conventions. Ported behavior is pinned to the
// reference; deviations are defects and live in PARITY.md.
package tui

import (
	"regexp"
	"strconv"
	"strings"
)

// RgbColor is a concrete sRGB triple; channels may be fractional before rounding.
type RgbColor struct {
	R float64
	G float64
	B float64
}

// TerminalColorScheme reports the terminal's dark/light preference.
type TerminalColorScheme string

const (
	TerminalColorSchemeDark  TerminalColorScheme = "dark"
	TerminalColorSchemeLight TerminalColorScheme = "light"
)

// OscColorSlot identifies OSC color slots: 10 is the terminal's default
// foreground, 11 its default background.
type OscColorSlot int

const (
	OscColorSlotForeground OscColorSlot = 10
	OscColorSlotBackground OscColorSlot = 11
)

func hexToRgb(hex string) RgbColor {
	normalized := hex
	if len(hex) > 0 && hex[0] == '#' {
		normalized = hex[1:]
	}
	r := parseHexPair(normalized[0:2])
	g := parseHexPair(normalized[2:4])
	b := parseHexPair(normalized[4:6])
	return RgbColor{R: r, G: g, B: b}
}

func parseHexPair(pair string) float64 {
	value, _ := strconv.ParseInt(pair, 16, 64)
	return float64(value)
}

func isHexDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// parseOscHexChannel scales a hex channel of any length to 0..255; ok=false
// when the text is not pure hex or the maximum value is not positive.
func parseOscHexChannel(channel string) (float64, bool) {
	if !isHexDigits(channel) {
		return 0, false
	}
	parsed, err := strconv.ParseInt(channel, 16, 64)
	if err != nil {
		return 0, false
	}
	max := float64(int64(1)<<uint(4*len(channel))) - 1
	if max <= 0 {
		return 0, false
	}
	return jsRound(float64(parsed) / max * 255), true
}

// OscColorResponse is a parsed OSC 10/11 reply; Rgb is nil when the reply
// carried an unparseable color.
type OscColorResponse struct {
	Slot OscColorSlot
	Rgb  *RgbColor
}

var oscColorResponsePattern = regexp.MustCompile(`^\x1b\](1[01]);([^\x07\x1b]*)(?:\x07|\x1b\\)$`)
var colorSchemeReportPattern = regexp.MustCompile(`^(?:\x1b\[\?997;(1|2)n)+$`)

// ParseOscColorResponse parses an OSC 10/11 color reply; returns nil when data
// is not such a reply.
func ParseOscColorResponse(data string) *OscColorResponse {
	match := oscColorResponsePattern.FindStringSubmatch(data)
	if match == nil {
		return nil
	}
	slot := OscColorSlotBackground
	if match[1] == "10" {
		slot = OscColorSlotForeground
	}
	rgb := parseOscColorValue(match[2])
	return &OscColorResponse{Slot: slot, Rgb: rgb}
}

// ParseOsc11BackgroundColor returns the background color from an OSC 11 reply,
// or nil when data is not a strict reply.
func ParseOsc11BackgroundColor(data string) *RgbColor {
	response := ParseOscColorResponse(data)
	if response == nil || response.Slot != OscColorSlotBackground {
		return nil
	}
	return response.Rgb
}

func parseOscColorValue(rawValue string) *RgbColor {
	value := strings.TrimSpace(rawValue)
	if strings.HasPrefix(value, "#") {
		hex := value[1:]
		if len(hex) == 6 && isHexDigits(hex) {
			rgb := hexToRgb(value)
			return &rgb
		}
		if len(hex) == 12 && isHexDigits(hex) {
			r, rOK := parseOscHexChannel(hex[0:4])
			g, gOK := parseOscHexChannel(hex[4:8])
			b, bOK := parseOscHexChannel(hex[8:12])
			if rOK && gOK && bOK {
				return &RgbColor{R: r, G: g, B: b}
			}
			return nil
		}
		return nil
	}

	rgbValue := value
	if len(rgbValue) >= 5 && (strings.EqualFold(rgbValue[:5], "rgba:")) {
		rgbValue = rgbValue[5:]
	} else if len(rgbValue) >= 4 && strings.EqualFold(rgbValue[:4], "rgb:") {
		rgbValue = rgbValue[4:]
	}
	parts := strings.Split(rgbValue, "/")
	if len(parts) != 3 {
		return nil
	}
	r, rOK := parseOscHexChannel(parts[0])
	g, gOK := parseOscHexChannel(parts[1])
	b, bOK := parseOscHexChannel(parts[2])
	if !rOK || !gOK || !bOK {
		return nil
	}
	return &RgbColor{R: r, G: g, B: b}
}

// ParseTerminalColorSchemeReport parses a ?997 scheme report; the reply is the
// last slot's value. Returns nil for anything else, including ?996 queries.
func ParseTerminalColorSchemeReport(data string) TerminalColorScheme {
	match := colorSchemeReportPattern.FindStringSubmatch(data)
	if match == nil {
		return ""
	}
	if match[1] == "2" {
		return TerminalColorSchemeLight
	}
	return TerminalColorSchemeDark
}
