package codingagent

// Port of modes/interactive/components/tool-execution.ts: the tool
// execution block with custom renderers (default-box vs self shells),
// fallback preview rendering, and expansion.

import (
	"strings"

	"github.com/gladmo/openagent/tui"
)

// fallbackPreviewLines bounds the no-definition preview.
const fallbackPreviewLines = 10

// ToolExecutionOptions configure the block.
type ToolExecutionOptions struct {
	ShowImages      bool
	ImageWidthCells int
}

// ToolRenderers is what the component needs from a tool: how to draw
// it. It neither executes tools nor reads schemas.
type ToolRenderers interface {
	RenderCall(args any) tui.Component
	RenderResult(result any, options ToolRenderResultOptions) tui.Component
	// RenderShell: "default" (boxed) or "self" (own framing).
	RenderShell() string
}

// ToolRenderResultOptions carry the render state.
type ToolRenderResultOptions struct {
	Expanded  bool
	IsPartial bool
}

// ToolExecutionComponent displays one tool call's execution and result.
type ToolExecutionComponent struct {
	*tui.VStack

	toolName     string
	toolCallID   string
	args         any
	renderers    ToolRenderers
	ui           tui.TUI
	contentBox   *tui.Box
	fallback     *tui.Text
	selfStack    *tui.VStack
	expanded     bool
	isPartial    bool
	execStarted  bool
	argsComplete bool
	result       *ToolExecutionResult
}

// ToolExecutionResult is the outcome the component renders.
type ToolExecutionResult struct {
	Content []map[string]any
	IsError bool
	Details any
}

// NewToolExecutionComponent builds the block.
func NewToolExecutionComponent(toolName, toolCallID string, args any, options ToolExecutionOptions, renderers ToolRenderers, ui tui.TUI) *ToolExecutionComponent {
	if options.ImageWidthCells == 0 {
		options.ImageWidthCells = 60
	}
	component := &ToolExecutionComponent{
		VStack:     tui.NewVStack(0),
		toolName:   toolName,
		toolCallID: toolCallID,
		args:       args,
		renderers:  renderers,
		ui:         ui,
		isPartial:  true,
		contentBox: tui.NewBox(1, 1, func(str string) string { return str }),
		selfStack:  tui.NewVStack(0),
	}
	component.VStack.AddChild(tui.NewSpacer(1), nil)
	if renderers != nil {
		if renderers.RenderShell() == "self" {
			component.VStack.AddChild(component.selfStack, nil)
		} else {
			component.VStack.AddChild(component.contentBox, nil)
		}
	} else {
		component.fallback = tui.NewText("", 1, 1, nil)
		component.VStack.AddChild(component.fallback, nil)
	}
	component.updateDisplay()
	return component
}

// SetArgs updates streaming arguments.
func (c *ToolExecutionComponent) SetArgs(args any) { c.args = args }

// SetExecutionStarted marks argument streaming done.
func (c *ToolExecutionComponent) SetExecutionStarted() { c.execStarted = true }

// SetArgsComplete marks the schema filled.
func (c *ToolExecutionComponent) SetArgsComplete() { c.argsComplete = true }

// SetPartial toggles streaming state.
func (c *ToolExecutionComponent) SetPartial(partial bool) { c.isPartial = partial }

// SetExpanded toggles the full-output view.
func (c *ToolExecutionComponent) SetExpanded(expanded bool) {
	if c.expanded != expanded {
		c.expanded = expanded
		c.updateDisplay()
	}
}

// Expanded reports the state (tests).
func (c *ToolExecutionComponent) Expanded() bool { return c.expanded }

// SetResult records the outcome and re-renders.
func (c *ToolExecutionComponent) SetResult(result *ToolExecutionResult) {
	c.result = result
	c.isPartial = false
	c.updateDisplay()
}

// IsError reports the result's error flag (tests).
func (c *ToolExecutionComponent) IsError() bool { return c.result != nil && c.result.IsError }

func (c *ToolExecutionComponent) updateDisplay() {
	if c.renderers != nil {
		if c.result != nil {
			renderResult := c.renderers.RenderResult(c.result, ToolRenderResultOptions{
				Expanded:  c.expanded,
				IsPartial: c.isPartial,
			})
			if renderResult != nil {
				if c.renderers.RenderShell() == "self" {
					c.selfStack = tui.NewVStack(0)
					c.selfStack.AddChild(renderResult, nil)
				} else {
					c.contentBox = tui.NewBox(1, 1, func(str string) string { return str })
					c.contentBox.AddChild(renderResult)
				}
			}
		} else {
			renderCall := c.renderers.RenderCall(c.args)
			if renderCall != nil {
				if c.renderers.RenderShell() == "self" {
					c.selfStack = tui.NewVStack(0)
					c.selfStack.AddChild(renderCall, nil)
				} else {
					c.contentBox = tui.NewBox(1, 1, func(str string) string { return str })
					c.contentBox.AddChild(renderCall)
				}
			}
		}
		c.rebuildStack()
		return
	}

	// Fallback: bold tool name; result text under preview.
	var text string
	if c.result == nil {
		text = c.toolName
	} else {
		output := c.textOutput()
		if output == "" {
			text = c.toolName
		} else {
			lines := strings.Split(output, "\n")
			display := lines
			if !c.expanded && len(lines) > fallbackPreviewLines {
				display = lines[:fallbackPreviewLines]
			}
			remaining := len(lines) - len(display)
			text = strings.Join(display, "\n")
			if remaining > 0 {
				text += "\n... (" + itoaBash(remaining) + " more lines, ctrl+o to expand)"
			}
		}
	}
	c.fallback = tui.NewText(text, 1, 1, nil)
	c.rebuildStack()
}

func (c *ToolExecutionComponent) rebuildStack() {
	// Rebuild the top-level children while preserving the leading spacer.
	c.VStack = tui.NewVStack(0)
	c.VStack.AddChild(tui.NewSpacer(1), nil)
	if c.renderers != nil {
		if c.renderers.RenderShell() == "self" {
			c.VStack.AddChild(c.selfStack, nil)
		} else {
			c.VStack.AddChild(c.contentBox, nil)
		}
	} else if c.fallback != nil {
		c.VStack.AddChild(c.fallback, nil)
	}
}

func (c *ToolExecutionComponent) textOutput() string {
	if c.result == nil {
		return ""
	}
	var parts []string
	for _, block := range c.result.Content {
		if block["type"] == "text" {
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}
