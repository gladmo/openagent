package tui

import (
	"os"
	"strings"
	"testing"
)

// Ported from pi/packages/tui/test/terminal-image.test.ts (pure functions;
// the Image component cases land with the components port).

func withEnvVars(t *testing.T, vars map[string]string, fn func()) {
	t.Helper()
	saved := map[string]string{}
	for name := range vars {
		saved[name] = os.Getenv(name)
		os.Unsetenv(name)
	}
	for name, value := range vars {
		if value == "" {
			os.Unsetenv(name)
		} else {
			os.Setenv(name, value)
		}
	}
	defer func() {
		for name, value := range saved {
			if value == "" {
				os.Unsetenv(name)
			} else {
				os.Setenv(name, value)
			}
		}
	}()
	fn()
}

func TestIsImageLine(t *testing.T) {
	iterm := "\x1b]1337;File=inline=1;size=3:AAAA\x07"
	kitty := "\x1b_Ga=T,f=100,q=2;AAAA\x1b\\"

	positives := []string{
		iterm,
		"text before " + iterm,
		strings.Repeat("x", 100000) + iterm,
		kitty,
		"text " + kitty + " padding",
		kitty + iterm,
		"\x1b[31m" + kitty,
		kitty + "\x1b[0m",
	}
	for _, line := range positives {
		if !IsImageLine(line) {
			t.Errorf("IsImageLine should detect image in %q...", truncate(line, 40))
		}
	}

	negatives := []string{
		"",
		"plain text",
		"\x1b[31mstyled\x1b[0m",
		"\x1b[2A\x1b[K",
		"Some text with ]1337;File but missing ESC at start",
		"Some text with _G but missing ESC at start",
		"\n\n",
		"/path/to/1337/File=",
	}
	for _, line := range negatives {
		if IsImageLine(line) {
			t.Errorf("IsImageLine false positive on %q", truncate(line, 40))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func TestDetectCapabilitiesEnvironment(t *testing.T) {
	ResetCapabilitiesCache()
	defer ResetCapabilitiesCache()

	withEnvVars(t, map[string]string{
		"TERM_PROGRAM": "", "TERM": "xterm-256color", "COLORTERM": "",
		"TMUX": "", "KITTY_WINDOW_ID": "", "GHOSTTY_RESOURCES_DIR": "",
		"WEZTERM_PANE": "", "ITERM_SESSION_ID": "", "WT_SESSION": "",
		"WARP_SESSION_ID": "", "WARP_TERMINAL_SESSION_UUID": "",
		"TERMINAL_EMULATOR": "", "SSH_CONNECTION": "", "SSH_TTY": "",
		"PI_HYPERLINKS": "", "PI_IMAGE_PROTOCOL": "", "PI_TRUE_COLOR": "",
	}, func() {
		caps := DetectCapabilities(func() bool { return false })
		if caps.Hyperlinks {
			t.Fatal("unknown terminals default hyperlinks off")
		}
		if caps.Images != ImageProtocolNone {
			t.Fatalf("images = %q", caps.Images)
		}

		// Ghostty
		withEnvVars(t, map[string]string{"TERM_PROGRAM": "ghostty"}, func() {
			caps := DetectCapabilities(func() bool { return false })
			if !caps.Hyperlinks || caps.Images != ImageProtocolKitty {
				t.Fatalf("ghostty: %+v", caps)
			}
		})

		// Kitty
		withEnvVars(t, map[string]string{"KITTY_WINDOW_ID": "1"}, func() {
			caps := DetectCapabilities(func() bool { return false })
			if caps.Images != ImageProtocolKitty || !caps.TrueColor {
				t.Fatalf("kitty: %+v", caps)
			}
		})

		// tmux forwarding on/off
		withEnvVars(t, map[string]string{"TMUX": "/tmp/tmux-0/default"}, func() {
			caps := DetectCapabilities(func() bool { return true })
			if !caps.Hyperlinks || caps.Images != ImageProtocolNone {
				t.Fatalf("tmux forwards: %+v", caps)
			}
			caps = DetectCapabilities(func() bool { return false })
			if caps.Hyperlinks {
				t.Fatal("tmux not forwarding should disable hyperlinks")
			}
		})

		// screen forces hyperlinks off
		withEnvVars(t, map[string]string{"TERM": "screen-256color"}, func() {
			caps := DetectCapabilities(func() bool { return true })
			if caps.Hyperlinks {
				t.Fatal("screen must disable hyperlinks")
			}
		})

		// Warp via TERM_PROGRAM
		withEnvVars(t, map[string]string{"TERM_PROGRAM": "WarpTerminal"}, func() {
			caps := DetectCapabilities(func() bool { return false })
			if caps.Images != ImageProtocolKitty || !caps.TrueColor || !caps.Hyperlinks {
				t.Fatalf("warp: %+v", caps)
			}
		})
	})
}

func TestDetectCapabilitiesOverrides(t *testing.T) {
	withEnvVars(t, map[string]string{
		"TERM_PROGRAM": "", "TERM": "xterm", "COLORTERM": "",
		"TMUX": "", "PI_HYPERLINKS": "", "PI_IMAGE_PROTOCOL": "", "PI_TRUE_COLOR": "",
	}, func() {
		// Environment overrides
		withEnvVars(t, map[string]string{"PI_HYPERLINKS": "1", "PI_IMAGE_PROTOCOL": "kitty", "PI_TRUE_COLOR": "1"}, func() {
			caps := DetectCapabilities(func() bool { return false })
			if !caps.Hyperlinks || caps.Images != ImageProtocolKitty || !caps.TrueColor {
				t.Fatalf("env overrides: %+v", caps)
			}
		})
		withEnvVars(t, map[string]string{"PI_IMAGE_PROTOCOL": "0"}, func() {
			caps := DetectCapabilities(func() bool { return true })
			if caps.Images != ImageProtocolNone {
				t.Fatalf("none override: %+v", caps)
			}
		})

		// PI_HYPERLINKS bypasses the tmux probe
		probed := false
		withEnvVars(t, map[string]string{"TMUX": "1", "PI_HYPERLINKS": "1", "KITTY_WINDOW_ID": ""}, func() {
			caps := DetectCapabilities(func() bool { probed = true; return false })
			if probed {
				t.Fatal("probe ran despite override")
			}
			if !caps.Hyperlinks {
				t.Fatalf("override ignored: %+v", caps)
			}
		})

		// Programmatic overrides (clean up for -count reruns: package state
		// persists across test reruns in one process)
		ResetCapabilitiesCache()
		SetCapabilityOverrides(TerminalCapabilitiesOverrides{})
		defer func() {
			ResetCapabilitiesCache()
			SetCapabilityOverrides(TerminalCapabilitiesOverrides{})
		}()
		caps := GetCapabilities()
		if caps.Hyperlinks || caps.Images != ImageProtocolNone {
			t.Fatalf("baseline: %+v", caps)
		}
		kittyProtocol := ImageProtocolKitty
		tr := true
		SetCapabilityOverrides(TerminalCapabilitiesOverrides{Images: &kittyProtocol, TrueColor: &tr, Hyperlinks: &tr})
		caps = GetCapabilities()
		if caps.Images != ImageProtocolKitty || !caps.TrueColor || !caps.Hyperlinks {
			t.Fatalf("overrides not applied: %+v", caps)
		}
	})
}

func TestEncodeITerm2SizeMetadata(t *testing.T) {
	width := 2
	sequence := EncodeITerm2("AAAA", EncodeITerm2Options{Width: width, Height: "auto"})
	want := "\x1b]1337;File=inline=1;size=3;width=2;height=auto:AAAA\x07"
	if sequence != want {
		t.Fatalf("got %q, want %q", sequence, want)
	}
}

func TestEncodeKittyCursorMovement(t *testing.T) {
	columns, rows := 2, 2
	moveCursor := false
	sequence := EncodeKitty("AAAA", EncodeKittyOptions{Columns: &columns, Rows: &rows, MoveCursor: &moveCursor})
	if !strings.HasPrefix(sequence, "\x1b_Ga=T,f=100,q=2,C=1,c=2,r=2;") {
		t.Fatalf("got %q", sequence)
	}

	if got := DeleteKittyImage(42); got != "\x1b_Ga=d,d=I,i=42,q=2\x1b\\" {
		t.Fatalf("delete: %q", got)
	}
	if got := DeleteAllKittyImages(); got != "\x1b_Ga=d,d=A,q=2\x1b\\" {
		t.Fatalf("delete all: %q", got)
	}
	if got := DeleteAllKittyPlacements(); got != "\x1b_Ga=d,d=a,q=2\x1b\\" {
		t.Fatalf("delete placements: %q", got)
	}
}

func TestRenderImageCellSizing(t *testing.T) {
	savedCaps := GetCapabilities()
	savedCell := GetCellDimensions()
	defer func() {
		SetCapabilities(savedCaps)
		SetCellDimensions(savedCell)
	}()

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})

	SetCellDimensions(CellDimensions{WidthPx: 9, HeightPx: 18})
	result := RenderImage("AAAA", ImageDimensions{WidthPx: 1200, HeightPx: 12}, ImageRenderOptions{})
	if result == nil {
		t.Fatal("thin image not rendered")
	}
	if result.Rows != 1 {
		t.Fatalf("thin image rows = %d, want 1", result.Rows)
	}
	if !strings.Contains(result.Sequence, ",c=80,r=1;") {
		t.Fatalf("sequence params: %q", result.Sequence)
	}

	SetCellDimensions(CellDimensions{WidthPx: 15, HeightPx: 28})
	maxWidth := 60
	result = RenderImage("AAAA", ImageDimensions{WidthPx: 615, HeightPx: 86}, ImageRenderOptions{MaxWidthCells: &maxWidth})
	if result == nil || result.Rows != 5 {
		t.Fatalf("ceiling placement: %+v", result)
	}
	if !strings.Contains(result.Sequence, ",c=60,r=5;") {
		t.Fatalf("sequence params: %q", result.Sequence)
	}
}

func TestImageFallback(t *testing.T) {
	savedCaps := GetCapabilities()
	defer SetCapabilities(savedCaps)

	home, _ := os.UserHomeDir()
	abs := home + "/.pi/agent/shot.png"

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolNone, TrueColor: false, Hyperlinks: false})
	if got := ImageFallback("image/png", &ImageDimensions{WidthPx: 1280, HeightPx: 720}, abs); got != "[Image: ~/.pi/agent/shot.png [image/png] 1280x720]" {
		t.Fatalf("no hyperlinks: %q", got)
	}

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolNone, TrueColor: false, Hyperlinks: true})
	got := ImageFallback("image/png", &ImageDimensions{WidthPx: 10, HeightPx: 10}, abs)
	if !strings.Contains(got, "\x1b]8;;file://") {
		t.Fatalf("missing OSC 8 file link: %q", got)
	}
	visible := regexpOsc8Strip(got)
	if visible != "[Image: ~/.pi/agent/shot.png [image/png] 10x10]" {
		t.Fatalf("visible text: %q", visible)
	}

	if got := ImageFallback("image/png", &ImageDimensions{WidthPx: 1, HeightPx: 1}, "clankolas.png"); got != "[Image: clankolas.png [image/png] 1x1]" {
		t.Fatalf("basename: %q", got)
	}

	if got := ImageFallback("image/png", &ImageDimensions{WidthPx: 8, HeightPx: 6}, ""); got != "[Image: [image/png] 8x6]" {
		t.Fatalf("no filename: %q", got)
	}
}

func regexpOsc8Strip(s string) string {
	runes := []rune(s)
	var out []rune
	i := 0
	for i < len(runes) {
		if code, n := extractAnsiCodeAt(runes, i); n > 0 && strings.HasPrefix(code, "\x1b]8;;") {
			i += n
			continue
		}
		out = append(out, runes[i])
		i++
	}
	return string(out)
}

func TestHyperlinkWrapping(t *testing.T) {
	if got := Hyperlink("click me", "https://example.com"); got != "\x1b]8;;https://example.com\x1b\\click me\x1b]8;;\x1b\\" {
		t.Fatalf("got %q", got)
	}
	if got := Hyperlink("", "https://example.com"); got != "\x1b]8;;https://example.com\x1b\\\x1b]8;;\x1b\\" {
		t.Fatalf("empty text: %q", got)
	}
	got := Hyperlink("README.md", "file:///home/user/README.md")
	if !strings.Contains(got, "file:///home/user/README.md") || !strings.Contains(got, "README.md") {
		t.Fatalf("file URL: %q", got)
	}
}
