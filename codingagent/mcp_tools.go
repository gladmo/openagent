package codingagent

// Port of extensions/mcp/tools.ts at reference commit a276dabe (the
// conversion half; the ToolDefinition wrappers land with the extension
// host phase, see PORTING.md): adapts MCP tools and results to pi's
// model-facing content.
//
// Results map onto pi's model-facing content (text and images). Text over
// 20KB keeps its start and end with the middle cut out, like Codex does,
// and the full text is saved to a temp file the model can read. Binary
// resources other than images are saved to temp files too, and resource
// links name the read_mcp_resource tool. MCP errors (isError) are error
// results for the model but keep the structured result.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/mcp"
)

// ToToolExposure maps an MCP exposure to the extensions ToolExposure.
// Codemode and deferred both leave tools out of the codemode
// description; they differ only in which tool the MCP extension
// activates to reach them.
func ToToolExposure(exposure McpExposure) string {
	if exposure == McpExposureCodemode {
		return "deferred"
	}
	return string(exposure)
}

// maxMcpToolNameLength: provider tool names are limited to 64 characters
// of [A-Za-z0-9_-].
const maxMcpToolNameLength = 64

// McpOutputMaxBytes bounds the model-facing text of an MCP result; longer
// text is cut in the middle.
const McpOutputMaxBytes = 20 * 1024

// ReadMcpResourceTool is the tool that reads the resources named by
// resource links.
const ReadMcpResourceTool = "read_mcp_resource"

// McpToolDetails are the MCP-specific details of one tool result.
type McpToolDetails struct {
	Server string
	Tool   string
	// FullOutputPath is the temp file with the full text output, set when
	// the model-facing text was truncated.
	FullOutputPath string
}

// McpOutputSaver saves the full text of a truncated result, or a binary
// resource, and returns the file path. Extension includes the dot, for
// example ".txt".
type McpOutputSaver func(data []byte, extension string) (string, error)

// SaveToTempFile writes oversized MCP output to a private temp file and
// returns its path.
func SaveToTempFile(data []byte, extension string) (string, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("pi-mcp-%s%s", hex.EncodeToString(suffix), extension))
	// Results can carry private data, so only the user may read the file.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// CreateMcpToolName builds mcp__<server>__<tool>, sanitized and
// shortened with a hash suffix when too long. Like Codex, everything but
// [A-Za-z0-9_] becomes _, so the name is also the identifier codemode
// scripts call it by. isTaken reports names used by a different MCP tool:
// sanitizing can map two tools to one name (a-b and a_b), which then get
// the hash suffix.
func CreateMcpToolName(server, tool string, isTaken func(string) bool) string {
	if isTaken == nil {
		isTaken = func(string) bool { return false }
	}
	name := mcpNamespaceSanitized(server, tool)
	if len(name) <= maxMcpToolNameLength && !isTaken(name) {
		return name
	}
	digest := sha256.Sum256([]byte(server + "\x00" + tool))
	hash := hex.EncodeToString(digest[:])[:8]
	kept := len(name)
	if kept > maxMcpToolNameLength-len(hash)-1 {
		kept = maxMcpToolNameLength - len(hash) - 1
	}
	return name[:kept] + "_" + hash
}

func mcpNamespaceSanitized(server, tool string) string {
	sanitized := strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, "mcp__"+server+"__"+tool)
	return sanitized
}

// mcpTextOf joins the text blocks of converted content.
func mcpTextOf(content []ai.ContentBlock) string {
	var texts []string
	for _, block := range content {
		if text, isText := block.(ai.TextContent); isText {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// CreateMcpResultSchema is the output schema of every MCP tool: the
// CallToolResult scripts receive, with the tool's own output schema as
// structuredContent. Codemode detects this shape to render
// CallToolResult<T> declarations.
func CreateMcpResultSchema(structuredContentSchema *jsonx.Obj) *jsonx.Obj {
	schema := jsonx.ObjFrom(
		"type", "object",
		"properties", jsonx.ObjFrom(
			"content", jsonx.ObjFrom("type", "array", "items", jsonx.ObjFrom("type", "object")),
		),
	)
	if structuredContentSchema != nil {
		schema.MustGet("properties").(*jsonx.Obj).Set("structuredContent", structuredContentSchema)
	}
	schema.MustGet("properties").(*jsonx.Obj).Set("isError", jsonx.ObjFrom("type", "boolean"))
	schema.MustGet("properties").(*jsonx.Obj).Set("_meta", jsonx.ObjFrom("type", "object"))
	schema.Set("required", []any{"content"})
	return schema
}

// LimitedMcpContent is the outcome of LimitMcpContent.
type LimitedMcpContent struct {
	Content        []ai.ContentBlock
	FullOutputPath string
}

// LimitMcpContent keeps model-facing text within McpOutputMaxBytes.
// Longer text becomes one text block in Codex's truncation format,
// followed by the path of the file with the full text; images follow it.
func LimitMcpContent(content []ai.ContentBlock, saveOutput McpOutputSaver) (*LimitedMcpContent, error) {
	if saveOutput == nil {
		saveOutput = SaveToTempFile
	}
	combined := mcpTextOf(content)
	truncation := TruncateMiddle(combined, McpOutputMaxBytes)
	if !truncation.Truncated {
		return &LimitedMcpContent{Content: content}, nil
	}
	fullOutputPath := ""
	where := ""
	saved, err := saveOutput([]byte(combined), ".txt")
	if err == nil {
		fullOutputPath = saved
		where = "[Full output: " + fullOutputPath + " (read it with offset/limit)]"
	} else {
		where = "[Could not save the full output: " + err.Error() + "]"
	}
	tokens := (truncation.TotalBytes + 3) / 4
	text := fmt.Sprintf("Warning: truncated output (original token count: %d)\nTotal output lines: %d\n\n%s\n\n%s",
		tokens, truncation.TotalLines, truncation.Content, where)
	limited := []ai.ContentBlock{ai.TextContent{Text: text}}
	for _, block := range content {
		if _, isImage := block.(ai.ImageContent); isImage {
			limited = append(limited, block)
		}
	}
	return &LimitedMcpContent{Content: limited, FullOutputPath: fullOutputPath}, nil
}

// ConvertMcpResultOptions tune result conversion.
type ConvertMcpResultOptions struct {
	// SaveOutput saves truncated text and binary resources; nil uses a
	// temp file.
	SaveOutput McpOutputSaver
	// ReadableResources reports whether the server's resources can be
	// read with read_mcp_resource, which resource links then name.
	ReadableResources bool
}

// extensionOf returns the file extension for a saved binary resource:
// the one its URI ends in, else ".bin".
func extensionOf(uri string) string {
	path := uri
	if parsed, err := mcp.ParseURLLoose(uri); err == nil {
		path = parsed.Path
	}
	if match := regexp.MustCompile(`\.[A-Za-z0-9]{1,8}$`).FindString(path); match != "" {
		return match
	}
	return ".bin"
}

// isTextMimeType reports whether blobs of the type are shown as text.
func isTextMimeType(mimeType string) bool {
	if mimeType == "" {
		return false
	}
	mediaType := mimeType
	if semicolon := strings.IndexByte(mediaType, ';'); semicolon >= 0 {
		mediaType = mediaType[:semicolon]
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" ||
		strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
}

// blockToContent converts one block of server's result to model-facing
// content.
func blockToContent(server string, block mcp.ContentBlock, options ConvertMcpResultOptions) []ai.ContentBlock {
	switch typed := block.(type) {
	case mcp.ResourceLinkContent:
		var details []string
		if typed.MimeType != "" {
			details = append(details, typed.MimeType)
		}
		if typed.HasSize {
			details = append(details, FormatSize(int64(typed.Size)))
		}
		read := ""
		if options.ReadableResources {
			read = ". Read it with " + ReadMcpResourceTool + " (server \"" + server + "\")"
		}
		description := ""
		if typed.Description != "" {
			description = ": " + typed.Description
		}
		detailText := ""
		if len(details) > 0 {
			detailText = " (" + strings.Join(details, ", ") + ")"
		}
		title := typed.Title
		if title == "" {
			title = typed.Name
		}
		return []ai.ContentBlock{ai.TextContent{Text: fmt.Sprintf("[Resource %s %q%s%s%s]",
			typed.URI, title, detailText, description, read)}}
	case mcp.EmbeddedResourceContent:
		resource := typed.Resource
		if resource.IsBlob && !strings.HasPrefix(resource.MimeType, "image/") {
			data, err := mcp.DecodeBase64(resource.Blob)
			kind := resource.MimeType
			if kind == "" {
				kind = "unknown type"
			}
			size := FormatSize(int64(len(data)))
			if err != nil {
				return []ai.ContentBlock{ai.TextContent{Text: fmt.Sprintf("[Binary resource %s (%s, %s) could not be decoded: %s]",
					resource.URI, kind, size, err.Error())}}
			}
			if isTextMimeType(resource.MimeType) {
				return []ai.ContentBlock{ai.TextContent{Text: string(data)}}
			}
			saver := options.SaveOutput
			if saver == nil {
				saver = SaveToTempFile
			}
			path, saveErr := saver(data, extensionOf(resource.URI))
			if saveErr != nil {
				return []ai.ContentBlock{ai.TextContent{Text: fmt.Sprintf("[Binary resource %s (%s, %s) could not be saved: %s]",
					resource.URI, kind, size, saveErr.Error())}}
			}
			return []ai.ContentBlock{ai.TextContent{Text: fmt.Sprintf("[Binary resource %s (%s, %s) saved to %s]",
				resource.URI, kind, size, path)}}
		}
	}
	// Remaining blocks map like the library's toLlmContent.
	var result mcp.CallToolResult
	result.Content = []mcp.ContentBlock{block}
	return llmContentToBlocks(mcp.ToLlmContent(&result))
}

func llmContentToBlocks(content []mcp.LlmContent) []ai.ContentBlock {
	blocks := make([]ai.ContentBlock, 0, len(content))
	for _, item := range content {
		switch item.Type {
		case "image":
			blocks = append(blocks, ai.ImageContent{Data: item.Data, MimeType: item.MimeType})
		default:
			blocks = append(blocks, ai.TextContent{Text: item.Text})
		}
	}
	return blocks
}

// ToModelContent converts server's content blocks to model-facing
// content, before the output limit.
func ToModelContent(server string, blocks []mcp.ContentBlock, options ConvertMcpResultOptions) []ai.ContentBlock {
	converted := []ai.ContentBlock{}
	for _, block := range blocks {
		converted = append(converted, blockToContent(server, block, options)...)
	}
	return converted
}

// ConvertMcpResult converts an MCP result. isError results become error
// results that keep the structured result.
func ConvertMcpResult(server, tool string, result *mcp.CallToolResult, options ConvertMcpResultOptions) (*agent.AgentToolResult, error) {
	// Without content blocks, ToLlmContent falls back to the structured
	// content as JSON.
	var converted []ai.ContentBlock
	if len(result.Content) > 0 {
		converted = ToModelContent(server, result.Content, options)
	} else {
		converted = llmContentToBlocks(mcp.ToLlmContent(result))
	}
	if result.IsError && mcpTextOf(converted) == "" {
		converted = append(converted, ai.TextContent{Text: "MCP tool " + server + "/" + tool + " returned an error"})
	}
	limited, err := LimitMcpContent(converted, options.SaveOutput)
	if err != nil {
		return nil, err
	}
	scriptResult := jsonx.NewObj()
	scriptResult.Set("content", contentBlocksToJSON(result.Content))
	if result.StructuredContent != nil {
		scriptResult.Set("structuredContent", result.StructuredContent)
	}
	scriptResult.Set("isError", result.IsError)
	return &agent.AgentToolResult{
		Content:           limited.Content,
		Details:           McpToolDetails{Server: server, Tool: tool, FullOutputPath: limited.FullOutputPath},
		StructuredContent: scriptResult,
		IsError:           result.IsError,
	}, nil
}

// contentBlocksToJSON renders content blocks as the JSON codemode scripts
// receive (the blocks as sent by the server).
func contentBlocksToJSON(blocks []mcp.ContentBlock) []any {
	items := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch typed := block.(type) {
		case mcp.TextContent:
			items = append(items, contentBlockObj("text", "text", typed.Text))
		case mcp.ImageContent:
			obj := contentBlockObj("image", "data", typed.Data)
			obj.Set("mimeType", typed.MimeType)
			items = append(items, obj)
		case mcp.AudioContent:
			obj := contentBlockObj("audio", "data", typed.Data)
			obj.Set("mimeType", typed.MimeType)
			items = append(items, obj)
		case mcp.ResourceLinkContent:
			obj := contentBlockObj("resource_link", "uri", typed.URI)
			obj.Set("name", typed.Name)
			if typed.Title != "" {
				obj.Set("title", typed.Title)
			}
			if typed.Description != "" {
				obj.Set("description", typed.Description)
			}
			if typed.MimeType != "" {
				obj.Set("mimeType", typed.MimeType)
			}
			if typed.HasSize {
				obj.Set("size", typed.Size)
			}
			items = append(items, obj)
		case mcp.EmbeddedResourceContent:
			resource := jsonx.ObjFrom("uri", typed.Resource.URI)
			if typed.Resource.MimeType != "" {
				resource.Set("mimeType", typed.Resource.MimeType)
			}
			if typed.Resource.IsBlob {
				resource.Set("blob", typed.Resource.Blob)
			} else {
				resource.Set("text", typed.Resource.Text)
			}
			items = append(items, jsonx.ObjFrom("type", "resource", "resource", resource))
		case mcp.UnknownContent:
			items = append(items, typed.Raw)
		}
	}
	return items
}

func contentBlockObj(blockType, key, value string) *jsonx.Obj {
	return jsonx.ObjFrom("type", blockType, key, value)
}
