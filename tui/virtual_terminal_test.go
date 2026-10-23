package tui

// VirtualTerminal: a test-only terminal emulator covering the escape subset
// TuiMainScreen emits (CR/LF, CUU/CUD/CHC, EL/ED, E3, cursor visibility,
// sync-output markers, OSC/APC passthrough) with viewport + scrollback
// semantics, replacing the reference's @xterm/headless fixture.

import (
	"strings"
	"sync"
	"time"
)

type vtCell struct {
	ch    rune
	width int
}

type virtualTerminal struct {
	mu sync.Mutex

	inputHandler  func(data string)
	resizeHandler func()

	cols, rows int
	// grid holds all lines including scrollback; viewport top = len(grid)-rows.
	grid          [][]vtCell
	cursorX       int
	cursorAbsY    int // absolute row index into grid
	cursorVisible bool

	writes []string
}

func newVirtualTerminal(cols, rows int) *virtualTerminal {
	vt := &virtualTerminal{cols: cols, rows: rows, cursorVisible: true}
	vt.reset()
	return vt
}

func (vt *virtualTerminal) reset() {
	vt.grid = make([][]vtCell, vt.rows)
	for i := range vt.grid {
		vt.grid[i] = make([]vtCell, vt.cols)
	}
	vt.cursorX = 0
	vt.cursorAbsY = 0
}

func (vt *virtualTerminal) Start(onInput func(string), onResize func()) {
	vt.mu.Lock()
	vt.inputHandler = onInput
	vt.resizeHandler = onResize
	vt.mu.Unlock()
}

func (vt *virtualTerminal) Stop() {
	vt.mu.Lock()
	vt.inputHandler = nil
	vt.resizeHandler = nil
	vt.mu.Unlock()
}

func (vt *virtualTerminal) DrainInput(maxMs, idleMs time.Duration) {}

func (vt *virtualTerminal) Write(data string) {
	vt.mu.Lock()
	defer vt.mu.Unlock()
	vt.writes = append(vt.writes, data)
	vt.feed(data)
}

func (vt *virtualTerminal) Columns() int { return vt.cols }
func (vt *virtualTerminal) Rows() int    { return vt.rows }

func (vt *virtualTerminal) KittyProtocolActive() bool { return true }

func (vt *virtualTerminal) MoveBy(lines int) {
	vt.mu.Lock()
	defer vt.mu.Unlock()
	if lines > 0 {
		vt.moveCursorDown(lines)
	} else if lines < 0 {
		vt.moveCursorUp(-lines)
	}
}

func (vt *virtualTerminal) HideCursor() {
	vt.mu.Lock()
	vt.cursorVisible = false
	vt.mu.Unlock()
}

func (vt *virtualTerminal) ShowCursor() {
	vt.mu.Lock()
	vt.cursorVisible = true
	vt.mu.Unlock()
}

func (vt *virtualTerminal) ClearLine()       {}
func (vt *virtualTerminal) ClearFromCursor() {}
func (vt *virtualTerminal) ClearScreen()     {}

func (vt *virtualTerminal) SetTitle(title string)   {}
func (vt *virtualTerminal) SetProgress(active bool) {}

// SendInput simulates keyboard input.
func (vt *virtualTerminal) SendInput(data string) {
	vt.mu.Lock()
	handler := vt.inputHandler
	vt.mu.Unlock()
	if handler != nil {
		handler(data)
	}
}

// Resize changes dimensions and fires the resize handler.
func (vt *virtualTerminal) Resize(cols, rows int) {
	vt.mu.Lock()
	vt.cols = cols
	vt.rows = rows
	for len(vt.grid) < rows {
		vt.grid = append(vt.grid, make([]vtCell, cols))
	}
	vt.clampCursor()
	vt.mu.Unlock()
	if vt.resizeHandler != nil {
		vt.resizeHandler()
	}
}

// GetViewport returns the visible rows with trailing spaces trimmed
// (translateToString(true) semantics).
func (vt *virtualTerminal) GetViewport() []string {
	vt.mu.Lock()
	defer vt.mu.Unlock()
	return vt.viewportLocked()
}

func (vt *virtualTerminal) viewportLocked() []string {
	lines := make([]string, vt.rows)
	top := len(vt.grid) - vt.rows
	if top < 0 {
		top = 0
	}
	for i := 0; i < vt.rows; i++ {
		row := top + i
		if row < 0 || row >= len(vt.grid) {
			lines[i] = ""
			continue
		}
		lines[i] = vt.lineString(vt.grid[row])
	}
	return lines
}

// GetScrollBuffer returns all lines including scrollback.
func (vt *virtualTerminal) GetScrollBuffer() []string {
	vt.mu.Lock()
	defer vt.mu.Unlock()
	lines := make([]string, len(vt.grid))
	for i, row := range vt.grid {
		lines[i] = vt.lineString(row)
	}
	return lines
}

// GetCursorPosition returns the cursor position relative to the viewport.
func (vt *virtualTerminal) GetCursorPosition() (x, y int) {
	vt.mu.Lock()
	defer vt.mu.Unlock()
	top := len(vt.grid) - vt.rows
	if top < 0 {
		top = 0
	}
	return vt.cursorX, vt.cursorAbsY - top
}

// Written returns the raw concatenated writes.
func (vt *virtualTerminal) Written() string {
	vt.mu.Lock()
	defer vt.mu.Unlock()
	return strings.Join(vt.writes, "")
}

// writesCount reports the number of writes (tests diff counts across
// renders while the writer goroutine may be active).
func (vt *virtualTerminal) writesCount() int {
	vt.mu.Lock()
	defer vt.mu.Unlock()
	return len(vt.writes)
}

func (vt *virtualTerminal) lineString(row []vtCell) string {
	var b strings.Builder
	for _, cell := range row {
		if cell.ch == 0 {
			b.WriteRune(' ')
		} else {
			b.WriteRune(cell.ch)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func (vt *virtualTerminal) clampCursor() {
	if vt.cursorAbsY < 0 {
		vt.cursorAbsY = 0
	}
	if vt.cursorAbsY >= len(vt.grid) {
		vt.cursorAbsY = len(vt.grid) - 1
	}
	if vt.cursorX < 0 {
		vt.cursorX = 0
	}
	if vt.cursorX >= vt.cols {
		vt.cursorX = vt.cols - 1
	}
}

func (vt *virtualTerminal) scrollUp() {
	vt.grid = append(vt.grid, make([]vtCell, vt.cols))
	vt.cursorAbsY++
}

func (vt *virtualTerminal) moveCursorUp(n int) {
	top := len(vt.grid) - vt.rows
	if vt.cursorAbsY-n < top {
		vt.cursorAbsY = top
	} else {
		vt.cursorAbsY -= n
	}
	vt.clampCursor()
}

func (vt *virtualTerminal) moveCursorDown(n int) {
	if vt.cursorAbsY+n >= len(vt.grid) {
		scroll := vt.cursorAbsY + n - (len(vt.grid) - 1)
		for i := 0; i < scroll; i++ {
			vt.scrollUp()
		}
		vt.cursorAbsY = len(vt.grid) - 1
	} else {
		vt.cursorAbsY += n
	}
}

func (vt *virtualTerminal) putRune(r rune) {
	if vt.cursorX >= vt.cols {
		// Auto-wrap: newline then place.
		vt.cursorX = 0
		vt.linefeed()
	}
	for vt.cursorAbsY >= len(vt.grid) {
		vt.scrollUp()
	}
	for len(vt.grid[vt.cursorAbsY]) <= vt.cursorX {
		vt.grid[vt.cursorAbsY] = append(vt.grid[vt.cursorAbsY], vtCell{})
	}
	vt.grid[vt.cursorAbsY][vt.cursorX] = vtCell{ch: r, width: 1}
	vt.cursorX++
}

func (vt *virtualTerminal) linefeed() {
	if vt.cursorAbsY == len(vt.grid)-1 {
		vt.scrollUp()
	} else {
		vt.cursorAbsY++
	}
}

func (vt *virtualTerminal) eraseLine(mode byte) {
	if vt.cursorAbsY < 0 || vt.cursorAbsY >= len(vt.grid) {
		return
	}
	row := vt.grid[vt.cursorAbsY]
	switch mode {
	case 0: // cursor to end
		for x := vt.cursorX; x < len(row); x++ {
			row[x] = vtCell{}
		}
	case 1: // start to cursor
		for x := 0; x <= vt.cursorX && x < len(row); x++ {
			row[x] = vtCell{}
		}
	case 2: // entire line
		for x := range row {
			row[x] = vtCell{}
		}
	}
}

func (vt *virtualTerminal) eraseDisplay(mode byte) {
	switch mode {
	case 2: // entire screen, cursor home
		for i := range vt.grid {
			for x := range vt.grid[i] {
				vt.grid[i][x] = vtCell{}
			}
		}
		vt.cursorX = 0
		vt.cursorAbsY = 0
	case 3: // scrollback only
		top := len(vt.grid) - vt.rows
		if top > 0 {
			vt.grid = append([][]vtCell(nil), vt.grid[top:]...)
			vt.cursorAbsY -= top
			vt.clampCursor()
		}
	}
}

// feed interprets the emitted escape subset.
func (vt *virtualTerminal) feed(data string) {
	runes := []rune(data)
	i := 0
	for i < len(runes) {
		r := runes[i]
		if r != 0x1b {
			switch r {
			case '\r':
				vt.cursorX = 0
			case '\n':
				vt.linefeed()
			default:
				vt.putRune(r)
			}
			i++
			continue
		}

		// Escape sequence
		if i+1 >= len(runes) {
			return
		}
		switch runes[i+1] {
		case '[':
			// CSI: parse params until final byte.
			j := i + 2
			params := ""
			for j < len(runes) && runes[j] >= 0x30 && runes[j] <= 0x3f {
				params += string(runes[j])
				j++
			}
			for j < len(runes) && runes[j] >= 0x20 && runes[j] <= 0x2f {
				j++
			}
			if j >= len(runes) {
				return
			}
			final := runes[j]
			vt.applyCSI(params, byte(final))
			i = j + 1
		case ']':
			// OSC: until BEL or ST.
			j := i + 2
			for j < len(runes) {
				if runes[j] == 0x07 {
					j++
					break
				}
				if runes[j] == 0x1b && j+1 < len(runes) && runes[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
		case '_':
			// APC: until BEL or ST (zero-width, ignored).
			j := i + 2
			for j < len(runes) {
				if runes[j] == 0x07 {
					j++
					break
				}
				if runes[j] == 0x1b && j+1 < len(runes) && runes[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
		default:
			// Two-char escape: consume.
			i += 2
		}
	}
}

func (vt *virtualTerminal) applyCSI(params string, final byte) {
	number := func(index, fallback int) int {
		parts := strings.Split(params, ";")
		if index >= len(parts) || parts[index] == "" {
			return fallback
		}
		value := 0
		for _, c := range parts[index] {
			if c < '0' || c > '9' {
				return fallback
			}
			value = value*10 + int(c-'0')
		}
		return value
	}

	private := strings.ContainsAny(params, "?<>=")

	switch final {
	case 'A':
		vt.moveCursorUp(maxInt(1, number(0, 1)))
	case 'B':
		vt.moveCursorDown(maxInt(1, number(0, 1)))
	case 'G':
		col := number(0, 1)
		vt.cursorX = maxInt(0, col-1)
		vt.clampCursor()
	case 'J':
		if !private {
			vt.eraseDisplay(byte(number(0, 0)))
		}
	case 'K':
		vt.eraseLine(byte(number(0, 0)))
	case 'H':
		if !private {
			row := number(0, 1)
			col := number(1, 1)
			vt.cursorAbsY = len(vt.grid) - vt.rows + row - 1
			vt.cursorX = col - 1
			vt.clampCursor()
		}
	default:
		// Unknown/unhandled (2026 sync, 25 cursor, 2004 paste, SGR styles):
		// ignore.
	}
}

// SetProgramStatus implements Terminal; the virtual terminal ignores it.
func (t *virtualTerminal) SetProgramStatus(status ProgramStatus) {}
