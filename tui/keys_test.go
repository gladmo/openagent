package tui

import (
	"testing"
)

// Ported from pi/packages/tui/test/keys.test.ts.

func withKitty(active bool, fn func()) {
	prev := kittyProtocolActive
	SetKittyProtocolActive(active)
	defer SetKittyProtocolActive(prev)
	fn()
}

func assertMatch(t *testing.T, data string, keyId KeyId, want bool) {
	t.Helper()
	if got := MatchesKey(data, keyId); got != want {
		t.Errorf("MatchesKey(%q, %q) = %v, want %v", data, keyId, got, want)
	}
}

func assertParse(t *testing.T, data string, want string) {
	t.Helper()
	got, ok := ParseKey(data)
	if !ok || got != want {
		t.Errorf("ParseKey(%q) = %q(ok=%v), want %q", data, got, ok, want)
	}
}

func assertParseUndefined(t *testing.T, data string) {
	t.Helper()
	if got, ok := ParseKey(data); ok {
		t.Errorf("ParseKey(%q) = %q, want undefined", data, got)
	}
}

func TestMatchesKeyKittyAlternateKeys(t *testing.T) {
	withKitty(true, func() {
		// Kitty flag 4: CSI codepoint:shifted:base;modifier:event u
		assertMatch(t, "\x1b[1089::99;5u", "ctrl+c", true)  // Cyrillic с + base c
		assertMatch(t, "\x1b[1074::100;5u", "ctrl+d", true) // Cyrillic в + base d
		assertMatch(t, "\x1b[1103::122;5u", "ctrl+z", true) // Cyrillic я + base z
		assertMatch(t, "\x1b[1079::112;6u", "ctrl+shift+p", true)
		assertMatch(t, "\x1b[99;5u", "ctrl+c", true)        // no base layout key
		assertMatch(t, "\x1b[1089::99;5u", "ctrl+d", false) // wrong key with base
		assertMatch(t, "\x1b[1089::99;5u", "ctrl+shift+c", false)
		assertMatch(t, "\x1b[99:67:99;2u", "shift+c", true)            // shifted key present
		assertMatch(t, "\x1b[1089::99;5:3u", "ctrl+c", true)           // release event
		assertMatch(t, "\x1b[1089:1057:99;6:2u", "ctrl+shift+c", true) // full format
		assertMatch(t, "\x1b[107::118;5u", "ctrl+k", true)             // Dvorak: prefer codepoint
		assertMatch(t, "\x1b[107::118;5u", "ctrl+v", false)
		assertMatch(t, "\x1b[47::91;5u", "ctrl+/", true) // Dvorak symbols
		assertMatch(t, "\x1b[47::91;5u", "ctrl+[", false)
	})

	withKitty(true, func() {
		assertMatch(t, "\x1b[107;9u", "super+k", true)
		assertMatch(t, "\x1b[13;9u", "super+enter", true)
		assertMatch(t, "\x1b[107;13u", KeyCtrlSuper("k"), true)
		assertMatch(t, "\x1b[107;13u", "ctrl+super+k", true)
		assertMatch(t, "\x1b[107;14u", "ctrl+shift+super+k", true)
		assertMatch(t, "\x1b[107;13u", "super+k", false)
		assertParse(t, "\x1b[107;9u", "super+k")
		assertParse(t, "\x1b[13;9u", "super+enter")
		assertParse(t, "\x1b[107;13u", "ctrl+super+k")
		assertParse(t, "\x1b[107;14u", "shift+ctrl+super+k")
	})

	withKitty(true, func() {
		assertMatch(t, "\x1b[49u", "1", true)
		assertMatch(t, "\x1b[49;5u", "ctrl+1", true)
		assertMatch(t, "\x1b[49;5u", "ctrl+2", false)
		assertParse(t, "\x1b[49u", "1")
		assertParse(t, "\x1b[49;5u", "ctrl+1")
	})

	withKitty(true, func() {
		// Kitty keypad functional keys normalize to logical keys
		assertMatch(t, "\x1b[57400u", "1", true)
		assertMatch(t, "\x1b[57410u", "/", true)
		assertMatch(t, "\x1b[57417u", "left", true)
		assertMatch(t, "\x1b[57426u", "delete", true)
		assertParse(t, "\x1b[57399u", "0")
		assertParse(t, "\x1b[57409u", ".")
		assertParse(t, "\x1b[57413u", "+")
		assertParse(t, "\x1b[57416u", ",")
		assertParse(t, "\x1b[57417u", "left")
		assertParse(t, "\x1b[57418u", "right")
		assertParse(t, "\x1b[57419u", "up")
		assertParse(t, "\x1b[57420u", "down")
		assertParse(t, "\x1b[57421u", "pageUp")
		assertParse(t, "\x1b[57422u", "pageDown")
		assertParse(t, "\x1b[57423u", "home")
		assertParse(t, "\x1b[57424u", "end")
		assertParse(t, "\x1b[57425u", "insert")
		assertParse(t, "\x1b[57426u", "delete")
	})
}

func TestMatchesKeyModifyOtherKeys(t *testing.T) {
	withKitty(false, func() {
		assertMatch(t, "\x1b[27;5;99~", "ctrl+c", true)
		assertParse(t, "\x1b[27;5;99~", "ctrl+c")
		assertMatch(t, "\x1b[27;5;100~", "ctrl+d", true)
		assertParse(t, "\x1b[27;5;100~", "ctrl+d")
		assertMatch(t, "\x1b[27;5;122~", "ctrl+z", true)
		assertParse(t, "\x1b[27;5;122~", "ctrl+z")

		assertMatch(t, "\x1b[27;5;13~", "ctrl+enter", true)
		assertMatch(t, "\x1b[27;2;13~", "shift+enter", true)
		assertMatch(t, "\x1b[27;3;13~", "alt+enter", true)
		assertParse(t, "\x1b[27;5;13~", "ctrl+enter")
		assertParse(t, "\x1b[27;2;13~", "shift+enter")
		assertParse(t, "\x1b[27;3;13~", "alt+enter")

		assertMatch(t, "\x1b[27;2;9~", "shift+tab", true)
		assertMatch(t, "\x1b[27;5;9~", "ctrl+tab", true)
		assertMatch(t, "\x1b[27;3;9~", "alt+tab", true)
		assertParse(t, "\x1b[27;2;9~", "shift+tab")
		assertParse(t, "\x1b[27;5;9~", "ctrl+tab")
		assertParse(t, "\x1b[27;3;9~", "alt+tab")

		assertMatch(t, "\x1b[27;1;127~", "backspace", true)
		assertMatch(t, "\x1b[27;5;127~", "ctrl+backspace", true)
		assertMatch(t, "\x1b[27;3;127~", "alt+backspace", true)
		assertParse(t, "\x1b[27;1;127~", "backspace")
		assertParse(t, "\x1b[27;5;127~", "ctrl+backspace")
		assertParse(t, "\x1b[27;3;127~", "alt+backspace")

		assertMatch(t, "\x1b[27;1;27~", "escape", true)
		assertParse(t, "\x1b[27;1;27~", "escape")

		assertMatch(t, "\x1b[27;1;32~", "space", true)
		assertMatch(t, "\x1b[27;5;32~", "ctrl+space", true)
		assertParse(t, "\x1b[27;1;32~", "space")
		assertParse(t, "\x1b[27;5;32~", "ctrl+space")

		assertMatch(t, "\x1b[27;5;47~", "ctrl+/", true)
		assertParse(t, "\x1b[27;5;47~", "ctrl+/")

		assertMatch(t, "\x1b[27;5;49~", "ctrl+1", true)
		assertMatch(t, "\x1b[27;2;49~", "shift+1", true)
		assertParse(t, "\x1b[27;5;49~", "ctrl+1")
		assertParse(t, "\x1b[27;2;49~", "shift+1")

		assertMatch(t, "\x1b[27;2;69~", "shift+e", true)
		assertMatch(t, "\x1b[27;6;69~", "ctrl+shift+e", true)
		assertParse(t, "\x1b[27;2;69~", "shift+e")
		assertParse(t, "\x1b[27;6;69~", "shift+ctrl+e")

		assertMatch(t, "\x1b[104;7u", "ctrl+alt+h", true)
		assertParse(t, "\x1b[104;7u", "ctrl+alt+h")
		assertMatch(t, "\x1b[27;7;104~", "ctrl+alt+h", true)
		assertParse(t, "\x1b[27;7;104~", "ctrl+alt+h")
	})
}

func TestMatchesKeyLegacy(t *testing.T) {
	withKitty(false, func() {
		assertMatch(t, "\x03", "ctrl+c", true)
		assertMatch(t, "\x04", "ctrl+d", true)
	})
	assertMatch(t, "\x1b", "escape", true)

	withKitty(false, func() {
		assertMatch(t, "\n", "enter", true)
		assertParse(t, "\n", "enter")
	})
	withKitty(true, func() {
		assertMatch(t, "\n", "shift+enter", true)
		assertMatch(t, "\n", "enter", false)
		assertParse(t, "\n", "shift+enter")
	})

	withKitty(false, func() {
		assertMatch(t, "\x00", "ctrl+space", true)
		assertParse(t, "\x00", "ctrl+space")

		assertMatch(t, "\x1c", "ctrl+\\", true)
		assertParse(t, "\x1c", "ctrl+\\")
		assertMatch(t, "\x1d", "ctrl+]", true)
		assertParse(t, "\x1d", "ctrl+]")
		assertMatch(t, "\x1f", "ctrl+_", true)
		assertMatch(t, "\x1f", "ctrl+-", true)
		assertParse(t, "\x1f", "ctrl+-")

		assertMatch(t, "\x1b\x1b", "ctrl+alt+[", true)
		assertParse(t, "\x1b\x1b", "ctrl+alt+[")
		assertMatch(t, "\x1b\x1c", "ctrl+alt+\\", true)
		assertParse(t, "\x1b\x1c", "ctrl+alt+\\")
		assertMatch(t, "\x1b\x1d", "ctrl+alt+]", true)
		assertParse(t, "\x1b\x1d", "ctrl+alt+]")
		assertMatch(t, "\x1b\x1f", "ctrl+alt+_", true)
		assertMatch(t, "\x1b\x1f", "ctrl+alt+-", true)
		assertParse(t, "\x1b\x1f", "ctrl+alt+-")
	})

	assertMatch(t, "\x1b[A", "up", true)
	assertMatch(t, "\x1b[B", "down", true)
	assertMatch(t, "\x1b[C", "right", true)
	assertMatch(t, "\x1b[D", "left", true)

	assertMatch(t, "\x1bOA", "up", true)
	assertMatch(t, "\x1bOB", "down", true)
	assertMatch(t, "\x1bOC", "right", true)
	assertMatch(t, "\x1bOD", "left", true)
	assertMatch(t, "\x1bOH", "home", true)
	assertMatch(t, "\x1bOF", "end", true)

	assertMatch(t, "\x1b[1;5H", "ctrl+home", true)
	assertMatch(t, "\x1b[1;5F", "ctrl+end", true)
	assertMatch(t, "\x1b[5;5~", "ctrl+pageUp", true)
	assertMatch(t, "\x1b[6;5~", "ctrl+pageDown", true)
	assertParse(t, "\x1b[1;5H", "ctrl+home")
	assertParse(t, "\x1b[1;5F", "ctrl+end")
	assertParse(t, "\x1b[5;5~", "ctrl+pageUp")
	assertParse(t, "\x1b[6;5~", "ctrl+pageDown")

	assertMatch(t, "\x1bOP", "f1", true)
	assertMatch(t, "\x1b[24~", "f12", true)
	assertMatch(t, "\x1b[E", "clear", true)

	assertMatch(t, "\x1bp", "alt+up", true)
	assertMatch(t, "\x1bp", "up", false)

	assertMatch(t, "\x1b[a", "shift+up", true)
	assertMatch(t, "\x1bOa", "ctrl+up", true)
	assertMatch(t, "\x1b[2$", "shift+insert", true)
	assertMatch(t, "\x1b[2^", "ctrl+insert", true)
	assertMatch(t, "\x1b[7$", "shift+home", true)

	assertParse(t, "\x1b[[5~", "pageUp")
}

func TestMatchesKeyRawBackspaceEnv(t *testing.T) {
	restore := stashEnv("WT_SESSION", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY")
	defer restore()

	withKitty(false, func() {
		// Outside Windows Terminal
		osUnsetenvForTest("WT_SESSION")
		osUnsetenvForTest("SSH_CONNECTION")
		osUnsetenvForTest("SSH_CLIENT")
		osUnsetenvForTest("SSH_TTY")
		assertMatch(t, "\x7f", "backspace", true)
		assertMatch(t, "\x7f", "ctrl+backspace", false)
		assertParse(t, "\x7f", "backspace")
		assertMatch(t, "\x08", "backspace", true)
		assertMatch(t, "\x08", "ctrl+backspace", false)
		assertParse(t, "\x08", "backspace")
		assertMatch(t, "\x08", "ctrl+h", true)

		// Local Windows Terminal
		osSetenvForTest("WT_SESSION", "test-session")
		assertMatch(t, "\x08", "ctrl+backspace", true)
		assertMatch(t, "\x08", "backspace", false)
		assertParse(t, "\x08", "ctrl+backspace")
		assertMatch(t, "\x08", "ctrl+h", true)

		// Windows Terminal over SSH
		osSetenvForTest("SSH_CONNECTION", "1 2 3 4")
		osSetenvForTest("SSH_CLIENT", "1 2 3")
		osSetenvForTest("SSH_TTY", "/dev/pts/1")
		assertMatch(t, "\x08", "ctrl+backspace", false)
		assertMatch(t, "\x08", "backspace", true)
		assertParse(t, "\x08", "backspace")
		assertMatch(t, "\x08", "ctrl+h", true)
	})
}

func TestMatchesKeyLegacyAltPrefixed(t *testing.T) {
	withKitty(false, func() {
		assertMatch(t, "\x1b ", "alt+space", true)
		assertParse(t, "\x1b ", "alt+space")
		assertMatch(t, "\x1b\b", "alt+backspace", true)
		assertParse(t, "\x1b\b", "alt+backspace")
		assertMatch(t, "\x1b\x03", "ctrl+alt+c", true)
		assertParse(t, "\x1b\x03", "ctrl+alt+c")
		assertMatch(t, "\x1bB", "alt+left", true)
		assertParse(t, "\x1bB", "alt+left")
		assertMatch(t, "\x1bF", "alt+right", true)
		assertParse(t, "\x1bF", "alt+right")
		assertMatch(t, "\x1ba", "alt+a", true)
		assertParse(t, "\x1ba", "alt+a")
		assertMatch(t, "\x1b1", "alt+1", true)
		assertParse(t, "\x1b1", "alt+1")
		assertMatch(t, "\x1b,", "alt+,", true)
		assertParse(t, "\x1b,", "alt+,")
		assertMatch(t, "\x1b.", "alt+.", true)
		assertParse(t, "\x1b.", "alt+.")
		assertMatch(t, "\x1by", "alt+y", true)
		assertParse(t, "\x1by", "alt+y")
		assertMatch(t, "\x1bz", "alt+z", true)
		assertParse(t, "\x1bz", "alt+z")
	})

	withKitty(true, func() {
		assertMatch(t, "\x1b ", "alt+space", false)
		assertParseUndefined(t, "\x1b ")
		assertMatch(t, "\x1b\b", "alt+backspace", true)
		assertParse(t, "\x1b\b", "alt+backspace")
		assertMatch(t, "\x1b\x03", "ctrl+alt+c", false)
		assertParseUndefined(t, "\x1b\x03")
		assertMatch(t, "\x1bB", "alt+left", false)
		assertParseUndefined(t, "\x1bB")
		assertMatch(t, "\x1bF", "alt+right", false)
		assertParseUndefined(t, "\x1bF")
		assertMatch(t, "\x1ba", "alt+a", false)
		assertParseUndefined(t, "\x1ba")
		assertMatch(t, "\x1b1", "alt+1", false)
		assertParseUndefined(t, "\x1b1")
		assertMatch(t, "\x1b,", "alt+,", false)
		assertParseUndefined(t, "\x1b,")
		assertMatch(t, "\x1b.", "alt+.", false)
		assertParseUndefined(t, "\x1b.")
		assertMatch(t, "\x1by", "alt+y", false)
		assertParseUndefined(t, "\x1by")
	})
}

func TestDecodeKittyPrintable(t *testing.T) {
	cases := []struct {
		data string
		want string
	}{
		{"\x1b[57399u", "0"},
		{"\x1b[57400u", "1"},
		{"\x1b[57409u", "."},
		{"\x1b[57410u", "/"},
		{"\x1b[57411u", "*"},
		{"\x1b[57412u", "-"},
		{"\x1b[57413u", "+"},
		{"\x1b[57415u", "="},
		{"\x1b[57416u", ","},
		{"\x1b[57417u", ""}, // navigation keys are not printable
	}
	for _, tc := range cases {
		got, ok := DecodeKittyPrintable(tc.data)
		if tc.want == "" {
			if ok {
				t.Errorf("DecodeKittyPrintable(%q) = %q, want undefined", tc.data, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("DecodeKittyPrintable(%q) = %q(ok=%v), want %q", tc.data, got, ok, tc.want)
		}
	}
}

func TestDecodePrintableKey(t *testing.T) {
	cases := []struct {
		data string
		want string
	}{
		{"\x1b[27;2;69~", "E"},
		{"\x1b[27;2;196~", "Ä"},
		{"\x1b[27;2;32~", " "},
		{"\x1b[27;2;13~", ""},
		{"\x1b[27;6;69~", ""},
	}
	for _, tc := range cases {
		got, ok := DecodePrintableKey(tc.data)
		if tc.want == "" {
			if ok {
				t.Errorf("DecodePrintableKey(%q) = %q, want undefined", tc.data, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("DecodePrintableKey(%q) = %q(ok=%v), want %q", tc.data, got, ok, tc.want)
		}
	}
}

func TestParseKeyKittyAlternateKeys(t *testing.T) {
	withKitty(true, func() {
		assertParse(t, "\x1b[1089::99;5u", "ctrl+c")
		assertParse(t, "\x1b[107::118;5u", "ctrl+k")
		assertParse(t, "\x1b[47::91;5u", "ctrl+/")
		assertParse(t, "\x1b[99;5u", "ctrl+c")
		assertMatch(t, "\x1b[69;2u", "shift+e", true)
		assertParse(t, "\x1b[69;2u", "shift+e")
		assertParseUndefined(t, "\x1b[99;17u")
	})
}

func TestParseKeyLegacy(t *testing.T) {
	withKitty(false, func() {
		assertParse(t, "\x03", "ctrl+c")
		assertParse(t, "\x04", "ctrl+d")

		assertParse(t, "\x1b", "escape")
		assertParse(t, "\t", "tab")
		assertParse(t, "\r", "enter")
		assertParse(t, "\n", "enter")
		assertParse(t, "\x00", "ctrl+space")
		assertParse(t, " ", "space")
		assertParse(t, "1", "1")
		assertMatch(t, "1", "1", true)

		assertParse(t, "\x1b[A", "up")
		assertParse(t, "\x1b[B", "down")
		assertParse(t, "\x1b[C", "right")
		assertParse(t, "\x1b[D", "left")

		assertParse(t, "\x1bOA", "up")
		assertParse(t, "\x1bOB", "down")
		assertParse(t, "\x1bOC", "right")
		assertParse(t, "\x1bOD", "left")
		assertParse(t, "\x1bOH", "home")
		assertParse(t, "\x1bOF", "end")

		assertParse(t, "\x1bOP", "f1")
		assertParse(t, "\x1b[24~", "f12")
		assertParse(t, "\x1b[E", "clear")
		assertParse(t, "\x1b[2^", "ctrl+insert")
		assertParse(t, "\x1bp", "alt+up")
	})
}

// stashEnv saves the current values of the named env vars and returns a
// restore closure.
func stashEnv(names ...string) func() {
	saved := map[string]string{}
	for _, name := range names {
		saved[name] = osGetenvForTest(name)
	}
	return func() {
		for name, value := range saved {
			if value == "" {
				osUnsetenvForTest(name)
			} else {
				osSetenvForTest(name, value)
			}
		}
	}
}
