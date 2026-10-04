package codingagent

// Port of core/settings-manager.ts: two-scope (global/project) settings
// with deep merge, migrations, trust gating, and modification tracking.
// proper-lockfile maps to an in-process mutex (single-host deviation,
// documented). Settings values ride jsonx objects so unknown keys survive
// round-trips.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// SettingsScope is "global" or "project".
type SettingsScope string

const (
	SettingsScopeGlobal  SettingsScope = "global"
	SettingsScopeProject SettingsScope = "project"
)

// Settings carries arbitrary keys (typed accessors read known names).
type Settings map[string]any

// SettingsError reports a load failure at one scope.
type SettingsError struct {
	Scope SettingsScope
	Path  string
	Err   error
}

// SettingsStorage is the persistence seam.
type SettingsStorage interface {
	WithLock(scope SettingsScope, fn func(current string) string)
}

// FileSettingsStorage persists settings.json per scope.
type FileSettingsStorage struct {
	mu                  sync.Mutex
	globalSettingsPath  string
	projectSettingsPath string
}

// NewFileSettingsStorage builds the file storage for cwd/agentDir.
func NewFileSettingsStorage(cwd, agentDir string) *FileSettingsStorage {
	resolvedCwd := ResolvePath(cwd, "", PathInputOptions{})
	resolvedAgentDir := ResolvePath(agentDir, "", PathInputOptions{})
	return &FileSettingsStorage{
		globalSettingsPath:  filepath.Join(resolvedAgentDir, "settings.json"),
		projectSettingsPath: filepath.Join(resolvedCwd, ConfigDirName(), "settings.json"),
	}
}

// WithLock runs fn under the storage lock; a non-empty return writes.
func (s *FileSettingsStorage) WithLock(scope SettingsScope, fn func(current string) string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.projectSettingsPath
	if scope == SettingsScopeGlobal {
		path = s.globalSettingsPath
	}

	current := ""
	if data, err := os.ReadFile(path); err == nil {
		current = string(data)
	}
	next := fn(current)
	if next != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return
		}
		_ = os.WriteFile(path, []byte(next), 0o644)
	}
}

// Paths reports the storage paths for error reporting.
func (s *FileSettingsStorage) Paths() map[SettingsScope]string {
	return map[SettingsScope]string{
		SettingsScopeGlobal:  s.globalSettingsPath,
		SettingsScopeProject: s.projectSettingsPath,
	}
}

// InMemorySettingsStorage keeps settings in memory.
type InMemorySettingsStorage struct {
	mu     sync.Mutex
	scopes map[SettingsScope]string
}

// NewInMemorySettingsStorage builds the in-memory storage.
func NewInMemorySettingsStorage() *InMemorySettingsStorage {
	return &InMemorySettingsStorage{scopes: map[SettingsScope]string{}}
}

// WithLock implements SettingsStorage.
func (s *InMemorySettingsStorage) WithLock(scope SettingsScope, fn func(current string) string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.scopes[scope]
	next := fn(current)
	if next != "" {
		s.scopes[scope] = next
	}
}

// SettingsManager loads, merges, and persists the two scopes.
type SettingsManager struct {
	mu      sync.Mutex
	storage SettingsStorage

	globalSettings  Settings
	projectSettings Settings
	settings        Settings

	projectTrusted bool

	modifiedFields              map[string]bool
	modifiedNestedFields        map[string]map[string]bool
	modifiedProjectFields       map[string]bool
	modifiedProjectNestedFields map[string]map[string]bool

	globalSettingsLoadError  error
	projectSettingsLoadError error

	errors []SettingsError
	paths  map[SettingsScope]string
}

// SettingsManagerCreateOptions gate project trust at load.
type SettingsManagerCreateOptions struct {
	ProjectTrusted *bool
}

// SettingsManagerCreate loads from files.
func SettingsManagerCreate(cwd string, agentDir string, options *SettingsManagerCreateOptions) *SettingsManager {
	if agentDir == "" {
		agentDir = GetAgentDir()
	}
	storage := NewFileSettingsStorage(cwd, agentDir)
	return settingsManagerFromStorage(storage, options, storage.Paths())
}

// SettingsManagerFromStorage builds from any backend.
func SettingsManagerFromStorage(storage SettingsStorage, options *SettingsManagerCreateOptions) *SettingsManager {
	return settingsManagerFromStorage(storage, options, nil)
}

// SettingsManagerInMemory builds a non-persisting manager.
func SettingsManagerInMemory(settings Settings, options *SettingsManagerCreateOptions) *SettingsManager {
	storage := NewInMemorySettingsStorage()
	migrated := MigrateSettings(settings)
	encoded, _ := json.MarshalIndent(migrated, "", "  ")
	storage.WithLock(SettingsScopeGlobal, func(current string) string { return string(encoded) })
	return settingsManagerFromStorage(storage, options, nil)
}

func settingsManagerFromStorage(storage SettingsStorage, options *SettingsManagerCreateOptions, paths map[SettingsScope]string) *SettingsManager {
	projectTrusted := true
	if options != nil && options.ProjectTrusted != nil {
		projectTrusted = *options.ProjectTrusted
	}

	globalSettings, globalErr := tryLoadSettings(storage, SettingsScopeGlobal, true)
	projectSettings, projectErr := tryLoadSettings(storage, SettingsScopeProject, projectTrusted)

	var initialErrors []SettingsError
	if globalErr != nil {
		initialErrors = append(initialErrors, SettingsError{Scope: SettingsScopeGlobal, Path: paths[SettingsScopeGlobal], Err: globalErr})
	}
	if projectErr != nil {
		initialErrors = append(initialErrors, SettingsError{Scope: SettingsScopeProject, Path: paths[SettingsScopeProject], Err: projectErr})
	}

	return &SettingsManager{
		storage:                     storage,
		globalSettings:              globalSettings,
		projectSettings:             projectSettings,
		projectTrusted:              projectTrusted,
		globalSettingsLoadError:     globalErr,
		projectSettingsLoadError:    projectErr,
		errors:                      initialErrors,
		paths:                       paths,
		modifiedFields:              map[string]bool{},
		modifiedNestedFields:        map[string]map[string]bool{},
		modifiedProjectFields:       map[string]bool{},
		modifiedProjectNestedFields: map[string]map[string]bool{},
		settings:                    DeepMergeSettings(globalSettings, projectSettings),
	}
}

func loadSettings(storage SettingsStorage, scope SettingsScope, projectTrusted bool) (Settings, error) {
	if scope == SettingsScopeProject && !projectTrusted {
		return Settings{}, nil
	}

	var content string
	storage.WithLock(scope, func(current string) string {
		content = current
		return ""
	})

	if content == "" {
		return Settings{}, nil
	}
	var settings Settings
	if err := json.Unmarshal([]byte(StripBom(content)), &settings); err != nil {
		return nil, err
	}
	if settings == nil {
		settings = Settings{}
	}
	return MigrateSettings(settings), nil
}

func tryLoadSettings(storage SettingsStorage, scope SettingsScope, projectTrusted bool) (Settings, error) {
	settings, err := loadSettings(storage, scope, projectTrusted)
	if err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// MigrateSettings upgrades old formats in place.
func MigrateSettings(settings Settings) Settings {
	if settings == nil {
		return Settings{}
	}

	// queueMode -> steeringMode.
	if _, hasQueue := settings["queueMode"]; hasQueue {
		if _, hasSteering := settings["steeringMode"]; !hasSteering {
			settings["steeringMode"] = settings["queueMode"]
		}
		delete(settings, "queueMode")
	}

	// websockets boolean -> transport enum.
	if _, hasTransport := settings["transport"]; !hasTransport {
		if websockets, ok := settings["websockets"].(bool); ok {
			if websockets {
				settings["transport"] = "websocket"
			} else {
				settings["transport"] = "sse"
			}
			delete(settings, "websockets")
		}
	}

	// skills object -> array + enableSkillCommands.
	if skills, ok := settings["skills"].(map[string]any); ok {
		if enable, has := skills["enableSkillCommands"]; has {
			if _, exists := settings["enableSkillCommands"]; !exists {
				settings["enableSkillCommands"] = enable
			}
		}
		if custom, ok := skills["customDirectories"].([]any); ok && len(custom) > 0 {
			settings["skills"] = custom
		} else {
			delete(settings, "skills")
		}
	}

	// retry.maxDelayMs -> retry.provider.maxRetryDelayMs.
	if retry, ok := settings["retry"].(map[string]any); ok {
		if maxDelay, isNumber := retry["maxDelayMs"].(float64); isNumber {
			provider, _ := retry["provider"].(map[string]any)
			if provider == nil {
				provider = map[string]any{}
			}
			if _, exists := provider["maxRetryDelayMs"]; !exists {
				provider["maxRetryDelayMs"] = maxDelay
				retry["provider"] = provider
			}
		}
		delete(retry, "maxDelayMs")
	}

	return settings
}

// DeepMergeSettings merges overrides over base recursively.
func DeepMergeSettings(base, overrides Settings) Settings {
	result := Settings{}
	for key, value := range base {
		result[key] = value
	}
	for key, override := range overrides {
		if baseValue, exists := result[key]; exists {
			if baseMap, baseOK := baseValue.(map[string]any); baseOK {
				if overrideMap, overrideOK := override.(map[string]any); overrideOK {
					merged := DeepMergeSettings(Settings(baseMap), Settings(overrideMap))
					result[key] = map[string]any(merged)
					continue
				}
			}
		}
		result[key] = override
	}
	return result
}

// GetGlobalSettings copies the global scope.
func (m *SettingsManager) GetGlobalSettings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneSettings(m.globalSettings)
}

// GetProjectSettings copies the project scope.
func (m *SettingsManager) GetProjectSettings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneSettings(m.projectSettings)
}

// GetSettings copies the merged view.
func (m *SettingsManager) GetSettings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneSettings(m.settings)
}

func cloneSettings(settings Settings) Settings {
	if settings == nil {
		return Settings{}
	}
	data, _ := json.Marshal(settings)
	var cloned Settings
	_ = json.Unmarshal(data, &cloned)
	if cloned == nil {
		cloned = Settings{}
	}
	return cloned
}

// IsProjectTrusted reports trust state.
func (m *SettingsManager) IsProjectTrusted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.projectTrusted
}

// SetProjectTrusted toggles trust and reloads the project scope.
func (m *SettingsManager) SetProjectTrusted(trusted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.projectTrusted == trusted {
		return
	}
	m.projectTrusted = trusted
	m.modifiedProjectFields = map[string]bool{}
	m.modifiedProjectNestedFields = map[string]map[string]bool{}

	if !trusted {
		m.projectSettings = Settings{}
		m.projectSettingsLoadError = nil
		m.settings = DeepMergeSettings(m.globalSettings, m.projectSettings)
		return
	}

	projectSettings, projectErr := tryLoadSettings(m.storage, SettingsScopeProject, trusted)
	m.projectSettings = projectSettings
	m.projectSettingsLoadError = projectErr
	if projectErr != nil {
		m.errors = append(m.errors, SettingsError{Scope: SettingsScopeProject, Path: m.paths[SettingsScopeProject], Err: projectErr})
	}
	m.settings = DeepMergeSettings(m.globalSettings, m.projectSettings)
}

// Reload re-reads both scopes.
func (m *SettingsManager) Reload() {
	m.mu.Lock()
	defer m.mu.Unlock()

	globalSettings, globalErr := tryLoadSettings(m.storage, SettingsScopeGlobal, true)
	if globalErr == nil {
		m.globalSettings = globalSettings
		m.globalSettingsLoadError = nil
	} else {
		m.globalSettingsLoadError = globalErr
		m.errors = append(m.errors, SettingsError{Scope: SettingsScopeGlobal, Path: m.paths[SettingsScopeGlobal], Err: globalErr})
	}

	m.modifiedFields = map[string]bool{}
	m.modifiedNestedFields = map[string]map[string]bool{}
	m.modifiedProjectFields = map[string]bool{}
	m.modifiedProjectNestedFields = map[string]map[string]bool{}

	projectSettings, projectErr := tryLoadSettings(m.storage, SettingsScopeProject, m.projectTrusted)
	if projectErr == nil {
		m.projectSettings = projectSettings
		m.projectSettingsLoadError = nil
	} else {
		m.projectSettingsLoadError = projectErr
		m.errors = append(m.errors, SettingsError{Scope: SettingsScopeProject, Path: m.paths[SettingsScopeProject], Err: projectErr})
	}

	m.settings = DeepMergeSettings(m.globalSettings, m.projectSettings)
}

// ApplyOverrides layers overrides over the merged view.
func (m *SettingsManager) ApplyOverrides(overrides Settings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = DeepMergeSettings(m.settings, overrides)
}

// DrainErrors returns and clears collected errors.
func (m *SettingsManager) DrainErrors() []SettingsError {
	m.mu.Lock()
	defer m.mu.Unlock()
	drained := append([]SettingsError(nil), m.errors...)
	m.errors = nil
	return drained
}

func (m *SettingsManager) markModified(field string, nestedKey string) {
	m.modifiedFields[field] = true
	if nestedKey != "" {
		if m.modifiedNestedFields[field] == nil {
			m.modifiedNestedFields[field] = map[string]bool{}
		}
		m.modifiedNestedFields[field][nestedKey] = true
	}
}

func (m *SettingsManager) markProjectModified(field string, nestedKey string) {
	m.modifiedProjectFields[field] = true
	if nestedKey != "" {
		if m.modifiedProjectNestedFields[field] == nil {
			m.modifiedProjectNestedFields[field] = map[string]bool{}
		}
		m.modifiedProjectNestedFields[field][nestedKey] = true
	}
}

func (m *SettingsManager) saveGlobal() {
	m.saveScope(SettingsScopeGlobal, m.globalSettings, m.modifiedFields, m.modifiedNestedFields)
}

func (m *SettingsManager) saveProject() {
	m.saveScope(SettingsScopeProject, m.projectSettings, m.modifiedProjectFields, m.modifiedProjectNestedFields)
}

// saveScope persists only fields marked modified this session.
func (m *SettingsManager) saveScope(scope SettingsScope, current Settings, modified map[string]bool, modifiedNested map[string]map[string]bool) {
	if len(modified) == 0 {
		return
	}
	stored := Settings{}
	if scope == SettingsScopeGlobal {
		if _, err := loadSettings(m.storage, SettingsScopeGlobal, true); err == nil {
			loaded, _ := loadSettings(m.storage, SettingsScopeGlobal, true)
			stored = loaded
		}
	} else if m.projectTrusted {
		if loaded, err := loadSettings(m.storage, SettingsScopeProject, true); err == nil {
			stored = loaded
		}
	}

	fields := make([]string, 0, len(modified))
	for field := range modified {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		if nested, has := modifiedNested[field]; has && len(nested) > 0 {
			// Merge nested keys one by one.
			nestedKeys := make([]string, 0, len(nested))
			for key := range nested {
				nestedKeys = append(nestedKeys, key)
			}
			sort.Strings(nestedKeys)
			existing, _ := stored[field].(map[string]any)
			if existing == nil {
				existing = map[string]any{}
			}
			currentNested, _ := current[field].(map[string]any)
			if currentNested != nil {
				for _, key := range nestedKeys {
					if value, ok := currentNested[key]; ok {
						existing[key] = value
					} else {
						delete(existing, key)
					}
				}
			}
			stored[field] = existing
			continue
		}
		if value, ok := current[field]; ok {
			stored[field] = value
		} else {
			delete(stored, field)
		}
	}

	encoded, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return
	}
	m.storage.WithLock(scope, func(content string) string { return string(encoded) })
}

// GetSetting reads one merged value.
func (m *SettingsManager) GetSetting(field string) (any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.settings[field]
	return value, ok
}

// SetSetting writes one global field and persists the modified set.
func (m *SettingsManager) SetSetting(field string, value any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings[field] = value
	m.settings[field] = value
	m.markModified(field, "")
	m.saveGlobal()
}

// SetProjectSetting writes one project field.
func (m *SettingsManager) SetProjectSetting(field string, value any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.projectTrusted {
		return fmt.Errorf("Project is not trusted; refusing to write project settings")
	}
	m.projectSettings[field] = value
	m.settings[field] = value
	m.markProjectModified(field, "")
	m.saveProject()
	return nil
}

// SetNestedSetting writes one nested key of a global field.
func (m *SettingsManager) SetNestedSetting(field, nestedKey string, value any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	nested, _ := m.globalSettings[field].(map[string]any)
	if nested == nil {
		nested = map[string]any{}
	}
	nested[nestedKey] = value
	m.globalSettings[field] = nested
	m.settings[field] = DeepMergeSettings(settingsToSettings(m.settings[field]), Settings(nested))
	m.markModified(field, nestedKey)
	m.saveGlobal()
}

func settingsToSettings(value any) Settings {
	if asMap, ok := value.(map[string]any); ok {
		return Settings(asMap)
	}
	return Settings{}
}

// GetGlobalSettingsLoadError reports the global load error (nil healthy).
func (m *SettingsManager) GetGlobalSettingsLoadError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.globalSettingsLoadError
}

// GetProjectSettingsLoadError reports the project load error.
func (m *SettingsManager) GetProjectSettingsLoadError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.projectSettingsLoadError
}

// GetLastChangelogVersion reads the changelog marker.
func (m *SettingsManager) GetLastChangelogVersion() string {
	value, _ := m.GetSetting("lastChangelogVersion")
	text, _ := value.(string)
	return text
}

// SetLastChangelogVersion writes the marker.
func (m *SettingsManager) SetLastChangelogVersion(version string) {
	m.SetSetting("lastChangelogVersion", version)
}

// GetSessionDir resolves the custom session dir (normalized).
func (m *SettingsManager) GetSessionDir() string {
	value, _ := m.GetSetting("sessionDir")
	text, _ := value.(string)
	if text == "" {
		return ""
	}
	return NormalizePathDefault(text)
}

// GetDefaultProvider reads the default provider.
func (m *SettingsManager) GetDefaultProvider() string {
	value, _ := m.GetSetting("defaultProvider")
	text, _ := value.(string)
	return text
}

// GetDefaultModel reads the default model id.
func (m *SettingsManager) GetDefaultModel() string {
	value, _ := m.GetSetting("defaultModel")
	text, _ := value.(string)
	return text
}

// SetDefaultProvider writes the provider.
func (m *SettingsManager) SetDefaultProvider(provider string) {
	m.SetSetting("defaultProvider", provider)
}

// SetDefaultModel writes the model id.
func (m *SettingsManager) SetDefaultModel(modelID string) {
	m.SetSetting("defaultModel", modelID)
}

// SetDefaultModelAndProvider writes both atomically.
func (m *SettingsManager) SetDefaultModelAndProvider(provider, modelID string) {
	m.mu.Lock()
	m.globalSettings["defaultProvider"] = provider
	m.globalSettings["defaultModel"] = modelID
	m.markModified("defaultProvider", "")
	m.markModified("defaultModel", "")
	m.saveGlobal()
	m.mu.Unlock()
}

// GetSteeringMode defaults to one-at-a-time.
func (m *SettingsManager) GetSteeringMode() string {
	value, _ := m.GetSetting("steeringMode")
	text, _ := value.(string)
	if text == "" {
		return "one-at-a-time"
	}
	return text
}

// SetSteeringMode writes the steering mode.
func (m *SettingsManager) SetSteeringMode(mode string) {
	m.SetSetting("steeringMode", mode)
}

// GetFollowUpMode defaults to one-at-a-time.
func (m *SettingsManager) GetFollowUpMode() string {
	value, _ := m.GetSetting("followUpMode")
	text, _ := value.(string)
	if text == "" {
		return "one-at-a-time"
	}
	return text
}

// SetFollowUpMode writes the follow-up mode.
func (m *SettingsManager) SetFollowUpMode(mode string) {
	m.SetSetting("followUpMode", mode)
}

// GetThemeSetting reads the raw theme value.
func (m *SettingsManager) GetThemeSetting() string {
	value, _ := m.GetSetting("theme")
	text, _ := value.(string)
	return text
}

// GetQuietStartup reports whether the startup header is silenced
// (settings-manager.ts getQuietStartup).
func (m *SettingsManager) GetQuietStartup() bool {
	value, _ := m.GetSetting("quietStartup")
	quiet, _ := value.(bool)
	return quiet
}

// SetQuietStartup writes the global quiet-startup flag.
func (m *SettingsManager) SetQuietStartup(quiet bool) {
	m.SetSetting("quietStartup", quiet)
}

// GetTheme returns non-path themes only.
func (m *SettingsManager) GetTheme() string {
	theme := m.GetThemeSetting()
	if strings.Contains(theme, "/") {
		return ""
	}
	return theme
}

// SetTheme writes the theme.
func (m *SettingsManager) SetTheme(theme string) {
	m.SetSetting("theme", theme)
}

// GetDefaultThinkingLevel reads the configured level.
func (m *SettingsManager) GetDefaultThinkingLevel() string {
	value, _ := m.GetSetting("defaultThinkingLevel")
	text, _ := value.(string)
	return text
}

// SetDefaultThinkingLevel writes the level.
func (m *SettingsManager) SetDefaultThinkingLevel(level string) {
	m.SetSetting("defaultThinkingLevel", level)
}

// GetModelThinkingLevels reads per-model overrides.
func (m *SettingsManager) GetModelThinkingLevels() map[string]string {
	value, _ := m.GetSetting("modelThinkingLevels")
	asMap, _ := value.(map[string]any)
	out := map[string]string{}
	for key, level := range asMap {
		if text, ok := level.(string); ok {
			out[key] = text
		}
	}
	return out
}

// SetModelThinkingLevel writes one per-model override.
func (m *SettingsManager) SetModelThinkingLevel(providerModel, level string) {
	m.SetNestedSetting("modelThinkingLevels", providerModel, level)
}

// GetShellPath reads the custom shell path.
func (m *SettingsManager) GetShellPath() string {
	value, _ := m.GetSetting("shellPath")
	text, _ := value.(string)
	return text
}

// GetShellCommandPrefix reads the bash command prefix.
func (m *SettingsManager) GetShellCommandPrefix() string {
	value, _ := m.GetSetting("shellCommandPrefix")
	text, _ := value.(string)
	return text
}

// GetEnabledModels reads the model patterns.
func (m *SettingsManager) GetEnabledModels() []string {
	return m.GetStringSlice("enabledModels")
}

// GetDefaultTools reads the tool selection.
func (m *SettingsManager) GetDefaultTools() []string {
	return m.GetStringSlice("defaultTools")
}

// GetStringSlice reads a []string setting.
func (m *SettingsManager) GetStringSlice(field string) []string {
	value, _ := m.GetSetting(field)
	asSlice, _ := value.([]any)
	out := make([]string, 0, len(asSlice))
	for _, entry := range asSlice {
		if text, ok := entry.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// GetBoolSetting reads a bool with default.
func (m *SettingsManager) GetBoolSetting(field string, fallback bool) bool {
	value, ok := m.GetSetting(field)
	if !ok {
		return fallback
	}
	asBool, ok := value.(bool)
	if !ok {
		return fallback
	}
	return asBool
}

// GetCompactionSetting reads a nested compaction field.
func (m *SettingsManager) GetCompactionSetting(field string) (any, bool) {
	value, _ := m.GetSetting("compaction")
	nested, _ := value.(map[string]any)
	if nested == nil {
		return nil, false
	}
	result, ok := nested[field]
	return result, ok
}

// GetEnableInstallTelemetry reports install telemetry (default on).
func (m *SettingsManager) GetEnableInstallTelemetry() bool {
	if value, ok := m.globalSettings["enableInstallTelemetry"].(bool); ok {
		return value
	}
	return true
}

// SetEnableInstallTelemetry persists the install telemetry setting.
func (m *SettingsManager) SetEnableInstallTelemetry(enabled bool) {
	m.globalSettings["enableInstallTelemetry"] = enabled
	m.markModified("enableInstallTelemetry", "")
	m.saveGlobal()
}
