// Command openagent is the Go port of the pi coding-agent CLI: an AI
// coding assistant with read, bash, edit, and write tools operating in
// text (print), JSON-event, RPC, and interactive modes.
package main

import (
	"fmt"
	"os"

	"github.com/gladmo/openagent/codingagent"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	// `openagent mcp ...` manages MCP servers without starting a session.
	if len(args) > 0 && args[0] == "mcp" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
			return 1
		}
		return codingagent.RunMcpCommand(args[1:], codingagent.McpCommandOptions{
			Cwd:      cwd,
			AgentDir: codingagent.GetAgentDir(),
		})
	}

	parsed := codingagent.ParseArgs(args)

	if parsed.Help {
		fmt.Print(codingagent.PrintHelp())
		return 0
	}
	if parsed.Version {
		fmt.Printf("%s %s\n", codingagent.AppName(), codingagent.Version())
		return 0
	}

	for _, diagnostic := range parsed.Diagnostics {
		if diagnostic.Type == "error" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", diagnostic.Message)
			return 1
		}
		fmt.Fprintf(os.Stderr, "Warning: %s\n", diagnostic.Message)
	}

	// `--list-models [pattern]` prints the model table and exits without
	// starting a session.
	if parsed.ListModels != nil {
		return runListModels(parsed)
	}

	switch resolveAppMode(parsed, codingagent.IsStdinATTY(), codingagent.IsStdoutATTY()) {
	case codingagent.ModeRPC:
		return runRPC()
	case codingagent.ModeInteractive:
		return runInteractive(parsed)
	default:
		return runPrint(parsed)
	}
}

// resolveAppMode ports main.ts resolveAppMode: explicit --mode rpc/json
// win; -p/--print or a non-TTY stdin/stdout forces print; otherwise the
// default is interactive.
func resolveAppMode(parsed *codingagent.CliArgs, stdinTTY, stdoutTTY bool) codingagent.Mode {
	switch parsed.Mode {
	case codingagent.ModeRPC:
		return codingagent.ModeRPC
	case codingagent.ModeJSON:
		return codingagent.ModeJSON
	}
	if parsed.Print || !stdinTTY || !stdoutTTY {
		return codingagent.ModeText
	}
	return codingagent.ModeInteractive
}

// runInteractive drives the interactive TUI. resolveAppMode routes here
// only when stdin and stdout are TTYs; the stdin guard keeps a non-TTY
// invocation loud instead of hanging the terminal.
func runInteractive(parsed *codingagent.CliArgs) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		return 1
	}
	if err := os.Setenv(codingagent.EnvAgentDirName(), codingagent.GetAgentDir()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		return 1
	}
	host, err := codingagent.SetupPrintSession(codingagent.PrintSessionOptions{
		Cwd:          dir,
		Mode:         string(codingagent.ModeText),
		Tools:        parsed.Tools,
		ExcludeTools: parsed.ExcludeTools,
		NoTools:      noToolsModeFromCli(parsed),
		NoMcp:        parsed.NoMcp,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		return 1
	}
	if message, fatal := applyCliModelSelection(host.Session(), parsed); reportSessionDiagnostic(message, fatal) {
		return 1
	}
	if !codingagent.IsStdinATTY() {
		fmt.Fprintln(os.Stderr, "Error: interactive mode requires a TTY on stdin")
		return 1
	}
	mode := codingagent.NewInteractiveMode(codingagent.InteractiveModeOptions{
		Session: host.Session(),
		TuiMode: parsed.TuiMode,
		Version: codingagent.Version(),
		Verbose: parsed.Verbose,
	})
	if err := mode.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		return 1
	}
	return 0
}

// runPrint drives the single-shot print mode over a fresh session.
func runPrint(parsed *codingagent.CliArgs) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if err := os.Setenv(codingagent.EnvAgentDirName(), codingagent.GetAgentDir()); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	session, err := codingagent.SetupPrintSession(codingagent.PrintSessionOptions{
		Cwd:          dir,
		Mode:         string(printOutputMode(parsed)),
		Tools:        parsed.Tools,
		ExcludeTools: parsed.ExcludeTools,
		NoTools:      noToolsModeFromCli(parsed),
		NoMcp:        parsed.NoMcp,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if message, fatal := applyCliModelSelection(session.Session(), parsed); reportSessionDiagnostic(message, fatal) {
		return 1
	}

	initial, rest := splitInitialMessage(parsed.Messages)
	return codingagent.RunPrintMode(session, codingagent.PrintModeOptions{
		Mode:           string(printOutputMode(parsed)),
		InitialMessage: initial,
		Messages:       rest,
	}, nil)
}

// noToolsModeFromCli ports main.ts's --no-tools/--no-builtin-tools mapping
// onto the sdk noTools mode: "all" disables every tool, "builtin" only the
// default built-in selection.
func noToolsModeFromCli(parsed *codingagent.CliArgs) string {
	switch {
	case parsed.NoTools:
		return "all"
	case parsed.NoBuiltinTools:
		return "builtin"
	default:
		return ""
	}
}

// applyCliModelSelection ports the buildSessionOptions model leg: resolve
// --provider/--model/--thinking onto the session, installing --api-key as
// a non-persistent runtime override for the resolved provider. The
// returned message is a warning unless fatal is set (resolution error or
// unauthenticated provider); an empty --model leaves the session
// untouched.
func applyCliModelSelection(session *codingagent.AgentSession, parsed *codingagent.CliArgs) (message string, fatal bool) {
	if parsed.Model == "" {
		// main.ts: --api-key without a model cannot pick a provider.
		if parsed.APIKey != "" {
			return "--api-key requires a model to be specified via --model, --provider/--model, or --models", true
		}
		if parsed.Thinking != "" {
			session.SetThinkingLevel(parsed.Thinking)
		}
		return "", false
	}
	resolved := codingagent.ResolveCliModel(codingagent.ResolveCliModelOptions{
		CliProvider:  parsed.Provider,
		CliModel:     parsed.Model,
		CliThinking:  parsed.Thinking,
		ModelRuntime: session.ModelRuntime,
	})
	if resolved.Error != "" {
		return resolved.Error, true
	}
	// The runtime override is installed before the model is applied so the
	// session's auth check sees it.
	if parsed.APIKey != "" {
		session.ModelRuntime.SetRuntimeApiKey(resolved.Model.Provider, parsed.APIKey)
	}
	if err := session.ModelRuntime.Refresh(); err != nil {
		return err.Error(), true
	}
	if err := session.SetModel(resolved.Model, false); err != nil {
		return err.Error(), true
	}
	if parsed.Thinking != "" {
		session.SetThinkingLevel(parsed.Thinking)
	} else if resolved.ThinkingLevel != "" {
		session.SetThinkingLevel(resolved.ThinkingLevel)
	}
	return resolved.Warning, false
}

// reportSessionDiagnostic prints one setup diagnostic; fatal diagnostics
// carry the process exit code.
func reportSessionDiagnostic(message string, fatal bool) bool {
	if message == "" {
		return false
	}
	if fatal {
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
		return true
	}
	fmt.Fprintf(os.Stderr, "Warning: %s\n", message)
	return false
}

// printOutputMode ports main.ts toPrintOutputMode: json streams events,
// every other print path is text.
func printOutputMode(parsed *codingagent.CliArgs) codingagent.Mode {
	if parsed.Mode == codingagent.ModeJSON {
		return codingagent.ModeJSON
	}
	return codingagent.ModeText
}

// splitInitialMessage takes the first positional message as the initial
// prompt and the rest as follow-ups; an empty command line yields none.
func splitInitialMessage(messages []string) (string, []string) {
	if len(messages) == 0 {
		return "", nil
	}
	return messages[0], messages[1:]
}

// runListModels prints the available-models table (cli/list-models.ts)
// and exits successfully.
func runListModels(parsed *codingagent.CliArgs) int {
	if err := os.Setenv(codingagent.EnvAgentDirName(), codingagent.GetAgentDir()); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	runtime, err := codingagent.NewModelRuntime(&codingagent.CreateModelRuntimeOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	// The runtime computes provider availability lazily; the listing needs
	// the refreshed snapshot (the reference's create()-time refresh).
	if err := runtime.Refresh(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	pattern, _ := parsed.ListModels.(string)
	codingagent.ListModels(os.Stdout, os.Stderr, runtime, pattern)
	return 0
}

// runRPC serves the JSON-line RPC protocol on stdin/stdout.
func runRPC() int {
	dispatcher, err := codingagent.SetupRpcDispatcher(codingagent.RpcSetupOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return dispatcher.Serve(os.Stdin)
}
