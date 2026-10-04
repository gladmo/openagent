package codingagent

// Port of modes/interactive/components/markdown-transform.ts,
// custom-message.ts, and custom-entry.ts: extension markdown
// transformers with panic containment, and the custom message/entry
// renderers with styled default fallbacks.

import (
	"fmt"

	"github.com/gladmo/openagent/tui"
)

// MarkdownTransformContext carries the render context to transformers.
type MarkdownTransformContext struct {
	MessageType    string // "assistant" | "user" | "custom"
	IsStreaming    bool
	AvailableWidth int
}

// MarkdownTransformer rewrites markdown before rendering ("" return =
// keep current).
type MarkdownTransformer func(markdown string, context MarkdownTransformContext) string

// CreateMarkdownTransform composes transformers into one apply func.
func CreateMarkdownTransform(messageType string, isStreaming bool, transformers []MarkdownTransformer) func(markdown string, availableWidth int) string {
	return func(markdown string, availableWidth int) string {
		context := MarkdownTransformContext{
			MessageType:    messageType,
			IsStreaming:    isStreaming,
			AvailableWidth: availableWidth,
		}
		transformed := markdown
		for _, transformer := range transformers {
			result := func() (out string) {
				defer func() {
					if recovered := recover(); recovered != nil {
						out = "" // keep current markdown, continue
					}
				}()
				return transformer(transformed, context)
			}()
			if result != "" {
				transformed = result
			}
		}
		return transformed
	}
}

// ============================================================================
// custom-message.ts
// ============================================================================

// CustomMessageView is the render-side view of an extension message.
type CustomMessageView struct {
	CustomType string
	Content    string
}

// CustomMessageRenderOptions carry the render state.
type CustomMessageRenderOptions struct {
	Expanded  bool
	OutputPad int
}

// CustomMessageRenderer is the extension renderer seam (nil component =
// default rendering).
type CustomMessageRenderer func(message *CustomMessageView, options CustomMessageRenderOptions) tui.Component

// CustomMessageComponent renders extension messages with the styled
// default fallback.
type CustomMessageComponent struct {
	*tui.VStack

	message        *CustomMessageView
	customRenderer CustomMessageRenderer
	box            *tui.Box
	expanded       bool
	outputPad      int
}

// NewCustomMessageComponent builds the component.
func NewCustomMessageComponent(message *CustomMessageView, customRenderer CustomMessageRenderer, outputPad int) *CustomMessageComponent {
	component := &CustomMessageComponent{
		VStack:         tui.NewVStack(0),
		message:        message,
		customRenderer: customRenderer,
		outputPad:      outputPad,
	}
	component.box = tui.NewBox(1, 1, func(str string) string { return str })
	component.VStack.AddChild(tui.NewSpacer(1), nil)
	component.rebuild()
	return component
}

// SetExpanded toggles the expansion state.
func (c *CustomMessageComponent) SetExpanded(expanded bool) {
	if c.expanded != expanded {
		c.expanded = expanded
		c.rebuild()
	}
}

// SetOutputPad adjusts padding.
func (c *CustomMessageComponent) SetOutputPad(outputPad int) {
	if c.outputPad != outputPad {
		c.outputPad = outputPad
		c.rebuild()
	}
}

func (c *CustomMessageComponent) rebuild() {
	// Custom renderer first — it owns its styling; panics fall through
	// to the default rendering.
	if c.customRenderer != nil {
		component := func() (result tui.Component) {
			defer func() { recover() }()
			return c.customRenderer(c.message, CustomMessageRenderOptions{
				Expanded:  c.expanded,
				OutputPad: c.outputPad,
			})
		}()
		if component != nil {
			c.VStack = tui.NewVStack(0)
			c.VStack.AddChild(tui.NewSpacer(1), nil)
			c.VStack.AddChild(component, nil)
			return
		}
	}

	// Default rendering: boxed label + markdown content. The box is
	// rebuilt per render (AddChild appends; reusing the instance would
	// duplicate label/content on every rebuild).
	c.box = tui.NewBox(1, 1, func(str string) string { return str })
	c.VStack = tui.NewVStack(0)
	c.VStack.AddChild(tui.NewSpacer(1), nil)
	c.VStack.AddChild(c.box, nil)
	label := fmt.Sprintf("\x1b[1m[%s]\x1b[22m", c.message.CustomType)
	c.box.AddChild(tui.NewText(label, 0, 0, nil))
	c.box.AddChild(tui.NewSpacer(1))
	markdown := tui.NewMarkdown(c.message.Content, 0, 0, tui.MarkdownTheme{}, nil, nil)
	c.box.AddChild(markdown)
}

// ============================================================================
// custom-entry.ts
// ============================================================================

// CustomEntryView is the render-side view of an extension entry.
type CustomEntryView struct {
	CustomType string
	Data       string
}

// CustomEntryRenderOptions carry the render state.
type CustomEntryRenderOptions struct {
	Expanded bool
}

// CustomEntryRenderer is the entry renderer seam.
type CustomEntryRenderer func(entry *CustomEntryView, options CustomEntryRenderOptions) tui.Component

// CustomEntryComponent renders extension entries (error boxed on
// renderer failure).
type CustomEntryComponent struct {
	*tui.VStack

	entry    *CustomEntryView
	renderer CustomEntryRenderer
	expanded bool
}

// NewCustomEntryComponent builds the component.
func NewCustomEntryComponent(entry *CustomEntryView, renderer CustomEntryRenderer) *CustomEntryComponent {
	component := &CustomEntryComponent{
		VStack:   tui.NewVStack(0),
		entry:    entry,
		renderer: renderer,
	}
	component.rebuild()
	return component
}

// SetExpanded toggles the expansion state.
func (c *CustomEntryComponent) SetExpanded(expanded bool) {
	if c.expanded != expanded {
		c.expanded = expanded
		c.rebuild()
	}
}

func (c *CustomEntryComponent) rebuild() {
	c.VStack = tui.NewVStack(0)

	var component tui.Component
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				message := fmt.Sprintf("%v", recovered)
				box := tui.NewBox(1, 1, func(str string) string { return str })
				box.AddChild(tui.NewText(fmt.Sprintf("[%s] renderer failed: %s", c.entry.CustomType, message), 0, 0, nil))
				component = box
			}
		}()
		component = c.renderer(c.entry, CustomEntryRenderOptions{Expanded: c.expanded})
	}()
	if component == nil {
		return
	}
	c.VStack.AddChild(tui.NewSpacer(1), nil)
	c.VStack.AddChild(component, nil)
}
