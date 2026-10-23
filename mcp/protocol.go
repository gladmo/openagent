package mcp

// Port of packages/mcp/src/protocol/types.ts and protocol/content.ts: MCP
// protocol constants and typed views over validated JSON values. Views keep
// the raw object so fields the views do not model survive round-trips.

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// LATEST_PROTOCOL_VERSION is the protocol version the client requests by
// default.
const LATEST_PROTOCOL_VERSION = "2025-11-25"

// SUPPORTED_PROTOCOL_VERSIONS are the versions the client accepts from a
// server. Servers that do not support the requested version answer with
// their own latest one, so older versions stay accepted for servers built
// on older SDKs.
var SUPPORTED_PROTOCOL_VERSIONS = []string{
	LATEST_PROTOCOL_VERSION, "2025-06-18", "2025-03-26", "2024-11-05",
}

// IsSupportedProtocolVersion reports whether the version is accepted.
func IsSupportedProtocolVersion(version string) bool {
	for _, supported := range SUPPORTED_PROTOCOL_VERSIONS {
		if supported == version {
			return true
		}
	}
	return false
}

// Implementation is a client or server identity from initialize.
type Implementation struct {
	Name    string
	Version string
	Title   string // "" when absent
}

// Root is a filesystem root offered to the server.
type Root struct {
	URI  string
	Name string // "" when absent
}

// ClientCapabilities declares what the client supports.
type ClientCapabilities struct {
	Experimental *jsonx.Obj
	Roots        *RootsCapability
	Sampling     *jsonx.Obj
	Elicitation  *jsonx.Obj
}

// RootsCapability is the roots client capability.
type RootsCapability struct {
	ListChanged bool
}

// ServerCapabilities declares what the server supports.
type ServerCapabilities struct {
	Experimental *jsonx.Obj
	Logging      *jsonx.Obj
	Prompts      *PromptsCapability
	Resources    *ResourcesCapability
	Tools        *ToolsCapability
	Completions  *jsonx.Obj
}

// PromptsCapability is the prompts server capability.
type PromptsCapability struct{ ListChanged bool }

// ResourcesCapability is the resources server capability.
type ResourcesCapability struct {
	Subscribe   bool
	ListChanged bool
}

// ToolsCapability is the tools server capability.
type ToolsCapability struct{ ListChanged bool }

// InitializeResult is the initialize handshake answer.
type InitializeResult struct {
	ProtocolVersion string
	Capabilities    ServerCapabilities
	ServerInfo      Implementation
	Instructions    string // "" when absent
}

// ProgressNotification is a notifications/progress payload.
type ProgressNotification struct {
	ProgressToken JsonRpcID
	Progress      float64
	Total         float64 // NaN when absent
	HasTotal      bool
	Message       string
	HasMessage    bool
}

// ToolAnnotations are the behavioral hints a server attaches to a tool.
type ToolAnnotations struct {
	Title           string // "" when absent
	ReadOnlyHint    *bool
	DestructiveHint *bool
	IdempotentHint  *bool
	OpenWorldHint   *bool
}

// Tool is one tool a server offers in tools/list.
type Tool struct {
	Name         string
	Title        string // "" when absent
	Description  string // "" when absent
	InputSchema  *jsonx.Obj
	OutputSchema *jsonx.Obj // nil when absent
	Annotations  *ToolAnnotations
	Execution    *ToolExecution
	Meta         *jsonx.Obj
}

// ToolExecution declares how a tool must run.
type ToolExecution struct {
	TaskSupport string // "forbidden" | "optional" | "required"
}

// Resource is a resource a server lists in resources/list.
type Resource struct {
	URI         string
	Name        string
	Title       string // "" when absent
	Description string // "" when absent
	MimeType    string // "" when absent
	Size        float64
	HasSize     bool
	Annotations *ContentAnnotations
	Meta        *jsonx.Obj
	Raw         *jsonx.Obj
}

// ResourceTemplate is a family of resources addressed by an RFC 6570 URI
// template from resources/templates/list.
type ResourceTemplate struct {
	URITemplate string
	Name        string
	Title       string // "" when absent
	Description string // "" when absent
	MimeType    string // "" when absent
	Annotations *ContentAnnotations
	Meta        *jsonx.Obj
	Raw         *jsonx.Obj
}

// ListResourcesResult is one page of resources/list.
type ListResourcesResult struct {
	Resources  []Resource
	NextCursor string // "" when absent
}

// ListResourceTemplatesResult is one page of resources/templates/list.
type ListResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplate
	NextCursor        string // "" when absent
}

// ReadResourceResult is a resources/read answer.
type ReadResourceResult struct {
	Contents []ResourceContents
}

// ResourceContents is one text or blob resource contents entry.
type ResourceContents struct {
	URI      string
	MimeType string // "" when absent
	Text     string
	Blob     string
	IsBlob   bool
	Meta     *jsonx.Obj
}

// ContentAnnotations are audience and priority hints on content blocks.
type ContentAnnotations struct {
	Audience     []string
	HasAudience  bool
	Priority     float64
	HasPriority  bool
	LastModified string // "" when absent
}

// ContentBlock is one block of a tool result: TextContent,
// ImageContent, AudioContent, ResourceLinkContent, or
// EmbeddedResourceContent.
type ContentBlock interface{ contentType() string }

// TextContent is a "text" block.
type TextContent struct {
	Text        string
	Annotations *ContentAnnotations
	Meta        *jsonx.Obj
}

func (TextContent) contentType() string { return "text" }

// ImageContent is an "image" block with base64 data.
type ImageContent struct {
	Data        string
	MimeType    string
	Annotations *ContentAnnotations
	Meta        *jsonx.Obj
}

func (ImageContent) contentType() string { return "image" }

// AudioContent is an "audio" block with base64 data.
type AudioContent struct {
	Data        string
	MimeType    string
	Annotations *ContentAnnotations
	Meta        *jsonx.Obj
}

func (AudioContent) contentType() string { return "audio" }

// ResourceLinkContent names a resource by URI.
type ResourceLinkContent struct {
	URI         string
	Name        string
	Title       string // "" when absent
	Description string // "" when absent
	MimeType    string // "" when absent
	Size        float64
	HasSize     bool
	Annotations *ContentAnnotations
	Meta        *jsonx.Obj
}

func (ResourceLinkContent) contentType() string { return "resource_link" }

// EmbeddedResourceContent embeds resource contents inline.
type EmbeddedResourceContent struct {
	Resource    ResourceContents
	Annotations *ContentAnnotations
	Meta        *jsonx.Obj
}

func (EmbeddedResourceContent) contentType() string { return "resource" }

// CallToolResult is a tools/call answer.
type CallToolResult struct {
	Content []ContentBlock
	// StructuredContent may be any JSON value, including arrays and nil
	// (MCP 2026-07-28; earlier revisions allowed only objects).
	StructuredContent any
	IsError           bool
	Meta              *jsonx.Obj
}

// UnknownContent passes through blocks the typed views do not model.
type UnknownContent struct {
	Type string
	Raw  any
}

func (u UnknownContent) contentType() string { return u.Type }

// LlmContent is tool result content in the shape LLM APIs accept: text and
// base64 images.
type LlmContent struct {
	Type     string // "text" | "image"
	Text     string
	Data     string
	MimeType string
}

// ToLlmContent converts a tool result to text and image content for a
// model. Text and images pass through, embedded text resources become
// text, embedded image resources become images, and other blocks (audio,
// resource links, binary resources) become a short text placeholder. A
// result without content blocks but with structuredContent becomes its
// JSON, since servers should, but do not always, mirror structured results
// as text.
func ToLlmContent(result *CallToolResult) []LlmContent {
	content := make([]LlmContent, 0, len(result.Content))
	for _, block := range result.Content {
		content = append(content, blockToLlmContent(block))
	}
	if len(content) == 0 && result.StructuredContent != nil {
		content = append(content, LlmContent{Type: "text", Text: stringifyIndent(result.StructuredContent)})
	}
	return content
}

func blockToLlmContent(block ContentBlock) LlmContent {
	switch typed := block.(type) {
	case TextContent:
		return LlmContent{Type: "text", Text: typed.Text}
	case ImageContent:
		return LlmContent{Type: "image", Data: typed.Data, MimeType: typed.MimeType}
	case AudioContent:
		return LlmContent{Type: "text", Text: fmt.Sprintf("[audio %s omitted]", typed.MimeType)}
	case ResourceLinkContent:
		return LlmContent{Type: "text", Text: typed.Name + ": " + typed.URI}
	case EmbeddedResourceContent:
		resource := typed.Resource
		if !resource.IsBlob {
			return LlmContent{Type: "text", Text: resource.Text}
		}
		if strings.HasPrefix(resource.MimeType, "image/") {
			return LlmContent{Type: "image", Data: resource.Blob, MimeType: resource.MimeType}
		}
		mimeType := resource.MimeType
		if mimeType == "" {
			mimeType = "unknown type"
		}
		return LlmContent{
			Type: "text",
			Text: fmt.Sprintf("[binary resource %s (%s) omitted]", resource.URI, mimeType),
		}
	}
	name := block.contentType()
	if name == "" {
		name = "undefined"
	}
	return LlmContent{Type: "text", Text: "[unsupported MCP content " + name + "]"}
}

// stringifyIndent renders a JSON value with two-space indentation, matching
// JSON.stringify(value, null, 2).
func stringifyIndent(value any) string {
	var builder strings.Builder
	writeIndented(&builder, value, "")
	return builder.String()
}

func writeIndented(builder *strings.Builder, value any, indent string) {
	switch typed := value.(type) {
	case *jsonx.Obj:
		if typed.Len() == 0 {
			builder.WriteString("{}")
			return
		}
		inner := indent + "  "
		builder.WriteString("{\n")
		for index, entry := range typed.Entries() {
			builder.WriteString(inner)
			builder.WriteString(jsonx.Stringify(entry[0]))
			builder.WriteString(": ")
			writeIndented(builder, entry[1], inner)
			if index < typed.Len()-1 {
				builder.WriteString(",")
			}
			builder.WriteString("\n")
		}
		builder.WriteString(indent)
		builder.WriteString("}")
	case []any:
		if len(typed) == 0 {
			builder.WriteString("[]")
			return
		}
		inner := indent + "  "
		builder.WriteString("[\n")
		for index, item := range typed {
			builder.WriteString(inner)
			writeIndented(builder, item, inner)
			if index < len(typed)-1 {
				builder.WriteString(",")
			}
			builder.WriteString("\n")
		}
		builder.WriteString(indent)
		builder.WriteString("]")
	default:
		builder.WriteString(jsonx.Stringify(value))
	}
}

// =============================================================================
// Parsers (structural validation like the reference)
// =============================================================================

func optionalString(obj *jsonx.Obj, key string) string {
	value, ok := obj.Get(key)
	if !ok || value == nil {
		return ""
	}
	text, _ := value.(string)
	return text
}

func optionalBool(obj *jsonx.Obj, key string) *bool {
	value, ok := obj.Get(key)
	if !ok {
		return nil
	}
	boolean, ok := value.(bool)
	if !ok {
		return nil
	}
	return &boolean
}

func parseImplementation(obj *jsonx.Obj) Implementation {
	return Implementation{
		Name:    optionalString(obj, "name"),
		Version: optionalString(obj, "version"),
		Title:   optionalString(obj, "title"),
	}
}

func parseAnnotations(value any) *ContentAnnotations {
	obj, ok := isObject(value)
	if !ok {
		return nil
	}
	annotations := &ContentAnnotations{}
	if audience, ok := obj.MustGet("audience").([]any); ok {
		annotations.HasAudience = true
		for _, entry := range audience {
			if text, ok := entry.(string); ok {
				annotations.Audience = append(annotations.Audience, text)
			}
		}
	}
	if priority, ok := obj.MustGet("priority").(float64); ok {
		annotations.HasPriority = true
		annotations.Priority = priority
	}
	annotations.LastModified = optionalString(obj, "lastModified")
	return annotations
}

func parseToolAnnotations(value any) *ToolAnnotations {
	obj, ok := isObject(value)
	if !ok {
		return nil
	}
	return &ToolAnnotations{
		Title:           optionalString(obj, "title"),
		ReadOnlyHint:    optionalBool(obj, "readOnlyHint"),
		DestructiveHint: optionalBool(obj, "destructiveHint"),
		IdempotentHint:  optionalBool(obj, "idempotentHint"),
		OpenWorldHint:   optionalBool(obj, "openWorldHint"),
	}
}

func parseToolExecution(value any) *ToolExecution {
	obj, ok := isObject(value)
	if !ok {
		return nil
	}
	taskSupport := optionalString(obj, "taskSupport")
	if taskSupport == "" {
		return &ToolExecution{}
	}
	return &ToolExecution{TaskSupport: taskSupport}
}

// ParseTool validates one tools/list entry.
func ParseTool(value any) (Tool, bool) {
	obj, ok := isObject(value)
	if !ok {
		return Tool{}, false
	}
	name, hasName := obj.MustGet("name").(string)
	schema, hasSchema := obj.MustGet("inputSchema").(*jsonx.Obj)
	if !hasName || !hasSchema {
		return Tool{}, false
	}
	outputSchema, _ := obj.MustGet("outputSchema").(*jsonx.Obj)
	meta, _ := obj.MustGet("_meta").(*jsonx.Obj)
	return Tool{
		Name:         name,
		Title:        optionalString(obj, "title"),
		Description:  optionalString(obj, "description"),
		InputSchema:  schema,
		OutputSchema: outputSchema,
		Annotations:  parseToolAnnotations(mustGet(obj, "annotations")),
		Execution:    parseToolExecution(mustGet(obj, "execution")),
		Meta:         meta,
	}, true
}

// name is required by the spec, but some servers omit it; the URI stands
// in.
func parseResource(value any) (Resource, bool) {
	obj, ok := isObject(value)
	if !ok {
		return Resource{}, false
	}
	uri, hasURI := obj.MustGet("uri").(string)
	if !hasURI {
		return Resource{}, false
	}
	if name, hasName := obj.Get("name"); hasName {
		if _, isString := name.(string); !isString {
			return Resource{}, false
		}
	}
	meta, _ := obj.MustGet("_meta").(*jsonx.Obj)
	resource := Resource{
		URI:         uri,
		Name:        optionalString(obj, "name"),
		Title:       optionalString(obj, "title"),
		Description: optionalString(obj, "description"),
		MimeType:    optionalString(obj, "mimeType"),
		Annotations: parseAnnotations(mustGet(obj, "annotations")),
		Meta:        meta,
		Raw:         obj,
	}
	if size, ok := obj.MustGet("size").(float64); ok {
		resource.HasSize = true
		resource.Size = size
	}
	return resource, true
}

func parseResourceTemplate(value any) (ResourceTemplate, bool) {
	obj, ok := isObject(value)
	if !ok {
		return ResourceTemplate{}, false
	}
	uriTemplate, hasURI := obj.MustGet("uriTemplate").(string)
	if !hasURI {
		return ResourceTemplate{}, false
	}
	if name, hasName := obj.Get("name"); hasName {
		if _, isString := name.(string); !isString {
			return ResourceTemplate{}, false
		}
	}
	meta, _ := obj.MustGet("_meta").(*jsonx.Obj)
	return ResourceTemplate{
		URITemplate: uriTemplate,
		Name:        optionalString(obj, "name"),
		Title:       optionalString(obj, "title"),
		Description: optionalString(obj, "description"),
		MimeType:    optionalString(obj, "mimeType"),
		Annotations: parseAnnotations(mustGet(obj, "annotations")),
		Meta:        meta,
		Raw:         obj,
	}, true
}

// ToResource converts a validated entry, defaulting the name to the URI.
func ToResource(item Resource) Resource {
	if item.Name == "" {
		item.Name = item.URI
	}
	return item
}

// ToResourceTemplate converts a validated entry, defaulting the name to
// the URI template.
func ToResourceTemplate(item ResourceTemplate) ResourceTemplate {
	if item.Name == "" {
		item.Name = item.URITemplate
	}
	return item
}

func parseResourceContents(value any) (ResourceContents, bool) {
	obj, ok := isObject(value)
	if !ok {
		return ResourceContents{}, false
	}
	uri, hasURI := obj.MustGet("uri").(string)
	if !hasURI {
		return ResourceContents{}, false
	}
	text, hasText := obj.MustGet("text").(string)
	blob, hasBlob := obj.MustGet("blob").(string)
	if !hasText && !hasBlob {
		return ResourceContents{}, false
	}
	meta, _ := obj.MustGet("_meta").(*jsonx.Obj)
	return ResourceContents{
		URI:      uri,
		MimeType: optionalString(obj, "mimeType"),
		Text:     text,
		Blob:     blob,
		IsBlob:   hasBlob,
		Meta:     meta,
	}, true
}

// ParseContentBlock views one tool result content block. Parsing is
// lenient like the reference: items the views do not model keep their raw
// value as UnknownContent instead of failing the result.
func ParseContentBlock(value any) ContentBlock {
	obj, ok := isObject(value)
	if !ok {
		return UnknownContent{}
	}
	blockType, _ := obj.MustGet("type").(string)
	annotations := parseAnnotations(mustGet(obj, "annotations"))
	meta, _ := obj.MustGet("_meta").(*jsonx.Obj)
	switch blockType {
	case "text":
		text, _ := obj.MustGet("text").(string)
		return TextContent{Text: text, Annotations: annotations, Meta: meta}
	case "image", "audio":
		data, _ := obj.MustGet("data").(string)
		mimeType, _ := obj.MustGet("mimeType").(string)
		if blockType == "image" {
			return ImageContent{Data: data, MimeType: mimeType, Annotations: annotations, Meta: meta}
		}
		return AudioContent{Data: data, MimeType: mimeType, Annotations: annotations, Meta: meta}
	case "resource_link":
		uri, _ := obj.MustGet("uri").(string)
		name, _ := obj.MustGet("name").(string)
		link := ResourceLinkContent{
			URI:         uri,
			Name:        name,
			Title:       optionalString(obj, "title"),
			Description: optionalString(obj, "description"),
			MimeType:    optionalString(obj, "mimeType"),
			Annotations: annotations,
			Meta:        meta,
		}
		if size, ok := obj.MustGet("size").(float64); ok {
			link.HasSize = true
			link.Size = size
		}
		return link
	case "resource":
		contents, _ := obj.MustGet("resource").(*jsonx.Obj)
		resource, _ := parseResourceContents(contents)
		return EmbeddedResourceContent{Resource: resource, Annotations: annotations, Meta: meta}
	}
	return UnknownContent{Type: blockType, Raw: value}
}

// ParseCallToolResult validates a tools/call result. content is required by
// the spec, but servers that only return structuredContent omit it (the
// SDK defaults it too).
func ParseCallToolResult(value any) (*CallToolResult, error) {
	obj, ok := isObject(value)
	if !ok {
		return nil, NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid MCP tools/call result", nil)
	}
	if rawContent, has := obj.Get("content"); has {
		if _, isList := rawContent.([]any); !isList {
			return nil, NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid MCP tools/call result", nil)
		}
	}
	if rawStructured, has := obj.Get("structuredContent"); has {
		if _, isObject := rawStructured.(*jsonx.Obj); !isObject {
			return nil, NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid MCP tools/call structured content", nil)
		}
	}
	result := &CallToolResult{}
	if rawContent, has := obj.Get("content"); has {
		items, _ := rawContent.([]any)
		for _, item := range items {
			result.Content = append(result.Content, ParseContentBlock(item))
		}
	}
	if raw, has := obj.Get("structuredContent"); has {
		result.StructuredContent = raw
	}
	result.IsError, _ = obj.MustGet("isError").(bool)
	result.Meta, _ = obj.MustGet("_meta").(*jsonx.Obj)
	return result, nil
}

// DecodeBase64 decodes a base64 contents payload.
func DecodeBase64(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}

// EncodeBase64 encodes bytes as standard base64, matching
// Buffer.toString("base64").
func EncodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
