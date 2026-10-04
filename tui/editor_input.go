package tui

// Editor continuation: input dispatch, cursor movement, visual line
// navigation, kill ring, and mouse handling.

import (
	"strings"
	"unicode"
)

// HandleInput processes one input chunk.
func (e *Editor) HandleInput(data string) {
	kb := GetKeybindings()

	// Character jump mode.
	if e.jumpMode != "" {
		if kb.Matches(data, "tui.editor.jumpForward") || kb.Matches(data, "tui.editor.jumpBackward") {
			e.jumpMode = ""
			return
		}
		printable, ok := DecodePrintableKey(data)
		if !ok && len(data) > 0 && data[0] >= 32 {
			printable, ok = data, true
		}
		if ok {
			direction := e.jumpMode
			e.jumpMode = ""
			e.jumpToChar(printable, direction)
			return
		}
		e.jumpMode = ""
	}

	// Bracketed paste.
	if strings.Contains(data, "\x1b[200~") {
		e.isInPaste = true
		e.pasteBuffer = ""
		data = strings.Replace(data, "\x1b[200~", "", 1)
	}
	if e.isInPaste {
		e.pasteBuffer += data
		if endIndex := strings.Index(e.pasteBuffer, "\x1b[201~"); endIndex != -1 {
			pasteContent := e.pasteBuffer[:endIndex]
			if len(pasteContent) > 0 {
				e.handlePaste(pasteContent)
			}
			e.isInPaste = false
			remaining := e.pasteBuffer[endIndex+6:]
			e.pasteBuffer = ""
			if len(remaining) > 0 {
				e.HandleInput(remaining)
			}
			return
		}
		return
	}

	// Ctrl+C: parent handles.
	if kb.Matches(data, "tui.input.copy") {
		return
	}

	// Undo.
	if kb.Matches(data, "tui.editor.undo") {
		e.undo()
		return
	}

	// Autocomplete mode.
	if e.autocompleteState != "" && e.autocompleteList != nil {
		if kb.Matches(data, "tui.select.cancel") {
			e.cancelAutocomplete()
			return
		}
		if kb.Matches(data, "tui.select.up") || kb.Matches(data, "tui.select.down") {
			e.autocompleteList.HandleInput(data)
			return
		}
		if kb.Matches(data, "tui.input.tab") {
			if selected, ok := e.autocompleteList.GetSelectedItem(); ok && e.autocompleteProvider != nil {
				e.pushUndoSnapshot()
				e.lastAction = ""
				result := e.autocompleteProvider.ApplyCompletion(
					e.state.lines, e.state.cursorLine, e.state.cursorCol,
					AutocompleteItem{Value: selected.Value, Label: selected.Label, Description: selected.Description},
					e.autocompletePrefix,
				)
				e.state.lines = result.Lines
				e.state.cursorLine = result.CursorLine
				e.setCursorCol(result.CursorCol)
				e.cancelAutocomplete()
				if e.OnChange != nil {
					e.OnChange(e.GetText())
				}
			}
			return
		}
		if kb.Matches(data, "tui.select.confirm") {
			if selected, ok := e.autocompleteList.GetSelectedItem(); ok && e.autocompleteProvider != nil {
				e.pushUndoSnapshot()
				e.lastAction = ""
				result := e.autocompleteProvider.ApplyCompletion(
					e.state.lines, e.state.cursorLine, e.state.cursorCol,
					AutocompleteItem{Value: selected.Value, Label: selected.Label, Description: selected.Description},
					e.autocompletePrefix,
				)
				e.state.lines = result.Lines
				e.state.cursorLine = result.CursorLine
				e.setCursorCol(result.CursorCol)

				if strings.HasPrefix(e.autocompletePrefix, "/") {
					e.cancelAutocomplete()
					// Fall through to submit.
				} else {
					e.cancelAutocomplete()
					if e.OnChange != nil {
						e.OnChange(e.GetText())
					}
					return
				}
			}
		}
	}

	// Tab triggers completion.
	if kb.Matches(data, "tui.input.tab") && e.autocompleteState == "" {
		e.handleTabCompletion()
		return
	}

	// Deletion.
	if kb.Matches(data, "tui.editor.deleteToLineEnd") {
		e.deleteToEndOfLine()
		return
	}
	if kb.Matches(data, "tui.editor.deleteToLineStart") {
		e.deleteToStartOfLine()
		return
	}
	if kb.Matches(data, "tui.editor.deleteWordBackward") {
		e.deleteWordBackwards()
		return
	}
	if kb.Matches(data, "tui.editor.deleteWordForward") {
		e.deleteWordForward()
		return
	}
	if kb.Matches(data, "tui.editor.deleteCharBackward") || MatchesKey(data, "shift+backspace") {
		e.handleBackspace()
		return
	}
	if kb.Matches(data, "tui.editor.deleteCharForward") || MatchesKey(data, "shift+delete") {
		e.handleForwardDelete()
		return
	}

	// Kill ring.
	if kb.Matches(data, "tui.editor.yank") {
		e.yank()
		return
	}
	if kb.Matches(data, "tui.editor.yankPop") {
		e.yankPop()
		return
	}

	// History.
	if kb.Matches(data, "tui.editor.historyPrevious") {
		e.cancelAutocomplete()
		e.navigateHistory(-1)
		return
	}
	if kb.Matches(data, "tui.editor.historyNext") {
		e.cancelAutocomplete()
		e.navigateHistory(1)
		return
	}

	// Cursor movement.
	if kb.Matches(data, "tui.editor.cursorLineStart") {
		e.moveToLineStart()
		return
	}
	if kb.Matches(data, "tui.editor.cursorLineEnd") {
		e.moveToLineEnd()
		return
	}
	if kb.Matches(data, "tui.editor.cursorWordLeft") {
		e.moveWordBackwards()
		return
	}
	if kb.Matches(data, "tui.editor.cursorWordRight") {
		e.moveWordForwards()
		return
	}

	// New line.
	isNewLine := kb.Matches(data, "tui.input.newLine") ||
		(len(data) > 1 && data[0] == 10) ||
		data == "\x1b\r" ||
		data == "\x1b[13;2~" ||
		(len(data) > 1 && strings.Contains(data, "\x1b") && strings.Contains(data, "\r")) ||
		data == "\n"
	if isNewLine {
		if e.shouldSubmitOnBackslashEnter(data) {
			e.handleBackspace()
			e.submitValue()
			return
		}
		e.addNewLine()
		return
	}

	// Submit.
	if kb.Matches(data, "tui.input.submit") {
		if e.DisableSubmit {
			return
		}
		currentLine := e.state.lines[e.state.cursorLine]
		if e.state.cursorCol > 0 && currentLine[e.state.cursorCol-1] == '\\' {
			e.handleBackspace()
			e.addNewLine()
			return
		}
		e.submitValue()
		return
	}

	// Arrows (history-aware).
	if kb.Matches(data, "tui.editor.cursorUp") {
		if e.isOnFirstVisualLine() && (e.isEditorEmpty() || e.historyIndex > -1 || e.state.cursorCol == 0) {
			e.navigateHistory(-1)
		} else if e.isOnFirstVisualLine() {
			e.moveToLineStart()
		} else {
			e.moveCursor(-1, 0)
		}
		return
	}
	if kb.Matches(data, "tui.editor.cursorDown") {
		if e.historyIndex > -1 && e.isOnLastVisualLine() {
			e.navigateHistory(1)
		} else if e.isOnLastVisualLine() {
			e.moveToLineEnd()
		} else {
			e.moveCursor(1, 0)
		}
		return
	}
	if kb.Matches(data, "tui.editor.cursorRight") {
		e.moveCursor(0, 1)
		return
	}
	if kb.Matches(data, "tui.editor.cursorLeft") {
		e.moveCursor(0, -1)
		return
	}

	// Page scroll.
	if kb.Matches(data, "tui.editor.pageUp") {
		e.pageScroll(-1)
		return
	}
	if kb.Matches(data, "tui.editor.pageDown") {
		e.pageScroll(1)
		return
	}

	// Jump mode triggers.
	if kb.Matches(data, "tui.editor.jumpForward") {
		e.jumpMode = "forward"
		return
	}
	if kb.Matches(data, "tui.editor.jumpBackward") {
		e.jumpMode = "backward"
		return
	}

	// Shift+Space.
	if MatchesKey(data, "shift+space") {
		e.insertCharacter(" ", false)
		return
	}

	if printable, ok := DecodePrintableKey(data); ok {
		e.insertCharacter(printable, false)
		return
	}

	if len(data) > 0 && data[0] >= 32 {
		e.insertCharacter(data, false)
	}
}

// HandleMouse positions the cursor on clicks and routes autocomplete rows.
func (e *Editor) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	autocompleteStartRow := e.renderedVisibleLineCount + 2
	if e.autocompleteState != "" && e.autocompleteList != nil &&
		event.Y >= autocompleteStartRow && event.Y < autocompleteStartRow+e.renderedAutocompleteHeight {
		maxPadding := maxInt(0, (event.Width-1)/2)
		paddingX := minInt(e.paddingX, maxPadding)
		contentWidth := maxInt(1, event.Width-paddingX*2)
		local := event
		local.X = event.X - paddingX
		local.Y = event.Y - autocompleteStartRow
		local.Width = contentWidth
		local.Height = e.renderedAutocompleteHeight
		if result := e.autocompleteList.HandleMouse(local); result != nil {
			result.Focus = true
			return DispatchMouseEvent(e, event)
		}
		return nil
	}

	// Press/drag/release stay unhandled so screen-level selection runs; only
	// synthesized clicks position the cursor.
	if event.Type != MouseClick || event.Button != MouseLeft {
		return nil
	}
	if event.Y <= 0 || event.Y > e.renderedVisibleLineCount {
		return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}, Handled: true}
	}

	visualLines := e.buildVisualLineMap(e.lastWidth)
	visualLineIndex := e.scrollOffset + event.Y - 1
	if visualLineIndex < 0 || visualLineIndex >= len(visualLines) {
		return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}, Handled: true}
	}
	visualLine := visualLines[visualLineIndex]
	logicalLine := e.state.lines[visualLine.logicalLine]
	chunkEnd := visualLine.startCol + visualLine.length
	chunk := logicalLine[visualLine.startCol:chunkEnd]
	maxPadding := maxInt(0, (event.Width-1)/2)
	paddingX := minInt(e.paddingX, maxPadding)
	targetColumn := maxInt(0, event.X-paddingX)
	visibleColumn := 0
	targetIndex := len(chunk)
	lastGraphemeIndex := 0
	for _, seg := range e.segment(chunk, false) {
		nextColumn := visibleColumn + VisibleWidth(seg.segment)
		lastGraphemeIndex = seg.index
		if targetColumn < nextColumn {
			targetIndex = seg.index
			break
		}
		visibleColumn = nextColumn
	}
	isLastSegment := visualLineIndex == len(visualLines)-1 ||
		visualLines[visualLineIndex+1].logicalLine != visualLine.logicalLine
	if !isLastSegment && targetIndex == len(chunk) && len(chunk) > 0 {
		targetIndex = lastGraphemeIndex
	}

	e.state.cursorLine = visualLine.logicalLine
	e.setCursorCol(visualLine.startCol + targetIndex)
	e.lastAction = ""
	e.exitHistoryBrowsing()
	if e.autocompleteState != "" {
		e.updateAutocomplete()
	}
	return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}, Handled: true}
}

type editorVisualLine struct {
	logicalLine int
	startCol    int
	length      int
}

func (e *Editor) buildVisualLineMap(width int) []editorVisualLine {
	var visualLines []editorVisualLine
	for i := 0; i < len(e.state.lines); i++ {
		line := e.state.lines[i]
		lineVisWidth := VisibleWidth(line)
		if len(line) == 0 {
			visualLines = append(visualLines, editorVisualLine{logicalLine: i, startCol: 0, length: 0})
		} else if lineVisWidth <= width {
			visualLines = append(visualLines, editorVisualLine{logicalLine: i, startCol: 0, length: len(line)})
		} else {
			chunks := WordWrapLine(line, width, e.segment(line, false))
			for _, chunk := range chunks {
				visualLines = append(visualLines, editorVisualLine{
					logicalLine: i,
					startCol:    chunk.StartIndex,
					length:      chunk.EndIndex - chunk.StartIndex,
				})
			}
		}
	}
	return visualLines
}

func (e *Editor) findVisualLineAt(visualLines []editorVisualLine, line, col int) int {
	for i := 0; i < len(visualLines); i++ {
		vl := visualLines[i]
		if vl.logicalLine != line {
			continue
		}
		offset := col - vl.startCol
		isLastSegmentOfLine := i == len(visualLines)-1 || visualLines[i+1].logicalLine != vl.logicalLine
		if offset >= 0 && (offset < vl.length || (isLastSegmentOfLine && offset == vl.length)) {
			return i
		}
	}
	return len(visualLines) - 1
}

func (e *Editor) findCurrentVisualLine(visualLines []editorVisualLine) int {
	return e.findVisualLineAt(visualLines, e.state.cursorLine, e.state.cursorCol)
}

// moveToVisualLine applies sticky-column vertical movement.
func (e *Editor) moveToVisualLine(visualLines []editorVisualLine, currentVisualLine, targetVisualLine int) {
	if currentVisualLine < 0 || currentVisualLine >= len(visualLines) || targetVisualLine < 0 || targetVisualLine >= len(visualLines) {
		return
	}
	currentVL := visualLines[currentVisualLine]
	targetVL := visualLines[targetVisualLine]

	var currentVisualCol int
	if e.snappedFromCursorCol != nil {
		vlIndex := e.findVisualLineAt(visualLines, currentVL.logicalLine, *e.snappedFromCursorCol)
		currentVisualCol = *e.snappedFromCursorCol - visualLines[vlIndex].startCol
	} else {
		currentVisualCol = e.state.cursorCol - currentVL.startCol
	}

	isLastSourceSegment := currentVisualLine == len(visualLines)-1 ||
		visualLines[currentVisualLine+1].logicalLine != currentVL.logicalLine
	sourceMaxVisualCol := currentVL.length
	if !isLastSourceSegment {
		sourceMaxVisualCol = maxInt(0, currentVL.length-1)
	}

	isLastTargetSegment := targetVisualLine == len(visualLines)-1 ||
		visualLines[targetVisualLine+1].logicalLine != targetVL.logicalLine
	targetMaxVisualCol := targetVL.length
	if !isLastTargetSegment {
		targetMaxVisualCol = maxInt(0, targetVL.length-1)
	}

	moveToVisualCol := e.computeVerticalMoveColumn(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol)

	e.state.cursorLine = targetVL.logicalLine
	targetCol := targetVL.startCol + moveToVisualCol
	logicalLine := e.state.lines[targetVL.logicalLine]
	if targetCol > len(logicalLine) {
		targetCol = len(logicalLine)
	}
	e.state.cursorCol = targetCol

	// Snap to atomic segment boundaries (paste markers).
	for _, seg := range e.segment(logicalLine, false) {
		if seg.index > e.state.cursorCol {
			break
		}
		if len(seg.segment) <= 1 {
			continue
		}
		if e.state.cursorCol < seg.index+len(seg.segment) {
			isContinuation := seg.index < targetVL.startCol
			isMovingDown := targetVisualLine > currentVisualLine

			if isContinuation && isMovingDown {
				segEnd := seg.index + len(seg.segment)
				next := targetVisualLine + 1
				for next < len(visualLines) &&
					visualLines[next].logicalLine == targetVL.logicalLine &&
					visualLines[next].startCol < segEnd {
					next++
				}
				if next < len(visualLines) {
					e.moveToVisualLine(visualLines, currentVisualLine, next)
					return
				}
			}

			preSnap := e.state.cursorCol
			e.snappedFromCursorCol = &preSnap
			e.state.cursorCol = seg.index
			return
		}
	}

	e.snappedFromCursorCol = nil
}

// computeVerticalMoveColumn implements the sticky column decision table.
func (e *Editor) computeVerticalMoveColumn(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol int) int {
	hasPreferred := e.preferredVisualCol != nil
	cursorInMiddle := currentVisualCol < sourceMaxVisualCol
	targetTooShort := targetMaxVisualCol < currentVisualCol

	if !hasPreferred || cursorInMiddle {
		if targetTooShort {
			preferred := currentVisualCol
			e.preferredVisualCol = &preferred
			return targetMaxVisualCol
		}
		e.preferredVisualCol = nil
		return currentVisualCol
	}

	targetCantFitPreferred := targetMaxVisualCol < *e.preferredVisualCol
	if targetTooShort || targetCantFitPreferred {
		return targetMaxVisualCol
	}

	result := *e.preferredVisualCol
	e.preferredVisualCol = nil
	return result
}

func (e *Editor) moveCursor(deltaLine, deltaCol int) {
	e.lastAction = ""
	visualLines := e.buildVisualLineMap(e.lastWidth)
	currentVisualLine := e.findCurrentVisualLine(visualLines)

	if deltaLine != 0 {
		targetVisualLine := currentVisualLine + deltaLine
		if targetVisualLine >= 0 && targetVisualLine < len(visualLines) {
			e.moveToVisualLine(visualLines, currentVisualLine, targetVisualLine)
		}
	}

	if deltaCol != 0 {
		currentLine := e.state.lines[e.state.cursorLine]

		if deltaCol > 0 {
			if e.state.cursorCol < len(currentLine) {
				afterCursor := currentLine[e.state.cursorCol:]
				segments := e.segment(afterCursor, false)
				length := 1
				if len(segments) > 0 {
					length = len(segments[0].segment)
				}
				e.setCursorCol(e.state.cursorCol + length)
			} else if e.state.cursorLine < len(e.state.lines)-1 {
				e.state.cursorLine++
				e.setCursorCol(0)
			} else {
				if currentVisualLine >= 0 && currentVisualLine < len(visualLines) {
					preferred := e.state.cursorCol - visualLines[currentVisualLine].startCol
					e.preferredVisualCol = &preferred
				}
			}
		} else {
			if e.state.cursorCol > 0 {
				beforeCursor := currentLine[:e.state.cursorCol]
				segments := e.segment(beforeCursor, false)
				length := 1
				if len(segments) > 0 {
					length = len(segments[len(segments)-1].segment)
				}
				e.setCursorCol(e.state.cursorCol - length)
			} else if e.state.cursorLine > 0 {
				e.state.cursorLine--
				e.setCursorCol(len(e.state.lines[e.state.cursorLine]))
			}
		}
	}

	if e.autocompleteState != "" {
		e.updateAutocomplete()
	}
}

func (e *Editor) pageScroll(direction int) {
	e.lastAction = ""
	terminalRows := e.tui.Terminal().Rows()
	pageSize := maxInt(5, terminalRows*3/10)

	visualLines := e.buildVisualLineMap(e.lastWidth)
	currentVisualLine := e.findCurrentVisualLine(visualLines)
	targetVisualLine := maxInt(0, minInt(len(visualLines)-1, currentVisualLine+direction*pageSize))

	e.moveToVisualLine(visualLines, currentVisualLine, targetVisualLine)
}

func (e *Editor) moveToLineStart() {
	e.lastAction = ""
	e.setCursorCol(0)
}

func (e *Editor) moveToLineEnd() {
	e.lastAction = ""
	e.setCursorCol(len(e.state.lines[e.state.cursorLine]))
}

func (e *Editor) jumpToChar(char, direction string) {
	e.lastAction = ""
	isForward := direction == "forward"
	lines := e.state.lines

	step := 1
	end := len(lines)
	if !isForward {
		step = -1
		end = -1
	}

	for lineIdx := e.state.cursorLine; lineIdx != end; lineIdx += step {
		line := lines[lineIdx]
		isCurrentLine := lineIdx == e.state.cursorLine

		searchFrom := -1
		if isCurrentLine {
			if isForward {
				searchFrom = e.state.cursorCol + 1
				// Cursor at line end: clamp so the slice stays valid (JS
				// slice() tolerates this; Go panics).
				if searchFrom > len(line) {
					searchFrom = len(line)
				}
			} else {
				searchFrom = e.state.cursorCol - 1
			}
		}

		idx := -1
		if isForward {
			if searchFrom >= 0 {
				idx = strings.Index(line[searchFrom:], char)
				if idx >= 0 {
					idx += searchFrom
				}
			} else {
				idx = strings.Index(line, char)
			}
		} else {
			if searchFrom >= 0 {
				idx = strings.LastIndex(line[:searchFrom+1], char)
			} else {
				idx = strings.LastIndex(line, char)
			}
		}

		if idx != -1 {
			e.state.cursorLine = lineIdx
			e.setCursorCol(idx)
			return
		}
	}
}

func (e *Editor) moveWordBackwards() {
	e.lastAction = ""
	currentLine := e.state.lines[e.state.cursorLine]

	if e.state.cursorCol == 0 {
		if e.state.cursorLine > 0 {
			e.state.cursorLine--
			e.setCursorCol(len(e.state.lines[e.state.cursorLine]))
		}
		return
	}

	newCol := FindWordBackward(currentLine, e.state.cursorCol, &WordNavigationOptions{
		Segment:         func(text string) []WordSegment { return e.segmentWordsForNav(text) },
		IsAtomicSegment: isPasteMarker,
	})
	e.setCursorCol(newCol)
}

func (e *Editor) moveWordForwards() {
	e.lastAction = ""
	currentLine := e.state.lines[e.state.cursorLine]

	if e.state.cursorCol >= len(currentLine) {
		if e.state.cursorLine < len(e.state.lines)-1 {
			e.state.cursorLine++
			e.setCursorCol(0)
		}
		return
	}

	newCol := FindWordForward(currentLine, e.state.cursorCol, &WordNavigationOptions{
		Segment:         func(text string) []WordSegment { return e.segmentWordsForNav(text) },
		IsAtomicSegment: isPasteMarker,
	})
	e.setCursorCol(newCol)
}

// segmentWordsForNav adapts editor word segmentation for word navigation.
func (e *Editor) segmentWordsForNav(text string) []WordSegment {
	segments := e.segment(text, true)
	out := make([]WordSegment, 0, len(segments))
	for _, seg := range segments {
		wordLike := false
		for _, r := range seg.segment {
			if unicode.IsLetter(r) {
				wordLike = true
				break
			}
			_ = r
		}
		if !wordLike {
			// Numbers count as word-like for navigation.
			for _, r := range seg.segment {
				if r >= '0' && r <= '9' {
					wordLike = true
					break
				}
			}
		}
		out = append(out, WordSegment{Segment: seg.segment, IsWordLike: wordLike})
	}
	return out
}

func (e *Editor) deleteToStartOfLine() {
	e.exitHistoryBrowsing()

	currentLine := e.state.lines[e.state.cursorLine]

	if e.state.cursorCol > 0 {
		e.pushUndoSnapshot()

		deletedText := currentLine[:e.state.cursorCol]
		e.killRing.Push(deletedText, KillRingPushOptions{Prepend: true, Accumulate: e.lastAction == "kill"})
		e.lastAction = "kill"

		e.state.lines[e.state.cursorLine] = currentLine[e.state.cursorCol:]
		e.setCursorCol(0)
	} else if e.state.cursorLine > 0 {
		e.pushUndoSnapshot()

		e.killRing.Push("\n", KillRingPushOptions{Prepend: true, Accumulate: e.lastAction == "kill"})
		e.lastAction = "kill"

		previousLine := e.state.lines[e.state.cursorLine-1]
		e.state.lines[e.state.cursorLine-1] = previousLine + currentLine
		e.state.lines = append(e.state.lines[:e.state.cursorLine], e.state.lines[e.state.cursorLine+1:]...)
		e.state.cursorLine--
		e.setCursorCol(len(previousLine))
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) deleteToEndOfLine() {
	e.exitHistoryBrowsing()

	currentLine := e.state.lines[e.state.cursorLine]

	if e.state.cursorCol < len(currentLine) {
		e.pushUndoSnapshot()

		deletedText := currentLine[e.state.cursorCol:]
		e.killRing.Push(deletedText, KillRingPushOptions{Prepend: false, Accumulate: e.lastAction == "kill"})
		e.lastAction = "kill"

		e.state.lines[e.state.cursorLine] = currentLine[:e.state.cursorCol]
	} else if e.state.cursorLine < len(e.state.lines)-1 {
		e.pushUndoSnapshot()

		e.killRing.Push("\n", KillRingPushOptions{Prepend: false, Accumulate: e.lastAction == "kill"})
		e.lastAction = "kill"

		nextLine := e.state.lines[e.state.cursorLine+1]
		e.state.lines[e.state.cursorLine] = currentLine + nextLine
		e.state.lines = append(e.state.lines[:e.state.cursorLine+1], e.state.lines[e.state.cursorLine+2:]...)
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) deleteWordBackwards() {
	e.exitHistoryBrowsing()

	currentLine := e.state.lines[e.state.cursorLine]

	if e.state.cursorCol == 0 {
		if e.state.cursorLine > 0 {
			e.pushUndoSnapshot()

			e.killRing.Push("\n", KillRingPushOptions{Prepend: true, Accumulate: e.lastAction == "kill"})
			e.lastAction = "kill"

			previousLine := e.state.lines[e.state.cursorLine-1]
			e.state.lines[e.state.cursorLine-1] = previousLine + currentLine
			e.state.lines = append(e.state.lines[:e.state.cursorLine], e.state.lines[e.state.cursorLine+1:]...)
			e.state.cursorLine--
			e.setCursorCol(len(previousLine))
		}
	} else {
		e.pushUndoSnapshot()

		wasKill := e.lastAction == "kill"

		oldCursorCol := e.state.cursorCol
		e.moveWordBackwards()
		deleteFrom := e.state.cursorCol
		e.setCursorCol(oldCursorCol)

		deletedText := currentLine[deleteFrom:e.state.cursorCol]
		e.killRing.Push(deletedText, KillRingPushOptions{Prepend: true, Accumulate: wasKill})
		e.lastAction = "kill"

		e.state.lines[e.state.cursorLine] = currentLine[:deleteFrom] + currentLine[e.state.cursorCol:]
		e.setCursorCol(deleteFrom)
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) deleteWordForward() {
	e.exitHistoryBrowsing()

	currentLine := e.state.lines[e.state.cursorLine]

	if e.state.cursorCol >= len(currentLine) {
		if e.state.cursorLine < len(e.state.lines)-1 {
			e.pushUndoSnapshot()

			e.killRing.Push("\n", KillRingPushOptions{Prepend: false, Accumulate: e.lastAction == "kill"})
			e.lastAction = "kill"

			nextLine := e.state.lines[e.state.cursorLine+1]
			e.state.lines[e.state.cursorLine] = currentLine + nextLine
			e.state.lines = append(e.state.lines[:e.state.cursorLine+1], e.state.lines[e.state.cursorLine+2:]...)
		}
	} else {
		e.pushUndoSnapshot()

		wasKill := e.lastAction == "kill"

		oldCursorCol := e.state.cursorCol
		e.moveWordForwards()
		deleteTo := e.state.cursorCol
		e.setCursorCol(oldCursorCol)

		deletedText := currentLine[e.state.cursorCol:deleteTo]
		e.killRing.Push(deletedText, KillRingPushOptions{Prepend: false, Accumulate: wasKill})
		e.lastAction = "kill"

		e.state.lines[e.state.cursorLine] = currentLine[:e.state.cursorCol] + currentLine[deleteTo:]
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) handleBackspace() {
	e.exitHistoryBrowsing()
	e.lastAction = ""

	if e.state.cursorCol > 0 {
		e.pushUndoSnapshot()

		beforeCursor := e.state.lines[e.state.cursorLine][:e.state.cursorCol]
		segments := e.segment(beforeCursor, false)
		graphemeLength := 1
		lastSegment := ""
		if len(segments) > 0 {
			lastSegment = segments[len(segments)-1].segment
			graphemeLength = len(lastSegment)
		}

		if isPasteMarker(lastSegment) {
			sub := pasteMarkerSingle.FindStringSubmatch(lastSegment)
			targetID := 0
			for _, c := range sub[1] {
				targetID = targetID*10 + int(c-'0')
			}
			delete(e.pastes, targetID)
			for i, id := range e.pasteOrder {
				if id == targetID {
					e.pasteOrder = append(e.pasteOrder[:i], e.pasteOrder[i+1:]...)
					break
				}
			}
			e.pasteCounter--

			// Shift higher registry entries down.
			for _, id := range e.sortPasteIDs() {
				if id > targetID {
					e.pastes[id-1] = e.pastes[id]
					delete(e.pastes, id)
				}
			}
			for i, id := range e.pasteOrder {
				if id > targetID {
					e.pasteOrder[i] = id - 1
				}
			}

			// Renumber markers above the removed one.
			for i, line := range e.state.lines {
				e.state.lines[i] = pasteMarkerPattern.ReplaceAllStringFunc(line, func(fullMatch string) string {
					sub := pasteMarkerPattern.FindStringSubmatch(fullMatch)
					x := 0
					for _, c := range sub[1] {
						x = x*10 + int(c-'0')
					}
					if x <= targetID {
						return fullMatch
					}
					return "[paste #" + itoa(x-1) + sub[2] + "]"
				})
			}
		}

		line := e.state.lines[e.state.cursorLine]
		e.state.lines[e.state.cursorLine] = line[:e.state.cursorCol-graphemeLength] + line[e.state.cursorCol:]
		e.setCursorCol(e.state.cursorCol - graphemeLength)
	} else if e.state.cursorLine > 0 {
		e.pushUndoSnapshot()

		currentLine := e.state.lines[e.state.cursorLine]
		previousLine := e.state.lines[e.state.cursorLine-1]

		e.state.lines[e.state.cursorLine-1] = previousLine + currentLine
		e.state.lines = append(e.state.lines[:e.state.cursorLine], e.state.lines[e.state.cursorLine+1:]...)
		e.state.cursorLine--
		e.setCursorCol(len(previousLine))
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}

	e.afterEditRecovery()
}

func (e *Editor) handleForwardDelete() {
	e.exitHistoryBrowsing()
	e.lastAction = ""

	currentLine := e.state.lines[e.state.cursorLine]

	if e.state.cursorCol < len(currentLine) {
		e.pushUndoSnapshot()

		afterCursor := currentLine[e.state.cursorCol:]
		segments := e.segment(afterCursor, false)
		graphemeLength := 1
		if len(segments) > 0 {
			graphemeLength = len(segments[0].segment)
		}

		e.state.lines[e.state.cursorLine] = currentLine[:e.state.cursorCol] + currentLine[e.state.cursorCol+graphemeLength:]
	} else if e.state.cursorLine < len(e.state.lines)-1 {
		e.pushUndoSnapshot()

		nextLine := e.state.lines[e.state.cursorLine+1]
		e.state.lines[e.state.cursorLine] = currentLine + nextLine
		e.state.lines = append(e.state.lines[:e.state.cursorLine+1], e.state.lines[e.state.cursorLine+2:]...)
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}

	e.afterEditRecovery()
}

// afterEditRecovery updates or re-triggers autocomplete after deletions.
func (e *Editor) afterEditRecovery() {
	if e.autocompleteState != "" {
		e.updateAutocomplete()
	} else {
		currentLine := e.state.lines[e.state.cursorLine]
		textBeforeCursor := currentLine[:e.state.cursorCol]
		if e.isInSlashCommandContext(textBeforeCursor) {
			e.tryTriggerAutocomplete(false)
		} else if e.autocompleteTriggerPattern.MatchString(textBeforeCursor) {
			e.tryTriggerAutocomplete(false)
		}
	}
}

func (e *Editor) yank() {
	if e.killRing.Len() == 0 {
		return
	}
	e.pushUndoSnapshot()

	text, _ := e.killRing.Peek()
	e.insertYankedText(text)
	e.lastAction = "yank"
}

func (e *Editor) yankPop() {
	if e.lastAction != "yank" || e.killRing.Len() <= 1 {
		return
	}
	e.pushUndoSnapshot()

	e.deleteYankedText()
	e.killRing.Rotate()

	text, _ := e.killRing.Peek()
	e.insertYankedText(text)
	e.lastAction = "yank"
}

func (e *Editor) insertYankedText(text string) {
	e.exitHistoryBrowsing()
	lines := strings.Split(text, "\n")

	if len(lines) == 1 {
		currentLine := e.state.lines[e.state.cursorLine]
		before := currentLine[:e.state.cursorCol]
		after := currentLine[e.state.cursorCol:]
		e.state.lines[e.state.cursorLine] = before + text + after
		e.setCursorCol(e.state.cursorCol + len(text))
	} else {
		currentLine := e.state.lines[e.state.cursorLine]
		before := currentLine[:e.state.cursorCol]
		after := currentLine[e.state.cursorCol:]

		e.state.lines[e.state.cursorLine] = before + lines[0]

		var middle []string
		for i := 1; i < len(lines)-1; i++ {
			middle = append(middle, lines[i])
		}

		lastLineIndex := e.state.cursorLine + len(lines) - 1
		newLines := append([]string(nil), e.state.lines[:e.state.cursorLine+1]...)
		newLines = append(newLines, middle...)
		newLines = append(newLines, lines[len(lines)-1]+after)
		newLines = append(newLines, e.state.lines[e.state.cursorLine+1:]...)
		e.state.lines = newLines

		e.state.cursorLine = lastLineIndex
		e.setCursorCol(len(lines[len(lines)-1]))
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) deleteYankedText() {
	yankedText, ok := e.killRing.Peek()
	if !ok {
		return
	}

	yankLines := strings.Split(yankedText, "\n")

	if len(yankLines) == 1 {
		currentLine := e.state.lines[e.state.cursorLine]
		deleteLen := len(yankedText)
		before := currentLine[:e.state.cursorCol-deleteLen]
		after := currentLine[e.state.cursorCol:]
		e.state.lines[e.state.cursorLine] = before + after
		e.setCursorCol(e.state.cursorCol - deleteLen)
	} else {
		startLine := e.state.cursorLine - (len(yankLines) - 1)
		startCol := len(e.state.lines[startLine]) - len(yankLines[0])

		afterCursor := e.state.lines[e.state.cursorLine][e.state.cursorCol:]
		beforeYank := e.state.lines[startLine][:startCol]

		var newLines []string
		newLines = append(newLines, e.state.lines[:startLine]...)
		newLines = append(newLines, beforeYank+afterCursor)
		newLines = append(newLines, e.state.lines[startLine+len(yankLines):]...)
		e.state.lines = newLines

		e.state.cursorLine = startLine
		e.setCursorCol(startCol)
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}
