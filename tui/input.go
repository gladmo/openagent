package tui

// Port of components/input.ts: single-line text input with horizontal
// scrolling, kill ring, undo, word navigation, bracketed paste, and Kitty
// CSI-u printable decoding. Cursor offsets are UTF-16-equivalent byte
// offsets into the UTF-8 value; grapheme stepping keeps them aligned.

import (
	"regexp"
	"strings"
)

// InputOptions configure the input. Prompt nil defaults to "> "; an empty
// non-nil prompt renders without a prompt (TS undefined vs "" distinction).
type InputOptions struct {
	Prompt           *string
	Placeholder      string
	PlaceholderStyle func(text string) string
}

type inputState struct {
	value  string
	cursor int
}

// Input is a single-line text input.
type Input struct {
	value               string
	cursor              int // byte offset into value
	prompt              string
	placeholder         string
	placeholderStyle    func(text string) string
	renderedStartColumn int

	OnSubmit func(value string)
	OnEscape func()

	focusedFlag bool

	pasteBuffer string
	isInPaste   bool

	killRing   KillRing
	lastAction string // "kill" | "yank" | "type-word" | ""
	undoStack  *UndoStack[inputState]
}

// NewInput builds an input with the given options.
func NewInput(options InputOptions) *Input {
	prompt := "> "
	if options.Prompt != nil {
		prompt = *options.Prompt
	}
	placeholderStyle := options.PlaceholderStyle
	if placeholderStyle == nil {
		placeholderStyle = func(text string) string { return text }
	}
	return &Input{
		prompt:           prompt,
		placeholder:      options.Placeholder,
		placeholderStyle: placeholderStyle,
		undoStack:        NewUndoStack[inputState](nil),
	}
}

// GetValue returns the current text.
func (i *Input) GetValue() string { return i.value }

// SetValue replaces the text, clamping the cursor.
func (i *Input) SetValue(value string) {
	i.value = value
	if i.cursor > len(value) {
		i.cursor = len(value)
	}
}

// IsFocused implements Focusable.
func (i *Input) IsFocused() bool { return i.focusedFlag }

// SetFocused implements Focusable.
func (i *Input) SetFocused(focused bool) { i.focusedFlag = focused }

var pasteNewlinesPattern = regexp.MustCompile(`\r\n|\r|\n`)

// HandleInput processes one input chunk (key sequence or paste fragment).
func (i *Input) HandleInput(data string) {
	// Bracketed paste: start marker \x1b[200~, end marker \x1b[201~.
	if strings.Contains(data, "\x1b[200~") {
		i.isInPaste = true
		i.pasteBuffer = ""
		data = strings.Replace(data, "\x1b[200~", "", 1)
	}

	if i.isInPaste {
		i.pasteBuffer += data
		if endIndex := strings.Index(i.pasteBuffer, "\x1b[201~"); endIndex != -1 {
			pasteContent := i.pasteBuffer[:endIndex]
			i.handlePaste(pasteContent)
			i.isInPaste = false
			remaining := i.pasteBuffer[endIndex+6:] // 6 = len(\x1b[201~)
			i.pasteBuffer = ""
			if remaining != "" {
				i.HandleInput(remaining)
			}
		}
		return
	}

	kb := GetKeybindings()

	switch {
	case kb.Matches(data, "tui.select.cancel"):
		if i.OnEscape != nil {
			i.OnEscape()
		}
		return
	case kb.Matches(data, "tui.editor.undo"):
		i.undo()
		return
	case kb.Matches(data, "tui.input.submit") || data == "\n":
		if i.OnSubmit != nil {
			i.OnSubmit(i.value)
		}
		return
	case kb.Matches(data, "tui.editor.deleteCharBackward"):
		i.handleBackspace()
		return
	case kb.Matches(data, "tui.editor.deleteCharForward"):
		i.handleForwardDelete()
		return
	case kb.Matches(data, "tui.editor.deleteWordBackward"):
		i.deleteWordBackwards()
		return
	case kb.Matches(data, "tui.editor.deleteWordForward"):
		i.deleteWordForward()
		return
	case kb.Matches(data, "tui.editor.deleteToLineStart"):
		i.deleteToLineStart()
		return
	case kb.Matches(data, "tui.editor.deleteToLineEnd"):
		i.deleteToLineEnd()
		return
	case kb.Matches(data, "tui.editor.yank"):
		i.yank()
		return
	case kb.Matches(data, "tui.editor.yankPop"):
		i.yankPop()
		return
	case kb.Matches(data, "tui.editor.cursorLeft"):
		i.lastAction = ""
		if i.cursor > 0 {
			i.cursor -= lastGraphemeLen(i.value[:i.cursor])
		}
		return
	case kb.Matches(data, "tui.editor.cursorRight"):
		i.lastAction = ""
		if i.cursor < len(i.value) {
			i.cursor += firstGraphemeLen(i.value[i.cursor:])
		}
		return
	case kb.Matches(data, "tui.editor.cursorLineStart"):
		i.lastAction = ""
		i.cursor = 0
		return
	case kb.Matches(data, "tui.editor.cursorLineEnd"):
		i.lastAction = ""
		i.cursor = len(i.value)
		return
	case kb.Matches(data, "tui.editor.cursorWordLeft"):
		i.moveWordBackwards()
		return
	case kb.Matches(data, "tui.editor.cursorWordRight"):
		i.moveWordForwards()
		return
	}

	// Kitty CSI-u printable character: terminals with flag 1 send CSI-u for
	// plain printables; decode before the control-char check since the
	// sequence contains \x1b.
	if kittyPrintable, ok := DecodeKittyPrintable(data); ok {
		i.insertCharacter(kittyPrintable)
		return
	}

	// Regular input: printable including Unicode, rejecting control chars
	// (C0 0x00-0x1F, DEL 0x7F, C1 0x80-0x9F).
	hasControlChars := false
	for _, ch := range data {
		if ch < 32 || ch == 0x7f || (ch >= 0x80 && ch <= 0x9f) {
			hasControlChars = true
			break
		}
	}
	if !hasControlChars {
		i.insertCharacter(data)
	}
}

// HandleMouse positions the cursor on a left press.
func (i *Input) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	if event.Type != MousePress || event.Button != MouseLeft || event.Y != 0 {
		return nil
	}
	visibleColumn := maxInt(0, event.X-2)
	targetColumn := i.renderedStartColumn + visibleColumn
	currentColumn := 0
	i.cursor = len(i.value)
	byteOffset := 0
	for _, grapheme := range segmentGraphemes(i.value) {
		nextColumn := currentColumn + VisibleWidth(grapheme)
		if targetColumn < nextColumn {
			i.cursor = byteOffset
			break
		}
		currentColumn = nextColumn
		byteOffset += len(grapheme)
	}
	i.lastAction = ""
	return &TuiMouseEventResult{Handled: true, Focus: true}
}

func (i *Input) insertCharacter(char string) {
	// Undo coalescing: consecutive word chars coalesce into one undo unit.
	if len(char) == 0 {
		return
	}
	firstRune := []rune(char)[0]
	if IsWhitespaceRune(firstRune) || i.lastAction != "type-word" {
		i.pushUndo()
	}
	i.lastAction = "type-word"

	i.value = i.value[:i.cursor] + char + i.value[i.cursor:]
	i.cursor += len(char)
}

func (i *Input) handleBackspace() {
	i.lastAction = ""
	if i.cursor > 0 {
		i.pushUndo()
		graphemeLength := lastGraphemeLen(i.value[:i.cursor])
		i.value = i.value[:i.cursor-graphemeLength] + i.value[i.cursor:]
		i.cursor -= graphemeLength
	}
}

func (i *Input) handleForwardDelete() {
	i.lastAction = ""
	if i.cursor < len(i.value) {
		i.pushUndo()
		graphemeLength := firstGraphemeLen(i.value[i.cursor:])
		i.value = i.value[:i.cursor] + i.value[i.cursor+graphemeLength:]
	}
}

func (i *Input) deleteToLineStart() {
	if i.cursor == 0 {
		return
	}
	i.pushUndo()
	deletedText := i.value[:i.cursor]
	i.killRing.Push(deletedText, KillRingPushOptions{Prepend: true, Accumulate: i.lastAction == "kill"})
	i.lastAction = "kill"
	i.value = i.value[i.cursor:]
	i.cursor = 0
}

func (i *Input) deleteToLineEnd() {
	if i.cursor >= len(i.value) {
		return
	}
	i.pushUndo()
	deletedText := i.value[i.cursor:]
	i.killRing.Push(deletedText, KillRingPushOptions{Prepend: false, Accumulate: i.lastAction == "kill"})
	i.lastAction = "kill"
	i.value = i.value[:i.cursor]
}

func (i *Input) deleteWordBackwards() {
	if i.cursor == 0 {
		return
	}
	wasKill := i.lastAction == "kill"
	i.pushUndo()

	oldCursor := i.cursor
	i.moveWordBackwards()
	deleteFrom := i.cursor
	i.cursor = oldCursor

	deletedText := i.value[deleteFrom:i.cursor]
	i.killRing.Push(deletedText, KillRingPushOptions{Prepend: true, Accumulate: wasKill})
	i.lastAction = "kill"

	i.value = i.value[:deleteFrom] + i.value[i.cursor:]
	i.cursor = deleteFrom
}

func (i *Input) deleteWordForward() {
	if i.cursor >= len(i.value) {
		return
	}
	wasKill := i.lastAction == "kill"
	i.pushUndo()

	oldCursor := i.cursor
	i.moveWordForwards()
	deleteTo := i.cursor
	i.cursor = oldCursor

	deletedText := i.value[i.cursor:deleteTo]
	i.killRing.Push(deletedText, KillRingPushOptions{Prepend: false, Accumulate: wasKill})
	i.lastAction = "kill"

	i.value = i.value[:i.cursor] + i.value[deleteTo:]
}

func (i *Input) yank() {
	text, ok := i.killRing.Peek()
	if !ok {
		return
	}
	i.pushUndo()
	i.value = i.value[:i.cursor] + text + i.value[i.cursor:]
	i.cursor += len(text)
	i.lastAction = "yank"
}

func (i *Input) yankPop() {
	if i.lastAction != "yank" || i.killRing.Len() <= 1 {
		return
	}
	i.pushUndo()

	prevText, _ := i.killRing.Peek()
	i.value = i.value[:i.cursor-len(prevText)] + i.value[i.cursor:]
	i.cursor -= len(prevText)

	i.killRing.Rotate()
	text, _ := i.killRing.Peek()
	i.value = i.value[:i.cursor] + text + i.value[i.cursor:]
	i.cursor += len(text)
	i.lastAction = "yank"
}

func (i *Input) pushUndo() {
	i.undoStack.Push(inputState{value: i.value, cursor: i.cursor})
}

func (i *Input) undo() {
	snapshot, ok := i.undoStack.Pop()
	if !ok {
		return
	}
	i.value = snapshot.value
	i.cursor = snapshot.cursor
	i.lastAction = ""
}

func (i *Input) moveWordBackwards() {
	if i.cursor == 0 {
		return
	}
	i.lastAction = ""
	prefix := i.value[:i.cursor]
	runesPrefix := []rune(prefix)
	newPos := FindWordBackward(prefix, len(runesPrefix), nil)
	i.cursor = len(string(runesPrefix[:newPos]))
}

func (i *Input) moveWordForwards() {
	if i.cursor >= len(i.value) {
		return
	}
	i.lastAction = ""
	// Convert the byte cursor to a rune index, move, convert back.
	runeValue := []rune(i.value)
	byteToRune := len([]rune(i.value[:i.cursor]))
	moved := FindWordForward(string(runeValue[byteToRune:]), 0, nil)
	i.cursor = len(string(runeValue[:byteToRune+int(moved)]))
}

func (i *Input) handlePaste(pastedText string) {
	i.lastAction = ""
	i.pushUndo()

	clean := pasteNewlinesPattern.ReplaceAllString(pastedText, "")
	clean = strings.ReplaceAll(clean, "\t", "    ")

	i.value = i.value[:i.cursor] + clean + i.value[i.cursor:]
	i.cursor += len(clean)
}

// Invalidate is a no-op (no cached state).
func (i *Input) Invalidate() {}

// Render draws the input with a fake inverse-video cursor and horizontal
// scrolling.
func (i *Input) Render(width int) []string {
	availableWidth := width - VisibleWidth(i.prompt)

	if availableWidth <= 0 {
		return []string{TruncateToWidth(i.prompt, width, "", false)}
	}

	if len(i.value) == 0 && i.placeholder != "" {
		placeholder := TruncateToWidth(i.placeholder, availableWidth, "", false)
		graphemes := segmentGraphemes(placeholder)
		atCursor := " "
		if len(graphemes) > 0 {
			atCursor = graphemes[0]
		}
		afterCursor := placeholder[len(atCursor):]
		marker := ""
		if i.focusedFlag {
			marker = CURSOR_MARKER
		}
		cursorChar := RenderFakeCursor(i.placeholderStyle(atCursor))
		textWithCursor := marker + cursorChar + i.placeholderStyle(afterCursor)
		padding := stringRepeat(" ", maxInt(0, availableWidth-VisibleWidth(textWithCursor)))
		return []string{i.prompt + textWithCursor + padding}
	}

	visibleText := ""
	cursorDisplay := i.cursor
	i.renderedStartColumn = 0
	totalWidth := VisibleWidth(i.value)

	if totalWidth < availableWidth {
		// Everything fits (leave room for cursor at end).
		visibleText = i.value
	} else {
		// Horizontal scrolling; reserve a column when the cursor is at the end.
		scrollWidth := availableWidth
		if i.cursor == len(i.value) {
			scrollWidth = availableWidth - 1
		}
		cursorCol := VisibleWidth(i.value[:i.cursor])

		if scrollWidth > 0 {
			halfWidth := scrollWidth / 2
			startCol := 0

			if cursorCol < halfWidth {
				startCol = 0
			} else if cursorCol > totalWidth-halfWidth {
				startCol = maxInt(0, totalWidth-scrollWidth)
			} else {
				startCol = maxInt(0, cursorCol-halfWidth)
			}

			i.renderedStartColumn = startCol
			visibleText = SliceByColumn(i.value, startCol, scrollWidth, true)
			beforeCursor := SliceByColumn(i.value, startCol, maxInt(0, cursorCol-startCol), true)
			cursorDisplay = len(beforeCursor)
		} else {
			visibleText = ""
			cursorDisplay = 0
		}
	}

	// Insert the cursor character at the cursor position.
	atCursor := " "
	if cursorDisplay < len(visibleText) {
		after := visibleText[cursorDisplay:]
		atCursor = firstGraphemeStr(after)
	}
	beforeCursor := visibleText[:cursorDisplay]
	afterCursor := ""
	if cursorDisplay+len(atCursor) <= len(visibleText) {
		afterCursor = visibleText[cursorDisplay+len(atCursor):]
	}

	// Hardware cursor marker (zero-width, before the fake cursor for IME).
	marker := ""
	if i.focusedFlag {
		marker = CURSOR_MARKER
	}

	// ESC[7m reverse video, ESC[27m normal.
	// The TUI draws the fake cursor in reverse video, or omits it when
	// the hardware cursor is shown.
	cursorChar := RenderFakeCursor(atCursor)
	textWithCursor := beforeCursor + marker + cursorChar + afterCursor

	visualLength := VisibleWidth(textWithCursor)
	padding := stringRepeat(" ", maxInt(0, availableWidth-visualLength))
	return []string{i.prompt + textWithCursor + padding}
}

func lastGraphemeLen(s string) int {
	graphemes := segmentGraphemes(s)
	if len(graphemes) == 0 {
		return 1
	}
	return len(graphemes[len(graphemes)-1])
}

func firstGraphemeLen(s string) int {
	graphemes := segmentGraphemes(s)
	if len(graphemes) == 0 {
		return 1
	}
	return len(graphemes[0])
}

func firstGraphemeStr(s string) string {
	graphemes := segmentGraphemes(s)
	if len(graphemes) == 0 {
		return ""
	}
	return graphemes[0]
}
