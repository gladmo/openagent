package codingagent

// Port of modes/interactive/interactive-mode.ts (helper surface): crash
// extension hints, resume-command formatting, shell-safe quoting, and
// login-provider completion plumbing. The InteractiveMode class itself
// (6852 lines) lands incrementally over the interactive rounds.

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// FormatCrashExtensionHint renders the crash-report extension hint (nil
// when no matches).
func FormatCrashExtensionHint(extensionMatches []string) string {
	var matches []string
	for _, match := range extensionMatches {
		if strings.TrimSpace(match) != "" {
			matches = append(matches, match)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	quoted := make([]string, len(matches))
	for i, match := range matches {
		quoted[i] = "`" + match + "`"
	}
	var labels string
	switch len(quoted) {
	case 1:
		labels = quoted[0]
	case 2:
		labels = strings.Join(quoted, " and ")
	default:
		labels = strings.Join(quoted[:len(quoted)-1], ", ") + ", and " + quoted[len(quoted)-1]
	}
	noun := "extensions"
	pronoun := "them"
	if len(matches) == 1 {
		noun = "extension"
		pronoun = "it"
	}
	return fmt.Sprintf("A stack frame came from loaded %s %s, which may be involved. Try disabling %s with `%s config`, or run `%s -ne` to confirm.", noun, labels, pronoun, AppName(), AppName())
}

// IsAnthropicSubscriptionAuthKey detects OAuth subscription keys.
func IsAnthropicSubscriptionAuthKey(apiKey string) bool {
	return strings.HasPrefix(apiKey, "sk-ant-oat")
}

// AnthropicSubscriptionAuthWarning is the billing warning text.
const AnthropicSubscriptionAuthWarning = "Anthropic subscription auth is active. Third-party harness usage draws from extra usage and is billed per token, not your Claude plan limits. Manage extra usage at https://claude.ai/settings/usage. Disable this warning in /settings."

// IsUnknownModel reports the unknown/unknown/unknown placeholder.
func IsUnknownModel(provider, modelID, api string) bool {
	return provider == "unknown" && modelID == "unknown" && api == "unknown"
}

var shellSafePattern = regexp.MustCompile(`^[a-zA-Z0-9_\-./~:@]+$`)

// QuoteIfNeeded shell-quotes values with special characters.
func QuoteIfNeeded(value string) string {
	if value != "" && shellSafePattern.MatchString(value) {
		return value
	}
	escaped := strings.ReplaceAll(value, "'", `'\''`)
	return "'" + escaped + "'"
}

// FormatResumeCommand renders the resume hint ("" when not applicable:
// non-TTY, unpersisted, or missing file).
func FormatResumeCommand(sessionManager *SessionManager, isTTY bool) string {
	if !isTTY {
		return ""
	}
	if !sessionManager.IsPersisted() {
		return ""
	}
	sessionFile := sessionManager.GetSessionFile()
	if sessionFile == "" {
		return ""
	}
	if _, err := os.Stat(sessionFile); err != nil {
		return ""
	}

	args := []string{AppName()}
	if !sessionManager.UsesDefaultSessionDir() {
		args = append(args, "--session-dir", QuoteIfNeeded(sessionManager.GetSessionDir()))
	}
	args = append(args, "--session", sessionManager.GetSessionID())
	return strings.Join(args, " ")
}

// HasDefaultModelProvider reports a provider with a known default model.
func HasDefaultModelProvider(providerID string) bool {
	_, ok := DefaultModelPerProvider[providerID]
	return ok
}

// LlamaCppPostLoginGuidance renders post-login model guidance.
func LlamaCppPostLoginGuidance(actionLabel string, loadedModelCount int) string {
	if loadedModelCount == 0 {
		return actionLabel + ". No llama.cpp models are loaded. Use /llama to load a model, then /model to select it."
	}
	return actionLabel + ". Use /model to select a loaded llama.cpp model, or /llama to manage models."
}

// LoginProviderCompletionOption aggregates one provider's auth types.
type LoginProviderCompletionOption struct {
	ID        string
	Name      string
	AuthTypes []string
}

var authTypeOrder = map[string]int{"oauth": 0, "api_key": 1}

// GetLoginProviderCompletionOptions deduplicates providers by id with
// sorted auth types, ordered by name.
func GetLoginProviderCompletionOptions(providerOptions []struct{ ID, Name, AuthType string }) []LoginProviderCompletionOption {
	byID := map[string]*LoginProviderCompletionOption{}
	var order []string
	for _, provider := range providerOptions {
		existing, ok := byID[provider.ID]
		if !ok {
			byID[provider.ID] = &LoginProviderCompletionOption{
				ID:        provider.ID,
				Name:      provider.Name,
				AuthTypes: []string{provider.AuthType},
			}
			order = append(order, provider.ID)
			continue
		}
		found := false
		for _, authType := range existing.AuthTypes {
			if authType == provider.AuthType {
				found = true
				break
			}
		}
		if !found {
			existing.AuthTypes = append(existing.AuthTypes, provider.AuthType)
			sort.SliceStable(existing.AuthTypes, func(a, b int) bool {
				return authTypeOrder[existing.AuthTypes[a]] < authTypeOrder[existing.AuthTypes[b]]
			})
		}
	}
	out := make([]LoginProviderCompletionOption, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// FormatAuthSelectorProviderType renders the auth-type label.
func FormatAuthSelectorProviderType(authType string) string {
	switch authType {
	case "oauth":
		return "OAuth"
	case "api_key":
		return "API key"
	}
	return authType
}

// GetLoginProviderSearchText builds the fuzzy search corpus.
func GetLoginProviderSearchText(provider LoginProviderCompletionOption) string {
	var parts []string
	for _, authType := range provider.AuthTypes {
		parts = append(parts, authType+" "+FormatAuthSelectorProviderType(authType))
	}
	return provider.ID + " " + provider.Name + " " + strings.Join(parts, " ")
}

// FormatLoginProviderCompletionDescription renders the item description.
func FormatLoginProviderCompletionDescription(provider LoginProviderCompletionOption) string {
	var labels []string
	for _, authType := range provider.AuthTypes {
		labels = append(labels, FormatAuthSelectorProviderType(authType))
	}
	authTypes := strings.Join(labels, "/")
	if provider.Name == provider.ID {
		return authTypes
	}
	return provider.Name + " · " + authTypes
}
