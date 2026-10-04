package codingagent

// Port of core/resolve-config-value.ts: config values that may be shell
// commands ("!..."), environment templates ("$VAR", "${VAR}"), or literals.

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	commandResultMu    sync.Mutex
	commandResultCache = map[string]*string{}
)

var envVarNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var envVarNamePrefixPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)

type templatePart struct {
	isEnv bool
	value string // literal text
	name  string // env var name
}

type configValueReference struct {
	isCommand bool
	config    string // raw command config (with !)
	parts     []templatePart
}

func appendLiteral(parts []templatePart, value string) []templatePart {
	if value == "" {
		return parts
	}
	if len(parts) > 0 && !parts[len(parts)-1].isEnv {
		parts[len(parts)-1].value += value
		return parts
	}
	return append(parts, templatePart{value: value})
}

func parseConfigValueTemplate(config string) []templatePart {
	var parts []templatePart
	index := 0

	for index < len(config) {
		dollarIndex := strings.IndexByte(config[index:], '$')
		if dollarIndex < 0 {
			parts = appendLiteral(parts, config[index:])
			break
		}
		dollarIndex += index

		parts = appendLiteral(parts, config[index:dollarIndex])
		nextChar := byte(0)
		if dollarIndex+1 < len(config) {
			nextChar = config[dollarIndex+1]
		}

		if nextChar == '$' || nextChar == '!' {
			parts = appendLiteral(parts, string(nextChar))
			index = dollarIndex + 2
			continue
		}

		if nextChar == '{' {
			endIndex := strings.IndexByte(config[dollarIndex+2:], '}')
			if endIndex < 0 {
				parts = appendLiteral(parts, "$")
				index = dollarIndex + 1
				continue
			}
			endIndex += dollarIndex + 2

			name := config[dollarIndex+2 : endIndex]
			if envVarNamePattern.MatchString(name) {
				parts = append(parts, templatePart{isEnv: true, name: name})
			} else {
				parts = appendLiteral(parts, config[dollarIndex:endIndex+1])
			}
			index = endIndex + 1
			continue
		}

		if match := envVarNamePrefixPattern.FindString(config[dollarIndex+1:]); match != "" {
			parts = append(parts, templatePart{isEnv: true, name: match})
			index = dollarIndex + 1 + len(match)
			continue
		}

		parts = appendLiteral(parts, "$")
		index = dollarIndex + 1
	}

	return parts
}

func parseConfigValueReference(config string) configValueReference {
	if strings.HasPrefix(config, "!") {
		return configValueReference{isCommand: true, config: config}
	}
	return configValueReference{parts: parseConfigValueTemplate(config)}
}

func resolveEnvConfigValue(name string, env map[string]string) (string, bool) {
	if value, ok := env[name]; ok && value != "" {
		return value, true
	}
	return os.LookupEnv(name)
}

func getTemplateEnvVarNames(parts []templatePart) []string {
	var names []string
	seen := map[string]bool{}
	for _, part := range parts {
		if !part.isEnv || seen[part.name] {
			continue
		}
		seen[part.name] = true
		names = append(names, part.name)
	}
	return names
}

func resolveTemplate(parts []templatePart, env map[string]string) (string, bool) {
	resolved := ""
	for _, part := range parts {
		if !part.isEnv {
			resolved += part.value
			continue
		}
		value, ok := resolveEnvConfigValue(part.name, env)
		if !ok {
			return "", false
		}
		resolved += value
	}
	return resolved, true
}

// GetConfigValueEnvVarName returns the sole env var when the value is one.
func GetConfigValueEnvVarName(config string) string {
	reference := parseConfigValueReference(config)
	if reference.isCommand || len(reference.parts) != 1 || !reference.parts[0].isEnv {
		return ""
	}
	return reference.parts[0].name
}

// GetConfigValueEnvVarNames lists every env var the value references.
func GetConfigValueEnvVarNames(config string) []string {
	reference := parseConfigValueReference(config)
	if reference.isCommand {
		return nil
	}
	return getTemplateEnvVarNames(reference.parts)
}

// GetMissingConfigValueEnvVarNames lists unset referenced env vars.
func GetMissingConfigValueEnvVarNames(config string, env map[string]string) []string {
	var missing []string
	for _, name := range GetConfigValueEnvVarNames(config) {
		if _, ok := resolveEnvConfigValue(name, env); !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// IsCommandConfigValue reports whether the value starts with "!".
func IsCommandConfigValue(config string) bool {
	return parseConfigValueReference(config).isCommand
}

// IsConfigValueConfigured reports whether all referenced env vars are set.
func IsConfigValueConfigured(config string, env map[string]string) bool {
	return len(GetMissingConfigValueEnvVarNames(config, env)) == 0
}

// ResolveConfigValue resolves a config value: "!" executes a shell command
// (cached), $VAR/${VAR} interpolate, $$/$! escape, else literal.
func ResolveConfigValue(config string, env map[string]string) (string, bool) {
	reference := parseConfigValueReference(config)
	if reference.isCommand {
		return executeCommand(reference.config)
	}
	return resolveTemplate(reference.parts, env)
}

func executeWithConfiguredShell(command string) (executed bool, value string, ok bool) {
	shellConfig, err := GetShellConfig("")
	if err != nil {
		return false, "", false
	}
	commandFromStdin := shellConfig.CommandTransport == "stdin"
	args := shellConfig.Args
	if !commandFromStdin {
		args = append(append([]string(nil), shellConfig.Args...), command)
	}

	cmd := exec.Command(shellConfig.Shell, args...)
	cmd.Stdin = nil
	if commandFromStdin {
		cmd.Stdin = strings.NewReader(command)
	}
	var stdout strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return false, "", false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	}
	return true, strings.TrimSpace(stdout.String()), true
}

func executeWithDefaultShell(command string) (string, bool) {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Stdin = nil
	var stdout strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", false
	}
	value := strings.TrimSpace(stdout.String())
	return value, value != ""
}

func executeCommandUncached(commandConfig string) (string, bool) {
	command := commandConfig[1:]
	return executeWithDefaultShell(command)
}

func executeCommand(commandConfig string) (string, bool) {
	commandResultMu.Lock()
	defer commandResultMu.Unlock()
	if cached, ok := commandResultCache[commandConfig]; ok {
		if cached == nil {
			return "", false
		}
		return *cached, true
	}

	value, ok := executeCommandUncached(commandConfig)
	if !ok {
		commandResultCache[commandConfig] = nil
		return "", false
	}
	commandResultCache[commandConfig] = &value
	return value, true
}

// ResolveConfigValueUncached resolves bypassing the command cache.
func ResolveConfigValueUncached(config string, env map[string]string) (string, bool) {
	reference := parseConfigValueReference(config)
	if reference.isCommand {
		return executeCommandUncached(reference.config)
	}
	return resolveTemplate(reference.parts, env)
}

// ResolveConfigValueOrThrow resolves or reports the actionable failure.
func ResolveConfigValueOrThrow(config, description string, env map[string]string) (string, error) {
	if value, ok := ResolveConfigValueUncached(config, env); ok {
		return value, nil
	}

	reference := parseConfigValueReference(config)
	if reference.isCommand {
		return "", errConfigValue("Failed to resolve " + description + " from shell command: " + reference.config[1:])
	}

	missing := GetMissingConfigValueEnvVarNames(config, env)
	switch len(missing) {
	case 1:
		return "", errConfigValue("Failed to resolve " + description + " from environment variable: " + missing[0])
	case 0:
	default:
		return "", errConfigValue("Failed to resolve " + description + " from environment variables: " + strings.Join(missing, ", "))
	}
	return "", errConfigValue("Failed to resolve " + description)
}

type errConfigValue string

func (e errConfigValue) Error() string { return string(e) }

// ResolveHeaders resolves every header value, dropping unresolved ones.
func ResolveHeaders(headers map[string]string, env map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	resolved := map[string]string{}
	for key, value := range headers {
		if resolvedValue, ok := ResolveConfigValue(value, env); ok && resolvedValue != "" {
			resolved[key] = resolvedValue
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

// ResolveHeadersOrThrow resolves every header value or fails.
func ResolveHeadersOrThrow(headers map[string]string, description string, env map[string]string) (map[string]string, error) {
	if headers == nil {
		return nil, nil
	}
	resolved := map[string]string{}
	for key, value := range headers {
		resolvedValue, err := ResolveConfigValueOrThrow(value, description+` header "`+key+`"`, env)
		if err != nil {
			return nil, err
		}
		resolved[key] = resolvedValue
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	return resolved, nil
}

// ClearConfigValueCache empties the command cache (exported for tests).
func ClearConfigValueCache() {
	commandResultMu.Lock()
	defer commandResultMu.Unlock()
	commandResultCache = map[string]*string{}
}
