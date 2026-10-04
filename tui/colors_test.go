package tui

import (
	"strings"
	"testing"
)

// Ported from pi/packages/tui/test/colors.test.ts and the parse-only subset of
// terminal-colors.test.ts (TUI query tests live with the TuiMainScreen port).

func TestParseColorParsesHexAndOklchAndRejectsEverythingElse(t *testing.T) {
	if got := ParseColor("#abc"); got != (RgbColorValue{R: 170, G: 187, B: 204}) {
		t.Fatalf("ParseColor(#abc) = %#v", got)
	}
	if got := ParseColor("oklch(62% 0.1 200)"); got != (OklchColorValue{L: 0.62, C: 0.1, H: 200}) {
		t.Fatalf("ParseColor(oklch) = %#v", got)
	}
	for _, invalid := range []string{"", "red"} {
		assertPanicsMessage(t, func() { ParseColor(invalid) }, "Invalid color value", "ParseColor("+invalid+")")
	}
}

func assertPanicsMessage(t *testing.T, fn func(), substring, context string) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("%s: expected panic", context)
		}
		message, ok := recovered.(string)
		if !ok {
			if err, isErr := recovered.(error); isErr {
				message = err.Error()
			} else {
				t.Fatalf("%s: panic value is %v", context, recovered)
			}
		}
		if !strings.Contains(message, substring) {
			t.Fatalf("%s: panic message %q does not contain %q", context, message, substring)
		}
	}()
	fn()
}

func TestColorToRgbGamutMapsOklchIncludingLightnessLimits(t *testing.T) {
	cases := []struct {
		color OklchColorValue
		want  RgbColor
	}{
		{OklchColorAt(0.627955, 0.257683, 29.2339), RgbColor{R: 255, G: 0, B: 0}},
		{OklchColorAt(1, 0.3, 150), RgbColor{R: 255, G: 255, B: 255}},
		{OklchColorAt(0, 0.3, 150), RgbColor{R: 0, G: 0, B: 0}},
	}
	for _, tc := range cases {
		if got := ColorToRgb(tc.color); got != tc.want {
			t.Fatalf("ColorToRgb(%#v) = %#v, want %#v", tc.color, got, tc.want)
		}
	}
}

func TestStyleTextClosesSequencesInReverseOrder(t *testing.T) {
	got := StyleText("Ready", TextStyle{
		TextAttributes: TextAttributes{Bold: true, Italic: true},
		Fg:             RgbColorAt(18, 52, 86),
		Bg:             IndexedColorAt(9),
	}, TerminalColorModeTrueColor)
	want := "\x1b[38;2;18;52;86m\x1b[48;5;9m\x1b[1m\x1b[3mReady\x1b[23m\x1b[22m\x1b[49m\x1b[39m"
	if got != want {
		t.Fatalf("StyleText = %q, want %q", got, want)
	}

	mode256 := StyleText("Ready", TextStyle{Fg: RgbColorAt(18, 52, 86)}, TerminalColorMode256)
	if !strings.HasPrefix(mode256, "\x1b[38;5;") || !strings.HasSuffix(mode256, "Ready\x1b[39m") {
		t.Fatalf("StyleText 256color = %q", mode256)
	}
}

func TestParseOsc11BackgroundColor(t *testing.T) {
	if got := ParseOsc11BackgroundColor("\x1b]11;rgb:0000/8000/ffff\x07"); got == nil || *got != (RgbColor{R: 0, G: 128, B: 255}) {
		t.Fatalf("16-bit rgb reply = %#v", got)
	}
	if got := ParseOsc11BackgroundColor("\x1b]11;#ffffff\x1b\\"); got == nil || *got != (RgbColor{R: 255, G: 255, B: 255}) {
		t.Fatalf("hex reply (ST terminator) = %#v", got)
	}
	if got := ParseOsc11BackgroundColor("\x1b]11;#000000\x07"); got == nil || *got != (RgbColor{R: 0, G: 0, B: 0}) {
		t.Fatalf("hex reply (BEL terminator) = %#v", got)
	}
	for _, reject := range []string{"x\x1b]11;#ffffff\x07", "\x1b]10;#ffffff\x07", "\x1b]11;#ffffff\x07x"} {
		if got := ParseOsc11BackgroundColor(reject); got != nil {
			t.Fatalf("ParseOsc11BackgroundColor(%q) = %#v, want nil", reject, got)
		}
	}
}

func TestParseTerminalColorSchemeReport(t *testing.T) {
	cases := []struct {
		data string
		want TerminalColorScheme
	}{
		{"\x1b[?997;1n", TerminalColorSchemeDark},
		{"\x1b[?997;2n", TerminalColorSchemeLight},
		{"\x1b[?997;2n\x1b[?997;1n\x1b[?997;1n", TerminalColorSchemeDark},
		{"\x1b[?997;1n\x1b[?997;2n\x1b[?997;2n", TerminalColorSchemeLight},
		{"\x1b[?997;3n", ""},
		{"\x1b[?996n", ""},
		{"x\x1b[?997;1n", ""},
	}
	for _, tc := range cases {
		if got := ParseTerminalColorSchemeReport(tc.data); got != tc.want {
			t.Fatalf("ParseTerminalColorSchemeReport(%q) = %q, want %q", tc.data, got, tc.want)
		}
	}
}
