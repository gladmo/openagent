package codingagent

// Port of core/event-bus.ts, core/timings.ts, core/source-info.ts, and
// core/pi-manifest.ts: the extension-system foundations.

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// =============================================================================
// event-bus.ts
// =============================================================================

// EventHandler receives one event payload.
type EventHandler func(data any)

// EventBus is the typed pub/sub surface extensions use.
type EventBus interface {
	Emit(channel string, data any)
	On(channel string, handler EventHandler) func()
}

// EventBusController adds Clear for shutdown.
type EventBusController interface {
	EventBus
	Clear()
}

type eventBus struct {
	mu       sync.Mutex
	channels map[string][]EventHandler
}

// CreateEventBus builds a bus whose handler errors are reported, not
// propagated.
func CreateEventBus() EventBusController {
	return &eventBus{channels: map[string][]EventHandler{}}
}

func (b *eventBus) Emit(channel string, data any) {
	b.mu.Lock()
	handlers := append([]EventHandler(nil), b.channels[channel]...)
	b.mu.Unlock()

	for _, handler := range handlers {
		func() {
			defer func() {
				if err := recover(); err != nil {
					fmt.Fprintf(os.Stderr, "Event handler error (%s): %v\n", channel, err)
				}
			}()
			handler(data)
		}()
	}
}

func (b *eventBus) On(channel string, handler EventHandler) func() {
	b.mu.Lock()
	b.channels[channel] = append(b.channels[channel], handler)
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		handlers := b.channels[channel]
		for i, existing := range handlers {
			if fmt.Sprintf("%p", existing) == fmt.Sprintf("%p", handler) && i >= 0 {
				b.channels[channel] = append(handlers[:i], handlers[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}
}

func (b *eventBus) Clear() {
	b.mu.Lock()
	b.channels = map[string][]EventHandler{}
	b.mu.Unlock()
}

// =============================================================================
// timings.ts
// =============================================================================

// TimingNamespace labels timing groups.
type TimingNamespace string

const (
	TimingMain       TimingNamespace = "main"
	TimingExtensions TimingNamespace = "extensions"
)

type timingNamespaceState struct {
	timings []struct {
		label string
		ms    int64
	}
	lastTime int64
}

var (
	timingsMu        sync.Mutex
	timingNamespaces = map[TimingNamespace]*timingNamespaceState{}
	timingsEnabled   = os.Getenv("PI_TIMING") == "1"
)

// ResetTimings clears one namespace's measurements.
func ResetTimings(namespace TimingNamespace) {
	if !timingsEnabled {
		return
	}
	timingsMu.Lock()
	defer timingsMu.Unlock()
	timingNamespaces[namespace] = &timingNamespaceState{lastTime: time.Now().UnixMilli()}
}

// Time records one measurement since the last mark.
func Time(label string, namespace TimingNamespace) {
	if !timingsEnabled {
		return
	}
	if namespace == "" {
		namespace = TimingMain
	}
	now := time.Now().UnixMilli()

	timingsMu.Lock()
	defer timingsMu.Unlock()
	state, ok := timingNamespaces[namespace]
	if !ok {
		state = &timingNamespaceState{lastTime: now}
		timingNamespaces[namespace] = state
	}
	state.timings = append(state.timings, struct {
		label string
		ms    int64
	}{label, now - state.lastTime})
	state.lastTime = now
}

// PrintTimings writes all namespaces to stderr.
func PrintTimings() {
	if !timingsEnabled {
		return
	}
	timingsMu.Lock()
	defer timingsMu.Unlock()
	for namespace, state := range timingNamespaces {
		printTimingGroup("Startup Timings: "+string(namespace), state)
	}
}

func printTimingGroup(title string, state *timingNamespaceState) {
	if len(state.timings) == 0 {
		return
	}
	total := int64(0)
	fmt.Fprintf(os.Stderr, "\n--- %s ---\n", title)
	for _, timing := range state.timings {
		fmt.Fprintf(os.Stderr, "  %s: %dms\n", timing.label, timing.ms)
		total += timing.ms
	}
	fmt.Fprintf(os.Stderr, "  TOTAL: %dms\n", total)
	fmt.Fprintf(os.Stderr, "%s\n\n", repeatString("-", len(title)+8))
}

func repeatString(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

// =============================================================================
// source-info.ts
// =============================================================================

// SourceScope is where a resource was discovered.
type SourceScope string

const (
	ScopeUser      SourceScope = "user"
	ScopeProject   SourceScope = "project"
	ScopeTemporary SourceScope = "temporary"
)

// SourceOrigin distinguishes package-managed from top-level resources.
type SourceOrigin string

const (
	OriginPackage  SourceOrigin = "package"
	OriginTopLevel SourceOrigin = "top-level"
)

// PathMetadata describes a discovered resource path (package-manager.ts).
type PathMetadata struct {
	Source      string
	Scope       SourceScope
	Origin      SourceOrigin
	BaseDir     string
	PackageRoot string
}

// SourceInfo attributes a loaded resource to its origin.
type SourceInfo struct {
	Path    string
	Source  string
	Scope   SourceScope
	Origin  SourceOrigin
	BaseDir string
}

// CreateSourceInfo builds SourceInfo from discovery metadata.
func CreateSourceInfo(path string, metadata PathMetadata) SourceInfo {
	return SourceInfo{
		Path:    path,
		Source:  metadata.Source,
		Scope:   metadata.Scope,
		Origin:  metadata.Origin,
		BaseDir: metadata.BaseDir,
	}
}

// CreateSyntheticSourceInfo builds SourceInfo for non-discovered resources.
func CreateSyntheticSourceInfo(path string, source string, scope SourceScope, origin SourceOrigin, baseDir string) SourceInfo {
	if scope == "" {
		scope = ScopeTemporary
	}
	if origin == "" {
		origin = OriginTopLevel
	}
	return SourceInfo{Path: path, Source: source, Scope: scope, Origin: origin, BaseDir: baseDir}
}

// =============================================================================
// pi-manifest.ts
// =============================================================================

// PiManifest lists package resources declared in package.json's pi field.
type PiManifest struct {
	Extensions []string `json:"extensions"`
	Skills     []string `json:"skills"`
	Prompts    []string `json:"prompts"`
	Themes     []string `json:"themes"`
}

var piManifestFields = []string{"extensions", "skills", "prompts", "themes"}

// ReadPiManifest reads the pi resource manifest from a package.json; nil
// when absent or malformed.
func ReadPiManifest(packageJsonPath string) *PiManifest {
	data, err := os.ReadFile(packageJsonPath)
	if err != nil {
		return nil
	}
	var pkg map[string]any
	if err := json.Unmarshal([]byte(StripBom(string(data))), &pkg); err != nil {
		return nil
	}
	piField, ok := pkg["pi"].(map[string]any)
	if !ok {
		return nil
	}

	manifest := &PiManifest{}
	for _, field := range piManifestFields {
		entries, ok := piField[field].([]any)
		if !ok {
			continue
		}
		strings := make([]string, 0, len(entries))
		valid := true
		for _, entry := range entries {
			text, ok := entry.(string)
			if !ok {
				valid = false
				break
			}
			strings = append(strings, text)
		}
		if !valid {
			continue
		}
		switch field {
		case "extensions":
			manifest.Extensions = strings
		case "skills":
			manifest.Skills = strings
		case "prompts":
			manifest.Prompts = strings
		case "themes":
			manifest.Themes = strings
		}
	}
	return manifest
}
