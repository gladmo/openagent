package pico3

// harness.go ports harness/pico3/harness.ts's registration core: the
// harness facade owning kinds, namespaces, tools, sections, entry kinds,
// revision counters, plugin handlers, and hook registrations. The builtin
// kind set is fixed; user kinds may not replace or shadow a built-in; task
// kind names beginning with "pi." are reserved.

import (
	"fmt"
	"sort"
	"sync"
)

// HarnessOptions mirrors the TS interface.
type HarnessOptions struct {
	TaskKinds []*KindPhases
	OnReport  func(err error)
}

// PluginHandler mirrors the TS plugin handler surface.
type PluginHandler struct {
	ID     string
	Handle func(harness *Harness, ctx ContextAlias) error
}

// NamespaceRegistration mirrors the TS shape for view projection.
type NamespaceRegistration struct {
	ID       string
	Defaults *NamespaceDefaults
	Project  func(merged *JSONObjectAlias) *JSONObjectAlias
}

// NamespaceDefaults carries per-doc plugin defaults.
type NamespaceDefaults struct {
	Rewindable *JSONObjectAlias
	Sticky     *JSONObjectAlias
	Session    *JSONObjectAlias
}

// BuiltinKindNames lists the fixed core kinds.
var BuiltinKindNames = []string{
	"pi.generation", "pi.tool", "pi.post_tools", "pi.collapse", "pi.job", "pi.plugin",
}

// Harness is the pico3 facade.
type Harness struct {
	mu         sync.Mutex
	session    *Session
	kinds      map[string]*KindPhases
	namespaces map[string]*NamespaceRegistration
	tools      map[string]*ToolDeclaration
	sectionMap map[string]SystemSection
	entryKinds map[string]EntryKindWitness
	revisions  struct {
		Sections int64
		Tools    int64
	}
	plugins           map[string]PluginHandler
	hookRegistrations []HookRegistration
	onReport          func(err error)
	nowFn             func() float64
	resumed           bool
	suspended         bool
}

// OpenHarness builds and initializes a harness over a storage.
func OpenHarness(storage Storage, options HarnessOptions) (*Harness, error) {
	session, err := NewSession(storage)
	if err != nil {
		return nil, err
	}
	harness := &Harness{
		session:    session,
		kinds:      map[string]*KindPhases{},
		namespaces: map[string]*NamespaceRegistration{},
		tools:      map[string]*ToolDeclaration{},
		sectionMap: map[string]SystemSection{},
		entryKinds: map[string]EntryKindWitness{},
		plugins:    map[string]PluginHandler{},
		onReport:   func(err error) {},
		nowFn:      func() float64 { return float64(0) },
	}
	if options.OnReport != nil {
		harness.onReport = func(err error) {
			defer func() { _ = recover() }()
			options.OnReport(err)
		}
	}
	// Fixed core kinds; user kinds cannot shadow them.
	for _, name := range BuiltinKindNames {
		harness.kinds[name] = &KindPhases{Name: name, Phases: map[string]Handler{}}
	}
	for _, kind := range options.TaskKinds {
		if err := harness.registerKind(kind); err != nil {
			_ = session.Close()
			return nil, err
		}
	}
	return harness, nil
}

func (h *Harness) registerKind(kind *KindPhases) error {
	if len(kind.Name) >= 3 && kind.Name[:3] == "pi." {
		return fmt.Errorf("task kind %q: names beginning with %q are reserved", kind.Name, "pi.")
	}
	if _, exists := h.kinds[kind.Name]; exists {
		return fmt.Errorf("task kind %q registered twice", kind.Name)
	}
	h.kinds[kind.Name] = kind
	return nil
}

// Session exposes the owning session.
func (h *Harness) Session() *Session { return h.session }

// Kind resolves one registered kind.
func (h *Harness) Kind(name string) (*KindPhases, bool) {
	kind, ok := h.kinds[name]
	return kind, ok
}

// Kinds lists the registered kind names (sorted).
func (h *Harness) Kinds() []string {
	out := make([]string, 0, len(h.kinds))
	for name := range h.kinds {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// RegisterNamespace installs a plugin namespace (twice = error).
func (h *Harness) RegisterNamespace(registration *NamespaceRegistration) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.namespaces[registration.ID]; exists {
		return fmt.Errorf("namespace %q registered twice", registration.ID)
	}
	h.namespaces[registration.ID] = registration
	return nil
}

// Namespace resolves one namespace.
func (h *Harness) Namespace(id string) (*NamespaceRegistration, bool) {
	registration, ok := h.namespaces[id]
	return registration, ok
}

// RegisterTool installs a tool (name clash = error).
func (h *Harness) RegisterTool(tool *ToolDeclaration) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.tools[tool.Name]; exists {
		return fmt.Errorf("tool %q registered twice", tool.Name)
	}
	h.tools[tool.Name] = tool
	h.revisions.Tools++
	return nil
}

// Tool resolves one tool.
func (h *Harness) Tool(name string) (*ToolDeclaration, bool) {
	tool, ok := h.tools[name]
	return tool, ok
}

// ToolRevision exposes the tool registry revision.
func (h *Harness) ToolRevision() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.revisions.Tools
}

// RegisterSection installs a system section (key clash = error).
func (h *Harness) RegisterSection(section SystemSection) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.sectionMap[section.Key]; exists {
		return fmt.Errorf("section %q registered twice", section.Key)
	}
	h.sectionMap[section.Key] = section
	h.revisions.Sections++
	return nil
}

// Section resolves one section.
func (h *Harness) Section(key string) (SystemSection, bool) {
	section, ok := h.sectionMap[key]
	return section, ok
}

// SectionRevision exposes the section registry revision.
func (h *Harness) SectionRevision() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.revisions.Sections
}

// RegisterEntryKind installs a custom entry kind witness.
func (h *Harness) RegisterEntryKind(witness EntryKindWitness) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.entryKinds[witness.KindName]; exists {
		return fmt.Errorf("entry kind %q registered twice", witness.KindName)
	}
	h.entryKinds[witness.KindName] = witness
	return nil
}

// RegisterHook appends a hook registration.
func (h *Harness) RegisterHook(registration HookRegistration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hookRegistrations = append(h.hookRegistrations, registration)
}

// HookRegistrations snapshots the registrations.
func (h *Harness) HookRegistrations() []HookRegistration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HookRegistration{}, h.hookRegistrations...)
}

// Resumed reports whether the harness resumed its scheduler.
func (h *Harness) Resumed() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.resumed }

// Suspended reports the suspend flag.
func (h *Harness) Suspended() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.suspended }

// SetResumed marks the harness resumed.
func (h *Harness) SetResumed() { h.mu.Lock(); h.resumed = true; h.mu.Unlock() }

// SetSuspended toggles the suspend flag.
func (h *Harness) SetSuspended(v bool) { h.mu.Lock(); h.suspended = v; h.mu.Unlock() }

// Report forwards an error to the onReport callback (never panics out).
func (h *Harness) Report(err error) {
	h.onReport(err)
}
