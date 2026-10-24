package tui

// Port of terminal.ts: the minimal Terminal interface plus ProcessTerminal,
// which negotiates the Kitty keyboard protocol (fallback xterm
// modifyOtherKeys) and re-wraps bracketed paste through StdinBuffer.
//
// Native helpers (NAPI .node modules for clipboard, modifier queries, and
// Windows VT input) are a documented deviation: modifier queries return
// false and Windows VT input is unavailable; the clipboard moves to OSC52 +
// system commands (PARITY.md).

import (
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	terminalProgressKeepaliveMs = 1000
	terminalProgressActiveSeq   = "\x1b]9;4;3\x07"
	terminalProgressClearSeq    = "\x1b]9;4;0\x07"
	nativeShiftEnterSeq         = "\x1b[13;2u"
	desiredKittyProtocolFlags   = 7
	keyboardFragmentTimeoutMs   = 150
)

var kittyKeyboardProtocolQuery = "\x1b[>" + strconv.Itoa(desiredKittyProtocolFlags) + "u\x1b[?u\x1b[c"

// KeyboardProtocolNegotiationSequence is a parsed negotiation reply:
// kitty-flags (?N u) or device-attributes (?...c).
type KeyboardProtocolNegotiationSequence struct {
	Type  string // "kitty-flags" | "device-attributes"
	Flags int
}

var kittyFlagsPattern = regexp.MustCompile(`^\x1b\[\?(\d+)u$`)
var deviceAttributesPattern = regexp.MustCompile(`^\x1b\[\?[\d;]*c$`)

// ParseKeyboardProtocolNegotiationSequence parses a negotiation reply; ok is
// false when the sequence is neither form.
func ParseKeyboardProtocolNegotiationSequence(sequence string) (KeyboardProtocolNegotiationSequence, bool) {
	if m := kittyFlagsPattern.FindStringSubmatch(sequence); m != nil {
		flags, _ := strconv.Atoi(m[1])
		return KeyboardProtocolNegotiationSequence{Type: "kitty-flags", Flags: flags}, true
	}
	if deviceAttributesPattern.MatchString(sequence) {
		return KeyboardProtocolNegotiationSequence{Type: "device-attributes"}, true
	}
	return KeyboardProtocolNegotiationSequence{}, false
}

func isKeyboardProtocolNegotiationSequencePrefix(sequence string) bool {
	if sequence == "\x1b[" {
		return true
	}
	return regexp.MustCompile(`^\x1b\[\?[\d;]*$`).MatchString(sequence)
}

// IsAppleTerminalSession reports the Apple_Terminal program on darwin.
func IsAppleTerminalSession() bool {
	return runtimeGOOS() == "darwin" && os.Getenv("TERM_PROGRAM") == "Apple_Terminal"
}

// RefreshTerminalDimensions refreshes terminal dimensions on POSIX by sending
// SIGWINCH to this process (best-effort; restricted environments skip).
func RefreshTerminalDimensions() {
	if runtimeGOOS() == "win32" || os.Getpid() <= 0 {
		return
	}
	sendSIGWINCH()
}

// NormalizeNativeShiftEnterInput converts CR to the Kitty shift+enter
// sequence when native shift detection is active. Without the native helper
// (deviation), shouldDetectNativeShiftEnter is never true on darwin; the
// Windows console path is unavailable in this port.
func NormalizeNativeShiftEnterInput(data string, shouldDetectNativeShiftEnter, isShiftPressed bool) string {
	if shouldDetectNativeShiftEnter && data == "\r" && isShiftPressed {
		return nativeShiftEnterSeq
	}
	return data
}

// NormalizeAppleTerminalInput applies the Apple Terminal shift+enter
// normalization.
func NormalizeAppleTerminalInput(data string, isAppleTerminal, isShiftPressed bool) string {
	return NormalizeNativeShiftEnterInput(data, isAppleTerminal, isShiftPressed)
}

// Terminal is the minimal terminal interface for TUI.
type Terminal interface {
	// Start begins reading input; onInput receives decoded sequences and
	// onResize fires on terminal size changes. Stop restores state.
	Start(onInput func(data string), onResize func())
	Stop()
	// DrainInput consumes pending stdin (Kitty key releases) before exit:
	// waits up to maxMs, exiting after idleMs without input.
	DrainInput(maxMs, idleMs time.Duration)
	Write(data string)
	Columns() int
	Rows() int
	KittyProtocolActive() bool
	MoveBy(lines int)
	HideCursor()
	ShowCursor()
	ClearLine()
	ClearFromCursor()
	ClearScreen()
	SetTitle(title string)
	SetProgress(active bool)
	// SetProgramStatus reports program status over OSC 7501 to terminals
	// that support it; unsupported terminals ignore it.
	SetProgramStatus(status ProgramStatus)
}

const (
	terminalDefaultEscapeTimeoutMs    = 10
	terminalDefaultSshEscapeTimeoutMs = 100
)

// ResolveEscapeTimeoutMs resolves how long to wait for the rest of an escape
// sequence before dispatching a lone ESC as Escape. PI_TUI_ESC_TIMEOUT
// overrides; SSH sessions get a longer reassembly window for legacy Alt+key.
func ResolveEscapeTimeoutMs(env func(string) string) int {
	if env == nil {
		env = os.Getenv
	}
	if configured, err := strconv.Atoi(env("PI_TUI_ESC_TIMEOUT")); err == nil && configured > 0 {
		return configured
	}
	if env("SSH_CONNECTION") != "" || env("SSH_TTY") != "" {
		return terminalDefaultSshEscapeTimeoutMs
	}
	return terminalDefaultEscapeTimeoutMs
}

// ProcessTerminal is a real terminal over stdin/stdout.
type ProcessTerminal struct {
	mu sync.Mutex

	wasRaw                 bool
	inputHandler           func(data string)
	resizeHandler          func()
	kittyActive            bool
	modifyOtherKeysActive  bool
	keyboardProtocolPushed bool
	negotiationBuffer      string
	// pendingDeviceAttributes counts owed DA replies; the last one answers
	// the latest query, so a program status reply raced against the
	// sentinel is attributed correctly.
	pendingDeviceAttributes   int
	programStatus             *ProgramStatus
	programStatusSupported    bool
	programStatusQueryPending bool
	negotiationFlushTimer     *time.Timer
	stdinBuffer               *StdinBuffer
	progressInterval          *time.Timer
	progressActive            bool
	writeLogPath              string

	stdinReaderStop chan struct{}
	stdinReaderDone chan struct{}
	resizeStop      chan struct{}
	resizeDone      chan struct{}
	drainWatcher    chan []byte

	// writeOut is the output seam; tests replace it to capture writes.
	writeOut func(data string)
}

// NewProcessTerminal builds a ProcessTerminal, honoring PI_TUI_WRITE_LOG.
func NewProcessTerminal() *ProcessTerminal {
	t := &ProcessTerminal{}
	t.writeOut = t.stdoutWrite
	env := os.Getenv("PI_TUI_WRITE_LOG")
	if env != "" {
		if info, err := os.Stat(env); err == nil && info.IsDir() {
			now := time.Now()
			ts := fmt.Sprintf("%04d-%02d-%02d_%02d-%02d-%02d",
				now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute(), now.Second())
			t.writeLogPath = env + "/tui-" + ts + "-" + strconv.Itoa(os.Getpid()) + ".log"
		} else {
			t.writeLogPath = env
		}
	}
	return t
}

// KittyProtocolActive reports whether the Kitty keyboard protocol is on.
func (t *ProcessTerminal) KittyProtocolActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kittyActive
}

// ModifyOtherKeysActive reports the xterm modifyOtherKeys fallback state.
func (t *ProcessTerminal) ModifyOtherKeysActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.modifyOtherKeysActive
}

// Start enables raw mode, bracketed paste, and keyboard protocol
// negotiation, then reads stdin on a dedicated goroutine.
func (t *ProcessTerminal) Start(onInput func(data string), onResize func()) {
	t.mu.Lock()
	t.inputHandler = onInput
	t.resizeHandler = onResize

	// Save previous state and enable raw mode.
	t.wasRaw = terminalIsRaw(os.Stdin)
	terminalSetRaw(os.Stdin, true)
	t.writeOut("\x1b[?2004h") // bracketed paste

	// Resize handler via SIGWINCH.
	t.resizeStop = make(chan struct{})
	t.resizeDone = make(chan struct{})
	go t.watchResize()

	// Dimensions may be stale after suspend/resume (SIGWINCH lost).
	RefreshTerminalDimensions()

	t.setupStdinBufferAndQuery()
	t.mu.Unlock()
}

func (t *ProcessTerminal) watchResize() {
	defer close(t.resizeDone)
	if resizeSignalValue() == nil {
		<-t.resizeStop
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, resizeSignalValue())
	defer signal.Stop(ch)
	for {
		select {
		case <-t.resizeStop:
			return
		case <-ch:
			t.mu.Lock()
			handler := t.resizeHandler
			t.mu.Unlock()
			if handler != nil {
				handler()
			}
		}
	}
}

// setupStdinBufferAndQuery wires StdinBuffer, starts the stdin reader
// goroutine, and writes the Kitty protocol query with DA sentinel.
// Caller holds t.mu.
func (t *ProcessTerminal) setupStdinBufferAndQuery() {
	t.beginNegotiation()
	t.stdinReaderStop = make(chan struct{})
	t.stdinReaderDone = make(chan struct{})
	go t.readStdin()
}

// beginNegotiation wires StdinBuffer and writes the Kitty protocol query with
// DA sentinel without touching stdin; tests drive the buffer directly.
// Caller holds t.mu.
func (t *ProcessTerminal) beginNegotiation() {
	t.stdinBuffer = NewStdinBuffer(
		func(sequence string) { t.onSequence(sequence) },
		func(content string) {
			t.mu.Lock()
			handler := t.inputHandler
			t.mu.Unlock()
			if handler != nil {
				handler("\x1b[200~" + content + "\x1b[201~")
			}
		},
		StdinBufferOptions{EscapeTimeoutMs: ResolveEscapeTimeoutMs(nil)},
	)

	t.keyboardProtocolPushed = true
	t.pendingDeviceAttributes++
	t.clearNegotiationBufferLocked()
	// The OSC 7501 program status query shares the DA sentinel: a terminal
	// that supports it replies before DA. PI_PROGRAM_STATUS=1 or 0 skips
	// the query.
	override := os.Getenv("PI_PROGRAM_STATUS")
	t.programStatusSupported = override == "1"
	t.programStatusQueryPending = override != "1" && override != "0"
	programStatusQuery := ""
	if t.programStatusQueryPending {
		programStatusQuery = ProgramStatusQuery
	}
	// kittyKeyboardProtocolQuery already ends with the DA sentinel; the
	// program status query goes between the Kitty query and the sentinel.
	kittyQuery := strings.TrimSuffix(kittyKeyboardProtocolQuery, "\x1b[c")
	t.writeOut(kittyQuery + programStatusQuery + "\x1b[c")
	t.writeProgramStatusLocked()
}

func (t *ProcessTerminal) readStdin() {
	defer close(t.stdinReaderDone)
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			t.mu.Lock()
			drainWatcher := t.drainWatcher
			stdinBuffer := t.stdinBuffer
			t.mu.Unlock()
			if drainWatcher != nil {
				select {
				case drainWatcher <- chunk:
				default:
				}
			}
			if stdinBuffer != nil {
				stdinBuffer.Process(chunk)
			}
		}
		if err != nil {
			return
		}
		select {
		case <-t.stdinReaderStop:
			return
		default:
		}
	}
}

// onSequence handles one decoded input sequence: keyboard protocol
// negotiation first, then forwarding to the input handler.
func (t *ProcessTerminal) onSequence(sequence string) {
	t.mu.Lock()
	if IsProgramStatusReply(sequence) {
		if t.programStatusQueryPending {
			t.programStatusQueryPending = false
			t.programStatusSupported = true
			t.writeProgramStatusLocked()
		}
		t.mu.Unlock()
		return
	}
	_, status := t.readNegotiationSequence(sequence)
	if status == negotiationPending {
		t.scheduleNegotiationFlush()
		t.mu.Unlock()
		return // Wait briefly for the rest of a split Kitty response.
	}
	if status == negotiationHandled {
		t.mu.Unlock()
		return
	}
	handler := t.inputHandler
	t.mu.Unlock()
	if handler != nil {
		handler(t.normalizedForward(sequence))
	}
}

func (t *ProcessTerminal) normalizedForward(sequence string) string {
	shouldDetectNativeShiftEnter := sequence == "\r" && (IsAppleTerminalSession() || runtimeGOOS() == "win32")
	// Native modifier query is unavailable in this port (deviation): pass
	// through unmodified.
	return NormalizeNativeShiftEnterInput(sequence, shouldDetectNativeShiftEnter, false)
}

type negotiationStatus int

const (
	negotiationNone negotiationStatus = iota
	negotiationPending
	negotiationHandled
)

// readNegotiationSequence consumes negotiation replies. Caller holds t.mu.
func (t *ProcessTerminal) readNegotiationSequence(sequence string) (KeyboardProtocolNegotiationSequence, negotiationStatus) {
	buffered := sequence
	if t.negotiationBuffer != "" {
		buffered = t.negotiationBuffer + sequence
		parsed, ok := ParseKeyboardProtocolNegotiationSequence(buffered)
		if ok {
			t.clearNegotiationBufferLocked()
			return parsed, t.handleNegotiationSequence(parsed)
		}
		if isKeyboardProtocolNegotiationSequencePrefix(buffered) {
			t.setNegotiationBuffer(buffered)
			return KeyboardProtocolNegotiationSequence{}, negotiationPending
		}
		t.flushNegotiationBufferAsInput()
	}

	if parsed, ok := ParseKeyboardProtocolNegotiationSequence(sequence); ok {
		return parsed, t.handleNegotiationSequence(parsed)
	}
	if isKeyboardProtocolNegotiationSequencePrefix(sequence) {
		t.setNegotiationBuffer(sequence)
		return KeyboardProtocolNegotiationSequence{}, negotiationPending
	}
	return KeyboardProtocolNegotiationSequence{}, negotiationNone
}

// handleNegotiationSequence enables Kitty (nonzero flags) or falls back to
// modifyOtherKeys. Caller holds t.mu.
func (t *ProcessTerminal) handleNegotiationSequence(negotiation KeyboardProtocolNegotiationSequence) negotiationStatus {
	t.clearNegotiationBufferLocked()
	if negotiation.Type == "kitty-flags" {
		if negotiation.Flags != 0 {
			t.disableModifyOtherKeysLocked()
			if !t.kittyActive {
				t.kittyActive = true
				SetKittyProtocolActive(true)
			}
		} else {
			t.enableModifyOtherKeysLocked()
		}
		return negotiationHandled
	}
	// device-attributes: DA before a Kitty response means no Kitty support.
	// A DA no query owes is forwarded to the input handler, matching the
	// reference's return false.
	if t.pendingDeviceAttributes == 0 {
		return negotiationNone
	}
	t.pendingDeviceAttributes--
	// The last owed DA answers the latest query, which got no program
	// status reply first; earlier DA replies belong to queries from
	// before a restart.
	if t.pendingDeviceAttributes == 0 {
		t.programStatusQueryPending = false
	}
	if !t.kittyActive {
		t.enableModifyOtherKeysLocked()
	}
	return negotiationHandled
}

// SetProgramStatus reports program status over OSC 7501; state "clear"
// removes it.
func (t *ProcessTerminal) SetProgramStatus(status ProgramStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if status.State == "clear" {
		t.programStatus = nil
	} else {
		t.programStatus = &status
	}
	if t.programStatusSupported {
		t.writeOut(FormatProgramStatus(status))
	}
}

func (t *ProcessTerminal) writeProgramStatusLocked() {
	if t.programStatusSupported && t.programStatus != nil {
		t.writeOut(FormatProgramStatus(*t.programStatus))
	}
}

func (t *ProcessTerminal) setNegotiationBuffer(sequence string) {
	t.clearNegotiationFlushTimer()
	t.negotiationBuffer = sequence
}

func (t *ProcessTerminal) clearNegotiationBufferLocked() {
	t.clearNegotiationFlushTimer()
	t.negotiationBuffer = ""
}

func (t *ProcessTerminal) flushNegotiationBufferAsInput() {
	if t.negotiationBuffer == "" {
		return
	}
	sequence := t.negotiationBuffer
	t.clearNegotiationBufferLocked()
	t.mu.Unlock()
	if handler := t.inputHandler; handler != nil {
		handler(t.normalizedForward(sequence))
	}
	t.mu.Lock()
}

func (t *ProcessTerminal) scheduleNegotiationFlush() {
	if t.negotiationBuffer == "" || t.negotiationFlushTimer != nil {
		return
	}
	t.negotiationFlushTimer = time.AfterFunc(keyboardFragmentTimeoutMs*time.Millisecond, func() {
		t.mu.Lock()
		t.negotiationFlushTimer = nil
		t.flushNegotiationBufferAsInput()
		t.mu.Unlock()
	})
}

func (t *ProcessTerminal) clearNegotiationFlushTimer() {
	if t.negotiationFlushTimer != nil {
		t.negotiationFlushTimer.Stop()
		t.negotiationFlushTimer = nil
	}
}

func (t *ProcessTerminal) enableModifyOtherKeysLocked() {
	if t.kittyActive || t.modifyOtherKeysActive {
		return
	}
	t.writeOut("\x1b[>4;2m")
	t.modifyOtherKeysActive = true
}

func (t *ProcessTerminal) disableModifyOtherKeysLocked() {
	if !t.modifyOtherKeysActive {
		return
	}
	t.writeOut("\x1b[>4;0m")
	t.modifyOtherKeysActive = false
}

// DrainInput consumes pending stdin so Kitty key releases do not leak to the
// parent shell over slow SSH connections.
func (t *ProcessTerminal) DrainInput(maxMs, idleMs time.Duration) {
	t.mu.Lock()
	shouldDisableKitty := t.keyboardProtocolPushed || t.kittyActive
	t.clearNegotiationBufferLocked()
	if shouldDisableKitty {
		t.writeOut("\x1b[<u")
		t.keyboardProtocolPushed = false
		t.kittyActive = false
		SetKittyProtocolActive(false)
	}
	t.disableModifyOtherKeysLocked()

	previousHandler := t.inputHandler
	t.inputHandler = nil

	drainWatcher := make(chan []byte, 16)
	t.drainWatcher = drainWatcher

	t.mu.Unlock()

	lastDataTime := time.Now()
	endTime := time.Now().Add(maxMs)
	for {
		now := time.Now()
		timeLeft := endTime.Sub(now)
		if timeLeft <= 0 {
			break
		}
		if now.Sub(lastDataTime) >= idleMs {
			break
		}
		wait := idleMs
		if timeLeft < wait {
			wait = timeLeft
		}
		select {
		case <-drainWatcher:
			lastDataTime = time.Now()
		case <-time.After(wait):
		}
	}

	t.mu.Lock()
	t.drainWatcher = nil
	t.inputHandler = previousHandler
	t.mu.Unlock()
}

// Stop restores terminal state: progress indicator, bracketed paste, Kitty
// protocol, raw mode, and handlers.
// RestoreOnExit writes the terminal mode resets for exit paths that skip
// Stop: bracketed paste off, kitty keyboard pop, modifyOtherKeys off, and
// any progress spinner cleared, wrapped by the caller's before/after
// sequences.
func (t *ProcessTerminal) RestoreOnExit(before, after string) {
	t.mu.Lock()
	sequence := before
	if t.progressActive {
		sequence += terminalProgressClearSeq
	}
	sequence += "\x1b[?2004l"
	if t.keyboardProtocolPushed || t.kittyActive {
		sequence += "\x1b[<u"
	}
	if t.modifyOtherKeysActive {
		sequence += "\x1b[>4;0m"
	}
	sequence += after
	t.mu.Unlock()
	// os.Stdout writes are synchronous on POSIX TTYs; a Windows
	// asynchronous console cannot flush during exit (documented deviation).
	_, _ = os.Stdout.WriteString(sequence)
}

func (t *ProcessTerminal) Stop() {
	t.mu.Lock()
	if t.progressActive {
		t.clearProgressIntervalLocked()
		t.writeOut(terminalProgressClearSeq)
	}
	// Remove the status while stopped, for example after exit or while
	// suspended; start() reports it again.
	if t.programStatusSupported && t.programStatus != nil {
		t.writeOut(FormatProgramStatus(ProgramStatus{State: "clear"}))
	}
	t.programStatusSupported = false
	t.programStatusQueryPending = false

	t.writeOut("\x1b[?2004l") // bracketed paste off

	shouldDisableKitty := t.keyboardProtocolPushed || t.kittyActive
	t.clearNegotiationBufferLocked()
	if shouldDisableKitty {
		t.stdoutWrite("\x1b[<u")
		t.keyboardProtocolPushed = false
		t.kittyActive = false
		SetKittyProtocolActive(false)
	}
	t.disableModifyOtherKeysLocked()

	if t.stdinBuffer != nil {
		t.stdinBuffer.Destroy()
		t.stdinBuffer = nil
	}
	t.inputHandler = nil
	t.resizeHandler = nil

	stdinStop := t.stdinReaderStop
	resizeStop := t.resizeStop
	t.mu.Unlock()

	if stdinStop != nil {
		close(stdinStop)
		// The reader goroutine exits on its next read boundary; stdin in raw
		// mode is not cancelable mid-read, mirroring the reference's
		// best-effort pause.
	}
	if resizeStop != nil {
		close(resizeStop)
	}

	terminalSetRaw(os.Stdin, t.wasRaw)
}

func (t *ProcessTerminal) stdoutWrite(data string) {
	os.Stdout.WriteString(data)
	if t.writeLogPath != "" {
		f, err := os.OpenFile(t.writeLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			f.WriteString(data)
			f.Close()
		}
	}
}

// Write writes output to the terminal (and the write log when configured).
func (t *ProcessTerminal) Write(data string) {
	t.mu.Lock()
	writeOut := t.writeOut
	t.mu.Unlock()
	writeOut(data)
}

// Columns returns the terminal width in columns.
func (t *ProcessTerminal) Columns() int {
	if cols := terminalSize(os.Stdout); cols > 0 {
		return cols
	}
	if cols, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && cols > 0 {
		return cols
	}
	return 80
}

// Rows returns the terminal height in rows.
func (t *ProcessTerminal) Rows() int {
	if rows := terminalRows(os.Stdout); rows > 0 {
		return rows
	}
	if rows, err := strconv.Atoi(os.Getenv("LINES")); err == nil && rows > 0 {
		return rows
	}
	return 24
}

// MoveBy moves the cursor up (negative) or down (positive).
func (t *ProcessTerminal) MoveBy(lines int) {
	if lines > 0 {
		t.Write("\x1b[" + strconv.Itoa(lines) + "B")
	} else if lines < 0 {
		t.Write("\x1b[" + strconv.Itoa(-lines) + "A")
	}
}

// HideCursor hides the cursor.
func (t *ProcessTerminal) HideCursor() { t.Write("\x1b[?25l") }

// ShowCursor shows the cursor.
func (t *ProcessTerminal) ShowCursor() { t.Write("\x1b[?25h") }

// ClearLine clears the current line.
func (t *ProcessTerminal) ClearLine() { t.Write("\x1b[K") }

// ClearFromCursor clears from the cursor to the end of the screen.
func (t *ProcessTerminal) ClearFromCursor() { t.Write("\x1b[J") }

// ClearScreen clears the screen and moves the cursor home.
func (t *ProcessTerminal) ClearScreen() { t.Write("\x1b[2J\x1b[H") }

// SetTitle sets the terminal window title (OSC 0).
func (t *ProcessTerminal) SetTitle(title string) { t.Write("\x1b]0;" + title + "\x07") }

// SetProgress shows or clears an indeterminate progress indicator (OSC 9;4),
// keeping it alive with a periodic re-write while active.
func (t *ProcessTerminal) SetProgress(active bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if active {
		t.writeOut(terminalProgressActiveSeq)
		if t.progressInterval == nil {
			t.progressActive = true
			t.progressInterval = time.AfterFunc(terminalProgressKeepaliveMs*time.Millisecond, func() {
				t.mu.Lock()
				defer t.mu.Unlock()
				if t.progressActive {
					t.writeOut(terminalProgressActiveSeq)
					t.progressInterval.Reset(terminalProgressKeepaliveMs * time.Millisecond)
				}
			})
		}
	} else {
		t.clearProgressIntervalLocked()
		t.writeOut(terminalProgressClearSeq)
	}
}

func (t *ProcessTerminal) clearProgressIntervalLocked() {
	t.progressActive = false
	if t.progressInterval != nil {
		t.progressInterval.Stop()
		t.progressInterval = nil
	}
}

func runtimeGOOS() string { return runtime.GOOS }
