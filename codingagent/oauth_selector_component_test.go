package codingagent

// Tests for the OAuth provider selector (adapted from
// oauth-selector.test.ts).

import (
	"strings"
	"testing"
)

func TestFormatAuthSelectorStatusType(t *testing.T) {
	if got := FormatAuthSelectorStatusType("oauth"); got != "subscription" {
		t.Fatalf("oauth = %q", got)
	}
	if got := FormatAuthSelectorStatusType("api_key"); got != "API key" {
		t.Fatalf("api_key = %q", got)
	}
}

func selectorProvider(id, name, authType string, status *AuthCheck) AuthSelectorProvider {
	return AuthSelectorProvider{ID: id, Name: name, AuthType: authType, Status: status}
}

func TestOAuthSelectorLoginTitle(t *testing.T) {
	component := NewOAuthSelectorComponent("login", nil, nil, nil, "")
	lines := component.Render(80)
	if !strings.Contains(strings.Join(lines, "\n"), "Select provider to configure:") {
		t.Fatalf("title = %v", lines)
	}

	logout := NewOAuthSelectorComponent("logout", nil, nil, nil, "")
	lines = logout.Render(80)
	if !strings.Contains(strings.Join(lines, "\n"), "Select provider to logout:") {
		t.Fatalf("logout title = %v", lines)
	}
}

func TestOAuthSelectorAuthTypeLabels(t *testing.T) {
	// Mixed auth types: labels show.
	mixed := NewOAuthSelectorComponent("login", []AuthSelectorProvider{
		selectorProvider("anthropic", "Anthropic", "oauth", nil),
		selectorProvider("zai", "ZAI", "api_key", nil),
	}, nil, nil, "")
	lines := mixed.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[subscription]") {
		t.Fatalf("oauth label = %q", joined)
	}
	if !strings.Contains(joined, "[API key]") {
		t.Fatalf("api key label = %q", joined)
	}

	// Single auth type: no labels.
	single := NewOAuthSelectorComponent("login", []AuthSelectorProvider{
		selectorProvider("a", "A", "api_key", nil),
		selectorProvider("b", "B", "api_key", nil),
	}, nil, nil, "")
	lines = single.Render(80)
	if strings.Contains(strings.Join(lines, "\n"), "[API key]") {
		t.Fatalf("labels leaked = %v", lines)
	}
}

func TestOAuthSelectorStatusIndicators(t *testing.T) {
	component := NewOAuthSelectorComponent("login", []AuthSelectorProvider{
		selectorProvider("unconfigured", "Unconfigured", "api_key", nil),
		selectorProvider("configured", "Configured", "api_key", &AuthCheck{Type: "api_key"}),
		selectorProvider("stored", "Stored", "oauth", &AuthCheck{Type: "oauth", Source: "stored credential"}),
		selectorProvider("env", "Env", "api_key", &AuthCheck{Type: "api_key", Source: "ANTHROPIC_API_KEY"}),
		selectorProvider("mismatch", "Mismatch", "api_key", &AuthCheck{Type: "oauth"}),
	}, nil, nil, "")

	lines := component.Render(100)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "• unconfigured") {
		t.Fatalf("unconfigured = %q", joined)
	}
	if !strings.Contains(joined, "✓ configured") {
		t.Fatalf("configured = %q", joined)
	}
	if !strings.Contains(joined, "✓ env: ANTHROPIC_API_KEY") {
		t.Fatalf("env = %q", joined)
	}
	if !strings.Contains(joined, "subscription configured") {
		t.Fatalf("mismatch = %q", joined)
	}
}

func TestOAuthSelectorSearch(t *testing.T) {
	component := NewOAuthSelectorComponent("login", []AuthSelectorProvider{
		selectorProvider("anthropic", "Anthropic", "oauth", nil),
		selectorProvider("zai", "ZAI", "api_key", nil),
	}, nil, nil, "")

	component.HandleInput("z")
	component.HandleInput("a")
	if component.SearchValue() != "za" {
		t.Fatalf("query = %q", component.SearchValue())
	}
	if component.FilteredCount() != 1 {
		t.Fatalf("filtered = %d", component.FilteredCount())
	}
	joined := strings.Join(component.Render(80), "\n")
	if !strings.Contains(joined, "ZAI") || strings.Contains(joined, "Anthropic") {
		t.Fatalf("render = %q", joined)
	}

	component.HandleInput("backspace")
	component.HandleInput("backspace")
	if component.FilteredCount() != 2 {
		t.Fatalf("restored = %d", component.FilteredCount())
	}
}

func TestOAuthSelectorInitialSearch(t *testing.T) {
	component := NewOAuthSelectorComponent("login", []AuthSelectorProvider{
		selectorProvider("anthropic", "Anthropic", "oauth", nil),
		selectorProvider("zai", "ZAI", "api_key", nil),
	}, nil, nil, "zai")
	if component.SearchValue() != "zai" {
		t.Fatalf("initial = %q", component.SearchValue())
	}
	if component.FilteredCount() != 1 {
		t.Fatalf("filtered = %d", component.FilteredCount())
	}
}

func TestOAuthSelectorNavigationClamps(t *testing.T) {
	component := NewOAuthSelectorComponent("login", []AuthSelectorProvider{
		selectorProvider("a", "A", "api_key", nil),
		selectorProvider("b", "B", "api_key", nil),
	}, nil, nil, "")

	// Up at the top clamps (no wrap, unlike the model selector).
	component.HandleInput("up")
	if component.SelectedIndex() != 0 {
		t.Fatalf("up = %d", component.SelectedIndex())
	}
	component.HandleInput("down")
	if component.SelectedIndex() != 1 {
		t.Fatalf("down = %d", component.SelectedIndex())
	}
	component.HandleInput("down")
	if component.SelectedIndex() != 1 {
		t.Fatalf("clamp = %d", component.SelectedIndex())
	}
}

func TestOAuthSelectorSelects(t *testing.T) {
	var gotID, gotType string
	component := NewOAuthSelectorComponent("login", []AuthSelectorProvider{
		selectorProvider("anthropic", "Anthropic", "oauth", nil),
	}, func(providerID, authType string) {
		gotID = providerID
		gotType = authType
	}, nil, "")

	component.HandleInput("enter")
	if gotID != "anthropic" || gotType != "oauth" {
		t.Fatalf("selected = %q %q", gotID, gotType)
	}
}

func TestOAuthSelectorCancel(t *testing.T) {
	cancelled := false
	component := NewOAuthSelectorComponent("login", nil, nil, func() { cancelled = true }, "")
	component.HandleInput("escape")
	if !cancelled {
		t.Fatal("escape did not cancel")
	}
}

func TestOAuthSelectorEmptyStates(t *testing.T) {
	// No providers at all (login).
	login := NewOAuthSelectorComponent("login", nil, nil, nil, "")
	joined := strings.Join(login.Render(80), "\n")
	if !strings.Contains(joined, "No providers available") {
		t.Fatalf("login empty = %q", joined)
	}

	// No providers at all (logout).
	logout := NewOAuthSelectorComponent("logout", nil, nil, nil, "")
	joined = strings.Join(logout.Render(80), "\n")
	if !strings.Contains(joined, "No providers logged in. Use /login first.") {
		t.Fatalf("logout empty = %q", joined)
	}

	// Providers exist but none match.
	mismatch := NewOAuthSelectorComponent("login", []AuthSelectorProvider{
		selectorProvider("a", "Alpha", "api_key", nil),
	}, nil, nil, "zzz")
	joined = strings.Join(mismatch.Render(80), "\n")
	if !strings.Contains(joined, "No matching providers") {
		t.Fatalf("no match = %q", joined)
	}
}
