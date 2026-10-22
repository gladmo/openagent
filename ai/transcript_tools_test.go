package ai

// Ports of the resolveTranscriptTools support-union tests (#11142): none,
// additions (with removal/redeclaration folding), and changes.

import (
	"testing"
)

func systemMessageWithTools(added []Tool, removed []ToolReference) *SystemMessage {
	return &SystemMessage{
		Content:      StringContent(""),
		ToolsAdded:   added,
		ToolsRemoved: removed,
		TimestampMs:  1,
	}
}

func namedTools(names ...string) []Tool {
	tools := make([]Tool, 0, len(names))
	for _, name := range names {
		tools = append(tools, Tool{Name: name, Description: name})
	}
	return tools
}

func toolNames(tools []Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestResolveTranscriptToolsNoneFoldsAllSystemMessages(t *testing.T) {
	messages := []Message{
		systemMessageWithTools(namedTools("a"), nil),
		&UserMessage{Content: StringContent("hi"), TimestampMs: 2},
		systemMessageWithTools(namedTools("b"), []ToolReference{{Name: "a"}}),
	}
	tools := ResolveTranscriptTools(messages, ToolSupportNone)
	if len(toolNames(tools.RequestTools)) != 1 || tools.RequestTools[0].Name != "b" {
		t.Fatalf("requestTools = %v", toolNames(tools.RequestTools))
	}
	if tools.FoldedSystemMessages != 2 {
		t.Fatalf("folded = %d", tools.FoldedSystemMessages)
	}
}

func TestResolveTranscriptToolsChangesDeclaresEverythingInPlace(t *testing.T) {
	messages := []Message{systemMessageWithTools(namedTools("a", "b"), nil)}
	tools := ResolveTranscriptTools(messages, ToolSupportChanges)
	if len(tools.RequestTools) != 0 || tools.FoldedSystemMessages != 0 {
		t.Fatalf("tools = %+v", tools)
	}
}

func TestResolveTranscriptToolsAdditionsFoldOnRemovalAndRedeclaration(t *testing.T) {
	// Plain additions keep only the initial system message folded.
	messages := []Message{
		systemMessageWithTools(namedTools("a"), nil),
		&UserMessage{Content: StringContent("hi"), TimestampMs: 2},
		systemMessageWithTools(namedTools("b"), nil),
	}
	tools := ResolveTranscriptTools(messages, ToolSupportAdditions)
	if len(toolNames(tools.RequestTools)) != 1 || tools.RequestTools[0].Name != "a" {
		t.Fatalf("requestTools = %v", toolNames(tools.RequestTools))
	}
	if tools.FoldedSystemMessages != 1 {
		t.Fatalf("folded = %d", tools.FoldedSystemMessages)
	}

	// A removal folds through that system message: the top-level list holds
	// the state after the removal ("a" gone, "b" still declared).
	removal := append(messages, systemMessageWithTools(nil, []ToolReference{{Name: "a"}}))
	tools = ResolveTranscriptTools(removal, ToolSupportAdditions)
	if names := toolNames(tools.RequestTools); len(names) != 1 || names[0] != "b" {
		t.Fatalf("after removal requestTools = %v", names)
	}
	if tools.FoldedSystemMessages != 3 {
		t.Fatalf("after removal folded = %d", tools.FoldedSystemMessages)
	}

	// A same-name redeclaration after removal also folds (Kimi K3 ignores a
	// removed tool that a later system message declares again); the folded
	// list carries the redeclared "a" and the still-declared "b".
	redeclared := append(removal, systemMessageWithTools(namedTools("a"), nil))
	tools = ResolveTranscriptTools(redeclared, ToolSupportAdditions)
	if tools.FoldedSystemMessages != 4 {
		t.Fatalf("after redeclaration folded = %d", tools.FoldedSystemMessages)
	}
	names := toolNames(tools.RequestTools)
	if len(names) != 2 {
		t.Fatalf("after redeclaration requestTools = %v", names)
	}
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("after redeclaration requestTools = %v", names)
	}
}
