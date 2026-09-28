package ai

// transcript.go ports utils/transcript.ts.

// CreateInitialSystemMessage builds the leading system message for a prompt
// and tool set. Returns nil when both are empty.
func CreateInitialSystemMessage(systemPrompt *string, tools []Tool) *SystemMessage {
	hasSystemPrompt := systemPrompt != nil && len(*systemPrompt) > 0
	hasTools := len(tools) > 0
	if !hasSystemPrompt && !hasTools {
		return nil
	}
	content := ""
	if systemPrompt != nil {
		content = *systemPrompt
	}
	m := &SystemMessage{Content: StringContent(content), TimestampMs: 0}
	if hasTools {
		m.ToolsAdded = tools
	}
	return m
}

// NormalizeContext folds Context.SystemPrompt/Tools into a leading system
// message. This is the only producer of TranscriptContext.
func NormalizeContext(context Context) *TranscriptContext {
	initial := CreateInitialSystemMessage(context.SystemPrompt, context.Tools)
	if initial == nil {
		return NewTranscriptContext(context.Messages)
	}
	messages := make([]Message, 0, len(context.Messages)+1)
	messages = append(messages, initial)
	messages = append(messages, context.Messages...)
	return NewTranscriptContext(messages)
}

// asSystemMessage returns the message as *SystemMessage when role is system.
func asSystemMessage(message Message) (*SystemMessage, bool) {
	m, ok := message.(*SystemMessage)
	return m, ok
}

// GetInitialSystemMessage returns the leading system message, if any.
func GetInitialSystemMessage(messages []Message) *SystemMessage {
	if len(messages) == 0 {
		return nil
	}
	if m, ok := asSystemMessage(messages[0]); ok {
		return m
	}
	return nil
}

// WithoutInitialSystemMessage drops the leading system message.
func WithoutInitialSystemMessage(messages []Message) []Message {
	if GetInitialSystemMessage(messages) != nil {
		return messages[1:]
	}
	return messages
}

// GetCurrentTools resolves the tools after applying every transcript delta.
func GetCurrentTools(messages []Message) []Tool {
	type entry struct {
		tool  Tool
		index int
	}
	tools := map[string]entry{}
	order := 0
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		for _, ref := range m.ToolsRemoved {
			delete(tools, ref.Name)
		}
		for _, tool := range m.ToolsAdded {
			if _, exists := tools[tool.Name]; !exists {
				order++
			}
			tools[tool.Name] = entry{tool: tool, index: order}
		}
	}
	out := make([]Tool, 0, len(tools))
	// Preserve insertion order like the TS Map.
	sorted := make([]string, 0, len(tools))
	for name := range tools {
		sorted = append(sorted, name)
	}
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && tools[sorted[j]].index < tools[sorted[j-1]].index; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	for _, name := range sorted {
		out = append(out, tools[name].tool)
	}
	return out
}

// GetCurrentSystemMessage replays every system message into one leading
// system message holding the current prompt and tools.
func GetCurrentSystemMessage(messages []Message) *SystemMessage {
	content := []string{}
	var sections map[string]*string
	var sectionOrder []string
	var timestamp *float64
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		if timestamp == nil {
			ts := m.TimestampMs
			timestamp = &ts
		}
		text := ContentText(m.Content, "\n")
		if len(text) > 0 {
			content = append(content, text)
		}
		for _, name := range m.SectionOrder {
			if m.Sections == nil {
				continue
			}
			value := m.Sections[name]
			if sections == nil {
				sections = map[string]*string{}
			}
			if value == nil {
				delete(sections, name)
			} else {
				if _, exists := sections[name]; !exists {
					sectionOrder = append(sectionOrder, name)
				}
				sections[name] = value
			}
		}
	}
	tools := GetCurrentTools(messages)
	if timestamp == nil && len(tools) == 0 {
		return nil
	}
	joined := joinStrings(content, "\n\n")
	m := &SystemMessage{Content: StringContent(joined)}
	if len(sections) > 0 {
		m.HasSections = true
		m.Sections = sections
		m.SectionOrder = filterOrder(sectionOrder, sections)
	}
	if len(tools) > 0 {
		m.ToolsAdded = tools
	}
	ts := 0.0
	if timestamp != nil {
		ts = *timestamp
	}
	m.TimestampMs = ts
	return m
}

func filterOrder(order []string, sections map[string]*string) []string {
	out := make([]string, 0, len(order))
	seen := map[string]bool{}
	for _, name := range order {
		if _, ok := sections[name]; ok && !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	return out
}

func joinStrings(parts []string, sep string) string {
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += sep
		}
		result += part
	}
	return result
}

// GetCurrentSystemPrompt renders the current system prompt text.
func GetCurrentSystemPrompt(messages []Message) string {
	if m := GetCurrentSystemMessage(messages); m != nil {
		return GetSystemMessageText(m)
	}
	return ""
}

// CollapseSystemMessages rebuilds the transcript for APIs without
// mid-conversation system messages.
func CollapseSystemMessages(context *TranscriptContext) *TranscriptContext {
	head := GetCurrentSystemMessage(context.Messages)
	messages := make([]Message, 0, len(context.Messages))
	for _, message := range context.Messages {
		if _, isSystem := asSystemMessage(message); !isSystem {
			messages = append(messages, message)
		}
	}
	if head != nil {
		return NewTranscriptContext(append([]Message{head}, messages...))
	}
	return NewTranscriptContext(messages)
}

// ResolveTranscript keeps later system messages when supported.
func ResolveTranscript(context *TranscriptContext, supportsMidConvoSystemMessages bool) *TranscriptContext {
	if supportsMidConvoSystemMessages {
		return context
	}
	return CollapseSystemMessages(context)
}

// ToToolDeclaration strips executable and display-only fields from a tool
// before transcript comparison or persistence.
func ToToolDeclaration(tool Tool) Tool {
	decl := Tool{Name: tool.Name, Description: tool.Description}
	// JSON round-trip: raw schemas re-decoded; built schemas serialize with
	// markers stripped.
	if tool.Parameters != nil {
		decl.Parameters = typeboxSchemaFromJSON(tool.Parameters.JSON())
		if decl.Parameters == nil {
			decl.Parameters = tool.Parameters
		}
	}
	decl.ConstrainedSampling = tool.ConstrainedSampling
	return decl
}

// DeclarationsEqual reports whether two tools declare the same interface to
// the model, comparing serialized declarations.
func DeclarationsEqual(left, right Tool) bool {
	return jsonStringify(ToolToJSON(ToToolDeclaration(left))) == jsonStringify(ToolToJSON(ToToolDeclaration(right)))
}

// ToolStateChanges mirrors the TS interface.
type ToolStateChanges struct {
	ToolsAdded   []Tool
	ToolsRemoved []ToolReference
}

// GetToolStateChanges compares two complete tool states.
func GetToolStateChanges(previous, current []Tool) ToolStateChanges {
	previousTools := map[string]Tool{}
	for _, tool := range previous {
		previousTools[tool.Name] = tool
	}
	currentTools := map[string]Tool{}
	for _, tool := range current {
		currentTools[tool.Name] = tool
	}
	added := []Tool{}
	for _, tool := range current {
		previousTool, exists := previousTools[tool.Name]
		if !exists || !DeclarationsEqual(previousTool, tool) {
			added = append(added, ToToolDeclaration(tool))
		}
	}
	removed := []ToolReference{}
	for _, tool := range previous {
		currentTool, exists := currentTools[tool.Name]
		if !exists || !DeclarationsEqual(tool, currentTool) {
			removed = append(removed, ToolReference{Name: tool.Name})
		}
	}
	return ToolStateChanges{ToolsAdded: added, ToolsRemoved: removed}
}

// GetDeclaredTools returns every referenced tool definition in
// first-declaration order.
func GetDeclaredTools(messages []Message) []Tool {
	definitions := map[string]Tool{}
	var order []string
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		for _, tool := range m.ToolsAdded {
			if _, exists := definitions[tool.Name]; !exists {
				order = append(order, tool.Name)
			}
			definitions[tool.Name] = tool
		}
	}
	out := make([]Tool, 0, len(order))
	for _, name := range order {
		out = append(out, definitions[name])
	}
	return out
}

// HasToolRedefinitions reports whether a tool name was declared twice with
// different definitions.
func HasToolRedefinitions(messages []Message) bool {
	declared := map[string]Tool{}
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		for _, tool := range m.ToolsAdded {
			if previous, exists := declared[tool.Name]; exists && !DeclarationsEqual(previous, tool) {
				return true
			}
			declared[tool.Name] = tool
		}
	}
	return false
}

// HasNonAdditiveToolChanges reports removals or same-name redeclarations.
func HasNonAdditiveToolChanges(messages []Message) bool {
	declared := map[string]bool{}
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		if len(m.ToolsRemoved) > 0 {
			return true
		}
		for _, tool := range m.ToolsAdded {
			if declared[tool.Name] {
				return true
			}
			declared[tool.Name] = true
		}
	}
	return false
}

// TranscriptTools mirrors the TS interface.
type TranscriptTools struct {
	RequestTools     []Tool
	AnchorsAdditions bool
}

// ResolveTranscriptTools splits tool declarations between the top-level
// request field and in-place additions.
func ResolveTranscriptTools(messages []Message, supportsToolAdditions bool) TranscriptTools {
	anchorsAdditions := supportsToolAdditions && !HasNonAdditiveToolChanges(messages)
	requestTools := GetCurrentTools(messages)
	if anchorsAdditions {
		requestTools = nil
		if initial := GetInitialSystemMessage(messages); initial != nil {
			requestTools = initial.ToolsAdded
		}
	}
	return TranscriptTools{RequestTools: requestTools, AnchorsAdditions: anchorsAdditions}
}
