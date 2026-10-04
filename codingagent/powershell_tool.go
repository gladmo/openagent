package codingagent

// Port of core/tools/powershell.ts: the PowerShell shell tool on the shared
// bash.ts factory (Windows-only resolution, UTF-8 output prefix).

// PowerShellToolSystemPromptContribution is the tool's prompt section.
var PowerShellToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet:    "Execute PowerShell commands",
	Guidelines: []string{"You can inspect PI_* environment variables for current model and session details."},
}

// utf8OutputPrefix forces PowerShell to emit UTF-8.
const utf8OutputPrefix = "try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}\n"

// CreateLocalPowerShellOperations builds the PowerShell local backend.
func CreateLocalPowerShellOperations() BashOperations {
	operations := CreateLocalShellOperations("PowerShell", GetPowerShellConfig)
	return &powerShellOperations{operations: operations}
}

type powerShellOperations struct {
	operations BashOperations
}

func (o *powerShellOperations) Exec(command, cwd string, options BashExecOptions) (BashExecResult, error) {
	return o.operations.Exec(utf8OutputPrefix+command, cwd, options)
}

var powershellToolConfig = ShellToolConfig{
	Name:             "powershell",
	Label:            "powershell",
	ShellName:        "PowerShell",
	Prompt:           "PS>",
	PromptSnippet:    PowerShellToolSystemPromptContribution.Snippet,
	PromptGuidelines: append([]string(nil), PowerShellToolSystemPromptContribution.Guidelines...),
	TempFilePrefix:   "pi-powershell",
}

// PowerShellToolOptions configure the PowerShell tool.
type PowerShellToolOptions struct {
	Operations               BashOperations
	ExposeSessionEnvironment *bool
	SpawnHook                BashSpawnHook
}

// CreatePowerShellToolDefinition builds the PowerShell tool.
func CreatePowerShellToolDefinition(cwd string, options *PowerShellToolOptions) *ToolDefinition {
	var bashOptions *BashToolOptions
	if options != nil {
		bashOptions = &BashToolOptions{
			Operations:               options.Operations,
			ExposeSessionEnvironment: options.ExposeSessionEnvironment,
			SpawnHook:                options.SpawnHook,
		}
	}
	if bashOptions == nil || bashOptions.Operations == nil {
		if bashOptions == nil {
			bashOptions = &BashToolOptions{}
		}
		bashOptions.Operations = CreateLocalPowerShellOperations()
	}
	return CreateShellToolDefinition(cwd, powershellToolConfig, bashOptions)
}

// CreatePowerShellTool wraps the definition into an AgentTool.
func CreatePowerShellTool(cwd string, options *PowerShellToolOptions) any {
	definition := CreatePowerShellToolDefinition(cwd, options)
	return WrapToolDefinition(definition, nil)
}
