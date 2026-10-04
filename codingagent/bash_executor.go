package codingagent

// Port of core/bash-executor.ts: unified bash execution with streaming
// sanitization, rolling buffers, and temp-file spill, plus utils/ansi.ts's
// stripAnsi.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gladmo/openagent/abort"
)

// ansiPattern matches CSI/OSC escape sequences and other control
// sequences the reference's stripAnsi removes.
var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[PX^_][^\x1b]*\x1b\\\\|\x07|[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]")

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

	ensureTempFile := func() {
		if tempFilePath != "" {
			return
		}
		path := filepath.Join(os.TempDir(), "pi-bash-"+randomHex(4)+".log")
		file, err := os.Create(path)
		if err != nil {
			return
		}
		tempFilePath = path
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
		text := strings.ReplaceAll(SanitizeBinaryOutput(StripAnsi(string(valid))), "\r", "")

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
