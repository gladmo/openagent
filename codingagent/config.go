// Package codingagent is a Go 1:1 port of pi/packages/coding-agent v0.87.1
// (commit ff72faba2): the pi CLI with sessions, its own tool implementations,
// compaction, an extension system, and print/JSON-event/RPC/interactive
// modes. Files mirror the TypeScript sources by basename; identifiers follow
// the repo-wide TS->Go mapping conventions. Ported behavior is pinned to the
// reference; deviations are defects and live in PARITY.md.
package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Package Detection: the Go binary replaces the Bun/Node runtimes; the
// bundled-runtime detection collapses to constants.

// IsBunBinary reports the Bun compiled-binary runtime (always false in Go).
const IsBunBinary = false

// IsBunRuntime reports Bun as the runtime (always false in Go).
const IsBunRuntime = false

// IsBundledNode reports the esbuild-bundled Node distribution (always false
// in Go).
const IsBundledNode = false

// =============================================================================
// Install Method Detection
// =============================================================================

// InstallMethod names how the CLI was installed.
type InstallMethod string

const (
	InstallMethodBunBinary InstallMethod = "bun-binary"
	InstallMethodNpm       InstallMethod = "npm"
	InstallMethodPnpm      InstallMethod = "pnpm"
	InstallMethodYarn      InstallMethod = "yarn"
	InstallMethodBun       InstallMethod = "bun"
	InstallMethodUnknown   InstallMethod = "unknown"
)

// SelfUpdateCommandStep is one command in a self-update chain.
type SelfUpdateCommandStep struct {
	Command string
	Args    []string
	Display string
}

// SelfUpdateCommand chains uninstall/install steps for self-update.
type SelfUpdateCommand struct {
	SelfUpdateCommandStep
	Steps []SelfUpdateCommandStep
}

// SelfUpdatePackageTarget is a package name or a name+spec pair.
type SelfUpdatePackageTarget struct {
	PackageName string
	InstallSpec string
}

// NormalizeSelfUpdatePackageTarget flattens the target union.
func NormalizeSelfUpdatePackageTarget(packageName, installSpec string) SelfUpdatePackageTarget {
	if installSpec == "" {
		installSpec = packageName
	}
	return SelfUpdatePackageTarget{PackageName: packageName, InstallSpec: installSpec}
}

func makeSelfUpdateCommand(installStep SelfUpdateCommandStep, uninstallStep *SelfUpdateCommandStep) SelfUpdateCommand {
	if uninstallStep == nil {
		return SelfUpdateCommand{SelfUpdateCommandStep: installStep}
	}
	return SelfUpdateCommand{
		SelfUpdateCommandStep: SelfUpdateCommandStep{
			Command: installStep.Command,
			Args:    installStep.Args,
			Display: uninstallStep.Display + " && " + installStep.Display,
		},
		Steps: []SelfUpdateCommandStep{*uninstallStep, installStep},
	}
}

var whitespacePattern = regexp.MustCompile(`\s`)

func makeSelfUpdateCommandStep(command string, args []string) SelfUpdateCommandStep {
	parts := []string{command}
	for _, arg := range args {
		if whitespacePattern.MatchString(arg) {
			parts = append(parts, "\""+arg+"\"")
		} else {
			parts = append(parts, arg)
		}
	}
	return SelfUpdateCommandStep{Command: command, Args: args, Display: strings.Join(parts, " ")}
}

// DetectInstallMethod reports how this executable was installed, following
// the reference's executable-path heuristics.
func DetectInstallMethod() InstallMethod {
	if IsBunBinary {
		return InstallMethodBunBinary
	}
	// npm: <prefix>/lib/node_modules/<name>/... or <prefix>/node_modules on
	// Windows.
	execPath := executablePath()
	if strings.Contains(filepath.ToSlash(execPath), "/lib/node_modules/") ||
		strings.Contains(filepath.ToSlash(execPath), "\\node_modules\\") {
		return detectNodePackageManager()
	}
	if strings.Contains(execPath, ".bun/bin/") {
		return InstallMethodBun
	}
	return InstallMethodUnknown
}

// detectNodePackageManager inspects the executable directory name.
func detectNodePackageManager() InstallMethod {
	resolved := strings.ToLower(strings.ReplaceAll(executablePath()+"\x00"+executableDir(), "\\", "/"))
	if strings.Contains(resolved, "/pnpm/") || strings.Contains(resolved, "/.pnpm/") {
		return InstallMethodPnpm
	}
	if strings.Contains(resolved, "/yarn/") || strings.Contains(resolved, "/.yarn/") {
		return InstallMethodYarn
	}
	if IsBunRuntime || strings.Contains(resolved, "/install/global/node_modules/") {
		return InstallMethodBun
	}
	if strings.Contains(resolved, "/npm/") || strings.Contains(resolved, "/node_modules/") {
		return InstallMethodNpm
	}
	return InstallMethodUnknown
}

// readCommandOutput runs a command synchronously and returns trimmed stdout.
func readCommandOutput(command string, args []string, requireSuccess bool) (string, error) {
	cmd := exec.Command(command, args...)
	cmd.Stdin = nil
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		trimmed := strings.TrimSpace(stdout.String())
		return trimmed, nil
	}
	if !requireSuccess {
		return "", nil
	}
	reason := strings.TrimSpace(stderr.String())
	if reason == "" {
		reason = err.Error()
	}
	return "", fmt.Errorf("Failed to run %s %s: %s", command, strings.Join(args, " "), reason)
}

// GetSelfUpdateCommand builds the self-update command for the install
// method.
func GetSelfUpdateCommand(target SelfUpdatePackageTarget) (SelfUpdateCommand, bool) {
	method := DetectInstallMethod()
	install := makeSelfUpdateCommandStep
	switch method {
	case InstallMethodBunBinary:
		// Bun binaries self-update through the baked-in updater in the
		// reference; the Go port has no equivalent (documented deviation).
		return SelfUpdateCommand{}, false
	case InstallMethodNpm:
		return makeSelfUpdateCommand(
			install("npm", []string{"install", "-g", target.InstallSpec + "@latest"}),
			nil,
		), true
	case InstallMethodPnpm:
		return makeSelfUpdateCommand(
			install("pnpm", []string{"add", "-g", target.InstallSpec + "@latest"}),
			nil,
		), true
	case InstallMethodYarn:
		uninstall := install("yarn", []string{"global", "remove", target.PackageName})
		return makeSelfUpdateCommand(
			install("yarn", []string{"global", "add", target.InstallSpec + "@latest"}),
			&uninstall,
		), true
	case InstallMethodBun:
		return makeSelfUpdateCommand(
			install("bun", []string{"add", "-g", target.InstallSpec + "@latest"}),
			nil,
		), true
	}
	return SelfUpdateCommand{}, false
}

func executablePath() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return path
}

func executableDir() string {
	return filepath.Dir(executablePath())
}

// =============================================================================
// Package Asset Paths (shipped with executable)
// =============================================================================

// FindNodePackageDir walks up from startDir to the directory containing
// package.json (dist/ defers to the package root).
func FindNodePackageDir(startDir string) string {
	dir := startDir
	for dir != filepath.Dir(dir) {
		if fileExists(filepath.Join(dir, "package.json")) {
			parent := filepath.Dir(dir)
			// build:binary places Bun's metadata inside dist/; the package
			// root keeps asset paths from becoming dist/dist/.
			if filepath.Base(dir) == "dist" && fileExists(filepath.Join(parent, "package.json")) {
				return parent
			}
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return startDir
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// GetPackageDir returns the package root (PI_PACKAGE_DIR override wins; the
// Go binary uses the executable's directory, replacing the Bun-binary
// branch).
func GetPackageDir() string {
	if envDir := os.Getenv("PI_PACKAGE_DIR"); envDir != "" {
		return NormalizePathDefault(envDir)
	}
	return filepath.Dir(executablePath())
}

// GetThemesDir returns the built-in themes directory.
func GetThemesDir() string {
	return srcOrDistSubdir(GetPackageDir(), filepath.Join("modes", "interactive", "theme"))
}

// GetExportTemplateDir returns the HTML export template directory.
func GetExportTemplateDir() string {
	return srcOrDistSubdir(GetPackageDir(), filepath.Join("core", "export-html"))
}

func srcOrDistSubdir(packageDir string, rel string) string {
	srcOrDist := "dist"
	if fileExists(filepath.Join(packageDir, "src")) {
		srcOrDist = "src"
	}
	return filepath.Join(packageDir, srcOrDist, rel)
}

// GetPackageJsonPath returns the package.json path.
func GetPackageJsonPath() string { return filepath.Join(GetPackageDir(), "package.json") }

// GetReadmePath returns the README.md path.
func GetReadmePath() string { return absJoin(GetPackageDir(), "README.md") }

// GetDocsPath returns the docs directory path.
func GetDocsPath() string { return absJoin(GetPackageDir(), "docs") }

// GetExamplesPath returns the examples directory path.
func GetExamplesPath() string { return absJoin(GetPackageDir(), "examples") }

// GetChangelogPath returns the CHANGELOG.md path.
func GetChangelogPath() string { return absJoin(GetPackageDir(), "CHANGELOG.md") }

// GetInteractiveAssetsDir returns the built-in interactive assets directory.
func GetInteractiveAssetsDir() string {
	return srcOrDistSubdir(GetPackageDir(), filepath.Join("modes", "interactive", "assets"))
}

// GetBundledInteractiveAssetPath returns a bundled interactive asset path.
func GetBundledInteractiveAssetPath(name string) string {
	return filepath.Join(GetInteractiveAssetsDir(), name)
}

func absJoin(parts ...string) string {
	absolute, err := filepath.Abs(filepath.Join(parts...))
	if err != nil {
		return filepath.Join(parts...)
	}
	return absolute
}

// =============================================================================
// App Config (from package.json piConfig)
// =============================================================================

type packageJsonPiConfig struct {
	Name      string `json:"name"`
	ConfigDir string `json:"configDir"`
}

type packageJsonFile struct {
	Name     string               `json:"name"`
	Version  string               `json:"version"`
	PiConfig *packageJsonPiConfig `json:"piConfig"`
}

var (
	appConfigOnce sync.Once
	pkg           packageJsonFile
	packageName_  string
	appName       string
	appTitle      string
	configDirName string
	version       string
)

func loadAppConfig() {
	appConfigOnce.Do(func() {
		data, err := os.ReadFile(GetPackageJsonPath())
		if err != nil {
			if !os.IsNotExist(err) {
				panic(err)
			}
			return
		}
		if err := json.Unmarshal([]byte(StripBom(string(data))), &pkg); err != nil {
			panic(err)
		}
	})
	packageName_ = pkg.Name
	if packageName_ == "" {
		packageName_ = "@earendil-works/pi-coding-agent"
	}
	appName = "pi"
	if pkg.PiConfig != nil && pkg.PiConfig.Name != "" {
		appName = pkg.PiConfig.Name
	}
	appTitle = "π"
	if pkg.PiConfig != nil && pkg.PiConfig.Name != "" {
		appTitle = appName
	}
	configDirName = ".pi"
	if pkg.PiConfig != nil && pkg.PiConfig.ConfigDir != "" {
		configDirName = pkg.PiConfig.ConfigDir
	}
	version = pkg.Version
	if version == "" {
		version = "0.0.0"
	}
}

// PackageName returns the npm package name.
func PackageName() string { loadAppConfig(); return packageName_ }

// AppName returns the app's short name.
func AppName() string { loadAppConfig(); return appName }

// AppTitle returns the display title.
func AppTitle() string { loadAppConfig(); return appTitle }

// ConfigDirName returns the config directory name.
func ConfigDirName() string { loadAppConfig(); return configDirName }

// Version returns the package version.
func Version() string { loadAppConfig(); return version }

// EnvAgentDirName is the per-app agent-dir env var (e.g.,
// PI_CODING_AGENT_DIR).
func EnvAgentDirName() string {
	return strings.ToUpper(AppName()) + "_CODING_AGENT_DIR"
}

// EnvSessionDirName is the per-app session-dir env var.
func EnvSessionDirName() string {
	return strings.ToUpper(AppName()) + "_CODING_AGENT_SESSION_DIR"
}

// ExpandTildePath expands a leading ~ to the home directory.
func ExpandTildePath(path string) string {
	return NormalizePathDefault(path)
}

const defaultShareViewerUrl = "https://pi.dev/session/"

// GetShareViewerUrl builds the share viewer URL for a gist ID.
func GetShareViewerUrl(gistID string) string {
	baseURL := os.Getenv("PI_SHARE_VIEWER_URL")
	if baseURL == "" {
		baseURL = defaultShareViewerUrl
	}
	return baseURL + "#" + gistID
}

// =============================================================================
// User Config Paths (~/.pi/agent/*)
// =============================================================================

// GetAgentDir returns the agent config directory (e.g., ~/.pi/agent/).
func GetAgentDir() string {
	if envDir := os.Getenv(EnvAgentDirName()); envDir != "" {
		return ExpandTildePath(envDir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return filepath.Join(home, ConfigDirName(), "agent")
}

// GetCustomThemesDir returns the user's custom themes directory.
func GetCustomThemesDir() string { return filepath.Join(GetAgentDir(), "themes") }

// GetModelsPath returns the models.json path.
func GetModelsPath() string { return filepath.Join(GetAgentDir(), "models.json") }

// GetAuthPath returns the auth.json path.
func GetAuthPath() string { return filepath.Join(GetAgentDir(), "auth.json") }

// GetSettingsPath returns the settings.json path.
func GetSettingsPath() string { return filepath.Join(GetAgentDir(), "settings.json") }

// GetToolsDir returns the tools directory.
func GetToolsDir() string { return filepath.Join(GetAgentDir(), "tools") }

// GetBinDir returns the managed binaries directory (fd, rg).
func GetBinDir() string { return filepath.Join(GetAgentDir(), "bin") }

// GetPromptsDir returns the prompt templates directory.
func GetPromptsDir() string { return filepath.Join(GetAgentDir(), "prompts") }

// GetSessionsDir returns the sessions directory.
func GetSessionsDir() string { return filepath.Join(GetAgentDir(), "sessions") }

// GetDebugLogPath returns the debug log file path.
func GetDebugLogPath() string {
	return filepath.Join(GetAgentDir(), AppName()+"-debug.log")
}
