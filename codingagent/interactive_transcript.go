package codingagent

// Port of modes/interactive/interactive-mode.ts (event bridge): the
// subscription that turns agent lifecycle events into live components —
// streaming assistant text/thinking, tool-execution blocks with
// streaming arguments and results, bash execution updates, and the turn
// boundary bookkeeping the driver needs.

import (
	"sync"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/tui"
)

// InteractiveTranscript renders the live conversation into a container.
type InteractiveTranscript struct {
	mu sync.Mutex

	stack *tui.VStack

	streaming      *AssistantMessageComponent
	streamingID    string
	toolComponents map[string]*ToolExecutionComponent
	bashComponents map[string]*BashExecutionComponent
	finalized      map[string]*AssistantMessageComponent
	order          []liveBlock
}

// liveBlock records render order for rebuilds and scroll anchoring.
type liveBlock struct {
	kind string // "assistant" | "tool" | "bash"
	id   string
}

// NewInteractiveTranscript builds the transcript renderer.
func NewInteractiveTranscript() *InteractiveTranscript {
	return &InteractiveTranscript{
		stack:          tui.NewVStack(0),
		toolComponents: map[string]*ToolExecutionComponent{},
		bashComponents: map[string]*BashExecutionComponent{},
		finalized:      map[string]*AssistantMessageComponent{},
	}
}

// Render implements tui.Component under the mutex: the renderer runs on
// the scheduler goroutine while agent events mutate the tree from the
// run goroutine, so every read of the current stack is serialized
// against HandleEvent/rebuild.
func (t *InteractiveTranscript) Render(width int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stack.Render(width)
}

// Invalidate implements tui.Component.
func (t *InteractiveTranscript) Invalidate() {}

// Component returns the transcript as its own layout node (the locked
// Render above is the synchronization boundary).
func (t *InteractiveTranscript) Component() tui.Component { return t }

// Blocks reports the render order (tests).
func (t *InteractiveTranscript) Blocks() []liveBlock {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]liveBlock(nil), t.order...)
}

// SetExpanded toggles every tool block between its summary and full output
// (toggleToolOutputExpansion).
func (t *InteractiveTranscript) SetExpanded(expanded bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, component := range t.toolComponents {
		component.SetExpanded(expanded)
	}
	t.rebuild()
}

// ToolComponent looks up a tool block (tests).
func (t *InteractiveTranscript) ToolComponent(toolCallID string) *ToolExecutionComponent {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.toolComponents[toolCallID]
}

// HandleEvent renders one agent event.
func (t *InteractiveTranscript) HandleEvent(event agent.AgentEvent, renderers func(toolName string) ToolRenderers) {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch typed := event.(type) {
	case *agent.EventMessageStart:
		t.startAssistant(typed.Message)

	case *agent.EventMessageUpdate:
		t.updateAssistant(typed.Message)

	case *agent.EventMessageEnd:
		t.endAssistant(typed.Message)

	case *agent.EventToolExecutionStart:
		t.startTool(typed.ToolCallID, typed.ToolName, typed.Args, renderers)

	case *agent.EventToolExecutionUpdate:
		t.updateTool(typed.ToolCallID, typed.PartialResult)

	case *agent.EventToolExecutionEnd:
		t.endTool(typed.ToolCallID, typed.Result, typed.IsError)
	}
}

// startAssistant opens a streaming assistant block.
func (t *InteractiveTranscript) startAssistant(message agent.AgentMessage) {
	if assistant, ok := message.(*ai.AssistantMessage); ok {
		t.streaming = NewAssistantMessageComponent(assistant, false, "Thinking...", 1)
		t.streamingID = "assistant-" + itoaBash(len(t.order))
		t.order = append(t.order, liveBlock{kind: "assistant", id: t.streamingID})
		t.rebuild()
	}
}

// updateAssistant re-renders the streaming block.
func (t *InteractiveTranscript) updateAssistant(message *ai.AssistantMessage) {
	if t.streaming == nil || message == nil {
		return
	}
	t.streaming.UpdateContent(message, true)
	t.rebuild()
}

// endAssistant finalizes the streaming block.
func (t *InteractiveTranscript) endAssistant(message agent.AgentMessage) {
	if assistant, ok := message.(*ai.AssistantMessage); ok && t.streaming != nil {
		t.streaming.UpdateContent(assistant, false)
	}
	if t.streaming != nil {
		t.finalized[t.streamingID] = t.streaming
	}
	t.streaming = nil
	t.rebuild()
}

// startTool opens a tool execution block.
func (t *InteractiveTranscript) startTool(toolCallID, toolName string, args any, renderers func(string) ToolRenderers) {
	var toolRenderers ToolRenderers
	if renderers != nil {
		toolRenderers = renderers(toolName)
	}
	component := NewToolExecutionComponent(toolName, toolCallID, args, ToolExecutionOptions{}, toolRenderers, nil)
	component.SetExecutionStarted()
	t.toolComponents[toolCallID] = component
	t.order = append(t.order, liveBlock{kind: "tool", id: toolCallID})
	t.rebuild()
}

// updateTool streams partial results into the block.
func (t *InteractiveTranscript) updateTool(toolCallID string, partial any) {
	component, ok := t.toolComponents[toolCallID]
	if !ok {
		return
	}
	if result, isResult := partial.(*ToolExecutionResult); isResult {
		component.SetResult(result)
	}
	t.rebuild()
}

// endTool records the final result.
func (t *InteractiveTranscript) endTool(toolCallID string, result *agent.AgentToolResult, isError bool) {
	component, ok := t.toolComponents[toolCallID]
	if !ok {
		return
	}
	component.SetResult(&ToolExecutionResult{
		Content: toolResultContentBlocks(result),
		IsError: isError,
	})
	t.rebuild()
}

// toolResultContentBlocks converts an agent tool result to render blocks.
func toolResultContentBlocks(result *agent.AgentToolResult) []map[string]any {
	if result == nil {
		return nil
	}
	var blocks []map[string]any
	for _, block := range result.Content {
		if text, ok := blockText(block); ok {
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
			continue
		}
		if mimeType, ok := blockImage(block); ok {
			blocks = append(blocks, map[string]any{"type": "image", "mimeType": mimeType})
		}
	}
	return blocks
}

// AppendBashBlock opens a streaming bash execution block.
func (t *InteractiveTranscript) AppendBashBlock(id, command string, excludeFromContext bool, ui tui.TUI) *BashExecutionComponent {
	t.mu.Lock()
	defer t.mu.Unlock()
	component := NewBashExecutionComponent(command, ui, excludeFromContext)
	t.bashComponents[id] = component
	t.order = append(t.order, liveBlock{kind: "bash", id: id})
	t.rebuild()
	return component
}

// HandleBashUpdate streams output into a bash block.
func (t *InteractiveTranscript) HandleBashUpdate(id, delta string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	component, ok := t.bashComponents[id]
	if !ok {
		return
	}
	component.AppendOutput(delta)
	t.rebuild()
}

// rebuild recomposes the layout from render order.
func (t *InteractiveTranscript) rebuild() {
	stack := tui.NewVStack(0)
	renderAssistant := func(component *AssistantMessageComponent) {
		if component != nil {
			stack.AddChild(component, nil)
		}
	}

	for _, block := range t.order {
		switch block.kind {
		case "assistant":
			// The streaming block is rendered last (it is the live tail);
			// finalized blocks keep their position.
			if t.streaming != nil && block.id == t.streamingID {
				continue
			}
			renderAssistant(t.finalized[block.id])
		case "tool":
			if component, ok := t.toolComponents[block.id]; ok {
				stack.AddChild(component, nil)
			}
		case "bash":
			if component, ok := t.bashComponents[block.id]; ok {
				stack.AddChild(component, nil)
			}
		}
	}
	renderAssistant(t.streaming)
	t.stack = stack
}

// Clear drops every block (session replacement).
func (t *InteractiveTranscript) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.streaming = nil
	t.streamingID = ""
	t.order = nil
	t.toolComponents = map[string]*ToolExecutionComponent{}
	t.bashComponents = map[string]*BashExecutionComponent{}
	t.finalized = map[string]*AssistantMessageComponent{}
	t.rebuild()
}
