package tui

// Port of components/editor.ts: multi-line editor with word-aware wrapping,
// paste markers, history, kill ring, undo, character jump, sticky-column
// navigation, and autocomplete integration. Byte offsets into UTF-8 lines
// replace JS UTF-16 indices; segmentation supplies byte indexes.

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gladmo/openagent/abort"
)

var pasteMarkerPattern = regexp.MustCompile(`\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]`)
var pasteMarkerSingle = regexp.MustCompile(`^\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]$`)

func isPasteMarker(segment string) bool {
	return len(segment) >= 10 && pasteMarkerSingle.MatchString(segment)
}

// editorSegment is one segmentation unit with its byte index.
type editorSegment struct {
	segment string
	index   int
}

// segmentWithMarkers merges paste-marker graphemes into atomic segments for
// the given segmentation mode.
func segmentWithMarkers(text string, word bool, validIDs map[int]string) []editorSegment {
	if len(validIDs) == 0 || !strings.Contains(text, "[paste #") {
		return baseEditorSegments(text, word)
	}

	type markerSpan struct{ start, end int }
	var markers []markerSpan
	for _, loc := range pasteMarkerPattern.FindAllStringSubmatchIndex(text, -1) {
		id := 0
		for _, c := range text[loc[2]:loc[3]] {
			id = id*10 + int(c-'0')
		}
		if _, ok := validIDs[id]; !ok {
			continue
		}
		markers = append(markers, markerSpan{start: loc[0], end: loc[1]})
	}
	if len(markers) == 0 {
		return baseEditorSegments(text, word)
	}

	base := baseEditorSegments(text, word)
	var result []editorSegment
	markerIdx := 0
	for _, seg := range base {
		for markerIdx < len(markers) && markers[markerIdx].end <= seg.index {
			markerIdx++
		}
		if markerIdx < len(markers) {
			marker := markers[markerIdx]
			if seg.index >= marker.start && seg.index < marker.end {
				if seg.index == marker.start {
					result = append(result, editorSegment{segment: text[marker.start:marker.end], index: marker.start})
				}
				continue
			}
		}
		result = append(result, seg)
	}
	return result
}

func baseEditorSegments(text string, word bool) []editorSegment {
	if word {
		segments := segmentWords(text)
		out := make([]editorSegment, 0, len(segments))
		offset := 0
		for _, segment := range segments {
			out = append(out, editorSegment{segment: segment.Segment, index: offset})
			offset += len(segment.Segment)
		}
		return out
	}
	graphemes := segmentGraphemes(text)
	out := make([]editorSegment, 0, len(graphemes))
	offset := 0
	for _, grapheme := range graphemes {
		out = append(out, editorSegment{segment: grapheme, index: offset})
		offset += len(grapheme)
	}
	return out
}

// TextChunk is one word-wrapped slice with its source range.
type TextChunk struct {
	Text       string
	StartIndex int
	EndIndex   int
}

// WordWrapLine wraps a line at word boundaries with CJK break support and
// paste-marker-aware atomic segments.
func WordWrapLine(line string, maxWidth int, preSegmented []editorSegment) []TextChunk {
	if line == "" || maxWidth <= 0 {
		return []TextChunk{{Text: "", StartIndex: 0, EndIndex: 0}}
	}

	lineWidth := VisibleWidth(line)
	if lineWidth <= maxWidth {
		return []TextChunk{{Text: line, StartIndex: 0, EndIndex: len(line)}}
	}

	segments := preSegmented
	if segments == nil {
		segments = baseEditorSegments(line, false)
	}

	var chunks []TextChunk
	currentWidth := 0
	chunkStart := 0

	wrapOppIndex := -1
	wrapOppWidth := 0

	for i := 0; i < len(segments); i++ {
		seg := segments[i]
		grapheme := seg.segment
		gWidth := VisibleWidth(grapheme)
		charIndex := seg.index
		isWs := !isPasteMarker(grapheme) && IsWhitespaceRune([]rune(grapheme)[0])

		if currentWidth+gWidth > maxWidth {
			if wrapOppIndex >= 0 && currentWidth-wrapOppWidth+gWidth <= maxWidth {
				chunks = append(chunks, TextChunk{Text: line[chunkStart:wrapOppIndex], StartIndex: chunkStart, EndIndex: wrapOppIndex})
				chunkStart = wrapOppIndex
				currentWidth -= wrapOppWidth
			} else if chunkStart < charIndex {
				chunks = append(chunks, TextChunk{Text: line[chunkStart:charIndex], StartIndex: chunkStart, EndIndex: charIndex})
				chunkStart = charIndex
				currentWidth = 0
			}
			wrapOppIndex = -1
		}

		if gWidth > maxWidth {
			// Single atomic segment wider than maxWidth: re-wrap visually.
			subChunks := WordWrapLine(grapheme, maxWidth, nil)
			for j := 0; j < len(subChunks)-1; j++ {
				sc := subChunks[j]
				chunks = append(chunks, TextChunk{Text: sc.Text, StartIndex: charIndex + sc.StartIndex, EndIndex: charIndex + sc.EndIndex})
			}
			last := subChunks[len(subChunks)-1]
			chunkStart = charIndex + last.StartIndex
			currentWidth = VisibleWidth(last.Text)
			wrapOppIndex = -1
			continue
		}

		currentWidth += gWidth

		var next *editorSegment
		if i+1 < len(segments) {
			next = &segments[i+1]
		}
		if next != nil {
			nextIsWs := !isPasteMarker(next.segment) && len(next.segment) > 0 && IsWhitespaceRune([]rune(next.segment)[0])
			if isWs && (isPasteMarker(next.segment) || !nextIsWs) {
				wrapOppIndex = next.index
				wrapOppWidth = currentWidth
			} else if !isWs && !nextIsWs {
				isCjk := !isPasteMarker(grapheme) && len(grapheme) > 0 && CjkBreakRune([]rune(grapheme)[0])
				nextIsCjk := !isPasteMarker(next.segment) && len(next.segment) > 0 && CjkBreakRune([]rune(next.segment)[0])
				if isCjk || nextIsCjk {
					wrapOppIndex = next.index
					wrapOppWidth = currentWidth
				}
			}
		}
	}

	chunks = append(chunks, TextChunk{Text: line[chunkStart:], StartIndex: chunkStart, EndIndex: len(line)})
	return chunks
}

// EditorTheme styles the editor chrome.
type EditorTheme struct {
	BorderColor func(str string) string
	SelectList  SelectListTheme
}

// EditorOptions configure the editor.
type EditorOptions struct {
	PaddingX               *int
	AutocompleteMaxVisible *int
	// TopBorder replaces the top border line when it returns a non-empty
	// string (the Go seam for the reference's CustomEditor.renderTopBorder
	// override, used to embed the working status in the border).
	TopBorder func(width, hiddenLineCount int) string
}

type editorState struct {
	lines      []string
	cursorLine int
	cursorCol  int
}

type editorSnapshot struct {
	state        editorState
	pastes       map[int]string
	pasteOrder   []int
	pasteCounter int
}

type editorLayoutLine struct {
	text      string
	hasCursor bool
	cursorPos int
}

const attachmentAutocompleteDebounceMs = 20

var defaultAutocompleteTriggerCharacters = []string{"@", "#"}

var editorWordChars = regexp.MustCompile(`[a-zA-Z0-9.\-_]`)
var editorCtrlUPaste = regexp.MustCompile(`\x1b\[(\d+);5u`)

var editorTriggerWordChars = regexp.MustCompile(`^[a-zA-Z0-9.\-_]$`)

// escapeCharacterClass escapes for a regex character class.
func escapeCharacterClass(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '\\', '^', '$', '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|', '-':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// triggerBoundaryClass matches a token boundary (line start, whitespace, or
// CJK punctuation).
const triggerBoundaryClass = "(?:^|[，．：；！？（）［］｛｝“”‘’…—]|" + jsWhitespaceClass + ")"

// wrapperRun matches opening wrappers like (( or ([ that may precede a token.
const wrapperRun = "[(\\[{<`]*"

// buildTriggerPattern matches "@..." / trigger-char tokens at boundaries.
func buildTriggerPattern(triggerCharacters []string) *regexp.Regexp {
	class := ""
	for _, character := range triggerCharacters {
		class += escapeCharacterClass(character)
	}
	notSeparator := "[^\\s，．：；！？（）［］｛｝“”‘’…—]*"
	source := triggerBoundaryClass + wrapperRun + `(?:@"[^"]*|[` + class + `]` + notSeparator + `)$`
	return regexp.MustCompile(source)
}

// buildDebouncePattern matches non-@ triggers for debouncing.
func buildDebouncePattern(triggerCharacters []string) *regexp.Regexp {
	class := ""
	for _, character := range triggerCharacters {
		if character == "@" {
			continue
		}
		class += escapeCharacterClass(character)
	}
	notSeparator := "[^\\s，．：；！？（）［］｛｝“”‘’…—]*"
	source := triggerBoundaryClass + wrapperRun + `(?:@(?:"[^"]*|` + notSeparator + `)|[` + class + `]` + notSeparator + `)$`
	return regexp.MustCompile(source)
}

func createScrollBorder(direction string, hiddenLineCount, width int) string {
	availableWidth := maxInt(0, width)
	label := " " + direction + " " + itoa(hiddenLineCount) + " more "
	labelWidth := VisibleWidth(label)
	if labelWidth+2 <= availableWidth {
		leftWidth := (availableWidth - labelWidth) / 2
		return stringRepeat("─", leftWidth) + label + stringRepeat("─", availableWidth-leftWidth-labelWidth)
	}

	indicator := "─── " + direction + " " + itoa(hiddenLineCount) + " more "
	remaining := availableWidth - VisibleWidth(indicator)
	if remaining >= 0 {
		return indicator + stringRepeat("─", remaining)
	}

	ellipsis := "..."
	if availableWidth < 3 {
		ellipsis = "..."[:maxInt(0, availableWidth)]
	}
	indicatorWidth := availableWidth - VisibleWidth(ellipsis)
	return SliceByColumn(indicator, 0, indicatorWidth, true) + ellipsis
}

// Editor is the multi-line text editor component.
type Editor struct {
	tui      TUI
	theme    EditorTheme
	paddingX int

	state editorState

	focusedFlag bool

	lastWidth                  int
	renderedVisibleLineCount   int
	renderedAutocompleteHeight int

	scrollOffset int

	BorderColor func(str string) string

	// topBorder optionally replaces the rendered top border line.
	topBorder func(width, hiddenLineCount int) string

	autocompleteProvider          AutocompleteProvider
	autocompleteTriggerCharacters []string
	autocompleteTriggerPattern    *regexp.Regexp
	autocompleteDebouncePattern   *regexp.Regexp
	autocompleteList              *SelectList
	autocompleteState             string // "" | "regular" | "force"
	autocompletePrefix            string
	autocompleteMaxVisible        int
	autocompleteAbort             *abort.Controller
	autocompleteDebounceTimer     *time.Timer
	autocompleteStartToken        int
	autocompleteRequestId         int

	pastes       map[int]string
	pasteOrder   []int
	pasteCounter int

	pasteBuffer string
	isInPaste   bool

	history      []string
	historyIndex int
	historyDraft *editorState

	killRing   KillRing
	lastAction string

	jumpMode string // "" | "forward" | "backward"

	preferredVisualCol   *int
	snappedFromCursorCol *int

	undoStack *UndoStack[editorSnapshot]

	OnSubmit      func(text string)
	OnChange      func(text string)
	DisableSubmit bool

	// After schedules debounced autocomplete (injectable for tests).
	After func(d time.Duration, fn func()) *time.Timer
}

// NewEditor builds the editor.
func NewEditor(tui TUI, theme EditorTheme, options EditorOptions) *Editor {
	paddingX := 0
	if options.PaddingX != nil {
		paddingX = *options.PaddingX
		if paddingX < 0 {
			paddingX = 0
		}
	}
	maxVisible := 5
	if options.AutocompleteMaxVisible != nil {
		maxVisible = *options.AutocompleteMaxVisible
		if maxVisible < 3 {
			maxVisible = 3
		}
		if maxVisible > 20 {
			maxVisible = 20
		}
	}
	editor := &Editor{
		tui:                           tui,
		theme:                         theme,
		state:                         editorState{lines: []string{""}},
		lastWidth:                     80,
		BorderColor:                   theme.BorderColor,
		autocompleteTriggerCharacters: append([]string(nil), defaultAutocompleteTriggerCharacters...),
		autocompleteMaxVisible:        maxVisible,
		pastes:                        map[int]string{},
		historyIndex:                  -1,
		undoStack:                     NewUndoStack[editorSnapshot](nil),
		After:                         time.AfterFunc,
		topBorder:                     options.TopBorder,
	}
	editor.autocompleteTriggerPattern = buildTriggerPattern(editor.autocompleteTriggerCharacters)
	editor.autocompleteDebouncePattern = buildDebouncePattern(editor.autocompleteTriggerCharacters)
	return editor
}

func (e *Editor) validPasteIds() map[int]string { return e.pastes }

func (e *Editor) segment(text string, word bool) []editorSegment {
	return segmentWithMarkers(text, word, e.validPasteIds())
}

// GetPaddingX returns horizontal padding.
func (e *Editor) GetPaddingX() int { return e.paddingX }

// SetPaddingX updates horizontal padding.
func (e *Editor) SetPaddingX(padding int) {
	newPadding := maxInt(0, padding)
	if e.paddingX != newPadding {
		e.paddingX = newPadding
		e.tui.RequestRender(false)
	}
}

// GetAutocompleteMaxVisible returns the autocomplete height cap.
func (e *Editor) GetAutocompleteMaxVisible() int { return e.autocompleteMaxVisible }

// SetAutocompleteMaxVisible updates the autocomplete height cap.
func (e *Editor) SetAutocompleteMaxVisible(maxVisible int) {
	newMaxVisible := maxInt(3, minInt(20, maxVisible))
	if e.autocompleteMaxVisible != newMaxVisible {
		e.autocompleteMaxVisible = newMaxVisible
		e.tui.RequestRender(false)
	}
}

// SetAutocompleteProvider installs the provider.
func (e *Editor) SetAutocompleteProvider(provider AutocompleteProvider) {
	e.cancelAutocomplete()
	e.autocompleteProvider = provider
	e.setAutocompleteTriggerCharacters(provider.TriggerCharacters())
}

// SetBorderColor updates the border color.
func (e *Editor) SetBorderColor(color func(str string) string) {
	e.BorderColor = color
}

// IsFocused implements Focusable.
func (e *Editor) IsFocused() bool { return e.focusedFlag }

// SetFocused implements Focusable.
func (e *Editor) SetFocused(focused bool) { e.focusedFlag = focused }

// AddToHistory records a prompt (deduped consecutive, capped at 100).
func (e *Editor) AddToHistory(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if len(e.history) > 0 && e.history[0] == trimmed {
		return
	}
	e.history = append([]string{trimmed}, e.history...)
	if len(e.history) > 100 {
		e.history = e.history[:100]
	}
}

func (e *Editor) isEditorEmpty() bool {
	return len(e.state.lines) == 1 && e.state.lines[0] == ""
}

func (e *Editor) isOnFirstVisualLine() bool {
	visualLines := e.buildVisualLineMap(e.lastWidth)
	return e.findCurrentVisualLine(visualLines) == 0
}

func (e *Editor) isOnLastVisualLine() bool {
	visualLines := e.buildVisualLineMap(e.lastWidth)
	return e.findCurrentVisualLine(visualLines) == len(visualLines)-1
}

func (e *Editor) navigateHistory(direction int) {
	e.lastAction = ""
	if len(e.history) == 0 {
		return
	}

	newIndex := e.historyIndex - direction
	if newIndex < -1 || newIndex >= len(e.history) {
		return
	}

	if e.historyIndex == -1 && newIndex >= 0 {
		e.pushUndoSnapshot()
		draft := e.cloneState()
		e.historyDraft = &draft
	}

	e.historyIndex = newIndex

	if e.historyIndex == -1 {
		draft := e.historyDraft
		e.historyDraft = nil
		if draft != nil {
			e.state = *draft
			e.preferredVisualCol = nil
			e.snappedFromCursorCol = nil
			e.scrollOffset = 0
			if e.OnChange != nil {
				e.OnChange(e.GetText())
			}
		} else {
			e.setTextInternal("", "end")
		}
	} else {
		placement := "end"
		if direction == -1 {
			placement = "start"
		}
		historyEntry := ""
		if e.historyIndex < len(e.history) {
			historyEntry = e.history[e.historyIndex]
		}
		e.setTextInternal(historyEntry, placement)
	}
}

func (e *Editor) exitHistoryBrowsing() {
	e.historyIndex = -1
	e.historyDraft = nil
}

func (e *Editor) cloneState() editorState {
	return editorState{
		lines:      append([]string(nil), e.state.lines...),
		cursorLine: e.state.cursorLine,
		cursorCol:  e.state.cursorCol,
	}
}

func (e *Editor) setTextInternal(text string, cursorPlacement string) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	e.state.lines = lines
	if cursorPlacement == "start" {
		e.state.cursorLine = 0
		e.setCursorCol(0)
	} else {
		e.state.cursorLine = len(e.state.lines) - 1
		e.setCursorCol(len(e.state.lines[e.state.cursorLine]))
	}
	e.scrollOffset = 0
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

// Invalidate is a no-op (no cached state).
func (e *Editor) Invalidate() {}

func (e *Editor) renderTopBorder(width, hiddenLineCount int) string {
	if e.topBorder != nil {
		if line := e.topBorder(width, hiddenLineCount); line != "" {
			return line
		}
	}
	border := stringRepeat("─", width)
	if hiddenLineCount > 0 {
		border = createScrollBorder("↑", hiddenLineCount, width)
	}
	return e.BorderColor(border)
}

func (e *Editor) renderBottomBorder(width, hiddenLineCount int) string {
	border := stringRepeat("─", width)
	if hiddenLineCount > 0 {
		border = createScrollBorder("↓", hiddenLineCount, width)
	}
	return e.BorderColor(border)
}

// Render lays out and renders the editor with borders and autocomplete.
func (e *Editor) Render(width int) []string {
	maxPadding := maxInt(0, (width-1)/2)
	paddingX := minInt(e.paddingX, maxPadding)
	contentWidth := maxInt(1, width-paddingX*2)

	layoutWidth := contentWidth
	if paddingX == 0 {
		layoutWidth = contentWidth - 1
	}
	layoutWidth = maxInt(1, layoutWidth)

	e.lastWidth = layoutWidth

	layoutLines := e.layoutText(layoutWidth)

	terminalRows := e.tui.Terminal().Rows()
	maxVisibleLines := maxInt(5, terminalRows*3/10)

	cursorLineIndex := -1
	for i, line := range layoutLines {
		if line.hasCursor {
			cursorLineIndex = i
			break
		}
	}
	if cursorLineIndex == -1 {
		cursorLineIndex = 0
	}

	if cursorLineIndex < e.scrollOffset {
		e.scrollOffset = cursorLineIndex
	} else if cursorLineIndex >= e.scrollOffset+maxVisibleLines {
		e.scrollOffset = cursorLineIndex - maxVisibleLines + 1
	}

	maxScrollOffset := maxInt(0, len(layoutLines)-maxVisibleLines)
	e.scrollOffset = maxInt(0, minInt(e.scrollOffset, maxScrollOffset))

	visibleEnd := minInt(len(layoutLines), e.scrollOffset+maxVisibleLines)
	visibleLines := layoutLines[e.scrollOffset:visibleEnd]
	e.renderedVisibleLineCount = len(visibleLines)

	var result []string
	leftPadding := stringRepeat(" ", paddingX)
	rightPadding := leftPadding

	result = append(result, e.renderTopBorder(width, e.scrollOffset))

	for _, layoutLine := range visibleLines {
		displayText := layoutLine.text
		lineVisibleWidth := VisibleWidth(layoutLine.text)
		cursorInPadding := false

		if layoutLine.hasCursor {
			before := displayText[:layoutLine.cursorPos]
			after := displayText[layoutLine.cursorPos:]

			marker := ""
			if e.focusedFlag {
				marker = CURSOR_MARKER
			}

			if len(after) > 0 {
				afterGraphemes := e.segment(after, false)
				firstGrapheme := ""
				if len(afterGraphemes) > 0 {
					firstGrapheme = afterGraphemes[0].segment
				}
				restAfter := after[len(firstGrapheme):]
				cursor := "\x1b[7m" + firstGrapheme + "\x1b[0m"
				displayText = before + marker + cursor + restAfter
			} else {
				cursor := "\x1b[7m \x1b[0m"
				displayText = before + marker + cursor
				lineVisibleWidth++
				if lineVisibleWidth > contentWidth && paddingX > 0 {
					cursorInPadding = true
				}
			}
		}

		padding := stringRepeat(" ", maxInt(0, contentWidth-lineVisibleWidth))
		lineRightPadding := rightPadding
		if cursorInPadding {
			lineRightPadding = rightPadding[1:]
		}
		result = append(result, leftPadding+displayText+padding+lineRightPadding)
	}

	linesBelow := len(layoutLines) - (e.scrollOffset + len(visibleLines))
	result = append(result, e.renderBottomBorder(width, maxInt(0, linesBelow)))

	e.renderedAutocompleteHeight = 0
	if e.autocompleteState != "" && e.autocompleteList != nil {
		autocompleteResult := e.autocompleteList.Render(contentWidth)
		e.renderedAutocompleteHeight = len(autocompleteResult)
		for _, line := range autocompleteResult {
			lineWidth := VisibleWidth(line)
			linePadding := stringRepeat(" ", maxInt(0, contentWidth-lineWidth))
			result = append(result, leftPadding+line+linePadding+rightPadding)
		}
	}

	return result
}

func (e *Editor) layoutText(contentWidth int) []editorLayoutLine {
	var layoutLines []editorLayoutLine

	if len(e.state.lines) == 0 || (len(e.state.lines) == 1 && e.state.lines[0] == "") {
		return []editorLayoutLine{{text: "", hasCursor: true, cursorPos: 0}}
	}

	for i := 0; i < len(e.state.lines); i++ {
		line := e.state.lines[i]
		isCurrentLine := i == e.state.cursorLine
		lineVisibleWidth := VisibleWidth(line)

		if lineVisibleWidth <= contentWidth {
			if isCurrentLine {
				layoutLines = append(layoutLines, editorLayoutLine{text: line, hasCursor: true, cursorPos: e.state.cursorCol})
			} else {
				layoutLines = append(layoutLines, editorLayoutLine{text: line})
			}
		} else {
			chunks := WordWrapLine(line, contentWidth, e.segment(line, false))

			for chunkIndex, chunk := range chunks {
				cursorPos := e.state.cursorCol
				isLastChunk := chunkIndex == len(chunks)-1

				hasCursorInChunk := false
				adjustedCursorPos := 0

				if isCurrentLine {
					if isLastChunk {
						hasCursorInChunk = cursorPos >= chunk.StartIndex
						adjustedCursorPos = cursorPos - chunk.StartIndex
					} else {
						hasCursorInChunk = cursorPos >= chunk.StartIndex && cursorPos < chunk.EndIndex
						if hasCursorInChunk {
							adjustedCursorPos = cursorPos - chunk.StartIndex
							if adjustedCursorPos > len(chunk.Text) {
								adjustedCursorPos = len(chunk.Text)
							}
						}
					}
				}

				if hasCursorInChunk {
					layoutLines = append(layoutLines, editorLayoutLine{text: chunk.Text, hasCursor: true, cursorPos: adjustedCursorPos})
				} else {
					layoutLines = append(layoutLines, editorLayoutLine{text: chunk.Text})
				}
			}
		}
	}

	return layoutLines
}

// GetText returns the raw text (markers unexpanded).
func (e *Editor) GetText() string {
	return strings.Join(e.state.lines, "\n")
}

func (e *Editor) expandPasteMarkers(text string) string {
	result := text
	for _, pasteID := range append([]int(nil), e.pasteOrder...) {
		pasteContent := e.pastes[pasteID]
		markerRegex := regexp.MustCompile(`\[paste #` + itoa(pasteID) + `( (\+\d+ lines|\d+ chars))?\]`)
		result = markerRegex.ReplaceAllString(result, regexpEscapeReplacement(pasteContent))
	}
	return result
}

func regexpEscapeReplacement(s string) string { return strings.ReplaceAll(s, "$", "$$") }

// GetExpandedText returns text with paste markers expanded.
func (e *Editor) GetExpandedText() string {
	return e.expandPasteMarkers(strings.Join(e.state.lines, "\n"))
}

// GetLines copies the raw lines.
func (e *Editor) GetLines() []string {
	return append([]string(nil), e.state.lines...)
}

// Cursor describes the cursor position.
type Cursor struct{ Line, Col int }

// GetCursor returns the cursor position.
func (e *Editor) GetCursor() Cursor {
	return Cursor{Line: e.state.cursorLine, Col: e.state.cursorCol}
}

// SetText replaces the content (undoable when differing).
func (e *Editor) SetText(text string) {
	e.cancelAutocomplete()
	e.lastAction = ""
	e.exitHistoryBrowsing()
	normalized := e.normalizeText(text)
	if e.GetText() != normalized {
		e.pushUndoSnapshot()
	}
	e.pastes = map[int]string{}
	e.pasteOrder = nil
	e.pasteCounter = 0
	e.setTextInternal(normalized, "end")
}

// InsertTextAtCursor inserts text atomically for undo.
func (e *Editor) InsertTextAtCursor(text string) {
	if text == "" {
		return
	}
	e.cancelAutocomplete()
	e.pushUndoSnapshot()
	e.lastAction = ""
	e.exitHistoryBrowsing()
	e.insertTextAtCursorInternal(text)
}

func (e *Editor) normalizeText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.ReplaceAll(text, "\t", "    ")
}

func (e *Editor) insertTextAtCursorInternal(text string) {
	if text == "" {
		return
	}

	normalized := e.normalizeText(text)
	insertedLines := strings.Split(normalized, "\n")

	currentLine := e.state.lines[e.state.cursorLine]
	beforeCursor := currentLine[:e.state.cursorCol]
	afterCursor := currentLine[e.state.cursorCol:]

	if len(insertedLines) == 1 {
		e.state.lines[e.state.cursorLine] = beforeCursor + normalized + afterCursor
		e.setCursorCol(e.state.cursorCol + len(normalized))
	} else {
		var newLines []string
		newLines = append(newLines, e.state.lines[:e.state.cursorLine]...)
		newLines = append(newLines, beforeCursor+insertedLines[0])
		newLines = append(newLines, insertedLines[1:len(insertedLines)-1]...)
		newLines = append(newLines, insertedLines[len(insertedLines)-1]+afterCursor)
		newLines = append(newLines, e.state.lines[e.state.cursorLine+1:]...)
		e.state.lines = newLines

		e.state.cursorLine += len(insertedLines) - 1
		e.setCursorCol(len(insertedLines[len(insertedLines)-1]))
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) insertCharacter(char string, skipUndoCoalescing bool) {
	e.exitHistoryBrowsing()

	if !skipUndoCoalescing {
		if len(char) > 0 && IsWhitespaceRune([]rune(char)[0]) || e.lastAction != "type-word" {
			e.pushUndoSnapshot()
		}
		e.lastAction = "type-word"
	}

	line := e.state.lines[e.state.cursorLine]

	before := line[:e.state.cursorCol]
	after := line[e.state.cursorCol:]

	e.state.lines[e.state.cursorLine] = before + char + after
	e.setCursorCol(e.state.cursorCol + len(char))

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}

	if e.autocompleteState == "" {
		currentLine := e.state.lines[e.state.cursorLine]
		textBeforeCursor := currentLine[:e.state.cursorCol]
		switch {
		case char == "/" && e.isAtStartOfMessage():
			e.tryTriggerAutocomplete(false)
		case containsString(e.autocompleteTriggerCharacters, char):
			if e.autocompleteTriggerPattern.MatchString(textBeforeCursor) {
				e.tryTriggerAutocomplete(false)
			}
		case len(char) > 0 && (editorTriggerWordChars.MatchString(char) || CjkBreakRune([]rune(char)[0])):
			if e.isInSlashCommandContext(textBeforeCursor) {
				e.tryTriggerAutocomplete(false)
			} else if e.autocompleteTriggerPattern.MatchString(textBeforeCursor) {
				e.tryTriggerAutocomplete(false)
			}
		}
	} else {
		e.updateAutocomplete()
	}
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

func (e *Editor) handlePaste(pastedText string) {
	e.cancelAutocomplete()
	e.exitHistoryBrowsing()
	e.lastAction = ""

	e.pushUndoSnapshot()

	// Terminals with extended-keys-format=csi-u re-encode control bytes
	// inside bracketed paste as CSI-u Ctrl+<letter>; decode back.
	decodedText := editorCtrlUPaste.ReplaceAllStringFunc(pastedText, func(match string) string {
		sub := editorCtrlUPaste.FindStringSubmatch(match)
		code := 0
		for _, c := range sub[1] {
			code = code*10 + int(c-'0')
		}
		if code >= 97 && code <= 122 {
			return string(rune(code - 96))
		}
		if code >= 65 && code <= 90 {
			return string(rune(code - 64))
		}
		return match
	})

	cleanText := e.normalizeText(decodedText)

	// Filter non-printable characters except newlines.
	var filteredRunes []rune
	for _, r := range cleanText {
		if r == '\n' || r >= 32 {
			filteredRunes = append(filteredRunes, r)
		}
	}
	filteredText := string(filteredRunes)

	// Prepend a space when pasting a path after a word character.
	if strings.HasPrefix(filteredText, "/") || strings.HasPrefix(filteredText, "~") || strings.HasPrefix(filteredText, ".") {
		currentLine := e.state.lines[e.state.cursorLine]
		charBeforeCursor := byte(0)
		if e.state.cursorCol > 0 {
			charBeforeCursor = currentLine[e.state.cursorCol-1]
		}
		if charBeforeCursor != 0 && isWordByte(charBeforeCursor) {
			filteredText = " " + filteredText
		}
	}

	pastedLines := strings.Split(filteredText, "\n")

	totalChars := len(filteredText)
	if len(pastedLines) > 10 || totalChars > 1000 {
		e.pasteCounter++
		pasteID := e.pasteCounter
		e.pastes[pasteID] = filteredText
		e.pasteOrder = append(e.pasteOrder, pasteID)

		var marker string
		if len(pastedLines) > 10 {
			marker = "[paste #" + itoa(pasteID) + " +" + itoa(len(pastedLines)) + " lines]"
		} else {
			marker = "[paste #" + itoa(pasteID) + " " + itoa(totalChars) + " chars]"
		}
		e.insertTextAtCursorInternal(marker)
		return
	}

	e.insertTextAtCursorInternal(filteredText)
}

func isWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func (e *Editor) addNewLine() {
	e.cancelAutocomplete()
	e.exitHistoryBrowsing()
	e.lastAction = ""

	e.pushUndoSnapshot()

	currentLine := e.state.lines[e.state.cursorLine]
	before := currentLine[:e.state.cursorCol]
	after := currentLine[e.state.cursorCol:]

	e.state.lines[e.state.cursorLine] = before
	newLines := append([]string(nil), e.state.lines[:e.state.cursorLine+1]...)
	newLines = append(newLines, after)
	newLines = append(newLines, e.state.lines[e.state.cursorLine+1:]...)
	e.state.lines = newLines

	e.state.cursorLine++
	e.setCursorCol(0)

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) shouldSubmitOnBackslashEnter(data string) bool {
	if e.DisableSubmit {
		return false
	}
	if !MatchesKey(data, "enter") {
		return false
	}
	submitKeys := GetKeybindings().GetKeys("tui.input.submit")
	hasShiftEnter := containsString(submitKeys, "shift+enter") || containsString(submitKeys, "shift+return")
	if !hasShiftEnter {
		return false
	}

	currentLine := e.state.lines[e.state.cursorLine]
	return e.state.cursorCol > 0 && currentLine[e.state.cursorCol-1] == '\\'
}

func (e *Editor) submitValue() {
	e.cancelAutocomplete()
	result := strings.TrimSpace(e.expandPasteMarkers(strings.Join(e.state.lines, "\n")))

	e.state = editorState{lines: []string{""}}
	e.pastes = map[int]string{}
	e.pasteOrder = nil
	e.pasteCounter = 0
	e.exitHistoryBrowsing()
	e.scrollOffset = 0
	e.undoStack.Clear()
	e.lastAction = ""

	if e.OnChange != nil {
		e.OnChange("")
	}
	if e.OnSubmit != nil {
		e.OnSubmit(result)
	}
}

func (e *Editor) setCursorCol(col int) {
	e.state.cursorCol = col
	e.preferredVisualCol = nil
	e.snappedFromCursorCol = nil
}

func (e *Editor) pushUndoSnapshot() {
	e.undoStack.Push(editorSnapshot{
		state:        e.cloneState(),
		pastes:       e.pastes,
		pasteOrder:   append([]int(nil), e.pasteOrder...),
		pasteCounter: e.pasteCounter,
	})
}

func (e *Editor) undo() {
	e.exitHistoryBrowsing()
	snapshot, ok := e.undoStack.Pop()
	if !ok {
		return
	}
	e.state = snapshot.state
	e.pastes = snapshot.pastes
	e.pasteOrder = snapshot.pasteOrder
	e.pasteCounter = snapshot.pasteCounter
	e.lastAction = ""
	e.preferredVisualCol = nil
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

// isSlashMenuAllowed gates slash menus to the first logical line.
func (e *Editor) isSlashMenuAllowed() bool {
	return e.state.cursorLine == 0
}

func (e *Editor) isAtStartOfMessage() bool {
	if !e.isSlashMenuAllowed() {
		return false
	}
	currentLine := e.state.lines[e.state.cursorLine]
	beforeCursor := strings.TrimSpace(currentLine[:e.state.cursorCol])
	return beforeCursor == "" || beforeCursor == "/"
}

func (e *Editor) isInSlashCommandContext(textBeforeCursor string) bool {
	return e.isSlashMenuAllowed() && strings.HasPrefix(strings.TrimLeft(textBeforeCursor, " \t"), "/")
}

func (e *Editor) getBestAutocompleteMatchIndex(items []AutocompleteItem, prefix string) int {
	if prefix == "" {
		return -1
	}
	firstPrefixIndex := -1
	for i, item := range items {
		if item.Value == prefix {
			return i
		}
		if firstPrefixIndex == -1 && strings.HasPrefix(item.Value, prefix) {
			firstPrefixIndex = i
		}
	}
	return firstPrefixIndex
}

func (e *Editor) createAutocompleteList(prefix string, items []AutocompleteItem) *SelectList {
	var layout SelectListLayoutOptions
	if strings.HasPrefix(prefix, "/") {
		minWidth, maxWidth := 12, 32
		layout = SelectListLayoutOptions{MinPrimaryColumnWidth: &minWidth, MaxPrimaryColumnWidth: &maxWidth}
	}
	selectItems := make([]SelectItem, len(items))
	for i, item := range items {
		selectItems[i] = SelectItem{Value: item.Value, Label: item.Label, Description: item.Description}
	}
	list := NewSelectList(selectItems, e.autocompleteMaxVisible, e.theme.SelectList, layout)
	list.OnSelect = func(selected SelectItem) {
		if e.autocompleteProvider == nil {
			return
		}
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
	return list
}

func (e *Editor) tryTriggerAutocomplete(explicitTab bool) {
	e.requestAutocomplete(false, explicitTab)
}

func (e *Editor) handleTabCompletion() {
	if e.autocompleteProvider == nil {
		return
	}

	currentLine := e.state.lines[e.state.cursorLine]
	beforeCursor := currentLine[:e.state.cursorCol]

	if e.isInSlashCommandContext(beforeCursor) && !strings.Contains(strings.TrimLeft(beforeCursor, " \t"), " ") {
		e.handleSlashCommandCompletion()
	} else {
		e.forceFileAutocomplete(true)
	}
}

func (e *Editor) handleSlashCommandCompletion() {
	e.requestAutocomplete(false, true)
}

func (e *Editor) forceFileAutocomplete(explicitTab bool) {
	e.requestAutocomplete(true, explicitTab)
}

func (e *Editor) requestAutocomplete(force, explicitTab bool) {
	if e.autocompleteProvider == nil {
		return
	}

	if force {
		if !e.autocompleteProvider.ShouldTriggerFileCompletion(e.state.lines, e.state.cursorLine, e.state.cursorCol) {
			return
		}
	}

	e.cancelAutocompleteRequest()
	e.autocompleteStartToken++
	startToken := e.autocompleteStartToken

	debounceMs := e.getAutocompleteDebounceMs(force, explicitTab)
	if debounceMs > 0 {
		e.autocompleteDebounceTimer = e.After(time.Duration(debounceMs)*time.Millisecond, func() {
			e.autocompleteDebounceTimer = nil
			e.startAutocompleteRequest(startToken, force, explicitTab)
		})
		return
	}

	e.startAutocompleteRequest(startToken, force, explicitTab)
}

func (e *Editor) startAutocompleteRequest(startToken int, force, explicitTab bool) {
	if startToken != e.autocompleteStartToken || e.autocompleteProvider == nil {
		return
	}

	controller := abort.NewController()
	e.autocompleteAbort = controller
	e.autocompleteRequestId++
	requestID := e.autocompleteRequestId
	snapshotText := e.GetText()
	snapshotLine := e.state.cursorLine
	snapshotCol := e.state.cursorCol

	e.runAutocompleteRequest(requestID, controller, snapshotText, snapshotLine, snapshotCol, force, explicitTab)
}

func (e *Editor) setAutocompleteTriggerCharacters(triggerCharacters string) {
	next := append([]string(nil), defaultAutocompleteTriggerCharacters...)
	for _, character := range triggerCharacters {
		char := string(character)
		if len(char) != 1 || char == "/" || IsWhitespaceRune(character) || containsString(next, char) {
			continue
		}
		next = append(next, char)
	}
	e.autocompleteTriggerCharacters = next
	e.autocompleteTriggerPattern = buildTriggerPattern(next)
	e.autocompleteDebouncePattern = buildDebouncePattern(next)
}

func (e *Editor) getAutocompleteDebounceMs(force, explicitTab bool) int {
	if explicitTab || force {
		return 0
	}

	currentLine := e.state.lines[e.state.cursorLine]
	textBeforeCursor := currentLine[:e.state.cursorCol]
	if e.autocompleteDebouncePattern.MatchString(textBeforeCursor) {
		return attachmentAutocompleteDebounceMs
	}
	return 0
}

func (e *Editor) runAutocompleteRequest(requestID int, controller *abort.Controller, snapshotText string, snapshotLine, snapshotCol int, force, explicitTab bool) {
	if e.autocompleteProvider == nil {
		return
	}

	suggestions := e.autocompleteProvider.GetSuggestions(
		e.state.lines, e.state.cursorLine, e.state.cursorCol,
		AutocompleteQueryOptions{Signal: controller.Signal(), Force: force},
	)

	if !e.isAutocompleteRequestCurrent(requestID, controller, snapshotText, snapshotLine, snapshotCol) {
		return
	}

	e.autocompleteAbort = nil

	if suggestions == nil || len(suggestions.Items) == 0 {
		e.cancelAutocomplete()
		e.tui.RequestRender(false)
		return
	}

	if force && explicitTab && len(suggestions.Items) == 1 {
		item := suggestions.Items[0]
		e.pushUndoSnapshot()
		e.lastAction = ""
		result := e.autocompleteProvider.ApplyCompletion(
			e.state.lines, e.state.cursorLine, e.state.cursorCol, item, suggestions.Prefix,
		)
		e.state.lines = result.Lines
		e.state.cursorLine = result.CursorLine
		e.setCursorCol(result.CursorCol)
		if e.OnChange != nil {
			e.OnChange(e.GetText())
		}
		e.tui.RequestRender(false)
		return
	}

	state := "regular"
	if force {
		state = "force"
	}
	e.applyAutocompleteSuggestions(suggestions, state)
	e.tui.RequestRender(false)
}

func (e *Editor) isAutocompleteRequestCurrent(requestID int, controller *abort.Controller, snapshotText string, snapshotLine, snapshotCol int) bool {
	return !controller.Signal().Aborted() &&
		requestID == e.autocompleteRequestId &&
		e.GetText() == snapshotText &&
		e.state.cursorLine == snapshotLine &&
		e.state.cursorCol == snapshotCol
}

func (e *Editor) applyAutocompleteSuggestions(suggestions *AutocompleteSuggestions, state string) {
	e.autocompletePrefix = suggestions.Prefix
	e.autocompleteList = e.createAutocompleteList(suggestions.Prefix, suggestions.Items)

	bestMatchIndex := e.getBestAutocompleteMatchIndex(suggestions.Items, suggestions.Prefix)
	if bestMatchIndex >= 0 {
		e.autocompleteList.SetSelectedIndex(bestMatchIndex)
	}

	e.autocompleteState = state
}

func (e *Editor) cancelAutocompleteRequest() {
	e.autocompleteStartToken++
	if e.autocompleteDebounceTimer != nil {
		e.autocompleteDebounceTimer.Stop()
		e.autocompleteDebounceTimer = nil
	}
	if e.autocompleteAbort != nil {
		e.autocompleteAbort.Abort()
		e.autocompleteAbort = nil
	}
}

func (e *Editor) cancelAutocomplete() {
	e.cancelAutocompleteRequest()
	e.autocompleteState = ""
	e.autocompleteList = nil
	e.autocompletePrefix = ""
}

// IsShowingAutocomplete reports whether the picker is visible.
func (e *Editor) IsShowingAutocomplete() bool {
	return e.autocompleteState != ""
}

func (e *Editor) updateAutocomplete() {
	if e.autocompleteState == "" || e.autocompleteProvider == nil {
		return
	}
	e.requestAutocomplete(e.autocompleteState == "force", false)
}

// sortPasteIDs returns paste IDs ascending.
func (e *Editor) sortPasteIDs() []int {
	ids := append([]int(nil), e.pasteOrder...)
	sort.Ints(ids)
	return ids
}
