package tui

// Port of keybindings.ts: the global keybinding registry and its manager.
// Downstream packages add keybindings by extending the definitions map.

import "sync"

// Keybinding names the bound action.
type Keybinding = string

// KeybindingDefinition declares default keys and a description.
type KeybindingDefinition struct {
	DefaultKeys []KeyId
	Description string
}

// KeybindingsConfig maps a keybinding to keys; nil means unset.
type KeybindingsConfig map[Keybinding][]KeyId

// TUIKeybindings is the built-in registry (declaration order preserved via
// keybindingsOrder for deterministic iteration).
var TUIKeybindings = map[Keybinding]KeybindingDefinition{
	"tui.editor.cursorUp":           {DefaultKeys: []KeyId{"up"}, Description: "Move cursor up"},
	"tui.editor.cursorDown":         {DefaultKeys: []KeyId{"down"}, Description: "Move cursor down"},
	"tui.editor.historyPrevious":    {Description: "Select previous prompt history entry"},
	"tui.editor.historyNext":        {Description: "Select next prompt history entry"},
	"tui.editor.cursorLeft":         {DefaultKeys: []KeyId{"left", "ctrl+b"}, Description: "Move cursor left"},
	"tui.editor.cursorRight":        {DefaultKeys: []KeyId{"right", "ctrl+f"}, Description: "Move cursor right"},
	"tui.editor.cursorWordLeft":     {DefaultKeys: []KeyId{"alt+left", "ctrl+left", "alt+b"}, Description: "Move cursor word left"},
	"tui.editor.cursorWordRight":    {DefaultKeys: []KeyId{"alt+right", "ctrl+right", "alt+f"}, Description: "Move cursor word right"},
	"tui.editor.cursorLineStart":    {DefaultKeys: []KeyId{"home", "ctrl+a"}, Description: "Move to line start"},
	"tui.editor.cursorLineEnd":      {DefaultKeys: []KeyId{"end", "ctrl+e"}, Description: "Move to line end"},
	"tui.editor.jumpForward":        {DefaultKeys: []KeyId{"ctrl+]"}, Description: "Jump forward to character"},
	"tui.editor.jumpBackward":       {DefaultKeys: []KeyId{"ctrl+alt+]"}, Description: "Jump backward to character"},
	"tui.editor.pageUp":             {DefaultKeys: []KeyId{"pageUp", "ctrl+pageUp"}, Description: "Page up"},
	"tui.editor.pageDown":           {DefaultKeys: []KeyId{"pageDown", "ctrl+pageDown"}, Description: "Page down"},
	"tui.editor.deleteCharBackward": {DefaultKeys: []KeyId{"backspace"}, Description: "Delete character backward"},
	"tui.editor.deleteCharForward":  {DefaultKeys: []KeyId{"delete", "ctrl+d"}, Description: "Delete character forward"},
	"tui.editor.deleteWordBackward": {DefaultKeys: []KeyId{"ctrl+w", "alt+backspace"}, Description: "Delete word backward"},
	"tui.editor.deleteWordForward":  {DefaultKeys: []KeyId{"alt+d", "alt+delete"}, Description: "Delete word forward"},
	"tui.editor.deleteToLineStart":  {DefaultKeys: []KeyId{"ctrl+u"}, Description: "Delete to line start"},
	"tui.editor.deleteToLineEnd":    {DefaultKeys: []KeyId{"ctrl+k"}, Description: "Delete to line end"},
	"tui.editor.yank":               {DefaultKeys: []KeyId{"ctrl+y"}, Description: "Yank"},
	"tui.editor.yankPop":            {DefaultKeys: []KeyId{"alt+y"}, Description: "Yank pop"},
	"tui.editor.undo":               {DefaultKeys: []KeyId{"ctrl+-"}, Description: "Undo"},
	"tui.input.newLine":             {DefaultKeys: []KeyId{"shift+enter", "ctrl+j"}, Description: "Insert newline"},
	"tui.input.submit":              {DefaultKeys: []KeyId{"enter"}, Description: "Submit input"},
	"tui.input.tab":                 {DefaultKeys: []KeyId{"tab"}, Description: "Tab / autocomplete"},
	"tui.input.copy":                {DefaultKeys: []KeyId{"ctrl+c"}, Description: "Copy selection"},
	"tui.select.up":                 {DefaultKeys: []KeyId{"up"}, Description: "Move selection up"},
	"tui.select.down":               {DefaultKeys: []KeyId{"down"}, Description: "Move selection down"},
	"tui.select.pageUp":             {DefaultKeys: []KeyId{"pageUp"}, Description: "Selection page up"},
	"tui.select.pageDown":           {DefaultKeys: []KeyId{"pageDown"}, Description: "Selection page down"},
	"tui.select.confirm":            {DefaultKeys: []KeyId{"enter"}, Description: "Confirm selection"},
	"tui.select.cancel":             {DefaultKeys: []KeyId{"escape", "ctrl+c"}, Description: "Cancel selection"},
	// These intentionally shadow the unmodified editor bindings in fullscreen
	// mode.
	"tui.altScreen.pageUp":         {DefaultKeys: []KeyId{"pageUp"}, Description: "Scroll viewport up one page"},
	"tui.altScreen.pageDown":       {DefaultKeys: []KeyId{"pageDown"}, Description: "Scroll viewport down one page"},
	"tui.altScreen.halfPageUp":     {Description: "Scroll viewport up half a page"},
	"tui.altScreen.halfPageDown":   {Description: "Scroll viewport down half a page"},
	"tui.altScreen.lineUp":         {Description: "Scroll viewport up one line"},
	"tui.altScreen.lineDown":       {Description: "Scroll viewport down one line"},
	"tui.altScreen.previousPrompt": {DefaultKeys: []KeyId{"ctrl+shift+up", "ctrl+up"}, Description: "Jump to previous semantic prompt"},
	"tui.altScreen.nextPrompt":     {DefaultKeys: []KeyId{"ctrl+shift+down", "ctrl+down"}, Description: "Jump to next semantic prompt"},
	"tui.altScreen.search":         {DefaultKeys: []KeyId{"ctrl+shift+f"}, Description: "Search the primary scroll view"},
	"tui.altScreen.searchNext":     {DefaultKeys: []KeyId{"enter", "ctrl+g"}, Description: "Select the next search match"},
	"tui.altScreen.searchPrevious": {DefaultKeys: []KeyId{"shift+enter", "ctrl+shift+g"}, Description: "Select the previous search match"},
	"tui.altScreen.searchClose":    {DefaultKeys: []KeyId{"escape"}, Description: "Close transcript search"},
	"tui.altScreen.top":            {DefaultKeys: []KeyId{"ctrl+home"}, Description: "Scroll viewport to top"},
	"tui.altScreen.bottom":         {DefaultKeys: []KeyId{"ctrl+end"}, Description: "Scroll viewport to bottom"},
}

// KeybindingConflict reports a key claimed by multiple user bindings.
type KeybindingConflict struct {
	Key         KeyId
	Keybindings []Keybinding
}

func normalizeKeys(keys []KeyId) []KeyId {
	seen := map[KeyId]bool{}
	var result []KeyId
	for _, key := range keys {
		if !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	return result
}

// KeybindingsManager resolves user bindings over definitions.
type KeybindingsManager struct {
	mu            sync.Mutex
	definitions   map[Keybinding]KeybindingDefinition
	definitionIDs []Keybinding
	userBindings  KeybindingsConfig
	keysByID      map[Keybinding][]KeyId
	conflicts     []KeybindingConflict
}

// NewKeybindingsManager builds a manager; definitions iteration order follows
// the first-seen order of the map's entries via definitionIDs (filled
// deterministically on first rebuild).
func NewKeybindingsManager(definitions map[Keybinding]KeybindingDefinition, userBindings KeybindingsConfig) *KeybindingsManager {
	manager := &KeybindingsManager{
		definitions:  definitions,
		userBindings: userBindings,
	}
	if userBindings == nil {
		manager.userBindings = KeybindingsConfig{}
	}
	manager.rebuild()
	return manager
}

func (m *KeybindingsManager) rebuild() {
	m.keysByID = map[Keybinding][]KeyId{}
	m.conflicts = nil

	if m.definitionIDs == nil {
		for id := range m.definitions {
			m.definitionIDs = append(m.definitionIDs, id)
		}
		sortStrings(m.definitionIDs)
	}

	userClaims := map[KeyId]map[Keybinding]bool{}
	for keybinding, keys := range m.userBindings {
		if _, defined := m.definitions[keybinding]; !defined {
			continue
		}
		for _, key := range normalizeKeys(keys) {
			claimants := userClaims[key]
			if claimants == nil {
				claimants = map[Keybinding]bool{}
				userClaims[key] = claimants
			}
			claimants[keybinding] = true
		}
	}

	for _, id := range sortKeyIDs(userClaims) {
		claimants := userClaims[id]
		if len(claimants) > 1 {
			conflict := KeybindingConflict{Key: id}
			for _, keybinding := range sortKeybindings(claimants) {
				conflict.Keybindings = append(conflict.Keybindings, keybinding)
			}
			m.conflicts = append(m.conflicts, conflict)
		}
	}

	for _, id := range m.definitionIDs {
		userKeys, hasUser := m.userBindings[id]
		var keys []KeyId
		if !hasUser {
			keys = normalizeKeys(m.definitions[id].DefaultKeys)
		} else {
			keys = normalizeKeys(userKeys)
		}
		m.keysByID[id] = keys
	}
}

// Matches reports whether raw input triggers the keybinding.
func (m *KeybindingsManager) Matches(data string, keybinding Keybinding) bool {
	m.mu.Lock()
	keys := m.keysByID[keybinding]
	m.mu.Unlock()
	for _, key := range keys {
		if MatchesKey(data, key) {
			return true
		}
	}
	return false
}

// GetKeys returns the resolved keys for a keybinding.
func (m *KeybindingsManager) GetKeys(keybinding Keybinding) []KeyId {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]KeyId(nil), m.keysByID[keybinding]...)
}

// GetDefinition returns the definition for a keybinding.
func (m *KeybindingsManager) GetDefinition(keybinding Keybinding) (KeybindingDefinition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	definition, ok := m.definitions[keybinding]
	return definition, ok
}

// GetConflicts returns the current conflicts.
func (m *KeybindingsManager) GetConflicts() []KeybindingConflict {
	m.mu.Lock()
	defer m.mu.Unlock()
	conflicts := make([]KeybindingConflict, len(m.conflicts))
	for i, conflict := range m.conflicts {
		conflicts[i] = KeybindingConflict{Key: conflict.Key, Keybindings: append([]Keybinding(nil), conflict.Keybindings...)}
	}
	return conflicts
}

// SetUserBindings replaces the user bindings and rebuilds.
func (m *KeybindingsManager) SetUserBindings(userBindings KeybindingsConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userBindings = userBindings
	m.rebuild()
}

// GetUserBindings copies the current user bindings.
func (m *KeybindingsManager) GetUserBindings() KeybindingsConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := KeybindingsConfig{}
	for id, keys := range m.userBindings {
		copied[id] = append([]KeyId(nil), keys...)
	}
	return copied
}

// GetResolvedBindings returns every definition's resolved keys.
func (m *KeybindingsManager) GetResolvedBindings() KeybindingsConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	resolved := KeybindingsConfig{}
	for _, id := range m.definitionIDs {
		resolved[id] = append([]KeyId(nil), m.keysByID[id]...)
	}
	return resolved
}

var globalKeybindings *KeybindingsManager
var globalKeybindingsOnce sync.Once

// SetKeybindings installs the global registry.
func SetKeybindings(keybindings *KeybindingsManager) {
	globalKeybindings = keybindings
}

// GetKeybindings returns the global registry, defaulting to TUIKeybindings.
func GetKeybindings() *KeybindingsManager {
	if globalKeybindings != nil {
		return globalKeybindings
	}
	globalKeybindingsOnce.Do(func() {
		globalKeybindings = NewKeybindingsManager(TUIKeybindings, nil)
	})
	return globalKeybindings
}
