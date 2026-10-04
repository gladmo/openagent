package codingagent

// Port of modes/interactive/components/oauth-selector.ts: the auth
// provider selector for login/logout with status indicators, auth-type
// labels (only when providers mix types), and a fuzzy search field.

import (
	"fmt"
	"regexp"

	"github.com/gladmo/openagent/tui"
)

// AuthSelectorProvider is one selectable auth provider.
type AuthSelectorProvider struct {
	ID       string
	Name     string
	AuthType string // "oauth" | "api_key"
	Method   string
	Status   *AuthCheck
}

// AuthCheck reports a provider's configured credential.
type AuthCheck struct {
	Type   string // "oauth" | "api_key"
	Source string
}

// authEnvNamePattern matches env-style uppercase names (possibly a
// comma-separated list).
var authEnvNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(?:, [A-Z][A-Z0-9_]*)*$`)

// FormatAuthSelectorStatusType renders the selector's auth-type label
// (the completion-list variant lives in interactive_helpers.go).
func FormatAuthSelectorStatusType(authType string) string {
	if authType == "oauth" {
		return "subscription"
	}
	return "API key"
}

// oauthSelectorVisibleRows bounds the list window.
const oauthSelectorVisibleRows = 8

// OAuthSelectorComponent renders the provider selector.
type OAuthSelectorComponent struct {
	*tui.VStack

	mode               string // "login" | "logout"
	allProviders       []AuthSelectorProvider
	filteredProviders  []AuthSelectorProvider
	selectedIndex      int
	showAuthTypeLabels bool
	searchValue        string
	onSelect           func(providerID, authType string)
	onCancel           func()
}

// NewOAuthSelectorComponent builds the selector.
func NewOAuthSelectorComponent(mode string, providers []AuthSelectorProvider, onSelect func(providerID, authType string), onCancel func(), initialSearch string) *OAuthSelectorComponent {
	authTypes := map[string]bool{}
	for _, provider := range providers {
		authTypes[provider.AuthType] = true
	}
	component := &OAuthSelectorComponent{
		VStack:             tui.NewVStack(0),
		mode:               mode,
		allProviders:       providers,
		filteredProviders:  providers,
		showAuthTypeLabels: len(authTypes) > 1,
		onSelect:           onSelect,
		onCancel:           onCancel,
	}
	component.filterProviders(initialSearch)
	return component
}

// SearchValue reports the query (tests).
func (c *OAuthSelectorComponent) SearchValue() string { return c.searchValue }

// FilteredCount reports the filtered list length (tests).
func (c *OAuthSelectorComponent) FilteredCount() int { return len(c.filteredProviders) }

// SelectedIndex reports the cursor (tests).
func (c *OAuthSelectorComponent) SelectedIndex() int { return c.selectedIndex }

// filterProviders applies the fuzzy query and clamps the cursor.
func (c *OAuthSelectorComponent) filterProviders(query string) {
	c.searchValue = query
	if query != "" {
		c.filteredProviders = tui.FuzzyFilter(c.allProviders, query, func(provider AuthSelectorProvider) string {
			return provider.Name + " " + provider.ID + " " + provider.AuthType + " " + provider.Method
		})
	} else {
		c.filteredProviders = c.allProviders
	}
	max := len(c.filteredProviders) - 1
	if max < 0 {
		max = 0
	}
	if c.selectedIndex > max {
		c.selectedIndex = max
	}
	c.updateList()
}

// formatStatusIndicator renders the configured/unconfigured marker.
func (c *OAuthSelectorComponent) formatStatusIndicator(provider AuthSelectorProvider) string {
	if provider.Status == nil {
		return " • unconfigured"
	}
	if provider.Status.Type != provider.AuthType {
		label := "API key configured"
		if provider.Status.Type == "oauth" {
			label = "subscription configured"
		}
		return " • " + label
	}
	if provider.Status.Source == "" || provider.Status.Source == "OAuth" || provider.Status.Source == "stored credential" {
		return " ✓ configured"
	}
	source := provider.Status.Source
	if authEnvNamePattern.MatchString(source) {
		source = "env: " + source
	}
	return " ✓ " + source
}

// updateList renders the visible window, scroll counter, and empty text.
func (c *OAuthSelectorComponent) updateList() {
	title := "Select provider to configure:"
	if c.mode != "login" {
		title = "Select provider to logout:"
	}

	c.VStack = tui.NewVStack(0)
	c.VStack.AddChild(tui.NewText(title, 1, 0, nil), nil)
	c.VStack.AddChild(tui.NewSpacer(1), nil)
	if c.searchValue != "" {
		c.VStack.AddChild(tui.NewText("Search: "+c.searchValue, 1, 0, nil), nil)
	}

	startIndex := c.selectedIndex - oauthSelectorVisibleRows/2
	if startIndex > len(c.filteredProviders)-oauthSelectorVisibleRows {
		startIndex = len(c.filteredProviders) - oauthSelectorVisibleRows
	}
	if startIndex < 0 {
		startIndex = 0
	}
	endIndex := startIndex + oauthSelectorVisibleRows
	if endIndex > len(c.filteredProviders) {
		endIndex = len(c.filteredProviders)
	}

	for i := startIndex; i < endIndex; i++ {
		provider := c.filteredProviders[i]
		authTypeLabel := ""
		if c.showAuthTypeLabels {
			authTypeLabel = " [" + FormatAuthSelectorStatusType(provider.AuthType) + "]"
		}
		prefix := "  "
		if i == c.selectedIndex {
			prefix = "→ "
		}
		line := fmt.Sprintf("%s%s%s%s", prefix, provider.Name, authTypeLabel, c.formatStatusIndicator(provider))
		c.VStack.AddChild(tui.NewText(line, 1, 0, nil), nil)
	}

	if startIndex > 0 || endIndex < len(c.filteredProviders) {
		c.VStack.AddChild(tui.NewText(fmt.Sprintf("  (%d/%d)", c.selectedIndex+1, len(c.filteredProviders)), 1, 0, nil), nil)
	}

	if len(c.filteredProviders) == 0 {
		message := "No matching providers"
		if len(c.allProviders) == 0 {
			if c.mode == "login" {
				message = "No providers available"
			} else {
				message = "No providers logged in. Use /login first."
			}
		}
		c.VStack.AddChild(tui.NewText("  "+message, 1, 0, nil), nil)
	}
}

// HandleInput drives navigation, selection, and search.
func (c *OAuthSelectorComponent) HandleInput(key string) {
	switch key {
	case "up":
		if len(c.filteredProviders) == 0 {
			return
		}
		if c.selectedIndex > 0 {
			c.selectedIndex--
		}
		c.updateList()
	case "down":
		if len(c.filteredProviders) == 0 {
			return
		}
		if c.selectedIndex < len(c.filteredProviders)-1 {
			c.selectedIndex++
		}
		c.updateList()
	case "enter":
		if c.selectedIndex < len(c.filteredProviders) && c.onSelect != nil {
			provider := c.filteredProviders[c.selectedIndex]
			c.onSelect(provider.ID, provider.AuthType)
		}
	case "escape", "ctrl+c":
		if c.onCancel != nil {
			c.onCancel()
		}
	default:
		if key == "backspace" {
			if len(c.searchValue) > 0 {
				c.filterProviders(c.searchValue[:len(c.searchValue)-1])
			}
			return
		}
		if len(key) == 1 {
			c.filterProviders(c.searchValue + key)
		}
	}
}
