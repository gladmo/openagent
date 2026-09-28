package ai

// provider_env.go ports utils/provider-env.ts: provider env lookups resolve
// from scoped overrides (options.env), then the process environment. The Bun
// sandbox fallback has no Go equivalent (Go never exposes an empty os.Environ
// there) and is intentionally omitted.

import "os"

// GetProviderEnvValue resolves a provider env value from scoped overrides
// first, then the normal process environment.
func GetProviderEnvValue(name string, env ProviderEnv) string {
	if env != nil {
		if value, ok := env[name]; ok && value != "" {
			return value
		}
	}
	return os.Getenv(name)
}
