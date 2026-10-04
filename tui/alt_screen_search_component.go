package tui

// Port of the AltScreenSearchComponent from alt-screen-search.ts: the
// boxed search overlay with result counter and navigation buttons. The TS
// version embeds the Input component; until Input lands (components batch),
// this carries a minimal single-line editor state with the same visible
// behavior (plain typing, backspace, placeholder). The full Input port
// replaces the internals without changing this component's contract.

import "strings"

// AltScreenSearchComponent renders the transcript search overlay.
type AltScreenSearchComponent struct {
	// Input is the embedded single-line editor seam; non-nil overrides the
	// minimal built-in editor (the Input component sets this).
	Input interface {
		GetValue() string
		HandleInput(data string)
		Render(width int) []string
		SetFocused(focused bool)
		Invalidate()
	}

	value                 string
	prompt                string
	placeholder           string
	onQueryChange         func(query string)
	navigationButtonStyle func(text string, hovered bool) string

	resultCount                int
	resultIndex                int
	previousButtonStart        int
	previousButtonEnd          int
	nextButtonStart            int
	nextButtonEnd              int
	hoveredNavigationDirection int // -1 | 0 | 1; 0 = none
	focusedFlag                bool
}

// NewAltScreenSearchComponent builds the search overlay component.
func NewAltScreenSearchComponent(onQueryChange func(string), navigationButtonStyle func(text string, hovered bool) string) *AltScreenSearchComponent {
	if navigationButtonStyle == nil {
		navigationButtonStyle = func(text string, hovered bool) string { return text }
	}
	return &AltScreenSearchComponent{
		prompt:                     " ",
		placeholder:                "Find in transcript",
		onQueryChange:              onQueryChange,
		navigationButtonStyle:      navigationButtonStyle,
		hoveredNavigationDirection: 0,
		resultIndex:                -1,
	}
}

// IsFocused implements Focusable.
func (c *AltScreenSearchComponent) IsFocused() bool { return c.focusedFlag }

// SetFocused implements Focusable; forwards to the embedded input.
func (c *AltScreenSearchComponent) SetFocused(focused bool) {
	c.focusedFlag = focused
	if c.Input != nil {
		c.Input.SetFocused(focused)
	}
}

// SetResult updates the result counter display.
func (c *AltScreenSearchComponent) SetResult(index, count int) {
	c.resultIndex = index
	c.resultCount = count
}

func (c *AltScreenSearchComponent) getValue() string {
	if c.Input != nil {
		return c.Input.GetValue()
	}
	return c.value
}

// GetNavigationDirectionAt reports -1/1 when (row=2, column) hits a button.
func (c *AltScreenSearchComponent) GetNavigationDirectionAt(row, column int) int {
	if row != 2 {
		return 0
	}
	if column >= c.previousButtonStart && column < c.previousButtonEnd {
		return -1
	}
	if column >= c.nextButtonStart && column < c.nextButtonEnd {
		return 1
	}
	return 0
}

// SetHoveredNavigationDirection updates hover state; true when it changed.
func (c *AltScreenSearchComponent) SetHoveredNavigationDirection(direction int) bool {
	if direction == c.hoveredNavigationDirection {
		return false
	}
	c.hoveredNavigationDirection = direction
	return true
}

// HandleInput edits the query through the input seam.
func (c *AltScreenSearchComponent) HandleInput(data string) {
	previous := c.getValue()
	if c.Input != nil {
		c.Input.HandleInput(data)
	} else {
		// Minimal single-line editing: printable runes append, backspace
		// removes the last grapheme.
		runes := []rune(c.value)
		if len(data) == 1 && data[0] == 0x7f || data == "\b" {
			if len(runes) > 0 {
				c.value = string(runes[:len(runes)-1])
			}
		} else if len(data) > 0 && data[0] != 0x1b && !strings.ContainsRune(data, '\r') && !strings.ContainsRune(data, '\n') && !strings.ContainsRune(data, '\t') {
			c.value += data
		}
	}
	query := c.getValue()
	if query != previous && c.onQueryChange != nil {
		c.onQueryChange(query)
	}
}

// Invalidate drops cached state.
func (c *AltScreenSearchComponent) Invalidate() {
	if c.Input != nil {
		c.Input.Invalidate()
	}
}

func (c *AltScreenSearchComponent) formatKey(key string) string {
	if key == "" {
		return "Unbound"
	}
	parts := strings.Split(key, "+")
	for i, part := range parts {
		if runtimeGOOS() == "darwin" && strings.ToLower(part) == "alt" {
			parts[i] = "Option"
			continue
		}
		if part != "" {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "+")
}

// Render draws the boxed search UI at the width.
func (c *AltScreenSearchComponent) Render(width int) []string {
	safeWidth := maxInt(1, width)
	innerWidth := maxInt(0, safeWidth-2)

	keybindings := GetKeybindings()
	previousKeys := keybindings.GetKeys("tui.altScreen.searchPrevious")
	nextKeys := keybindings.GetKeys("tui.altScreen.searchNext")
	previousKey := "Unbound"
	if len(previousKeys) > 0 {
		previousKey = c.formatKey(previousKeys[0])
	}
	nextKey := "Unbound"
	if len(nextKeys) > 0 {
		nextKey = c.formatKey(nextKeys[0])
	}

	query := c.getValue()
	result := ""
	if query != "" {
		if c.resultCount == 0 {
			result = "No matches"
		} else {
			result = itoa(c.resultIndex+1) + "/" + itoa(c.resultCount)
		}
	}
	resultSpace := maxInt(0, innerWidth-3)
	visibleResult := TruncateToWidth(result, resultSpace, "", false)
	resultText := ""
	if visibleResult != "" {
		resultText = "\x1b[2m " + visibleResult + " \x1b[22m"
	}
	inputWidth := maxInt(0, innerWidth-VisibleWidth(resultText))

	inputLine := ""
	if c.Input != nil {
		rendered := c.Input.Render(maxInt(1, inputWidth))
		if len(rendered) > 0 {
			inputLine = rendered[0]
		}
	} else {
		if c.value == "" {
			placeholder := "\x1b[2m" + c.placeholder + "\x1b[22m"
			inputLine = c.prompt + placeholder
		} else {
			inputLine = c.prompt + c.value
		}
	}
	inputLine = TruncateToWidth(inputLine, inputWidth, "", false)
	inputPadding := stringRepeat(" ", maxInt(0, inputWidth-VisibleWidth(inputLine)))
	content := inputLine + inputPadding + resultText

	previousButton := "↑ " + previousKey
	nextButton := "↓ " + nextKey
	separator := " · "
	outerGapWidth := 1
	availableControlsWidth := maxInt(0, innerWidth-outerGapWidth*2-1)
	controlsWidth := VisibleWidth(previousButton) + VisibleWidth(separator) + VisibleWidth(nextButton)
	if controlsWidth > availableControlsWidth {
		previousButton = "↑"
		nextButton = "↓"
		separator = " "
		controlsWidth = VisibleWidth(previousButton) + VisibleWidth(separator) + VisibleWidth(nextButton)
	}
	showButtons := controlsWidth <= availableControlsWidth
	renderedButtons := ""
	if showButtons {
		prev := c.navigationButtonStyle(previousButton, c.hoveredNavigationDirection == -1)
		next := c.navigationButtonStyle(nextButton, c.hoveredNavigationDirection == 1)
		renderedButtons = prev + separator + next
	}
	outerGapsWidth := 0
	if showButtons {
		outerGapsWidth = outerGapWidth * 2
	}
	rightRuleWidth := 0
	if renderedButtons != "" && innerWidth > controlsWidth+outerGapsWidth {
		rightRuleWidth = 1
	}
	leftRuleWidth := maxInt(0, innerWidth-controlsWidth-outerGapsWidth-rightRuleWidth)
	if !showButtons {
		leftRuleWidth = maxInt(0, innerWidth-rightRuleWidth)
	}
	previousStart := 1 + leftRuleWidth + outerGapWidth
	if showButtons {
		c.previousButtonStart = previousStart
		c.previousButtonEnd = previousStart + VisibleWidth(previousButton)
		c.nextButtonStart = c.previousButtonEnd + VisibleWidth(separator)
		c.nextButtonEnd = c.nextButtonStart + VisibleWidth(nextButton)
	} else {
		c.previousButtonStart, c.previousButtonEnd = -1, -1
		c.nextButtonStart, c.nextButtonEnd = -1, -1
	}

	if safeWidth == 1 {
		return []string{"┌", "│", "└"}
	}
	emptyPad := ""
	if showButtons {
		emptyPad = " "
	}
	return []string{
		"┌" + stringRepeat("─", innerWidth) + "┐",
		"│" + content + "│",
		"└" + stringRepeat("─", leftRuleWidth) + emptyPad + renderedButtons + emptyPad + stringRepeat("─", rightRuleWidth) + "┘",
	}
}
