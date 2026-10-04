package codingagent

// Port of core/slash-commands.ts: the built-in slash command catalog.

import "fmt"

// SlashCommandSource names where a command came from.
type SlashCommandSource string

const (
	SlashCommandSourceExtension SlashCommandSource = "extension"
	SlashCommandSourcePrompt    SlashCommandSource = "prompt"
	SlashCommandSourceSkill     SlashCommandSource = "skill"
)

// SlashCommandInfo is one registered command.
type SlashCommandInfo struct {
	Name        string
	Description string
	Source      SlashCommandSource
	SourceInfo  SourceInfo
}

// BuiltinSlashCommand is one catalog entry.
type BuiltinSlashCommand struct {
	Name         string
	Description  string
	ArgumentHint string
}

// BuiltinSlashCommands is the built-in catalog.
var BuiltinSlashCommands = []BuiltinSlashCommand{
	{Name: "settings", Description: "Open settings menu"},
	{Name: "model", Description: "Select model (opens selector UI)", ArgumentHint: "<provider/model>"},
	{Name: "tree", Description: "Navigate session tree (switch branches)"},
	{Name: "thinking", Description: "Set thinking level", ArgumentHint: "<level>"},
	{Name: "scoped-models", Description: "Enable/disable models for Ctrl+P cycling"},
	{Name: "export", Description: "Export session (HTML default, or specify path: .html/.jsonl)"},
	{Name: "import", Description: "Import and resume a session from a JSONL file"},
	{Name: "share", Description: "Share session as a secret GitHub gist"},
	{Name: "bug", Description: "Report a bug to the Pi developers", ArgumentHint: "<description>"},
	{Name: "copy", Description: "Copy last agent message to clipboard"},
	{Name: "name", Description: "Set session display name"},
	{Name: "session", Description: "Show session info and stats"},
	{Name: "changelog", Description: "Show changelog entries"},
	{Name: "hotkeys", Description: "Show all keyboard shortcuts"},
	{Name: "fork", Description: "Create a new fork from a previous user message"},
	{Name: "clone", Description: "Duplicate the current session at the current position"},
	{Name: "trust", Description: "Save project trust decision for future sessions"},
	{Name: "login", Description: "Configure provider authentication", ArgumentHint: "<provider>"},
	{Name: "logout", Description: "Remove provider authentication"},
	{Name: "new", Description: "Start a new session"},
	{Name: "compact", Description: "Manually compact the session context"},
	{Name: "resume", Description: "Resume a different session"},
	{Name: "reload", Description: "Reload keybindings, extensions, skills, prompts, themes, and context files"},
	{Name: "quit", Description: fmt.Sprintf("Quit %s", AppName())},
}
