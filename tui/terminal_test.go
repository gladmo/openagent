package tui

import (
	"sync"

	"testing"
	"time"
)

// Ported from pi/packages/tui/test/terminal.test.ts (pure functions and the
// Kitty keyboard protocol negotiation harness; ProcessTerminal's writeOut
// seam replaces the test's stdout monkey-patching).

func TestResolveEscapeTimeoutMs(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(name string) string { return vars[name] }
	}
	cases := []struct {
		name string
		vars map[string]string
		want int
	}{
		{"configured override", map[string]string{"PI_TUI_ESC_TIMEOUT": "80"}, 80},
		{"override beats ssh", map[string]string{"PI_TUI_ESC_TIMEOUT": "80", "SSH_TTY": "/dev/pts/1"}, 80},
		{"invalid ignored", map[string]string{"PI_TUI_ESC_TIMEOUT": "abc"}, 10},
		{"zero ignored", map[string]string{"PI_TUI_ESC_TIMEOUT": "0"}, 10},
		{"negative ignored", map[string]string{"PI_TUI_ESC_TIMEOUT": "-5"}, 10},
		{"empty ignored", map[string]string{"PI_TUI_ESC_TIMEOUT": ""}, 10},
		{"ssh connection", map[string]string{"SSH_CONNECTION": "10.0.0.1 22"}, 100},
		{"ssh tty", map[string]string{"SSH_TTY": "/dev/pts/1"}, 100},
		{"default", map[string]string{}, 10},
	}
	for _, tc := range cases {
		if got := ResolveEscapeTimeoutMs(env(tc.vars)); got != tc.want {
			t.Errorf("%s: ResolveEscapeTimeoutMs = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeNativeShiftEnterInput(t *testing.T) {
	if got := NormalizeNativeShiftEnterInput("\r", true, true); got != "\x1b[13;2u" {
		t.Fatalf("shift pressed: %q", got)
	}
	if got := NormalizeNativeShiftEnterInput("\r", false, true); got != "\r" {
		t.Fatalf("detection off: %q", got)
	}
	if got := NormalizeNativeShiftEnterInput("\r", true, false); got != "\r" {
		t.Fatalf("shift not pressed: %q", got)
	}
	if got := NormalizeNativeShiftEnterInput("\x1b[13;2u", true, true); got != "\x1b[13;2u" {
		t.Fatalf("already CSI-u: %q", got)
	}
	if got := NormalizeNativeShiftEnterInput("a", true, true); got != "a" {
		t.Fatalf("non-return: %q", got)
	}
}

func TestNormalizeAppleTerminalInput(t *testing.T) {
	if got := NormalizeAppleTerminalInput("\r", true, true); got != "\x1b[13;2u" {
		t.Fatalf("shift pressed: %q", got)
	}
	if got := NormalizeAppleTerminalInput("\r", true, false); got != "\r" {
		t.Fatalf("shift not pressed: %q", got)
	}
	if got := NormalizeAppleTerminalInput("\r", false, true); got != "\r" {
		t.Fatalf("not apple terminal: %q", got)
	}
	if got := NormalizeAppleTerminalInput("a", true, true); got != "a" {
		t.Fatalf("non-return: %q", got)
	}
}

func TestParseKeyboardProtocolNegotiationSequence(t *testing.T) {
	if got, ok := ParseKeyboardProtocolNegotiationSequence("\x1b[?7u"); !ok || got.Type != "kitty-flags" || got.Flags != 7 {
		t.Fatalf("kitty flags: %+v ok=%v", got, ok)
	}
	if got, ok := ParseKeyboardProtocolNegotiationSequence("\x1b[?62;4;52c"); !ok || got.Type != "device-attributes" {
		t.Fatalf("device attributes: %+v ok=%v", got, ok)
	}
	if _, ok := ParseKeyboardProtocolNegotiationSequence("a"); ok {
		t.Fatal("plain input parsed as negotiation")
	}
}

type negotiationHarness struct {
	terminal *ProcessTerminal
	writes   *[]string
	inputs   *[]string

	// inputsMu guards the inputs slice: the negotiation flush runs on a
	// timer goroutine while tests read the last input.
	inputsMu sync.Mutex
}

func (h *negotiationHarness) appendInput(data string) {
	h.inputsMu.Lock()
	*h.inputs = append(*h.inputs, data)
	h.inputsMu.Unlock()
}

func (h *negotiationHarness) lastInput() (string, bool) {
	h.inputsMu.Lock()
	defer h.inputsMu.Unlock()
	if len(*h.inputs) == 0 {
		return "", false
	}
	return (*h.inputs)[len(*h.inputs)-1], true
}

func setupNegotiation(t *testing.T) *negotiationHarness {
	t.Helper()
	SetKittyProtocolActive(false)
	terminal := NewProcessTerminal()
	writes := &[]string{}
	inputs := &[]string{}
	harness := &negotiationHarness{terminal: terminal, writes: writes, inputs: inputs}
	terminal.writeOut = func(data string) { *writes = append(*writes, data) }
	terminal.inputHandler = harness.appendInput

	terminal.mu.Lock()
	terminal.beginNegotiation()
	terminal.mu.Unlock()

	t.Cleanup(func() {
		terminal.mu.Lock()
		terminal.clearNegotiationBufferLocked()
		terminal.stdinBuffer = nil
		terminal.keyboardProtocolPushed = false
		terminal.kittyActive = false
		terminal.mu.Unlock()
		SetKittyProtocolActive(false)
	})

	return harness
}

func (h *negotiationHarness) send(data string) {
	h.terminal.mu.Lock()
	buffer := h.terminal.stdinBuffer
	h.terminal.mu.Unlock()
	if buffer != nil {
		buffer.Process([]byte(data))
	}
}

func countWrites(writes []string, target string) int {
	count := 0
	for _, w := range writes {
		if w == target {
			count++
		}
	}
	return count
}

func TestNegotiationQueriesBeforeFallback(t *testing.T) {
	h := setupNegotiation(t)
	// The OSC 7501 program status query shares the DA sentinel (503c60552).
	if (*h.writes)[0] != "\x1b[>7u\x1b[?u\x1b]7501;?\x1b\\\x1b[c" {
		t.Fatalf("query = %q", (*h.writes)[0])
	}
	if countWrites(*h.writes, "\x1b[>4;2m") != 0 {
		t.Fatal("modifyOtherKeys enabled before response")
	}
	if h.terminal.KittyProtocolActive() {
		t.Fatal("kitty active before response")
	}
}

func TestNegotiationActivatesKittyForNonZeroFlags(t *testing.T) {
	h := setupNegotiation(t)
	h.send("\x1b[?7u")
	if _, ok := h.lastInput(); ok {
		t.Fatal("negotiation reply forwarded as input")
	}
	if !h.terminal.KittyProtocolActive() {
		t.Fatal("kitty not active")
	}
	if countWrites(*h.writes, "\x1b[>4;2m") != 0 || countWrites(*h.writes, "\x1b[>4;0m") != 0 {
		t.Fatal("modifyOtherKeys writes during kitty activation")
	}
}

func TestNegotiationFallsBackForZeroFlags(t *testing.T) {
	h := setupNegotiation(t)
	h.send("\x1b[?0u")
	if _, ok := h.lastInput(); ok {
		t.Fatal("negotiation reply forwarded as input")
	}
	if h.terminal.KittyProtocolActive() {
		t.Fatal("kitty active for zero flags")
	}
	if countWrites(*h.writes, "\x1b[>4;2m") != 1 {
		t.Fatal("modifyOtherKeys not enabled exactly once")
	}
}

func TestNegotiationFallsBackForDeviceAttributes(t *testing.T) {
	h := setupNegotiation(t)
	h.send("\x1b[?62;4;52c")
	if _, ok := h.lastInput(); ok {
		t.Fatal("device attributes forwarded as input")
	}
	if h.terminal.KittyProtocolActive() {
		t.Fatal("kitty active from DA")
	}
	if countWrites(*h.writes, "\x1b[>4;2m") != 1 {
		t.Fatal("modifyOtherKeys not enabled")
	}
}

func TestNegotiationForwardsNormalInput(t *testing.T) {
	h := setupNegotiation(t)
	h.send("a")
	if got, ok := h.lastInput(); !ok || got != "a" {
		t.Fatalf("input = %q ok=%v", got, ok)
	}
	if h.terminal.KittyProtocolActive() {
		t.Fatal("kitty active from plain input")
	}
}

func TestNegotiationTracksSplitKittyConfirmation(t *testing.T) {
	h := setupNegotiation(t)
	h.send("\x1b[?7")
	time.Sleep(10 * time.Millisecond)
	if got, ok := h.lastInput(); ok {
		t.Fatalf("partial negotiation forwarded: %q", got)
	}
	h.send("u")
	if !h.terminal.KittyProtocolActive() {
		t.Fatal("kitty not active after split confirmation")
	}
	if countWrites(*h.writes, "\x1b[>4;2m") != 0 {
		t.Fatal("modifyOtherKeys enabled during kitty path")
	}
}

func TestNegotiationReplaysBufferedCsiPrefix(t *testing.T) {
	h := setupNegotiation(t)
	h.send("\x1b[")
	time.Sleep(50 * time.Millisecond) // StdinBuffer sequence timeout, not lone-ESC
	if got, ok := h.lastInput(); ok {
		t.Fatalf("prefix forwarded early: %q", got)
	}
	time.Sleep(200 * time.Millisecond) // past the negotiation fragment timeout
	if got, ok := h.lastInput(); !ok || got != "\x1b[" {
		t.Fatalf("buffered prefix not replayed: %q ok=%v", got, ok)
	}
}
