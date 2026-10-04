package codingagent

// Port of cli/args.ts: CLI argument parsing with flag diagnostics,
// @file args, unknown extension flags, and help text.

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// Mode is text | json | rpc.
type Mode string

// CliArgs is the parsed command line.
type CliArgs struct {
	Provider             string
	Model                string
	APIKey               string
	SystemPrompt         string
	AppendSystemPrompt   []string
	Thinking             string
	Continue             bool
	Resume               bool
	Help                 bool
	Version              bool
	Mode                 Mode
	Name                 string
	NoSession            bool
	Session              string
	SessionID            string
	Fork                 string
	SessionDir           string
	Models               []string
	Tools                []string
	ExcludeTools         []string
	NoTools              bool
	NoBuiltinTools       bool
	Extensions           []string
	NoExtensions         bool
	Print                bool
	Export               string
	NoSkills             bool
	NoPromptTemplates    bool
	Skills               []string
	PromptTemplates      []string
	Themes               []string
	UseTheme             string
	NoThemes             bool
	NoContextFiles       bool
	ListModels           any // string or true
	Offline              bool
	TuiMode              string
	Verbose              bool
	ProjectTrustOverride *bool

	Messages     []string
	FileArgs     []string
	UnknownFlags map[string]any // flag name -> bool | string
	Diagnostics  []CliDiagnostic
}

// CliDiagnostic is one parse warning/error.
type CliDiagnostic struct {
	Type    string // "warning" | "error"
	Message string
}

// IsValidThinkingLevel validates off..max.
func IsValidThinkingLevel(level string) bool {
	for _, valid := range ThinkingLevelOptions {
		if valid == level {
			return true
		}
	}
	return false
}

// NormalizeSessionName trims to a non-empty name.
func NormalizeSessionName(value string) string {
	name := strings.TrimSpace(value)
	if name == "" {
		return ""
	}
	return name
}

// ParseArgs parses the command line.
func ParseArgs(args []string) *CliArgs {
	result := &CliArgs{
		UnknownFlags: map[string]any{},
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--":
			for _, positional := range args[i+1:] {
				if strings.HasPrefix(positional, "@") {
					result.FileArgs = append(result.FileArgs, positional[1:])
				} else {
					result.Messages = append(result.Messages, positional)
				}
			}
			return result

		case arg == "--help" || arg == "-h":
			result.Help = true

		case arg == "--version" || arg == "-v":
			result.Version = true

		case arg == "--mode":
			mode := ""
			if i+1 < len(args) {
				mode = args[i+1]
			}
			if mode == "" || strings.HasPrefix(mode, "-") {
				result.Diagnostics = append(result.Diagnostics, CliDiagnostic{Type: "error", Message: "--mode requires text, json, rpc, or interactive"})
				continue
			}
			i++
			if mode != "text" && mode != "json" && mode != "rpc" && mode != "interactive" {
				result.Diagnostics = append(result.Diagnostics, CliDiagnostic{
					Type: "error", Message: fmt.Sprintf("Invalid mode %q. Valid values: text, json, rpc, interactive", mode),
				})
				continue
			}
			result.Mode = Mode(mode)

		case arg == "--continue" || arg == "-c":
			result.Continue = true

		case arg == "--resume" || arg == "-r":
			result.Resume = true

		case arg == "--provider" && i+1 < len(args):
			i++
			result.Provider = args[i]

		case arg == "--model" && i+1 < len(args):
			i++
			result.Model = args[i]

		case arg == "--api-key" && i+1 < len(args):
			i++
			result.APIKey = args[i]

		case arg == "--system-prompt" && i+1 < len(args):
			i++
			result.SystemPrompt = args[i]

		case arg == "--append-system-prompt" && i+1 < len(args):
			i++
			result.AppendSystemPrompt = append(result.AppendSystemPrompt, args[i])

		case arg == "--name" || arg == "-n":
			if i+1 < len(args) {
				i++
				result.Name = args[i]
			} else {
				result.Diagnostics = append(result.Diagnostics, CliDiagnostic{Type: "error", Message: "--name requires a value"})
			}

		case arg == "--no-session":
			result.NoSession = true

		case arg == "--session" && i+1 < len(args):
			i++
			result.Session = args[i]

		case arg == "--session-id" && i+1 < len(args):
			i++
			result.SessionID = args[i]

		case arg == "--fork" && i+1 < len(args):
			i++
			result.Fork = args[i]

		case arg == "--session-dir" && i+1 < len(args):
			i++
			result.SessionDir = args[i]

		case arg == "--models" && i+1 < len(args):
			i++
			for _, part := range strings.Split(args[i], ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					result.Models = append(result.Models, trimmed)
				}
			}

		case arg == "--no-tools" || arg == "-nt":
			result.NoTools = true

		case arg == "--no-builtin-tools" || arg == "-nbt":
			result.NoBuiltinTools = true

		case (arg == "--tools" || arg == "-t") && i+1 < len(args):
			i++
			for _, part := range strings.Split(args[i], ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					result.Tools = append(result.Tools, trimmed)
				}
			}

		case (arg == "--exclude-tools" || arg == "-xt") && i+1 < len(args):
			i++
			for _, part := range strings.Split(args[i], ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					result.ExcludeTools = append(result.ExcludeTools, trimmed)
				}
			}

		case arg == "--thinking" && i+1 < len(args):
			i++
			level := args[i]
			if IsValidThinkingLevel(level) {
				result.Thinking = level
			} else {
				result.Diagnostics = append(result.Diagnostics, CliDiagnostic{
					Type:    "warning",
					Message: fmt.Sprintf("Invalid thinking level %q. Valid values: %s", level, strings.Join(ThinkingLevelOptions, ", ")),
				})
			}

		case arg == "--print" || arg == "-p":
			result.Print = true
			if i+1 < len(args) {
				next := args[i+1]
				if !strings.HasPrefix(next, "@") && (!strings.HasPrefix(next, "-") || strings.HasPrefix(next, "---")) {
					result.Messages = append(result.Messages, next)
					i++
				}
			}

		case arg == "--export" && i+1 < len(args):
			i++
			result.Export = args[i]

		case (arg == "--extension" || arg == "-e") && i+1 < len(args):
			i++
			result.Extensions = append(result.Extensions, args[i])

		case arg == "--no-extensions" || arg == "-ne":
			result.NoExtensions = true

		case arg == "--skill" && i+1 < len(args):
			i++
			result.Skills = append(result.Skills, args[i])

		case arg == "--prompt-template" && i+1 < len(args):
			i++
			result.PromptTemplates = append(result.PromptTemplates, args[i])

		case arg == "--theme" && i+1 < len(args):
			i++
			result.Themes = append(result.Themes, args[i])

		case arg == "--use-theme":
			themeName := ""
			if i+1 < len(args) {
				themeName = args[i+1]
			}
			if themeName == "" || strings.HasPrefix(themeName, "-") {
				result.Diagnostics = append(result.Diagnostics, CliDiagnostic{Type: "error", Message: "--use-theme requires a theme name"})
			} else {
				result.UseTheme = themeName
				i++
			}

		case arg == "--no-skills" || arg == "-ns":
			result.NoSkills = true

		case arg == "--no-prompt-templates" || arg == "-np":
			result.NoPromptTemplates = true

		case arg == "--no-themes":
			result.NoThemes = true

		case arg == "--no-context-files" || arg == "-nc":
			result.NoContextFiles = true

		case arg == "--list-models":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !strings.HasPrefix(args[i+1], "@") {
				i++
				result.ListModels = args[i]
			} else {
				result.ListModels = true
			}

		case arg == "--tui-mode":
			mode := ""
			if i+1 < len(args) {
				mode = args[i+1]
			}
			if mode == "regular" || mode == "fullscreen" {
				result.TuiMode = mode
				i++
			} else if mode == "" || strings.HasPrefix(mode, "-") {
				result.Diagnostics = append(result.Diagnostics, CliDiagnostic{Type: "error", Message: "--tui-mode requires regular or fullscreen"})
			} else {
				i++
				result.Diagnostics = append(result.Diagnostics, CliDiagnostic{
					Type: "error", Message: fmt.Sprintf("Invalid TUI mode %q. Valid values: regular, fullscreen", mode),
				})
			}

		case arg == "--verbose":
			result.Verbose = true

		case arg == "--approve" || arg == "-a":
			value := true
			result.ProjectTrustOverride = &value

		case arg == "--no-approve" || arg == "-na":
			value := false
			result.ProjectTrustOverride = &value

		case arg == "--offline":
			result.Offline = true

		case strings.HasPrefix(arg, "@"):
			result.FileArgs = append(result.FileArgs, arg[1:])

		case strings.HasPrefix(arg, "--"):
			if eqIndex := strings.IndexByte(arg, '='); eqIndex != -1 {
				result.UnknownFlags[arg[2:eqIndex]] = arg[eqIndex+1:]
			} else {
				flagName := arg[2:]
				next := ""
				if i+1 < len(args) {
					next = args[i+1]
				}
				if next != "" && !strings.HasPrefix(next, "-") && !strings.HasPrefix(next, "@") {
					result.UnknownFlags[flagName] = next
					i++
				} else {
					result.UnknownFlags[flagName] = true
				}
			}

		case strings.HasPrefix(arg, "-"):
			result.Diagnostics = append(result.Diagnostics, CliDiagnostic{
				Type: "error", Message: "Unknown option: " + arg,
			})

		default:
			result.Messages = append(result.Messages, arg)
		}
	}

	return result
}

// PrintHelp renders the usage text.
func PrintHelp() string {
	return fmt.Sprintf(`%s - AI coding assistant with read, bash, edit, write tools

Usage:
  %s [options] [--] [@files...] [messages...]

Options:
  --provider <name>              Provider name (default: google)
  --model <pattern>              Model pattern or ID (supports "provider/id" and optional ":<thinking>")
  --api-key <key>                API key (defaults to env vars)
  --system-prompt <text>         System prompt (default: coding assistant prompt)
  --append-system-prompt <text>  Append text or file contents to the system prompt (can be used multiple times)
  --mode <mode>                  Output mode: text (default), json, rpc, or interactive
  --print, -p                    Non-interactive mode: process prompt and exit
  --continue, -c                 Continue previous session
  --resume, -r                   Select a session to resume
  --session <path|id>            Use specific session file or partial UUID
  --session-id <id>              Use exact project session ID, creating it if missing
  --fork <path|id>               Fork specific session file or partial UUID into a new session
  --no-session                   Don't create a session file
  --session-dir <path>           Custom session storage directory
  --models <patterns>            Comma-separated model patterns for Ctrl+P cycling
  --tools <names>                Comma-separated initial tool selection (default: read,bash,edit,write)
  --exclude-tools <names>        Comma-separated tool names to exclude
  --no-tools, -nt                Disable all tools
  --no-builtin-tools, -nbt       Disable built-in tools (extensions may register their own)
  --thinking <level>             Thinking level: off, minimal, low, medium (default), high, xhigh, max
  --extension, -e <path>         Load additional extension (file or directory)
  --no-extensions, -ne           Disable all extensions
  --skill <path>                 Load additional skill (file or directory)
  --prompt-template <path>       Load additional prompt template (file or directory)
  --theme <path>                 Load additional theme (file or directory)
  --use-theme <name>             Select a theme by name
  --no-skills, -ns               Disable all skills
  --no-prompt-templates, -np     Disable all prompt templates
  --no-themes                    Disable all themes
  --no-context-files, -nc        Disable loading of project context files
  --list-models [pattern]        List available models, optionally filtering by pattern
  --tui-mode <mode>              TUI mode: regular (default) or fullscreen
  --verbose                      Enable verbose logging for extensions
  --approve, -a                  Trust the project directory without asking
  --no-approve, -na              Never trust the project directory
  --offline                      Disable network access for model catalog updates
  --version, -v                  Show version information
  --help, -h                     Show this help message

File Arguments:
  @file                          Include file contents as part of the message
`, AppName(), AppName())
}

// Mode values.
const (
	ModeText        Mode = "text"
	ModeJSON        Mode = "json"
	ModeRPC         Mode = "rpc"
	ModeInteractive Mode = "interactive"
)

// SetupPrintSession builds a session host for the print modes (wiring the
// session, settings, and model runtime over the working directory).
type PrintSessionOptions struct {
	Cwd  string
	Mode string
}

// PrintSessionHost adapts the constructed session for RunPrintMode.
type PrintSessionHost struct {
	session *AgentSession
}

// Session implements PrintModeHost.
func (h *PrintSessionHost) Session() *AgentSession { return h.session }

// Dispose implements PrintModeHost.
func (h *PrintSessionHost) Dispose() error { return nil }

// SetupPrintSession constructs the print-mode host.
func SetupPrintSession(options PrintSessionOptions) (*PrintSessionHost, error) {
	sessionManager, err := SessionManagerCreate(options.Cwd, "", nil)
	if err != nil {
		return nil, err
	}
	settings := SettingsManagerCreate(options.Cwd, GetAgentDir(), nil)
	runtime, err := NewModelRuntime(&CreateModelRuntimeOptions{})
	if err != nil {
		return nil, err
	}
	session := NewAgentSession(AgentSessionRuntimeOptions{
		Config: AgentSessionConfig{
			Agent:           newAgentForSession(),
			SessionManager:  sessionManager,
			SettingsManager: settings,
			ModelRuntime:    runtime,
			Cwd:             options.Cwd,
		},
	})
	// The agent reaches models only through its stream function; the
	// reference wires createAgentSession's streamFn (the process-global
	// default is a test seam).
	session.Agent.StreamFunction = sessionStreamFn(runtime)
	return &PrintSessionHost{session: session}, nil
}

// sessionStreamFn adapts the model runtime to the agent's stream seam. The
// transcript carries the system prompt and tool state as messages, so only
// Messages cross the boundary.
func sessionStreamFn(runtime *ModelRuntime) agent.StreamFn {
	return func(model *ai.Model, context *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		return runtime.StreamSimple(model, ai.Context{Messages: context.Messages}, options)
	}
}

// RpcSetupOptions configure the RPC dispatcher construction.
type RpcSetupOptions struct{}

// SetupRpcDispatcher builds the dispatcher over a fresh session.
func SetupRpcDispatcher(options RpcSetupOptions) (*RpcDispatcher, error) {
	cwd, err := osGetwd()
	if err != nil {
		return nil, err
	}
	host, err := SetupPrintSession(PrintSessionOptions{Cwd: cwd})
	if err != nil {
		return nil, err
	}
	return NewRpcDispatcher(host, nil), nil
}

// Serve reads JSONL commands until EOF, dispatching each.
func (d *RpcDispatcher) Serve(input io.Reader) int {
	AttachJsonlLineReader(input, func(line string) {
		if line == "" {
			return
		}
		response := d.Dispatch([]byte(line))
		if response != nil {
			d.Output(response)
		}
	})
	return 0
}

func newAgentForSession() *agent.Agent { return agent.NewAgent(agent.AgentOptions{}) }

func osGetwd() (string, error) { return os.Getwd() }
