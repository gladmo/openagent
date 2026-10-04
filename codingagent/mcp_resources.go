package codingagent

// Port of extensions/mcp/resources.ts at reference commit a276dabe (the
// shared helpers; the three resource ToolDefinitions land with the
// extension host phase, see PORTING.md): resource visibility rules for
// the Codex-style list_mcp_resources, list_mcp_resource_templates, and
// read_mcp_resource tools.

import (
	"regexp"
	"strings"

	"github.com/gladmo/openagent/mcp"
)

// Names of the three resource tools models trained on Codex and opencode
// use unchanged.
const (
	ListMcpResourcesTool         = "list_mcp_resources"
	ListMcpResourceTemplatesTool = "list_mcp_resource_templates"
)

// McpResourceServer is a connected server that offers resources.
type McpResourceServer interface {
	Name() string
	TimeoutMs() int
	ResourcesPage(cursor string, options mcp.McpRequestOptions) (*mcp.ListResourcesResult, error)
	ResourceTemplatesPage(cursor string, options mcp.McpRequestOptions) (*mcp.ListResourceTemplatesResult, error)
	AllResources(options mcp.McpRequestOptions) ([]mcp.Resource, error)
	AllResourceTemplates(options mcp.McpRequestOptions) ([]mcp.ResourceTemplate, error)
	ReadResource(uri string, options mcp.McpRequestOptions) (*mcp.ReadResourceResult, error)
}

var mcpAppProfilePattern = regexp.MustCompile(`;\s*profile\s*=\s*"?mcp-app"?`)

// IsMcpAppResource reports whether a listed item is an MCP App user
// interface, which only hosts that render them can use.
func IsMcpAppResource(item mcp.Resource) bool {
	uri := item.URI
	return strings.HasPrefix(uri, "ui://") || mcpAppProfilePattern.MatchString(item.MimeType)
}

// IsMcpAppTemplate is IsMcpAppResource for resource templates.
func IsMcpAppTemplate(template mcp.ResourceTemplate) bool {
	uri := template.URITemplate
	return strings.HasPrefix(uri, "ui://") || mcpAppProfilePattern.MatchString(template.MimeType)
}
