package codingagent

// Port of core/tools/tool-definition-wrapper.ts, file-mutation-queue.ts, and
// the ToolDefinition surface of core/extensions/types.ts. The full
// extensions/types.ts port lands with the extensions phase; these pieces are
// the tools subsystem's foundation.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/tui"
	"github.com/gladmo/openagent/typebox"
)

// =============================================================================
// ToolDefinition (extensions/types.ts subset)
// =============================================================================

// ConstrainedSamplingConfig requests provider-side structured output.
type ConstrainedSamplingConfig struct {
	Type   string // "json_schema"
	Strict string // "prefer" | "required"
}

// ToolExecutionMode is "sequential" | "parallel".
type ToolExecutionMode string

const (
	ToolExecutionSequential ToolExecutionMode = "sequential"
	ToolExecutionParallel   ToolExecutionMode = "parallel"
)

// ExtensionContext carries the per-tool execution context (cwd plus the
// extension host surface; the full interface lands with extensions).
type ExtensionContext struct {
	// Cwd is the working directory for relative paths.
	Cwd string
}

// ToolRenderContext carries state to renderers.
type ToolRenderContext struct {
	Args          any
	ToolCallID    string
	State         map[string]any
	Cwd           string
	LastComponent any
	// Invalidate re-renders the owning component and requests a frame.
	Invalidate       func()
	ExecutionStarted bool
	ArgsComplete     bool
	IsPartial        bool
	Expanded         bool
	ShowImages       bool
	IsError          bool
}

// ToolDefinition describes an LLM-callable tool.
type ToolDefinition struct {
	Name        string
	Label       string
	Description string
	// PromptSnippet is a one-line note for the Available-tools section of the
	// default system prompt.
	PromptSnippet string
	// PromptGuidelines are bullets appended to the default guidelines.
	PromptGuidelines []string
	// Parameters is the TypeBox parameter schema.
	Parameters any
	// ConstrainedSampling is an optional provider-side structured-output
	// request; nil disables the field.
	ConstrainedSampling *ConstrainedSamplingConfig
	// RenderShell is "default" or "self" ("" = default).
	RenderShell string
	// RenderCall builds the tool-call row (nil = fallback rendering).
	RenderCall func(args any, context *ToolRenderContext) tui.Component
	// RenderResult builds the result view (nil = fallback rendering).
	RenderResult func(result *ToolExecutionResult, options ToolRenderResultOptions, context *ToolRenderContext) tui.Component
	// PrepareArguments is an optional shim applied before validation.
	PrepareArguments func(args any) any
	// ExecutionMode overrides the default execution mode ("" = default).
	ExecutionMode *ToolExecutionMode
	// Execute runs the tool.
	Execute func(toolCallID string, params any, signal *abort.Signal, onUpdate func(partial any), ctx *ExtensionContext) (any, error)
}

// =============================================================================
// tool-definition-wrapper.ts
// =============================================================================

// AgentToolForDefinition is the agent-package tool specialized for
// ToolDefinition-backed execution (execute bridges into the definition with
// the extension context).
type AgentToolForDefinition struct {
	agent.AgentTool
	Definition *ToolDefinition
	CtxFactory func() *ExtensionContext
}

// WrapToolDefinition converts a ToolDefinition into an AgentTool.
func WrapToolDefinition(definition *ToolDefinition, ctxFactory func() *ExtensionContext) *agent.AgentTool {
	wrapped := &AgentToolForDefinition{
		AgentTool: agent.AgentTool{
			Name:        definition.Name,
			Label:       definition.Label,
			Description: definition.Description,
			Parameters:  definition.Parameters.(*typebox.Schema),
		},
		Definition: definition,
		CtxFactory: ctxFactory,
	}
	wrapped.Execute = func(toolCallID string, params any, signal *abort.Signal, onUpdate agent.AgentToolUpdateCallback) (*agent.AgentToolResult, error) {
		var ctx *ExtensionContext
		if ctxFactory != nil {
			ctx = ctxFactory()
		}
		result, err := definition.Execute(toolCallID, params, signal, func(partial any) {
			if onUpdate != nil {
				onUpdate(nil)
			}
		}, ctx)
		if err != nil {
			return nil, err
		}
		return toAgentToolResult(result), nil
	}
	return &wrapped.AgentTool
}

// WrapToolDefinitions converts several ToolDefinitions into AgentTools.
func WrapToolDefinitions(definitions []*ToolDefinition, ctxFactory func() *ExtensionContext) []*agent.AgentTool {
	tools := make([]*agent.AgentTool, len(definitions))
	for i, definition := range definitions {
		tools[i] = WrapToolDefinition(definition, ctxFactory)
	}
	return tools
}

// toAgentToolResult maps a definition result into the agent result shape.
func toAgentToolResult(result any) *agent.AgentToolResult {
	if result == nil {
		return &agent.AgentToolResult{}
	}
	if typed, ok := result.(*agent.AgentToolResult); ok {
		return typed
	}

	// Built-in tools return their own output shapes; convert instead of
	// silently dropping content (an empty result starves the model of
	// file contents, command output, and edit confirmations).
	var text, imageData, mimeType string
	var details any
	switch typed := result.(type) {
	case *BashToolOutput:
		text, details = typed.Text, typed.Details
	case *EditToolOutput:
		text, details = typed.Text, typed.Details
	case *ReadToolOutput:
		text, imageData, mimeType, details = typed.Text, typed.ImageData, typed.MimeType, typed.Details
	case *FindToolOutput:
		text, details = typed.Text, typed.Details
	case *GrepToolOutput:
		text, details = typed.Text, typed.Details
	case *LsToolOutput:
		text, details = typed.Text, typed.Details
	case *ToolResultText:
		text = typed.Text
	default:
		// Unknown shapes surface as an explicit error result rather than
		// an empty success.
		return &agent.AgentToolResult{
			Content: []ai.ContentBlock{ai.TextContent{Text: fmt.Sprintf("tool returned unsupported output type %T", result)}},
		}
	}

	content := []ai.ContentBlock{ai.TextContent{Text: text}}
	if imageData != "" {
		content = append(content, ai.ImageContent{Data: imageData, MimeType: mimeType})
	}
	return &agent.AgentToolResult{Content: content, Details: details}
}

// CreateToolDefinitionFromAgentTool synthesizes a minimal ToolDefinition
// from a plain AgentTool.
func CreateToolDefinitionFromAgentTool(tool *agent.AgentTool) *ToolDefinition {
	return &ToolDefinition{
		Name:             tool.Name,
		Label:            tool.Label,
		Description:      tool.Description,
		Parameters:       tool.Parameters,
		PrepareArguments: tool.PrepareArguments,
		ExecutionMode:    toolExecutionModeFromAgent(tool.ExecutionMode),
		Execute: func(toolCallID string, params any, signal *abort.Signal, onUpdate func(partial any), _ *ExtensionContext) (any, error) {
			result, err := tool.Execute(toolCallID, params, signal, nil)
			if err != nil {
				return nil, err
			}
			return result, nil
		},
	}
}

// =============================================================================
// file-mutation-queue.ts
// =============================================================================

type fileMutationEntry struct {
	done    chan struct{}
	waiters int
}

// sharedCompletedMutationEntry is the head of fresh queues: already done,
// never deleted (its waiter count is unused).
var sharedCompletedMutationEntry = &fileMutationEntry{done: func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()}

var (
	fileMutationMu    sync.Mutex
	fileMutationQueue = map[string]*fileMutationEntry{}
)

func isMissingPathError(err error) bool {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return os.IsNotExist(pathError) || errors.Is(pathError.Err, errNotDir)
	}
	return false
}

// errNotDir captures ENOTDIR for the missing-path check.
var errNotDir = syscallENOTDIR()

func getMutationQueueKey(filePath string) (string, error) {
	resolvedPath := mustAbs(filePath)
	real, err := filepath.EvalSymlinks(resolvedPath)
	if err != nil {
		if isMissingPathError(err) {
			return resolvedPath, nil
		}
		return "", err
	}
	return real, nil
}

// WithFileMutationQueue serializes mutations targeting the same file;
// operations for different files run in parallel. Queue entries are removed
// only once no registrant still waits on them, so a late registration can
// never bypass a draining chain.
func WithFileMutationQueue(filePath string, fn func() error) error {
	key, err := getMutationQueueKey(filePath)
	if err != nil {
		return err
	}

	fileMutationMu.Lock()
	tail, ok := fileMutationQueue[key]
	if !ok {
		tail = sharedCompletedMutationEntry
	}
	tail.waiters++
	mine := &fileMutationEntry{done: make(chan struct{})}
	fileMutationQueue[key] = mine
	fileMutationMu.Unlock()

	<-tail.done
	defer func() {
		fileMutationMu.Lock()
		if tail != sharedCompletedMutationEntry {
			tail.waiters--
			if tail.waiters == 0 && fileMutationQueue[key] == mine {
				delete(fileMutationQueue, key)
			}
		} else if fileMutationQueue[key] == mine {
			// No chain formed behind the shared head; drop our own entry
			// when nothing replaced it.
			delete(fileMutationQueue, key)
		}
		close(mine.done)
		fileMutationMu.Unlock()
	}()

	return fn()
}

func mustAbs(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return absolute
}

// toolExecutionModeFromAgent converts the agent package's string mode.
func toolExecutionModeFromAgent(mode *string) *ToolExecutionMode {
	if mode == nil {
		return nil
	}
	converted := ToolExecutionMode(*mode)
	return &converted
}
