package codingagent

// Port of core/package-manager.ts (settings + source-parsing core): the
// PathMetadata/ResolvedResource shapes, package-source parsing (npm/git/
// local), settings add/remove with scope awareness, and install-path
// resolution. Network install/update land with the runtime phase.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
// manifest drives resource kinds; missing sources surface through the
// callback (nil = skip).
func (m *PackageManager) Resolve(onMissing func(source string) string) *ResolvedPaths {
	result := &ResolvedPaths{}

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
			result.Skills = append(result.Skills, ResolvedResource{
				Path: filepath.Join(installPath, skill), Enabled: true, Metadata: metadata,
			})
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

	return result
}

var _ = fmt.Sprintf
