package tools

// tools_builtin.go ports read.ts, write.ts, tool-context.ts and
// file-mutation-queue.ts.

import (
	"fmt"

	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gladmo/openagent/jsonx"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/typebox"
)

// ExecutionToolContext is the filesystem/shell context required by the
// built-in tools.
type ExecutionToolContext struct {
	Env harness.ExecutionEnv
}

// harnessTool adapts a plain execute function into a harness tool.
type harnessTool struct {
	name        string
	label       string
	description string
	parameters  *typebox.Schema
	execute     func(toolCallID string, params any, onUpdate harness.AgentHarnessToolUpdateCallback, toolContext any, invocation harness.AgentHarnessToolInvocation, ctx harness.Context) (*agent.AgentToolResult, error)
}

func (t *harnessTool) tool() *harness.AgentHarnessTool {
	return &harness.AgentHarnessTool{
		AgentTool: agent.AgentTool{
			Name:        t.name,
			Label:       t.label,
			Description: t.description,
			Parameters:  t.parameters,
		},
		Execute: t.execute,
	}
}

// ---------------------------------------------------------------------------
// file-mutation-queue.ts
// ---------------------------------------------------------------------------

type mutationQueueEntry struct {
	chained chan struct{}
}

type mutationQueueState struct {
	mu     sync.Mutex
	queues map[string]*mutationQueueEntry
}

var (
	mutationStatesMu sync.Mutex
	mutationStates   = map[harness.ExecutionEnv]*mutationQueueState{}
)

func getMutationState(env harness.ExecutionEnv) *mutationQueueState {
	mutationStatesMu.Lock()
	defer mutationStatesMu.Unlock()
	if state, ok := mutationStates[env]; ok {
		return state
	}
	state := &mutationQueueState{queues: map[string]*mutationQueueEntry{}}
	mutationStates[env] = state
	return state
}

func getMutationQueueKey(env harness.ExecutionEnv, path string, ctx harness.Context) (string, error) {
	absoluteResult := env.AbsolutePath(path, ctx)
	if !absoluteResult.Ok {
		panic(absoluteResult.Error)
	}
	canonical := env.CanonicalPath(absoluteResult.Value, ctx)
	if canonical.Ok {
		return canonical.Value, nil
	}
	if canonical.Error.Code == harness.FileErrNotFound || canonical.Error.Code == harness.FileErrNotSupported {
		return absoluteResult.Value, nil
	}
	return "", canonical.Error
}

// WithFileMutationQueue serializes file mutations targeting the same
// environment and canonical path.
func WithFileMutationQueue(env harness.ExecutionEnv, path string, fn func() (*agent.AgentToolResult, error), ctx harness.Context) (*agent.AgentToolResult, error) {
	state := getMutationState(env)
	key, err := getMutationQueueKey(env, path, ctx)
	if err != nil {
		return nil, err
	}
	state.mu.Lock()
	current := state.queues[key]
	entry := &mutationQueueEntry{chained: make(chan struct{})}
	state.queues[key] = entry
	state.mu.Unlock()

	if current != nil {
		<-current.chained
	}
	defer func() {
		close(entry.chained)
		state.mu.Lock()
		if state.queues[key] == entry {
			delete(state.queues, key)
		}
		state.mu.Unlock()
	}()
	return fn()
}

// ---------------------------------------------------------------------------
// write.ts
// ---------------------------------------------------------------------------

// CreateWriteTool builds the write tool.
func CreateWriteTool() *harness.AgentHarnessTool {
	schema := typebox.Object([]*typebox.Property{
		typebox.Prop("path", typebox.String(typebox.Description("Path to the file to write (relative or absolute)"))),
		typebox.Prop("content", typebox.String(typebox.Description("Content to write to the file"))),
	})
	tool := &harnessTool{
		name:        "write",
		label:       "write",
		description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		parameters:  schema,
	}
	tool.execute = func(_ string, params any, _ harness.AgentHarnessToolUpdateCallback, toolContext any, _ harness.AgentHarnessToolInvocation, ctx harness.Context) (*agent.AgentToolResult, error) {
		argsObj, _ := params.(*jsonx.Obj)
		writePath, _ := argsObj.Get("path")
		writeContent, _ := argsObj.Get("content")
		_ = writePath
		_ = writeContent
		env := toolContext.(ExecutionToolContext).Env
		path := writePath.(string)
		content := writeContent.(string)
		absolutePath, err := ResolveToolPath(env, path, ctx)
		if err != nil {
			return nil, err
		}
		return WithFileMutationQueue(env, absolutePath, func() (*agent.AgentToolResult, error) {
			if ctx.AbortSignal().Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}
			if result := env.WriteFile(absolutePath, []byte(content), ctx); !result.Ok {
				panic(result.Error)
			}
			if ctx.AbortSignal().Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}
			return &agent.AgentToolResult{
				Content: []ai.ContentBlock{ai.TextContent{Text: "Successfully wrote to " + path}},
			}, nil
		}, ctx)
	}
	return tool.tool()
}

// ---------------------------------------------------------------------------
// read.ts
// ---------------------------------------------------------------------------

// ReadImageProcessorResult mirrors the TS union.
type ReadImageProcessorResult struct {
	Ok       bool
	Data     string
	MimeType string
	Hints    []string
	Message  string
}

// ReadImageProcessor mirrors the TS type.
type ReadImageProcessor func(bytes []byte, mimeType string, options struct {
	AutoResizeImages bool
}, ctx harness.Context) ReadImageProcessorResult

// ReadToolOptions mirrors the TS interface.
type ReadToolOptions struct {
	AutoResizeImages *bool
	ImageProcessor   ReadImageProcessor
}

// ReadToolDetails carries the truncation metadata.
type ReadToolDetails struct {
	Truncation *harness.TruncationResult
}

type readToolParams struct {
	Path   string   `json:"path"`
	Offset *float64 `json:"offset"`
	Limit  *float64 `json:"limit"`
}

// CreateReadTool builds the read tool.
func CreateReadTool(options ...ReadToolOptions) *harness.AgentHarnessTool {
	opts := ReadToolOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	autoResize := true
	if opts.AutoResizeImages != nil {
		autoResize = *opts.AutoResizeImages
	}
	schema := typebox.Object([]*typebox.Property{
		typebox.Prop("path", typebox.String(typebox.Description("Path to the file to read (relative or absolute)"))),
		typebox.Prop("offset", typebox.Optional(typebox.Number(typebox.Description("Line number to start reading from (1-indexed)")))),
		typebox.Prop("limit", typebox.Optional(typebox.Number(typebox.Description("Maximum number of lines to read")))),
	})
	tool := &harnessTool{
		name:  "read",
		label: "read",
		description: fmt.Sprintf(
			"Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.",
			harness.DefaultMaxLines, harness.DefaultMaxBytes/1024),
		parameters: schema,
	}
	tool.execute = func(_ string, params any, _ harness.AgentHarnessToolUpdateCallback, toolContext any, _ harness.AgentHarnessToolInvocation, ctx harness.Context) (*agent.AgentToolResult, error) {
		args := decodeReadParams(params)
		env := toolContext.(ExecutionToolContext).Env
		absolutePath, err := ResolveReadToolPath(env, args.Path, ctx)
		if err != nil {
			return nil, err
		}
		readResult := env.ReadBinaryFile(absolutePath, ctx)
		if !readResult.Ok {
			panic(readResult.Error)
		}
		bytes := readResult.Value
		mimeType := DetectSupportedImageMimeType(bytes)
		if mimeType != "" {
			if opts.ImageProcessor != nil {
				processed := opts.ImageProcessor(bytes, mimeType, struct{ AutoResizeImages bool }{autoResize}, ctx)
				if !processed.Ok {
					return &agent.AgentToolResult{
						Content: []ai.ContentBlock{ai.TextContent{Text: "Read image file [" + mimeType + "]\n" + processed.Message}},
					}, nil
				}
				hints := ""
				if len(processed.Hints) > 0 {
					hints = "\n" + strings.Join(processed.Hints, "\n")
				}
				return &agent.AgentToolResult{
					Content: []ai.ContentBlock{
						ai.TextContent{Text: "Read image file [" + processed.MimeType + "]" + hints},
						ai.ImageContent{Data: processed.Data, MimeType: processed.MimeType},
					},
				}, nil
			}
			if mimeType == "image/bmp" {
				return &agent.AgentToolResult{
					Content: []ai.ContentBlock{ai.TextContent{Text: "Read image file [image/bmp]\n[Image omitted: configure an imageProcessor to convert BMP images.]"}},
				}, nil
			}
			return &agent.AgentToolResult{
				Content: []ai.ContentBlock{
					ai.TextContent{Text: "Read image file [" + mimeType + "]"},
					ai.ImageContent{Data: EncodeBase64(bytes), MimeType: mimeType},
				},
			}, nil
		}

		textContent := string(bytes)
		allLines := strings.Split(textContent, "\n")
		totalFileLines := len(allLines)
		startLine := 0
		if args.Offset != nil {
			startLine = int(*args.Offset) - 1
			if startLine < 0 {
				startLine = 0
			}
		}
		startLineDisplay := startLine + 1
		if startLine >= len(allLines) {
			return nil, fmt.Errorf("Offset %v is beyond end of file (%d lines total)", *args.Offset, len(allLines))
		}

		var selectedContent string
		userLimitedLines := -1
		if args.Limit != nil {
			endLine := startLine + int(*args.Limit)
			if endLine > len(allLines) {
				endLine = len(allLines)
			}
			selectedContent = strings.Join(allLines[startLine:endLine], "\n")
			userLimitedLines = endLine - startLine
		} else {
			selectedContent = strings.Join(allLines[startLine:], "\n")
		}

		truncation := harness.TruncateHead(selectedContent, nil)
		var outputText string
		var details any
		setDetails := func() { details = ReadToolDetails{Truncation: &truncation} }
		switch {
		case truncation.FirstLineExceedsLimit:
			firstLineSize := harness.FormatSize(int64(utf8.RuneCountInString(allLines[startLine])))
			outputText = fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]",
				startLineDisplay, firstLineSize, harness.FormatSize(harness.DefaultMaxBytes), startLineDisplay, args.Path, harness.DefaultMaxBytes)
			setDetails()
		case truncation.Truncated:
			endLineDisplay := startLineDisplay + truncation.OutputLines - 1
			nextOffset := endLineDisplay + 1
			outputText = truncation.Content
			if truncation.TruncatedBy == "lines" {
				outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]", startLineDisplay, endLineDisplay, totalFileLines, nextOffset)
			} else {
				outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]",
					startLineDisplay, endLineDisplay, totalFileLines, harness.FormatSize(harness.DefaultMaxBytes), nextOffset)
			}
			setDetails()
		case userLimitedLines >= 0 && startLine+userLimitedLines < len(allLines):
			remaining := len(allLines) - (startLine + userLimitedLines)
			nextOffset := startLine + userLimitedLines + 1
			outputText = fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]", truncation.Content, remaining, nextOffset)
		default:
			outputText = truncation.Content
		}
		return &agent.AgentToolResult{
			Content: []ai.ContentBlock{ai.TextContent{Text: outputText}},
			Details: details,
		}, nil
	}
	return tool.tool()
}

func decodeReadParams(params any) readToolParams {
	var out readToolParams
	obj, ok := params.(*jsonx.Obj)
	if !ok {
		return out
	}
	if v, ok := obj.Get("path"); ok {
		if s, ok := v.(string); ok {
			out.Path = s
		}
	}
	if v, ok := obj.Get("offset"); ok {
		if f, ok := v.(float64); ok {
			out.Offset = &f
		}
	}
	if v, ok := obj.Get("limit"); ok {
		if f, ok := v.(float64); ok {
			out.Limit = &f
		}
	}
	return out
}
