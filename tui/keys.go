package tui

// Keyboard input handling for terminal applications: legacy terminal
// sequences and the Kitty keyboard protocol. Port of keys.ts, which follows
// https://sw.kovidgoyal.net/kitty/keyboard-protocol/ (legacy ctrl mapping of
// ASCII keys overlaps, e.g. ctrl+[ = ESC; those still work for ctrl+shift
// combos).

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// KeyId is a key identifier: a base key ("escape", "a", "/") optionally
// prefixed with "+"-joined modifiers ("ctrl+shift+p").
type KeyId = string

// Global Kitty protocol state, set by ProcessTerminal after detecting support.
var kittyProtocolActive = false

// SetKittyProtocolActive sets the global Kitty keyboard protocol state.
func SetKittyProtocolActive(active bool) { kittyProtocolActive = active }

// IsKittyProtocolActive queries whether the Kitty protocol is active.
func IsKittyProtocolActive() bool { return kittyProtocolActive }

// Key identifiers, mirroring the TS Key helper object.
const (
	KeyEscape    KeyId = "escape"
	KeyEsc       KeyId = "esc"
	KeyEnter     KeyId = "enter"
	KeyReturn    KeyId = "return"
	KeyTab       KeyId = "tab"
	KeySpace     KeyId = "space"
	KeyBackspace KeyId = "backspace"
	KeyDelete    KeyId = "delete"
	KeyInsert    KeyId = "insert"
	KeyClear     KeyId = "clear"
	KeyHome      KeyId = "home"
	KeyEnd       KeyId = "end"
	KeyPageUp    KeyId = "pageUp"
	KeyPageDown  KeyId = "pageDown"
	KeyUp        KeyId = "up"
	KeyDown      KeyId = "down"
	KeyLeft      KeyId = "left"
	KeyRight     KeyId = "right"
	KeyF1        KeyId = "f1"
	KeyF2        KeyId = "f2"
	KeyF3        KeyId = "f3"
	KeyF4        KeyId = "f4"
	KeyF5        KeyId = "f5"
	KeyF6        KeyId = "f6"
	KeyF7        KeyId = "f7"
	KeyF8        KeyId = "f8"
	KeyF9        KeyId = "f9"
	KeyF10       KeyId = "f10"
	KeyF11       KeyId = "f11"
	KeyF12       KeyId = "f12"

	KeyBacktick     KeyId = "`"
	KeyHyphen       KeyId = "-"
	KeyEquals       KeyId = "="
	KeyLeftbracket  KeyId = "["
	KeyRightbracket KeyId = "]"
	KeyBackslash    KeyId = "\\"
	KeySemicolon    KeyId = ";"
	KeyQuote        KeyId = "'"
	KeyComma        KeyId = ","
	KeyPeriod       KeyId = "."
	KeySlash        KeyId = "/"
	KeyExclamation  KeyId = "!"
	KeyAt           KeyId = "@"
	KeyHash         KeyId = "#"
	KeyDollar       KeyId = "$"
	KeyPercent      KeyId = "%"
	KeyCaret        KeyId = "^"
	KeyAmpersand    KeyId = "&"
	KeyAsterisk     KeyId = "*"
	KeyLeftparen    KeyId = "("
	KeyRightparen   KeyId = ")"
	KeyUnderscore   KeyId = "_"
	KeyPlus         KeyId = "+"
	KeyPipe         KeyId = "|"
	KeyTilde        KeyId = "~"
	KeyLeftbrace    KeyId = "{"
	KeyRightbrace   KeyId = "}"
	KeyColon        KeyId = ":"
	KeyLessthan     KeyId = "<"
	KeyGreaterthan  KeyId = ">"
	KeyQuestion     KeyId = "?"
)

// Modifier helper constructors mirroring the TS Key object's methods.
func KeyCtrl(key KeyId) KeyId           { return "ctrl+" + key }
func KeyShift(key KeyId) KeyId          { return "shift+" + key }
func KeyAlt(key KeyId) KeyId            { return "alt+" + key }
func KeySuper(key KeyId) KeyId          { return "super+" + key }
func KeyCtrlShift(key KeyId) KeyId      { return "ctrl+shift+" + key }
func KeyShiftCtrl(key KeyId) KeyId      { return "shift+ctrl+" + key }
func KeyCtrlAlt(key KeyId) KeyId        { return "ctrl+alt+" + key }
func KeyAltCtrl(key KeyId) KeyId        { return "alt+ctrl+" + key }
func KeyShiftAlt(key KeyId) KeyId       { return "shift+alt+" + key }
func KeyAltShift(key KeyId) KeyId       { return "alt+shift+" + key }
func KeyCtrlSuper(key KeyId) KeyId      { return "ctrl+super+" + key }
func KeySuperCtrl(key KeyId) KeyId      { return "super+ctrl+" + key }
func KeyShiftSuper(key KeyId) KeyId     { return "shift+super+" + key }
func KeySuperShift(key KeyId) KeyId     { return "super+shift+" + key }
func KeyAltSuper(key KeyId) KeyId       { return "alt+super+" + key }
func KeySuperAlt(key KeyId) KeyId       { return "super+alt+" + key }
func KeyCtrlShiftAlt(key KeyId) KeyId   { return "ctrl+shift+alt+" + key }
func KeyCtrlShiftSuper(key KeyId) KeyId { return "ctrl+shift+super+" + key }

var symbolKeys = map[rune]bool{
	'`': true, '-': true, '=': true, '[': true, ']': true, '\\': true,
	';': true, '\'': true, ',': true, '.': true, '/': true, '!': true,
	'@': true, '#': true, '$': true, '%': true, '^': true, '&': true,
	'*': true, '(': true, ')': true, '_': true, '+': true, '|': true,
	'~': true, '{': true, '}': true, ':': true, '<': true, '>': true,
	'?': true,
}

// shiftedSymbolKeys are the symbols a physical key produces only with
// Shift: bindings name the produced symbol, so the implicit Shift is
// consumed when matching.
var shiftedSymbolKeys = map[rune]bool{
	'~': true, '!': true, '@': true, '#': true, '$': true, '%': true, '^': true,
	'&': true, '*': true, '(': true, ')': true, '_': true, '+': true, '|': true,
	'{': true, '}': true, ':': true, '<': true, '>': true, '?': true,
}

// logicalShiftedSymbolCandidate treats a shifted symbol (Kitty reports "+"
// as Shift plus the physical "=" key) as the produced symbol without Shift,
// preserving Ctrl, Alt, and Super. allowed picks the vocabulary: Kitty
// reports a shiftedKey for every symbol (full symbol set); modifyOtherKeys
// reports the produced character (only symbols Shift can produce).
func logicalShiftedSymbolCandidate(codepoint, modifier int, allowed map[rune]bool) (int, int, bool) {
	if codepoint == 0 || modifier&modShift == 0 {
		return 0, 0, false
	}
	normalized := normalizeKittyFunctionalCodepoint(codepoint)
	if !allowed[rune(normalized)] {
		return 0, 0, false
	}
	return normalized, modifier &^ modShift, true
}

const (
	modShift = 1
	modAlt   = 2
	modCtrl  = 4
	modSuper = 8
	lockMask = 64 + 128 // Caps Lock + Num Lock
)

const (
	cpEscape    = 27
	cpTab       = 9
	cpEnter     = 13
	cpSpace     = 32
	cpBackspace = 127
	cpKpEnter   = 57414 // Numpad Enter (Kitty protocol)
)

const (
	cpUpArrow    = -1
	cpDownArrow  = -2
	cpRightArrow = -3
	cpLeftArrow  = -4
)

const (
	cpDelete   = -10
	cpInsert   = -11
	cpPageUp   = -12
	cpPageDown = -13
	cpHome     = -14
	cpEnd      = -15
)

var kittyFunctionalKeyEquivalents = map[int]int{
	57399: 48, // KP_0 -> 0
	57400: 49, // KP_1 -> 1
	57401: 50, // KP_2 -> 2
	57402: 51, // KP_3 -> 3
	57403: 52, // KP_4 -> 4
	57404: 53, // KP_5 -> 5
	57405: 54, // KP_6 -> 6
	57406: 55, // KP_7 -> 7
	57407: 56, // KP_8 -> 8
	57408: 57, // KP_9 -> 9
	57409: 46, // KP_DECIMAL -> .
	57410: 47, // KP_DIVIDE -> /
	57411: 42, // KP_MULTIPLY -> *
	57412: 45, // KP_SUBTRACT -> -
	57413: 43, // KP_ADD -> +
	57415: 61, // KP_EQUAL -> =
	57416: 44, // KP_SEPARATOR -> ,
	57417: cpLeftArrow,
	57418: cpRightArrow,
	57419: cpUpArrow,
	57420: cpDownArrow,
	57421: cpPageUp,
	57422: cpPageDown,
	57423: cpHome,
	57424: cpEnd,
	57425: cpInsert,
	57426: cpDelete,
}

func normalizeKittyFunctionalCodepoint(codepoint int) int {
	if equivalent, ok := kittyFunctionalKeyEquivalents[codepoint]; ok {
		return equivalent
	}
	return codepoint
}

func normalizeShiftedLetterIdentityCodepoint(codepoint, modifier int) int {
	effectiveModifier := modifier & ^lockMask
	if effectiveModifier&modShift != 0 && codepoint >= 65 && codepoint <= 90 {
		return codepoint + 32
	}
	return codepoint
}

var legacyKeySequences = map[string][]string{
	"up":       {"\x1b[A", "\x1bOA"},
	"down":     {"\x1b[B", "\x1bOB"},
	"right":    {"\x1b[C", "\x1bOC"},
	"left":     {"\x1b[D", "\x1bOD"},
	"home":     {"\x1b[H", "\x1bOH", "\x1b[1~", "\x1b[7~"},
	"end":      {"\x1b[F", "\x1bOF", "\x1b[4~", "\x1b[8~"},
	"insert":   {"\x1b[2~"},
	"delete":   {"\x1b[3~"},
	"pageUp":   {"\x1b[5~", "\x1b[[5~"},
	"pageDown": {"\x1b[6~", "\x1b[[6~"},
	"clear":    {"\x1b[E", "\x1bOE"},
	"f1":       {"\x1bOP", "\x1b[11~", "\x1b[[A"},
	"f2":       {"\x1bOQ", "\x1b[12~", "\x1b[[B"},
	"f3":       {"\x1bOR", "\x1b[13~", "\x1b[[C"},
	"f4":       {"\x1bOS", "\x1b[14~", "\x1b[[D"},
	"f5":       {"\x1b[15~", "\x1b[[E"},
	"f6":       {"\x1b[17~"},
	"f7":       {"\x1b[18~"},
	"f8":       {"\x1b[19~"},
	"f9":       {"\x1b[20~"},
	"f10":      {"\x1b[21~"},
	"f11":      {"\x1b[23~"},
	"f12":      {"\x1b[24~"},
}

var legacyShiftSequences = map[string][]string{
	"up":       {"\x1b[a"},
	"down":     {"\x1b[b"},
	"right":    {"\x1b[c"},
	"left":     {"\x1b[d"},
	"clear":    {"\x1b[e"},
	"insert":   {"\x1b[2$"},
	"delete":   {"\x1b[3$"},
	"pageUp":   {"\x1b[5$"},
	"pageDown": {"\x1b[6$"},
	"home":     {"\x1b[7$"},
	"end":      {"\x1b[8$"},
}

var legacyCtrlSequences = map[string][]string{
	"up":       {"\x1bOa"},
	"down":     {"\x1bOb"},
	"right":    {"\x1bOc"},
	"left":     {"\x1bOd"},
	"clear":    {"\x1bOe"},
	"insert":   {"\x1b[2^"},
	"delete":   {"\x1b[3^"},
	"pageUp":   {"\x1b[5^"},
	"pageDown": {"\x1b[6^"},
	"home":     {"\x1b[7^"},
	"end":      {"\x1b[8^"},
}

var legacySequenceKeyIds = map[string]KeyId{
	"\x1bOA":   "up",
	"\x1bOB":   "down",
	"\x1bOC":   "right",
	"\x1bOD":   "left",
	"\x1bOH":   "home",
	"\x1bOF":   "end",
	"\x1b[E":   "clear",
	"\x1bOE":   "clear",
	"\x1bOe":   "ctrl+clear",
	"\x1b[e":   "shift+clear",
	"\x1b[2~":  "insert",
	"\x1b[2$":  "shift+insert",
	"\x1b[2^":  "ctrl+insert",
	"\x1b[3$":  "shift+delete",
	"\x1b[3^":  "ctrl+delete",
	"\x1b[[5~": "pageUp",
	"\x1b[[6~": "pageDown",
	"\x1b[a":   "shift+up",
	"\x1b[b":   "shift+down",
	"\x1b[c":   "shift+right",
	"\x1b[d":   "shift+left",
	"\x1bOa":   "ctrl+up",
	"\x1bOb":   "ctrl+down",
	"\x1bOc":   "ctrl+right",
	"\x1bOd":   "ctrl+left",
	"\x1b[5$":  "shift+pageUp",
	"\x1b[6$":  "shift+pageDown",
	"\x1b[7$":  "shift+home",
	"\x1b[8$":  "shift+end",
	"\x1b[5^":  "ctrl+pageUp",
	"\x1b[6^":  "ctrl+pageDown",
	"\x1b[7^":  "ctrl+home",
	"\x1b[8^":  "ctrl+end",
	"\x1bOP":   "f1",
	"\x1bOQ":   "f2",
	"\x1bOR":   "f3",
	"\x1bOS":   "f4",
	"\x1b[11~": "f1",
	"\x1b[12~": "f2",
	"\x1b[13~": "f3",
	"\x1b[14~": "f4",
	"\x1b[[A":  "f1",
	"\x1b[[B":  "f2",
	"\x1b[[C":  "f3",
	"\x1b[[D":  "f4",
	"\x1b[[E":  "f5",
	"\x1b[15~": "f5",
	"\x1b[17~": "f6",
	"\x1b[18~": "f7",
	"\x1b[19~": "f8",
	"\x1b[20~": "f9",
	"\x1b[21~": "f10",
	"\x1b[23~": "f11",
	"\x1b[24~": "f12",
	"\x1bb":    "alt+left",
	"\x1bf":    "alt+right",
	"\x1bp":    "alt+up",
	"\x1bn":    "alt+down",
}

func matchesLegacySequence(data string, sequences []string) bool {
	for _, s := range sequences {
		if data == s {
			return true
		}
	}
	return false
}

func matchesLegacyModifierSequence(data, key string, modifier int) bool {
	if modifier == modShift {
		return matchesLegacySequence(data, legacyShiftSequences[key])
	}
	if modifier == modCtrl {
		return matchesLegacySequence(data, legacyCtrlSequences[key])
	}
	return false
}

// KeyEventType is the Kitty protocol (flag 2) event type: 1=press, 2=repeat,
// 3=release.
type KeyEventType string

const (
	KeyEventPress   KeyEventType = "press"
	KeyEventRepeat  KeyEventType = "repeat"
	KeyEventRelease KeyEventType = "release"
)

type parsedKittySequence struct {
	codepoint     int
	shiftedKey    int // 0 when absent
	baseLayoutKey int // 0 when absent
	modifier      int
	eventType     KeyEventType
}

type parsedModifyOtherKeysSequence struct {
	codepoint int
	modifier  int
}

// lastEventType stores the last parsed event type for IsKeyRelease queries.
var lastEventType = KeyEventPress

// IsKeyRelease reports whether data is a Kitty key-release event (flag 2).
// Bracketed paste content is never treated as release even when it contains
// patterns like ":3F" (e.g. bluetooth MAC addresses).
func IsKeyRelease(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, suffix := range []string{":3u", ":3~", ":3A", ":3B", ":3C", ":3D", ":3H", ":3F"} {
		if strings.Contains(data, suffix) {
			return true
		}
	}
	return false
}

// IsKeyRepeat reports whether data is a Kitty key-repeat event (flag 2).
func IsKeyRepeat(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, suffix := range []string{":2u", ":2~", ":2A", ":2B", ":2C", ":2D", ":2H", ":2F"} {
		if strings.Contains(data, suffix) {
			return true
		}
	}
	return false
}

func parseEventType(eventTypeStr string) KeyEventType {
	if eventTypeStr == "" {
		return KeyEventPress
	}
	eventType, _ := strconv.Atoi(eventTypeStr)
	if eventType == 2 {
		return KeyEventRepeat
	}
	if eventType == 3 {
		return KeyEventRelease
	}
	return KeyEventPress
}

var (
	kittyCsiUPattern       = regexp.MustCompile(`^\x1b\[(\d+)(?::(\d*))?(?::(\d+))?(?:;(\d+))?(?::(\d+))?u$`)
	kittyArrowPattern      = regexp.MustCompile(`^\x1b\[1;(\d+)(?::(\d+))?([ABCD])$`)
	kittyFuncPattern       = regexp.MustCompile(`^\x1b\[(\d+)(?:;(\d+))?(?::(\d+))?~$`)
	kittyHomeEndPattern    = regexp.MustCompile(`^\x1b\[1;(\d+)(?::(\d+))?([HF])$`)
	modifyOtherKeysPattern = regexp.MustCompile(`^\x1b\[27;(\d+);(\d+)~$`)
)

func atoiOr(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	value, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return value
}

func parseKittySequence(data string) *parsedKittySequence {
	// CSI u format with alternate keys (flag 4):
	// \x1b[<cp>u | \x1b[<cp>;<mod>u | \x1b[<cp>;<mod>:<event>u |
	// \x1b[<cp>:<shifted>;<mod>u | \x1b[<cp>:<shifted>:<base>;<mod>u |
	// \x1b[<cp>::<base>;<mod>u
	if m := kittyCsiUPattern.FindStringSubmatch(data); m != nil {
		codepoint := atoiOr(m[1], 0)
		shiftedKey := 0
		if m[2] != "" {
			shiftedKey = atoiOr(m[2], 0)
		}
		baseLayoutKey := 0
		if m[3] != "" {
			baseLayoutKey = atoiOr(m[3], 0)
		}
		modValue := 1
		if m[4] != "" {
			modValue = atoiOr(m[4], 1)
		}
		eventType := parseEventType(m[5])
		lastEventType = eventType
		return &parsedKittySequence{codepoint: codepoint, shiftedKey: shiftedKey, baseLayoutKey: baseLayoutKey, modifier: modValue - 1, eventType: eventType}
	}

	// Arrow keys with modifier: \x1b[1;<mod>A/B/C/D or with :<event>
	if m := kittyArrowPattern.FindStringSubmatch(data); m != nil {
		modValue := atoiOr(m[1], 0)
		eventType := parseEventType(m[2])
		arrowCodes := map[byte]int{'A': -1, 'B': -2, 'C': -3, 'D': -4}
		lastEventType = eventType
		return &parsedKittySequence{codepoint: arrowCodes[m[3][0]], modifier: modValue - 1, eventType: eventType}
	}

	// Functional keys: \x1b[<num>~ with optional modifier and event
	if m := kittyFuncPattern.FindStringSubmatch(data); m != nil {
		keyNum := atoiOr(m[1], 0)
		modValue := 1
		if m[2] != "" {
			modValue = atoiOr(m[2], 1)
		}
		eventType := parseEventType(m[3])
		funcCodes := map[int]int{
			2: cpInsert,
			3: cpDelete,
			5: cpPageUp,
			6: cpPageDown,
			7: cpHome,
			8: cpEnd,
		}
		if codepoint, ok := funcCodes[keyNum]; ok {
			lastEventType = eventType
			return &parsedKittySequence{codepoint: codepoint, modifier: modValue - 1, eventType: eventType}
		}
	}

	// Home/End with modifier: \x1b[1;<mod>H/F
	if m := kittyHomeEndPattern.FindStringSubmatch(data); m != nil {
		modValue := atoiOr(m[1], 0)
		eventType := parseEventType(m[2])
		codepoint := cpEnd
		if m[3] == "H" {
			codepoint = cpHome
		}
		lastEventType = eventType
		return &parsedKittySequence{codepoint: codepoint, modifier: modValue - 1, eventType: eventType}
	}

	return nil
}

func matchesKittySequence(data string, expectedCodepoint, expectedModifier int) bool {
	parsed := parseKittySequence(data)
	if parsed == nil {
		return false
	}
	actualMod := parsed.modifier & ^lockMask
	expectedMod := expectedModifier & ^lockMask

	normalizedCodepoint := normalizeShiftedLetterIdentityCodepoint(
		normalizeKittyFunctionalCodepoint(parsed.codepoint), parsed.modifier)
	normalizedExpectedCodepoint := normalizeShiftedLetterIdentityCodepoint(
		normalizeKittyFunctionalCodepoint(expectedCodepoint), expectedModifier)

	// Physical match: primary codepoint and every reported modifier match.
	if actualMod == expectedMod && normalizedCodepoint == normalizedExpectedCodepoint {
		return true
	}

	// Logical match: bindings name the produced symbol, so consume that
	// implicit Shift while preserving Ctrl, Alt, and Super. The physical
	// "shift+=" spelling stays valid through the match above.
	if code, modifier, ok := logicalShiftedSymbolCandidate(parsed.shiftedKey, actualMod, symbolKeys); ok {
		if modifier&^lockMask == expectedMod && code == normalizeKittyFunctionalCodepoint(expectedCodepoint) {
			return true
		}
	}

	if actualMod != expectedMod {
		return false
	}

	if normalizedCodepoint == normalizedExpectedCodepoint {
		return true
	}

	// Alternate match: base layout key for non-Latin keyboard layouts, so
	// Ctrl+Cyrillic-С matches ctrl+c. Only fall back when the codepoint is
	// NOT already a recognized Latin letter or symbol: the codepoint is then
	// authoritative regardless of physical key position, which prevents
	// remapped layouts (Dvorak, Colemak, xremap) from causing false matches.
	if parsed.baseLayoutKey != 0 && parsed.baseLayoutKey == expectedCodepoint {
		cp := normalizedCodepoint
		isLatinLetter := cp >= 97 && cp <= 122
		isKnownSymbol := symbolKeys[rune(cp)]
		if !isLatinLetter && !isKnownSymbol {
			return true
		}
	}

	return false
}

func parseModifyOtherKeysSequence(data string) *parsedModifyOtherKeysSequence {
	m := modifyOtherKeysPattern.FindStringSubmatch(data)
	if m == nil {
		return nil
	}
	modValue := atoiOr(m[1], 0)
	codepoint := atoiOr(m[2], 0)
	return &parsedModifyOtherKeysSequence{codepoint: codepoint, modifier: modValue - 1}
}

// matchesModifyOtherKeys matches the xterm format CSI 27 ; modifiers ;
// keycode ~, used when the Kitty protocol is not enabled. Modifier values are
// 1-indexed: 2=shift, 3=alt, 5=ctrl, etc.
func matchesModifyOtherKeys(data string, expectedKeycode, expectedModifier int) bool {
	parsed := parseModifyOtherKeysSequence(data)
	if parsed == nil {
		return false
	}
	return parsed.codepoint == expectedKeycode && parsed.modifier == expectedModifier
}

func isWindowsTerminalSession() bool {
	return os.Getenv("WT_SESSION") != "" && os.Getenv("SSH_CONNECTION") == "" &&
		os.Getenv("SSH_CLIENT") == "" && os.Getenv("SSH_TTY") == ""
}

// matchesRawBackspace disambiguates raw 0x08 (BS): Windows Terminal uses it
// for Ctrl+Backspace, some legacy terminals and tmux setups for plain
// Backspace. Explicit Kitty/CSI-u/modifyOtherKeys sequences are preferred.
func matchesRawBackspace(data string, expectedModifier int) bool {
	if data == "\x7f" {
		return expectedModifier == 0
	}
	if data != "\x08" {
		return false
	}
	if isWindowsTerminalSession() {
		return expectedModifier == modCtrl
	}
	return expectedModifier == 0
}

// rawCtrlChar returns the control character for a key via the universal
// formula code & 0x1f: letters a-z map to 1-26, [\]_ map to 27/28/29/31, and
// "-" maps to 31 (same physical key as "_" on US keyboards). Returns ok=false
// for other keys.
func rawCtrlChar(key string) (string, bool) {
	char := strings.ToLower(key)
	if len(char) != 1 {
		return "", false
	}
	code := char[0]
	if (code >= 97 && code <= 122) || char == "[" || char == "\\" || char == "]" || char == "_" {
		return string(rune(code & 0x1f)), true
	}
	if char == "-" {
		return "\x1f", true
	}
	return "", false
}

func isDigitKey(key string) bool {
	return len(key) == 1 && key[0] >= '0' && key[0] <= '9'
}

func matchesPrintableModifyOtherKeys(data string, expectedKeycode, expectedModifier int) bool {
	parsed := parseModifyOtherKeysSequence(data)
	if parsed == nil {
		return false
	}
	physicalMatch := parsed.modifier == expectedModifier &&
		normalizeShiftedLetterIdentityCodepoint(parsed.codepoint, parsed.modifier) ==
			normalizeShiftedLetterIdentityCodepoint(expectedKeycode, expectedModifier)
	if physicalMatch {
		return true
	}
	code, modifier, ok := logicalShiftedSymbolCandidate(parsed.codepoint, parsed.modifier, shiftedSymbolKeys)
	return ok && modifier == expectedModifier && code == expectedKeycode
}

func formatKeyNameWithModifiers(keyName string, modifier int) (string, bool) {
	var mods []string
	effectiveMod := modifier & ^lockMask
	supportedModifierMask := modShift | modCtrl | modAlt | modSuper
	if effectiveMod & ^supportedModifierMask != 0 {
		return "", false
	}
	if effectiveMod&modShift != 0 {
		mods = append(mods, "shift")
	}
	if effectiveMod&modCtrl != 0 {
		mods = append(mods, "ctrl")
	}
	if effectiveMod&modAlt != 0 {
		mods = append(mods, "alt")
	}
	if effectiveMod&modSuper != 0 {
		mods = append(mods, "super")
	}
	if len(mods) == 0 {
		return keyName, true
	}
	return strings.Join(mods, "+") + "+" + keyName, true
}

type parsedKeyId struct {
	key      string
	ctrl     bool
	shift    bool
	alt      bool
	superMod bool
}

func parseKeyId(keyId KeyId) *parsedKeyId {
	key := strings.ToLower(keyId)
	result := &parsedKeyId{}
	for {
		matched := ""
		for _, modifier := range []string{"shift", "alt", "ctrl", "super"} {
			if strings.HasPrefix(key, modifier+"+") {
				matched = modifier
				break
			}
		}
		if matched == "" {
			break
		}
		switch matched {
		case "ctrl":
			if result.ctrl {
				return nil
			}
			result.ctrl = true
		case "shift":
			if result.shift {
				return nil
			}
			result.shift = true
		case "alt":
			if result.alt {
				return nil
			}
			result.alt = true
		case "super":
			if result.superMod {
				return nil
			}
			result.superMod = true
		}
		key = key[len(matched)+1:]
	}
	if key == "" || (strings.Contains(key, "+") && key != "+") {
		return nil
	}
	result.key = key
	return result
}

// MatchesKey reports whether raw terminal input data matches a key identifier
// like "ctrl+c", "escape", "shift+tab", or "ctrl+super+k".
func MatchesKey(data string, keyId KeyId) bool {
	parsed := parseKeyId(keyId)
	if parsed == nil {
		return false
	}

	key := parsed.key
	modifier := 0
	if parsed.shift {
		modifier |= modShift
	}
	if parsed.alt {
		modifier |= modAlt
	}
	if parsed.ctrl {
		modifier |= modCtrl
	}
	if parsed.superMod {
		modifier |= modSuper
	}

	switch key {
	case "escape", "esc":
		if modifier != 0 {
			return false
		}
		return data == "\x1b" ||
			matchesKittySequence(data, cpEscape, 0) ||
			matchesModifyOtherKeys(data, cpEscape, 0)

	case "space":
		if !kittyProtocolActive {
			if modifier == modCtrl && data == "\x00" {
				return true
			}
			if modifier == modAlt && data == "\x1b " {
				return true
			}
		}
		if modifier == 0 {
			return data == " " ||
				matchesKittySequence(data, cpSpace, 0) ||
				matchesModifyOtherKeys(data, cpSpace, 0)
		}
		return matchesKittySequence(data, cpSpace, modifier) ||
			matchesModifyOtherKeys(data, cpSpace, modifier)

	case "tab":
		if modifier == modShift {
			return data == "\x1b[Z" ||
				matchesKittySequence(data, cpTab, modShift) ||
				matchesModifyOtherKeys(data, cpTab, modShift)
		}
		if modifier == 0 {
			return data == "\t" || matchesKittySequence(data, cpTab, 0)
		}
		return matchesKittySequence(data, cpTab, modifier) ||
			matchesModifyOtherKeys(data, cpTab, modifier)

	case "enter", "return":
		if modifier == modShift {
			if matchesKittySequence(data, cpEnter, modShift) ||
				matchesKittySequence(data, cpKpEnter, modShift) {
				return true
			}
			if matchesModifyOtherKeys(data, cpEnter, modShift) {
				return true
			}
			// With Kitty active, legacy sequences are custom terminal
			// mappings: \x1b\r = Kitty's shift+enter map, \n = Ghostty's.
			if kittyProtocolActive {
				return data == "\x1b\r" || data == "\n"
			}
			return false
		}
		if modifier == modAlt {
			if matchesKittySequence(data, cpEnter, modAlt) ||
				matchesKittySequence(data, cpKpEnter, modAlt) {
				return true
			}
			if matchesModifyOtherKeys(data, cpEnter, modAlt) {
				return true
			}
			// \x1b\r is alt+enter only in legacy mode.
			if !kittyProtocolActive {
				return data == "\x1b\r"
			}
			return false
		}
		if modifier == 0 {
			return data == "\r" ||
				(!kittyProtocolActive && data == "\n") ||
				data == "\x1bOM" || // SS3 M (numpad enter in some terminals)
				matchesKittySequence(data, cpEnter, 0) ||
				matchesKittySequence(data, cpKpEnter, 0)
		}
		return matchesKittySequence(data, cpEnter, modifier) ||
			matchesKittySequence(data, cpKpEnter, modifier) ||
			matchesModifyOtherKeys(data, cpEnter, modifier)

	case "backspace":
		if modifier == modAlt {
			if data == "\x1b\x7f" || data == "\x1b\b" {
				return true
			}
			return matchesKittySequence(data, cpBackspace, modAlt) ||
				matchesModifyOtherKeys(data, cpBackspace, modAlt)
		}
		if modifier == modCtrl {
			// Legacy raw 0x08 is ambiguous: Ctrl+Backspace on Windows
			// Terminal, plain Backspace elsewhere, overlapping Ctrl+H.
			if matchesRawBackspace(data, modCtrl) {
				return true
			}
			return matchesKittySequence(data, cpBackspace, modCtrl) ||
				matchesModifyOtherKeys(data, cpBackspace, modCtrl)
		}
		if modifier == 0 {
			return matchesRawBackspace(data, 0) ||
				matchesKittySequence(data, cpBackspace, 0) ||
				matchesModifyOtherKeys(data, cpBackspace, 0)
		}
		return matchesKittySequence(data, cpBackspace, modifier) ||
			matchesModifyOtherKeys(data, cpBackspace, modifier)

	case "insert":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["insert"]) ||
				matchesKittySequence(data, cpInsert, 0)
		}
		if matchesLegacyModifierSequence(data, "insert", modifier) {
			return true
		}
		return matchesKittySequence(data, cpInsert, modifier)

	case "delete":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["delete"]) ||
				matchesKittySequence(data, cpDelete, 0)
		}
		if matchesLegacyModifierSequence(data, "delete", modifier) {
			return true
		}
		return matchesKittySequence(data, cpDelete, modifier)

	case "clear":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["clear"])
		}
		return matchesLegacyModifierSequence(data, "clear", modifier)

	case "home":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["home"]) ||
				matchesKittySequence(data, cpHome, 0)
		}
		if matchesLegacyModifierSequence(data, "home", modifier) {
			return true
		}
		return matchesKittySequence(data, cpHome, modifier)

	case "end":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["end"]) ||
				matchesKittySequence(data, cpEnd, 0)
		}
		if matchesLegacyModifierSequence(data, "end", modifier) {
			return true
		}
		return matchesKittySequence(data, cpEnd, modifier)

	case "pageup":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["pageUp"]) ||
				matchesKittySequence(data, cpPageUp, 0)
		}
		if matchesLegacyModifierSequence(data, "pageUp", modifier) {
			return true
		}
		return matchesKittySequence(data, cpPageUp, modifier)

	case "pagedown":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["pageDown"]) ||
				matchesKittySequence(data, cpPageDown, 0)
		}
		if matchesLegacyModifierSequence(data, "pageDown", modifier) {
			return true
		}
		return matchesKittySequence(data, cpPageDown, modifier)

	case "up":
		if modifier == modAlt {
			return data == "\x1bp" || matchesKittySequence(data, cpUpArrow, modAlt)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["up"]) ||
				matchesKittySequence(data, cpUpArrow, 0)
		}
		if matchesLegacyModifierSequence(data, "up", modifier) {
			return true
		}
		return matchesKittySequence(data, cpUpArrow, modifier)

	case "down":
		if modifier == modAlt {
			return data == "\x1bn" || matchesKittySequence(data, cpDownArrow, modAlt)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["down"]) ||
				matchesKittySequence(data, cpDownArrow, 0)
		}
		if matchesLegacyModifierSequence(data, "down", modifier) {
			return true
		}
		return matchesKittySequence(data, cpDownArrow, modifier)

	case "left":
		if modifier == modAlt {
			return data == "\x1b[1;3D" ||
				(!kittyProtocolActive && data == "\x1bB") ||
				data == "\x1bb" ||
				matchesKittySequence(data, cpLeftArrow, modAlt)
		}
		if modifier == modCtrl {
			return data == "\x1b[1;5D" ||
				matchesLegacyModifierSequence(data, "left", modCtrl) ||
				matchesKittySequence(data, cpLeftArrow, modCtrl)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["left"]) ||
				matchesKittySequence(data, cpLeftArrow, 0)
		}
		if matchesLegacyModifierSequence(data, "left", modifier) {
			return true
		}
		return matchesKittySequence(data, cpLeftArrow, modifier)

	case "right":
		if modifier == modAlt {
			return data == "\x1b[1;3C" ||
				(!kittyProtocolActive && data == "\x1bF") ||
				data == "\x1bf" ||
				matchesKittySequence(data, cpRightArrow, modAlt)
		}
		if modifier == modCtrl {
			return data == "\x1b[1;5C" ||
				matchesLegacyModifierSequence(data, "right", modCtrl) ||
				matchesKittySequence(data, cpRightArrow, modCtrl)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["right"]) ||
				matchesKittySequence(data, cpRightArrow, 0)
		}
		if matchesLegacyModifierSequence(data, "right", modifier) {
			return true
		}
		return matchesKittySequence(data, cpRightArrow, modifier)

	case "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12":
		if modifier != 0 {
			return false
		}
		return matchesLegacySequence(data, legacyKeySequences[key])
	}

	// Single letter/digit keys and symbols
	if len(key) == 1 {
		r := rune(key[0])
		isLetter := r >= 'a' && r <= 'z'
		isDigit := isDigitKey(key)
		if isLetter || isDigit || symbolKeys[r] {
			codepoint := int(r)
			rawCtrl, hasRawCtrl := rawCtrlChar(key)

			if modifier == modCtrl+modAlt && !kittyProtocolActive && hasRawCtrl {
				// Legacy: ctrl+alt+key is ESC followed by the control
				// character. Fall through to CSI-u / modifyOtherKeys on miss.
				if data == "\x1b"+rawCtrl {
					return true
				}
			}

			if modifier == modAlt && !kittyProtocolActive {
				// Legacy: alt+printable key is ESC followed by the key
				if data == "\x1b"+key {
					return true
				}
			}

			if modifier == modCtrl {
				// Legacy: ctrl+key sends the control character
				if hasRawCtrl && data == rawCtrl {
					return true
				}
				return matchesKittySequence(data, codepoint, modCtrl) ||
					matchesPrintableModifyOtherKeys(data, codepoint, modCtrl)
			}

			if modifier == modShift+modCtrl {
				return matchesKittySequence(data, codepoint, modShift+modCtrl) ||
					matchesPrintableModifyOtherKeys(data, codepoint, modShift+modCtrl)
			}

			if modifier == modShift {
				// Legacy: shift+letter produces uppercase
				if isLetter && data == strings.ToUpper(key) {
					return true
				}
				return matchesKittySequence(data, codepoint, modShift) ||
					matchesPrintableModifyOtherKeys(data, codepoint, modShift)
			}

			if modifier != 0 {
				return matchesKittySequence(data, codepoint, modifier) ||
					matchesPrintableModifyOtherKeys(data, codepoint, modifier)
			}

			// Check both raw char and Kitty sequence (needed for release
			// events)
			return data == key || matchesKittySequence(data, codepoint, 0)
		}
	}

	return false
}

func formatParsedKey(codepoint, modifier, baseLayoutKey int) (string, bool) {
	normalizedCodepoint := normalizeKittyFunctionalCodepoint(codepoint)
	identityCodepoint := normalizeShiftedLetterIdentityCodepoint(normalizedCodepoint, modifier)

	// Base layout key only when the codepoint is not a recognized Latin
	// letter, digit, or symbol; recognized codepoints are authoritative
	// regardless of physical key position (Dvorak/Colemak/xremap safety).
	isLatinLetter := identityCodepoint >= 97 && identityCodepoint <= 122
	isDigit := identityCodepoint >= 48 && identityCodepoint <= 57
	isKnownSymbol := identityCodepoint >= 0 && identityCodepoint <= 0x10ffff && symbolKeys[rune(identityCodepoint)]
	effectiveCodepoint := identityCodepoint
	if !(isLatinLetter || isDigit || isKnownSymbol) && baseLayoutKey != 0 {
		effectiveCodepoint = baseLayoutKey
	}

	var keyName string
	switch {
	case effectiveCodepoint == cpEscape:
		keyName = "escape"
	case effectiveCodepoint == cpTab:
		keyName = "tab"
	case effectiveCodepoint == cpEnter || effectiveCodepoint == cpKpEnter:
		keyName = "enter"
	case effectiveCodepoint == cpSpace:
		keyName = "space"
	case effectiveCodepoint == cpBackspace:
		keyName = "backspace"
	case effectiveCodepoint == cpDelete:
		keyName = "delete"
	case effectiveCodepoint == cpInsert:
		keyName = "insert"
	case effectiveCodepoint == cpHome:
		keyName = "home"
	case effectiveCodepoint == cpEnd:
		keyName = "end"
	case effectiveCodepoint == cpPageUp:
		keyName = "pageUp"
	case effectiveCodepoint == cpPageDown:
		keyName = "pageDown"
	case effectiveCodepoint == cpUpArrow:
		keyName = "up"
	case effectiveCodepoint == cpDownArrow:
		keyName = "down"
	case effectiveCodepoint == cpLeftArrow:
		keyName = "left"
	case effectiveCodepoint == cpRightArrow:
		keyName = "right"
	case effectiveCodepoint >= 48 && effectiveCodepoint <= 57:
		keyName = string(rune(effectiveCodepoint))
	case effectiveCodepoint >= 97 && effectiveCodepoint <= 122:
		keyName = string(rune(effectiveCodepoint))
	case effectiveCodepoint >= 0 && symbolKeys[rune(effectiveCodepoint)]:
		keyName = string(rune(effectiveCodepoint))
	default:
		return "", false
	}
	return formatKeyNameWithModifiers(keyName, modifier)
}

// ParseKey parses raw input data into a key identifier ("ctrl+c"), or ok=false
// when unrecognized.
func ParseKey(data string) (string, bool) {
	if kitty := parseKittySequence(data); kitty != nil {
		code, modifier := kitty.codepoint, kitty.modifier
		if logicalCode, logicalModifier, ok := logicalShiftedSymbolCandidate(kitty.shiftedKey, kitty.modifier, symbolKeys); ok {
			code, modifier = logicalCode, logicalModifier
		}
		return formatParsedKey(code, modifier, kitty.baseLayoutKey)
	}

	if modifyOtherKeys := parseModifyOtherKeysSequence(data); modifyOtherKeys != nil {
		code, modifier := modifyOtherKeys.codepoint, modifyOtherKeys.modifier
		if logicalCode, logicalModifier, ok := logicalShiftedSymbolCandidate(modifyOtherKeys.codepoint, modifyOtherKeys.modifier, shiftedSymbolKeys); ok {
			code, modifier = logicalCode, logicalModifier
		}
		return formatParsedKey(code, modifier, 0)
	}

	// Mode-aware legacy sequences: with Kitty active, \x1b\r = shift+enter
	// (Kitty mapping) and \n = shift+enter (Ghostty mapping).
	if kittyProtocolActive {
		if data == "\x1b\r" || data == "\n" {
			return "shift+enter", true
		}
	}

	if keyId, ok := legacySequenceKeyIds[data]; ok {
		return keyId, true
	}

	switch {
	case data == "\x1b":
		return "escape", true
	case data == "\x1c":
		return "ctrl+\\", true
	case data == "\x1d":
		return "ctrl+]", true
	case data == "\x1f":
		return "ctrl+-", true
	case data == "\x1b\x1b":
		return "ctrl+alt+[", true
	case data == "\x1b\x1c":
		return "ctrl+alt+\\", true
	case data == "\x1b\x1d":
		return "ctrl+alt+]", true
	case data == "\x1b\x1f":
		return "ctrl+alt+-", true
	case data == "\t":
		return "tab", true
	case data == "\r" || (!kittyProtocolActive && data == "\n") || data == "\x1bOM":
		return "enter", true
	case data == "\x00":
		return "ctrl+space", true
	case data == " ":
		return "space", true
	case data == "\x7f":
		return "backspace", true
	case data == "\x08":
		if isWindowsTerminalSession() {
			return "ctrl+backspace", true
		}
		return "backspace", true
	case data == "\x1b[Z":
		return "shift+tab", true
	}

	if !kittyProtocolActive {
		switch data {
		case "\x1b\r":
			return "alt+enter", true
		case "\x1b ":
			return "alt+space", true
		case "\x1bB":
			return "alt+left", true
		case "\x1bF":
			return "alt+right", true
		}
	}

	switch data {
	case "\x1b\x7f", "\x1b\b":
		return "alt+backspace", true
	case "\x1b[A":
		return "up", true
	case "\x1b[B":
		return "down", true
	case "\x1b[C":
		return "right", true
	case "\x1b[D":
		return "left", true
	case "\x1b[H", "\x1bOH":
		return "home", true
	case "\x1b[F", "\x1bOF":
		return "end", true
	case "\x1b[3~":
		return "delete", true
	case "\x1b[5~":
		return "pageUp", true
	case "\x1b[6~":
		return "pageDown", true
	}

	if !kittyProtocolActive && len(data) == 2 && data[0] == '\x1b' {
		code := int(data[1])
		if code >= 1 && code <= 26 {
			return "ctrl+alt+" + string(rune(code+96)), true
		}
		key := string(rune(code))
		if (code >= 97 && code <= 122) || (code >= 48 && code <= 57) || symbolKeys[rune(code)] {
			return "alt+" + key, true
		}
	}

	// Raw Ctrl+letter
	if len(data) == 1 {
		code := int(data[0])
		if code >= 1 && code <= 26 {
			return "ctrl+" + string(rune(code+96)), true
		}
		if code >= 32 && code <= 126 {
			return data, true
		}
	}

	return "", false
}

const kittyPrintableAllowedModifiers = modShift | lockMask

// DecodeKittyPrintable decodes a Kitty CSI-u sequence into a printable
// character when applicable. Only plain or Shift-modified keys are accepted;
// Ctrl/Alt and unsupported modifiers are rejected (keybinding matching owns
// those). Prefers the shifted keycode when Shift is held.
func DecodeKittyPrintable(data string) (string, bool) {
	match := kittyCsiUPattern.FindStringSubmatch(data)
	if match == nil {
		return "", false
	}

	codepoint := atoiOr(match[1], -1)
	if codepoint < 0 {
		return "", false
	}

	shiftedKey := 0
	if match[2] != "" {
		shiftedKey = atoiOr(match[2], 0)
	}
	modValue := 1
	if match[4] != "" {
		modValue = atoiOr(match[4], 1)
	}
	modifier := modValue - 1

	if modifier & ^kittyPrintableAllowedModifiers != 0 {
		return "", false
	}
	if modifier&(modAlt|modCtrl) != 0 {
		return "", false
	}

	effectiveCodepoint := codepoint
	if modifier&modShift != 0 && shiftedKey != 0 {
		effectiveCodepoint = shiftedKey
	}
	effectiveCodepoint = normalizeKittyFunctionalCodepoint(effectiveCodepoint)
	if effectiveCodepoint < 32 {
		return "", false
	}

	return string(rune(effectiveCodepoint)), true
}

func decodeModifyOtherKeysPrintable(data string) (string, bool) {
	parsed := parseModifyOtherKeysSequence(data)
	if parsed == nil {
		return "", false
	}
	modifier := parsed.modifier & ^lockMask
	if modifier & ^modShift != 0 {
		return "", false
	}
	if parsed.codepoint < 32 {
		return "", false
	}
	return string(rune(parsed.codepoint)), true
}

// DecodePrintableKey decodes a printable character from a Kitty CSI-u or
// xterm modifyOtherKeys sequence.
func DecodePrintableKey(data string) (string, bool) {
	if ch, ok := DecodeKittyPrintable(data); ok {
		return ch, true
	}
	return decodeModifyOtherKeysPrintable(data)
}

// Test seams for env access, kept adjacent to their only consumers.
func osGetenvForTest(name string) string { return os.Getenv(name) }
func osSetenvForTest(name, value string) { os.Setenv(name, value) }
func osUnsetenvForTest(name string)      { os.Unsetenv(name) }
