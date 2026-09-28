package harness

// prompt_templates.go ports harness/prompt-templates.ts.

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// PromptTemplateDiagnosticCode values.
const (
	TemplateDiagFileInfoFailed = "file_info_failed"
	TemplateDiagListFailed     = "list_failed"
	TemplateDiagReadFailed     = "read_failed"
	TemplateDiagParseFailed    = "parse_failed"
)

// PromptTemplateDiagnostic mirrors the TS interface.
type PromptTemplateDiagnostic struct {
	Type    string
	Code    string
	Message string
	Path    string
}

type templateFrontmatter struct {
	Description  string `yaml:"description"`
	ArgumentHint string `yaml:"argument-hint"`
}

// LoadPromptTemplatesResult is the loader return.
type LoadPromptTemplatesResult struct {
	PromptTemplates []PromptTemplate
	Diagnostics     []PromptTemplateDiagnostic
}

// LoadPromptTemplates loads templates from directories (non-recursive .md)
// or explicit .md files.
func LoadPromptTemplates(env ExecutionEnv, paths []string, ctx Context) LoadPromptTemplatesResult {
	templates := []PromptTemplate{}
	diagnostics := []PromptTemplateDiagnostic{}
	for _, path := range paths {
		infoResult := env.FileInfo(path, ctx)
		if !infoResult.Ok {
			if infoResult.Error.Code != FileErrNotFound {
				diagnostics = append(diagnostics, PromptTemplateDiagnostic{"warning", TemplateDiagFileInfoFailed, infoResult.Error.Message, path})
			}
			continue
		}
		kind := resolveKindForTemplate(env, infoResult.Value, &diagnostics, ctx)
		if kind == FileKindDirectory {
			result := loadTemplatesFromDir(env, infoResult.Value.Path, ctx)
			templates = append(templates, result.PromptTemplates...)
			diagnostics = append(diagnostics, result.Diagnostics...)
		} else if kind == FileKindFile && strings.HasSuffix(infoResult.Value.Name, ".md") {
			result := loadTemplateFromFile(env, infoResult.Value.Path, infoResult.Value.Name, ctx)
			if result.promptTemplate != nil {
				templates = append(templates, *result.promptTemplate)
			}
			diagnostics = append(diagnostics, result.diagnostics...)
		}
	}
	return LoadPromptTemplatesResult{templates, diagnostics}
}

// resolveKindForTemplate is resolveKind parameterized over diagnostics.
func resolveKindForTemplate(env ExecutionEnv, info FileInfo, diagnostics *[]PromptTemplateDiagnostic, ctx Context) string {
	if info.Kind == FileKindFile || info.Kind == FileKindDirectory {
		return info.Kind
	}
	canonicalPath := env.CanonicalPath(info.Path, ctx)
	if !canonicalPath.Ok {
		if canonicalPath.Error.Code != FileErrNotFound {
			*diagnostics = append(*diagnostics, PromptTemplateDiagnostic{"warning", TemplateDiagFileInfoFailed, canonicalPath.Error.Message, info.Path})
		}
		return ""
	}
	target := env.FileInfo(canonicalPath.Value, ctx)
	if !target.Ok {
		if target.Error.Code != FileErrNotFound {
			*diagnostics = append(*diagnostics, PromptTemplateDiagnostic{"warning", TemplateDiagFileInfoFailed, target.Error.Message, info.Path})
		}
		return ""
	}
	if target.Value.Kind == FileKindFile || target.Value.Kind == FileKindDirectory {
		return target.Value.Kind
	}
	return ""
}

func loadTemplatesFromDir(env ExecutionEnv, dir string, ctx Context) LoadPromptTemplatesResult {
	templates := []PromptTemplate{}
	diagnostics := []PromptTemplateDiagnostic{}
	entriesResult := env.ListDir(dir, ctx)
	if !entriesResult.Ok {
		diagnostics = append(diagnostics, PromptTemplateDiagnostic{"warning", TemplateDiagListFailed, entriesResult.Error.Message, dir})
		return LoadPromptTemplatesResult{templates, diagnostics}
	}
	sorted := append([]FileInfo{}, entriesResult.Value...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, entry := range sorted {
		kind := resolveKindForTemplate(env, entry, &diagnostics, ctx)
		if kind != FileKindFile || !strings.HasSuffix(entry.Name, ".md") {
			continue
		}
		result := loadTemplateFromFile(env, entry.Path, entry.Name, ctx)
		if result.promptTemplate != nil {
			templates = append(templates, *result.promptTemplate)
		}
		diagnostics = append(diagnostics, result.diagnostics...)
	}
	return LoadPromptTemplatesResult{templates, diagnostics}
}

type loadTemplateFileResult struct {
	promptTemplate *PromptTemplate
	diagnostics    []PromptTemplateDiagnostic
}

var mdSuffixRe = regexp.MustCompile(`(?i)\.md$`)

func loadTemplateFromFile(env ExecutionEnv, filePath, fileName string, ctx Context) loadTemplateFileResult {
	diagnostics := []PromptTemplateDiagnostic{}
	rawContent := env.ReadTextFile(filePath, ctx)
	if !rawContent.Ok {
		diagnostics = append(diagnostics, PromptTemplateDiagnostic{"warning", TemplateDiagReadFailed, rawContent.Error.Message, filePath})
		return loadTemplateFileResult{nil, diagnostics}
	}

	frontmatter, body, ok := parseTemplateFrontmatter(rawContent.Value)
	if !ok {
		diagnostics = append(diagnostics, PromptTemplateDiagnostic{"warning", TemplateDiagParseFailed, "invalid frontmatter", filePath})
		return loadTemplateFileResult{nil, diagnostics}
	}

	description := frontmatter.Description
	if description == "" {
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) != "" {
				description = line
				if len([]rune(line)) > 60 {
					runes := []rune(line)
					description = string(runes[:60]) + "..."
				}
				break
			}
		}
	}
	_ = frontmatter.ArgumentHint
	return loadTemplateFileResult{&PromptTemplate{
		Name:           mdSuffixRe.ReplaceAllString(fileName, ""),
		Description:    description,
		HasDescription: description != "",
		Content:        body,
	}, diagnostics}
}

func parseTemplateFrontmatter(content string) (templateFrontmatter, string, bool) {
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	if !strings.HasPrefix(normalized, "---") {
		return templateFrontmatter{}, normalized, true
	}
	endIndex := strings.Index(normalized[3:], "\n---")
	if endIndex == -1 {
		return templateFrontmatter{}, normalized, true
	}
	yamlString := normalized[4 : 3+endIndex]
	body := strings.TrimSpace(normalized[3+endIndex+4:])
	var fm templateFrontmatter
	if err := yaml.Unmarshal([]byte(yamlString), &fm); err != nil {
		return templateFrontmatter{}, "", false
	}
	return fm, body, true
}

// LoadSourcedPromptTemplates loads templates with provenance tags.
func LoadSourcedPromptTemplates[TSource any](
	env ExecutionEnv,
	inputs []struct {
		Path   string
		Source TSource
	},
	mapTemplate func(template PromptTemplate, source TSource, ctx Context) PromptTemplate,
	ctx Context,
) (templates []struct {
	PromptTemplate PromptTemplate
	Source         TSource
}, diagnostics []struct {
	Diagnostic PromptTemplateDiagnostic
	Source     TSource
}) {
	for _, input := range inputs {
		result := LoadPromptTemplates(env, []string{input.Path}, ctx)
		for _, template := range result.PromptTemplates {
			mapped := template
			if mapTemplate != nil {
				mapped = mapTemplate(template, input.Source, ctx)
			}
			templates = append(templates, struct {
				PromptTemplate PromptTemplate
				Source         TSource
			}{mapped, input.Source})
		}
		for _, d := range result.Diagnostics {
			diagnostics = append(diagnostics, struct {
				Diagnostic PromptTemplateDiagnostic
				Source     TSource
			}{d, input.Source})
		}
	}
	return templates, diagnostics
}

// ParseCommandArgs parses shell-style single/double quoted arguments.
func ParseCommandArgs(argsString string) []string {
	args := []string{}
	current := ""
	inQuote := byte(0)
	for i := 0; i < len(argsString); i++ {
		ch := argsString[i]
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			} else {
				current += string(ch)
			}
		} else if ch == '"' || ch == '\'' {
			inQuote = ch
		} else if ch == ' ' || ch == '\t' {
			if current != "" {
				args = append(args, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		args = append(args, current)
	}
	return args
}

var (
	positionalArgRe = regexp.MustCompile(`\$(\d+)`)
	sliceArgRe      = regexp.MustCompile(`\$\{@:(\d+)(?::(\d+))?\}`)
)

// SubstituteArgs substitutes $N, ${@:N}, ${@:N:L}, $ARGUMENTS and $@.
func SubstituteArgs(content string, args []string) string {
	result := positionalArgRe.ReplaceAllStringFunc(content, func(match string) string {
		n, _ := strconv.Atoi(match[1:])
		if n >= 1 && n <= len(args) {
			return args[n-1]
		}
		return ""
	})
	result = sliceArgRe.ReplaceAllStringFunc(result, func(match string) string {
		sub := sliceArgRe.FindStringSubmatch(match)
		start, _ := strconv.Atoi(sub[1])
		start--
		if start < 0 {
			start = 0
		}
		if sub[2] != "" {
			length, _ := strconv.Atoi(sub[2])
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
	})
	allArgs := strings.Join(args, " ")
	result = strings.ReplaceAll(result, "$ARGUMENTS", allArgs)
	result = strings.ReplaceAll(result, "$@", allArgs)
	return result
}

// FormatPromptTemplateInvocation formats an invocation with arguments.
func FormatPromptTemplateInvocation(template PromptTemplate, args ...string) string {
	return SubstituteArgs(template.Content, args)
}
