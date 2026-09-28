package harness

// skills.go ports harness/skills.ts + system-prompt.ts.

import (
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gladmo/openagent/ignore"
)

// Skill limits.
const (
	MaxSkillNameLength        = 64
	MaxSkillDescriptionLength = 1024
)

// ignoreFileNames mirrors IGNORE_FILE_NAMES.
var skillIgnoreFileNames = []string{".gitignore", ".ignore", ".fdignore"}

// SkillDiagnosticCode values.
const (
	SkillDiagFileInfoFailed  = "file_info_failed"
	SkillDiagListFailed      = "list_failed"
	SkillDiagReadFailed      = "read_failed"
	SkillDiagParseFailed     = "parse_failed"
	SkillDiagInvalidMetadata = "invalid_metadata"
)

// SkillDiagnostic mirrors the TS interface.
type SkillDiagnostic struct {
	Type    string // "warning"
	Code    string
	Message string
	Path    string
}

type skillFrontmatter struct {
	Name                   string `yaml:"name"`
	Description            string `yaml:"description"`
	DisableModelInvocation bool   `yaml:"disable-model-invocation"`
}

// FormatSkillInvocation formats a skill invocation prompt.
func FormatSkillInvocation(skill Skill, additionalInstructions ...string) string {
	skillBlock := "<skill name=\"" + skill.Name + "\" location=\"" + skill.FilePath + "\">\nReferences are relative to " + dirnameEnvPath(skill.FilePath) + ".\n\n" + skill.Content + "\n</skill>"
	if len(additionalInstructions) > 0 && additionalInstructions[0] != "" {
		return skillBlock + "\n\n" + additionalInstructions[0]
	}
	return skillBlock
}

// LoadSkillsResult is the LoadSkills return.
type LoadSkillsResult struct {
	Skills      []Skill
	Diagnostics []SkillDiagnostic
}

// LoadSkills loads skills from one or more directories.
func LoadSkills(env ExecutionEnv, dirs []string, ctx Context) LoadSkillsResult {
	skills := []Skill{}
	diagnostics := []SkillDiagnostic{}
	for _, dir := range dirs {
		rootInfoResult := env.FileInfo(dir, ctx)
		if !rootInfoResult.Ok {
			if rootInfoResult.Error.Code != FileErrNotFound {
				diagnostics = append(diagnostics, SkillDiagnostic{"warning", SkillDiagFileInfoFailed, rootInfoResult.Error.Message, dir})
			}
			continue
		}
		if resolveKind(env, rootInfoResult.Value, &diagnostics, ctx) != FileKindDirectory {
			continue
		}
		result := loadSkillsFromDirInternal(env, rootInfoResult.Value.Path, true, ignore.New(nil), rootInfoResult.Value.Path, ctx)
		skills = append(skills, result.Skills...)
		diagnostics = append(diagnostics, result.Diagnostics...)
	}
	return LoadSkillsResult{Skills: skills, Diagnostics: diagnostics}
}

// LoadSourcedSkills loads skills with provenance tags.
func LoadSourcedSkills[TSource any](
	env ExecutionEnv,
	inputs []struct {
		Path   string
		Source TSource
	},
	mapSkill func(skill Skill, source TSource, ctx Context) Skill,
	ctx Context,
) (skills []struct {
	Skill  Skill
	Source TSource
}, diagnostics []struct {
	Diagnostic SkillDiagnostic
	Source     TSource
}) {
	for _, input := range inputs {
		result := LoadSkills(env, []string{input.Path}, ctx)
		for _, skill := range result.Skills {
			mapped := skill
			if mapSkill != nil {
				mapped = mapSkill(skill, input.Source, ctx)
			}
			skills = append(skills, struct {
				Skill  Skill
				Source TSource
			}{mapped, input.Source})
		}
		for _, diagnostic := range result.Diagnostics {
			diagnostics = append(diagnostics, struct {
				Diagnostic SkillDiagnostic
				Source     TSource
			}{diagnostic, input.Source})
		}
	}
	return skills, diagnostics
}

func loadSkillsFromDirInternal(env ExecutionEnv, dir string, includeRootFiles bool, ignoreMatcher *ignore.Ignore, rootDir string, ctx Context) LoadSkillsResult {
	skills := []Skill{}
	diagnostics := []SkillDiagnostic{}

	dirInfoResult := env.FileInfo(dir, ctx)
	if !dirInfoResult.Ok {
		if dirInfoResult.Error.Code != FileErrNotFound {
			diagnostics = append(diagnostics, SkillDiagnostic{"warning", SkillDiagFileInfoFailed, dirInfoResult.Error.Message, dir})
		}
		return LoadSkillsResult{skills, diagnostics}
	}
	if resolveKind(env, dirInfoResult.Value, &diagnostics, ctx) != FileKindDirectory {
		return LoadSkillsResult{skills, diagnostics}
	}

	addIgnoreRules(env, ignoreMatcher, dir, rootDir, &diagnostics, ctx)

	entriesResult := env.ListDir(dir, ctx)
	if !entriesResult.Ok {
		diagnostics = append(diagnostics, SkillDiagnostic{"warning", SkillDiagListFailed, entriesResult.Error.Message, dir})
		return LoadSkillsResult{skills, diagnostics}
	}
	entries := entriesResult.Value

	// First SKILL.md in the direct directory wins.
	for _, entry := range entries {
		if entry.Name != "SKILL.md" {
			continue
		}
		if resolveKind(env, entry, &diagnostics, ctx) != FileKindFile {
			continue
		}
		relPath := relativeEnvPath(rootDir, entry.Path)
		if ignoreMatcher.Ignores(relPath) {
			continue
		}
		result := loadSkillFromFile(env, entry.Path, dirInfoResult.Value.Name, ctx)
		if result.skill != nil {
			skills = append(skills, *result.skill)
		}
		diagnostics = append(diagnostics, result.diagnostics...)
		return LoadSkillsResult{skills, diagnostics}
	}

	sorted := append([]FileInfo{}, entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, entry := range sorted {
		if strings.HasPrefix(entry.Name, ".") || entry.Name == "node_modules" {
			continue
		}
		kind := resolveKind(env, entry, &diagnostics, ctx)
		if kind == "" {
			continue
		}
		relPath := relativeEnvPath(rootDir, entry.Path)
		ignorePath := relPath
		if kind == FileKindDirectory {
			ignorePath = relPath + "/"
		}
		if ignoreMatcher.Ignores(ignorePath) {
			continue
		}
		if kind == FileKindDirectory {
			result := loadSkillsFromDirInternal(env, entry.Path, false, ignoreMatcher, rootDir, ctx)
			skills = append(skills, result.Skills...)
			diagnostics = append(diagnostics, result.Diagnostics...)
		} else if includeRootFiles && strings.HasSuffix(entry.Name, ".md") {
			result := loadSkillFromFile(env, entry.Path, dirInfoResult.Value.Name, ctx)
			if result.skill != nil {
				skills = append(skills, *result.skill)
			}
			diagnostics = append(diagnostics, result.diagnostics...)
		}
	}
	return LoadSkillsResult{skills, diagnostics}
}

func addIgnoreRules(env ExecutionEnv, ig *ignore.Ignore, dir, rootDir string, diagnostics *[]SkillDiagnostic, ctx Context) {
	for _, filename := range skillIgnoreFileNames {
		ignorePathResult := env.JoinPath([]string{dir, filename}, ctx)
		if !ignorePathResult.Ok {
			*diagnostics = append(*diagnostics, SkillDiagnostic{"warning", SkillDiagFileInfoFailed, ignorePathResult.Error.Message, dir})
			continue
		}
		ignorePath := ignorePathResult.Value
		info := env.FileInfo(ignorePath, ctx)
		if !info.Ok {
			if info.Error.Code != FileErrNotFound {
				*diagnostics = append(*diagnostics, SkillDiagnostic{"warning", SkillDiagFileInfoFailed, info.Error.Message, ignorePath})
			}
			continue
		}
		if info.Value.Kind != FileKindFile {
			continue
		}
		content := env.ReadTextFile(ignorePath, ctx)
		if !content.Ok {
			*diagnostics = append(*diagnostics, SkillDiagnostic{"warning", SkillDiagReadFailed, content.Error.Message, ignorePath})
			continue
		}
		prefix := relativeEnvPath(rootDir, dir)
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		var patterns []string
		for _, line := range splitLinesCRLF(content.Value) {
			if prefixed := prefixIgnorePattern(line, prefix); prefixed != "" {
				patterns = append(patterns, prefixed)
			}
		}
		if len(patterns) > 0 {
			ig.Add(strings.Join(patterns, "\n"))
		}
	}
}

func splitLinesCRLF(content string) []string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	if normalized == "" {
		return nil
	}
	return strings.Split(normalized, "\n")
}

// prefixIgnorePattern prefixes a gitignore line relative to the root.
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
	prefixed := prefix + pattern
	if negated {
		return "!" + prefixed
	}
	return prefixed
}

type loadSkillFileResult struct {
	skill       *Skill
	diagnostics []SkillDiagnostic
}

func loadSkillFromFile(env ExecutionEnv, filePath, parentDirName string, ctx Context) loadSkillFileResult {
	diagnostics := []SkillDiagnostic{}
	isDeclaredSkill := lastPathSegment(filePath) == "SKILL.md"
	rawContent := env.ReadTextFile(filePath, ctx)
	if !rawContent.Ok {
		diagnostics = append(diagnostics, SkillDiagnostic{"warning", SkillDiagReadFailed, rawContent.Error.Message, filePath})
		return loadSkillFileResult{nil, diagnostics}
	}

	frontmatter, body, ok := parseSkillFrontmatter(rawContent.Value)
	if !ok {
		if isDeclaredSkill {
			diagnostics = append(diagnostics, SkillDiagnostic{"warning", SkillDiagParseFailed, "invalid frontmatter", filePath})
		}
		return loadSkillFileResult{nil, diagnostics}
	}

	description := frontmatter.Description
	if !isDeclaredSkill && strings.TrimSpace(description) == "" {
		return loadSkillFileResult{nil, diagnostics}
	}

	for _, errMessage := range validateSkillDescription(description) {
		diagnostics = append(diagnostics, SkillDiagnostic{"warning", SkillDiagInvalidMetadata, errMessage, filePath})
	}

	name := frontmatter.Name
	if name == "" {
		name = parentDirName
	}
	for _, errMessage := range validateSkillName(name, parentDirName) {
		diagnostics = append(diagnostics, SkillDiagnostic{"warning", SkillDiagInvalidMetadata, errMessage, filePath})
	}

	if strings.TrimSpace(description) == "" {
		return loadSkillFileResult{nil, diagnostics}
	}

	return loadSkillFileResult{&Skill{
		Name: name, Description: description, Content: body, FilePath: filePath,
		DisableModelInvocation: frontmatter.DisableModelInvocation,
	}, diagnostics}
}

var skillNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

func validateSkillName(name, parentDirName string) []string {
	var errors []string
	if name != parentDirName {
		errors = append(errors, "name \""+name+"\" does not match parent directory \""+parentDirName+"\"")
	}
	if len(name) > MaxSkillNameLength {
		errors = append(errors, sprintfLength("name", MaxSkillNameLength, len(name)))
	}
	if !skillNameRe.MatchString(name) {
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

func validateSkillDescription(description string) []string {
	var errors []string
	if strings.TrimSpace(description) == "" {
		errors = append(errors, "description is required")
	} else if len(description) > MaxSkillDescriptionLength {
		errors = append(errors, sprintfLength("description", MaxSkillDescriptionLength, len(description)))
	}
	return errors
}

func sprintfLength(field string, max, actual int) string {
	return field + " exceeds " + itoaUtil(max) + " characters (" + itoaUtil(actual) + ")"
}

func itoaUtil(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

// parseSkillFrontmatter parses the `---` YAML frontmatter block.
func parseSkillFrontmatter(content string) (skillFrontmatter, string, bool) {
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	if !strings.HasPrefix(normalized, "---") {
		return skillFrontmatter{}, normalized, true
	}
	endIndex := strings.Index(normalized[3:], "\n---")
	if endIndex == -1 {
		return skillFrontmatter{}, normalized, true
	}
	yamlString := normalized[4 : 3+endIndex]
	body := strings.TrimSpace(normalized[3+endIndex+4:])
	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(yamlString), &fm); err != nil {
		return skillFrontmatter{}, "", false
	}
	return fm, body, true
}

func resolveKind(env ExecutionEnv, info FileInfo, diagnostics *[]SkillDiagnostic, ctx Context) string {
	if info.Kind == FileKindFile || info.Kind == FileKindDirectory {
		return info.Kind
	}
	// Symlink: resolve the target.
	canonicalPath := env.CanonicalPath(info.Path, ctx)
	if !canonicalPath.Ok {
		if canonicalPath.Error.Code != FileErrNotFound {
			*diagnostics = append(*diagnostics, SkillDiagnostic{"warning", SkillDiagFileInfoFailed, canonicalPath.Error.Message, info.Path})
		}
		return ""
	}
	target := env.FileInfo(canonicalPath.Value, ctx)
	if !target.Ok {
		if target.Error.Code != FileErrNotFound {
			*diagnostics = append(*diagnostics, SkillDiagnostic{"warning", SkillDiagFileInfoFailed, target.Error.Message, info.Path})
		}
		return ""
	}
	if target.Value.Kind == FileKindFile || target.Value.Kind == FileKindDirectory {
		return target.Value.Kind
	}
	return ""
}

func lastPathSegment(path string) string {
	normalized := strings.TrimRight(path, "/\\")
	if idx := strings.LastIndexAny(normalized, "/\\"); idx != -1 {
		return normalized[idx+1:]
	}
	return normalized
}

func dirnameEnvPath(path string) string {
	normalized := strings.TrimRight(path, "/\\")
	separatorIndex := strings.LastIndexAny(normalized, "/\\")
	if separatorIndex == 2 && len(normalized) > 1 && normalized[1] == ':' {
		return normalized[:3]
	}
	if separatorIndex <= 0 {
		return "/"
	}
	return normalized[:separatorIndex]
}

func relativeEnvPath(root, path string) string {
	normalizedRoot := strings.TrimRight(strings.ReplaceAll(root, "\\", "/"), "/")
	normalizedPath := strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
	if normalizedPath == normalizedRoot {
		return ""
	}
	if strings.HasPrefix(normalizedPath, normalizedRoot+"/") {
		return normalizedPath[len(normalizedRoot)+1:]
	}
	return strings.TrimLeft(normalizedPath, "/")
}

// ---------------------------------------------------------------------------
// system-prompt.ts
// ---------------------------------------------------------------------------

// FormatSkillsForSystemPrompt renders the <available_skills> block.
func FormatSkillsForSystemPrompt(skills []Skill) string {
	visible := []Skill{}
	for _, skill := range skills {
		if !skill.DisableModelInvocation {
			visible = append(visible, skill)
		}
	}
	if len(visible) == 0 {
		return ""
	}
	lines := []string{
		"The following skills provide specialized instructions for specific tasks.",
		"Read the full skill file when the task matches its description.",
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	}
	for _, skill := range visible {
		lines = append(lines, "  <skill>")
		lines = append(lines, "    <name>"+escapeXML(skill.Name)+"</name>")
		lines = append(lines, "    <description>"+escapeXML(skill.Description)+"</description>")
		lines = append(lines, "    <location>"+escapeXML(skill.FilePath)+"</location>")
		lines = append(lines, "  </skill>")
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

func escapeXML(value string) string {
	replacements := []struct{ from, to string }{
		{"&", "&amp;"}, {"<", "&lt;"}, {">", "&gt;"}, {"\"", "&quot;"}, {"'", "&apos;"},
	}
	out := value
	for _, r := range replacements {
		out = strings.ReplaceAll(out, r.from, r.to)
	}
	return out
}
