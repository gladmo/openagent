package codingagent

// Port of core/package-manager.ts (settings + source-parsing core): the
// PathMetadata/ResolvedResource shapes, package-source parsing (npm/git/
// local), settings add/remove with scope awareness, install-path
// resolution, and the skills slice of resolve()'s auto-discovery (project
// .pi/skills, ancestor .agents/skills, user agent skills, ~/.agents/skills
// with settings overrides). Network install/update, local settings
// entries, and extensions/prompts/themes auto-discovery land with the
// resolve-completion round.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gladmo/openagent/ignore"
	"github.com/gladmo/openagent/minimatch"
)

// PackageManagerOptions construct the manager.
type PackageManagerOptions struct {
	Cwd             string
	AgentDir        string
	SettingsManager *SettingsManager
}

// ResolvedResource is one discovered resource path.
type ResolvedResource struct {
	Path     string
	Enabled  bool
	Metadata PathMetadata
}

// ResolvedPaths groups resources by kind.
type ResolvedPaths struct {
	Extensions []ResolvedResource
	Skills     []ResolvedResource
	Prompts    []ResolvedResource
	Themes     []ResolvedResource
}

// ProgressEvent reports install/remove/update progress.
type ProgressEvent struct {
	Type    string // start | progress | complete | error
	Action  string // install | remove | update | clone | pull
	Source  string
	Message string
}

// ProgressCallback receives progress events.
type ProgressCallback func(event ProgressEvent)

// PackageSource is the parsed form of a configured package.
type PackageSource struct {
	Type string // npm | git | local
	Spec string
	Git  *GitSource
	Path string
	// Filtered resources (autoload=false with explicit patterns).
	Patterns map[string][]string
	Autoload bool
}

var npmSourcePattern = regexp.MustCompile(`^npm:(.+)$`)

// ParsePackageSource parses one configured source string.
func ParsePackageSource(source string) *PackageSource {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return nil
	}

	if match := npmSourcePattern.FindStringSubmatch(trimmed); match != nil {
		return &PackageSource{Type: "npm", Spec: match[1], Autoload: true}
	}

	if git := ParseGitUrl(trimmed); git != nil {
		return &PackageSource{Type: "git", Spec: trimmed, Git: git, Autoload: true}
	}

	if IsLocalPath(trimmed) {
		return &PackageSource{Type: "local", Path: trimmed, Autoload: true}
	}
	return nil
}

// PackageManager manages package sources.
type PackageManager struct {
	cwd             string
	agentDir        string
	settingsManager *SettingsManager

	progressCallback ProgressCallback
}

// NewPackageManager builds the manager.
func NewPackageManager(options PackageManagerOptions) *PackageManager {
	return &PackageManager{
		cwd:             ResolvePath(options.Cwd, "", PathInputOptions{}),
		agentDir:        ResolvePath(options.AgentDir, "", PathInputOptions{}),
		settingsManager: options.SettingsManager,
	}
}

// SetProgressCallback installs the progress listener.
func (m *PackageManager) SetProgressCallback(callback ProgressCallback) {
	m.progressCallback = callback
}

func (m *PackageManager) emitProgress(event ProgressEvent) {
	if m.progressCallback != nil {
		m.progressCallback(event)
	}
}

// baseDirForScope resolves the packages root per scope.
func (m *PackageManager) baseDirForScope(scope SourceScope) string {
	if scope == ScopeProject {
		return filepath.Join(m.cwd, ConfigDirName(), "packages")
	}
	return filepath.Join(m.agentDir, "packages")
}

// installDirFor computes the install directory for one source.
func (m *PackageManager) installDirFor(source *PackageSource, scope SourceScope) string {
	base := m.baseDirForScope(scope)
	switch source.Type {
	case "npm":
		return filepath.Join(base, "npm", strings.ReplaceAll(source.Spec, "/", string(os.PathSeparator)))
	case "git":
		if source.Git != nil {
			name := strings.ReplaceAll(source.Git.Path, "/", "-")
			if source.Git.Ref != "" {
				name += "@" + source.Git.Ref
			}
			return filepath.Join(base, "git", name)
		}
		return filepath.Join(base, "git", "unknown")
	default:
		return source.Path
	}
}

// GetInstalledPath resolves where a source is installed (nil when
// missing).
func (m *PackageManager) GetInstalledPath(source string, scope SourceScope) string {
	parsed := ParsePackageSource(source)
	if parsed == nil {
		return ""
	}
	if parsed.Type == "local" {
		baseDir := m.baseDirForScope(scope)
		resolved := ResolvePath(parsed.Path, baseDir, PathInputOptions{})
		if _, err := os.Stat(resolved); err == nil {
			return resolved
		}
		return ""
	}
	path := m.installDirFor(parsed, scope)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

// PackageSourcesFromSettings extracts the configured package sources.
func (m *PackageManager) PackageSourcesFromSettings() map[SourceScope][]any {
	out := map[SourceScope][]any{}
	if m.settingsManager != nil {
		global := m.settingsManager.GetGlobalSettings()
		if packages, ok := global["packages"].([]any); ok {
			out[ScopeUser] = packages
		}
		project := m.settingsManager.GetProjectSettings()
		if packages, ok := project["packages"].([]any); ok {
			out[ScopeProject] = packages
		}
	}
	return out
}

// PackageSourceString extracts the source string from a configured value.
func PackageSourceString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if source, ok := typed["source"].(string); ok {
			return source
		}
	}
	return ""
}

// AddSourceToSettings records a source in the scoped settings (false when
// unchanged).
func (m *PackageManager) AddSourceToSettings(source string, local bool) bool {
	scope := ScopeUser
	field := "packages"
	if local {
		scope = ScopeProject
		field = "packages"
	}
	_ = scope

	current := m.currentPackages(local)
	normalized := source

	for _, existing := range current {
		if PackageSourceString(existing) == normalized {
			return false
		}
	}
	next := append(current, normalized)
	m.writePackages(local, next, field)
	return true
}

// RemoveSourceFromSettings drops a source (false when absent).
func (m *PackageManager) RemoveSourceFromSettings(source string, local bool) bool {
	current := m.currentPackages(local)
	var next []any
	removed := false
	for _, existing := range current {
		if PackageSourceString(existing) == source {
			removed = true
			continue
		}
		next = append(next, existing)
	}
	if !removed {
		return false
	}
	m.writePackages(local, next, "packages")
	return true
}

func (m *PackageManager) currentPackages(local bool) []any {
	if m.settingsManager == nil {
		return nil
	}
	settings := m.settingsManager.GetGlobalSettings()
	if local {
		settings = m.settingsManager.GetProjectSettings()
	}
	packages, _ := settings["packages"].([]any)
	return packages
}

func (m *PackageManager) writePackages(local bool, next []any, field string) {
	if m.settingsManager == nil {
		return
	}
	if local {
		if err := m.settingsManager.SetProjectSetting(field, next); err != nil {
			return
		}
	} else {
		m.settingsManager.SetSetting(field, next)
	}
}

// ListConfiguredPackages lists the configured sources with install state.
type ConfiguredPackage struct {
	Source        string
	Scope         SourceScope
	Filtered      bool
	InstalledPath string
}

// ListConfiguredPackages enumerates settings sources.
func (m *PackageManager) ListConfiguredPackages() []ConfiguredPackage {
	var out []ConfiguredPackage
	for scope, packages := range m.PackageSourcesFromSettings() {
		scopeKey := ScopeUser
		if scope == ScopeProject {
			scopeKey = ScopeProject
		}
		names := make([]string, 0, len(packages))
		for _, value := range packages {
			names = append(names, PackageSourceString(value))
		}
		sort.Strings(names)
		for _, name := range names {
			entry := ConfiguredPackage{
				Source:        name,
				Scope:         scopeKey,
				InstalledPath: m.GetInstalledPath(name, scopeKey),
			}
			out = append(out, entry)
		}
	}
	return out
}

// Resolve discovers resources from configured + explicit sources. The
// manifest drives package resource kinds; skills additionally flow through
// the accumulator shared with auto-discovery (first record per path wins,
// output ordered by precedence rank with canonical dedupe). Missing
// sources surface through the callback (nil = skip).
func (m *PackageManager) Resolve(onMissing func(source string) string) *ResolvedPaths {
	result := &ResolvedPaths{}
	skills := newSkillAccumulator()

	addManifestResources := func(installPath string, metadata PathMetadata) {
		manifest := ReadPiManifest(filepath.Join(installPath, "package.json"))
		if manifest == nil {
			return
		}
		for _, extension := range manifest.Extensions {
			result.Extensions = append(result.Extensions, ResolvedResource{
				Path: filepath.Join(installPath, extension), Enabled: true, Metadata: metadata,
			})
		}
		for _, skill := range manifest.Skills {
			skills.add(filepath.Join(installPath, skill), metadata, true)
		}
		for _, prompt := range manifest.Prompts {
			result.Prompts = append(result.Prompts, ResolvedResource{
				Path: filepath.Join(installPath, prompt), Enabled: true, Metadata: metadata,
			})
		}
		for _, theme := range manifest.Themes {
			result.Themes = append(result.Themes, ResolvedResource{
				Path: filepath.Join(installPath, theme), Enabled: true, Metadata: metadata,
			})
		}
	}

	// Project scope first so cwd resources win collisions.
	for _, scope := range []SourceScope{ScopeProject, ScopeUser} {
		packages := m.PackageSourcesFromSettings()[scope]
		for _, value := range packages {
			name := PackageSourceString(value)
			parsed := ParsePackageSource(name)
			if parsed == nil {
				continue
			}
			installPath := m.GetInstalledPath(name, scope)
			if installPath == "" {
				if onMissing != nil {
					if action := onMissing(name); action == "install" {
						continue // network install lands with the runtime phase
					}
				}
				continue
			}
			addManifestResources(installPath, PathMetadata{
				Source:      name,
				Scope:       scope,
				Origin:      OriginPackage,
				BaseDir:     installPath,
				PackageRoot: installPath,
			})
		}
	}

	m.addAutoDiscoveredSkills(
		skills,
		m.settingsManager.GetGlobalSettings(),
		m.settingsManager.GetProjectSettings(),
		m.agentDir,
		filepath.Join(m.cwd, ConfigDirName()),
	)

	result.Skills = skills.resolved()
	return result
}

// ---------------------------------------------------------------------------
// Auto-discovery (package-manager.ts): skill entry collection, .agents
// ancestor walk, settings override matching, precedence ordering.
// ---------------------------------------------------------------------------

// Skill discovery modes: "pi" accepts root markdown in pi-owned skill
// dirs; "agents" skips root markdown and only accepts nested entries.
const (
	skillDiscoveryModePi     = "pi"
	skillDiscoveryModeAgents = "agents"
)

// collectSkillEntries collects skill file paths from a directory tree: a
// SKILL.md makes its directory a skill root (no recursion into it);
// otherwise markdown files and subdirectories recurse. Ignore files apply
// within the walked tree only. The mode selects whether root ("pi") or
// nested ("agents") markdown files count as entries.
func collectSkillEntries(dir, mode string, matcher *ignore.Ignore, rootDir string) []string {
	entries := []string{}
	if _, err := os.Stat(dir); err != nil {
		return entries
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

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return entries
	}

	// First SKILL.md in the direct directory wins.
	for _, entry := range dirEntries {
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
		if isFile && !ig.Ignores(toPosixPath(relPath)) {
			entries = append(entries, fullPath)
			return entries
		}
	}

	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Name() == "node_modules" {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		isDir, isFile := entry.IsDir(), entry.Type().IsRegular()
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isDir, isFile = info.IsDir(), info.Mode().IsRegular()
		}
		relPath, err := filepath.Rel(root, fullPath)
		if err != nil {
			continue
		}
		relPosix := toPosixPath(relPath)
		includeMarkdown := isFile && strings.HasSuffix(entry.Name(), ".md") && !ig.Ignores(relPosix) &&
			((mode == skillDiscoveryModePi && dir == root) || (mode == skillDiscoveryModeAgents && dir != root))
		if includeMarkdown {
			entries = append(entries, fullPath)
			continue
		}
		if !isDir {
			continue
		}
		if ig.Ignores(relPosix + "/") {
			continue
		}
		entries = append(entries, collectSkillEntries(fullPath, mode, ig, root)...)
	}
	return entries
}

// collectAutoSkillEntries collects from a fresh ignore matcher.
func collectAutoSkillEntries(dir, mode string) []string {
	return collectSkillEntries(dir, mode, nil, "")
}

// findGitRepoRoot walks up until a .git entry exists; "" when none does.
func findGitRepoRoot(startDir string) string {
	dir := ResolvePath(startDir, "", PathInputOptions{})
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// collectAncestorAgentsSkillDirs lists <dir>/.agents/skills for every
// ancestor from startDir through the git repo root (through the
// filesystem root outside a repo).
func collectAncestorAgentsSkillDirs(startDir string) []string {
	skillDirs := []string{}
	resolvedStart := ResolvePath(startDir, "", PathInputOptions{})
	gitRepoRoot := findGitRepoRoot(resolvedStart)

	dir := resolvedStart
	for {
		skillDirs = append(skillDirs, filepath.Join(dir, ".agents", "skills"))
		if gitRepoRoot != "" && dir == gitRepoRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return skillDirs
}

// settingsStringList reads a string-array field from one settings scope.
func settingsStringList(settings Settings, field string) []string {
	raw, ok := settings[field]
	if !ok {
		return nil
	}
	asSlice, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(asSlice))
	for _, entry := range asSlice {
		if text, ok := entry.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// getOverridePatterns keeps the !/+/- override patterns.
func getOverridePatterns(entries []string) []string {
	out := []string{}
	for _, pattern := range entries {
		if strings.HasPrefix(pattern, "!") || strings.HasPrefix(pattern, "+") || strings.HasPrefix(pattern, "-") {
			out = append(out, pattern)
		}
	}
	return out
}

// normalizeExactPattern strips a leading ./ and posix-normalizes.
func normalizeExactPattern(pattern string) string {
	normalized := pattern
	if strings.HasPrefix(normalized, "./") || strings.HasPrefix(normalized, `.\`) {
		normalized = normalized[2:]
	}
	return toPosixPath(normalized)
}

func relativePosix(baseDir, path string) string {
	rel, err := filepath.Rel(baseDir, path)
	if err != nil {
		return ""
	}
	return toPosixPath(rel)
}

// matchesAnyPattern glob-matches a path against patterns; SKILL.md paths
// also match through their parent directory.
func matchesAnyPattern(filePath string, patterns []string, baseDir string) bool {
	rel := relativePosix(baseDir, filePath)
	name := filepath.Base(filePath)
	filePathPosix := toPosixPath(filePath)
	isSkillFile := name == "SKILL.md"
	parentDir := filepath.Dir(filePath)
	parentRel, parentName, parentDirPosix := "", "", ""
	if isSkillFile {
		parentRel = relativePosix(baseDir, parentDir)
		parentName = filepath.Base(parentDir)
		parentDirPosix = toPosixPath(parentDir)
	}
	for _, pattern := range patterns {
		normalized := toPosixPath(pattern)
		if minimatch.MinimatchMatch(rel, normalized) ||
			minimatch.MinimatchMatch(name, normalized) ||
			minimatch.MinimatchMatch(filePathPosix, normalized) {
			return true
		}
		if !isSkillFile {
			continue
		}
		if minimatch.MinimatchMatch(parentRel, normalized) ||
			minimatch.MinimatchMatch(parentName, normalized) ||
			minimatch.MinimatchMatch(parentDirPosix, normalized) {
			return true
		}
	}
	return false
}

// matchesAnyExactPattern compares exact paths; SKILL.md paths also match
// via their parent directory.
func matchesAnyExactPattern(filePath string, patterns []string, baseDir string) bool {
	if len(patterns) == 0 {
		return false
	}
	rel := relativePosix(baseDir, filePath)
	name := filepath.Base(filePath)
	filePathPosix := toPosixPath(filePath)
	isSkillFile := name == "SKILL.md"
	parentDir := filepath.Dir(filePath)
	parentRel, parentDirPosix := "", ""
	if isSkillFile {
		parentRel = relativePosix(baseDir, parentDir)
		parentDirPosix = toPosixPath(parentDir)
	}
	for _, pattern := range patterns {
		normalized := normalizeExactPattern(pattern)
		if normalized == rel || normalized == filePathPosix {
			return true
		}
		if !isSkillFile {
			continue
		}
		if normalized == parentRel || normalized == parentDirPosix {
			return true
		}
	}
	return false
}

// isEnabledByOverrides applies settings patterns: !glob excludes, +path
// force-includes, -path force-excludes (evaluated in that order).
func isEnabledByOverrides(filePath string, patterns []string, baseDir string) bool {
	overrides := getOverridePatterns(patterns)
	excludes := []string{}
	forceIncludes := []string{}
	forceExcludes := []string{}
	for _, pattern := range overrides {
		switch {
		case strings.HasPrefix(pattern, "!"):
			excludes = append(excludes, pattern[1:])
		case strings.HasPrefix(pattern, "+"):
			forceIncludes = append(forceIncludes, pattern[1:])
		case strings.HasPrefix(pattern, "-"):
			forceExcludes = append(forceExcludes, pattern[1:])
		}
	}

	enabled := true
	if len(excludes) > 0 && matchesAnyPattern(filePath, excludes, baseDir) {
		enabled = false
	}
	if len(forceIncludes) > 0 && matchesAnyExactPattern(filePath, forceIncludes, baseDir) {
		enabled = true
	}
	if len(forceExcludes) > 0 && matchesAnyExactPattern(filePath, forceExcludes, baseDir) {
		enabled = false
	}
	return enabled
}

// resourcePrecedenceRank orders resolved resources: project local <
// project auto < user local < user auto < package.
func resourcePrecedenceRank(m PathMetadata) int {
	if m.Origin == OriginPackage {
		return 4
	}
	scopeBase := 2
	if m.Scope == ScopeProject {
		scopeBase = 0
	}
	if m.Source == "local" {
		return scopeBase
	}
	return scopeBase + 1
}

type skillAccumulatorEntry struct {
	metadata PathMetadata
	enabled  bool
}

// skillAccumulator is resolve()'s skills map: the first record per path
// wins; output sorts by precedence rank (stable) and drops canonical-path
// duplicates.
type skillAccumulator struct {
	order   []string
	entries map[string]skillAccumulatorEntry
}

func newSkillAccumulator() *skillAccumulator {
	return &skillAccumulator{entries: map[string]skillAccumulatorEntry{}}
}

func (a *skillAccumulator) add(path string, metadata PathMetadata, enabled bool) {
	if path == "" {
		return
	}
	if _, exists := a.entries[path]; exists {
		return
	}
	a.entries[path] = skillAccumulatorEntry{metadata, enabled}
	a.order = append(a.order, path)
}

func (a *skillAccumulator) resolved() []ResolvedResource {
	ordered := make([]ResolvedResource, 0, len(a.order))
	for _, path := range a.order {
		entry := a.entries[path]
		ordered = append(ordered, ResolvedResource{Path: path, Enabled: entry.enabled, Metadata: entry.metadata})
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return resourcePrecedenceRank(ordered[i].Metadata) < resourcePrecedenceRank(ordered[j].Metadata)
	})

	seen := map[string]bool{}
	out := make([]ResolvedResource, 0, len(ordered))
	for _, entry := range ordered {
		canonicalPath := CanonicalizePath(entry.Path)
		if seen[canonicalPath] {
			continue
		}
		seen[canonicalPath] = true
		out = append(out, entry)
	}
	return out
}

// addAutoDiscoveredSkills ports the skills half of
// addAutoDiscoveredResources: project .pi/skills and ancestor
// .agents/skills (both trust-gated), then user agent skills and
// ~/.agents/skills (the cross-tool pool). Entries carry settings override
// flags against their owning baseDir.
func (m *PackageManager) addAutoDiscoveredSkills(
	target *skillAccumulator,
	globalSettings Settings,
	projectSettings Settings,
	globalBaseDir string,
	projectBaseDir string,
) {
	userMetadata := PathMetadata{Source: "auto", Scope: ScopeUser, Origin: OriginTopLevel, BaseDir: globalBaseDir}
	projectMetadata := PathMetadata{Source: "auto", Scope: ScopeProject, Origin: OriginTopLevel, BaseDir: projectBaseDir}

	userOverrides := settingsStringList(globalSettings, "skills")
	projectOverrides := settingsStringList(projectSettings, "skills")

	userSkillsDir := filepath.Join(globalBaseDir, "skills")
	projectSkillsDir := filepath.Join(projectBaseDir, "skills")

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	userAgentsSkillsDir := filepath.Join(home, ".agents", "skills")
	projectTrusted := m.settingsManager.IsProjectTrusted()
	projectAgentsSkillDirs := []string{}
	if projectTrusted {
		for _, dir := range collectAncestorAgentsSkillDirs(m.cwd) {
			if ResolvePath(dir, "", PathInputOptions{}) != ResolvePath(userAgentsSkillsDir, "", PathInputOptions{}) {
				projectAgentsSkillDirs = append(projectAgentsSkillDirs, dir)
			}
		}
	}

	addResources := func(paths []string, metadata PathMetadata, overrides []string, baseDir string) {
		for _, path := range paths {
			enabled := isEnabledByOverrides(path, overrides, baseDir)
			target.add(path, metadata, enabled)
		}
	}

	if projectTrusted {
		// Project skills from .pi/
		addResources(collectAutoSkillEntries(projectSkillsDir, skillDiscoveryModePi), projectMetadata, projectOverrides, projectBaseDir)
	}

	// Project skills from .agents/ (each with its own baseDir)
	for _, agentsSkillsDir := range projectAgentsSkillDirs {
		agentsBaseDir := filepath.Dir(agentsSkillsDir)
		agentsMetadata := projectMetadata
		agentsMetadata.BaseDir = agentsBaseDir
		addResources(collectAutoSkillEntries(agentsSkillsDir, skillDiscoveryModeAgents), agentsMetadata, projectOverrides, agentsBaseDir)
	}

	// User skills from the agent dir
	addResources(collectAutoSkillEntries(userSkillsDir, skillDiscoveryModePi), userMetadata, userOverrides, globalBaseDir)

	// User skills from ~/.agents/ (with its own baseDir)
	userAgentsBaseDir := filepath.Dir(userAgentsSkillsDir)
	userAgentsMetadata := userMetadata
	userAgentsMetadata.BaseDir = userAgentsBaseDir
	addResources(collectAutoSkillEntries(userAgentsSkillsDir, skillDiscoveryModeAgents), userAgentsMetadata, userOverrides, userAgentsBaseDir)
}
