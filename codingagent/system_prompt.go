package codingagent

// Port of core/system-prompt.ts: structured system-prompt construction,
// project context, rules, and section diffing.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Skill is the prompt-facing skill shape (skills.ts lands in full later).
type Skill struct {
	Name        string
	Description string
}

// BuildSystemPromptOptions configure prompt construction.
type BuildSystemPromptOptions struct {
	// CustomPrompt replaces the default preamble.
	CustomPrompt string
	// ForceSystemPrompt is an exact full-prompt replacement.
	ForceSystemPrompt *string
	// SelectedTools default to read/bash/edit/write.
	SelectedTools []string
	// ToolSnippets maps tool names to one-line snippets.
	ToolSnippets map[string]string
	// ToolGuidelines maps tool names to guideline bullets.
	ToolGuidelines map[string][]string
	// PromptGuidelines append to the default rules.
	PromptGuidelines []string
	// AppendSystemPrompt is appended before project context/skills/cwd.
	AppendSystemPrompt string
	// Sections are additional XML-wrapped sections keyed by tag.
	Sections map[string]string
	// Cwd is the working directory.
	Cwd string
	// ContextFiles are pre-loaded project instruction files.
	ContextFiles []ContextFile
	// Skills are pre-loaded skills.
	Skills []Skill
	// HiddenTools are selected tools whose declarations requests leave out;
	// they are reachable only through another tool, so the tool list and
	// rules leave them out too.
	HiddenTools []string
}

// ContextFile is one pre-loaded project instruction file.
type ContextFile struct {
	Path    string
	Content string
}

// NormalizedBuildSystemPromptOptions is the collection-complete shape.
type NormalizedBuildSystemPromptOptions struct {
	BuildSystemPromptOptions
	SelectedTools      []string
	ToolSnippets       map[string]string
	ToolGuidelines     map[string][]string
	PromptGuidelines   []string
	AppendSystemPrompt string
	Sections           map[string]string
	ContextFiles       []ContextFile
	Skills             []Skill
	// HiddenTools are selected tools whose declarations requests leave out
	// (prepareLoadout hidden declarations). They are reachable only through
	// another tool, so the tool list and rules leave them out too.
	HiddenTools []string
}

// SystemPromptSections are the ordered, replaceable prompt sections;
// "preamble" is untagged text, others wrap in same-named tags.
type SystemPromptSections map[string]string

var systemPromptSectionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// NormalizeBuildSystemPromptOptions fills the defaults and copies inputs.
func NormalizeBuildSystemPromptOptions(input BuildSystemPromptOptions) NormalizedBuildSystemPromptOptions {
	selectedTools := input.SelectedTools
	if selectedTools == nil {
		selectedTools = []string{"read", "bash", "edit", "write"}
	} else {
		selectedTools = append([]string(nil), selectedTools...)
	}
	toolSnippets := map[string]string{}
	for key, value := range input.ToolSnippets {
		toolSnippets[key] = value
	}
	toolGuidelines := map[string][]string{}
	for key, values := range input.ToolGuidelines {
		toolGuidelines[key] = append([]string(nil), values...)
	}
	sections := map[string]string{}
	for key, value := range input.Sections {
		sections[key] = value
	}
	contextFiles := append([]ContextFile(nil), input.ContextFiles...)
	skills := append([]Skill(nil), input.Skills...)
	hiddenTools := append([]string(nil), input.HiddenTools...)
	return NormalizedBuildSystemPromptOptions{
		BuildSystemPromptOptions: input,
		SelectedTools:            selectedTools,
		ToolSnippets:             toolSnippets,
		ToolGuidelines:           toolGuidelines,
		PromptGuidelines:         append([]string(nil), input.PromptGuidelines...),
		AppendSystemPrompt:       input.AppendSystemPrompt,
		Sections:                 sections,
		ContextFiles:             contextFiles,
		Skills:                   skills,
		HiddenTools:              hiddenTools,
	}
}

func renderProjectContext(contextFiles []ContextFile) string {
	parts := []string{"Project-specific instructions and guidelines:"}
	for _, file := range contextFiles {
		parts = append(parts, fmt.Sprintf("<project_instructions path=%q>\n%s\n</project_instructions>", file.Path, file.Content))
	}
	return strings.Join(parts, "\n\n")
}

func buildRules(selectedTools []string, toolGuidelines map[string][]string, promptGuidelines []string) string {
	var rules []string
	seen := map[string]bool{}
	addRule := func(rule string) {
		normalized := strings.TrimSpace(rule)
		if normalized == "" || seen[normalized] {
			return
		}
		seen[normalized] = true
		rules = append(rules, normalized)
	}

	hasTool := func(name string) bool {
		for _, tool := range selectedTools {
			if tool == name {
				return true
			}
		}
		return false
	}
	hasBash := hasTool("bash")
	hasPowerShell := hasTool("powershell")
	hasGrep := hasTool("grep")
	hasFind := hasTool("find")
	hasLs := hasTool("ls")

	if (hasBash || hasPowerShell) && !hasGrep && !hasFind && !hasLs {
		switch {
		case hasBash && hasPowerShell:
			addRule("Use bash or PowerShell for file operations like listing, searching, and finding files")
		case hasPowerShell:
			addRule("Use PowerShell for file operations like listing, searching, and finding files")
		default:
			addRule("Use bash for file operations like ls, rg, find")
		}
	}

	for _, name := range selectedTools {
		for _, rule := range toolGuidelines[name] {
			addRule(rule)
		}
	}
	for _, rule := range promptGuidelines {
		addRule(rule)
	}
	addRule("Be concise in your responses")
	addRule("Show file paths clearly when working with files")

	rendered := make([]string, len(rules))
	for i, rule := range rules {
		rendered[i] = "- " + rule
	}
	return strings.Join(rendered, "\n")
}

// FormatSkillsForPrompt renders skills for the skills section.
func FormatSkillsForPrompt(skills []Skill, fileReadTool string) string {
	if len(skills) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "Available skills:")
	lines = append(lines, "")
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	sort.Strings(names)
	byName := map[string]Skill{}
	for _, skill := range skills {
		byName[skill.Name] = skill
	}
	for _, name := range names {
		skill := byName[name]
		// "indirect" names no tool: the reader is reachable only through
		// another tool.
		reader := ""
		if fileReadTool == "indirect" {
			reader = "Load a skill's file when the task matches its description."
		} else {
			reader = fmt.Sprintf("use the %s tool to read it", fileReadTool)
		}
		if skill.Description != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s (%s)", name, skill.Description, reader))
		} else {
			lines = append(lines, fmt.Sprintf("- %s (%s)", name, reader))
		}
	}
	return strings.Join(lines, "\n")
}

// BuildSystemPromptSections builds the ordered, replaceable sections.
func BuildSystemPromptSections(input BuildSystemPromptOptions) (SystemPromptSections, error) {
	options := NormalizeBuildSystemPromptOptions(input)

	for name := range options.Sections {
		if !systemPromptSectionNamePattern.MatchString(name) || name == "preamble" {
			return nil, fmt.Errorf("Invalid system prompt section name: %s", name)
		}
	}

	promptSections := SystemPromptSections{}
	if options.CustomPrompt != "" {
		promptSections["preamble"] = options.CustomPrompt
	} else {
		promptSections["preamble"] = "You are an expert coding assistant operating inside pi, a coding agent harness. You help users by reading files, executing commands, editing code, and writing new files."

		declaredTools := make([]string, 0, len(options.SelectedTools))
		for _, name := range options.SelectedTools {
			if !containsStringIn(options.HiddenTools, name) {
				declaredTools = append(declaredTools, name)
			}
		}
		var visibleTools []string
		for _, name := range declaredTools {
			if snippet := options.ToolSnippets[name]; snippet != "" {
				visibleTools = append(visibleTools, name)
			}
		}
		tools := "(none)"
		if len(visibleTools) > 0 {
			var lines []string
			for _, name := range visibleTools {
				lines = append(lines, "- "+name+": "+options.ToolSnippets[name])
			}
			tools = strings.Join(lines, "\n")
		}
		promptSections["tools"] = tools + "\n\nIn addition to the tools above, you may have access to other custom tools depending on the project."
		promptSections["rules"] = buildRules(declaredToolsForRules(options), options.ToolGuidelines, options.PromptGuidelines)
		promptSections["docs"] = fmt.Sprintf(`Pi documentation (read only when the user asks about pi itself, its SDK, extensions, themes, skills, or TUI):
- Main documentation: %s
- Additional docs: %s
- Examples: %s (extensions, custom tools, SDK)
- When reading pi docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory
- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), pi packages (docs/packages.md), environment variables (docs/environment-variables.md)
- When working on pi topics, read the docs and examples, and follow .md cross-references before implementing
- Always read pi .md files completely and follow links to related docs (e.g., tui.md for TUI API details)`, GetReadmePath(), GetDocsPath(), GetExamplesPath())
	}

	if options.AppendSystemPrompt != "" {
		promptSections["addendum"] = options.AppendSystemPrompt
	}
	if len(options.ContextFiles) > 0 {
		promptSections["project_context"] = renderProjectContext(options.ContextFiles)
	}
	// A hidden reader is still reachable through another tool, so skills
	// stay but the hint names no tool.
	skillFileReadTool := ""
	readerSelected := false
	for _, tool := range []string{"read", "bash"} {
		for _, selected := range options.SelectedTools {
			if selected == tool {
				readerSelected = true
				if !containsStringIn(options.HiddenTools, tool) {
					skillFileReadTool = tool
				}
				break
			}
		}
		if skillFileReadTool != "" {
			break
		}
	}
	if skillFileReadTool == "" && readerSelected {
		skillFileReadTool = "indirect"
	}
	if skillFileReadTool != "" && len(options.Skills) > 0 {
		skillsPrompt := strings.TrimSpace(FormatSkillsForPrompt(options.Skills, skillFileReadTool))
		if skillsPrompt != "" {
			promptSections["skills"] = skillsPrompt
		}
	}
	promptSections["cwd"] = strings.ReplaceAll(options.Cwd, "\\", "/")

	// Custom sections append after cwd in sorted order (matching JS object
	// insertion semantics for the collected keys).
	var customNames []string
	for name := range options.Sections {
		customNames = append(customNames, name)
	}
	sort.Strings(customNames)
	for _, name := range customNames {
		if content := options.Sections[name]; content != "" {
			promptSections[name] = content
		}
	}

	sections := SystemPromptSections{"preamble": promptSections["preamble"]}
	sectionNames := make([]string, 0, len(promptSections))
	for name := range promptSections {
		sectionNames = append(sectionNames, name)
	}
	sort.Strings(sectionNames)
	for _, name := range sectionNames {
		if name == "preamble" {
			continue
		}
		sections[name] = "<" + name + ">\n" + promptSections[name] + "\n</" + name + ">"
	}
	return sections, nil
}

// SystemPromptState is either forced content or structured sections.
type SystemPromptState struct {
	Content  string
	Sections SystemPromptSections
}

// BuildSystemPromptState resolves the prompt state (forced prompts are
// opaque; otherwise sections carry the prompt).
func BuildSystemPromptState(input BuildSystemPromptOptions) (*SystemPromptState, error) {
	if input.ForceSystemPrompt != nil {
		return &SystemPromptState{Content: *input.ForceSystemPrompt}, nil
	}
	sections, err := BuildSystemPromptSections(input)
	if err != nil {
		return nil, err
	}
	return &SystemPromptState{Sections: sections}, nil
}

// DiffSystemPromptSections diffs the model's current sections against the
// desired ones; nil when unchanged.
func DiffSystemPromptSections(previous map[string]*string, current SystemPromptSections) map[string]*string {
	patch := map[string]*string{}
	for name, text := range current {
		existing, ok := previous[name]
		if !ok || existing == nil || *existing != text {
			copied := text
			patch[name] = &copied
		}
	}
	for name := range previous {
		if _, ok := current[name]; !ok {
			patch[name] = nil
		}
	}
	if len(patch) == 0 {
		return nil
	}
	return patch
}

func containsStringIn(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func declaredToolsForRules(options NormalizedBuildSystemPromptOptions) []string {
	declared := make([]string, 0, len(options.SelectedTools))
	for _, name := range options.SelectedTools {
		if !containsStringIn(options.HiddenTools, name) {
			declared = append(declared, name)
		}
	}
	return declared
}
