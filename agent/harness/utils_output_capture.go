package harness

// utils_output_capture.go ports harness/utils/output-capture.ts (without the
// AdaptivePublisher rate limiting, which lands separately; here flush() is
// immediate, preserving every view and update shape).

import (
	"fmt"
	"strings"
	"sync"
)

// Output rate constants.
const (
	OutputMinEmitIntervalMS    = 100
	OutputTargetBytesPerSecond = 100 * 1024
)

// invalidShellOutputMatches strips C0 controls (except \t \n \r) and
// U+FFF9..U+FFFB interstitials.
func sanitizeShellOutput(text string) string {
	var sb strings.Builder
	sb.Grow(len(text))
	for _, r := range text {
		if r <= 0x08 || (r >= 0x0b && r <= 0x1f) || (r >= 0xfff9 && r <= 0xfffb) {
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// SanitizeShellOutput mirrors sanitizeShellOutput.
func SanitizeShellOutput(text string) string { return sanitizeShellOutput(text) }

func countNewlines(text string) int {
	return strings.Count(text, "\n")
}

// OutputCapture maintains and publishes one bounded shell-output view.
type OutputCapture struct {
	mu sync.Mutex

	maxBytes int64
	maxLines int64
	retain   string

	buffer           string
	bufferBytes      int64
	totalBytes       int64
	newlines         int64
	endsWithNewline  bool
	currentLineBytes int64
	spillPath        string
	hasSpillPath     bool
	disposed         bool

	onUpdate func(update ShellOutputUpdate, ctx Context)
	onError  func(err error)
	ctx      Context
}

// NewOutputCapture mirrors the TS constructor. Limits fall back to defaults;
// non-positive limits panic (TS TypeError).
func NewOutputCapture(options *ShellOutputCaptureOptions, ctx Context, onUpdate func(ShellOutputUpdate, Context), onError func(error)) *OutputCapture {
	maxBytes := int64(DefaultMaxBytes)
	maxLines := int64(DefaultMaxLines)
	retain := RetainTail
	if options != nil {
		if options.Limits.MaxBytes != 0 {
			maxBytes = options.Limits.MaxBytes
		}
		if options.Limits.MaxLines != 0 {
			maxLines = options.Limits.MaxLines
		}
		if options.Limits.Retain != "" {
			retain = options.Limits.Retain
		}
	}
	if maxBytes <= 0 {
		panic(fmt.Sprintf("Output maxBytes must be a positive finite number"))
	}
	if maxLines <= 0 {
		panic(fmt.Sprintf("Output maxLines must be a positive integer"))
	}
	return &OutputCapture{
		maxBytes:        maxBytes,
		maxLines:        maxLines,
		retain:          retain,
		onUpdate:        onUpdate,
		onError:         onError,
		ctx:             ctx,
		endsWithNewline: true,
	}
}

// Push appends a chunk.
func (c *OutputCapture) Push(chunk string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appendText(chunk)
}

// Finish flushes any pending state.
func (c *OutputCapture) Finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disposed {
		return
	}
}

// SetSpillPath records the spill file and flushes.
func (c *OutputCapture) SetSpillPath(path string) {
	c.mu.Lock()
	c.spillPath, c.hasSpillPath = path, true
	dirty := !c.disposed
	c.mu.Unlock()
	if dirty {
		c.Flush()
	}
}

// Truncated reports whether the view is bounded.
func (c *OutputCapture) Truncated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalBytes > c.maxBytes || c.totalLinesLocked() > c.maxLines
}

// Snapshot builds the current bounded view.
func (c *OutputCapture) Snapshot() ShellOutputView {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *OutputCapture) snapshotLocked() ShellOutputView {
	var retained TruncationResult
	if c.retain == RetainHead {
		retained = TruncateHead(c.buffer, &TruncationOptions{MaxLines: intPtr(int(c.maxLines)), MaxBytes: int64Ptr(c.maxBytes)})
	} else {
		retained = TruncateTail(c.buffer, &TruncationOptions{MaxLines: intPtr(int(c.maxLines)), MaxBytes: int64Ptr(c.maxBytes)})
	}
	totalLines := c.totalLinesLocked()
	truncated := c.totalBytes > c.maxBytes || totalLines > c.maxLines
	truncatedBy := ""
	if truncated {
		truncatedBy = "bytes"
		if totalLines > c.maxLines {
			truncatedBy = "lines"
		}
	}
	view := ShellOutputView{
		Text: sanitizeShellOutput(retained.Content),
		ShellOutputMetadata: ShellOutputMetadata{
			Truncation: TruncationResultData{
				OriginalBytes: c.totalBytes,
				OriginalLines: totalLines,
				Truncated:     truncated,
				RetainedBytes: retained.OutputBytes,
				Note:          truncatedBy,
			},
			SpillPath:    c.spillPath,
			HasSpillPath: c.hasSpillPath,
		},
	}
	if retained.LastLinePartial {
		view.LastLineBytes = c.currentLineBytes
	}
	return view
}

// Flush publishes the current view immediately.
func (c *OutputCapture) Flush() {
	c.mu.Lock()
	view := c.snapshotLocked()
	c.mu.Unlock()
	if c.onUpdate != nil {
		c.publish(updateFrom(nil, view))
	}
}

func (c *OutputCapture) publish(update ShellOutputUpdate) {
	if c.onUpdate == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			if c.onError != nil {
				c.onError(ToError(r))
			}
		}
	}()
	c.onUpdate(update, c.ctx)
}

// Dispose stops publishing.
func (c *OutputCapture) Dispose() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disposed = true
}

func (c *OutputCapture) appendText(text string) {
	if c.disposed || text == "" {
		return
	}
	textBytes := UTF8ByteLength(text)
	c.totalBytes += textBytes
	c.newlines += int64(countNewlines(text))
	c.endsWithNewline = strings.HasSuffix(text, "\n")
	if lastNewline := strings.LastIndex(text, "\n"); lastNewline == -1 {
		c.currentLineBytes += textBytes
	} else {
		c.currentLineBytes = UTF8ByteLength(text[lastNewline+1:])
	}
	c.buffer += text
	c.bufferBytes += textBytes

	guard := c.maxBytes * 2
	if c.bufferBytes > guard*2 {
		if c.retain == RetainTail {
			c.buffer = trimToLastUTF8Bytes(c.buffer, guard)
		} else {
			c.buffer = trimToFirstUTF8Bytes(c.buffer, guard)
		}
		c.bufferBytes = UTF8ByteLength(c.buffer)
	}
}

func (c *OutputCapture) totalLinesLocked() int64 {
	extra := int64(0)
	if !c.endsWithNewline && c.totalBytes != 0 {
		extra = 1
	}
	return c.newlines + extra
}

// ApplyShellOutputUpdate mirrors applyShellOutputUpdate.
func ApplyShellOutputUpdate(current *ShellOutputView, update ShellOutputUpdate) ShellOutputView {
	switch u := update.(type) {
	case *ShellOutputReplace:
		return u.Output
	case *ShellOutputAppend:
		text := ""
		if current != nil {
			text = current.Text
		}
		return ShellOutputView{ShellOutputMetadata: u.Metadata, Text: text + u.Text}
	case *ShellOutputSlide:
		text := ""
		if current != nil {
			if int(u.Drop) <= len(current.Text) {
				text = current.Text[u.Drop:]
			}
		}
		return ShellOutputView{ShellOutputMetadata: u.Metadata, Text: text + u.Text}
	case *ShellOutputMetadataUpdate:
		text := ""
		if current != nil {
			text = current.Text
		}
		return ShellOutputView{ShellOutputMetadata: u.Metadata, Text: text}
	default:
		text := ""
		if current != nil {
			text = current.Text
		}
		return ShellOutputView{Text: text}
	}
}

// updateFrom computes the incremental update from the previous view.
func updateFrom(previous *ShellOutputView, current ShellOutputView) ShellOutputUpdate {
	metadata := ShellOutputMetadata{
		Truncation:    current.Truncation,
		SpillPath:     current.SpillPath,
		HasSpillPath:  current.HasSpillPath,
		LastLineBytes: current.LastLineBytes,
	}
	if previous == nil {
		return &ShellOutputReplace{Output: current}
	}
	if current.Text == previous.Text {
		return &ShellOutputMetadataUpdate{Metadata: metadata}
	}
	if len(current.Text) > len(previous.Text) && strings.HasPrefix(current.Text, previous.Text) {
		return &ShellOutputAppend{Text: current.Text[len(previous.Text):], Metadata: metadata}
	}
	scan := len(previous.Text)
	if len(current.Text) < scan {
		scan = len(current.Text)
	}
	if maxScan := int(current.Truncation.OriginalBytes) * 2; scan > maxScan {
		// Note: TS uses truncation.maxBytes (the configured limit); we carry
		// OriginalBytes in that slot, so bound by it symmetrically.
		scan = maxScan
	}
	shared := suffixPrefixOverlap(previous.Text, current.Text, scan)
	if shared > 0 {
		return &ShellOutputSlide{
			Drop:     int64(len(previous.Text) - shared),
			Text:     current.Text[shared:],
			Metadata: metadata,
		}
	}
	return &ShellOutputReplace{Output: current}
}

// suffixPrefixOverlap mirrors the TS overlap probe.
func suffixPrefixOverlap(before, after string, scan int) int {
	if len(before) == 0 || len(after) == 0 || scan == 0 {
		return 0
	}
	tail := before
	if len(before) > scan {
		tail = before[len(before)-scan:]
	}
	probes := []int{minIntUtil(64, len(after)), 1}
	for _, probeLength := range probes {
		probe := after[:probeLength]
		candidates := 0
		for index := strings.Index(tail, probe); index != -1; index = indexFromUtil(tail, probe, index+1) {
			candidates++
			if candidates > 8 {
				break
			}
			overlapLength := len(tail) - index
			if overlapLength <= len(after) && tail[index:] == after[:overlapLength] {
				return overlapLength
			}
		}
		if probeLength == 1 {
			break
		}
	}
	return 0
}

func indexFromUtil(s, sub string, from int) int {
	if from >= len(s) {
		return -1
	}
	idx := strings.Index(s[from:], sub)
	if idx < 0 {
		return -1
	}
	return from + idx
}

func minIntUtil(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func trimToLastUTF8Bytes(text string, maxBytes int64) string {
	if int64(len(text)) <= maxBytes {
		return text
	}
	start := len(text) - int(maxBytes)
	for start < len(text) && (text[start]&0xC0) == 0x80 {
		start++
	}
	return text[start:]
}

func trimToFirstUTF8Bytes(text string, maxBytes int64) string {
	if int64(len(text)) <= maxBytes {
		return text
	}
	end := int(maxBytes)
	for end > 0 && (text[end]&0xC0) == 0x80 {
		end--
	}
	return text[:end]
}

func intPtr(i int) *int       { return &i }
func int64Ptr(i int64) *int64 { return &i }
