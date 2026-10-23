package tui

// Port of tui-main-screen.ts: the TUI that renders into the terminal's main
// screen and scrollback with differential updates, Kitty image block
// accounting, and synchronized output.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxRenderWriteBytes = 1024 * 1024

// boundedTerminalWriter streams terminal output in 1 MiB chunks so a full
// render never forms one string large enough to stress allocators. The TS
// original counts UTF-16 chars and preserves surrogate pairs; Go strings are
// UTF-8, so chunk boundaries split on bytes with no surrogate concern.
type boundedTerminalWriter struct {
	buffer       strings.Builder
	writtenBytes int
	write        func(data string)
}

func newBoundedTerminalWriter(write func(string)) *boundedTerminalWriter {
	return &boundedTerminalWriter{write: write}
}

// Append adds terminal data, flushing full chunks as needed; callers Flush
// after the final append.
func (w *boundedTerminalWriter) Append(value string) {
	offset := 0
	for offset < len(value) {
		capacity := maxRenderWriteBytes - w.buffer.Len()
		if capacity == 0 {
			w.Flush()
			continue
		}

		end := len(value)
		if offset+capacity < end {
			end = offset + capacity
		}
		if end == offset {
			w.Flush()
			continue
		}

		w.buffer.WriteString(value[offset:end])
		offset = end
		if w.buffer.Len() == maxRenderWriteBytes {
			w.Flush()
		}
	}
}

// Flush writes the current chunk, if any.
func (w *boundedTerminalWriter) Flush() {
	if w.buffer.Len() == 0 {
		return
	}
	w.write(w.buffer.String())
	w.writtenBytes += w.buffer.Len()
	w.buffer.Reset()
}

// Length returns bytes written plus buffered bytes.
func (w *boundedTerminalWriter) Length() int {
	return w.writtenBytes + w.buffer.Len()
}

type kittyImageHeader struct {
	ids  []int
	rows int
}

func parseKittyImageHeader(line string) *kittyImageHeader {
	sequenceStart := strings.Index(line, kittyPrefix)
	if sequenceStart == -1 {
		return nil
	}
	paramsStart := sequenceStart + len(kittyPrefix)
	paramsEnd := strings.IndexByte(line[paramsStart:], ';')
	if paramsEnd == -1 {
		return nil
	}
	paramsEnd += paramsStart

	header := &kittyImageHeader{rows: 1}
	for _, param := range strings.Split(line[paramsStart:paramsEnd], ",") {
		key, value, found := strings.Cut(param, "=")
		if !found {
			continue
		}
		numberValue, err := strconv.Atoi(value)
		if err != nil || numberValue <= 0 || numberValue > 0xffffffff {
			continue
		}
		if key == "i" {
			header.ids = append(header.ids, numberValue)
		} else if key == "r" {
			header.rows = numberValue
		}
	}
	return header
}

func extractKittyImageIDs(line string) []int {
	if header := parseKittyImageHeader(line); header != nil {
		return header.ids
	}
	return nil
}

func extractKittyImageRows(line string) int {
	if header := parseKittyImageHeader(line); header != nil {
		return header.rows
	}
	return 1
}

func isTermuxSession() bool {
	return os.Getenv("TERMUX_VERSION") != ""
}

// TuiMainScreenRenderState is a captured render state snapshot.
type TuiMainScreenRenderState struct {
	PreviousLines       []string
	PreviousWidth       int
	PreviousHeight      int
	CursorRow           int
	HardwareCursorRow   int
	MaxLinesRendered    int
	PreviousViewportTop int
}

// TuiMainScreen renders into the terminal's main screen and scrollback.
type TuiMainScreen struct {
	TuiBase

	previousLines         []string
	previousKittyImageIDs map[int]bool
	previousWidth         int
	previousHeight        int
	cursorRow             int
	hardwareCursorRow     int
	maxLinesRendered      int
	previousViewportTop   int
}

// NewTuiMainScreen builds the main-screen TUI.
func NewTuiMainScreen(terminal Terminal, showHardwareCursor *bool, logDirectory string) *TuiMainScreen {
	screen := &TuiMainScreen{}
	screen.InitTuiBase(terminal, showHardwareCursor)
	screen.LogDirectory = logDirectory
	screen.ModeField = TuiModeRegular
	screen.previousKittyImageIDs = map[int]bool{}
	screen.ResetRenderState = screen.resetRenderState
	screen.BeforeTerminalStop = screen.beforeTerminalStop
	screen.DoRender = screen.doRender
	return screen
}

// CaptureRenderState snapshots the render state.
func (s *TuiMainScreen) CaptureRenderState() TuiMainScreenRenderState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return TuiMainScreenRenderState{
		PreviousLines:       append([]string(nil), s.previousLines...),
		PreviousWidth:       s.previousWidth,
		PreviousHeight:      s.previousHeight,
		CursorRow:           s.cursorRow,
		HardwareCursorRow:   s.hardwareCursorRow,
		MaxLinesRendered:    s.maxLinesRendered,
		PreviousViewportTop: s.previousViewportTop,
	}
}

// RestoreRenderState restores a snapshot; image lines restore as blank.
func (s *TuiMainScreen) RestoreRenderState(state TuiMainScreenRenderState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previousLines = nil
	for _, line := range state.PreviousLines {
		if IsImageLine(line) {
			s.previousLines = append(s.previousLines, "")
		} else {
			s.previousLines = append(s.previousLines, line)
		}
	}
	s.previousKittyImageIDs = map[int]bool{}
	s.previousWidth = state.PreviousWidth
	s.previousHeight = state.PreviousHeight
	s.cursorRow = state.CursorRow
	s.hardwareCursorRow = state.HardwareCursorRow
	s.maxLinesRendered = state.MaxLinesRendered
	s.previousViewportTop = state.PreviousViewportTop
}

func (s *TuiMainScreen) resetRenderState() {
	s.previousLines = nil
	s.previousWidth = -1
	s.previousHeight = -1
	s.cursorRow = 0
	s.hardwareCursorRow = 0
	s.maxLinesRendered = 0
	s.previousViewportTop = 0
}

func (s *TuiMainScreen) beforeTerminalStop(options *TuiStopOptions) {
	if options == nil || options.PreserveScreen || len(s.previousLines) == 0 {
		return
	}
	s.terminalRef.Write(" ")
	targetRow := len(s.previousLines)
	lineDiff := targetRow - s.hardwareCursorRow
	if lineDiff > 0 {
		s.terminalRef.Write("\x1b[" + strconv.Itoa(lineDiff) + "B")
	} else if lineDiff < 0 {
		s.terminalRef.Write("\x1b[" + strconv.Itoa(-lineDiff) + "A")
	}
	s.terminalRef.Write("\r\n")
}

func (s *TuiMainScreen) collectKittyImageIDs(lines []string) map[int]bool {
	ids := map[int]bool{}
	for _, line := range lines {
		for _, id := range extractKittyImageIDs(line) {
			ids[id] = true
		}
	}
	return ids
}

func (s *TuiMainScreen) deleteKittyImages(ids map[int]bool) string {
	var buffer strings.Builder
	for id := range ids {
		buffer.WriteString(DeleteKittyImage(id))
	}
	return buffer.String()
}

func (s *TuiMainScreen) getKittyImageReservedRows(lines []string, index int, maxIndex int) int {
	if maxIndex < 0 {
		maxIndex = len(lines) - 1
	}
	line := ""
	if index >= 0 && index < len(lines) {
		line = lines[index]
	}
	rows := extractKittyImageRows(line)
	if rows <= 1 {
		return 1
	}

	maxRows := rows
	if maxIndex-index+1 < maxRows {
		maxRows = maxIndex - index + 1
	}
	if len(lines)-index < maxRows {
		maxRows = len(lines) - index
	}
	reservedRows := 1
	for reservedRows < maxRows {
		following := ""
		if index+reservedRows >= 0 && index+reservedRows < len(lines) {
			following = lines[index+reservedRows]
		}
		if IsImageLine(following) || VisibleWidth(following) > 0 {
			break
		}
		reservedRows++
	}
	return reservedRows
}

func (s *TuiMainScreen) expandChangedRangeForKittyImages(firstChanged, lastChanged int, newLines []string) (int, int) {
	expandedFirstChanged := firstChanged
	expandedLastChanged := lastChanged
	expandForLines := func(lines []string) {
		for i := 0; i < len(lines); i++ {
			if len(extractKittyImageIDs(lines[i])) == 0 {
				continue
			}
			blockEnd := i + s.getKittyImageReservedRows(lines, i, len(lines)-1) - 1
			if i >= firstChanged || (i <= lastChanged && blockEnd >= firstChanged) {
				expandedFirstChanged = minInt(expandedFirstChanged, i)
				expandedLastChanged = maxInt(expandedLastChanged, blockEnd)
			}
		}
	}

	expandForLines(s.previousLines)
	expandForLines(newLines)
	return expandedFirstChanged, expandedLastChanged
}

func (s *TuiMainScreen) deleteChangedKittyImages(firstChanged, lastChanged int) string {
	if firstChanged < 0 || lastChanged < firstChanged {
		return ""
	}

	ids := map[int]bool{}
	maxLine := minInt(lastChanged, len(s.previousLines)-1)
	for i := firstChanged; i <= maxLine; i++ {
		line := ""
		if i >= 0 && i < len(s.previousLines) {
			line = s.previousLines[i]
		}
		for _, id := range extractKittyImageIDs(line) {
			ids[id] = true
		}
	}

	return s.deleteKittyImages(ids)
}

func (s *TuiMainScreen) doRender() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stoppedFlag {
		return
	}
	width := s.terminalRef.Columns()
	height := s.terminalRef.Rows()
	widthChanged := s.previousWidth != 0 && s.previousWidth != width
	heightChanged := s.previousHeight != 0 && s.previousHeight != height
	previousBufferLength := height
	if s.previousHeight > 0 {
		previousBufferLength = s.previousViewportTop + s.previousHeight
	}
	prevViewportTop := s.previousViewportTop
	if heightChanged {
		prevViewportTop = maxInt(0, previousBufferLength-height)
	}
	viewportTop := prevViewportTop
	hardwareCursorRow := s.hardwareCursorRow
	computeLineDiff := func(targetRow int) int {
		currentScreenRow := hardwareCursorRow - prevViewportTop
		targetScreenRow := targetRow - viewportTop
		return targetScreenRow - currentScreenRow
	}

	// Render all components to get new lines.
	newLines := s.resolveFakeCursors(s.Render(width))

	// Composite overlays before the differential compare.
	if s.hasOverlayEntries() {
		newLines = s.CompositeOverlays(newLines, width, height)
	}

	// Extract the cursor position before line resets (marker first).
	cursorRow, cursorCol, hasCursor := s.ExtractCursorPosition(newLines, height)
	cursorPos := cursorPosition{row: cursorRow, col: cursorCol, ok: hasCursor}

	newLines = s.ApplyLineResets(newLines)

	logRedraw := func(reason string) {
		if os.Getenv("PI_TUI_DEBUG_REDRAW") != "1" || s.LogDirectory == "" {
			return
		}
		logPath := filepath.Join(s.LogDirectory, "pi-tui-debug.log")
		msg := fmt.Sprintf("[%s] fullRender: %s (prev=%d, new=%d, height=%d)\n",
			time.Now().UTC().Format(time.RFC3339), reason, len(s.previousLines), len(newLines), height)
		os.MkdirAll(filepath.Dir(logPath), 0o755)
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			f.WriteString(msg)
			f.Close()
		}
	}

	// fullRender clears (optionally) and renders all new lines.
	fullRender := func(clear bool) {
		s.FullRedrawCount++
		output := newBoundedTerminalWriter(s.terminalRef.Write)
		output.Append("\x1b[?2026h") // Begin synchronized output
		if clear {
			output.Append(s.deleteKittyImages(s.previousKittyImageIDs))
			output.Append("\x1b[2J\x1b[H\x1b[3J") // Clear screen, home, clear scrollback
		}
		for i := 0; i < len(newLines); i++ {
			if i > 0 {
				output.Append("\r\n")
			}
			line := newLines[i]
			isImage := IsImageLine(line)
			imageReservedRows := 1
			if isImage {
				imageReservedRows = s.getKittyImageReservedRows(newLines, i, len(newLines)-1)
			}
			if imageReservedRows > 1 && imageReservedRows <= height {
				for row := 1; row < imageReservedRows; row++ {
					output.Append("\r\n")
				}
				output.Append("\x1b[" + strconv.Itoa(imageReservedRows-1) + "A")
				output.Append(line)
				output.Append("\x1b[" + strconv.Itoa(imageReservedRows-1) + "B")
				i += imageReservedRows - 1
				continue
			}
			output.Append(line)
		}
		output.Append("\x1b[?2026l") // End synchronized output
		output.Flush()
		s.cursorRow = maxInt(0, len(newLines)-1)
		s.hardwareCursorRow = s.cursorRow
		// Reset max lines when clearing, otherwise track growth.
		if clear {
			s.maxLinesRendered = len(newLines)
		} else {
			s.maxLinesRendered = maxInt(s.maxLinesRendered, len(newLines))
		}
		bufferLength := maxInt(height, len(newLines))
		s.previousViewportTop = maxInt(0, bufferLength-height)
		s.positionHardwareCursorLocked(cursorPos, len(newLines))
		s.previousLines = newLines
		s.previousKittyImageIDs = s.collectKittyImageIDs(newLines)
		s.previousWidth = width
		s.previousHeight = height
	}

	// First render: output everything without clearing (clean screen assumed).
	if len(s.previousLines) == 0 && !widthChanged && !heightChanged {
		logRedraw("first render")
		fullRender(false)
		return
	}

	// Width changes always need a full re-render because wrapping changes.
	if widthChanged {
		logRedraw(fmt.Sprintf("terminal width changed (%d -> %d)", s.previousWidth, width))
		fullRender(true)
		return
	}

	// Height changes normally need a full re-render, but Termux changes
	// height when the software keyboard shows/hides; a full redraw would
	// replay the entire history on every toggle there.
	if heightChanged && !isTermuxSession() {
		logRedraw(fmt.Sprintf("terminal height changed (%d -> %d)", s.previousHeight, height))
		fullRender(true)
		return
	}

	// Content shrunk below the working area and no overlays: re-render to
	// clear empty rows (overlays need the padding). setClearOnShrink toggles.
	if s.clearOnShrink && len(newLines) < s.maxLinesRendered && !s.hasOverlayEntries() {
		logRedraw(fmt.Sprintf("clearOnShrink (maxLinesRendered=%d)", s.maxLinesRendered))
		fullRender(true)
		return
	}

	// Find first and last changed lines.
	firstChanged := -1
	lastChanged := -1
	maxLines := maxInt(len(newLines), len(s.previousLines))
	for i := 0; i < maxLines; i++ {
		oldLine := ""
		if i < len(s.previousLines) {
			oldLine = s.previousLines[i]
		}
		newLine := ""
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}
	appendedLines := len(newLines) > len(s.previousLines)
	if appendedLines {
		if firstChanged == -1 {
			firstChanged = len(s.previousLines)
		}
		lastChanged = len(newLines) - 1
	}
	if firstChanged != -1 {
		firstChanged, lastChanged = s.expandChangedRangeForKittyImages(firstChanged, lastChanged, newLines)
	}
	appendStart := appendedLines && firstChanged == len(s.previousLines) && firstChanged > 0

	// No changes: still update the hardware cursor if it moved.
	if firstChanged == -1 {
		s.positionHardwareCursorLocked(cursorPos, len(newLines))
		s.previousViewportTop = prevViewportTop
		s.previousHeight = height
		return
	}

	// All changes are in deleted lines (nothing to render, just clear).
	if firstChanged >= len(newLines) {
		if len(s.previousLines) > len(newLines) {
			output := newBoundedTerminalWriter(s.terminalRef.Write)
			output.Append("\x1b[?2026h")
			output.Append(s.deleteChangedKittyImages(firstChanged, lastChanged))
			targetRow := maxInt(0, len(newLines)-1)
			if targetRow < prevViewportTop {
				logRedraw(fmt.Sprintf("deleted lines moved viewport up (%d < %d)", targetRow, prevViewportTop))
				fullRender(true)
				return
			}
			lineDiff := computeLineDiff(targetRow)
			if lineDiff > 0 {
				output.Append("\x1b[" + strconv.Itoa(lineDiff) + "B")
			} else if lineDiff < 0 {
				output.Append("\x1b[" + strconv.Itoa(-lineDiff) + "A")
			}
			output.Append("\r")
			// Clear extra lines without scrolling.
			extraLines := len(s.previousLines) - len(newLines)
			if extraLines > height {
				logRedraw(fmt.Sprintf("extraLines > height (%d > %d)", extraLines, height))
				fullRender(true)
				return
			}
			clearStartOffset := 0
			if len(newLines) != 0 {
				clearStartOffset = 1
			}
			if extraLines > 0 && clearStartOffset > 0 {
				output.Append("\x1b[" + strconv.Itoa(clearStartOffset) + "B")
			}
			for i := 0; i < extraLines; i++ {
				output.Append("\r\x1b[2K")
				if i < extraLines-1 {
					output.Append("\x1b[1B")
				}
			}
			moveBack := maxInt(0, extraLines-1+clearStartOffset)
			if moveBack > 0 {
				output.Append("\x1b[" + strconv.Itoa(moveBack) + "A")
			}
			output.Append("\x1b[?2026l")
			output.Flush()
			s.cursorRow = targetRow
			s.hardwareCursorRow = targetRow
		}
		s.positionHardwareCursorLocked(cursorPos, len(newLines))
		s.previousLines = newLines
		s.previousKittyImageIDs = s.collectKittyImageIDs(newLines)
		s.previousWidth = width
		s.previousHeight = height
		s.previousViewportTop = prevViewportTop
		return
	}

	// Differential rendering can only touch what was actually visible; a
	// first changed line above the previous viewport needs a full redraw.
	if firstChanged < prevViewportTop {
		logRedraw(fmt.Sprintf("firstChanged < viewportTop (%d < %d)", firstChanged, prevViewportTop))
		fullRender(true)
		return
	}

	// Render from the first changed line onward, wrapped in synchronized
	// output and bounded chunks.
	output := newBoundedTerminalWriter(s.terminalRef.Write)
	output.Append("\x1b[?2026h") // Begin synchronized output
	output.Append(s.deleteChangedKittyImages(firstChanged, lastChanged))
	prevViewportBottom := prevViewportTop + height - 1
	moveTargetRow := firstChanged
	if appendStart {
		moveTargetRow = firstChanged - 1
	}
	if moveTargetRow > prevViewportBottom {
		currentScreenRow := maxInt(0, minInt(height-1, hardwareCursorRow-prevViewportTop))
		moveToBottom := height - 1 - currentScreenRow
		if moveToBottom > 0 {
			output.Append("\x1b[" + strconv.Itoa(moveToBottom) + "B")
		}
		scroll := moveTargetRow - prevViewportBottom
		for i := 0; i < scroll; i++ {
			output.Append("\r\n")
		}
		prevViewportTop += scroll
		viewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	// Move cursor to the first changed line.
	lineDiff := computeLineDiff(moveTargetRow)
	if lineDiff > 0 {
		output.Append("\x1b[" + strconv.Itoa(lineDiff) + "B") // Move down
	} else if lineDiff < 0 {
		output.Append("\x1b[" + strconv.Itoa(-lineDiff) + "A") // Move up
	}

	if appendStart {
		output.Append("\r\n") // Move to column 0 on a new line
	} else {
		output.Append("\r")
	}

	// Only render changed lines (firstChanged..lastChanged) — reduces flicker
	// when a single line changes (spinner animation).
	renderEnd := minInt(lastChanged, len(newLines)-1)
	for i := firstChanged; i <= renderEnd; i++ {
		if i > firstChanged {
			output.Append("\r\n")
		}
		line := newLines[i]
		isImage := IsImageLine(line)
		imageReservedRows := 1
		if isImage {
			imageReservedRows = s.getKittyImageReservedRows(newLines, i, renderEnd)
		}
		if imageReservedRows > 1 {
			imageStartScreenRow := i - viewportTop
			if imageStartScreenRow < 0 || imageStartScreenRow+imageReservedRows > height {
				logRedraw(fmt.Sprintf("kitty image pre-clear would scroll (%d + %d > %d)",
					imageStartScreenRow, imageReservedRows, height))
				fullRender(true)
				return
			}

			output.Append("\x1b[2K")
			for row := 1; row < imageReservedRows; row++ {
				output.Append("\r\n\x1b[2K")
			}
			output.Append("\x1b[" + strconv.Itoa(imageReservedRows-1) + "A")
			output.Append(line)
			output.Append("\x1b[" + strconv.Itoa(imageReservedRows-1) + "B")
			i += imageReservedRows - 1
			continue
		}

		output.Append("\x1b[2K") // Clear current line
		if !isImage && VisibleWidth(line) > width {
			s.writeCrashLogAndPanic(width, i, line, newLines)
		}
		output.Append(line)
	}

	// Track where the cursor ended after rendering.
	finalCursorRow := renderEnd

	// More lines before: clear them and move the cursor back.
	if len(s.previousLines) > len(newLines) {
		if renderEnd < len(newLines)-1 {
			moveDown := len(newLines) - 1 - renderEnd
			output.Append("\x1b[" + strconv.Itoa(moveDown) + "B")
			finalCursorRow = len(newLines) - 1
		}
		extraLines := len(s.previousLines) - len(newLines)
		for i := len(newLines); i < len(s.previousLines); i++ {
			output.Append("\r\n\x1b[2K")
		}
		// Move cursor back to end of new content.
		output.Append("\x1b[" + strconv.Itoa(extraLines) + "A")
	}

	output.Append("\x1b[?2026l") // End synchronized output
	output.Flush()

	// cursorRow tracks end of content (viewport calculation);
	// hardwareCursorRow tracks the actual terminal cursor (movement).
	s.cursorRow = maxInt(0, len(newLines)-1)
	s.hardwareCursorRow = finalCursorRow
	s.maxLinesRendered = maxInt(s.maxLinesRendered, len(newLines))
	s.previousViewportTop = maxInt(prevViewportTop, finalCursorRow-height+1)

	// Position the hardware cursor for IME.
	s.positionHardwareCursorLocked(cursorPos, len(newLines))

	s.previousLines = newLines
	s.previousKittyImageIDs = s.collectKittyImageIDs(newLines)
	s.previousWidth = width
	s.previousHeight = height
}

type cursorPosition struct {
	row int
	col int
	ok  bool
}

func (s *TuiMainScreen) writeCrashLogAndPanic(width, index int, line string, newLines []string) {
	crashDir := s.LogDirectory
	if crashDir == "" {
		crashDir = os.TempDir()
	}
	crashLogPath := filepath.Join(crashDir, "pi-tui-crash.log")
	var crashData strings.Builder
	crashData.WriteString(fmt.Sprintf("Crash at %s\n", time.Now().UTC().Format(time.RFC3339)))
	crashData.WriteString(fmt.Sprintf("Terminal width: %d\n", width))
	crashData.WriteString(fmt.Sprintf("Line %d visible width: %d\n", index, VisibleWidth(line)))
	crashData.WriteString("\n=== All rendered lines ===\n")
	for idx, l := range newLines {
		crashData.WriteString(fmt.Sprintf("[%d] (w=%d) %s\n", idx, VisibleWidth(l), l))
	}
	os.MkdirAll(filepath.Dir(crashLogPath), 0o755)
	os.WriteFile(crashLogPath, []byte(crashData.String()), 0o644)

	// Clean up terminal state before panicking.
	s.mu.Unlock()
	s.Stop(nil)
	s.mu.Lock()

	errorMsg := fmt.Sprintf(`Rendered line %d exceeds terminal width (%d > %d).

This is likely caused by a custom TUI component not truncating its output.
Use visibleWidth() to measure and truncateToWidth() to truncate lines.

Debug log written to: %s`, index, VisibleWidth(line), width, crashLogPath)
	panic(errorMsg)
}

// positionHardwareCursorLocked positions the hardware cursor for the IME
// candidate window. Caller holds s.mu.
func (s *TuiMainScreen) positionHardwareCursorLocked(cursorPos cursorPosition, totalLines int) {
	if !cursorPos.ok || totalLines <= 0 {
		s.terminalRef.HideCursor()
		return
	}

	// Clamp cursor position to valid range.
	targetRow := maxInt(0, minInt(cursorPos.row, totalLines-1))
	targetCol := maxInt(0, cursorPos.col)

	// Move cursor from current position to target.
	rowDelta := targetRow - s.hardwareCursorRow
	var buffer strings.Builder
	if rowDelta > 0 {
		buffer.WriteString("\x1b[" + strconv.Itoa(rowDelta) + "B") // Move down
	} else if rowDelta < 0 {
		buffer.WriteString("\x1b[" + strconv.Itoa(-rowDelta) + "A") // Move up
	}
	// Move to absolute column (1-indexed).
	buffer.WriteString("\x1b[" + strconv.Itoa(targetCol+1) + "G")

	if buffer.Len() > 0 {
		s.terminalRef.Write(buffer.String())
	}

	s.hardwareCursorRow = targetRow
	if s.showHardwareCursor {
		s.terminalRef.ShowCursor()
	} else {
		s.terminalRef.HideCursor()
	}
}
