package codingagent

// Port of core/auth-guidance.ts: provider login help messages.

import (
	"path/filepath"
)

// unknownProvider is the placeholder provider id.
const unknownProvider = "unknown"

// GetProviderLoginHelp renders the login pointer.
func GetProviderLoginHelp() string {
	docs := GetDocsPath()
	return "Use /login to log into a provider via OAuth or API key. See:\n" +
		"  " + filepath.Join(docs, "providers.md") + "\n" +
		"  " + filepath.Join(docs, "models.md")
}

// FormatNoModelsAvailableMessage renders the no-models error.
func FormatNoModelsAvailableMessage() string {
	return "No models available. " + GetProviderLoginHelp()
}

// FormatNoModelSelectedMessage renders the no-selection error.
func FormatNoModelSelectedMessage() string {
	return "No model selected.\n\n" + GetProviderLoginHelp() + "\n\nThen use /model to select a model."
}

// FormatNoApiKeyFoundMessage renders the missing-key error.
func FormatNoApiKeyFoundMessage(provider string) string {
	providerDisplay := provider
	if provider == unknownProvider {
		providerDisplay = "the selected model"
	}
	return "No API key found for " + providerDisplay + ".\n\n" + GetProviderLoginHelp()
}
