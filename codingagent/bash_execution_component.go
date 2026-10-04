package codingagent

// Port of modes/interactive/components/bash-execution.ts: the streaming
// bash execution block with dynamic borders, running loader, preview/
// expanded states, and context + visual truncation.

import (
	"regexp"
	"strings"

	"github.com/gladmo/openagent/tui"
)

// previewLines bounds the collapsed view (matches tool execution).
const bashPreviewLines = 20

var ansiStripPattern = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]|\x1b\\][^\x07]*\x07|\x1b\\[[0-9;]*m")

// StripAnsiCodes removes ANSI escapes (CSI/OSC/SGR).
func StripAnsiCodes(text string) string {
	return ansiStripPattern.ReplaceAllString(text, "")
}

// BashExecutionComponent displays a bash command with streaming output.
type BashExecutionComponent struct {
	*tui.VStack

	command        string
	excludeFromCtx bool
	outputLines    []string
	status         string // running | complete | cancelled | error
	exitCode       *int
	truncatedLines int
	truncatedBytes int
	fullOutputPath string
	expanded       bool
	contentStack   *tui.VStack
	loader         *tui.Loader
	loaderText     string
}

// NewBashExecutionComponent builds the bordered block.
func NewBashExecutionComponent(command string, ui tui.TUI, excludeFromContext bool) *BashExecutionComponent {
	component := &BashExecutionComponent{
		VStack:         tui.NewVStack(0),
		command:        command,
		excludeFromCtx: excludeFromContext,
		status:         "running",
		contentStack:   tui.NewVStack(0),
	}
	if ui != nil {
		component.loader = tui.NewLoader(ui, nil, nil, "Running...", nil)
	} else {
		// A nil UI (tests, headless) skips the animated loader; the
		// static text carries the same label.
		component.loaderText = "Running..."
	}

	component.VStack.AddChild(tui.NewSpacer(1), nil)
	component.VStack.AddChild(component.contentStack, nil)
	component.rebuild()
	return component
}

// SetExpanded toggles the full-output view.
func (c *BashExecutionComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
	c.rebuild()
}

// Expanded reports the state (tests).
func (c *BashExecutionComponent) Expanded() bool { return c.expanded }

// AppendOutput streams one chunk (ANSI stripped, line endings
// normalized, incomplete lines joined).
func (c *BashExecutionComponent) AppendOutput(chunk string) {
	clean := strings.ReplaceAll(chunk, "\r\n", "\n")
	clean = strings.ReplaceAll(clean, "\r", "\n")
	clean = StripAnsiCodes(clean)

	newLines := strings.Split(clean, "\n")
	if len(c.outputLines) > 0 && len(newLines) > 0 {
		c.outputLines[len(c.outputLines)-1] += newLines[0]
		c.outputLines = append(c.outputLines, newLines[1:]...)
	} else {
		c.outputLines = append(c.outputLines, newLines...)
	}
	c.rebuild()
}

// SetComplete finishes the block (status from exit code/cancellation).
func (c *BashExecutionComponent) SetComplete(exitCode *int, cancelled bool, truncatedLines, truncatedBytes int, fullOutputPath string) {
	c.exitCode = exitCode
	c.status = "complete"
	if cancelled {
		c.status = "cancelled"
	} else if exitCode != nil && *exitCode != 0 {
		c.status = "error"
	}
	c.truncatedLines = truncatedLines
	c.truncatedBytes = truncatedBytes
	c.fullOutputPath = fullOutputPath
	if c.loader != nil {
		c.loader.Stop()
	}
	c.rebuild()
}

// Status reports the completion state (tests).
func (c *BashExecutionComponent) Status() string { return c.status }

// ExitCode reports the recorded exit code (tests).
func (c *BashExecutionComponent) ExitCode() *int { return c.exitCode }

func (c *BashExecutionComponent) rebuild() {
	c.contentStack = tui.NewVStack(0)
	children := c.VStack
	for i := len(children.Entries) - 1; i >= 0; i-- {
		children.Entries = children.Entries[:i]
	}
	children.AddChild(tui.NewSpacer(1), nil)
	children.AddChild(c.contentStack, nil)

	// Command header.
	c.contentStack.AddChild(tui.NewText("$ "+c.command, 1, 0, nil), nil)

	availableLines := c.availableLines()

	if c.status == "running" {
		if c.loader != nil {
			c.contentStack.AddChild(c.loader, nil)
		} else if c.loaderText != "" {
			c.contentStack.AddChild(tui.NewText(c.loaderText, 1, 0, nil), nil)
		}
	}

	if len(availableLines) > 0 {
		if c.expanded {
			display := "\n" + strings.Join(availableLines, "\n")
			c.contentStack.AddChild(tui.NewText(display, 1, 0, nil), nil)
		} else {
			// Preview: the last previewLines logical lines.
			start := len(availableLines) - bashPreviewLines
			if start < 0 {
				start = 0
			}
			preview := availableLines[start:]
			if len(preview) > 0 {
				display := "\n" + strings.Join(preview, "\n")
				c.contentStack.AddChild(tui.NewText(display, 1, 0, nil), nil)
			}
			if hidden := len(availableLines) - len(preview); hidden > 0 {
				hint := "\n... " + itoaBash(hidden) + " earlier lines hidden (ctrl+o to expand)"
				c.contentStack.AddChild(tui.NewText(hint, 1, 0, nil), nil)
			}
		}
	}

	// Completion footer.
	switch c.status {
	case "complete", "error", "cancelled":
		footer := "\n"
		switch c.status {
		case "complete":
			footer += "Completed"
		case "error":
			if c.exitCode != nil {
				footer += "Exited with code " + itoaBash(*c.exitCode)
			} else {
				footer += "Failed"
			}
		case "cancelled":
			footer += "Cancelled (esc)"
		}
		c.contentStack.AddChild(tui.NewText(footer, 1, 0, nil), nil)
		if c.fullOutputPath != "" {
			note := "\nFull output: " + c.fullOutputPath
			c.contentStack.AddChild(tui.NewText(note, 1, 0, nil), nil)
		}
	}
}

// availableLines returns the context-truncated output lines.
func (c *BashExecutionComponent) availableLines() []string {
	fullOutput := strings.Join(c.outputLines, "\n")
	if fullOutput == "" {
		return nil
	}
	// Context truncation rides the shared tail truncation defaults.
	result := TruncateTail(fullOutput, TruncationOptions{})
	if result.Content == "" {
		return nil
	}
	return strings.Split(result.Content, "\n")
}

func itoaBash(value int) string {
	digits := "0123456789"
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var out []byte
	for value > 0 {
		out = append([]byte{digits[value%10]}, out...)
		value /= 10
	}
	if negative {
		return "-" + string(out)
	}
	return string(out)
}
