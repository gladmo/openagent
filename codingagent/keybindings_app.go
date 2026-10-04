package codingagent

// Port of core/keybindings.ts: the app keybinding catalog (layered over
// the ported tui TUI_KEYBINDINGS with platform-aware overrides), legacy
// name migrations, and the user-config loader.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	"github.com/gladmo/openagent/tui"
)

// Keybinding and KeyId alias the tui types.
type Keybinding = tui.Keybinding
type KeyId = tui.KeyId

// KeybindingDefinition carries default keys and a description.
type KeybindingDefinition = tui.KeybindingDefinition

// KeybindingsConfig maps bindings to user keys.
type KeybindingsConfig = tui.KeybindingsConfig

// UseWindowsKeybindings detects Windows/WSL platforms.
func UseWindowsKeybindings(platform string, env map[string]string) bool {
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform == "windows" {
		return true
	}
	if platform == "linux" {
		return env["WSL_DISTRO_NAME"] != "" || env["WSL_INTEROP"] != ""
	}
	return false
}

func isWindows() bool { return runtime.GOOS == "windows" }
func isDarwin() bool  { return runtime.GOOS == "darwin" }

// AppKeybindings returns the app-layer catalog merged over the tui
// definitions with the platform overrides from the reference.
func AppKeybindings() map[Keybinding]KeybindingDefinition {
	windowsKeys := UseWindowsKeybindings("", nil)
	isWin := isWindows()

	definitions := map[Keybinding]KeybindingDefinition{}
	for key, definition := range tui.TUIKeybindings {
		definitions[key] = definition
	}

	// Platform overrides on the tui layer.
	definitions["tui.editor.undo"] = KeybindingDefinition{
		Description: definitions["tui.editor.undo"].Description,
		DefaultKeys: []KeyId{KeyId(platformUndoKey(isWin, windowsKeys))},
	}
	definitions["tui.altScreen.previousPrompt"] = KeybindingDefinition{
		Description: definitions["tui.altScreen.previousPrompt"].Description,
		DefaultKeys: platformPromptKeys(windowsKeys, "up"),
	}
	definitions["tui.altScreen.nextPrompt"] = KeybindingDefinition{
		Description: definitions["tui.altScreen.nextPrompt"].Description,
		DefaultKeys: platformPromptKeys(windowsKeys, "down"),
	}
	definitions["tui.altScreen.search"] = KeybindingDefinition{
		Description: definitions["tui.altScreen.search"].Description,
		DefaultKeys: []KeyId{KeyId(platformSearchKey(windowsKeys))},
	}

	single := func(key string) []KeyId { return []KeyId{KeyId(key)} }
	app := map[Keybinding]KeybindingDefinition{
		"app.interrupt":                 {DefaultKeys: single("escape"), Description: "Cancel or abort"},
		"app.clear":                     {DefaultKeys: single("ctrl+c"), Description: "Clear editor"},
		"app.exit":                      {DefaultKeys: single("ctrl+d"), Description: "Exit when editor is empty"},
		"app.thinking.cycle":            {DefaultKeys: single("shift+tab"), Description: "Cycle thinking level"},
		"app.thinking.save":             {DefaultKeys: single("ctrl+s"), Description: "Save thinking level"},
		"app.model.cycleForward":        {DefaultKeys: single("ctrl+p"), Description: "Cycle to next model"},
		"app.model.cycleBackward":       {DefaultKeys: single(platformModelBackward(windowsKeys)), Description: "Cycle to previous model"},
		"app.model.select":              {DefaultKeys: single("ctrl+l"), Description: "Open model selector"},
		"app.tools.expand":              {DefaultKeys: single("ctrl+o"), Description: "Toggle tool output"},
		"app.thinking.toggle":           {DefaultKeys: single("ctrl+t"), Description: "Toggle thinking blocks"},
		"app.session.toggleNamedFilter": {DefaultKeys: single("ctrl+n"), Description: "Toggle named session filter"},
		"app.editor.external":           {DefaultKeys: single("ctrl+g"), Description: "Open external editor"},
		"app.message.copy":              {DefaultKeys: single("ctrl+x"), Description: "Copy selection or last assistant message"},
		"app.message.followUp":          {DefaultKeys: single(platformFollowUp(windowsKeys)), Description: "Queue follow-up message"},
		"app.message.dequeue":           {DefaultKeys: single(platformDequeue(windowsKeys)), Description: "Restore queued messages"},
		"app.clipboard.pasteImage":      {DefaultKeys: single(platformPasteImage(windowsKeys)), Description: "Paste image from clipboard (text fallback)"},
		"app.session.new":               {Description: "Start a new session"},
		"app.session.tree":              {Description: "Open session tree"},
		"app.session.fork":              {Description: "Fork current session"},
		"app.session.resume":            {Description: "Resume a session"},
		"app.tree.foldOrUp":             {DefaultKeys: platformTreeKeys(isDarwin(), "left"), Description: "Fold tree branch or move up"},
		"app.tree.unfoldOrDown":         {DefaultKeys: platformTreeKeys(isDarwin(), "right"), Description: "Unfold tree branch or move down"},
		"app.tree.editLabel":            {DefaultKeys: single("shift+l"), Description: "Edit tree label"},
		"app.tree.toggleLabelTimestamp": {DefaultKeys: single("shift+t"), Description: "Toggle tree label timestamps"},
		"app.session.togglePath":        {DefaultKeys: single("ctrl+p"), Description: "Toggle session path display"},
		"app.session.toggleSort":        {DefaultKeys: single("ctrl+s"), Description: "Toggle session sort mode"},
		"app.session.rename":            {DefaultKeys: single("ctrl+r"), Description: "Rename session"},
		"app.session.delete":            {DefaultKeys: single("ctrl+d"), Description: "Delete session"},
		"app.session.deleteNoninvasive": {DefaultKeys: single("ctrl+backspace"), Description: "Delete session when query is empty"},
		"app.models.save":               {DefaultKeys: single("ctrl+s"), Description: "Save model selection"},
		"app.models.enableAll":          {DefaultKeys: single("ctrl+a"), Description: "Enable all models"},
		"app.models.clearAll":           {DefaultKeys: single("ctrl+x"), Description: "Clear all models"},
		"app.models.toggleProvider":     {DefaultKeys: single("ctrl+p"), Description: "Toggle all models for provider"},
		"app.models.reorderUp":          {DefaultKeys: single("alt+up"), Description: "Move model up in order"},
		"app.models.reorderDown":        {DefaultKeys: single("alt+down"), Description: "Move model down in order"},
		"app.tree.filter.default":       {DefaultKeys: single("ctrl+d"), Description: "Tree filter: default view"},
		"app.tree.filter.noTools":       {DefaultKeys: single("ctrl+t"), Description: "Tree filter: hide tool results"},
		"app.tree.filter.userOnly":      {DefaultKeys: single("ctrl+u"), Description: "Tree filter: user messages only"},
		"app.tree.filter.labeledOnly":   {DefaultKeys: single("ctrl+l"), Description: "Tree filter: labeled entries only"},
		"app.tree.filter.all":           {DefaultKeys: single("ctrl+a"), Description: "Tree filter: show all entries"},
		"app.tree.filter.cycleForward":  {DefaultKeys: single("ctrl+o"), Description: "Tree filter: cycle forward"},
		"app.tree.filter.cycleBackward": {DefaultKeys: single("shift+ctrl+o"), Description: "Tree filter: cycle backward"},
	}
	if !isWin {
		definitions["app.suspend"] = KeybindingDefinition{DefaultKeys: single("ctrl+z"), Description: "Suspend to background"}
	} else {
		definitions["app.suspend"] = KeybindingDefinition{Description: "Suspend to background"}
	}
	for key, definition := range app {
		definitions[key] = definition
	}
	return definitions
}

func platformUndoKey(isWin, windowsKeys bool) string {
	switch {
	case isWin:
		return "ctrl+z"
	case windowsKeys:
		return "alt+z"
	default:
		return "ctrl+-"
	}
}

func platformPromptKeys(windowsKeys bool, direction string) []KeyId {
	if windowsKeys {
		return []KeyId{KeyId("ctrl+" + direction)}
	}
	return []KeyId{KeyId("ctrl+shift+" + direction), KeyId("ctrl+" + direction)}
}

func platformSearchKey(windowsKeys bool) string {
	if windowsKeys {
		return "ctrl+f"
	}
	return "ctrl+shift+f"
}

func platformModelBackward(windowsKeys bool) string {
	if windowsKeys {
		return "alt+p"
	}
	return "shift+ctrl+p"
}

func platformFollowUp(windowsKeys bool) string {
	if windowsKeys {
		return "ctrl+q"
	}
	return "alt+enter"
}

func platformDequeue(windowsKeys bool) string {
	if windowsKeys {
		return "alt+q"
	}
	return "alt+up"
}

func platformPasteImage(windowsKeys bool) string {
	if windowsKeys {
		return "alt+v"
	}
	return "ctrl+v"
}

func platformTreeKeys(darwin bool, direction string) []KeyId {
	if darwin {
		return []KeyId{KeyId("alt+" + direction), KeyId("ctrl+" + direction)}
	}
	return []KeyId{KeyId("ctrl+" + direction), KeyId("alt+" + direction)}
}

// KeybindingNameMigrations maps legacy short names to qualified bindings.
var KeybindingNameMigrations = map[string]string{
	"cursorUp":                 "tui.editor.cursorUp",
	"cursorDown":               "tui.editor.cursorDown",
	"cursorLeft":               "tui.editor.cursorLeft",
	"cursorRight":              "tui.editor.cursorRight",
	"cursorWordLeft":           "tui.editor.cursorWordLeft",
	"cursorWordRight":          "tui.editor.cursorWordRight",
	"cursorLineStart":          "tui.editor.cursorLineStart",
	"cursorLineEnd":            "tui.editor.cursorLineEnd",
	"jumpForward":              "tui.editor.jumpForward",
	"jumpBackward":             "tui.editor.jumpBackward",
	"pageUp":                   "tui.editor.pageUp",
	"pageDown":                 "tui.editor.pageDown",
	"deleteCharBackward":       "tui.editor.deleteCharBackward",
	"deleteCharForward":        "tui.editor.deleteCharForward",
	"deleteWordBackward":       "tui.editor.deleteWordBackward",
	"deleteWordForward":        "tui.editor.deleteWordForward",
	"deleteToLineStart":        "tui.editor.deleteToLineStart",
	"deleteToLineEnd":          "tui.editor.deleteToLineEnd",
	"yank":                     "tui.editor.yank",
	"yankPop":                  "tui.editor.yankPop",
	"undo":                     "tui.editor.undo",
	"newLine":                  "tui.input.newLine",
	"submit":                   "tui.input.submit",
	"tab":                      "tui.input.tab",
	"copy":                     "tui.input.copy",
	"selectUp":                 "tui.select.up",
	"selectDown":               "tui.select.down",
	"selectPageUp":             "tui.select.pageUp",
	"selectPageDown":           "tui.select.pageDown",
	"selectConfirm":            "tui.select.confirm",
	"selectCancel":             "tui.select.cancel",
	"interrupt":                "app.interrupt",
	"clear":                    "app.clear",
	"exit":                     "app.exit",
	"suspend":                  "app.suspend",
	"cycleThinkingLevel":       "app.thinking.cycle",
	"cycleModelForward":        "app.model.cycleForward",
	"cycleModelBackward":       "app.model.cycleBackward",
	"selectModel":              "app.model.select",
	"expandTools":              "app.tools.expand",
	"toggleThinking":           "app.thinking.toggle",
	"toggleSessionNamedFilter": "app.session.toggleNamedFilter",
	"externalEditor":           "app.editor.external",
	"followUp":                 "app.message.followUp",
	"dequeue":                  "app.message.dequeue",
	"pasteImage":               "app.clipboard.pasteImage",
	"newSession":               "app.session.new",
	"tree":                     "app.session.tree",
	"fork":                     "app.session.fork",
	"resume":                   "app.session.resume",
	"treeFoldOrUp":             "app.tree.foldOrUp",
	"treeUnfoldOrDown":         "app.tree.unfoldOrDown",
	"treeEditLabel":            "app.tree.editLabel",
	"treeToggleLabelTimestamp": "app.tree.toggleLabelTimestamp",
	"toggleSessionPath":        "app.session.togglePath",
	"toggleSessionSort":        "app.session.toggleSort",
	"renameSession":            "app.session.rename",
	"deleteSession":            "app.session.delete",
	"deleteSessionNoninvasive": "app.session.deleteNoninvasive",
}

// ToKeybindingsConfig coerces parsed JSON into a keybindings config.
func ToKeybindingsConfig(value map[string]any) KeybindingsConfig {
	config := KeybindingsConfig{}
	for key, binding := range value {
		switch typed := binding.(type) {
		case string:
			config[Keybinding(key)] = []KeyId{KeyId(typed)}
		case []any:
			var keys []KeyId
			valid := true
			for _, entry := range typed {
				text, ok := entry.(string)
				if !ok {
					valid = false
					break
				}
				keys = append(keys, KeyId(text))
			}
			if valid && len(keys) > 0 {
				config[Keybinding(key)] = keys
			}
		}
	}
	return config
}

// LoadUserKeybindings reads keybindings.json with legacy-name migration.
func LoadUserKeybindings(agentDir string) KeybindingsConfig {
	if agentDir == "" {
		agentDir = GetAgentDir()
	}
	path := filepath.Join(agentDir, "keybindings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return KeybindingsConfig{}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(StripBom(string(data))), &parsed); err != nil {
		return KeybindingsConfig{}
	}

	config := ToKeybindingsConfig(parsed)
	// Migrate legacy names.
	migrated := KeybindingsConfig{}
	for key, keys := range config {
		if qualified, ok := KeybindingNameMigrations[string(key)]; ok {
			migrated[Keybinding(qualified)] = keys
			continue
		}
		migrated[key] = keys
	}
	return migrated
}
