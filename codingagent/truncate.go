package codingagent

// Port of core/tools/truncate.ts: head/tail truncation with independent
// line and byte limits.

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	DefaultMaxLines   = 2000
	DefaultMaxBytes   = 50 * 1024
	GrepMaxLineLength = 500
)

// TruncationResult carries the truncated content and limits metadata.
type TruncationResult struct {
	Content string
	// Truncated reports whether truncation occurred.
	Truncated bool
	// TruncatedBy names the hit limit: "lines", "bytes", or "".
	TruncatedBy string
	TotalLines  int
	TotalBytes  int
	OutputLines int
	OutputBytes int
	// LastLinePartial marks bash-tail partial-line truncation.
	LastLinePartial bool
	// FirstLineExceedsLimit marks head truncation where the first line alone
	// exceeded the byte limit.
	FirstLineExceedsLimit bool
	MaxLines              int
	MaxBytes              int
}

// TruncationOptions bound lines and bytes.
type TruncationOptions struct {
	MaxLines *int
	MaxBytes *int
}

func (o TruncationOptions) limits() (int, int) {
	maxLines, maxBytes := DefaultMaxLines, DefaultMaxBytes
	if o.MaxLines != nil {
		maxLines = *o.MaxLines
	}
	if o.MaxBytes != nil {
		maxBytes = *o.MaxBytes
	}
	return maxLines, maxBytes
}

func splitLinesForCounting(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// FormatSize renders bytes as a human-readable size.
func FormatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

// TruncateHead keeps the first N lines/bytes; never partial lines.
func TruncateHead(content string, options TruncationOptions) TruncationResult {
	maxLines, maxBytes := options.limits()

	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content: content, TotalLines: totalLines, TotalBytes: totalBytes,
			OutputLines: totalLines, OutputBytes: totalBytes,
			MaxLines: maxLines, MaxBytes: maxBytes,
		}
	}

	if len(lines) > 0 && len(lines[0]) > maxBytes {
		return TruncationResult{
			Truncated: true, TruncatedBy: "bytes",
			TotalLines: totalLines, TotalBytes: totalBytes,
			FirstLineExceedsLimit: true,
			MaxLines:              maxLines, MaxBytes: maxBytes,
		}
	}

	var outputLines []string
	outputBytesCount := 0
	truncatedBy := "lines"

	for i := 0; i < len(lines) && i < maxLines; i++ {
		lineBytes := len(lines[i])
		if i > 0 {
			lineBytes++ // newline
		}
		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		outputLines = append(outputLines, lines[i])
		outputBytesCount += lineBytes
	}

	if len(outputLines) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := strings.Join(outputLines, "\n")
	return TruncationResult{
		Content: outputContent, Truncated: true, TruncatedBy: truncatedBy,
		TotalLines: totalLines, TotalBytes: totalBytes,
		OutputLines: len(outputLines), OutputBytes: len(outputContent),
		MaxLines: maxLines, MaxBytes: maxBytes,
	}
}

// TruncateTail keeps the last N lines/bytes; may return a partial first
// line when the final line alone exceeds the byte limit.
func TruncateTail(content string, options TruncationOptions) TruncationResult {
	maxLines, maxBytes := options.limits()

	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content: content, TotalLines: totalLines, TotalBytes: totalBytes,
			OutputLines: totalLines, OutputBytes: totalBytes,
			MaxLines: maxLines, MaxBytes: maxBytes,
		}
	}

	var outputLines []string
	outputBytesCount := 0
	truncatedBy := "lines"
	lastLinePartial := false

	for i := len(lines) - 1; i >= 0 && len(outputLines) < maxLines; i-- {
		lineBytes := len(lines[i])
		if len(outputLines) > 0 {
			lineBytes++ // newline
		}
		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			if len(outputLines) == 0 {
				truncatedLine := truncateStringToBytesFromEnd(lines[i], maxBytes)
				outputLines = append([]string{truncatedLine}, outputLines...)
				outputBytesCount = len(truncatedLine)
				lastLinePartial = true
			}
			break
		}
		outputLines = append([]string{lines[i]}, outputLines...)
		outputBytesCount += lineBytes
	}

	if len(outputLines) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := strings.Join(outputLines, "\n")
	return TruncationResult{
		Content: outputContent, Truncated: true, TruncatedBy: truncatedBy,
		TotalLines: totalLines, TotalBytes: totalBytes,
		OutputLines: len(outputLines), OutputBytes: len(outputContent),
		LastLinePartial: lastLinePartial,
		MaxLines:        maxLines, MaxBytes: maxBytes,
	}
}

// truncateStringToBytesFromEnd keeps the final maxBytes, aligned to a UTF-8
// character boundary.
func truncateStringToBytesFromEnd(str string, maxBytes int) string {
	if len(str) <= maxBytes {
		return str
	}
	start := len(str) - maxBytes
	for start < len(str) && !utf8.RuneStart(str[start]) {
		start++
	}
	return str[start:]
}

// TruncateLineResult carries the clipped line text.
type TruncateLineResult struct {
	Text         string
	WasTruncated bool
}

// TruncateLine clips a single line to max runes with a [truncated] suffix.
func TruncateLine(line string, maxChars int) TruncateLineResult {
	runes := []rune(line)
	if len(runes) <= maxChars {
		return TruncateLineResult{Text: line}
	}
	return TruncateLineResult{
		Text:         string(runes[:maxChars]) + "... [truncated]",
		WasTruncated: true,
	}
}

// MiddleTruncationResult is the outcome of TruncateMiddle.
type MiddleTruncationResult struct {
	Content      string
	Truncated    bool
	RemovedChars int
	TotalBytes   int
	TotalLines   int
}

// TruncateMiddle keeps the start and end of content with the middle cut
// out, like Codex does for oversized tool output.
func TruncateMiddle(content string, maxBytes int) MiddleTruncationResult {
	buffer := []byte(content)
	totalLines := len(splitLinesForCounting(content))
	if len(buffer) <= maxBytes {
		return MiddleTruncationResult{Content: content, TotalBytes: len(buffer), TotalLines: totalLines}
	}
	// Continuation bytes (10xxxxxx) are not character starts.
	isBoundary := func(index int) bool {
		return index >= len(buffer) || (buffer[index]&0xc0) != 0x80
	}
	headEnd := maxBytes / 2
	for headEnd > 0 && !isBoundary(headEnd) {
		headEnd--
	}
	tailStart := len(buffer) - (maxBytes - maxBytes/2)
	for tailStart < len(buffer) && !isBoundary(tailStart) {
		tailStart++
	}
	head := string(buffer[:headEnd])
	tail := string(buffer[tailStart:])
	removedChars := len([]rune(string(buffer[headEnd:tailStart])))
	return MiddleTruncationResult{
		Content:      fmt.Sprintf("%s…%d chars truncated…%s", head, removedChars, tail),
		Truncated:    true,
		RemovedChars: removedChars,
		TotalBytes:   len(buffer),
		TotalLines:   totalLines,
	}
}
