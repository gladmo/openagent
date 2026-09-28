package harness

// utils_truncate.go ports harness/utils/truncate.ts.

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Default truncation limits.
const (
	DefaultMaxLines   = 2000
	DefaultMaxBytes   = 50 * 1024
	GrepMaxLineLength = 500
)

// TruncationResult mirrors the TS interface.
type TruncationResult struct {
	Content               string
	Truncated             bool
	TruncatedBy           string // "lines" | "bytes" | ""
	TotalLines            int
	TotalBytes            int64
	OutputLines           int
	OutputBytes           int64
	LastLinePartial       bool
	FirstLineExceedsLimit bool
	MaxLines              int
	MaxBytes              int64
}

// TruncationOptions mirrors the TS interface.
type TruncationOptions struct {
	MaxLines *int
	MaxBytes *int64
}

// UTF8ByteLength mirrors utf8ByteLength.
func UTF8ByteLength(content string) int64 {
	return int64(len(content)) // Go strings hold UTF-8 bytes
}

func splitLinesForCounting(content string) []string {
	if len(content) == 0 {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// FormatSize formats bytes as a human-readable size.
func FormatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	} else if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

// TruncateHead truncates from the head (keep first N lines/bytes). Never
// returns partial lines; a first line over the byte limit yields empty
// content with FirstLineExceedsLimit.
func TruncateHead(content string, options *TruncationOptions) TruncationResult {
	maxLines := DefaultMaxLines
	maxBytes := int64(DefaultMaxBytes)
	if options != nil {
		if options.MaxLines != nil {
			maxLines = *options.MaxLines
		}
		if options.MaxBytes != nil {
			maxBytes = *options.MaxBytes
		}
	}

	totalBytes := UTF8ByteLength(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content: content, Truncated: false, TruncatedBy: "",
			TotalLines: totalLines, TotalBytes: totalBytes,
			OutputLines: totalLines, OutputBytes: totalBytes,
			MaxLines: maxLines, MaxBytes: maxBytes,
		}
	}

	if len(lines) > 0 && UTF8ByteLength(lines[0]) > maxBytes {
		return TruncationResult{
			Content: "", Truncated: true, TruncatedBy: "bytes",
			TotalLines: totalLines, TotalBytes: totalBytes,
			OutputLines: 0, OutputBytes: 0,
			FirstLineExceedsLimit: true,
			MaxLines:              maxLines, MaxBytes: maxBytes,
		}
	}

	outputLinesArr := []string{}
	var outputBytesCount int64
	truncatedBy := "lines"

	for i := 0; i < len(lines) && i < maxLines; i++ {
		line := lines[i]
		lineBytes := UTF8ByteLength(line)
		if i > 0 {
			lineBytes++ // newline
		}
		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		outputLinesArr = append(outputLinesArr, line)
		outputBytesCount += lineBytes
	}

	if len(outputLinesArr) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := strings.Join(outputLinesArr, "\n")
	return TruncationResult{
		Content: outputContent, Truncated: true, TruncatedBy: truncatedBy,
		TotalLines: totalLines, TotalBytes: totalBytes,
		OutputLines: len(outputLinesArr), OutputBytes: UTF8ByteLength(outputContent),
		MaxLines: maxLines, MaxBytes: maxBytes,
	}
}

// TruncateTail truncates from the tail (keep last N lines/bytes). May
// return a partial first line when the last line exceeds the byte limit.
func TruncateTail(content string, options *TruncationOptions) TruncationResult {
	maxLines := DefaultMaxLines
	maxBytes := int64(DefaultMaxBytes)
	if options != nil {
		if options.MaxLines != nil {
			maxLines = *options.MaxLines
		}
		if options.MaxBytes != nil {
			maxBytes = *options.MaxBytes
		}
	}

	totalBytes := UTF8ByteLength(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content: content, Truncated: false, TruncatedBy: "",
			TotalLines: totalLines, TotalBytes: totalBytes,
			OutputLines: totalLines, OutputBytes: totalBytes,
			MaxLines: maxLines, MaxBytes: maxBytes,
		}
	}

	outputLinesArr := []string{}
	var outputBytesCount int64
	truncatedBy := "lines"
	lastLinePartial := false

	for i := len(lines) - 1; i >= 0 && len(outputLinesArr) < maxLines; i-- {
		line := lines[i]
		lineBytes := UTF8ByteLength(line)
		if len(outputLinesArr) > 0 {
			lineBytes++
		}
		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			if len(outputLinesArr) == 0 {
				truncatedLine := truncateStringToBytesFromEnd(line, maxBytes)
				outputLinesArr = append([]string{truncatedLine}, outputLinesArr...)
				outputBytesCount = UTF8ByteLength(truncatedLine)
				lastLinePartial = true
			}
			break
		}
		outputLinesArr = append([]string{line}, outputLinesArr...)
		outputBytesCount += lineBytes
	}

	if len(outputLinesArr) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := strings.Join(outputLinesArr, "\n")
	return TruncationResult{
		Content: outputContent, Truncated: true, TruncatedBy: truncatedBy,
		TotalLines: totalLines, TotalBytes: totalBytes,
		OutputLines: len(outputLinesArr), OutputBytes: UTF8ByteLength(outputContent),
		LastLinePartial: lastLinePartial,
		MaxLines:        maxLines, MaxBytes: maxBytes,
	}
}

// truncateStringToBytesFromEnd keeps the end of the string within a byte
// budget, cutting at rune boundaries.
func truncateStringToBytesFromEnd(str string, maxBytes int64) string {
	if maxBytes <= 0 {
		return ""
	}
	var outputBytes int64
	start := len(str)
	for i := len(str); i > 0; {
		characterStart := i - 1
		characterBytes := int64(1)
		if str[characterStart] < 0x80 {
			characterBytes = 1
		} else {
			// Walk back to the rune start.
			for characterStart > 0 && str[characterStart]&0xC0 == 0x80 {
				characterStart--
			}
			characterBytes = int64(utf8.RuneLen(runeAt(str, characterStart)))
		}
		if outputBytes+characterBytes > maxBytes {
			break
		}
		outputBytes += characterBytes
		start = characterStart
		i = characterStart
	}
	return str[start:]
}

func runeAt(s string, i int) rune {
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r
}

// TruncateLineResult mirrors the TS return.
type TruncateLineResult struct {
	Text         string
	WasTruncated bool
}

// TruncateLine truncates a single line to max runes with a suffix.
func TruncateLine(line string, maxChars ...int) TruncateLineResult {
	limit := GrepMaxLineLength
	if len(maxChars) > 0 {
		limit = maxChars[0]
	}
	runes := []rune(line)
	if len(runes) <= limit {
		return TruncateLineResult{Text: line, WasTruncated: false}
	}
	return TruncateLineResult{
		Text:         string(runes[:limit]) + "... [truncated]",
		WasTruncated: true,
	}
}
