package codingagent

// Port of core/extensions/types.ts (Extension surface) and the core of
// core/extensions/runner.ts: the ExtensionRunner with event dispatch to
// per-event handler lists, error reporting, invalidation, and the runtime
// action binding. Extension loading from TS modules is the documented
// jiti deviation: Go-native extensions register handlers directly.

import (
	"fmt"
	"sync"
)

// ExtensionHandler is one event subscriber.
type ExtensionHandler func(event any) error

// RegisteredTool pairs a definition with its source metadata.
type RegisteredTool struct {
	Definition *ToolDefinition
}

// RegisteredCommand is one slash command.
type RegisteredCommand struct {
	Name        string
	Description string
	// GetArgumentCompletions returns completions for the command's args.
	GetArgumentCompletions func(argumentPrefix string) []struct {
		Value       string
		Label       string
		Description string
	}
}

// ExtensionFlag is a CLI flag registered by an extension.
type ExtensionFlag struct {
	Name        string
	Type        string // "string" | "boolean"
	Description string
}

// ExtensionShortcut is a key binding registered by an extension.
type ExtensionShortcut struct {
	KeyID   string
	Command string
	Args    []string
	When    string
}

// Extension is one loaded extension with its registrations.
type Extension struct {
	Path         string
	ResolvedPath string
	Hidden       bool
	SourceInfo   SourceInfo

	handlers  map[string][]ExtensionHandler
	tools     map[string]*RegisteredTool
	commands  map[string]*RegisteredCommand
	flags     map[string]*ExtensionFlag
	shortcuts map[string]*ExtensionShortcut
}

// NewExtension builds an empty extension.
func NewExtension(path, resolvedPath string, sourceInfo SourceInfo) *Extension {
	return &Extension{
		Path:         path,
		ResolvedPath: resolvedPath,
		SourceInfo:   sourceInfo,
		handlers:     map[string][]ExtensionHandler{},
		tools:        map[string]*RegisteredTool{},
		commands:     map[string]*RegisteredCommand{},
		flags:        map[string]*ExtensionFlag{},
		shortcuts:    map[string]*ExtensionShortcut{},
	}
}

// On subscribes one handler to an event.
func (e *Extension) On(event string, handler ExtensionHandler) {
	e.handlers[event] = append(e.handlers[event], handler)
}

// RegisterTool registers one tool.
func (e *Extension) RegisterTool(tool *RegisteredTool) {
	e.tools[tool.Definition.Name] = tool
}

// RegisterCommand registers one command.
func (e *Extension) RegisterCommand(command *RegisteredCommand) {
	e.commands[command.Name] = command
}

// RegisterFlag registers one CLI flag.
func (e *Extension) RegisterFlag(flag *ExtensionFlag) {
	e.flags[flag.Name] = flag
}

// RegisterShortcut registers one shortcut.
func (e *Extension) RegisterShortcut(shortcut *ExtensionShortcut) {
	e.shortcuts[shortcut.KeyID] = shortcut
}

// Tools lists the registered tools.
func (e *Extension) Tools() map[string]*RegisteredTool { return e.tools }

// Commands lists the registered commands.
func (e *Extension) Commands() map[string]*RegisteredCommand { return e.commands }

// Flags lists the registered flags.
func (e *Extension) Flags() map[string]*ExtensionFlag { return e.flags }

// Shortcuts lists the registered shortcuts.
func (e *Extension) Shortcuts() map[string]*ExtensionShortcut { return e.shortcuts }

// ExtensionError reports one handler failure.
type ExtensionError struct {
	ExtensionPath string
	Event         string
	Error         string
}

func (e *ExtensionError) String() string {
	return fmt.Sprintf("Extension error (%s) during %s: %s", e.ExtensionPath, e.Event, e.Error)
}

// LoadExtensionsResult is the loader outcome.
type LoadExtensionsResult struct {
	Extensions []*Extension
	Errors     []struct {
		Path  string
		Error string
	}
	Warnings []struct {
		Path    string
		Warning string
	}
}

// ExtensionRunner dispatches events to extensions.
type ExtensionRunner struct {
	mu         sync.Mutex
	extensions []*Extension
	cwd        string

	errorListeners []func(*ExtensionError)

	staleMessage string
}

// NewExtensionRunner builds the runner.
func NewExtensionRunner(extensions []*Extension, cwd string) *ExtensionRunner {
	return &ExtensionRunner{extensions: append([]*Extension(nil), extensions...), cwd: cwd}
}

// HasHandlers reports whether an event has subscribers.
func (r *ExtensionRunner) HasHandlers(event string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, extension := range r.extensions {
		if len(extension.handlers[event]) > 0 {
			return true
		}
	}
	return false
}

// Emit dispatches one event to every subscriber; handler errors are
// reported through the error listeners, never propagated.
func (r *ExtensionRunner) Emit(event string, payload any) {
	r.mu.Lock()
	extensions := append([]*Extension(nil), r.extensions...)
	var listeners []func(*ExtensionError)
	listeners = append(listeners, r.errorListeners...)
	r.mu.Unlock()

	for _, extension := range extensions {
		for _, handler := range extension.handlers[event] {
			result := func() (err error) {
				defer func() {
					if recovered := recover(); recovered != nil {
						err = fmt.Errorf("%v", recovered)
					}
				}()
				return handler(payload)
			}()
			if result != nil {
				report := &ExtensionError{
					ExtensionPath: extension.Path,
					Event:         event,
					Error:         result.Error(),
				}
				for _, listener := range listeners {
					listener(report)
				}
			}
		}
	}
}

// OnError registers an error listener.
func (r *ExtensionRunner) OnError(listener func(*ExtensionError)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errorListeners = append(r.errorListeners, listener)
}

// Invalidate marks all extension contexts stale (session replacement).
func (r *ExtensionRunner) Invalidate(message string) {
	r.mu.Lock()
	r.staleMessage = message
	r.mu.Unlock()
}

// StaleMessage reports the invalidation reason ("" when live).
func (r *ExtensionRunner) StaleMessage() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.staleMessage
}

// Extensions lists the runner's extensions.
func (r *ExtensionRunner) Extensions() []*Extension {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*Extension(nil), r.extensions...)
}

// AllTools aggregates every extension's tools by name (first wins).
func (r *ExtensionRunner) AllTools() map[string]*RegisteredTool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]*RegisteredTool{}
	for _, extension := range r.extensions {
		for name, tool := range extension.tools {
			if _, exists := out[name]; !exists {
				out[name] = tool
			}
		}
	}
	return out
}

// AllCommands aggregates every extension's commands (first wins).
func (r *ExtensionRunner) AllCommands() map[string]*RegisteredCommand {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]*RegisteredCommand{}
	for _, extension := range r.extensions {
		for name, command := range extension.commands {
			if _, exists := out[name]; !exists {
				out[name] = command
			}
		}
	}
	return out
}

// AllFlags aggregates every extension's flags (first wins).
func (r *ExtensionRunner) AllFlags() map[string]*ExtensionFlag {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]*ExtensionFlag{}
	for _, extension := range r.extensions {
		for name, flag := range extension.flags {
			if _, exists := out[name]; !exists {
				out[name] = flag
			}
		}
	}
	return out
}
