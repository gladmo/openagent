package tui

// StdinBuffer buffers input and emits complete sequences: stdin chunks can
// split escape sequences (especially mouse events) across events, and partial
// sequences would be misinterpreted as keypresses. Port of stdin-buffer.ts
// (based on OpenTUI). Indices are rune-based; escape sequences are ASCII so
// this matches the reference's UTF-16 indexing for every sequence it detects.

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	escRune                  = '\x1b'
	defaultSequenceTimeoutMs = 50
	defaultEscapeTimeoutMs   = 10
	bracketedPasteStartSeq   = "\x1b[200~"
	bracketedPasteEndSeq     = "\x1b[201~"
)

type sequenceStatus int

const (
	seqNotEscape sequenceStatus = iota
	seqIncomplete
	seqComplete
)

// isCompleteSequence checks whether data is a complete escape sequence, needs
// more data, or is not an escape sequence at all.
func isCompleteSequence(data []rune) sequenceStatus {
	if len(data) == 0 || data[0] != escRune {
		return seqNotEscape
	}
	if len(data) == 1 {
		return seqIncomplete
	}

	afterEsc := data[1:]

	if afterEsc[0] == '[' {
		// Old-style mouse: ESC[M + 3 bytes = 6 total
		if len(afterEsc) >= 2 && afterEsc[1] == 'M' {
			if len(data) >= 6 {
				return seqComplete
			}
			return seqIncomplete
		}
		return isCompleteCsiSequence(data)
	}

	if afterEsc[0] == ']' {
		return isCompleteOscSequence(data)
	}
	if afterEsc[0] == 'P' {
		return isCompleteDcsSequence(data)
	}
	if afterEsc[0] == '_' {
		return isCompleteApcSequence(data)
	}
	if afterEsc[0] == 'O' {
		if len(afterEsc) >= 2 {
			return seqComplete
		}
		return seqIncomplete
	}
	// Meta key sequence: ESC + single character; unknown sequences complete.
	return seqComplete
}

// isCompleteCsiSequence: CSI sequences end with a final byte 0x40-0x7E.
func isCompleteCsiSequence(data []rune) sequenceStatus {
	if len(data) < 2 || data[0] != escRune || data[1] != '[' {
		return seqComplete
	}
	if len(data) < 3 {
		return seqIncomplete
	}

	payload := data[2:]
	lastChar := payload[len(payload)-1]

	if lastChar >= 0x40 && lastChar <= 0x7e {
		// SGR mouse sequences: ESC[<B;X;Ym or ESC[<B;X;YM
		if payload[0] == '<' {
			if sgrMousePattern.MatchString(string(payload)) {
				return seqComplete
			}
			if lastChar == 'M' || lastChar == 'm' {
				parts := strings.Split(string(payload[1:len(payload)-1]), ";")
				if len(parts) == 3 && allDigits(parts) {
					return seqComplete
				}
			}
			return seqIncomplete
		}
		return seqComplete
	}

	return seqIncomplete
}

var sgrMousePattern = regexp.MustCompile(`^<\d+;\d+;\d+[Mm]$`)

func allDigits(parts []string) bool {
	for _, part := range parts {
		if len(part) == 0 {
			return false
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// isCompleteOscSequence: OSC ends with ST (ESC \) or BEL.
func isCompleteOscSequence(data []rune) sequenceStatus {
	if len(data) < 2 || data[0] != escRune || data[1] != ']' {
		return seqComplete
	}
	if len(data) >= 2 && data[len(data)-1] == '\x07' {
		return seqComplete
	}
	if len(data) >= 3 && data[len(data)-2] == escRune && data[len(data)-1] == '\\' {
		return seqComplete
	}
	return seqIncomplete
}

// isCompleteDcsSequence: DCS (XTVersion responses) ends with ST.
func isCompleteDcsSequence(data []rune) sequenceStatus {
	if len(data) < 2 || data[0] != escRune || data[1] != 'P' {
		return seqComplete
	}
	if len(data) >= 3 && data[len(data)-2] == escRune && data[len(data)-1] == '\\' {
		return seqComplete
	}
	return seqIncomplete
}

// isCompleteApcSequence: APC (Kitty graphics responses) ends with ST.
func isCompleteApcSequence(data []rune) sequenceStatus {
	if len(data) < 2 || data[0] != escRune || data[1] != '_' {
		return seqComplete
	}
	if len(data) >= 3 && data[len(data)-2] == escRune && data[len(data)-1] == '\\' {
		return seqComplete
	}
	return seqIncomplete
}

var unmodifiedKittyPrintablePattern = regexp.MustCompile(`^\x1b\[(\d+)(?::\d*)?(?::\d+)?u$`)

func parseUnmodifiedKittyPrintableCodepoint(sequence string) (int, bool) {
	match := unmodifiedKittyPrintablePattern.FindStringSubmatch(sequence)
	if match == nil {
		return 0, false
	}
	codepoint, _ := strconv.Atoi(match[1])
	if codepoint >= 32 {
		return codepoint, true
	}
	return 0, false
}

// extractCompleteSequencesResult carries split sequences plus any trailing
// incomplete remainder.
type extractCompleteSequencesResult struct {
	sequences []string
	remainder []rune
}

// terminalReplyHeaderPattern recognizes color and program-status OSC
// replies, XTVersion, and Kitty graphics replies. Until their header is
// complete, the input may be batched legacy Alt keystrokes.
var terminalReplyHeaderPattern = regexp.MustCompile(`^\x1b(?:\](?:1[01];|4;\d{1,3};|7501;\?)|P>\||_G(?:[a-z]=\d+,)*[a-z]=\d+;)`)

func isControlStringRunes(data []rune) bool {
	return len(data) >= 2 && data[0] == escRune && (data[1] == ']' || data[1] == 'P' || data[1] == '_')
}

func extractCompleteSequences(buffer []rune) extractCompleteSequencesResult {
	var sequences []string
	pos := 0

	for pos < len(buffer) {
		remaining := buffer[pos:]

		if remaining[0] == escRune {
			terminalReply := isControlStringRunes(remaining) && terminalReplyHeaderPattern.MatchString(string(remaining))
			seqEnd := 1
			emitted := false
			for seqEnd <= len(remaining) {
				if terminalReply && seqEnd > 2 {
					char := remaining[seqEnd-1]
					if char == '\x18' || char == '\x1a' {
						// CAN and SUB cancel the string, not subsequent
						// keyboard input.
						pos += seqEnd
						emitted = true
						break
					}
					if char == escRune && seqEnd < len(remaining) && remaining[seqEnd] != '\\' {
						// A new escape sequence aborts an unterminated
						// reply. Keep a trailing ESC buffered until we can
						// distinguish it from ST.
						pos += seqEnd - 1
						emitted = true
						break
					}
				}
				candidate := remaining[:seqEnd]
				status := isCompleteSequence(candidate)

				if status == seqComplete {
					// WezTerm's kitty keyboard sends Escape press as a raw
					// '\x1b' byte and the release as a full CSI-u sequence,
					// concatenated as '\x1b\x1b[27;...u'. If the char after
					// '\x1b\x1b' starts a new escape sequence, emit only the
					// first ESC and restart from the second.
					if seqEnd == 2 && remaining[1] == escRune && seqEnd < len(remaining) {
						next := remaining[seqEnd]
						if next == '[' || next == ']' || next == 'O' || next == 'P' || next == '_' {
							sequences = append(sequences, "\x1b")
							pos++
							emitted = true
							break
						}
					}
					sequences = append(sequences, string(candidate))
					pos += seqEnd
					emitted = true
					break
				} else if status == seqIncomplete {
					seqEnd++
				} else {
					// Should not happen when starting with ESC
					sequences = append(sequences, string(candidate))
					pos += seqEnd
					emitted = true
					break
				}
			}

			if !emitted && seqEnd > len(remaining) {
				return extractCompleteSequencesResult{sequences: sequences, remainder: remaining}
			}
		} else {
			// Not an escape sequence - take a single character
			sequences = append(sequences, string(remaining[0]))
			pos++
		}
	}

	return extractCompleteSequencesResult{sequences: sequences}
}

// StdinBufferOptions configures the reassembly windows.
type StdinBufferOptions struct {
	// TimeoutMs waits for an incomplete CSI/mouse sequence (default 50ms).
	TimeoutMs int
	// EscapeTimeoutMs waits after a lone ESC before treating it as Escape
	// (default 10ms). Increase for high-latency Alt+key input (SSH).
	EscapeTimeoutMs int
}

// StdinBuffer buffers stdin input and emits complete sequences through the
// Data handler; bracketed paste content arrives through the Paste handler.
type StdinBuffer struct {
	mu sync.Mutex

	dataHandler  func(sequence string)
	pasteHandler func(content string)

	buffer                         []rune
	timeout                        *time.Timer
	timeoutMs                      int
	escapeTimeMs                   int
	pasteMode                      bool
	pasteBuffer                    []rune
	pendingKittyPrintableCodepoint int
	pendingOK                      bool

	// After is the timer scheduler (injectable for tests).
	After func(d time.Duration, fn func()) *time.Timer
}

// NewStdinBuffer builds a buffer with the given handlers and options.
func NewStdinBuffer(dataHandler func(string), pasteHandler func(string), options StdinBufferOptions) *StdinBuffer {
	timeoutMs := options.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = defaultSequenceTimeoutMs
	}
	escapeMs := options.EscapeTimeoutMs
	if escapeMs == 0 {
		escapeMs = defaultEscapeTimeoutMs
	}
	return &StdinBuffer{
		dataHandler:  dataHandler,
		pasteHandler: pasteHandler,
		timeoutMs:    timeoutMs,
		escapeTimeMs: escapeMs,
		After:        time.AfterFunc,
	}
}

// Process feeds one chunk of input data. A single high byte (>127) converts
// to ESC + (byte-128) for meta-key compatibility.
func (b *StdinBuffer) Process(data []byte) {
	for {
		b.mu.Lock()
		dataSequences, pasteContent, reprocess := b.processLocked(data)
		b.mu.Unlock()

		if pasteContent != nil && b.pasteHandler != nil {
			b.pasteHandler(*pasteContent)
		}
		for _, sequence := range dataSequences {
			b.emitDataSequence(sequence)
		}
		if reprocess == nil {
			return
		}
		data = reprocess
	}
}

// processLocked runs one processing pass; the caller emits results after
// releasing the lock. reprocess is non-nil when paste handling split the input.
func (b *StdinBuffer) processLocked(data []byte) (dataSequences []string, pasteContent *string, reprocess []byte) {
	b.clearTimeoutLocked()

	var str []rune
	if len(data) == 1 && data[0] > 127 {
		str = []rune{'\x1b', rune(data[0] - 128)}
	} else {
		str = []rune(string(data))
	}

	if len(str) == 0 && len(b.buffer) == 0 {
		return []string{""}, nil, nil
	}

	b.buffer = append(b.buffer, str...)

	if b.pasteMode {
		b.pasteBuffer = append(b.pasteBuffer, b.buffer...)
		b.buffer = nil

		endIndex := runesIndex(b.pasteBuffer, []rune(bracketedPasteEndSeq))
		if endIndex == -1 {
			return nil, nil, nil
		}
		content := string(b.pasteBuffer[:endIndex])
		remaining := append([]rune(nil), b.pasteBuffer[endIndex+len(bracketedPasteEndSeq):]...)

		b.pasteMode = false
		b.pasteBuffer = nil
		b.pendingKittyPrintableCodepoint = 0
		b.pendingOK = false

		if len(remaining) > 0 {
			return nil, &content, []byte(string(remaining))
		}
		return nil, &content, nil
	}

	if startIndex := runesIndex(b.buffer, []rune(bracketedPasteStartSeq)); startIndex != -1 {
		if startIndex > 0 {
			dataSequences = extractCompleteSequences(b.buffer[:startIndex]).sequences
		}

		b.pendingKittyPrintableCodepoint = 0
		b.pendingOK = false
		b.pasteBuffer = append([]rune(nil), b.buffer[startIndex+len(bracketedPasteStartSeq):]...)
		b.buffer = nil
		b.pasteMode = true

		if endIndex := runesIndex(b.pasteBuffer, []rune(bracketedPasteEndSeq)); endIndex != -1 {
			content := string(b.pasteBuffer[:endIndex])
			remaining := append([]rune(nil), b.pasteBuffer[endIndex+len(bracketedPasteEndSeq):]...)

			b.pasteMode = false
			b.pasteBuffer = nil
			b.pendingKittyPrintableCodepoint = 0
			b.pendingOK = false

			if len(remaining) > 0 {
				return dataSequences, &content, []byte(string(remaining))
			}
			return dataSequences, &content, nil
		}
		return dataSequences, nil, nil
	}

	result := extractCompleteSequences(b.buffer)
	b.buffer = append([]rune(nil), result.remainder...)

	if len(b.buffer) > 0 {
		// Only a recognizable reply header establishes a delimited string.
		// Flushing it would turn a delayed tail into text and an OSC's BEL
		// terminator into Ctrl+G (the external editor shortcut). Other
		// input, including Alt+] followed by text, must retain bounded
		// recovery.
		if terminalReplyHeaderPattern.MatchString(string(b.buffer)) {
			return result.sequences, nil, nil
		}
		timeoutMs := b.timeoutMs
		if len(b.buffer) == 1 && b.buffer[0] == escRune {
			timeoutMs = b.escapeTimeMs
		}
		b.timeout = b.After(time.Duration(timeoutMs)*time.Millisecond, func() {
			b.mu.Lock()
			flushed := b.flushLocked()
			b.mu.Unlock()
			for _, sequence := range flushed {
				runes := []rune(sequence)
				if len(runes) > 2 && isControlStringRunes(runes) {
					// Recover the Alt key and reparse its tail so batched
					// text, Enter, and control keys are not swallowed as
					// one event.
					b.emitDataSequence(string(runes[:2]))
					b.Process([]byte(string(runes[2:])))
				} else {
					b.emitDataSequence(sequence)
				}
			}
		})
	}
	return result.sequences, nil, nil
}

func (b *StdinBuffer) emitDataSequence(sequence string) {
	b.mu.Lock()
	rawCodepoint := -1
	runes := []rune(sequence)
	if len(runes) == 1 {
		rawCodepoint = int(runes[0])
	}
	if rawCodepoint != -1 && b.pendingOK && rawCodepoint == b.pendingKittyPrintableCodepoint {
		b.pendingKittyPrintableCodepoint = 0
		b.pendingOK = false
		b.mu.Unlock()
		return
	}

	if cp, ok := parseUnmodifiedKittyPrintableCodepoint(sequence); ok {
		b.pendingKittyPrintableCodepoint = cp
		b.pendingOK = true
	} else {
		b.pendingKittyPrintableCodepoint = 0
		b.pendingOK = false
	}
	handler := b.dataHandler
	b.mu.Unlock()
	if handler != nil {
		handler(sequence)
	}
}

func runesIndex(haystack, needle []rune) int {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func (b *StdinBuffer) clearTimeoutLocked() {
	if b.timeout != nil {
		b.timeout.Stop()
		b.timeout = nil
	}
}

// Flush flushes any pending buffer as a single sequence after a timeout.
func (b *StdinBuffer) Flush() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.flushLocked()
}

func (b *StdinBuffer) flushLocked() []string {
	b.clearTimeoutLocked()
	if len(b.buffer) == 0 {
		return nil
	}
	sequences := []string{string(b.buffer)}
	b.buffer = nil
	b.pendingKittyPrintableCodepoint = 0
	b.pendingOK = false
	return sequences
}

// Clear drops all buffered state.
func (b *StdinBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clearTimeoutLocked()
	b.buffer = nil
	b.pasteMode = false
	b.pasteBuffer = nil
	b.pendingKittyPrintableCodepoint = 0
	b.pendingOK = false
}

// GetBuffer returns the current pending buffer.
func (b *StdinBuffer) GetBuffer() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buffer)
}

// Destroy clears all state; the buffer cannot be reused afterwards.
func (b *StdinBuffer) Destroy() {
	b.Clear()
}
