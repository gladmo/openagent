package codingagent

// Port of core/tools/ls.ts: directory listing with entry and byte limits.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/typebox"
)

// LsToolSystemPromptContribution is the ls tool's prompt section.
var LsToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet: "List directory contents",
}

// LsToolInput is the tool's validated input.
type LsToolInput struct {
	Path  string  `json:"path,omitempty"`
	Limit float64 `json:"limit,omitempty"`
}

// LsToolDetails carries truncation metadata for renderers.
type LsToolDetails struct {
	Truncation        *TruncationResult
	EntryLimitReached int
}

// LsOperations are the pluggable listing operations.
type LsOperations interface {
	Exists(absolutePath string) bool
	Stat(absolutePath string) (bool, error) // (isDirectory, error)
	Readdir(absolutePath string) ([]string, error)
}

type localLsOperations struct{}

func (localLsOperations) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (localLsOperations) Stat(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func (localLsOperations) Readdir(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names, nil
}

// DefaultLsOperations lists the local filesystem.
var DefaultLsOperations LsOperations = localLsOperations{}

// LsToolOptions configure the ls tool.
type LsToolOptions struct {
	Operations LsOperations
}

const lsDefaultLimit = 500

// LsToolOutput is the ls result shape.
type LsToolOutput struct {
	Text    string
	Details *LsToolDetails
}

// CreateLsToolDefinition builds the ls ToolDefinition.
func CreateLsToolDefinition(cwd string, options *LsToolOptions) *ToolDefinition {
	ops := LsOperations(DefaultLsOperations)
	if options != nil && options.Operations != nil {
		ops = options.Operations
	}
	return &ToolDefinition{
		Name:  "ls",
		Label: "ls",
		Description: fmt.Sprintf(
			"List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to %d entries or %dKB (whichever is hit first).",
			lsDefaultLimit, DefaultMaxBytes/1024),
		PromptSnippet: LsToolSystemPromptContribution.Snippet,
		Parameters: typebox.Object([]*typebox.Property{
			typebox.Prop("path", typebox.Optional(typebox.String(typebox.Description("Directory to list (default: current directory)")))),
			typebox.Prop("limit", typebox.Optional(typebox.Number(typebox.Description("Maximum number of entries to return (default: 500)")))),
		}),
		Execute: func(toolCallID string, params any, signal *abort.Signal, onUpdate func(partial any), ctx *ExtensionContext) (any, error) {
			if signal != nil && signal.Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}

			input, ok := params.(LsToolInput)
			if !ok {
				mapped, mappedOk := mapLsToolInput(params)
				if !mappedOk {
					return nil, fmt.Errorf("ls: invalid params %T", params)
				}
				input = mapped
			}

			path := input.Path
			if path == "" {
				path = "."
			}
			base := cwd
			if ctx != nil && ctx.Cwd != "" {
				base = ctx.Cwd
			}
			dirPath := ResolveToCwd(path, base)
			effectiveLimit := lsDefaultLimit
			if input.Limit != 0 {
				effectiveLimit = int(input.Limit)
			}

			if !ops.Exists(dirPath) {
				return nil, fmt.Errorf("Path not found: %s", dirPath)
			}

			isDirectory, err := ops.Stat(dirPath)
			if err != nil {
				return nil, err
			}
			if !isDirectory {
				return nil, fmt.Errorf("Not a directory: %s", dirPath)
			}

			entries, err := ops.Readdir(dirPath)
			if err != nil {
				return nil, fmt.Errorf("Cannot read directory: %s", err.Error())
			}

			// Alphabetical, case-insensitive.
			sort.SliceStable(entries, func(a, b int) bool {
				return strings.ToLower(entries[a]) < strings.ToLower(entries[b])
			})

			var results []string
			entryLimitReached := false
			for _, entry := range entries {
				if len(results) >= effectiveLimit {
					entryLimitReached = true
					break
				}
				fullPath := filepath.Join(dirPath, entry)
				entryIsDir, statErr := ops.Stat(fullPath)
				if statErr != nil {
					// Skip entries we cannot stat.
					continue
				}
				suffix := ""
				if entryIsDir {
					suffix = "/"
				}
				results = append(results, entry+suffix)
			}

			if signal != nil && signal.Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}

			if len(results) == 0 {
				return &LsToolOutput{Text: "(empty directory)"}, nil
			}

			rawOutput := strings.Join(results, "\n")
			// Byte truncation only; entry count is already capped.
			hugeLines := int(^uint(0) >> 1)
			truncation := TruncateHead(rawOutput, TruncationOptions{MaxLines: &hugeLines})
			output := truncation.Content
			details := &LsToolDetails{}
			var notices []string
			if entryLimitReached {
				notices = append(notices, fmt.Sprintf("%d entries limit reached. Use limit=%d for more", effectiveLimit, effectiveLimit*2))
				details.EntryLimitReached = effectiveLimit
			}
			if truncation.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(DefaultMaxBytes)))
				details.Truncation = &truncation
			}
			if len(notices) > 0 {
				output += "\n\n[" + strings.Join(notices, ". ") + "]"
			}

			if details.Truncation == nil && details.EntryLimitReached == 0 {
				details = nil
			}
			return &LsToolOutput{Text: output, Details: details}, nil
		},
	}
}

// CreateLsTool wraps the definition into an AgentTool.
func CreateLsTool(cwd string, options *LsToolOptions) any {
	return WrapToolDefinition(CreateLsToolDefinition(cwd, options), nil)
}

func mapLsToolInput(params any) (LsToolInput, bool) {
	switch typed := params.(type) {
	case map[string]any:
		input := LsToolInput{}
		if path, ok := typed["path"].(string); ok {
			input.Path = path
		}
		if limit, ok := toFloat(typed["limit"]); ok {
			input.Limit = limit
		}
		return input, true
	}
	return LsToolInput{}, false
}
