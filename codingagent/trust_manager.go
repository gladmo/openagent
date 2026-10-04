package codingagent

// Port of core/trust-manager.ts: the project-trust store (trust.json with
// nearest-ancestor lookup) and trust-requiring resource detection.

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// trustRequiringProjectConfigResources lists .pi entries that gate trust.
var trustRequiringProjectConfigResources = []string{
	"settings.json", "extensions", "skills", "prompts", "themes",
	"SYSTEM.md", "APPEND_SYSTEM.md",
}

func normalizeTrustCwd(cwd string) string {
	return CanonicalizePath(ResolvePath(cwd, "", PathInputOptions{}))
}

// findNearestTrustEntry walks ancestors for a recorded decision.
func findNearestTrustEntry(data map[string]*bool, cwd string) *struct {
	Path     string
	Decision bool
} {
	current := normalizeTrustCwd(cwd)
	for {
		if value, ok := data[current]; ok && value != nil {
			return &struct {
				Path     string
				Decision bool
			}{Path: current, Decision: *value}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

// GetProjectTrustParentPath returns the parent directory ("" at root).
func GetProjectTrustParentPath(cwd string) string {
	trustPath := normalizeTrustCwd(cwd)
	parent := filepath.Dir(trustPath)
	if parent == trustPath {
		return ""
	}
	return parent
}

// HasTrustRequiringProjectResources reports whether cwd (or an ancestor)
// carries trust-gating resources: .pi config entries or .agents/skills
// directories (excluding the user's own home skills dir).
func HasTrustRequiringProjectResources(cwd string) bool {
	homeDir := CanonicalizePath(ResolvePath(osGetenv("HOME"), "", PathInputOptions{}))
	userAgentsSkillsDir := filepath.Join(homeDir, ".agents", "skills")
	current := normalizeTrustCwd(cwd)

	configDir := filepath.Join(current, ConfigDirName())
	for _, entry := range trustRequiringProjectConfigResources {
		if _, err := os.Stat(filepath.Join(configDir, entry)); err == nil {
			return true
		}
	}

	for {
		agentsSkillsDir := filepath.Join(current, ".agents", "skills")
		if agentsSkillsDir != userAgentsSkillsDir {
			if _, err := os.Stat(agentsSkillsDir); err == nil {
				return true
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

// ProjectTrustStore persists trust decisions in trust.json.
type ProjectTrustStore struct {
	trustPath string
	mu        chan struct{}
}

// NewProjectTrustStore builds the store under agentDir.
func NewProjectTrustStore(agentDir string) *ProjectTrustStore {
	store := &ProjectTrustStore{
		trustPath: filepath.Join(ResolvePath(agentDir, "", PathInputOptions{}), "trust.json"),
		mu:        make(chan struct{}, 1),
	}
	store.mu <- struct{}{}
	return store
}

func (s *ProjectTrustStore) lock()   { <-s.mu }
func (s *ProjectTrustStore) unlock() { s.mu <- struct{}{} }

func (s *ProjectTrustStore) read() map[string]*bool {
	data, err := os.ReadFile(s.trustPath)
	if err != nil {
		return map[string]*bool{}
	}
	var parsed map[string]*bool
	if err := json.Unmarshal(data, &parsed); err != nil {
		return map[string]*bool{}
	}
	return parsed
}

func (s *ProjectTrustStore) write(data map[string]*bool) {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(s.trustPath), 0o755)
	os.WriteFile(s.trustPath, append(encoded, '\n'), 0o644)
}

// Get returns the nearest-ancestor decision (nil when unrecorded).
func (s *ProjectTrustStore) Get(cwd string) (bool, bool) {
	s.lock()
	defer s.unlock()
	entry := findNearestTrustEntry(s.read(), cwd)
	if entry == nil {
		return false, false
	}
	return entry.Decision, true
}

// Set records one decision (nil clears).
func (s *ProjectTrustStore) Set(cwd string, decision *bool) {
	s.SetMany([]struct {
		Path     string
		Decision *bool
	}{{Path: cwd, Decision: decision}})
}

// SetMany records several decisions atomically.
func (s *ProjectTrustStore) SetMany(decisions []struct {
	Path     string
	Decision *bool
}) {
	s.lock()
	defer s.unlock()
	data := s.read()
	for _, update := range decisions {
		key := normalizeTrustCwd(update.Path)
		if update.Decision == nil {
			delete(data, key)
		} else {
			value := *update.Decision
			data[key] = &value
		}
	}
	s.write(data)
}
