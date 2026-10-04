package codingagent

// Port of core/tools/write.ts: the write tool with pluggable operations and
// mutation-queue serialization.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/typebox"
)

// WriteToolSystemPromptContribution is the write tool's prompt section.
var WriteToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet:    "Create or overwrite files",
	Guidelines: []string{"Use write only for new files or complete rewrites."},
}

// WriteToolInput is the tool's validated input.
type WriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteOperations are the pluggable file operations; override to delegate
// to remote systems (e.g. SSH).
type WriteOperations interface {
	WriteFile(absolutePath, content string) error
	Mkdir(dir string) error
}

type localWriteOperations struct{}

func (localWriteOperations) WriteFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func (localWriteOperations) Mkdir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// DefaultWriteOperations writes to the local filesystem.
var DefaultWriteOperations WriteOperations = localWriteOperations{}

// WriteToolOptions configure the write tool.
type WriteToolOptions struct {
	// Operations override file writing; default local filesystem.
	Operations WriteOperations
}

// CreateWriteToolDefinition builds the write ToolDefinition.
func CreateWriteToolDefinition(cwd string, options *WriteToolOptions) *ToolDefinition {
	ops := WriteOperations(DefaultWriteOperations)
	if options != nil && options.Operations != nil {
		ops = options.Operations
	}
	return &ToolDefinition{
		Name:                "write",
		Label:               "write",
		Description:         "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		PromptSnippet:       WriteToolSystemPromptContribution.Snippet,
		PromptGuidelines:    append([]string(nil), WriteToolSystemPromptContribution.Guidelines...),
		Parameters:          buildWriteToolSchema(),
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
		Execute: func(toolCallID string, params any, signal *abort.Signal, onUpdate func(partial any), ctx *ExtensionContext) (any, error) {
			input, ok := params.(WriteToolInput)
			if !ok {
				if mapped, ok := mapWriteToolInput(params); ok {
					input = mapped
				} else {
					return nil, fmt.Errorf("write: invalid params %T", params)
				}
			}

			base := cwd
			if ctx != nil && ctx.Cwd != "" {
				base = ctx.Cwd
			}
			absolutePath := ResolveToCwd(input.Path, base)
			dir := filepath.Dir(absolutePath)

			var result *ToolResultText
			err := WithFileMutationQueue(absolutePath, func() error {
				// Abort is observed after each step rather than rejecting
				// from a listener, keeping the queue locked until the
				// current operation settles.
				throwIfAborted := func() error {
					if signal != nil && signal.Aborted() {
						return fmt.Errorf("Operation aborted")
					}
					return nil
				}

				if err := throwIfAborted(); err != nil {
					return err
				}
				if err := ops.Mkdir(dir); err != nil {
					return err
				}
				if err := throwIfAborted(); err != nil {
					return err
				}
				if err := ops.WriteFile(absolutePath, input.Content); err != nil {
					return err
				}
				if err := throwIfAborted(); err != nil {
					return err
				}
				result = &ToolResultText{Text: "Successfully wrote to " + input.Path}
				return nil
			})
			if err != nil {
				return nil, err
			}
			return result, nil
		},
	}
}

// CreateWriteTool wraps the definition into an AgentTool.
func CreateWriteTool(cwd string, options *WriteToolOptions) any {
	return WrapToolDefinition(CreateWriteToolDefinition(cwd, options), nil)
}

func buildWriteToolSchema() *typebox.Schema {
	return typebox.Object([]*typebox.Property{
		typebox.Prop("path", typebox.String(typebox.Description("Path to the file to write (relative or absolute)"))),
		typebox.Prop("content", typebox.String(typebox.Description("Content to write to the file"))),
	})
}

// ToolResultText is the text content of a tool result.
type ToolResultText struct {
	Text string
}

func mapWriteToolInput(params any) (WriteToolInput, bool) {
	switch typed := params.(type) {
	case map[string]any:
		path, _ := typed["path"].(string)
		content, _ := typed["content"].(string)
		return WriteToolInput{Path: path, Content: content}, true
	}
	return WriteToolInput{}, false
}
