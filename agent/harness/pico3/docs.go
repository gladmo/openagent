package pico3

// docs.go ports the pico3 document state shapes and the defaults routing
// that backs the ConversationView config projection: rewindable (folded
// history), sticky (the present, truncated to its last base), and session
// state, each carried as a jsonx object with typed accessors, plus the
// Defaults registry (per-kind routed keys with per-doc defaults).

import (
	"github.com/gladmo/openagent/jsonx"
)

// Rewindable keys.
const (
	KeyModel         = "model"
	KeyThinkingLevel = "thinkingLevel"
	KeySelectedTools = "selectedTools"
	KeyProfile       = "profile"
	KeyThreshold     = "threshold"
	KeyKeepRecent    = "keepRecent"
	KeyPlugins       = "plugins"
)

// Sticky keys.
const (
	KeyRetry        = "retry"
	KeySteeringMode = "steeringMode"
	KeyFollowUpMode = "followUpMode"
	KeyInbox        = "inbox"
	KeyTurn         = "turn"
	KeyTasks        = "tasks"
)

// NewRewindableState builds the empty rewindable document.
func NewRewindableState() *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set(KeyThinkingLevel, "off")
	obj.Set(KeySelectedTools, []any{})
	obj.Set(KeyProfile, "")
	obj.Set(KeyThreshold, float64(0))
	obj.Set(KeyKeepRecent, float64(0))
	obj.Set(KeyPlugins, jsonx.NewObj())
	return obj
}

// NewStickyState builds the empty sticky document.
func NewStickyState() *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set(KeyRetry, jsonx.ObjFrom("enabled", false, "maxRetries", float64(0), "baseDelayMs", float64(0)))
	obj.Set(KeySteeringMode, "all")
	obj.Set(KeyFollowUpMode, "all")
	obj.Set(KeyInbox, []any{})
	obj.Set(KeyTurn, jsonx.ObjFrom("tools", []any{}))
	obj.Set(KeyTasks, jsonx.NewObj())
	obj.Set(KeyPlugins, jsonx.NewObj())
	return obj
}

// NewSessionState builds the empty session document.
func NewSessionState() *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set(KeyPlugins, jsonx.NewObj())
	return obj
}

// DocRouter routes config keys to their owning document.
type DocRouter struct {
	// Route maps a config key to "rewindable" | "sticky".
	Route map[string]string
	// RewindableDefaults are the fallback values for rewindable-routed
	// keys absent from the folded state.
	RewindableDefaults *jsonx.Obj
	// StickyDefaults likewise for sticky-routed keys.
	StickyDefaults *jsonx.Obj
}

// Defaults is the per-conversation defaults registry.
type Defaults struct {
	Route      map[string]string
	Rewindable *jsonx.Obj
	Sticky     *jsonx.Obj
}

// CollectDefaults walks kinds' config declarations and builds the routing
// table: each kind declares {key, doc, default}; core keys are always
// present.
type KindConfigDeclaration struct {
	Key     string
	Doc     string // "rewindable" | "sticky"
	Default any
}

// CoreConfigDeclarations are the built-in config routes.
func CoreConfigDeclarations() []KindConfigDeclaration {
	return []KindConfigDeclaration{
		{Key: KeyModel, Doc: "rewindable"},
		{Key: KeyThinkingLevel, Doc: "rewindable", Default: "off"},
		{Key: KeySelectedTools, Doc: "rewindable", Default: []any{}},
		{Key: KeyProfile, Doc: "rewindable", Default: ""},
		{Key: KeyThreshold, Doc: "rewindable", Default: float64(0)},
		{Key: KeyKeepRecent, Doc: "rewindable", Default: float64(0)},
		{Key: KeyRetry, Doc: "sticky"},
		{Key: KeySteeringMode, Doc: "sticky", Default: "all"},
		{Key: KeyFollowUpMode, Doc: "sticky", Default: "all"},
	}
}

// CollectDefaults builds the routing + fallback table from declarations.
// Later declarations override earlier ones for the same key.
func CollectDefaults(declarations []KindConfigDeclaration) *Defaults {
	defaults := &Defaults{
		Route:      map[string]string{},
		Rewindable: jsonx.NewObj(),
		Sticky:     jsonx.NewObj(),
	}
	for _, declaration := range declarations {
		defaults.Route[declaration.Key] = declaration.Doc
		if declaration.Default != nil {
			switch declaration.Doc {
			case "rewindable":
				defaults.Rewindable.Set(declaration.Key, declaration.Default)
			default:
				defaults.Sticky.Set(declaration.Key, declaration.Default)
			}
		}
	}
	return defaults
}

// ProjectConfig builds the view config object: for each routed key, the
// owning document's value when present, else the registered default, else
// omitted.
func ProjectConfig(defaults *Defaults, rewindable, sticky *jsonx.Obj) *jsonx.Obj {
	out := jsonx.NewObj()
	for key, doc := range defaults.Route {
		source := rewindable
		fallback := defaults.Rewindable
		if doc != "rewindable" {
			source = sticky
			fallback = defaults.Sticky
		}
		if value, ok := source.Get(key); ok && value != nil {
			out.Set(key, cloneJSONValue(value))
			continue
		}
		if value, ok := fallback.Get(key); ok && value != nil {
			out.Set(key, cloneJSONValue(value))
		}
	}
	return out
}

// MergePlugins projects the plugins view for one namespace: registration
// defaults (rewindable, sticky, session) then document slices (rewindable,
// sticky, session) merged in order, then projected through the namespace's
// pure function.
func MergePlugins(registrationDefaults [](*jsonx.Obj), documentSlices [](*jsonx.Obj), project func(merged *jsonx.Obj) *jsonx.Obj) *jsonx.Obj {
	merged := jsonx.NewObj()
	for _, source := range append(append([]*jsonx.Obj{}, registrationDefaults...), documentSlices...) {
		if source == nil {
			continue
		}
		for _, key := range source.Keys() {
			value, _ := source.Get(key)
			merged.Set(key, cloneJSONValue(value))
		}
	}
	if project == nil {
		return merged
	}
	return project(merged)
}
