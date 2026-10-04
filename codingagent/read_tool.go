package codingagent

// Port of core/tools/read.ts: file reading with image attachment support,
// offset/limit windows, and truncation notices.

import (
	"fmt"
	"os"
	"strings"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/typebox"
)

// ReadToolSystemPromptContribution is the read tool's prompt section.
var ReadToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet:    "Read file contents",
	Guidelines: []string{"Use read to examine files instead of cat or sed."},
}

// ReadToolInput is the tool's validated input.
type ReadToolInput struct {
	Path   string  `json:"path"`
	Offset float64 `json:"offset,omitempty"`
	Limit  float64 `json:"limit,omitempty"`
}

// ReadToolDetails carries truncation metadata for renderers.
type ReadToolDetails struct {
	Truncation *TruncationResult
}

// ReadOperations are the pluggable read operations.
type ReadOperations interface {
	ReadFile(absolutePath string) ([]byte, error)
	Access(absolutePath string) error
	DetectImageMimeType(absolutePath string) string
}

type localReadOperations struct{}

func (localReadOperations) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (localReadOperations) Access(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return file.Close()
}

func (localReadOperations) DetectImageMimeType(path string) string {
	return DetectSupportedImageMimeTypeFromFile(path)
}

// DefaultReadOperations reads from the local filesystem.
var DefaultReadOperations ReadOperations = localReadOperations{}

// ReadToolOptions configure the read tool.
type ReadToolOptions struct {
	// AutoResizeImages resizes images to inline provider limits (default true).
	AutoResizeImages *bool
	// ResizeOptions is the fallback profile when no model metadata exists.
	ResizeOptions *ImageResizeOptions
	// Operations override file reading; default local filesystem.
	Operations ReadOperations
}

// ReadToolOutput is the result shape (text plus optional image content).
type ReadToolOutput struct {
	Text      string
	ImageData string // base64
	MimeType  string
	Details   *ReadToolDetails
}

func getNonVisionImageNote(supportsImages bool) string {
	if supportsImages {
		return ""
	}
	return "[Current model does not support images. The image will be omitted from this request.]"
}

// CreateReadToolDefinition builds the read ToolDefinition.
func CreateReadToolDefinition(cwd string, options *ReadToolOptions) *ToolDefinition {
	autoResizeImages := true
	var fallbackResizeOptions *ImageResizeOptions
	ops := ReadOperations(DefaultReadOperations)
	if options != nil {
		if options.AutoResizeImages != nil {
			autoResizeImages = *options.AutoResizeImages
		}
		fallbackResizeOptions = options.ResizeOptions
		if options.Operations != nil {
			ops = options.Operations
		}
	}

	return &ToolDefinition{
		Name:  "read",
		Label: "read",
		Description: fmt.Sprintf(
			"Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.",
			DefaultMaxLines, DefaultMaxBytes/1024),
		PromptSnippet:    ReadToolSystemPromptContribution.Snippet,
		PromptGuidelines: append([]string(nil), ReadToolSystemPromptContribution.Guidelines...),
		Parameters: typebox.Object([]*typebox.Property{
			typebox.Prop("path", typebox.String(typebox.Description("Path to the file to read (relative or absolute)"))),
			typebox.Prop("offset", typebox.Optional(typebox.Number(typebox.Description("Line number to start reading from (1-indexed)")))),
			typebox.Prop("limit", typebox.Optional(typebox.Number(typebox.Description("Maximum number of lines to read")))),
		}),
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
		Execute: func(toolCallID string, params any, signal *abort.Signal, onUpdate func(partial any), ctx *ExtensionContext) (any, error) {
			if signal != nil && signal.Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}

			input, ok := params.(ReadToolInput)
			if !ok {
				mapped, mappedOk := mapReadToolInput(params)
				if !mappedOk {
					return nil, fmt.Errorf("read: invalid params %T", params)
				}
				input = mapped
			}

			base := cwd
			if ctx != nil && ctx.Cwd != "" {
				base = ctx.Cwd
			}
			absolutePath := ResolveReadPath(input.Path, base)

			if err := ops.Access(absolutePath); err != nil {
				return nil, err
			}

			mimeType := ops.DetectImageMimeType(absolutePath)
			// Model image-input support: the full extension context lands
			// with the extensions phase; vision models are the default.
			nonVisionImageNote := getNonVisionImageNote(true)

			if mimeType != "" {
				buffer, err := ops.ReadFile(absolutePath)
				if err != nil {
					return nil, err
				}
				resizeOptions := fallbackResizeOptions
				autoResize := autoResizeImages
				processed := ProcessImage(buffer, mimeType, &ProcessImageOptions{AutoResizeImages: &autoResize, ResizeOptions: resizeOptions})
				if !processed.OK {
					textNote := "Read image file [" + mimeType + "]\n" + processed.Message
					if nonVisionImageNote != "" {
						textNote += "\n" + nonVisionImageNote
					}
					return &ReadToolOutput{Text: textNote}, nil
				}
				textNote := "Read image file [" + processed.MimeType + "]"
				if len(processed.Hints) > 0 {
					textNote += "\n" + strings.Join(processed.Hints, "\n")
				}
				if nonVisionImageNote != "" {
					textNote += "\n" + nonVisionImageNote
				}
				return &ReadToolOutput{Text: textNote, ImageData: processed.Data, MimeType: processed.MimeType}, nil
			}

			buffer, err := ops.ReadFile(absolutePath)
			if err != nil {
				return nil, err
			}
			textContent := string(buffer)
			allLines := strings.Split(textContent, "\n")
			totalFileLines := len(allLines)

			startLine := 0
			if input.Offset != 0 {
				startLine = int(input.Offset) - 1
				if startLine < 0 {
					startLine = 0
				}
			}
			startLineDisplay := startLine + 1
			if startLine >= len(allLines) {
				return nil, fmt.Errorf("Offset %d is beyond end of file (%d lines total)", int(input.Offset), len(allLines))
			}

			var selectedContent string
			userLimitedLines := -1
			if input.Limit != 0 {
				endLine := startLine + int(input.Limit)
				if endLine > len(allLines) {
					endLine = len(allLines)
				}
				if endLine < startLine {
					// Negative limits (model-supplied) must not slice
					// backwards; clamp like grep does.
					endLine = startLine
				}
				selectedContent = strings.Join(allLines[startLine:endLine], "\n")
				userLimitedLines = endLine - startLine
			} else {
				selectedContent = strings.Join(allLines[startLine:], "\n")
			}

			truncation := TruncateHead(selectedContent, TruncationOptions{})
			var outputText string
			var details *ReadToolDetails
			switch {
			case truncation.FirstLineExceedsLimit:
				firstLineSize := FormatSize(int64(len(allLines[startLine])))
				outputText = fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]",
					startLineDisplay, firstLineSize, FormatSize(DefaultMaxBytes), startLineDisplay, input.Path, DefaultMaxBytes)
				details = &ReadToolDetails{Truncation: &truncation}
			case truncation.Truncated:
				endLineDisplay := startLineDisplay + truncation.OutputLines - 1
				nextOffset := endLineDisplay + 1
				outputText = truncation.Content
				if truncation.TruncatedBy == "lines" {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]",
						startLineDisplay, endLineDisplay, totalFileLines, nextOffset)
				} else {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]",
						startLineDisplay, endLineDisplay, totalFileLines, FormatSize(DefaultMaxBytes), nextOffset)
				}
				details = &ReadToolDetails{Truncation: &truncation}
			case userLimitedLines >= 0 && startLine+userLimitedLines < len(allLines):
				remaining := len(allLines) - (startLine + userLimitedLines)
				nextOffset := startLine + userLimitedLines + 1
				outputText = fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]",
					truncation.Content, remaining, nextOffset)
			default:
				outputText = truncation.Content
			}

			if signal != nil && signal.Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}
			return &ReadToolOutput{Text: outputText, Details: details}, nil
		},
	}
}

// CreateReadTool wraps the definition into an AgentTool.
func CreateReadTool(cwd string, options *ReadToolOptions) any {
	return WrapToolDefinition(CreateReadToolDefinition(cwd, options), nil)
}

func mapReadToolInput(params any) (ReadToolInput, bool) {
	switch typed := params.(type) {
	case map[string]any:
		input := ReadToolInput{}
		if path, ok := typed["path"].(string); ok {
			input.Path = path
		}
		if offset, ok := toFloat(typed["offset"]); ok {
			input.Offset = offset
		}
		if limit, ok := toFloat(typed["limit"]); ok {
			input.Limit = limit
		}
		return input, true
	case ReadToolInput:
		return typed, true
	}
	return ReadToolInput{}, false
}

func toFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	}
	return 0, false
}
