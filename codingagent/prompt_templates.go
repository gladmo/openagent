package codingagent

// Port of core/prompt-templates.ts: markdown prompt templates with bash-
// style argument parsing and substitution, plus slash-command expansion.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// PromptTemplate is one loaded prompt template.
type PromptTemplate struct {
	Name         string
	Description  string
	ArgumentHint string
	Content      string
	SourceInfo   SourceInfo
	FilePath     string
}

// ParseCommandArgs splits args respecting double/single quotes.
func ParseCommandArgs(argsString string) []string {
	var args []string
	current := ""
	inQuote := byte(0)

	for i := 0; i < len(argsString); i++ {
		char := argsString[i]
		switch {
		case inQuote != 0:
			if char == inQuote {
				inQuote = 0
			} else {
				current += string(char)
			}
		case char == '"' || char == '\'':
			inQuote = char
		case char == ' ' || char == '\t' || char == '\n' || char == '\r' || char == '\v' || char == '\f':
			if current != "" {
				args = append(args, current)
				current = ""
			}
		default:
			current += string(char)
		}
	}
	if current != "" {
		args = append(args, current)
	}
	return args
}

// argPattern matches every substitution form:
// ${N|ARGUMENTS|@:-default}, ${@:N}, ${@:N:L}, and $N/$@/$ARGUMENTS.
var argPattern = regexp.MustCompile(`\$\{(\d+|ARGUMENTS|@):-([^}]*)\}|\$\{@:(\d+)(?::(\d+))?\}|\$(ARGUMENTS|@|\d+)`)

// SubstituteArgs expands positional and whole-args placeholders. Values
// are not recursively substituted.
func SubstituteArgs(content string, args []string) string {
	allArgs := strings.Join(args, " ")

	return argPattern.ReplaceAllStringFunc(content, func(match string) string {
		sub := argPattern.FindStringSubmatch(match)
		// Group layout: 1 defaultTarget, 2 defaultValue, 3 sliceStart,
		// 4 sliceLength, 5 simple.
		defaultTarget, defaultValue, sliceStart, sliceLength, simple := sub[1], sub[2], sub[3], sub[4], sub[5]

		if defaultTarget != "" {
			var value string
			if defaultTarget == "@" || defaultTarget == "ARGUMENTS" {
				value = allArgs
			} else {
				index, _ := strconv.Atoi(defaultTarget)
				if index >= 1 && index <= len(args) {
					value = args[index-1]
				}
			}
			if value != "" {
				return value
			}
			return defaultValue
		}

		if sliceStart != "" {
			start, _ := strconv.Atoi(sliceStart)
			start-- // 1-indexed input to 0-indexed
			if start < 0 {
				start = 0
			}
			if sliceLength != "" {
				length, _ := strconv.Atoi(sliceLength)
				end := start + length
				if end > len(args) {
					end = len(args)
				}
				if start > len(args) {
					start = len(args)
				}
				return strings.Join(args[start:end], " ")
			}
			if start > len(args) {
				start = len(args)
			}
			return strings.Join(args[start:], " ")
		}

		if simple == "ARGUMENTS" || simple == "@" {
			return allArgs
		}
		index, _ := strconv.Atoi(simple)
		if index >= 1 && index <= len(args) {
			return args[index-1]
		}
		return ""
	})
}

// loadTemplateFromFile loads one .md file into a template.
func loadTemplateFromFile(filePath string, sourceInfo SourceInfo) struct {
	Template    *PromptTemplate
	Diagnostics []SkillDiagnostic
} {
	var diagnostics []SkillDiagnostic
	rawContent, err := os.ReadFile(filePath)
	if err != nil {
		diagnostics = append(diagnostics, SkillDiagnostic{Type: "warning", Message: "failed to read prompt template file", Path: filePath})
		return struct {
			Template    *PromptTemplate
			Diagnostics []SkillDiagnostic
		}{nil, diagnostics}
	}

	parsed := ParseFrontmatter(string(rawContent))

	name := strings.TrimSuffix(filepath.Base(filePath), ".md")

	// Description from frontmatter or first non-empty body line.
	description, _ := parsed.Frontmatter["description"].(string)
	if description == "" {
		for _, line := range strings.Split(parsed.Body, "\n") {
			if strings.TrimSpace(line) != "" {
				runes := []rune(line)
				if len(runes) > 60 {
					description = string(runes[:60]) + "..."
				} else {
					description = line
				}
				break
			}
		}
	}

	argumentHint, _ := parsed.Frontmatter["argument-hint"].(string)
	return struct {
		Template    *PromptTemplate
		Diagnostics []SkillDiagnostic
	}{
		Template: &PromptTemplate{
			Name:         name,
			Description:  description,
			ArgumentHint: argumentHint,
			Content:      parsed.Body,
			SourceInfo:   sourceInfo,
			FilePath:     filePath,
		},
		Diagnostics: diagnostics,
	}
}

// loadTemplatesFromDir scans one directory (non-recursive) for .md files.
func loadTemplatesFromDir(dir string, getSourceInfo func(filePath string) SourceInfo) struct {
	Templates   []PromptTemplate
	Diagnostics []SkillDiagnostic
} {
	var templates []PromptTemplate
	var diagnostics []SkillDiagnostic

	if _, err := os.Stat(dir); err != nil {
		return struct {
			Templates   []PromptTemplate
			Diagnostics []SkillDiagnostic
		}{templates, diagnostics}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return struct {
			Templates   []PromptTemplate
			Diagnostics []SkillDiagnostic
		}{templates, diagnostics}
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())
		isFile := entry.Type().IsRegular()
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(fullPath)
			if err != nil {
				continue // broken symlink
			}
			isFile = info.Mode().IsRegular()
		}
		if isFile && strings.HasSuffix(entry.Name(), ".md") {
			result := loadTemplateFromFile(fullPath, getSourceInfo(fullPath))
			if result.Template != nil {
				templates = append(templates, *result.Template)
			}
			diagnostics = append(diagnostics, result.Diagnostics...)
		}
	}

	return struct {
		Templates   []PromptTemplate
		Diagnostics []SkillDiagnostic
	}{templates, diagnostics}
}

// LoadPromptTemplatesOptions configure discovery.
type LoadPromptTemplatesOptions struct {
	Cwd             string
	AgentDir        string
	PromptPaths     []string
	IncludeDefaults bool
}

// LoadPromptTemplatesResult carries templates plus diagnostics.
type LoadPromptTemplatesResult struct {
	Templates   []PromptTemplate
	Diagnostics []SkillDiagnostic
}

// LoadPromptTemplates loads from global, project, and explicit paths.
func LoadPromptTemplates(options LoadPromptTemplatesOptions) LoadPromptTemplatesResult {
	resolvedCwd := ResolvePath(options.Cwd, "", PathInputOptions{})
	resolvedAgentDir := ResolvePath(options.AgentDir, "", PathInputOptions{})

	var templates []PromptTemplate
	var diagnostics []SkillDiagnostic
	addResult := func(result struct {
		Templates   []PromptTemplate
		Diagnostics []SkillDiagnostic
	}) {
		templates = append(templates, result.Templates...)
		diagnostics = append(diagnostics, result.Diagnostics...)
	}

	globalPromptsDir := filepath.Join(resolvedAgentDir, "prompts")
	projectPromptsDir := filepath.Join(resolvedCwd, ConfigDirName(), "prompts")

	isUnderPath := func(target, root string) bool {
		normalizedRoot := ResolvePath(root, "", PathInputOptions{})
		if target == normalizedRoot {
			return true
		}
		suffix := string(os.PathSeparator)
		if !strings.HasSuffix(normalizedRoot, suffix) {
			normalizedRoot += suffix
		}
		return strings.HasPrefix(target, normalizedRoot)
	}

	getSourceInfo := func(resolvedPath string) SourceInfo {
		if isUnderPath(resolvedPath, globalPromptsDir) {
			return CreateSyntheticSourceInfo(resolvedPath, "local", ScopeUser, "", globalPromptsDir)
		}
		if isUnderPath(resolvedPath, projectPromptsDir) {
			return CreateSyntheticSourceInfo(resolvedPath, "local", ScopeProject, "", projectPromptsDir)
		}
		baseDir := filepath.Dir(resolvedPath)
		if info, err := os.Stat(resolvedPath); err == nil && info.IsDir() {
			baseDir = resolvedPath
		}
		return CreateSyntheticSourceInfo(resolvedPath, "local", "", "", baseDir)
	}

	if options.IncludeDefaults {
		addResult(loadTemplatesFromDir(globalPromptsDir, getSourceInfo))
		addResult(loadTemplatesFromDir(projectPromptsDir, getSourceInfo))
	}

	for _, rawPath := range options.PromptPaths {
		resolvedPath := ResolvePath(rawPath, resolvedCwd, PathInputOptions{Trim: true})
		info, err := os.Stat(resolvedPath)
		if err != nil {
			continue
		}
		if info.IsDir() {
			addResult(loadTemplatesFromDir(resolvedPath, getSourceInfo))
			continue
		}
		if info.Mode().IsRegular() && strings.HasSuffix(resolvedPath, ".md") {
			result := loadTemplateFromFile(resolvedPath, getSourceInfo(resolvedPath))
			if result.Template != nil {
				templates = append(templates, *result.Template)
			}
			diagnostics = append(diagnostics, result.Diagnostics...)
		}
	}

	return LoadPromptTemplatesResult{Templates: templates, Diagnostics: diagnostics}
}

// promptCommandPattern splits /name and the rest.
var promptCommandPattern = regexp.MustCompile(`^/([^\s]+)(?:\s+([\s\S]*))?$`)

// ExpandPromptTemplate expands /name args when name matches a template.
func ExpandPromptTemplate(text string, templates []PromptTemplate) string {
	if !strings.HasPrefix(text, "/") {
		return text
	}
	match := promptCommandPattern.FindStringSubmatch(text)
	if match == nil {
		return text
	}
	templateName := match[1]
	argsString := match[2]

	for i := range templates {
		if templates[i].Name == templateName {
			args := ParseCommandArgs(argsString)
			return SubstituteArgs(templates[i].Content, args)
		}
	}
	return text
}

var _ = fmt.Sprintf
