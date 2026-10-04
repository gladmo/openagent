package codingagent

// Port of core/export-html/tool-renderer.ts: renders custom tool calls
// and results to HTML by invoking their renderers and converting the ANSI
// output. The component-render seam rides the tool definition's render
// hooks.

import (
	"encoding/json"
	"regexp"
	"strings"
)

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[\d;]*m`)

// isBlankRenderedLine reports ANSI-stripped emptiness.
func isBlankRenderedLine(line string) bool {
	return strings.TrimSpace(ansiEscapePattern.ReplaceAllString(line, "")) == ""
}

// trimRenderedResultLines drops leading/trailing blank lines.
func trimRenderedResultLines(lines []string) []string {
	start := 0
	end := len(lines)
	for start < end && isBlankRenderedLine(lines[start]) {
		start++
	}
	for end > start && isBlankRenderedLine(lines[end-1]) {
		end--
	}
	return lines[start:end]
}

// RenderedToolHtml carries one tool's pre-rendered call/result HTML.
type RenderedToolHtml struct {
	CallHtml            string
	ResultHtmlCollapsed string
	ResultHtmlExpanded  string
}

// ToolHtmlRenderer renders custom tools for HTML export.
type ToolHtmlRenderer struct {
	// GetToolDefinition looks up a tool by name.
	GetToolDefinition func(name string) *ToolDefinition
	// RenderCallLines renders a call to ANSI lines (nil = no renderer).
	RenderCallLines func(toolCallID string, definition *ToolDefinition, args any) []string
	// RenderResultLines renders a result to ANSI lines (nil = no
	// renderer); expanded toggles the view.
	RenderResultLines func(toolCallID string, definition *ToolDefinition, result []map[string]any, details any, isError, expanded bool) []string
}

// RenderCall renders one tool call ("" when no renderer or on error).
func (r *ToolHtmlRenderer) RenderCall(toolCallID, toolName string, args any) string {
	if r.GetToolDefinition == nil || r.RenderCallLines == nil {
		return ""
	}
	defer func() { recover() }()
	definition := r.GetToolDefinition(toolName)
	if definition == nil {
		return ""
	}
	lines := r.RenderCallLines(toolCallID, definition, args)
	if lines == nil {
		return ""
	}
	return AnsiLinesToHtml(lines)
}

// RenderResult renders collapsed/expanded views (both "" when no
// renderer; collapsed omitted when identical to expanded).
func (r *ToolHtmlRenderer) RenderResult(toolCallID, toolName string, result []map[string]any, details any, isError bool) *RenderedToolHtml {
	if r.GetToolDefinition == nil || r.RenderResultLines == nil {
		return nil
	}
	defer func() { recover() }()
	definition := r.GetToolDefinition(toolName)
	if definition == nil {
		return nil
	}

	collapsedLines := trimRenderedResultLines(r.RenderResultLines(toolCallID, definition, result, details, isError, false))
	expandedLines := trimRenderedResultLines(r.RenderResultLines(toolCallID, definition, result, details, isError, true))

	collapsed := AnsiLinesToHtml(collapsedLines)
	expanded := AnsiLinesToHtml(expandedLines)

	out := &RenderedToolHtml{ResultHtmlExpanded: expanded}
	if collapsed != "" && collapsed != expanded {
		out.ResultHtmlCollapsed = collapsed
	}
	return out
}

// TemplateRenderedTools are the tools the HTML template renders directly
// (not pre-rendered through the ANSI pipeline).
var TemplateRenderedTools = map[string]bool{
	"bash": true, "read": true, "write": true, "edit": true, "ls": true,
}

// PreRenderCustomTools walks entries collecting custom-tool call/result
// HTML (template-rendered tools skipped).
func PreRenderCustomTools(entries []SessionEntry, renderer *ToolHtmlRenderer) map[string]*RenderedToolHtml {
	if renderer == nil {
		return nil
	}
	rendered := map[string]*RenderedToolHtml{}

	for _, entry := range entries {
		message, ok := entry.(*SessionMessageEntry)
		if !ok {
			continue
		}
		var probe struct {
			Role string `json:"role"`
		}
		if err := jsonUnmarshal(message.Message, &probe); err != nil {
			continue
		}

		if probe.Role == "assistant" {
			var assistant struct {
				Content []struct {
					Type string `json:"type"`
					ID   string `json:"ID"`
					Name string `json:"Name"`
				} `json:"Content"`
			}
			if err := jsonUnmarshal(message.Message, &assistant); err == nil {
				for _, block := range assistant.Content {
					if block.Type == "toolCall" && !TemplateRenderedTools[block.Name] {
						if callHtml := renderer.RenderCall(block.ID, block.Name, nil); callHtml != "" {
							if rendered[block.ID] == nil {
								rendered[block.ID] = &RenderedToolHtml{}
							}
							rendered[block.ID].CallHtml = callHtml
						}
					}
				}
			}
			continue
		}

		if probe.Role == "toolResult" {
			var result struct {
				ToolCallID string           `json:"toolCallId"`
				ToolName   string           `json:"toolName"`
				Content    []map[string]any `json:"Content"`
				Details    any              `json:"Details"`
				IsError    bool             `json:"IsError"`
			}
			if err := jsonUnmarshal(message.Message, &result); err == nil && result.ToolCallID != "" {
				existing := rendered[result.ToolCallID]
				if existing != nil || !TemplateRenderedTools[result.ToolName] {
					if out := renderer.RenderResult(result.ToolCallID, result.ToolName, result.Content, result.Details, result.IsError); out != nil {
						if existing == nil {
							rendered[result.ToolCallID] = &RenderedToolHtml{
								ResultHtmlCollapsed: out.ResultHtmlCollapsed,
								ResultHtmlExpanded:  out.ResultHtmlExpanded,
							}
							continue
						}
						existing.ResultHtmlCollapsed = out.ResultHtmlCollapsed
						existing.ResultHtmlExpanded = out.ResultHtmlExpanded
					}
				}
			}
		}
	}
	return rendered
}

func jsonUnmarshal(data []byte, target any) error {
	return json.Unmarshal(data, target)
}
