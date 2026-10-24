package codingagent

// Port of core/bash-executor.ts: unified bash execution with streaming
// sanitization, rolling buffers, and temp-file spill, plus utils/ansi.ts's
// stripAnsi.

import (
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gladmo/openagent/abort"
)

// ansiPattern matches CSI/OSC escape sequences and other control
// sequences the reference's stripAnsi removes.
var ansiPattern = regexp.MustCompile("\\x1b\\[[0-9;]*[a-zA-Z]|\\x1b\\][^\\x07\\x1b\\x9c]*(?:\\x07|\\x1b\\\\|\\x9c)|\\x1b[PX^_][^\\x1b]*\\x1b\\\\|\\x07|[\\x00-\\x08\\x0b\\x0c\\x0e-\\x1f\\x7f]")

// StripAnsi removes ANSI escape sequences and control characters.
func StripAnsi(value string) string {
	return ansiPattern.ReplaceAllString(value, "")
}

// BashExecutorOptions carry the streaming callback and cancellation.
type BashExecutorOptions struct {
	// OnChunk receives sanitized output chunks.
	OnChunk func(chunk string)
	Signal  *abort.Signal
}

// BashResult reports the execution outcome.
type BashResult struct {
	// Output is the combined stdout + stderr (sanitized, truncated).
	Output string
	// ExitCode is nil when killed/cancelled.
	ExitCode *int
	// Cancelled reports signal cancellation.
	Cancelled bool
	// Truncated reports output truncation.
	Truncated bool
	// FullOutputPath is the spill file when output exceeded limits.
	FullOutputPath string
}

const bashExecutorMaxOutputBytes = DefaultMaxBytes * 2

// ExecuteBashWithOperations runs a command through custom operations
// (remote execution) with streaming sanitization and spill.
func ExecuteBashWithOperations(command, cwd string, operations BashOperations, options *BashExecutorOptions) (*BashResult, error) {
	var mu sync.Mutex
	var outputChunks []string
	outputBytes := 0

	tempFilePath := ""
	var tempFile *os.File
	totalBytes := 0

	decoderPending := []byte(nil)
	// Unfinished escape sequence at the end of the previous chunk,
	// completed by the next chunk.
	pendingAnsi := ""

	ensureTempFile := func() {
		if tempFilePath != "" {
			return
		}
		file, err := CreateOutputFileStream("pi-bash", ".log")
		if err != nil {
			return
		}
		tempFilePath = file.Name()
		tempFile = file
		for _, chunk := range outputChunks {
			_, _ = file.WriteString(chunk)
		}
	}

	onData := func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		totalBytes += len(data)

		// Streaming UTF-8 decode with pending partials, then sanitize.
		buffer := append(append([]byte(nil), decoderPending...), data...)
		valid := buffer
		decoderPending = nil
		for len(valid) > 0 && !utf8.Valid(valid) {
			cut := len(valid)
			for cut > 0 && (valid[cut-1]&0xC0) == 0x80 {
				cut--
			}
			if cut > 0 {
				decoderPending = append([]byte(nil), valid[cut-1:]...)
				valid = valid[:cut-1]
			}
			break
		}
		if len(valid) > 0 && !utf8.Valid(valid) {
			decoderPending = append([]byte(nil), valid...)
			return
		}
		decoded := pendingAnsi + string(valid)
		var completeText string
		completeText, pendingAnsi = SplitIncompleteAnsiSuffix(decoded)
		text := strings.ReplaceAll(SanitizeBinaryOutput(StripAnsi(completeText)), "\r", "")

		if totalBytes > DefaultMaxBytes {
			ensureTempFile()
		}
		if tempFile != nil {
			_, _ = tempFile.WriteString(text)
		}

		outputChunks = append(outputChunks, text)
		outputBytes += len(text)
		for outputBytes > bashExecutorMaxOutputBytes && len(outputChunks) > 1 {
			removed := outputChunks[0]
			outputChunks = outputChunks[1:]
			outputBytes -= len(removed)
		}

		if options != nil && options.OnChunk != nil {
			options.OnChunk(text)
		}
	}

	flushTail := func() {
		// End of stream: whatever was held back (an unfinished escape
		// sequence, a partial UTF-8 rune) is emitted as-is, matching the
		// reference's flushOutput.
		rest := pendingAnsi + string(decoderPending)
		pendingAnsi, decoderPending = "", nil
		if rest == "" {
			return
		}
		text := strings.ReplaceAll(SanitizeBinaryOutput(StripAnsi(rest)), "\r", "")
		if text == "" {
			return
		}
		if totalBytes > DefaultMaxBytes {
			ensureTempFile()
		}
		if tempFile != nil {
			_, _ = tempFile.WriteString(text)
		}
		outputChunks = append(outputChunks, text)
		outputBytes += len(text)
		for outputBytes > bashExecutorMaxOutputBytes && len(outputChunks) > 1 {
			removed := outputChunks[0]
			outputChunks = outputChunks[1:]
			outputBytes -= len(removed)
		}
		if options != nil && options.OnChunk != nil {
			options.OnChunk(text)
		}
	}

	finalize := func() (*BashResult, bool) {
		mu.Lock()
		defer mu.Unlock()
		flushTail()
		fullOutput := strings.Join(outputChunks, "")
		truncation := TruncateTail(fullOutput, TruncationOptions{})
		if truncation.Truncated {
			ensureTempFile()
		}
		if tempFile != nil {
			tempFile.Close()
			tempFile = nil
		}
		cancelled := options != nil && options.Signal != nil && options.Signal.Aborted()

		output := fullOutput
		if truncation.Truncated {
			output = truncation.Content
		}
		return &BashResult{
			Output:         output,
			Cancelled:      cancelled,
			Truncated:      truncation.Truncated,
			FullOutputPath: tempFilePath,
		}, cancelled
	}

	var signal *abort.Signal
	if options != nil {
		signal = options.Signal
	}
	result, execErr := operations.Exec(command, cwd, BashExecOptions{
		OnData: onData,
		Signal: signal,
	})
	if execErr != nil {
		// Abort path: still produce a result.
		if options != nil && options.Signal != nil && options.Signal.Aborted() {
			bashResult, _ := finalize()
			bashResult.Cancelled = true
			bashResult.ExitCode = nil
			return bashResult, nil
		}
		if tempFile != nil {
			tempFile.Close()
		}
		return nil, execErr
	}

	bashResult, _ := finalize()
	if bashResult.Cancelled {
		bashResult.ExitCode = nil
	} else if result.ExitCode != nil {
		code := *result.ExitCode
		bashResult.ExitCode = &code
	}
	return bashResult, nil
}

// maxPendingAnsiLength bounds the unfinished sequence held back while
// streaming; longer ones are processed as-is. 256 bytes matches the
// reference window (256 code units) and covers real OSC 8/7501 payloads.
const maxPendingAnsiLength = 256

// SplitIncompleteAnsiSuffix holds back an unfinished trailing escape
// sequence so a chunk boundary inside it cannot leak partial codes: an OSC
// without its terminator (a trailing ESC may start ESC\\), or a CSI
// without its final byte. RE2 has no lookahead, so the scan is procedural.
func SplitIncompleteAnsiSuffix(value string) (complete, pending string) {
	if !strings.ContainsRune(value, '\x1b') && !strings.ContainsRune(value, '\x9b') {
		return value, ""
	}
	windowStart := len(value) - maxPendingAnsiLength
	if windowStart < 0 {
		windowStart = 0
	}
	window := value[windowStart:]
	splitAt := -1
	for index := 0; index < len(window); index++ {
		switch window[index] {
		case '\x1b', '\x9b':
			if span, unfinished := unfinishedSequenceLength(window[index:]); unfinished && index+span == len(window) {
				splitAt = index
			}
		}
	}
	if splitAt == -1 {
		return value, ""
	}
	return value[:windowStart+splitAt], value[windowStart+splitAt:]
}

// unfinishedSequenceLength reports how many bytes the escape sequence at
// the start of window spans, and whether it lacks its terminator. A
// trailing ESC inside an OSC may start the ESC\\ terminator, so the
// sequence stays unfinished from that ESC.
func unfinishedSequenceLength(window string) (int, bool) {
	if len(window) >= 2 && window[0] == '\x1b' && window[1] == ']' {
		for index := 2; index < len(window); index++ {
			switch window[index] {
			case '\x07', '\x9c':
				return index + 1, false
			case '\x1b':
				if index+1 < len(window) && window[index+1] == '\\' {
					return index + 2, false
				}
				// A bare ESC is a legal body character (the TS body class
				// only excludes it when it starts the ST); keep scanning,
				// and hold the whole unterminated OSC when the window ends.
				if index == len(window)-1 {
					return index, true
				}
			}
		}
		return len(window), true
	}
	index := 1
	if window[0] == '\x1b' {
		if len(window) < 2 || window[1] != '[' {
			if len(window) == 1 {
				return 1, true
			}
			return 2, false
		}
		index = 2
	}
	for index < len(window) {
		b := window[index]
		if b >= 0x40 && b <= 0x7e {
			return index + 1, false
		}
		if b == '\x1b' || b == '\x07' || b == '\x9c' {
			return index, false
		}
		index++
	}
	return len(window), true
}
