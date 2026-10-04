package codingagent

// Port of core/tools/output-accumulator.ts: bounded-memory streaming output
// with a decoded rolling tail and spill-to-temp-file persistence.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"
)

// OutputAccumulatorOptions configure the accumulator.
type OutputAccumulatorOptions struct {
	MaxLines       *int
	MaxBytes       *int
	TempFilePrefix string
}

// OutputSnapshot is a bounded view of the accumulated output.
type OutputSnapshot struct {
	Content        string
	Truncation     TruncationResult
	FullOutputPath string
}

func defaultTempFilePath(prefix string) string {
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	return filepath.Join(os.TempDir(), prefix+"-"+hex.EncodeToString(id)+".log")
}

// OutputAccumulator tracks streaming output incrementally.
type OutputAccumulator struct {
	maxLines        int
	maxBytes        int
	maxRollingBytes int
	tempFilePrefix  string

	mu sync.Mutex

	decoderPending           []byte // undecoded partial UTF-8 prefix
	rawChunks                [][]byte
	tailText                 string
	tailBytes                int
	tailStartsAtLineBoundary bool
	totalRawBytes            int
	totalDecodedBytes        int
	completedLines           int
	totalLines               int
	currentLineBytes         int
	hasOpenLine              bool
	finished                 bool

	tempFilePath string
	tempFile     *os.File
}

// NewOutputAccumulator builds the accumulator.
func NewOutputAccumulator(options *OutputAccumulatorOptions) *OutputAccumulator {
	maxLines, maxBytes := DefaultMaxLines, DefaultMaxBytes
	tempFilePrefix := "pi-output"
	if options != nil {
		if options.MaxLines != nil {
			maxLines = *options.MaxLines
		}
		if options.MaxBytes != nil {
			maxBytes = *options.MaxBytes
		}
		if options.TempFilePrefix != "" {
			tempFilePrefix = options.TempFilePrefix
		}
	}
	rolling := maxBytes * 2
	if rolling < 1 {
		rolling = 1
	}
	return &OutputAccumulator{
		maxLines:        maxLines,
		maxBytes:        maxBytes,
		maxRollingBytes: rolling,
		tempFilePrefix:  tempFilePrefix,
		// The tail begins at a line boundary (mirror of the TS field init).
		tailStartsAtLineBoundary: true,
	}
}

// Append adds one output chunk.
func (a *OutputAccumulator) Append(data []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		panic("Cannot append to a finished output accumulator")
	}

	a.totalRawBytes += len(data)
	a.appendDecodedText(decodeStreaming(&a.decoderPending, data))

	if a.tempFile != nil || a.shouldUseTempFile() {
		a.ensureTempFile()
		if a.tempFile != nil {
			_, _ = a.tempFile.Write(data)
		}
	} else if len(data) > 0 {
		a.rawChunks = append(a.rawChunks, append([]byte(nil), data...))
	}
}

// decodeStreaming decodes complete runes from the buffer, retaining any
// trailing partial sequence for the next chunk.
func decodeStreaming(pending *[]byte, data []byte) string {
	buffer := append(append([]byte(nil), (*pending)...), data...)
	*pending = nil

	valid := buffer
	for len(valid) > 0 && !utf8.Valid(valid) {
		// Walk back to find the longest valid prefix; partial multi-byte
		// sequences at the end stay pending.
		cut := len(valid)
		for cut > 0 && (valid[cut-1]&0xC0) == 0x80 {
			cut--
		}
		if cut > 0 {
			r, size := utf8.DecodeRune(valid[cut-1:])
			_ = r
			if size == 1 && valid[cut-1] >= 0x80 {
				*pending = append([]byte(nil), valid[cut-1:]...)
				valid = valid[:cut-1]
				break
			}
		}
		break
	}
	if len(valid) > 0 && !utf8.Valid(valid) {
		// Fallback: keep everything pending (never expected beyond a
		// partial tail).
		*pending = append([]byte(nil), valid...)
		return ""
	}
	return string(valid)
}

// Finish flushes the decoder and finalizes state.
func (a *OutputAccumulator) Finish() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.finished = true
	flushed := a.decoderPending
	a.decoderPending = nil
	if len(flushed) > 0 {
		// Lone partial bytes decode as replacement characters, matching the
		// streaming TextDecoder's final flush.
		a.appendDecodedText(string(flushed))
	}
	if a.shouldUseTempFile() {
		a.ensureTempFile()
	}
}

// Snapshot returns the bounded view; persistIfTruncated opens the temp file
// when the output exceeded the limits.
func (a *OutputAccumulator) Snapshot(persistIfTruncated bool) OutputSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()

	tailTruncation := TruncateTail(a.getSnapshotText(), TruncationOptions{
		MaxLines: &a.maxLines,
		MaxBytes: &a.maxBytes,
	})
	truncated := a.totalLines > a.maxLines || a.totalDecodedBytes > a.maxBytes
	truncatedBy := ""
	if truncated {
		truncatedBy = tailTruncation.TruncatedBy
		if truncatedBy == "" {
			truncatedBy = "lines"
			if a.totalDecodedBytes > a.maxBytes {
				truncatedBy = "bytes"
			}
		}
	}
	truncation := tailTruncation
	truncation.Truncated = truncated
	truncation.TruncatedBy = truncatedBy
	truncation.TotalLines = a.totalLines
	truncation.TotalBytes = a.totalDecodedBytes
	truncation.MaxLines = a.maxLines
	truncation.MaxBytes = a.maxBytes

	if persistIfTruncated && truncation.Truncated {
		a.ensureTempFile()
	}

	fullOutputPath := ""
	if a.tempFilePath != "" {
		fullOutputPath = a.tempFilePath
	}
	return OutputSnapshot{Content: truncation.Content, Truncation: truncation, FullOutputPath: fullOutputPath}
}

// CloseTempFile flushes and closes the spill file.
func (a *OutputAccumulator) CloseTempFile() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tempFile == nil {
		return nil
	}
	err := a.tempFile.Close()
	a.tempFile = nil
	return err
}

// GetLastLineBytes returns the byte length of the current (possibly open)
// line.
func (a *OutputAccumulator) GetLastLineBytes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentLineBytes
}

func (a *OutputAccumulator) appendDecodedText(text string) {
	if len(text) == 0 {
		return
	}

	bytes := len(text)
	a.totalDecodedBytes += bytes
	a.tailText += text
	a.tailBytes += bytes
	if a.tailBytes > a.maxRollingBytes*2 {
		a.trimTail()
	}

	newlines := 0
	lastNewline := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			newlines++
			lastNewline = i
		}
	}
	if newlines == 0 {
		a.currentLineBytes += bytes
		a.hasOpenLine = true
	} else {
		a.completedLines += newlines
		tail := text[lastNewline+1:]
		a.currentLineBytes = len(tail)
		a.hasOpenLine = len(tail) > 0
	}
	a.totalLines = a.completedLines
	if a.hasOpenLine {
		a.totalLines++
	}
}

func (a *OutputAccumulator) trimTail() {
	if a.tailBytes <= a.maxRollingBytes {
		return
	}

	buffer := []byte(a.tailText)
	start := len(buffer) - a.maxRollingBytes
	for start < len(buffer) && (buffer[start]&0xC0) == 0x80 {
		start++
	}

	if start > 0 {
		a.tailStartsAtLineBoundary = buffer[start-1] == 0x0a
	}
	a.tailText = string(buffer[start:])
	a.tailBytes = len(a.tailText)
}

func (a *OutputAccumulator) getSnapshotText() string {
	if a.tailStartsAtLineBoundary {
		return a.tailText
	}
	firstNewline := -1
	for i := 0; i < len(a.tailText); i++ {
		if a.tailText[i] == '\n' {
			firstNewline = i
			break
		}
	}
	if firstNewline == -1 {
		return a.tailText
	}
	return a.tailText[firstNewline+1:]
}

func (a *OutputAccumulator) shouldUseTempFile() bool {
	return a.totalRawBytes > a.maxBytes || a.totalDecodedBytes > a.maxBytes || a.totalLines > a.maxLines
}

func (a *OutputAccumulator) ensureTempFile() {
	if a.tempFilePath != "" {
		return
	}
	a.tempFilePath = defaultTempFilePath(a.tempFilePrefix)
	file, err := os.Create(a.tempFilePath)
	if err != nil {
		a.tempFilePath = ""
		return
	}
	a.tempFile = file
	for _, chunk := range a.rawChunks {
		_, _ = file.Write(chunk)
	}
	a.rawChunks = nil
}

var _ = fmt.Sprintf
