package codingagent

// Port of core/skills.ts: Agent Skills spec validation, directory
// discovery with ignore-file support, dedup with collision diagnostics,
// and the XML prompt format.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gladmo/openagent/ignore"
)

// Skill name/description limits per the Agent Skills spec.
const (
	MaxSkillNameLength        = 64
	MaxSkillDescriptionLength = 1024
)

// ignoreFileNames feed the directory walker.
var skillIgnoreFileNames = []string{".gitignore", ".ignore", ".fdignore"}

// FullSkill is the loaded skill shape.
type FullSkill struct {
	Name                   string
	Description            string
	FilePath               string
	BaseDir                string
	SourceInfo             SourceInfo
	DisableModelInvocation bool
}

// SkillDiagnostic reports one load/validation issue.
type SkillDiagnostic struct {
	Type    string // "warning" | "collision"
	Message string
	Path    string
	// Collision fields (type == "collision").
	WinnerPath string
	LoserPath  string
}

// LoadSkillsResult carries skills plus diagnostics.
type LoadSkillsResult struct {
	Skills      []FullSkill
	Diagnostics []SkillDiagnostic
}

var skillNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// validateName checks the spec's name rules.
func validateSkillName(name string) []string {
	var errors []string
	if len(name) > MaxSkillNameLength {
		errors = append(errors, fmt.Sprintf("name exceeds %d characters (%d)", MaxSkillNameLength, len(name)))
	}
	if !skillNamePattern.MatchString(name) {
		errors = append(errors, "name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)")
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		errors = append(errors, "name must not start or end with a hyphen")
	}
	if strings.Contains(name, "--") {
		errors = append(errors, "name must not contain consecutive hyphens")
	}
	return errors
}

// validateDescription checks the spec's description rules.
func validateSkillDescription(description string, hasDescription bool) []string {
	var errors []string
	if !hasDescription {
		errors = append(errors, "description is required")
		return errors
	}
	if len(description) > MaxSkillDescriptionLength {
		errors = append(errors, fmt.Sprintf("description exceeds %d characters (%d)", MaxSkillDescriptionLength, len(description)))
	}
	return errors
}

// createSkillSourceInfo maps source tags to discovery metadata.
func createSkillSourceInfo(filePath, baseDir, source string) SourceInfo {
	switch source {
	case "user":
		return CreateSyntheticSourceInfo(filePath, "local", ScopeUser, "", baseDir)
	case "project":
		return CreateSyntheticSourceInfo(filePath, "local", ScopeProject, "", baseDir)
	case "path":
		return CreateSyntheticSourceInfo(filePath, "local", "", "", baseDir)
	default:
		return CreateSyntheticSourceInfo(filePath, source, "", "", baseDir)
	}
}

// prefixIgnorePattern rewrites one ignore rule against the walk root.
func prefixIgnorePattern(line, prefix string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, `\#`) {
		return ""
	}

	pattern := line
	negated := false
	if strings.HasPrefix(pattern, "!") {
		negated = true
		pattern = pattern[1:]
	} else if strings.HasPrefix(pattern, `\!`) {
		pattern = pattern[1:]
	}
	pattern = strings.TrimPrefix(pattern, "/")

	prefixed := pattern
	if prefix != "" {
		prefixed = prefix + pattern
	}
	if negated {
		return "!" + prefixed
	}
	return prefixed
}

// addIgnoreRules loads a directory's ignore files into the matcher.
func addIgnoreRules(ig *ignore.Ignore, dir, rootDir string) {
	relativeDir, err := filepath.Rel(rootDir, dir)
	if err != nil {
		relativeDir = ""
	}
	prefix := ""
	if relativeDir != "" && relativeDir != "." {
		prefix = strings.ReplaceAll(relativeDir, string(os.PathSeparator), "/") + "/"
	}

	for _, filename := range skillIgnoreFileNames {
		ignorePath := filepath.Join(dir, filename)
		content, err := os.ReadFile(ignorePath)
		if err != nil {
			continue
		}
		var patterns []string
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if pattern := prefixIgnorePattern(line, prefix); pattern != "" {
				patterns = append(patterns, pattern)
			}
		}
		if len(patterns) > 0 {
			ig.Add(patterns...)
		}
	}
}

func toPosixPath(p string) string {
	return strings.ReplaceAll(p, string(os.PathSeparator), "/")
}

// LoadSkillsFromDir scans a directory tree for skills: a dir containing
// SKILL.md is a skill root (no recursion); otherwise root .md files load
// and subdirectories recurse looking for SKILL.md.
func LoadSkillsFromDir(dir, source string) LoadSkillsResult {
	return loadSkillsFromDirInternal(dir, source, true, nil, "")
}

func loadSkillsFromDirInternal(dir, source string, includeRootFiles bool, matcher *ignore.Ignore, rootDir string) LoadSkillsResult {
	var skills []FullSkill
	var diagnostics []SkillDiagnostic

	if _, err := os.Stat(dir); err != nil {
		return LoadSkillsResult{}
	}

	root := rootDir
	if root == "" {
		root = dir
	}
	ig := matcher
	if ig == nil {
		ig = ignore.New(nil)
	}
	addIgnoreRules(ig, dir, root)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return LoadSkillsResult{}
	}

	// A SKILL.md in this directory makes it a skill root.
	for _, entry := range entries {
		if entry.Name() != "SKILL.md" {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		isFile := entry.Type().IsRegular()
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isFile = info.Mode().IsRegular()
		}
		relPath, err := filepath.Rel(root, fullPath)
		if err != nil {
			continue
		}
		if !isFile || ig.Ignores(toPosixPath(relPath)) {
			continue
		}
		result := loadSkillFromFile(fullPath, source)
		if result.Skill != nil {
			skills = append(skills, *result.Skill)
		}
		diagnostics = append(diagnostics, result.Diagnostics...)
		return LoadSkillsResult{Skills: skills, Diagnostics: diagnostics}
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Name() == "node_modules" {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())

		isDirectory := entry.IsDir()
		isFile := entry.Type().IsRegular()
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(fullPath)
			if err != nil {
				continue // broken symlink
			}
			isDirectory = info.IsDir()
			isFile = info.Mode().IsRegular()
		}

		relPath, err := filepath.Rel(root, fullPath)
		if err != nil {
			continue
		}
		ignorePath := toPosixPath(relPath)
		if isDirectory {
			ignorePath += "/"
		}
		if ig.Ignores(ignorePath) {
			continue
		}

		if isDirectory {
			sub := loadSkillsFromDirInternal(fullPath, source, false, ig, root)
			skills = append(skills, sub.Skills...)
			diagnostics = append(diagnostics, sub.Diagnostics...)
			continue
		}

		if !isFile || !includeRootFiles || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		result := loadSkillFromFile(fullPath, source)
		if result.Skill != nil {
			skills = append(skills, *result.Skill)
		}
		diagnostics = append(diagnostics, result.Diagnostics...)
	}

	return LoadSkillsResult{Skills: skills, Diagnostics: diagnostics}
}

func loadSkillFromFile(filePath, source string) struct {
	Skill       *FullSkill
	Diagnostics []SkillDiagnostic
} {
	var diagnostics []SkillDiagnostic
	isDeclaredSkill := filepath.Base(filePath) == "SKILL.md"

	rawContent, err := os.ReadFile(filePath)
	if err != nil {
		message := "failed to read skill file"
		diagnostics = append(diagnostics, SkillDiagnostic{Type: "warning", Message: message, Path: filePath})
		return struct {
			Skill       *FullSkill
			Diagnostics []SkillDiagnostic
		}{nil, diagnostics}
	}

	parsed := ParseFrontmatter(string(rawContent))
	frontmatter := parsed.Frontmatter

	description, hasDescription := frontmatter["description"].(string)
	if hasDescription {
		hasDescription = strings.TrimSpace(description) != ""
	}
	if !isDeclaredSkill && !hasDescription {
		return struct {
			Skill       *FullSkill
			Diagnostics []SkillDiagnostic
		}{nil, diagnostics}
	}

	skillDir := filepath.Dir(filePath)
	parentDirName := filepath.Base(skillDir)

	for _, err := range validateSkillDescription(description, hasDescription) {
		diagnostics = append(diagnostics, SkillDiagnostic{Type: "warning", Message: err, Path: filePath})
	}

	name := parentDirName
	if frontmatterName, ok := frontmatter["name"].(string); ok && frontmatterName != "" {
		name = frontmatterName
	}

	for _, err := range validateSkillName(name) {
		diagnostics = append(diagnostics, SkillDiagnostic{Type: "warning", Message: err, Path: filePath})
	}

	if !hasDescription {
		return struct {
			Skill       *FullSkill
			Diagnostics []SkillDiagnostic
		}{nil, diagnostics}
	}

	return struct {
		Skill       *FullSkill
		Diagnostics []SkillDiagnostic
	}{
		Skill: &FullSkill{
			Name:                   name,
			Description:            description,
			FilePath:               filePath,
			BaseDir:                skillDir,
			SourceInfo:             createSkillSourceInfo(filePath, skillDir, source),
			DisableModelInvocation: frontmatter["disable-model-invocation"] == true,
		},
		Diagnostics: diagnostics,
	}
}

// escapeXml escapes the five XML specials.
func escapeXml(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}

// FormatSkillsForPrompt renders skills in the agentskills.io XML format;
// skills with disable-model-invocation are excluded (explicit /skill:
// commands only).
func FormatSkillsForPromptXML(skills []FullSkill, fileReadTool string) string {
	var visible []FullSkill
	for _, skill := range skills {
		if !skill.DisableModelInvocation {
			visible = append(visible, skill)
		}
	}
	if len(visible) == 0 {
		return ""
	}

	lines := []string{
		"",
		"",
		"The following skills provide specialized instructions for specific tasks.",
	}
	if fileReadTool == "bash" {
		lines = append(lines, "Use bash to load a skill's file when the task matches its description.")
	} else {
		lines = append(lines, "Use the read tool to load a skill's file when the task matches its description.")
	}
	lines = append(lines,
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	)

	for _, skill := range visible {
		lines = append(lines,
			"  <skill>",
			"    <name>"+escapeXml(skill.Name)+"</name>",
			"    <description>"+escapeXml(skill.Description)+"</description>",
			"    <location>"+escapeXml(skill.FilePath)+"</location>",
			"  </skill>",
		)
	}
	lines = append(lines, "</available_skills>")

	return strings.Join(lines, "\n")
}

// LoadSkillsOptions configure the full discovery.
type LoadSkillsOptions struct {
	Cwd             string
	AgentDir        string
	SkillPaths      []string
	IncludeDefaults bool
}

// LoadSkills discovers skills from defaults plus explicit paths with
// symlink-aware dedup and name-collision diagnostics.
func LoadSkills(options LoadSkillsOptions) LoadSkillsResult {
	resolvedCwd := ResolvePath(options.Cwd, "", PathInputOptions{})
	agentDir := options.AgentDir
	if agentDir == "" {
		agentDir = GetAgentDir()
	}
	resolvedAgentDir := ResolvePath(agentDir, "", PathInputOptions{})

	skillMap := map[string]FullSkill{}
	var skillOrder []string
	realPathSet := map[string]bool{}
	var allDiagnostics []SkillDiagnostic
	var collisionDiagnostics []SkillDiagnostic

	addSkills := func(result LoadSkillsResult) {
		allDiagnostics = append(allDiagnostics, result.Diagnostics...)
		for _, skill := range result.Skills {
			realPath := CanonicalizePath(skill.FilePath)
			if realPathSet[realPath] {
				continue
			}
			if existing, ok := skillMap[skill.Name]; ok {
				collisionDiagnostics = append(collisionDiagnostics, SkillDiagnostic{
					Type:       "collision",
					Message:    fmt.Sprintf("name %q collision", skill.Name),
					Path:       skill.FilePath,
					WinnerPath: existing.FilePath,
					LoserPath:  skill.FilePath,
				})
				continue
			}
			skillMap[skill.Name] = skill
			skillOrder = append(skillOrder, skill.Name)
			realPathSet[realPath] = true
		}
	}

	if options.IncludeDefaults {
		addSkills(loadSkillsFromDirInternal(filepath.Join(resolvedAgentDir, "skills"), "user", true, nil, ""))
		addSkills(loadSkillsFromDirInternal(filepath.Join(resolvedCwd, ConfigDirName(), "skills"), "project", true, nil, ""))
	}

	userSkillsDir := filepath.Join(resolvedAgentDir, "skills")
	projectSkillsDir := filepath.Join(resolvedCwd, ConfigDirName(), "skills")

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

	getSource := func(resolvedPath string) string {
		if !options.IncludeDefaults {
			if isUnderPath(resolvedPath, userSkillsDir) {
				return "user"
			}
			if isUnderPath(resolvedPath, projectSkillsDir) {
				return "project"
			}
		}
		return "path"
	}

	for _, rawPath := range options.SkillPaths {
		resolvedPath := ResolvePath(rawPath, resolvedCwd, PathInputOptions{Trim: true})
		info, err := os.Stat(resolvedPath)
		if err != nil {
			allDiagnostics = append(allDiagnostics, SkillDiagnostic{Type: "warning", Message: "skill path does not exist", Path: resolvedPath})
			continue
		}

		source := getSource(resolvedPath)
		if info.IsDir() {
			addSkills(loadSkillsFromDirInternal(resolvedPath, source, true, nil, ""))
			continue
		}
		if info.Mode().IsRegular() && strings.HasSuffix(resolvedPath, ".md") {
			result := loadSkillFromFile(resolvedPath, source)
			if result.Skill != nil {
				addSkills(LoadSkillsResult{Skills: []FullSkill{*result.Skill}, Diagnostics: result.Diagnostics})
			} else {
				allDiagnostics = append(allDiagnostics, result.Diagnostics...)
			}
			continue
		}
		allDiagnostics = append(allDiagnostics, SkillDiagnostic{Type: "warning", Message: "skill path is not a markdown file", Path: resolvedPath})
	}

	skills := make([]FullSkill, 0, len(skillOrder))
	for _, name := range skillOrder {
		skills = append(skills, skillMap[name])
	}
	return LoadSkillsResult{
		Skills:      skills,
		Diagnostics: append(allDiagnostics, collisionDiagnostics...),
	}
}
