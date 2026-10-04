package codingagent

// Port of modes/interactive/components/assistant-message.ts: the
// assistant-message renderer with OSC 133 shell-integration zones,
// thinking-block grouping with per-run visibility, and the
// hidden-thinking collapse.

import (
	"strings"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/tui"
)

const (
	osc133ZoneStart = "\x1b]133;A\x07"
	osc133ZoneEnd   = "\x1b]133;B\x07"
	osc133ZoneFinal = "\x1b]133;C\x07"
)

// AssistantMessageComponent renders one complete assistant message.
type AssistantMessageComponent struct {
	*tui.VStack

	hideThinkingBlock           bool
	hiddenThinkingLabel         string
	outputPad                   int
	lastMessage                 *ai.AssistantMessage
	hasToolCalls                bool
	isStreaming                 bool
	thinkingVisibilityOverrides map[int]bool
}

// NewAssistantMessageComponent builds the component (message optional).
func NewAssistantMessageComponent(message *ai.AssistantMessage, hideThinkingBlock bool, hiddenThinkingLabel string, outputPad int) *AssistantMessageComponent {
	component := &AssistantMessageComponent{
		VStack:                      tui.NewVStack(0),
		hideThinkingBlock:           hideThinkingBlock,
		hiddenThinkingLabel:         hiddenThinkingLabel,
		outputPad:                   outputPad,
		thinkingVisibilityOverrides: map[int]bool{},
	}
	if message != nil {
		component.UpdateContent(message, false)
	}
	return component
}

// SetHideThinkingBlock toggles collapse (clears per-run overrides).
func (c *AssistantMessageComponent) SetHideThinkingBlock(hide bool) {
	c.hideThinkingBlock = hide
	c.thinkingVisibilityOverrides = map[int]bool{}
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage, c.isStreaming)
	}
}

// SetHiddenThinkingLabel swaps the collapsed label.
func (c *AssistantMessageComponent) SetHiddenThinkingLabel(label string) {
	c.hiddenThinkingLabel = label
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage, c.isStreaming)
	}
}

// SetOutputPad adjusts horizontal padding.
func (c *AssistantMessageComponent) SetOutputPad(padding int) {
	c.outputPad = padding
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage, c.isStreaming)
	}
}

// ToggleThinkingVisibility flips one thinking run's override.
func (c *AssistantMessageComponent) ToggleThinkingVisibility(runIndex int) {
	current := c.hideThinkingBlock
	if override, ok := c.thinkingVisibilityOverrides[runIndex]; ok {
		current = override
	}
	c.thinkingVisibilityOverrides[runIndex] = !current
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage, c.isStreaming)
	}
}

// Render adds OSC 133 zones around non-tool-call output.
func (c *AssistantMessageComponent) Render(width int) []string {
	lines := c.VStack.Render(width)
	if c.hasToolCalls || len(lines) == 0 {
		return lines
	}
	lines[0] = osc133ZoneStart + lines[0]
	lines[len(lines)-1] = osc133ZoneEnd + osc133ZoneFinal + lines[len(lines)-1]
	return lines
}

// UpdateContent re-renders from a message (streaming flag optional).
func (c *AssistantMessageComponent) UpdateContent(message *ai.AssistantMessage, isStreaming bool) {
	c.lastMessage = message
	c.isStreaming = isStreaming

	// Clear children.
	c.VStack = tui.NewVStack(0)

	hasVisibleContent := false
	hasToolCalls := false
	for _, block := range message.Content {
		if text, ok := blockText(block); ok {
			if strings.TrimSpace(text) != "" {
				hasVisibleContent = true
			}
			continue
		}
		if thinking, ok := blockThinking(block); ok {
			if strings.TrimSpace(thinking) != "" {
				hasVisibleContent = true
			}
			continue
		}
		if _, ok := block.(*ai.ToolCall); ok {
			hasToolCalls = true
		}
	}
	c.hasToolCalls = hasToolCalls

	if hasVisibleContent {
		c.VStack.AddChild(tui.NewSpacer(1), nil)
	}

	// Render content in order; consecutive thinking blocks group.
	thinkingRunIndex := 0
	blocks := message.Content
	for i := 0; i < len(blocks); i++ {
		if text, ok := blockText(blocks[i]); ok {
			if strings.TrimSpace(text) == "" {
				continue
			}
			markdown := tui.NewMarkdown(strings.TrimSpace(text), c.outputPad, 0, tui.MarkdownTheme{}, nil, nil)
			c.VStack.AddChild(markdown, nil)
			continue
		}

		if _, ok := blockThinking(blocks[i]); ok {
			var thinkingBlocks []string
			for ; i < len(blocks); i++ {
				thinking, ok := blockThinking(blocks[i])
				if !ok {
					break
				}
				trimmed := strings.TrimSpace(thinking)
				if trimmed != "" {
					thinkingBlocks = append(thinkingBlocks, trimmed)
				}
			}
			i--

			if len(thinkingBlocks) == 0 {
				continue
			}

			// Spacing only when visible content follows (avoids a blank
			// line before separately-rendered tool executions).
			hasVisibleAfter := false
			for _, later := range blocks[i+1:] {
				if text, ok := blockText(later); ok && strings.TrimSpace(text) != "" {
					hasVisibleAfter = true
					continue
				}
				if thinking, ok := blockThinking(later); ok && strings.TrimSpace(thinking) != "" {
					hasVisibleAfter = true
				}
			}

			runIndex := thinkingRunIndex
			thinkingRunIndex++
			hidden := c.hideThinkingBlock
			if override, ok := c.thinkingVisibilityOverrides[runIndex]; ok {
				hidden = override
			}

			if hidden {
				label := c.hiddenThinkingLabel
				text := tui.NewText(label, 1, 0, nil)
				c.VStack.AddChild(text, nil)
			} else {
				joined := strings.Join(thinkingBlocks, "\n\n")
				markdown := tui.NewMarkdown(joined, c.outputPad, 0, tui.MarkdownTheme{}, nil, nil)
				c.VStack.AddChild(markdown, nil)
			}
			if hasVisibleAfter {
				c.VStack.AddChild(tui.NewSpacer(1), nil)
			}
		}
	}
}

// LastMessage reports the rendered message (tests).
func (c *AssistantMessageComponent) LastMessage() *ai.AssistantMessage { return c.lastMessage }

// HasToolCalls reports tool-call presence (tests).
func (c *AssistantMessageComponent) HasToolCalls() bool { return c.hasToolCalls }
