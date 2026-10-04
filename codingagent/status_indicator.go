package codingagent

// Port of modes/interactive/components/status-indicator.ts: the loader-based
// status indicators rendered inside the editor's top border.

import (
	"github.com/gladmo/openagent/tui"
)

// StatusIndicatorKind values.
const (
	StatusIndicatorWorking       = "working"
	StatusIndicatorRetry         = "retry"
	StatusIndicatorCompaction    = "compaction"
	StatusIndicatorBranchSummary = "branchSummary"
)

// StatusIndicator is a Loader that can render inside a border line.
type StatusIndicator struct {
	*tui.Loader
	Kind string
}

// NewStatusIndicator builds a status indicator over the ported loader.
func NewStatusIndicator(kind string, ui tui.TUI, spinnerColorFn, messageColorFn func(string) string, message string, indicator *tui.LoaderIndicatorOptions) *StatusIndicator {
	return &StatusIndicator{
		Loader: tui.NewLoader(ui, spinnerColorFn, messageColorFn, message, indicator),
		Kind:   kind,
	}
}

// RenderInBorder renders the spinner and message without the leading blank
// line, truncated to the border width.
func (s *StatusIndicator) RenderInBorder(width int) string {
	if width <= 0 {
		return ""
	}
	lines := s.Loader.Render(width + 2)
	line := ""
	if len(lines) > 1 {
		line = lines[1]
	}
	if len(line) > 0 && line[0] == ' ' {
		line = line[1:]
	}
	line = trimRightSpace(line)
	return tui.TruncateToWidth(line, width, "", false)
}

// RenderSpinnerInBorder renders only the spinner glyph.
func (s *StatusIndicator) RenderSpinnerInBorder(width int) string {
	if width <= 0 {
		return ""
	}
	return tui.TruncateToWidth(s.Loader.GetRenderedIndicator(), width, "", false)
}

// Dispose stops the animation.
func (s *StatusIndicator) Dispose() { s.Loader.Stop() }

// WorkingStatusIndicator renders the in-flight run indicator.
type WorkingStatusIndicator struct {
	*StatusIndicator
}

// NewWorkingStatusIndicator builds the working indicator with the default
// message.
func NewWorkingStatusIndicator(ui tui.TUI, message string) *WorkingStatusIndicator {
	return &WorkingStatusIndicator{StatusIndicator: NewStatusIndicator(
		StatusIndicatorWorking, ui, nil, nil, message, nil,
	)}
}

func trimRightSpace(text string) string {
	end := len(text)
	for end > 0 {
		switch text[end-1] {
		case ' ', '\t', '\n', '\r':
			end--
			continue
		}
		break
	}
	return text[:end]
}
