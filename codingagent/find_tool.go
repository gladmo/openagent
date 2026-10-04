package codingagent

// Port of core/tools/find.ts: fd-backed glob file search with custom-glob
// delegation, git-repo awareness, and result limiting.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/typebox"
)

// RelativizeFindResultPath relativizes a result against the search root and
// normalizes to posix separators, preserving a trailing slash.
func RelativizeFindResultPath(resultPath, searchPath string) string {
	hadTrailingSeparator := strings.HasSuffix(resultPath, string(os.PathSeparator))
	if os.PathSeparator == '\\' && strings.HasSuffix(resultPath, "/") {
		hadTrailingSeparator = true
	}
	relativePath := resultPath
	if filepath.IsAbs(resultPath) {
		if relative, err := filepath.Rel(searchPath, resultPath); err == nil {
			relativePath = relative
		}
	}
	posixPath := strings.ReplaceAll(relativePath, string(os.PathSeparator), "/")
	if hadTrailingSeparator && !strings.HasSuffix(posixPath, "/") {
		return posixPath + "/"
	}
	return posixPath
}

// FindToolSystemPromptContribution is the find tool's prompt section.
var FindToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet: "Find files by glob pattern (respects .gitignore)",
}

// FindToolInput is the tool's validated input.
type FindToolInput struct {
	Pattern string  `json:"pattern"`
	Path    string  `json:"path,omitempty"`
	Limit   float64 `json:"limit,omitempty"`
}

// FindToolDetails carries truncation metadata for renderers.
type FindToolDetails struct {
	Truncation         *TruncationResult
	ResultLimitReached int
}

// FindGlobOptions parameterize the custom glob operation.
type FindGlobOptions struct {
	Ignore []string
	Limit  int
}

// FindOperations are the pluggable search operations; a non-nil Glob
// replaces fd entirely.
type FindOperations interface {
	Exists(absolutePath string) bool
	Glob(pattern, cwd string, options FindGlobOptions) ([]string, error)
}

type localFindOperations struct{}

func (localFindOperations) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (localFindOperations) Glob(pattern, cwd string, options FindGlobOptions) ([]string, error) {
	// Placeholder mirroring the reference: actual fd execution happens in
	// Execute when no custom glob is provided.
	return nil, nil
}

// FindToolOptions configure the find tool.
type FindToolOptions struct {
	Operations FindOperations
}

const findDefaultLimit = 1000

// FindToolOutput is the find result shape.
type FindToolOutput struct {
	Text    string
	Details *FindToolDetails
}

// CreateFindToolDefinition builds the find ToolDefinition.
func CreateFindToolDefinition(cwd string, options *FindToolOptions) *ToolDefinition {
	customOps := FindOperations(nil)
	if options != nil {
		customOps = options.Operations
	}
	return &ToolDefinition{
		Name:  "find",
		Label: "find",
		Description: fmt.Sprintf(
			"Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore. Output is truncated to %d results or %dKB (whichever is hit first).",
			findDefaultLimit, DefaultMaxBytes/1024),
		PromptSnippet: FindToolSystemPromptContribution.Snippet,
		Parameters: typebox.Object([]*typebox.Property{
			typebox.Prop("pattern", typebox.String(typebox.Description("Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'"))),
			typebox.Prop("path", typebox.Optional(typebox.String(typebox.Description("Directory to search in (default: current directory)")))),
			typebox.Prop("limit", typebox.Optional(typebox.Number(typebox.Description("Maximum number of results (default: 1000)")))),
		}),
		Execute: func(toolCallID string, params any, signal *abort.Signal, onUpdate func(partial any), ctx *ExtensionContext) (any, error) {
			if signal != nil && signal.Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}

			input, ok := params.(FindToolInput)
			if !ok {
				mapped, mappedOk := mapFindToolInput(params)
				if !mappedOk {
					return nil, fmt.Errorf("find: invalid params %T", params)
				}
				input = mapped
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
			effectiveLimit := findDefaultLimit
			if input.Limit != 0 {
				effectiveLimit = int(input.Limit)
			}

			ops := customOps
			if ops == nil {
				ops = localFindOperations{}
			}

			// Custom glob replaces fd.
			if _, isCustom := ops.(localFindOperations); !isCustom {
				if !ops.Exists(searchPath) {
					return nil, fmt.Errorf("Path not found: %s", searchPath)
				}
				if signal != nil && signal.Aborted() {
					return nil, fmt.Errorf("Operation aborted")
				}
				results, err := ops.Glob(input.Pattern, searchPath, FindGlobOptions{
					Ignore: []string{"**/node_modules/**", "**/.git/**"},
					Limit:  effectiveLimit,
				})
				if err != nil {
					return nil, err
				}
				if signal != nil && signal.Aborted() {
					return nil, fmt.Errorf("Operation aborted")
				}
				if len(results) == 0 {
					return &FindToolOutput{Text: "No files found matching pattern"}, nil
				}

				relativized := make([]string, len(results))
				for i, result := range results {
					relativized[i] = RelativizeFindResultPath(result, searchPath)
				}
				return findResultFromRelativized(relativized, effectiveLimit, true), nil
			}

			// Default implementation uses fd.
			fdPath, err := EnsureTool("fd", nil)
			if err != nil || fdPath == "" {
				return nil, fmt.Errorf("fd is not available and could not be downloaded")
			}
			if signal != nil && signal.Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}

			args := []string{"--glob", "--color=never", "--hidden"}

			// fd ignores .gitignore outside git repos without --no-require-git;
			// inside repos the default git-aware behavior keeps parent
			// .gitignore rules from crossing nested repo boundaries (pi#5960).
			insideGitRepo := false
			for current := searchPath; ; {
				if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
					insideGitRepo = true
					break
				}
				parent := filepath.Dir(current)
				if parent == current {
					break
				}
				current = parent
			}
			if !insideGitRepo {
				args = append(args, "--no-require-git")
			}
			args = append(args, "--max-results", fmt.Sprintf("%d", effectiveLimit))

			// --full-path mode needs a leading **/ for path-containing patterns.
			effectivePattern := input.Pattern
			if strings.Contains(input.Pattern, "/") {
				args = append(args, "--full-path")
				if !strings.HasPrefix(input.Pattern, "/") && !strings.HasPrefix(input.Pattern, "**/") && input.Pattern != "**" {
					effectivePattern = "**/" + input.Pattern
				}
				if runtime.GOOS == "windows" {
					effectivePattern = strings.ReplaceAll(effectivePattern, "/", `[/\\]`)
				}
			}
			args = append(args, "--", effectivePattern, searchPath)

			cmd := exec.Command(fdPath, args...)
			cmd.Stdin = nil
			stdout, pipeErr := cmd.StdoutPipe()
			if pipeErr != nil {
				return nil, fmt.Errorf("Failed to run fd: %s", pipeErr.Error())
			}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				return nil, fmt.Errorf("Failed to run fd: %s", err.Error())
			}

			var removeAbort func()
			if signal != nil {
				removeAbort = signal.OnAbort(func() {
					if cmd.Process != nil {
						_ = cmd.Process.Kill()
					}
				})
			}

			var lines []string
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
			for scanner.Scan() {
				lines = append(lines, scanner.Text())
			}
			waitErr := cmd.Wait()
			if removeAbort != nil {
				removeAbort()
			}

			if signal != nil && signal.Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}
			if waitErr != nil {
				if _, isExit := waitErr.(*exec.ExitError); isExit {
					output := strings.Join(lines, "\n")
					if output == "" {
						message := strings.TrimSpace(stderr.String())
						return nil, fmt.Errorf("%s", message)
					}
				} else {
					return nil, fmt.Errorf("Failed to run fd: %s", waitErr.Error())
				}
			}
			if len(lines) == 0 {
				return &FindToolOutput{Text: "No files found matching pattern"}, nil
			}

			var relativized []string
			for _, rawLine := range lines {
				line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
				if line == "" {
					continue
				}
				relativized = append(relativized, RelativizeFindResultPath(line, searchPath))
			}

			return findResultFromRelativized(relativized, effectiveLimit, false), nil
		},
	}
}

// findResultFromRelativized builds the output with notices; custom globs use
// a shorter notice (no refine-pattern hint).
func findResultFromRelativized(relativized []string, effectiveLimit int, customGlob bool) *FindToolOutput {
	resultLimitReached := len(relativized) >= effectiveLimit
	rawOutput := strings.Join(relativized, "\n")
	hugeLines := int(^uint(0) >> 1)
	truncation := TruncateHead(rawOutput, TruncationOptions{MaxLines: &hugeLines})
	resultOutput := truncation.Content
	details := &FindToolDetails{}
	var notices []string
	if resultLimitReached {
		if customGlob {
			notices = append(notices, fmt.Sprintf("%d results limit reached", effectiveLimit))
		} else {
			notices = append(notices, fmt.Sprintf("%d results limit reached. Use limit=%d for more, or refine pattern", effectiveLimit, effectiveLimit*2))
		}
		details.ResultLimitReached = effectiveLimit
	}
	if truncation.Truncated {
		notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(DefaultMaxBytes)))
		details.Truncation = &truncation
	}
	if len(notices) > 0 {
		resultOutput += "\n\n[" + strings.Join(notices, ". ") + "]"
	}
	if details.Truncation == nil && details.ResultLimitReached == 0 {
		details = nil
	}
	return &FindToolOutput{Text: resultOutput, Details: details}
}

// CreateFindTool wraps the definition into an AgentTool.
func CreateFindTool(cwd string, options *FindToolOptions) any {
	return WrapToolDefinition(CreateFindToolDefinition(cwd, options), nil)
}

func mapFindToolInput(params any) (FindToolInput, bool) {
	switch typed := params.(type) {
	case map[string]any:
		input := FindToolInput{}
		if pattern, ok := typed["pattern"].(string); ok {
			input.Pattern = pattern
		}
		if path, ok := typed["path"].(string); ok {
			input.Path = path
		}
		if limit, ok := toFloat(typed["limit"]); ok {
			input.Limit = limit
		}
		return input, true
	}
	return FindToolInput{}, false
}
