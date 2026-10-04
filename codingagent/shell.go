package codingagent

// Port of utils/shell.ts: shell discovery, environment augmentation,
// binary-output sanitization, detached-child tracking, and process-tree
// kills.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// ShellConfig describes how to invoke the shell.
type ShellConfig struct {
	Shell string
	Args  []string
	// CommandTransport selects argv ("-c command") or stdin ("-s").
	CommandTransport string // "" (argv default) | "stdin"
}

var legacyWslBashPattern = regexp.MustCompile(`^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$`)

func isLegacyWslBashPath(path string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(path, "/", "\\"))
	return legacyWslBashPattern.MatchString(normalized)
}

func getBashShellConfig(shell string) ShellConfig {
	if isLegacyWslBashPath(shell) {
		return ShellConfig{Shell: shell, Args: []string{"-s"}, CommandTransport: "stdin"}
	}
	return ShellConfig{Shell: shell, Args: []string{"-c"}}
}

func findExecutableOnPath(executable string) string {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("where", executable).Output()
		if err == nil {
			firstMatch := strings.Split(strings.TrimSpace(string(out)), "\r\n")[0]
			firstMatch = strings.Split(firstMatch, "\n")[0]
			if firstMatch != "" {
				if _, err := os.Stat(firstMatch); err == nil {
					return firstMatch
				}
			}
		}
		return ""
	}

	out, err := exec.Command("which", executable).Output()
	if err == nil {
		firstMatch := strings.Split(strings.TrimSpace(string(out)), "\n")[0]
		if firstMatch != "" {
			return firstMatch
		}
	}
	return ""
}

// GetShellConfig resolves the shell configuration: custom path, then
// platform defaults.
func GetShellConfig(customShellPath string) (ShellConfig, error) {
	if customShellPath != "" {
		if _, err := os.Stat(customShellPath); err == nil {
			return getBashShellConfig(customShellPath), nil
		}
		return ShellConfig{}, fmt.Errorf("Custom shell path not found: %s", customShellPath)
	}

	if runtime.GOOS == "windows" {
		var paths []string
		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			paths = append(paths, programFiles+"\\Git\\bin\\bash.exe")
		}
		if programFilesX86 := os.Getenv("ProgramFiles(x86)"); programFilesX86 != "" {
			paths = append(paths, programFilesX86+"\\Git\\bin\\bash.exe")
		}
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				return getBashShellConfig(path), nil
			}
		}
		if bashOnPath := findExecutableOnPath("bash.exe"); bashOnPath != "" {
			return getBashShellConfig(bashOnPath), nil
		}
		return ShellConfig{}, fmt.Errorf("No bash shell found. Options:\n"+
			"  1. Install Git for Windows: https://git-scm.com/download/win\n"+
			"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n"+
			"  3. Set shellPath in settings.json\n\n"+
			"Searched Git Bash in:\n%s",
			"  "+strings.Join(paths, "\n  "))
	}

	if _, err := os.Stat("/bin/bash"); err == nil {
		return getBashShellConfig("/bin/bash"), nil
	}
	if bashOnPath := findExecutableOnPath("bash"); bashOnPath != "" {
		return getBashShellConfig(bashOnPath), nil
	}
	return ShellConfig{Shell: "sh", Args: []string{"-c"}}, nil
}

// PowerShellArgs is the fixed PowerShell invocation.
var PowerShellArgs = []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"}

// GetPowerShellConfig resolves PowerShell on Windows, preferring pwsh.
func GetPowerShellConfig() (ShellConfig, error) {
	if runtime.GOOS != "windows" {
		return ShellConfig{}, fmt.Errorf("The powershell tool is only available on Windows.")
	}
	shell := findExecutableOnPath("pwsh.exe")
	if shell == "" {
		shell = findExecutableOnPath("powershell.exe")
	}
	if shell == "" {
		return ShellConfig{}, fmt.Errorf("No PowerShell executable found. Install PowerShell or add powershell.exe/pwsh.exe to PATH.")
	}
	return ShellConfig{Shell: shell, Args: append([]string(nil), PowerShellArgs...)}, nil
}

// GetShellEnv augments the process environment with the managed bin dir on
// PATH.
func GetShellEnv() []string {
	binDir := GetBinDir()
	pathKey := "PATH"
	for _, key := range os.Environ() {
		if strings.EqualFold(strings.SplitN(key, "=", 2)[0], "path") {
			pathKey = strings.SplitN(key, "=", 2)[0]
			break
		}
	}
	currentPath := os.Getenv(pathKey)
	entries := []string{}
	for _, entry := range filepath.SplitList(currentPath) {
		if entry != "" {
			entries = append(entries, entry)
		}
	}
	hasBinDir := false
	for _, entry := range entries {
		if entry == binDir {
			hasBinDir = true
			break
		}
	}
	updatedPath := currentPath
	if !hasBinDir {
		parts := []string{binDir}
		if currentPath != "" {
			parts = append(parts, currentPath)
		}
		updatedPath = strings.Join(parts, string(os.PathListSeparator))
	}

	env := os.Environ()
	replaced := false
	for i, entry := range env {
		key := strings.SplitN(entry, "=", 2)[0]
		if key == pathKey {
			env[i] = pathKey + "=" + updatedPath
			replaced = true
			break
		}
	}
	if !replaced {
		env = append(env, pathKey+"="+updatedPath)
	}
	return env
}

// SanitizeBinaryOutput removes characters that crash width computation or
// display: control characters (except tab/newline/CR), lone surrogates
// (impossible in Go strings), and Unicode format characters.
func SanitizeBinaryOutput(str string) string {
	var b strings.Builder
	for _, r := range str {
		if r == 0x09 || r == 0x0a || r == 0x0d {
			b.WriteRune(r)
			continue
		}
		if r <= 0x1f {
			continue
		}
		if r >= 0xfff9 && r <= 0xfffb {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// trackedDetachedChildPids tracks detached children for shutdown kills.
var (
	trackedDetachedMu   sync.Mutex
	trackedDetachedPids = map[int]bool{}
)

// TrackDetachedChildPid registers a detached child for shutdown kills.
func TrackDetachedChildPid(pid int) {
	trackedDetachedMu.Lock()
	defer trackedDetachedMu.Unlock()
	trackedDetachedPids[pid] = true
}

// UntrackDetachedChildPid removes a detached child.
func UntrackDetachedChildPid(pid int) {
	trackedDetachedMu.Lock()
	defer trackedDetachedMu.Unlock()
	delete(trackedDetachedPids, pid)
}

// KillTrackedDetachedChildren kills all tracked detached children.
func KillTrackedDetachedChildren() {
	trackedDetachedMu.Lock()
	pids := make([]int, 0, len(trackedDetachedPids))
	for pid := range trackedDetachedPids {
		pids = append(pids, pid)
	}
	trackedDetachedPids = map[int]bool{}
	trackedDetachedMu.Unlock()

	for _, pid := range pids {
		KillProcessTree(pid)
	}
}

// KillProcessTree kills a process and its children (process group on Unix,
// taskkill /T on Windows); the platform bodies live in shell_unix.go and
// shell_windows.go.

// KillProcessTree dispatches to the platform body.
func KillProcessTree(pid int) { killProcessTreePlatform(pid) }
