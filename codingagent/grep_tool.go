package codingagent

// Port of core/tools/grep.ts: ripgrep-backed content search with JSON event
// streaming, match limits, context blocks, and truncation notices.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/typebox"
)

// GrepToolSystemPromptContribution is the grep tool's prompt section.
var GrepToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet: "Search file contents for patterns (respects .gitignore)",
}

// GrepToolInput is the tool's validated input.
type GrepToolInput struct {
	Pattern    string  `json:"pattern"`
	Path       string  `json:"path,omitempty"`
	Glob       string  `json:"glob,omitempty"`
	IgnoreCase bool    `json:"ignoreCase,omitempty"`
	Literal    bool    `json:"literal,omitempty"`
	Context    float64 `json:"context,omitempty"`
	Limit      float64 `json:"limit,omitempty"`
}

// GrepToolDetails carries truncation metadata for renderers.
type GrepToolDetails struct {
	Truncation        *TruncationResult
	MatchLimitReached int
	LinesTruncated    bool
}

// GrepOperations are the pluggable search operations.
type GrepOperations interface {
	IsDirectory(absolutePath string) (bool, error)
	ReadFile(absolutePath string) (string, error)
}

type localGrepOperations struct{}

func (localGrepOperations) IsDirectory(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func (localGrepOperations) ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GrepToolOptions configure the grep tool.
type GrepToolOptions struct {
	Operations GrepOperations
}

const grepDefaultLimit = 100

// GrepToolOutput is the grep result shape.
type GrepToolOutput struct {
	Text    string
	Details *GrepToolDetails
}

type rgMatchEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		LineNumber float64 `json:"line_number"`
		Lines      struct {
			Text string `json:"text"`
		} `json:"lines"`
	} `json:"data"`
}

// CreateGrepToolDefinition builds the grep ToolDefinition.
func CreateGrepToolDefinition(cwd string, options *GrepToolOptions) *ToolDefinition {
	customOps := GrepOperations(nil)
	if options != nil {
		customOps = options.Operations
	}
	return &ToolDefinition{
		Name:  "grep",
		Label: "grep",
		Description: fmt.Sprintf(
			"Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to %d matches or %dKB (whichever is hit first). Long lines are truncated to %d chars.",
			grepDefaultLimit, DefaultMaxBytes/1024, GrepMaxLineLength),
		PromptSnippet: GrepToolSystemPromptContribution.Snippet,
		Parameters: typebox.Object([]*typebox.Property{
			typebox.Prop("pattern", typebox.String(typebox.Description("Search pattern (regex or literal string)"))),
			typebox.Prop("path", typebox.Optional(typebox.String(typebox.Description("Directory or file to search (default: current directory)")))),
			typebox.Prop("glob", typebox.Optional(typebox.String(typebox.Description("Filter files by glob pattern, e.g. '*.ts' or '**/*.spec.ts'")))),
			typebox.Prop("ignoreCase", typebox.Optional(typebox.Boolean(typebox.Description("Case-insensitive search (default: false)")))),
			typebox.Prop("literal", typebox.Optional(typebox.Boolean(typebox.Description("Treat pattern as literal string instead of regex (default: false)")))),
			typebox.Prop("context", typebox.Optional(typebox.Number(typebox.Description("Number of lines to show before and after each match (default: 0)")))),
			typebox.Prop("limit", typebox.Optional(typebox.Number(typebox.Description("Maximum number of matches to return (default: 100)")))),
		}),
		Execute: func(toolCallID string, params any, signal *abort.Signal, onUpdate func(partial any), ctx *ExtensionContext) (any, error) {
			if signal != nil && signal.Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}

			input, ok := params.(GrepToolInput)
			if !ok {
				mapped, mappedOk := mapGrepToolInput(params)
				if !mappedOk {
					return nil, fmt.Errorf("grep: invalid params %T", params)
				}
				input = mapped
			}

			rgPath, err := EnsureTool("rg", nil)
			if err != nil || rgPath == "" {
				return nil, fmt.Errorf("ripgrep (rg) is not available and could not be downloaded")
			}

			searchDir := input.Path
			if searchDir == "" {
				searchDir = "."
			}
			base := cwd
			if ctx != nil && ctx.Cwd != "" {
				base = ctx.Cwd
			}
			searchPath := ResolveToCwd(searchDir, base)

			ops := customOps
			if ops == nil {
				ops = localGrepOperations{}
			}
			isDirectory, statErr := ops.IsDirectory(searchPath)
			if statErr != nil {
				return nil, fmt.Errorf("Path not found: %s", searchPath)
			}

			contextValue := 0
			if input.Context > 0 {
				contextValue = int(input.Context)
			}
			effectiveLimit := grepDefaultLimit
			if input.Limit > 0 {
				effectiveLimit = int(input.Limit)
			} else if input.Limit < 0 {
				effectiveLimit = 1
			}
			formatPath := func(filePath string) string {
				if isDirectory {
					relative, err := filepath.Rel(searchPath, filePath)
					if err == nil && relative != "" && !strings.HasPrefix(relative, "..") {
						return strings.ReplaceAll(relative, "\\", "/")
					}
				}
				return filepath.Base(filePath)
			}

			fileCache := map[string][]string{}
			getFileLines := func(filePath string) []string {
				if lines, ok := fileCache[filePath]; ok {
					return lines
				}
				var lines []string
				if content, err := ops.ReadFile(filePath); err == nil {
					content = strings.ReplaceAll(content, "\r\n", "\n")
					content = strings.ReplaceAll(content, "\r", "\n")
					lines = strings.Split(content, "\n")
				}
				fileCache[filePath] = lines
				return lines
			}

			args := []string{"--json", "--line-number", "--color=never", "--hidden"}
			if input.IgnoreCase {
				args = append(args, "--ignore-case")
			}
			if input.Literal {
				args = append(args, "--fixed-strings")
			}
			if input.Glob != "" {
				args = append(args, "--glob", input.Glob)
			}
			args = append(args, "--", input.Pattern, searchPath)

			cmd := exec.Command(rgPath, args...)
			cmd.Stdin = nil
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				return nil, fmt.Errorf("Failed to run ripgrep: %s", err.Error())
			}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				return nil, fmt.Errorf("Failed to run ripgrep: %s", err.Error())
			}

			aborted := false
			killedDueToLimit := false
			stopChild := func(dueToLimit bool) {
				if cmd.Process != nil {
					killedDueToLimit = killedDueToLimit || dueToLimit
					_ = cmd.Process.Kill()
				}
			}
			var removeAbort func()
			if signal != nil {
				removeAbort = signal.OnAbort(func() {
					aborted = true
					stopChild(false)
				})
			}

			type rgMatch struct {
				filePath    string
				lineNumber  int
				lineText    string
				hasLineText bool
			}
			var matches []rgMatch
			matchCount := 0
			matchLimitReached := false

			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) == "" || matchCount >= effectiveLimit {
					continue
				}
				var event rgMatchEvent
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					continue
				}
				if event.Type != "match" {
					continue
				}
				matchCount++
				if event.Data.Path.Text != "" && event.Data.LineNumber > 0 {
					matches = append(matches, rgMatch{
						filePath:    event.Data.Path.Text,
						lineNumber:  int(event.Data.LineNumber),
						lineText:    event.Data.Lines.Text,
						hasLineText: true,
					})
				}
				if matchCount >= effectiveLimit {
					matchLimitReached = true
					stopChild(true)
				}
			}
			waitErr := cmd.Wait()
			if removeAbort != nil {
				removeAbort()
			}

			if aborted {
				return nil, fmt.Errorf("Operation aborted")
			}
			if !killedDueToLimit && waitErr != nil {
				if exitErr, ok := waitErr.(*exec.ExitError); ok {
					if code := exitErr.ExitCode(); code != 0 && code != 1 {
						message := strings.TrimSpace(stderr.String())
						if message == "" {
							message = fmt.Sprintf("ripgrep exited with code %d", code)
						}
						return nil, fmt.Errorf("%s", message)
					}
				} else {
					return nil, fmt.Errorf("Failed to run ripgrep: %s", waitErr.Error())
				}
			}

			if matchCount == 0 {
				return &GrepToolOutput{Text: "No matches found"}, nil
			}

			linesTruncated := false
			var outputLines []string
			for _, match := range matches {
				if contextValue == 0 && match.hasLineText {
					relativePath := formatPath(match.filePath)
					sanitized := strings.ReplaceAll(match.lineText, "\r\n", "\n")
					sanitized = strings.ReplaceAll(sanitized, "\r", "")
					sanitized = strings.TrimSuffix(sanitized, "\n")
					truncated := TruncateLine(sanitized, GrepMaxLineLength)
					if truncated.WasTruncated {
						linesTruncated = true
					}
					outputLines = append(outputLines, fmt.Sprintf("%s:%d: %s", relativePath, match.lineNumber, truncated.Text))
				} else {
					relativePath := formatPath(match.filePath)
					lines := getFileLines(match.filePath)
					if len(lines) == 0 {
						outputLines = append(outputLines, fmt.Sprintf("%s:%d: (unable to read file)", relativePath, match.lineNumber))
						continue
					}
					start := match.lineNumber
					end := match.lineNumber
					if contextValue > 0 {
						start = match.lineNumber - contextValue
						if start < 1 {
							start = 1
						}
						end = match.lineNumber + contextValue
						if end > len(lines) {
							end = len(lines)
						}
					}
					for current := start; current <= end; current++ {
						lineText := ""
						if current-1 < len(lines) {
							lineText = lines[current-1]
						}
						sanitized := strings.ReplaceAll(lineText, "\r", "")
						truncated := TruncateLine(sanitized, GrepMaxLineLength)
						if truncated.WasTruncated {
							linesTruncated = true
						}
						if current == match.lineNumber {
							outputLines = append(outputLines, fmt.Sprintf("%s:%d: %s", relativePath, current, truncated.Text))
						} else {
							outputLines = append(outputLines, fmt.Sprintf("%s-%d- %s", relativePath, current, truncated.Text))
						}
					}
				}
			}

			rawOutput := strings.Join(outputLines, "\n")
			hugeLines := int(^uint(0) >> 1)
			truncation := TruncateHead(rawOutput, TruncationOptions{MaxLines: &hugeLines})
			output := truncation.Content
			details := &GrepToolDetails{}
			var notices []string
			if matchLimitReached {
				notices = append(notices, fmt.Sprintf("%d matches limit reached. Use limit=%d for more, or refine pattern", effectiveLimit, effectiveLimit*2))
				details.MatchLimitReached = effectiveLimit
			}
			if truncation.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(DefaultMaxBytes)))
				details.Truncation = &truncation
			}
			if linesTruncated {
				notices = append(notices, fmt.Sprintf("Some lines truncated to %d chars. Use read tool to see full lines", GrepMaxLineLength))
				details.LinesTruncated = true
			}
			if len(notices) > 0 {
				output += "\n\n[" + strings.Join(notices, ". ") + "]"
			}

			if details.Truncation == nil && details.MatchLimitReached == 0 && !details.LinesTruncated {
				details = nil
			}
			return &GrepToolOutput{Text: output, Details: details}, nil
		},
	}
}

// CreateGrepTool wraps the definition into an AgentTool.
func CreateGrepTool(cwd string, options *GrepToolOptions) any {
	return WrapToolDefinition(CreateGrepToolDefinition(cwd, options), nil)
}

func mapGrepToolInput(params any) (GrepToolInput, bool) {
	switch typed := params.(type) {
	case map[string]any:
		input := GrepToolInput{}
		if pattern, ok := typed["pattern"].(string); ok {
			input.Pattern = pattern
		}
		if path, ok := typed["path"].(string); ok {
			input.Path = path
		}
		if glob, ok := typed["glob"].(string); ok {
			input.Glob = glob
		}
		if ignoreCase, ok := typed["ignoreCase"].(bool); ok {
			input.IgnoreCase = ignoreCase
		}
		if literal, ok := typed["literal"].(bool); ok {
			input.Literal = literal
		}
		if context, ok := toFloat(typed["context"]); ok {
			input.Context = context
		}
		if limit, ok := toFloat(typed["limit"]); ok {
			input.Limit = limit
		}
		return input, true
	}
	return GrepToolInput{}, false
}
